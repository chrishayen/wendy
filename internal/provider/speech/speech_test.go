package speech

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wendy/internal/contracts"
)

func TestDryRunTTSGeneratesAudioArtifact(t *testing.T) {
	server := newTestServer(t, Config{Endpoint: "http://provider.local", DryRun: true, VoiceCatalogPath: writeCatalog(t)})

	data := invoke(t, server, DefaultTTSCapabilityID, map[string]any{
		"text":   "hello from Wendy",
		"voice":  "narrator",
		"format": "wav",
		"speed":  1.25,
		"pitch":  -2,
	}, http.StatusOK)

	output := data["output"].(map[string]any)
	if output["voice"] != "narrator" || output["format"] != "wav" || output["media_type"] != "audio/wav" || output["dry_run"] != true {
		t.Fatalf("output = %#v", output)
	}
	if output["speed"].(float64) != 1.25 || output["pitch"].(float64) != -2 {
		t.Fatalf("output = %#v", output)
	}
	artifacts := data["artifacts"].([]any)
	if len(artifacts) != 1 {
		t.Fatalf("artifacts = %#v", artifacts)
	}
	artifact := artifacts[0].(map[string]any)
	if artifact["media_type"] != "audio/wav" || artifact["checksum"] == "" || artifact["content_base64"] == "" {
		t.Fatalf("artifact = %#v", artifact)
	}
}

func TestDryRunTTSDefaultsToCatalogVoice(t *testing.T) {
	server := newTestServer(t, Config{Endpoint: "http://provider.local", DryRun: true, VoiceCatalogPath: writeCatalog(t)})

	data := invoke(t, server, DefaultTTSCapabilityID, map[string]any{"text": "hello from Wendy"}, http.StatusOK)

	output := data["output"].(map[string]any)
	if output["voice"] != "narrator" || output["format"] != "wav" || output["speed"].(float64) != defaultTTSSpeed || output["pitch"].(float64) != defaultTTSPitch {
		t.Fatalf("output = %#v", output)
	}
}

func TestManifestPublishesSpeechCatalogSchemas(t *testing.T) {
	server := newTestServer(t, Config{Endpoint: "http://provider.local", DryRun: true, VoiceCatalogPath: writeCatalog(t)})

	manifest := providerManifest(t, server)
	tts := capabilityByID(t, manifest, DefaultTTSCapabilityID)
	stt := capabilityByID(t, manifest, DefaultSTTCapabilityID)

	textProperty := schemaProperty(t, tts.InputSchema, "text")
	if textProperty["maxLength"] != float64(maxTextLength) {
		t.Fatalf("text property = %#v", textProperty)
	}
	voiceProperty := schemaProperty(t, tts.InputSchema, "voice")
	if !enumEquals(voiceProperty["enum"], []string{"narrator"}) {
		t.Fatalf("voice property = %#v", voiceProperty)
	}
	formatProperty := schemaProperty(t, tts.InputSchema, "format")
	if !enumEquals(formatProperty["enum"], []string{"wav"}) {
		t.Fatalf("format property = %#v", formatProperty)
	}
	if tts.Examples[0]["voice"] != "narrator" || tts.Examples[0]["format"] != "wav" {
		t.Fatalf("tts examples = %#v", tts.Examples)
	}
	speedProperty := schemaProperty(t, tts.InputSchema, "speed")
	if speedProperty["minimum"] != 0.5 || speedProperty["maximum"] != 2.0 {
		t.Fatalf("speed property = %#v", speedProperty)
	}
	pitchProperty := schemaProperty(t, tts.InputSchema, "pitch")
	if pitchProperty["minimum"] != float64(-12) || pitchProperty["maximum"] != float64(12) {
		t.Fatalf("pitch property = %#v", pitchProperty)
	}
	if tts.Examples[0]["speed"] != defaultTTSSpeed || tts.Examples[0]["pitch"] != defaultTTSPitch {
		t.Fatalf("tts examples = %#v", tts.Examples)
	}

	audioProperty := schemaProperty(t, stt.InputSchema, "audio_base64")
	if audioProperty["contentEncoding"] != "base64" || audioProperty["maxLength"] != float64(base64.StdEncoding.EncodedLen(maxAudioBytes)) {
		t.Fatalf("audio property = %#v", audioProperty)
	}
	mediaTypeProperty := schemaProperty(t, stt.InputSchema, "media_type")
	if !enumEquals(mediaTypeProperty["enum"], []string{"audio/wav"}) {
		t.Fatalf("media_type property = %#v", mediaTypeProperty)
	}
}

func TestRejectsInvalidVoice(t *testing.T) {
	server := newTestServer(t, Config{Endpoint: "http://provider.local", DryRun: true, VoiceCatalogPath: writeCatalog(t)})
	envelope := invokeEnvelope(t, server, DefaultTTSCapabilityID, map[string]any{
		"text":  "hello",
		"voice": "missing",
	}, http.StatusBadRequest)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "validation_failed" || !strings.Contains(errObj["message"].(string), "voice") {
		t.Fatalf("error = %#v", errObj)
	}
}

func TestRejectsInvalidTTSGenerationOption(t *testing.T) {
	server := newTestServer(t, Config{Endpoint: "http://provider.local", DryRun: true, VoiceCatalogPath: writeCatalog(t)})
	envelope := invokeEnvelope(t, server, DefaultTTSCapabilityID, map[string]any{
		"text":  "hello",
		"speed": 3.0,
	}, http.StatusBadRequest)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "validation_failed" || !strings.Contains(errObj["message"].(string), "speed") {
		t.Fatalf("error = %#v", errObj)
	}
}

func TestDryRunSTTReturnsTranscript(t *testing.T) {
	server := newTestServer(t, Config{Endpoint: "http://provider.local", DryRun: true, VoiceCatalogPath: writeCatalog(t)})
	audio := base64.StdEncoding.EncodeToString(wavSilence(defaultSampleRate, 120000000))

	data := invoke(t, server, DefaultSTTCapabilityID, map[string]any{
		"audio_base64": audio,
		"media_type":   "audio/wav",
		"format":       "wav",
		"language":     "en",
	}, http.StatusOK)

	output := data["output"].(map[string]any)
	if output["transcript"] != "dry-run transcript" || output["language"] != "en" || output["dry_run"] != true {
		t.Fatalf("output = %#v", output)
	}
}

func TestRejectsInvalidSTTFormat(t *testing.T) {
	server := newTestServer(t, Config{Endpoint: "http://provider.local", DryRun: true, VoiceCatalogPath: writeCatalog(t)})
	audio := base64.StdEncoding.EncodeToString(wavSilence(defaultSampleRate, 120000000))
	envelope := invokeEnvelope(t, server, DefaultSTTCapabilityID, map[string]any{
		"audio_base64": audio,
		"format":       "mp3",
	}, http.StatusBadRequest)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "validation_failed" || !strings.Contains(errObj["message"].(string), "format") {
		t.Fatalf("error = %#v", errObj)
	}
}

func TestRejectsInvalidSTTAudioBase64(t *testing.T) {
	server := newTestServer(t, Config{Endpoint: "http://provider.local", DryRun: true, VoiceCatalogPath: writeCatalog(t)})
	envelope := invokeEnvelope(t, server, DefaultSTTCapabilityID, map[string]any{
		"audio_base64": "not base64",
		"format":       "wav",
	}, http.StatusBadRequest)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "validation_failed" || !strings.Contains(errObj["message"].(string), "audio_base64") {
		t.Fatalf("error = %#v", errObj)
	}
}

func TestRejectsMismatchedSTTMediaType(t *testing.T) {
	server := newTestServer(t, Config{Endpoint: "http://provider.local", DryRun: true, VoiceCatalogPath: writeCatalog(t)})
	audio := base64.StdEncoding.EncodeToString(wavSilence(defaultSampleRate, 120000000))
	envelope := invokeEnvelope(t, server, DefaultSTTCapabilityID, map[string]any{
		"audio_base64": audio,
		"media_type":   "audio/mpeg",
		"format":       "wav",
	}, http.StatusBadRequest)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "validation_failed" || !strings.Contains(errObj["message"].(string), "media_type") {
		t.Fatalf("error = %#v", errObj)
	}
}

func TestMapsTTSEngineFailureToProviderUnavailable(t *testing.T) {
	server := newTestServer(t, Config{
		Endpoint:   "http://provider.local",
		TTSCommand: []string{"/bin/false"},
		STTCommand: []string{"/bin/false"},
	})
	envelope := invokeEnvelope(t, server, DefaultTTSCapabilityID, map[string]any{"text": "hello"}, http.StatusInternalServerError)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "provider_unavailable" {
		t.Fatalf("error = %#v", errObj)
	}
}

func TestMapsSTTEngineFailureToProviderUnavailable(t *testing.T) {
	server := newTestServer(t, Config{
		Endpoint:   "http://provider.local",
		TTSCommand: []string{"/bin/false"},
		STTCommand: []string{"/bin/false"},
	})
	audio := base64.StdEncoding.EncodeToString(wavSilence(defaultSampleRate, 120000000))
	envelope := invokeEnvelope(t, server, DefaultSTTCapabilityID, map[string]any{
		"audio_base64": audio,
		"format":       "wav",
	}, http.StatusInternalServerError)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "provider_unavailable" {
		t.Fatalf("error = %#v", errObj)
	}
}

func newTestServer(t *testing.T, cfg Config) http.Handler {
	t.Helper()
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("new speech provider: %v", err)
	}
	return server
}

func providerManifest(t *testing.T, handler http.Handler) contracts.ProviderManifest {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/provider/manifest", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		OK   bool                       `json:"ok"`
		Data contracts.ProviderManifest `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode manifest envelope: %v", err)
	}
	if !envelope.OK {
		t.Fatalf("manifest envelope = %#v", envelope)
	}
	return envelope.Data
}

func capabilityByID(t *testing.T, manifest contracts.ProviderManifest, id string) contracts.Capability {
	t.Helper()
	for _, capability := range manifest.Capabilities {
		if capability.ID == id {
			return capability
		}
	}
	t.Fatalf("capability %s not found in %#v", id, manifest.Capabilities)
	return contracts.Capability{}
}

func schemaProperty(t *testing.T, schema map[string]any, key string) map[string]any {
	t.Helper()
	properties, _ := schema["properties"].(map[string]any)
	property, _ := properties[key].(map[string]any)
	if property == nil {
		t.Fatalf("property %s not found in %#v", key, schema)
	}
	return property
}

func enumEquals(value any, want []string) bool {
	values, ok := value.([]any)
	if !ok || len(values) != len(want) {
		return false
	}
	for i, item := range values {
		if item != want[i] {
			return false
		}
	}
	return true
}

func invoke(t *testing.T, handler http.Handler, capabilityID string, input map[string]any, wantStatus int) map[string]any {
	t.Helper()
	envelope := invokeEnvelope(t, handler, capabilityID, input, wantStatus)
	if !envelope["ok"].(bool) {
		t.Fatalf("error envelope = %#v", envelope)
	}
	return envelope["data"].(map[string]any)
}

func invokeEnvelope(t *testing.T, handler http.Handler, capabilityID string, input map[string]any, wantStatus int) map[string]any {
	t.Helper()
	var raw bytes.Buffer
	if err := json.NewEncoder(&raw).Encode(map[string]any{"input": input}); err != nil {
		t.Fatalf("encode invoke: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/provider/capabilities/"+capabilityID+"/invoke", &raw)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, wantStatus, rec.Body.String())
	}
	var envelope map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return envelope
}

func writeCatalog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "speech-catalog.json")
	body := []byte(`{
  "voices": [{"id": "narrator", "name": "Narrator", "language": "en"}],
  "formats": [{"id": "wav", "media_type": "audio/wav", "sample_rate": 16000}],
  "models": [{"id": "default", "name": "Default", "language": "en"}]
}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	return path
}
