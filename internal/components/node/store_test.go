package node

import (
	"errors"
	"os/exec"
	"testing"

	"pacp/internal/contracts"
)

func TestStoreLifecycleAndAuth(t *testing.T) {
	store := newTestStore(t)
	if err := store.CheckAuth("Bearer token_runner", "node.read"); err != nil {
		t.Fatalf("runner read auth: %v", err)
	}
	if err := store.CheckAuth("Bearer token_agent", "node.service.start"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected forbidden agent start, got %v", err)
	}
	if err := store.CheckAuth("bearer token_runner", "node.read"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected malformed credential unauthorized, got %v", err)
	}

	service, err := store.GetService("svc_comfyui_gpu")
	if err != nil {
		t.Fatalf("get service: %v", err)
	}
	if service.Status != "stopped" {
		t.Fatalf("initial service = %#v", service)
	}
	starting, status, err := store.StartService("svc_comfyui_gpu", "start-1")
	if err != nil {
		t.Fatalf("start service: %v", err)
	}
	if status != 202 || starting.Status != "starting" {
		t.Fatalf("start response status=%d service=%#v", status, starting)
	}
	running, err := store.GetService("svc_comfyui_gpu")
	if err != nil {
		t.Fatalf("poll service: %v", err)
	}
	if running.Status != "running" {
		t.Fatalf("running service = %#v", running)
	}
	replay, status, err := store.StartService("svc_comfyui_gpu", "start-1")
	if err != nil {
		t.Fatalf("start replay: %v", err)
	}
	if status != 200 || replay.Status != "running" {
		t.Fatalf("start replay status=%d service=%#v", status, replay)
	}
	stopped, err := store.StopService("svc_comfyui_gpu")
	if err != nil {
		t.Fatalf("stop service: %v", err)
	}
	if stopped.Status != "stopped" {
		t.Fatalf("stopped service = %#v", stopped)
	}
}

func TestStoreResources(t *testing.T) {
	store := newTestStore(t)
	resources := store.Resources()
	if len(resources) != 1 || resources[0].ResourceID != "res_gpu_0" {
		t.Fatalf("resources = %#v", resources)
	}
}

func TestStoreProcessRuntimeLifecycle(t *testing.T) {
	sleepPath, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep command is not available")
	}
	cfg := testConfig()
	cfg.Services = []contracts.NodeServiceConfig{{
		ServiceID:        "svc_process_provider",
		RuntimeAdapter:   "process",
		ProviderEndpoint: "http://localhost:18088",
		InitialStatus:    "stopped",
		Process: &contracts.ProcessRuntimeConfig{
			Command:            []string{sleepPath, "60"},
			StopTimeoutSeconds: 1,
		},
	}}
	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("new process store: %v", err)
	}
	t.Cleanup(func() {
		_, _ = store.StopService("svc_process_provider")
	})

	starting, status, err := store.StartService("svc_process_provider", "start-process-1")
	if err != nil {
		t.Fatalf("start process service: %v", err)
	}
	if status != 202 || starting.Status != "starting" {
		t.Fatalf("start response status=%d service=%#v", status, starting)
	}
	running, err := store.GetService("svc_process_provider")
	if err != nil {
		t.Fatalf("poll process service: %v", err)
	}
	if running.Status != "running" {
		t.Fatalf("running process service = %#v", running)
	}
	replay, status, err := store.StartService("svc_process_provider", "start-process-1")
	if err != nil {
		t.Fatalf("replay process start: %v", err)
	}
	if status != 200 || replay.Status != "running" {
		t.Fatalf("replay status=%d service=%#v", status, replay)
	}
	stopped, err := store.StopService("svc_process_provider")
	if err != nil {
		t.Fatalf("stop process service: %v", err)
	}
	if stopped.Status != "stopped" {
		t.Fatalf("stopped process service = %#v", stopped)
	}
	record := store.services["svc_process_provider"]
	if record.process != nil {
		t.Fatalf("process runtime was not cleared")
	}
}

func TestStoreRejectsInvalidProcessRuntimeConfig(t *testing.T) {
	cfg := testConfig()
	cfg.Services = []contracts.NodeServiceConfig{{
		ServiceID:        "svc_process_provider",
		RuntimeAdapter:   "process",
		ProviderEndpoint: "http://localhost:18088",
		Process:          &contracts.ProcessRuntimeConfig{},
	}}
	if _, err := NewStore(cfg); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(testConfig())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return store
}

func testConfig() contracts.NodeConfig {
	return contracts.NodeConfig{
		NodeID:      "node_linux_gpu",
		DisplayName: "Linux GPU",
		Resources: []contracts.NodeResource{{
			ResourceID: "res_gpu_0",
			Tags:       []string{"gpu", "gpu:0"},
			Metadata:   map[string]any{"kind": "gpu"},
		}},
		Auth: []contracts.NodeAuthSubject{
			{Token: "token_runner", SubjectID: "sub_runner", Scopes: []string{"worker"}, AllowedActions: []string{"node.read", "node.service.start", "node.service.stop"}},
			{Token: "token_agent", SubjectID: "sub_agent", Scopes: []string{"agent"}, AllowedActions: []string{"node.read"}},
		},
		Services: []contracts.NodeServiceConfig{{
			ServiceID:        "svc_comfyui_gpu",
			RuntimeAdapter:   "fake",
			ProviderEndpoint: "http://node_linux_gpu:8188",
			InitialStatus:    "stopped",
		}},
	}
}
