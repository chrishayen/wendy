package contracts

import "time"

type ComponentHealth struct {
	Status    string         `json:"status"`
	Version   string         `json:"version"`
	CheckedAt string         `json:"checked_at"`
	Details   map[string]any `json:"details,omitempty"`
}

func NewComponentHealth(component string, details map[string]any) ComponentHealth {
	if details == nil {
		details = map[string]any{}
	}
	details["component"] = component
	return ComponentHealth{
		Status:    "healthy",
		Version:   "v1",
		CheckedAt: time.Now().UTC().Format(time.RFC3339),
		Details:   details,
	}
}
