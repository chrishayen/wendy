package contracts

type Tool struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	Description   string           `json:"description"`
	Tags          []string         `json:"tags,omitempty"`
	ExecutionMode string           `json:"execution_mode"`
	InputSchema   map[string]any   `json:"input_schema"`
	OutputSchema  map[string]any   `json:"output_schema"`
	SideEffects   string           `json:"side_effects"`
	ResourceHints []ResourceHint   `json:"resource_hints"`
	ArtifactHints []ArtifactHint   `json:"artifact_hints"`
	Examples      []map[string]any `json:"examples"`
	Links         map[string]any   `json:"links"`
}

type InvokeToolRequest struct {
	Input         map[string]any `json:"input"`
	DryRun        bool           `json:"dry_run,omitempty"`
	PreferredMode string         `json:"preferred_mode,omitempty"`
}

type InvokeToolResponse struct {
	Mode      string         `json:"mode"`
	JobID     string         `json:"job_id,omitempty"`
	Output    map[string]any `json:"output,omitempty"`
	Artifacts []Artifact     `json:"artifacts,omitempty"`
}

type AgentArtifact struct {
	ArtifactID  string         `json:"artifact_id"`
	Name        string         `json:"name"`
	MediaType   string         `json:"media_type"`
	Size        int64          `json:"size"`
	Checksum    string         `json:"checksum"`
	CreatedAt   string         `json:"created_at"`
	ProducerRef string         `json:"producer_ref,omitempty"`
	Links       map[string]any `json:"links"`
}
