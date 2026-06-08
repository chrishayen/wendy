package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"pacp/internal/contracts"
	"pacp/internal/deploy"
	"pacp/internal/provider"
)

type primaryTestEnvelope struct {
	OK    bool                  `json:"ok"`
	Data  json.RawMessage       `json:"data"`
	Error contracts.ErrorObject `json:"error"`
}

func TestRunPrimaryStackServesCoreHealth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan primaryEndpoints, 1)
	errCh := make(chan error, 1)
	cfg := ephemeralPrimaryConfig(t)
	cfg.ready = ready
	go func() {
		errCh <- runPrimaryStack(ctx, cfg)
	}()

	endpoints := waitForPrimaryReady(t, ready)
	client := &http.Client{Timeout: time.Second}
	for _, target := range []struct {
		url  string
		path string
	}{
		{url: endpoints.CatalogURL, path: "/v1/catalog/health"},
		{url: endpoints.JobsURL, path: "/v1/jobs/health"},
		{url: endpoints.LeasesURL, path: "/v1/leases/health"},
		{url: endpoints.ArtifactsURL, path: "/v1/artifacts/health"},
		{url: endpoints.PolicyURL, path: "/v1/policy/health"},
		{url: endpoints.GatewayURL, path: "/v1/gateway/health"},
	} {
		assertHealth(t, client, target.url+target.path, "")
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run primary: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("primary stack did not stop after context cancellation")
	}
}

func TestRunPrimaryStackProtectsCoreComponentsWithToken(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan primaryEndpoints, 1)
	errCh := make(chan error, 1)
	cfg := ephemeralPrimaryConfig(t)
	cfg.ComponentToken = "component-token"
	cfg.ready = ready
	go func() {
		errCh <- runPrimaryStack(ctx, cfg)
	}()

	endpoints := waitForPrimaryReady(t, ready)
	client := &http.Client{Timeout: time.Second}
	unauthorized := assertStatus(t, client, endpoints.JobsURL+"/v1/jobs/health", "", http.StatusUnauthorized)
	_ = unauthorized.Body.Close()
	assertHealth(t, client, endpoints.JobsURL+"/v1/jobs/health", "Bearer component-token")
	assertHealth(t, client, endpoints.GatewayURL+"/v1/gateway/health", "")

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run primary: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("primary stack did not stop after context cancellation")
	}
}

func TestRunPrimaryStackRouteAwareComponentAuth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan primaryEndpoints, 1)
	errCh := make(chan error, 1)
	cfg := ephemeralPrimaryConfig(t)
	cfg.ComponentToken = "component-token"
	cfg.RouteAwareAuth = true
	cfg.PolicySeedPath = writePrimaryPolicySeed(t)
	cfg.ready = ready
	go func() {
		errCh <- runPrimaryStack(ctx, cfg)
	}()

	endpoints := waitForPrimaryReady(t, ready)
	client := &http.Client{Timeout: time.Second}
	unauthorized := assertStatus(t, client, endpoints.CatalogURL+"/v1/catalog/capabilities", "", http.StatusUnauthorized)
	_ = unauthorized.Body.Close()
	forbidden := assertStatus(t, client, endpoints.CatalogURL+"/v1/catalog/capabilities", "Bearer worker-token", http.StatusForbidden)
	_ = forbidden.Body.Close()
	authorized := assertStatus(t, client, endpoints.CatalogURL+"/v1/catalog/capabilities", "Bearer component-token", http.StatusOK)
	_ = authorized.Body.Close()

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run primary: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("primary stack did not stop after context cancellation")
	}
}

func TestRunPrimaryStackRouteAwareAsyncArtifactFlow(t *testing.T) {
	providerServer, err := provider.NewServer(primaryTestProviderManifest("http://provider.local"), map[string]provider.CapabilityHandler{
		"cap_primary_artifact": func(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
			body := []byte("primary artifact bytes")
			sum := sha256.Sum256(body)
			return contracts.ProviderInvokeResponse{
				Output: map[string]any{"ok": true},
				Artifacts: []contracts.ProviderArtifact{{
					Name:          "primary-artifact.txt",
					MediaType:     "text/plain",
					ContentBase64: base64.StdEncoding.EncodeToString(body),
					Checksum:      "sha256:" + hex.EncodeToString(sum[:]),
				}},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("new provider server: %v", err)
	}
	providerHTTP := httptest.NewServer(providerServer)
	defer providerHTTP.Close()

	manifestPath := writePrimaryManifest(t, primaryTestProviderManifest(providerHTTP.URL))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan primaryEndpoints, 1)
	errCh := make(chan error, 1)
	cfg := ephemeralPrimaryConfig(t)
	cfg.DisableRunner = false
	cfg.RouteAwareAuth = true
	cfg.ComponentToken = "component-token"
	cfg.RunnerCredential = "worker-token"
	cfg.RunnerSubjectID = "sub_worker"
	cfg.RunnerActorID = "sub_worker"
	cfg.WorkerID = "runner_primary_test"
	cfg.PollInterval = 10 * time.Millisecond
	cfg.ManifestPath = manifestPath
	cfg.PolicySeedPath = writePrimaryPolicySeed(t)
	cfg.ready = ready
	go func() {
		errCh <- runPrimaryStack(ctx, cfg)
	}()

	endpoints := waitForPrimaryReady(t, ready)
	client := &http.Client{Timeout: 2 * time.Second}
	jobID := invokePrimaryArtifact(t, client, endpoints.GatewayURL, "token_agent")
	waitForPrimaryJob(t, client, endpoints.GatewayURL, "token_agent", jobID)
	artifactID := firstPrimaryArtifact(t, client, endpoints.GatewayURL, "token_agent", jobID)
	content := readPrimaryArtifactContent(t, client, endpoints.GatewayURL, "token_agent", artifactID)
	if string(content) != "primary artifact bytes" {
		t.Fatalf("artifact content = %q", string(content))
	}

	client.CloseIdleConnections()
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run primary: %v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("primary stack did not stop after context cancellation")
	}
}

func TestParseNodeURLMap(t *testing.T) {
	parsed, err := parseNodeURLMap("node_linux=http://linux:18087, node_mac=http://mac:18087/")
	if err != nil {
		t.Fatalf("parse node urls: %v", err)
	}
	if parsed["node_linux"] != "http://linux:18087" {
		t.Fatalf("node_linux URL = %q", parsed["node_linux"])
	}
	if parsed["node_mac"] != "http://mac:18087" {
		t.Fatalf("node_mac URL = %q", parsed["node_mac"])
	}
	if _, err := parseNodeURLMap("bad-entry"); err == nil {
		t.Fatal("expected invalid node URL mapping error")
	}
}

func TestLoadPrimaryInputsFromRenderedBundle(t *testing.T) {
	outDir := t.TempDir()
	raw, err := os.ReadFile("../../testdata/deploy/generic-gpu-bundle.json")
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	bundle, err := deploy.Parse(raw)
	if err != nil {
		t.Fatalf("parse bundle: %v", err)
	}
	rendered, err := deploy.Render(bundle)
	if err != nil {
		t.Fatalf("render bundle: %v", err)
	}
	files, err := rendered.Files()
	if err != nil {
		t.Fatalf("render files: %v", err)
	}
	for _, file := range files {
		target := filepath.Join(outDir, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(target, file.Data, 0o600); err != nil {
			t.Fatalf("write %s: %v", target, err)
		}
	}

	cfg := ephemeralPrimaryConfig(t)
	cfg.ManifestPath = filepath.Join(outDir, "catalog")
	cfg.ResourcesPath = filepath.Join(outDir, "leases", "resources.json")
	cfg.PolicySeedPath = filepath.Join(outDir, "policy", "policy-seed.json")
	stores, err := newPrimaryStores(cfg)
	if err != nil {
		t.Fatalf("new stores: %v", err)
	}
	if err := loadPrimaryInputs(cfg, stores); err != nil {
		t.Fatalf("load inputs: %v", err)
	}

	if _, ok := stores.catalogStore.GetService("svc_generic_gpu_image"); !ok {
		t.Fatal("catalog did not load generated manifest")
	}
	resource, err := stores.leaseStore.GetResource("res_gpu_0")
	if err != nil {
		t.Fatalf("get generated resource: %v", err)
	}
	if resource.Selector != "gpu" || resource.NodeID != "node_linux_gpu" {
		t.Fatalf("resource = %#v", resource)
	}
	verification, err := stores.policyStore.VerifyCredential(contracts.VerifyCredentialRequest{Credential: "Bearer token_agent"})
	if err != nil {
		t.Fatalf("verify generated policy seed credential: %v", err)
	}
	if !verification.Valid || verification.SubjectID == nil || *verification.SubjectID != "sub_agent" {
		t.Fatalf("verification = %#v", verification)
	}
}

func writePrimaryPolicySeed(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy-seed.json")
	raw := []byte(`{
	  "api_keys": [
	    {"subject_id": "sub_agent", "scopes": ["agent"], "token": "token_agent"},
	    {"subject_id": "sub_component", "scopes": ["component"], "token": "component-token"},
	    {"subject_id": "sub_worker", "scopes": ["worker"], "token": "worker-token"}
	  ]
}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write policy seed: %v", err)
	}
	return path
}

func writePrimaryManifest(t *testing.T, manifest contracts.ProviderManifest) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "provider.manifest.json")
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

func primaryTestProviderManifest(endpoint string) contracts.ProviderManifest {
	return contracts.ProviderManifest{
		SchemaVersion: "v1",
		Service: contracts.Service{
			ID:           "svc_primary_provider",
			Name:         "Primary Test Provider",
			Description:  "Provider used by primary stack integration tests.",
			Version:      "0.1.0",
			ProviderKind: "test",
			Tags:         []string{"test"},
		},
		Provider: contracts.Provider{Endpoint: endpoint, HealthPath: "/v1/provider/health"},
		Capabilities: []contracts.Capability{{
			ID:            "cap_primary_artifact",
			Name:          "Primary artifact",
			Description:   "Produces one artifact through the primary stack.",
			ExecutionMode: "async",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []any{"prompt"},
				"properties": map[string]any{
					"prompt": map[string]any{"type": "string"},
				},
			},
			OutputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"ok": map[string]any{"type": "boolean"},
				},
			},
			SideEffects:   "external",
			ArtifactHints: []contracts.ArtifactHint{{MediaType: "text/plain", Count: "one"}},
			TimeoutHint:   "30s",
		}},
	}
}

func ephemeralPrimaryConfig(t *testing.T) primaryConfig {
	t.Helper()
	root := t.TempDir()
	return primaryConfig{
		CatalogAddr:   "127.0.0.1:0",
		JobsAddr:      "127.0.0.1:0",
		LeasesAddr:    "127.0.0.1:0",
		ArtifactsAddr: "127.0.0.1:0",
		PolicyAddr:    "127.0.0.1:0",
		GatewayAddr:   "127.0.0.1:0",
		ArtifactRoot:  filepath.Join(root, "artifacts"),
		StateDir:      filepath.Join(root, "state"),
		DisableRunner: true,
	}
}

func waitForPrimaryReady(t *testing.T, ready <-chan primaryEndpoints) primaryEndpoints {
	t.Helper()
	select {
	case endpoints := <-ready:
		return endpoints
	case <-time.After(2 * time.Second):
		t.Fatal("primary stack did not become ready")
		return primaryEndpoints{}
	}
}

func assertHealth(t *testing.T, client *http.Client, url, credential string) {
	t.Helper()
	resp := assertStatus(t, client, url, credential, http.StatusOK)
	defer resp.Body.Close()
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode health %s: %v", url, err)
	}
	if !envelope.OK || envelope.Data.Status != "healthy" {
		t.Fatalf("health %s = %#v", url, envelope)
	}
}

func assertStatus(t *testing.T, client *http.Client, url, credential string, want int) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if credential != "" {
		req.Header.Set("Authorization", credential)
	}
	var lastErr error
	for i := 0; i < 20; i++ {
		resp, err := client.Do(req)
		if err == nil {
			if resp.StatusCode == want {
				return resp
			}
			_ = resp.Body.Close()
		} else {
			lastErr = err
		}
		time.Sleep(25 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("GET %s: %v", url, lastErr)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	t.Fatalf("GET %s status = %d, want %d", url, resp.StatusCode, want)
	return nil
}

func invokePrimaryArtifact(t *testing.T, client *http.Client, gatewayURL, token string) string {
	t.Helper()
	var response contracts.InvokeToolResponse
	primaryJSON(t, client, http.MethodPost, gatewayURL+"/v1/tools/cap_primary_artifact/invoke", token, "primary-artifact-test", map[string]any{
		"input": map[string]any{"prompt": "primary stack"},
	}, http.StatusAccepted, &response)
	if response.Mode != "async" || response.JobID == "" {
		t.Fatalf("invoke response = %#v", response)
	}
	return response.JobID
}

func waitForPrimaryJob(t *testing.T, client *http.Client, gatewayURL, token, jobID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var job contracts.AgentJob
		primaryJSON(t, client, http.MethodGet, gatewayURL+"/v1/agent/jobs/"+jobID, token, "", nil, http.StatusOK, &job)
		switch job.State {
		case contracts.JobSucceeded:
			return
		case contracts.JobFailed, contracts.JobCanceled:
			t.Fatalf("job reached terminal state %#v", job)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s did not succeed", jobID)
}

func firstPrimaryArtifact(t *testing.T, client *http.Client, gatewayURL, token, jobID string) string {
	t.Helper()
	var response struct {
		Items []contracts.AgentArtifact `json:"items"`
	}
	primaryJSON(t, client, http.MethodGet, gatewayURL+"/v1/agent/jobs/"+jobID+"/artifacts", token, "", nil, http.StatusOK, &response)
	if len(response.Items) != 1 || response.Items[0].ArtifactID == "" {
		t.Fatalf("artifact list = %#v", response.Items)
	}
	return response.Items[0].ArtifactID
}

func readPrimaryArtifactContent(t *testing.T, client *http.Client, gatewayURL, token, artifactID string) []byte {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, gatewayURL+"/v1/artifacts/"+artifactID+"/content", nil)
	if err != nil {
		t.Fatalf("new artifact content request: %v", err)
	}
	req.Header.Set("Authorization", authorizationHeader(token))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("artifact content request: %v", err)
	}
	defer resp.Body.Close()
	content, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read artifact content: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("artifact content status=%d body=%q", resp.StatusCode, string(content))
	}
	return content
}

func primaryJSON(t *testing.T, client *http.Client, method, endpoint, token, idempotencyKey string, body any, wantStatus int, out any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, endpoint, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", authorizationHeader(token))
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, endpoint, err)
	}
	defer resp.Body.Close()
	var envelope primaryTestEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope status=%d: %v", resp.StatusCode, err)
	}
	if resp.StatusCode != wantStatus || !envelope.OK {
		t.Fatalf("status=%d ok=%t error=%#v", resp.StatusCode, envelope.OK, envelope.Error)
	}
	if out != nil {
		if err := json.Unmarshal(envelope.Data, out); err != nil {
			t.Fatalf("decode data: %v", err)
		}
	}
}
