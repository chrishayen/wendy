package noderegistry

import (
	"errors"
	"testing"
	"time"

	"pacp/internal/contracts"
)

func TestStoreRegistersTrustedConfiguredNodeAndResolvesRunnable(t *testing.T) {
	store := NewStore()
	record, err := store.Register(contracts.RegisterNodeRequest{
		NodeID:     "node_linux_gpu",
		URL:        "http://linux-box:18087/",
		TrustState: contracts.NodeTrustTrusted,
		Status:     contracts.NodeStatusRegistered,
		Tags:       []string{"gpu", "gpu"},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if record.URL != "http://linux-box:18087" || record.TrustState != contracts.NodeTrustTrusted || record.Status != contracts.NodeStatusRegistered {
		t.Fatalf("record = %#v", record)
	}
	if len(record.Tags) != 1 || record.Tags[0] != "gpu" {
		t.Fatalf("tags = %#v", record.Tags)
	}
	resolved, err := store.ResolveRunnable("node_linux_gpu")
	if err != nil {
		t.Fatalf("resolve runnable: %v", err)
	}
	if resolved.URL != "http://linux-box:18087" {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestStoreDefaultsPublicRegistrationToUntrusted(t *testing.T) {
	store := NewStore()
	record, err := store.Register(contracts.RegisterNodeRequest{NodeID: "node_mac", URL: "http://mac:18087"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if record.TrustState != contracts.NodeTrustUntrusted {
		t.Fatalf("trust state = %q", record.TrustState)
	}
	if _, err := store.ResolveRunnable("node_mac"); !errors.Is(err, ErrNotRunnable) {
		t.Fatalf("resolve error = %v", err)
	}
}

func TestStoreRejectsExplicitStaleRegistrationStatus(t *testing.T) {
	store := NewStore()
	_, err := store.Register(contracts.RegisterNodeRequest{
		NodeID: "node_linux_gpu",
		URL:    "http://linux-box:18087",
		Status: contracts.NodeStatusStale,
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestStoreBlocksDisabledUnreachableAndStaleNodes(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return now })
	store.SetStaleAfter(time.Minute)
	if _, err := store.Register(contracts.RegisterNodeRequest{
		NodeID:     "node_linux_gpu",
		URL:        "http://linux-box:18087",
		TrustState: contracts.NodeTrustTrusted,
		Status:     contracts.NodeStatusRegistered,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := store.Heartbeat("node_linux_gpu", contracts.NodeHeartbeatRequest{Status: contracts.NodeStatusReachable}); err != nil {
		t.Fatalf("heartbeat reachable: %v", err)
	}
	if _, err := store.ResolveRunnable("node_linux_gpu"); err != nil {
		t.Fatalf("reachable should be runnable: %v", err)
	}
	now = now.Add(2 * time.Minute)
	stale, err := store.Get("node_linux_gpu")
	if err != nil {
		t.Fatalf("get stale: %v", err)
	}
	if stale.Status != contracts.NodeStatusStale {
		t.Fatalf("stale status = %#v", stale)
	}
	if _, err := store.ResolveRunnable("node_linux_gpu"); !errors.Is(err, ErrNotRunnable) {
		t.Fatalf("stale resolve error = %v", err)
	}
	now = time.Date(2026, 6, 8, 12, 5, 0, 0, time.UTC)
	if _, err := store.Heartbeat("node_linux_gpu", contracts.NodeHeartbeatRequest{Status: contracts.NodeStatusUnreachable}); err != nil {
		t.Fatalf("heartbeat unreachable: %v", err)
	}
	if _, err := store.ResolveRunnable("node_linux_gpu"); !errors.Is(err, ErrNotRunnable) {
		t.Fatalf("unreachable resolve error = %v", err)
	}
	if _, err := store.UpdateTrust("node_linux_gpu", contracts.UpdateNodeTrustRequest{TrustState: contracts.NodeTrustDisabled, Reason: "maintenance"}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := store.ResolveRunnable("node_linux_gpu"); !errors.Is(err, ErrNotRunnable) {
		t.Fatalf("disabled resolve error = %v", err)
	}
}

func TestStorePersistsRecords(t *testing.T) {
	path := t.TempDir() + "/node-registry.json"
	store, err := NewPersistentStore(path)
	if err != nil {
		t.Fatalf("new persistent: %v", err)
	}
	if _, err := store.Register(contracts.RegisterNodeRequest{
		NodeID:     "node_linux_gpu",
		URL:        "http://linux-box:18087",
		TrustState: contracts.NodeTrustTrusted,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	reloaded, err := NewPersistentStore(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	record, err := reloaded.Get("node_linux_gpu")
	if err != nil {
		t.Fatalf("get reloaded: %v", err)
	}
	if record.TrustState != contracts.NodeTrustTrusted || record.URL != "http://linux-box:18087" {
		t.Fatalf("record = %#v", record)
	}
}
