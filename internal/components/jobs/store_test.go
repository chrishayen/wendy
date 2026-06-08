package jobs

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"pacp/internal/contracts"
)

func TestJobLifecycle(t *testing.T) {
	store := NewStore()
	store.SetClock(fixedClock("2026-06-05T20:00:00Z"))

	if _, _, err := store.Create(createRequest(), ""); !errors.Is(err, ErrMissingIdempotency) {
		t.Fatalf("expected missing idempotency, got %v", err)
	}

	job, created, err := store.Create(createRequest(), "idem_create_1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !created || job.State != contracts.JobQueued {
		t.Fatalf("created=%v state=%s", created, job.State)
	}

	replay, created, err := store.Create(createRequest(), "idem_create_1")
	if err != nil {
		t.Fatalf("create replay: %v", err)
	}
	if created || replay.JobID != job.JobID {
		t.Fatalf("idempotent replay created=%v job=%s want %s", created, replay.JobID, job.JobID)
	}

	claimed, err := store.Claim(job.JobID, contracts.JobClaimRequest{WorkerID: "runner_1", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.State != contracts.JobClaimed || claimed.Claim.WorkerID != "runner_1" {
		t.Fatalf("claim state=%s claim=%+v", claimed.State, claimed.Claim)
	}

	running, err := store.Heartbeat(job.JobID, contracts.JobHeartbeatRequest{
		WorkerID: "runner_1", TransitionTo: "running", StatusMessage: "running provider invocation",
	})
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if running.State != contracts.JobRunning {
		t.Fatalf("state=%s, want running", running.State)
	}

	if _, _, err := store.AppendLogs(job.JobID, contracts.AppendJobLogRequest{
		WorkerID: "runner_1",
		Entries:  []contracts.JobLogEntry{{Timestamp: "2026-06-05T20:00:01Z", Level: "info", Message: "claimed job"}},
	}); err != nil {
		t.Fatalf("append logs: %v", err)
	}
	logs, _, err := store.Logs(job.JobID, "", 10)
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("log count=%d, want 1", len(logs))
	}

	done, err := store.Complete(job.JobID, contracts.JobCompleteRequest{
		WorkerID: "runner_1", ArtifactRefs: []string{"art_1"},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if done.State != contracts.JobSucceeded || len(done.ArtifactRefs) != 1 {
		t.Fatalf("done=%+v", done)
	}
	if _, err := store.Claim(job.JobID, contracts.JobClaimRequest{WorkerID: "runner_2"}); !errors.Is(err, ErrTerminalState) {
		t.Fatalf("claim terminal err=%v", err)
	}
}

func TestClaimConflictAndExpiry(t *testing.T) {
	now := mustTime("2026-06-05T20:00:00Z")
	store := NewStore()
	store.SetClock(func() time.Time { return now })
	job, _, err := store.Create(createRequest(), "idem_claim_conflict")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.Claim(job.JobID, contracts.JobClaimRequest{WorkerID: "runner_1", LeaseSeconds: 60}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := store.Claim(job.JobID, contracts.JobClaimRequest{WorkerID: "runner_2", LeaseSeconds: 60}); !errors.Is(err, ErrClaimConflict) {
		t.Fatalf("claim conflict err=%v", err)
	}
	now = now.Add(61 * time.Second)
	claimed, err := store.Claim(job.JobID, contracts.JobClaimRequest{WorkerID: "runner_2", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("expired reclaim: %v", err)
	}
	if claimed.Claim.WorkerID != "runner_2" {
		t.Fatalf("claim worker=%s", claimed.Claim.WorkerID)
	}
}

func TestCancelQueuedJobIsIdempotent(t *testing.T) {
	store := NewStore()
	job, _, err := store.Create(createRequest(), "idem_cancel_queued")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	cancelReq := contracts.CancelRequest{Reason: "stop requested"}
	if _, err := store.Cancel(job.JobID, cancelReq, ""); !errors.Is(err, ErrMissingIdempotency) {
		t.Fatalf("expected missing idempotency, got %v", err)
	}
	canceled, err := store.Cancel(job.JobID, cancelReq, "idem_cancel_queued_1")
	if err != nil {
		t.Fatalf("cancel queued: %v", err)
	}
	if canceled.State != contracts.JobCanceled || canceled.StatusMessage != "stop requested" || canceled.Claim != nil {
		t.Fatalf("canceled = %#v", canceled)
	}
	replay, err := store.Cancel(job.JobID, cancelReq, "idem_cancel_queued_1")
	if err != nil {
		t.Fatalf("replay cancel: %v", err)
	}
	if replay.StatusMessage != "stop requested" {
		t.Fatalf("replay changed terminal message: %#v", replay)
	}
	if _, err := store.Cancel(job.JobID, contracts.CancelRequest{Reason: "different reason"}, "idem_cancel_queued_1"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected cancel idempotency conflict, got %v", err)
	}
	terminalReplay, err := store.Cancel(job.JobID, contracts.CancelRequest{Reason: "new terminal key"}, "idem_cancel_queued_2")
	if err != nil {
		t.Fatalf("terminal cancel replay: %v", err)
	}
	if terminalReplay.StatusMessage != "stop requested" {
		t.Fatalf("terminal replay changed message: %#v", terminalReplay)
	}
}

func TestCancelRunningJobIsRejected(t *testing.T) {
	store := NewStore()
	job, _, err := store.Create(createRequest(), "idem_cancel_running")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.Claim(job.JobID, contracts.JobClaimRequest{WorkerID: "runner_1", LeaseSeconds: 60}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := store.Heartbeat(job.JobID, contracts.JobHeartbeatRequest{WorkerID: "runner_1", TransitionTo: "running"}); err != nil {
		t.Fatalf("running heartbeat: %v", err)
	}
	if _, err := store.Cancel(job.JobID, contracts.CancelRequest{Reason: "stop requested"}, "idem_cancel_running_1"); !errors.Is(err, ErrCancellationClosed) {
		t.Fatalf("expected cancellation closed, got %v", err)
	}
	got, err := store.Get(job.JobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.State != contracts.JobRunning {
		t.Fatalf("state after rejected cancel = %s", got.State)
	}
}

func TestListJobsSupportsCursors(t *testing.T) {
	store := NewStore()
	for i := 0; i < 3; i++ {
		if _, _, err := store.Create(createRequest(), "idem_list_cursor_"+string(rune('a'+i))); err != nil {
			t.Fatalf("create job %d: %v", i, err)
		}
	}

	first, next, err := store.List(ListFilter{Limit: 2})
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	if len(first) != 2 || next == nil {
		t.Fatalf("first page len=%d next=%v", len(first), next)
	}
	second, next, err := store.List(ListFilter{Cursor: *next, Limit: 2})
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if len(second) != 1 || next != nil {
		t.Fatalf("second page len=%d next=%v", len(second), next)
	}
	if _, _, err := store.List(ListFilter{Cursor: "cursor_jobs_logs_000001"}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("invalid cursor err=%v", err)
	}
}

func TestPolicyAndAgentProjectionHideMetadata(t *testing.T) {
	store := NewStore()
	job, _, err := store.Create(createRequest(), "idem_policy_projection")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	context, err := store.PolicyContext(job.JobID)
	if err != nil {
		t.Fatalf("policy context: %v", err)
	}
	if context.OwnerSubjectID != "sub_agent_1" {
		t.Fatalf("owner=%s", context.OwnerSubjectID)
	}
	projection, err := store.AgentProjection(job.JobID)
	if err != nil {
		t.Fatalf("agent projection: %v", err)
	}
	if projection.JobID != job.JobID || projection.State != contracts.JobQueued {
		t.Fatalf("projection=%+v", projection)
	}
}

func TestPersistentStoreReloadsJobState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	store, err := NewPersistentStore(path)
	if err != nil {
		t.Fatalf("new persistent store: %v", err)
	}
	store.SetClock(fixedClock("2030-01-01T00:00:00Z"))
	job, created, err := store.Create(createRequest(), "idem_persist_create")
	if err != nil {
		t.Fatalf("create persistent job: %v", err)
	}
	if !created {
		t.Fatalf("expected new persistent job")
	}
	if _, err := store.Claim(job.JobID, contracts.JobClaimRequest{WorkerID: "runner_1", LeaseSeconds: 60}); err != nil {
		t.Fatalf("claim persistent job: %v", err)
	}
	if _, _, err := store.AppendLogs(job.JobID, contracts.AppendJobLogRequest{
		WorkerID: "runner_1",
		Entries:  []contracts.JobLogEntry{{Timestamp: "2030-01-01T00:00:01Z", Level: "info", Message: "persisted log"}},
	}); err != nil {
		t.Fatalf("append persistent logs: %v", err)
	}

	reloaded, err := NewPersistentStore(path)
	if err != nil {
		t.Fatalf("reload persistent store: %v", err)
	}
	reloaded.SetClock(fixedClock("2030-01-01T00:00:10Z"))
	replay, created, err := reloaded.Create(createRequest(), "idem_persist_create")
	if err != nil {
		t.Fatalf("replay persisted create: %v", err)
	}
	if created || replay.JobID != job.JobID {
		t.Fatalf("replay created=%v job=%s want %s", created, replay.JobID, job.JobID)
	}
	logs, _, err := reloaded.Logs(job.JobID, "", 10)
	if err != nil {
		t.Fatalf("persisted logs: %v", err)
	}
	if len(logs) != 1 || logs[0].Message != "persisted log" {
		t.Fatalf("logs = %#v", logs)
	}
	running, err := reloaded.Heartbeat(job.JobID, contracts.JobHeartbeatRequest{WorkerID: "runner_1", TransitionTo: "running"})
	if err != nil {
		t.Fatalf("heartbeat after reload: %v", err)
	}
	if running.State != contracts.JobRunning {
		t.Fatalf("running = %#v", running)
	}

	reloadedAgain, err := NewPersistentStore(path)
	if err != nil {
		t.Fatalf("reload updated persistent store: %v", err)
	}
	got, err := reloadedAgain.Get(job.JobID)
	if err != nil {
		t.Fatalf("get persisted running job: %v", err)
	}
	if got.State != contracts.JobRunning {
		t.Fatalf("persisted state = %s", got.State)
	}
}

func createRequest() contracts.CreateJobRequest {
	return contracts.CreateJobRequest{
		RequesterID:  "sub_agent_1",
		CapabilityID: "cap_image_generate_gpu",
		InputSummary: map[string]any{"prompt_present": true},
		Metadata: map[string]any{
			"execution_plan": map[string]any{
				"capability_id": "cap_image_generate_gpu",
				"subject_id":    "sub_agent_1",
			},
		},
	}
}

func fixedClock(value string) func() time.Time {
	t := mustTime(value)
	return func() time.Time { return t }
}

func mustTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return t
}
