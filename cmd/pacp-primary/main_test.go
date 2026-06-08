package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"pacp/internal/contracts"
	"pacp/internal/deploy"
)

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
