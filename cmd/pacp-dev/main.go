package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
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
	"pacp/internal/provider"
	"pacp/internal/runner"
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
	AgentToken     string
	ComponentToken string
	WorkerToken    string
	WorkerID       string
	PollInterval   time.Duration
	DisableRunner  bool
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

	catalogStore := catalog.NewStore()
	manifest := devManifest(providerURL)
	if _, err := catalogStore.RegisterManifest(manifest); err != nil {
		return err
	}

	jobStore := jobs.NewStore()
	leaseStore := leases.NewStore()
	if _, err := leaseStore.RegisterResource(contracts.RegisterResourceRequest{
		ResourceID:  "res_dev_gpu",
		Selector:    "gpu",
		DisplayName: "Local development GPU",
		Status:      contracts.ResourceAvailable,
		Tags:        []string{"gpu", "dev"},
	}); err != nil {
		return err
	}
	artifactStore, err := artifacts.NewStore(cfg.ArtifactRoot)
	if err != nil {
		return err
	}
	policyStore := policy.NewStore()
	if _, err := policyStore.CreateAPIKey(contracts.CreateAPIKeyRequest{SubjectID: "sub_agent_local", Scopes: []string{"agent"}, Token: cfg.AgentToken}); err != nil {
		return err
	}
	if _, err := policyStore.CreateAPIKey(contracts.CreateAPIKeyRequest{SubjectID: "sub_gateway_local", Scopes: []string{"component"}, Token: cfg.ComponentToken}); err != nil {
		return err
	}
	if _, err := policyStore.CreateAPIKey(contracts.CreateAPIKeyRequest{SubjectID: "sub_runner_local", Scopes: []string{"worker"}, Token: cfg.WorkerToken}); err != nil {
		return err
	}

	providerServer, err := provider.NewServer(manifest, map[string]provider.CapabilityHandler{
		"cap_dev_echo":     devEchoHandler,
		"cap_dev_artifact": devArtifactHandler,
	})
	if err != nil {
		return err
	}

	servers := []*http.Server{}
	for _, svc := range []struct {
		name    string
		addr    string
		handler http.Handler
	}{
		{name: "catalog", addr: cfg.CatalogAddr, handler: catalog.NewHandler(catalogStore)},
		{name: "jobs", addr: cfg.JobsAddr, handler: jobs.NewHandler(jobStore)},
		{name: "leases", addr: cfg.LeasesAddr, handler: leases.NewHandler(leaseStore)},
		{name: "artifacts", addr: cfg.ArtifactsAddr, handler: artifacts.NewHandler(artifactStore)},
		{name: "policy", addr: cfg.PolicyAddr, handler: policy.NewHandler(policyStore)},
		{name: "provider", addr: cfg.ProviderAddr, handler: providerServer},
		{name: "gateway", addr: cfg.GatewayAddr, handler: gateway.NewHandler(gateway.Config{
			CatalogURL:        catalogURL,
			PolicyURL:         policyURL,
			JobsURL:           jobsURL,
			ArtifactsURL:      artifactsURL,
			GatewayCredential: authorizationHeader(cfg.ComponentToken),
		})},
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
			ComponentCredential: authorizationHeader(cfg.WorkerToken),
		})
		go runnerLoop(ctx, r, cfg.PollInterval)
	}

	log.Printf("dev stack ready gateway=%s tools=cap_dev_echo,cap_dev_artifact", gatewayURL)
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownServers(shutdownCtx, servers)
	return nil
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

func runnerLoop(ctx context.Context, r *runner.Runner, interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		jobID, ok, err := r.RunOnce(ctx)
		if err != nil {
			log.Printf("runner error job_id=%s err=%v", jobID, err)
		} else if ok {
			log.Printf("runner processed job_id=%s", jobID)
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
