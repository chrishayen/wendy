package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"pacp/internal/components/artifacts"
	"pacp/internal/components/catalog"
	"pacp/internal/components/jobs"
	"pacp/internal/components/policy"
	"pacp/internal/contracts"
	"pacp/internal/provider"
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
	idempotency := details["idempotency"].(map[string]any)
	if idempotency["store_backend"] != "memory" || idempotency["record_count"] != float64(0) {
		t.Fatalf("health response = %#v", envelope)
	}
}

func TestGatewayMetricsReportsConfiguredDownstreams(t *testing.T) {
	handler := NewHandler(Config{CatalogURL: "http://catalog.local", JobsURL: "http://jobs.local"})
	healthReq := httptest.NewRequest(http.MethodGet, "/v1/gateway/health", nil)
	healthRec := httptest.NewRecorder()
	handler.ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", healthRec.Code, healthRec.Body.String())
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
	assertMetric(t, data, "http_requests_total", map[string]string{"method": "GET", "route_group": "/v1/gateway/health", "status_class": "2xx"}, 1)
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
	}, map[string]string{"Authorization": "Bearer token_agent", "Idempotency-Key": "invoke-1"}, http.StatusCreated)
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
	}, map[string]string{"Authorization": "Bearer token_agent", "Idempotency-Key": "invoke-1"}, http.StatusOK)
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
	}, map[string]string{"Authorization": "Bearer token_agent", "Idempotency-Key": "invoke-2"}, http.StatusCreated)
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
	}, map[string]string{"Authorization": "Bearer token_agent", "Idempotency-Key": "invoke-running-cancel"}, http.StatusCreated)
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

	artifactStore, err := artifacts.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new artifact store: %v", err)
	}
	artifactsServer := httptest.NewServer(artifacts.NewHandler(artifactStore))

	gateway := NewHandler(Config{
		CatalogURL:        catalogServer.URL,
		PolicyURL:         policyServer.URL,
		JobsURL:           jobsServer.URL,
		ArtifactsURL:      artifactsServer.URL,
		GatewayCredential: "Bearer token_gateway",
		Client:            policyServer.Client(),
	})
	env := &gatewayTestEnv{
		gateway:       gateway,
		jobStore:      jobStore,
		artifactStore: artifactStore,
		servers:       []*httptest.Server{policyServer, catalogServer, jobsServer, artifactsServer},
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
