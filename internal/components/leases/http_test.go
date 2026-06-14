package leases

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

func TestHandlerLeaseLifecycle(t *testing.T) {
	handler := NewHandler(NewStore())

	resource := doJSON(t, handler, http.MethodPost, "/v1/resources", map[string]any{
		"resource_id":  "res_gpu_0",
		"selector":     "gpu",
		"display_name": "Linux GPU",
		"status":       "available",
	}, nil)
	if resource["resource_id"] != "res_gpu_0" {
		t.Fatalf("resource response = %#v", resource)
	}

	first := doJSON(t, handler, http.MethodPost, "/v1/lease-requests", map[string]any{
		"requester_id":              "job_1",
		"resource_selector":         "gpu",
		"heartbeat_timeout_seconds": 60,
	}, nil)
	if first["state"] != "granted" {
		t.Fatalf("first lease request = %#v", first)
	}
	lease := first["lease"].(map[string]any)
	leaseID := lease["lease_id"].(string)

	second := doJSON(t, handler, http.MethodPost, "/v1/lease-requests", map[string]any{
		"requester_id":      "job_2",
		"resource_selector": "gpu",
	}, nil)
	if second["state"] != "pending" || second["queue_position"].(float64) != 1 {
		t.Fatalf("second lease request = %#v", second)
	}

	heartbeat := doJSON(t, handler, http.MethodPost, "/v1/leases/"+leaseID+"/heartbeat", map[string]any{
		"holder_id": "job_1",
	}, nil)
	if heartbeat["holder_id"] != "job_1" {
		t.Fatalf("heartbeat = %#v", heartbeat)
	}

	missingID := doJSONStatus(t, handler, http.MethodPost, "/v1/leases/"+leaseID+"/release", map[string]any{
		"holder_id": "job_1",
		"reason":    "job completed",
	}, nil, http.StatusBadRequest)
	if missingID["error"].(map[string]any)["code"] != "missing_idempotency_key" {
		t.Fatalf("missing idempotency error = %#v", missingID)
	}

	released := doJSON(t, handler, http.MethodPost, "/v1/leases/"+leaseID+"/release", map[string]any{
		"holder_id": "job_1",
		"reason":    "job completed",
	}, map[string]string{
		"Idempotency-Key":    "release-http-1",
		"X-Actor-Subject-ID": "sub_runner",
	})
	if released["released_by"] != "sub_runner" || released["release_reason"] != "job completed" {
		t.Fatalf("released = %#v", released)
	}

	secondStatus := doJSON(t, handler, http.MethodGet, "/v1/lease-requests/"+second["request_id"].(string), nil, nil)
	if secondStatus["state"] != "granted" {
		t.Fatalf("second was not granted after release = %#v", secondStatus)
	}

	inspection := doJSON(t, handler, http.MethodGet, "/v1/resources/res_gpu_0/inspection", nil, nil)
	if inspection["active_lease"] == nil {
		t.Fatalf("inspection missing active lease = %#v", inspection)
	}
}

func TestHandlerListsLeaseRequestsByRequester(t *testing.T) {
	handler := NewHandler(NewStore())

	_ = doJSON(t, handler, http.MethodPost, "/v1/resources", map[string]any{
		"resource_id": "res_gpu_0",
		"selector":    "gpu",
		"status":      "available",
	}, nil)
	_ = doJSON(t, handler, http.MethodPost, "/v1/lease-requests", map[string]any{
		"requester_id":      "job_holder",
		"resource_selector": "gpu",
	}, nil)
	queued := doJSON(t, handler, http.MethodPost, "/v1/lease-requests", map[string]any{
		"requester_id":      "job_waiting",
		"resource_selector": "gpu",
	}, nil)
	if queued["state"] != "pending" || queued["queue_position"].(float64) != 1 {
		t.Fatalf("queued request = %#v", queued)
	}

	data := doJSON(t, handler, http.MethodGet, "/v1/lease-requests?requester_id=job_waiting", nil, nil)
	items := data["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("lease request list = %#v", data)
	}
	item := items[0].(map[string]any)
	if item["requester_id"] != "job_waiting" || item["state"] != "pending" || item["queue_position"].(float64) != 1 {
		t.Fatalf("lease request item = %#v", item)
	}

	missingFilter := doJSONStatus(t, handler, http.MethodGet, "/v1/lease-requests", nil, nil, http.StatusBadRequest)
	if missingFilter["error"].(map[string]any)["code"] != "validation_failed" {
		t.Fatalf("missing requester_id error = %#v", missingFilter)
	}
}

func TestHandlerListResourcesPaginates(t *testing.T) {
	handler := NewHandler(NewStore())
	for _, resourceID := range []string{"res_gpu_0", "res_gpu_1"} {
		_ = doJSON(t, handler, http.MethodPost, "/v1/resources", map[string]any{
			"resource_id": resourceID,
			"selector":    "gpu",
			"status":      "available",
		}, nil)
	}

	first := doJSON(t, handler, http.MethodGet, "/v1/resources?selector=gpu&limit=1", nil, nil)
	items := first["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["resource_id"] != "res_gpu_0" {
		t.Fatalf("first resources page = %#v", first)
	}
	cursor, ok := first["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("first resources page missing cursor = %#v", first)
	}

	second := doJSON(t, handler, http.MethodGet, "/v1/resources?selector=gpu&limit=1&cursor="+cursor, nil, nil)
	items = second["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["resource_id"] != "res_gpu_1" || second["next_cursor"] != nil {
		t.Fatalf("second resources page = %#v", second)
	}

	invalidLimit := doJSONStatus(t, handler, http.MethodGet, "/v1/resources?limit=0", nil, nil, http.StatusBadRequest)
	if invalidLimit["error"].(map[string]any)["code"] != "validation_failed" {
		t.Fatalf("invalid resource limit error = %#v", invalidLimit)
	}
	invalidCursor := doJSONStatus(t, handler, http.MethodGet, "/v1/resources?cursor=cursor_lease_requests_000001", nil, nil, http.StatusBadRequest)
	if invalidCursor["error"].(map[string]any)["code"] != "invalid_cursor" {
		t.Fatalf("invalid resource cursor error = %#v", invalidCursor)
	}
}

func TestHandlerListLeaseRequestsPaginates(t *testing.T) {
	handler := NewHandler(NewStore())

	_ = doJSON(t, handler, http.MethodPost, "/v1/resources", map[string]any{
		"resource_id": "res_gpu_0",
		"selector":    "gpu",
		"status":      "available",
	}, nil)
	firstRequest := doJSON(t, handler, http.MethodPost, "/v1/lease-requests", map[string]any{
		"requester_id":      "job_page",
		"resource_selector": "gpu",
	}, nil)
	secondRequest := doJSON(t, handler, http.MethodPost, "/v1/lease-requests", map[string]any{
		"requester_id":      "job_page",
		"resource_selector": "gpu",
	}, nil)

	first := doJSON(t, handler, http.MethodGet, "/v1/lease-requests?requester_id=job_page&limit=1", nil, nil)
	items := first["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["request_id"] != firstRequest["request_id"] {
		t.Fatalf("first lease request page = %#v", first)
	}
	cursor, ok := first["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("first lease request page missing cursor = %#v", first)
	}

	second := doJSON(t, handler, http.MethodGet, "/v1/lease-requests?requester_id=job_page&limit=1&cursor="+cursor, nil, nil)
	items = second["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["request_id"] != secondRequest["request_id"] || second["next_cursor"] != nil {
		t.Fatalf("second lease request page = %#v", second)
	}

	invalidLimit := doJSONStatus(t, handler, http.MethodGet, "/v1/lease-requests?requester_id=job_page&limit=0", nil, nil, http.StatusBadRequest)
	if invalidLimit["error"].(map[string]any)["code"] != "validation_failed" {
		t.Fatalf("invalid lease request limit error = %#v", invalidLimit)
	}
	invalidCursor := doJSONStatus(t, handler, http.MethodGet, "/v1/lease-requests?requester_id=job_page&cursor=cursor_leases_resources_000001", nil, nil, http.StatusBadRequest)
	if invalidCursor["error"].(map[string]any)["code"] != "invalid_cursor" {
		t.Fatalf("invalid lease request cursor error = %#v", invalidCursor)
	}
}

func TestHandlerErrorsUseStableEnvelopes(t *testing.T) {
	handler := NewHandler(NewStore())
	data := doJSONStatus(t, handler, http.MethodPost, "/v1/lease-requests", map[string]any{
		"requester_id":      "job_1",
		"resource_selector": "gpu",
	}, nil, http.StatusConflict)
	if data["ok"].(bool) {
		t.Fatalf("expected error envelope = %#v", data)
	}
	errObj := data["error"].(map[string]any)
	if errObj["code"] != "resource_unavailable" || errObj["retryable"] != true {
		t.Fatalf("error object = %#v", errObj)
	}
}

func TestHandlerHealth(t *testing.T) {
	handler := NewHandler(NewStore())
	data := doJSON(t, handler, http.MethodGet, "/v1/leases/health", nil, nil)
	details := data["details"].(map[string]any)
	if data["status"] != "healthy" || details["component"] != "leases" {
		t.Fatalf("health = %#v", data)
	}
	if details["store_backend"] != "memory" || details["resource_count"] != float64(0) || details["queue_depth"] != float64(0) {
		t.Fatalf("health = %#v", data)
	}
}

func TestHandlerMetricsReportsQueueDepth(t *testing.T) {
	handler := NewHandler(NewStore())
	_ = doJSON(t, handler, http.MethodPost, "/v1/resources", map[string]any{
		"resource_id": "res_gpu_0",
		"selector":    "gpu",
		"status":      "available",
	}, nil)
	_ = doJSON(t, handler, http.MethodPost, "/v1/lease-requests", map[string]any{
		"requester_id":      "job_1",
		"resource_selector": "gpu",
	}, nil)
	_ = doJSON(t, handler, http.MethodPost, "/v1/lease-requests", map[string]any{
		"requester_id":      "job_2",
		"resource_selector": "gpu",
	}, nil)

	data := doJSON(t, handler, http.MethodGet, "/v1/leases/metrics", nil, nil)
	if data["component"] != "leases" {
		t.Fatalf("metrics = %#v", data)
	}
	assertMetric(t, data, "lease_queue_depth", map[string]string{"selector": "gpu"}, 1)
	assertMetric(t, data, "http_requests_total", map[string]string{"method": "POST", "route_group": "/v1/lease-requests", "status_class": "2xx"}, 2)
}

func TestHandlerReplaysS003LeaseFixtures(t *testing.T) {
	scenario, err := testkit.LoadScenario(filepath.Join("..", "..", "..", "testdata", "contract-sim"), filepath.Join("fixtures", "S003", "manifest.json"))
	if err != nil {
		t.Fatalf("load scenario: %v", err)
	}
	pkg, ok := testkit.FindPackage(scenario, "c06-resource-lease-service")
	if !ok {
		t.Fatalf("c06 fixture package not found")
	}

	tests := []struct {
		fixtureID string
		now       string
		seed      func(*testing.T, *Store)
		audit     *s003LeaseAuditExpectation
	}{
		{"lease_heartbeat_ok", "2026-06-05T20:00:32Z", seedS003ActiveLease("2026-06-05T20:01:02Z"), nil},
		{"lease_release_ok", "2026-06-05T20:00:46Z", seedS003ActiveLease("2026-06-05T20:01:02Z"), &s003LeaseAuditExpectation{Reason: "job completed", OccurredAt: "2026-06-05T20:00:46Z", IdempotencyKey: "idem_s003_lease_release"}},
		{"lease_release_replay", "2026-06-05T20:00:47Z", seedS003ReleaseReplay("job completed", "2026-06-05T20:00:46Z", "idem_s003_lease_release"), &s003LeaseAuditExpectation{Reason: "job completed", OccurredAt: "2026-06-05T20:00:46Z", IdempotencyKey: "idem_s003_lease_release"}},
		{"lease_release_provider_failure", "2026-06-05T20:00:08Z", seedS003ActiveLease("2026-06-05T20:01:02Z"), &s003LeaseAuditExpectation{Reason: "provider failed", OccurredAt: "2026-06-05T20:00:08Z", IdempotencyKey: "idem_s003_lease_release_provider_failure"}},
		{"lease_pending_cancel", "2026-06-05T20:00:10Z", seedS003PendingRequest, nil},
		{"lease_pending_cancel_replay", "2026-06-05T20:00:11Z", seedS003CanceledRequest, nil},
		{"lease_holder_mismatch", "2026-06-05T20:00:05Z", seedS003ActiveLease("2026-06-05T20:01:02Z"), nil},
		{"lease_heartbeat_expired", "2026-06-05T20:01:03Z", seedS003ActiveLease("2026-06-05T20:01:02Z"), nil},
		{"lease_release_expired", "2026-06-05T20:01:03Z", seedS003ActiveLease("2026-06-05T20:01:02Z"), nil},
		{"lease_request_status_pending", "2026-06-05T20:00:04Z", seedS003PendingRequest, nil},
		{"lease_release_idempotency_conflict", "2026-06-05T20:00:47Z", seedS003ReleaseReplay("job completed", "2026-06-05T20:00:46Z", "idem_s003_lease_release"), nil},
		{"lease_unknown_selector", "2026-06-05T20:00:04Z", nil, nil},
		{"lease_resource_unavailable", "2026-06-05T20:00:04Z", seedS003UnavailableResource, nil},
		{"lease_release_provider_timeout", "2026-06-05T20:15:08Z", seedS003ActiveLease("2026-06-05T20:15:37Z"), &s003LeaseAuditExpectation{Reason: "provider timed out", OccurredAt: "2026-06-05T20:15:08Z", IdempotencyKey: "idem_s003_lease_release_provider_timeout"}},
		{"lease_release_replay_audit_dedupe", "2026-06-05T20:15:09Z", seedS003ReleaseReplay("provider timed out", "2026-06-05T20:15:08Z", "idem_s003_lease_release_provider_timeout"), &s003LeaseAuditExpectation{Reason: "provider timed out", OccurredAt: "2026-06-05T20:15:08Z", IdempotencyKey: "idem_s003_lease_release_provider_timeout"}},
	}

	for _, test := range tests {
		t.Run(test.fixtureID, func(t *testing.T) {
			store := NewStore()
			store.SetClock(fixedS003LeaseTime(test.now))
			if test.seed != nil {
				test.seed(t, store)
			}
			if _, err := testkit.ReplayHTTPFixture(s003LeaseFixtureHandler(store), pkg, test.fixtureID); err != nil {
				t.Fatalf("replay %s: %v", test.fixtureID, err)
			}
			if test.audit != nil {
				assertS003LeaseAudit(t, store, *test.audit)
			}
		})
	}
}

func doJSON(t *testing.T, handler http.Handler, method, path string, body any, headers map[string]string) map[string]any {
	t.Helper()
	envelope := doJSONStatus(t, handler, method, path, body, headers, successStatus(method, path))
	if !envelope["ok"].(bool) {
		t.Fatalf("error response for %s %s: %#v", method, path, envelope)
	}
	return envelope["data"].(map[string]any)
}

func doJSONStatus(t *testing.T, handler http.Handler, method, path string, body any, headers map[string]string, wantStatus int) map[string]any {
	t.Helper()
	var raw bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&raw).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &raw)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("%s %s status = %d, want %d, body=%s", method, path, rec.Code, wantStatus, rec.Body.String())
	}
	var envelope map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return envelope
}

func successStatus(method, path string) int {
	if method == http.MethodPost && (path == "/v1/resources" || path == "/v1/lease-requests") {
		return http.StatusCreated
	}
	return http.StatusOK
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

const (
	s003LeaseID        = "lease_s003_0001"
	s003LeaseRequestID = "lease_req_s003_0001"
	s003PendingID      = "lease_req_s003_0002"
	s003ResourceID     = "res_gpu_0"
	s003LeaseHolder    = "job_s003_0001"
	s003RunnerSubject  = "sub_runner_s003"
)

type s003LeaseAuditExpectation struct {
	Reason         string
	OccurredAt     string
	IdempotencyKey string
}

func s003LeaseFixtureHandler(store *Store) http.Handler {
	next := NewHandler(store)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer token_s003_runner" && r.Header.Get("X-Actor-Subject-ID") == "" {
			r.Header.Set("X-Actor-Subject-ID", s003RunnerSubject)
		}
		next.ServeHTTP(w, r)
	})
}

func seedS003ActiveLease(expiresAt string) func(*testing.T, *Store) {
	return func(t *testing.T, store *Store) {
		t.Helper()
		store.mu.Lock()
		defer store.mu.Unlock()
		store.resources[s003ResourceID] = &resourceRecord{resource: s003Resource(contracts.ResourceAvailable)}
		store.requests[s003LeaseRequestID] = &leaseRequestRecord{
			request:          s003GrantedLeaseRequest(expiresAt),
			requesterID:      s003LeaseHolder,
			priority:         0,
			sequence:         1,
			heartbeatTimeout: time.Minute,
			leaseID:          s003LeaseID,
		}
		store.leases[s003LeaseID] = &leaseRecord{
			lease:     s003ActiveLease(expiresAt),
			requestID: s003LeaseRequestID,
			timeout:   time.Minute,
			state:     leaseActive,
		}
	}
}

func seedS003PendingRequest(t *testing.T, store *Store) {
	t.Helper()
	position := 1
	store.mu.Lock()
	defer store.mu.Unlock()
	store.resources[s003ResourceID] = &resourceRecord{resource: s003Resource(contracts.ResourceAvailable)}
	store.requests[s003PendingID] = &leaseRequestRecord{
		request: contracts.LeaseRequest{
			RequestID:        s003PendingID,
			State:            contracts.LeaseRequestPending,
			RequesterID:      "job_s003_0002",
			ResourceSelector: "gpu",
			QueuePosition:    &position,
			Lease:            nil,
			CreatedAt:        "2026-06-05T20:00:03Z",
			UpdatedAt:        "2026-06-05T20:00:03Z",
			Links:            pendingRequestLinks(s003PendingID),
		},
		requesterID:      "job_s003_0002",
		priority:         0,
		sequence:         2,
		heartbeatTimeout: time.Minute,
	}
	store.queues["gpu"] = []string{s003PendingID}
}

func seedS003CanceledRequest(t *testing.T, store *Store) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	store.requests[s003PendingID] = &leaseRequestRecord{
		request: contracts.LeaseRequest{
			RequestID:        s003PendingID,
			State:            contracts.LeaseRequestCanceled,
			RequesterID:      "job_s003_0002",
			ResourceSelector: "gpu",
			QueuePosition:    nil,
			Lease:            nil,
			CreatedAt:        "2026-06-05T20:00:03Z",
			UpdatedAt:        "2026-06-05T20:00:10Z",
			Links:            map[string]any{},
		},
		requesterID:      "job_s003_0002",
		priority:         0,
		sequence:         2,
		heartbeatTimeout: time.Minute,
	}
}

func seedS003UnavailableResource(t *testing.T, store *Store) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	store.resources[s003ResourceID] = &resourceRecord{resource: s003Resource(contracts.ResourceUnavailable)}
}

func seedS003ReleaseReplay(reason, occurredAt, idempotencyKey string) func(*testing.T, *Store) {
	return func(t *testing.T, store *Store) {
		t.Helper()
		fp := mustS003LeaseFingerprint(t, contracts.LeaseReleaseRequest{HolderID: s003LeaseHolder, Reason: reason})
		released := s003ReleasedLease(occurredAt, reason)
		store.mu.Lock()
		defer store.mu.Unlock()
		store.releaseIdempotency[idempotencyKey] = idempotentRelease{
			fingerprint: fp,
			leaseID:     s003LeaseID,
			response:    released,
		}
		store.audit = append(store.audit, contracts.LeaseAuditEvent{
			EventType:      "lease.released",
			LeaseID:        s003LeaseID,
			ResourceID:     s003ResourceID,
			HolderID:       s003LeaseHolder,
			ActorSubjectID: s003RunnerSubject,
			ReleaseReason:  reason,
			OccurredAt:     occurredAt,
			IdempotencyKey: idempotencyKey,
		})
	}
}

func s003Resource(status contracts.ResourceStatus) contracts.ResourceRecord {
	return contracts.ResourceRecord{
		ResourceID:  s003ResourceID,
		Selector:    "gpu",
		DisplayName: "Linux GPU",
		Status:      status,
		Links:       resourceLinks(s003ResourceID),
	}
}

func s003GrantedLeaseRequest(expiresAt string) contracts.LeaseRequest {
	return contracts.LeaseRequest{
		RequestID:        s003LeaseRequestID,
		State:            contracts.LeaseRequestGranted,
		RequesterID:      s003LeaseHolder,
		ResourceSelector: "gpu",
		QueuePosition:    nil,
		Lease:            leasePtr(s003ActiveLease(expiresAt)),
		CreatedAt:        "2026-06-05T20:00:02Z",
		UpdatedAt:        "2026-06-05T20:00:02Z",
		Links:            grantedRequestLinks(s003LeaseRequestID),
	}
}

func s003ActiveLease(expiresAt string) contracts.Lease {
	return contracts.Lease{
		LeaseID:    s003LeaseID,
		ResourceID: s003ResourceID,
		HolderID:   s003LeaseHolder,
		ExpiresAt:  expiresAt,
		Links:      leaseLinks(s003LeaseID),
	}
}

func s003ReleasedLease(occurredAt, reason string) contracts.Lease {
	lease := s003ActiveLease(occurredAt)
	lease.ReleasedAt = occurredAt
	lease.ReleasedBy = s003RunnerSubject
	lease.ReleaseReason = reason
	lease.Links = map[string]any{}
	return lease
}

func assertS003LeaseAudit(t *testing.T, store *Store, want s003LeaseAuditExpectation) {
	t.Helper()
	events := store.AuditEvents()
	if len(events) != 1 {
		t.Fatalf("audit events = %#v", events)
	}
	event := events[0]
	if event.EventType != "lease.released" ||
		event.LeaseID != s003LeaseID ||
		event.ResourceID != s003ResourceID ||
		event.HolderID != s003LeaseHolder ||
		event.ActorSubjectID != s003RunnerSubject ||
		event.ReleaseReason != want.Reason ||
		event.OccurredAt != want.OccurredAt ||
		event.IdempotencyKey != want.IdempotencyKey {
		t.Fatalf("audit event = %#v, want reason=%q occurred_at=%q idempotency=%q", event, want.Reason, want.OccurredAt, want.IdempotencyKey)
	}
}

func fixedS003LeaseTime(value string) func() time.Time {
	return func() time.Time {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			panic(err)
		}
		return parsed
	}
}

func mustS003LeaseFingerprint(t *testing.T, value any) string {
	t.Helper()
	fp, err := fingerprint(value)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	return fp
}
