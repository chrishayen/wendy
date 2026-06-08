package testkit

import (
	"bytes"
	"context"
	"encoding/json"
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

	started := doFakeNodeEnvelope(t, server, http.MethodPost, "/v1/node/services/svc_fake_stopped/start", map[string]string{
		"Idempotency-Key": "fake-node-start-1",
	}, http.StatusAccepted)
	var service contracts.NodeService
	decodeEnvelopeData(t, started, &service)
	if service.Status != "starting" {
		t.Fatalf("start service = %#v", service)
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

func decodeEnvelopeData(t *testing.T, envelope fakePolicyEnvelope, out any) {
	t.Helper()
	if !envelope.OK {
		t.Fatalf("expected success envelope, got %#v", envelope.Error)
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		t.Fatalf("decode data: %v", err)
	}
}
