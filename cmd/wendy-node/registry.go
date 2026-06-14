package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"wendy/internal/contracts"
	"wendy/internal/observability"
)

type registryConfig struct {
	URL               string
	Credential        string
	PublicURL         string
	Register          bool
	TrustState        string
	HeartbeatInterval time.Duration
}

func validateRegistryConfig(cfg registryConfig) error {
	cfg.URL = strings.TrimSpace(cfg.URL)
	if cfg.URL == "" {
		if cfg.Register || cfg.HeartbeatInterval > 0 {
			return fmt.Errorf("node-registry-url is required when registry register or heartbeat is enabled")
		}
		return nil
	}
	if cfg.HeartbeatInterval < 0 {
		return fmt.Errorf("node-registry-heartbeat must be non-negative")
	}
	if cfg.Register && strings.TrimSpace(cfg.PublicURL) == "" {
		return fmt.Errorf("node-public-url is required when node-registry-register is enabled")
	}
	return nil
}

func registerNodeWithRegistry(ctx context.Context, client *http.Client, nodeCfg contracts.NodeConfig, registry registryConfig) error {
	req := contracts.RegisterNodeRequest{
		NodeID:      nodeCfg.NodeID,
		URL:         strings.TrimRight(strings.TrimSpace(registry.PublicURL), "/"),
		DisplayName: nodeCfg.DisplayName,
		TrustState:  registry.TrustState,
		Status:      contracts.NodeStatusReachable,
		Tags:        registryTags(nodeCfg),
		Metadata:    registryMetadata(nodeCfg),
	}
	return postRegistryJSON(ctx, client, registry, "/v1/node-registry/nodes", req)
}

func heartbeatNodeRegistryOnce(ctx context.Context, client *http.Client, nodeCfg contracts.NodeConfig, registry registryConfig) error {
	req := contracts.NodeHeartbeatRequest{
		Status:   contracts.NodeStatusReachable,
		Metadata: registryMetadata(nodeCfg),
	}
	path := "/v1/node-registry/nodes/" + url.PathEscape(nodeCfg.NodeID) + "/heartbeat"
	return postRegistryJSON(ctx, client, registry, path, req)
}

func startNodeRegistryHeartbeat(ctx context.Context, client *http.Client, nodeCfg contracts.NodeConfig, registry registryConfig, logf func(string, ...any)) {
	if registry.HeartbeatInterval <= 0 || strings.TrimSpace(registry.URL) == "" {
		return
	}
	if client == nil {
		client = http.DefaultClient
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	go func() {
		if err := heartbeatNodeRegistryOnce(ctx, client, nodeCfg, registry); err != nil {
			logf("node registry heartbeat failed node_id=%s error=%v", nodeCfg.NodeID, err)
		}
		ticker := time.NewTicker(registry.HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := heartbeatNodeRegistryOnce(ctx, client, nodeCfg, registry); err != nil {
					logf("node registry heartbeat failed node_id=%s error=%v", nodeCfg.NodeID, err)
				}
			}
		}
	}()
}

func postRegistryJSON(ctx context.Context, client *http.Client, registry registryConfig, path string, body any) error {
	if client == nil {
		client = http.DefaultClient
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	baseURL := strings.TrimRight(strings.TrimSpace(registry.URL), "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(observability.RequestIDHeader, observability.NewRequestID("req_node_registry_report"))
	if credential := authorizationHeader(registry.Credential); credential != "" {
		req.Header.Set("Authorization", credential)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	rawResp, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("node registry returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(rawResp)))
}

func registryMetadata(cfg contracts.NodeConfig) map[string]any {
	return map[string]any{
		"service_count":  len(cfg.Services),
		"resource_count": len(cfg.Resources),
	}
}

func registryTags(cfg contracts.NodeConfig) []string {
	seen := map[string]bool{}
	for _, resource := range cfg.Resources {
		for _, tag := range resource.Tags {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				seen[tag] = true
			}
		}
	}
	tags := make([]string, 0, len(seen))
	for tag := range seen {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}

func envFirst(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}

func boolEnv(name string) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	switch value {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func durationEnvOrDefault(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err == nil {
		return value
	}
	seconds, err := strconv.Atoi(raw)
	if err == nil {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}

func authorizationHeader(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if strings.HasPrefix(token, "Bearer ") {
		return token
	}
	return "Bearer " + token
}
