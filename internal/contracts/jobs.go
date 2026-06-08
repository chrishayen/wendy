package contracts

type JobState string

const (
	JobQueued    JobState = "queued"
	JobClaimed   JobState = "claimed"
	JobRunning   JobState = "running"
	JobSucceeded JobState = "succeeded"
	JobFailed    JobState = "failed"
	JobCanceled  JobState = "canceled"
	JobExpired   JobState = "expired"
)

type Job struct {
	JobID         string         `json:"job_id"`
	State         JobState       `json:"state"`
	CreatedAt     string         `json:"created_at"`
	UpdatedAt     string         `json:"updated_at"`
	StatusMessage string         `json:"status_message,omitempty"`
	InputSummary  map[string]any `json:"input_summary,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	Claim         *JobClaim      `json:"claim"`
	ResourceRefs  []string       `json:"resource_refs,omitempty"`
	ArtifactRefs  []string       `json:"artifact_refs"`
	LogCursor     *string        `json:"log_cursor"`
	TerminalError *ErrorObject   `json:"terminal_error"`
	Links         map[string]any `json:"links"`
}

type AgentJob struct {
	JobID         string         `json:"job_id"`
	State         JobState       `json:"state"`
	CreatedAt     string         `json:"created_at"`
	UpdatedAt     string         `json:"updated_at"`
	StatusMessage *string        `json:"status_message"`
	InputSummary  map[string]any `json:"input_summary,omitempty"`
	ArtifactRefs  []string       `json:"artifact_refs"`
	LogCursor     *string        `json:"log_cursor"`
	TerminalError *ErrorObject   `json:"terminal_error"`
	Links         map[string]any `json:"links"`
}

type JobClaim struct {
	WorkerID  string `json:"worker_id"`
	ClaimedAt string `json:"claimed_at"`
	ExpiresAt string `json:"expires_at"`
}

type CreateJobRequest struct {
	RequesterID  string         `json:"requester_id"`
	CapabilityID string         `json:"capability_id,omitempty"`
	InputSummary map[string]any `json:"input_summary,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

type JobClaimRequest struct {
	WorkerID     string `json:"worker_id"`
	LeaseSeconds int    `json:"lease_seconds,omitempty"`
}

type JobHeartbeatRequest struct {
	WorkerID      string `json:"worker_id"`
	TransitionTo  string `json:"transition_to,omitempty"`
	StatusMessage string `json:"status_message,omitempty"`
}

type JobCompleteRequest struct {
	WorkerID     string         `json:"worker_id"`
	ArtifactRefs []string       `json:"artifact_refs,omitempty"`
	Output       map[string]any `json:"output,omitempty"`
}

type JobFailRequest struct {
	WorkerID string      `json:"worker_id"`
	Error    ErrorObject `json:"error"`
}

type CancelRequest struct {
	RequesterID string `json:"requester_id,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type JobLogEntry struct {
	Timestamp string         `json:"timestamp"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Fields    map[string]any `json:"fields"`
}

type AppendJobLogRequest struct {
	WorkerID string        `json:"worker_id"`
	Entries  []JobLogEntry `json:"entries"`
}

type JobPolicyContext struct {
	ResourceKind   string `json:"resource_kind"`
	JobID          string `json:"job_id"`
	OwnerSubjectID string `json:"owner_subject_id"`
	RequesterID    string `json:"requester_id,omitempty"`
	JobState       string `json:"job_state"`
	PolicyState    string `json:"policy_state"`
}
