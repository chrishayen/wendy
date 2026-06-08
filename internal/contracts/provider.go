package contracts

type ProviderHealth struct {
	Status    string         `json:"status"`
	Version   string         `json:"version"`
	CheckedAt string         `json:"checked_at"`
	Details   map[string]any `json:"details,omitempty"`
}

type ProviderInvokeRequest struct {
	Input   map[string]any        `json:"input"`
	Context ProviderInvokeContext `json:"context,omitempty"`
}

type ProviderInvokeContext struct {
	SubjectID       string `json:"subject_id,omitempty"`
	RequestID       string `json:"request_id,omitempty"`
	JobID           string `json:"job_id,omitempty"`
	ArtifactBaseURL string `json:"artifact_base_url,omitempty"`
	ResourceLeaseID string `json:"resource_lease_id,omitempty"`
	DryRun          bool   `json:"dry_run,omitempty"`
}

type ProviderInvokeResponse struct {
	Output    map[string]any     `json:"output"`
	Artifacts []ProviderArtifact `json:"artifacts,omitempty"`
}

type ProviderArtifact struct {
	Name          string `json:"name"`
	MediaType     string `json:"media_type"`
	ContentBase64 string `json:"content_base64,omitempty"`
	Checksum      string `json:"checksum,omitempty"`
}
