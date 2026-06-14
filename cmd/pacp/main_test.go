package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pacp/internal/components/leases"
)

func TestWriteConfigDoesNotOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pacp.yaml")
	cfg, err := defaultConfig()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}
	if err := writeConfig(path, cfg, false); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := writeConfig(path, cfg, false); err == nil {
		t.Fatal("expected no-overwrite error")
	}
}

func TestLoadConfigExpandsEnvRefs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pacp.yaml")
	t.Setenv("PACP_TEST_AGENT_TOKEN", "agent-from-env")
	raw := `
schema_version: v1
primary:
  host: localhost
credentials:
  agent: ${PACP_TEST_AGENT_TOKEN}
  component: component-token
  runner: runner-token
providers:
  - service_id: svc_dev_provider
    kind: dev
    endpoint: http://localhost:18088
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Credentials.Agent != "agent-from-env" {
		t.Fatalf("agent token = %q", cfg.Credentials.Agent)
	}
}

func TestEnsureConfigCreatesConfigAndIgnore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pacp.yaml")
	cfg, created, err := ensureConfig(path)
	if err != nil {
		t.Fatalf("ensure config: %v", err)
	}
	if !created {
		t.Fatal("expected config to be created")
	}
	if cfg.Credentials.Agent == "" || cfg.Credentials.Component == "" || cfg.Credentials.Runner == "" {
		t.Fatalf("credentials were not generated: %#v", cfg.Credentials)
	}
	ignore, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read generated .gitignore: %v", err)
	}
	if !strings.Contains(string(ignore), "pacp.yaml") || !strings.Contains(string(ignore), ".pacp/") {
		t.Fatalf("generated .gitignore missing entries: %q", string(ignore))
	}
}

func TestInitDistributedProfile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pacp.yaml")
	var stdout, stderr bytes.Buffer
	if code := runInit(path, []string{"--profile", "distributed"}, &stdout, &stderr); code != 0 {
		t.Fatalf("init exited %d stderr=%s", code, stderr.String())
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Primary.Host != "primary-host" || cfg.Primary.BindHost != "0.0.0.0" {
		t.Fatalf("primary host/bind host = %q/%q", cfg.Primary.Host, cfg.Primary.BindHost)
	}
	if cfg.Providers[0].ServiceID != "svc_generic_gpu_image" || cfg.Providers[0].NodeID != "node_gpu" {
		t.Fatalf("provider = %#v", cfg.Providers[0])
	}
	if cfg.Nodes[0].NodeID != "node_gpu" || cfg.Nodes[0].Addr != "0.0.0.0:18087" {
		t.Fatalf("node = %#v", cfg.Nodes[0])
	}
}

func TestRegisterResourcesUsesConfig(t *testing.T) {
	store := leases.NewStore()
	cfg := Config{
		Nodes: []RuntimeNodeConfig{{
			NodeID: "node_custom",
			Resources: []ResourceConfig{{
				ResourceID:  "res_custom_gpu",
				Selector:    "gpu",
				DisplayName: "Custom GPU",
				Tags:        []string{"gpu", "remote"},
				Metadata:    map[string]string{"host": "gpu-host"},
			}},
		}},
	}
	if err := registerResources(store, cfg); err != nil {
		t.Fatalf("register resources: %v", err)
	}
	resource, err := store.GetResource("res_custom_gpu")
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	if resource.Selector != "gpu" || resource.NodeID != "node_custom" || resource.Metadata["host"] != "gpu-host" {
		t.Fatalf("resource was not seeded from config: %#v", resource)
	}
}

func TestConfiguredStackServesToolsAndInvoke(t *testing.T) {
	cfg, err := defaultConfig()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}
	cfg.Primary.Host = "127.0.0.1"
	cfg.Primary.Ports.NodeRegistry = freePort(t)
	cfg.Primary.Ports.Catalog = freePort(t)
	cfg.Primary.Ports.Jobs = freePort(t)
	cfg.Primary.Ports.Leases = freePort(t)
	cfg.Primary.Ports.Artifacts = freePort(t)
	cfg.Primary.Ports.Policy = freePort(t)
	cfg.Primary.Ports.Gateway = freePort(t)
	cfg.Primary.Ports.Provider = freePort(t)
	nodePort := freePort(t)
	cfg.Primary.StateDir = filepath.Join(t.TempDir(), "state")
	cfg.Primary.ArtifactRoot = filepath.Join(t.TempDir(), "artifacts")
	cfg.Providers[0].Addr = addrFor(cfg.Primary.Host, cfg.Primary.Ports.Provider)
	cfg.Providers[0].Endpoint = endpointForAddr(cfg.Providers[0].Addr)
	cfg.Nodes[0].Addr = addrFor(cfg.Primary.Host, nodePort)
	cfg.Nodes[0].PublicURL = endpointForAddr(cfg.Nodes[0].Addr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- runConfiguredStack(ctx, cfg, stackOptions{StartProvider: true, StartNodes: true, Ready: ready})
	}()
	select {
	case <-ready:
	case err := <-errCh:
		t.Fatalf("stack failed before ready: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("stack did not become ready")
	}

	client := &http.Client{Timeout: 2 * time.Second}
	var tools bytes.Buffer
	if code := runTools(cfg, &tools, &bytes.Buffer{}, client); code != 0 {
		t.Fatalf("tools exited %d output=%s", code, tools.String())
	}
	if !strings.Contains(tools.String(), "cap_dev_echo") {
		t.Fatalf("tools output missing cap_dev_echo: %s", tools.String())
	}
	var invoke bytes.Buffer
	if code := runInvoke(cfg, []string{"cap_dev_echo", "--input", `{"message":"hello"}`}, &invoke, &bytes.Buffer{}, client); code != 0 {
		t.Fatalf("invoke exited %d output=%s", code, invoke.String())
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Output map[string]string `json:"output"`
		} `json:"data"`
	}
	if err := json.Unmarshal(invoke.Bytes(), &envelope); err != nil {
		t.Fatalf("decode invoke output: %v\n%s", err, invoke.String())
	}
	if !envelope.OK || envelope.Data.Output["message"] != "hello" {
		t.Fatalf("invoke envelope = %#v", envelope)
	}
	var async bytes.Buffer
	if code := runInvoke(cfg, []string{"cap_dev_artifact", "--input", `{"prompt":"red mug"}`, "--wait"}, &async, &bytes.Buffer{}, client); code != 0 {
		t.Fatalf("async invoke exited %d output=%s", code, async.String())
	}
	var jobEnvelope struct {
		OK   bool `json:"ok"`
		Data struct {
			JobID        string   `json:"job_id"`
			State        string   `json:"state"`
			ArtifactRefs []string `json:"artifact_refs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(async.Bytes(), &jobEnvelope); err != nil {
		t.Fatalf("decode async output: %v\n%s", err, async.String())
	}
	if !jobEnvelope.OK || jobEnvelope.Data.State != "succeeded" || len(jobEnvelope.Data.ArtifactRefs) != 1 {
		t.Fatalf("async job envelope = %#v", jobEnvelope)
	}
	var artifacts bytes.Buffer
	if code := runArtifacts(cfg, []string{jobEnvelope.Data.JobID}, &artifacts, &bytes.Buffer{}, client); code != 0 {
		t.Fatalf("artifacts exited %d output=%s", code, artifacts.String())
	}
	if !strings.Contains(artifacts.String(), "dev-artifact.txt") {
		t.Fatalf("artifacts output missing dev artifact: %s", artifacts.String())
	}

	client.CloseIdleConnections()
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("stack returned error: %v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("stack did not stop")
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}
