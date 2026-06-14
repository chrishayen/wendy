package leases

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"wendy/internal/contracts"
)

func TestStoreGrantsHeartbeatsAndReleasesLease(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return now })

	resource, err := store.RegisterResource(contracts.RegisterResourceRequest{
		ResourceID:  "res_gpu_0",
		Selector:    "gpu",
		DisplayName: "Linux GPU",
		Status:      contracts.ResourceAvailable,
		Tags:        []string{"gpu:0"},
	})
	if err != nil {
		t.Fatalf("register resource: %v", err)
	}
	if resource.ResourceID != "res_gpu_0" {
		t.Fatalf("resource id = %q", resource.ResourceID)
	}

	request, err := store.CreateLeaseRequest(contracts.CreateLeaseRequest{
		RequesterID:             "job_1",
		ResourceSelector:        "gpu",
		HeartbeatTimeoutSeconds: 60,
	})
	if err != nil {
		t.Fatalf("create lease request: %v", err)
	}
	if request.State != contracts.LeaseRequestGranted {
		t.Fatalf("state = %q", request.State)
	}
	if request.Lease == nil || request.Lease.HolderID != "job_1" {
		t.Fatalf("lease not granted correctly: %#v", request.Lease)
	}

	now = now.Add(30 * time.Second)
	heartbeat, err := store.Heartbeat(request.Lease.LeaseID, contracts.LeaseHeartbeatRequest{HolderID: "job_1"})
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if heartbeat.ExpiresAt != "2026-06-07T12:01:30Z" {
		t.Fatalf("expires_at = %q", heartbeat.ExpiresAt)
	}

	_, err = store.Release(request.Lease.LeaseID, contracts.LeaseReleaseRequest{
		HolderID: "job_1",
		Reason:   "job completed",
	}, "", "sub_runner")
	if !errors.Is(err, ErrMissingIdempotency) {
		t.Fatalf("expected missing idempotency, got %v", err)
	}

	released, err := store.Release(request.Lease.LeaseID, contracts.LeaseReleaseRequest{
		HolderID: "job_1",
		Reason:   "job completed",
	}, "release-1", "sub_runner")
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if released.ReleasedAt == "" || released.ReleasedBy != "sub_runner" || released.ReleaseReason != "job completed" {
		t.Fatalf("release fields = %#v", released)
	}
	if len(released.Links) != 0 {
		t.Fatalf("released lease links = %#v", released.Links)
	}

	replay, err := store.Release(request.Lease.LeaseID, contracts.LeaseReleaseRequest{
		HolderID: "job_1",
		Reason:   "job completed",
	}, "release-1", "sub_runner")
	if err != nil {
		t.Fatalf("release replay: %v", err)
	}
	if replay.ReleasedAt != released.ReleasedAt {
		t.Fatalf("release replay did not return original response: %#v", replay)
	}
	if events := store.AuditEvents(); len(events) != 1 {
		t.Fatalf("audit event count = %d", len(events))
	}

	_, err = store.Release(request.Lease.LeaseID, contracts.LeaseReleaseRequest{
		HolderID: "job_1",
		Reason:   "different reason",
	}, "release-1", "sub_runner")
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
}

func TestStoreQueuesCancelsAndGrantsNextRequester(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return now })
	_, err := store.RegisterResource(contracts.RegisterResourceRequest{Selector: "gpu", Status: contracts.ResourceAvailable})
	if err != nil {
		t.Fatalf("register resource: %v", err)
	}

	first, err := store.CreateLeaseRequest(contracts.CreateLeaseRequest{RequesterID: "job_1", ResourceSelector: "gpu"})
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	second, err := store.CreateLeaseRequest(contracts.CreateLeaseRequest{RequesterID: "job_2", ResourceSelector: "gpu"})
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	if second.State != contracts.LeaseRequestPending || second.QueuePosition == nil || *second.QueuePosition != 1 {
		t.Fatalf("second queue state = %#v", second)
	}

	canceled, err := store.CancelLeaseRequest(second.RequestID, contracts.CancelRequest{Reason: "no longer needed"})
	if err != nil {
		t.Fatalf("cancel pending: %v", err)
	}
	if canceled.State != contracts.LeaseRequestCanceled || canceled.QueuePosition != nil {
		t.Fatalf("canceled state = %#v", canceled)
	}

	third, err := store.CreateLeaseRequest(contracts.CreateLeaseRequest{RequesterID: "job_3", ResourceSelector: "gpu"})
	if err != nil {
		t.Fatalf("third request: %v", err)
	}
	if third.State != contracts.LeaseRequestPending {
		t.Fatalf("third state = %#v", third)
	}

	_, err = store.Release(first.Lease.LeaseID, contracts.LeaseReleaseRequest{HolderID: "job_1", Reason: "done"}, "release-first", "sub_runner")
	if err != nil {
		t.Fatalf("release first: %v", err)
	}
	granted, err := store.GetLeaseRequest(third.RequestID)
	if err != nil {
		t.Fatalf("get third: %v", err)
	}
	if granted.State != contracts.LeaseRequestGranted || granted.Lease == nil || granted.Lease.HolderID != "job_3" {
		t.Fatalf("third was not granted: %#v", granted)
	}
}

func TestStoreHonorsPriorityWithinQueue(t *testing.T) {
	store := NewStore()
	_, err := store.RegisterResource(contracts.RegisterResourceRequest{Selector: "gpu", Status: contracts.ResourceAvailable})
	if err != nil {
		t.Fatalf("register resource: %v", err)
	}
	first, err := store.CreateLeaseRequest(contracts.CreateLeaseRequest{RequesterID: "job_1", ResourceSelector: "gpu"})
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	low, err := store.CreateLeaseRequest(contracts.CreateLeaseRequest{RequesterID: "job_low", ResourceSelector: "gpu", Priority: 0})
	if err != nil {
		t.Fatalf("low request: %v", err)
	}
	high, err := store.CreateLeaseRequest(contracts.CreateLeaseRequest{RequesterID: "job_high", ResourceSelector: "gpu", Priority: 10})
	if err != nil {
		t.Fatalf("high request: %v", err)
	}
	if low.QueuePosition == nil || *low.QueuePosition != 1 {
		t.Fatalf("low initial position = %#v", low.QueuePosition)
	}
	high, err = store.GetLeaseRequest(high.RequestID)
	if err != nil {
		t.Fatalf("get high: %v", err)
	}
	if high.QueuePosition == nil || *high.QueuePosition != 1 {
		t.Fatalf("high priority position = %#v", high.QueuePosition)
	}

	_, err = store.Release(first.Lease.LeaseID, contracts.LeaseReleaseRequest{HolderID: "job_1"}, "release-priority", "sub_runner")
	if err != nil {
		t.Fatalf("release first: %v", err)
	}
	granted, err := store.GetLeaseRequest(high.RequestID)
	if err != nil {
		t.Fatalf("get high after release: %v", err)
	}
	if granted.State != contracts.LeaseRequestGranted {
		t.Fatalf("high priority request not granted: %#v", granted)
	}
}

func TestStoreExpiresLeaseAndRejectsLateHolderActions(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return now })
	_, err := store.RegisterResource(contracts.RegisterResourceRequest{Selector: "gpu", Status: contracts.ResourceAvailable})
	if err != nil {
		t.Fatalf("register resource: %v", err)
	}
	request, err := store.CreateLeaseRequest(contracts.CreateLeaseRequest{
		RequesterID:             "job_1",
		ResourceSelector:        "gpu",
		HeartbeatTimeoutSeconds: 2,
	})
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	now = now.Add(3 * time.Second)

	_, err = store.Heartbeat(request.Lease.LeaseID, contracts.LeaseHeartbeatRequest{HolderID: "job_1"})
	if !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("expected heartbeat lease expired, got %v", err)
	}
	_, err = store.Release(request.Lease.LeaseID, contracts.LeaseReleaseRequest{HolderID: "job_1"}, "release-expired", "sub_runner")
	if !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("expected release lease expired, got %v", err)
	}

	next, err := store.CreateLeaseRequest(contracts.CreateLeaseRequest{RequesterID: "job_2", ResourceSelector: "gpu"})
	if err != nil {
		t.Fatalf("next request: %v", err)
	}
	if next.State != contracts.LeaseRequestGranted || next.Lease == nil || next.Lease.HolderID != "job_2" {
		t.Fatalf("next request was not granted after expiration: %#v", next)
	}
}

func TestStoreRejectsUnknownAndUnavailableSelectors(t *testing.T) {
	store := NewStore()
	_, err := store.CreateLeaseRequest(contracts.CreateLeaseRequest{RequesterID: "job_1", ResourceSelector: "gpu"})
	if !errors.Is(err, ErrResourceUnavailable) {
		t.Fatalf("expected unknown selector error, got %v", err)
	}
	_, err = store.RegisterResource(contracts.RegisterResourceRequest{Selector: "gpu", Status: contracts.ResourceUnavailable})
	if err != nil {
		t.Fatalf("register unavailable: %v", err)
	}
	_, err = store.CreateLeaseRequest(contracts.CreateLeaseRequest{RequesterID: "job_1", ResourceSelector: "gpu"})
	if !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("expected no capacity error, got %v", err)
	}
}

func TestStoreRejectsHolderMismatch(t *testing.T) {
	store := NewStore()
	_, err := store.RegisterResource(contracts.RegisterResourceRequest{Selector: "gpu", Status: contracts.ResourceAvailable})
	if err != nil {
		t.Fatalf("register resource: %v", err)
	}
	request, err := store.CreateLeaseRequest(contracts.CreateLeaseRequest{RequesterID: "job_1", ResourceSelector: "gpu"})
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	_, err = store.Heartbeat(request.Lease.LeaseID, contracts.LeaseHeartbeatRequest{HolderID: "job_other"})
	if !errors.Is(err, ErrHolderMismatch) {
		t.Fatalf("expected holder mismatch, got %v", err)
	}
}

func TestStoreListResourcesPaginatesWithOpaqueCursor(t *testing.T) {
	store := NewStore()
	for _, resourceID := range []string{"res_gpu_0", "res_gpu_1"} {
		if _, err := store.RegisterResource(contracts.RegisterResourceRequest{
			ResourceID: resourceID,
			Selector:   "gpu",
			Status:     contracts.ResourceAvailable,
		}); err != nil {
			t.Fatalf("register %s: %v", resourceID, err)
		}
	}

	first, next, err := store.ListResources("gpu", ListOptions{Limit: 1})
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	if len(first) != 1 || first[0].ResourceID != "res_gpu_0" || next == nil {
		t.Fatalf("first page resources=%#v next=%v", first, next)
	}
	if *next != resourceListCursor(1) {
		t.Fatalf("next cursor = %q", *next)
	}

	second, next, err := store.ListResources("gpu", ListOptions{Cursor: *next, Limit: 1})
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if len(second) != 1 || second[0].ResourceID != "res_gpu_1" || next != nil {
		t.Fatalf("second page resources=%#v next=%v", second, next)
	}

	if _, _, err := store.ListResources("gpu", ListOptions{Cursor: "cursor_lease_requests_000001"}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("expected invalid cursor prefix, got %v", err)
	}
	if _, _, err := store.ListResources("gpu", ListOptions{Cursor: resourceListCursor(3)}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("expected past-end cursor error, got %v", err)
	}
}

func TestStoreListLeaseRequestsPaginatesWithOpaqueCursor(t *testing.T) {
	store := NewStore()
	if _, err := store.RegisterResource(contracts.RegisterResourceRequest{
		ResourceID: "res_gpu_0",
		Selector:   "gpu",
		Status:     contracts.ResourceAvailable,
	}); err != nil {
		t.Fatalf("register resource: %v", err)
	}
	firstRequest, err := store.CreateLeaseRequest(contracts.CreateLeaseRequest{RequesterID: "job_page", ResourceSelector: "gpu"})
	if err != nil {
		t.Fatalf("create first request: %v", err)
	}
	secondRequest, err := store.CreateLeaseRequest(contracts.CreateLeaseRequest{RequesterID: "job_page", ResourceSelector: "gpu"})
	if err != nil {
		t.Fatalf("create second request: %v", err)
	}

	first, next, err := store.ListLeaseRequestsByRequester("job_page", ListOptions{Limit: 1})
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	if len(first) != 1 || first[0].RequestID != firstRequest.RequestID || next == nil {
		t.Fatalf("first page requests=%#v next=%v", first, next)
	}
	if *next != leaseRequestListCursor(1) {
		t.Fatalf("next cursor = %q", *next)
	}

	second, next, err := store.ListLeaseRequestsByRequester("job_page", ListOptions{Cursor: *next, Limit: 1})
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if len(second) != 1 || second[0].RequestID != secondRequest.RequestID || next != nil {
		t.Fatalf("second page requests=%#v next=%v", second, next)
	}

	if _, _, err := store.ListLeaseRequestsByRequester("job_page", ListOptions{Cursor: "cursor_leases_resources_000001"}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("expected invalid cursor prefix, got %v", err)
	}
	if _, _, err := store.ListLeaseRequestsByRequester("job_page", ListOptions{Cursor: leaseRequestListCursor(3)}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("expected past-end cursor error, got %v", err)
	}
}

func TestPersistentStoreReloadsLeaseState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leases.json")
	store, err := NewPersistentStore(path)
	if err != nil {
		t.Fatalf("new persistent store: %v", err)
	}
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return now })
	if _, err := store.RegisterResource(contracts.RegisterResourceRequest{ResourceID: "res_gpu_0", Selector: "gpu", Status: contracts.ResourceAvailable}); err != nil {
		t.Fatalf("register persistent resource: %v", err)
	}
	first, err := store.CreateLeaseRequest(contracts.CreateLeaseRequest{RequesterID: "job_1", ResourceSelector: "gpu", HeartbeatTimeoutSeconds: 60})
	if err != nil {
		t.Fatalf("first persistent request: %v", err)
	}
	second, err := store.CreateLeaseRequest(contracts.CreateLeaseRequest{RequesterID: "job_2", ResourceSelector: "gpu", HeartbeatTimeoutSeconds: 60})
	if err != nil {
		t.Fatalf("second persistent request: %v", err)
	}
	if second.State != contracts.LeaseRequestPending {
		t.Fatalf("second initial state = %#v", second)
	}

	reloaded, err := NewPersistentStore(path)
	if err != nil {
		t.Fatalf("reload persistent store: %v", err)
	}
	now = now.Add(10 * time.Second)
	reloaded.SetClock(func() time.Time { return now })
	pending, err := reloaded.GetLeaseRequest(second.RequestID)
	if err != nil {
		t.Fatalf("get pending after reload: %v", err)
	}
	if pending.State != contracts.LeaseRequestPending || pending.QueuePosition == nil || *pending.QueuePosition != 1 {
		t.Fatalf("pending after reload = %#v", pending)
	}
	if _, err := reloaded.Heartbeat(first.Lease.LeaseID, contracts.LeaseHeartbeatRequest{HolderID: "job_1"}); err != nil {
		t.Fatalf("heartbeat after reload: %v", err)
	}
	released, err := reloaded.Release(first.Lease.LeaseID, contracts.LeaseReleaseRequest{HolderID: "job_1", Reason: "done"}, "release-persist", "sub_runner")
	if err != nil {
		t.Fatalf("release after reload: %v", err)
	}

	reloadedAgain, err := NewPersistentStore(path)
	if err != nil {
		t.Fatalf("reload released persistent store: %v", err)
	}
	granted, err := reloadedAgain.GetLeaseRequest(second.RequestID)
	if err != nil {
		t.Fatalf("get promoted request after reload: %v", err)
	}
	if granted.State != contracts.LeaseRequestGranted || granted.Lease == nil || granted.Lease.HolderID != "job_2" {
		t.Fatalf("promoted request = %#v", granted)
	}
	replay, err := reloadedAgain.Release(first.Lease.LeaseID, contracts.LeaseReleaseRequest{HolderID: "job_1", Reason: "done"}, "release-persist", "sub_runner")
	if err != nil {
		t.Fatalf("release replay after reload: %v", err)
	}
	if replay.ReleasedAt != released.ReleasedAt {
		t.Fatalf("release replay = %#v want released_at %s", replay, released.ReleasedAt)
	}
	if events := reloadedAgain.AuditEvents(); len(events) != 1 {
		t.Fatalf("audit events = %#v", events)
	}
}
