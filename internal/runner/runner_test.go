package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"pacp/internal/components/artifacts"
	"pacp/internal/components/jobs"
	"pacp/internal/components/leases"
	"pacp/internal/components/policy"
	"pacp/internal/contracts"
	"pacp/internal/observability"
	"pacp/internal/provider"
)

func TestRunnerCompletesJobAndUploadsArtifact(t *testing.T) {
	jobStore := jobs.NewStore()
	jobsServer := httptest.NewServer(jobs.NewHandler(jobStore))
	defer jobsServer.Close()

	leaseStore := leases.NewStore()
	if _, err := leaseStore.RegisterResource(contracts.RegisterResourceRequest{Selector: "gpu", Status: contracts.ResourceAvailable}); err != nil {
		t.Fatalf("register resource: %v", err)
	}
	leasesServer := httptest.NewServer(leases.NewHandler(leaseStore))
	defer leasesServer.Close()

	artifactStore, err := artifacts.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("artifact store: %v", err)
	}
	artifactsServer := httptest.NewServer(artifacts.NewHandler(artifactStore))
	defer artifactsServer.Close()

	providerServer := httptest.NewServer(newFakeProvider(t))
	defer providerServer.Close()

	route := contracts.CapabilityRoute{
		CapabilityID:       "cap_fake_image",
		ServiceID:          "svc_fake_provider",
		ProviderEndpoint:   providerServer.URL,
		ProviderHealthPath: "/v1/provider/health",
		ProviderInvokePath: "/v1/provider/capabilities/cap_fake_image/invoke",
		ServiceStartMode:   "manual",
		ResourceHints:      []contracts.ResourceHint{{Selector: "gpu", Required: true}},
		ArtifactHints:      []contracts.ArtifactHint{{MediaType: "text/plain", Count: "one"}},
	}
	created, _, err := jobStore.Create(contracts.CreateJobRequest{
		RequesterID:  "sub_agent",
		CapabilityID: "cap_fake_image",
		InputSummary: map[string]any{"prompt_present": true},
		Metadata: map[string]any{"execution_plan": map[string]any{
			"capability_id":     "cap_fake_image",
			"subject_id":        "sub_agent",
			"input":             map[string]any{"prompt": "red mug"},
			"route":             route,
			"resource_selector": "gpu",
			"timeout_seconds":   30,
		}},
	}, "create-job")
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	r := New(Config{
		WorkerID:       "runner_test",
		JobsURL:        jobsServer.URL,
		LeasesURL:      leasesServer.URL,
		ArtifactsURL:   artifactsServer.URL,
		ActorSubjectID: "sub_runner_test",
		Client:         jobsServer.Client(),
	})
	jobID, ok, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if !ok || jobID != created.JobID {
		t.Fatalf("run result jobID=%q ok=%v", jobID, ok)
	}
	completed, err := jobStore.Get(created.JobID)
	if err != nil {
		t.Fatalf("get completed job: %v", err)
	}
	if completed.State != contracts.JobSucceeded || len(completed.ArtifactRefs) != 1 {
		t.Fatalf("completed job = %#v", completed)
	}
	artifact, err := artifactStore.GetArtifact(completed.ArtifactRefs[0])
	if err != nil {
		t.Fatalf("get artifact: %v", err)
	}
	if artifact.ProducerRef != created.JobID || artifact.OwnerSubjectID != "sub_agent" {
		t.Fatalf("artifact = %#v", artifact)
	}
	auditEvents := leaseStore.AuditEvents()
	if len(auditEvents) != 1 || auditEvents[0].ActorSubjectID != "sub_runner_test" {
		t.Fatalf("lease audit events = %#v", auditEvents)
	}

	metrics := r.Metrics(context.Background())
	assertContractMetric(t, metrics.Samples, "runner_run_once_total", map[string]string{"result": "success"}, 1)
	assertContractMetric(t, metrics.Samples, "runner_job_heartbeats_total", nil, 1)
	assertContractMetric(t, metrics.Samples, "runner_dependency_reachable", map[string]string{"dependency": "jobs", "required": "true", "status": "healthy"}, 1)
	assertContractMetricExists(t, metrics.Samples, "runner_last_successful_heartbeat_unix_seconds", nil)
}

func TestRunnerFetchesProviderContentRefsAndUploadsArtifact(t *testing.T) {
	jobStore := jobs.NewStore()
	jobsServer := httptest.NewServer(jobs.NewHandler(jobStore))
	defer jobsServer.Close()

	artifactStore, err := artifacts.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("artifact store: %v", err)
	}
	artifactsServer := httptest.NewServer(artifacts.NewHandler(artifactStore))
	defer artifactsServer.Close()

	providerBody := []byte("provider content bytes")
	checksum, digest := checksumAndDigest(providerBody)
	contentFetched := false
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token_runner" {
			t.Fatalf("provider Authorization = %q", got)
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/provider/capabilities/cap_ref_image/invoke":
			writeRunnerTestSuccess(w, http.StatusOK, contracts.ProviderInvokeResponse{
				Output: map[string]any{"result": "image_generated", "media_type": "image/png", "filename": "provider-image.png"},
				ContentRefs: []contracts.ProviderContentRef{{
					ContentRef: "pcr_test",
					Name:       "provider-image.png",
					MediaType:  "image/png",
					Size:       int64(len(providerBody)),
					Checksum:   checksum,
					ExpiresAt:  "2030-01-01T00:00:00Z",
				}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/provider/artifacts/pcr_test/content":
			contentFetched = true
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Length", strconv.Itoa(len(providerBody)))
			w.Header().Set("Digest", digest)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(providerBody)
		default:
			t.Fatalf("unexpected provider request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer providerServer.Close()

	route := contracts.CapabilityRoute{
		CapabilityID:       "cap_ref_image",
		ServiceID:          "svc_ref_provider",
		ProviderEndpoint:   providerServer.URL,
		ProviderHealthPath: "/v1/provider/health",
		ProviderInvokePath: "/v1/provider/capabilities/cap_ref_image/invoke",
		ServiceStartMode:   "manual",
		ArtifactHints:      []contracts.ArtifactHint{{MediaType: "image/png", Count: "one"}},
	}
	created, _, err := jobStore.Create(contracts.CreateJobRequest{
		RequesterID:  "sub_agent",
		CapabilityID: "cap_ref_image",
		InputSummary: map[string]any{"prompt_present": true},
		Metadata: map[string]any{"execution_plan": map[string]any{
			"capability_id":   "cap_ref_image",
			"subject_id":      "sub_agent",
			"input":           map[string]any{"prompt": "red mug"},
			"route":           route,
			"timeout_seconds": 30,
		}},
	}, "create-ref-job")
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	r := New(Config{
		WorkerID:            "runner_test",
		JobsURL:             jobsServer.URL,
		ArtifactsURL:        artifactsServer.URL,
		ComponentCredential: "Bearer token_runner",
		Client:              jobsServer.Client(),
	})
	jobID, ok, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if !ok || jobID != created.JobID {
		t.Fatalf("run result jobID=%q ok=%v", jobID, ok)
	}
	if !contentFetched {
		t.Fatal("provider content ref was not fetched")
	}
	completed, err := jobStore.Get(created.JobID)
	if err != nil {
		t.Fatalf("get completed job: %v", err)
	}
	if completed.State != contracts.JobSucceeded || len(completed.ArtifactRefs) != 1 {
		t.Fatalf("completed job = %#v", completed)
	}
	artifact, err := artifactStore.GetArtifact(completed.ArtifactRefs[0])
	if err != nil {
		t.Fatalf("get artifact: %v", err)
	}
	if artifact.Name != "provider-image.png" || artifact.MediaType != "image/png" || artifact.ProducerRef != created.JobID || artifact.OwnerSubjectID != "sub_agent" {
		t.Fatalf("artifact = %#v", artifact)
	}
	content, err := artifactStore.ReadContent(completed.ArtifactRefs[0])
	if err != nil {
		t.Fatalf("read artifact content: %v", err)
	}
	if !bytes.Equal(content.Body, providerBody) || content.Digest != digest {
		t.Fatalf("content = %#v", content)
	}
}

func TestRunnerPreservesProviderTimeoutFailureCodeAndReleaseReason(t *testing.T) {
	runProviderFailureBranch(t, contracts.ErrorObject{
		Code:      "provider_timeout",
		Message:   "provider invocation timed out",
		Retryable: true,
	}, http.StatusGatewayTimeout, "provider invocation timed out", "provider timed out")
}

func TestRunnerPreservesProviderUnavailableFailureCodeAndReleaseReason(t *testing.T) {
	runProviderFailureBranch(t, contracts.ErrorObject{
		Code:      "provider_unavailable",
		Message:   "ComfyUI backend is unavailable",
		Retryable: true,
	}, http.StatusServiceUnavailable, "provider invocation failed", "provider failed")
}

func TestRunnerTimesOutProviderInvocationAndKeepsClaimsAlive(t *testing.T) {
	jobStore := jobs.NewStore()
	var jobHeartbeats atomic.Int32
	jobsHandler := jobs.NewHandler(jobStore)
	jobsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/heartbeat") {
			jobHeartbeats.Add(1)
		}
		jobsHandler.ServeHTTP(w, r)
	}))
	defer jobsServer.Close()

	leaseStore := leases.NewStore()
	if _, err := leaseStore.RegisterResource(contracts.RegisterResourceRequest{Selector: "gpu", Status: contracts.ResourceAvailable}); err != nil {
		t.Fatalf("register resource: %v", err)
	}
	var leaseHeartbeats atomic.Int32
	leasesHandler := leases.NewHandler(leaseStore)
	leasesServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/heartbeat") {
			leaseHeartbeats.Add(1)
		}
		leasesHandler.ServeHTTP(w, r)
	}))
	defer leasesServer.Close()

	releaseProvider := make(chan struct{})
	var releaseProviderOnce sync.Once
	defer releaseProviderOnce.Do(func() { close(releaseProvider) })
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/provider/capabilities/cap_slow/invoke" {
			t.Fatalf("unexpected provider request %s %s", r.Method, r.URL.Path)
		}
		<-releaseProvider
	}))
	defer providerServer.Close()

	route := contracts.CapabilityRoute{
		CapabilityID:       "cap_slow",
		ServiceID:          "svc_slow_provider",
		ProviderEndpoint:   providerServer.URL,
		ProviderHealthPath: "/v1/provider/health",
		ProviderInvokePath: "/v1/provider/capabilities/cap_slow/invoke",
		ServiceStartMode:   "manual",
		ResourceHints:      []contracts.ResourceHint{{Selector: "gpu", Required: true}},
	}
	created, _, err := jobStore.Create(contracts.CreateJobRequest{
		RequesterID:  "sub_agent",
		CapabilityID: "cap_slow",
		InputSummary: map[string]any{"prompt_present": true},
		Metadata: map[string]any{"execution_plan": map[string]any{
			"capability_id":     "cap_slow",
			"subject_id":        "sub_agent",
			"input":             map[string]any{"prompt": "red mug"},
			"route":             route,
			"resource_selector": "gpu",
			"timeout_seconds":   1,
		}},
	}, "create-slow-provider-job")
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	runner := New(Config{
		WorkerID:                  "runner_test",
		JobsURL:                   jobsServer.URL,
		LeasesURL:                 leasesServer.URL,
		ArtifactsURL:              "http://artifacts.invalid",
		ActorSubjectID:            "sub_runner_test",
		ProviderHeartbeatInterval: 10 * time.Millisecond,
		Client:                    jobsServer.Client(),
	})
	jobID, ok, err := runner.RunOnce(context.Background())
	releaseProviderOnce.Do(func() { close(releaseProvider) })
	providerServer.CloseClientConnections()
	if err == nil {
		t.Fatal("expected provider timeout error")
	}
	if !ok || jobID != created.JobID {
		t.Fatalf("run result jobID=%q ok=%v", jobID, ok)
	}
	if jobHeartbeats.Load() < 2 {
		t.Fatalf("job heartbeats = %d, want initial plus keepalive", jobHeartbeats.Load())
	}
	if leaseHeartbeats.Load() == 0 {
		t.Fatal("lease was not kept alive during provider invocation")
	}
	failed, err := jobStore.Get(created.JobID)
	if err != nil {
		t.Fatalf("get failed job: %v", err)
	}
	if failed.State != contracts.JobFailed || failed.TerminalError == nil || failed.TerminalError.Code != "provider_timeout" || !failed.TerminalError.Retryable {
		t.Fatalf("failed job = %#v", failed)
	}
	metrics := runner.Metrics(context.Background())
	assertContractMetric(t, metrics.Samples, "runner_run_once_total", map[string]string{"result": "error"}, 1)
	assertContractMetric(t, metrics.Samples, "runner_errors_total", map[string]string{"code": "provider_timeout"}, 1)
	events := leaseStore.AuditEvents()
	if len(events) != 1 || events[0].ReleaseReason != "provider timed out" {
		t.Fatalf("lease audit events = %#v", events)
	}
}

func TestRunnerFailsJobWhenLeaseExpiresDuringProviderInvocation(t *testing.T) {
	jobStore := jobs.NewStore()
	jobsServer := httptest.NewServer(jobs.NewHandler(jobStore))
	defer jobsServer.Close()

	leaseStore := leases.NewStore()
	baseNow := time.Date(2026, 6, 5, 20, 0, 0, 0, time.UTC)
	var leaseNow atomic.Int64
	leaseNow.Store(baseNow.UnixNano())
	leaseStore.SetClock(func() time.Time {
		return time.Unix(0, leaseNow.Load()).UTC()
	})
	if _, err := leaseStore.RegisterResource(contracts.RegisterResourceRequest{Selector: "gpu", Status: contracts.ResourceAvailable}); err != nil {
		t.Fatalf("register resource: %v", err)
	}
	var leaseHeartbeats atomic.Int32
	var releaseAttempts atomic.Int32
	var observedMu sync.Mutex
	heartbeatLeaseID := ""
	releaseBody := map[string]any{}
	leasesHandler := leases.NewHandler(leaseStore)
	leasesServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/heartbeat") {
			leaseHeartbeats.Add(1)
			leaseNow.Store(baseNow.Add(61 * time.Second).UnixNano())
			observedMu.Lock()
			heartbeatLeaseID = leaseIDFromRunnerTestPath(r.URL.Path)
			observedMu.Unlock()
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/release") {
			releaseAttempts.Add(1)
			leaseNow.Store(baseNow.Add(61 * time.Second).UnixNano())
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read release body: %v", err)
			}
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("decode release body: %v", err)
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			observedMu.Lock()
			releaseBody = body
			observedMu.Unlock()
		}
		leasesHandler.ServeHTTP(w, r)
	}))
	defer leasesServer.Close()

	releaseProvider := make(chan struct{})
	var releaseProviderOnce sync.Once
	defer releaseProviderOnce.Do(func() { close(releaseProvider) })
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/provider/capabilities/cap_lease_expiring/invoke" {
			t.Fatalf("unexpected provider request %s %s", r.Method, r.URL.Path)
		}
		<-releaseProvider
	}))
	defer providerServer.Close()

	route := contracts.CapabilityRoute{
		CapabilityID:       "cap_lease_expiring",
		ServiceID:          "svc_lease_expiring_provider",
		ProviderEndpoint:   providerServer.URL,
		ProviderHealthPath: "/v1/provider/health",
		ProviderInvokePath: "/v1/provider/capabilities/cap_lease_expiring/invoke",
		ServiceStartMode:   "manual",
		ResourceHints:      []contracts.ResourceHint{{Selector: "gpu", Required: true}},
	}
	created, _, err := jobStore.Create(contracts.CreateJobRequest{
		RequesterID:  "sub_agent",
		CapabilityID: "cap_lease_expiring",
		InputSummary: map[string]any{"prompt_present": true},
		Metadata: map[string]any{"execution_plan": map[string]any{
			"capability_id":     "cap_lease_expiring",
			"subject_id":        "sub_agent",
			"input":             map[string]any{"prompt": "red mug"},
			"route":             route,
			"resource_selector": "gpu",
			"timeout_seconds":   30,
		}},
	}, "create-lease-expiring-provider-job")
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	runner := New(Config{
		WorkerID:                  "runner_test",
		JobsURL:                   jobsServer.URL,
		LeasesURL:                 leasesServer.URL,
		ArtifactsURL:              "http://artifacts.invalid",
		ActorSubjectID:            "sub_runner_test",
		ProviderHeartbeatInterval: 10 * time.Millisecond,
		Client:                    jobsServer.Client(),
	})
	jobID, ok, err := runner.RunOnce(context.Background())
	releaseProviderOnce.Do(func() { close(releaseProvider) })
	providerServer.CloseClientConnections()
	if err == nil {
		t.Fatal("expected lease expiration error")
	}
	if !ok || jobID != created.JobID {
		t.Fatalf("run result jobID=%q ok=%v", jobID, ok)
	}
	if leaseHeartbeats.Load() == 0 {
		t.Fatal("lease heartbeat was not attempted")
	}
	failed, err := jobStore.Get(created.JobID)
	if err != nil {
		t.Fatalf("get failed job: %v", err)
	}
	if failed.State != contracts.JobFailed || failed.TerminalError == nil || failed.TerminalError.Code != "lease_expired" || failed.TerminalError.Message != "resource lease expired before completion" || !failed.TerminalError.Retryable {
		t.Fatalf("failed job = %#v", failed)
	}
	logs, _, err := jobStore.Logs(created.JobID, "", 20)
	if err != nil {
		t.Fatalf("read logs: %v", err)
	}
	lastLog := logs[len(logs)-1]
	observedMu.Lock()
	gotLeaseID := heartbeatLeaseID
	gotReleaseBody := releaseBody
	observedMu.Unlock()
	if lastLog.Level != "error" || lastLog.Message != "resource lease expired" || lastLog.Fields["lease_id"] != gotLeaseID {
		t.Fatalf("last log = %#v, leaseID=%q", lastLog, gotLeaseID)
	}
	if releaseAttempts.Load() == 0 || gotReleaseBody["reason"] != "lease expired" {
		t.Fatalf("release attempts=%d body=%#v", releaseAttempts.Load(), gotReleaseBody)
	}
	if events := leaseStore.AuditEvents(); len(events) != 0 {
		t.Fatalf("expired lease release should not create audit events: %#v", events)
	}
	metrics := runner.Metrics(context.Background())
	assertContractMetric(t, metrics.Samples, "runner_errors_total", map[string]string{"code": "lease_expired"}, 1)
}

func TestRunnerMonitorHTTPReportsHealthAndMetrics(t *testing.T) {
	jobStore := jobs.NewStore()
	jobsServer := httptest.NewServer(jobs.NewHandler(jobStore))
	defer jobsServer.Close()

	leasesServer := httptest.NewServer(leases.NewHandler(leases.NewStore()))
	defer leasesServer.Close()

	artifactStore, err := artifacts.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("artifact store: %v", err)
	}
	artifactsServer := httptest.NewServer(artifacts.NewHandler(artifactStore))
	defer artifactsServer.Close()

	r := New(Config{
		WorkerID:     "runner_test",
		JobsURL:      jobsServer.URL,
		LeasesURL:    leasesServer.URL,
		ArtifactsURL: artifactsServer.URL,
		Client:       jobsServer.Client(),
	})
	handler := NewHandler(r)

	health := requestRunnerData(t, handler, http.MethodGet, "/v1/runner/health")
	if health["status"] != "healthy" {
		t.Fatalf("health = %#v", health)
	}
	details := health["details"].(map[string]any)
	if details["worker_id"] != "runner_test" || int(details["active_jobs"].(float64)) != 0 {
		t.Fatalf("health details = %#v", details)
	}
	if len(details["dependencies"].([]any)) != 3 {
		t.Fatalf("dependencies = %#v", details["dependencies"])
	}

	metrics := requestRunnerData(t, handler, http.MethodGet, "/v1/runner/metrics")
	if metrics["component"] != "runner" {
		t.Fatalf("metrics = %#v", metrics)
	}
	assertDecodedMetric(t, metrics, "runner_dependency_reachable", map[string]string{"dependency": "jobs", "required": "true", "status": "healthy"}, 1)
	assertDecodedMetric(t, metrics, "http_requests_total", map[string]string{"method": "GET", "route_group": "/v1/runner/health", "status_class": "2xx"}, 1)
}

func TestRunnerPropagatesRequestIDToComponentRequests(t *testing.T) {
	seenRequestID := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/jobs" || r.Method != http.MethodGet {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		seenRequestID = r.Header.Get("X-Request-ID")
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"ok":    true,
			"data":  map[string]any{"items": []any{}, "next_cursor": nil},
			"links": map[string]any{},
			"meta":  map[string]any{"request_id": "req_test", "schema_version": "v1"},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	r := New(Config{
		WorkerID: "runner_test",
		JobsURL:  server.URL,
		Client:   server.Client(),
	})
	_, ok, err := r.RunOnce(observability.WithRequestID(context.Background(), "req_runner_trace"))
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if ok {
		t.Fatal("expected no queued job")
	}
	if seenRequestID != "req_runner_trace" {
		t.Fatalf("X-Request-ID = %q", seenRequestID)
	}
}

func TestRunnerChecksProviderInvokePolicy(t *testing.T) {
	jobStore := jobs.NewStore()
	jobsServer := httptest.NewServer(jobs.NewHandler(jobStore))
	defer jobsServer.Close()

	policyStore := policy.NewStore()
	if _, err := policyStore.CreateAPIKey(contracts.CreateAPIKeyRequest{SubjectID: "sub_runner", Scopes: []string{"worker"}, Token: "token_worker"}); err != nil {
		t.Fatalf("create runner key: %v", err)
	}
	policyServer := httptest.NewServer(policy.NewHandler(policyStore))
	defer policyServer.Close()

	invoked := false
	providerServer, err := provider.NewServer(providerManifest("svc_policy_provider", "cap_policy_echo"), map[string]provider.CapabilityHandler{
		"cap_policy_echo": func(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
			invoked = true
			return contracts.ProviderInvokeResponse{Output: map[string]any{"message": "ok"}}, nil
		},
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	providerHTTP := httptest.NewServer(providerServer)
	defer providerHTTP.Close()

	created := createRunnerPolicyJob(t, jobStore, providerHTTP.URL, "cap_policy_echo")
	r := New(Config{
		WorkerID:            "runner_test",
		JobsURL:             jobsServer.URL,
		ArtifactsURL:        "http://artifacts.invalid",
		PolicyURL:           policyServer.URL,
		ComponentCredential: "Bearer token_worker",
		Client:              jobsServer.Client(),
	})
	jobID, ok, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if !ok || jobID != created.JobID {
		t.Fatalf("run result jobID=%q ok=%v", jobID, ok)
	}
	if !invoked {
		t.Fatal("provider was not invoked")
	}
	completed, err := jobStore.Get(created.JobID)
	if err != nil {
		t.Fatalf("get completed job: %v", err)
	}
	if completed.State != contracts.JobSucceeded {
		t.Fatalf("job = %#v", completed)
	}
	events := policyStore.AuditEvents()
	if len(events) == 0 || events[len(events)-1].Action != "provider.invoke" || !events[len(events)-1].Allowed {
		t.Fatalf("audit events = %#v", events)
	}
}

func TestRunnerFailsJobWhenProviderInvokePolicyDenied(t *testing.T) {
	jobStore := jobs.NewStore()
	jobsServer := httptest.NewServer(jobs.NewHandler(jobStore))
	defer jobsServer.Close()

	policyStore := policy.NewStore()
	if _, err := policyStore.CreateAPIKey(contracts.CreateAPIKeyRequest{SubjectID: "sub_runner", Scopes: []string{"component"}, Token: "token_component"}); err != nil {
		t.Fatalf("create runner key: %v", err)
	}
	policyServer := httptest.NewServer(policy.NewHandler(policyStore))
	defer policyServer.Close()

	invoked := false
	providerServer, err := provider.NewServer(providerManifest("svc_policy_provider", "cap_policy_echo"), map[string]provider.CapabilityHandler{
		"cap_policy_echo": func(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
			invoked = true
			return contracts.ProviderInvokeResponse{Output: map[string]any{"message": "ok"}}, nil
		},
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	providerHTTP := httptest.NewServer(providerServer)
	defer providerHTTP.Close()

	created := createRunnerPolicyJob(t, jobStore, providerHTTP.URL, "cap_policy_echo")
	r := New(Config{
		WorkerID:            "runner_test",
		JobsURL:             jobsServer.URL,
		ArtifactsURL:        "http://artifacts.invalid",
		PolicyURL:           policyServer.URL,
		ComponentCredential: "Bearer token_component",
		Client:              jobsServer.Client(),
	})
	jobID, ok, err := r.RunOnce(context.Background())
	if err == nil {
		t.Fatal("expected policy denial error")
	}
	if !ok || jobID != created.JobID {
		t.Fatalf("run result jobID=%q ok=%v", jobID, ok)
	}
	if invoked {
		t.Fatal("provider was invoked despite policy denial")
	}
	failed, err := jobStore.Get(created.JobID)
	if err != nil {
		t.Fatalf("get failed job: %v", err)
	}
	if failed.State != contracts.JobFailed || failed.TerminalError == nil || failed.TerminalError.Code != "policy_denied" {
		t.Fatalf("failed job = %#v", failed)
	}
}

func TestRunnerDoesNotCompleteOrUploadArtifactsAfterJobBecomesTerminal(t *testing.T) {
	jobStore := jobs.NewStore()
	jobsServer := httptest.NewServer(jobs.NewHandler(jobStore))
	defer jobsServer.Close()

	artifactStore, err := artifacts.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("artifact store: %v", err)
	}
	artifactsServer := httptest.NewServer(artifacts.NewHandler(artifactStore))
	defer artifactsServer.Close()

	var created contracts.Job
	providerServer, err := provider.NewServer(contracts.ProviderManifest{
		SchemaVersion: "v1",
		Service: contracts.Service{
			ID:           "svc_cancel_provider",
			Name:         "Cancel Provider",
			Description:  "Cancel provider.",
			Version:      "0.1.0",
			ProviderKind: "fake",
			Tags:         []string{"fake"},
		},
		Provider: contracts.Provider{Endpoint: "http://provider.invalid"},
		Capabilities: []contracts.Capability{{
			ID:            "cap_cancel_image",
			Name:          "Cancel image",
			Description:   "Cancel image.",
			ExecutionMode: "sync",
			InputSchema:   map[string]any{"type": "object"},
			OutputSchema:  map[string]any{"type": "object"},
			Examples:      []map[string]any{},
			SideEffects:   "external",
			ResourceHints: []contracts.ResourceHint{},
			ArtifactHints: []contracts.ArtifactHint{{MediaType: "text/plain", Count: "one"}},
			TimeoutHint:   "30s",
		}},
	}, map[string]provider.CapabilityHandler{
		"cap_cancel_image": func(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
			if _, err := jobStore.Fail(created.JobID, contracts.JobFailRequest{
				WorkerID: "runner_test",
				Error: contracts.ErrorObject{
					Code:      "external_terminal",
					Message:   "job became terminal",
					Retryable: false,
				},
			}); err != nil {
				t.Fatalf("fail running job: %v", err)
			}
			body := []byte("late artifact bytes")
			sum := sha256.Sum256(body)
			return contracts.ProviderInvokeResponse{
				Output: map[string]any{"artifact_count": 1},
				Artifacts: []contracts.ProviderArtifact{{
					Name:          "late-artifact.txt",
					MediaType:     "text/plain",
					ContentBase64: base64.StdEncoding.EncodeToString(body),
					Checksum:      "sha256:" + hex.EncodeToString(sum[:]),
				}},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	providerHTTP := httptest.NewServer(providerServer)
	defer providerHTTP.Close()

	route := contracts.CapabilityRoute{
		CapabilityID:       "cap_cancel_image",
		ServiceID:          "svc_cancel_provider",
		ProviderEndpoint:   providerHTTP.URL,
		ProviderHealthPath: "/v1/provider/health",
		ProviderInvokePath: "/v1/provider/capabilities/cap_cancel_image/invoke",
		ServiceStartMode:   "manual",
	}
	created, _, err = jobStore.Create(contracts.CreateJobRequest{
		RequesterID:  "sub_agent",
		CapabilityID: "cap_cancel_image",
		InputSummary: map[string]any{"prompt_present": true},
		Metadata: map[string]any{"execution_plan": map[string]any{
			"capability_id":   "cap_cancel_image",
			"subject_id":      "sub_agent",
			"input":           map[string]any{"prompt": "red mug"},
			"route":           route,
			"timeout_seconds": 30,
		}},
	}, "create-cancel-job")
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	r := New(Config{
		WorkerID:     "runner_test",
		JobsURL:      jobsServer.URL,
		ArtifactsURL: artifactsServer.URL,
		Client:       jobsServer.Client(),
	})
	jobID, ok, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if !ok || jobID != created.JobID {
		t.Fatalf("run result jobID=%q ok=%v", jobID, ok)
	}
	terminal, err := jobStore.Get(created.JobID)
	if err != nil {
		t.Fatalf("get terminal job: %v", err)
	}
	if terminal.State != contracts.JobFailed || terminal.TerminalError == nil || terminal.TerminalError.Code != "external_terminal" || len(terminal.ArtifactRefs) != 0 {
		t.Fatalf("terminal job = %#v", terminal)
	}
	if artifacts := artifactStore.ListArtifacts(artifacts.ListFilter{ProducerRef: created.JobID}); len(artifacts) != 0 {
		t.Fatalf("late artifacts were uploaded: %#v", artifacts)
	}
}

func TestRunnerWaitsForNodeManagedServiceStartup(t *testing.T) {
	gets := 0
	starts := 0
	nodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/node/services/svc_remote_provider":
			gets++
			status := "stopped"
			if starts > 0 {
				status = "starting"
			}
			if gets >= 3 {
				status = "running"
			}
			writeRunnerTestSuccess(w, http.StatusOK, contracts.NodeService{ServiceID: "svc_remote_provider", Status: status})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/node/services/svc_remote_provider/start":
			starts++
			if got := r.Header.Get("Idempotency-Key"); got != "runner-start-svc_remote_provider" {
				t.Fatalf("start idempotency key = %q", got)
			}
			writeRunnerTestSuccess(w, http.StatusAccepted, contracts.NodeService{ServiceID: "svc_remote_provider", Status: "starting"})
		default:
			t.Fatalf("unexpected node request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer nodeServer.Close()

	r := New(Config{
		NodeURLs:         map[string]string{"node_linux_gpu": nodeServer.URL + "/"},
		NodeStartTimeout: 250 * time.Millisecond,
		NodePollInterval: time.Millisecond,
		Client:           nodeServer.Client(),
	})
	nodeID := "node_linux_gpu"
	route := contracts.CapabilityRoute{ServiceID: "svc_remote_provider", NodeID: &nodeID, NodeManaged: true}
	if err := r.ensureNodeService(context.Background(), route); err != nil {
		t.Fatalf("ensureNodeService: %v", err)
	}
	if starts != 1 || gets < 3 {
		t.Fatalf("starts=%d gets=%d", starts, gets)
	}
}

func TestRunnerTimesOutWaitingForNodeManagedServiceStartup(t *testing.T) {
	nodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/node/services/svc_slow_provider":
			writeRunnerTestSuccess(w, http.StatusOK, contracts.NodeService{ServiceID: "svc_slow_provider", Status: "starting"})
		default:
			t.Fatalf("unexpected node request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer nodeServer.Close()

	r := New(Config{
		NodeURL:          nodeServer.URL,
		NodeStartTimeout: 5 * time.Millisecond,
		NodePollInterval: time.Millisecond,
		Client:           nodeServer.Client(),
	})
	route := contracts.CapabilityRoute{ServiceID: "svc_slow_provider", NodeManaged: true}
	if err := r.ensureNodeService(context.Background(), route); err == nil {
		t.Fatal("expected node start timeout")
	}
}

func TestRunnerRequiresConfiguredNodeURLForNodeID(t *testing.T) {
	r := New(Config{})
	nodeID := "node_linux_gpu"
	route := contracts.CapabilityRoute{ServiceID: "svc_remote_provider", NodeID: &nodeID, NodeManaged: true}
	if err := r.ensureNodeService(context.Background(), route); err == nil {
		t.Fatal("expected missing node URL error")
	}
}

func runProviderFailureBranch(t *testing.T, providerError contracts.ErrorObject, status int, wantLogMessage, wantReleaseReason string) {
	t.Helper()
	jobStore := jobs.NewStore()
	jobsServer := httptest.NewServer(jobs.NewHandler(jobStore))
	defer jobsServer.Close()

	leaseStore := leases.NewStore()
	if _, err := leaseStore.RegisterResource(contracts.RegisterResourceRequest{Selector: "gpu", Status: contracts.ResourceAvailable}); err != nil {
		t.Fatalf("register resource: %v", err)
	}
	leasesServer := httptest.NewServer(leases.NewHandler(leaseStore))
	defer leasesServer.Close()

	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/provider/capabilities/cap_failure/invoke" {
			t.Fatalf("unexpected provider request %s %s", r.Method, r.URL.Path)
		}
		writeRunnerTestError(w, status, providerError)
	}))
	defer providerServer.Close()

	route := contracts.CapabilityRoute{
		CapabilityID:       "cap_failure",
		ServiceID:          "svc_failure_provider",
		ProviderEndpoint:   providerServer.URL,
		ProviderHealthPath: "/v1/provider/health",
		ProviderInvokePath: "/v1/provider/capabilities/cap_failure/invoke",
		ServiceStartMode:   "manual",
		ResourceHints:      []contracts.ResourceHint{{Selector: "gpu", Required: true}},
	}
	created, _, err := jobStore.Create(contracts.CreateJobRequest{
		RequesterID:  "sub_agent",
		CapabilityID: "cap_failure",
		InputSummary: map[string]any{"prompt_present": true},
		Metadata: map[string]any{"execution_plan": map[string]any{
			"capability_id":     "cap_failure",
			"subject_id":        "sub_agent",
			"input":             map[string]any{"prompt": "red mug"},
			"route":             route,
			"resource_selector": "gpu",
			"timeout_seconds":   30,
		}},
	}, "create-provider-failure-job")
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	runner := New(Config{
		WorkerID:       "runner_test",
		JobsURL:        jobsServer.URL,
		LeasesURL:      leasesServer.URL,
		ArtifactsURL:   "http://artifacts.invalid",
		ActorSubjectID: "sub_runner_test",
		Client:         jobsServer.Client(),
	})
	jobID, ok, err := runner.RunOnce(context.Background())
	if err == nil {
		t.Fatal("expected provider failure error")
	}
	if !ok || jobID != created.JobID {
		t.Fatalf("run result jobID=%q ok=%v", jobID, ok)
	}
	failed, err := jobStore.Get(created.JobID)
	if err != nil {
		t.Fatalf("get failed job: %v", err)
	}
	if failed.State != contracts.JobFailed || failed.TerminalError == nil {
		t.Fatalf("failed job = %#v", failed)
	}
	if *failed.TerminalError != providerError {
		t.Fatalf("terminal error = %#v want %#v", *failed.TerminalError, providerError)
	}
	logs, _, err := jobStore.Logs(created.JobID, "", 20)
	if err != nil {
		t.Fatalf("read logs: %v", err)
	}
	if len(logs) < 3 {
		t.Fatalf("logs = %#v", logs)
	}
	lastLog := logs[len(logs)-1]
	if lastLog.Level != "error" || lastLog.Message != wantLogMessage || lastLog.Fields["code"] != providerError.Code {
		t.Fatalf("last log = %#v", lastLog)
	}
	events := leaseStore.AuditEvents()
	if len(events) != 1 {
		t.Fatalf("lease audit events = %#v", events)
	}
	if events[0].ReleaseReason != wantReleaseReason || events[0].ActorSubjectID != "sub_runner_test" || events[0].HolderID != created.JobID {
		t.Fatalf("lease audit event = %#v", events[0])
	}
}

func createRunnerPolicyJob(t *testing.T, store *jobs.Store, providerEndpoint, capabilityID string) contracts.Job {
	t.Helper()
	route := contracts.CapabilityRoute{
		CapabilityID:       capabilityID,
		ServiceID:          "svc_policy_provider",
		ProviderEndpoint:   providerEndpoint,
		ProviderHealthPath: "/v1/provider/health",
		ProviderInvokePath: "/v1/provider/capabilities/" + capabilityID + "/invoke",
		ServiceStartMode:   "manual",
	}
	created, _, err := store.Create(contracts.CreateJobRequest{
		RequesterID:  "sub_agent",
		CapabilityID: capabilityID,
		InputSummary: map[string]any{"message_present": true},
		Metadata: map[string]any{"execution_plan": map[string]any{
			"capability_id":   capabilityID,
			"subject_id":      "sub_agent",
			"input":           map[string]any{"message": "hello"},
			"route":           route,
			"timeout_seconds": 30,
		}},
	}, "create-policy-job-"+capabilityID)
	if err != nil {
		t.Fatalf("create policy job: %v", err)
	}
	return created
}

func providerManifest(serviceID, capabilityID string) contracts.ProviderManifest {
	return contracts.ProviderManifest{
		SchemaVersion: "v1",
		Service: contracts.Service{
			ID:           serviceID,
			Name:         "Policy Provider",
			Description:  "Policy provider.",
			Version:      "0.1.0",
			ProviderKind: "fake",
			Tags:         []string{"fake"},
		},
		Provider: contracts.Provider{Endpoint: "http://provider.invalid"},
		Capabilities: []contracts.Capability{{
			ID:            capabilityID,
			Name:          "Policy echo",
			Description:   "Policy echo.",
			ExecutionMode: "sync",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []any{"message"},
				"properties": map[string]any{
					"message": map[string]any{"type": "string"},
				},
			},
			OutputSchema: map[string]any{
				"type":     "object",
				"required": []any{"message"},
				"properties": map[string]any{
					"message": map[string]any{"type": "string"},
				},
			},
			Examples:      []map[string]any{},
			SideEffects:   "none",
			ResourceHints: []contracts.ResourceHint{},
			ArtifactHints: []contracts.ArtifactHint{},
			TimeoutHint:   "30s",
		}},
	}
}

func newFakeProvider(t *testing.T) *provider.Server {
	t.Helper()
	server, err := provider.NewServer(contracts.ProviderManifest{
		SchemaVersion: "v1",
		Service: contracts.Service{
			ID:           "svc_fake_provider",
			Name:         "Fake Provider",
			Description:  "Fake provider.",
			Version:      "0.1.0",
			ProviderKind: "fake",
			Tags:         []string{"fake"},
		},
		Provider: contracts.Provider{Endpoint: "http://provider.invalid"},
		Capabilities: []contracts.Capability{{
			ID:            "cap_fake_image",
			Name:          "Fake image",
			Description:   "Fake image.",
			ExecutionMode: "sync",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []any{"prompt"},
				"properties": map[string]any{
					"prompt": map[string]any{"type": "string"},
				},
			},
			OutputSchema: map[string]any{
				"type":     "object",
				"required": []any{"artifact_count"},
				"properties": map[string]any{
					"artifact_count": map[string]any{"type": "integer"},
				},
			},
			Examples:      []map[string]any{},
			SideEffects:   "external",
			ResourceHints: []contracts.ResourceHint{{Selector: "gpu", Required: true}},
			ArtifactHints: []contracts.ArtifactHint{{MediaType: "text/plain", Count: "one"}},
			TimeoutHint:   "30s",
		}},
	}, map[string]provider.CapabilityHandler{
		"cap_fake_image": func(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
			body := []byte("artifact bytes")
			sum := sha256.Sum256(body)
			return contracts.ProviderInvokeResponse{
				Output: map[string]any{"artifact_count": 1},
				Artifacts: []contracts.ProviderArtifact{{
					Name:          "fake-image.txt",
					MediaType:     "text/plain",
					ContentBase64: base64.StdEncoding.EncodeToString(body),
					Checksum:      "sha256:" + hex.EncodeToString(sum[:]),
				}},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	return server
}

func writeRunnerTestSuccess(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(contracts.SuccessEnvelope{OK: true, Data: data, Links: map[string]any{}, Meta: map[string]string{"request_id": "req_test", "schema_version": "v1"}})
}

func writeRunnerTestError(w http.ResponseWriter, status int, errObj contracts.ErrorObject) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(contracts.ErrorEnvelope{OK: false, Error: errObj, Links: map[string]any{}, Meta: map[string]string{"request_id": "req_test", "schema_version": "v1"}})
}

func leaseIDFromRunnerTestPath(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 4 && parts[0] == "v1" && parts[1] == "leases" {
		return parts[2]
	}
	return ""
}

func requestRunnerData(t *testing.T, handler http.Handler, method, path string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s %s status=%d body=%s", method, path, rec.Code, rec.Body.String())
	}
	var envelope map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !envelope["ok"].(bool) {
		t.Fatalf("envelope = %#v", envelope)
	}
	return envelope["data"].(map[string]any)
}

func assertContractMetric(t *testing.T, samples []contracts.MetricSample, name string, labels map[string]string, want float64) {
	t.Helper()
	for _, sample := range samples {
		if sample.Name != name || !contractLabelsMatch(sample.Labels, labels) {
			continue
		}
		if sample.Value != want {
			t.Fatalf("%s value=%v want=%v labels=%#v", name, sample.Value, want, labels)
		}
		return
	}
	t.Fatalf("missing metric %s labels=%#v in %#v", name, labels, samples)
}

func assertContractMetricExists(t *testing.T, samples []contracts.MetricSample, name string, labels map[string]string) {
	t.Helper()
	for _, sample := range samples {
		if sample.Name == name && contractLabelsMatch(sample.Labels, labels) {
			return
		}
	}
	t.Fatalf("missing metric %s labels=%#v in %#v", name, labels, samples)
}

func contractLabelsMatch(actual, want map[string]string) bool {
	for key, value := range want {
		if actual[key] != value {
			return false
		}
	}
	return true
}

func assertDecodedMetric(t *testing.T, data map[string]any, name string, labels map[string]string, want float64) {
	t.Helper()
	samples := data["samples"].([]any)
	for _, rawSample := range samples {
		sample, ok := rawSample.(map[string]any)
		if !ok || sample["name"] != name {
			continue
		}
		rawLabels, _ := sample["labels"].(map[string]any)
		matched := true
		for key, value := range labels {
			if rawLabels[key] != value {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		if sample["value"].(float64) != want {
			t.Fatalf("%s value=%v want=%v labels=%#v", name, sample["value"], want, labels)
		}
		return
	}
	t.Fatalf("missing metric %s labels=%#v in %#v", name, labels, samples)
}
