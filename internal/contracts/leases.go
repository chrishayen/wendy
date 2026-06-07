package contracts

type ResourceStatus string

const (
	ResourceAvailable   ResourceStatus = "available"
	ResourceUnavailable ResourceStatus = "unavailable"
)

type ResourceRecord struct {
	ResourceID  string         `json:"resource_id"`
	Selector    string         `json:"selector"`
	DisplayName string         `json:"display_name,omitempty"`
	Status      ResourceStatus `json:"status"`
	NodeID      string         `json:"node_id,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Links       map[string]any `json:"links,omitempty"`
}

type RegisterResourceRequest struct {
	ResourceID  string         `json:"resource_id,omitempty"`
	Selector    string         `json:"selector"`
	DisplayName string         `json:"display_name,omitempty"`
	Status      ResourceStatus `json:"status,omitempty"`
	NodeID      string         `json:"node_id,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type LeaseRequestState string

const (
	LeaseRequestPending  LeaseRequestState = "pending"
	LeaseRequestGranted  LeaseRequestState = "granted"
	LeaseRequestCanceled LeaseRequestState = "canceled"
	LeaseRequestExpired  LeaseRequestState = "expired"
)

type LeaseRequest struct {
	RequestID        string            `json:"request_id"`
	State            LeaseRequestState `json:"state"`
	RequesterID      string            `json:"requester_id,omitempty"`
	ResourceSelector string            `json:"resource_selector"`
	QueuePosition    *int              `json:"queue_position"`
	Lease            *Lease            `json:"lease"`
	CreatedAt        string            `json:"created_at"`
	UpdatedAt        string            `json:"updated_at"`
	Links            map[string]any    `json:"links"`
}

type Lease struct {
	LeaseID       string         `json:"lease_id"`
	ResourceID    string         `json:"resource_id"`
	HolderID      string         `json:"holder_id"`
	ExpiresAt     string         `json:"expires_at"`
	ReleasedAt    string         `json:"released_at,omitempty"`
	ReleasedBy    string         `json:"released_by,omitempty"`
	ReleaseReason string         `json:"release_reason,omitempty"`
	Links         map[string]any `json:"links"`
}

type CreateLeaseRequest struct {
	RequesterID             string `json:"requester_id"`
	ResourceSelector        string `json:"resource_selector"`
	Priority                int    `json:"priority,omitempty"`
	HeartbeatTimeoutSeconds int    `json:"heartbeat_timeout_seconds,omitempty"`
}

type LeaseHeartbeatRequest struct {
	HolderID string `json:"holder_id"`
}

type LeaseReleaseRequest struct {
	HolderID string `json:"holder_id"`
	Reason   string `json:"reason,omitempty"`
}

type ResourceInspection struct {
	Resource    ResourceRecord     `json:"resource"`
	ActiveLease *Lease             `json:"active_lease"`
	QueueLength int                `json:"queue_length"`
	Queue       []LeaseQueueRecord `json:"queue"`
}

type LeaseQueueRecord struct {
	RequestID     string `json:"request_id"`
	RequesterID   string `json:"requester_id,omitempty"`
	Priority      int    `json:"priority"`
	QueuePosition int    `json:"queue_position"`
}

type LeaseAuditEvent struct {
	EventType      string `json:"event_type"`
	LeaseID        string `json:"lease_id"`
	HolderID       string `json:"holder_id"`
	ActorSubjectID string `json:"actor_subject_id"`
	OccurredAt     string `json:"occurred_at"`
}
