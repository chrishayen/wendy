package testkit

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"pacp/internal/contracts"
	"pacp/internal/provider"
)

type FakeComponentConfig struct {
	Kind         string
	Credential   string
	HealthStatus string
	Now          func() time.Time
	Samples      []contracts.MetricSample
}

type FakeProviderConfig struct {
	Endpoint   string
	Credential string
	Now        func() time.Time
}

func NewFakeComponentHandler(cfg FakeComponentConfig) (http.Handler, error) {
	contract, ok := componentContractFor(strings.TrimSpace(cfg.Kind))
	if !ok {
		return nil, errors.New("unsupported fake component kind: " + cfg.Kind)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.HealthStatus == "" {
		cfg.HealthStatus = "healthy"
	}
	handler := &fakeComponentHandler{
		cfg:      cfg,
		contract: contract,
	}
	if cfg.Credential != "" {
		return requireFakeCredential(cfg.Credential, handler), nil
	}
	return handler, nil
}

func NewFakeProviderHandler(cfg FakeProviderConfig) (http.Handler, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	if endpoint == "" {
		endpoint = "http://provider.fake"
	}
	server, err := provider.NewServer(fakeProviderManifest(endpoint), map[string]provider.CapabilityHandler{
		"cap_echo": func(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
			message, _ := req.Input["message"].(string)
			return contracts.ProviderInvokeResponse{Output: map[string]any{"reply": message}}, nil
		},
	})
	if err != nil {
		return nil, err
	}
	if cfg.Now != nil {
		server.SetClock(cfg.Now)
	}
	if cfg.Credential != "" {
		return requireFakeCredential(cfg.Credential, server), nil
	}
	return server, nil
}

type fakeComponentHandler struct {
	cfg      FakeComponentConfig
	contract componentContract
}

func (h *fakeComponentHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodGet && path == h.contract.HealthPath:
		health := contracts.NewComponentHealth(h.contract.Kind, nil)
		health.Status = h.cfg.HealthStatus
		health.CheckedAt = h.cfg.Now().UTC().Format(time.RFC3339)
		writeFakeSuccess(w, r, http.StatusOK, health)
	case r.Method == http.MethodGet && path == h.contract.MetricsPath:
		metrics := contracts.NewComponentMetrics(h.contract.Kind, h.cfg.Samples)
		metrics.CollectedAt = h.cfg.Now().UTC().Format(time.RFC3339)
		writeFakeSuccess(w, r, http.StatusOK, metrics)
	default:
		for _, check := range h.contract.ListChecks {
			if r.Method == http.MethodGet && path == listPathOnly(check.Path) {
				writeFakeSuccess(w, r, http.StatusOK, fakeListPayload(h.contract.Kind))
				return
			}
		}
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake component route not found", false)
	}
}

func fakeListPayload(kind string) map[string]any {
	return map[string]any{
		"items":       fakeListItems(kind),
		"next_cursor": nil,
	}
}

func fakeListItems(kind string) []any {
	now := "2026-06-08T00:00:00Z"
	switch kind {
	case "artifacts":
		return []any{contracts.Artifact{
			ArtifactID:     "art_fake_001",
			Name:           "fake-output.txt",
			MediaType:      "text/plain",
			Size:           11,
			Checksum:       "sha256:fake",
			CreatedAt:      now,
			ProducerRef:    "job_fake_001",
			OwnerSubjectID: "sub_fake_agent",
			Links:          map[string]any{},
		}}
	case "catalog":
		capability := fakeProviderManifest("http://provider.fake").Capabilities[0]
		capability.ServiceID = "svc_fake_provider"
		return []any{contracts.CatalogCapabilityRecord{
			Capability: capability,
			Route: contracts.CapabilityRoute{
				CapabilityID:       capability.ID,
				ServiceID:          "svc_fake_provider",
				ProviderEndpoint:   "http://provider.fake",
				ProviderHealthPath: "/v1/provider/health",
				ProviderInvokePath: "/v1/provider/capabilities/cap_echo/invoke",
			},
			Service: fakeProviderManifest("http://provider.fake").Service,
		}}
	case "jobs":
		return []any{contracts.Job{
			JobID:        "job_fake_001",
			State:        contracts.JobQueued,
			CreatedAt:    now,
			UpdatedAt:    now,
			ArtifactRefs: []string{},
			Links:        map[string]any{},
		}}
	case "leases":
		return []any{contracts.ResourceRecord{
			ResourceID: "res_fake_gpu",
			Selector:   "gpu",
			Status:     contracts.ResourceAvailable,
			NodeID:     "node_fake",
			Links:      map[string]any{},
		}}
	case "node":
		return []any{contracts.NodeResource{
			ResourceID: "res_fake_gpu",
			Tags:       []string{"gpu"},
		}}
	default:
		return []any{}
	}
}

func listPathOnly(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Path == "" {
		return raw
	}
	return strings.TrimSuffix(parsed.Path, "/")
}

func fakeProviderManifest(endpoint string) contracts.ProviderManifest {
	return contracts.ProviderManifest{
		SchemaVersion: "v1",
		Service: contracts.Service{
			ID:           "svc_fake_provider",
			Name:         "Fake Provider",
			Description:  "Reusable provider fake for isolated contract tests.",
			Version:      "v1",
			ProviderKind: "fake",
			Tags:         []string{"fake", "testkit"},
		},
		Provider: contracts.Provider{
			Endpoint:   endpoint,
			HealthPath: "/v1/provider/health",
		},
		Capabilities: []contracts.Capability{{
			ID:            "cap_echo",
			Name:          "Echo",
			Description:   "Echoes a message for contract tests.",
			ExecutionMode: "sync",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []any{"message"},
				"properties": map[string]any{
					"message": map[string]any{"type": "string"},
				},
			},
			OutputSchema: map[string]any{
				"type":     "object",
				"required": []any{"reply"},
				"properties": map[string]any{
					"reply": map[string]any{"type": "string"},
				},
			},
			Examples:    []map[string]any{},
			SideEffects: "none",
			TimeoutHint: "30s",
		}},
	}
}

func requireFakeCredential(credential string, next http.Handler) http.Handler {
	want := normalizeFakeCredential(credential)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if normalizeFakeCredential(r.Header.Get("Authorization")) != want {
			writeFakeError(w, r, http.StatusUnauthorized, "unauthorized", "fake API credential required", false)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func normalizeFakeCredential(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "Bearer ") {
		return value
	}
	return "Bearer " + value
}

func writeFakeSuccess(w http.ResponseWriter, r *http.Request, status int, data any) {
	writeJSON(w, status, map[string]any{
		"ok":    true,
		"data":  data,
		"links": map[string]any{},
		"meta":  fakeMeta(r),
	})
}

func writeFakeError(w http.ResponseWriter, r *http.Request, status int, code, message string, retryable bool) {
	writeJSON(w, status, map[string]any{
		"ok":    false,
		"error": map[string]any{"code": code, "message": message, "retryable": retryable},
		"links": map[string]any{},
		"meta":  fakeMeta(r),
	})
}

func fakeMeta(r *http.Request) map[string]string {
	requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
	if requestID == "" {
		requestID = "req_fake"
	}
	return map[string]string{"request_id": requestID, "schema_version": "v1"}
}
