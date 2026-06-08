package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"pacp/internal/components/artifacts"
	"pacp/internal/components/catalog"
	"pacp/internal/components/gateway"
	"pacp/internal/components/jobs"
	"pacp/internal/components/leases"
	"pacp/internal/components/policy"
	"pacp/internal/contracts"
	"pacp/internal/observability"
	"pacp/internal/provider"
	"pacp/internal/routeauth"
	"pacp/internal/runner"
	"pacp/internal/transportauth"
)

type devConfig struct {
	CatalogAddr    string
	JobsAddr       string
	LeasesAddr     string
	ArtifactsAddr  string
	PolicyAddr     string
	GatewayAddr    string
	ProviderAddr   string
	ArtifactRoot   string
	StateDir       string
	AgentToken     string
	ComponentToken string
	WorkerToken    string
	WorkerID       string
	PollInterval   time.Duration
	DisableRunner  bool
}

type devStores struct {
	catalogStore  *catalog.Store
	jobStore      *jobs.Store
	leaseStore    *leases.Store
	artifactStore *artifacts.Store
	policyStore   *policy.Store
}

func main() {
	cfg := devConfig{}
	flag.StringVar(&cfg.CatalogAddr, "catalog-addr", "localhost:18081", "catalog listen address")
	flag.StringVar(&cfg.JobsAddr, "jobs-addr", "localhost:18082", "jobs listen address")
	flag.StringVar(&cfg.LeasesAddr, "leases-addr", "localhost:18083", "lease listen address")
	flag.StringVar(&cfg.ArtifactsAddr, "artifacts-addr", "localhost:18084", "artifact listen address")
	flag.StringVar(&cfg.PolicyAddr, "policy-addr", "localhost:18085", "policy listen address")
	flag.StringVar(&cfg.GatewayAddr, "gateway-addr", "localhost:18086", "gateway listen address")
	flag.StringVar(&cfg.ProviderAddr, "provider-addr", "localhost:18088", "fake provider listen address")
	flag.StringVar(&cfg.ArtifactRoot, "artifact-root", "/tmp/pacp-dev-artifacts", "artifact storage root")
	flag.StringVar(&cfg.StateDir, "state-dir", "", "optional directory for durable dev stack state")
	flag.StringVar(&cfg.AgentToken, "agent-token", "token_agent", "local agent token")
	flag.StringVar(&cfg.ComponentToken, "component-token", "token_component", "local component token")
	flag.StringVar(&cfg.WorkerToken, "worker-token", "token_worker", "local worker token")
	flag.StringVar(&cfg.WorkerID, "worker-id", "runner_dev", "local runner worker id")
	flag.DurationVar(&cfg.PollInterval, "poll", time.Second, "runner poll interval")
	flag.BoolVar(&cfg.DisableRunner, "disable-runner", false, "start services without the local runner")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runDevStack(ctx, cfg); err != nil {
		log.Fatal(err)
	}
}

func runDevStack(ctx context.Context, cfg devConfig) error {
	catalogURL := endpointForAddr(cfg.CatalogAddr)
	jobsURL := endpointForAddr(cfg.JobsAddr)
	leasesURL := endpointForAddr(cfg.LeasesAddr)
	artifactsURL := endpointForAddr(cfg.ArtifactsAddr)
	policyURL := endpointForAddr(cfg.PolicyAddr)
	gatewayURL := endpointForAddr(cfg.GatewayAddr)
	providerURL := endpointForAddr(cfg.ProviderAddr)

	manifest := devManifest(providerURL)
	stores, err := newDevStores(cfg, manifest)
	if err != nil {
		return err
	}

	providerServer, err := provider.NewServer(manifest, map[string]provider.CapabilityHandler{
		"cap_dev_echo":     devEchoHandler,
		"cap_dev_artifact": devArtifactHandler,
	})
	if err != nil {
		return err
	}
	gatewayHandler, err := gateway.NewPersistentHandler(gateway.Config{
		CatalogURL:        catalogURL,
		PolicyURL:         policyURL,
		JobsURL:           jobsURL,
		LeasesURL:         leasesURL,
		ArtifactsURL:      artifactsURL,
		GatewayCredential: authorizationHeader(cfg.ComponentToken),
	}, statePath(cfg, "gateway-idempotency"))
	if err != nil {
		return err
	}

	servers := []*http.Server{}
	handlers := devComponentHandlers(stores, policyURL)
	for _, svc := range []struct {
		name    string
		addr    string
		handler http.Handler
	}{
		{name: "catalog", addr: cfg.CatalogAddr, handler: handlers["catalog"]},
		{name: "jobs", addr: cfg.JobsAddr, handler: handlers["jobs"]},
		{name: "leases", addr: cfg.LeasesAddr, handler: handlers["leases"]},
		{name: "artifacts", addr: cfg.ArtifactsAddr, handler: handlers["artifacts"]},
		{name: "policy", addr: cfg.PolicyAddr, handler: handlers["policy"]},
		{name: "provider", addr: cfg.ProviderAddr, handler: providerServer},
		{name: "gateway", addr: cfg.GatewayAddr, handler: gatewayHandler},
	} {
		server, err := serve(ctx, svc.name, svc.addr, svc.handler)
		if err != nil {
			shutdownServers(context.Background(), servers)
			return err
		}
		servers = append(servers, server)
	}

	if !cfg.DisableRunner {
		r := runner.New(runner.Config{
			WorkerID:            cfg.WorkerID,
			JobsURL:             jobsURL,
			LeasesURL:           leasesURL,
			ArtifactsURL:        artifactsURL,
			PolicyURL:           policyURL,
			ComponentCredential: authorizationHeader(cfg.WorkerToken),
			PolicyCredential:    authorizationHeader(cfg.ComponentToken),
			WorkerSubjectID:     "sub_runner_local",
			ActorSubjectID:      "sub_runner_local",
		})
		go runnerLoop(ctx, r, cfg.PollInterval, cfg.WorkerToken)
	}

	log.Printf("dev stack ready gateway=%s state_dir=%s tools=cap_dev_echo,cap_dev_artifact", gatewayURL, cfg.StateDir)
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownServers(shutdownCtx, servers)
	return nil
}

func devComponentHandlers(stores devStores, policyURL string) map[string]http.Handler {
	return map[string]http.Handler{
		"catalog": transportauth.RequireVerifiedScopes(catalog.NewHandler(stores.catalogStore), transportauth.ScopeConfig{
			PolicyURL: policyURL,
			Rules:     routeauth.CatalogScopeRules(),
		}),
		"jobs": transportauth.RequireVerifiedScopes(jobs.NewHandler(stores.jobStore), transportauth.ScopeConfig{
			PolicyURL: policyURL,
			Rules:     routeauth.JobScopeRules(),
		}),
		"leases": transportauth.RequireVerifiedScopes(leases.NewHandler(stores.leaseStore), transportauth.ScopeConfig{
			PolicyURL: policyURL,
			Rules:     routeauth.LeaseScopeRules(),
		}),
		"artifacts": transportauth.RequireVerifiedScopes(artifacts.NewHandler(stores.artifactStore), transportauth.ScopeConfig{
			PolicyURL: policyURL,
			Rules:     routeauth.ArtifactScopeRules(),
		}),
		"policy": policy.NewHandler(stores.policyStore),
	}
}

func newDevStores(cfg devConfig, manifest contracts.ProviderManifest) (devStores, error) {
	durable := cfg.StateDir != ""
	if durable {
		if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
			return devStores{}, err
		}
	}

	catalogStore, err := newCatalogStore(cfg)
	if err != nil {
		return devStores{}, fmt.Errorf("create catalog store: %w", err)
	}
	jobStore, err := newJobStore(cfg)
	if err != nil {
		return devStores{}, fmt.Errorf("create job store: %w", err)
	}
	leaseStore, err := newLeaseStore(cfg)
	if err != nil {
		return devStores{}, fmt.Errorf("create lease store: %w", err)
	}
	artifactStore, err := newArtifactStore(cfg)
	if err != nil {
		return devStores{}, fmt.Errorf("create artifact store: %w", err)
	}
	policyStore, err := newPolicyStore(cfg)
	if err != nil {
		return devStores{}, fmt.Errorf("create policy store: %w", err)
	}

	if err := registerDevManifest(catalogStore, manifest, durable); err != nil {
		return devStores{}, fmt.Errorf("seed catalog: %w", err)
	}
	if err := registerDevResource(leaseStore, durable); err != nil {
		return devStores{}, fmt.Errorf("seed resource: %w", err)
	}
	for _, req := range []contracts.CreateAPIKeyRequest{
		{SubjectID: "sub_agent_local", Scopes: []string{"agent"}, Token: cfg.AgentToken},
		{SubjectID: "sub_gateway_local", Scopes: []string{"component"}, Token: cfg.ComponentToken},
		{SubjectID: "sub_runner_local", Scopes: []string{"worker"}, Token: cfg.WorkerToken},
	} {
		if err := createDevAPIKey(policyStore, durable, req); err != nil {
			return devStores{}, fmt.Errorf("seed api key %s: %w", req.SubjectID, err)
		}
	}

	return devStores{
		catalogStore:  catalogStore,
		jobStore:      jobStore,
		leaseStore:    leaseStore,
		artifactStore: artifactStore,
		policyStore:   policyStore,
	}, nil
}

func newCatalogStore(cfg devConfig) (*catalog.Store, error) {
	if path := statePath(cfg, "catalog"); path != "" {
		return catalog.NewPersistentStore(path)
	}
	return catalog.NewStore(), nil
}

func newJobStore(cfg devConfig) (*jobs.Store, error) {
	if path := statePath(cfg, "jobs"); path != "" {
		return jobs.NewPersistentStore(path)
	}
	return jobs.NewStore(), nil
}

func newLeaseStore(cfg devConfig) (*leases.Store, error) {
	if path := statePath(cfg, "leases"); path != "" {
		return leases.NewPersistentStore(path)
	}
	return leases.NewStore(), nil
}

func newArtifactStore(cfg devConfig) (*artifacts.Store, error) {
	if path := statePath(cfg, "artifacts"); path != "" {
		return artifacts.NewPersistentStore(cfg.ArtifactRoot, path)
	}
	return artifacts.NewStore(cfg.ArtifactRoot)
}

func newPolicyStore(cfg devConfig) (*policy.Store, error) {
	if path := statePath(cfg, "policy"); path != "" {
		return policy.NewPersistentStore(path)
	}
	return policy.NewStore(), nil
}

func statePath(cfg devConfig, name string) string {
	if cfg.StateDir == "" {
		return ""
	}
	return filepath.Join(cfg.StateDir, name+".json")
}

func registerDevManifest(store *catalog.Store, manifest contracts.ProviderManifest, durable bool) error {
	_, err := store.RegisterManifest(manifest)
	if err == nil {
		return nil
	}
	if !durable || !errors.Is(err, catalog.ErrDuplicateService) {
		return err
	}
	for _, capability := range manifest.Capabilities {
		if _, ok := store.GetCapability(capability.ID); !ok {
			return err
		}
	}
	return nil
}

func registerDevResource(store *leases.Store, durable bool) error {
	req := contracts.RegisterResourceRequest{
		ResourceID:  "res_dev_gpu",
		Selector:    "gpu",
		DisplayName: "Local development GPU",
		Status:      contracts.ResourceAvailable,
		Tags:        []string{"gpu", "dev"},
	}
	_, err := store.RegisterResource(req)
	if err == nil {
		return nil
	}
	if !durable || !errors.Is(err, leases.ErrResourceConflict) {
		return err
	}
	resource, getErr := store.GetResource(req.ResourceID)
	if getErr == nil && resource.Selector == req.Selector && resource.Status == req.Status {
		return nil
	}
	return err
}

func createDevAPIKey(store *policy.Store, durable bool, req contracts.CreateAPIKeyRequest) error {
	_, err := store.CreateAPIKey(req)
	if err == nil {
		return nil
	}
	if !durable || !errors.Is(err, policy.ErrConflict) {
		return err
	}
	verification, verifyErr := store.VerifyCredential(contracts.VerifyCredentialRequest{Credential: authorizationHeader(req.Token)})
	if verifyErr == nil && verification.Valid && verification.SubjectID != nil && *verification.SubjectID == req.SubjectID && hasScopes(verification.Scopes, req.Scopes) {
		return nil
	}
	return err
}

func hasScopes(actual, required []string) bool {
	seen := map[string]bool{}
	for _, scope := range actual {
		seen[scope] = true
	}
	for _, scope := range required {
		if !seen[scope] {
			return false
		}
	}
	return true
}

func serve(ctx context.Context, name, addr string, handler http.Handler) (*http.Server, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	server := &http.Server{Handler: handler}
	go func() {
		log.Printf("serving %s addr=%s", name, addr)
		err := server.Serve(listener)
		if err != nil && err != http.ErrServerClosed && ctx.Err() == nil {
			log.Printf("%s server error: %v", name, err)
		}
	}()
	return server, nil
}

func shutdownServers(ctx context.Context, servers []*http.Server) {
	for i := len(servers) - 1; i >= 0; i-- {
		if err := servers[i].Shutdown(ctx); err != nil {
			log.Printf("shutdown server: %v", err)
		}
	}
}

func runnerLoop(ctx context.Context, r *runner.Runner, interval time.Duration, redactionValues ...string) {
	if interval <= 0 {
		interval = time.Second
	}
	logger := observability.NewStructuredLogger(os.Stderr, "runner", observability.WithRedactionValues(redactionValues...))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		runCtx := observability.WithRequestID(ctx, observability.NewRequestID("req_runner"))
		jobID, ok, err := r.RunOnce(runCtx)
		if err != nil {
			logger.Error(runCtx, "runner iteration failed", err, observability.Field("job_id", jobID))
		} else if ok {
			logger.Info(runCtx, "processed job", observability.Field("job_id", jobID))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func devEchoHandler(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
	return contracts.ProviderInvokeResponse{Output: map[string]any{"message": req.Input["message"]}}, nil
}

func devArtifactHandler(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
	body := []byte("dev artifact bytes")
	sum := sha256.Sum256(body)
	checksum := "sha256:" + hex.EncodeToString(sum[:])
	return contracts.ProviderInvokeResponse{
		Output: map[string]any{"artifact_count": 1},
		Artifacts: []contracts.ProviderArtifact{{
			Name:          "dev-artifact.txt",
			MediaType:     "text/plain",
			ContentBase64: base64.StdEncoding.EncodeToString(body),
			Checksum:      checksum,
		}},
	}, nil
}

func devManifest(endpoint string) contracts.ProviderManifest {
	return contracts.ProviderManifest{
		SchemaVersion: "v1",
		Service: contracts.Service{
			ID:           "svc_dev_provider",
			Name:         "Development Provider",
			Description:  "Local provider for development smoke flows.",
			Version:      "0.1.0",
			ProviderKind: "development",
			Tags:         []string{"development", "local"},
		},
		Provider: contracts.Provider{Endpoint: endpoint, HealthPath: "/v1/provider/health"},
		Capabilities: []contracts.Capability{
			{
				ID:            "cap_dev_echo",
				Name:          "Echo",
				Description:   "Echo a message through the gateway and provider SDK.",
				Tags:          []string{"development", "sync"},
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
				Examples:      []map[string]any{{"message": "hello"}},
				SideEffects:   "none",
				ResourceHints: []contracts.ResourceHint{},
				ArtifactHints: []contracts.ArtifactHint{},
				TimeoutHint:   "30s",
			},
			{
				ID:            "cap_dev_artifact",
				Name:          "Artifact job",
				Description:   "Create a deterministic text artifact through the async runner path.",
				Tags:          []string{"development", "async", "artifact"},
				ExecutionMode: "async",
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
				Examples:      []map[string]any{{"prompt": "red mug"}},
				SideEffects:   "external",
				ResourceHints: []contracts.ResourceHint{{Selector: "gpu", Required: true, Quantity: 1}},
				ArtifactHints: []contracts.ArtifactHint{{MediaType: "text/plain", Count: "one"}},
				TimeoutHint:   "30s",
			},
		},
	}
}

func endpointForAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		addr = "localhost" + addr
	}
	return "http://" + addr
}

func authorizationHeader(token string) string {
	if strings.HasPrefix(token, "Bearer ") {
		return token
	}
	return "Bearer " + token
}
