package runner

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		WorkerID:     "runner_test",
		JobsURL:      jobsServer.URL,
		LeasesURL:    leasesServer.URL,
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

func TestRunnerDoesNotCompleteOrUploadArtifactsAfterRunningCancel(t *testing.T) {
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
			if _, err := jobStore.Cancel(created.JobID, contracts.CancelRequest{Reason: "canceled while running"}); err != nil {
				t.Fatalf("cancel running job: %v", err)
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
	canceled, err := jobStore.Get(created.JobID)
	if err != nil {
		t.Fatalf("get canceled job: %v", err)
	}
	if canceled.State != contracts.JobCanceled || canceled.StatusMessage != "canceled while running" || len(canceled.ArtifactRefs) != 0 {
		t.Fatalf("canceled job = %#v", canceled)
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
