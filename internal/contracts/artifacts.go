package contracts

type ArtifactUploadState string

const (
	ArtifactUploadCreated   ArtifactUploadState = "created"
	ArtifactUploadReceived  ArtifactUploadState = "received"
	ArtifactUploadCompleted ArtifactUploadState = "completed"
	ArtifactUploadAborted   ArtifactUploadState = "aborted"
	ArtifactUploadExpired   ArtifactUploadState = "expired"
)

type CreateArtifactUploadRequest struct {
	Name             string         `json:"name"`
	MediaType        string         `json:"media_type"`
	ProducerRef      string         `json:"producer_ref,omitempty"`
	OwnerSubjectID   string         `json:"owner_subject_id"`
	ExpectedSize     *int64         `json:"expected_size,omitempty"`
	ExpectedChecksum string         `json:"expected_checksum,omitempty"`
	Metadata         map[string]any `json:"metadata,omitempty"`
}

type ArtifactUploadSession struct {
	UploadID         string              `json:"upload_id"`
	State            ArtifactUploadState `json:"state"`
	Name             string              `json:"name,omitempty"`
	MediaType        string              `json:"media_type,omitempty"`
	ProducerRef      string              `json:"producer_ref,omitempty"`
	OwnerSubjectID   string              `json:"owner_subject_id,omitempty"`
	ReceivedSize     *int64              `json:"received_size"`
	ExpectedSize     *int64              `json:"expected_size,omitempty"`
	ExpectedChecksum string              `json:"expected_checksum,omitempty"`
	ArtifactID       *string             `json:"artifact_id"`
	ExpiresAt        string              `json:"expires_at,omitempty"`
	CompletedAt      string              `json:"completed_at,omitempty"`
	Links            map[string]any      `json:"links"`
}

type CompleteArtifactUploadRequest struct {
	Checksum string `json:"checksum"`
	Size     int64  `json:"size"`
}

type Artifact struct {
	ArtifactID     string         `json:"artifact_id"`
	Name           string         `json:"name"`
	MediaType      string         `json:"media_type"`
	Size           int64          `json:"size"`
	Checksum       string         `json:"checksum"`
	CreatedAt      string         `json:"created_at"`
	ExpiresAt      string         `json:"expires_at,omitempty"`
	ProducerRef    string         `json:"producer_ref,omitempty"`
	OwnerSubjectID string         `json:"owner_subject_id"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	Links          map[string]any `json:"links"`
}

type ArtifactRetentionSweepResult struct {
	CheckedAt            string `json:"checked_at"`
	ExpiredUploads       int    `json:"expired_uploads"`
	ExpiredArtifacts     int    `json:"expired_artifacts"`
	DeletedUploadFiles   int    `json:"deleted_upload_files"`
	DeletedArtifactFiles int    `json:"deleted_artifact_files"`
}

type RegisterLocalArtifactRequest struct {
	Path           string         `json:"path"`
	Name           string         `json:"name,omitempty"`
	MediaType      string         `json:"media_type"`
	ProducerRef    string         `json:"producer_ref,omitempty"`
	OwnerSubjectID string         `json:"owner_subject_id"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type ArtifactPolicyContext struct {
	ResourceKind   string `json:"resource_kind"`
	ArtifactID     string `json:"artifact_id"`
	OwnerSubjectID string `json:"owner_subject_id"`
	ProducerRef    string `json:"producer_ref,omitempty"`
	PolicyState    string `json:"policy_state"`
}
