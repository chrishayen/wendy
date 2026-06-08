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
