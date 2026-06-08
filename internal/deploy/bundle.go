package deploy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"pacp/internal/contracts"
)

const (
	defaultManifestVersion = "v1"
	defaultHealthPath      = "/v1/provider/health"
	defaultRuntimeAdapter  = "fake"
	defaultInitialStatus   = "stopped"
)

type Bundle struct {
	SchemaVersion string                      `json:"schema_version"`
	Node          BundleNode                  `json:"node"`
	Auth          []contracts.NodeAuthSubject `json:"auth,omitempty"`
	Resources     []contracts.NodeResource    `json:"resources,omitempty"`
	Services      []ServiceBundle             `json:"services"`
	PolicySeed    *PolicySeedFile             `json:"policy_seed,omitempty"`
}

type BundleNode struct {
	NodeID      string `json:"node_id"`
	DisplayName string `json:"display_name,omitempty"`
}

type ServiceBundle struct {
	Manifest           contracts.ProviderManifest      `json:"manifest"`
	RuntimeAdapter     string                          `json:"runtime_adapter,omitempty"`
	ProviderEndpoint   string                          `json:"provider_endpoint,omitempty"`
	InitialStatus      string                          `json:"initial_status,omitempty"`
	IdleTimeoutSeconds int                             `json:"idle_timeout_seconds,omitempty"`
	Process            *contracts.ProcessRuntimeConfig `json:"process,omitempty"`
	Docker             *contracts.DockerRuntimeConfig  `json:"docker,omitempty"`
	Metadata           map[string]any                  `json:"metadata,omitempty"`
}

type PolicySeedFile struct {
	APIKeys []contracts.CreateAPIKeyRequest     `json:"api_keys,omitempty"`
	Rules   []contracts.CreatePolicyRuleRequest `json:"rules,omitempty"`
	Secrets []contracts.CreateSecretRequest     `json:"secrets,omitempty"`
}

type ResourceSeedFile struct {
	Resources []contracts.RegisterResourceRequest `json:"resources"`
}

type RenderedBundle struct {
	NodeConfig   contracts.NodeConfig
	ResourceSeed ResourceSeedFile
	Manifests    []ManifestFile
	PolicySeed   *PolicySeedFile
}

type ManifestFile struct {
	ServiceID string
	Path      string
	Manifest  contracts.ProviderManifest
}

type RenderedFile struct {
	Path string
	Data []byte
}

func Parse(raw []byte) (Bundle, error) {
	var bundle Bundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return Bundle{}, fmt.Errorf("invalid deployment bundle JSON: %w", err)
	}
	return bundle, nil
}

func Render(bundle Bundle) (RenderedBundle, error) {
	var findings []string
	if bundle.SchemaVersion != defaultManifestVersion {
		findings = append(findings, "schema_version must be v1")
	}
	nodeID := strings.TrimSpace(bundle.Node.NodeID)
	if nodeID == "" {
		findings = append(findings, "node.node_id is required")
	}
	if !safeIdentifier(nodeID) {
		findings = append(findings, "node.node_id must contain only letters, digits, underscore, dash, or dot")
	}
	if len(bundle.Services) == 0 {
		findings = append(findings, "services must contain at least one service")
	}

	auth := cloneNodeAuth(bundle.Auth)
	for i, subject := range auth {
		if strings.TrimSpace(subject.Token) == "" {
			findings = append(findings, fmt.Sprintf("auth[%d].token is required", i))
		}
		if strings.TrimSpace(subject.SubjectID) == "" {
			findings = append(findings, fmt.Sprintf("auth[%d].subject_id is required", i))
		}
		if len(subject.AllowedActions) == 0 {
			findings = append(findings, fmt.Sprintf("auth[%d].allowed_actions must contain at least one action", i))
		}
	}

	resources := cloneNodeResources(bundle.Resources)
	seenResourceIDs := map[string]struct{}{}
	for i, resource := range resources {
		resourceID := strings.TrimSpace(resource.ResourceID)
		if resourceID == "" {
			findings = append(findings, fmt.Sprintf("resources[%d].resource_id is required", i))
			continue
		}
		if !safeIdentifier(resourceID) {
			findings = append(findings, fmt.Sprintf("resources[%d].resource_id must contain only letters, digits, underscore, dash, or dot", i))
		}
		if _, exists := seenResourceIDs[resourceID]; exists {
			findings = append(findings, fmt.Sprintf("resources[%d].resource_id duplicates another resource", i))
		}
		seenResourceIDs[resourceID] = struct{}{}
	}

	nodeServices := make([]contracts.NodeServiceConfig, 0, len(bundle.Services))
	manifests := make([]ManifestFile, 0, len(bundle.Services))
	seenServiceIDs := map[string]struct{}{}
	seenCapabilityIDs := map[string]struct{}{}
	for i, service := range bundle.Services {
		manifest, serviceFindings := renderServiceManifest(nodeID, i, service, seenServiceIDs, seenCapabilityIDs)
		findings = append(findings, serviceFindings...)
		if len(serviceFindings) > 0 {
			continue
		}
		if service.IdleTimeoutSeconds < 0 {
			findings = append(findings, fmt.Sprintf("services[%d].idle_timeout_seconds must be >= 0", i))
			continue
		}
		runtimeAdapter, runtimeFindings := serviceRuntimeAdapter(i, service)
		findings = append(findings, runtimeFindings...)
		if len(runtimeFindings) > 0 {
			continue
		}
		initialStatus := strings.TrimSpace(service.InitialStatus)
		if initialStatus == "" {
			initialStatus = defaultInitialStatus
		}
		endpoint := strings.TrimSpace(service.ProviderEndpoint)
		if endpoint == "" {
			endpoint = manifest.Provider.Endpoint
		}
		manifestCopy := manifest
		nodeServices = append(nodeServices, contracts.NodeServiceConfig{
			ServiceID:          manifest.Service.ID,
			DisplayName:        manifest.Service.Name,
			RuntimeAdapter:     runtimeAdapter,
			ProviderEndpoint:   endpoint,
			InitialStatus:      initialStatus,
			IdleTimeoutSeconds: service.IdleTimeoutSeconds,
			Manifest:           &manifestCopy,
			Process:            cloneProcessConfig(service.Process),
			Docker:             cloneDockerConfig(service.Docker),
			Metadata:           cloneMap(service.Metadata),
		})
		manifests = append(manifests, ManifestFile{
			ServiceID: manifest.Service.ID,
			Path:      "catalog/" + manifest.Service.ID + ".manifest.json",
			Manifest:  manifest,
		})
	}
	if len(findings) > 0 {
		return RenderedBundle{}, fmt.Errorf("invalid deployment bundle: %s", strings.Join(findings, "; "))
	}

	sort.Slice(manifests, func(i, j int) bool {
		return manifests[i].Path < manifests[j].Path
	})
	return RenderedBundle{
		NodeConfig: contracts.NodeConfig{
			NodeID:      nodeID,
			DisplayName: strings.TrimSpace(bundle.Node.DisplayName),
			Resources:   resources,
			Auth:        auth,
			Services:    nodeServices,
		},
		ResourceSeed: ResourceSeedFile{Resources: leaseResourceRegistrations(nodeID, bundle.Node.DisplayName, resources)},
		Manifests:    manifests,
		PolicySeed:   clonePolicySeed(bundle.PolicySeed),
	}, nil
}

func (r RenderedBundle) Files() ([]RenderedFile, error) {
	files := []RenderedFile{}
	add := func(path string, value any) error {
		raw, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		raw = append(raw, '\n')
		files = append(files, RenderedFile{Path: path, Data: raw})
		return nil
	}
	if err := add("node/node.json", r.NodeConfig); err != nil {
		return nil, fmt.Errorf("render node config: %w", err)
	}
	if err := add("leases/resources.json", r.ResourceSeed); err != nil {
		return nil, fmt.Errorf("render resource seed: %w", err)
	}
	for _, manifest := range r.Manifests {
		if err := add(manifest.Path, manifest.Manifest); err != nil {
			return nil, fmt.Errorf("render manifest %s: %w", manifest.ServiceID, err)
		}
	}
	if r.PolicySeed != nil {
		if err := add("policy/policy-seed.json", r.PolicySeed); err != nil {
			return nil, fmt.Errorf("render policy seed: %w", err)
		}
	}
	return files, nil
}

func renderServiceManifest(nodeID string, serviceIndex int, service ServiceBundle, seenServiceIDs, seenCapabilityIDs map[string]struct{}) (contracts.ProviderManifest, []string) {
	var findings []string
	manifest := cloneManifest(service.Manifest)
	if manifest.SchemaVersion == "" {
		manifest.SchemaVersion = defaultManifestVersion
	}
	manifest.Service.ID = strings.TrimSpace(manifest.Service.ID)
	manifest.Service.Name = strings.TrimSpace(manifest.Service.Name)
	manifest.Service.Description = strings.TrimSpace(manifest.Service.Description)
	manifest.Service.Version = strings.TrimSpace(manifest.Service.Version)
	manifest.Service.ProviderKind = strings.TrimSpace(manifest.Service.ProviderKind)
	if manifest.Service.ID != "" && !safeIdentifier(manifest.Service.ID) {
		findings = append(findings, fmt.Sprintf("services[%d].manifest.service.id must contain only letters, digits, underscore, dash, or dot", serviceIndex))
	}
	if _, exists := seenServiceIDs[manifest.Service.ID]; manifest.Service.ID != "" && exists {
		findings = append(findings, fmt.Sprintf("services[%d].manifest.service.id duplicates another service", serviceIndex))
	}
	if manifest.Service.ID != "" {
		seenServiceIDs[manifest.Service.ID] = struct{}{}
	}

	endpoint := strings.TrimSpace(service.ProviderEndpoint)
	if endpoint == "" {
		endpoint = strings.TrimSpace(manifest.Provider.Endpoint)
	}
	manifest.Provider.Endpoint = endpoint
	if manifest.Provider.NodeID == "" {
		manifest.Provider.NodeID = nodeID
	} else if manifest.Provider.NodeID != nodeID {
		findings = append(findings, fmt.Sprintf("services[%d].manifest.provider.node_id must match node.node_id", serviceIndex))
	}
	if manifest.Provider.HealthPath == "" {
		manifest.Provider.HealthPath = defaultHealthPath
	}
	for capabilityIndex := range manifest.Capabilities {
		capability := &manifest.Capabilities[capabilityIndex]
		capability.ID = strings.TrimSpace(capability.ID)
		if capability.ServiceID == "" {
			capability.ServiceID = manifest.Service.ID
		} else if capability.ServiceID != manifest.Service.ID {
			findings = append(findings, fmt.Sprintf("services[%d].manifest.capabilities[%d].service_id must match manifest.service.id", serviceIndex, capabilityIndex))
		}
		if capability.ID != "" && !safeIdentifier(capability.ID) {
			findings = append(findings, fmt.Sprintf("services[%d].manifest.capabilities[%d].id must contain only letters, digits, underscore, dash, or dot", serviceIndex, capabilityIndex))
		}
		if _, exists := seenCapabilityIDs[capability.ID]; capability.ID != "" && exists {
			findings = append(findings, fmt.Sprintf("services[%d].manifest.capabilities[%d].id duplicates another capability", serviceIndex, capabilityIndex))
		}
		if capability.ID != "" {
			seenCapabilityIDs[capability.ID] = struct{}{}
		}
	}
	if errs := contracts.ValidateProviderManifest(manifest); len(errs) > 0 {
		for _, err := range errs {
			findings = append(findings, fmt.Sprintf("services[%d].manifest.%s", serviceIndex, err))
		}
	}
	return manifest, findings
}

func leaseResourceRegistrations(nodeID, displayName string, resources []contracts.NodeResource) []contracts.RegisterResourceRequest {
	out := make([]contracts.RegisterResourceRequest, 0, len(resources))
	for _, resource := range resources {
		out = append(out, contracts.RegisterResourceRequest{
			ResourceID:  resource.ResourceID,
			Selector:    resourceSelector(resource),
			DisplayName: resourceDisplayName(displayName, resource, len(resources)),
			Status:      contracts.ResourceAvailable,
			NodeID:      nodeID,
			Tags:        append([]string(nil), resource.Tags...),
			Metadata:    cloneMap(resource.Metadata),
		})
	}
	return out
}

func resourceSelector(resource contracts.NodeResource) string {
	if resource.Metadata != nil {
		if selector, ok := resource.Metadata["selector"].(string); ok && selector != "" {
			return selector
		}
	}
	for _, tag := range resource.Tags {
		if tag != "" {
			return tag
		}
	}
	return resource.ResourceID
}

func resourceDisplayName(nodeDisplayName string, resource contracts.NodeResource, resourceCount int) string {
	if nodeDisplayName == "" {
		return resource.ResourceID
	}
	if resourceCount == 1 {
		return nodeDisplayName
	}
	return nodeDisplayName + " " + resource.ResourceID
}

func safeIdentifier(value string) bool {
	if value == "" {
		return true
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func cloneManifest(in contracts.ProviderManifest) contracts.ProviderManifest {
	out := in
	out.Service.Tags = append([]string(nil), in.Service.Tags...)
	out.Capabilities = make([]contracts.Capability, len(in.Capabilities))
	for i, capability := range in.Capabilities {
		out.Capabilities[i] = capability
		out.Capabilities[i].Tags = append([]string(nil), capability.Tags...)
		out.Capabilities[i].InputSchema = cloneMap(capability.InputSchema)
		out.Capabilities[i].OutputSchema = cloneMap(capability.OutputSchema)
		out.Capabilities[i].Examples = cloneExamples(capability.Examples)
		out.Capabilities[i].ResourceHints = append([]contracts.ResourceHint(nil), capability.ResourceHints...)
		out.Capabilities[i].ArtifactHints = append([]contracts.ArtifactHint(nil), capability.ArtifactHints...)
	}
	return out
}

func cloneExamples(in []map[string]any) []map[string]any {
	if in == nil {
		return nil
	}
	out := make([]map[string]any, len(in))
	for i, item := range in {
		out[i] = cloneMap(item)
	}
	return out
}

func cloneNodeResources(in []contracts.NodeResource) []contracts.NodeResource {
	if in == nil {
		return nil
	}
	out := make([]contracts.NodeResource, len(in))
	for i, resource := range in {
		out[i] = resource
		out[i].Tags = append([]string(nil), resource.Tags...)
		out[i].Metadata = cloneMap(resource.Metadata)
	}
	return out
}

func cloneNodeAuth(in []contracts.NodeAuthSubject) []contracts.NodeAuthSubject {
	if in == nil {
		return nil
	}
	out := make([]contracts.NodeAuthSubject, len(in))
	for i, subject := range in {
		out[i] = subject
		out[i].Scopes = append([]string(nil), subject.Scopes...)
		out[i].AllowedActions = append([]string(nil), subject.AllowedActions...)
	}
	return out
}

func serviceRuntimeAdapter(serviceIndex int, service ServiceBundle) (string, []string) {
	var findings []string
	runtimeAdapter := strings.TrimSpace(service.RuntimeAdapter)
	if runtimeAdapter == "" {
		switch {
		case service.Process != nil && service.Docker != nil:
			findings = append(findings, fmt.Sprintf("services[%d].runtime_adapter is required when both process and docker config are present", serviceIndex))
		case service.Process != nil:
			runtimeAdapter = "process"
		case service.Docker != nil:
			runtimeAdapter = "docker"
		default:
			runtimeAdapter = defaultRuntimeAdapter
		}
	}
	switch runtimeAdapter {
	case "fake":
		if service.Process != nil || service.Docker != nil {
			findings = append(findings, fmt.Sprintf("services[%d].runtime_adapter fake must not include process or docker config", serviceIndex))
		}
	case "process":
		if service.Process == nil || len(service.Process.Command) == 0 || strings.TrimSpace(service.Process.Command[0]) == "" {
			findings = append(findings, fmt.Sprintf("services[%d].process.command is required for process runtime", serviceIndex))
		}
		if service.Docker != nil {
			findings = append(findings, fmt.Sprintf("services[%d].runtime_adapter process must not include docker config", serviceIndex))
		}
	case "docker":
		if service.Docker == nil || strings.TrimSpace(service.Docker.ContainerName) == "" {
			findings = append(findings, fmt.Sprintf("services[%d].docker.container_name is required for docker runtime", serviceIndex))
		}
		if service.Process != nil {
			findings = append(findings, fmt.Sprintf("services[%d].runtime_adapter docker must not include process config", serviceIndex))
		}
	default:
		findings = append(findings, fmt.Sprintf("services[%d].runtime_adapter must be fake, process, or docker", serviceIndex))
	}
	return runtimeAdapter, findings
}

func cloneProcessConfig(in *contracts.ProcessRuntimeConfig) *contracts.ProcessRuntimeConfig {
	if in == nil {
		return nil
	}
	out := *in
	out.Command = append([]string(nil), in.Command...)
	out.Environment = cloneStringMap(in.Environment)
	return &out
}

func cloneDockerConfig(in *contracts.DockerRuntimeConfig) *contracts.DockerRuntimeConfig {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func clonePolicySeed(in *PolicySeedFile) *PolicySeedFile {
	if in == nil {
		return nil
	}
	out := &PolicySeedFile{
		APIKeys: append([]contracts.CreateAPIKeyRequest(nil), in.APIKeys...),
		Rules:   append([]contracts.CreatePolicyRuleRequest(nil), in.Rules...),
		Secrets: append([]contracts.CreateSecretRequest(nil), in.Secrets...),
	}
	for i := range out.APIKeys {
		out.APIKeys[i].Scopes = append([]string(nil), in.APIKeys[i].Scopes...)
	}
	return out
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
