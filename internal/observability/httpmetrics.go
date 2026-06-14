package observability

import (
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"wendy/internal/contracts"
)

type HTTPMetrics struct {
	mu      sync.RWMutex
	buckets map[httpMetricKey]*httpMetricBucket
}

type httpMetricKey struct {
	Method      string
	RouteGroup  string
	StatusClass string
}

type httpMetricBucket struct {
	Count           int
	ErrorCount      int
	DurationSeconds float64
}

type latencyKey struct {
	Method     string
	RouteGroup string
}

type latencyBucket struct {
	Count           int
	DurationSeconds float64
}

func NewHTTPMetrics() *HTTPMetrics {
	return &HTTPMetrics{buckets: map[httpMetricKey]*httpMetricBucket{}}
}

func (m *HTTPMetrics) Record(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	if m == nil {
		next(w, r)
		return
	}
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	start := time.Now()
	next(rec, r)
	m.Observe(r.Method, r.URL.Path, rec.status, time.Since(start))
}

func (m *HTTPMetrics) Observe(method, path string, status int, duration time.Duration) {
	if m == nil {
		return
	}
	key := httpMetricKey{
		Method:      strings.ToUpper(method),
		RouteGroup:  RouteGroup(path),
		StatusClass: statusClass(status),
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket := m.buckets[key]
	if bucket == nil {
		bucket = &httpMetricBucket{}
		m.buckets[key] = bucket
	}
	bucket.Count++
	if status >= 400 {
		bucket.ErrorCount++
	}
	bucket.DurationSeconds += duration.Seconds()
}

func (m *HTTPMetrics) Samples() []contracts.MetricSample {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := make([]httpMetricKey, 0, len(m.buckets))
	for key := range m.buckets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].RouteGroup != keys[j].RouteGroup {
			return keys[i].RouteGroup < keys[j].RouteGroup
		}
		if keys[i].Method != keys[j].Method {
			return keys[i].Method < keys[j].Method
		}
		return keys[i].StatusClass < keys[j].StatusClass
	})

	samples := make([]contracts.MetricSample, 0, len(keys)*3)
	latencies := map[latencyKey]*latencyBucket{}
	for _, key := range keys {
		bucket := m.buckets[key]
		labels := map[string]string{
			"method":       key.Method,
			"route_group":  key.RouteGroup,
			"status_class": key.StatusClass,
		}
		samples = append(samples, contracts.CountMetric("http_requests_total", bucket.Count, labels))
		if bucket.ErrorCount > 0 {
			samples = append(samples, contracts.CountMetric("http_errors_total", bucket.ErrorCount, labels))
		}
		latency := latencyKey{Method: key.Method, RouteGroup: key.RouteGroup}
		if latencies[latency] == nil {
			latencies[latency] = &latencyBucket{}
		}
		latencies[latency].Count += bucket.Count
		latencies[latency].DurationSeconds += bucket.DurationSeconds
	}
	latencyKeys := make([]latencyKey, 0, len(latencies))
	for key := range latencies {
		latencyKeys = append(latencyKeys, key)
	}
	sort.Slice(latencyKeys, func(i, j int) bool {
		if latencyKeys[i].RouteGroup != latencyKeys[j].RouteGroup {
			return latencyKeys[i].RouteGroup < latencyKeys[j].RouteGroup
		}
		return latencyKeys[i].Method < latencyKeys[j].Method
	})
	for _, key := range latencyKeys {
		bucket := latencies[key]
		if bucket.Count == 0 {
			continue
		}
		samples = append(samples, contracts.GaugeMetric("http_request_duration_seconds_avg", bucket.DurationSeconds/float64(bucket.Count), "seconds", map[string]string{
			"method":      key.Method,
			"route_group": key.RouteGroup,
		}))
	}
	return samples
}

func RouteGroup(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		return "/"
	}
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i := 1; i < len(segments); i++ {
		if placeholder, ok := dynamicPlaceholder(segments[i-1], segments[i]); ok {
			segments[i] = placeholder
		}
	}
	return "/" + strings.Join(segments, "/")
}

func dynamicPlaceholder(previous, current string) (string, bool) {
	switch previous {
	case "jobs":
		return "{job_id}", true
	case "tools", "capabilities":
		return "{capability_id}", true
	case "services":
		return "{service_id}", true
	case "resources":
		return "{resource_id}", true
	case "lease-requests":
		return "{request_id}", true
	case "leases":
		return "{lease_id}", true
	case "artifact-uploads":
		return "{upload_id}", true
	case "artifacts":
		if current == "register-local" {
			return "", false
		}
		return "{artifact_id}", true
	case "api-keys":
		return "{key_id}", true
	default:
		return "", false
	}
}

func statusClass(status int) string {
	switch {
	case status >= 100 && status < 200:
		return "1xx"
	case status >= 200 && status < 300:
		return "2xx"
	case status >= 300 && status < 400:
		return "3xx"
	case status >= 400 && status < 500:
		return "4xx"
	case status >= 500 && status < 600:
		return "5xx"
	default:
		return "unknown"
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.wrote {
		return
	}
	r.status = status
	r.wrote = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	if !r.wrote {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(body)
}
