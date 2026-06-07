package contracts

type ProviderManifest struct {
	SchemaVersion string       `json:"schema_version"`
	Service       Service      `json:"service"`
	Provider      Provider     `json:"provider"`
	Capabilities  []Capability `json:"capabilities"`
}

type Service struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Version      string   `json:"version"`
	ProviderKind string   `json:"provider_kind"`
	Tags         []string `json:"tags"`
}

type Provider struct {
	Endpoint   string `json:"endpoint"`
	NodeID     string `json:"node_id,omitempty"`
	HealthPath string `json:"health_path,omitempty"`
}

type Capability struct {
	ID            string           `json:"id"`
	ServiceID     string           `json:"service_id,omitempty"`
	Name          string           `json:"name"`
	Description   string           `json:"description"`
	Tags          []string         `json:"tags,omitempty"`
	ExecutionMode string           `json:"execution_mode"`
	InputSchema   map[string]any   `json:"input_schema"`
	OutputSchema  map[string]any   `json:"output_schema"`
	Examples      []map[string]any `json:"examples"`
	SideEffects   string           `json:"side_effects"`
	ResourceHints []ResourceHint   `json:"resource_hints"`
	ArtifactHints []ArtifactHint   `json:"artifact_hints"`
	TimeoutHint   string           `json:"timeout_hint"`
}

type ResourceHint struct {
	Selector string `json:"selector"`
	Required bool   `json:"required"`
	Quantity int    `json:"quantity,omitempty"`
	Notes    string `json:"notes,omitempty"`
}

type ArtifactHint struct {
	MediaType string `json:"media_type"`
	Count     string `json:"count,omitempty"`
	Notes     string `json:"notes,omitempty"`
}

type CapabilityRoute struct {
	CapabilityID       string         `json:"capability_id"`
	ServiceID          string         `json:"service_id"`
	ProviderEndpoint   string         `json:"provider_endpoint"`
	ProviderHealthPath string         `json:"provider_health_path"`
	ProviderInvokePath string         `json:"provider_invoke_path"`
	NodeID             *string        `json:"node_id,omitempty"`
	NodeManaged        bool           `json:"node_managed"`
	ServiceStartMode   string         `json:"service_start_mode"`
	ResourceHints      []ResourceHint `json:"resource_hints,omitempty"`
	ArtifactHints      []ArtifactHint `json:"artifact_hints,omitempty"`
}

type CatalogCapabilityRecord struct {
	Capability Capability      `json:"capability"`
	Route      CapabilityRoute `json:"route"`
	Service    Service         `json:"service,omitempty"`
}

type SuccessEnvelope struct {
	OK    bool              `json:"ok"`
	Data  any               `json:"data"`
	Links map[string]any    `json:"links"`
	Meta  map[string]string `json:"meta"`
}

type ErrorEnvelope struct {
	OK    bool              `json:"ok"`
	Error ErrorObject       `json:"error"`
	Links map[string]any    `json:"links"`
	Meta  map[string]string `json:"meta"`
}

type ErrorObject struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}
