package node

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if _, _, err := store.StartService("svc_comfyui_gpu", ""); !errors.Is(err, ErrMissingIdempotency) {
		t.Fatalf("expected missing idempotency error, got %v", err)
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
	stopped, status, err := store.StopService("svc_comfyui_gpu", "stop-1")
	if err != nil {
		t.Fatalf("stop service: %v", err)
	}
	if status != 202 || stopped.Status != "stopped" {
		t.Fatalf("stop response status=%d service=%#v", status, stopped)
	}
	replayedStop, status, err := store.StopService("svc_comfyui_gpu", "stop-1")
	if err != nil {
		t.Fatalf("stop replay: %v", err)
	}
	if status != 200 || replayedStop.Status != "stopped" {
		t.Fatalf("stop replay status=%d service=%#v", status, replayedStop)
	}
	events, _, err := store.LifecycleEvents(ListOptions{})
	if err != nil {
		t.Fatalf("lifecycle events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("lifecycle events = %#v", events)
	}
	if events[0].Action != "start" || events[0].ServiceID != "svc_comfyui_gpu" || events[0].Status != "starting" || events[0].IdempotencyKey != "start-1" {
		t.Fatalf("start event = %#v", events[0])
	}
	if events[1].Action != "stop" || events[1].ServiceID != "svc_comfyui_gpu" || events[1].Status != "stopped" || events[1].IdempotencyKey != "stop-1" {
		t.Fatalf("stop event = %#v", events[1])
	}
}

func TestStoreResources(t *testing.T) {
	store := newTestStore(t)
	resources, _, err := store.Resources(ListOptions{})
	if err != nil {
		t.Fatalf("resources: %v", err)
	}
	if len(resources) != 1 || resources[0].ResourceID != "res_gpu_0" {
		t.Fatalf("resources = %#v", resources)
	}
}

func TestStoreIdleShutdownStopsFakeService(t *testing.T) {
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	cfg := testConfig()
	cfg.Services[0].IdleTimeoutSeconds = 10
	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	store.SetClock(func() time.Time { return now })

	if _, _, err := store.StartService("svc_comfyui_gpu", "start-idle-1"); err != nil {
		t.Fatalf("start service: %v", err)
	}
	running, err := store.GetService("svc_comfyui_gpu")
	if err != nil {
		t.Fatalf("get running service: %v", err)
	}
	if running.Status != "running" {
		t.Fatalf("running service = %#v", running)
	}
	now = now.Add(11 * time.Second)
	stopped, err := store.GetService("svc_comfyui_gpu")
	if err != nil {
		t.Fatalf("get idle-stopped service: %v", err)
	}
	if stopped.Status != "stopped" {
		t.Fatalf("idle-stopped service = %#v", stopped)
	}
	events, _, err := store.LifecycleEvents(ListOptions{})
	if err != nil {
		t.Fatalf("lifecycle events: %v", err)
	}
	if len(events) != 2 || events[1].Action != "idle_stop" || events[1].Status != "stopped" {
		t.Fatalf("idle lifecycle events = %#v", events)
	}
	metrics := store.Metrics()
	assertStoreMetric(t, metrics, "node_service_idle_stop_total", 1)
	assertStoreMetric(t, metrics, "node_service_stop_total", 1)
	assertStoreMetric(t, metrics, "node_lifecycle_events_total", 2)
}

func TestStoreTouchRefreshesIdleDeadline(t *testing.T) {
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	cfg := testConfig()
	cfg.Services[0].IdleTimeoutSeconds = 10
	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	store.SetClock(func() time.Time { return now })

	if _, _, err := store.StartService("svc_comfyui_gpu", "start-touch-1"); err != nil {
		t.Fatalf("start service: %v", err)
	}
	if _, err := store.GetService("svc_comfyui_gpu"); err != nil {
		t.Fatalf("get running service: %v", err)
	}
	now = now.Add(9 * time.Second)
	touched, err := store.TouchService("svc_comfyui_gpu")
	if err != nil {
		t.Fatalf("touch service: %v", err)
	}
	if touched.Status != "running" {
		t.Fatalf("touched service = %#v", touched)
	}
	now = now.Add(9 * time.Second)
	running, err := store.GetService("svc_comfyui_gpu")
	if err != nil {
		t.Fatalf("get touched service: %v", err)
	}
	if running.Status != "running" {
		t.Fatalf("service stopped before refreshed idle deadline: %#v", running)
	}
	now = now.Add(2 * time.Second)
	stopped, err := store.GetService("svc_comfyui_gpu")
	if err != nil {
		t.Fatalf("get expired service: %v", err)
	}
	if stopped.Status != "stopped" {
		t.Fatalf("expired service = %#v", stopped)
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
		_, _, _ = store.StopService("svc_process_provider", "cleanup-process")
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
	stopped, status, err := store.StopService("svc_process_provider", "stop-process-1")
	if err != nil {
		t.Fatalf("stop process service: %v", err)
	}
	if status != 202 || stopped.Status != "stopped" {
		t.Fatalf("stopped process status=%d service=%#v", status, stopped)
	}
	record := store.services["svc_process_provider"]
	if record.process != nil {
		t.Fatalf("process runtime was not cleared")
	}
}

func TestStoreDockerRuntimeLifecycle(t *testing.T) {
	dockerPath, statePath := writeFakeDocker(t)
	cfg := testConfig()
	cfg.Services = []contracts.NodeServiceConfig{{
		ServiceID:        "svc_docker_provider",
		RuntimeAdapter:   "docker",
		ProviderEndpoint: "http://localhost:18088",
		InitialStatus:    "stopped",
		Docker: &contracts.DockerRuntimeConfig{
			Binary:             dockerPath,
			ContainerName:      "provider-dev",
			StopTimeoutSeconds: 1,
		},
	}}
	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("new docker store: %v", err)
	}

	starting, status, err := store.StartService("svc_docker_provider", "start-docker-1")
	if err != nil {
		t.Fatalf("start docker service: %v", err)
	}
	if status != 202 || starting.Status != "starting" {
		t.Fatalf("start response status=%d service=%#v", status, starting)
	}
	running, err := store.GetService("svc_docker_provider")
	if err != nil {
		t.Fatalf("poll docker service: %v", err)
	}
	if running.Status != "running" {
		t.Fatalf("running docker service = %#v", running)
	}
	replay, status, err := store.StartService("svc_docker_provider", "start-docker-1")
	if err != nil {
		t.Fatalf("replay docker start: %v", err)
	}
	if status != 200 || replay.Status != "running" {
		t.Fatalf("replay status=%d service=%#v", status, replay)
	}
	stopped, status, err := store.StopService("svc_docker_provider", "stop-docker-1")
	if err != nil {
		t.Fatalf("stop docker service: %v", err)
	}
	if status != 202 || stopped.Status != "stopped" {
		t.Fatalf("stopped docker status=%d service=%#v", status, stopped)
	}
	state, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read docker state: %v", err)
	}
	if string(state) != "stopped\n" {
		t.Fatalf("docker state = %q", state)
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

func TestStoreRejectsInvalidDockerRuntimeConfig(t *testing.T) {
	cfg := testConfig()
	cfg.Services = []contracts.NodeServiceConfig{{
		ServiceID:        "svc_docker_provider",
		RuntimeAdapter:   "docker",
		ProviderEndpoint: "http://localhost:18088",
		Docker:           &contracts.DockerRuntimeConfig{},
	}}
	if _, err := NewStore(cfg); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestStoreRejectsInvalidProviderEndpoint(t *testing.T) {
	cfg := testConfig()
	cfg.Services[0].ProviderEndpoint = "node-local:8188"

	_, err := NewStore(cfg)
	if !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), "provider_endpoint must be an absolute http or https URL") {
		t.Fatalf("expected provider_endpoint validation error, got %v", err)
	}
}

func TestStoreRejectsNegativeIdleTimeout(t *testing.T) {
	cfg := testConfig()
	cfg.Services[0].IdleTimeoutSeconds = -1
	if _, err := NewStore(cfg); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestStoreListResourcesPaginatesWithOpaqueCursor(t *testing.T) {
	cfg := testConfig()
	cfg.Resources = []contracts.NodeResource{
		{ResourceID: "res_gpu_0", Tags: []string{"gpu"}},
		{ResourceID: "res_gpu_1", Tags: []string{"gpu"}},
	}
	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	first, next, err := store.Resources(ListOptions{Limit: 1})
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	if len(first) != 1 || first[0].ResourceID != "res_gpu_0" || next == nil {
		t.Fatalf("first page resources=%#v next=%v", first, next)
	}
	if *next != resourceListCursor(1) {
		t.Fatalf("next cursor = %q", *next)
	}

	second, next, err := store.Resources(ListOptions{Cursor: *next, Limit: 1})
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if len(second) != 1 || second[0].ResourceID != "res_gpu_1" || next != nil {
		t.Fatalf("second page resources=%#v next=%v", second, next)
	}

	if _, _, err := store.Resources(ListOptions{Cursor: "cursor_node_events_000001"}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("expected invalid cursor prefix, got %v", err)
	}
	if _, _, err := store.Resources(ListOptions{Cursor: resourceListCursor(3)}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("expected past-end cursor error, got %v", err)
	}
}

func TestStoreListServicesPaginatesWithOpaqueCursor(t *testing.T) {
	cfg := testConfig()
	cfg.Services = []contracts.NodeServiceConfig{
		{ServiceID: "svc_z", RuntimeAdapter: "fake", ProviderEndpoint: "http://node_linux_gpu:9002", InitialStatus: "stopped"},
		{ServiceID: "svc_a", RuntimeAdapter: "fake", ProviderEndpoint: "http://node_linux_gpu:9001", InitialStatus: "stopped"},
	}
	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	first, next, err := store.ListServices(ListOptions{Limit: 1})
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	if len(first) != 1 || first[0].ServiceID != "svc_a" || next == nil {
		t.Fatalf("first page services=%#v next=%v", first, next)
	}
	if *next != serviceListCursor(1) {
		t.Fatalf("next cursor = %q", *next)
	}

	second, next, err := store.ListServices(ListOptions{Cursor: *next, Limit: 1})
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if len(second) != 1 || second[0].ServiceID != "svc_z" || next != nil {
		t.Fatalf("second page services=%#v next=%v", second, next)
	}

	if _, _, err := store.ListServices(ListOptions{Cursor: "cursor_node_resources_000001"}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("expected invalid cursor prefix, got %v", err)
	}
	if _, _, err := store.ListServices(ListOptions{Cursor: serviceListCursor(3)}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("expected past-end cursor error, got %v", err)
	}
}

func TestStoreListLifecycleEventsPaginatesWithOpaqueCursor(t *testing.T) {
	store := newTestStore(t)
	if _, _, err := store.StartService("svc_comfyui_gpu", "start-events"); err != nil {
		t.Fatalf("start service: %v", err)
	}
	if _, err := store.TouchService("svc_comfyui_gpu"); err != nil {
		t.Fatalf("touch service: %v", err)
	}
	if _, _, err := store.StopService("svc_comfyui_gpu", "stop-events"); err != nil {
		t.Fatalf("stop service: %v", err)
	}

	first, next, err := store.LifecycleEvents(ListOptions{Limit: 2})
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	if len(first) != 2 || first[0].Action != "start" || first[1].Action != "touch" || next == nil {
		t.Fatalf("first page events=%#v next=%v", first, next)
	}
	if *next != eventListCursor(2) {
		t.Fatalf("next cursor = %q", *next)
	}

	second, next, err := store.LifecycleEvents(ListOptions{Cursor: *next, Limit: 2})
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if len(second) != 1 || second[0].Action != "stop" || next != nil {
		t.Fatalf("second page events=%#v next=%v", second, next)
	}

	if _, _, err := store.LifecycleEvents(ListOptions{Cursor: "cursor_node_services_000001"}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("expected invalid cursor prefix, got %v", err)
	}
	if _, _, err := store.LifecycleEvents(ListOptions{Cursor: eventListCursor(4)}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("expected past-end cursor error, got %v", err)
	}
}

func assertStoreMetric(t *testing.T, metrics contracts.ComponentMetrics, name string, value float64) {
	t.Helper()
	for _, sample := range metrics.Samples {
		if sample.Name == name {
			if sample.Value != value {
				t.Fatalf("metric %s value=%v want=%v", name, sample.Value, value)
			}
			return
		}
	}
	t.Fatalf("metric %s not found in %#v", name, metrics.Samples)
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(testConfig())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return store
}

func writeFakeDocker(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "docker")
	statePath := filepath.Join(dir, "state")
	script := fmt.Sprintf(`#!/bin/sh
state=%q
case "$1" in
  start)
    echo running > "$state"
    exit 0
    ;;
  inspect)
    if [ -f "$state" ] && [ "$(cat "$state")" = "running" ]; then
      echo true
    else
      echo false
    fi
    exit 0
    ;;
  stop)
    echo stopped > "$state"
    exit 0
    ;;
esac
echo "unexpected docker args: $*" >&2
exit 2
`, statePath)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}
	return path, statePath
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
			{Token: "token_runner", SubjectID: "sub_runner", Scopes: []string{"worker"}, AllowedActions: []string{"node.read", "node.service.start", "node.service.touch", "node.service.stop"}},
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
