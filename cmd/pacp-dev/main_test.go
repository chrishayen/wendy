package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"pacp/internal/contracts"
)

type devTestEnvelope struct {
	OK    bool                  `json:"ok"`
	Data  json.RawMessage       `json:"data"`
	Error contracts.ErrorObject `json:"error"`
}

func TestDevManifestIsValidAndUsesEndpoint(t *testing.T) {
	manifest := devManifest("http://provider.local:18088")
	if errs := contracts.ValidateProviderManifest(manifest); len(errs) > 0 {
		t.Fatalf("manifest validation errors = %v", errs)
	}
	if manifest.Provider.Endpoint != "http://provider.local:18088" {
		t.Fatalf("endpoint = %q", manifest.Provider.Endpoint)
	}
	if len(manifest.Capabilities) != 2 {
		t.Fatalf("capabilities = %#v", manifest.Capabilities)
	}
	if manifest.Capabilities[0].ID != "cap_dev_echo" || manifest.Capabilities[0].ExecutionMode != "sync" {
		t.Fatalf("sync capability = %#v", manifest.Capabilities[0])
	}
	if manifest.Capabilities[1].ID != "cap_dev_artifact" || manifest.Capabilities[1].ExecutionMode != "async" {
		t.Fatalf("async capability = %#v", manifest.Capabilities[1])
	}
}

func TestEndpointForAddr(t *testing.T) {
	tests := map[string]string{
		"localhost:18086": "http://localhost:18086",
		":18086":          "http://localhost:18086",
		"127.0.0.1:18086": "http://127.0.0.1:18086",
	}
	for addr, want := range tests {
		if got := endpointForAddr(addr); got != want {
			t.Fatalf("endpointForAddr(%q) = %q want %q", addr, got, want)
		}
	}
}

func TestNewDevStoresStateDirReloadsSeeds(t *testing.T) {
	cfg := devConfig{
		ArtifactRoot:   filepath.Join(t.TempDir(), "artifacts"),
		StateDir:       t.TempDir(),
		AgentToken:     "token_agent",
		ComponentToken: "token_component",
		WorkerToken:    "token_worker",
	}
	manifest := devManifest("http://provider.local:18088")

	stores, err := newDevStores(cfg, manifest)
	if err != nil {
		t.Fatalf("newDevStores first run: %v", err)
	}
	assertSeededDevStores(t, stores, cfg)
	for _, name := range []string{"catalog", "leases", "policy"} {
		if _, err := os.Stat(statePath(cfg, name)); err != nil {
			t.Fatalf("state file %s was not created: %v", name, err)
		}
	}

	reloaded, err := newDevStores(cfg, manifest)
	if err != nil {
		t.Fatalf("newDevStores reload: %v", err)
	}
	assertSeededDevStores(t, reloaded, cfg)
}

func TestDevStackProcessesAsyncArtifact(t *testing.T) {
	cfg := devConfig{
		CatalogAddr:    freeDevAddr(t),
		JobsAddr:       freeDevAddr(t),
		LeasesAddr:     freeDevAddr(t),
		ArtifactsAddr:  freeDevAddr(t),
		PolicyAddr:     freeDevAddr(t),
		GatewayAddr:    freeDevAddr(t),
		ProviderAddr:   freeDevAddr(t),
		ArtifactRoot:   filepath.Join(t.TempDir(), "artifacts"),
		AgentToken:     "token_agent",
		ComponentToken: "token_component",
		WorkerToken:    "token_worker",
		WorkerID:       "runner_dev_test",
		PollInterval:   10 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- runDevStack(ctx, cfg)
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	gatewayURL := endpointForAddr(cfg.GatewayAddr)
	waitForDevGateway(t, client, gatewayURL, errCh)

	jobID := invokeDevArtifact(t, client, gatewayURL, cfg.AgentToken)
	waitForDevJob(t, client, gatewayURL, cfg.AgentToken, jobID)
	artifactID := firstDevArtifact(t, client, gatewayURL, cfg.AgentToken, jobID)
	content := readDevArtifactContent(t, client, gatewayURL, cfg.AgentToken, artifactID)
	if string(content) != "dev artifact bytes" {
		t.Fatalf("artifact content = %q", string(content))
	}

	client.CloseIdleConnections()
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("dev stack returned error: %v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("dev stack did not stop after context cancellation")
	}
}

func assertSeededDevStores(t *testing.T, stores devStores, cfg devConfig) {
	t.Helper()
	if _, ok := stores.catalogStore.GetCapability("cap_dev_echo"); !ok {
		t.Fatal("cap_dev_echo was not seeded")
	}
	if _, ok := stores.catalogStore.GetCapability("cap_dev_artifact"); !ok {
		t.Fatal("cap_dev_artifact was not seeded")
	}
	resource, err := stores.leaseStore.GetResource("res_dev_gpu")
	if err != nil {
		t.Fatalf("res_dev_gpu was not seeded: %v", err)
	}
	if resource.Selector != "gpu" || resource.Status != contracts.ResourceAvailable {
		t.Fatalf("resource = %#v", resource)
	}
	for _, tc := range []struct {
		token     string
		subjectID string
		scope     string
	}{
		{token: cfg.AgentToken, subjectID: "sub_agent_local", scope: "agent"},
		{token: cfg.ComponentToken, subjectID: "sub_gateway_local", scope: "component"},
		{token: cfg.WorkerToken, subjectID: "sub_runner_local", scope: "worker"},
	} {
		verification, err := stores.policyStore.VerifyCredential(contracts.VerifyCredentialRequest{Credential: authorizationHeader(tc.token)})
		if err != nil {
			t.Fatalf("verify %s: %v", tc.subjectID, err)
		}
		if !verification.Valid || verification.SubjectID == nil || *verification.SubjectID != tc.subjectID || !hasScopes(verification.Scopes, []string{tc.scope}) {
			t.Fatalf("verification for %s = %#v", tc.subjectID, verification)
		}
	}
}

func freeDevAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free addr: %v", err)
	}
	defer listener.Close()
	return listener.Addr().String()
}

func waitForDevGateway(t *testing.T, client *http.Client, gatewayURL string, errCh <-chan error) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			t.Fatalf("dev stack exited before health check: %v", err)
		default:
		}
		req, err := http.NewRequest(http.MethodGet, gatewayURL+"/v1/gateway/health", nil)
		if err != nil {
			t.Fatalf("new health request: %v", err)
		}
		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("gateway health did not become ready")
}

func invokeDevArtifact(t *testing.T, client *http.Client, gatewayURL, token string) string {
	t.Helper()
	var response contracts.InvokeToolResponse
	devJSON(t, client, http.MethodPost, gatewayURL+"/v1/tools/cap_dev_artifact/invoke", token, "dev-artifact-test", map[string]any{
		"input": map[string]any{"prompt": "dev stack"},
	}, http.StatusAccepted, &response)
	if response.Mode != "async" || response.JobID == "" {
		t.Fatalf("invoke response = %#v", response)
	}
	return response.JobID
}

func waitForDevJob(t *testing.T, client *http.Client, gatewayURL, token, jobID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var job contracts.AgentJob
		devJSON(t, client, http.MethodGet, gatewayURL+"/v1/agent/jobs/"+jobID, token, "", nil, http.StatusOK, &job)
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

func firstDevArtifact(t *testing.T, client *http.Client, gatewayURL, token, jobID string) string {
	t.Helper()
	var response struct {
		Items []contracts.AgentArtifact `json:"items"`
	}
	devJSON(t, client, http.MethodGet, gatewayURL+"/v1/agent/jobs/"+jobID+"/artifacts", token, "", nil, http.StatusOK, &response)
	if len(response.Items) != 1 || response.Items[0].ArtifactID == "" {
		t.Fatalf("artifact list = %#v", response.Items)
	}
	return response.Items[0].ArtifactID
}

func readDevArtifactContent(t *testing.T, client *http.Client, gatewayURL, token, artifactID string) []byte {
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

func devJSON(t *testing.T, client *http.Client, method, endpoint, token, idempotencyKey string, body any, wantStatus int, out any) {
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
	var envelope devTestEnvelope
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
