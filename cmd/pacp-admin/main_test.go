package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHealthChecksCoreServicesWithComponentToken(t *testing.T) {
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = true
		if r.URL.Path != "/v1/gateway/health" && r.Header.Get("Authorization") != "Bearer component-token" {
			t.Fatalf("%s Authorization = %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Request-ID") != "req_admin_trace" {
			t.Fatalf("%s X-Request-ID = %q", r.URL.Path, r.Header.Get("X-Request-ID"))
		}
		writeHealth(t, w, http.StatusOK, "healthy")
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run(append(coreURLArgs(server.URL), "-component-token", "component-token", "-request-id", "req_admin_trace", "health"), &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	for _, path := range []string{
		"/v1/catalog/health",
		"/v1/jobs/health",
		"/v1/leases/health",
		"/v1/artifacts/health",
		"/v1/policy/health",
		"/v1/gateway/health",
	} {
		if !seen[path] {
			t.Fatalf("path %s was not checked; seen=%#v", path, seen)
		}
	}
	report := decodeReport(t, stdout.Bytes())
	if !report.OK || report.Data.Summary.Healthy != 6 || report.Data.Summary.Skipped != 1 {
		t.Fatalf("report = %#v", report)
	}
	if report.Meta["request_id"] != "req_admin_trace" {
		t.Fatalf("meta = %#v", report.Meta)
	}
}

func TestHealthFailsWhenRequiredServiceIsUnhealthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/jobs/health" {
			writeHealth(t, w, http.StatusServiceUnavailable, "unhealthy")
			return
		}
		writeHealth(t, w, http.StatusOK, "healthy")
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run(append(coreURLArgs(server.URL), "health"), &stdout, &stderr, server.Client())
	if code != 1 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	report := decodeReport(t, stdout.Bytes())
	if report.OK || report.Data.Summary.Unhealthy != 1 {
		t.Fatalf("report = %#v", report)
	}
}

func TestHealthChecksConfiguredNodeWithNodeToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/node/health" {
			if r.Header.Get("Authorization") != "Bearer node-token" {
				t.Fatalf("node Authorization = %q", r.Header.Get("Authorization"))
			}
			writeHealth(t, w, http.StatusOK, "healthy")
			return
		}
		writeHealth(t, w, http.StatusOK, "healthy")
	}))
	defer server.Close()

	args := append(coreURLArgs(server.URL), "-node-url", server.URL, "-node-token", "node-token", "health")
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	report := decodeReport(t, stdout.Bytes())
	if report.Data.Summary.Healthy != 7 || report.Data.Summary.Skipped != 0 {
		t.Fatalf("report = %#v", report)
	}
}

func TestHealthChecksMultipleConfiguredNodesWithNodeToken(t *testing.T) {
	nodeHealthHits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/node/health" {
			nodeHealthHits++
			if r.Header.Get("Authorization") != "Bearer node-token" {
				t.Fatalf("node Authorization = %q", r.Header.Get("Authorization"))
			}
			writeHealth(t, w, http.StatusOK, "healthy")
			return
		}
		writeHealth(t, w, http.StatusOK, "healthy")
	}))
	defer server.Close()

	args := append(coreURLArgs(server.URL),
		"-node-urls", "linux="+server.URL+",mac="+server.URL,
		"-node-token", "node-token",
		"health",
	)
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if nodeHealthHits != 2 {
		t.Fatalf("node health hits = %d", nodeHealthHits)
	}
	report := decodeReport(t, stdout.Bytes())
	if report.Data.Summary.Healthy != 8 || report.Data.Summary.Skipped != 0 {
		t.Fatalf("report = %#v", report)
	}
	if !hasHealthItem(report, "node:linux") || !hasHealthItem(report, "node:mac") {
		t.Fatalf("node targets missing from report: %#v", report.Data.Items)
	}
}

func TestHealthFailsForConfiguredUnhealthyNode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/node/health" {
			writeHealth(t, w, http.StatusUnauthorized, "unhealthy")
			return
		}
		writeHealth(t, w, http.StatusOK, "healthy")
	}))
	defer server.Close()

	args := append(coreURLArgs(server.URL), "-node-url", server.URL, "health")
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, server.Client())
	if code != 1 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	report := decodeReport(t, stdout.Bytes())
	if report.OK || report.Data.Summary.Unhealthy != 1 {
		t.Fatalf("report = %#v", report)
	}
}

func TestHealthCanCheckCatalogProviderHealth(t *testing.T) {
	providerHealthHits := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/provider/health" {
			t.Fatalf("provider request = %s %s", r.Method, r.URL.Path)
		}
		providerHealthHits++
		writeHealth(t, w, http.StatusOK, "healthy")
	}))
	defer provider.Close()

	sawCatalogCapabilities := false
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/catalog/capabilities" {
			sawCatalogCapabilities = true
			if r.Header.Get("Authorization") != "Bearer component-token" {
				t.Fatalf("catalog capabilities Authorization = %q", r.Header.Get("Authorization"))
			}
			writeEnvelope(t, w, http.StatusOK, map[string]any{
				"items": []map[string]any{{
					"route": map[string]any{
						"capability_id":        "cap_search",
						"service_id":           "svc_search",
						"provider_endpoint":    provider.URL,
						"provider_health_path": "/v1/provider/health",
					},
					"service": map[string]any{"id": "svc_search"},
				}},
			})
			return
		}
		writeHealth(t, w, http.StatusOK, "healthy")
	}))
	defer catalog.Close()

	args := append(coreURLArgs(catalog.URL), "-component-token", "component-token", "health", "-providers")
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, catalog.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !sawCatalogCapabilities {
		t.Fatal("catalog capabilities were not requested")
	}
	if providerHealthHits != 1 {
		t.Fatalf("provider health hits = %d", providerHealthHits)
	}
	report := decodeReport(t, stdout.Bytes())
	if report.Data.Summary.Healthy != 7 || report.Data.Summary.Skipped != 1 {
		t.Fatalf("report = %#v", report)
	}
	providerItem := findHealthItem(report, "provider:svc_search")
	if providerItem == nil || providerItem.Kind != "provider" || providerItem.ServiceID != "svc_search" {
		t.Fatalf("provider item = %#v", providerItem)
	}
}

func TestHealthFailsWhenCatalogProviderIsUnhealthy(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeHealth(t, w, http.StatusServiceUnavailable, "unhealthy")
	}))
	defer provider.Close()

	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/catalog/capabilities" {
			writeEnvelope(t, w, http.StatusOK, map[string]any{
				"items": []map[string]any{{
					"route": map[string]any{
						"capability_id":        "cap_search",
						"service_id":           "svc_search",
						"provider_endpoint":    provider.URL,
						"provider_health_path": "/v1/provider/health",
					},
				}},
			})
			return
		}
		writeHealth(t, w, http.StatusOK, "healthy")
	}))
	defer catalog.Close()

	var stdout, stderr bytes.Buffer
	code := run(append(coreURLArgs(catalog.URL), "health", "-providers"), &stdout, &stderr, catalog.Client())
	if code != 1 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	report := decodeReport(t, stdout.Bytes())
	if report.OK || report.Data.Summary.Unhealthy != 1 {
		t.Fatalf("report = %#v", report)
	}
	providerItem := findHealthItem(report, "provider:svc_search")
	if providerItem == nil || providerItem.Status != "unhealthy" || providerItem.HTTPStatus != http.StatusServiceUnavailable {
		t.Fatalf("provider item = %#v", providerItem)
	}
}

func TestMetricsCollectsCoreServicesAndConfiguredNode(t *testing.T) {
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = true
		switch r.URL.Path {
		case "/v1/gateway/metrics":
			if r.Header.Get("Authorization") != "" {
				t.Fatalf("gateway Authorization = %q", r.Header.Get("Authorization"))
			}
			writeMetrics(t, w, http.StatusOK, "gateway", []map[string]any{{
				"name":   "gateway_idempotency_records_total",
				"value":  0,
				"unit":   "count",
				"labels": map[string]string{},
			}})
		case "/v1/node/metrics":
			if r.Header.Get("Authorization") != "Bearer node-token" {
				t.Fatalf("node Authorization = %q", r.Header.Get("Authorization"))
			}
			writeMetrics(t, w, http.StatusOK, "node", []map[string]any{{
				"name":  "node_services_total",
				"value": 1,
				"unit":  "count",
				"labels": map[string]string{
					"node_id": "linux",
				},
			}})
		default:
			if r.Header.Get("Authorization") != "Bearer component-token" {
				t.Fatalf("%s Authorization = %q", r.URL.Path, r.Header.Get("Authorization"))
			}
			writeMetrics(t, w, http.StatusOK, strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/"), "/metrics"), []map[string]any{{
				"name":  "sample_total",
				"value": 1,
				"unit":  "count",
			}})
		}
	}))
	defer server.Close()

	args := append(coreURLArgs(server.URL),
		"-component-token", "component-token",
		"-node-urls", "linux="+server.URL,
		"-node-token", "node-token",
		"metrics",
	)
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	for _, path := range []string{
		"/v1/catalog/metrics",
		"/v1/jobs/metrics",
		"/v1/leases/metrics",
		"/v1/artifacts/metrics",
		"/v1/policy/metrics",
		"/v1/gateway/metrics",
		"/v1/node/metrics",
	} {
		if !seen[path] {
			t.Fatalf("path %s was not collected; seen=%#v", path, seen)
		}
	}
	report := decodeMetricsReport(t, stdout.Bytes())
	if !report.OK || report.Data.Summary.Available != 7 || report.Data.Summary.Skipped != 0 {
		t.Fatalf("report = %#v", report)
	}
	if report.Data.Summary.Samples != 7 {
		t.Fatalf("sample count = %d", report.Data.Summary.Samples)
	}
}

func TestHealthIncludesConfiguredRunnerMonitor(t *testing.T) {
	seenRunner := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runner/health" {
			seenRunner = true
			if r.Header.Get("Authorization") != "Bearer runner-token" {
				t.Fatalf("runner Authorization = %q", r.Header.Get("Authorization"))
			}
			writeHealth(t, w, http.StatusOK, "healthy")
			return
		}
		writeHealth(t, w, http.StatusOK, "healthy")
	}))
	defer server.Close()

	args := append(coreURLArgs(server.URL),
		"-runner-url", server.URL,
		"-runner-token", "runner-token",
		"health",
	)
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !seenRunner {
		t.Fatal("runner health was not checked")
	}
	report := decodeReport(t, stdout.Bytes())
	item := findHealthItem(report, "runner")
	if item == nil || item.Kind != "runner" || item.Status != "healthy" {
		t.Fatalf("runner item = %#v", item)
	}
}

func TestMetricsIncludesConfiguredRunnerMonitor(t *testing.T) {
	seenRunner := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runner/metrics" {
			seenRunner = true
			if r.Header.Get("Authorization") != "Bearer runner-token" {
				t.Fatalf("runner Authorization = %q", r.Header.Get("Authorization"))
			}
			writeMetrics(t, w, http.StatusOK, "runner", []map[string]any{{
				"name":  "runner_active_jobs",
				"value": 1,
				"unit":  "count",
			}})
			return
		}
		writeMetrics(t, w, http.StatusOK, "component", []map[string]any{})
	}))
	defer server.Close()

	args := append(coreURLArgs(server.URL),
		"-runner-url", server.URL,
		"-runner-token", "runner-token",
		"metrics",
	)
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !seenRunner {
		t.Fatal("runner metrics were not collected")
	}
	report := decodeMetricsReport(t, stdout.Bytes())
	found := false
	for _, item := range report.Data.Items {
		if item.Name == "runner" && item.Kind == "runner" && item.Component == "runner" {
			found = true
		}
	}
	if !found {
		t.Fatalf("runner metrics item missing: %#v", report.Data.Items)
	}
}

func TestMetricsFailsWhenRequiredComponentUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/jobs/metrics" {
			writeEnvelope(t, w, http.StatusServiceUnavailable, map[string]any{"status": "down"})
			return
		}
		writeMetrics(t, w, http.StatusOK, "component", []map[string]any{})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run(append(coreURLArgs(server.URL), "metrics"), &stdout, &stderr, server.Client())
	if code != 1 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	report := decodeMetricsReport(t, stdout.Bytes())
	if report.OK || report.Data.Summary.Unavailable != 1 {
		t.Fatalf("report = %#v", report)
	}
}

func TestAlertsReportsHealthAndMetricFindings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/health") {
			if r.URL.Path == "/v1/policy/health" {
				writeHealth(t, w, http.StatusServiceUnavailable, "unhealthy")
				return
			}
			writeHealth(t, w, http.StatusOK, "healthy")
			return
		}
		switch r.URL.Path {
		case "/v1/jobs/metrics":
			writeMetrics(t, w, http.StatusOK, "jobs", []map[string]any{
				{"name": "jobs_by_state", "value": 2, "unit": "count", "labels": map[string]string{"state": "failed"}},
				{"name": "jobs_by_state", "value": 1, "unit": "count", "labels": map[string]string{"state": "queued"}},
				{"name": "jobs_expired_claims", "value": 1, "unit": "count"},
			})
		case "/v1/leases/metrics":
			writeMetrics(t, w, http.StatusOK, "leases", []map[string]any{
				{"name": "lease_queue_depth", "value": 3, "unit": "count", "labels": map[string]string{"selector": "gpu"}},
			})
		case "/v1/artifacts/metrics":
			writeMetrics(t, w, http.StatusOK, "artifacts", []map[string]any{
				{"name": "artifact_uploads_by_state", "value": 1, "unit": "count", "labels": map[string]string{"state": "expired"}},
			})
		case "/v1/policy/metrics":
			writeMetrics(t, w, http.StatusOK, "policy", []map[string]any{
				{"name": "policy_decisions_total", "value": 2, "unit": "count", "labels": map[string]string{"action": "tool.invoke", "decision": "deny"}},
			})
		case "/v1/node/metrics":
			writeMetrics(t, w, http.StatusOK, "node", []map[string]any{
				{"name": "node_services_by_status", "value": 1, "unit": "count", "labels": map[string]string{"status": "failed"}},
			})
		case "/v1/gateway/metrics":
			writeMetrics(t, w, http.StatusOK, "gateway", []map[string]any{
				{"name": "gateway_downstream_configured", "value": 1, "unit": "count", "labels": map[string]string{"downstream": "catalog", "required": "true", "status": "unreachable"}},
				{"name": "gateway_downstream_reachable", "value": 0, "unit": "boolean", "labels": map[string]string{"downstream": "catalog", "required": "true", "status": "unreachable"}},
			})
		default:
			writeMetrics(t, w, http.StatusOK, "component", []map[string]any{})
		}
	}))
	defer server.Close()

	args := append(coreURLArgs(server.URL),
		"-node-url", server.URL,
		"alerts",
		"-queue-depth-threshold", "2",
	)
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, server.Client())
	if code != 1 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	output := stdout.String()
	for _, expected := range []string{
		`"code": "target_unhealthy"`,
		`"code": "jobs_failed"`,
		`"code": "jobs_queued"`,
		`"code": "jobs_expired_claims"`,
		`"code": "lease_queue_depth"`,
		`"code": "artifact_uploads_not_completed"`,
		`"code": "policy_denies"`,
		`"code": "node_services_failed"`,
		`"code": "gateway_downstream_unreachable"`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("stdout missing %q:\n%s", expected, output)
		}
	}
	var report alertsReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode alerts: %v\n%s", err, output)
	}
	if report.OK || report.Data.Summary.Errors < 2 || report.Data.Summary.Warnings < 4 {
		t.Fatalf("report = %#v", report)
	}
}

func TestAlertsCanIncludeProviderHealthFindings(t *testing.T) {
	providerHealthHits := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/provider/health" {
			t.Fatalf("provider request = %s %s", r.Method, r.URL.Path)
		}
		providerHealthHits++
		writeHealth(t, w, http.StatusServiceUnavailable, "unhealthy")
	}))
	defer provider.Close()

	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/catalog/capabilities":
			if r.Header.Get("Authorization") != "Bearer component-token" {
				t.Fatalf("catalog capabilities Authorization = %q", r.Header.Get("Authorization"))
			}
			writeEnvelope(t, w, http.StatusOK, map[string]any{
				"items": []map[string]any{{
					"route": map[string]any{
						"capability_id":        "cap_search",
						"service_id":           "svc_search",
						"provider_endpoint":    provider.URL,
						"provider_health_path": "/v1/provider/health",
					},
					"service": map[string]any{"id": "svc_search"},
				}},
			})
		case strings.HasSuffix(r.URL.Path, "/health"):
			writeHealth(t, w, http.StatusOK, "healthy")
		case strings.HasSuffix(r.URL.Path, "/metrics"):
			writeMetrics(t, w, http.StatusOK, "component", []map[string]any{})
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer catalog.Close()

	var stdout, stderr bytes.Buffer
	code := run(append(coreURLArgs(catalog.URL), "-component-token", "component-token", "alerts", "-providers"), &stdout, &stderr, catalog.Client())
	if code != 1 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if providerHealthHits != 1 {
		t.Fatalf("provider health hits = %d", providerHealthHits)
	}
	output := stdout.String()
	for _, expected := range []string{
		`"code": "target_unhealthy"`,
		`"name": "provider:svc_search"`,
		`"kind": "provider"`,
		`"service_id": "svc_search"`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("stdout missing %q:\n%s", expected, output)
		}
	}
	var report alertsReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode alerts: %v\n%s", err, output)
	}
	if report.OK || report.Data.Summary.Errors == 0 {
		t.Fatalf("report = %#v", report)
	}
}

func TestAlertsReportsStaleRunnerHeartbeat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/health") {
			writeHealth(t, w, http.StatusOK, "healthy")
			return
		}
		if r.URL.Path == "/v1/runner/metrics" {
			writeMetrics(t, w, http.StatusOK, "runner", []map[string]any{
				{"name": "runner_active_jobs", "value": 1, "unit": "count"},
				{"name": "runner_last_successful_heartbeat_unix_seconds", "value": 1, "unit": "seconds"},
			})
			return
		}
		writeMetrics(t, w, http.StatusOK, "component", []map[string]any{})
	}))
	defer server.Close()

	args := append(coreURLArgs(server.URL),
		"-runner-url", server.URL,
		"alerts",
		"-runner-heartbeat-stale-after", "1s",
	)
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	output := stdout.String()
	if !strings.Contains(output, `"code": "runner_heartbeat_stale"`) {
		t.Fatalf("stdout missing runner_heartbeat_stale:\n%s", output)
	}
}

func TestCatalogRouteCommandUsesComponentToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/catalog/capabilities/cap_1/route" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer component-token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Request-ID") != "req_catalog_trace" {
			t.Fatalf("X-Request-ID = %q", r.Header.Get("X-Request-ID"))
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{"capability_id": "cap_1"})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-catalog-url", server.URL,
		"-component-token", "component-token",
		"-request-id", "req_catalog_trace",
		"catalog", "route", "cap_1",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"capability_id": "cap_1"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestCatalogImportCommandPostsManifestWithComponentToken(t *testing.T) {
	manifestPath := writeAdminTestManifest(t, t.TempDir(), "svc_admin_import", "cap_admin_import")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/catalog/manifests" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer component-token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		var manifest map[string]any
		if err := json.NewDecoder(r.Body).Decode(&manifest); err != nil {
			t.Fatalf("decode manifest: %v", err)
		}
		service := manifest["service"].(map[string]any)
		if service["id"] != "svc_admin_import" {
			t.Fatalf("service id = %#v", service["id"])
		}
		writeEnvelope(t, w, http.StatusCreated, map[string]any{"service_id": "svc_admin_import", "capability_ids": []string{"cap_admin_import"}})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-catalog-url", server.URL,
		"-component-token", "component-token",
		"catalog", "import", manifestPath,
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"service_id": "svc_admin_import"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestCatalogImportCommandLoadsManifestDirectory(t *testing.T) {
	dir := t.TempDir()
	writeAdminTestManifest(t, dir, "svc_admin_import_one", "cap_admin_import_one")
	writeAdminTestManifest(t, dir, "svc_admin_import_two", "cap_admin_import_two")
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/catalog/manifests" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var manifest map[string]any
		if err := json.NewDecoder(r.Body).Decode(&manifest); err != nil {
			t.Fatalf("decode manifest: %v", err)
		}
		service := manifest["service"].(map[string]any)
		serviceID := service["id"].(string)
		seen[serviceID] = true
		writeEnvelope(t, w, http.StatusCreated, map[string]any{"service_id": serviceID})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-catalog-url", server.URL,
		"catalog", "import", dir,
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !seen["svc_admin_import_one"] || !seen["svc_admin_import_two"] {
		t.Fatalf("seen imports = %#v", seen)
	}
}

func TestNodeServicesCommandUsesNodeToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/node/services" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer node-token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{"items": []any{}})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-node-url", server.URL,
		"-node-token", "node-token",
		"node", "services",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"items": []`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestNodeEventsCommandUsesNodeToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/node/events" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer node-token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{"items": []map[string]any{{
			"event_id":   "node_evt_000001",
			"event_type": "start",
			"service_id": "svc_gpu",
		}}})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-node-url", server.URL,
		"-node-token", "node-token",
		"node", "events",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"event_id": "node_evt_000001"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func writeAdminTestManifest(t *testing.T, dir, serviceID, capabilityID string) string {
	t.Helper()
	manifest := map[string]any{
		"schema_version": "v1",
		"service": map[string]any{
			"id":            serviceID,
			"name":          serviceID,
			"description":   "Admin import test provider.",
			"version":       "v1",
			"provider_kind": "test",
		},
		"provider": map[string]any{
			"endpoint": "http://provider.invalid",
		},
		"capabilities": []map[string]any{{
			"id":             capabilityID,
			"name":           capabilityID,
			"description":    "Admin import test capability.",
			"execution_mode": "sync",
			"input_schema":   map[string]any{"type": "object"},
			"output_schema":  map[string]any{"type": "object"},
			"examples":       []any{},
			"side_effects":   "none",
			"timeout_hint":   "30s",
		}},
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	path := filepath.Join(dir, serviceID+".json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

func TestJobsCancelCommandUsesGatewayTokenAndIdempotencyKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/agent/jobs/job_1/cancel" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer gateway-token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Idempotency-Key") != "cancel-1" {
			t.Fatalf("Idempotency-Key = %q", r.Header.Get("Idempotency-Key"))
		}
		if r.Header.Get("X-Request-ID") != "req_cancel_trace" {
			t.Fatalf("X-Request-ID = %q", r.Header.Get("X-Request-ID"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["reason"] != "stop running" {
			t.Fatalf("reason = %#v", body["reason"])
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{"job_id": "job_1", "state": "canceled"})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-gateway-url", server.URL,
		"-gateway-token", "gateway-token",
		"-request-id", "req_cancel_trace",
		"jobs", "cancel", "job_1", "-idempotency-key", "cancel-1", "-reason", "stop running",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"state": "canceled"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestJobsCancelRequiresGatewayToken(t *testing.T) {
	t.Setenv("PACP_GATEWAY_TOKEN", "")
	t.Setenv("PACP_AGENT_TOKEN", "")

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-gateway-url", "http://gateway.invalid",
		"jobs", "cancel", "job_1", "-idempotency-key", "cancel-1",
	}, &stdout, &stderr, http.DefaultClient)
	if code != 2 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "gateway-token is required") {
		t.Fatalf("stderr = %s", stderr.String())
	}
}

func TestDiagnoseJobFetchesJobAndLogsWithComponentToken(t *testing.T) {
	now := time.Now().UTC()
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = true
		switch r.URL.Path {
		case "/v1/jobs/job_1":
			if r.Header.Get("Authorization") != "Bearer component-token" {
				t.Fatalf("%s Authorization = %q", r.URL.Path, r.Header.Get("Authorization"))
			}
			writeEnvelope(t, w, http.StatusOK, map[string]any{
				"job_id":     "job_1",
				"state":      "running",
				"created_at": now.Add(-time.Minute).Format(time.RFC3339),
				"updated_at": now.Format(time.RFC3339),
				"claim": map[string]any{
					"worker_id":  "runner_1",
					"claimed_at": now.Add(-time.Minute).Format(time.RFC3339),
					"expires_at": now.Add(time.Minute).Format(time.RFC3339),
				},
				"metadata": map[string]any{
					"execution_plan": map[string]any{
						"resource_selector": "gpu",
						"route": map[string]any{
							"node_managed": true,
							"node_id":      "node_linux_gpu",
							"service_id":   "svc_comfyui_gpu",
						},
					},
				},
				"artifact_refs": []any{"art_1"},
				"links":         map[string]any{},
			})
		case "/v1/jobs/job_1/logs":
			if r.Header.Get("Authorization") != "Bearer component-token" {
				t.Fatalf("%s Authorization = %q", r.URL.Path, r.Header.Get("Authorization"))
			}
			if r.URL.Query().Get("limit") != "20" {
				t.Fatalf("logs query = %s", r.URL.RawQuery)
			}
			writeEnvelope(t, w, http.StatusOK, map[string]any{
				"items": []map[string]any{{
					"timestamp": now.Format(time.RFC3339),
					"level":     "info",
					"message":   "running provider invocation",
				}},
				"next_cursor": nil,
			})
		case "/v1/resources":
			if r.Header.Get("Authorization") != "Bearer component-token" {
				t.Fatalf("%s Authorization = %q", r.URL.Path, r.Header.Get("Authorization"))
			}
			if r.URL.Query().Get("selector") != "gpu" {
				t.Fatalf("resources query = %s", r.URL.RawQuery)
			}
			writeEnvelope(t, w, http.StatusOK, map[string]any{
				"items": []map[string]any{{
					"resource_id":  "res_gpu_0",
					"selector":     "gpu",
					"display_name": "GPU 0",
					"status":       "available",
					"node_id":      "node_linux_gpu",
					"links":        map[string]any{},
				}},
				"next_cursor": nil,
			})
		case "/v1/resources/res_gpu_0/inspection":
			if r.Header.Get("Authorization") != "Bearer component-token" {
				t.Fatalf("%s Authorization = %q", r.URL.Path, r.Header.Get("Authorization"))
			}
			writeEnvelope(t, w, http.StatusOK, map[string]any{
				"resource": map[string]any{
					"resource_id": "res_gpu_0",
					"selector":    "gpu",
					"status":      "available",
					"node_id":     "node_linux_gpu",
				},
				"active_lease": nil,
				"queue_length": 1,
				"queue": []map[string]any{{
					"request_id":     "lease_req_1",
					"requester_id":   "job_1",
					"priority":       0,
					"queue_position": 1,
				}},
			})
		case "/v1/node/services/svc_comfyui_gpu":
			if r.Header.Get("Authorization") != "Bearer node-token" {
				t.Fatalf("%s Authorization = %q", r.URL.Path, r.Header.Get("Authorization"))
			}
			writeEnvelope(t, w, http.StatusOK, map[string]any{
				"service_id":        "svc_comfyui_gpu",
				"status":            "running",
				"runtime_adapter":   "fake",
				"provider_endpoint": "http://node-linux:8188",
				"links":             map[string]any{},
			})
		case "/v1/artifacts/art_1":
			if r.Header.Get("Authorization") != "Bearer component-token" {
				t.Fatalf("%s Authorization = %q", r.URL.Path, r.Header.Get("Authorization"))
			}
			writeEnvelope(t, w, http.StatusOK, map[string]any{
				"artifact_id":      "art_1",
				"name":             "output.png",
				"media_type":       "image/png",
				"size":             68,
				"checksum":         "sha256:abc",
				"created_at":       now.Format(time.RFC3339),
				"producer_ref":     "job_1",
				"owner_subject_id": "sub_agent",
				"links":            map[string]any{},
			})
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-jobs-url", server.URL,
		"-leases-url", server.URL,
		"-artifacts-url", server.URL,
		"-node-urls", "node_linux_gpu=" + server.URL,
		"-node-token", "node-token",
		"-component-token", "component-token",
		"diagnose", "job", "job_1",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !seen["/v1/jobs/job_1"] || !seen["/v1/jobs/job_1/logs"] {
		t.Fatalf("seen requests = %#v", seen)
	}
	if !seen["/v1/resources"] || !seen["/v1/resources/res_gpu_0/inspection"] || !seen["/v1/node/services/svc_comfyui_gpu"] {
		t.Fatalf("seen requests = %#v", seen)
	}
	if !seen["/v1/artifacts/art_1"] {
		t.Fatalf("seen requests = %#v", seen)
	}
	output := stdout.String()
	for _, expected := range []string{
		`"admin_command": "diagnose job"`,
		`"code": "job_running"`,
		`"code": "resource_selector"`,
		`"code": "node_managed_route"`,
		`"code": "lease_resources_found"`,
		`"code": "resource_queue_depth"`,
		`"code": "node_service_status"`,
		`"code": "artifact_metadata_available"`,
		`"lease_resources"`,
		`"lease_inspections"`,
		`"node_service"`,
		`"artifact_metadata"`,
		`"check node health and service status"`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("stdout missing %q:\n%s", expected, output)
		}
	}
}

func TestDiagnoseJobExplainsArtifactUploadFailure(t *testing.T) {
	now := time.Now().UTC()
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = true
		if r.Header.Get("Authorization") != "Bearer component-token" {
			t.Fatalf("%s Authorization = %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/v1/jobs/job_artifact_failed":
			writeEnvelope(t, w, http.StatusOK, map[string]any{
				"job_id":     "job_artifact_failed",
				"state":      "failed",
				"created_at": now.Add(-time.Minute).Format(time.RFC3339),
				"updated_at": now.Format(time.RFC3339),
				"terminal_error": map[string]any{
					"code":      "artifact_upload_failed",
					"message":   "artifact upload content failed: artifact content backend unavailable",
					"retryable": true,
				},
				"artifact_refs": []any{},
				"links":         map[string]any{},
			})
		case "/v1/jobs/job_artifact_failed/logs":
			if r.URL.Query().Get("limit") != "20" {
				t.Fatalf("logs query = %s", r.URL.RawQuery)
			}
			writeEnvelope(t, w, http.StatusOK, map[string]any{
				"items": []map[string]any{{
					"timestamp": now.Format(time.RFC3339),
					"level":     "error",
					"message":   "artifact materialization failed",
					"fields": map[string]any{
						"code":  "artifact_upload_failed",
						"stage": "content",
					},
				}},
				"next_cursor": nil,
			})
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-jobs-url", server.URL,
		"-component-token", "component-token",
		"diagnose", "job", "job_artifact_failed",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !seen["/v1/jobs/job_artifact_failed"] || !seen["/v1/jobs/job_artifact_failed/logs"] {
		t.Fatalf("seen requests = %#v", seen)
	}
	output := stdout.String()
	for _, expected := range []string{
		`"code": "job_failed"`,
		`"code": "artifact_materialization_failed"`,
		`artifact upload stage content failed`,
		`inspect artifact service health and permissions`,
		`inspect runner logs for artifact upload stage content`,
		`retry after artifact store is healthy`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("stdout missing %q:\n%s", expected, output)
		}
	}
}

func TestDiagnoseResourceFetchesInspectionAndRelatedJobs(t *testing.T) {
	now := time.Now().UTC()
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = true
		if r.Header.Get("Authorization") != "Bearer component-token" {
			t.Fatalf("%s Authorization = %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/v1/resources/res_gpu_0/inspection":
			writeEnvelope(t, w, http.StatusOK, map[string]any{
				"resource": map[string]any{
					"resource_id":  "res_gpu_0",
					"selector":     "gpu",
					"display_name": "GPU 0",
					"status":       "available",
					"node_id":      "node_linux_gpu",
					"links":        map[string]any{},
				},
				"active_lease": map[string]any{
					"lease_id":    "lease_1",
					"resource_id": "res_gpu_0",
					"holder_id":   "job_running",
					"expires_at":  now.Add(time.Minute).Format(time.RFC3339),
					"links":       map[string]any{},
				},
				"queue_length": 1,
				"queue": []map[string]any{{
					"request_id":     "lease_req_1",
					"requester_id":   "job_queued",
					"priority":       0,
					"queue_position": 1,
				}},
			})
		case "/v1/jobs/job_running":
			writeEnvelope(t, w, http.StatusOK, map[string]any{
				"job_id":        "job_running",
				"state":         "running",
				"created_at":    now.Add(-time.Minute).Format(time.RFC3339),
				"updated_at":    now.Format(time.RFC3339),
				"artifact_refs": []any{},
				"links":         map[string]any{},
			})
		case "/v1/jobs/job_queued":
			writeEnvelope(t, w, http.StatusOK, map[string]any{
				"job_id":        "job_queued",
				"state":         "queued",
				"created_at":    now.Add(-time.Minute).Format(time.RFC3339),
				"updated_at":    now.Format(time.RFC3339),
				"artifact_refs": []any{},
				"links":         map[string]any{},
			})
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-jobs-url", server.URL,
		"-leases-url", server.URL,
		"-component-token", "component-token",
		"diagnose", "resource", "res_gpu_0",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	for _, path := range []string{"/v1/resources/res_gpu_0/inspection", "/v1/jobs/job_running", "/v1/jobs/job_queued"} {
		if !seen[path] {
			t.Fatalf("missing request %s in %#v", path, seen)
		}
	}
	output := stdout.String()
	for _, expected := range []string{
		`"admin_command": "diagnose resource"`,
		`"code": "resource_available"`,
		`"code": "resource_active_lease"`,
		`"code": "resource_queue_depth"`,
		`"code": "active_holder_job_state"`,
		`"code": "related_job_state"`,
		`"code": "related_jobs_available"`,
		`"related_jobs"`,
		`"job_running"`,
		`"job_queued"`,
		`"diagnose job job_running"`,
		`"diagnose job job_queued"`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("stdout missing %q:\n%s", expected, output)
		}
	}
}

func TestLeasesRegisterResourceCommandPostsResource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/resources" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer component-token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["resource_id"] != "res_gpu_0" || body["selector"] != "gpu" || body["node_id"] != "node_linux_gpu" {
			t.Fatalf("body = %#v", body)
		}
		tags := body["tags"].([]any)
		if len(tags) != 2 || tags[0] != "gpu" || tags[1] != "gpu:0" {
			t.Fatalf("tags = %#v", tags)
		}
		metadata := body["metadata"].(map[string]any)
		if metadata["kind"] != "gpu" {
			t.Fatalf("metadata = %#v", metadata)
		}
		writeEnvelope(t, w, http.StatusCreated, map[string]any{"resource_id": "res_gpu_0", "selector": "gpu"})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-leases-url", server.URL,
		"-component-token", "component-token",
		"leases", "register-resource",
		"-resource-id", "res_gpu_0",
		"-selector", "gpu",
		"-node-id", "node_linux_gpu",
		"-tags", "gpu,gpu:0",
		"-metadata", `{"kind":"gpu"}`,
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"resource_id": "res_gpu_0"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestLeasesCreateRequestCommandPostsRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/lease-requests" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["requester_id"] != "job_1" || body["resource_selector"] != "gpu" {
			t.Fatalf("body = %#v", body)
		}
		if body["priority"].(float64) != 5 || body["heartbeat_timeout_seconds"].(float64) != 30 {
			t.Fatalf("body = %#v", body)
		}
		writeEnvelope(t, w, http.StatusCreated, map[string]any{"request_id": "lease_req_1", "state": "pending"})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-leases-url", server.URL,
		"leases", "create-request",
		"-requester-id", "job_1",
		"-selector", "gpu",
		"-priority", "5",
		"-heartbeat-timeout-seconds", "30",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"request_id": "lease_req_1"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestLeasesRequestsCommandListsByRequester(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/lease-requests" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("requester_id") != "job_1" {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer component-token" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{"items": []any{map[string]any{"request_id": "lease_req_1", "state": "pending"}}})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-leases-url", server.URL,
		"-component-token", "component-token",
		"leases", "requests",
		"-requester-id", "job_1",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"request_id": "lease_req_1"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestLeasesCancelRequestCommandPostsCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/lease-requests/lease_req_1/cancel" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["reason"] != "operator cleanup" {
			t.Fatalf("reason = %#v", body["reason"])
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{"request_id": "lease_req_1", "state": "canceled"})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-leases-url", server.URL,
		"leases", "cancel-request", "lease_req_1", "-reason", "operator cleanup",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"state": "canceled"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestLeasesReleaseCommandPostsReleaseWithAuditActor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/leases/lease_1/release" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer component-token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Idempotency-Key") != "release-1" {
			t.Fatalf("Idempotency-Key = %q", r.Header.Get("Idempotency-Key"))
		}
		if r.Header.Get("X-Actor-Subject-ID") != "sub_admin" {
			t.Fatalf("X-Actor-Subject-ID = %q", r.Header.Get("X-Actor-Subject-ID"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["holder_id"] != "job_1" || body["reason"] != "operator release" {
			t.Fatalf("body = %#v", body)
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{"lease_id": "lease_1", "released_by": "sub_admin"})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-leases-url", server.URL,
		"-component-token", "component-token",
		"leases", "release", "lease_1",
		"-holder-id", "job_1",
		"-idempotency-key", "release-1",
		"-actor-subject-id", "sub_admin",
		"-reason", "operator release",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"released_by": "sub_admin"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestLeasesReleaseRequiresIdempotencyKey(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"leases", "release", "lease_1", "-holder-id", "job_1"}, &stdout, &stderr, http.DefaultClient)
	if code != 2 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "idempotency-key is required") {
		t.Fatalf("stderr = %s", stderr.String())
	}
}

func TestArtifactsCreateUploadCommandPostsUpload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/artifact-uploads" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer component-token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Idempotency-Key") != "upload-create-1" {
			t.Fatalf("Idempotency-Key = %q", r.Header.Get("Idempotency-Key"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["name"] != "image.png" || body["media_type"] != "image/png" || body["owner_subject_id"] != "sub_agent" || body["producer_ref"] != "job_1" {
			t.Fatalf("body = %#v", body)
		}
		if body["expected_size"].(float64) != 42 {
			t.Fatalf("expected_size = %#v", body["expected_size"])
		}
		metadata := body["metadata"].(map[string]any)
		if metadata["kind"] != "preview" {
			t.Fatalf("metadata = %#v", metadata)
		}
		writeEnvelope(t, w, http.StatusCreated, map[string]any{"upload_id": "upload_1", "state": "created"})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-artifacts-url", server.URL,
		"-component-token", "component-token",
		"artifacts", "create-upload",
		"-name", "image.png",
		"-media-type", "image/png",
		"-owner-subject-id", "sub_agent",
		"-producer-ref", "job_1",
		"-expected-size", "42",
		"-metadata", `{"kind":"preview"}`,
		"-idempotency-key", "upload-create-1",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"upload_id": "upload_1"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestArtifactsPutContentCommandUploadsFileWithDigest(t *testing.T) {
	body := "hello artifact"
	filePath := filepath.Join(t.TempDir(), "artifact.txt")
	if err := os.WriteFile(filePath, []byte(body), 0o600); err != nil {
		t.Fatalf("write artifact file: %v", err)
	}
	expectedDigest, err := sha256Digest(strings.NewReader(body))
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/v1/artifact-uploads/upload_1/content" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer component-token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Idempotency-Key") != "upload-content-1" {
			t.Fatalf("Idempotency-Key = %q", r.Header.Get("Idempotency-Key"))
		}
		if r.Header.Get("Content-Type") != "text/plain" {
			t.Fatalf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		if r.ContentLength != int64(len(body)) {
			t.Fatalf("ContentLength = %d", r.ContentLength)
		}
		if r.Header.Get("Digest") != expectedDigest {
			t.Fatalf("Digest = %q want %q", r.Header.Get("Digest"), expectedDigest)
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if string(raw) != body {
			t.Fatalf("body = %q", string(raw))
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{"upload_id": "upload_1", "state": "received"})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-artifacts-url", server.URL,
		"-component-token", "component-token",
		"artifacts", "put-content", "upload_1",
		"-file", filePath,
		"-media-type", "text/plain",
		"-idempotency-key", "upload-content-1",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"state": "received"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestArtifactsPutContentRequiresIdempotencyKey(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-artifacts-url", "http://artifacts.invalid",
		"artifacts", "put-content", "upload_1",
		"-file", "artifact.txt",
		"-media-type", "text/plain",
	}, &stdout, &stderr, http.DefaultClient)
	if code != 2 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "idempotency-key is required") {
		t.Fatalf("stderr = %s", stderr.String())
	}
}

func TestArtifactsCompleteUploadCommandPostsComplete(t *testing.T) {
	body := "complete bytes"
	filePath := filepath.Join(t.TempDir(), "artifact.txt")
	if err := os.WriteFile(filePath, []byte(body), 0o600); err != nil {
		t.Fatalf("write artifact file: %v", err)
	}
	expectedDigest, err := sha256Digest(strings.NewReader(body))
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/artifact-uploads/upload_1/complete" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Idempotency-Key") != "upload-complete-1" {
			t.Fatalf("Idempotency-Key = %q", r.Header.Get("Idempotency-Key"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["checksum"] != expectedDigest || body["size"].(float64) != 14 {
			t.Fatalf("body = %#v", body)
		}
		writeEnvelope(t, w, http.StatusCreated, map[string]any{"artifact_id": "art_1", "checksum": expectedDigest})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-artifacts-url", server.URL,
		"artifacts", "complete-upload", "upload_1",
		"-file", filePath,
		"-idempotency-key", "upload-complete-1",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"artifact_id": "art_1"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestArtifactsRegisterLocalCommandPostsArtifact(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/artifacts/register-local" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer component-token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["path"] != "blobs/artifact.bin" || body["name"] != "artifact.bin" || body["media_type"] != "application/octet-stream" || body["owner_subject_id"] != "sub_admin" {
			t.Fatalf("body = %#v", body)
		}
		metadata := body["metadata"].(map[string]any)
		if metadata["source"] != "operator" {
			t.Fatalf("metadata = %#v", metadata)
		}
		writeEnvelope(t, w, http.StatusCreated, map[string]any{"artifact_id": "art_1", "name": "artifact.bin"})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-artifacts-url", server.URL,
		"-component-token", "component-token",
		"artifacts", "register-local",
		"-path", "blobs/artifact.bin",
		"-name", "artifact.bin",
		"-media-type", "application/octet-stream",
		"-owner-subject-id", "sub_admin",
		"-metadata", `{"source":"operator"}`,
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"artifact_id": "art_1"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestArtifactsRetentionSweepCommandPosts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/artifacts/retention/sweep" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer component-token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"expired_uploads":   1,
			"expired_artifacts": 2,
			"deleted_blobs":     3,
		})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-artifacts-url", server.URL,
		"-component-token", "component-token",
		"artifacts", "retention-sweep",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"deleted_blobs": 3`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestPolicyCreateKeyCommandPostsKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/api-keys" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer component-token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["subject_id"] != "sub_admin" || body["token"] != "token_admin" {
			t.Fatalf("body = %#v", body)
		}
		scopes := body["scopes"].([]any)
		if len(scopes) != 2 || scopes[0] != "admin" || scopes[1] != "component" {
			t.Fatalf("scopes = %#v", scopes)
		}
		writeEnvelope(t, w, http.StatusCreated, map[string]any{"key_id": "key_1", "subject_id": "sub_admin"})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-policy-url", server.URL,
		"-component-token", "component-token",
		"policy", "create-key",
		"-subject-id", "sub_admin",
		"-scopes", "admin,component",
		"-token", "token_admin",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"key_id": "key_1"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestPolicyRevokeKeyCommandPostsRevoke(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/api-keys/key_1/revoke" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{"key_id": "key_1", "revoked_at": "2026-06-07T00:00:00Z"})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"-policy-url", server.URL, "policy", "revoke-key", "key_1"}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"revoked_at": "2026-06-07T00:00:00Z"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestPolicyRotateKeyCommandPostsRotate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/api-keys/key_1/rotate" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer component-token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["token"] != "token_rotated" {
			t.Fatalf("body = %#v", body)
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"key_id":     "key_1",
			"subject_id": "sub_admin",
			"token":      "token_rotated",
			"rotated_at": "2026-06-07T00:00:00Z",
		})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-policy-url", server.URL,
		"-component-token", "component-token",
		"policy", "rotate-key", "key_1",
		"-token", "token_rotated",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"token": "token_rotated"`) || !strings.Contains(stdout.String(), `"rotated_at": "2026-06-07T00:00:00Z"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestPolicyCheckCommandPostsDecisionRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/policy/check" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["subject_id"] != "sub_agent" || body["action"] != "tool.invoke" || body["resource"] != "cap_image" {
			t.Fatalf("body = %#v", body)
		}
		context := body["context"].(map[string]any)
		if context["job_state"] != "queued" {
			t.Fatalf("context = %#v", context)
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{"allowed": true, "reason": "builtin_allow"})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-policy-url", server.URL,
		"policy", "check",
		"-subject-id", "sub_agent",
		"-action", "tool.invoke",
		"-resource", "cap_image",
		"-context", `{"job_state":"queued"}`,
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"allowed": true`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestPolicyCreateRuleCommandPostsRule(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/policy/rules" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["scope"] != "agent" || body["action"] != "tool.invoke" || body["resource"] != "cap_image" || body["effect"] != "allow" {
			t.Fatalf("body = %#v", body)
		}
		writeEnvelope(t, w, http.StatusCreated, map[string]any{"rule_id": "rule_1", "effect": "allow"})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-policy-url", server.URL,
		"policy", "create-rule",
		"-scope", "agent",
		"-action", "tool.invoke",
		"-resource", "cap_image",
		"-effect", "allow",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"rule_id": "rule_1"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestPolicyCreateSecretCommandReadsValueEnv(t *testing.T) {
	t.Setenv("PACP_TEST_SECRET", "super-secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/secrets" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["name"] != "provider_token" || body["value"] != "super-secret" {
			t.Fatalf("body = %#v", body)
		}
		writeEnvelope(t, w, http.StatusCreated, map[string]any{"secret_ref": "secret_1", "name": "provider_token"})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-policy-url", server.URL,
		"policy", "create-secret",
		"-name", "provider_token",
		"-value-env", "PACP_TEST_SECRET",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"secret_ref": "secret_1"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestPolicyRedactCommandPostsText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/redact" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["text"] != "token is super-secret" {
			t.Fatalf("body = %#v", body)
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{"text": "token is [REDACTED]"})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-policy-url", server.URL,
		"policy", "redact",
		"-text", "token is super-secret",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"text": "token is [REDACTED]"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestNodeCommandRequiresNodeURL(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-node-url", "", "node", "services"}, &stdout, &stderr, http.DefaultClient)
	if code != 2 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "node-url is required") {
		t.Fatalf("stderr = %s", stderr.String())
	}
}

func TestNodeStartCommandPostsWithIdempotencyKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/node/services/svc_gpu/start" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer node-token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Idempotency-Key") != "start-1" {
			t.Fatalf("Idempotency-Key = %q", r.Header.Get("Idempotency-Key"))
		}
		writeEnvelope(t, w, http.StatusAccepted, map[string]any{"service_id": "svc_gpu", "status": "starting"})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-node-url", server.URL,
		"-node-token", "node-token",
		"node", "start", "svc_gpu", "-idempotency-key", "start-1",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"status": "starting"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestNodeStartRequiresIdempotencyKey(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-node-url", "http://node.invalid", "node", "start", "svc_gpu"}, &stdout, &stderr, http.DefaultClient)
	if code != 2 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "idempotency-key is required") {
		t.Fatalf("stderr = %s", stderr.String())
	}
}

func TestNodeTouchCommandPostsWithoutIdempotencyKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/node/services/svc_gpu/touch" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer node-token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Idempotency-Key") != "" {
			t.Fatalf("Idempotency-Key = %q", r.Header.Get("Idempotency-Key"))
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{"service_id": "svc_gpu", "status": "running"})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-node-url", server.URL,
		"-node-token", "node-token",
		"node", "touch", "svc_gpu",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"status": "running"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestNodeStopCommandPosts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/node/services/svc_gpu/stop" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer node-token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Idempotency-Key") != "stop-1" {
			t.Fatalf("Idempotency-Key = %q", r.Header.Get("Idempotency-Key"))
		}
		writeEnvelope(t, w, http.StatusAccepted, map[string]any{"service_id": "svc_gpu", "status": "stopped"})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-node-url", server.URL,
		"-node-token", "node-token",
		"node", "stop", "svc_gpu", "-idempotency-key", "stop-1",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"status": "stopped"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestNodeStopRequiresIdempotencyKey(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-node-url", "http://node.invalid", "node", "stop", "svc_gpu"}, &stdout, &stderr, http.DefaultClient)
	if code != 2 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "idempotency-key is required") {
		t.Fatalf("stderr = %s", stderr.String())
	}
}

func coreURLArgs(url string) []string {
	return []string{
		"-catalog-url", url,
		"-jobs-url", url,
		"-leases-url", url,
		"-artifacts-url", url,
		"-policy-url", url,
		"-gateway-url", url,
	}
}

func writeEnvelope(t *testing.T, w http.ResponseWriter, status int, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"ok":    status >= 200 && status < 300,
		"data":  data,
		"links": map[string]any{},
		"meta":  map[string]any{"request_id": "req_test", "schema_version": "v1"},
	}); err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
}

func writeHealth(t *testing.T, w http.ResponseWriter, status int, health string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if status >= 200 && status < 300 {
		if err := json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"data": map[string]any{"status": health},
		}); err != nil {
			t.Fatalf("encode health: %v", err)
		}
		return
	}
	if err := json.NewEncoder(w).Encode(map[string]any{
		"ok":    false,
		"error": map[string]any{"code": "unhealthy", "message": strings.ToUpper(health), "retryable": true},
	}); err != nil {
		t.Fatalf("encode health error: %v", err)
	}
}

func writeMetrics(t *testing.T, w http.ResponseWriter, status int, component string, samples []map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if status >= 200 && status < 300 {
		if err := json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"data": map[string]any{
				"component":    component,
				"version":      "v1",
				"collected_at": "2026-06-08T00:00:00Z",
				"samples":      samples,
			},
			"links": map[string]any{},
			"meta":  map[string]any{"request_id": "req_test", "schema_version": "v1"},
		}); err != nil {
			t.Fatalf("encode metrics: %v", err)
		}
		return
	}
	if err := json.NewEncoder(w).Encode(map[string]any{
		"ok":    false,
		"error": map[string]any{"code": "metrics_unavailable", "message": "metrics unavailable", "retryable": true},
	}); err != nil {
		t.Fatalf("encode metrics error: %v", err)
	}
}

func decodeReport(t *testing.T, raw []byte) healthReport {
	t.Helper()
	var report healthReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, string(raw))
	}
	return report
}

func decodeMetricsReport(t *testing.T, raw []byte) metricsReport {
	t.Helper()
	var report metricsReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("decode metrics report: %v\n%s", err, string(raw))
	}
	return report
}

func findHealthItem(report healthReport, name string) *healthItem {
	for i := range report.Data.Items {
		if report.Data.Items[i].Name == name {
			return &report.Data.Items[i]
		}
	}
	return nil
}

func hasHealthItem(report healthReport, name string) bool {
	return findHealthItem(report, name) != nil
}
