package testkit

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"pacp/internal/contracts"
)

func TestFakeComponentHandlersPassComponentChecks(t *testing.T) {
	kinds := []string{"artifacts", "catalog", "gateway", "jobs", "leases", "node", "policy", "runner"}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			handler, err := NewFakeComponentHandler(FakeComponentConfig{
				Kind: kind,
				Now:  fixedFakeClock,
			})
			if err != nil {
				t.Fatalf("new fake component: %v", err)
			}
			server := httptest.NewServer(handler)
			defer server.Close()

			report := CheckComponent(context.Background(), server.Client(), ComponentCheckOptions{
				BaseURL:   server.URL,
				Kind:      kind,
				RequestID: "req_fake_component",
			})
			if !report.Passed() {
				t.Fatalf("report = %#v", report)
			}
		})
	}
}

func TestFakeComponentHandlerRequiresCredential(t *testing.T) {
	handler, err := NewFakeComponentHandler(FakeComponentConfig{
		Kind:       "jobs",
		Credential: "component-token",
		Now:        fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake component: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	denied := CheckComponent(context.Background(), server.Client(), ComponentCheckOptions{
		BaseURL:   server.URL,
		Kind:      "jobs",
		RequestID: "req_fake_component",
	})
	if denied.Passed() {
		t.Fatalf("unauthenticated check unexpectedly passed: %#v", denied)
	}

	allowed := CheckComponent(context.Background(), server.Client(), ComponentCheckOptions{
		BaseURL:    server.URL,
		Kind:       "jobs",
		Credential: "Bearer component-token",
		RequestID:  "req_fake_component",
	})
	if !allowed.Passed() {
		t.Fatalf("authenticated check failed: %#v", allowed)
	}
}

func TestFakeComponentHandlerSupportsDeniedBehavior(t *testing.T) {
	handler, err := NewFakeComponentHandler(FakeComponentConfig{
		Kind:     "jobs",
		Behavior: FakeComponentDenied,
		Now:      fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake component: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/v1/jobs/health")
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var envelope rawErrorEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if envelope.OK || envelope.Error.Code != "forbidden" {
		t.Fatalf("envelope = %#v", envelope)
	}

	report := CheckComponent(context.Background(), server.Client(), ComponentCheckOptions{
		BaseURL:   server.URL,
		Kind:      "jobs",
		RequestID: "req_fake_component_denied",
	})
	if report.Passed() {
		t.Fatalf("denied component check unexpectedly passed: %#v", report)
	}
}

func TestFakeComponentHandlerSupportsUnavailableBehavior(t *testing.T) {
	handler, err := NewFakeComponentHandler(FakeComponentConfig{
		Kind:     "leases",
		Behavior: FakeComponentUnavailable,
		Now:      fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake component: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/v1/leases/metrics")
	if err != nil {
		t.Fatalf("get metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var envelope rawErrorEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if envelope.OK || envelope.Error.Code != "component_unavailable" || !envelope.Error.Retryable {
		t.Fatalf("envelope = %#v", envelope)
	}
}

func TestFakeComponentHandlerSupportsCustomListItems(t *testing.T) {
	now := "2026-06-08T00:00:00Z"
	handler, err := NewFakeComponentHandler(FakeComponentConfig{
		Kind: "jobs",
		ListItems: []any{contracts.Job{
			JobID:        "job_done",
			State:        contracts.JobSucceeded,
			CreatedAt:    now,
			UpdatedAt:    now,
			ArtifactRefs: []string{"art_done"},
			Links:        map[string]any{},
		}},
		Now: fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake component: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	report := CheckComponent(context.Background(), server.Client(), ComponentCheckOptions{
		BaseURL:   server.URL,
		Kind:      "jobs",
		RequestID: "req_fake_component_custom",
	})
	if !report.Passed() {
		t.Fatalf("custom list check failed: %#v", report)
	}
}

func TestFakeComponentHandlerSupportsExplicitEmptyList(t *testing.T) {
	handler, err := NewFakeComponentHandler(FakeComponentConfig{
		Kind:      "artifacts",
		ListItems: []any{},
		Now:       fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake component: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	report := CheckComponent(context.Background(), server.Client(), ComponentCheckOptions{
		BaseURL:   server.URL,
		Kind:      "artifacts",
		RequestID: "req_fake_component_empty",
	})
	if !report.Passed() {
		t.Fatalf("empty list check failed: %#v", report)
	}
}

func TestFakeComponentHandlerRejectsUnknownBehavior(t *testing.T) {
	_, err := NewFakeComponentHandler(FakeComponentConfig{
		Kind:     "jobs",
		Behavior: FakeComponentBehavior("strange"),
	})
	if err == nil {
		t.Fatal("expected unknown behavior error")
	}
}

func TestFakeCatalogHandlerSupportsCapabilityOutcomes(t *testing.T) {
	handler, err := NewFakeCatalogHandler(FakeCatalogConfig{Now: fixedFakeClock})
	if err != nil {
		t.Fatalf("new fake catalog: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	listEnvelope := doFakeCatalogEnvelope(t, server, http.MethodGet, "/v1/catalog/capabilities?tag=valid", nil, http.StatusOK)
	var list struct {
		Items []contracts.CatalogCapabilityRecord `json:"items"`
	}
	decodeEnvelopeData(t, listEnvelope, &list)
	if len(list.Items) != 1 || list.Items[0].Capability.ID != "cap_fake_valid" {
		t.Fatalf("list = %#v", list.Items)
	}

	recordEnvelope := doFakeCatalogEnvelope(t, server, http.MethodGet, "/v1/catalog/capabilities/cap_fake_valid", nil, http.StatusOK)
	var record contracts.CatalogCapabilityRecord
	decodeEnvelopeData(t, recordEnvelope, &record)
	if record.Route.ProviderInvokePath == "" || record.Service.ID == "" {
		t.Fatalf("record = %#v", record)
	}

	routeEnvelope := doFakeCatalogEnvelope(t, server, http.MethodGet, "/v1/catalog/capabilities/cap_fake_valid/route", nil, http.StatusOK)
	var route contracts.CapabilityRoute
	decodeEnvelopeData(t, routeEnvelope, &route)
	if route.CapabilityID != "cap_fake_valid" {
		t.Fatalf("route = %#v", route)
	}

	tagsEnvelope := doFakeCatalogEnvelope(t, server, http.MethodGet, "/v1/catalog/tags", nil, http.StatusOK)
	var tags struct {
		Items []string `json:"items"`
	}
	decodeEnvelopeData(t, tagsEnvelope, &tags)
	if !containsString(tags.Items, "valid") {
		t.Fatalf("tags = %#v", tags.Items)
	}

	exportEnvelope := doFakeCatalogEnvelope(t, server, http.MethodGet, "/v1/catalog/export", nil, http.StatusOK)
	var export contracts.CatalogExport
	decodeEnvelopeData(t, exportEnvelope, &export)
	if len(export.Manifests) != 1 || len(export.Manifests[0].Capabilities) == 0 {
		t.Fatalf("export = %#v", export)
	}

	denied := doFakeCatalogEnvelope(t, server, http.MethodGet, "/v1/catalog/capabilities/cap_fake_denied", nil, http.StatusForbidden)
	if denied.OK || denied.Error.Code != "forbidden" {
		t.Fatalf("denied = %#v", denied)
	}
	unavailable := doFakeCatalogEnvelope(t, server, http.MethodGet, "/v1/catalog/capabilities/cap_fake_unavailable", nil, http.StatusServiceUnavailable)
	if unavailable.OK || unavailable.Error.Code != "provider_unavailable" || !unavailable.Error.Retryable {
		t.Fatalf("unavailable = %#v", unavailable)
	}
	missing := doFakeCatalogEnvelope(t, server, http.MethodGet, "/v1/catalog/capabilities/cap_missing", nil, http.StatusNotFound)
	if missing.OK || missing.Error.Code != "not_found" {
		t.Fatalf("missing = %#v", missing)
	}
}

func TestFakeCatalogHandlerRejectsInvalidManifestAndRegistersValidManifest(t *testing.T) {
	handler, err := NewFakeCatalogHandler(FakeCatalogConfig{Now: fixedFakeClock})
	if err != nil {
		t.Fatalf("new fake catalog: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	invalid := doFakeCatalogEnvelope(t, server, http.MethodPost, "/v1/catalog/manifests", contracts.ProviderManifest{}, http.StatusBadRequest)
	if invalid.OK || invalid.Error.Code != "validation_failed" {
		t.Fatalf("invalid = %#v", invalid)
	}

	manifest := fakeProviderManifest("http://provider.new")
	manifest.Service.ID = "svc_fake_new"
	manifest.Capabilities[0].ID = "cap_fake_new"
	created := doFakeCatalogEnvelope(t, server, http.MethodPost, "/v1/catalog/manifests", manifest, http.StatusCreated)
	var result struct {
		ServiceID     string   `json:"service_id"`
		CapabilityIDs []string `json:"capability_ids"`
	}
	decodeEnvelopeData(t, created, &result)
	if result.ServiceID != "svc_fake_new" || !containsString(result.CapabilityIDs, "cap_fake_new") {
		t.Fatalf("created = %#v", result)
	}

	registered := doFakeCatalogEnvelope(t, server, http.MethodGet, "/v1/catalog/capabilities/cap_fake_new", nil, http.StatusOK)
	var record contracts.CatalogCapabilityRecord
	decodeEnvelopeData(t, registered, &record)
	if record.Capability.ServiceID != "svc_fake_new" {
		t.Fatalf("registered = %#v", record)
	}
}

func TestFakeCatalogHandlerSupportsUnavailableBehavior(t *testing.T) {
	handler, err := NewFakeCatalogHandler(FakeCatalogConfig{
		Behavior: FakeComponentUnavailable,
		Now:      fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake catalog: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	envelope := doFakeCatalogEnvelope(t, server, http.MethodGet, "/v1/catalog/health", nil, http.StatusServiceUnavailable)
	if envelope.OK || envelope.Error.Code != "component_unavailable" || !envelope.Error.Retryable {
		t.Fatalf("envelope = %#v", envelope)
	}
}

func TestFakeJobsHandlerExposesRequiredStates(t *testing.T) {
	handler, err := NewFakeJobsHandler(FakeJobsConfig{Now: fixedFakeClock})
	if err != nil {
		t.Fatalf("new fake jobs: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	envelope := doFakeJobsEnvelope(t, server, http.MethodGet, "/v1/jobs", nil, nil, http.StatusOK)
	var list struct {
		Items []contracts.Job `json:"items"`
	}
	decodeEnvelopeData(t, envelope, &list)
	states := map[contracts.JobState]bool{}
	for _, job := range list.Items {
		states[job.State] = true
	}
	for _, want := range []contracts.JobState{
		contracts.JobQueued,
		contracts.JobClaimed,
		contracts.JobRunning,
		contracts.JobSucceeded,
		contracts.JobFailed,
		contracts.JobCanceled,
		contracts.JobExpired,
	} {
		if !states[want] {
			t.Fatalf("state %q missing from %#v", want, list.Items)
		}
	}

	expiredEnvelope := doFakeJobsEnvelope(t, server, http.MethodGet, "/v1/jobs?state=expired", nil, nil, http.StatusOK)
	decodeEnvelopeData(t, expiredEnvelope, &list)
	if len(list.Items) != 1 || list.Items[0].State != contracts.JobExpired {
		t.Fatalf("expired list = %#v", list.Items)
	}
}

func TestFakeJobsHandlerRunsLifecycle(t *testing.T) {
	handler, err := NewFakeJobsHandler(FakeJobsConfig{Now: fixedFakeClock})
	if err != nil {
		t.Fatalf("new fake jobs: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	claimed := doFakeJobsEnvelope(t, server, http.MethodPost, "/v1/jobs/job_fake_queued/claim", nil, contracts.JobClaimRequest{
		WorkerID:     "runner_test",
		LeaseSeconds: 30,
	}, http.StatusOK)
	var job contracts.Job
	decodeEnvelopeData(t, claimed, &job)
	if job.State != contracts.JobClaimed || job.Claim == nil || job.Claim.WorkerID != "runner_test" {
		t.Fatalf("claimed job = %#v", job)
	}

	running := doFakeJobsEnvelope(t, server, http.MethodPost, "/v1/jobs/job_fake_queued/heartbeat", nil, contracts.JobHeartbeatRequest{
		WorkerID:      "runner_test",
		TransitionTo:  "running",
		StatusMessage: "fake running",
	}, http.StatusOK)
	decodeEnvelopeData(t, running, &job)
	if job.State != contracts.JobRunning || job.StatusMessage != "fake running" {
		t.Fatalf("running job = %#v", job)
	}

	logs := doFakeJobsEnvelope(t, server, http.MethodPost, "/v1/jobs/job_fake_queued/logs", nil, contracts.AppendJobLogRequest{
		WorkerID: "runner_test",
		Entries: []contracts.JobLogEntry{{
			Timestamp: "2026-06-08T00:02:00Z",
			Level:     "info",
			Message:   "progress",
			Fields:    map[string]any{},
		}},
	}, http.StatusOK)
	var logList struct {
		Items []contracts.JobLogEntry `json:"items"`
	}
	decodeEnvelopeData(t, logs, &logList)
	if len(logList.Items) < 2 {
		t.Fatalf("logs = %#v", logList.Items)
	}

	completed := doFakeJobsEnvelope(t, server, http.MethodPost, "/v1/jobs/job_fake_queued/complete", nil, contracts.JobCompleteRequest{
		WorkerID:     "runner_test",
		ArtifactRefs: []string{"art_test"},
	}, http.StatusOK)
	decodeEnvelopeData(t, completed, &job)
	if job.State != contracts.JobSucceeded || len(job.ArtifactRefs) != 1 || job.ArtifactRefs[0] != "art_test" {
		t.Fatalf("completed job = %#v", job)
	}
}

func TestFakeJobsHandlerCreatesAndCancelsWithIdempotency(t *testing.T) {
	handler, err := NewFakeJobsHandler(FakeJobsConfig{Now: fixedFakeClock})
	if err != nil {
		t.Fatalf("new fake jobs: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	missingCreateKey := doFakeJobsEnvelope(t, server, http.MethodPost, "/v1/jobs", nil, contracts.CreateJobRequest{RequesterID: "sub_test"}, http.StatusBadRequest)
	if missingCreateKey.OK || missingCreateKey.Error.Code != "missing_idempotency_key" {
		t.Fatalf("missing create key = %#v", missingCreateKey)
	}

	created := doFakeJobsEnvelope(t, server, http.MethodPost, "/v1/jobs", map[string]string{"Idempotency-Key": "create-1"}, contracts.CreateJobRequest{
		RequesterID:  "sub_test",
		CapabilityID: "cap_test",
		InputSummary: map[string]any{"prompt": "hello"},
	}, http.StatusCreated)
	var job contracts.Job
	decodeEnvelopeData(t, created, &job)
	if job.State != contracts.JobQueued || job.JobID == "" {
		t.Fatalf("created job = %#v", job)
	}

	replayed := doFakeJobsEnvelope(t, server, http.MethodPost, "/v1/jobs", map[string]string{"Idempotency-Key": "create-1"}, contracts.CreateJobRequest{
		RequesterID:  "sub_test",
		CapabilityID: "cap_test",
		InputSummary: map[string]any{"prompt": "hello"},
	}, http.StatusOK)
	var replayedJob contracts.Job
	decodeEnvelopeData(t, replayed, &replayedJob)
	if replayedJob.JobID != job.JobID {
		t.Fatalf("replayed job = %#v, created = %#v", replayedJob, job)
	}

	createConflict := doFakeJobsEnvelope(t, server, http.MethodPost, "/v1/jobs", map[string]string{"Idempotency-Key": "create-1"}, contracts.CreateJobRequest{
		RequesterID:  "sub_test",
		CapabilityID: "cap_other",
	}, http.StatusConflict)
	if createConflict.OK || createConflict.Error.Code != "idempotency_conflict" {
		t.Fatalf("create conflict = %#v", createConflict)
	}

	missingCancelKey := doFakeJobsEnvelope(t, server, http.MethodPost, "/v1/jobs/job_fake_cancelable/cancel", nil, contracts.CancelRequest{Reason: "stop"}, http.StatusBadRequest)
	if missingCancelKey.OK || missingCancelKey.Error.Code != "missing_idempotency_key" {
		t.Fatalf("missing cancel key = %#v", missingCancelKey)
	}

	canceled := doFakeJobsEnvelope(t, server, http.MethodPost, "/v1/jobs/job_fake_cancelable/cancel", map[string]string{"Idempotency-Key": "cancel-1"}, contracts.CancelRequest{Reason: "stop"}, http.StatusOK)
	decodeEnvelopeData(t, canceled, &job)
	if job.State != contracts.JobCanceled || job.StatusMessage != "stop" {
		t.Fatalf("canceled job = %#v", job)
	}

	conflict := doFakeJobsEnvelope(t, server, http.MethodPost, "/v1/jobs/job_fake_cancelable/cancel", map[string]string{"Idempotency-Key": "cancel-1"}, contracts.CancelRequest{Reason: "different"}, http.StatusConflict)
	if conflict.OK || conflict.Error.Code != "idempotency_conflict" {
		t.Fatalf("conflict = %#v", conflict)
	}
}

func TestFakeJobsHandlerSupportsUnavailableBehavior(t *testing.T) {
	handler, err := NewFakeJobsHandler(FakeJobsConfig{
		Behavior: FakeComponentUnavailable,
		Now:      fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake jobs: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	envelope := doFakeJobsEnvelope(t, server, http.MethodGet, "/v1/jobs/health", nil, nil, http.StatusServiceUnavailable)
	if envelope.OK || envelope.Error.Code != "component_unavailable" || !envelope.Error.Retryable {
		t.Fatalf("envelope = %#v", envelope)
	}
}

func TestFakeLeasesHandlerExposesResourceAndRequestStates(t *testing.T) {
	handler, err := NewFakeLeasesHandler(FakeLeasesConfig{Now: fixedFakeClock})
	if err != nil {
		t.Fatalf("new fake leases: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	resourcesEnvelope := doFakeLeasesEnvelope(t, server, http.MethodGet, "/v1/resources", nil, nil, http.StatusOK)
	var resources struct {
		Items []contracts.ResourceRecord `json:"items"`
	}
	decodeEnvelopeData(t, resourcesEnvelope, &resources)
	statuses := map[contracts.ResourceStatus]bool{}
	for _, resource := range resources.Items {
		statuses[resource.Status] = true
	}
	if !statuses[contracts.ResourceAvailable] || !statuses[contracts.ResourceUnavailable] {
		t.Fatalf("resource statuses = %#v", resources.Items)
	}

	for requesterID, wantState := range map[string]contracts.LeaseRequestState{
		"job_fake_holder":   contracts.LeaseRequestGranted,
		"job_fake_waiting":  contracts.LeaseRequestPending,
		"job_fake_expired":  contracts.LeaseRequestExpired,
		"job_fake_canceled": contracts.LeaseRequestCanceled,
	} {
		envelope := doFakeLeasesEnvelope(t, server, http.MethodGet, "/v1/lease-requests?requester_id="+requesterID, nil, nil, http.StatusOK)
		var list struct {
			Items []contracts.LeaseRequest `json:"items"`
		}
		decodeEnvelopeData(t, envelope, &list)
		if len(list.Items) != 1 || list.Items[0].State != wantState {
			t.Fatalf("requester %s list = %#v, want %s", requesterID, list.Items, wantState)
		}
	}

	inspectionEnvelope := doFakeLeasesEnvelope(t, server, http.MethodGet, "/v1/resources/res_fake_gpu/inspection", nil, nil, http.StatusOK)
	var inspection contracts.ResourceInspection
	decodeEnvelopeData(t, inspectionEnvelope, &inspection)
	if inspection.ActiveLease == nil || inspection.QueueLength == 0 {
		t.Fatalf("inspection = %#v", inspection)
	}
}

func TestFakeLeasesHandlerCreatesQueuesReleasesAndPromotes(t *testing.T) {
	handler, err := NewFakeLeasesHandler(FakeLeasesConfig{Now: fixedFakeClock})
	if err != nil {
		t.Fatalf("new fake leases: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	grantedEnvelope := doFakeLeasesEnvelope(t, server, http.MethodPost, "/v1/lease-requests", nil, contracts.CreateLeaseRequest{
		RequesterID:      "job_cpu",
		ResourceSelector: "cpu",
	}, http.StatusCreated)
	var request contracts.LeaseRequest
	decodeEnvelopeData(t, grantedEnvelope, &request)
	if request.State != contracts.LeaseRequestGranted || request.Lease == nil || request.Lease.HolderID != "job_cpu" {
		t.Fatalf("granted request = %#v", request)
	}

	queuedEnvelope := doFakeLeasesEnvelope(t, server, http.MethodPost, "/v1/lease-requests", nil, contracts.CreateLeaseRequest{
		RequesterID:      "job_gpu_queued",
		ResourceSelector: "gpu",
	}, http.StatusCreated)
	decodeEnvelopeData(t, queuedEnvelope, &request)
	if request.State != contracts.LeaseRequestPending || request.QueuePosition == nil {
		t.Fatalf("queued request = %#v", request)
	}

	heartbeatEnvelope := doFakeLeasesEnvelope(t, server, http.MethodPost, "/v1/leases/lease_fake_active/heartbeat", nil, contracts.LeaseHeartbeatRequest{
		HolderID: "job_fake_holder",
	}, http.StatusOK)
	var lease contracts.Lease
	decodeEnvelopeData(t, heartbeatEnvelope, &lease)
	if lease.HolderID != "job_fake_holder" {
		t.Fatalf("heartbeat lease = %#v", lease)
	}

	missingKey := doFakeLeasesEnvelope(t, server, http.MethodPost, "/v1/leases/lease_fake_active/release", nil, contracts.LeaseReleaseRequest{
		HolderID: "job_fake_holder",
	}, http.StatusBadRequest)
	if missingKey.OK || missingKey.Error.Code != "missing_idempotency_key" {
		t.Fatalf("missing key = %#v", missingKey)
	}

	releasedEnvelope := doFakeLeasesEnvelope(t, server, http.MethodPost, "/v1/leases/lease_fake_active/release", map[string]string{
		"Idempotency-Key":    "release-1",
		"X-Actor-Subject-ID": "sub_runner",
	}, contracts.LeaseReleaseRequest{
		HolderID: "job_fake_holder",
		Reason:   "done",
	}, http.StatusOK)
	decodeEnvelopeData(t, releasedEnvelope, &lease)
	if lease.ReleasedBy != "sub_runner" || lease.ReleaseReason != "done" {
		t.Fatalf("released lease = %#v", lease)
	}

	promotedEnvelope := doFakeLeasesEnvelope(t, server, http.MethodGet, "/v1/lease-requests/lease_req_fake_pending", nil, nil, http.StatusOK)
	decodeEnvelopeData(t, promotedEnvelope, &request)
	if request.State != contracts.LeaseRequestGranted || request.Lease == nil || request.Lease.HolderID != "job_fake_waiting" {
		t.Fatalf("promoted request = %#v", request)
	}
}

func TestFakeLeasesHandlerSupportsDeniedAndUnavailableBehavior(t *testing.T) {
	handler, err := NewFakeLeasesHandler(FakeLeasesConfig{Now: fixedFakeClock})
	if err != nil {
		t.Fatalf("new fake leases: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	denied := doFakeLeasesEnvelope(t, server, http.MethodPost, "/v1/lease-requests", nil, contracts.CreateLeaseRequest{
		RequesterID:      "job_denied",
		ResourceSelector: "missing",
	}, http.StatusConflict)
	if denied.OK || denied.Error.Code != "resource_unavailable" || !denied.Error.Retryable {
		t.Fatalf("denied = %#v", denied)
	}

	unavailable, err := NewFakeLeasesHandler(FakeLeasesConfig{
		Behavior: FakeComponentUnavailable,
		Now:      fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new unavailable fake leases: %v", err)
	}
	unavailableServer := httptest.NewServer(unavailable)
	defer unavailableServer.Close()

	envelope := doFakeLeasesEnvelope(t, unavailableServer, http.MethodGet, "/v1/leases/health", nil, nil, http.StatusServiceUnavailable)
	if envelope.OK || envelope.Error.Code != "component_unavailable" || !envelope.Error.Retryable {
		t.Fatalf("envelope = %#v", envelope)
	}
}

func TestFakeArtifactsHandlerExposesAvailableDeniedExpiredAndMissingOutcomes(t *testing.T) {
	handler, err := NewFakeArtifactsHandler(FakeArtifactsConfig{Now: fixedFakeClock})
	if err != nil {
		t.Fatalf("new fake artifacts: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	listEnvelope := doFakeArtifactsEnvelope(t, server, http.MethodGet, "/v1/artifacts?producer_ref=job_fake_001", nil, nil, http.StatusOK)
	var list struct {
		Items []contracts.Artifact `json:"items"`
	}
	decodeEnvelopeData(t, listEnvelope, &list)
	if len(list.Items) != 1 || list.Items[0].ArtifactID != "art_fake_available" {
		t.Fatalf("list = %#v", list.Items)
	}

	metadata := doFakeArtifactsEnvelope(t, server, http.MethodGet, "/v1/artifacts/art_fake_available", nil, nil, http.StatusOK)
	var artifact contracts.Artifact
	decodeEnvelopeData(t, metadata, &artifact)
	if artifact.OwnerSubjectID != "sub_fake_agent" || artifact.Links["content"] == nil {
		t.Fatalf("artifact = %#v", artifact)
	}

	policy := doFakeArtifactsEnvelope(t, server, http.MethodGet, "/v1/artifacts/art_fake_available/policy-context", nil, nil, http.StatusOK)
	var context contracts.ArtifactPolicyContext
	decodeEnvelopeData(t, policy, &context)
	if context.ArtifactID != "art_fake_available" || context.OwnerSubjectID != "sub_fake_agent" {
		t.Fatalf("policy context = %#v", context)
	}

	body, headers := doFakeArtifactContent(t, server, "/v1/artifacts/art_fake_available/content", http.StatusOK)
	if string(body) != "fake artifact body" || headers.Get("Digest") != checksumString(body) {
		t.Fatalf("content body=%q headers=%#v", string(body), headers)
	}

	denied := doFakeArtifactsEnvelope(t, server, http.MethodGet, "/v1/artifacts/art_fake_denied", nil, nil, http.StatusForbidden)
	if denied.OK || denied.Error.Code != "forbidden" {
		t.Fatalf("denied = %#v", denied)
	}
	expired := doFakeArtifactsEnvelope(t, server, http.MethodGet, "/v1/artifacts/art_fake_expired", nil, nil, http.StatusGone)
	if expired.OK || expired.Error.Code != "artifact_expired" {
		t.Fatalf("expired = %#v", expired)
	}
	missing := doFakeArtifactsEnvelope(t, server, http.MethodGet, "/v1/artifacts/art_missing", nil, nil, http.StatusNotFound)
	if missing.OK || missing.Error.Code != "not_found" {
		t.Fatalf("missing = %#v", missing)
	}

	upload := doFakeArtifactsEnvelope(t, server, http.MethodGet, "/v1/artifact-uploads/upload_fake_expired", nil, nil, http.StatusOK)
	var session contracts.ArtifactUploadSession
	decodeEnvelopeData(t, upload, &session)
	if session.State != contracts.ArtifactUploadExpired {
		t.Fatalf("upload = %#v", session)
	}
	expiredPut := doFakeArtifactPutContent(t, server, "/v1/artifact-uploads/upload_fake_expired/content", []byte("late"), map[string]string{"Idempotency-Key": "content-expired"}, http.StatusGone)
	if expiredPut.OK || expiredPut.Error.Code != "artifact_expired" {
		t.Fatalf("expired put = %#v", expiredPut)
	}
}

func TestFakeArtifactsHandlerUploadLifecycleAndRegisterLocal(t *testing.T) {
	handler, err := NewFakeArtifactsHandler(FakeArtifactsConfig{Now: fixedFakeClock})
	if err != nil {
		t.Fatalf("new fake artifacts: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	missingKey := doFakeArtifactsEnvelope(t, server, http.MethodPost, "/v1/artifact-uploads", nil, contracts.CreateArtifactUploadRequest{
		Name:           "result.txt",
		MediaType:      "text/plain",
		OwnerSubjectID: "sub_agent",
	}, http.StatusBadRequest)
	if missingKey.OK || missingKey.Error.Code != "missing_idempotency_key" {
		t.Fatalf("missing key = %#v", missingKey)
	}

	created := doFakeArtifactsEnvelope(t, server, http.MethodPost, "/v1/artifact-uploads", map[string]string{"Idempotency-Key": "upload-create"}, contracts.CreateArtifactUploadRequest{
		Name:           "result.txt",
		MediaType:      "text/plain",
		ProducerRef:    "job_upload",
		OwnerSubjectID: "sub_agent",
	}, http.StatusCreated)
	var session contracts.ArtifactUploadSession
	decodeEnvelopeData(t, created, &session)
	if session.State != contracts.ArtifactUploadCreated || session.UploadID == "" {
		t.Fatalf("created = %#v", session)
	}

	body := []byte("uploaded artifact")
	received := doFakeArtifactPutContent(t, server, "/v1/artifact-uploads/"+session.UploadID+"/content", body, map[string]string{"Idempotency-Key": "upload-content"}, http.StatusOK)
	decodeEnvelopeData(t, received, &session)
	if session.State != contracts.ArtifactUploadReceived || session.ReceivedSize == nil || *session.ReceivedSize != int64(len(body)) {
		t.Fatalf("received = %#v", session)
	}

	completed := doFakeArtifactsEnvelope(t, server, http.MethodPost, "/v1/artifact-uploads/"+session.UploadID+"/complete", map[string]string{"Idempotency-Key": "upload-complete"}, contracts.CompleteArtifactUploadRequest{
		Checksum: checksumString(body),
		Size:     int64(len(body)),
	}, http.StatusCreated)
	var artifact contracts.Artifact
	decodeEnvelopeData(t, completed, &artifact)
	if artifact.ProducerRef != "job_upload" || artifact.OwnerSubjectID != "sub_agent" {
		t.Fatalf("completed artifact = %#v", artifact)
	}

	readBody, _ := doFakeArtifactContent(t, server, "/v1/artifacts/"+artifact.ArtifactID+"/content", http.StatusOK)
	if string(readBody) != string(body) {
		t.Fatalf("read body = %q", string(readBody))
	}

	local := doFakeArtifactsEnvelope(t, server, http.MethodPost, "/v1/artifacts/register-local", nil, contracts.RegisterLocalArtifactRequest{
		Path:           "/tmp/secret-provider-path.txt",
		Name:           "local.txt",
		MediaType:      "text/plain",
		OwnerSubjectID: "sub_agent",
	}, http.StatusCreated)
	decodeEnvelopeData(t, local, &artifact)
	raw, err := json.Marshal(artifact)
	if err != nil {
		t.Fatalf("marshal artifact: %v", err)
	}
	if bytes.Contains(raw, []byte("secret-provider-path")) {
		t.Fatalf("local artifact leaked path: %s", string(raw))
	}
}

func TestFakeArtifactsHandlerSupportsUnavailableBehavior(t *testing.T) {
	handler, err := NewFakeArtifactsHandler(FakeArtifactsConfig{
		Behavior: FakeComponentUnavailable,
		Now:      fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake artifacts: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	envelope := doFakeArtifactsEnvelope(t, server, http.MethodGet, "/v1/artifacts/health", nil, nil, http.StatusServiceUnavailable)
	if envelope.OK || envelope.Error.Code != "component_unavailable" || !envelope.Error.Retryable {
		t.Fatalf("envelope = %#v", envelope)
	}
}

func TestFakePolicyHandlerSupportsAuthAllowAndDeny(t *testing.T) {
	handler := NewFakePolicyHandler(FakePolicyConfig{
		ValidCredential: "token_policy",
		SubjectID:       "sub_policy",
		Scopes:          []string{"component", "worker"},
		Decision:        contracts.PolicyDecision{Allowed: true, Reason: "test_allow"},
		Now:             fixedFakeClock,
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	verify := postFakePolicyEnvelope(t, server, "/v1/auth/verify", contracts.VerifyCredentialRequest{Credential: "Bearer token_policy"}, http.StatusOK)
	var verification contracts.CredentialVerification
	decodeEnvelopeData(t, verify, &verification)
	if !verification.Valid || verification.SubjectID == nil || *verification.SubjectID != "sub_policy" {
		t.Fatalf("verification = %#v", verification)
	}
	if len(verification.Scopes) != 2 {
		t.Fatalf("scopes = %#v", verification.Scopes)
	}

	check := postFakePolicyEnvelope(t, server, "/v1/policy/check", contracts.PolicyCheckRequest{
		SubjectID: "sub_policy",
		Action:    "tool.invoke",
		Resource:  "cap_fake",
	}, http.StatusOK)
	var decision contracts.PolicyDecision
	decodeEnvelopeData(t, check, &decision)
	if !decision.Allowed || decision.Reason != "test_allow" {
		t.Fatalf("decision = %#v", decision)
	}

	denyHandler := NewFakePolicyHandler(FakePolicyConfig{
		Decision: contracts.PolicyDecision{Allowed: false, Reason: "test_deny"},
	})
	denyServer := httptest.NewServer(denyHandler)
	defer denyServer.Close()
	denyEnvelope := postFakePolicyEnvelope(t, denyServer, "/v1/policy/check", contracts.PolicyCheckRequest{
		SubjectID: "sub_policy",
		Action:    "tool.invoke",
		Resource:  "cap_fake",
	}, http.StatusOK)
	decodeEnvelopeData(t, denyEnvelope, &decision)
	if decision.Allowed || decision.Reason != "test_deny" {
		t.Fatalf("deny decision = %#v", decision)
	}
}

func TestFakePolicyHandlerSupportsAuthFailure(t *testing.T) {
	handler := NewFakePolicyHandler(FakePolicyConfig{ValidCredential: "token_policy"})
	server := httptest.NewServer(handler)
	defer server.Close()

	envelope := postFakePolicyEnvelope(t, server, "/v1/auth/verify", contracts.VerifyCredentialRequest{Credential: "Bearer other"}, http.StatusOK)
	var verification contracts.CredentialVerification
	decodeEnvelopeData(t, envelope, &verification)
	if verification.Valid || verification.SubjectID != nil || len(verification.Scopes) != 0 {
		t.Fatalf("verification = %#v", verification)
	}
}

func TestFakePolicyHandlerSupportsSecretsAndRedaction(t *testing.T) {
	handler := NewFakePolicyHandler(FakePolicyConfig{
		SubjectID: "sub_component",
		Secrets:   map[string]string{"secret_fake": "super-secret"},
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	resolve := postFakePolicyEnvelope(t, server, "/v1/secrets/resolve", contracts.ResolveSecretRequest{
		SecretRef: "secret_fake",
		SubjectID: "sub_component",
	}, http.StatusOK)
	var secret contracts.ResolvedSecret
	decodeEnvelopeData(t, resolve, &secret)
	if secret.Value != "super-secret" {
		t.Fatalf("secret = %#v", secret)
	}

	redact := postFakePolicyEnvelope(t, server, "/v1/redact", contracts.RedactRequest{Text: "token is super-secret"}, http.StatusOK)
	var redacted contracts.RedactResponse
	decodeEnvelopeData(t, redact, &redacted)
	if redacted.Text != "token is [REDACTED]" {
		t.Fatalf("redacted = %#v", redacted)
	}

	forbidden := postFakePolicyEnvelope(t, server, "/v1/secrets/resolve", contracts.ResolveSecretRequest{
		SecretRef: "secret_fake",
		SubjectID: "sub_agent",
	}, http.StatusForbidden)
	if forbidden.OK || forbidden.Error.Code != "forbidden" {
		t.Fatalf("forbidden envelope = %#v", forbidden)
	}
}

func TestFakePolicyHandlerRequiresComponentCredential(t *testing.T) {
	handler := NewFakePolicyHandler(FakePolicyConfig{ComponentCredential: "component-token"})
	server := httptest.NewServer(handler)
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/v1/policy/health", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	req, err = http.NewRequest(http.MethodGet, server.URL+"/v1/policy/health", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer component-token")
	resp, err = server.Client().Do(req)
	if err != nil {
		t.Fatalf("get authorized health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authorized status = %d", resp.StatusCode)
	}
}

func TestFakeNodeHandlerExposesServiceStates(t *testing.T) {
	handler, err := NewFakeNodeHandler(FakeNodeConfig{Now: fixedFakeClock})
	if err != nil {
		t.Fatalf("new fake node: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	envelope := doFakeNodeEnvelope(t, server, http.MethodGet, "/v1/node/services", nil, http.StatusOK)
	var list struct {
		Items []contracts.NodeService `json:"items"`
	}
	decodeEnvelopeData(t, envelope, &list)
	statuses := map[string]bool{}
	for _, service := range list.Items {
		statuses[service.Status] = true
	}
	for _, want := range []string{"running", "stopped", "starting", "failed"} {
		if !statuses[want] {
			t.Fatalf("status %q missing from %#v", want, list.Items)
		}
	}
}

func TestFakeNodeHandlerGetsOneService(t *testing.T) {
	handler, err := NewFakeNodeHandler(FakeNodeConfig{Now: fixedFakeClock})
	if err != nil {
		t.Fatalf("new fake node: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	envelope := doFakeNodeEnvelope(t, server, http.MethodGet, "/v1/node/services/svc_fake_failed", nil, http.StatusOK)
	var service contracts.NodeService
	decodeEnvelopeData(t, envelope, &service)
	if service.ServiceID != "svc_fake_failed" || service.Status != "failed" {
		t.Fatalf("service = %#v", service)
	}

	missing := doFakeNodeEnvelope(t, server, http.MethodGet, "/v1/node/services/svc_missing", nil, http.StatusNotFound)
	if missing.OK || missing.Error.Code != "not_found" {
		t.Fatalf("missing envelope = %#v", missing)
	}
}

func TestFakeNodeHandlerStartsAndStopsService(t *testing.T) {
	handler, err := NewFakeNodeHandler(FakeNodeConfig{Now: fixedFakeClock})
	if err != nil {
		t.Fatalf("new fake node: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	missingKey := doFakeNodeEnvelope(t, server, http.MethodPost, "/v1/node/services/svc_fake_stopped/start", nil, http.StatusBadRequest)
	if missingKey.OK || missingKey.Error.Code != "missing_idempotency_key" {
		t.Fatalf("missing key envelope = %#v", missingKey)
	}

	touchStopped := doFakeNodeEnvelope(t, server, http.MethodPost, "/v1/node/services/svc_fake_stopped/touch", nil, http.StatusServiceUnavailable)
	if touchStopped.OK || touchStopped.Error.Code != "provider_unavailable" {
		t.Fatalf("touch stopped envelope = %#v", touchStopped)
	}

	started := doFakeNodeEnvelope(t, server, http.MethodPost, "/v1/node/services/svc_fake_stopped/start", map[string]string{
		"Idempotency-Key": "fake-node-start-1",
	}, http.StatusAccepted)
	var service contracts.NodeService
	decodeEnvelopeData(t, started, &service)
	if service.Status != "starting" {
		t.Fatalf("start service = %#v", service)
	}

	touched := doFakeNodeEnvelope(t, server, http.MethodPost, "/v1/node/services/svc_fake_running/touch", nil, http.StatusOK)
	decodeEnvelopeData(t, touched, &service)
	if service.Status != "running" {
		t.Fatalf("touch service = %#v", service)
	}

	stopped := doFakeNodeEnvelope(t, server, http.MethodPost, "/v1/node/services/svc_fake_stopped/stop", map[string]string{
		"Idempotency-Key": "fake-node-stop-1",
	}, http.StatusAccepted)
	decodeEnvelopeData(t, stopped, &service)
	if service.Status != "stopped" {
		t.Fatalf("stop service = %#v", service)
	}
}

func TestFakeNodeHandlerSupportsUnavailableBehavior(t *testing.T) {
	handler, err := NewFakeNodeHandler(FakeNodeConfig{
		Behavior: FakeComponentUnavailable,
		Now:      fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake node: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	envelope := doFakeNodeEnvelope(t, server, http.MethodGet, "/v1/node/health", nil, http.StatusServiceUnavailable)
	if envelope.OK || envelope.Error.Code != "component_unavailable" || !envelope.Error.Retryable {
		t.Fatalf("envelope = %#v", envelope)
	}
}

func TestFakeProviderHandlerPassesProviderCheck(t *testing.T) {
	handler, err := NewFakeProviderHandler(FakeProviderConfig{
		Endpoint:   "http://provider.fake",
		Credential: "provider-token",
		Now:        fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake provider: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	report := CheckProvider(context.Background(), server.Client(), ProviderCheckOptions{
		BaseURL:      server.URL,
		CapabilityID: "cap_echo",
		Input:        map[string]any{"message": "hello"},
		Credential:   "Bearer provider-token",
		RequestID:    "req_fake_provider",
	})
	if !report.Passed() {
		t.Fatalf("report = %#v", report)
	}
}

func TestFakeProviderHandlerPassesArtifactProviderCheck(t *testing.T) {
	handler, err := NewFakeProviderHandler(FakeProviderConfig{
		Endpoint: "http://provider.fake",
		Now:      fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake provider: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	report := CheckProvider(context.Background(), server.Client(), ProviderCheckOptions{
		BaseURL:      server.URL,
		CapabilityID: "cap_artifact",
		Input:        map[string]any{"prompt": "hello"},
		RequestID:    "req_fake_provider",
	})
	if !report.Passed() {
		t.Fatalf("report = %#v", report)
	}
	if !hasProviderCheck(report, "provider.artifact_metadata") {
		t.Fatalf("artifact metadata check missing: %#v", report.Checks)
	}
}

func TestFakeProviderHandlerPassesAsyncProviderCheck(t *testing.T) {
	handler, err := NewFakeProviderHandler(FakeProviderConfig{
		Endpoint: "http://provider.fake",
		Now:      fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake provider: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	report := CheckProvider(context.Background(), server.Client(), ProviderCheckOptions{
		BaseURL:      server.URL,
		CapabilityID: "cap_async_accept",
		Input:        map[string]any{},
		RequestID:    "req_fake_provider",
	})
	if !report.Passed() {
		t.Fatalf("report = %#v", report)
	}
}

func TestFakeProviderHandlerPassesExpectedFailureCheck(t *testing.T) {
	handler, err := NewFakeProviderHandler(FakeProviderConfig{
		Endpoint: "http://provider.fake",
		Now:      fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake provider: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	report := CheckProviderExpectedError(context.Background(), server.Client(), ProviderExpectedErrorOptions{
		BaseURL:        server.URL,
		CapabilityID:   "cap_fail",
		WantHTTPStatus: 503,
		WantCode:       "provider_unavailable",
		RequestID:      "req_fake_provider_failure",
	})
	if !report.Passed() {
		t.Fatalf("report = %#v", report)
	}
}

func TestFakeProviderHandlerRequiresCredential(t *testing.T) {
	handler, err := NewFakeProviderHandler(FakeProviderConfig{
		Endpoint:   "http://provider.fake",
		Credential: "provider-token",
		Now:        fixedFakeClock,
	})
	if err != nil {
		t.Fatalf("new fake provider: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	report := CheckProvider(context.Background(), server.Client(), ProviderCheckOptions{
		BaseURL:   server.URL,
		RequestID: "req_fake_provider",
	})
	if report.Passed() {
		t.Fatalf("unauthenticated provider check unexpectedly passed: %#v", report)
	}
}

func fixedFakeClock() time.Time {
	return time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)
}

func hasProviderCheck(report ProviderCheckReport, name string) bool {
	for _, check := range report.Checks {
		if check.Name == name && check.OK {
			return true
		}
	}
	return false
}

type fakePolicyEnvelope struct {
	OK    bool                  `json:"ok"`
	Data  json.RawMessage       `json:"data"`
	Error contracts.ErrorObject `json:"error"`
}

func postFakePolicyEnvelope(t *testing.T, server *httptest.Server, path string, body any, wantStatus int) fakePolicyEnvelope {
	t.Helper()
	var raw bytes.Buffer
	if err := json.NewEncoder(&raw).Encode(body); err != nil {
		t.Fatalf("encode body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, server.URL+path, &raw)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "req_fake_policy")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("POST %s status = %d, want %d", path, resp.StatusCode, wantStatus)
	}
	var envelope fakePolicyEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return envelope
}

func doFakeNodeEnvelope(t *testing.T, server *httptest.Server, method, path string, headers map[string]string, wantStatus int) fakePolicyEnvelope {
	t.Helper()
	req, err := http.NewRequest(method, server.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-Request-ID", "req_fake_node")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d", method, path, resp.StatusCode, wantStatus)
	}
	var envelope fakePolicyEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return envelope
}

func doFakeCatalogEnvelope(t *testing.T, server *httptest.Server, method, path string, body any, wantStatus int) fakePolicyEnvelope {
	t.Helper()
	var req *http.Request
	var err error
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		req, err = http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
	} else {
		req, err = http.NewRequest(method, server.URL+path, nil)
	}
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-Request-ID", "req_fake_catalog")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d", method, path, resp.StatusCode, wantStatus)
	}
	var envelope fakePolicyEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return envelope
}

func doFakeJobsEnvelope(t *testing.T, server *httptest.Server, method, path string, headers map[string]string, body any, wantStatus int) fakePolicyEnvelope {
	t.Helper()
	var req *http.Request
	var err error
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		req, err = http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
	} else {
		req, err = http.NewRequest(method, server.URL+path, nil)
	}
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-Request-ID", "req_fake_jobs")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d", method, path, resp.StatusCode, wantStatus)
	}
	var envelope fakePolicyEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return envelope
}

func doFakeLeasesEnvelope(t *testing.T, server *httptest.Server, method, path string, headers map[string]string, body any, wantStatus int) fakePolicyEnvelope {
	t.Helper()
	var req *http.Request
	var err error
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		req, err = http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
	} else {
		req, err = http.NewRequest(method, server.URL+path, nil)
	}
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-Request-ID", "req_fake_leases")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d", method, path, resp.StatusCode, wantStatus)
	}
	var envelope fakePolicyEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return envelope
}

func doFakeArtifactsEnvelope(t *testing.T, server *httptest.Server, method, path string, headers map[string]string, body any, wantStatus int) fakePolicyEnvelope {
	t.Helper()
	var req *http.Request
	var err error
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		req, err = http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
	} else {
		req, err = http.NewRequest(method, server.URL+path, nil)
	}
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-Request-ID", "req_fake_artifacts")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d", method, path, resp.StatusCode, wantStatus)
	}
	var envelope fakePolicyEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return envelope
}

func doFakeArtifactPutContent(t *testing.T, server *httptest.Server, path string, body []byte, headers map[string]string, wantStatus int) fakePolicyEnvelope {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, server.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-Request-ID", "req_fake_artifacts")
	req.Header.Set("Content-Type", "text/plain")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("PUT %s status = %d, want %d", path, resp.StatusCode, wantStatus)
	}
	var envelope fakePolicyEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return envelope
}

func doFakeArtifactContent(t *testing.T, server *httptest.Server, path string, wantStatus int) ([]byte, http.Header) {
	t.Helper()
	resp, err := server.Client().Get(server.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("GET %s status = %d, want %d", path, resp.StatusCode, wantStatus)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return body, resp.Header
}

func decodeEnvelopeData(t *testing.T, envelope fakePolicyEnvelope, out any) {
	t.Helper()
	if !envelope.OK {
		t.Fatalf("expected success envelope, got %#v", envelope.Error)
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		t.Fatalf("decode data: %v", err)
	}
}
