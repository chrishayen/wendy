package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"pacp/internal/components/artifacts"
	"pacp/internal/components/catalog"
	"pacp/internal/components/jobs"
	"pacp/internal/components/leases"
	"pacp/internal/components/policy"
	"pacp/internal/contracts"
	"pacp/internal/provider"
	"pacp/internal/testkit"
)

func TestGatewayHealthDoesNotRequireDownstreamServices(t *testing.T) {
	handler := NewHandler(Config{})
	req := httptest.NewRequest(http.MethodGet, "/v1/gateway/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", rec.Code, rec.Body.String())
	}
	var envelope map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	data := envelope["data"].(map[string]any)
	details := data["details"].(map[string]any)
	if data["status"] != "healthy" || details["component"] != "gateway" {
		t.Fatalf("health response = %#v", envelope)
	}
	downstreams := details["downstreams_configured"].(map[string]any)
	if downstreams["catalog"] != false || downstreams["jobs"] != false {
		t.Fatalf("health response = %#v", envelope)
	}
	catalogDependency := dependencyStatusByName(t, details["dependencies"], "catalog")
	if catalogDependency["configured"] != false || catalogDependency["status"] != "missing" {
		t.Fatalf("catalog dependency = %#v", catalogDependency)
	}
	idempotency := details["idempotency"].(map[string]any)
	if idempotency["store_backend"] != "memory" || idempotency["record_count"] != float64(0) {
		t.Fatalf("health response = %#v", envelope)
	}
}

func TestGatewayMetricsReportsConfiguredDownstreams(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/catalog/health":
			writeGatewayTestEnvelope(t, w, http.StatusOK, contracts.NewComponentHealth("catalog", nil))
		case "/v1/jobs/health":
			writeGatewayTestEnvelope(t, w, http.StatusOK, contracts.NewComponentHealth("jobs", nil))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(downstream.Close)

	handler := NewHandler(Config{CatalogURL: downstream.URL, JobsURL: downstream.URL})
	healthReq := httptest.NewRequest(http.MethodGet, "/v1/gateway/health", nil)
	healthRec := httptest.NewRecorder()
	handler.ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", healthRec.Code, healthRec.Body.String())
	}
	var healthEnvelope map[string]any
	if err := json.NewDecoder(healthRec.Body).Decode(&healthEnvelope); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	healthData := healthEnvelope["data"].(map[string]any)
	healthDetails := healthData["details"].(map[string]any)
	if healthData["status"] != "healthy" {
		t.Fatalf("health response = %#v", healthEnvelope)
	}
	jobsDependency := dependencyStatusByName(t, healthDetails["dependencies"], "jobs")
	if jobsDependency["configured"] != true || jobsDependency["reachable"] != true || jobsDependency["status"] != "healthy" {
		t.Fatalf("jobs dependency = %#v", jobsDependency)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/gateway/metrics", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics status=%d body=%s", rec.Code, rec.Body.String())
	}
	var envelope map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode metrics response: %v", err)
	}
	data := envelope["data"].(map[string]any)
	if data["component"] != "gateway" {
		t.Fatalf("metrics response = %#v", envelope)
	}
	assertMetric(t, data, "gateway_downstream_configured", map[string]string{"downstream": "catalog"}, 1)
	assertMetric(t, data, "gateway_downstream_configured", map[string]string{"downstream": "policy"}, 0)
	assertMetric(t, data, "gateway_downstream_reachable", map[string]string{"downstream": "jobs", "status": "healthy"}, 1)
	assertMetric(t, data, "http_requests_total", map[string]string{"method": "GET", "route_group": "/v1/gateway/health", "status_class": "2xx"}, 1)
}

func TestGatewayHealthDegradesWhenConfiguredDependencyFails(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		health := contracts.NewComponentHealth("catalog", map[string]any{"store": "offline"})
		health.Status = "degraded"
		writeGatewayTestEnvelope(t, w, http.StatusOK, health)
	}))
	t.Cleanup(downstream.Close)

	handler := NewHandler(Config{CatalogURL: downstream.URL})
	req := httptest.NewRequest(http.MethodGet, "/v1/gateway/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", rec.Code, rec.Body.String())
	}
	var envelope map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	data := envelope["data"].(map[string]any)
	details := data["details"].(map[string]any)
	dependency := dependencyStatusByName(t, details["dependencies"], "catalog")
	if data["status"] != "degraded" || dependency["reachable"] != false || dependency["status"] != "degraded" {
		t.Fatalf("health response = %#v", envelope)
	}
}

func TestGatewayDiscoveryInvokeAndJobProjection(t *testing.T) {
	env := newGatewayTestEnv(t)

	tools := env.doJSON(http.MethodGet, "/v1/tools", nil, map[string]string{"Authorization": "Bearer token_agent"}, http.StatusOK)
	items := tools["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("tools = %#v", tools)
	}
	tool := items[0].(map[string]any)
	if tool["id"] != "cap_image_generate_gpu" {
		t.Fatalf("tool = %#v", tool)
	}

	invoke := env.doJSON(http.MethodPost, "/v1/tools/cap_image_generate_gpu/invoke", map[string]any{
		"input": map[string]any{
			"prompt": "red mug",
			"width":  1024,
			"height": 1024,
		},
		"preferred_mode": "async",
	}, map[string]string{"Authorization": "Bearer token_agent", "Idempotency-Key": "invoke-1"}, http.StatusAccepted)
	if invoke["mode"] != "async" || invoke["job_id"] != "job_000001" {
		t.Fatalf("invoke = %#v", invoke)
	}

	replay := env.doJSON(http.MethodPost, "/v1/tools/cap_image_generate_gpu/invoke", map[string]any{
		"input": map[string]any{
			"prompt": "red mug",
			"width":  1024,
			"height": 1024,
		},
		"preferred_mode": "async",
	}, map[string]string{"Authorization": "Bearer token_agent", "Idempotency-Key": "invoke-1"}, http.StatusAccepted)
	if replay["job_id"] != "job_000001" {
		t.Fatalf("invoke replay = %#v", replay)
	}

	env.doJSONEnvelope(http.MethodPost, "/v1/tools/cap_image_generate_gpu/invoke", map[string]any{
		"input": map[string]any{"prompt": "blue mug"},
	}, map[string]string{"Authorization": "Bearer token_agent", "Idempotency-Key": "invoke-1"}, http.StatusConflict)

	status := env.doJSON(http.MethodGet, "/v1/agent/jobs/job_000001", nil, map[string]string{"Authorization": "Bearer token_agent"}, http.StatusOK)
	if status["job_id"] != "job_000001" || status["metadata"] != nil || status["claim"] != nil {
		t.Fatalf("job status leaked private fields = %#v", status)
	}
}

func TestGatewayAgentJobProjectionIncludesLeaseQueueStatus(t *testing.T) {
	env := newGatewayTestEnv(t)

	invoke := env.doJSON(http.MethodPost, "/v1/tools/cap_image_generate_gpu/invoke", map[string]any{
		"input": map[string]any{
			"prompt": "red mug",
			"width":  1024,
			"height": 1024,
		},
		"preferred_mode": "async",
	}, map[string]string{"Authorization": "Bearer token_agent", "Idempotency-Key": "invoke-queue"}, http.StatusAccepted)
	jobID := invoke["job_id"].(string)

	if _, err := env.leaseStore.RegisterResource(contracts.RegisterResourceRequest{
		ResourceID: "res_gpu_0",
		Selector:   "gpu",
		Status:     contracts.ResourceAvailable,
	}); err != nil {
		t.Fatalf("register resource: %v", err)
	}
	if _, err := env.leaseStore.CreateLeaseRequest(contracts.CreateLeaseRequest{
		RequesterID:      "job_holder",
		ResourceSelector: "gpu",
	}); err != nil {
		t.Fatalf("create holder lease: %v", err)
	}
	queued, err := env.leaseStore.CreateLeaseRequest(contracts.CreateLeaseRequest{
		RequesterID:      jobID,
		ResourceSelector: "gpu",
	})
	if err != nil {
		t.Fatalf("create queued lease: %v", err)
	}
	if queued.State != contracts.LeaseRequestPending || queued.QueuePosition == nil {
		t.Fatalf("queued lease = %#v", queued)
	}

	status := env.doJSON(http.MethodGet, "/v1/agent/jobs/"+jobID, nil, map[string]string{"Authorization": "Bearer token_agent"}, http.StatusOK)
	queue := status["queue"].(map[string]any)
	if queue["request_id"] != queued.RequestID || queue["state"] != "pending" || queue["resource_selector"] != "gpu" {
		t.Fatalf("queue status = %#v", queue)
	}
	if queue["queue_position"] != float64(1) {
		t.Fatalf("queue position = %#v", queue)
	}
}

func TestGatewayReplaysS003PublicDiscoveryAndInvokeFixtures(t *testing.T) {
	pkg := loadS003GatewayFixturePackage(t)
	deps := newS003GatewayFixtureDependencies(t, pkg)
	defer deps.server.Close()

	handler := NewHandler(Config{
		CatalogURL:        deps.server.URL,
		PolicyURL:         deps.server.URL,
		JobsURL:           deps.server.URL,
		ArtifactsURL:      deps.server.URL,
		GatewayCredential: "Bearer token_s003_gateway",
		Client:            deps.server.Client(),
	})

	for _, fixtureID := range []string{
		"public_tools_list_ok",
		"public_tool_detail_ok",
		"public_invoke_async_ok",
		"public_invoke_idempotency_replay",
		"public_invoke_idempotency_conflict",
		"public_invoke_invalid_input",
	} {
		if _, err := deps.replay(handler, fixtureID); err != nil {
			t.Fatalf("replay %s: %v", fixtureID, err)
		}
	}
}

func TestGatewayReplaysS003PublicJobAndArtifactFixtures(t *testing.T) {
	pkg := loadS003GatewayFixturePackage(t)
	deps := newS003GatewayFixtureDependencies(t, pkg)
	defer deps.server.Close()

	handler := NewHandler(Config{
		PolicyURL:         deps.server.URL,
		JobsURL:           deps.server.URL,
		ArtifactsURL:      deps.server.URL,
		GatewayCredential: "Bearer token_s003_gateway",
		Client:            deps.server.Client(),
	})

	for _, fixtureID := range []string{
		"public_cancel_queued_ok",
		"public_running_cancel_forbidden",
		"public_job_not_found",
		"public_job_missing_owner_context",
		"public_job_policy_missing_context_forbidden",
		"public_job_context_build_failure",
		"public_job_running",
		"public_job_succeeded",
		"public_logs_first_page",
		"public_logs_final_page",
		"public_artifact_list_ok",
		"public_artifact_content_proxy",
		"public_artifact_content_not_found",
		"public_artifact_content_forbidden",
	} {
		if _, err := deps.replay(handler, fixtureID); err != nil {
			t.Fatalf("replay %s: %v", fixtureID, err)
		}
	}
}

func TestGatewayReplaysS003AgentUserFixtures(t *testing.T) {
	actorPkg := loadS003FixturePackage(t, "agent-user")
	dependencyPkg := loadS003GatewayFixturePackage(t)
	deps := newS003GatewayFixtureDependencies(t, dependencyPkg)
	defer deps.server.Close()

	handler := NewHandler(Config{
		CatalogURL:        deps.server.URL,
		PolicyURL:         deps.server.URL,
		JobsURL:           deps.server.URL,
		ArtifactsURL:      deps.server.URL,
		GatewayCredential: "Bearer token_s003_gateway",
		Client:            deps.server.Client(),
	})

	for _, fixtureID := range []string{
		"public_tools_list_ok",
		"public_tool_detail_ok",
		"public_invoke_async_ok",
		"public_cancel_queued_ok",
		"public_job_running",
		"public_job_succeeded",
		"public_logs_first_page",
		"public_logs_final_page",
		"public_artifact_list_ok",
		"public_artifact_content_ok",
	} {
		if _, err := deps.replayPackage(handler, actorPkg, fixtureID); err != nil {
			t.Fatalf("replay %s: %v", fixtureID, err)
		}
	}
}

func TestGatewayPropagatesRequestIDToDownstreams(t *testing.T) {
	seen := map[string]string{}
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = r.Header.Get("X-Request-ID")
		switch r.URL.Path {
		case "/v1/auth/verify":
			writeGatewayTestEnvelope(t, w, http.StatusOK, map[string]any{
				"valid":      true,
				"subject_id": "sub_agent",
				"scopes":     []string{"agent"},
			})
		case "/v1/policy/check":
			writeGatewayTestEnvelope(t, w, http.StatusOK, map[string]any{"allowed": true, "reason": "test_allow"})
		case "/v1/catalog/capabilities":
			writeGatewayTestEnvelope(t, w, http.StatusOK, map[string]any{"items": []any{}, "next_cursor": nil})
		default:
			t.Fatalf("unexpected downstream request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer downstream.Close()

	handler := NewHandler(Config{
		CatalogURL: downstream.URL,
		PolicyURL:  downstream.URL,
		Client:     downstream.Client(),
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/tools", nil)
	req.Header.Set("Authorization", "Bearer token_agent")
	req.Header.Set("X-Request-ID", "req_gateway_trace")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	for _, path := range []string{"/v1/auth/verify", "/v1/policy/check", "/v1/catalog/capabilities"} {
		if seen[path] != "req_gateway_trace" {
			t.Fatalf("%s request id = %q; seen=%#v", path, seen[path], seen)
		}
	}
}

func assertMetric(t *testing.T, data map[string]any, name string, labels map[string]string, value float64) {
	t.Helper()
	for _, rawSample := range data["samples"].([]any) {
		sample := rawSample.(map[string]any)
		if sample["name"] != name {
			continue
		}
		if !labelsMatch(sample["labels"], labels) {
			continue
		}
		if sample["value"] != value {
			t.Fatalf("metric %s value=%#v want=%v", name, sample["value"], value)
		}
		return
	}
	t.Fatalf("metric %s labels=%#v not found in %#v", name, labels, data["samples"])
}

func dependencyStatusByName(t *testing.T, raw any, name string) map[string]any {
	t.Helper()
	for _, item := range raw.([]any) {
		dependency := item.(map[string]any)
		if dependency["name"] == name {
			return dependency
		}
	}
	t.Fatalf("dependency %s not found in %#v", name, raw)
	return nil
}

func writeGatewayTestEnvelope(t *testing.T, w http.ResponseWriter, status int, data any) {
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

type s003GatewayFixtureDependencies struct {
	t                   *testing.T
	pkg                 testkit.FixturePackage
	server              *httptest.Server
	activePublicFixture string
}

func newS003GatewayFixtureDependencies(t *testing.T, pkg testkit.FixturePackage) *s003GatewayFixtureDependencies {
	t.Helper()
	deps := &s003GatewayFixtureDependencies{t: t, pkg: pkg}
	deps.server = httptest.NewServer(http.HandlerFunc(deps.serveHTTP))
	return deps
}

func (d *s003GatewayFixtureDependencies) replay(handler http.Handler, fixtureID string) (testkit.ReplayResult, error) {
	return d.replayPackage(handler, d.pkg, fixtureID)
}

func (d *s003GatewayFixtureDependencies) replayPackage(handler http.Handler, pkg testkit.FixturePackage, fixtureID string) (testkit.ReplayResult, error) {
	d.activePublicFixture = fixtureID
	defer func() { d.activePublicFixture = "" }()
	return testkit.ReplayHTTPFixture(handler, pkg, fixtureID)
}

func (d *s003GatewayFixtureDependencies) serveHTTP(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		d.fail(w, "read downstream request body: %v", err)
		return
	}
	fixtureID, ok := d.fixtureIDFor(r, raw)
	if !ok {
		d.fail(w, "unexpected downstream request: %s %s?%s body=%s", r.Method, r.URL.Path, r.URL.RawQuery, string(raw))
		return
	}
	fixture, ok := s003FixtureByID(d.pkg, fixtureID)
	if !ok {
		d.fail(w, "fixture %s not found", fixtureID)
		return
	}
	if !d.assertDownstreamRequest(w, fixtureID, fixture, r, raw) {
		return
	}
	writeS003FixtureResponse(d.t, w, d.pkg, fixtureID, fixture)
}

func (d *s003GatewayFixtureDependencies) fixtureIDFor(r *http.Request, raw []byte) (string, bool) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodPost && path == "/v1/auth/verify":
		return "c08_auth_agent_ok", true
	case r.Method == http.MethodPost && path == "/v1/policy/check":
		var body map[string]any
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				return "", false
			}
		}
		action, _ := body["action"].(string)
		resource, _ := body["resource"].(string)
		switch {
		case action == "tool.discover" && resource == "tools":
			return "c08_policy_tool_discover_allow", true
		case action == "tool.discover" && resource == "cap_image_generate_gpu":
			return "c08_policy_tool_discover_capability_allow", true
		case action == "tool.invoke" && resource == "cap_image_generate_gpu":
			return "c08_policy_tool_invoke_allow", true
		case action == "job.read" && resource == "job_s003_0001" && d.activePublicFixture == "public_job_policy_missing_context_forbidden":
			return "c08_policy_job_read_missing_context_deny", true
		case action == "job.read" && resource == "job_s003_0001":
			return "c08_policy_job_read_allow", true
		case action == "job.cancel" && resource == "job_s003_0001" && d.activePublicFixture == "public_running_cancel_forbidden":
			return "c08_policy_job_cancel_running_deny", true
		case action == "job.cancel" && resource == "job_s003_0001":
			return "c08_policy_job_cancel_queued_allow", true
		case action == "artifact.read" && resource == "job_artifacts:job_s003_0001":
			return "c08_policy_job_artifacts_read_allow", true
		case action == "artifact.read" && resource == "art_s003_0001" && d.activePublicFixture == "public_artifact_content_forbidden":
			return "c08_policy_artifact_read_forbidden", true
		case action == "artifact.read" && resource == "art_s003_0001":
			return "c08_policy_artifact_read_allow", true
		default:
			return "", false
		}
	case r.Method == http.MethodGet && path == "/v1/catalog/capabilities" && r.URL.Query().Get("limit") == "50":
		return "c03_catalog_list_ok", true
	case r.Method == http.MethodGet && path == "/v1/catalog/capabilities" && r.URL.Query().Get("capability_id") == "cap_image_generate_gpu":
		return "c03_catalog_detail_lookup_ok", true
	case r.Method == http.MethodGet && path == "/v1/catalog/capabilities/cap_image_generate_gpu/route":
		return "c03_catalog_route_ok", true
	case r.Method == http.MethodPost && path == "/v1/jobs":
		return "c05_create_job_ok", true
	case r.Method == http.MethodGet && path == "/v1/jobs/job_s003_0001/policy-context":
		return d.jobPolicyContextFixtureID()
	case r.Method == http.MethodGet && path == "/v1/jobs/job_s003_missing/policy-context":
		return "c05_job_policy_context_not_found", true
	case r.Method == http.MethodGet && path == "/v1/jobs/job_s003_0001/agent-projection" && d.activePublicFixture == "public_job_running":
		return "c05_agent_projection_running", true
	case r.Method == http.MethodGet && path == "/v1/jobs/job_s003_0001/agent-projection":
		return "c05_agent_projection_succeeded", true
	case r.Method == http.MethodGet && path == "/v1/jobs/job_s003_0001/logs" && d.activePublicFixture == "public_logs_final_page":
		return "c05_logs_final_page", true
	case r.Method == http.MethodGet && path == "/v1/jobs/job_s003_0001/logs":
		return "c05_logs_read", true
	case r.Method == http.MethodPost && path == "/v1/jobs/job_s003_0001/cancel":
		return "c05_cancel_queued_ok", true
	case r.Method == http.MethodGet && path == "/v1/artifacts" && r.URL.Query().Get("producer_ref") == "job_s003_0001":
		return "c07_artifact_list_by_producer_ok", true
	case r.Method == http.MethodGet && path == "/v1/artifacts/art_s003_0001/policy-context":
		return "c07_artifact_policy_context_ok", true
	case r.Method == http.MethodGet && path == "/v1/artifacts/art_s003_missing/policy-context":
		return "c07_artifact_policy_context_not_found", true
	case r.Method == http.MethodGet && path == "/v1/artifacts/art_s003_0001/content":
		return "c07_artifact_content_ok", true
	default:
		return "", false
	}
}

func (d *s003GatewayFixtureDependencies) jobPolicyContextFixtureID() (string, bool) {
	switch d.activePublicFixture {
	case "public_cancel_queued_ok", "public_job_policy_missing_context_forbidden":
		return "c05_job_policy_context_queued", true
	case "public_running_cancel_forbidden", "public_job_running", "public_logs_first_page":
		return "c05_job_policy_context_running", true
	case "public_job_succeeded", "public_logs_final_page", "public_artifact_list_ok":
		return "c05_job_policy_context_succeeded", true
	case "public_job_missing_owner_context":
		return "c05_job_policy_context_missing_owner", true
	case "public_job_context_build_failure":
		return "c05_job_policy_context_malformed", true
	default:
		return "", false
	}
}

func (d *s003GatewayFixtureDependencies) assertDownstreamRequest(w http.ResponseWriter, fixtureID string, fixture contracts.Fixture, r *http.Request, raw []byte) bool {
	if fixture.Request == nil {
		d.fail(w, "%s has no request", fixtureID)
		return false
	}
	if r.Method != fixture.Request.Method || r.URL.Path != fixture.Request.Path {
		d.fail(w, "%s request = %s %s, want %s %s", fixtureID, r.Method, r.URL.Path, fixture.Request.Method, fixture.Request.Path)
		return false
	}
	for key, want := range fixture.Request.Headers {
		if strings.EqualFold(key, "Idempotency-Key") {
			continue
		}
		if got := r.Header.Get(key); got != want {
			d.fail(w, "%s header %s = %q, want %q", fixtureID, key, got, want)
			return false
		}
	}
	for key, rawWant := range fixture.Request.Query {
		want := queryValues(rawWant)
		got := r.URL.Query()[key]
		if !reflect.DeepEqual(got, want) {
			d.fail(w, "%s query %s = %#v, want %#v", fixtureID, key, got, want)
			return false
		}
	}
	if fixture.Request.Body != nil {
		var got any
		if err := json.Unmarshal(raw, &got); err != nil {
			d.fail(w, "%s decode request body: %v", fixtureID, err)
			return false
		}
		if err := compareS003JSONSubset("$", fixture.Request.Body, got); err != nil {
			d.fail(w, "%s request body mismatch: %v; got %#v", fixtureID, err, got)
			return false
		}
	}
	return true
}

func (d *s003GatewayFixtureDependencies) fail(w http.ResponseWriter, format string, args ...any) {
	d.t.Helper()
	d.t.Errorf(format, args...)
	writeGatewayTestEnvelope(d.t, w, http.StatusInternalServerError, map[string]any{"fixture_error": true})
}

func loadS003GatewayFixturePackage(t *testing.T) testkit.FixturePackage {
	return loadS003FixturePackage(t, "c04-agent-tool-gateway")
}

func loadS003FixturePackage(t *testing.T, owner string) testkit.FixturePackage {
	t.Helper()
	scenario, err := testkit.LoadScenario(filepath.Join("..", "..", "..", "testdata", "contract-sim"), filepath.Join("fixtures", "S003", "manifest.json"))
	if err != nil {
		t.Fatalf("load S003 fixtures: %v", err)
	}
	pkg, ok := testkit.FindPackage(scenario, owner)
	if !ok {
		t.Fatalf("%s fixture package not found", owner)
	}
	return pkg
}

func s003FixtureByID(pkg testkit.FixturePackage, id string) (contracts.Fixture, bool) {
	for _, fixture := range pkg.File.Fixtures {
		if fixture.ID == id {
			return fixture, true
		}
	}
	return contracts.Fixture{}, false
}

func writeS003FixtureResponse(t *testing.T, w http.ResponseWriter, pkg testkit.FixturePackage, fixtureID string, fixture contracts.Fixture) {
	t.Helper()
	if fixture.Response == nil || fixture.Response.Status == nil {
		t.Fatalf("%s has no response status", fixtureID)
	}
	for key, value := range fixture.Response.Headers {
		w.Header().Set(key, value)
	}
	if fixture.Response.Body != nil && w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(*fixture.Response.Status)
	if fixture.Response.Body != nil {
		if err := json.NewEncoder(w).Encode(fixture.Response.Body); err != nil {
			t.Fatalf("encode %s fixture response: %v", fixtureID, err)
		}
		return
	}
	if fixture.Response.BodyFixture != "" {
		raw, err := os.ReadFile(filepath.Join(filepath.Dir(pkg.AbsPath), fixture.Response.BodyFixture))
		if err != nil {
			t.Fatalf("read %s fixture body: %v", fixtureID, err)
		}
		body, err := base64.StdEncoding.DecodeString(string(raw))
		if err != nil {
			t.Fatalf("decode %s fixture body: %v", fixtureID, err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatalf("write %s fixture body: %v", fixtureID, err)
		}
	}
}

func queryValues(raw any) []string {
	switch value := raw.(type) {
	case string:
		return []string{value}
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func compareS003JSONSubset(path string, expected any, actual any) error {
	switch typedExpected := expected.(type) {
	case map[string]any:
		typedActual, ok := actual.(map[string]any)
		if !ok {
			return errS003JSON(path, "expected object, got %T", actual)
		}
		for key, value := range typedExpected {
			actualValue, ok := typedActual[key]
			if !ok {
				return errS003JSON(path+"."+key, "missing")
			}
			if err := compareS003JSONSubset(path+"."+key, value, actualValue); err != nil {
				return err
			}
		}
	case []any:
		typedActual, ok := actual.([]any)
		if !ok {
			return errS003JSON(path, "expected array, got %T", actual)
		}
		if len(typedActual) != len(typedExpected) {
			return errS003JSON(path, "length %d, want %d", len(typedActual), len(typedExpected))
		}
		for i := range typedExpected {
			if err := compareS003JSONSubset(path+"["+strconv.Itoa(i)+"]", typedExpected[i], typedActual[i]); err != nil {
				return err
			}
		}
	default:
		if !reflect.DeepEqual(expected, actual) {
			return errS003JSON(path, "got %#v, want %#v", actual, expected)
		}
	}
	return nil
}

func errS003JSON(path, format string, args ...any) error {
	return fmt.Errorf("%s: "+format, append([]any{path}, args...)...)
}

func labelsMatch(raw any, want map[string]string) bool {
	if len(want) == 0 {
		return raw == nil
	}
	labels, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	for key, value := range want {
		if labels[key] != value {
			return false
		}
	}
	return true
}

func TestGatewayCancelArtifactsAndContent(t *testing.T) {
	env := newGatewayTestEnv(t)
	invoke := env.doJSON(http.MethodPost, "/v1/tools/cap_image_generate_gpu/invoke", map[string]any{
		"input": map[string]any{"prompt": "red mug"},
	}, map[string]string{"Authorization": "Bearer token_agent", "Idempotency-Key": "invoke-2"}, http.StatusAccepted)
	jobID := invoke["job_id"].(string)

	canceled := env.doJSON(http.MethodPost, "/v1/agent/jobs/"+jobID+"/cancel", map[string]any{"reason": "canceled by requester"}, map[string]string{
		"Authorization":   "Bearer token_agent",
		"Idempotency-Key": "cancel-1",
	}, http.StatusOK)
	if canceled["state"] != "canceled" {
		t.Fatalf("cancel = %#v", canceled)
	}

	artifact := createTestArtifact(t, env.artifactStore, jobID)
	artifacts := env.doJSON(http.MethodGet, "/v1/agent/jobs/"+jobID+"/artifacts", nil, map[string]string{"Authorization": "Bearer token_agent"}, http.StatusOK)
	items := artifacts["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["artifact_id"] != artifact.ArtifactID {
		t.Fatalf("artifacts = %#v", artifacts)
	}
	if _, leaked := items[0].(map[string]any)["owner_subject_id"]; leaked {
		t.Fatalf("agent artifact leaked owner_subject_id = %#v", items[0])
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/artifacts/"+artifact.ArtifactID+"/content", nil)
	req.Header.Set("Authorization", "Bearer token_agent")
	rec := httptest.NewRecorder()
	env.gateway.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("content status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "artifact bytes" || rec.Header().Get("Digest") == "" {
		t.Fatalf("content response headers=%#v body=%q", rec.Header(), rec.Body.String())
	}
}

func TestGatewayRejectsRunningJobCancellation(t *testing.T) {
	env := newGatewayTestEnv(t)
	invoke := env.doJSON(http.MethodPost, "/v1/tools/cap_image_generate_gpu/invoke", map[string]any{
		"input": map[string]any{"prompt": "red mug"},
	}, map[string]string{"Authorization": "Bearer token_agent", "Idempotency-Key": "invoke-running-cancel"}, http.StatusAccepted)
	jobID := invoke["job_id"].(string)
	if _, err := env.jobStore.Claim(jobID, contracts.JobClaimRequest{WorkerID: "runner_1", LeaseSeconds: 60}); err != nil {
		t.Fatalf("claim job: %v", err)
	}
	if _, err := env.jobStore.Heartbeat(jobID, contracts.JobHeartbeatRequest{WorkerID: "runner_1", TransitionTo: "running"}); err != nil {
		t.Fatalf("mark job running: %v", err)
	}

	status := env.doJSON(http.MethodGet, "/v1/agent/jobs/"+jobID, nil, map[string]string{"Authorization": "Bearer token_agent"}, http.StatusOK)
	links := status["links"].(map[string]any)
	if _, ok := links["cancel"]; ok {
		t.Fatalf("running job advertised cancel link: %#v", links)
	}
	envelope := env.doJSONEnvelope(http.MethodPost, "/v1/agent/jobs/"+jobID+"/cancel", map[string]any{"reason": "stop running"}, map[string]string{
		"Authorization":   "Bearer token_agent",
		"Idempotency-Key": "cancel-running-1",
	}, http.StatusForbidden)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "forbidden" {
		t.Fatalf("running cancel error = %#v", errObj)
	}
}

func TestGatewayPersistentIdempotencyReplaysSyncAfterRestart(t *testing.T) {
	calls := 0
	providerServer, err := provider.NewServer(syncTestManifest("http://provider.invalid"), map[string]provider.CapabilityHandler{
		"cap_sync_echo": func(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
			calls++
			return contracts.ProviderInvokeResponse{
				Output: map[string]any{
					"message": req.Input["message"],
					"calls":   calls,
				},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	providerHTTP := httptest.NewServer(providerServer)

	policyStore := policy.NewStore()
	if _, err := policyStore.CreateAPIKey(contracts.CreateAPIKeyRequest{SubjectID: "sub_agent", Scopes: []string{"agent"}, Token: "token_agent"}); err != nil {
		t.Fatalf("create agent key: %v", err)
	}
	if _, err := policyStore.CreateAPIKey(contracts.CreateAPIKeyRequest{SubjectID: "sub_gateway", Scopes: []string{"component"}, Token: "token_gateway"}); err != nil {
		t.Fatalf("create gateway key: %v", err)
	}
	policyServer := httptest.NewServer(policy.NewHandler(policyStore))

	catalogStore := catalog.NewStore()
	if _, err := catalogStore.RegisterManifest(syncTestManifest(providerHTTP.URL)); err != nil {
		t.Fatalf("register manifest: %v", err)
	}
	catalogServer := httptest.NewServer(catalog.NewHandler(catalogStore))
	jobsServer := httptest.NewServer(jobs.NewHandler(jobs.NewStore()))
	artifactStore, err := artifacts.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new artifact store: %v", err)
	}
	artifactsServer := httptest.NewServer(artifacts.NewHandler(artifactStore))

	stateFile := filepath.Join(t.TempDir(), "gateway-idempotency.json")
	newGateway := func() http.Handler {
		t.Helper()
		handler, err := NewPersistentHandler(Config{
			CatalogURL:        catalogServer.URL,
			PolicyURL:         policyServer.URL,
			JobsURL:           jobsServer.URL,
			ArtifactsURL:      artifactsServer.URL,
			GatewayCredential: "Bearer token_gateway",
			Client:            providerHTTP.Client(),
		}, stateFile)
		if err != nil {
			t.Fatalf("new persistent gateway: %v", err)
		}
		return handler
	}
	env := &gatewayTestEnv{
		gateway: newGateway(),
		servers: []*httptest.Server{providerHTTP, policyServer, catalogServer, jobsServer, artifactsServer},
		t:       t,
	}
	t.Cleanup(func() {
		for _, server := range env.servers {
			server.Close()
		}
	})

	first := env.doJSON(http.MethodPost, "/v1/tools/cap_sync_echo/invoke", map[string]any{
		"input": map[string]any{"message": "hello"},
	}, map[string]string{"Authorization": "Bearer token_agent", "Idempotency-Key": "sync-1"}, http.StatusOK)
	if first["mode"] != "sync" || first["output"].(map[string]any)["calls"].(float64) != 1 {
		t.Fatalf("first sync invoke = %#v", first)
	}
	if calls != 1 {
		t.Fatalf("provider calls after first invoke = %d", calls)
	}

	env.gateway = newGateway()
	replay := env.doJSON(http.MethodPost, "/v1/tools/cap_sync_echo/invoke", map[string]any{
		"input": map[string]any{"message": "hello"},
	}, map[string]string{"Authorization": "Bearer token_agent", "Idempotency-Key": "sync-1"}, http.StatusOK)
	if replay["mode"] != "sync" || replay["output"].(map[string]any)["calls"].(float64) != 1 {
		t.Fatalf("sync replay = %#v", replay)
	}
	if calls != 1 {
		t.Fatalf("provider calls after replay = %d", calls)
	}

	env.doJSONEnvelope(http.MethodPost, "/v1/tools/cap_sync_echo/invoke", map[string]any{
		"input": map[string]any{"message": "different"},
	}, map[string]string{"Authorization": "Bearer token_agent", "Idempotency-Key": "sync-1"}, http.StatusConflict)
	if calls != 1 {
		t.Fatalf("provider calls after conflict = %d", calls)
	}
}

type gatewayTestEnv struct {
	gateway       http.Handler
	jobStore      *jobs.Store
	leaseStore    *leases.Store
	artifactStore *artifacts.Store
	servers       []*httptest.Server
	t             *testing.T
}

func newGatewayTestEnv(t *testing.T) *gatewayTestEnv {
	t.Helper()
	policyStore := policy.NewStore()
	_, err := policyStore.CreateAPIKey(contracts.CreateAPIKeyRequest{SubjectID: "sub_agent", Scopes: []string{"agent"}, Token: "token_agent"})
	if err != nil {
		t.Fatalf("create agent key: %v", err)
	}
	_, err = policyStore.CreateAPIKey(contracts.CreateAPIKeyRequest{SubjectID: "sub_gateway", Scopes: []string{"component"}, Token: "token_gateway"})
	if err != nil {
		t.Fatalf("create gateway key: %v", err)
	}
	policyServer := httptest.NewServer(policy.NewHandler(policyStore))

	catalogStore := catalog.NewStore()
	if _, err := catalogStore.RegisterManifest(testManifest()); err != nil {
		t.Fatalf("register manifest: %v", err)
	}
	catalogServer := httptest.NewServer(catalog.NewHandler(catalogStore))

	jobStore := jobs.NewStore()
	jobsServer := httptest.NewServer(jobs.NewHandler(jobStore))

	leaseStore := leases.NewStore()
	leasesServer := httptest.NewServer(leases.NewHandler(leaseStore))

	artifactStore, err := artifacts.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new artifact store: %v", err)
	}
	artifactsServer := httptest.NewServer(artifacts.NewHandler(artifactStore))

	gateway := NewHandler(Config{
		CatalogURL:        catalogServer.URL,
		PolicyURL:         policyServer.URL,
		JobsURL:           jobsServer.URL,
		LeasesURL:         leasesServer.URL,
		ArtifactsURL:      artifactsServer.URL,
		GatewayCredential: "Bearer token_gateway",
		Client:            policyServer.Client(),
	})
	env := &gatewayTestEnv{
		gateway:       gateway,
		jobStore:      jobStore,
		leaseStore:    leaseStore,
		artifactStore: artifactStore,
		servers:       []*httptest.Server{policyServer, catalogServer, jobsServer, leasesServer, artifactsServer},
		t:             t,
	}
	t.Cleanup(func() {
		for _, server := range env.servers {
			server.Close()
		}
	})
	return env
}

func (e *gatewayTestEnv) doJSON(method, path string, body any, headers map[string]string, wantStatus int) map[string]any {
	e.t.Helper()
	envelope := e.doJSONEnvelope(method, path, body, headers, wantStatus)
	if !envelope["ok"].(bool) {
		e.t.Fatalf("error response for %s %s: %#v", method, path, envelope)
	}
	return envelope["data"].(map[string]any)
}

func (e *gatewayTestEnv) doJSONEnvelope(method, path string, body any, headers map[string]string, wantStatus int) map[string]any {
	e.t.Helper()
	var raw bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&raw).Encode(body); err != nil {
			e.t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &raw)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	e.gateway.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		e.t.Fatalf("%s %s status=%d want=%d body=%s", method, path, rec.Code, wantStatus, rec.Body.String())
	}
	var envelope map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		e.t.Fatalf("decode response: %v", err)
	}
	return envelope
}

func testManifest() contracts.ProviderManifest {
	return contracts.ProviderManifest{
		SchemaVersion: "v1",
		Service: contracts.Service{
			ID:           "svc_comfyui_gpu",
			Name:         "ComfyUI GPU",
			Description:  "GPU image generation service",
			Version:      "0.1.0",
			ProviderKind: "comfyui",
			Tags:         []string{"image", "gpu"},
		},
		Provider: contracts.Provider{Endpoint: "http://provider.invalid", NodeID: "node_linux_gpu"},
		Capabilities: []contracts.Capability{{
			ID:            "cap_image_generate_gpu",
			Name:          "GPU image generation",
			Description:   "Generate an image using a GPU-backed provider.",
			Tags:          []string{"image"},
			ExecutionMode: "async",
			InputSchema:   map[string]any{"type": "object"},
			OutputSchema:  map[string]any{"type": "object"},
			Examples:      []map[string]any{},
			SideEffects:   "external",
			ResourceHints: []contracts.ResourceHint{{
				Selector: "gpu",
				Required: true,
				Quantity: 1,
			}},
			ArtifactHints: []contracts.ArtifactHint{{MediaType: "image/png", Count: "one"}},
			TimeoutHint:   "900s",
		}},
	}
}

func syncTestManifest(endpoint string) contracts.ProviderManifest {
	return contracts.ProviderManifest{
		SchemaVersion: "v1",
		Service: contracts.Service{
			ID:           "svc_sync_echo",
			Name:         "Sync Echo",
			Description:  "Synchronous echo service",
			Version:      "0.1.0",
			ProviderKind: "test",
			Tags:         []string{"sync"},
		},
		Provider: contracts.Provider{Endpoint: endpoint},
		Capabilities: []contracts.Capability{{
			ID:            "cap_sync_echo",
			Name:          "Sync echo",
			Description:   "Echo a message synchronously.",
			Tags:          []string{"sync"},
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
				"required": []any{"message", "calls"},
				"properties": map[string]any{
					"message": map[string]any{"type": "string"},
					"calls":   map[string]any{"type": "integer"},
				},
			},
			Examples:      []map[string]any{{"message": "hello"}},
			SideEffects:   "external",
			ResourceHints: []contracts.ResourceHint{},
			ArtifactHints: []contracts.ArtifactHint{},
			TimeoutHint:   "30s",
		}},
	}
}

func createTestArtifact(t *testing.T, store *artifacts.Store, producerRef string) contracts.Artifact {
	t.Helper()
	body := []byte("artifact bytes")
	checksum, digest := artifactChecksumAndDigest(body)
	size := int64(len(body))
	session, _, err := store.CreateUpload(contracts.CreateArtifactUploadRequest{
		Name:             "result.txt",
		MediaType:        "text/plain",
		ProducerRef:      producerRef,
		OwnerSubjectID:   "sub_agent",
		ExpectedSize:     &size,
		ExpectedChecksum: checksum,
	}, "create-"+producerRef)
	if err != nil {
		t.Fatalf("create artifact upload: %v", err)
	}
	if _, err := store.PutContent(session.UploadID, artifacts.ContentUpload{
		Body:          body,
		ContentType:   "text/plain",
		ContentLength: "14",
		Digest:        digest,
	}, "content-"+producerRef); err != nil {
		t.Fatalf("put artifact content: %v", err)
	}
	artifact, _, err := store.CompleteUpload(session.UploadID, contracts.CompleteArtifactUploadRequest{Checksum: checksum, Size: size}, "complete-"+producerRef)
	if err != nil {
		t.Fatalf("complete artifact upload: %v", err)
	}
	return artifact
}

func artifactChecksumAndDigest(body []byte) (string, string) {
	sum := sha256Sum(body)
	return "sha256:" + sum.hex, "sha-256=" + sum.base64
}

type digestParts struct {
	hex    string
	base64 string
}

func sha256Sum(body []byte) digestParts {
	sum := sha256.Sum256(body)
	return digestParts{hex: hex.EncodeToString(sum[:]), base64: base64.StdEncoding.EncodeToString(sum[:])}
}
