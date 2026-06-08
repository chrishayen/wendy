package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"

	"pacp/internal/components/node"
)

func main() {
	addr := flag.String("addr", "localhost:18087", "listen address")
	configPath := flag.String("config", "", "node config JSON path")
	exportLeaseResources := flag.Bool("export-lease-resources", false, "print lease resource registration JSON for this node config and exit")
	nodeRegistryURL := flag.String("node-registry-url", os.Getenv("PACP_NODE_REGISTRY_URL"), "optional node registry service base URL")
	nodeRegistryCredential := flag.String("node-registry-credential", envFirst("PACP_NODE_REGISTRY_CREDENTIAL", "PACP_COMPONENT_TOKEN"), "optional node registry bearer token or raw token")
	nodePublicURL := flag.String("node-public-url", os.Getenv("PACP_NODE_PUBLIC_URL"), "public node API base URL used when registering this node")
	nodeRegistryRegister := flag.Bool("node-registry-register", boolEnv("PACP_NODE_REGISTRY_REGISTER"), "register this node with the node registry on startup")
	nodeRegistryTrustState := flag.String("node-registry-trust-state", os.Getenv("PACP_NODE_REGISTRY_TRUST_STATE"), "optional trust state to request when registering: trusted, untrusted, or disabled")
	nodeRegistryHeartbeat := flag.Duration("node-registry-heartbeat", durationEnvOrDefault("PACP_NODE_REGISTRY_HEARTBEAT", 0), "optional interval for node registry reachability heartbeats; 0 disables")
	flag.Parse()
	if *configPath == "" {
		log.Fatal("-config is required")
	}
	cfg, err := node.LoadConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	if *exportLeaseResources {
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"resources": node.LeaseResourceRegistrations(cfg)}); err != nil {
			log.Fatal(err)
		}
		return
	}
	store, err := node.NewStore(cfg)
	if err != nil {
		log.Fatal(err)
	}
	registry := registryConfig{
		URL:               *nodeRegistryURL,
		Credential:        *nodeRegistryCredential,
		PublicURL:         *nodePublicURL,
		Register:          *nodeRegistryRegister,
		TrustState:        *nodeRegistryTrustState,
		HeartbeatInterval: *nodeRegistryHeartbeat,
	}
	if err := validateRegistryConfig(registry); err != nil {
		log.Fatal(err)
	}
	if registry.Register {
		if err := registerNodeWithRegistry(context.Background(), http.DefaultClient, cfg, registry); err != nil {
			log.Fatal(err)
		}
		log.Printf("registered node with registry node_id=%s registry=%s", cfg.NodeID, registry.URL)
	}
	startNodeRegistryHeartbeat(context.Background(), http.DefaultClient, cfg, registry, log.Printf)
	log.Printf("serving node addr=%s node_id=%s", *addr, cfg.NodeID)
	if err := http.ListenAndServe(*addr, node.NewHandler(store)); err != nil {
		log.Fatal(err)
	}
}
