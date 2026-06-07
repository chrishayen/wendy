package leases

import (
	"errors"
	"testing"
	"time"

	"pacp/internal/contracts"
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
