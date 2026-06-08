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
)

func TestDryRunTTSGeneratesAudioArtifact(t *testing.T) {
	server := newTestServer(t, Config{Endpoint: "http://provider.local", DryRun: true, VoiceCatalogPath: writeCatalog(t)})

	data := invoke(t, server, DefaultTTSCapabilityID, map[string]any{
		"text":   "hello from PACP",
		"voice":  "narrator",
		"format": "wav",
	}, http.StatusOK)

	output := data["output"].(map[string]any)
	if output["voice"] != "narrator" || output["format"] != "wav" || output["media_type"] != "audio/wav" || output["dry_run"] != true {
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

func TestRejectsInvalidVoice(t *testing.T) {
	server := newTestServer(t, Config{Endpoint: "http://provider.local", DryRun: true, VoiceCatalogPath: writeCatalog(t)})
	envelope := invokeEnvelope(t, server, DefaultTTSCapabilityID, map[string]any{
		"text":  "hello",
		"voice": "missing",
	}, http.StatusBadRequest)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "validation_failed" || !strings.Contains(errObj["message"].(string), "missing") {
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
	if errObj["code"] != "validation_failed" || !strings.Contains(errObj["message"].(string), "mp3") {
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

func newTestServer(t *testing.T, cfg Config) http.Handler {
	t.Helper()
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("new speech provider: %v", err)
	}
	return server
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
