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
	"pacp/internal/contracts"
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
		NodeURL:          nodeServer.URL,
		NodeStartTimeout: 250 * time.Millisecond,
		NodePollInterval: time.Millisecond,
		Client:           nodeServer.Client(),
	})
	if err := r.ensureNodeService(context.Background(), "svc_remote_provider"); err != nil {
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
	if err := r.ensureNodeService(context.Background(), "svc_slow_provider"); err == nil {
		t.Fatal("expected node start timeout")
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
