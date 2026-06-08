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

type FakeJobsConfig struct {
	Credential   string
	Behavior     FakeComponentBehavior
	HealthStatus string
	Jobs         []contracts.Job
	Logs         map[string][]contracts.JobLogEntry
	Now          func() time.Time
	Samples      []contracts.MetricSample
}

type FakeLeasesConfig struct {
	Credential    string
	Behavior      FakeComponentBehavior
	HealthStatus  string
	Resources     []contracts.ResourceRecord
	LeaseRequests []contracts.LeaseRequest
	Leases        []contracts.Lease
	Now           func() time.Time
	Samples       []contracts.MetricSample
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

func NewFakeJobsHandler(cfg FakeJobsConfig) (http.Handler, error) {
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
		return nil, errors.New("unsupported fake jobs behavior: " + string(cfg.Behavior))
	}
	jobs := cfg.Jobs
	if jobs == nil {
		jobs = fakeJobs()
	}
	handler := newFakeJobsHandler(cfg, jobs)
	if cfg.Credential != "" {
		return requireFakeCredential(cfg.Credential, handler), nil
	}
	return handler, nil
}

func NewFakeLeasesHandler(cfg FakeLeasesConfig) (http.Handler, error) {
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
		return nil, errors.New("unsupported fake leases behavior: " + string(cfg.Behavior))
	}
	resources := cfg.Resources
	if resources == nil {
		resources = fakeLeaseResources()
	}
	leases := cfg.Leases
	if leases == nil {
		leases = fakeLeases()
	}
	requests := cfg.LeaseRequests
	if requests == nil {
		requests = fakeLeaseRequests(leases)
	}
	handler := newFakeLeasesHandler(cfg, resources, requests, leases)
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

type fakeJobsHandler struct {
	mu          sync.Mutex
	cfg         FakeJobsConfig
	jobs        map[string]contracts.Job
	jobOrder    []string
	logs        map[string][]contracts.JobLogEntry
	idempotency map[string]fakeJobIdempotency
	nextID      int
}

type fakeJobIdempotency struct {
	operation   string
	jobID       string
	fingerprint string
}

type fakeLeasesHandler struct {
	mu              sync.Mutex
	cfg             FakeLeasesConfig
	resources       map[string]contracts.ResourceRecord
	resourceOrder   []string
	requests        map[string]contracts.LeaseRequest
	requestOrder    []string
	leases          map[string]contracts.Lease
	idempotency     map[string]fakeLeaseIdempotency
	nextRequestID   int
	nextLeaseID     int
	releasedLeases  map[string]bool
	requestByLease  map[string]string
	selectorByLease map[string]string
}

type fakeLeaseIdempotency struct {
	operation   string
	leaseID     string
	fingerprint string
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

func newFakeJobsHandler(cfg FakeJobsConfig, jobs []contracts.Job) *fakeJobsHandler {
	jobMap := make(map[string]contracts.Job, len(jobs))
	jobOrder := make([]string, 0, len(jobs))
	for _, job := range jobs {
		job = cloneFakeJob(job)
		if job.JobID == "" {
			continue
		}
		jobMap[job.JobID] = job
		jobOrder = append(jobOrder, job.JobID)
	}
	logs := map[string][]contracts.JobLogEntry{}
	if cfg.Logs != nil {
		for jobID, entries := range cfg.Logs {
			logs[jobID] = append([]contracts.JobLogEntry(nil), entries...)
		}
	}
	for _, jobID := range jobOrder {
		if _, ok := logs[jobID]; !ok {
			logs[jobID] = fakeJobLogs(jobID)
		}
	}
	return &fakeJobsHandler{
		cfg:         cfg,
		jobs:        jobMap,
		jobOrder:    jobOrder,
		logs:        logs,
		idempotency: map[string]fakeJobIdempotency{},
		nextID:      len(jobOrder) + 1,
	}
}

func newFakeLeasesHandler(cfg FakeLeasesConfig, resources []contracts.ResourceRecord, requests []contracts.LeaseRequest, leases []contracts.Lease) *fakeLeasesHandler {
	resourceMap := make(map[string]contracts.ResourceRecord, len(resources))
	resourceOrder := make([]string, 0, len(resources))
	for _, resource := range resources {
		resource = cloneFakeResource(resource)
		if resource.ResourceID == "" {
			continue
		}
		resourceMap[resource.ResourceID] = resource
		resourceOrder = append(resourceOrder, resource.ResourceID)
	}
	leaseMap := make(map[string]contracts.Lease, len(leases))
	for _, lease := range leases {
		lease = cloneFakeLease(lease)
		if lease.LeaseID == "" {
			continue
		}
		leaseMap[lease.LeaseID] = lease
	}
	requestMap := make(map[string]contracts.LeaseRequest, len(requests))
	requestOrder := make([]string, 0, len(requests))
	requestByLease := map[string]string{}
	selectorByLease := map[string]string{}
	for _, request := range requests {
		request = cloneFakeLeaseRequest(request)
		if request.RequestID == "" {
			continue
		}
		requestMap[request.RequestID] = request
		requestOrder = append(requestOrder, request.RequestID)
		if request.Lease != nil {
			requestByLease[request.Lease.LeaseID] = request.RequestID
			selectorByLease[request.Lease.LeaseID] = request.ResourceSelector
		}
	}
	return &fakeLeasesHandler{
		cfg:             cfg,
		resources:       resourceMap,
		resourceOrder:   resourceOrder,
		requests:        requestMap,
		requestOrder:    requestOrder,
		leases:          leaseMap,
		idempotency:     map[string]fakeLeaseIdempotency{},
		nextRequestID:   len(requestOrder) + 1,
		nextLeaseID:     len(leaseMap) + 1,
		releasedLeases:  map[string]bool{},
		requestByLease:  requestByLease,
		selectorByLease: selectorByLease,
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

func (h *fakeJobsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.writeBlocked(w, r) {
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodGet && path == "/v1/jobs/health":
		health := contracts.NewComponentHealth("jobs", map[string]any{"fake": true})
		health.Status = h.cfg.HealthStatus
		health.CheckedAt = h.cfg.Now().UTC().Format(time.RFC3339)
		writeFakeSuccess(w, r, http.StatusOK, health)
	case r.Method == http.MethodGet && path == "/v1/jobs/metrics":
		samples := h.cfg.Samples
		if samples == nil {
			samples = h.metricsSamples()
		}
		metrics := contracts.NewComponentMetrics("jobs", samples)
		metrics.CollectedAt = h.cfg.Now().UTC().Format(time.RFC3339)
		writeFakeSuccess(w, r, http.StatusOK, metrics)
	case r.Method == http.MethodGet && path == "/v1/jobs":
		writeFakeSuccess(w, r, http.StatusOK, map[string]any{"items": h.listJobs(contracts.JobState(r.URL.Query().Get("state"))), "next_cursor": nil})
	case r.Method == http.MethodPost && path == "/v1/jobs":
		h.createJob(w, r)
	case strings.HasPrefix(path, "/v1/jobs/"):
		h.jobRoute(w, r, strings.TrimPrefix(path, "/v1/jobs/"))
	default:
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake jobs route not found", false)
	}
}

func (h *fakeJobsHandler) jobRoute(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake job route not found", false)
		return
	}
	jobID, err := url.PathUnescape(parts[0])
	if err != nil {
		writeFakeError(w, r, http.StatusBadRequest, "validation_failed", "job_id is invalid", false)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		job, ok := h.getJob(jobID)
		if !ok {
			writeFakeError(w, r, http.StatusNotFound, "not_found", "fake job not found", false)
			return
		}
		writeFakeSuccess(w, r, http.StatusOK, job)
		return
	}
	if len(parts) != 2 {
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake job route not found", false)
		return
	}
	switch parts[1] {
	case "policy-context":
		if r.Method != http.MethodGet {
			writeFakeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
			return
		}
		h.writePolicyContext(w, r, jobID)
	case "agent-projection":
		if r.Method != http.MethodGet {
			writeFakeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
			return
		}
		h.writeAgentProjection(w, r, jobID)
	case "claim":
		h.claimJob(w, r, jobID)
	case "heartbeat":
		h.heartbeatJob(w, r, jobID)
	case "complete":
		h.completeJob(w, r, jobID)
	case "fail":
		h.failJob(w, r, jobID)
	case "cancel":
		h.cancelJob(w, r, jobID)
	case "logs":
		if r.Method == http.MethodGet {
			h.readLogs(w, r, jobID)
			return
		}
		h.appendLogs(w, r, jobID)
	default:
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake job route not found", false)
	}
}

func (h *fakeJobsHandler) createJob(w http.ResponseWriter, r *http.Request) {
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		writeFakeError(w, r, http.StatusBadRequest, "missing_idempotency_key", "Idempotency-Key header is required for this job operation", false)
		return
	}
	var req contracts.CreateJobRequest
	if !decodeFakeBody(w, r, &req) {
		return
	}
	fingerprint := fakeJSONFingerprint("create", req)
	h.mu.Lock()
	defer h.mu.Unlock()
	if existing, ok := h.idempotency[idempotencyKey]; ok {
		if existing.operation != "create" || existing.fingerprint != fingerprint {
			writeFakeError(w, r, http.StatusConflict, "idempotency_conflict", "idempotency key was reused with different request content", false)
			return
		}
		job := h.jobs[existing.jobID]
		writeFakeSuccess(w, r, http.StatusOK, cloneFakeJob(job))
		return
	}
	now := h.cfg.Now().UTC().Format(time.RFC3339)
	jobID := fmt.Sprintf("job_fake_created_%03d", h.nextID)
	h.nextID++
	job := contracts.Job{
		JobID:        jobID,
		State:        contracts.JobQueued,
		CreatedAt:    now,
		UpdatedAt:    now,
		InputSummary: req.InputSummary,
		Metadata:     req.Metadata,
		ArtifactRefs: []string{},
		Links:        fakeJobLinks(jobID),
	}
	h.jobs[jobID] = job
	h.jobOrder = append(h.jobOrder, jobID)
	h.logs[jobID] = []contracts.JobLogEntry{}
	h.idempotency[idempotencyKey] = fakeJobIdempotency{operation: "create", jobID: jobID, fingerprint: fingerprint}
	writeFakeSuccess(w, r, http.StatusCreated, cloneFakeJob(job))
}

func (h *fakeJobsHandler) claimJob(w http.ResponseWriter, r *http.Request, jobID string) {
	var req contracts.JobClaimRequest
	if !decodeFakeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.WorkerID) == "" {
		writeFakeError(w, r, http.StatusBadRequest, "validation_failed", "worker_id is required", false)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	job, ok := h.jobs[jobID]
	if !ok {
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake job not found", false)
		return
	}
	if isFakeJobTerminal(job.State) {
		writeFakeError(w, r, http.StatusConflict, "job_terminal", "job is already terminal", false)
		return
	}
	now := h.cfg.Now().UTC()
	leaseSeconds := req.LeaseSeconds
	if leaseSeconds <= 0 {
		leaseSeconds = 60
	}
	job.State = contracts.JobClaimed
	job.UpdatedAt = now.Format(time.RFC3339)
	job.Claim = &contracts.JobClaim{
		WorkerID:  req.WorkerID,
		ClaimedAt: now.Format(time.RFC3339),
		ExpiresAt: now.Add(time.Duration(leaseSeconds) * time.Second).Format(time.RFC3339),
	}
	h.jobs[jobID] = job
	writeFakeSuccess(w, r, http.StatusOK, cloneFakeJob(job))
}

func (h *fakeJobsHandler) heartbeatJob(w http.ResponseWriter, r *http.Request, jobID string) {
	var req contracts.JobHeartbeatRequest
	if !decodeFakeBody(w, r, &req) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	job, ok := h.jobs[jobID]
	if !ok {
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake job not found", false)
		return
	}
	if !fakeJobWorkerMatches(job, req.WorkerID) {
		writeFakeError(w, r, http.StatusForbidden, "forbidden", "worker_id does not match the active job claim", false)
		return
	}
	if req.TransitionTo != "" {
		if req.TransitionTo != string(contracts.JobRunning) {
			writeFakeError(w, r, http.StatusBadRequest, "validation_failed", "transition_to must be running when heartbeat changes state", false)
			return
		}
		job.State = contracts.JobRunning
	}
	if req.StatusMessage != "" {
		job.StatusMessage = req.StatusMessage
	}
	job.UpdatedAt = h.cfg.Now().UTC().Format(time.RFC3339)
	h.jobs[jobID] = job
	writeFakeSuccess(w, r, http.StatusOK, cloneFakeJob(job))
}

func (h *fakeJobsHandler) completeJob(w http.ResponseWriter, r *http.Request, jobID string) {
	var req contracts.JobCompleteRequest
	if !decodeFakeBody(w, r, &req) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	job, ok := h.jobs[jobID]
	if !ok {
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake job not found", false)
		return
	}
	if !fakeJobWorkerMatches(job, req.WorkerID) {
		writeFakeError(w, r, http.StatusForbidden, "forbidden", "worker_id does not match the active job claim", false)
		return
	}
	job.State = contracts.JobSucceeded
	job.ArtifactRefs = append([]string(nil), req.ArtifactRefs...)
	job.UpdatedAt = h.cfg.Now().UTC().Format(time.RFC3339)
	h.jobs[jobID] = job
	writeFakeSuccess(w, r, http.StatusOK, cloneFakeJob(job))
}

func (h *fakeJobsHandler) failJob(w http.ResponseWriter, r *http.Request, jobID string) {
	var req contracts.JobFailRequest
	if !decodeFakeBody(w, r, &req) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	job, ok := h.jobs[jobID]
	if !ok {
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake job not found", false)
		return
	}
	if !fakeJobWorkerMatches(job, req.WorkerID) {
		writeFakeError(w, r, http.StatusForbidden, "forbidden", "worker_id does not match the active job claim", false)
		return
	}
	job.State = contracts.JobFailed
	job.TerminalError = &req.Error
	job.UpdatedAt = h.cfg.Now().UTC().Format(time.RFC3339)
	h.jobs[jobID] = job
	writeFakeSuccess(w, r, http.StatusOK, cloneFakeJob(job))
}

func (h *fakeJobsHandler) cancelJob(w http.ResponseWriter, r *http.Request, jobID string) {
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		writeFakeError(w, r, http.StatusBadRequest, "missing_idempotency_key", "Idempotency-Key header is required for this job operation", false)
		return
	}
	req := contracts.CancelRequest{}
	if r.Body != nil {
		defer r.Body.Close()
		if r.Body != http.NoBody {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeFakeError(w, r, http.StatusBadRequest, "validation_failed", "request body is invalid JSON", false)
				return
			}
		}
	}
	fingerprint := fakeJSONFingerprint("cancel:"+jobID, req)
	h.mu.Lock()
	defer h.mu.Unlock()
	if existing, ok := h.idempotency[idempotencyKey]; ok {
		if existing.operation != "cancel" || existing.fingerprint != fingerprint {
			writeFakeError(w, r, http.StatusConflict, "idempotency_conflict", "idempotency key was reused with different request content", false)
			return
		}
		job := h.jobs[existing.jobID]
		writeFakeSuccess(w, r, http.StatusOK, cloneFakeJob(job))
		return
	}
	job, ok := h.jobs[jobID]
	if !ok {
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake job not found", false)
		return
	}
	if isFakeJobTerminal(job.State) {
		writeFakeError(w, r, http.StatusConflict, "job_terminal", "job is already terminal", false)
		return
	}
	job.State = contracts.JobCanceled
	if req.Reason != "" {
		job.StatusMessage = req.Reason
	}
	job.UpdatedAt = h.cfg.Now().UTC().Format(time.RFC3339)
	h.jobs[jobID] = job
	h.idempotency[idempotencyKey] = fakeJobIdempotency{operation: "cancel", jobID: jobID, fingerprint: fingerprint}
	writeFakeSuccess(w, r, http.StatusOK, cloneFakeJob(job))
}

func (h *fakeJobsHandler) writePolicyContext(w http.ResponseWriter, r *http.Request, jobID string) {
	job, ok := h.getJob(jobID)
	if !ok {
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake job not found", false)
		return
	}
	writeFakeSuccess(w, r, http.StatusOK, contracts.JobPolicyContext{
		ResourceKind:   "job",
		JobID:          job.JobID,
		OwnerSubjectID: "sub_fake_agent",
		RequesterID:    "sub_fake_agent",
		JobState:       string(job.State),
		PolicyState:    "active",
	})
}

func (h *fakeJobsHandler) writeAgentProjection(w http.ResponseWriter, r *http.Request, jobID string) {
	job, ok := h.getJob(jobID)
	if !ok {
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake job not found", false)
		return
	}
	statusMessage := job.StatusMessage
	projection := contracts.AgentJob{
		JobID:         job.JobID,
		State:         job.State,
		CreatedAt:     job.CreatedAt,
		UpdatedAt:     job.UpdatedAt,
		InputSummary:  cloneMap(job.InputSummary),
		ArtifactRefs:  append([]string(nil), job.ArtifactRefs...),
		LogCursor:     cloneStringPointer(job.LogCursor),
		TerminalError: cloneErrorPointer(job.TerminalError),
		Links:         cloneMap(job.Links),
	}
	if statusMessage != "" {
		projection.StatusMessage = &statusMessage
	}
	writeFakeSuccess(w, r, http.StatusOK, projection)
}

func (h *fakeJobsHandler) readLogs(w http.ResponseWriter, r *http.Request, jobID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.jobs[jobID]; !ok {
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake job not found", false)
		return
	}
	entries := append([]contracts.JobLogEntry(nil), h.logs[jobID]...)
	writeFakeSuccess(w, r, http.StatusOK, map[string]any{"items": entries, "next_cursor": nil})
}

func (h *fakeJobsHandler) appendLogs(w http.ResponseWriter, r *http.Request, jobID string) {
	var req contracts.AppendJobLogRequest
	if !decodeFakeBody(w, r, &req) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	job, ok := h.jobs[jobID]
	if !ok {
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake job not found", false)
		return
	}
	if !fakeJobWorkerMatches(job, req.WorkerID) {
		writeFakeError(w, r, http.StatusForbidden, "forbidden", "worker_id does not match the active job claim", false)
		return
	}
	h.logs[jobID] = append(h.logs[jobID], req.Entries...)
	entries := append([]contracts.JobLogEntry(nil), h.logs[jobID]...)
	writeFakeSuccess(w, r, http.StatusOK, map[string]any{"items": entries, "next_cursor": nil})
}

func (h *fakeJobsHandler) listJobs(state contracts.JobState) []contracts.Job {
	h.mu.Lock()
	defer h.mu.Unlock()
	jobs := make([]contracts.Job, 0, len(h.jobOrder))
	for _, jobID := range h.jobOrder {
		job, ok := h.jobs[jobID]
		if !ok {
			continue
		}
		if state != "" && job.State != state {
			continue
		}
		jobs = append(jobs, cloneFakeJob(job))
	}
	return jobs
}

func (h *fakeJobsHandler) getJob(jobID string) (contracts.Job, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	job, ok := h.jobs[jobID]
	if !ok {
		return contracts.Job{}, false
	}
	return cloneFakeJob(job), true
}

func (h *fakeJobsHandler) metricsSamples() []contracts.MetricSample {
	h.mu.Lock()
	defer h.mu.Unlock()
	counts := map[contracts.JobState]int{}
	for _, job := range h.jobs {
		counts[job.State]++
	}
	samples := []contracts.MetricSample{contracts.CountMetric("jobs_total", len(h.jobs), nil)}
	for state, count := range counts {
		samples = append(samples, contracts.CountMetric("jobs_by_state", count, map[string]string{"state": string(state)}))
	}
	return samples
}

func (h *fakeJobsHandler) writeBlocked(w http.ResponseWriter, r *http.Request) bool {
	switch h.cfg.Behavior {
	case FakeComponentDenied:
		writeFakeError(w, r, http.StatusForbidden, "forbidden", "fake jobs access denied", false)
		return true
	case FakeComponentUnavailable:
		writeFakeError(w, r, http.StatusServiceUnavailable, "component_unavailable", "fake jobs unavailable", true)
		return true
	default:
		return false
	}
}

func (h *fakeLeasesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.writeBlocked(w, r) {
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodGet && path == "/v1/leases/health":
		health := contracts.NewComponentHealth("leases", map[string]any{"fake": true})
		health.Status = h.cfg.HealthStatus
		health.CheckedAt = h.cfg.Now().UTC().Format(time.RFC3339)
		writeFakeSuccess(w, r, http.StatusOK, health)
	case r.Method == http.MethodGet && path == "/v1/leases/metrics":
		samples := h.cfg.Samples
		if samples == nil {
			samples = h.metricsSamples()
		}
		metrics := contracts.NewComponentMetrics("leases", samples)
		metrics.CollectedAt = h.cfg.Now().UTC().Format(time.RFC3339)
		writeFakeSuccess(w, r, http.StatusOK, metrics)
	case r.Method == http.MethodGet && path == "/v1/resources":
		writeFakeSuccess(w, r, http.StatusOK, map[string]any{"items": h.listResources(r.URL.Query().Get("selector")), "next_cursor": nil})
	case r.Method == http.MethodPost && path == "/v1/resources":
		h.registerResource(w, r)
	case strings.HasPrefix(path, "/v1/resources/"):
		h.resourceRoute(w, r, strings.TrimPrefix(path, "/v1/resources/"))
	case r.Method == http.MethodGet && path == "/v1/lease-requests":
		h.listLeaseRequests(w, r)
	case r.Method == http.MethodPost && path == "/v1/lease-requests":
		h.createLeaseRequest(w, r)
	case strings.HasPrefix(path, "/v1/lease-requests/"):
		h.leaseRequestRoute(w, r, strings.TrimPrefix(path, "/v1/lease-requests/"))
	case strings.HasPrefix(path, "/v1/leases/"):
		h.leaseRoute(w, r, strings.TrimPrefix(path, "/v1/leases/"))
	default:
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake leases route not found", false)
	}
}

func (h *fakeLeasesHandler) resourceRoute(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake resource route not found", false)
		return
	}
	resourceID, err := url.PathUnescape(parts[0])
	if err != nil {
		writeFakeError(w, r, http.StatusBadRequest, "validation_failed", "resource_id is invalid", false)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		resource, ok := h.getResource(resourceID)
		if !ok {
			writeFakeError(w, r, http.StatusNotFound, "not_found", "fake resource not found", false)
			return
		}
		writeFakeSuccess(w, r, http.StatusOK, resource)
		return
	}
	if len(parts) == 2 && parts[1] == "inspection" && r.Method == http.MethodGet {
		h.inspectResource(w, r, resourceID)
		return
	}
	writeFakeError(w, r, http.StatusNotFound, "not_found", "fake resource route not found", false)
}

func (h *fakeLeasesHandler) leaseRequestRoute(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake lease request route not found", false)
		return
	}
	requestID, err := url.PathUnescape(parts[0])
	if err != nil {
		writeFakeError(w, r, http.StatusBadRequest, "validation_failed", "request_id is invalid", false)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		request, ok := h.getLeaseRequest(requestID)
		if !ok {
			writeFakeError(w, r, http.StatusNotFound, "not_found", "fake lease request not found", false)
			return
		}
		writeFakeSuccess(w, r, http.StatusOK, request)
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost {
		h.cancelLeaseRequest(w, r, requestID)
		return
	}
	writeFakeError(w, r, http.StatusNotFound, "not_found", "fake lease request route not found", false)
}

func (h *fakeLeasesHandler) leaseRoute(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake lease route not found", false)
		return
	}
	leaseID, err := url.PathUnescape(parts[0])
	if err != nil {
		writeFakeError(w, r, http.StatusBadRequest, "validation_failed", "lease_id is invalid", false)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		lease, ok := h.getLease(leaseID)
		if !ok {
			writeFakeError(w, r, http.StatusNotFound, "not_found", "fake lease not found", false)
			return
		}
		writeFakeSuccess(w, r, http.StatusOK, lease)
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake lease route not found", false)
		return
	}
	switch parts[1] {
	case "heartbeat":
		h.heartbeatLease(w, r, leaseID)
	case "release":
		h.releaseLease(w, r, leaseID)
	default:
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake lease route not found", false)
	}
}

func (h *fakeLeasesHandler) registerResource(w http.ResponseWriter, r *http.Request) {
	var req contracts.RegisterResourceRequest
	if !decodeFakeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Selector) == "" {
		writeFakeError(w, r, http.StatusBadRequest, "validation_failed", "selector is required", false)
		return
	}
	status := req.Status
	if status == "" {
		status = contracts.ResourceAvailable
	}
	if status != contracts.ResourceAvailable && status != contracts.ResourceUnavailable {
		writeFakeError(w, r, http.StatusBadRequest, "validation_failed", "status must be available or unavailable", false)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	resourceID := strings.TrimSpace(req.ResourceID)
	if resourceID == "" {
		resourceID = fmt.Sprintf("res_fake_created_%03d", len(h.resourceOrder)+1)
	}
	if _, exists := h.resources[resourceID]; exists {
		writeFakeError(w, r, http.StatusConflict, "resource_conflict", "resource already exists", false)
		return
	}
	resource := contracts.ResourceRecord{
		ResourceID:  resourceID,
		Selector:    req.Selector,
		DisplayName: req.DisplayName,
		Status:      status,
		NodeID:      req.NodeID,
		Tags:        append([]string(nil), req.Tags...),
		Metadata:    cloneMap(req.Metadata),
		Links:       fakeResourceLinks(resourceID),
	}
	h.resources[resourceID] = resource
	h.resourceOrder = append(h.resourceOrder, resourceID)
	writeFakeSuccess(w, r, http.StatusCreated, cloneFakeResource(resource))
}

func (h *fakeLeasesHandler) createLeaseRequest(w http.ResponseWriter, r *http.Request) {
	var req contracts.CreateLeaseRequest
	if !decodeFakeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.RequesterID) == "" || strings.TrimSpace(req.ResourceSelector) == "" {
		writeFakeError(w, r, http.StatusBadRequest, "validation_failed", "requester_id and resource_selector are required", false)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	resource, ok := h.firstResourceForSelector(req.ResourceSelector)
	if !ok || resource.Status != contracts.ResourceAvailable {
		writeFakeError(w, r, http.StatusConflict, "resource_unavailable", "no available resource matches the requested selector", true)
		return
	}
	now := h.cfg.Now().UTC().Format(time.RFC3339)
	requestID := fmt.Sprintf("lease_req_fake_created_%03d", h.nextRequestID)
	h.nextRequestID++
	request := contracts.LeaseRequest{
		RequestID:        requestID,
		State:            contracts.LeaseRequestPending,
		RequesterID:      req.RequesterID,
		ResourceSelector: req.ResourceSelector,
		CreatedAt:        now,
		UpdatedAt:        now,
		Links:            fakeLeaseRequestLinks(requestID, contracts.LeaseRequestPending),
	}
	if h.activeLeaseForResource(resource.ResourceID) == nil {
		request = h.grantRequestLocked(request, resource.ResourceID, req.RequesterID)
	} else {
		position := h.queuePositionLocked(req.ResourceSelector) + 1
		request.QueuePosition = &position
	}
	h.requests[requestID] = request
	h.requestOrder = append(h.requestOrder, requestID)
	writeFakeSuccess(w, r, http.StatusCreated, cloneFakeLeaseRequest(request))
}

func (h *fakeLeasesHandler) listLeaseRequests(w http.ResponseWriter, r *http.Request) {
	requesterID := strings.TrimSpace(r.URL.Query().Get("requester_id"))
	if requesterID == "" {
		writeFakeError(w, r, http.StatusBadRequest, "validation_failed", "requester_id is required", false)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	items := []contracts.LeaseRequest{}
	for _, requestID := range h.requestOrder {
		request, ok := h.requests[requestID]
		if ok && request.RequesterID == requesterID {
			items = append(items, cloneFakeLeaseRequest(request))
		}
	}
	writeFakeSuccess(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (h *fakeLeasesHandler) cancelLeaseRequest(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Body != nil {
		_ = r.Body.Close()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	request, ok := h.requests[requestID]
	if !ok {
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake lease request not found", false)
		return
	}
	if request.State == contracts.LeaseRequestPending {
		request.State = contracts.LeaseRequestCanceled
		request.QueuePosition = nil
		request.UpdatedAt = h.cfg.Now().UTC().Format(time.RFC3339)
		request.Links = fakeLeaseRequestLinks(request.RequestID, request.State)
		h.requests[requestID] = request
	}
	writeFakeSuccess(w, r, http.StatusOK, cloneFakeLeaseRequest(request))
}

func (h *fakeLeasesHandler) heartbeatLease(w http.ResponseWriter, r *http.Request, leaseID string) {
	var req contracts.LeaseHeartbeatRequest
	if !decodeFakeBody(w, r, &req) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	lease, ok := h.leases[leaseID]
	if !ok {
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake lease not found", false)
		return
	}
	if h.releasedLeases[leaseID] || lease.ReleasedAt != "" {
		writeFakeError(w, r, http.StatusConflict, "lease_expired", "lease heartbeat rejected because lease has expired", false)
		return
	}
	if req.HolderID != lease.HolderID {
		writeFakeError(w, r, http.StatusForbidden, "forbidden", "holder_id does not match the active lease", false)
		return
	}
	lease.ExpiresAt = h.cfg.Now().UTC().Add(60 * time.Second).Format(time.RFC3339)
	h.leases[leaseID] = lease
	if requestID := h.requestByLease[leaseID]; requestID != "" {
		request := h.requests[requestID]
		request.Lease = cloneFakeLeasePointer(lease)
		request.UpdatedAt = h.cfg.Now().UTC().Format(time.RFC3339)
		h.requests[requestID] = request
	}
	writeFakeSuccess(w, r, http.StatusOK, cloneFakeLease(lease))
}

func (h *fakeLeasesHandler) releaseLease(w http.ResponseWriter, r *http.Request, leaseID string) {
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		writeFakeError(w, r, http.StatusBadRequest, "missing_idempotency_key", "Idempotency-Key header is required for this lease operation", false)
		return
	}
	var req contracts.LeaseReleaseRequest
	if !decodeFakeBody(w, r, &req) {
		return
	}
	fingerprint := fakeJSONFingerprint("release:"+leaseID, req)
	h.mu.Lock()
	defer h.mu.Unlock()
	if existing, ok := h.idempotency[idempotencyKey]; ok {
		if existing.operation != "release" || existing.leaseID != leaseID || existing.fingerprint != fingerprint {
			writeFakeError(w, r, http.StatusConflict, "idempotency_conflict", "idempotency key was reused with different request content", false)
			return
		}
		lease := h.leases[leaseID]
		writeFakeSuccess(w, r, http.StatusOK, cloneFakeLease(lease))
		return
	}
	lease, ok := h.leases[leaseID]
	if !ok {
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake lease not found", false)
		return
	}
	if req.HolderID == "" {
		writeFakeError(w, r, http.StatusBadRequest, "validation_failed", "holder_id is required", false)
		return
	}
	if req.HolderID != lease.HolderID {
		writeFakeError(w, r, http.StatusForbidden, "forbidden", "holder_id does not match the active lease", false)
		return
	}
	now := h.cfg.Now().UTC().Format(time.RFC3339)
	lease.ReleasedAt = now
	lease.ReleasedBy = strings.TrimSpace(r.Header.Get("X-Actor-Subject-ID"))
	if lease.ReleasedBy == "" {
		lease.ReleasedBy = "sub_fake_actor"
	}
	lease.ReleaseReason = req.Reason
	h.leases[leaseID] = lease
	h.releasedLeases[leaseID] = true
	h.idempotency[idempotencyKey] = fakeLeaseIdempotency{operation: "release", leaseID: leaseID, fingerprint: fingerprint}
	h.promoteNextPendingLocked(h.selectorByLease[leaseID])
	writeFakeSuccess(w, r, http.StatusOK, cloneFakeLease(lease))
}

func (h *fakeLeasesHandler) inspectResource(w http.ResponseWriter, r *http.Request, resourceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	resource, ok := h.resources[resourceID]
	if !ok {
		writeFakeError(w, r, http.StatusNotFound, "not_found", "fake resource not found", false)
		return
	}
	queue := []contracts.LeaseQueueRecord{}
	for _, requestID := range h.requestOrder {
		request := h.requests[requestID]
		if request.ResourceSelector != resource.Selector || request.State != contracts.LeaseRequestPending {
			continue
		}
		position := 0
		if request.QueuePosition != nil {
			position = *request.QueuePosition
		}
		queue = append(queue, contracts.LeaseQueueRecord{
			RequestID:     request.RequestID,
			RequesterID:   request.RequesterID,
			QueuePosition: position,
		})
	}
	writeFakeSuccess(w, r, http.StatusOK, contracts.ResourceInspection{
		Resource:    cloneFakeResource(resource),
		ActiveLease: h.activeLeaseForResource(resourceID),
		QueueLength: len(queue),
		Queue:       queue,
	})
}

func (h *fakeLeasesHandler) listResources(selector string) []contracts.ResourceRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	items := make([]contracts.ResourceRecord, 0, len(h.resourceOrder))
	for _, resourceID := range h.resourceOrder {
		resource, ok := h.resources[resourceID]
		if !ok {
			continue
		}
		if selector != "" && resource.Selector != selector {
			continue
		}
		items = append(items, cloneFakeResource(resource))
	}
	return items
}

func (h *fakeLeasesHandler) getResource(resourceID string) (contracts.ResourceRecord, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	resource, ok := h.resources[resourceID]
	if !ok {
		return contracts.ResourceRecord{}, false
	}
	return cloneFakeResource(resource), true
}

func (h *fakeLeasesHandler) getLeaseRequest(requestID string) (contracts.LeaseRequest, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	request, ok := h.requests[requestID]
	if !ok {
		return contracts.LeaseRequest{}, false
	}
	return cloneFakeLeaseRequest(request), true
}

func (h *fakeLeasesHandler) getLease(leaseID string) (contracts.Lease, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	lease, ok := h.leases[leaseID]
	if !ok {
		return contracts.Lease{}, false
	}
	return cloneFakeLease(lease), true
}

func (h *fakeLeasesHandler) metricsSamples() []contracts.MetricSample {
	h.mu.Lock()
	defer h.mu.Unlock()
	requestsByState := map[contracts.LeaseRequestState]int{}
	for _, request := range h.requests {
		requestsByState[request.State]++
	}
	samples := []contracts.MetricSample{
		contracts.CountMetric("lease_resources_total", len(h.resources), nil),
		contracts.CountMetric("leases_active_total", h.activeLeaseCountLocked(), nil),
	}
	for state, count := range requestsByState {
		samples = append(samples, contracts.CountMetric("lease_requests_by_state", count, map[string]string{"state": string(state)}))
	}
	return samples
}

func (h *fakeLeasesHandler) firstResourceForSelector(selector string) (contracts.ResourceRecord, bool) {
	for _, resourceID := range h.resourceOrder {
		resource := h.resources[resourceID]
		if resource.Selector == selector {
			return resource, true
		}
	}
	return contracts.ResourceRecord{}, false
}

func (h *fakeLeasesHandler) activeLeaseForResource(resourceID string) *contracts.Lease {
	for _, lease := range h.leases {
		if lease.ResourceID == resourceID && lease.ReleasedAt == "" && !h.releasedLeases[lease.LeaseID] {
			return cloneFakeLeasePointer(lease)
		}
	}
	return nil
}

func (h *fakeLeasesHandler) queuePositionLocked(selector string) int {
	count := 0
	for _, request := range h.requests {
		if request.ResourceSelector == selector && request.State == contracts.LeaseRequestPending {
			count++
		}
	}
	return count
}

func (h *fakeLeasesHandler) grantRequestLocked(request contracts.LeaseRequest, resourceID, holderID string) contracts.LeaseRequest {
	now := h.cfg.Now().UTC()
	leaseID := fmt.Sprintf("lease_fake_created_%03d", h.nextLeaseID)
	h.nextLeaseID++
	lease := contracts.Lease{
		LeaseID:    leaseID,
		ResourceID: resourceID,
		HolderID:   holderID,
		ExpiresAt:  now.Add(60 * time.Second).Format(time.RFC3339),
		Links:      fakeLeaseLinks(leaseID),
	}
	h.leases[leaseID] = lease
	h.requestByLease[leaseID] = request.RequestID
	h.selectorByLease[leaseID] = request.ResourceSelector
	request.State = contracts.LeaseRequestGranted
	request.QueuePosition = nil
	request.Lease = cloneFakeLeasePointer(lease)
	request.UpdatedAt = now.Format(time.RFC3339)
	request.Links = fakeLeaseRequestLinks(request.RequestID, request.State)
	return request
}

func (h *fakeLeasesHandler) promoteNextPendingLocked(selector string) {
	if selector == "" {
		return
	}
	for _, requestID := range h.requestOrder {
		request := h.requests[requestID]
		if request.ResourceSelector != selector || request.State != contracts.LeaseRequestPending {
			continue
		}
		resource, ok := h.firstResourceForSelector(selector)
		if !ok {
			return
		}
		h.requests[requestID] = h.grantRequestLocked(request, resource.ResourceID, request.RequesterID)
		return
	}
}

func (h *fakeLeasesHandler) activeLeaseCountLocked() int {
	count := 0
	for _, lease := range h.leases {
		if lease.ReleasedAt == "" && !h.releasedLeases[lease.LeaseID] {
			count++
		}
	}
	return count
}

func (h *fakeLeasesHandler) writeBlocked(w http.ResponseWriter, r *http.Request) bool {
	switch h.cfg.Behavior {
	case FakeComponentDenied:
		writeFakeError(w, r, http.StatusForbidden, "forbidden", "fake leases access denied", false)
		return true
	case FakeComponentUnavailable:
		writeFakeError(w, r, http.StatusServiceUnavailable, "component_unavailable", "fake leases unavailable", true)
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

func fakeLeaseResources() []contracts.ResourceRecord {
	return []contracts.ResourceRecord{{
		ResourceID:  "res_fake_gpu",
		Selector:    "gpu",
		DisplayName: "Fake GPU",
		Status:      contracts.ResourceAvailable,
		NodeID:      "node_fake",
		Tags:        []string{"gpu"},
		Metadata:    map[string]any{"fake": true},
		Links:       fakeResourceLinks("res_fake_gpu"),
	}, {
		ResourceID:  "res_fake_cpu",
		Selector:    "cpu",
		DisplayName: "Fake CPU",
		Status:      contracts.ResourceAvailable,
		NodeID:      "node_fake",
		Tags:        []string{"cpu"},
		Metadata:    map[string]any{"fake": true},
		Links:       fakeResourceLinks("res_fake_cpu"),
	}, {
		ResourceID:  "res_fake_unavailable",
		Selector:    "unavailable",
		DisplayName: "Fake Unavailable Resource",
		Status:      contracts.ResourceUnavailable,
		NodeID:      "node_fake",
		Tags:        []string{"offline"},
		Metadata:    map[string]any{"fake": true},
		Links:       fakeResourceLinks("res_fake_unavailable"),
	}}
}

func fakeLeases() []contracts.Lease {
	return []contracts.Lease{{
		LeaseID:    "lease_fake_active",
		ResourceID: "res_fake_gpu",
		HolderID:   "job_fake_holder",
		ExpiresAt:  "2026-06-08T00:10:00Z",
		Links:      fakeLeaseLinks("lease_fake_active"),
	}}
}

func fakeLeaseRequests(leases []contracts.Lease) []contracts.LeaseRequest {
	now := "2026-06-08T00:00:00Z"
	active := contracts.Lease{}
	if len(leases) > 0 {
		active = leases[0]
	}
	queuePosition := 1
	return []contracts.LeaseRequest{{
		RequestID:        "lease_req_fake_granted",
		State:            contracts.LeaseRequestGranted,
		RequesterID:      "job_fake_holder",
		ResourceSelector: "gpu",
		Lease:            cloneFakeLeasePointer(active),
		CreatedAt:        now,
		UpdatedAt:        now,
		Links:            fakeLeaseRequestLinks("lease_req_fake_granted", contracts.LeaseRequestGranted),
	}, {
		RequestID:        "lease_req_fake_pending",
		State:            contracts.LeaseRequestPending,
		RequesterID:      "job_fake_waiting",
		ResourceSelector: "gpu",
		QueuePosition:    &queuePosition,
		CreatedAt:        now,
		UpdatedAt:        now,
		Links:            fakeLeaseRequestLinks("lease_req_fake_pending", contracts.LeaseRequestPending),
	}, {
		RequestID:        "lease_req_fake_expired",
		State:            contracts.LeaseRequestExpired,
		RequesterID:      "job_fake_expired",
		ResourceSelector: "gpu",
		CreatedAt:        now,
		UpdatedAt:        now,
		Links:            fakeLeaseRequestLinks("lease_req_fake_expired", contracts.LeaseRequestExpired),
	}, {
		RequestID:        "lease_req_fake_canceled",
		State:            contracts.LeaseRequestCanceled,
		RequesterID:      "job_fake_canceled",
		ResourceSelector: "gpu",
		CreatedAt:        now,
		UpdatedAt:        now,
		Links:            fakeLeaseRequestLinks("lease_req_fake_canceled", contracts.LeaseRequestCanceled),
	}}
}

func fakeResourceLinks(resourceID string) map[string]any {
	return map[string]any{
		"self":       map[string]any{"method": "GET", "href": "/v1/resources/" + resourceID},
		"inspection": map[string]any{"method": "GET", "href": "/v1/resources/" + resourceID + "/inspection"},
	}
}

func fakeLeaseRequestLinks(requestID string, state contracts.LeaseRequestState) map[string]any {
	links := map[string]any{
		"self": map[string]any{"method": "GET", "href": "/v1/lease-requests/" + requestID},
	}
	if state == contracts.LeaseRequestPending {
		links["cancel"] = map[string]any{"method": "POST", "href": "/v1/lease-requests/" + requestID + "/cancel"}
	}
	return links
}

func fakeLeaseLinks(leaseID string) map[string]any {
	return map[string]any{
		"self":      map[string]any{"method": "GET", "href": "/v1/leases/" + leaseID},
		"heartbeat": map[string]any{"method": "POST", "href": "/v1/leases/" + leaseID + "/heartbeat"},
		"release":   map[string]any{"method": "POST", "href": "/v1/leases/" + leaseID + "/release"},
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

func cloneFakeResource(resource contracts.ResourceRecord) contracts.ResourceRecord {
	resource.Tags = append([]string(nil), resource.Tags...)
	resource.Metadata = cloneMap(resource.Metadata)
	resource.Links = cloneMap(resource.Links)
	return resource
}

func cloneFakeLeaseRequest(request contracts.LeaseRequest) contracts.LeaseRequest {
	request.QueuePosition = cloneIntPointer(request.QueuePosition)
	request.Lease = cloneFakeLeasePointerValue(request.Lease)
	request.Links = cloneMap(request.Links)
	return request
}

func cloneFakeLease(lease contracts.Lease) contracts.Lease {
	lease.Links = cloneMap(lease.Links)
	return lease
}

func cloneFakeLeasePointer(lease contracts.Lease) *contracts.Lease {
	if lease.LeaseID == "" {
		return nil
	}
	cloned := cloneFakeLease(lease)
	return &cloned
}

func cloneFakeLeasePointerValue(lease *contracts.Lease) *contracts.Lease {
	if lease == nil {
		return nil
	}
	cloned := cloneFakeLease(*lease)
	return &cloned
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func fakeJobs() []contracts.Job {
	now := "2026-06-08T00:00:00Z"
	claimedAt := "2026-06-08T00:01:00Z"
	expiresAt := "2026-06-08T00:06:00Z"
	claim := &contracts.JobClaim{WorkerID: "runner_fake", ClaimedAt: claimedAt, ExpiresAt: expiresAt}
	failedError := &contracts.ErrorObject{Code: "provider_unavailable", Message: "fake provider failed", Retryable: true}
	return []contracts.Job{
		fakeJob("job_fake_queued", contracts.JobQueued, now, nil, nil),
		fakeJob("job_fake_cancelable", contracts.JobQueued, now, nil, nil),
		fakeJob("job_fake_claimed", contracts.JobClaimed, now, claim, nil),
		fakeJob("job_fake_running", contracts.JobRunning, now, claim, nil),
		fakeJob("job_fake_succeeded", contracts.JobSucceeded, now, nil, nil),
		fakeJob("job_fake_failed", contracts.JobFailed, now, nil, failedError),
		fakeJob("job_fake_canceled", contracts.JobCanceled, now, nil, nil),
		fakeJob("job_fake_expired", contracts.JobExpired, now, nil, nil),
	}
}

func fakeJob(jobID string, state contracts.JobState, now string, claim *contracts.JobClaim, terminalError *contracts.ErrorObject) contracts.Job {
	job := contracts.Job{
		JobID:         jobID,
		State:         state,
		CreatedAt:     now,
		UpdatedAt:     now,
		InputSummary:  map[string]any{"capability_id": "cap_fake"},
		Metadata:      map[string]any{"fake": true},
		Claim:         cloneJobClaimPointer(claim),
		ArtifactRefs:  []string{},
		TerminalError: cloneErrorPointer(terminalError),
		Links:         fakeJobLinks(jobID),
	}
	switch state {
	case contracts.JobSucceeded:
		job.ArtifactRefs = []string{"art_fake_001"}
	case contracts.JobCanceled:
		job.StatusMessage = "fake cancellation"
	case contracts.JobExpired:
		job.StatusMessage = "fake claim expired"
	}
	return job
}

func fakeJobLinks(jobID string) map[string]any {
	return map[string]any{
		"self": map[string]any{"method": "GET", "href": "/v1/jobs/" + jobID},
		"logs": map[string]any{"method": "GET", "href": "/v1/jobs/" + jobID + "/logs"},
	}
}

func fakeJobLogs(jobID string) []contracts.JobLogEntry {
	return []contracts.JobLogEntry{{
		Timestamp: "2026-06-08T00:00:00Z",
		Level:     "info",
		Message:   "fake job state available",
		Fields:    map[string]any{"job_id": jobID},
	}}
}

func isFakeJobTerminal(state contracts.JobState) bool {
	switch state {
	case contracts.JobSucceeded, contracts.JobFailed, contracts.JobCanceled, contracts.JobExpired:
		return true
	default:
		return false
	}
}

func fakeJobWorkerMatches(job contracts.Job, workerID string) bool {
	if strings.TrimSpace(workerID) == "" {
		return false
	}
	if job.Claim == nil {
		return true
	}
	return job.Claim.WorkerID == workerID
}

func cloneFakeJob(job contracts.Job) contracts.Job {
	job.InputSummary = cloneMap(job.InputSummary)
	job.Metadata = cloneMap(job.Metadata)
	job.Claim = cloneJobClaimPointer(job.Claim)
	job.ResourceRefs = append([]string(nil), job.ResourceRefs...)
	job.ArtifactRefs = append([]string(nil), job.ArtifactRefs...)
	job.LogCursor = cloneStringPointer(job.LogCursor)
	job.TerminalError = cloneErrorPointer(job.TerminalError)
	job.Links = cloneMap(job.Links)
	return job
}

func cloneJobClaimPointer(claim *contracts.JobClaim) *contracts.JobClaim {
	if claim == nil {
		return nil
	}
	cloned := *claim
	return &cloned
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneErrorPointer(value *contracts.ErrorObject) *contracts.ErrorObject {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
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
		jobs := fakeJobs()
		items := make([]any, 0, len(jobs))
		for _, job := range jobs {
			items = append(items, job)
		}
		return items
	case "leases":
		resources := fakeLeaseResources()
		items := make([]any, 0, len(resources))
		for _, resource := range resources {
			items = append(items, resource)
		}
		return items
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

func fakeJSONFingerprint(prefix string, value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return prefix + ":unmarshalable"
	}
	return prefix + ":" + string(raw)
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
