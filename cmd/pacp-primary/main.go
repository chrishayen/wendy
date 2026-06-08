package main

import (
	"context"
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
	"pacp/internal/observability"
	"pacp/internal/runner"
	"pacp/internal/transportauth"
)

type primaryConfig struct {
	CatalogAddr       string
	JobsAddr          string
	LeasesAddr        string
	ArtifactsAddr     string
	PolicyAddr        string
	GatewayAddr       string
	ArtifactRoot      string
	StateDir          string
	ManifestPath      string
	ResourcesPath     string
	PolicySeedPath    string
	ComponentToken    string
	GatewayCredential string
	RunnerCredential  string
	RunnerSubjectID   string
	RunnerActorID     string
	WorkerID          string
	NodeURL           string
	NodeURLsRaw       string
	NodeStartTimeout  time.Duration
	NodeStartPoll     time.Duration
	PollInterval      time.Duration
	DisableRunner     bool
	ready             chan primaryEndpoints
}

type primaryStores struct {
	catalogStore  *catalog.Store
	jobStore      *jobs.Store
	leaseStore    *leases.Store
	artifactStore *artifacts.Store
	policyStore   *policy.Store
}

type primaryEndpoints struct {
	CatalogURL   string
	JobsURL      string
	LeasesURL    string
	ArtifactsURL string
	PolicyURL    string
	GatewayURL   string
}

type boundService struct {
	name     string
	listener net.Listener
	url      string
}

func main() {
	cfg := primaryConfig{}
	flag.StringVar(&cfg.CatalogAddr, "catalog-addr", "localhost:18081", "catalog listen address")
	flag.StringVar(&cfg.JobsAddr, "jobs-addr", "localhost:18082", "jobs listen address")
	flag.StringVar(&cfg.LeasesAddr, "leases-addr", "localhost:18083", "lease listen address")
	flag.StringVar(&cfg.ArtifactsAddr, "artifacts-addr", "localhost:18084", "artifact listen address")
	flag.StringVar(&cfg.PolicyAddr, "policy-addr", "localhost:18085", "policy listen address")
	flag.StringVar(&cfg.GatewayAddr, "gateway-addr", "localhost:18086", "gateway listen address")
	flag.StringVar(&cfg.ArtifactRoot, "artifact-root", "/tmp/pacp-artifacts", "artifact storage root")
	flag.StringVar(&cfg.StateDir, "state-dir", "", "optional directory for durable primary state")
	flag.StringVar(&cfg.ManifestPath, "manifest", "", "optional provider manifest file or directory to load into the catalog")
	flag.StringVar(&cfg.ResourcesPath, "resources", "", "optional lease resource seed JSON file")
	flag.StringVar(&cfg.PolicySeedPath, "policy-seed", "", "optional policy seed JSON file")
	flag.StringVar(&cfg.ComponentToken, "component-token", os.Getenv("PACP_COMPONENT_TOKEN"), "optional bearer token required for component API calls")
	flag.StringVar(&cfg.GatewayCredential, "gateway-credential", componentCredentialDefault("PACP_GATEWAY_CREDENTIAL"), "component credential used by the gateway for downstream calls")
	flag.StringVar(&cfg.RunnerCredential, "runner-credential", componentCredentialDefault("PACP_RUNNER_CREDENTIAL"), "component credential used by the runner for downstream calls")
	flag.StringVar(&cfg.RunnerSubjectID, "runner-subject-id", os.Getenv("PACP_RUNNER_SUBJECT_ID"), "optional runner subject id for policy checks")
	flag.StringVar(&cfg.RunnerActorID, "runner-actor-subject-id", os.Getenv("PACP_RUNNER_ACTOR_SUBJECT_ID"), "optional runner actor subject id for lease release audit; defaults to runner subject id")
	flag.StringVar(&cfg.WorkerID, "worker-id", "runner_primary", "runner worker id")
	flag.StringVar(&cfg.NodeURL, "node-url", os.Getenv("PACP_NODE_URL"), "optional default node service base URL")
	flag.StringVar(&cfg.NodeURLsRaw, "node-urls", os.Getenv("PACP_NODE_URLS"), "optional comma-separated node_id=URL mappings")
	flag.DurationVar(&cfg.NodeStartTimeout, "node-start-timeout", 30*time.Second, "maximum time to wait for node-managed service startup")
	flag.DurationVar(&cfg.NodeStartPoll, "node-start-poll", 500*time.Millisecond, "poll interval while waiting for node-managed service startup")
	flag.DurationVar(&cfg.PollInterval, "poll", time.Second, "runner poll interval")
	flag.BoolVar(&cfg.DisableRunner, "disable-runner", false, "start primary services without the local runner")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runPrimaryStack(ctx, cfg); err != nil {
		log.Fatal(err)
	}
}

func runPrimaryStack(ctx context.Context, cfg primaryConfig) error {
	bound, endpoints, err := bindPrimaryServices(cfg)
	if err != nil {
		return err
	}
	defer closeListeners(bound)

	stores, err := newPrimaryStores(cfg)
	if err != nil {
		return err
	}
	if err := loadPrimaryInputs(cfg, stores); err != nil {
		return err
	}
	gatewayHandler, err := gateway.NewPersistentHandler(gateway.Config{
		CatalogURL:        endpoints.CatalogURL,
		PolicyURL:         endpoints.PolicyURL,
		JobsURL:           endpoints.JobsURL,
		ArtifactsURL:      endpoints.ArtifactsURL,
		GatewayCredential: authorizationHeader(cfg.GatewayCredential),
	}, statePath(cfg, "gateway-idempotency"))
	if err != nil {
		return err
	}

	componentToken := cfg.ComponentToken
	servers := []*http.Server{}
	handlers := map[string]http.Handler{
		"catalog":   transportauth.RequireBearer(catalog.NewHandler(stores.catalogStore), componentToken),
		"jobs":      transportauth.RequireBearer(jobs.NewHandler(stores.jobStore), componentToken),
		"leases":    transportauth.RequireBearer(leases.NewHandler(stores.leaseStore), componentToken),
		"artifacts": transportauth.RequireBearer(artifacts.NewHandler(stores.artifactStore), componentToken),
		"policy":    transportauth.RequireBearer(policy.NewHandler(stores.policyStore), componentToken),
		"gateway":   gatewayHandler,
	}
	for _, service := range bound {
		server := serveBound(ctx, service, handlers[service.name])
		servers = append(servers, server)
	}
	if cfg.ready != nil {
		cfg.ready <- endpoints
	}

	if !cfg.DisableRunner {
		nodeURLs, err := parseNodeURLMap(cfg.NodeURLsRaw)
		if err != nil {
			shutdownServers(context.Background(), servers)
			return err
		}
		r := runner.New(runner.Config{
			WorkerID:            cfg.WorkerID,
			JobsURL:             endpoints.JobsURL,
			LeasesURL:           endpoints.LeasesURL,
			ArtifactsURL:        endpoints.ArtifactsURL,
			PolicyURL:           endpoints.PolicyURL,
			NodeURL:             strings.TrimRight(cfg.NodeURL, "/"),
			NodeURLs:            nodeURLs,
			NodeStartTimeout:    cfg.NodeStartTimeout,
			NodePollInterval:    cfg.NodeStartPoll,
			ComponentCredential: authorizationHeader(cfg.RunnerCredential),
			WorkerSubjectID:     cfg.RunnerSubjectID,
			ActorSubjectID:      cfg.RunnerActorID,
		})
		go runnerLoop(ctx, r, cfg.PollInterval, cfg.RunnerCredential)
	}

	log.Printf("primary ready gateway=%s state_dir=%s runner_disabled=%t", endpoints.GatewayURL, cfg.StateDir, cfg.DisableRunner)
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownServers(shutdownCtx, servers)
	return nil
}

func bindPrimaryServices(cfg primaryConfig) ([]boundService, primaryEndpoints, error) {
	specs := []struct {
		name string
		addr string
	}{
		{name: "catalog", addr: cfg.CatalogAddr},
		{name: "jobs", addr: cfg.JobsAddr},
		{name: "leases", addr: cfg.LeasesAddr},
		{name: "artifacts", addr: cfg.ArtifactsAddr},
		{name: "policy", addr: cfg.PolicyAddr},
		{name: "gateway", addr: cfg.GatewayAddr},
	}
	bound := make([]boundService, 0, len(specs))
	endpoints := primaryEndpoints{}
	for _, spec := range specs {
		listener, err := net.Listen("tcp", spec.addr)
		if err != nil {
			closeListeners(bound)
			return nil, primaryEndpoints{}, err
		}
		service := boundService{name: spec.name, listener: listener, url: endpointForListener(spec.addr, listener)}
		bound = append(bound, service)
		switch spec.name {
		case "catalog":
			endpoints.CatalogURL = service.url
		case "jobs":
			endpoints.JobsURL = service.url
		case "leases":
			endpoints.LeasesURL = service.url
		case "artifacts":
			endpoints.ArtifactsURL = service.url
		case "policy":
			endpoints.PolicyURL = service.url
		case "gateway":
			endpoints.GatewayURL = service.url
		}
	}
	return bound, endpoints, nil
}

func newPrimaryStores(cfg primaryConfig) (primaryStores, error) {
	if cfg.StateDir != "" {
		if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
			return primaryStores{}, err
		}
	}
	catalogStore, err := newCatalogStore(cfg)
	if err != nil {
		return primaryStores{}, fmt.Errorf("create catalog store: %w", err)
	}
	jobStore, err := newJobStore(cfg)
	if err != nil {
		return primaryStores{}, fmt.Errorf("create job store: %w", err)
	}
	leaseStore, err := newLeaseStore(cfg)
	if err != nil {
		return primaryStores{}, fmt.Errorf("create lease store: %w", err)
	}
	artifactStore, err := newArtifactStore(cfg)
	if err != nil {
		return primaryStores{}, fmt.Errorf("create artifact store: %w", err)
	}
	policyStore, err := newPolicyStore(cfg)
	if err != nil {
		return primaryStores{}, fmt.Errorf("create policy store: %w", err)
	}
	return primaryStores{catalogStore: catalogStore, jobStore: jobStore, leaseStore: leaseStore, artifactStore: artifactStore, policyStore: policyStore}, nil
}

func newCatalogStore(cfg primaryConfig) (*catalog.Store, error) {
	if path := statePath(cfg, "catalog"); path != "" {
		return catalog.NewPersistentStore(path)
	}
	return catalog.NewStore(), nil
}

func newJobStore(cfg primaryConfig) (*jobs.Store, error) {
	if path := statePath(cfg, "jobs"); path != "" {
		return jobs.NewPersistentStore(path)
	}
	return jobs.NewStore(), nil
}

func newLeaseStore(cfg primaryConfig) (*leases.Store, error) {
	if path := statePath(cfg, "leases"); path != "" {
		return leases.NewPersistentStore(path)
	}
	return leases.NewStore(), nil
}

func newArtifactStore(cfg primaryConfig) (*artifacts.Store, error) {
	if path := statePath(cfg, "artifacts"); path != "" {
		return artifacts.NewPersistentStore(cfg.ArtifactRoot, path)
	}
	return artifacts.NewStore(cfg.ArtifactRoot)
}

func newPolicyStore(cfg primaryConfig) (*policy.Store, error) {
	if path := statePath(cfg, "policy"); path != "" {
		return policy.NewPersistentStore(path)
	}
	return policy.NewStore(), nil
}

func loadPrimaryInputs(cfg primaryConfig, stores primaryStores) error {
	if cfg.ManifestPath != "" {
		loaded, err := loadManifests(stores.catalogStore, cfg.ManifestPath, cfg.StateDir != "")
		if err != nil {
			return err
		}
		log.Printf("loaded catalog manifests count=%d path=%s", loaded, cfg.ManifestPath)
	}
	if cfg.ResourcesPath != "" {
		loaded, err := loadResources(stores.leaseStore, cfg.ResourcesPath, cfg.StateDir != "")
		if err != nil {
			return err
		}
		log.Printf("loaded lease resources count=%d path=%s", loaded, cfg.ResourcesPath)
	}
	if cfg.PolicySeedPath != "" {
		seed, err := policy.LoadSeedFile(cfg.PolicySeedPath)
		if err != nil {
			return fmt.Errorf("load policy seed: %w", err)
		}
		result, err := stores.policyStore.ApplySeed(seed)
		if err != nil {
			return fmt.Errorf("apply policy seed: %w", err)
		}
		log.Printf("applied policy seed path=%s api_keys_created=%d api_keys_skipped=%d rules_created=%d rules_skipped=%d secrets_created=%d secrets_skipped=%d", cfg.PolicySeedPath, result.APIKeysCreated, result.APIKeysSkipped, result.RulesCreated, result.RulesSkipped, result.SecretsCreated, result.SecretsSkipped)
	}
	return nil
}

func loadManifests(store *catalog.Store, path string, durable bool) (int, error) {
	manifests, err := catalog.LoadManifests(path)
	if err != nil {
		return 0, fmt.Errorf("load manifests: %w", err)
	}
	loaded := 0
	for _, manifest := range manifests {
		if _, err := store.RegisterManifest(manifest); err != nil {
			if durable && errors.Is(err, catalog.ErrDuplicateService) {
				log.Printf("manifest %s already present in catalog state; skipping", manifest.Service.ID)
				continue
			}
			return loaded, fmt.Errorf("register manifest %s: %w", manifest.Service.ID, err)
		}
		loaded++
	}
	return loaded, nil
}

func loadResources(store *leases.Store, path string, durable bool) (int, error) {
	resources, err := leases.LoadResourceRegistrations(path)
	if err != nil {
		return 0, fmt.Errorf("load resources: %w", err)
	}
	loaded := 0
	for _, resource := range resources {
		if _, err := store.RegisterResource(resource); err != nil {
			if durable && errors.Is(err, leases.ErrResourceConflict) {
				log.Printf("resource %s already present in lease state; skipping", resource.ResourceID)
				continue
			}
			return loaded, fmt.Errorf("register resource %s: %w", resource.ResourceID, err)
		}
		loaded++
	}
	return loaded, nil
}

func statePath(cfg primaryConfig, name string) string {
	if cfg.StateDir == "" {
		return ""
	}
	return filepath.Join(cfg.StateDir, name+".json")
}

func serveBound(ctx context.Context, service boundService, handler http.Handler) *http.Server {
	server := &http.Server{Handler: handler}
	go func() {
		log.Printf("serving %s addr=%s url=%s", service.name, service.listener.Addr().String(), service.url)
		err := server.Serve(service.listener)
		if err != nil && err != http.ErrServerClosed && ctx.Err() == nil {
			log.Printf("%s server error: %v", service.name, err)
		}
	}()
	return server
}

func shutdownServers(ctx context.Context, servers []*http.Server) {
	for i := len(servers) - 1; i >= 0; i-- {
		if err := servers[i].Shutdown(ctx); err != nil {
			log.Printf("shutdown server: %v", err)
		}
	}
}

func closeListeners(bound []boundService) {
	for _, service := range bound {
		_ = service.listener.Close()
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

func endpointForListener(requestedAddr string, listener net.Listener) string {
	addr := listener.Addr().String()
	host, port, err := net.SplitHostPort(addr)
	if err == nil {
		if host == "" || host == "::" || host == "[::]" {
			host = "localhost"
		}
		if strings.Contains(host, ":") {
			host = "[" + strings.Trim(host, "[]") + "]"
		}
		return "http://" + host + ":" + port
	}
	if strings.HasPrefix(requestedAddr, ":") {
		return "http://localhost" + requestedAddr
	}
	return "http://" + requestedAddr
}

func parseNodeURLMap(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	out := map[string]string{}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("node URL mapping %q must be node_id=URL", entry)
		}
		nodeID := strings.TrimSpace(parts[0])
		nodeURL := strings.TrimRight(strings.TrimSpace(parts[1]), "/")
		if nodeID == "" || nodeURL == "" {
			return nil, fmt.Errorf("node URL mapping %q must include node_id and URL", entry)
		}
		out[nodeID] = nodeURL
	}
	return out, nil
}

func componentCredentialDefault(primaryEnv string) string {
	if value := os.Getenv(primaryEnv); value != "" {
		return value
	}
	return os.Getenv("PACP_COMPONENT_TOKEN")
}

func authorizationHeader(token string) string {
	if token == "" {
		return ""
	}
	if strings.HasPrefix(token, "Bearer ") {
		return token
	}
	return "Bearer " + token
}
