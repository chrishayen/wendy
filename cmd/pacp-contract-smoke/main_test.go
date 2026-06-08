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

	"pacp/internal/contracts"
)

func TestRunScenarioSmokeStillPasses(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-root", filepath.Join("..", "..", "testdata", "contract-sim"),
		"-manifest", filepath.Join("fixtures", "S003", "manifest.json"),
	}, &stdout, &stderr, http.DefaultClient)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "contract-smoke=pass") {
		t.Fatalf("stdout = %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "fixture-replay=pass") {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestRunProviderSmokeChecksLiveProvider(t *testing.T) {
	invoked := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer provider-token" {
			writeSmokeErrorEnvelope(t, w, r, http.StatusUnauthorized, "unauthorized", "missing token")
			return
		}
		switch r.URL.Path {
		case "/v1/provider/manifest":
			writeSmokeEnvelope(t, w, r, http.StatusOK, smokeProviderManifest("http://"+r.Host))
		case "/v1/provider/health":
			writeSmokeEnvelope(t, w, r, http.StatusOK, map[string]any{"status": "healthy"})
		case "/v1/provider/capabilities/cap_echo/invoke":
			if r.Header.Get("X-Request-ID") != "req_contract_provider" {
				t.Fatalf("invoke X-Request-ID = %q", r.Header.Get("X-Request-ID"))
			}
			invoked = true
			var req contracts.ProviderInvokeRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode invoke: %v", err)
			}
			if req.Context.RequestID != "req_contract_provider" {
				t.Fatalf("invoke context request id = %q", req.Context.RequestID)
			}
			if req.Input["message"] != "hello" {
				if _, exists := req.Input["message"]; !exists {
					writeSmokeErrorEnvelope(t, w, r, http.StatusBadRequest, "validation_failed", "message is required")
					return
				}
				t.Fatalf("input = %#v", req.Input)
			}
			writeSmokeEnvelope(t, w, r, http.StatusOK, contracts.ProviderInvokeResponse{
				Output: map[string]any{"reply": "hello"},
			})
		case "/v1/provider/metrics":
			samples := []contracts.MetricSample{}
			if invoked {
				samples = append(samples, contracts.CountMetric("provider_invocations_total", 1, map[string]string{
					"capability_id": "cap_echo",
					"service_id":    "svc_smoke_provider",
					"status":        "success",
				}))
			}
			writeSmokeEnvelope(t, w, r, http.StatusOK, contracts.NewComponentMetrics("provider", samples))
		default:
			t.Fatalf("unexpected request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-provider-url", server.URL,
		"-provider-credential", "provider-token",
		"-capability-id", "cap_echo",
		"-input", `{"message":"hello"}`,
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	for _, expected := range []string{
		"check=provider.manifest status=pass",
		"check=provider.health status=pass",
		"check=provider.invoke status=pass",
		"check=provider.invalid_input status=pass",
		"check=provider.metrics status=pass",
		"contract-smoke=pass",
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("stdout missing %q:\n%s", expected, stdout.String())
		}
	}
}

func TestRunComponentSmokeChecksLiveComponent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer component-token" {
			writeSmokeErrorEnvelope(t, w, r, http.StatusUnauthorized, "unauthorized", "missing token")
			return
		}
		switch r.URL.Path {
		case "/v1/jobs/health":
			writeSmokeEnvelope(t, w, r, http.StatusOK, contracts.NewComponentHealth("jobs", nil))
		case "/v1/jobs/metrics":
			writeSmokeEnvelope(t, w, r, http.StatusOK, contracts.NewComponentMetrics("jobs", []contracts.MetricSample{}))
		case "/v1/jobs":
			writeSmokeEnvelope(t, w, r, http.StatusOK, map[string]any{"items": []any{}, "next_cursor": nil})
		default:
			t.Fatalf("unexpected request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-component-url", server.URL,
		"-component-kind", "jobs",
		"-component-credential", "component-token",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	for _, expected := range []string{
		"component=" + server.URL + " kind=jobs checks=3",
		"check=component.health status=pass",
		"check=component.metrics status=pass",
		"check=component.surface.jobs.list status=pass",
		"contract-smoke=pass",
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("stdout missing %q:\n%s", expected, stdout.String())
		}
	}
}

func TestRunComponentSmokeRejectsUnknownKind(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-component-url", "http://component.invalid",
		"-component-kind", "unknown",
	}, &stdout, &stderr, http.DefaultClient)
	if code != 1 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stderr.String(), "unsupported component kind") {
		t.Fatalf("stderr = %s", stderr.String())
	}
}

func TestRunProviderSmokeRejectsInvalidInputJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-provider-url", "http://provider.invalid",
		"-capability-id", "cap_echo",
		"-input", `[]`,
	}, &stdout, &stderr, http.DefaultClient)
	if code != 2 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stderr.String(), "must be a JSON object") {
		t.Fatalf("stderr = %s", stderr.String())
	}
}

func TestRunOpenAPICheckPassesRepositoryContracts(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-openapi", strings.Join([]string{
			filepath.Join("..", "..", "openapi", "public-gateway.v1.yaml"),
			filepath.Join("..", "..", "openapi", "component-services.v1.yaml"),
		}, ","),
	}, &stdout, &stderr, http.DefaultClient)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "openapi=pass") {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestRunOpenAPICheckReportsFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad-openapi.yaml")
	if err := os.WriteFile(path, []byte(`
openapi: 3.1.0
info:
  title: Bad
  version: v1
paths:
  /bad:
    get:
      operationId: bad
      responses:
        "200":
          description: Raw response.
          content:
            application/json:
              schema:
                type: object
components:
  schemas: {}
`), 0o600); err != nil {
		t.Fatalf("write bad openapi: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"-openapi", path}, &stdout, &stderr, http.DefaultClient)
	if code != 1 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stderr.String(), "success_response_not_enveloped") {
		t.Fatalf("stderr = %s", stderr.String())
	}
}

func TestRunFakePublicAPISmokePasses(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-fake-public-apis", "-timeout", "5s"}, &stdout, &stderr, http.DefaultClient)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	for _, expected := range []string{
		"fake-public-apis=checked",
		"check=fake.component.catalog.component.surface.catalog.capabilities status=pass",
		"check=fake.component.jobs.component.surface.jobs.list status=pass",
		"check=fake.component.node.component.surface.node.resources status=pass",
		"check=fake.artifacts.available.metadata status=pass",
		"check=fake.artifacts.available.content status=pass",
		"check=fake.artifacts.denied.forbidden status=pass",
		"check=fake.artifacts.expired status=pass",
		"check=fake.artifacts.missing.not_found status=pass",
		"check=fake.artifacts.upload.lifecycle status=pass",
		"check=fake.artifacts.unavailable.component_unavailable status=pass",
		"check=fake.catalog.capability.valid status=pass",
		"check=fake.catalog.manifest.invalid status=pass",
		"check=fake.catalog.capability.denied status=pass",
		"check=fake.catalog.capability.unavailable status=pass",
		"check=fake.catalog.capability.missing status=pass",
		"check=fake.catalog.unavailable.component_unavailable status=pass",
		"check=fake.jobs.states status=pass",
		"check=fake.jobs.create_idempotency status=pass",
		"check=fake.jobs.lifecycle.succeed status=pass",
		"check=fake.jobs.cancel status=pass",
		"check=fake.jobs.missing.not_found status=pass",
		"check=fake.jobs.unavailable.component_unavailable status=pass",
		"check=fake.leases.resources.states status=pass",
		"check=fake.leases.requests.states status=pass",
		"check=fake.leases.create.grant status=pass",
		"check=fake.leases.release.promotes status=pass",
		"check=fake.leases.denied.resource_unavailable status=pass",
		"check=fake.leases.unavailable.component_unavailable status=pass",
		"check=fake.node.services.states status=pass",
		"check=fake.node.service.failed_detail status=pass",
		"check=fake.node.lifecycle.missing_idempotency status=pass",
		"check=fake.node.lifecycle.start status=pass",
		"check=fake.node.lifecycle.touch status=pass",
		"check=fake.node.lifecycle.stop status=pass",
		"check=fake.node.unreachable.component_unavailable status=pass",
		"check=fake.policy.auth.allow status=pass",
		"check=fake.policy.auth.failure status=pass",
		"check=fake.policy.check.allow status=pass",
		"check=fake.policy.check.deny status=pass",
		"check=fake.policy.secret.resolve status=pass",
		"check=fake.policy.redact status=pass",
		"check=fake.provider.echo.provider.invoke status=pass",
		"check=fake.provider.echo.provider.invalid_input status=pass",
		"check=fake.provider.artifact.provider.artifact_metadata status=pass",
		"check=fake.provider.async.provider.invoke status=pass",
		"check=fake.provider.failure.provider.expected_error status=pass",
		"fake-public-apis=pass",
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("stdout missing %q:\n%s", expected, stdout.String())
		}
	}
}

func TestRunDistributedSmokePasses(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-distributed", "-timeout", "5s"}, &stdout, &stderr, http.DefaultClient)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	for _, expected := range []string{
		"check=component.catalog.surface.capabilities status=pass",
		"check=component.node.surface.resources status=pass",
		"check=gateway.invoke status=pass",
		"check=runner.run_once status=pass",
		"check=node.service_running status=pass",
		"check=leases.release_audit status=pass",
		"check=provider.invoked status=pass",
		"distributed-smoke=pass",
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("stdout missing %q:\n%s", expected, stdout.String())
		}
	}
}

func smokeProviderManifest(endpoint string) contracts.ProviderManifest {
	return contracts.ProviderManifest{
		SchemaVersion: "v1",
		Service: contracts.Service{
			ID:           "svc_smoke_provider",
			Name:         "Smoke Provider",
			Description:  "Provider used by the smoke command tests.",
			Version:      "v1",
			ProviderKind: "test",
		},
		Provider: contracts.Provider{
			Endpoint:   endpoint,
			HealthPath: "/v1/provider/health",
		},
		Capabilities: []contracts.Capability{{
			ID:            "cap_echo",
			Name:          "Echo",
			Description:   "Echoes a message.",
			ExecutionMode: "sync",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []any{"message"},
				"properties": map[string]any{
					"message": map[string]any{"type": "string"},
				},
			},
			OutputSchema: map[string]any{
				"type":     "object",
				"required": []any{"reply"},
				"properties": map[string]any{
					"reply": map[string]any{"type": "string"},
				},
			},
			Examples:    []map[string]any{},
			SideEffects: "none",
			TimeoutHint: "30s",
		}},
	}
}

func writeSmokeEnvelope(t *testing.T, w http.ResponseWriter, r *http.Request, status int, data any) {
	t.Helper()
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = "req_test"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"ok":    status >= 200 && status < 300,
		"data":  data,
		"links": map[string]any{},
		"meta":  map[string]any{"request_id": requestID, "schema_version": "v1"},
	}); err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
}

func writeSmokeErrorEnvelope(t *testing.T, w http.ResponseWriter, r *http.Request, status int, code, message string) {
	t.Helper()
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = "req_test"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"ok":    false,
		"error": map[string]any{"code": code, "message": message, "retryable": false},
		"links": map[string]any{},
		"meta":  map[string]any{"request_id": requestID, "schema_version": "v1"},
	}); err != nil {
		t.Fatalf("encode error envelope: %v", err)
	}
}
