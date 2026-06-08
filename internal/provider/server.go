package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"pacp/internal/contracts"
	"pacp/internal/observability"
)

var (
	ErrValidation = errors.New("validation failed")
	ErrNotFound   = errors.New("provider capability not found")
)

type CapabilityHandler func(context.Context, contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error)

type Server struct {
	manifest          contracts.ProviderManifest
	handlers          map[string]CapabilityHandler
	now               func() time.Time
	httpMetrics       *observability.HTTPMetrics
	invocationMetrics *providerInvocationMetrics
}

func NewServer(manifest contracts.ProviderManifest, handlers map[string]CapabilityHandler) (*Server, error) {
	if errs := contracts.ValidateProviderManifest(manifest); len(errs) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrValidation, strings.Join(errs, "; "))
	}
	for _, capability := range manifest.Capabilities {
		if handlers[capability.ID] == nil {
			return nil, fmt.Errorf("%w: missing handler for %s", ErrValidation, capability.ID)
		}
	}
	return &Server{
		manifest:          manifest,
		handlers:          handlers,
		now:               time.Now,
		httpMetrics:       observability.NewHTTPMetrics(),
		invocationMetrics: newProviderInvocationMetrics(),
	}, nil
}

func (s *Server) SetClock(now func() time.Time) {
	s.now = now
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.httpMetrics.Record(w, r, s.serveHTTP)
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodGet && path == "/v1/provider/manifest":
		writeSuccess(w, r, http.StatusOK, s.manifest)
	case r.Method == http.MethodGet && path == "/v1/provider/health":
		writeSuccess(w, r, http.StatusOK, contracts.ProviderHealth{
			Status:    "healthy",
			Version:   "v1",
			CheckedAt: s.now().UTC().Format(time.RFC3339),
			Details:   map[string]any{"service_id": s.manifest.Service.ID},
		})
	case r.Method == http.MethodGet && path == "/v1/provider/metrics":
		samples := s.invocationMetrics.Samples(s.manifest.Service.ID)
		samples = append(samples, s.httpMetrics.Samples()...)
		writeSuccess(w, r, http.StatusOK, contracts.NewComponentMetrics("provider", samples))
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/v1/provider/capabilities/") && strings.HasSuffix(path, "/invoke"):
		capabilityID := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/provider/capabilities/"), "/invoke")
		s.invoke(w, r, capabilityID)
	default:
		writeError(w, r, http.StatusNotFound, "not_found", "provider route not found", false)
	}
}

func (s *Server) invoke(w http.ResponseWriter, r *http.Request, capabilityID string) {
	start := time.Now()
	record := func(status, errorCode string) {
		s.invocationMetrics.Observe(capabilityID, status, errorCode, time.Since(start))
	}

	capability, ok := s.capability(capabilityID)
	if !ok {
		record("error", "not_found")
		writeError(w, r, http.StatusNotFound, "not_found", "capability not found", false)
		return
	}
	var req contracts.ProviderInvokeRequest
	if !decodeBody(w, r, &req) {
		record("error", "validation_failed")
		return
	}
	if err := ValidateObject(req.Input, capability.InputSchema); err != nil {
		record("error", "validation_failed")
		writeError(w, r, http.StatusBadRequest, "validation_failed", err.Error(), false)
		return
	}
	response, err := s.handlers[capabilityID](r.Context(), req)
	if err != nil {
		if errors.Is(err, ErrValidation) {
			record("error", "validation_failed")
			writeError(w, r, http.StatusBadRequest, "validation_failed", err.Error(), false)
			return
		}
		if errors.Is(err, ErrNotFound) {
			record("error", "not_found")
			writeError(w, r, http.StatusNotFound, "not_found", err.Error(), false)
			return
		}
		record("error", "provider_unavailable")
		writeError(w, r, http.StatusInternalServerError, "provider_unavailable", err.Error(), true)
		return
	}
	if err := ValidateObject(response.Output, capability.OutputSchema); err != nil {
		record("error", "validation_failed")
		writeError(w, r, http.StatusInternalServerError, "validation_failed", "provider output failed schema validation: "+err.Error(), false)
		return
	}
	record("success", "")
	writeSuccess(w, r, http.StatusOK, response)
}

func (s *Server) capability(capabilityID string) (contracts.Capability, bool) {
	for _, capability := range s.manifest.Capabilities {
		if capability.ID == capabilityID {
			return capability, true
		}
	}
	return contracts.Capability{}, false
}

type providerInvocationMetrics struct {
	mu      sync.RWMutex
	buckets map[providerInvocationKey]*providerInvocationBucket
}

type providerInvocationKey struct {
	CapabilityID string
	Status       string
	ErrorCode    string
}

type providerInvocationBucket struct {
	Count           int
	DurationSeconds float64
}

func newProviderInvocationMetrics() *providerInvocationMetrics {
	return &providerInvocationMetrics{buckets: map[providerInvocationKey]*providerInvocationBucket{}}
}

func (m *providerInvocationMetrics) Observe(capabilityID, status, errorCode string, duration time.Duration) {
	if m == nil {
		return
	}
	capabilityID = strings.TrimSpace(capabilityID)
	if capabilityID == "" {
		capabilityID = "unknown"
	}
	status = strings.TrimSpace(status)
	if status == "" {
		status = "unknown"
	}
	key := providerInvocationKey{
		CapabilityID: capabilityID,
		Status:       status,
		ErrorCode:    strings.TrimSpace(errorCode),
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket := m.buckets[key]
	if bucket == nil {
		bucket = &providerInvocationBucket{}
		m.buckets[key] = bucket
	}
	bucket.Count++
	bucket.DurationSeconds += duration.Seconds()
}

func (m *providerInvocationMetrics) Samples(serviceID string) []contracts.MetricSample {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := make([]providerInvocationKey, 0, len(m.buckets))
	for key := range m.buckets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].CapabilityID != keys[j].CapabilityID {
			return keys[i].CapabilityID < keys[j].CapabilityID
		}
		if keys[i].Status != keys[j].Status {
			return keys[i].Status < keys[j].Status
		}
		return keys[i].ErrorCode < keys[j].ErrorCode
	})

	samples := make([]contracts.MetricSample, 0, len(keys)*3)
	for _, key := range keys {
		bucket := m.buckets[key]
		labels := map[string]string{
			"service_id":    serviceID,
			"capability_id": key.CapabilityID,
			"status":        key.Status,
		}
		if key.ErrorCode != "" {
			labels["error_code"] = key.ErrorCode
		}
		samples = append(samples, contracts.CountMetric("provider_invocations_total", bucket.Count, labels))
		if key.Status != "success" {
			samples = append(samples, contracts.CountMetric("provider_invocation_errors_total", bucket.Count, labels))
		}
		if bucket.Count > 0 {
			samples = append(samples, contracts.GaugeMetric("provider_invocation_duration_seconds_avg", bucket.DurationSeconds/float64(bucket.Count), "seconds", labels))
		}
	}
	return samples
}

func ValidateObject(value map[string]any, schema map[string]any) error {
	if schema == nil {
		return nil
	}
	if schemaType, _ := schema["type"].(string); schemaType != "" && schemaType != "object" {
		return fmt.Errorf("%w: only object schemas are supported", ErrValidation)
	}
	for _, required := range stringSlice(schema["required"]) {
		if _, ok := value[required]; !ok {
			return fmt.Errorf("%w: %s is required", ErrValidation, required)
		}
	}
	properties, _ := schema["properties"].(map[string]any)
	for key, rawProperty := range properties {
		property, _ := rawProperty.(map[string]any)
		expected, _ := property["type"].(string)
		if expected == "" {
			continue
		}
		actual, exists := value[key]
		if !exists || actual == nil {
			continue
		}
		if !matchesJSONType(actual, expected) {
			return fmt.Errorf("%w: %s must be %s", ErrValidation, key, expected)
		}
	}
	return nil
}

func stringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func matchesJSONType(value any, expected string) bool {
	switch expected {
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer":
		switch value.(type) {
		case int, int64, float64:
			if number, ok := value.(float64); ok {
				return number == float64(int64(number))
			}
			return true
		default:
			return false
		}
	case "number":
		switch value.(type) {
		case int, int64, float64:
			return true
		default:
			return false
		}
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	default:
		return true
	}
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
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = "req_provider"
	}
	return map[string]string{"request_id": requestID, "schema_version": "v1"}
}
