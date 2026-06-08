package node

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"pacp/internal/contracts"
	"pacp/internal/testkit"
)

func TestHandlerReplaysS003ReadAndAuthFixtures(t *testing.T) {
	scenario, err := testkit.LoadScenario(filepath.Join("..", "..", "..", "testdata", "contract-sim"), filepath.Join("fixtures", "S003", "manifest.json"))
	if err != nil {
		t.Fatalf("load scenario: %v", err)
	}
	pkg, ok := testkit.FindPackage(scenario, "c09-runtime-node-agent")
	if !ok {
		t.Fatalf("c09 fixture package not found")
	}
	store, err := NewStore(s003FixtureConfig())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	store.SetClock(func() time.Time { return time.Date(2026, 6, 5, 20, 0, 4, 0, time.UTC) })
	handler := NewHandler(store)

	for _, fixtureID := range []string{
		"node_health_ok",
		"service_stopped",
		"node_unauthorized",
		"node_forbidden",
		"unknown_service",
	} {
		if _, err := testkit.ReplayHTTPFixture(handler, pkg, fixtureID); err != nil {
			t.Fatalf("replay %s: %v", fixtureID, err)
		}
	}
}

func s003FixtureConfig() contracts.NodeConfig {
	return contracts.NodeConfig{
		NodeID:      "node_linux_gpu",
		DisplayName: "Linux GPU",
		Resources: []contracts.NodeResource{{
			ResourceID: "res_gpu_0",
			Tags:       []string{"gpu", "gpu:0"},
			Metadata:   map[string]any{"kind": "gpu"},
		}},
		Auth: []contracts.NodeAuthSubject{
			{Token: "token_s003_runner", SubjectID: "sub_runner_s003", Scopes: []string{"worker"}, AllowedActions: []string{"node.read", "node.service.start", "node.service.touch", "node.service.stop"}},
			{Token: "token_s003_agent", SubjectID: "sub_agent_s003", Scopes: []string{"agent"}, AllowedActions: []string{"node.read"}},
		},
		Services: []contracts.NodeServiceConfig{{
			ServiceID:        "svc_comfyui_gpu",
			RuntimeAdapter:   "docker",
			ProviderEndpoint: "http://node_linux_gpu:8188",
			InitialStatus:    "stopped",
			Docker:           &contracts.DockerRuntimeConfig{ContainerName: "comfyui_gpu"},
		}},
	}
}

func TestHandlerNodeLifecycle(t *testing.T) {
	handler := NewHandler(newTestStore(t))
	headers := map[string]string{"Authorization": "Bearer token_runner"}

	health := doJSON(t, handler, http.MethodGet, "/v1/node/health", headers, http.StatusOK)
	if health["status"] != "healthy" {
		t.Fatalf("health = %#v", health)
	}
	if details, ok := health["details"].(map[string]any); !ok || details["component"] != "node" {
		t.Fatalf("health details = %#v", health["details"])
	}
	resources := doJSON(t, handler, http.MethodGet, "/v1/node/resources", headers, http.StatusOK)
	if len(resources["items"].([]any)) != 1 {
		t.Fatalf("resources = %#v", resources)
	}
	service := doJSON(t, handler, http.MethodGet, "/v1/node/services/svc_comfyui_gpu", headers, http.StatusOK)
	if service["status"] != "stopped" {
		t.Fatalf("initial service = %#v", service)
	}
	starting := doJSON(t, handler, http.MethodPost, "/v1/node/services/svc_comfyui_gpu/start", map[string]string{
		"Authorization":   "Bearer token_runner",
		"Idempotency-Key": "start-http-1",
	}, http.StatusAccepted)
	if starting["status"] != "starting" {
		t.Fatalf("start = %#v", starting)
	}
	running := doJSON(t, handler, http.MethodGet, "/v1/node/services/svc_comfyui_gpu", headers, http.StatusOK)
	if running["status"] != "running" {
		t.Fatalf("running = %#v", running)
	}
	touched := doJSON(t, handler, http.MethodPost, "/v1/node/services/svc_comfyui_gpu/touch", map[string]string{
		"Authorization": "Bearer token_runner",
	}, http.StatusOK)
	if touched["status"] != "running" {
		t.Fatalf("touch = %#v", touched)
	}
	stopped := doJSON(t, handler, http.MethodPost, "/v1/node/services/svc_comfyui_gpu/stop", map[string]string{
		"Authorization":   "Bearer token_runner",
		"Idempotency-Key": "stop-http-1",
	}, http.StatusAccepted)
	if stopped["status"] != "stopped" {
		t.Fatalf("stopped = %#v", stopped)
	}
	replayedStop := doJSON(t, handler, http.MethodPost, "/v1/node/services/svc_comfyui_gpu/stop", map[string]string{
		"Authorization":   "Bearer token_runner",
		"Idempotency-Key": "stop-http-1",
	}, http.StatusOK)
	if replayedStop["status"] != "stopped" {
		t.Fatalf("replayed stop = %#v", replayedStop)
	}
	events := doJSON(t, handler, http.MethodGet, "/v1/node/events", headers, http.StatusOK)
	eventItems := events["items"].([]any)
	if len(eventItems) != 3 {
		t.Fatalf("events = %#v", events)
	}
	if eventItems[0].(map[string]any)["action"] != "start" ||
		eventItems[1].(map[string]any)["action"] != "touch" ||
		eventItems[2].(map[string]any)["action"] != "stop" {
		t.Fatalf("event actions = %#v", eventItems)
	}
	metrics := doJSON(t, handler, http.MethodGet, "/v1/node/metrics", headers, http.StatusOK)
	if metrics["component"] != "node" {
		t.Fatalf("metrics = %#v", metrics)
	}
	assertMetric(t, metrics, "node_service_start_total", map[string]string{"node_id": "node_linux_gpu"}, 1)
	assertMetric(t, metrics, "node_service_stop_total", map[string]string{"node_id": "node_linux_gpu"}, 1)
	assertMetric(t, metrics, "node_lifecycle_events_total", map[string]string{"node_id": "node_linux_gpu"}, 3)
	assertMetric(t, metrics, "http_requests_total", map[string]string{"method": "POST", "route_group": "/v1/node/services/{service_id}/start", "status_class": "2xx"}, 1)
	assertMetric(t, metrics, "http_requests_total", map[string]string{"method": "POST", "route_group": "/v1/node/services/{service_id}/touch", "status_class": "2xx"}, 1)
}

func TestHandlerNodeEventsStartsAsEmptyArray(t *testing.T) {
	handler := NewHandler(newTestStore(t))
	events := doJSON(t, handler, http.MethodGet, "/v1/node/events", map[string]string{
		"Authorization": "Bearer token_runner",
	}, http.StatusOK)
	items, ok := events["items"].([]any)
	if !ok || len(items) != 0 {
		t.Fatalf("events = %#v", events)
	}
}

func TestHandlerNodeResourcesPaginate(t *testing.T) {
	handler := NewHandler(newPaginatedNodeStore(t))
	headers := map[string]string{"Authorization": "Bearer token_runner"}

	first := doJSON(t, handler, http.MethodGet, "/v1/node/resources?limit=1", headers, http.StatusOK)
	items := first["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["resource_id"] != "res_gpu_0" {
		t.Fatalf("first resources page = %#v", first)
	}
	cursor, ok := first["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("first resources page missing cursor = %#v", first)
	}

	second := doJSON(t, handler, http.MethodGet, "/v1/node/resources?limit=1&cursor="+cursor, headers, http.StatusOK)
	items = second["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["resource_id"] != "res_gpu_1" || second["next_cursor"] != nil {
		t.Fatalf("second resources page = %#v", second)
	}

	invalidLimit := doJSONEnvelope(t, handler, http.MethodGet, "/v1/node/resources?limit=0", headers, http.StatusBadRequest)
	if invalidLimit["error"].(map[string]any)["code"] != "validation_failed" {
		t.Fatalf("invalid resource limit error = %#v", invalidLimit)
	}
	invalidCursor := doJSONEnvelope(t, handler, http.MethodGet, "/v1/node/resources?cursor=cursor_node_events_000001", headers, http.StatusBadRequest)
	if invalidCursor["error"].(map[string]any)["code"] != "invalid_cursor" {
		t.Fatalf("invalid resource cursor error = %#v", invalidCursor)
	}
}

func TestHandlerNodeServicesPaginate(t *testing.T) {
	handler := NewHandler(newPaginatedNodeStore(t))
	headers := map[string]string{"Authorization": "Bearer token_runner"}

	first := doJSON(t, handler, http.MethodGet, "/v1/node/services?limit=1", headers, http.StatusOK)
	items := first["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["service_id"] != "svc_a" {
		t.Fatalf("first services page = %#v", first)
	}
	cursor, ok := first["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("first services page missing cursor = %#v", first)
	}

	second := doJSON(t, handler, http.MethodGet, "/v1/node/services?limit=1&cursor="+cursor, headers, http.StatusOK)
	items = second["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["service_id"] != "svc_z" || second["next_cursor"] != nil {
		t.Fatalf("second services page = %#v", second)
	}

	invalidLimit := doJSONEnvelope(t, handler, http.MethodGet, "/v1/node/services?limit=0", headers, http.StatusBadRequest)
	if invalidLimit["error"].(map[string]any)["code"] != "validation_failed" {
		t.Fatalf("invalid service limit error = %#v", invalidLimit)
	}
	invalidCursor := doJSONEnvelope(t, handler, http.MethodGet, "/v1/node/services?cursor=cursor_node_resources_000001", headers, http.StatusBadRequest)
	if invalidCursor["error"].(map[string]any)["code"] != "invalid_cursor" {
		t.Fatalf("invalid service cursor error = %#v", invalidCursor)
	}
}

func TestHandlerNodeEventsPaginate(t *testing.T) {
	store := newTestStore(t)
	if _, _, err := store.StartService("svc_comfyui_gpu", "start-http-events"); err != nil {
		t.Fatalf("start service: %v", err)
	}
	if _, err := store.TouchService("svc_comfyui_gpu"); err != nil {
		t.Fatalf("touch service: %v", err)
	}
	if _, _, err := store.StopService("svc_comfyui_gpu", "stop-http-events"); err != nil {
		t.Fatalf("stop service: %v", err)
	}
	handler := NewHandler(store)
	headers := map[string]string{"Authorization": "Bearer token_runner"}

	first := doJSON(t, handler, http.MethodGet, "/v1/node/events?limit=2", headers, http.StatusOK)
	items := first["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["action"] != "start" || items[1].(map[string]any)["action"] != "touch" {
		t.Fatalf("first events page = %#v", first)
	}
	cursor, ok := first["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("first events page missing cursor = %#v", first)
	}

	second := doJSON(t, handler, http.MethodGet, "/v1/node/events?limit=2&cursor="+cursor, headers, http.StatusOK)
	items = second["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["action"] != "stop" || second["next_cursor"] != nil {
		t.Fatalf("second events page = %#v", second)
	}

	invalidLimit := doJSONEnvelope(t, handler, http.MethodGet, "/v1/node/events?limit=0", headers, http.StatusBadRequest)
	if invalidLimit["error"].(map[string]any)["code"] != "validation_failed" {
		t.Fatalf("invalid event limit error = %#v", invalidLimit)
	}
	invalidCursor := doJSONEnvelope(t, handler, http.MethodGet, "/v1/node/events?cursor=cursor_node_services_000001", headers, http.StatusBadRequest)
	if invalidCursor["error"].(map[string]any)["code"] != "invalid_cursor" {
		t.Fatalf("invalid event cursor error = %#v", invalidCursor)
	}
}

func TestHandlerRejectsUnauthorizedLifecycle(t *testing.T) {
	handler := NewHandler(newTestStore(t))
	envelope := doJSONEnvelope(t, handler, http.MethodPost, "/v1/node/services/svc_comfyui_gpu/start", map[string]string{
		"Authorization": "Bearer token_agent",
	}, http.StatusForbidden)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "forbidden" {
		t.Fatalf("error = %#v", errObj)
	}
}

func TestHandlerRequiresIdempotencyForStart(t *testing.T) {
	handler := NewHandler(newTestStore(t))
	envelope := doJSONEnvelope(t, handler, http.MethodPost, "/v1/node/services/svc_comfyui_gpu/start", map[string]string{
		"Authorization": "Bearer token_runner",
	}, http.StatusBadRequest)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "missing_idempotency_key" {
		t.Fatalf("error = %#v", errObj)
	}
}

func TestHandlerRequiresIdempotencyForStop(t *testing.T) {
	handler := NewHandler(newTestStore(t))
	envelope := doJSONEnvelope(t, handler, http.MethodPost, "/v1/node/services/svc_comfyui_gpu/stop", map[string]string{
		"Authorization": "Bearer token_runner",
	}, http.StatusBadRequest)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "missing_idempotency_key" {
		t.Fatalf("error = %#v", errObj)
	}
}

func doJSON(t *testing.T, handler http.Handler, method, path string, headers map[string]string, wantStatus int) map[string]any {
	t.Helper()
	envelope := doJSONEnvelope(t, handler, method, path, headers, wantStatus)
	if !envelope["ok"].(bool) {
		t.Fatalf("error response for %s %s: %#v", method, path, envelope)
	}
	return envelope["data"].(map[string]any)
}

func doJSONEnvelope(t *testing.T, handler http.Handler, method, path string, headers map[string]string, wantStatus int) map[string]any {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, rec.Code, wantStatus, rec.Body.String())
	}
	var envelope map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return envelope
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

func newPaginatedNodeStore(t *testing.T) *Store {
	t.Helper()
	cfg := testConfig()
	cfg.Resources = []contracts.NodeResource{
		{ResourceID: "res_gpu_0", Tags: []string{"gpu"}},
		{ResourceID: "res_gpu_1", Tags: []string{"gpu"}},
	}
	cfg.Services = []contracts.NodeServiceConfig{
		{ServiceID: "svc_z", RuntimeAdapter: "fake", ProviderEndpoint: "http://node_linux_gpu:9002", InitialStatus: "stopped"},
		{ServiceID: "svc_a", RuntimeAdapter: "fake", ProviderEndpoint: "http://node_linux_gpu:9001", InitialStatus: "stopped"},
	}
	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return store
}
