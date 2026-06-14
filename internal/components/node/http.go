package node

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"wendy/internal/contracts"
	"wendy/internal/observability"
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
	case path == "/v1/node/health" && r.Method == http.MethodGet:
		if !h.require(w, r, "node.read") {
			return
		}
		writeSuccess(w, r, http.StatusOK, h.store.Health())
	case path == "/v1/node/metrics" && r.Method == http.MethodGet:
		if !h.require(w, r, "node.read") {
			return
		}
		metrics := h.store.Metrics()
		metrics.Samples = append(metrics.Samples, h.httpMetrics.Samples()...)
		writeSuccess(w, r, http.StatusOK, metrics)
	case path == "/v1/node/resources" && r.Method == http.MethodGet:
		if !h.require(w, r, "node.read") {
			return
		}
		h.listResources(w, r)
	case path == "/v1/node/events" && r.Method == http.MethodGet:
		if !h.require(w, r, "node.read") {
			return
		}
		h.listEvents(w, r)
	case path == "/v1/node/services" && r.Method == http.MethodGet:
		if !h.require(w, r, "node.read") {
			return
		}
		h.listServices(w, r)
	case strings.HasPrefix(path, "/v1/node/services/"):
		h.serviceRoute(w, r, strings.TrimPrefix(path, "/v1/node/services/"))
	default:
		writeError(w, r, http.StatusNotFound, "not_found", "node route not found", false)
	}
}

func (h Handler) listResources(w http.ResponseWriter, r *http.Request) {
	opts, ok := listOptions(w, r)
	if !ok {
		return
	}
	items, next, err := h.store.Resources(opts)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (h Handler) listEvents(w http.ResponseWriter, r *http.Request) {
	opts, ok := listOptions(w, r)
	if !ok {
		return
	}
	items, next, err := h.store.LifecycleEvents(opts)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (h Handler) listServices(w http.ResponseWriter, r *http.Request) {
	opts, ok := listOptions(w, r)
	if !ok {
		return
	}
	items, next, err := h.store.ListServices(opts)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (h Handler) serviceRoute(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, r, http.StatusNotFound, "not_found", "node service route not found", false)
		return
	}
	serviceID := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		if !h.require(w, r, "node.read") {
			return
		}
		service, err := h.store.GetService(serviceID)
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
		writeSuccess(w, r, http.StatusOK, service)
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		writeError(w, r, http.StatusNotFound, "not_found", "node service route not found", false)
		return
	}
	switch parts[1] {
	case "start":
		if !h.require(w, r, "node.service.start") {
			return
		}
		service, status, err := h.store.StartService(serviceID, r.Header.Get("Idempotency-Key"))
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
		writeSuccess(w, r, status, service)
	case "touch":
		if !h.require(w, r, "node.service.touch") {
			return
		}
		service, err := h.store.TouchService(serviceID)
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
		writeSuccess(w, r, http.StatusOK, service)
	case "stop":
		if !h.require(w, r, "node.service.stop") {
			return
		}
		service, status, err := h.store.StopService(serviceID, r.Header.Get("Idempotency-Key"))
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
		writeSuccess(w, r, status, service)
	default:
		writeError(w, r, http.StatusNotFound, "not_found", "node service route not found", false)
	}
}

func (h Handler) require(w http.ResponseWriter, r *http.Request, action string) bool {
	if err := h.store.CheckAuth(r.Header.Get("Authorization"), action); err != nil {
		writeStoreError(w, r, err)
		return false
	}
	return true
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "service not found", false)
	case errors.Is(err, ErrValidation):
		writeError(w, r, http.StatusBadRequest, "validation_failed", err.Error(), false)
	case errors.Is(err, ErrInvalidCursor):
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is invalid or expired", false)
	case errors.Is(err, ErrUnauthorized):
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "credential is missing, malformed, or unknown", false)
	case errors.Is(err, ErrForbidden):
		writeError(w, r, http.StatusForbidden, "forbidden", "caller is not allowed to perform the node action", false)
	case errors.Is(err, ErrRuntimeUnavailable):
		writeError(w, r, http.StatusServiceUnavailable, "provider_unavailable", "runtime adapter is unavailable", true)
	case errors.Is(err, ErrMissingIdempotency):
		writeError(w, r, http.StatusBadRequest, "missing_idempotency_key", "Idempotency-Key header is required for node service lifecycle operations", false)
	case errors.Is(err, ErrIdempotencyConflict):
		writeError(w, r, http.StatusConflict, "idempotency_conflict", "idempotency key was reused with different node lifecycle content", false)
	default:
		writeError(w, r, http.StatusInternalServerError, "internal_error", "node operation failed", false)
	}
}

func listOptions(w http.ResponseWriter, r *http.Request) (ListOptions, bool) {
	limit, err := positiveInt(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "validation_failed", "limit must be a positive integer", false)
		return ListOptions{}, false
	}
	return ListOptions{Cursor: r.URL.Query().Get("cursor"), Limit: limit}, true
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
		requestID = "req_node"
	}
	return map[string]string{"request_id": requestID, "schema_version": "v1"}
}
