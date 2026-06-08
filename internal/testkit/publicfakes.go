package testkit

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"pacp/internal/contracts"
	"pacp/internal/provider"
)

type FakeComponentConfig struct {
	Kind         string
	Credential   string
	Behavior     FakeComponentBehavior
	ListItems    []any
	HealthStatus string
	Now          func() time.Time
	Samples      []contracts.MetricSample
}

type FakeComponentBehavior string

const (
	FakeComponentSuccess     FakeComponentBehavior = "success"
	FakeComponentDenied      FakeComponentBehavior = "denied"
	FakeComponentUnavailable FakeComponentBehavior = "unavailable"
)

type FakeProviderConfig struct {
	Endpoint   string
	Credential string
	Now        func() time.Time
}

type FakePolicyConfig struct {
	ComponentCredential string
	ValidCredential     string
	SubjectID           string
	Scopes              []string
	Decision            contracts.PolicyDecision
	Secrets             map[string]string
	Now                 func() time.Time
	Samples             []contracts.MetricSample
}

type FakeNodeConfig struct {
	Credential   string
	Behavior     FakeComponentBehavior
	NodeID       string
	HealthStatus string
	Resources    []contracts.NodeResource
	Services     []contracts.NodeService
	Now          func() time.Time
	Samples      []contracts.MetricSample
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
	if cfg.Behavior == "" {
		cfg.Behavior = FakeComponentSuccess
	}
	switch cfg.Behavior {
	case FakeComponentSuccess, FakeComponentDenied, FakeComponentUnavailable:
	default:
		return nil, errors.New("unsupported fake component behavior: " + string(cfg.Behavior))
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
		"cap_artifact": func(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
			prompt, _ := req.Input["prompt"].(string)
			body := []byte("fake artifact: " + prompt)
			return contracts.ProviderInvokeResponse{
				Output: map[string]any{
					"result": "artifact_created",
					"name":   "fake-artifact.txt",
				},
				Artifacts: []contracts.ProviderArtifact{{
					Name:          "fake-artifact.txt",
					MediaType:     "text/plain",
					ContentBase64: base64.StdEncoding.EncodeToString(body),
					Checksum:      checksumString(body),
				}},
			}, nil
		},
		"cap_async_accept": func(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
			return contracts.ProviderInvokeResponse{
				Output: map[string]any{"result": "accepted"},
			}, nil
		},
		"cap_fail": func(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
			return contracts.ProviderInvokeResponse{}, provider.InvokeError{
				ErrorObject: contracts.ErrorObject{
					Code:      "provider_unavailable",
					Message:   "fake provider failure",
					Retryable: true,
				},
				StatusCode: http.StatusServiceUnavailable,
			}
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

func NewFakePolicyHandler(cfg FakePolicyConfig) http.Handler {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.ValidCredential == "" {
		cfg.ValidCredential = "Bearer token_fake_policy"
	}
	cfg.ValidCredential = normalizeFakeCredential(cfg.ValidCredential)
	if cfg.SubjectID == "" {
		cfg.SubjectID = "sub_fake_policy"
	}
	if cfg.Scopes == nil {
		cfg.Scopes = []string{"component"}
	}
	if cfg.Decision.Reason == "" {
		if cfg.Decision.Allowed {
			cfg.Decision.Reason = "fake_allow"
		} else {
			cfg.Decision = contracts.PolicyDecision{Allowed: true, Reason: "fake_allow"}
		}
	}
	if cfg.Secrets == nil {
		cfg.Secrets = map[string]string{}
	}
	handler := &fakePolicyHandler{cfg: cfg}
	if cfg.ComponentCredential != "" {
		return requireFakeCredential(cfg.ComponentCredential, handler)
	}
	return handler
}

func NewFakeNodeHandler(cfg FakeNodeConfig) (http.Handler, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.NodeID == "" {
		cfg.NodeID = "node_fake"
	}
	if cfg.HealthStatus == "" {
		cfg.HealthStatus = "healthy"
	}
	if cfg.Behavior == "" {
		cfg.Behavior = FakeComponentSuccess
	}
	switch cfg.Behavior {
	case FakeComponentSuccess, FakeComponentDenied, FakeComponentUnavailable:
	default:
		return nil, errors.New("unsupported fake node behavior: " + string(cfg.Behavior))
	}
	resources := cfg.Resources
	if resources == nil {
		resources = fakeNodeResources()
	}
	services := cfg.Services
	if services == nil {
		services = fakeNodeServices()
	}
	handler := newFakeNodeHandler(cfg, resources, services)
	if cfg.Credential != "" {
		return requireFakeCredential(cfg.Credential, handler), nil
	}
	return handler, nil
}

type fakeComponentHandler struct {
	cfg      FakeComponentConfig
	contract componentContract
}

type fakePolicyHandler struct {
	cfg FakePolicyConfig
}

type fakeNodeHandler struct {
	mu           sync.Mutex
	cfg          FakeNodeConfig
	resources    []contracts.NodeResource
	services     map[string]contracts.NodeService
	serviceOrder []string
	idempotency  map[string]fakeNodeLifecycle
}

type fakeNodeLifecycle struct {
	operation string
	serviceID string
}

func newFakeNodeHandler(cfg FakeNodeConfig, resources []contracts.NodeResource, services []contracts.NodeService) *fakeNodeHandler {
	serviceMap := make(map[string]contracts.NodeService, len(services))
	serviceOrder := make([]string, 0, len(services))
	for _, service := range services {
		service = cloneFakeNodeService(service)
		if service.ServiceID == "" {
			continue
		}
		if service.Links == nil {
			service.Links = map[string]any{}
		}
		serviceMap[service.ServiceID] = service
		serviceOrder = append(serviceOrder, service.ServiceID)
	}
	return &fakeNodeHandler{
		cfg:          cfg,
		resources:    cloneFakeNodeResources(resources),
		services:     serviceMap,
		serviceOrder: serviceOrder,
		idempotency:  map[string]fakeNodeLifecycle{},
	}
}

func (h *fakeComponentHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodGet && path == h.contract.HealthPath:
		if h.writeBlocked(w, r) {
			return
		}
		health := contracts.NewComponentHealth(h.contract.Kind, nil)
		health.Status = h.cfg.HealthStatus
		health.CheckedAt = h.cfg.Now().UTC().Format(time.RFC3339)
		writeFakeSuccess(w, r, http.StatusOK, health)
	case r.Method == http.MethodGet && path == h.contract.MetricsPath:
		if h.writeBlocked(w, r) {
			return
		}
		metrics := contracts.NewComponentMetrics(h.contract.Kind, h.cfg.Samples)
		metrics.CollectedAt = h.cfg.Now().UTC().Format(time.RFC3339)
		writeFakeSuccess(w, r, http.StatusOK, metrics)
	default:
		for _, check := range h.contract.ListChecks {
			if r.Method == http.MethodGet && path == listPathOnly(check.Path) {
				if h.writeBlocked(w, r) {
					return
				}
				writeFakeSuccess(w, r, http.StatusOK, fakeListPayload(h.contract.Kind, h.cfg.ListItems))
				return
			}
		}
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake component route not found", false)
	}
}

func (h *fakePolicyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodGet && path == "/v1/policy/health":
		health := contracts.NewComponentHealth("policy", map[string]any{"fake": true})
		health.CheckedAt = h.cfg.Now().UTC().Format(time.RFC3339)
		writeFakeSuccess(w, r, http.StatusOK, health)
	case r.Method == http.MethodGet && path == "/v1/policy/metrics":
		metrics := contracts.NewComponentMetrics("policy", h.cfg.Samples)
		metrics.CollectedAt = h.cfg.Now().UTC().Format(time.RFC3339)
		writeFakeSuccess(w, r, http.StatusOK, metrics)
	case r.Method == http.MethodPost && path == "/v1/auth/verify":
		var req contracts.VerifyCredentialRequest
		if !decodeFakeBody(w, r, &req) {
			return
		}
		if normalizeFakeCredential(req.Credential) != h.cfg.ValidCredential {
			writeFakeSuccess(w, r, http.StatusOK, contracts.CredentialVerification{Valid: false, Scopes: []string{}})
			return
		}
		subjectID := h.cfg.SubjectID
		writeFakeSuccess(w, r, http.StatusOK, contracts.CredentialVerification{
			Valid:     true,
			SubjectID: &subjectID,
			Scopes:    append([]string(nil), h.cfg.Scopes...),
		})
	case r.Method == http.MethodPost && path == "/v1/policy/check":
		var req contracts.PolicyCheckRequest
		if !decodeFakeBody(w, r, &req) {
			return
		}
		if req.SubjectID == "" || req.Action == "" {
			writeFakeError(w, r, http.StatusBadRequest, "validation_failed", "subject_id and action are required", false)
			return
		}
		writeFakeSuccess(w, r, http.StatusOK, h.cfg.Decision)
	case r.Method == http.MethodPost && path == "/v1/secrets/resolve":
		var req contracts.ResolveSecretRequest
		if !decodeFakeBody(w, r, &req) {
			return
		}
		if req.SecretRef == "" || req.SubjectID == "" {
			writeFakeError(w, r, http.StatusBadRequest, "validation_failed", "secret_ref and subject_id are required", false)
			return
		}
		value, ok := h.cfg.Secrets[req.SecretRef]
		if !ok {
			writeFakeError(w, r, http.StatusNotFound, "not_found", "fake secret not found", false)
			return
		}
		if req.SubjectID != h.cfg.SubjectID {
			writeFakeError(w, r, http.StatusForbidden, "forbidden", "fake subject is not authorized for this secret", false)
			return
		}
		writeFakeSuccess(w, r, http.StatusOK, contracts.ResolvedSecret{SecretRef: req.SecretRef, Value: value})
	case r.Method == http.MethodPost && path == "/v1/redact":
		var req contracts.RedactRequest
		if !decodeFakeBody(w, r, &req) {
			return
		}
		text := req.Text
		for _, value := range h.cfg.Secrets {
			if value != "" {
				text = strings.ReplaceAll(text, value, "[REDACTED]")
			}
		}
		writeFakeSuccess(w, r, http.StatusOK, contracts.RedactResponse{Text: text})
	default:
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake policy route not found", false)
	}
}

func (h *fakeNodeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.writeBlocked(w, r) {
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodGet && path == "/v1/node/health":
		writeFakeSuccess(w, r, http.StatusOK, contracts.NodeHealth{
			Status:    h.cfg.HealthStatus,
			Version:   "v1",
			CheckedAt: h.cfg.Now().UTC().Format(time.RFC3339),
			Details: map[string]any{
				"component": "node",
				"fake":      true,
				"node_id":   h.cfg.NodeID,
			},
		})
	case r.Method == http.MethodGet && path == "/v1/node/metrics":
		samples := h.cfg.Samples
		if samples == nil {
			samples = h.metricsSamples()
		}
		metrics := contracts.NewComponentMetrics("node", samples)
		metrics.CollectedAt = h.cfg.Now().UTC().Format(time.RFC3339)
		writeFakeSuccess(w, r, http.StatusOK, metrics)
	case r.Method == http.MethodGet && path == "/v1/node/resources":
		h.mu.Lock()
		resources := cloneFakeNodeResources(h.resources)
		h.mu.Unlock()
		writeFakeSuccess(w, r, http.StatusOK, map[string]any{"items": resources, "next_cursor": nil})
	case r.Method == http.MethodGet && path == "/v1/node/services":
		writeFakeSuccess(w, r, http.StatusOK, map[string]any{"items": h.listServices(), "next_cursor": nil})
	case strings.HasPrefix(path, "/v1/node/services/"):
		h.serviceRoute(w, r, strings.TrimPrefix(path, "/v1/node/services/"))
	default:
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake node route not found", false)
	}
}

func (h *fakeNodeHandler) serviceRoute(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake node service route not found", false)
		return
	}
	serviceID, err := url.PathUnescape(parts[0])
	if err != nil {
		writeFakeError(w, r, http.StatusBadRequest, "validation_failed", "service_id is invalid", false)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		service, ok := h.getService(serviceID)
		if !ok {
			writeFakeError(w, r, http.StatusNotFound, "not_found", "fake node service not found", false)
			return
		}
		writeFakeSuccess(w, r, http.StatusOK, service)
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake node service route not found", false)
		return
	}
	switch parts[1] {
	case "start":
		h.lifecycle(w, r, serviceID, "start")
	case "stop":
		h.lifecycle(w, r, serviceID, "stop")
	default:
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake node service route not found", false)
	}
}

func (h *fakeNodeHandler) lifecycle(w http.ResponseWriter, r *http.Request, serviceID, operation string) {
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		writeFakeError(w, r, http.StatusBadRequest, "missing_idempotency_key", "Idempotency-Key header is required for node service lifecycle operations", false)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	service, ok := h.services[serviceID]
	if !ok {
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake node service not found", false)
		return
	}
	if existing, ok := h.idempotency[idempotencyKey]; ok {
		if existing.operation != operation || existing.serviceID != serviceID {
			writeFakeError(w, r, http.StatusConflict, "idempotency_conflict", "idempotency key was reused with different node lifecycle content", false)
			return
		}
		writeFakeSuccess(w, r, http.StatusOK, cloneFakeNodeService(service))
		return
	}
	status := http.StatusAccepted
	switch operation {
	case "start":
		if service.Status == "running" {
			status = http.StatusOK
		} else {
			service.Status = "starting"
		}
	case "stop":
		service.Status = "stopped"
	}
	h.services[serviceID] = service
	h.idempotency[idempotencyKey] = fakeNodeLifecycle{operation: operation, serviceID: serviceID}
	writeFakeSuccess(w, r, status, cloneFakeNodeService(service))
}

func (h *fakeNodeHandler) listServices() []contracts.NodeService {
	h.mu.Lock()
	defer h.mu.Unlock()
	services := make([]contracts.NodeService, 0, len(h.serviceOrder))
	for _, serviceID := range h.serviceOrder {
		if service, ok := h.services[serviceID]; ok {
			services = append(services, cloneFakeNodeService(service))
		}
	}
	return services
}

func (h *fakeNodeHandler) getService(serviceID string) (contracts.NodeService, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	service, ok := h.services[serviceID]
	if !ok {
		return contracts.NodeService{}, false
	}
	return cloneFakeNodeService(service), true
}

func (h *fakeNodeHandler) metricsSamples() []contracts.MetricSample {
	h.mu.Lock()
	defer h.mu.Unlock()
	counts := map[string]int{}
	for _, service := range h.services {
		counts[service.Status]++
	}
	samples := []contracts.MetricSample{
		contracts.CountMetric("node_services_total", len(h.services), map[string]string{"node_id": h.cfg.NodeID}),
	}
	for status, count := range counts {
		samples = append(samples, contracts.CountMetric("node_services_by_status", count, map[string]string{"node_id": h.cfg.NodeID, "status": status}))
	}
	return samples
}

func (h *fakeNodeHandler) writeBlocked(w http.ResponseWriter, r *http.Request) bool {
	switch h.cfg.Behavior {
	case FakeComponentDenied:
		writeFakeError(w, r, http.StatusForbidden, "forbidden", "fake node access denied", false)
		return true
	case FakeComponentUnavailable:
		writeFakeError(w, r, http.StatusServiceUnavailable, "component_unavailable", "fake node unavailable", true)
		return true
	default:
		return false
	}
}

func (h *fakeComponentHandler) writeBlocked(w http.ResponseWriter, r *http.Request) bool {
	switch h.cfg.Behavior {
	case FakeComponentDenied:
		writeFakeError(w, r, http.StatusForbidden, "forbidden", "fake component access denied", false)
		return true
	case FakeComponentUnavailable:
		writeFakeError(w, r, http.StatusServiceUnavailable, "component_unavailable", "fake component unavailable", true)
		return true
	default:
		return false
	}
}

func fakeNodeResources() []contracts.NodeResource {
	return []contracts.NodeResource{{
		ResourceID: "res_fake_gpu",
		Tags:       []string{"gpu", "gpu:0"},
		Metadata:   map[string]any{"kind": "gpu"},
	}}
}

func fakeNodeServices() []contracts.NodeService {
	return []contracts.NodeService{
		fakeNodeService("svc_fake_running", "running"),
		fakeNodeService("svc_fake_stopped", "stopped"),
		fakeNodeService("svc_fake_starting", "starting"),
		fakeNodeService("svc_fake_failed", "failed"),
	}
}

func fakeNodeService(serviceID, status string) contracts.NodeService {
	return contracts.NodeService{
		ServiceID:        serviceID,
		Status:           status,
		RuntimeAdapter:   "fake",
		ProviderEndpoint: "http://node.fake/providers/" + serviceID,
		Links:            map[string]any{},
	}
}

func cloneFakeNodeResources(resources []contracts.NodeResource) []contracts.NodeResource {
	cloned := make([]contracts.NodeResource, len(resources))
	for i, resource := range resources {
		cloned[i] = resource
		cloned[i].Tags = append([]string(nil), resource.Tags...)
		cloned[i].Metadata = cloneMap(resource.Metadata)
	}
	return cloned
}

func cloneFakeNodeService(service contracts.NodeService) contracts.NodeService {
	service.Links = cloneMap(service.Links)
	if service.Manifest != nil {
		manifest := *service.Manifest
		service.Manifest = &manifest
	}
	return service
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func decodeFakeBody(w http.ResponseWriter, r *http.Request, out any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		writeFakeError(w, r, http.StatusBadRequest, "validation_failed", "request body is invalid JSON", false)
		return false
	}
	return true
}

func fakeListPayload(kind string, override []any) map[string]any {
	items := override
	if items == nil {
		items = fakeListItems(kind)
	}
	return map[string]any{
		"items":       items,
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
		}, {
			ID:            "cap_artifact",
			Name:          "Artifact",
			Description:   "Returns a provider artifact with public metadata.",
			ExecutionMode: "sync",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []any{"prompt"},
				"properties": map[string]any{
					"prompt": map[string]any{"type": "string"},
				},
			},
			OutputSchema: map[string]any{
				"type":     "object",
				"required": []any{"result", "name"},
				"properties": map[string]any{
					"result": map[string]any{"type": "string"},
					"name":   map[string]any{"type": "string"},
				},
			},
			Examples:      []map[string]any{},
			SideEffects:   "writes artifact output",
			ArtifactHints: []contracts.ArtifactHint{{MediaType: "text/plain", Count: "one"}},
			TimeoutHint:   "30s",
		}, {
			ID:            "cap_async_accept",
			Name:          "Async Accept",
			Description:   "Represents an async provider acceptance path for contract tests.",
			ExecutionMode: "async",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			OutputSchema: map[string]any{
				"type":     "object",
				"required": []any{"result"},
				"properties": map[string]any{
					"result": map[string]any{"type": "string"},
				},
			},
			Examples:    []map[string]any{},
			SideEffects: "none",
			TimeoutHint: "30s",
		}, {
			ID:            "cap_fail",
			Name:          "Failure",
			Description:   "Returns a normalized provider failure envelope.",
			ExecutionMode: "sync",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			OutputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			Examples:    []map[string]any{},
			SideEffects: "none",
			TimeoutHint: "30s",
		}},
	}
}

func checksumString(body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf("sha256:%x", sum)
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
