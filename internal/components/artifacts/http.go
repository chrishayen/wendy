package artifacts

import (
	"encoding/json"
	"errors"
	"io"
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
	case path == "/v1/artifacts/health" && r.Method == http.MethodGet:
		writeSuccess(w, r, http.StatusOK, contracts.NewComponentHealth("artifacts", h.store.HealthDetails()))
	case path == "/v1/artifacts/metrics" && r.Method == http.MethodGet:
		metrics := h.store.Metrics()
		metrics.Samples = append(metrics.Samples, h.httpMetrics.Samples()...)
		writeSuccess(w, r, http.StatusOK, metrics)
	case path == "/v1/artifact-uploads" && r.Method == http.MethodPost:
		h.createUpload(w, r)
	case strings.HasPrefix(path, "/v1/artifact-uploads/"):
		h.uploadRoute(w, r, strings.TrimPrefix(path, "/v1/artifact-uploads/"))
	case path == "/v1/artifacts" && r.Method == http.MethodGet:
		h.listArtifacts(w, r)
	case path == "/v1/artifacts/register-local" && r.Method == http.MethodPost:
		h.registerLocalArtifact(w, r)
	case strings.HasPrefix(path, "/v1/artifacts/"):
		h.artifactRoute(w, r, strings.TrimPrefix(path, "/v1/artifacts/"))
	default:
		writeError(w, r, http.StatusNotFound, "not_found", "artifact route not found", false)
	}
}

func (h Handler) uploadRoute(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, r, http.StatusNotFound, "not_found", "artifact upload route not found", false)
		return
	}
	uploadID := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		h.getUpload(w, r, uploadID)
		return
	}
	if len(parts) != 2 {
		writeError(w, r, http.StatusNotFound, "not_found", "artifact upload route not found", false)
		return
	}
	switch parts[1] {
	case "content":
		if r.Method != http.MethodPut {
			writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
			return
		}
		h.putContent(w, r, uploadID)
	case "complete":
		if r.Method != http.MethodPost {
			writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
			return
		}
		h.completeUpload(w, r, uploadID)
	default:
		writeError(w, r, http.StatusNotFound, "not_found", "artifact upload route not found", false)
	}
}

func (h Handler) artifactRoute(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, r, http.StatusNotFound, "not_found", "artifact route not found", false)
		return
	}
	artifactID := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		h.getArtifact(w, r, artifactID)
		return
	}
	if len(parts) != 2 || r.Method != http.MethodGet {
		writeError(w, r, http.StatusNotFound, "not_found", "artifact route not found", false)
		return
	}
	switch parts[1] {
	case "policy-context":
		h.getPolicyContext(w, r, artifactID)
	case "content":
		h.readContent(w, r, artifactID)
	default:
		writeError(w, r, http.StatusNotFound, "not_found", "artifact route not found", false)
	}
}

func (h Handler) createUpload(w http.ResponseWriter, r *http.Request) {
	var req contracts.CreateArtifactUploadRequest
	if !decodeBody(w, r, &req) {
		return
	}
	session, created, err := h.store.CreateUpload(req, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	writeSuccess(w, r, status, session)
}

func (h Handler) getUpload(w http.ResponseWriter, r *http.Request, uploadID string) {
	session, err := h.store.GetUpload(uploadID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, session)
}

func (h Handler) putContent(w http.ResponseWriter, r *http.Request, uploadID string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "validation_failed", "request body could not be read", false)
		return
	}
	session, err := h.store.PutContent(uploadID, HeadersFromRequest(r, body), r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, session)
}

func (h Handler) completeUpload(w http.ResponseWriter, r *http.Request, uploadID string) {
	var req contracts.CompleteArtifactUploadRequest
	if !decodeBody(w, r, &req) {
		return
	}
	artifact, created, err := h.store.CompleteUpload(uploadID, req, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	writeSuccess(w, r, status, artifact)
}

func (h Handler) registerLocalArtifact(w http.ResponseWriter, r *http.Request) {
	var req contracts.RegisterLocalArtifactRequest
	if !decodeBody(w, r, &req) {
		return
	}
	artifact, err := h.store.RegisterLocalArtifact(req)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusCreated, artifact)
}

func (h Handler) listArtifacts(w http.ResponseWriter, r *http.Request) {
	items := h.store.ListArtifacts(ListFilter{
		ProducerRef:    r.URL.Query().Get("producer_ref"),
		OwnerSubjectID: r.URL.Query().Get("owner_subject_id"),
	})
	writeSuccess(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (h Handler) getArtifact(w http.ResponseWriter, r *http.Request, artifactID string) {
	artifact, err := h.store.GetArtifact(artifactID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, artifact)
}

func (h Handler) getPolicyContext(w http.ResponseWriter, r *http.Request, artifactID string) {
	context, err := h.store.PolicyContext(artifactID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, context)
}

func (h Handler) readContent(w http.ResponseWriter, r *http.Request, artifactID string) {
	content, err := h.store.ReadContent(artifactID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", content.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(content.Size, 10))
	w.Header().Set("Digest", content.Digest)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content.Body)
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
		writeError(w, r, http.StatusNotFound, "not_found", "artifact resource not found", false)
	case errors.Is(err, ErrValidation):
		writeError(w, r, http.StatusBadRequest, "validation_failed", err.Error(), false)
	case errors.Is(err, ErrMissingIdempotencyKey):
		writeError(w, r, http.StatusBadRequest, "missing_idempotency_key", "Idempotency-Key header is required for artifact upload operations", false)
	case errors.Is(err, ErrIdempotencyConflict):
		writeError(w, r, http.StatusConflict, "idempotency_conflict", "idempotency key was reused with different upload content", false)
	case errors.Is(err, ErrExpired):
		writeError(w, r, http.StatusGone, "artifact_expired", "artifact upload session has expired", false)
	case errors.Is(err, ErrInvalidTransition):
		writeError(w, r, http.StatusConflict, "invalid_transition", "artifact upload cannot transition from its current state", false)
	case errors.Is(err, ErrDisallowedPath):
		writeError(w, r, http.StatusBadRequest, "validation_failed", "artifact path is outside the configured root", false)
	case errors.Is(err, ErrAlreadyCompleted):
		writeError(w, r, http.StatusConflict, "idempotency_conflict", "upload is already completed with a different idempotency key", false)
	case errors.Is(err, ErrContentAlreadyReceived):
		writeError(w, r, http.StatusConflict, "idempotency_conflict", "upload content is already received with a different idempotency key", false)
	default:
		writeError(w, r, http.StatusInternalServerError, "internal_error", "artifact operation failed", false)
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
		requestID = "req_artifacts"
	}
	return map[string]string{"request_id": requestID, "schema_version": "v1"}
}
