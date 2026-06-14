package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"wendy/internal/contracts"
	"wendy/internal/observability"
)

var (
	ErrUnauthorized        = errors.New("unauthorized")
	ErrForbidden           = errors.New("forbidden")
	ErrValidation          = errors.New("validation failed")
	ErrDownstream          = errors.New("downstream error")
	ErrIdempotencyConflict = errors.New("idempotency conflict")
	ErrMissingIdempotency  = errors.New("missing idempotency key")
)

type Config struct {
	CatalogURL        string
	PolicyURL         string
	JobsURL           string
	LeasesURL         string
	ArtifactsURL      string
	GatewayCredential string
	Client            *http.Client
	DependencyTimeout time.Duration
	StaticManifests   []contracts.ProviderManifest
}

type Handler struct {
	cfg           Config
	client        *http.Client
	idempotency   *idempotencyStore
	httpMetrics   *observability.HTTPMetrics
	staticRecords []contracts.CatalogCapabilityRecord
}

type invokeRecord struct {
	fingerprint string
	status      int
	response    contracts.InvokeToolResponse
	links       map[string]any
}

type subject struct {
	ID     string
	Scopes []string
}

type downstreamError struct {
	status  int
	code    string
	message string
}

func (e downstreamError) Error() string {
	return e.message
}

type gatewayDependencyStatus struct {
	Name       string `json:"name"`
	Required   bool   `json:"required"`
	Configured bool   `json:"configured"`
	Reachable  bool   `json:"reachable"`
	Status     string `json:"status"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Error      string `json:"error,omitempty"`
}

type gatewayDependencyTarget struct {
	name     string
	baseURL  string
	path     string
	required bool
}

func NewHandler(cfg Config) http.Handler {
	handler, err := NewPersistentHandler(cfg, "")
	if err != nil {
		panic(err)
	}
	return handler
}

func NewPersistentHandler(cfg Config, idempotencyStatePath string) (http.Handler, error) {
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	cfg.CatalogURL = strings.TrimRight(cfg.CatalogURL, "/")
	cfg.PolicyURL = strings.TrimRight(cfg.PolicyURL, "/")
	cfg.JobsURL = strings.TrimRight(cfg.JobsURL, "/")
	cfg.LeasesURL = strings.TrimRight(cfg.LeasesURL, "/")
	cfg.ArtifactsURL = strings.TrimRight(cfg.ArtifactsURL, "/")
	if cfg.DependencyTimeout <= 0 {
		cfg.DependencyTimeout = 2 * time.Second
	}
	staticRecords, err := buildStaticCatalogRecords(cfg.StaticManifests)
	if err != nil {
		return nil, err
	}
	idempotency, err := newPersistentIdempotencyStore(idempotencyStatePath)
	if err != nil {
		return nil, err
	}
	return &Handler{cfg: cfg, client: client, idempotency: idempotency, httpMetrics: observability.NewHTTPMetrics(), staticRecords: staticRecords}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r = observability.EnsureRequestID(r, "req_gateway")
	h.httpMetrics.Record(w, r, h.serveHTTP)
}

func (h *Handler) serveHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case path == "/v1/gateway/health" && r.Method == http.MethodGet:
		writeSuccess(w, r, http.StatusOK, h.health(r.Context()))
	case path == "/v1/gateway/metrics" && r.Method == http.MethodGet:
		writeSuccess(w, r, http.StatusOK, h.metrics(r.Context()))
	case path == "/v1/tools" && r.Method == http.MethodGet:
		h.listTools(w, r)
	case strings.HasPrefix(path, "/v1/tools/"):
		h.toolRoute(w, r, strings.TrimPrefix(path, "/v1/tools/"))
	case strings.HasPrefix(path, "/v1/agent/jobs/"):
		h.agentJobRoute(w, r, strings.TrimPrefix(path, "/v1/agent/jobs/"))
	case strings.HasPrefix(path, "/v1/agent/resources/queues/") && r.Method == http.MethodGet:
		resourceSelector := strings.TrimPrefix(path, "/v1/agent/resources/queues/")
		h.getAgentResourceQueue(w, r, resourceSelector)
	case strings.HasPrefix(path, "/v1/artifacts/") && strings.HasSuffix(path, "/content") && r.Method == http.MethodGet:
		artifactID := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/artifacts/"), "/content")
		h.readArtifactContent(w, r, artifactID)
	default:
		writeError(w, r, http.StatusNotFound, "not_found", "gateway route not found", false)
	}
}

func (h *Handler) metrics(ctx context.Context) contracts.ComponentMetrics {
	samples := []contracts.MetricSample{
		contracts.CountMetric("gateway_idempotency_records_total", h.idempotency.recordCount(), nil),
	}
	for _, dependency := range h.dependencyStatuses(ctx) {
		labels := map[string]string{
			"downstream": dependency.Name,
			"required":   boolLabel(dependency.Required),
			"status":     dependency.Status,
		}
		samples = append(samples, contracts.CountMetric("gateway_downstream_configured", boolCount(dependency.Configured), labels))
		samples = append(samples, contracts.GaugeMetric("gateway_downstream_reachable", metricBool(dependency.Reachable), "boolean", labels))
	}
	samples = append(samples, h.httpMetrics.Samples()...)
	return contracts.NewComponentMetrics("gateway", samples)
}

func (h *Handler) health(ctx context.Context) contracts.ComponentHealth {
	dependencies := h.dependencyStatuses(ctx)
	health := contracts.NewComponentHealth("gateway", map[string]any{
		"schema_version": "v1",
		"downstreams_configured": map[string]bool{
			"catalog":   h.cfg.CatalogURL != "" || h.hasStaticCatalog(),
			"policy":    h.cfg.PolicyURL != "",
			"jobs":      h.cfg.JobsURL != "",
			"leases":    h.cfg.LeasesURL != "",
			"artifacts": h.cfg.ArtifactsURL != "",
		},
		"dependencies": dependencies,
		"idempotency":  h.idempotency.healthDetails(),
	})
	for _, dependency := range dependencies {
		if dependency.Configured && !dependency.Reachable {
			health.Status = "degraded"
			return health
		}
	}
	return health
}

func (h *Handler) dependencyStatuses(ctx context.Context) []gatewayDependencyStatus {
	targets := []gatewayDependencyTarget{
		{name: "catalog", baseURL: h.cfg.CatalogURL, path: "/v1/catalog/health", required: true},
		{name: "policy", baseURL: h.cfg.PolicyURL, path: "/v1/policy/health", required: true},
		{name: "jobs", baseURL: h.cfg.JobsURL, path: "/v1/jobs/health", required: true},
		{name: "leases", baseURL: h.cfg.LeasesURL, path: "/v1/leases/health"},
		{name: "artifacts", baseURL: h.cfg.ArtifactsURL, path: "/v1/artifacts/health", required: true},
	}
	statuses := make([]gatewayDependencyStatus, 0, len(targets))
	for _, target := range targets {
		statuses = append(statuses, h.checkDependency(ctx, target))
	}
	return statuses
}

func (h *Handler) checkDependency(ctx context.Context, target gatewayDependencyTarget) gatewayDependencyStatus {
	status := gatewayDependencyStatus{Name: target.name, Required: target.required}
	baseURL := strings.TrimRight(strings.TrimSpace(target.baseURL), "/")
	if baseURL == "" {
		if target.name == "catalog" && h.hasStaticCatalog() {
			status.Configured = true
			status.Reachable = true
			status.Status = "static"
			return status
		}
		status.Status = "missing"
		status.Error = "base URL is not configured"
		return status
	}
	status.Configured = true
	checkCtx, cancel := context.WithTimeout(ctx, h.cfg.DependencyTimeout)
	defer cancel()
	resp, err := h.downstreamRequest(checkCtx, http.MethodGet, baseURL+target.path, nil, "")
	if err != nil {
		status.Status = "unreachable"
		status.Error = err.Error()
		return status
	}
	defer resp.Body.Close()
	status.HTTPStatus = resp.StatusCode
	var envelope struct {
		OK    bool                      `json:"ok"`
		Data  contracts.ComponentHealth `json:"data"`
		Error contracts.ErrorObject     `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		status.Status = "invalid_response"
		status.Error = err.Error()
		return status
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status.Status = "unreachable"
		status.Error = envelope.Error.Message
		if status.Error == "" {
			status.Error = resp.Status
		}
		return status
	}
	if !envelope.OK || envelope.Data.Status == "" {
		status.Status = "invalid_response"
		status.Error = "health response was not ok"
		return status
	}
	status.Status = envelope.Data.Status
	status.Reachable = envelope.Data.Status == "healthy"
	if !status.Reachable {
		status.Error = "reported status " + envelope.Data.Status
	}
	return status
}

func (h *Handler) toolRoute(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, r, http.StatusNotFound, "not_found", "tool route not found", false)
		return
	}
	capabilityID := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		h.getTool(w, r, capabilityID)
		return
	}
	if len(parts) == 2 && parts[1] == "invoke" && r.Method == http.MethodPost {
		h.invokeTool(w, r, capabilityID)
		return
	}
	writeError(w, r, http.StatusNotFound, "not_found", "tool route not found", false)
}

func (h *Handler) agentJobRoute(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, r, http.StatusNotFound, "not_found", "agent job route not found", false)
		return
	}
	jobID := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		h.getAgentJob(w, r, jobID)
		return
	}
	if len(parts) != 2 {
		writeError(w, r, http.StatusNotFound, "not_found", "agent job route not found", false)
		return
	}
	switch parts[1] {
	case "cancel":
		if r.Method != http.MethodPost {
			writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
			return
		}
		h.cancelAgentJob(w, r, jobID)
	case "logs":
		if r.Method != http.MethodGet {
			writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
			return
		}
		h.getAgentLogs(w, r, jobID)
	case "artifacts":
		if r.Method != http.MethodGet {
			writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
			return
		}
		h.listAgentArtifacts(w, r, jobID)
	default:
		writeError(w, r, http.StatusNotFound, "not_found", "agent job route not found", false)
	}
}

func (h *Handler) listTools(w http.ResponseWriter, r *http.Request) {
	sub, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if !h.requirePolicy(w, r, sub.ID, "tool.discover", "tools", map[string]any{"operation": "listTools"}, "tool discovery denied") {
		return
	}

	limit := clampLimit(r.URL.Query().Get("limit"), 50, 1, 100)
	records, nextCursor, err := h.listCapabilities(r.Context(), limit, r.URL.Query().Get("cursor"))
	if err != nil {
		h.writeGatewayError(w, r, err)
		return
	}
	tools := make([]contracts.Tool, 0, len(records))
	for _, item := range records {
		decision, err := h.checkPolicy(r.Context(), sub.ID, "tool.discover", item.Capability.ID, map[string]any{"capability_id": item.Capability.ID})
		if err != nil {
			h.writeGatewayError(w, r, err)
			return
		}
		if decision.Allowed {
			tools = append(tools, projectTool(item.Capability))
		}
	}
	writeSuccess(w, r, http.StatusOK, map[string]any{"items": tools, "next_cursor": nextCursor})
}

func (h *Handler) getTool(w http.ResponseWriter, r *http.Request, capabilityID string) {
	sub, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if !h.requirePolicy(w, r, sub.ID, "tool.discover", capabilityID, map[string]any{"capability_id": capabilityID}, "tool discovery denied") {
		return
	}
	record, err := h.getCapability(r.Context(), capabilityID)
	if err != nil {
		h.writeGatewayError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, projectTool(record.Capability))
}

func (h *Handler) invokeTool(w http.ResponseWriter, r *http.Request, capabilityID string) {
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		writeError(w, r, http.StatusBadRequest, "missing_idempotency_key", "Idempotency-Key header is required for tool invocation", false)
		return
	}
	sub, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var req contracts.InvokeToolRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if !validPreferredMode(req.PreferredMode) {
		writeError(w, r, http.StatusBadRequest, "validation_failed", "preferred_mode must be sync or async", false)
		return
	}
	fp := invokeFingerprint(sub.ID, capabilityID, req)
	if replay, ok := h.idempotencyReplay(idempotencyKey, fp); ok {
		writeJSON(w, replayStatus(replay), contracts.SuccessEnvelope{OK: true, Data: replay.response, Links: replay.links, Meta: meta(r)})
		return
	}
	if h.idempotencyConflict(idempotencyKey, fp) {
		writeError(w, r, http.StatusConflict, "idempotency_conflict", "idempotency key was reused with different request content", false)
		return
	}
	record, err := h.getCapability(r.Context(), capabilityID)
	if err != nil {
		h.writeGatewayError(w, r, err)
		return
	}
	if message := validateInvokeInput(req.Input, record.Capability.InputSchema); message != "" {
		writeError(w, r, http.StatusBadRequest, "validation_failed", message, false)
		return
	}
	if !h.requirePolicy(w, r, sub.ID, "tool.invoke", capabilityID, map[string]any{"capability_id": capabilityID}, "tool invocation denied") {
		return
	}
	if req.PreferredMode == "async" {
		route, err := h.getCapabilityRoute(r.Context(), capabilityID)
		if err != nil {
			h.writeGatewayError(w, r, err)
			return
		}
		record.Route = route
		h.invokeAsyncJob(w, r, sub, record, req, idempotencyKey, fp)
		return
	}
	if shouldRunSync(record.Capability, req) {
		h.invokeSyncProvider(w, r, sub, record.Route, req, idempotencyKey, fp)
		return
	}
	h.invokeAsyncJob(w, r, sub, record, req, idempotencyKey, fp)
}

func (h *Handler) invokeAsyncJob(w http.ResponseWriter, r *http.Request, sub subject, record contracts.CatalogCapabilityRecord, req contracts.InvokeToolRequest, idempotencyKey, fingerprint string) {
	job, err := h.createJob(r.Context(), sub.ID, record, req, idempotencyKey)
	if err != nil {
		h.writeGatewayError(w, r, err)
		return
	}
	response := contracts.InvokeToolResponse{Mode: "async", JobID: job.JobID}
	links := asyncJobLinks(job.JobID, true)
	if err := h.storeIdempotency(idempotencyKey, fingerprint, http.StatusAccepted, response, links); err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "gateway idempotency record could not be stored", true)
		return
	}
	writeJSON(w, http.StatusAccepted, contracts.SuccessEnvelope{OK: true, Data: response, Links: links, Meta: meta(r)})
}

func (h *Handler) getAgentJob(w http.ResponseWriter, r *http.Request, jobID string) {
	sub, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	context, ok := h.authorizeJobRead(w, r, sub.ID, jobID)
	if !ok {
		return
	}
	var job contracts.AgentJob
	if err := h.getJSON(r.Context(), h.cfg.JobsURL+"/v1/jobs/"+url.PathEscape(jobID)+"/agent-projection", &job); err != nil {
		h.writeGatewayError(w, r, err)
		return
	}
	if err := h.attachQueueStatus(r.Context(), &job); err != nil {
		h.writeGatewayError(w, r, err)
		return
	}
	job.Links = agentJobLinks(job.JobID, stringToJobState(context.JobState))
	writeSuccess(w, r, http.StatusOK, job)
}

func (h *Handler) cancelAgentJob(w http.ResponseWriter, r *http.Request, jobID string) {
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		writeError(w, r, http.StatusBadRequest, "missing_idempotency_key", "Idempotency-Key header is required for job cancellation", false)
		return
	}
	sub, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	context, ok := h.jobPolicyContext(w, r, jobID)
	if !ok {
		return
	}
	if !h.requirePolicy(w, r, sub.ID, "job.cancel", jobID, jobContextMap(context), "job cannot be canceled in its current state") {
		return
	}
	var req contracts.CancelRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	req.RequesterID = sub.ID
	if req.Reason == "" {
		req.Reason = "canceled by requester"
	}
	var job contracts.Job
	if err := h.postJSON(r.Context(), h.cfg.JobsURL+"/v1/jobs/"+url.PathEscape(jobID)+"/cancel", req, "gateway-cancel-"+idempotencyKey, &job); err != nil {
		h.writeGatewayError(w, r, err)
		return
	}
	projection := projectAgentJob(job)
	if err := h.attachQueueStatus(r.Context(), &projection); err != nil {
		h.writeGatewayError(w, r, err)
		return
	}
	projection.Links = agentJobLinks(job.JobID, job.State)
	writeSuccess(w, r, http.StatusOK, projection)
}

func (h *Handler) getAgentLogs(w http.ResponseWriter, r *http.Request, jobID string) {
	sub, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if _, ok := h.authorizeJobRead(w, r, sub.ID, jobID); !ok {
		return
	}
	limit := clampLimit(r.URL.Query().Get("limit"), 50, 1, 100)
	query := url.Values{}
	query.Set("limit", strconv.Itoa(limit))
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		query.Set("cursor", cursor)
	}
	var data struct {
		Items      []contracts.JobLogEntry `json:"items"`
		NextCursor *string                 `json:"next_cursor"`
	}
	if err := h.getJSON(r.Context(), h.cfg.JobsURL+"/v1/jobs/"+url.PathEscape(jobID)+"/logs?"+query.Encode(), &data); err != nil {
		h.writeGatewayError(w, r, err)
		return
	}
	items := make([]contracts.JobLogEntry, 0, len(data.Items))
	for _, item := range data.Items {
		item.Fields = map[string]any{}
		item.Message = publicLogMessage(item.Message)
		items = append(items, item)
	}
	writeSuccess(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": data.NextCursor})
}

func (h *Handler) listAgentArtifacts(w http.ResponseWriter, r *http.Request, jobID string) {
	sub, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	context, ok := h.jobPolicyContext(w, r, jobID)
	if !ok {
		return
	}
	policyContext := map[string]any{
		"job_id":           jobID,
		"producer_ref":     jobID,
		"owner_subject_id": context.OwnerSubjectID,
	}
	if !h.requirePolicy(w, r, sub.ID, "artifact.read", "job_artifacts:"+jobID, policyContext, "artifact access denied") {
		return
	}
	var data struct {
		Items      []contracts.Artifact `json:"items"`
		NextCursor *string              `json:"next_cursor"`
	}
	limit := clampLimit(r.URL.Query().Get("limit"), 50, 1, 100)
	query := url.Values{}
	query.Set("producer_ref", jobID)
	query.Set("limit", strconv.Itoa(limit))
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		query.Set("cursor", cursor)
	}
	if err := h.getJSON(r.Context(), h.cfg.ArtifactsURL+"/v1/artifacts?"+query.Encode(), &data); err != nil {
		h.writeGatewayError(w, r, err)
		return
	}
	items := make([]contracts.AgentArtifact, 0, len(data.Items))
	for _, artifact := range data.Items {
		items = append(items, projectArtifact(artifact))
	}
	writeSuccess(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": data.NextCursor})
}

func (h *Handler) getAgentResourceQueue(w http.ResponseWriter, r *http.Request, resourceSelector string) {
	sub, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	policyContext := map[string]any{
		"resource_selector": resourceSelector,
		"surface":           "agent_resource_queue",
	}
	if !h.requirePolicy(w, r, sub.ID, "lease.read", resourceSelector, policyContext, "resource queue access denied") {
		return
	}
	queue, err := h.agentResourceQueue(r.Context(), resourceSelector)
	if err != nil {
		h.writeGatewayError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, queue)
}

func (h *Handler) readArtifactContent(w http.ResponseWriter, r *http.Request, artifactID string) {
	sub, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var context contracts.ArtifactPolicyContext
	if err := h.getJSON(r.Context(), h.cfg.ArtifactsURL+"/v1/artifacts/"+url.PathEscape(artifactID)+"/policy-context", &context); err != nil {
		h.writeGatewayError(w, r, err)
		return
	}
	policyContext := map[string]any{
		"artifact_id":      artifactID,
		"producer_ref":     context.ProducerRef,
		"job_id":           context.ProducerRef,
		"owner_subject_id": context.OwnerSubjectID,
	}
	if !h.requirePolicy(w, r, sub.ID, "artifact.read", artifactID, policyContext, "artifact access denied") {
		return
	}
	resp, err := h.downstreamRequest(r.Context(), http.MethodGet, h.cfg.ArtifactsURL+"/v1/artifacts/"+url.PathEscape(artifactID)+"/content", nil, "")
	if err != nil {
		h.writeGatewayError(w, r, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		h.writeGatewayError(w, r, decodeDownstreamError(resp))
		return
	}
	for _, header := range []string{"Content-Type", "Content-Length", "Digest"} {
		if value := resp.Header.Get(header); value != "" {
			w.Header().Set(header, value)
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, resp.Body)
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (subject, bool) {
	credential := r.Header.Get("Authorization")
	if credential == "" {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "Authorization bearer token is required", false)
		return subject{}, false
	}
	var verification contracts.CredentialVerification
	err := h.postJSON(r.Context(), h.cfg.PolicyURL+"/v1/auth/verify", contracts.VerifyCredentialRequest{
		Credential: credential,
		Context:    map[string]any{"surface": "public_api"},
	}, "", &verification)
	if err != nil {
		h.writeGatewayError(w, r, err)
		return subject{}, false
	}
	if !verification.Valid || verification.SubjectID == nil {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "credential is not valid", false)
		return subject{}, false
	}
	return subject{ID: *verification.SubjectID, Scopes: verification.Scopes}, true
}

func (h *Handler) requirePolicy(w http.ResponseWriter, r *http.Request, subjectID, action, resource string, context map[string]any, message string) bool {
	decision, err := h.checkPolicy(r.Context(), subjectID, action, resource, context)
	if err != nil {
		h.writeGatewayError(w, r, err)
		return false
	}
	if !decision.Allowed {
		if decision.Reason == "missing_context" {
			message = "required policy context is missing"
		}
		writeError(w, r, http.StatusForbidden, "forbidden", message, false)
		return false
	}
	return true
}

func (h *Handler) checkPolicy(ctx context.Context, subjectID, action, resource string, context map[string]any) (contracts.PolicyDecision, error) {
	var decision contracts.PolicyDecision
	err := h.postJSON(ctx, h.cfg.PolicyURL+"/v1/policy/check", contracts.PolicyCheckRequest{
		SubjectID: subjectID,
		Action:    action,
		Resource:  resource,
		Context:   context,
	}, "", &decision)
	return decision, err
}

func (h *Handler) authorizeJobRead(w http.ResponseWriter, r *http.Request, subjectID, jobID string) (contracts.JobPolicyContext, bool) {
	context, ok := h.jobPolicyContext(w, r, jobID)
	if !ok {
		return contracts.JobPolicyContext{}, false
	}
	if !h.requirePolicy(w, r, subjectID, "job.read", jobID, jobContextMap(context), "job access denied") {
		return contracts.JobPolicyContext{}, false
	}
	return context, true
}

func (h *Handler) jobPolicyContext(w http.ResponseWriter, r *http.Request, jobID string) (contracts.JobPolicyContext, bool) {
	var context contracts.JobPolicyContext
	if err := h.getJSON(r.Context(), h.cfg.JobsURL+"/v1/jobs/"+url.PathEscape(jobID)+"/policy-context", &context); err != nil {
		h.writeGatewayError(w, r, err)
		return contracts.JobPolicyContext{}, false
	}
	if context.ResourceKind != "job" || context.JobID == "" || context.JobState == "" {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "could not build policy context", true)
		return contracts.JobPolicyContext{}, false
	}
	if context.OwnerSubjectID == "" {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "job policy context is missing required owner", false)
		return contracts.JobPolicyContext{}, false
	}
	return context, true
}

func (h *Handler) getCapability(ctx context.Context, capabilityID string) (contracts.CatalogCapabilityRecord, error) {
	if h.useStaticCatalog() {
		for _, record := range h.staticRecords {
			if record.Capability.ID == capabilityID {
				return cloneCatalogRecord(record), nil
			}
		}
		return contracts.CatalogCapabilityRecord{}, downstreamError{status: http.StatusNotFound, code: "not_found", message: "capability not found"}
	}
	var data struct {
		Items []contracts.CatalogCapabilityRecord `json:"items"`
	}
	target := h.cfg.CatalogURL + "/v1/catalog/capabilities?capability_id=" + url.QueryEscape(capabilityID)
	if err := h.getJSON(ctx, target, &data); err != nil {
		return contracts.CatalogCapabilityRecord{}, err
	}
	if len(data.Items) == 0 {
		return contracts.CatalogCapabilityRecord{}, downstreamError{status: http.StatusNotFound, code: "not_found", message: "capability not found"}
	}
	return data.Items[0], nil
}

func (h *Handler) getCapabilityRoute(ctx context.Context, capabilityID string) (contracts.CapabilityRoute, error) {
	if h.useStaticCatalog() {
		record, err := h.getCapability(ctx, capabilityID)
		if err != nil {
			return contracts.CapabilityRoute{}, err
		}
		return record.Route, nil
	}
	var route contracts.CapabilityRoute
	err := h.getJSON(ctx, h.cfg.CatalogURL+"/v1/catalog/capabilities/"+url.PathEscape(capabilityID)+"/route", &route)
	return route, err
}

func (h *Handler) listCapabilities(ctx context.Context, limit int, cursor string) ([]contracts.CatalogCapabilityRecord, *string, error) {
	if h.useStaticCatalog() {
		start := 0
		if cursor != "" {
			parsed, err := parseStaticToolCursor(cursor)
			if err != nil {
				return nil, nil, downstreamError{status: http.StatusBadRequest, code: "invalid_cursor", message: "cursor is invalid or expired"}
			}
			start = parsed
		}
		if start > len(h.staticRecords) {
			return nil, nil, downstreamError{status: http.StatusBadRequest, code: "invalid_cursor", message: "cursor is invalid or expired"}
		}
		end := len(h.staticRecords)
		var next *string
		if limit > 0 && start+limit < end {
			end = start + limit
			value := staticToolCursor(end)
			next = &value
		}
		records := make([]contracts.CatalogCapabilityRecord, 0, len(h.staticRecords))
		for _, record := range h.staticRecords[start:end] {
			records = append(records, cloneCatalogRecord(record))
		}
		return records, next, nil
	}
	var data struct {
		Items      []contracts.CatalogCapabilityRecord `json:"items"`
		NextCursor *string                             `json:"next_cursor"`
	}
	query := url.Values{}
	query.Set("limit", strconv.Itoa(limit))
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	target := h.cfg.CatalogURL + "/v1/catalog/capabilities?" + query.Encode()
	if err := h.getJSON(ctx, target, &data); err != nil {
		return nil, nil, err
	}
	return data.Items, data.NextCursor, nil
}

func staticToolCursor(index int) string {
	return fmt.Sprintf("cursor_gateway_tools_%06d", index)
}

func parseStaticToolCursor(cursor string) (int, error) {
	var index int
	if _, err := fmt.Sscanf(cursor, "cursor_gateway_tools_%06d", &index); err != nil {
		return 0, err
	}
	if index < 0 || staticToolCursor(index) != cursor {
		return 0, downstreamError{status: http.StatusBadRequest, code: "invalid_cursor", message: "cursor is invalid or expired"}
	}
	return index, nil
}

func (h *Handler) hasStaticCatalog() bool {
	return len(h.staticRecords) > 0
}

func (h *Handler) useStaticCatalog() bool {
	return h.cfg.CatalogURL == "" && h.hasStaticCatalog()
}

func buildStaticCatalogRecords(manifests []contracts.ProviderManifest) ([]contracts.CatalogCapabilityRecord, error) {
	if len(manifests) == 0 {
		return nil, nil
	}
	seenServices := map[string]bool{}
	seenCapabilities := map[string]bool{}
	records := []contracts.CatalogCapabilityRecord{}
	for _, manifest := range manifests {
		if errs := contracts.ValidateProviderManifest(manifest); len(errs) > 0 {
			return nil, downstreamError{status: http.StatusBadRequest, code: "validation_failed", message: "invalid static manifest: " + strings.Join(errs, "; ")}
		}
		if seenServices[manifest.Service.ID] {
			return nil, downstreamError{status: http.StatusConflict, code: "duplicate_id", message: "duplicate static service id: " + manifest.Service.ID}
		}
		seenServices[manifest.Service.ID] = true
		provider := manifest.Provider
		if provider.HealthPath == "" {
			provider.HealthPath = "/v1/provider/health"
		}
		for _, capability := range manifest.Capabilities {
			if seenCapabilities[capability.ID] {
				return nil, downstreamError{status: http.StatusConflict, code: "duplicate_id", message: "duplicate static capability id: " + capability.ID}
			}
			seenCapabilities[capability.ID] = true
			capability.ServiceID = manifest.Service.ID
			records = append(records, contracts.CatalogCapabilityRecord{
				Capability: capability,
				Route:      staticCapabilityRoute(manifest.Service.ID, capability, provider),
				Service:    manifest.Service,
			})
		}
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Capability.ID < records[j].Capability.ID
	})
	return records, nil
}

func staticCapabilityRoute(serviceID string, capability contracts.Capability, provider contracts.Provider) contracts.CapabilityRoute {
	route := contracts.CapabilityRoute{
		CapabilityID:       capability.ID,
		ServiceID:          serviceID,
		ProviderEndpoint:   provider.Endpoint,
		ProviderHealthPath: provider.HealthPath,
		ProviderInvokePath: "/v1/provider/capabilities/" + capability.ID + "/invoke",
		NodeManaged:        provider.NodeID != "",
		ServiceStartMode:   "manual",
		ResourceHints:      capability.ResourceHints,
		ArtifactHints:      capability.ArtifactHints,
	}
	if provider.NodeID != "" {
		nodeID := provider.NodeID
		route.NodeID = &nodeID
		route.ServiceStartMode = "on_demand"
	}
	return route
}

func cloneCatalogRecord(record contracts.CatalogCapabilityRecord) contracts.CatalogCapabilityRecord {
	raw, err := json.Marshal(record)
	if err != nil {
		return record
	}
	var cloned contracts.CatalogCapabilityRecord
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return record
	}
	return cloned
}

func (h *Handler) createJob(ctx context.Context, subjectID string, record contracts.CatalogCapabilityRecord, req contracts.InvokeToolRequest, publicKey string) (contracts.Job, error) {
	plan := map[string]any{
		"capability_id":     record.Capability.ID,
		"subject_id":        subjectID,
		"input":             req.Input,
		"route":             record.Route,
		"resource_selector": firstResourceSelector(record.Route.ResourceHints),
		"timeout_seconds":   timeoutSeconds(record.Capability.TimeoutHint),
		"artifact_hints":    record.Route.ArtifactHints,
		"provider_context":  map[string]any{},
	}
	create := contracts.CreateJobRequest{
		RequesterID:  subjectID,
		CapabilityID: record.Capability.ID,
		InputSummary: inputSummary(req.Input),
		Metadata:     map[string]any{"execution_plan": plan},
	}
	var job contracts.Job
	err := h.postJSON(ctx, h.cfg.JobsURL+"/v1/jobs", create, "gateway-create-job-"+publicKey, &job)
	return job, err
}

func (h *Handler) attachQueueStatus(ctx context.Context, job *contracts.AgentJob) error {
	if h.cfg.LeasesURL == "" || job == nil || job.JobID == "" {
		return nil
	}
	var data struct {
		Items []contracts.LeaseRequest `json:"items"`
	}
	target := h.cfg.LeasesURL + "/v1/lease-requests?requester_id=" + url.QueryEscape(job.JobID)
	if err := h.getJSON(ctx, target, &data); err != nil {
		return err
	}
	if len(data.Items) == 0 {
		return nil
	}
	request := latestLeaseRequest(data.Items)
	queue := &contracts.AgentJobQueue{
		RequestID:        request.RequestID,
		State:            request.State,
		ResourceSelector: request.ResourceSelector,
		QueuePosition:    request.QueuePosition,
	}
	if request.Lease != nil {
		queue.LeaseID = request.Lease.LeaseID
		queue.ResourceID = request.Lease.ResourceID
	}
	job.Queue = queue
	return nil
}

func (h *Handler) agentResourceQueue(ctx context.Context, resourceSelector string) (contracts.AgentResourceQueue, error) {
	if h.cfg.LeasesURL == "" {
		return contracts.AgentResourceQueue{}, downstreamError{status: http.StatusServiceUnavailable, code: "downstream_unavailable", message: "lease service URL is not configured"}
	}
	resources, err := h.listLeaseResources(ctx, resourceSelector)
	if err != nil {
		return contracts.AgentResourceQueue{}, err
	}
	if len(resources) == 0 {
		return contracts.AgentResourceQueue{}, downstreamError{status: http.StatusNotFound, code: "not_found", message: "resource queue not found"}
	}
	queue := contracts.AgentResourceQueue{
		ResourceSelector:     resourceSelector,
		ResourceCount:        len(resources),
		CurrentHolderVisible: false,
		Items:                []contracts.AgentResourceQueueItem{},
		Links: map[string]any{
			"self": map[string]any{"method": "GET", "href": "/v1/agent/resources/queues/" + url.PathEscape(resourceSelector), "description": "Read resource queue.", "idempotency": "none", "side_effects": "read"},
		},
	}
	seenRequests := map[string]bool{}
	for _, resource := range resources {
		inspection, err := h.inspectLeaseResource(ctx, resource.ResourceID)
		if err != nil {
			return contracts.AgentResourceQueue{}, err
		}
		if inspection.ActiveLease != nil {
			queue.ActiveLeaseCount++
		}
		for _, item := range inspection.Queue {
			if seenRequests[item.RequestID] {
				continue
			}
			seenRequests[item.RequestID] = true
			queue.Items = append(queue.Items, contracts.AgentResourceQueueItem{
				RequestID: item.RequestID,
				Position:  item.QueuePosition,
			})
		}
	}
	sort.SliceStable(queue.Items, func(i, j int) bool {
		if queue.Items[i].Position != queue.Items[j].Position {
			return queue.Items[i].Position < queue.Items[j].Position
		}
		return queue.Items[i].RequestID < queue.Items[j].RequestID
	})
	queue.WaitingCount = len(queue.Items)
	return queue, nil
}

func (h *Handler) listLeaseResources(ctx context.Context, resourceSelector string) ([]contracts.ResourceRecord, error) {
	query := url.Values{}
	query.Set("selector", resourceSelector)
	query.Set("limit", "100")
	resources := []contracts.ResourceRecord{}
	for {
		var data struct {
			Items      []contracts.ResourceRecord `json:"items"`
			NextCursor *string                    `json:"next_cursor"`
		}
		if err := h.getJSON(ctx, h.cfg.LeasesURL+"/v1/resources?"+query.Encode(), &data); err != nil {
			return nil, err
		}
		resources = append(resources, data.Items...)
		if data.NextCursor == nil || *data.NextCursor == "" {
			return resources, nil
		}
		query.Set("cursor", *data.NextCursor)
	}
}

func (h *Handler) inspectLeaseResource(ctx context.Context, resourceID string) (contracts.ResourceInspection, error) {
	var inspection contracts.ResourceInspection
	err := h.getJSON(ctx, h.cfg.LeasesURL+"/v1/resources/"+url.PathEscape(resourceID)+"/inspection", &inspection)
	return inspection, err
}

func (h *Handler) invokeSyncProvider(w http.ResponseWriter, r *http.Request, sub subject, route contracts.CapabilityRoute, req contracts.InvokeToolRequest, idempotencyKey, fingerprint string) {
	body := map[string]any{
		"input": req.Input,
		"context": map[string]any{
			"subject_id": sub.ID,
			"request_id": requestID(r),
			"dry_run":    req.DryRun,
		},
	}
	var provider struct {
		Output    map[string]any       `json:"output"`
		Artifacts []contracts.Artifact `json:"artifacts"`
	}
	target := strings.TrimRight(route.ProviderEndpoint, "/") + route.ProviderInvokePath
	if err := h.postJSON(r.Context(), target, body, "", &provider); err != nil {
		h.writeGatewayError(w, r, err)
		return
	}
	response := contracts.InvokeToolResponse{Mode: "sync", Output: provider.Output, Artifacts: provider.Artifacts}
	if err := h.storeIdempotency(idempotencyKey, fingerprint, http.StatusOK, response, map[string]any{}); err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "gateway idempotency record could not be stored", true)
		return
	}
	writeSuccess(w, r, http.StatusOK, response)
}

func (h *Handler) getJSON(ctx context.Context, target string, out any) error {
	resp, err := h.downstreamRequest(ctx, http.MethodGet, target, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeEnvelope(resp, out)
}

func (h *Handler) postJSON(ctx context.Context, target string, body any, idempotencyKey string, out any) error {
	resp, err := h.downstreamRequest(ctx, http.MethodPost, target, body, idempotencyKey)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeEnvelope(resp, out)
}

func (h *Handler) downstreamRequest(ctx context.Context, method, target string, body any, idempotencyKey string) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if h.cfg.GatewayCredential != "" {
		req.Header.Set("Authorization", h.cfg.GatewayCredential)
	}
	observability.PropagateRequestID(ctx, req)
	return h.client.Do(req)
}

func decodeEnvelope(resp *http.Response, out any) error {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeDownstreamError(resp)
	}
	var envelope struct {
		OK    bool                  `json:"ok"`
		Data  json.RawMessage       `json:"data"`
		Error contracts.ErrorObject `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return err
	}
	if !envelope.OK {
		return downstreamError{status: http.StatusBadGateway, code: envelope.Error.Code, message: envelope.Error.Message}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(envelope.Data, out)
}

func decodeDownstreamError(resp *http.Response) error {
	var envelope struct {
		Error contracts.ErrorObject `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&envelope)
	code := envelope.Error.Code
	message := envelope.Error.Message
	if code == "" {
		code = "downstream_error"
	}
	if message == "" {
		message = "downstream request failed"
	}
	return downstreamError{status: resp.StatusCode, code: code, message: message}
}

func (h *Handler) writeGatewayError(w http.ResponseWriter, r *http.Request, err error) {
	var down downstreamError
	if errors.As(err, &down) {
		status := down.status
		if status == 401 {
			status = http.StatusUnauthorized
		}
		if status == 403 {
			status = http.StatusForbidden
		}
		if status == 404 {
			status = http.StatusNotFound
		}
		if status < 400 || status >= 600 {
			status = http.StatusBadGateway
		}
		writeError(w, r, status, down.code, down.message, false)
		return
	}
	writeError(w, r, http.StatusBadGateway, "downstream_error", err.Error(), true)
}

func (h *Handler) idempotencyReplay(key, fp string) (invokeRecord, bool) {
	return h.idempotency.replay(key, fp)
}

func (h *Handler) idempotencyConflict(key, fp string) bool {
	return h.idempotency.hasConflict(key, fp)
}

func replayStatus(record invokeRecord) int {
	if record.status != 0 {
		return record.status
	}
	if record.response.Mode == "async" && record.response.JobID != "" {
		return http.StatusAccepted
	}
	return http.StatusOK
}

func (h *Handler) storeIdempotency(key, fp string, status int, response contracts.InvokeToolResponse, links map[string]any) error {
	return h.idempotency.store(key, fp, status, response, links)
}

func projectTool(capability contracts.Capability) contracts.Tool {
	return contracts.Tool{
		ID:            capability.ID,
		Name:          capability.Name,
		Description:   capability.Description,
		Tags:          capability.Tags,
		ExecutionMode: capability.ExecutionMode,
		InputSchema:   capability.InputSchema,
		OutputSchema:  capability.OutputSchema,
		SideEffects:   capability.SideEffects,
		ResourceHints: capability.ResourceHints,
		ArtifactHints: capability.ArtifactHints,
		Examples:      capability.Examples,
		Links: map[string]any{
			"invoke":  map[string]any{"method": "POST", "href": "/v1/tools/" + capability.ID + "/invoke", "description": "Invoke tool.", "idempotency": "required", "side_effects": capability.SideEffects},
			"details": map[string]any{"method": "GET", "href": "/v1/tools/" + capability.ID, "description": "Read tool details.", "idempotency": "none", "side_effects": "read"},
		},
	}
}

func projectAgentJob(job contracts.Job) contracts.AgentJob {
	return contracts.AgentJob{
		JobID:         job.JobID,
		State:         job.State,
		CreatedAt:     job.CreatedAt,
		UpdatedAt:     job.UpdatedAt,
		StatusMessage: agentStatusMessagePtr(job.StatusMessage),
		InputSummary:  job.InputSummary,
		ArtifactRefs:  job.ArtifactRefs,
		LogCursor:     job.LogCursor,
		TerminalError: job.TerminalError,
		Links:         agentJobLinks(job.JobID, job.State),
	}
}

func agentStatusMessagePtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func latestLeaseRequest(requests []contracts.LeaseRequest) contracts.LeaseRequest {
	return requests[len(requests)-1]
}

func projectArtifact(artifact contracts.Artifact) contracts.AgentArtifact {
	return contracts.AgentArtifact{
		ArtifactID:  artifact.ArtifactID,
		Name:        artifact.Name,
		MediaType:   artifact.MediaType,
		Size:        artifact.Size,
		Checksum:    artifact.Checksum,
		CreatedAt:   artifact.CreatedAt,
		ProducerRef: artifact.ProducerRef,
		Links: map[string]any{
			"content": map[string]any{"method": "GET", "href": "/v1/artifacts/" + artifact.ArtifactID + "/content", "description": "Read artifact content.", "idempotency": "none", "side_effects": "read"},
		},
	}
}

func asyncJobLinks(jobID string, includeCancel bool) map[string]any {
	links := map[string]any{
		"status":    map[string]any{"method": "GET", "href": "/v1/agent/jobs/" + jobID, "description": "Read job status.", "idempotency": "none", "side_effects": "read"},
		"logs":      map[string]any{"method": "GET", "href": "/v1/agent/jobs/" + jobID + "/logs", "description": "Read logs.", "idempotency": "none", "side_effects": "read"},
		"artifacts": map[string]any{"method": "GET", "href": "/v1/agent/jobs/" + jobID + "/artifacts", "description": "List artifacts.", "idempotency": "none", "side_effects": "read"},
	}
	if includeCancel {
		links["cancel"] = map[string]any{"method": "POST", "href": "/v1/agent/jobs/" + jobID + "/cancel", "description": "Cancel job.", "idempotency": "required", "side_effects": "write"}
	}
	return links
}

func agentJobLinks(jobID string, state contracts.JobState) map[string]any {
	links := asyncJobLinks(jobID, isCancelableJobState(state))
	return links
}

func isCancelableJobState(state contracts.JobState) bool {
	return state == contracts.JobQueued
}

func jobContextMap(context contracts.JobPolicyContext) map[string]any {
	return map[string]any{
		"job_id":           context.JobID,
		"requester_id":     context.RequesterID,
		"owner_subject_id": context.OwnerSubjectID,
		"job_state":        context.JobState,
	}
}

func validateInvokeInput(input map[string]any, schema map[string]any) string {
	if err := contracts.ValidateObject(input, schema); err != nil {
		return gatewayInputValidationMessage(err.Error())
	}
	return ""
}

func gatewayInputValidationMessage(message string) string {
	if message == "" || strings.HasPrefix(message, "only object schemas") {
		return message
	}
	return "input." + message
}

func inputSummary(input map[string]any) map[string]any {
	summary := map[string]any{}
	for key, value := range input {
		if key == "prompt" {
			if text, ok := value.(string); ok {
				summary["prompt_present"] = text != ""
			} else {
				summary["prompt_present"] = value != nil
			}
			continue
		}
		switch value.(type) {
		case string, bool, float64, int, int64, json.Number:
			summary[key] = value
		}
	}
	return summary
}

func firstResourceSelector(hints []contracts.ResourceHint) string {
	for _, hint := range hints {
		if hint.Required && hint.Selector != "" {
			return hint.Selector
		}
	}
	if len(hints) > 0 {
		return hints[0].Selector
	}
	return ""
}

func timeoutSeconds(raw string) int {
	if raw == "" {
		return 900
	}
	duration, err := time.ParseDuration(raw)
	if err != nil {
		return 900
	}
	return int(duration.Seconds())
}

func shouldRunSync(capability contracts.Capability, req contracts.InvokeToolRequest) bool {
	if req.PreferredMode == "async" {
		return false
	}
	return capability.ExecutionMode == "sync"
}

func validPreferredMode(value string) bool {
	return value == "" || value == "sync" || value == "async"
}

func clampLimit(raw string, defaultValue, min, max int) int {
	if raw == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return defaultValue
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func publicLogMessage(message string) string {
	switch message {
	case "claimed job":
		return "job accepted"
	case "running provider invocation":
		return "job running"
	default:
		return message
	}
}

func invokeFingerprint(subjectID, capabilityID string, req contracts.InvokeToolRequest) string {
	raw, _ := json.Marshal(map[string]any{"subject_id": subjectID, "capability_id": capabilityID, "request": req})
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func stringToJobState(value string) contracts.JobState {
	return contracts.JobState(value)
}

func requestID(r *http.Request) string {
	return observability.RequestIDFromRequest(r, "req_gateway")
}

func boolLabel(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func metricBool(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func decodeBody(w http.ResponseWriter, r *http.Request, out any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		writeError(w, r, http.StatusBadRequest, "validation_failed", "request body is invalid JSON", false)
		return false
	}
	return true
}

func writeSuccess(w http.ResponseWriter, r *http.Request, status int, data any) {
	writeJSON(w, status, contracts.SuccessEnvelope{
		OK:    true,
		Data:  data,
		Links: map[string]any{},
		Meta:  meta(r),
	})
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, retryable bool) {
	writeJSON(w, status, contracts.ErrorEnvelope{
		OK: false,
		Error: contracts.ErrorObject{
			Code: code, Message: message, Retryable: retryable,
		},
		Links: map[string]any{},
		Meta:  meta(r),
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func meta(r *http.Request) map[string]string {
	return map[string]string{"request_id": requestID(r), "schema_version": "v1"}
}
