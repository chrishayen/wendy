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
	ServiceID          string                `json:"service_id"`
	DisplayName        string                `json:"display_name,omitempty"`
	RuntimeAdapter     string                `json:"runtime_adapter"`
	ProviderEndpoint   string                `json:"provider_endpoint"`
	InitialStatus      string                `json:"initial_status,omitempty"`
	IdleTimeoutSeconds int                   `json:"idle_timeout_seconds,omitempty"`
	Manifest           *ProviderManifest     `json:"manifest,omitempty"`
	Process            *ProcessRuntimeConfig `json:"process,omitempty"`
	Docker             *DockerRuntimeConfig  `json:"docker,omitempty"`
	Metadata           map[string]any        `json:"metadata,omitempty"`
}

type ProcessRuntimeConfig struct {
	Command             []string          `json:"command"`
	WorkingDirectory    string            `json:"working_directory,omitempty"`
	Environment         map[string]string `json:"environment,omitempty"`
	ReadyURL            string            `json:"ready_url,omitempty"`
	ReadyTimeoutSeconds int               `json:"ready_timeout_seconds,omitempty"`
	StopTimeoutSeconds  int               `json:"stop_timeout_seconds,omitempty"`
}

type DockerRuntimeConfig struct {
	ContainerName       string `json:"container_name"`
	Binary              string `json:"binary,omitempty"`
	ReadyURL            string `json:"ready_url,omitempty"`
	ReadyTimeoutSeconds int    `json:"ready_timeout_seconds,omitempty"`
	StopTimeoutSeconds  int    `json:"stop_timeout_seconds,omitempty"`
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

type NodeLifecycleEvent struct {
	EventID        string `json:"event_id"`
	ServiceID      string `json:"service_id"`
	Action         string `json:"action"`
	Status         string `json:"status"`
	Message        string `json:"message,omitempty"`
	OccurredAt     string `json:"occurred_at"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}
