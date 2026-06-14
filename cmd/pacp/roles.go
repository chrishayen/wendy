package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"pacp/internal/components/node"
	"pacp/internal/contracts"
	"pacp/internal/provider/comfyui"
)

func runNodeRole(ctx context.Context, cfg Config, nodeID string, ready chan string) error {
	nodeCfg, err := configuredNodeConfig(cfg, nodeID)
	if err != nil {
		return err
	}
	store, err := node.NewStore(nodeCfg)
	if err != nil {
		return err
	}
	if nodeRecord, ok := findRuntimeNode(cfg, nodeID); ok && nodeRecord.PublicURL != "" {
		addr := nodeRecord.Addr
		if addr == "" {
			addr = addrFromHTTPURL(nodeRecord.PublicURL)
		}
		if addr == "" {
			addr = "localhost:18087"
		}
		return runNodeServer(ctx, addr, store, ready)
	}
	addr := "localhost:18087"
	return runNodeServer(ctx, addr, store, ready)
}

func runNodeServer(ctx context.Context, addr string, store *node.Store, ready chan string) error {
	server, err := serveHTTP(ctx, "node", addr, node.NewHandler(store))
	if err != nil {
		return err
	}
	if ready != nil {
		ready <- "http://" + addr
	}
	<-ctx.Done()
	shutdownServers(context.Background(), []*http.Server{server})
	return nil
}

func addrFromHTTPURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return parsed.Host
}

func configuredNodeConfig(cfg Config, nodeID string) (contracts.NodeConfig, error) {
	runtimeNode, ok := findRuntimeNode(cfg, nodeID)
	if !ok {
		return contracts.NodeConfig{}, fmt.Errorf("node %q is not configured", nodeID)
	}
	resources := make([]contracts.NodeResource, 0, len(runtimeNode.Resources))
	for _, resource := range runtimeNode.Resources {
		metadata := map[string]any{}
		for key, value := range resource.Metadata {
			metadata[key] = value
		}
		if resource.Selector != "" {
			metadata["selector"] = resource.Selector
		}
		resources = append(resources, contracts.NodeResource{
			ResourceID: resource.ResourceID,
			Tags:       resource.Tags,
			Metadata:   metadata,
		})
	}
	services := []contracts.NodeServiceConfig{}
	for _, provider := range cfg.Providers {
		if provider.NodeID != "" && provider.NodeID != nodeID {
			continue
		}
		services = append(services, contracts.NodeServiceConfig{
			ServiceID:          provider.ServiceID,
			DisplayName:        provider.ServiceName,
			RuntimeAdapter:     "fake",
			ProviderEndpoint:   provider.Endpoint,
			InitialStatus:      "stopped",
			IdleTimeoutSeconds: 900,
			Manifest:           ptrManifest(manifestForProvider(provider)),
		})
	}
	if len(services) == 0 {
		return contracts.NodeConfig{}, fmt.Errorf("node %q has no configured services", nodeID)
	}
	return contracts.NodeConfig{
		NodeID:      runtimeNode.NodeID,
		DisplayName: runtimeNode.DisplayName,
		Resources:   resources,
		Auth: []contracts.NodeAuthSubject{
			{
				Token:          cfg.Credentials.Runner,
				SubjectID:      "sub_runner_local",
				Scopes:         []string{"worker"},
				AllowedActions: []string{"node.read", "node.service.start", "node.service.touch", "node.service.stop"},
			},
			{
				Token:          cfg.Credentials.NodeAdmin,
				SubjectID:      "sub_node_admin",
				Scopes:         []string{"admin"},
				AllowedActions: []string{"node.read", "node.service.start", "node.service.touch", "node.service.stop"},
			},
		},
		Services: services,
	}, nil
}

func findRuntimeNode(cfg Config, nodeID string) (RuntimeNodeConfig, bool) {
	for _, node := range cfg.Nodes {
		if node.NodeID == nodeID {
			return node, true
		}
	}
	return RuntimeNodeConfig{}, false
}

func ptrManifest(manifest contracts.ProviderManifest) *contracts.ProviderManifest {
	return &manifest
}

func runProviderRole(ctx context.Context, cfg Config, serviceID string, ready chan string) error {
	providerCfg, ok := findProvider(cfg, serviceID)
	if !ok {
		return fmt.Errorf("provider service %q is not configured", serviceID)
	}
	handler, err := newConfiguredProviderServer(providerCfg, manifestForProvider(providerCfg), cfg)
	if err != nil {
		return err
	}
	server, err := serveHTTP(ctx, "provider", providerCfg.Addr, handler)
	if err != nil {
		return err
	}
	if ready != nil {
		ready <- endpointForAddr(providerCfg.Addr)
	}
	<-ctx.Done()
	shutdownServers(context.Background(), []*http.Server{server})
	return nil
}

func findProvider(cfg Config, serviceID string) (ProviderConfig, bool) {
	for _, provider := range cfg.Providers {
		if provider.ServiceID == serviceID {
			return provider, true
		}
	}
	return ProviderConfig{}, false
}

func newConfiguredProviderServer(providerCfg ProviderConfig, manifest contracts.ProviderManifest, cfg Config) (http.Handler, error) {
	switch providerCfg.Kind {
	case "comfyui":
		serviceName := providerCfg.ServiceName
		if serviceName == "" {
			serviceName = "ComfyUI Provider"
		}
		capabilityID := providerCfg.CapabilityID
		if capabilityID == "" {
			capabilityID = "cap_comfyui_image_generate"
		}
		return comfyui.NewServer(comfyui.Config{
			Endpoint:        providerCfg.Endpoint,
			ServiceID:       providerCfg.ServiceID,
			ServiceName:     serviceName,
			CapabilityID:    capabilityID,
			ComfyUIURL:      providerCfg.ComfyUIURL,
			WorkflowPath:    providerCfg.Workflow,
			LoraCatalogPath: providerCfg.LoraCatalog,
			DryRun:          providerCfg.DryRun,
			Timeout:         2 * time.Minute,
			PollInterval:    500 * time.Millisecond,
			ContentTTL:      15 * time.Minute,
			RunnerTokens:    []string{cfg.Credentials.Runner},
			ComponentTokens: []string{cfg.Credentials.Component},
		})
	default:
		return newDevProviderServer(manifest)
	}
}
