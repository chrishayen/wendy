package leases

import (
	"encoding/json"
	"errors"
	"net/http"
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
	case path == "/v1/leases/health" && r.Method == http.MethodGet:
		writeSuccess(w, r, http.StatusOK, contracts.NewComponentHealth("leases", h.store.HealthDetails()))
	case path == "/v1/leases/metrics" && r.Method == http.MethodGet:
		metrics := h.store.Metrics()
		metrics.Samples = append(metrics.Samples, h.httpMetrics.Samples()...)
		writeSuccess(w, r, http.StatusOK, metrics)
	case path == "/v1/resources" && r.Method == http.MethodGet:
		h.listResources(w, r)
	case path == "/v1/resources" && r.Method == http.MethodPost:
		h.registerResource(w, r)
	case strings.HasPrefix(path, "/v1/resources/"):
		h.resourceRoute(w, r, strings.TrimPrefix(path, "/v1/resources/"))
	case path == "/v1/lease-requests" && r.Method == http.MethodPost:
		h.createLeaseRequest(w, r)
	case strings.HasPrefix(path, "/v1/lease-requests/"):
		h.leaseRequestRoute(w, r, strings.TrimPrefix(path, "/v1/lease-requests/"))
	case strings.HasPrefix(path, "/v1/leases/"):
		h.leaseRoute(w, r, strings.TrimPrefix(path, "/v1/leases/"))
	default:
		writeError(w, r, http.StatusNotFound, "not_found", "lease route not found", false)
	}
}

func (h Handler) resourceRoute(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, r, http.StatusNotFound, "not_found", "resource route not found", false)
		return
	}
	resourceID := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		h.getResource(w, r, resourceID)
		return
	}
	if len(parts) == 2 && parts[1] == "inspection" && r.Method == http.MethodGet {
		h.inspectResource(w, r, resourceID)
		return
	}
	writeError(w, r, http.StatusNotFound, "not_found", "resource route not found", false)
}

func (h Handler) leaseRequestRoute(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, r, http.StatusNotFound, "not_found", "lease request route not found", false)
		return
	}
	requestID := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		h.getLeaseRequest(w, r, requestID)
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost {
		h.cancelLeaseRequest(w, r, requestID)
		return
	}
	writeError(w, r, http.StatusNotFound, "not_found", "lease request route not found", false)
}

func (h Handler) leaseRoute(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, r, http.StatusNotFound, "not_found", "lease route not found", false)
		return
	}
	leaseID := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		h.getLease(w, r, leaseID)
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		writeError(w, r, http.StatusNotFound, "not_found", "lease route not found", false)
		return
	}
	switch parts[1] {
	case "heartbeat":
		h.heartbeatLease(w, r, leaseID)
	case "release":
		h.releaseLease(w, r, leaseID)
	default:
		writeError(w, r, http.StatusNotFound, "not_found", "lease route not found", false)
	}
}

func (h Handler) listResources(w http.ResponseWriter, r *http.Request) {
	resources := h.store.ListResources(r.URL.Query().Get("selector"))
	writeSuccess(w, r, http.StatusOK, map[string]any{"items": resources, "next_cursor": nil})
}

func (h Handler) registerResource(w http.ResponseWriter, r *http.Request) {
	var req contracts.RegisterResourceRequest
	if !decodeBody(w, r, &req) {
		return
	}
	resource, err := h.store.RegisterResource(req)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusCreated, resource)
}

func (h Handler) getResource(w http.ResponseWriter, r *http.Request, resourceID string) {
	resource, err := h.store.GetResource(resourceID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, resource)
}

func (h Handler) inspectResource(w http.ResponseWriter, r *http.Request, resourceID string) {
	inspection, err := h.store.InspectResource(resourceID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, inspection)
}

func (h Handler) createLeaseRequest(w http.ResponseWriter, r *http.Request) {
	var req contracts.CreateLeaseRequest
	if !decodeBody(w, r, &req) {
		return
	}
	request, err := h.store.CreateLeaseRequest(req)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusCreated, request)
}

func (h Handler) getLeaseRequest(w http.ResponseWriter, r *http.Request, requestID string) {
	request, err := h.store.GetLeaseRequest(requestID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, request)
}

func (h Handler) cancelLeaseRequest(w http.ResponseWriter, r *http.Request, requestID string) {
	var req contracts.CancelRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	request, err := h.store.CancelLeaseRequest(requestID, req)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, request)
}

func (h Handler) getLease(w http.ResponseWriter, r *http.Request, leaseID string) {
	lease, err := h.store.GetLease(leaseID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, lease)
}

func (h Handler) heartbeatLease(w http.ResponseWriter, r *http.Request, leaseID string) {
	var req contracts.LeaseHeartbeatRequest
	if !decodeBody(w, r, &req) {
		return
	}
	lease, err := h.store.Heartbeat(leaseID, req)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, lease)
}

func (h Handler) releaseLease(w http.ResponseWriter, r *http.Request, leaseID string) {
	var req contracts.LeaseReleaseRequest
	if !decodeBody(w, r, &req) {
		return
	}
	lease, err := h.store.Release(leaseID, req, r.Header.Get("Idempotency-Key"), actorSubjectID(r))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, lease)
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
		writeError(w, r, http.StatusNotFound, "not_found", "lease resource not found", false)
	case errors.Is(err, ErrValidation):
		writeError(w, r, http.StatusBadRequest, "validation_failed", err.Error(), false)
	case errors.Is(err, ErrResourceConflict):
		writeError(w, r, http.StatusConflict, "resource_conflict", "resource already exists", false)
	case errors.Is(err, ErrResourceUnavailable):
		writeError(w, r, http.StatusConflict, "resource_unavailable", "resource selector is unknown", true)
	case errors.Is(err, ErrNoCapacity):
		writeError(w, r, http.StatusServiceUnavailable, "resource_unavailable", "resource selector has no leasing capacity", true)
	case errors.Is(err, ErrHolderMismatch):
		writeError(w, r, http.StatusForbidden, "forbidden", "holder mismatch", false)
	case errors.Is(err, ErrLeaseExpired):
		writeError(w, r, http.StatusConflict, "lease_expired", "lease has expired", false)
	case errors.Is(err, ErrInvalidTransition):
		writeError(w, r, http.StatusConflict, "invalid_transition", "lease request cannot transition from its current state", false)
	case errors.Is(err, ErrMissingIdempotency):
		writeError(w, r, http.StatusBadRequest, "missing_idempotency_key", "Idempotency-Key header is required for lease release", false)
	case errors.Is(err, ErrIdempotencyConflict):
		writeError(w, r, http.StatusConflict, "idempotency_conflict", "idempotency key was reused with different request content", false)
	default:
		writeError(w, r, http.StatusInternalServerError, "internal_error", "lease operation failed", false)
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
		requestID = "req_leases"
	}
	return map[string]string{"request_id": requestID, "schema_version": "v1"}
}

func actorSubjectID(r *http.Request) string {
	subjectID := r.Header.Get("X-Actor-Subject-ID")
	if subjectID == "" {
		subjectID = "sub_unknown"
	}
	return subjectID
}
