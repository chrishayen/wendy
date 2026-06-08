package aitoolkit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"pacp/internal/contracts"
	"pacp/internal/provider"
)

const (
	DefaultServiceID                 = "svc_ai_toolkit_provider"
	DefaultDatasetRegisterCapability = "cap_ai_toolkit_dataset_register"
	DefaultDatasetListCapability     = "cap_ai_toolkit_dataset_list"
	DefaultDatasetInspectCapability  = "cap_ai_toolkit_dataset_inspect"
	DefaultDatasetUpdateCapability   = "cap_ai_toolkit_dataset_update"
	DefaultTrainCapability           = "cap_ai_toolkit_lora_train"

	defaultServiceName = "AI-Toolkit Provider"
	defaultVersion     = "0.1.0"
	defaultPreset      = "z-image-turbo-lora"
)

type Config struct {
	Endpoint                  string
	ServiceID                 string
	ServiceName               string
	Version                   string
	DatasetRegisterCapability string
	DatasetListCapability     string
	DatasetInspectCapability  string
	DatasetUpdateCapability   string
	TrainCapability           string
	WorkspaceRoot             string
	DryRun                    bool
	TrainCommand              []string
	Timeout                   time.Duration
}

type Dataset struct {
	DatasetID  string         `json:"dataset_id"`
	Name       string         `json:"name"`
	Path       string         `json:"path"`
	ImageCount int            `json:"image_count"`
	CreatedAt  string         `json:"created_at"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type TrainingOutput struct {
	OutputID   string         `json:"output_id"`
	DatasetID  string         `json:"dataset_id"`
	OutputName string         `json:"output_name"`
	Preset     string         `json:"preset"`
	Steps      int            `json:"steps"`
	Rank       int            `json:"rank"`
	CreatedAt  string         `json:"created_at"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type stateFile struct {
	Datasets map[string]Dataset        `json:"datasets"`
	Outputs  map[string]TrainingOutput `json:"outputs"`
}

type providerImpl struct {
	cfg       Config
	mu        sync.Mutex
	statePath string
	datasets  map[string]Dataset
	outputs   map[string]TrainingOutput
	now       func() time.Time
}

type trainEngineRequest struct {
	Dataset    Dataset        `json:"dataset"`
	OutputName string         `json:"output_name"`
	Preset     string         `json:"preset"`
	Steps      int            `json:"steps"`
	Rank       int            `json:"rank"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type trainEngineResponse struct {
	Metadata      map[string]any `json:"metadata,omitempty"`
	ArtifactName  string         `json:"artifact_name,omitempty"`
	MediaType     string         `json:"media_type,omitempty"`
	ContentBase64 string         `json:"content_base64,omitempty"`
}

func NewServer(cfg Config) (*provider.Server, error) {
	normalized, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(normalized.WorkspaceRoot, 0o700); err != nil {
		return nil, err
	}
	p := &providerImpl{
		cfg:       normalized,
		statePath: filepath.Join(normalized.WorkspaceRoot, "provider-state.json"),
		datasets:  map[string]Dataset{},
		outputs:   map[string]TrainingOutput{},
		now:       time.Now,
	}
	if err := p.loadState(); err != nil {
		return nil, err
	}
	return provider.NewServer(manifest(normalized), map[string]provider.CapabilityHandler{
		normalized.DatasetRegisterCapability: p.registerDataset,
		normalized.DatasetListCapability:     p.listDatasets,
		normalized.DatasetInspectCapability:  p.inspectDataset,
		normalized.DatasetUpdateCapability:   p.updateDataset,
		normalized.TrainCapability:           p.train,
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
	if cfg.DatasetRegisterCapability == "" {
		cfg.DatasetRegisterCapability = DefaultDatasetRegisterCapability
	}
	if cfg.DatasetListCapability == "" {
		cfg.DatasetListCapability = DefaultDatasetListCapability
	}
	if cfg.DatasetInspectCapability == "" {
		cfg.DatasetInspectCapability = DefaultDatasetInspectCapability
	}
	if cfg.DatasetUpdateCapability == "" {
		cfg.DatasetUpdateCapability = DefaultDatasetUpdateCapability
	}
	if cfg.TrainCapability == "" {
		cfg.TrainCapability = DefaultTrainCapability
	}
	if cfg.WorkspaceRoot == "" {
		return Config{}, fmt.Errorf("%w: workspace_root is required", provider.ErrValidation)
	}
	absRoot, err := filepath.Abs(cfg.WorkspaceRoot)
	if err != nil {
		return Config{}, err
	}
	cfg.WorkspaceRoot = absRoot
	if cfg.Timeout <= 0 {
		cfg.Timeout = time.Minute
	}
	if !cfg.DryRun && len(cfg.TrainCommand) == 0 {
		return Config{}, fmt.Errorf("%w: train_command is required unless dry_run is enabled", provider.ErrValidation)
	}
	return cfg, nil
}

func (p *providerImpl) loadState() error {
	body, err := os.ReadFile(p.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var state stateFile
	if err := json.Unmarshal(body, &state); err != nil {
		return err
	}
	if state.Datasets != nil {
		p.datasets = state.Datasets
	}
	if state.Outputs != nil {
		p.outputs = state.Outputs
	}
	return nil
}

func (p *providerImpl) saveStateLocked() error {
	body, err := json.MarshalIndent(stateFile{Datasets: p.datasets, Outputs: p.outputs}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p.statePath, body, 0o600)
}

func (p *providerImpl) registerDataset(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
	datasetID, err := requiredID(req.Input, "dataset_id")
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	name, err := requiredString(req.Input, "name")
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	rawPath, err := requiredString(req.Input, "path")
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	metadata, _ := req.Input["metadata"].(map[string]any)
	resolved, err := p.resolveDatasetPath(rawPath)
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	count, err := countImages(resolved)
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	dataset := Dataset{
		DatasetID:  datasetID,
		Name:       name,
		Path:       relativeToRoot(p.cfg.WorkspaceRoot, resolved),
		ImageCount: count,
		CreatedAt:  p.now().UTC().Format(time.RFC3339),
		Metadata:   metadata,
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.datasets[datasetID]; exists {
		return contracts.ProviderInvokeResponse{}, fmt.Errorf("%w: dataset_id %s is already registered", provider.ErrValidation, datasetID)
	}
	p.datasets[datasetID] = dataset
	if err := p.saveStateLocked(); err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	return contracts.ProviderInvokeResponse{Output: datasetOutput(dataset)}, nil
}

func (p *providerImpl) listDatasets(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := make([]string, 0, len(p.datasets))
	for id := range p.datasets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	items := make([]any, 0, len(ids))
	for _, id := range ids {
		items = append(items, datasetOutput(p.datasets[id]))
	}
	return contracts.ProviderInvokeResponse{Output: map[string]any{"items": items, "count": len(items)}}, nil
}

func (p *providerImpl) inspectDataset(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
	datasetID, err := requiredID(req.Input, "dataset_id")
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	dataset, ok := p.datasets[datasetID]
	if !ok {
		return contracts.ProviderInvokeResponse{}, fmt.Errorf("%w: dataset %s is not registered", provider.ErrValidation, datasetID)
	}
	return contracts.ProviderInvokeResponse{Output: datasetOutput(dataset)}, nil
}

func (p *providerImpl) updateDataset(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
	datasetID, err := requiredID(req.Input, "dataset_id")
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	name, hasName, err := optionalString(req.Input, "name")
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	path, hasPath, err := optionalString(req.Input, "path")
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	metadata, hasMetadata, err := optionalObject(req.Input, "metadata")
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	if !hasName && !hasPath && !hasMetadata {
		return contracts.ProviderInvokeResponse{}, fmt.Errorf("%w: at least one of name, path, or metadata is required", provider.ErrValidation)
	}

	var resolved string
	var count int
	if hasPath {
		resolved, err = p.resolveDatasetPath(path)
		if err != nil {
			return contracts.ProviderInvokeResponse{}, err
		}
		count, err = countImages(resolved)
		if err != nil {
			return contracts.ProviderInvokeResponse{}, err
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	dataset, ok := p.datasets[datasetID]
	if !ok {
		return contracts.ProviderInvokeResponse{}, fmt.Errorf("%w: dataset %s is not registered", provider.ErrValidation, datasetID)
	}
	if hasName {
		dataset.Name = name
	}
	if hasPath {
		dataset.Path = relativeToRoot(p.cfg.WorkspaceRoot, resolved)
		dataset.ImageCount = count
	}
	if hasMetadata {
		dataset.Metadata = metadata
	}
	p.datasets[datasetID] = dataset
	if err := p.saveStateLocked(); err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	return contracts.ProviderInvokeResponse{Output: datasetOutput(dataset)}, nil
}

func (p *providerImpl) train(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
	datasetID, err := requiredID(req.Input, "dataset_id")
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	outputName, err := requiredID(req.Input, "output_name")
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	preset := optionalStringDefault(req.Input, "preset", defaultPreset)
	steps, err := intInput(req.Input, "steps", 1000)
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	if steps < 1 || steps > 100000 {
		return contracts.ProviderInvokeResponse{}, fmt.Errorf("%w: steps must be between 1 and 100000", provider.ErrValidation)
	}
	rank, err := intInput(req.Input, "rank", 16)
	if err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}
	if rank < 1 || rank > 256 {
		return contracts.ProviderInvokeResponse{}, fmt.Errorf("%w: rank must be between 1 and 256", provider.ErrValidation)
	}
	metadata, _ := req.Input["metadata"].(map[string]any)
	p.mu.Lock()
	dataset, ok := p.datasets[datasetID]
	p.mu.Unlock()
	if !ok {
		return contracts.ProviderInvokeResponse{}, fmt.Errorf("%w: dataset %s is not registered", provider.ErrValidation, datasetID)
	}

	engine := trainEngineResponse{}
	if p.cfg.DryRun {
		engine.Metadata = map[string]any{"mode": "dry_run"}
		engine.ArtifactName = outputName + ".json"
		engine.MediaType = "application/json"
		body, _ := json.Marshal(map[string]any{"dataset_id": datasetID, "output_name": outputName, "preset": preset})
		engine.ContentBase64 = base64.StdEncoding.EncodeToString(body)
	} else if err := runTrainEngine(ctx, p.cfg.Timeout, p.cfg.TrainCommand, trainEngineRequest{
		Dataset: dataset, OutputName: outputName, Preset: preset, Steps: steps, Rank: rank, Metadata: metadata,
	}, &engine); err != nil {
		return contracts.ProviderInvokeResponse{}, err
	}

	output := TrainingOutput{
		OutputID:   "lora_" + outputName,
		DatasetID:  datasetID,
		OutputName: outputName,
		Preset:     preset,
		Steps:      steps,
		Rank:       rank,
		CreatedAt:  p.now().UTC().Format(time.RFC3339),
		Metadata:   mergeMaps(metadata, engine.Metadata),
	}
	p.mu.Lock()
	p.outputs[output.OutputID] = output
	if err := p.saveStateLocked(); err != nil {
		p.mu.Unlock()
		return contracts.ProviderInvokeResponse{}, err
	}
	p.mu.Unlock()

	response := contracts.ProviderInvokeResponse{Output: trainingOutput(output, p.cfg.DryRun)}
	if engine.ContentBase64 != "" {
		body, err := base64.StdEncoding.DecodeString(engine.ContentBase64)
		if err != nil {
			return contracts.ProviderInvokeResponse{}, fmt.Errorf("%w: training engine returned invalid content_base64", provider.ErrBackend)
		}
		name := engine.ArtifactName
		if name == "" {
			name = outputName + ".bin"
		}
		mediaType := engine.MediaType
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		response.Artifacts = []contracts.ProviderArtifact{{
			Name:          name,
			MediaType:     mediaType,
			ContentBase64: engine.ContentBase64,
			Checksum:      checksum(body),
		}}
	}
	return response, nil
}

func (p *providerImpl) resolveDatasetPath(rawPath string) (string, error) {
	path := rawPath
	if !filepath.IsAbs(path) {
		path = filepath.Join(p.cfg.WorkspaceRoot, path)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if !withinRoot(p.cfg.WorkspaceRoot, absPath) {
		return "", fmt.Errorf("%w: dataset path is outside the provider workspace", provider.ErrValidation)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: dataset path must be a directory", provider.ErrValidation)
	}
	return absPath, nil
}

func withinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != "..")
}

func countImages(root string) (int, error) {
	count := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(entry.Name())) {
		case ".jpg", ".jpeg", ".png", ".webp":
			count++
		}
		return nil
	})
	return count, err
}

func relativeToRoot(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}

func runTrainEngine(ctx context.Context, timeout time.Duration, command []string, request trainEngineRequest, out *trainEngineResponse) error {
	if len(command) == 0 {
		return fmt.Errorf("%w: training command is not configured", provider.ErrBackend)
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
		return fmt.Errorf("%w: training command failed: %s", provider.ErrBackend, message)
	}
	if err := json.Unmarshal(stdout, out); err != nil {
		return fmt.Errorf("%w: training command returned invalid JSON: %s", provider.ErrBackend, err)
	}
	return nil
}

func datasetOutput(dataset Dataset) map[string]any {
	return map[string]any{
		"dataset_id":  dataset.DatasetID,
		"name":        dataset.Name,
		"path":        dataset.Path,
		"image_count": dataset.ImageCount,
		"created_at":  dataset.CreatedAt,
		"metadata":    dataset.Metadata,
	}
}

func trainingOutput(output TrainingOutput, dryRun bool) map[string]any {
	return map[string]any{
		"output_id":   output.OutputID,
		"dataset_id":  output.DatasetID,
		"output_name": output.OutputName,
		"preset":      output.Preset,
		"steps":       output.Steps,
		"rank":        output.Rank,
		"created_at":  output.CreatedAt,
		"metadata":    output.Metadata,
		"dry_run":     dryRun,
	}
}

func mergeMaps(first, second map[string]any) map[string]any {
	if len(first) == 0 && len(second) == 0 {
		return nil
	}
	out := map[string]any{}
	for key, value := range first {
		out[key] = value
	}
	for key, value := range second {
		out[key] = value
	}
	return out
}

func requiredID(input map[string]any, key string) (string, error) {
	value, err := requiredString(input, key)
	if err != nil {
		return "", err
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			continue
		}
		return "", fmt.Errorf("%w: %s may contain only letters, digits, underscore, dash, or dot", provider.ErrValidation, key)
	}
	return value, nil
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

func optionalString(input map[string]any, key string) (string, bool, error) {
	raw, ok := input[key]
	if !ok || raw == nil {
		return "", false, nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", false, fmt.Errorf("%w: %s must be a string", provider.ErrValidation, key)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false, fmt.Errorf("%w: %s must not be empty", provider.ErrValidation, key)
	}
	return value, true, nil
}

func optionalObject(input map[string]any, key string) (map[string]any, bool, error) {
	raw, ok := input[key]
	if !ok || raw == nil {
		return nil, false, nil
	}
	value, ok := raw.(map[string]any)
	if !ok {
		return nil, false, fmt.Errorf("%w: %s must be an object", provider.ErrValidation, key)
	}
	return value, true, nil
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

func checksum(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func manifest(cfg Config) contracts.ProviderManifest {
	return contracts.ProviderManifest{
		SchemaVersion: "v1",
		Service: contracts.Service{
			ID:           cfg.ServiceID,
			Name:         cfg.ServiceName,
			Description:  "Purpose-specific AI-Toolkit dataset and LoRA training provider.",
			Version:      cfg.Version,
			ProviderKind: "ai_toolkit",
			Tags:         []string{"ai-toolkit", "training", "lora"},
		},
		Provider: contracts.Provider{Endpoint: cfg.Endpoint, HealthPath: "/v1/provider/health"},
		Capabilities: []contracts.Capability{
			datasetRegisterCapability(cfg.DatasetRegisterCapability),
			datasetListCapability(cfg.DatasetListCapability),
			datasetInspectCapability(cfg.DatasetInspectCapability),
			datasetUpdateCapability(cfg.DatasetUpdateCapability),
			trainCapability(cfg.TrainCapability),
		},
	}
}

func datasetRegisterCapability(id string) contracts.Capability {
	return contracts.Capability{
		ID:            id,
		Name:          "Register dataset",
		Description:   "Register a dataset directory in the provider workspace.",
		Tags:          []string{"dataset", "register"},
		ExecutionMode: "sync",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []any{"dataset_id", "name", "path"},
			"properties": map[string]any{
				"dataset_id": map[string]any{"type": "string"},
				"name":       map[string]any{"type": "string"},
				"path":       map[string]any{"type": "string"},
				"metadata":   map[string]any{"type": "object"},
			},
		},
		OutputSchema:  datasetSchema(),
		Examples:      []map[string]any{{"dataset_id": "product_photos", "name": "Product Photos", "path": "datasets/product_photos"}},
		SideEffects:   "local_state",
		ResourceHints: []contracts.ResourceHint{},
		ArtifactHints: []contracts.ArtifactHint{},
		TimeoutHint:   "30s",
	}
}

func datasetListCapability(id string) contracts.Capability {
	return contracts.Capability{
		ID:            id,
		Name:          "List datasets",
		Description:   "List datasets registered in the provider workspace.",
		Tags:          []string{"dataset", "list"},
		ExecutionMode: "sync",
		InputSchema:   map[string]any{"type": "object"},
		OutputSchema: map[string]any{
			"type":     "object",
			"required": []any{"items", "count"},
			"properties": map[string]any{
				"items": map[string]any{"type": "array"},
				"count": map[string]any{"type": "integer"},
			},
		},
		Examples:      []map[string]any{{}},
		SideEffects:   "none",
		ResourceHints: []contracts.ResourceHint{},
		ArtifactHints: []contracts.ArtifactHint{},
		TimeoutHint:   "30s",
	}
}

func datasetInspectCapability(id string) contracts.Capability {
	return contracts.Capability{
		ID:            id,
		Name:          "Inspect dataset",
		Description:   "Read metadata for a registered dataset.",
		Tags:          []string{"dataset", "inspect"},
		ExecutionMode: "sync",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []any{"dataset_id"},
			"properties": map[string]any{
				"dataset_id": map[string]any{"type": "string"},
			},
		},
		OutputSchema:  datasetSchema(),
		Examples:      []map[string]any{{"dataset_id": "product_photos"}},
		SideEffects:   "none",
		ResourceHints: []contracts.ResourceHint{},
		ArtifactHints: []contracts.ArtifactHint{},
		TimeoutHint:   "30s",
	}
}

func datasetUpdateCapability(id string) contracts.Capability {
	return contracts.Capability{
		ID:            id,
		Name:          "Update dataset",
		Description:   "Update the name, path, or metadata for a registered dataset in the provider workspace.",
		Tags:          []string{"dataset", "update"},
		ExecutionMode: "sync",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []any{"dataset_id"},
			"properties": map[string]any{
				"dataset_id": map[string]any{"type": "string"},
				"name":       map[string]any{"type": "string"},
				"path":       map[string]any{"type": "string"},
				"metadata":   map[string]any{"type": "object"},
			},
		},
		OutputSchema:  datasetSchema(),
		Examples:      []map[string]any{{"dataset_id": "product_photos", "name": "Updated Product Photos", "metadata": map[string]any{"source": "operator"}}},
		SideEffects:   "local_state",
		ResourceHints: []contracts.ResourceHint{},
		ArtifactHints: []contracts.ArtifactHint{},
		TimeoutHint:   "30s",
	}
}

func trainCapability(id string) contracts.Capability {
	return contracts.Capability{
		ID:            id,
		Name:          "Train LoRA",
		Description:   "Run a LoRA training request against a registered dataset.",
		Tags:          []string{"training", "lora"},
		ExecutionMode: "sync",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []any{"dataset_id", "output_name"},
			"properties": map[string]any{
				"dataset_id":  map[string]any{"type": "string"},
				"output_name": map[string]any{"type": "string"},
				"preset":      map[string]any{"type": "string"},
				"steps":       map[string]any{"type": "integer"},
				"rank":        map[string]any{"type": "integer"},
				"metadata":    map[string]any{"type": "object"},
			},
		},
		OutputSchema: map[string]any{
			"type":     "object",
			"required": []any{"output_id", "dataset_id", "output_name", "preset", "steps", "rank", "dry_run"},
			"properties": map[string]any{
				"output_id":   map[string]any{"type": "string"},
				"dataset_id":  map[string]any{"type": "string"},
				"output_name": map[string]any{"type": "string"},
				"preset":      map[string]any{"type": "string"},
				"steps":       map[string]any{"type": "integer"},
				"rank":        map[string]any{"type": "integer"},
				"created_at":  map[string]any{"type": "string"},
				"metadata":    map[string]any{"type": "object"},
				"dry_run":     map[string]any{"type": "boolean"},
			},
		},
		Examples:      []map[string]any{{"dataset_id": "product_photos", "output_name": "product_photo_lora", "preset": defaultPreset}},
		SideEffects:   "external",
		ResourceHints: []contracts.ResourceHint{{Selector: "gpu", Required: true, Quantity: 1}},
		ArtifactHints: []contracts.ArtifactHint{{MediaType: "application/json", Count: "zero-or-one"}},
		TimeoutHint:   "3600s",
	}
}

func datasetSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []any{"dataset_id", "name", "path", "image_count", "created_at"},
		"properties": map[string]any{
			"dataset_id":  map[string]any{"type": "string"},
			"name":        map[string]any{"type": "string"},
			"path":        map[string]any{"type": "string"},
			"image_count": map[string]any{"type": "integer"},
			"created_at":  map[string]any{"type": "string"},
			"metadata":    map[string]any{"type": "object"},
		},
	}
}
