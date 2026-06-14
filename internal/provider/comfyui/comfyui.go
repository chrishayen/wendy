package comfyui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"wendy/internal/contracts"
	"wendy/internal/provider"
)

const (
	DefaultServiceID    = "svc_comfyui_provider"
	DefaultCapabilityID = "cap_comfyui_image_generate"

	defaultServiceName  = "ComfyUI Provider"
	defaultVersion      = "0.1.0"
	defaultWidth        = 1024
	defaultHeight       = 1024
	defaultSteps        = 20
	defaultPollInterval = 500 * time.Millisecond
)

type Config struct {
	Endpoint        string
	ServiceID       string
	ServiceName     string
	Version         string
	CapabilityID    string
	ComfyUIURL      string
	WorkflowPath    string
	LoraCatalogPath string
	DryRun          bool
	Timeout         time.Duration
	PollInterval    time.Duration
	Client          *http.Client
	ContentTTL      time.Duration
	RunnerTokens    []string
	ComponentTokens []string
	AgentTokens     []string
}

type LoraOption struct {
	ID       string         `json:"id"`
	Name     string         `json:"name,omitempty"`
	Notes    string         `json:"notes,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type loraCatalogFile struct {
	Items []LoraOption `json:"items"`
}

type generator struct {
	cfg      Config
	client   *http.Client
	workflow map[string]any
	loras    map[string]LoraOption
}

type Server struct {
	base    *provider.Server
	cfg     Config
	gen     *generator
	now     func() time.Time
	content *contentStore
	auth    tokenPolicy
}

type tokenPolicy struct {
	allowed   map[string]struct{}
	forbidden map[string]struct{}
	enabled   bool
}

type contentStore struct {
	mu      sync.RWMutex
	records map[string]contentRecord
}

type contentRecord struct {
	ref         contracts.ProviderContentRef
	body        []byte
	unavailable bool
}

type generatedImage struct {
	Name      string
	MediaType string
	Body      []byte
}

type request struct {
	Prompt string
	Width  int
	Height int
	Seed   int64
	Steps  int
	Lora   string
}

type promptResponse struct {
	PromptID string `json:"prompt_id"`
}

type comfyImage struct {
	Filename  string
	Subfolder string
	Type      string
}

var errProviderTimeout = errors.New("provider timeout")

func NewServer(cfg Config) (*Server, error) {
	normalized, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	workflow, err := loadWorkflow(normalized.WorkflowPath)
	if err != nil {
		return nil, err
	}
	loras, err := loadLoraCatalog(normalized.LoraCatalogPath)
	if err != nil {
		return nil, err
	}
	client := normalized.Client
	if client == nil {
		client = &http.Client{Timeout: normalized.Timeout}
	}
	g := &generator{cfg: normalized, client: client, workflow: workflow, loras: loras}
	manifest := manifest(normalized)
	base, err := provider.NewServer(manifest, map[string]provider.CapabilityHandler{
		normalized.CapabilityID: g.generate,
	})
	if err != nil {
		return nil, err
	}
	return &Server{
		base:    base,
		cfg:     normalized,
		gen:     g,
		now:     time.Now,
		content: newContentStore(),
		auth:    newTokenPolicy(normalized),
	}, nil
}

func (s *Server) SetClock(now func() time.Time) {
	if now == nil {
		now = time.Now
	}
	s.now = now
	s.base.SetClock(now)
}

func (s *Server) MarkContentUnavailable(contentRef string, unavailable bool) {
	s.content.markUnavailable(contentRef, unavailable)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodGet && path == "/v1/provider/health":
		writeSuccess(w, r, http.StatusOK, contracts.ProviderHealth{
			Status:    "healthy",
			Version:   "v1",
			CheckedAt: s.now().UTC().Format(time.RFC3339),
			Details:   map[string]any{"backend": "comfyui"},
		})
	case r.Method == http.MethodGet && path == "/v1/provider/manifest":
		s.base.ServeHTTP(w, r)
	case r.Method == http.MethodGet && path == "/v1/provider/metrics":
		s.base.ServeHTTP(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/v1/provider/capabilities/") && strings.HasSuffix(path, "/invoke"):
		capabilityID := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/provider/capabilities/"), "/invoke")
		s.invoke(w, r, capabilityID)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/provider/artifacts/") && strings.HasSuffix(path, "/content"):
		contentRef := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/provider/artifacts/"), "/content")
		s.readContent(w, r, contentRef)
	default:
		writeError(w, r, http.StatusNotFound, "not_found", "route not found", false)
	}
}

func (s *Server) invoke(w http.ResponseWriter, r *http.Request, capabilityID string) {
	if capabilityID != s.cfg.CapabilityID {
		writeError(w, r, http.StatusNotFound, "not_found", "capability not found", false)
		return
	}
	if !s.authorize(w, r, authInvoke) {
		return
	}
	req, rawContext, ok := decodeInvokeBody(w, r)
	if !ok {
		return
	}
	if err := validateInvokeInput(req.Input); err != nil {
		writeError(w, r, http.StatusBadRequest, "validation_failed", validationMessage(err), false)
		return
	}
	if err := validateInvokeContext(rawContext, req.Context); err != nil {
		writeError(w, r, http.StatusBadRequest, "validation_failed", validationMessage(err), false)
		return
	}
	response, err := s.invokeComfyUI(r.Context(), req)
	if err != nil {
		s.writeInvokeError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, providerInvokeHTTPResponse{
		Output:      response.Output,
		ContentRefs: response.ContentRefs,
	})
}

func (s *Server) readContent(w http.ResponseWriter, r *http.Request, contentRef string) {
	if !s.authorize(w, r, authContent) {
		return
	}
	record, ok := s.content.get(contentRef, s.now())
	if !ok {
		writeError(w, r, http.StatusNotFound, "not_found", "provider content reference not found", false)
		return
	}
	if record.unavailable || len(record.body) == 0 {
		writeError(w, r, http.StatusServiceUnavailable, "provider_unavailable", "provider content could not be read", true)
		return
	}
	w.Header().Set("Content-Type", record.ref.MediaType)
	w.Header().Set("Content-Length", strconv.Itoa(len(record.body)))
	w.Header().Set("Digest", digestHeader(record.body))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(record.body)
}

func (s *Server) writeInvokeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errProviderTimeout):
		writeError(w, r, http.StatusGatewayTimeout, "provider_timeout", "provider invocation timed out", true)
	case errors.Is(err, provider.ErrValidation):
		writeError(w, r, http.StatusBadRequest, "validation_failed", validationMessage(err), false)
	case errors.Is(err, provider.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", validationMessage(err), false)
	case errors.Is(err, provider.ErrBackend):
		writeError(w, r, http.StatusServiceUnavailable, "provider_unavailable", "ComfyUI backend is unavailable", true)
	default:
		writeError(w, r, http.StatusInternalServerError, "provider_unavailable", err.Error(), true)
	}
}

type authKind string

const (
	authInvoke  authKind = "invoke"
	authContent authKind = "content"
)

func (s *Server) authorize(w http.ResponseWriter, r *http.Request, kind authKind) bool {
	decision := s.auth.authorize(r.Header.Get("Authorization"))
	if decision == authAllowed {
		return true
	}
	if decision == authForbidden {
		message := "provider invocation requires a runner or component credential"
		if kind == authContent {
			message = "provider content reference is runner-only"
		}
		writeError(w, r, http.StatusForbidden, "forbidden", message, false)
		return false
	}
	message := "provider invocation requires a valid runner or component credential"
	if kind == authContent {
		message = "provider content retrieval requires a valid runner or component credential"
	}
	writeError(w, r, http.StatusUnauthorized, "unauthorized", message, false)
	return false
}

type authDecision int

const (
	authAllowed authDecision = iota
	authUnauthorized
	authForbidden
)

func newTokenPolicy(cfg Config) tokenPolicy {
	policy := tokenPolicy{allowed: map[string]struct{}{}, forbidden: map[string]struct{}{}}
	for _, token := range append(cfg.RunnerTokens, cfg.ComponentTokens...) {
		if normalized := normalizeToken(token); normalized != "" {
			policy.allowed[normalized] = struct{}{}
			policy.enabled = true
		}
	}
	for _, token := range cfg.AgentTokens {
		if normalized := normalizeToken(token); normalized != "" {
			policy.forbidden[normalized] = struct{}{}
			policy.enabled = true
		}
	}
	return policy
}

func (p tokenPolicy) authorize(header string) authDecision {
	if !p.enabled {
		return authAllowed
	}
	token, ok := bearerToken(header)
	if !ok {
		return authUnauthorized
	}
	if _, ok := p.allowed[token]; ok {
		return authAllowed
	}
	if _, ok := p.forbidden[token]; ok {
		return authForbidden
	}
	return authUnauthorized
}

func bearerToken(header string) (string, bool) {
	header = strings.TrimSpace(header)
	if !strings.HasPrefix(header, "Bearer ") {
		return "", false
	}
	token := normalizeToken(header)
	if token == "" {
		return "", false
	}
	return token, true
}

func normalizeToken(token string) string {
	token = strings.TrimSpace(token)
	token = strings.TrimPrefix(token, "Bearer ")
	return strings.TrimSpace(token)
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
	if cfg.CapabilityID == "" {
		cfg.CapabilityID = DefaultCapabilityID
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 2 * time.Minute
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
	}
	if cfg.ContentTTL <= 0 {
		cfg.ContentTTL = 15 * time.Minute
	}
	cfg.ComfyUIURL = strings.TrimRight(strings.TrimSpace(cfg.ComfyUIURL), "/")
	if !cfg.DryRun {
		if cfg.ComfyUIURL == "" {
			return Config{}, fmt.Errorf("%w: comfyui_url is required unless dry_run is enabled", provider.ErrValidation)
		}
		if cfg.WorkflowPath == "" {
			return Config{}, fmt.Errorf("%w: workflow_path is required unless dry_run is enabled", provider.ErrValidation)
		}
		if parsed, err := url.Parse(cfg.ComfyUIURL); err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return Config{}, fmt.Errorf("%w: comfyui_url must be an absolute URL", provider.ErrValidation)
		}
	}
	return cfg, nil
}

type providerInvokeHTTPResponse struct {
	Output      map[string]any                 `json:"output"`
	ContentRefs []contracts.ProviderContentRef `json:"content_refs"`
}

func (s *Server) invokeComfyUI(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
	parsed, err := s.gen.parseRequest(req.Input)
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	if req.Context.DryRun {
		return contracts.ProviderInvokeResponse{
			Output: map[string]any{
				"result":     "dry_run_valid",
				"media_type": "image/png",
				"filename":   nil,
			},
			ContentRefs: []contracts.ProviderContentRef{},
		}, nil
	}
	images, err := s.gen.generateImages(ctx, parsed, req.Context.RequestID)
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	contentRefs := s.content.putImages(req.Context.JobID, images, s.now(), s.cfg.ContentTTL)
	filename := any(nil)
	if len(contentRefs) > 0 {
		filename = contentRefs[0].Name
	}
	return contracts.ProviderInvokeResponse{
		Output: map[string]any{
			"result":     "image_generated",
			"media_type": "image/png",
			"filename":   filename,
		},
		ContentRefs: contentRefs,
	}, nil
}

func decodeInvokeBody(w http.ResponseWriter, r *http.Request) (contracts.ProviderInvokeRequest, map[string]any, bool) {
	defer r.Body.Close()
	var body struct {
		Input   map[string]any `json:"input"`
		Context map[string]any `json:"context"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, r, http.StatusBadRequest, "validation_failed", "request body is invalid JSON", false)
		return contracts.ProviderInvokeRequest{}, nil, false
	}
	req := contracts.ProviderInvokeRequest{
		Input: body.Input,
		Context: contracts.ProviderInvokeContext{
			SubjectID:       stringContextField(body.Context, "subject_id"),
			RequestID:       stringContextField(body.Context, "request_id"),
			JobID:           stringContextField(body.Context, "job_id"),
			ArtifactBaseURL: stringContextField(body.Context, "artifact_base_url"),
			ResourceLeaseID: stringContextField(body.Context, "resource_lease_id"),
			DryRun:          boolContextField(body.Context, "dry_run"),
		},
	}
	return req, body.Context, true
}

func validateInvokeInput(input map[string]any) error {
	if _, err := requiredString(input, "prompt"); err != nil {
		return fmt.Errorf("%w: input.prompt is required", provider.ErrValidation)
	}
	width, err := requiredInt(input, "width")
	if err != nil {
		return err
	}
	height, err := requiredInt(input, "height")
	if err != nil {
		return err
	}
	if width < 64 || width > 2048 || height < 64 || height > 2048 {
		return fmt.Errorf("%w: width and height must be multiples of 8 between 64 and 2048", provider.ErrValidation)
	}
	if width%8 != 0 {
		return fmt.Errorf("%w: input.width must be a multiple of 8", provider.ErrValidation)
	}
	if height%8 != 0 {
		return fmt.Errorf("%w: input.height must be a multiple of 8", provider.ErrValidation)
	}
	return nil
}

func validateInvokeContext(raw map[string]any, context contracts.ProviderInvokeContext) error {
	missing := []string{}
	if context.SubjectID == "" {
		missing = append(missing, "context.subject_id")
	}
	if context.RequestID == "" {
		missing = append(missing, "context.request_id")
	}
	if context.JobID == "" {
		missing = append(missing, "context.job_id")
	}
	_, dryRunPresent := raw["dry_run"]
	if !context.DryRun && context.ResourceLeaseID == "" {
		missing = append(missing, "context.resource_lease_id")
	}
	if !dryRunPresent {
		missing = append(missing, "context.dry_run")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s required", provider.ErrValidation, requiredList(missing))
	}
	return nil
}

func requiredList(fields []string) string {
	switch len(fields) {
	case 0:
		return "no fields are"
	case 1:
		return fields[0] + " is"
	case 2:
		return fields[0] + " and " + fields[1] + " are"
	default:
		return strings.Join(fields[:len(fields)-1], ", ") + ", and " + fields[len(fields)-1] + " are"
	}
}

func stringContextField(context map[string]any, key string) string {
	value, _ := context[key].(string)
	return strings.TrimSpace(value)
}

func boolContextField(context map[string]any, key string) bool {
	value, _ := context[key].(bool)
	return value
}

func requiredInt(input map[string]any, key string) (int, error) {
	if _, ok := input[key]; !ok {
		return 0, fmt.Errorf("%w: input.%s is required", provider.ErrValidation, key)
	}
	value, err := intInput(input, key, 0)
	if err != nil {
		return 0, fmt.Errorf("%w: input.%s must be an integer", provider.ErrValidation, key)
	}
	return value, nil
}

func validationMessage(err error) string {
	message := err.Error()
	for _, prefix := range []string{
		provider.ErrValidation.Error() + ": ",
		provider.ErrBackend.Error() + ": ",
		provider.ErrNotFound.Error() + ": ",
	} {
		message = strings.TrimPrefix(message, prefix)
	}
	return message
}

func newContentStore() *contentStore {
	return &contentStore{records: map[string]contentRecord{}}
}

func (s *contentStore) putImages(jobID string, images []generatedImage, now time.Time, ttl time.Duration) []contracts.ProviderContentRef {
	refs := make([]contracts.ProviderContentRef, 0, len(images))
	expiresAt := now.UTC().Add(ttl).Format(time.RFC3339)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, image := range images {
		name := providerFilename(jobID, i, image.Name)
		refID := providerContentRef(jobID, i, image.Body)
		ref := contracts.ProviderContentRef{
			ContentRef: refID,
			Name:       name,
			MediaType:  mediaTypeOrDefault(image.MediaType),
			Size:       int64(len(image.Body)),
			Checksum:   checksum(image.Body),
			ExpiresAt:  expiresAt,
		}
		s.records[refID] = contentRecord{ref: ref, body: append([]byte(nil), image.Body...)}
		refs = append(refs, ref)
	}
	return refs
}

func (s *contentStore) get(contentRef string, now time.Time) (contentRecord, bool) {
	s.mu.RLock()
	record, ok := s.records[contentRef]
	s.mu.RUnlock()
	if !ok {
		return contentRecord{}, false
	}
	expiresAt, err := time.Parse(time.RFC3339, record.ref.ExpiresAt)
	if err != nil || !now.Before(expiresAt) {
		return contentRecord{}, false
	}
	return record, true
}

func (s *contentStore) markUnavailable(contentRef string, unavailable bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[contentRef]
	if !ok {
		return
	}
	record.unavailable = unavailable
	s.records[contentRef] = record
}

func providerFilename(jobID string, index int, fallback string) string {
	base := strings.TrimSpace(jobID)
	if base == "" {
		base = strings.TrimSuffix(strings.TrimSpace(fallback), ".png")
	}
	if base == "" {
		base = "provider-image"
	}
	if index == 0 {
		return base + ".png"
	}
	return base + "-" + strconv.Itoa(index+1) + ".png"
}

func providerContentRef(jobID string, index int, body []byte) string {
	if strings.HasPrefix(jobID, "job_") {
		ref := "pcr_" + strings.TrimPrefix(jobID, "job_")
		if index > 0 {
			ref += "_" + strconv.Itoa(index+1)
		}
		return ref
	}
	sum := sha256.Sum256(body)
	ref := "pcr_" + hex.EncodeToString(sum[:8])
	if index > 0 {
		ref += "_" + strconv.Itoa(index+1)
	}
	return ref
}

func mediaTypeOrDefault(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "image/png"
	}
	return value
}

func digestHeader(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha-256=" + base64.StdEncoding.EncodeToString(sum[:])
}

func writeSuccess(w http.ResponseWriter, r *http.Request, status int, data any) {
	writeEnvelopeJSON(w, status, contracts.SuccessEnvelope{
		OK:    true,
		Data:  data,
		Links: map[string]any{},
		Meta:  meta(r),
	})
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, retryable bool) {
	writeEnvelopeJSON(w, status, contracts.ErrorEnvelope{
		OK: false,
		Error: contracts.ErrorObject{
			Code: code, Message: message, Retryable: retryable,
		},
		Links: map[string]any{},
		Meta:  meta(r),
	})
}

func writeEnvelopeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func meta(r *http.Request) map[string]string {
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = "req_provider"
	}
	return map[string]string{"request_id": requestID, "schema_version": "v1"}
}

func loadWorkflow(path string) (map[string]any, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var workflow map[string]any
	if err := json.Unmarshal(body, &workflow); err != nil {
		return nil, fmt.Errorf("decode workflow %s: %w", path, err)
	}
	return workflow, nil
}

func loadLoraCatalog(path string) (map[string]LoraOption, error) {
	if strings.TrimSpace(path) == "" {
		return map[string]LoraOption{}, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var object loraCatalogFile
	if err := json.Unmarshal(body, &object); err != nil {
		return nil, fmt.Errorf("decode LoRA catalog %s: %w", path, err)
	}
	out := map[string]LoraOption{}
	for _, item := range object.Items {
		if item.ID == "" {
			return nil, fmt.Errorf("%w: lora id is required", provider.ErrValidation)
		}
		out[item.ID] = item
	}
	return out, nil
}

func (g *generator) generate(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
	parsed, err := g.parseRequest(req.Input)
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	if req.Context.DryRun {
		return contracts.ProviderInvokeResponse{
			Output: map[string]any{
				"result":     "dry_run_valid",
				"media_type": "image/png",
				"filename":   nil,
			},
			ContentRefs: []contracts.ProviderContentRef{},
		}, nil
	}
	images, err := g.generateImages(ctx, parsed, req.Context.RequestID)
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	artifacts := make([]contracts.ProviderArtifact, 0, len(images))
	for _, image := range images {
		artifacts = append(artifacts, contracts.ProviderArtifact{
			Name:          image.Name,
			MediaType:     mediaTypeOrDefault(image.MediaType),
			ContentBase64: base64.StdEncoding.EncodeToString(image.Body),
			Checksum:      checksum(image.Body),
		})
	}
	filename := any(nil)
	if len(images) > 0 {
		filename = images[0].Name
	}
	return contracts.ProviderInvokeResponse{
		Output: map[string]any{
			"result":     "image_generated",
			"media_type": "image/png",
			"filename":   filename,
		},
		Artifacts: artifacts,
	}, nil
}

func (g *generator) parseRequest(input map[string]any) (request, error) {
	prompt, err := requiredString(input, "prompt")
	if err != nil {
		return request{}, fmt.Errorf("%w: input.prompt is required", provider.ErrValidation)
	}
	width, err := requiredInt(input, "width")
	if err != nil {
		return request{}, err
	}
	height, err := requiredInt(input, "height")
	if err != nil {
		return request{}, err
	}
	if err := validateDimension("width", width); err != nil {
		return request{}, err
	}
	if err := validateDimension("height", height); err != nil {
		return request{}, err
	}
	steps, err := intInput(input, "steps", defaultSteps)
	if err != nil {
		return request{}, err
	}
	if steps < 1 || steps > 150 {
		return request{}, fmt.Errorf("%w: steps must be between 1 and 150", provider.ErrValidation)
	}
	seed, err := int64Input(input, "seed", 0)
	if err != nil {
		return request{}, err
	}
	lora, _ := optionalString(input, "lora")
	if lora != "" && len(g.loras) > 0 {
		if _, ok := g.loras[lora]; !ok {
			return request{}, fmt.Errorf("%w: lora %s is not in the configured catalog", provider.ErrValidation, lora)
		}
	}
	return request{Prompt: prompt, Width: width, Height: height, Seed: seed, Steps: steps, Lora: lora}, nil
}

func validateDimension(name string, value int) error {
	if value < 64 || value > 2048 {
		return fmt.Errorf("%w: width and height must be multiples of 8 between 64 and 2048", provider.ErrValidation)
	}
	if value%8 != 0 {
		return fmt.Errorf("%w: input.%s must be a multiple of 8", provider.ErrValidation, name)
	}
	return nil
}

func (g *generator) generateImages(ctx context.Context, req request, requestID string) ([]generatedImage, error) {
	if g.cfg.DryRun {
		return []generatedImage{{
			Name:      "comfyui-dry-run.png",
			MediaType: "image/png",
			Body:      dryRunPNG(),
		}}, nil
	}
	return g.generateImagesWithComfyUI(ctx, req, requestID)
}

func (g *generator) generateWithComfyUI(ctx context.Context, req request, requestID string) (contracts.ProviderInvokeResponse, error) {
	images, err := g.generateImagesWithComfyUI(ctx, req, requestID)
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	artifacts := make([]contracts.ProviderArtifact, 0, len(images))
	for _, image := range images {
		artifacts = append(artifacts, contracts.ProviderArtifact{
			Name:          image.Name,
			MediaType:     mediaTypeOrDefault(image.MediaType),
			ContentBase64: base64.StdEncoding.EncodeToString(image.Body),
			Checksum:      checksum(image.Body),
		})
	}
	return contracts.ProviderInvokeResponse{
		Output: map[string]any{
			"result":     "image_generated",
			"media_type": "image/png",
			"filename":   artifacts[0].Name,
		},
		Artifacts: artifacts,
	}, nil
}

func (g *generator) generateImagesWithComfyUI(ctx context.Context, req request, requestID string) ([]generatedImage, error) {
	if g.workflow == nil {
		return nil, fmt.Errorf("%w: workflow template is not configured", provider.ErrValidation)
	}
	if g.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, g.cfg.Timeout)
		defer cancel()
	}
	promptID, err := g.submitPrompt(ctx, req, requestID)
	if err != nil {
		return nil, err
	}
	images, err := g.waitForImages(ctx, promptID)
	if err != nil {
		return nil, err
	}
	out := make([]generatedImage, 0, len(images))
	for index, image := range images {
		body, mediaType, err := g.fetchImage(ctx, image)
		if err != nil {
			return nil, err
		}
		name := image.Filename
		if name == "" {
			name = "comfyui-image-" + strconv.Itoa(index+1)
		}
		out = append(out, generatedImage{
			Name:      name,
			MediaType: mediaType,
			Body:      body,
		})
	}
	return out, nil
}

func (g *generator) submitPrompt(ctx context.Context, req request, requestID string) (string, error) {
	rendered, err := renderWorkflow(g.workflow, map[string]any{
		"prompt": req.Prompt,
		"width":  req.Width,
		"height": req.Height,
		"seed":   req.Seed,
		"steps":  req.Steps,
		"lora":   req.Lora,
	})
	if err != nil {
		return "", err
	}
	body := map[string]any{"prompt": rendered}
	if requestID != "" {
		body["client_id"] = "wendy-" + requestID
	}
	var response promptResponse
	if err := g.postJSON(ctx, g.cfg.ComfyUIURL+"/prompt", body, &response); err != nil {
		return "", err
	}
	if response.PromptID == "" {
		return "", fmt.Errorf("%w: ComfyUI response missing prompt_id", provider.ErrBackend)
	}
	return response.PromptID, nil
}

func renderWorkflow(value any, vars map[string]any) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			rendered, err := renderWorkflow(child, vars)
			if err != nil {
				return nil, err
			}
			out[key] = rendered
		}
		return out, nil
	case []any:
		out := make([]any, 0, len(typed))
		for _, child := range typed {
			rendered, err := renderWorkflow(child, vars)
			if err != nil {
				return nil, err
			}
			out = append(out, rendered)
		}
		return out, nil
	case string:
		for key, value := range vars {
			placeholder := "{{" + key + "}}"
			if typed == placeholder {
				return value, nil
			}
		}
		out := typed
		for key, value := range vars {
			out = strings.ReplaceAll(out, "{{"+key+"}}", fmt.Sprint(value))
		}
		return out, nil
	default:
		return typed, nil
	}
}

func (g *generator) waitForImages(ctx context.Context, promptID string) ([]comfyImage, error) {
	ticker := time.NewTicker(g.cfg.PollInterval)
	defer ticker.Stop()
	for {
		images, err := g.historyImages(ctx, promptID)
		if err != nil {
			return nil, err
		}
		if len(images) > 0 {
			return images, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: %w", provider.ErrBackend, errProviderTimeout)
		case <-ticker.C:
		}
	}
}

func (g *generator) historyImages(ctx context.Context, promptID string) ([]comfyImage, error) {
	var history map[string]any
	if err := g.getJSON(ctx, g.cfg.ComfyUIURL+"/history/"+url.PathEscape(promptID), &history); err != nil {
		return nil, err
	}
	if entry, ok := history[promptID]; ok {
		return findImages(entry), nil
	}
	return findImages(history), nil
}

func findImages(value any) []comfyImage {
	images := []comfyImage{}
	var walk func(any)
	walk = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			if rawImages, ok := typed["images"].([]any); ok {
				for _, raw := range rawImages {
					if image, ok := parseImage(raw); ok {
						images = append(images, image)
					}
				}
			}
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
	return images
}

func parseImage(value any) (comfyImage, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return comfyImage{}, false
	}
	filename, _ := object["filename"].(string)
	if filename == "" {
		return comfyImage{}, false
	}
	subfolder, _ := object["subfolder"].(string)
	imageType, _ := object["type"].(string)
	return comfyImage{Filename: filename, Subfolder: subfolder, Type: imageType}, true
}

func (g *generator) fetchImage(ctx context.Context, image comfyImage) ([]byte, string, error) {
	values := url.Values{}
	values.Set("filename", image.Filename)
	if image.Subfolder != "" {
		values.Set("subfolder", image.Subfolder)
	}
	if image.Type != "" {
		values.Set("type", image.Type)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.cfg.ComfyUIURL+"/view?"+values.Encode(), nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := g.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, "", fmt.Errorf("%w: %w", provider.ErrBackend, errProviderTimeout)
		}
		return nil, "", fmt.Errorf("%w: %s", provider.ErrBackend, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, "", fmt.Errorf("%w: ComfyUI image fetch HTTP %d: %s", provider.ErrBackend, resp.StatusCode, strings.TrimSpace(string(message)))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 25<<20))
	if err != nil {
		return nil, "", err
	}
	mediaType := resp.Header.Get("Content-Type")
	if mediaType == "" {
		mediaType = "image/png"
	}
	return body, mediaType, nil
}

func (g *generator) postJSON(ctx context.Context, target string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%w: %w", provider.ErrBackend, errProviderTimeout)
		}
		return fmt.Errorf("%w: %s", provider.ErrBackend, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%w: ComfyUI HTTP %d: %s", provider.ErrBackend, resp.StatusCode, strings.TrimSpace(string(message)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (g *generator) getJSON(ctx context.Context, target string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	resp, err := g.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%w: %w", provider.ErrBackend, errProviderTimeout)
		}
		return fmt.Errorf("%w: %s", provider.ErrBackend, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%w: ComfyUI HTTP %d: %s", provider.ErrBackend, resp.StatusCode, strings.TrimSpace(string(message)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func requiredString(input map[string]any, key string) (string, error) {
	value, _ := input[key].(string)
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: %s is required", provider.ErrValidation, key)
	}
	return value, nil
}

func optionalString(input map[string]any, key string) (string, bool) {
	value, ok := input[key].(string)
	return strings.TrimSpace(value), ok
}

func intInput(input map[string]any, key string, fallback int) (int, error) {
	value, ok := input[key]
	if !ok || value == nil {
		return fallback, nil
	}
	switch typed := value.(type) {
	case int:
		return typed, nil
	case int64:
		return int(typed), nil
	case float64:
		if typed != float64(int64(typed)) {
			return 0, fmt.Errorf("%w: %s must be an integer", provider.ErrValidation, key)
		}
		return int(typed), nil
	default:
		return 0, fmt.Errorf("%w: %s must be an integer", provider.ErrValidation, key)
	}
}

func int64Input(input map[string]any, key string, fallback int64) (int64, error) {
	value, ok := input[key]
	if !ok || value == nil {
		return fallback, nil
	}
	switch typed := value.(type) {
	case int:
		return int64(typed), nil
	case int64:
		return typed, nil
	case float64:
		if typed != float64(int64(typed)) {
			return 0, fmt.Errorf("%w: %s must be an integer", provider.ErrValidation, key)
		}
		return int64(typed), nil
	default:
		return 0, fmt.Errorf("%w: %s must be an integer", provider.ErrValidation, key)
	}
}

func checksum(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func dryRunPNG() []byte {
	body, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII=")
	return body
}

func manifest(cfg Config) contracts.ProviderManifest {
	return contracts.ProviderManifest{
		SchemaVersion: "v1",
		Service: contracts.Service{
			ID:           cfg.ServiceID,
			Name:         cfg.ServiceName,
			Description:  "Purpose-specific ComfyUI image generation provider.",
			Version:      cfg.Version,
			ProviderKind: "comfyui",
			Tags:         []string{"image", "comfyui", "gpu"},
		},
		Provider: contracts.Provider{Endpoint: cfg.Endpoint, HealthPath: "/v1/provider/health"},
		Capabilities: []contracts.Capability{{
			ID:            cfg.CapabilityID,
			Name:          "ComfyUI image generation",
			Description:   "Generate an image through a configured ComfyUI workflow.",
			Tags:          []string{"image", "generation", "comfyui"},
			ExecutionMode: "sync",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []any{"prompt", "width", "height"},
				"properties": map[string]any{
					"prompt": map[string]any{"type": "string"},
					"width":  map[string]any{"type": "integer"},
					"height": map[string]any{"type": "integer"},
					"seed":   map[string]any{"type": "integer"},
					"steps":  map[string]any{"type": "integer"},
					"lora":   map[string]any{"type": "string"},
				},
			},
			OutputSchema: map[string]any{
				"type":     "object",
				"required": []any{"result", "media_type", "filename"},
				"properties": map[string]any{
					"result":     map[string]any{"type": "string"},
					"media_type": map[string]any{"type": "string"},
					"filename":   map[string]any{"type": "string"},
				},
			},
			Examples: []map[string]any{{
				"prompt": "a clean product photo of a red ceramic mug",
				"width":  1024,
				"height": 1024,
				"steps":  20,
			}},
			SideEffects:   "external",
			ResourceHints: []contracts.ResourceHint{{Selector: "gpu", Required: true, Quantity: 1}},
			ArtifactHints: []contracts.ArtifactHint{{MediaType: "image/png", Count: "one-or-more"}},
			TimeoutHint:   "120s",
		}},
	}
}
