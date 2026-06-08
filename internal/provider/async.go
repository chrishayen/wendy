package provider

import (
	"context"
	"strings"

	"pacp/internal/contracts"
)

// AsyncCapabilityHandler adapts provider-local async acceptance into ordinary
// C01 provider output. It does not create PACP jobs or expose provider-run APIs.
type AsyncCapabilityHandler func(context.Context, contracts.ProviderInvokeRequest) (AcceptedHandle, error)

type AcceptedHandle struct {
	HandleID  string         `json:"handle_id"`
	Status    string         `json:"status"`
	StatusURL string         `json:"status_url,omitempty"`
	ExpiresAt string         `json:"expires_at,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

func AsyncHandler(handler AsyncCapabilityHandler) CapabilityHandler {
	return func(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
		handle, err := handler(ctx, req)
		if err != nil {
			return contracts.ProviderInvokeResponse{}, err
		}
		return contracts.ProviderInvokeResponse{Output: handle.Output()}, nil
	}
}

func (h AcceptedHandle) Output() map[string]any {
	output := map[string]any{}
	if handleID := strings.TrimSpace(h.HandleID); handleID != "" {
		output["handle_id"] = handleID
	}
	status := strings.TrimSpace(h.Status)
	if status == "" {
		status = "accepted"
	}
	output["status"] = status
	if statusURL := strings.TrimSpace(h.StatusURL); statusURL != "" {
		output["status_url"] = statusURL
	}
	if expiresAt := strings.TrimSpace(h.ExpiresAt); expiresAt != "" {
		output["expires_at"] = expiresAt
	}
	if len(h.Metadata) > 0 {
		output["metadata"] = h.Metadata
	}
	return output
}
