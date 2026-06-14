package noderegistry

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
	case path == "/v1/node-registry/health" && r.Method == http.MethodGet:
		writeSuccess(w, r, http.StatusOK, h.store.Health())
	case path == "/v1/node-registry/nodes" && r.Method == http.MethodGet:
		h.listNodes(w, r)
	case path == "/v1/node-registry/nodes" && r.Method == http.MethodPost:
		h.registerNode(w, r)
	case strings.HasPrefix(path, "/v1/node-registry/nodes/"):
		h.nodeRoute(w, r, strings.TrimPrefix(path, "/v1/node-registry/nodes/"))
	default:
		writeError(w, r, http.StatusNotFound, "not_found", "node registry route not found", false)
	}
}

func (h Handler) listNodes(w http.ResponseWriter, r *http.Request) {
	opts, ok := listOptions(w, r)
	if !ok {
		return
	}
	nodes, err := h.store.List(opts)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, nodes)
}

func (h Handler) registerNode(w http.ResponseWriter, r *http.Request) {
	var req contracts.RegisterNodeRequest
	if !decodeBody(w, r, &req) {
		return
	}
	record, err := h.store.Register(req)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, record)
}

func (h Handler) nodeRoute(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, r, http.StatusNotFound, "not_found", "node registry route not found", false)
		return
	}
	nodeID := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		record, err := h.store.Get(nodeID)
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
		writeSuccess(w, r, http.StatusOK, record)
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		writeError(w, r, http.StatusNotFound, "not_found", "node registry route not found", false)
		return
	}
	switch parts[1] {
	case "trust":
		h.updateTrust(w, r, nodeID)
	case "heartbeat":
		h.heartbeat(w, r, nodeID)
	default:
		writeError(w, r, http.StatusNotFound, "not_found", "node registry route not found", false)
	}
}

func (h Handler) updateTrust(w http.ResponseWriter, r *http.Request, nodeID string) {
	var req contracts.UpdateNodeTrustRequest
	if !decodeBody(w, r, &req) {
		return
	}
	record, err := h.store.UpdateTrust(nodeID, req)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, record)
}

func (h Handler) heartbeat(w http.ResponseWriter, r *http.Request, nodeID string) {
	var req contracts.NodeHeartbeatRequest
	if !decodeBody(w, r, &req) {
		return
	}
	record, err := h.store.Heartbeat(nodeID, req)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, record)
}

func decodeBody(w http.ResponseWriter, r *http.Request, out any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		writeError(w, r, http.StatusBadRequest, "validation_failed", "request body must be valid JSON", false)
		return false
	}
	return true
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

func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "node not found", false)
	case errors.Is(err, ErrValidation):
		writeError(w, r, http.StatusBadRequest, "validation_failed", err.Error(), false)
	case errors.Is(err, ErrInvalidCursor):
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is invalid or expired", false)
	case errors.Is(err, ErrNotRunnable):
		writeError(w, r, http.StatusForbidden, "node_not_runnable", err.Error(), false)
	default:
		writeError(w, r, http.StatusInternalServerError, "internal_error", "node registry operation failed", false)
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
			Code:      code,
			Message:   message,
			Retryable: retryable,
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
		requestID = "req_node_registry"
	}
	return map[string]string{"request_id": requestID, "schema_version": "v1"}
}
