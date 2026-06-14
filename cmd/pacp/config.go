package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultConfigPath  = "pacp.yaml"
	defaultStateDir    = ".pacp/state"
	defaultArtifactDir = ".pacp/artifacts"
)

type Config struct {
	SchemaVersion string              `yaml:"schema_version"`
	Primary       PrimaryConfig       `yaml:"primary"`
	Credentials   CredentialConfig    `yaml:"credentials"`
	Providers     []ProviderConfig    `yaml:"providers"`
	Nodes         []RuntimeNodeConfig `yaml:"nodes"`
}

type PrimaryConfig struct {
	Host           string     `yaml:"host"`
	BindHost       string     `yaml:"bind_host,omitempty"`
	Ports          PortConfig `yaml:"ports"`
	StateDir       string     `yaml:"state_dir"`
	ArtifactRoot   string     `yaml:"artifact_root"`
	EmbeddedRunner bool       `yaml:"embedded_runner"`
}

type PortConfig struct {
	NodeRegistry int `yaml:"node_registry"`
	Catalog      int `yaml:"catalog"`
	Jobs         int `yaml:"jobs"`
	Leases       int `yaml:"leases"`
	Artifacts    int `yaml:"artifacts"`
	Policy       int `yaml:"policy"`
	Gateway      int `yaml:"gateway"`
	Provider     int `yaml:"provider"`
}

type CredentialConfig struct {
	Agent     string `yaml:"agent"`
	Component string `yaml:"component"`
	Runner    string `yaml:"runner"`
	NodeAdmin string `yaml:"node_admin"`
}

type ProviderConfig struct {
	ServiceID    string `yaml:"service_id"`
	ServiceName  string `yaml:"service_name,omitempty"`
	NodeID       string `yaml:"node_id,omitempty"`
	Kind         string `yaml:"kind"`
	Addr         string `yaml:"addr"`
	Endpoint     string `yaml:"endpoint"`
	CapabilityID string `yaml:"capability_id,omitempty"`
	DryRun       bool   `yaml:"dry_run,omitempty"`
	Workflow     string `yaml:"workflow,omitempty"`
	LoraCatalog  string `yaml:"lora_catalog,omitempty"`
	ComfyUIURL   string `yaml:"comfyui_url,omitempty"`
}

type RuntimeNodeConfig struct {
	NodeID      string           `yaml:"node_id"`
	DisplayName string           `yaml:"display_name,omitempty"`
	Addr        string           `yaml:"addr,omitempty"`
	PublicURL   string           `yaml:"public_url,omitempty"`
	Resources   []ResourceConfig `yaml:"resources,omitempty"`
}

type ResourceConfig struct {
	ResourceID  string            `yaml:"resource_id"`
	Selector    string            `yaml:"selector"`
	DisplayName string            `yaml:"display_name,omitempty"`
	Tags        []string          `yaml:"tags,omitempty"`
	Metadata    map[string]string `yaml:"metadata,omitempty"`
}

func defaultConfig() (Config, error) {
	agentToken, err := generateToken()
	if err != nil {
		return Config{}, err
	}
	componentToken, err := generateToken()
	if err != nil {
		return Config{}, err
	}
	runnerToken, err := generateToken()
	if err != nil {
		return Config{}, err
	}
	nodeAdminToken, err := generateToken()
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		SchemaVersion: "v1",
		Primary: PrimaryConfig{
			Host:           "localhost",
			Ports:          defaultPorts(),
			StateDir:       defaultStateDir,
			ArtifactRoot:   defaultArtifactDir,
			EmbeddedRunner: true,
		},
		Credentials: CredentialConfig{
			Agent:     agentToken,
			Component: componentToken,
			Runner:    runnerToken,
			NodeAdmin: nodeAdminToken,
		},
		Providers: []ProviderConfig{{
			ServiceID:    "svc_dev_provider",
			ServiceName:  "Development Provider",
			NodeID:       "node_local",
			Kind:         "dev",
			Addr:         "localhost:18088",
			Endpoint:     "http://localhost:18088",
			CapabilityID: "cap_dev_echo",
			DryRun:       true,
		}},
		Nodes: []RuntimeNodeConfig{{
			NodeID:      "node_local",
			DisplayName: "Local Node",
			Addr:        "localhost:18087",
			PublicURL:   "http://localhost:18087",
			Resources: []ResourceConfig{{
				ResourceID:  "res_dev_gpu",
				Selector:    "gpu",
				DisplayName: "Local development GPU",
				Tags:        []string{"gpu", "dev"},
				Metadata: map[string]string{
					"kind": "gpu",
				},
			}},
		}},
	}
	return cfg, nil
}

func defaultPorts() PortConfig {
	return PortConfig{
		NodeRegistry: 18080,
		Catalog:      18081,
		Jobs:         18082,
		Leases:       18083,
		Artifacts:    18084,
		Policy:       18085,
		Gateway:      18086,
		Provider:     18088,
	}
}

func generateToken() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return "pacp_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func loadConfig(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := expandConfigEnv(&cfg); err != nil {
		return Config{}, err
	}
	applyConfigDefaults(&cfg)
	if err := validateConfig(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func writeConfig(path string, cfg Config, force bool) error {
	if path == "" {
		path = defaultConfigPath
	}
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists; use --force to overwrite", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return os.WriteFile(path, raw, 0o600)
}

func ensureConfig(path string) (Config, bool, error) {
	if path == "" {
		path = defaultConfigPath
	}
	cfg, err := loadConfig(path)
	if err == nil {
		return cfg, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Config{}, false, err
	}
	cfg, err = defaultConfig()
	if err != nil {
		return Config{}, false, err
	}
	if err := writeConfig(path, cfg, false); err != nil {
		return Config{}, false, err
	}
	_ = ensureGeneratedIgnore(filepath.Dir(path))
	return cfg, true, nil
}

func applyConfigDefaults(cfg *Config) {
	if cfg.SchemaVersion == "" {
		cfg.SchemaVersion = "v1"
	}
	if cfg.Primary.Host == "" {
		cfg.Primary.Host = "localhost"
	}
	if cfg.Primary.Ports == (PortConfig{}) {
		cfg.Primary.Ports = defaultPorts()
	}
	defaults := defaultPorts()
	if cfg.Primary.Ports.NodeRegistry == 0 {
		cfg.Primary.Ports.NodeRegistry = defaults.NodeRegistry
	}
	if cfg.Primary.Ports.Catalog == 0 {
		cfg.Primary.Ports.Catalog = defaults.Catalog
	}
	if cfg.Primary.Ports.Jobs == 0 {
		cfg.Primary.Ports.Jobs = defaults.Jobs
	}
	if cfg.Primary.Ports.Leases == 0 {
		cfg.Primary.Ports.Leases = defaults.Leases
	}
	if cfg.Primary.Ports.Artifacts == 0 {
		cfg.Primary.Ports.Artifacts = defaults.Artifacts
	}
	if cfg.Primary.Ports.Policy == 0 {
		cfg.Primary.Ports.Policy = defaults.Policy
	}
	if cfg.Primary.Ports.Gateway == 0 {
		cfg.Primary.Ports.Gateway = defaults.Gateway
	}
	if cfg.Primary.Ports.Provider == 0 {
		cfg.Primary.Ports.Provider = defaults.Provider
	}
	if cfg.Primary.StateDir == "" {
		cfg.Primary.StateDir = defaultStateDir
	}
	if cfg.Primary.ArtifactRoot == "" {
		cfg.Primary.ArtifactRoot = defaultArtifactDir
	}
	if len(cfg.Providers) == 0 {
		cfg.Providers = []ProviderConfig{{
			ServiceID:    "svc_dev_provider",
			ServiceName:  "Development Provider",
			NodeID:       "node_local",
			Kind:         "dev",
			Addr:         addrFor(cfg.Primary.Host, cfg.Primary.Ports.Provider),
			Endpoint:     "http://" + addrFor(cfg.Primary.Host, cfg.Primary.Ports.Provider),
			CapabilityID: "cap_dev_echo",
			DryRun:       true,
		}}
	}
	for i := range cfg.Providers {
		if cfg.Providers[i].Kind == "" {
			cfg.Providers[i].Kind = "dev"
		}
		if cfg.Providers[i].Addr == "" {
			cfg.Providers[i].Addr = addrFor(cfg.Primary.Host, cfg.Primary.Ports.Provider)
		}
		if cfg.Providers[i].Endpoint == "" {
			cfg.Providers[i].Endpoint = endpointForAddr(cfg.Providers[i].Addr)
		}
		if cfg.Providers[i].CapabilityID == "" && cfg.Providers[i].Kind == "dev" {
			cfg.Providers[i].CapabilityID = "cap_dev_echo"
		}
	}
}

func validateConfig(cfg Config) error {
	if cfg.SchemaVersion != "v1" {
		return fmt.Errorf("unsupported schema_version %q", cfg.SchemaVersion)
	}
	if strings.TrimSpace(cfg.Credentials.Agent) == "" {
		return errors.New("credentials.agent is required")
	}
	if strings.TrimSpace(cfg.Credentials.Component) == "" {
		return errors.New("credentials.component is required")
	}
	if strings.TrimSpace(cfg.Credentials.Runner) == "" {
		return errors.New("credentials.runner is required")
	}
	for _, provider := range cfg.Providers {
		if strings.TrimSpace(provider.ServiceID) == "" {
			return errors.New("providers[].service_id is required")
		}
		if strings.TrimSpace(provider.Kind) == "" {
			return fmt.Errorf("provider %s kind is required", provider.ServiceID)
		}
	}
	for _, node := range cfg.Nodes {
		if strings.TrimSpace(node.NodeID) == "" {
			return errors.New("nodes[].node_id is required")
		}
		for _, resource := range node.Resources {
			if strings.TrimSpace(resource.ResourceID) == "" {
				return fmt.Errorf("node %s resource_id is required", node.NodeID)
			}
			if strings.TrimSpace(resource.Selector) == "" {
				return fmt.Errorf("node %s resource %s selector is required", node.NodeID, resource.ResourceID)
			}
		}
	}
	return nil
}

var envRefPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func expandConfigEnv(cfg *Config) error {
	var err error
	fields := []*string{
		&cfg.Primary.Host,
		&cfg.Primary.BindHost,
		&cfg.Primary.StateDir,
		&cfg.Primary.ArtifactRoot,
		&cfg.Credentials.Agent,
		&cfg.Credentials.Component,
		&cfg.Credentials.Runner,
		&cfg.Credentials.NodeAdmin,
	}
	for _, field := range fields {
		*field, err = expandEnvValue(*field)
		if err != nil {
			return err
		}
	}
	for i := range cfg.Providers {
		fields := []*string{
			&cfg.Providers[i].ServiceID,
			&cfg.Providers[i].ServiceName,
			&cfg.Providers[i].NodeID,
			&cfg.Providers[i].Kind,
			&cfg.Providers[i].Addr,
			&cfg.Providers[i].Endpoint,
			&cfg.Providers[i].CapabilityID,
			&cfg.Providers[i].Workflow,
			&cfg.Providers[i].LoraCatalog,
			&cfg.Providers[i].ComfyUIURL,
		}
		for _, field := range fields {
			*field, err = expandEnvValue(*field)
			if err != nil {
				return err
			}
		}
	}
	for i := range cfg.Nodes {
		fields := []*string{&cfg.Nodes[i].NodeID, &cfg.Nodes[i].DisplayName, &cfg.Nodes[i].Addr, &cfg.Nodes[i].PublicURL}
		for _, field := range fields {
			*field, err = expandEnvValue(*field)
			if err != nil {
				return err
			}
		}
		for j := range cfg.Nodes[i].Resources {
			fields := []*string{
				&cfg.Nodes[i].Resources[j].ResourceID,
				&cfg.Nodes[i].Resources[j].Selector,
				&cfg.Nodes[i].Resources[j].DisplayName,
			}
			for _, field := range fields {
				*field, err = expandEnvValue(*field)
				if err != nil {
					return err
				}
			}
			for k := range cfg.Nodes[i].Resources[j].Tags {
				cfg.Nodes[i].Resources[j].Tags[k], err = expandEnvValue(cfg.Nodes[i].Resources[j].Tags[k])
				if err != nil {
					return err
				}
			}
			for key, value := range cfg.Nodes[i].Resources[j].Metadata {
				expanded, expandErr := expandEnvValue(value)
				if expandErr != nil {
					return expandErr
				}
				cfg.Nodes[i].Resources[j].Metadata[key] = expanded
			}
		}
	}
	return nil
}

func expandEnvValue(value string) (string, error) {
	var missing []string
	out := envRefPattern.ReplaceAllStringFunc(value, func(match string) string {
		name := envRefPattern.FindStringSubmatch(match)[1]
		envValue, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
			return match
		}
		return envValue
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("environment variable %s is referenced but not set", strings.Join(missing, ", "))
	}
	return out, nil
}

func ensureGeneratedIgnore(dir string) error {
	if dir == "" {
		dir = "."
	}
	path := filepath.Join(dir, ".gitignore")
	raw, _ := os.ReadFile(path)
	text := string(raw)
	entries := []string{"pacp.yaml", ".pacp/"}
	var additions []string
	for _, entry := range entries {
		if !gitignoreContains(text, entry) {
			additions = append(additions, entry)
		}
	}
	if len(additions) == 0 {
		return nil
	}
	prefix := ""
	if len(text) > 0 && !strings.HasSuffix(text, "\n") {
		prefix = "\n"
	}
	return os.WriteFile(path, []byte(text+prefix+strings.Join(additions, "\n")+"\n"), 0o644)
}

func gitignoreContains(text, entry string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == entry {
			return true
		}
	}
	return false
}

func addrFor(host string, port int) string {
	return fmt.Sprintf("%s:%d", host, port)
}

func (cfg Config) primaryBindHost() string {
	if cfg.Primary.BindHost != "" {
		return cfg.Primary.BindHost
	}
	return cfg.Primary.Host
}

func (cfg Config) catalogURL() string {
	return "http://" + addrFor(cfg.Primary.Host, cfg.Primary.Ports.Catalog)
}

func (cfg Config) jobsURL() string {
	return "http://" + addrFor(cfg.Primary.Host, cfg.Primary.Ports.Jobs)
}

func (cfg Config) leasesURL() string {
	return "http://" + addrFor(cfg.Primary.Host, cfg.Primary.Ports.Leases)
}

func (cfg Config) artifactsURL() string {
	return "http://" + addrFor(cfg.Primary.Host, cfg.Primary.Ports.Artifacts)
}

func (cfg Config) policyURL() string {
	return "http://" + addrFor(cfg.Primary.Host, cfg.Primary.Ports.Policy)
}

func (cfg Config) gatewayURL() string {
	return "http://" + addrFor(cfg.Primary.Host, cfg.Primary.Ports.Gateway)
}

func (cfg Config) nodeRegistryURL() string {
	return "http://" + addrFor(cfg.Primary.Host, cfg.Primary.Ports.NodeRegistry)
}
