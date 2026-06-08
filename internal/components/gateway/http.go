package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"pacp/internal/contracts"
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
	ArtifactsURL      string
	GatewayCredential string
	Client            *http.Client
}

type Handler struct {
	cfg         Config
	client      *http.Client
	mu          sync.Mutex
	idempotency map[string]invokeRecord
}

type invokeRecord struct {
	fingerprint string
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

func NewHandler(cfg Config) http.Handler {
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	cfg.CatalogURL = strings.TrimRight(cfg.CatalogURL, "/")
	cfg.PolicyURL = strings.TrimRight(cfg.PolicyURL, "/")
	cfg.JobsURL = strings.TrimRight(cfg.JobsURL, "/")
	cfg.ArtifactsURL = strings.TrimRight(cfg.ArtifactsURL, "/")
	return &Handler{cfg: cfg, client: client, idempotency: map[string]invokeRecord{}}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case path == "/v1/tools" && r.Method == http.MethodGet:
		h.listTools(w, r)
	case strings.HasPrefix(path, "/v1/tools/"):
		h.toolRoute(w, r, strings.TrimPrefix(path, "/v1/tools/"))
	case strings.HasPrefix(path, "/v1/agent/jobs/"):
		h.agentJobRoute(w, r, strings.TrimPrefix(path, "/v1/agent/jobs/"))
	case strings.HasPrefix(path, "/v1/artifacts/") && strings.HasSuffix(path, "/content") && r.Method == http.MethodGet:
		artifactID := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/artifacts/"), "/content")
		h.readArtifactContent(w, r, artifactID)
	default:
		writeError(w, r, http.StatusNotFound, "not_found", "gateway route not found", false)
	}
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

	var data struct {
		Items      []contracts.CatalogCapabilityRecord `json:"items"`
		NextCursor *string                             `json:"next_cursor"`
	}
	if err := h.getJSON(r.Context(), h.cfg.CatalogURL+"/v1/catalog/capabilities", &data); err != nil {
		h.writeGatewayError(w, r, err)
		return
	}
	tools := make([]contracts.Tool, 0, len(data.Items))
	for _, item := range data.Items {
		decision, err := h.checkPolicy(r.Context(), sub.ID, "tool.discover", item.Capability.ID, nil)
		if err != nil {
			h.writeGatewayError(w, r, err)
			return
		}
		if decision.Allowed {
			tools = append(tools, projectTool(item.Capability))
		}
	}
	writeSuccess(w, r, http.StatusOK, map[string]any{"items": tools, "next_cursor": data.NextCursor})
}

func (h *Handler) getTool(w http.ResponseWriter, r *http.Request, capabilityID string) {
	sub, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if !h.requirePolicy(w, r, sub.ID, "tool.discover", capabilityID, nil, "tool discovery denied") {
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
	fp := invokeFingerprint(sub.ID, capabilityID, req)
	if replay, ok := h.idempotencyReplay(idempotencyKey, fp); ok {
		writeJSON(w, http.StatusOK, contracts.SuccessEnvelope{OK: true, Data: replay.response, Links: replay.links, Meta: meta(r)})
		return
	}
	if h.idempotencyConflict(idempotencyKey, fp) {
		writeError(w, r, http.StatusConflict, "idempotency_conflict", "idempotency key was reused with different request content", false)
		return
	}
	if !h.requirePolicy(w, r, sub.ID, "tool.invoke", capabilityID, nil, "tool invocation denied") {
		return
	}
	record, err := h.getCapability(r.Context(), capabilityID)
	if err != nil {
		h.writeGatewayError(w, r, err)
		return
	}
	if shouldRunSync(record.Capability, req) {
		h.invokeSyncProvider(w, r, sub, record.Route, req)
		return
	}
	job, err := h.createJob(r.Context(), sub.ID, record, req, idempotencyKey)
	if err != nil {
		h.writeGatewayError(w, r, err)
		return
	}
	response := contracts.InvokeToolResponse{Mode: "async", JobID: job.JobID}
	links := asyncJobLinks(job.JobID, true)
	h.storeIdempotency(idempotencyKey, fp, response, links)
	writeJSON(w, http.StatusCreated, contracts.SuccessEnvelope{OK: true, Data: response, Links: links, Meta: meta(r)})
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
	var job contracts.Job
	if err := h.postJSON(r.Context(), h.cfg.JobsURL+"/v1/jobs/"+url.PathEscape(jobID)+"/cancel", req, "gateway-cancel-"+idempotencyKey, &job); err != nil {
		h.writeGatewayError(w, r, err)
		return
	}
	projection := projectAgentJob(job)
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
		item.Fields = nil
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
	if err := h.getJSON(r.Context(), h.cfg.ArtifactsURL+"/v1/artifacts?producer_ref="+url.QueryEscape(jobID), &data); err != nil {
		h.writeGatewayError(w, r, err)
		return
	}
	items := make([]contracts.AgentArtifact, 0, len(data.Items))
	for _, artifact := range data.Items {
		items = append(items, projectArtifact(artifact))
	}
	writeSuccess(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": data.NextCursor})
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
	if context.OwnerSubjectID == "" {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "job policy context is missing owner_subject_id", false)
		return contracts.JobPolicyContext{}, false
	}
	return context, true
}

func (h *Handler) getCapability(ctx context.Context, capabilityID string) (contracts.CatalogCapabilityRecord, error) {
	var record contracts.CatalogCapabilityRecord
	err := h.getJSON(ctx, h.cfg.CatalogURL+"/v1/catalog/capabilities/"+url.PathEscape(capabilityID), &record)
	return record, err
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

func (h *Handler) invokeSyncProvider(w http.ResponseWriter, r *http.Request, sub subject, route contracts.CapabilityRoute, req contracts.InvokeToolRequest) {
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
	writeSuccess(w, r, http.StatusOK, contracts.InvokeToolResponse{Mode: "sync", Output: provider.Output, Artifacts: provider.Artifacts})
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
	h.mu.Lock()
	defer h.mu.Unlock()
	record, ok := h.idempotency[key]
	if !ok || record.fingerprint != fp {
		return invokeRecord{}, false
	}
	return record, true
}

func (h *Handler) idempotencyConflict(key, fp string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	record, ok := h.idempotency[key]
	return ok && record.fingerprint != fp
}

func (h *Handler) storeIdempotency(key, fp string, response contracts.InvokeToolResponse, links map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.idempotency[key] = invokeRecord{fingerprint: fp, response: response, links: links}
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
		StatusMessage: job.StatusMessage,
		InputSummary:  job.InputSummary,
		ArtifactRefs:  job.ArtifactRefs,
		LogCursor:     job.LogCursor,
		TerminalError: job.TerminalError,
		Links:         agentJobLinks(job.JobID, job.State),
	}
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
	links := asyncJobLinks(jobID, state == contracts.JobQueued)
	return links
}

func jobContextMap(context contracts.JobPolicyContext) map[string]any {
	return map[string]any{
		"job_id":           context.JobID,
		"requester_id":     context.RequesterID,
		"owner_subject_id": context.OwnerSubjectID,
		"job_state":        context.JobState,
	}
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
	id := r.Header.Get("X-Request-ID")
	if id == "" {
		id = "req_gateway"
	}
	return id
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
