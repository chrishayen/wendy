package catalog

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
	case r.Method == http.MethodGet && path == "/v1/catalog/health":
		writeSuccess(w, r, http.StatusOK, contracts.NewComponentHealth("catalog", h.store.HealthDetails()))
	case r.Method == http.MethodGet && path == "/v1/catalog/metrics":
		metrics := h.store.Metrics()
		metrics.Samples = append(metrics.Samples, h.httpMetrics.Samples()...)
		writeSuccess(w, r, http.StatusOK, metrics)
	case r.Method == http.MethodPost && path == "/v1/catalog/manifests":
		h.registerManifest(w, r)
	case r.Method == http.MethodGet && path == "/v1/catalog/export":
		writeSuccess(w, r, http.StatusOK, h.store.Export())
	case r.Method == http.MethodGet && path == "/v1/catalog/services":
		h.listServices(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/catalog/services/"):
		h.getService(w, r, strings.TrimPrefix(path, "/v1/catalog/services/"))
	case r.Method == http.MethodGet && path == "/v1/catalog/capabilities":
		h.listCapabilities(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/catalog/capabilities/") && strings.HasSuffix(path, "/route"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/catalog/capabilities/"), "/route")
		h.getCapabilityRoute(w, r, id)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/catalog/capabilities/"):
		h.getCapability(w, r, strings.TrimPrefix(path, "/v1/catalog/capabilities/"))
	case r.Method == http.MethodGet && path == "/v1/catalog/tags":
		h.listTags(w, r)
	default:
		writeError(w, r, http.StatusNotFound, "not_found", "catalog route not found", false)
	}
}

func (h Handler) registerManifest(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var manifest contracts.ProviderManifest
	if err := json.NewDecoder(r.Body).Decode(&manifest); err != nil {
		writeError(w, r, http.StatusBadRequest, "validation_failed", "request body must be a provider manifest", false)
		return
	}
	capabilityIDs, err := h.store.RegisterManifest(manifest)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidManifest):
			writeError(w, r, http.StatusBadRequest, "validation_failed", strings.TrimPrefix(err.Error(), ErrInvalidManifest.Error()+": "), false)
		case errors.Is(err, ErrDuplicateService), errors.Is(err, ErrDuplicateCapability):
			writeError(w, r, http.StatusConflict, "duplicate_id", err.Error(), false)
		default:
			writeError(w, r, http.StatusInternalServerError, "internal_error", "catalog registration failed", false)
		}
		return
	}
	writeSuccess(w, r, http.StatusCreated, map[string]any{
		"service_id":     manifest.Service.ID,
		"capability_ids": capabilityIDs,
	})
}

func (h Handler) listServices(w http.ResponseWriter, r *http.Request) {
	opts, ok := listOptions(w, r)
	if !ok {
		return
	}
	items, next, err := h.store.ListServices(opts)
	if err != nil {
		writeCatalogQueryError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": next,
	})
}

func (h Handler) getService(w http.ResponseWriter, r *http.Request, id string) {
	service, ok := h.store.GetService(id)
	if !ok {
		writeError(w, r, http.StatusNotFound, "not_found", "service not found", false)
		return
	}
	writeSuccess(w, r, http.StatusOK, service)
}

func (h Handler) listCapabilities(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	opts, ok := listOptions(w, r)
	if !ok {
		return
	}

	records, nextCursor, err := h.store.ListCapabilities(CapabilityFilter{
		CapabilityID:         query.Get("capability_id"),
		ServiceID:            query.Get("service_id"),
		Tag:                  query.Get("tag"),
		ExecutionMode:        query.Get("execution_mode"),
		ResourceSelector:     query.Get("resource_selector"),
		VisibleCapabilityIDs: query["visible_capability_ids"],
		Cursor:               opts.Cursor,
		Limit:                opts.Limit,
	})
	if err != nil {
		writeCatalogQueryError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, map[string]any{
		"items":       records,
		"next_cursor": nextCursor,
	})
}

func (h Handler) getCapability(w http.ResponseWriter, r *http.Request, id string) {
	record, ok := h.store.GetCapability(id)
	if !ok {
		writeError(w, r, http.StatusNotFound, "not_found", "capability not found", false)
		return
	}
	writeSuccess(w, r, http.StatusOK, record)
}

func (h Handler) getCapabilityRoute(w http.ResponseWriter, r *http.Request, id string) {
	route, ok := h.store.GetRoute(id)
	if !ok {
		writeError(w, r, http.StatusNotFound, "not_found", "capability not found", false)
		return
	}
	writeSuccess(w, r, http.StatusOK, route)
}

func (h Handler) listTags(w http.ResponseWriter, r *http.Request) {
	opts, ok := listOptions(w, r)
	if !ok {
		return
	}
	items, next, err := h.store.Tags(opts)
	if err != nil {
		writeCatalogQueryError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": next,
	})
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
		return 0, errors.New("invalid limit")
	}
	return value, nil
}

func writeCatalogQueryError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrInvalidCursor) {
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is invalid or expired", false)
		return
	}
	writeError(w, r, http.StatusInternalServerError, "internal_error", "catalog query failed", false)
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
		requestID = "req_catalog"
	}
	return map[string]string{
		"request_id":     requestID,
		"schema_version": "v1",
	}
}
