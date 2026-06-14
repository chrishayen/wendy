package speech

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"wendy/internal/contracts"
	"wendy/internal/provider"
)

const (
	DefaultServiceID       = "svc_speech_provider"
	DefaultTTSCapabilityID = "cap_speech_tts"
	DefaultSTTCapabilityID = "cap_speech_stt"

	defaultServiceName = "Speech Provider"
	defaultVersion     = "0.1.0"
	defaultVoiceID     = "default"
	defaultFormatID    = "wav"
	defaultSampleRate  = 16000
	defaultTTSSpeed    = 1.0
	defaultTTSPitch    = 0.0
	maxTextLength      = 5000
	maxAudioBytes      = 25 << 20
)

type Config struct {
	Endpoint         string
	ServiceID        string
	ServiceName      string
	Version          string
	AuthCredential   string
	TTSCapabilityID  string
	STTCapabilityID  string
	VoiceCatalogPath string
	DryRun           bool
	TTSCommand       []string
	STTCommand       []string
	Timeout          time.Duration
}

type Catalog struct {
	Voices  []Voice       `json:"voices"`
	Formats []AudioFormat `json:"formats"`
	Models  []SpeechModel `json:"models,omitempty"`
}

type Voice struct {
	ID       string         `json:"id"`
	Name     string         `json:"name,omitempty"`
	Language string         `json:"language,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type AudioFormat struct {
	ID         string `json:"id"`
	MediaType  string `json:"media_type"`
	SampleRate int    `json:"sample_rate,omitempty"`
}

type SpeechModel struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Language string `json:"language,omitempty"`
}

type ttsRequest struct {
	Text       string  `json:"text"`
	Voice      string  `json:"voice"`
	Format     string  `json:"format"`
	SampleRate int     `json:"sample_rate"`
	Speed      float64 `json:"speed"`
	Pitch      float64 `json:"pitch"`
}

type sttRequest struct {
	AudioBase64 string `json:"audio_base64"`
	MediaType   string `json:"media_type"`
	Format      string `json:"format"`
	Language    string `json:"language,omitempty"`
}

type ttsEngineResponse struct {
	ContentBase64   string  `json:"content_base64"`
	MediaType       string  `json:"media_type"`
	DurationSeconds float64 `json:"duration_seconds"`
}

type sttEngineResponse struct {
	Transcript      string  `json:"transcript"`
	Language        string  `json:"language,omitempty"`
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
}

type speechProvider struct {
	cfg     Config
	voices  map[string]Voice
	formats map[string]AudioFormat
}

func NewServer(cfg Config) (*provider.Server, error) {
	normalized, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	catalog, err := loadCatalog(normalized.VoiceCatalogPath)
	if err != nil {
		return nil, err
	}
	p := &speechProvider{cfg: normalized, voices: mapVoices(catalog.Voices), formats: mapFormats(catalog.Formats)}
	return provider.NewServerWithOptions(manifest(normalized, p.voices, p.formats), map[string]provider.CapabilityHandler{
		normalized.TTSCapabilityID: p.tts,
		normalized.STTCapabilityID: p.stt,
	}, provider.WithAuthCredential(normalized.AuthCredential))
}

func normalizeConfig(cfg Config) (Config, error) {
	if cfg.ServiceID == "" {
		cfg.ServiceID = DefaultServiceID
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = defaultServiceName
	}
	if cfg.Version == "" {
		cfg.Version = defaultVersion
	}
	if cfg.TTSCapabilityID == "" {
		cfg.TTSCapabilityID = DefaultTTSCapabilityID
	}
	if cfg.STTCapabilityID == "" {
		cfg.STTCapabilityID = DefaultSTTCapabilityID
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = time.Minute
	}
	if !cfg.DryRun {
		if len(cfg.TTSCommand) == 0 {
			return Config{}, fmt.Errorf("%w: tts_command is required unless dry_run is enabled", provider.ErrValidation)
		}
		if len(cfg.STTCommand) == 0 {
			return Config{}, fmt.Errorf("%w: stt_command is required unless dry_run is enabled", provider.ErrValidation)
		}
	}
	return cfg, nil
}

func loadCatalog(path string) (Catalog, error) {
	if strings.TrimSpace(path) == "" {
		return defaultCatalog(), nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return Catalog{}, err
	}
	var catalog Catalog
	if err := json.Unmarshal(body, &catalog); err != nil {
		return Catalog{}, fmt.Errorf("decode speech catalog %s: %w", path, err)
	}
	if len(catalog.Voices) == 0 {
		catalog.Voices = defaultCatalog().Voices
	}
	if len(catalog.Formats) == 0 {
		catalog.Formats = defaultCatalog().Formats
	}
	return catalog, nil
}

func defaultCatalog() Catalog {
	return Catalog{
		Voices:  []Voice{{ID: defaultVoiceID, Name: "Default", Language: "en"}},
		Formats: []AudioFormat{{ID: defaultFormatID, MediaType: "audio/wav", SampleRate: defaultSampleRate}},
		Models:  []SpeechModel{{ID: "default", Name: "Default", Language: "en"}},
	}
}

func mapVoices(values []Voice) map[string]Voice {
	out := map[string]Voice{}
	for _, value := range values {
		if value.ID != "" {
			out[value.ID] = value
		}
	}
	return out
}

func mapFormats(values []AudioFormat) map[string]AudioFormat {
	out := map[string]AudioFormat{}
	for _, value := range values {
		if value.ID != "" {
			out[value.ID] = value
		}
	}
	return out
}

func (p *speechProvider) tts(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
	parsed, format, err := p.parseTTS(req.Input)
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	var engine ttsEngineResponse
	if p.cfg.DryRun {
		audio := wavSilence(parsed.SampleRate, 120*time.Millisecond)
		engine = ttsEngineResponse{
			ContentBase64:   base64.StdEncoding.EncodeToString(audio),
			MediaType:       format.MediaType,
			DurationSeconds: 0.12,
		}
	} else if err := runEngine(ctx, p.cfg.Timeout, p.cfg.TTSCommand, parsed, &engine); err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	body, err := base64.StdEncoding.DecodeString(engine.ContentBase64)
	if err != nil || len(body) == 0 {
		return contracts.ProviderInvokeResponse{}, fmt.Errorf("%w: TTS engine returned invalid content_base64", provider.ErrBackend)
	}
	mediaType := engine.MediaType
	if mediaType == "" {
		mediaType = format.MediaType
	}
	return contracts.ProviderInvokeResponse{
		Output: map[string]any{
			"voice":            parsed.Voice,
			"format":           parsed.Format,
			"media_type":       mediaType,
			"sample_rate":      parsed.SampleRate,
			"speed":            parsed.Speed,
			"pitch":            parsed.Pitch,
			"duration_seconds": engine.DurationSeconds,
			"dry_run":          p.cfg.DryRun,
		},
		Artifacts: []contracts.ProviderArtifact{{
			Name:          "speech-tts." + parsed.Format,
			MediaType:     mediaType,
			ContentBase64: base64.StdEncoding.EncodeToString(body),
			Checksum:      checksum(body),
		}},
	}, nil
}

func (p *speechProvider) stt(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
	parsed, err := p.parseSTT(req.Input)
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	if _, err := base64.StdEncoding.DecodeString(parsed.AudioBase64); err != nil {
		return contracts.ProviderInvokeResponse{}, fmt.Errorf("%w: audio_base64 is invalid", provider.ErrValidation)
	}
	var engine sttEngineResponse
	if p.cfg.DryRun {
		engine = sttEngineResponse{Transcript: "dry-run transcript", Language: defaultLanguage(parsed.Language), DurationSeconds: 0.12}
	} else if err := runEngine(ctx, p.cfg.Timeout, p.cfg.STTCommand, parsed, &engine); err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	if strings.TrimSpace(engine.Transcript) == "" {
		return contracts.ProviderInvokeResponse{}, fmt.Errorf("%w: STT engine returned empty transcript", provider.ErrBackend)
	}
	return contracts.ProviderInvokeResponse{Output: map[string]any{
		"transcript":       engine.Transcript,
		"language":         defaultLanguage(engine.Language),
		"duration_seconds": engine.DurationSeconds,
		"dry_run":          p.cfg.DryRun,
	}}, nil
}

func (p *speechProvider) parseTTS(input map[string]any) (ttsRequest, AudioFormat, error) {
	text, err := requiredString(input, "text")
	if err != nil {
		return ttsRequest{}, AudioFormat{}, err
	}
	if len(text) > maxTextLength {
		return ttsRequest{}, AudioFormat{}, fmt.Errorf("%w: text exceeds %d characters", provider.ErrValidation, maxTextLength)
	}
	voice := optionalStringDefault(input, "voice", p.defaultVoiceID())
	if _, ok := p.voices[voice]; !ok {
		return ttsRequest{}, AudioFormat{}, fmt.Errorf("%w: voice %s is not supported", provider.ErrValidation, voice)
	}
	formatID := optionalStringDefault(input, "format", p.defaultFormatID())
	format, ok := p.formats[formatID]
	if !ok {
		return ttsRequest{}, AudioFormat{}, fmt.Errorf("%w: format %s is not supported", provider.ErrValidation, formatID)
	}
	speed, err := numberInput(input, "speed", defaultTTSSpeed)
	if err != nil {
		return ttsRequest{}, AudioFormat{}, err
	}
	if speed < 0.5 || speed > 2.0 {
		return ttsRequest{}, AudioFormat{}, fmt.Errorf("%w: speed must be between 0.5 and 2.0", provider.ErrValidation)
	}
	pitch, err := numberInput(input, "pitch", defaultTTSPitch)
	if err != nil {
		return ttsRequest{}, AudioFormat{}, err
	}
	if pitch < -12 || pitch > 12 {
		return ttsRequest{}, AudioFormat{}, fmt.Errorf("%w: pitch must be between -12 and 12 semitones", provider.ErrValidation)
	}
	sampleRate := format.SampleRate
	if sampleRate <= 0 {
		sampleRate = defaultSampleRate
	}
	return ttsRequest{Text: text, Voice: voice, Format: formatID, SampleRate: sampleRate, Speed: speed, Pitch: pitch}, format, nil
}

func (p *speechProvider) parseSTT(input map[string]any) (sttRequest, error) {
	audio, err := requiredString(input, "audio_base64")
	if err != nil {
		return sttRequest{}, err
	}
	if len(audio) > base64.StdEncoding.EncodedLen(maxAudioBytes) {
		return sttRequest{}, fmt.Errorf("%w: audio_base64 exceeds maximum supported size", provider.ErrValidation)
	}
	formatID := optionalStringDefault(input, "format", p.defaultFormatID())
	format, ok := p.formats[formatID]
	if !ok {
		return sttRequest{}, fmt.Errorf("%w: format %s is not supported", provider.ErrValidation, formatID)
	}
	mediaType := optionalStringDefault(input, "media_type", format.MediaType)
	if format.MediaType != "" && mediaType != format.MediaType {
		return sttRequest{}, fmt.Errorf("%w: media_type %s is not supported for format %s", provider.ErrValidation, mediaType, formatID)
	}
	language := optionalStringDefault(input, "language", "en")
	return sttRequest{AudioBase64: audio, MediaType: mediaType, Format: formatID, Language: language}, nil
}

func (p *speechProvider) defaultVoiceID() string {
	if _, ok := p.voices[defaultVoiceID]; ok {
		return defaultVoiceID
	}
	return firstString(sortedVoiceIDs(p.voices), defaultVoiceID)
}

func (p *speechProvider) defaultFormatID() string {
	if _, ok := p.formats[defaultFormatID]; ok {
		return defaultFormatID
	}
	return firstString(sortedFormatIDs(p.formats), defaultFormatID)
}

func runEngine(ctx context.Context, timeout time.Duration, command []string, request any, out any) error {
	if len(command) == 0 {
		return fmt.Errorf("%w: speech engine command is not configured", provider.ErrBackend)
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Stdin = bytes.NewReader(body)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("%w: speech engine failed: %s", provider.ErrBackend, message)
	}
	if err := json.Unmarshal(stdout, out); err != nil {
		return fmt.Errorf("%w: speech engine returned invalid JSON: %s", provider.ErrBackend, err)
	}
	return nil
}

func wavSilence(sampleRate int, duration time.Duration) []byte {
	if sampleRate <= 0 {
		sampleRate = defaultSampleRate
	}
	samples := int(float64(sampleRate) * duration.Seconds())
	dataSize := samples * 2
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(36+dataSize))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(sampleRate*2))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(2))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(dataSize))
	buf.Write(make([]byte, dataSize))
	return buf.Bytes()
}

func requiredString(input map[string]any, key string) (string, error) {
	value, _ := input[key].(string)
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: %s is required", provider.ErrValidation, key)
	}
	return value, nil
}

func optionalStringDefault(input map[string]any, key, fallback string) string {
	value, _ := input[key].(string)
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func numberInput(input map[string]any, key string, fallback float64) (float64, error) {
	value, ok := input[key]
	if !ok || value == nil {
		return fallback, nil
	}
	switch typed := value.(type) {
	case int:
		return float64(typed), nil
	case int64:
		return float64(typed), nil
	case float64:
		return typed, nil
	default:
		return 0, fmt.Errorf("%w: %s must be a number", provider.ErrValidation, key)
	}
}

func defaultLanguage(value string) string {
	if strings.TrimSpace(value) == "" {
		return "en"
	}
	return value
}

func checksum(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func manifest(cfg Config, voices map[string]Voice, formats map[string]AudioFormat) contracts.ProviderManifest {
	return contracts.ProviderManifest{
		SchemaVersion: "v1",
		Service: contracts.Service{
			ID:           cfg.ServiceID,
			Name:         cfg.ServiceName,
			Description:  "Purpose-specific text-to-speech and speech-to-text provider.",
			Version:      cfg.Version,
			ProviderKind: "speech",
			Tags:         []string{"speech", "tts", "stt"},
		},
		Provider: contracts.Provider{Endpoint: cfg.Endpoint, HealthPath: "/v1/provider/health"},
		Capabilities: []contracts.Capability{
			ttsCapability(cfg.TTSCapabilityID, voices, formats),
			sttCapability(cfg.STTCapabilityID, formats),
		},
	}
}

func ttsCapability(id string, voices map[string]Voice, formats map[string]AudioFormat) contracts.Capability {
	voiceIDs := sortedVoiceIDs(voices)
	formatIDs := sortedFormatIDs(formats)
	return contracts.Capability{
		ID:            id,
		Name:          "Text to speech",
		Description:   "Generate an audio artifact from text.",
		Tags:          []string{"tts", "audio"},
		ExecutionMode: "sync",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []any{"text"},
			"properties": map[string]any{
				"text":   map[string]any{"type": "string", "minLength": 1, "maxLength": maxTextLength},
				"voice":  stringProperty(voiceIDs),
				"format": stringProperty(formatIDs),
				"speed":  map[string]any{"type": "number", "minimum": 0.5, "maximum": 2.0},
				"pitch":  map[string]any{"type": "number", "minimum": -12, "maximum": 12},
			},
		},
		OutputSchema: map[string]any{
			"type":     "object",
			"required": []any{"voice", "format", "media_type", "sample_rate", "speed", "pitch", "duration_seconds", "dry_run"},
			"properties": map[string]any{
				"voice":            map[string]any{"type": "string"},
				"format":           map[string]any{"type": "string"},
				"media_type":       map[string]any{"type": "string"},
				"sample_rate":      map[string]any{"type": "integer"},
				"speed":            map[string]any{"type": "number"},
				"pitch":            map[string]any{"type": "number"},
				"duration_seconds": map[string]any{"type": "number"},
				"dry_run":          map[string]any{"type": "boolean"},
			},
		},
		Examples:      []map[string]any{{"text": "hello", "voice": firstString(voiceIDs, defaultVoiceID), "format": firstString(formatIDs, defaultFormatID), "speed": defaultTTSSpeed, "pitch": defaultTTSPitch}},
		SideEffects:   "external",
		ResourceHints: []contracts.ResourceHint{},
		ArtifactHints: []contracts.ArtifactHint{{MediaType: "audio/wav", Count: "one"}},
		TimeoutHint:   "60s",
	}
}

func sttCapability(id string, formats map[string]AudioFormat) contracts.Capability {
	formatIDs := sortedFormatIDs(formats)
	return contracts.Capability{
		ID:            id,
		Name:          "Speech to text",
		Description:   "Transcribe an uploaded audio payload.",
		Tags:          []string{"stt", "audio", "transcription"},
		ExecutionMode: "sync",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []any{"audio_base64"},
			"properties": map[string]any{
				"audio_base64": map[string]any{"type": "string", "contentEncoding": "base64", "maxLength": base64.StdEncoding.EncodedLen(maxAudioBytes)},
				"media_type":   stringProperty(sortedMediaTypes(formats)),
				"format":       stringProperty(formatIDs),
				"language":     map[string]any{"type": "string"},
			},
		},
		OutputSchema: map[string]any{
			"type":     "object",
			"required": []any{"transcript", "language", "duration_seconds", "dry_run"},
			"properties": map[string]any{
				"transcript":       map[string]any{"type": "string"},
				"language":         map[string]any{"type": "string"},
				"duration_seconds": map[string]any{"type": "number"},
				"dry_run":          map[string]any{"type": "boolean"},
			},
		},
		Examples:      []map[string]any{{"audio_base64": "...", "format": firstString(formatIDs, defaultFormatID), "language": "en"}},
		SideEffects:   "external",
		ResourceHints: []contracts.ResourceHint{},
		ArtifactHints: []contracts.ArtifactHint{},
		TimeoutHint:   "60s",
	}
}

func stringProperty(enumValues []string) map[string]any {
	property := map[string]any{"type": "string"}
	if len(enumValues) > 0 {
		property["enum"] = stringEnum(enumValues)
	}
	return property
}

func stringEnum(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func sortedVoiceIDs(voices map[string]Voice) []string {
	out := make([]string, 0, len(voices))
	for id := range voices {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func sortedFormatIDs(formats map[string]AudioFormat) []string {
	out := make([]string, 0, len(formats))
	for id := range formats {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func sortedMediaTypes(formats map[string]AudioFormat) []string {
	seen := map[string]bool{}
	for _, format := range formats {
		if format.MediaType != "" {
			seen[format.MediaType] = true
		}
	}
	out := make([]string, 0, len(seen))
	for mediaType := range seen {
		out = append(out, mediaType)
	}
	sort.Strings(out)
	return out
}

func firstString(values []string, fallback string) string {
	if len(values) == 0 {
		return fallback
	}
	return values[0]
}
