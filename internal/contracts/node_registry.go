package contracts

import (
	"fmt"
	"strings"
)

const (
	NodeTrustTrusted   = "trusted"
	NodeTrustUntrusted = "untrusted"
	NodeTrustDisabled  = "disabled"

	NodeStatusRegistered  = "registered"
	NodeStatusReachable   = "reachable"
	NodeStatusUnreachable = "unreachable"
	NodeStatusStale       = "stale"
)

type NodeRecord struct {
	NodeID         string         `json:"node_id"`
	URL            string         `json:"url"`
	DisplayName    string         `json:"display_name,omitempty"`
	TrustState     string         `json:"trust_state"`
	Status         string         `json:"status"`
	DisabledReason string         `json:"disabled_reason,omitempty"`
	LastSeenAt     string         `json:"last_seen_at,omitempty"`
	LastCheckedAt  string         `json:"last_checked_at,omitempty"`
	Tags           []string       `json:"tags,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	Links          map[string]any `json:"links"`
}

type RegisterNodeRequest struct {
	NodeID      string         `json:"node_id"`
	URL         string         `json:"url"`
	DisplayName string         `json:"display_name,omitempty"`
	TrustState  string         `json:"trust_state,omitempty"`
	Status      string         `json:"status,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type UpdateNodeTrustRequest struct {
	TrustState string `json:"trust_state"`
	Reason     string `json:"reason,omitempty"`
}

type NodeHeartbeatRequest struct {
	Status   string         `json:"status"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type NodeRecordList struct {
	Items      []NodeRecord `json:"items"`
	NextCursor *string      `json:"next_cursor,omitempty"`
}

func NodeRunnableBlockReason(record NodeRecord) string {
	if record.TrustState != NodeTrustTrusted {
		return fmt.Sprintf("node %s trust_state is %s", record.NodeID, record.TrustState)
	}
	switch record.Status {
	case NodeStatusUnreachable, NodeStatusStale:
		return fmt.Sprintf("node %s status is %s", record.NodeID, record.Status)
	}
	if strings.TrimSpace(record.URL) == "" {
		return fmt.Sprintf("node %s url is missing", record.NodeID)
	}
	return ""
}
