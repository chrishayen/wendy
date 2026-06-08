package contracts

type NodeConfig struct {
	NodeID      string              `json:"node_id"`
	DisplayName string              `json:"display_name,omitempty"`
	Resources   []NodeResource      `json:"resources,omitempty"`
	Auth        []NodeAuthSubject   `json:"auth,omitempty"`
	Services    []NodeServiceConfig `json:"services"`
}

type NodeResource struct {
	ResourceID string         `json:"resource_id"`
	Tags       []string       `json:"tags"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type NodeAuthSubject struct {
	Token          string   `json:"token"`
	SubjectID      string   `json:"subject_id"`
	Scopes         []string `json:"scopes,omitempty"`
	AllowedActions []string `json:"allowed_actions"`
}

type NodeServiceConfig struct {
	ServiceID        string            `json:"service_id"`
	DisplayName      string            `json:"display_name,omitempty"`
	RuntimeAdapter   string            `json:"runtime_adapter"`
	ProviderEndpoint string            `json:"provider_endpoint"`
	InitialStatus    string            `json:"initial_status,omitempty"`
	Manifest         *ProviderManifest `json:"manifest,omitempty"`
	Metadata         map[string]any    `json:"metadata,omitempty"`
}

type NodeHealth struct {
	Status    string         `json:"status"`
	Version   string         `json:"version"`
	CheckedAt string         `json:"checked_at"`
	Details   map[string]any `json:"details"`
}

type NodeService struct {
	ServiceID        string            `json:"service_id"`
	Status           string            `json:"status"`
	RuntimeAdapter   string            `json:"runtime_adapter"`
	ProviderEndpoint string            `json:"provider_endpoint"`
	Manifest         *ProviderManifest `json:"manifest"`
	Links            map[string]any    `json:"links"`
}
