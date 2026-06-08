package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"pacp/internal/contracts"
	"pacp/internal/observability"
)

type Handler struct {
	runner      *Runner
	httpMetrics *observability.HTTPMetrics
}

func NewHandler(runner *Runner) http.Handler {
	return Handler{runner: runner, httpMetrics: observability.NewHTTPMetrics()}
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.httpMetrics.Record(w, r, h.serveHTTP)
}

func (h Handler) serveHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case path == "/v1/runner/health" && r.Method == http.MethodGet:
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		writeSuccess(w, r, http.StatusOK, h.runner.Health(ctx))
	case path == "/v1/runner/metrics" && r.Method == http.MethodGet:
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		metrics := h.runner.Metrics(ctx)
		metrics.Samples = append(metrics.Samples, h.httpMetrics.Samples()...)
		writeSuccess(w, r, http.StatusOK, metrics)
	default:
		writeError(w, r, http.StatusNotFound, "not_found", "runner route not found", false)
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
		requestID = "req_runner"
	}
	return map[string]string{"request_id": requestID, "schema_version": "v1"}
}
