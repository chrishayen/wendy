package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"pacp/internal/components/artifacts"
	"pacp/internal/components/catalog"
	"pacp/internal/components/gateway"
	"pacp/internal/components/jobs"
	"pacp/internal/components/leases"
	nodecomponent "pacp/internal/components/node"
	"pacp/internal/components/noderegistry"
	"pacp/internal/components/policy"
	"pacp/internal/contracts"
	"pacp/internal/observability"
	"pacp/internal/provider"
	"pacp/internal/routeauth"
	"pacp/internal/runner"
	"pacp/internal/transportauth"
)

type stackOptions struct {
	StartProvider bool
	StartNodes    bool
	DisableRunner bool
	Ready         chan string
}

type stackStores struct {
	catalogStore      *catalog.Store
	jobStore          *jobs.Store
	leaseStore        *leases.Store
	artifactStore     *artifacts.Store
	policyStore       *policy.Store
	nodeRegistryStore *noderegistry.Store
}

func runConfiguredStack(ctx context.Context, cfg Config, opts stackOptions) error {
	manifests := manifestsForProviders(cfg.Providers)
	stores, err := newStackStores(cfg, manifests)
	if err != nil {
		return err
	}
	if err := seedConfiguredNodes(stores.nodeRegistryStore, cfg); err != nil {
		return err
	}

	gatewayHandler, err := gateway.NewPersistentHandler(gateway.Config{
		CatalogURL:        cfg.catalogURL(),
		PolicyURL:         cfg.policyURL(),
		JobsURL:           cfg.jobsURL(),
		LeasesURL:         cfg.leasesURL(),
		ArtifactsURL:      cfg.artifactsURL(),
		GatewayCredential: authorizationHeader(cfg.Credentials.Component),
	}, stackStatePath(cfg, "gateway-idempotency"))
	if err != nil {
		return err
	}

	handlers := stackComponentHandlers(cfg, stores, gatewayHandler)
	services := []struct {
		name    string
		addr    string
		handler http.Handler
	}{
		{name: "node-registry", addr: addrFor(cfg.primaryBindHost(), cfg.Primary.Ports.NodeRegistry), handler: handlers["node-registry"]},
		{name: "catalog", addr: addrFor(cfg.primaryBindHost(), cfg.Primary.Ports.Catalog), handler: handlers["catalog"]},
		{name: "jobs", addr: addrFor(cfg.primaryBindHost(), cfg.Primary.Ports.Jobs), handler: handlers["jobs"]},
		{name: "leases", addr: addrFor(cfg.primaryBindHost(), cfg.Primary.Ports.Leases), handler: handlers["leases"]},
		{name: "artifacts", addr: addrFor(cfg.primaryBindHost(), cfg.Primary.Ports.Artifacts), handler: handlers["artifacts"]},
		{name: "policy", addr: addrFor(cfg.primaryBindHost(), cfg.Primary.Ports.Policy), handler: handlers["policy"]},
		{name: "gateway", addr: addrFor(cfg.primaryBindHost(), cfg.Primary.Ports.Gateway), handler: handlers["gateway"]},
	}
	if opts.StartProvider {
		for i, providerCfg := range cfg.Providers {
			providerServer, err := newConfiguredProviderServer(providerCfg, manifests[i], cfg)
			if err != nil {
				return err
			}
			services = append(services, struct {
				name    string
				addr    string
				handler http.Handler
			}{name: "provider:" + providerCfg.ServiceID, addr: providerCfg.Addr, handler: providerServer})
		}
	}
	if opts.StartNodes {
		nodeServices, err := configuredNodeServices(cfg)
		if err != nil {
			return err
		}
		services = append(services, nodeServices...)
	}

	servers := []*http.Server{}
	for _, svc := range services {
		server, err := serveHTTP(ctx, svc.name, svc.addr, svc.handler)
		if err != nil {
			shutdownServers(context.Background(), servers)
			return err
		}
		servers = append(servers, server)
	}

	if cfg.Primary.EmbeddedRunner && !opts.DisableRunner {
		r := runner.New(runner.Config{
			WorkerID:               "runner_local",
			CatalogURL:             cfg.catalogURL(),
			JobsURL:                cfg.jobsURL(),
			LeasesURL:              cfg.leasesURL(),
			ArtifactsURL:           cfg.artifactsURL(),
			PolicyURL:              cfg.policyURL(),
			LeasePollInterval:      time.Second,
			ComponentCredential:    authorizationHeader(cfg.Credentials.Runner),
			PolicyCredential:       authorizationHeader(cfg.Credentials.Component),
			NodeRegistryURL:        cfg.nodeRegistryURL(),
			NodeRegistryCredential: authorizationHeader(cfg.Credentials.Component),
			WorkerSubjectID:        "sub_runner_local",
			ActorSubjectID:         "sub_runner_local",
		})
		go runnerLoop(ctx, r, time.Second, cfg.Credentials.Runner, cfg.Credentials.Component)
	}

	log.Printf("pacp ready gateway=%s config=%s", cfg.gatewayURL(), defaultConfigPath)
	if opts.Ready != nil {
		opts.Ready <- cfg.gatewayURL()
	}
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownServers(shutdownCtx, servers)
	return nil
}

func manifestsForProviders(providers []ProviderConfig) []contracts.ProviderManifest {
	manifests := make([]contracts.ProviderManifest, 0, len(providers))
	for _, provider := range providers {
		manifests = append(manifests, manifestForProvider(provider))
	}
	return manifests
}

func newStackStores(cfg Config, manifests []contracts.ProviderManifest) (stackStores, error) {
	if err := os.MkdirAll(cfg.Primary.StateDir, 0o700); err != nil {
		return stackStores{}, err
	}
	catalogStore, err := catalog.NewPersistentStore(stackStatePath(cfg, "catalog"))
	if err != nil {
		return stackStores{}, fmt.Errorf("create catalog store: %w", err)
	}
	jobStore, err := jobs.NewPersistentStore(stackStatePath(cfg, "jobs"))
	if err != nil {
		return stackStores{}, fmt.Errorf("create job store: %w", err)
	}
	leaseStore, err := leases.NewPersistentStore(stackStatePath(cfg, "leases"))
	if err != nil {
		return stackStores{}, fmt.Errorf("create lease store: %w", err)
	}
	artifactStore, err := artifacts.NewPersistentStore(cfg.Primary.ArtifactRoot, stackStatePath(cfg, "artifacts"))
	if err != nil {
		return stackStores{}, fmt.Errorf("create artifact store: %w", err)
	}
	policyStore, err := policy.NewPersistentStore(stackStatePath(cfg, "policy"))
	if err != nil {
		return stackStores{}, fmt.Errorf("create policy store: %w", err)
	}
	nodeRegistryStore, err := noderegistry.NewPersistentStore(stackStatePath(cfg, "node-registry"))
	if err != nil {
		return stackStores{}, fmt.Errorf("create node registry store: %w", err)
	}
	for _, manifest := range manifests {
		if err := registerManifest(catalogStore, manifest); err != nil {
			return stackStores{}, fmt.Errorf("seed catalog: %w", err)
		}
	}
	if err := registerResources(leaseStore, cfg); err != nil {
		return stackStores{}, fmt.Errorf("seed resource: %w", err)
	}
	for _, req := range []contracts.CreateAPIKeyRequest{
		{SubjectID: "sub_agent_local", Scopes: []string{"agent"}, Token: cfg.Credentials.Agent},
		{SubjectID: "sub_gateway_local", Scopes: []string{"component"}, Token: cfg.Credentials.Component},
		{SubjectID: "sub_runner_local", Scopes: []string{"worker"}, Token: cfg.Credentials.Runner},
	} {
		if err := createAPIKey(policyStore, req); err != nil {
			return stackStores{}, fmt.Errorf("seed api key %s: %w", req.SubjectID, err)
		}
	}
	return stackStores{
		catalogStore:      catalogStore,
		jobStore:          jobStore,
		leaseStore:        leaseStore,
		artifactStore:     artifactStore,
		policyStore:       policyStore,
		nodeRegistryStore: nodeRegistryStore,
	}, nil
}

func stackComponentHandlers(cfg Config, stores stackStores, gatewayHandler http.Handler) map[string]http.Handler {
	policyURL := cfg.policyURL()
	policyCredential := authorizationHeader(cfg.Credentials.Component)
	return map[string]http.Handler{
		"catalog": transportauth.RequireVerifiedScopes(catalog.NewHandler(stores.catalogStore), transportauth.ScopeConfig{
			PolicyURL:        policyURL,
			PolicyCredential: policyCredential,
			Rules:            routeauth.CatalogScopeRules(),
		}),
		"jobs": transportauth.RequireVerifiedScopes(jobs.NewHandler(stores.jobStore), transportauth.ScopeConfig{
			PolicyURL:        policyURL,
			PolicyCredential: policyCredential,
			Rules:            routeauth.JobScopeRules(),
		}),
		"leases": transportauth.RequireVerifiedScopes(leases.NewHandler(stores.leaseStore), transportauth.ScopeConfig{
			PolicyURL:        policyURL,
			PolicyCredential: policyCredential,
			Rules:            routeauth.LeaseScopeRules(),
		}),
		"artifacts": transportauth.RequireVerifiedScopes(artifacts.NewHandler(stores.artifactStore), transportauth.ScopeConfig{
			PolicyURL:        policyURL,
			PolicyCredential: policyCredential,
			Rules:            routeauth.ArtifactScopeRules(),
		}),
		"policy":        transportauth.RequireBearer(policy.NewHandler(stores.policyStore), cfg.Credentials.Component),
		"node-registry": transportauth.RequireBearer(noderegistry.NewHandler(stores.nodeRegistryStore), cfg.Credentials.Component),
		"gateway":       gatewayHandler,
	}
}

func stackStatePath(cfg Config, name string) string {
	return filepath.Join(cfg.Primary.StateDir, name+".json")
}

func configuredNodeServices(cfg Config) ([]struct {
	name    string
	addr    string
	handler http.Handler
}, error) {
	services := []struct {
		name    string
		addr    string
		handler http.Handler
	}{}
	for _, runtimeNode := range cfg.Nodes {
		addr := runtimeNode.Addr
		if addr == "" {
			addr = addrFromHTTPURL(runtimeNode.PublicURL)
		}
		if strings.TrimSpace(runtimeNode.NodeID) == "" || strings.TrimSpace(addr) == "" {
			continue
		}
		nodeCfg, err := configuredNodeConfig(cfg, runtimeNode.NodeID)
		if err != nil {
			return nil, err
		}
		store, err := nodecomponent.NewStore(nodeCfg)
		if err != nil {
			return nil, err
		}
		services = append(services, struct {
			name    string
			addr    string
			handler http.Handler
		}{name: "node:" + runtimeNode.NodeID, addr: addr, handler: nodecomponent.NewHandler(store)})
	}
	return services, nil
}

func registerManifest(store *catalog.Store, manifest contracts.ProviderManifest) error {
	_, err := store.RegisterManifest(manifest)
	if err == nil {
		return nil
	}
	if !errors.Is(err, catalog.ErrDuplicateService) {
		return err
	}
	for _, capability := range manifest.Capabilities {
		if _, ok := store.GetCapability(capability.ID); !ok {
			return err
		}
	}
	return nil
}

func registerResources(store *leases.Store, cfg Config) error {
	for _, node := range cfg.Nodes {
		for _, resource := range node.Resources {
			req := contracts.RegisterResourceRequest{
				ResourceID:  resource.ResourceID,
				Selector:    resource.Selector,
				DisplayName: resource.DisplayName,
				Status:      contracts.ResourceAvailable,
				NodeID:      node.NodeID,
				Tags:        append([]string(nil), resource.Tags...),
				Metadata:    resourceMetadata(resource),
			}
			if err := registerResource(store, req); err != nil {
				return err
			}
		}
	}
	return nil
}

func resourceMetadata(resource ResourceConfig) map[string]any {
	if len(resource.Metadata) == 0 {
		return nil
	}
	metadata := make(map[string]any, len(resource.Metadata))
	for key, value := range resource.Metadata {
		metadata[key] = value
	}
	return metadata
}

func registerResource(store *leases.Store, req contracts.RegisterResourceRequest) error {
	_, err := store.RegisterResource(req)
	if err == nil {
		return nil
	}
	if !errors.Is(err, leases.ErrResourceConflict) {
		return err
	}
	resource, getErr := store.GetResource(req.ResourceID)
	if getErr == nil && resource.Selector == req.Selector && resource.Status == req.Status && resource.NodeID == req.NodeID {
		return nil
	}
	return err
}

func createAPIKey(store *policy.Store, req contracts.CreateAPIKeyRequest) error {
	_, err := store.CreateAPIKey(req)
	if err == nil {
		return nil
	}
	if !errors.Is(err, policy.ErrConflict) {
		return err
	}
	verification, verifyErr := store.VerifyCredential(contracts.VerifyCredentialRequest{Credential: authorizationHeader(req.Token)})
	if verifyErr == nil && verification.Valid && verification.SubjectID != nil && *verification.SubjectID == req.SubjectID && hasScopes(verification.Scopes, req.Scopes) {
		return nil
	}
	return err
}

func seedConfiguredNodes(store *noderegistry.Store, cfg Config) error {
	for _, node := range cfg.Nodes {
		if strings.TrimSpace(node.NodeID) == "" || strings.TrimSpace(node.PublicURL) == "" {
			continue
		}
		_, err := store.Register(contracts.RegisterNodeRequest{
			NodeID:      node.NodeID,
			URL:         node.PublicURL,
			DisplayName: node.DisplayName,
			TrustState:  contracts.NodeTrustTrusted,
			Status:      contracts.NodeStatusReachable,
			Tags:        []string{"local"},
		})
		if err != nil {
			return err
		}
	}
	return nil
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

func serveHTTP(ctx context.Context, name, addr string, handler http.Handler) (*http.Server, error) {
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

func newDevProviderServer(manifest contracts.ProviderManifest) (http.Handler, error) {
	return provider.NewServer(manifest, map[string]provider.CapabilityHandler{
		"cap_dev_echo":     devEchoHandler,
		"cap_dev_artifact": devArtifactHandler,
	})
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

func manifestForProvider(provider ProviderConfig) contracts.ProviderManifest {
	switch provider.Kind {
	case "comfyui":
		return imageProviderManifest(provider)
	default:
		return devManifest(provider)
	}
}

func devManifest(provider ProviderConfig) contracts.ProviderManifest {
	serviceName := provider.ServiceName
	if serviceName == "" {
		serviceName = "Development Provider"
	}
	return contracts.ProviderManifest{
		SchemaVersion: "v1",
		Service: contracts.Service{
			ID:           provider.ServiceID,
			Name:         serviceName,
			Description:  "Local provider for development smoke flows.",
			Version:      "0.1.0",
			ProviderKind: "development",
			Tags:         []string{"development", "local"},
		},
		Provider: contracts.Provider{Endpoint: provider.Endpoint, NodeID: provider.NodeID, HealthPath: "/v1/provider/health"},
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

func imageProviderManifest(provider ProviderConfig) contracts.ProviderManifest {
	serviceName := provider.ServiceName
	if serviceName == "" {
		serviceName = "ComfyUI Provider"
	}
	capabilityID := provider.CapabilityID
	if capabilityID == "" {
		capabilityID = "cap_comfyui_image_generate"
	}
	return contracts.ProviderManifest{
		SchemaVersion: "v1",
		Service: contracts.Service{
			ID:           provider.ServiceID,
			Name:         serviceName,
			Description:  "Image generation provider.",
			Version:      "0.1.0",
			ProviderKind: "comfyui",
			Tags:         []string{"image", "gpu"},
		},
		Provider: contracts.Provider{Endpoint: provider.Endpoint, NodeID: provider.NodeID, HealthPath: "/v1/provider/health"},
		Capabilities: []contracts.Capability{{
			ID:            capabilityID,
			Name:          "Image generation",
			Description:   "Generate an image using a GPU-backed provider.",
			Tags:          []string{"image", "gpu"},
			ExecutionMode: "async",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []any{"prompt"},
				"properties": map[string]any{
					"prompt": map[string]any{"type": "string"},
					"width":  map[string]any{"type": "integer"},
					"height": map[string]any{"type": "integer"},
					"seed":   map[string]any{"type": "integer"},
				},
			},
			OutputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"artifact_refs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
			},
			Examples:      []map[string]any{{"prompt": "red mug"}},
			SideEffects:   "external",
			ResourceHints: []contracts.ResourceHint{{Selector: "gpu", Required: true, Quantity: 1}},
			ArtifactHints: []contracts.ArtifactHint{{MediaType: "image/png", Count: "one"}},
			TimeoutHint:   "15m",
		}},
	}
}

func endpointForAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		addr = "localhost" + addr
	}
	return "http://" + addr
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
