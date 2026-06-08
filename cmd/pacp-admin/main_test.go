package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHealthChecksCoreServicesWithComponentToken(t *testing.T) {
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = true
		if r.URL.Path != "/v1/gateway/health" && r.Header.Get("Authorization") != "Bearer component-token" {
			t.Fatalf("%s Authorization = %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		writeHealth(t, w, http.StatusOK, "healthy")
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run(append(coreURLArgs(server.URL), "-component-token", "component-token", "health"), &stdout, &stderr, server.Client())
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

func TestCatalogRouteCommandUsesComponentToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/catalog/capabilities/cap_1/route" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer component-token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{"capability_id": "cap_1"})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-catalog-url", server.URL,
		"-component-token", "component-token",
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

func TestNodeStopCommandPosts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/node/services/svc_gpu/stop" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer node-token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		writeEnvelope(t, w, http.StatusAccepted, map[string]any{"service_id": "svc_gpu", "status": "stopped"})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-node-url", server.URL,
		"-node-token", "node-token",
		"node", "stop", "svc_gpu",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"status": "stopped"`) {
		t.Fatalf("stdout = %s", stdout.String())
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

func decodeReport(t *testing.T, raw []byte) healthReport {
	t.Helper()
	var report healthReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, string(raw))
	}
	return report
}
