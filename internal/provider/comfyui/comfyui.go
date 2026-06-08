package comfyui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"pacp/internal/contracts"
	"pacp/internal/provider"
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

func NewServer(cfg Config) (*provider.Server, error) {
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
	return provider.NewServer(manifest(normalized), map[string]provider.CapabilityHandler{
		normalized.CapabilityID: g.generate,
	})
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
	if g.cfg.DryRun {
		return g.dryRun(parsed), nil
	}
	return g.generateWithComfyUI(ctx, parsed, req.Context.RequestID)
}

func (g *generator) parseRequest(input map[string]any) (request, error) {
	prompt, err := requiredString(input, "prompt")
	if err != nil {
		return request{}, err
	}
	width, err := intInput(input, "width", defaultWidth)
	if err != nil {
		return request{}, err
	}
	height, err := intInput(input, "height", defaultHeight)
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
	if value < 64 || value > 4096 {
		return fmt.Errorf("%w: %s must be between 64 and 4096", provider.ErrValidation, name)
	}
	if value%8 != 0 {
		return fmt.Errorf("%w: %s must be divisible by 8", provider.ErrValidation, name)
	}
	return nil
}

func (g *generator) dryRun(req request) contracts.ProviderInvokeResponse {
	body := dryRunPNG()
	checksum := checksum(body)
	return contracts.ProviderInvokeResponse{
		Output: map[string]any{
			"prompt_id":   "dry_run",
			"image_count": 1,
			"width":       req.Width,
			"height":      req.Height,
			"seed":        req.Seed,
			"steps":       req.Steps,
			"lora":        req.Lora,
			"dry_run":     true,
		},
		Artifacts: []contracts.ProviderArtifact{{
			Name:          "comfyui-dry-run.png",
			MediaType:     "image/png",
			ContentBase64: base64.StdEncoding.EncodeToString(body),
			Checksum:      checksum,
		}},
	}
}

func (g *generator) generateWithComfyUI(ctx context.Context, req request, requestID string) (contracts.ProviderInvokeResponse, error) {
	if g.workflow == nil {
		return contracts.ProviderInvokeResponse{}, fmt.Errorf("%w: workflow template is not configured", provider.ErrValidation)
	}
	if g.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, g.cfg.Timeout)
		defer cancel()
	}
	promptID, err := g.submitPrompt(ctx, req, requestID)
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	images, err := g.waitForImages(ctx, promptID)
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	artifacts := make([]contracts.ProviderArtifact, 0, len(images))
	for index, image := range images {
		body, mediaType, err := g.fetchImage(ctx, image)
		if err != nil {
			return contracts.ProviderInvokeResponse{}, err
		}
		name := image.Filename
		if name == "" {
			name = "comfyui-image-" + strconv.Itoa(index+1)
		}
		artifacts = append(artifacts, contracts.ProviderArtifact{
			Name:          name,
			MediaType:     mediaType,
			ContentBase64: base64.StdEncoding.EncodeToString(body),
			Checksum:      checksum(body),
		})
	}
	return contracts.ProviderInvokeResponse{
		Output: map[string]any{
			"prompt_id":   promptID,
			"image_count": len(artifacts),
			"width":       req.Width,
			"height":      req.Height,
			"seed":        req.Seed,
			"steps":       req.Steps,
			"lora":        req.Lora,
			"dry_run":     false,
		},
		Artifacts: artifacts,
	}, nil
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
		body["client_id"] = "pacp-" + requestID
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
			return nil, fmt.Errorf("%w: ComfyUI prompt %s did not produce images before timeout", provider.ErrBackend, promptID)
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
				"required": []any{"prompt"},
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
				"required": []any{"prompt_id", "image_count", "width", "height", "seed", "steps", "dry_run"},
				"properties": map[string]any{
					"prompt_id":   map[string]any{"type": "string"},
					"image_count": map[string]any{"type": "integer"},
					"width":       map[string]any{"type": "integer"},
					"height":      map[string]any{"type": "integer"},
					"seed":        map[string]any{"type": "integer"},
					"steps":       map[string]any{"type": "integer"},
					"lora":        map[string]any{"type": "string"},
					"dry_run":     map[string]any{"type": "boolean"},
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
