package jobs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"wendy/internal/contracts"
	"wendy/internal/testkit"
)

func TestHTTPJobLifecycle(t *testing.T) {
	handler := NewHandler(NewStore())

	createResp := requestJSON(t, handler, http.MethodPost, "/v1/jobs", createRequest(), map[string]string{"Idempotency-Key": "create-http-1"})
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	jobID := responseData(t, createResp)["job_id"].(string)

	claimResp := requestJSON(t, handler, http.MethodPost, "/v1/jobs/"+jobID+"/claim", contracts.JobClaimRequest{WorkerID: "runner_1", LeaseSeconds: 60})
	if claimResp.Code != http.StatusOK {
		t.Fatalf("claim status=%d body=%s", claimResp.Code, claimResp.Body.String())
	}

	heartbeatResp := requestJSON(t, handler, http.MethodPost, "/v1/jobs/"+jobID+"/heartbeat", contracts.JobHeartbeatRequest{WorkerID: "runner_1", TransitionTo: "running"})
	if heartbeatResp.Code != http.StatusOK {
		t.Fatalf("heartbeat status=%d body=%s", heartbeatResp.Code, heartbeatResp.Body.String())
	}

	logResp := requestJSON(t, handler, http.MethodPost, "/v1/jobs/"+jobID+"/logs", contracts.AppendJobLogRequest{
		WorkerID: "runner_1",
		Entries:  []contracts.JobLogEntry{{Timestamp: "2026-06-05T20:00:01Z", Level: "info", Message: "running"}},
	})
	if logResp.Code != http.StatusOK {
		t.Fatalf("log status=%d body=%s", logResp.Code, logResp.Body.String())
	}

	completeResp := requestJSON(t, handler, http.MethodPost, "/v1/jobs/"+jobID+"/complete", contracts.JobCompleteRequest{WorkerID: "runner_1", ArtifactRefs: []string{"art_1"}})
	if completeResp.Code != http.StatusOK {
		t.Fatalf("complete status=%d body=%s", completeResp.Code, completeResp.Body.String())
	}
	data := responseData(t, completeResp)
	if data["state"] != string(contracts.JobSucceeded) {
		t.Fatalf("state=%v", data["state"])
	}

	projectionResp := requestJSON(t, handler, http.MethodGet, "/v1/jobs/"+jobID+"/agent-projection", nil)
	if projectionResp.Code != http.StatusOK {
		t.Fatalf("projection status=%d body=%s", projectionResp.Code, projectionResp.Body.String())
	}
	projection := responseData(t, projectionResp)
	if _, hasMetadata := projection["metadata"]; hasMetadata {
		t.Fatalf("agent projection leaked metadata: %+v", projection)
	}
}

func TestHTTPErrors(t *testing.T) {
	handler := NewHandler(NewStore())
	createResp := requestJSON(t, handler, http.MethodPost, "/v1/jobs", createRequest())
	if createResp.Code != http.StatusBadRequest {
		t.Fatalf("create without idempotency status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	if code := responseErrorCode(t, createResp); code != "missing_idempotency_key" {
		t.Fatalf("create without idempotency code=%s", code)
	}

	resp := requestJSON(t, handler, http.MethodGet, "/v1/jobs/job_missing", nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	metrics := requestJSON(t, handler, http.MethodGet, "/v1/jobs/metrics", nil)
	data := responseData(t, metrics)
	assertMetric(t, data, "http_errors_total", map[string]string{"method": "GET", "route_group": "/v1/jobs/{job_id}", "status_class": "4xx"}, 1)
}

func TestHTTPCancelRequiresIdempotencyAndReplays(t *testing.T) {
	handler := NewHandler(NewStore())
	createResp := requestJSON(t, handler, http.MethodPost, "/v1/jobs", createRequest(), map[string]string{"Idempotency-Key": "create-http-cancel"})
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	jobID := responseData(t, createResp)["job_id"].(string)
	cancelReq := contracts.CancelRequest{Reason: "stop requested"}

	missingID := requestJSON(t, handler, http.MethodPost, "/v1/jobs/"+jobID+"/cancel", cancelReq)
	if missingID.Code != http.StatusBadRequest {
		t.Fatalf("cancel without idempotency status=%d body=%s", missingID.Code, missingID.Body.String())
	}
	if code := responseErrorCode(t, missingID); code != "missing_idempotency_key" {
		t.Fatalf("cancel without idempotency code=%s", code)
	}

	canceled := requestJSON(t, handler, http.MethodPost, "/v1/jobs/"+jobID+"/cancel", cancelReq, map[string]string{"Idempotency-Key": "cancel-http-1"})
	if canceled.Code != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", canceled.Code, canceled.Body.String())
	}
	if data := responseData(t, canceled); data["state"] != string(contracts.JobCanceled) || data["status_message"] != "stop requested" {
		t.Fatalf("canceled = %#v", data)
	}

	replay := requestJSON(t, handler, http.MethodPost, "/v1/jobs/"+jobID+"/cancel", cancelReq, map[string]string{"Idempotency-Key": "cancel-http-1"})
	if replay.Code != http.StatusOK {
		t.Fatalf("cancel replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	if data := responseData(t, replay); data["status_message"] != "stop requested" {
		t.Fatalf("replay = %#v", data)
	}

	conflict := requestJSON(t, handler, http.MethodPost, "/v1/jobs/"+jobID+"/cancel", contracts.CancelRequest{Reason: "different"}, map[string]string{"Idempotency-Key": "cancel-http-1"})
	if conflict.Code != http.StatusConflict {
		t.Fatalf("cancel conflict status=%d body=%s", conflict.Code, conflict.Body.String())
	}
	if code := responseErrorCode(t, conflict); code != "idempotency_conflict" {
		t.Fatalf("cancel conflict code=%s", code)
	}
}

func TestHTTPHealth(t *testing.T) {
	handler := NewHandler(NewStore())
	resp := requestJSON(t, handler, http.MethodGet, "/v1/jobs/health", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", resp.Code, resp.Body.String())
	}
	data := responseData(t, resp)
	details := data["details"].(map[string]any)
	if data["status"] != "healthy" || details["component"] != "jobs" {
		t.Fatalf("health = %#v", data)
	}
	if details["store_backend"] != "memory" || details["job_count"] != float64(0) || details["active_claim_count"] != float64(0) {
		t.Fatalf("health = %#v", data)
	}
}

func TestHTTPListJobsCursor(t *testing.T) {
	handler := NewHandler(NewStore())
	for i := 0; i < 3; i++ {
		resp := requestJSON(t, handler, http.MethodPost, "/v1/jobs", createRequest(), map[string]string{"Idempotency-Key": "create-http-list-" + string(rune('a'+i))})
		if resp.Code != http.StatusCreated {
			t.Fatalf("create status=%d body=%s", resp.Code, resp.Body.String())
		}
	}
	first := requestJSON(t, handler, http.MethodGet, "/v1/jobs?limit=2", nil)
	if first.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", first.Code, first.Body.String())
	}
	firstData := responseData(t, first)
	items := firstData["items"].([]any)
	cursor, _ := firstData["next_cursor"].(string)
	if len(items) != 2 || cursor == "" {
		t.Fatalf("first page = %#v", firstData)
	}
	second := requestJSON(t, handler, http.MethodGet, "/v1/jobs?limit=2&cursor="+cursor, nil)
	if second.Code != http.StatusOK {
		t.Fatalf("second list status=%d body=%s", second.Code, second.Body.String())
	}
	secondData := responseData(t, second)
	if len(secondData["items"].([]any)) != 1 || secondData["next_cursor"] != nil {
		t.Fatalf("second page = %#v", secondData)
	}
}

func TestHTTPCreateAndListExposeCapabilityID(t *testing.T) {
	handler := NewHandler(NewStore())
	req := contracts.CreateJobRequest{
		RequesterID:  "sub_agent_2",
		CapabilityID: "cap_text_summarize",
		InputSummary: map[string]any{"text_present": true},
		Metadata:     map[string]any{},
	}
	createResp := requestJSON(t, handler, http.MethodPost, "/v1/jobs", req, map[string]string{"Idempotency-Key": "create-http-capability"})
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	created := responseData(t, createResp)
	if created["capability_id"] != "cap_text_summarize" {
		t.Fatalf("created capability_id = %#v", created)
	}

	listResp := requestJSON(t, handler, http.MethodGet, "/v1/jobs?capability_id=cap_text_summarize", nil)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listResp.Code, listResp.Body.String())
	}
	items := responseData(t, listResp)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["capability_id"] != "cap_text_summarize" {
		t.Fatalf("list items = %#v", items)
	}

	missResp := requestJSON(t, handler, http.MethodGet, "/v1/jobs?capability_id=cap_other", nil)
	if missResp.Code != http.StatusOK {
		t.Fatalf("miss status=%d body=%s", missResp.Code, missResp.Body.String())
	}
	if items := responseData(t, missResp)["items"].([]any); len(items) != 0 {
		t.Fatalf("miss items = %#v", items)
	}
}

func TestHTTPMetrics(t *testing.T) {
	handler := NewHandler(NewStore())
	createResp := requestJSON(t, handler, http.MethodPost, "/v1/jobs", createRequest(), map[string]string{"Idempotency-Key": "create-http-metrics"})
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	resp := requestJSON(t, handler, http.MethodGet, "/v1/jobs/metrics", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("metrics status=%d body=%s", resp.Code, resp.Body.String())
	}
	data := responseData(t, resp)
	if data["component"] != "jobs" {
		t.Fatalf("metrics = %#v", data)
	}
	assertMetric(t, data, "jobs_by_state", map[string]string{"state": "queued"}, 1)
	assertMetric(t, data, "http_requests_total", map[string]string{"method": "POST", "route_group": "/v1/jobs", "status_class": "2xx"}, 1)
}

func TestHTTPMetricsReportsExpiredClaims(t *testing.T) {
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	store := NewStore()
	store.SetClock(func() time.Time { return now })
	handler := NewHandler(store)

	createResp := requestJSON(t, handler, http.MethodPost, "/v1/jobs", createRequest(), map[string]string{"Idempotency-Key": "create-http-expired-claims"})
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	jobID := responseData(t, createResp)["job_id"].(string)

	claimResp := requestJSON(t, handler, http.MethodPost, "/v1/jobs/"+jobID+"/claim", contracts.JobClaimRequest{WorkerID: "runner_1", LeaseSeconds: 1})
	if claimResp.Code != http.StatusOK {
		t.Fatalf("claim status=%d body=%s", claimResp.Code, claimResp.Body.String())
	}
	now = now.Add(2 * time.Second)

	resp := requestJSON(t, handler, http.MethodGet, "/v1/jobs/metrics", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("metrics status=%d body=%s", resp.Code, resp.Body.String())
	}
	data := responseData(t, resp)
	assertMetric(t, data, "jobs_active_claims", nil, 0)
	assertMetric(t, data, "jobs_expired_claims", nil, 1)
}

func TestHandlerReplaysS003JobReadFixtures(t *testing.T) {
	scenario, err := testkit.LoadScenario(filepath.Join("..", "..", "..", "testdata", "contract-sim"), filepath.Join("fixtures", "S003", "manifest.json"))
	if err != nil {
		t.Fatalf("load scenario: %v", err)
	}
	pkg, ok := testkit.FindPackage(scenario, "c05-async-job-service")
	if !ok {
		t.Fatalf("c05 fixture package not found")
	}

	tests := []struct {
		fixtureID string
		seed      func(*Store)
	}{
		{"job_policy_context_queued", seedS003JobQueued},
		{"job_policy_context_running", seedS003JobRunning},
		{"job_policy_context_succeeded", seedS003JobSucceeded},
		{"agent_projection_queued", seedS003JobQueued},
		{"agent_projection_running", seedS003JobRunning},
		{"agent_projection_canceled", seedS003JobCanceled},
		{"agent_projection_succeeded", seedS003JobSucceeded},
		{"agent_projection_provider_timeout", seedS003JobProviderTimeout},
		{"agent_projection_provider_failure", seedS003JobProviderFailure},
		{"agent_projection_lease_expired", seedS003JobLeaseExpired},
		{"job_not_found", nil},
	}
	for _, test := range tests {
		t.Run(test.fixtureID, func(t *testing.T) {
			store := NewStore()
			if test.seed != nil {
				test.seed(store)
			}
			if _, err := testkit.ReplayHTTPFixture(NewHandler(store), pkg, test.fixtureID); err != nil {
				t.Fatalf("replay %s: %v", test.fixtureID, err)
			}
		})
	}
}

func TestHandlerReplaysS003JobLifecycleFixtures(t *testing.T) {
	scenario, err := testkit.LoadScenario(filepath.Join("..", "..", "..", "testdata", "contract-sim"), filepath.Join("fixtures", "S003", "manifest.json"))
	if err != nil {
		t.Fatalf("load scenario: %v", err)
	}
	pkg, ok := testkit.FindPackage(scenario, "c05-async-job-service")
	if !ok {
		t.Fatalf("c05 fixture package not found")
	}

	tests := []struct {
		fixtureID string
		now       string
		seed      func(*Store)
	}{
		{"job_claim_ok", "2026-06-05T20:00:01Z", seedS003JobQueued},
		{"job_heartbeat_running", "2026-06-05T20:00:03Z", seedS003JobClaimedWithCursor("cursor_s003_logs_0001")},
		{"job_heartbeat_waiting_for_gpu_lease", "2026-06-05T20:00:02Z", seedS003JobClaimed},
		{"job_heartbeat_claim_renewed", "2026-06-05T20:00:32Z", seedS003JobRunningWithCursor("cursor_s003_logs_0002")},
		{"job_fail_provider_unavailable", "2026-06-05T20:00:08Z", seedS003JobRunningWithCursor("cursor_s003_logs_provider_failure")},
		{"job_fail_provider_timeout", "2026-06-05T20:15:08Z", seedS003JobRunningWithCursorAndClaim("cursor_s003_logs_provider_timeout", "2026-06-05T20:16:08Z")},
		{"job_complete_ok", "2026-06-05T20:00:46Z", seedS003JobRunning},
		{"job_complete_worker_mismatch", "2026-06-05T20:00:46Z", seedS003JobRunning},
		{"job_complete_expired_claim", "2026-06-05T20:01:02Z", seedS003JobRunningExpiredClaim},
		{"job_cancel_queued", "2026-06-05T20:00:01Z", seedS003JobQueued},
		{"job_cancel_queued_replay", "2026-06-05T20:00:02Z", seedS003JobCanceledWithCancelIdempotency},
		{"job_cancel_idempotency_conflict", "2026-06-05T20:00:02Z", seedS003JobCanceledWithCancelIdempotency},
		{"job_fail_lease_expired", "2026-06-05T20:01:04Z", seedS003JobRunningWithCursorAndClaim("cursor_s003_logs_lease_expired", "2026-06-05T20:02:04Z")},
		{"job_invalid_transition", "2026-06-05T20:00:02Z", seedS003JobClaimed},
		{"job_append_log_worker_mismatch", "2026-06-05T20:00:02Z", seedS003JobClaimed},
		{"job_append_log_expired_claim", "2026-06-05T20:01:02Z", seedS003JobClaimedExpiredClaim},
		{"job_claim_same_worker_replay", "2026-06-05T20:00:02Z", seedS003JobClaimed},
		{"job_claim_worker_conflict", "2026-06-05T20:00:02Z", seedS003JobClaimed},
		{"job_claim_expired_reclaim", "2026-06-05T20:02:02Z", seedS003JobClaimedExpiredClaim},
		{"job_claim_terminal_conflict", "2026-06-05T20:00:47Z", seedS003JobSucceeded},
		{"job_cancel_running_conflict", "2026-06-05T20:00:04Z", seedS003JobRunning},
		{"job_complete_terminal_conflict", "2026-06-05T20:00:47Z", seedS003JobSucceeded},
		{"job_fail_worker_mismatch", "2026-06-05T20:00:08Z", seedS003JobRunning},
		{"job_fail_expired_claim", "2026-06-05T20:01:02Z", seedS003JobRunningExpiredClaim},
		{"job_fail_terminal_conflict", "2026-06-05T20:15:09Z", seedS003JobProviderTimeout},
	}
	for _, test := range tests {
		t.Run(test.fixtureID, func(t *testing.T) {
			store := NewStore()
			store.SetClock(fixedS003JobTime(test.now))
			if test.seed != nil {
				test.seed(store)
			}
			if _, err := testkit.ReplayHTTPFixture(NewHandler(store), pkg, test.fixtureID); err != nil {
				t.Fatalf("replay %s: %v", test.fixtureID, err)
			}
		})
	}
}

func requestJSON(t *testing.T, handler http.Handler, method, path string, body any, headers ...map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, group := range headers {
		for name, value := range group {
			req.Header.Set(name, value)
		}
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func responseData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("data missing from envelope: %+v", envelope)
	}
	return data
}

func responseErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	errObj, ok := envelope["error"].(map[string]any)
	if !ok {
		t.Fatalf("error missing from envelope: %+v", envelope)
	}
	code, _ := errObj["code"].(string)
	return code
}

func assertMetric(t *testing.T, data map[string]any, name string, labels map[string]string, value float64) {
	t.Helper()
	for _, rawSample := range data["samples"].([]any) {
		sample := rawSample.(map[string]any)
		if sample["name"] != name {
			continue
		}
		if !labelsMatch(sample["labels"], labels) {
			continue
		}
		if sample["value"] != value {
			t.Fatalf("metric %s value=%#v want=%v", name, sample["value"], value)
		}
		return
	}
	t.Fatalf("metric %s labels=%#v not found in %#v", name, labels, data["samples"])
}

func labelsMatch(raw any, want map[string]string) bool {
	if len(want) == 0 {
		return raw == nil
	}
	labels, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	for key, value := range want {
		if labels[key] != value {
			return false
		}
	}
	return true
}

const s003JobID = "job_s003_0001"

func seedS003JobQueued(store *Store) {
	seedS003Job(store, contracts.Job{
		JobID:         s003JobID,
		State:         contracts.JobQueued,
		CreatedAt:     "2026-06-05T20:00:00Z",
		UpdatedAt:     "2026-06-05T20:00:00Z",
		InputSummary:  s003InputSummary(),
		ArtifactRefs:  []string{},
		LogCursor:     nil,
		TerminalError: nil,
		Links:         map[string]any{},
	})
}

func seedS003JobClaimed(store *Store) {
	seedS003Job(store, s003ClaimedJob(nil))
}

func seedS003JobClaimedWithCursor(cursor string) func(*Store) {
	return func(store *Store) {
		seedS003Job(store, s003ClaimedJob(&cursor))
	}
}

func seedS003JobClaimedExpiredClaim(store *Store) {
	job := s003ClaimedJob(nil)
	job.Claim.ExpiresAt = "2026-06-05T20:01:01Z"
	seedS003Job(store, job)
}

func seedS003JobRunning(store *Store) {
	cursor := "cursor_s003_logs_0001"
	seedS003Job(store, contracts.Job{
		JobID:         s003JobID,
		State:         contracts.JobRunning,
		CreatedAt:     "2026-06-05T20:00:00Z",
		UpdatedAt:     "2026-06-05T20:00:03Z",
		StatusMessage: "running",
		InputSummary:  s003InputSummary(),
		Metadata:      s003ExecutionMetadata(),
		Claim:         s003ActiveClaim("2026-06-05T20:01:03Z"),
		ArtifactRefs:  []string{},
		LogCursor:     &cursor,
		TerminalError: nil,
		Links:         map[string]any{},
	})
}

func seedS003JobRunningWithCursor(cursor string) func(*Store) {
	return seedS003JobRunningWithCursorAndClaim(cursor, "2026-06-05T20:01:03Z")
}

func seedS003JobRunningWithCursorAndClaim(cursor, expiresAt string) func(*Store) {
	return func(store *Store) {
		seedS003JobRunning(store)
		store.mu.Lock()
		defer store.mu.Unlock()
		store.jobs[s003JobID].job.LogCursor = &cursor
		store.jobs[s003JobID].job.Claim.ExpiresAt = expiresAt
	}
}

func seedS003JobRunningExpiredClaim(store *Store) {
	seedS003JobRunning(store)
	store.mu.Lock()
	defer store.mu.Unlock()
	store.jobs[s003JobID].job.Claim.ExpiresAt = "2026-06-05T20:01:01Z"
}

func seedS003JobCanceled(store *Store) {
	seedS003Job(store, contracts.Job{
		JobID:         s003JobID,
		State:         contracts.JobCanceled,
		CreatedAt:     "2026-06-05T20:00:00Z",
		UpdatedAt:     "2026-06-05T20:00:01Z",
		StatusMessage: "canceled by requester",
		InputSummary:  s003InputSummary(),
		Metadata:      s003ExecutionMetadata(),
		ArtifactRefs:  []string{},
		LogCursor:     nil,
		TerminalError: &contracts.ErrorObject{Code: "canceled", Message: "canceled by requester", Retryable: false},
		Links:         map[string]any{},
	})
}

func seedS003JobCanceledWithCancelIdempotency(store *Store) {
	seedS003JobCanceled(store)
	fingerprint, err := fingerprint(struct {
		JobID   string                  `json:"job_id"`
		Request contracts.CancelRequest `json:"request"`
	}{JobID: s003JobID, Request: contracts.CancelRequest{RequesterID: "sub_agent_s003", Reason: "canceled by requester"}})
	if err != nil {
		panic(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.cancelIdempotency["idem_s003_c05_cancel_queued"] = idempotentCancel{fingerprint: fingerprint, jobID: s003JobID}
}

func seedS003JobSucceeded(store *Store) {
	cursor := "cursor_s003_logs_0002"
	seedS003Job(store, contracts.Job{
		JobID:         s003JobID,
		State:         contracts.JobSucceeded,
		CreatedAt:     "2026-06-05T20:00:00Z",
		UpdatedAt:     "2026-06-05T20:00:46Z",
		StatusMessage: "completed",
		InputSummary:  s003InputSummary(),
		Metadata:      s003ExecutionMetadata(),
		ArtifactRefs:  []string{"art_s003_0001"},
		LogCursor:     &cursor,
		TerminalError: nil,
		Links:         map[string]any{},
	})
}

func seedS003JobProviderTimeout(store *Store) {
	cursor := "cursor_s003_logs_provider_timeout"
	seedS003Job(store, contracts.Job{
		JobID:         s003JobID,
		State:         contracts.JobFailed,
		CreatedAt:     "2026-06-05T20:00:00Z",
		UpdatedAt:     "2026-06-05T20:15:08Z",
		StatusMessage: "provider invocation timed out",
		InputSummary:  s003InputSummary(),
		Metadata:      s003ExecutionMetadata(),
		ArtifactRefs:  []string{},
		LogCursor:     &cursor,
		TerminalError: &contracts.ErrorObject{Code: "provider_timeout", Message: "provider invocation timed out", Retryable: true},
		Links:         map[string]any{},
	})
}

func seedS003JobProviderFailure(store *Store) {
	cursor := "cursor_s003_logs_provider_failure"
	seedS003Job(store, contracts.Job{
		JobID:         s003JobID,
		State:         contracts.JobFailed,
		CreatedAt:     "2026-06-05T20:00:00Z",
		UpdatedAt:     "2026-06-05T20:00:08Z",
		StatusMessage: "ComfyUI backend is unavailable",
		InputSummary:  s003InputSummary(),
		Metadata:      s003ExecutionMetadata(),
		ArtifactRefs:  []string{},
		LogCursor:     &cursor,
		TerminalError: &contracts.ErrorObject{Code: "provider_unavailable", Message: "ComfyUI backend is unavailable", Retryable: true},
		Links:         map[string]any{},
	})
}

func seedS003JobLeaseExpired(store *Store) {
	cursor := "cursor_s003_logs_lease_expired"
	seedS003Job(store, contracts.Job{
		JobID:         s003JobID,
		State:         contracts.JobFailed,
		CreatedAt:     "2026-06-05T20:00:00Z",
		UpdatedAt:     "2026-06-05T20:01:04Z",
		StatusMessage: "resource lease expired before completion",
		InputSummary:  s003InputSummary(),
		Metadata:      s003ExecutionMetadata(),
		ArtifactRefs:  []string{},
		LogCursor:     &cursor,
		TerminalError: &contracts.ErrorObject{Code: "lease_expired", Message: "resource lease expired before completion", Retryable: true},
		Links:         map[string]any{},
	})
}

func seedS003Job(store *Store, job contracts.Job) {
	if job.Metadata == nil {
		job.Metadata = s003ExecutionMetadata()
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.jobs[s003JobID] = &record{
		job:            job,
		requesterID:    "sub_agent_s003",
		ownerSubjectID: "sub_agent_s003",
		claimLease:     time.Minute,
	}
}

func s003ClaimedJob(cursor *string) contracts.Job {
	return contracts.Job{
		JobID:         s003JobID,
		State:         contracts.JobClaimed,
		CreatedAt:     "2026-06-05T20:00:00Z",
		UpdatedAt:     "2026-06-05T20:00:01Z",
		InputSummary:  s003InputSummary(),
		Metadata:      s003ExecutionMetadata(),
		Claim:         s003ActiveClaim("2026-06-05T20:01:01Z"),
		ArtifactRefs:  []string{},
		LogCursor:     cursor,
		TerminalError: nil,
		Links:         map[string]any{},
	}
}

func s003ActiveClaim(expiresAt string) *contracts.JobClaim {
	return &contracts.JobClaim{
		WorkerID:  "runner_s003_0001",
		ClaimedAt: "2026-06-05T20:00:01Z",
		ExpiresAt: expiresAt,
	}
}

func s003InputSummary() map[string]any {
	return map[string]any{
		"prompt_present": true,
		"width":          1024,
		"height":         1024,
	}
}

func s003ExecutionMetadata() map[string]any {
	return map[string]any{
		"execution_plan": map[string]any{
			"capability_id": "cap_image_generate_gpu",
			"subject_id":    "sub_agent_s003",
			"input": map[string]any{
				"prompt": "a clean product photo of a red ceramic mug",
				"width":  1024,
				"height": 1024,
			},
			"route": map[string]any{
				"capability_id":        "cap_image_generate_gpu",
				"service_id":           "svc_comfyui_gpu",
				"provider_endpoint":    "http://node_linux_gpu:8188",
				"provider_health_path": "/v1/provider/health",
				"provider_invoke_path": "/v1/provider/capabilities/cap_image_generate_gpu/invoke",
				"node_id":              "node_linux_gpu",
				"node_managed":         true,
				"service_start_mode":   "on_demand",
				"resource_hints": []any{
					map[string]any{"selector": "gpu", "required": true, "quantity": 1},
				},
				"artifact_hints": []any{
					map[string]any{"media_type": "image/png", "count": "one"},
				},
			},
			"resource_selector": "gpu",
			"timeout_seconds":   900,
			"artifact_hints": []any{
				map[string]any{"media_type": "image/png", "count": "one"},
			},
			"provider_context": map[string]any{},
		},
	}
}

func fixedS003JobTime(value string) func() time.Time {
	return func() time.Time {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			panic(err)
		}
		return parsed
	}
}
