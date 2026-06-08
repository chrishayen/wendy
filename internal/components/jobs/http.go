package jobs

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"pacp/internal/contracts"
	"pacp/internal/observability"
)

type Handler struct {
	store       *Store
	httpMetrics *observability.HTTPMetrics
}

func NewHandler(store *Store) http.Handler {
	return Handler{store: store, httpMetrics: observability.NewHTTPMetrics()}
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.httpMetrics.Record(w, r, h.serveHTTP)
}

func (h Handler) serveHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case path == "/v1/jobs/health" && r.Method == http.MethodGet:
		writeSuccess(w, r, http.StatusOK, contracts.NewComponentHealth("jobs", h.store.HealthDetails()))
	case path == "/v1/jobs/metrics" && r.Method == http.MethodGet:
		metrics := h.store.Metrics()
		metrics.Samples = append(metrics.Samples, h.httpMetrics.Samples()...)
		writeSuccess(w, r, http.StatusOK, metrics)
	case path == "/v1/jobs" && r.Method == http.MethodGet:
		h.listJobs(w, r)
	case path == "/v1/jobs" && r.Method == http.MethodPost:
		h.createJob(w, r)
	case strings.HasPrefix(path, "/v1/jobs/"):
		h.jobRoute(w, r, strings.TrimPrefix(path, "/v1/jobs/"))
	default:
		writeError(w, r, http.StatusNotFound, "not_found", "job route not found", false)
	}
}

func (h Handler) jobRoute(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, r, http.StatusNotFound, "not_found", "job route not found", false)
		return
	}
	jobID := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		h.getJob(w, r, jobID)
		return
	}
	if len(parts) != 2 {
		writeError(w, r, http.StatusNotFound, "not_found", "job route not found", false)
		return
	}

	switch parts[1] {
	case "policy-context":
		h.getPolicyContext(w, r, jobID)
	case "agent-projection":
		h.getAgentProjection(w, r, jobID)
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
		writeError(w, r, http.StatusNotFound, "not_found", "job route not found", false)
	}
}

func (h Handler) listJobs(w http.ResponseWriter, r *http.Request) {
	limit, err := positiveInt(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "validation_failed", "limit must be a positive integer", false)
		return
	}
	jobs, err := h.store.List(ListFilter{
		State:        contracts.JobState(r.URL.Query().Get("state")),
		CapabilityID: r.URL.Query().Get("capability_id"),
		Cursor:       r.URL.Query().Get("cursor"),
		Limit:        limit,
	})
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, map[string]any{"items": jobs, "next_cursor": nil})
}

func (h Handler) createJob(w http.ResponseWriter, r *http.Request) {
	var req contracts.CreateJobRequest
	if !decodeBody(w, r, &req) {
		return
	}
	job, created, err := h.store.Create(req, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	writeSuccess(w, r, status, job)
}

func (h Handler) getJob(w http.ResponseWriter, r *http.Request, jobID string) {
	job, err := h.store.Get(jobID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, job)
}

func (h Handler) getPolicyContext(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}
	context, err := h.store.PolicyContext(jobID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, context)
}

func (h Handler) getAgentProjection(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}
	projection, err := h.store.AgentProjection(jobID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, projection)
}

func (h Handler) claimJob(w http.ResponseWriter, r *http.Request, jobID string) {
	var req contracts.JobClaimRequest
	if !decodeBody(w, r, &req) {
		return
	}
	job, err := h.store.Claim(jobID, req)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, job)
}

func (h Handler) heartbeatJob(w http.ResponseWriter, r *http.Request, jobID string) {
	var req contracts.JobHeartbeatRequest
	if !decodeBody(w, r, &req) {
		return
	}
	job, err := h.store.Heartbeat(jobID, req)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, job)
}

func (h Handler) completeJob(w http.ResponseWriter, r *http.Request, jobID string) {
	var req contracts.JobCompleteRequest
	if !decodeBody(w, r, &req) {
		return
	}
	job, err := h.store.Complete(jobID, req)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, job)
}

func (h Handler) failJob(w http.ResponseWriter, r *http.Request, jobID string) {
	var req contracts.JobFailRequest
	if !decodeBody(w, r, &req) {
		return
	}
	job, err := h.store.Fail(jobID, req)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, job)
}

func (h Handler) cancelJob(w http.ResponseWriter, r *http.Request, jobID string) {
	var req contracts.CancelRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	job, err := h.store.Cancel(jobID, req)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, job)
}

func (h Handler) readLogs(w http.ResponseWriter, r *http.Request, jobID string) {
	limit, err := positiveInt(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "validation_failed", "limit must be a positive integer", false)
		return
	}
	items, next, err := h.store.Logs(jobID, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (h Handler) appendLogs(w http.ResponseWriter, r *http.Request, jobID string) {
	var req contracts.AppendJobLogRequest
	if !decodeBody(w, r, &req) {
		return
	}
	items, next, err := h.store.AppendLogs(jobID, req)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func decodeBody(w http.ResponseWriter, r *http.Request, out any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		writeError(w, r, http.StatusBadRequest, "validation_failed", "request body is invalid JSON", false)
		return false
	}
	return true
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "job not found", false)
	case errors.Is(err, ErrValidation):
		writeError(w, r, http.StatusBadRequest, "validation_failed", err.Error(), false)
	case errors.Is(err, ErrMissingIdempotency):
		writeError(w, r, http.StatusBadRequest, "missing_idempotency_key", "Idempotency-Key header is required for job creation", false)
	case errors.Is(err, ErrIdempotencyConflict):
		writeError(w, r, http.StatusConflict, "idempotency_conflict", "idempotency key was reused with different request content", false)
	case errors.Is(err, ErrWorkerMismatch):
		writeError(w, r, http.StatusForbidden, "forbidden", "job is claimed by another worker", false)
	case errors.Is(err, ErrClaimConflict):
		writeError(w, r, http.StatusConflict, "claim_conflict", "job is claimed by another worker", true)
	case errors.Is(err, ErrClaimExpired):
		writeError(w, r, http.StatusConflict, "claim_expired", "job claim expired", true)
	case errors.Is(err, ErrInvalidTransition):
		writeError(w, r, http.StatusBadRequest, "invalid_transition", "job cannot transition from its current state", false)
	case errors.Is(err, ErrTerminalState):
		writeError(w, r, http.StatusConflict, "terminal_state", "job is already terminal", false)
	case errors.Is(err, ErrInvalidCursor):
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is invalid or expired", false)
	default:
		writeError(w, r, http.StatusInternalServerError, "internal_error", "job operation failed", false)
	}
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
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = "req_jobs"
	}
	return map[string]string{"request_id": requestID, "schema_version": "v1"}
}

func positiveInt(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, ErrValidation
	}
	return value, nil
}
