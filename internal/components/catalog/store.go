package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"pacp/internal/contracts"
)

var (
	ErrDuplicateService    = errors.New("duplicate service id")
	ErrDuplicateCapability = errors.New("duplicate capability id")
	ErrInvalidManifest     = errors.New("invalid manifest")
	ErrNotFound            = errors.New("not found")
	ErrInvalidCursor       = errors.New("invalid cursor")
)

type Store struct {
	mu           sync.RWMutex
	services     map[string]contracts.Service
	providers    map[string]contracts.Provider
	capabilities map[string]contracts.Capability
	routes       map[string]contracts.CapabilityRoute
	snapshotPath string
}

type snapshotFile struct {
	Version      int                             `json:"version"`
	Services     map[string]contracts.Service    `json:"services"`
	Providers    map[string]contracts.Provider   `json:"providers"`
	Capabilities map[string]contracts.Capability `json:"capabilities"`
}

type CapabilityFilter struct {
	CapabilityID         string
	ServiceID            string
	Tag                  string
	ExecutionMode        string
	ResourceSelector     string
	VisibleCapabilityIDs []string
	Cursor               string
	Limit                int
}

func NewPersistentStore(path string) (*Store, error) {
	store := NewStore()
	store.snapshotPath = path
	if path == "" {
		return store, nil
	}
	if err := store.loadSnapshot(path); err != nil {
		return nil, err
	}
	return store, nil
}

func NewStore() *Store {
	return &Store{
		services:     map[string]contracts.Service{},
		providers:    map[string]contracts.Provider{},
		capabilities: map[string]contracts.Capability{},
		routes:       map[string]contracts.CapabilityRoute{},
	}
}

func (s *Store) HealthDetails() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return map[string]any{
		"store_backend":     backendLabel(s.snapshotPath),
		"service_count":     len(s.services),
		"provider_count":    len(s.providers),
		"capability_count":  len(s.capabilities),
		"schema_version":    "v1",
		"catalog_available": true,
	}
}

func (s *Store) Metrics() contracts.ComponentMetrics {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return contracts.NewComponentMetrics("catalog", []contracts.MetricSample{
		contracts.CountMetric("catalog_services_total", len(s.services), nil),
		contracts.CountMetric("catalog_providers_total", len(s.providers), nil),
		contracts.CountMetric("catalog_capabilities_total", len(s.capabilities), nil),
	})
}

func (s *Store) RegisterManifest(manifest contracts.ProviderManifest) ([]string, error) {
	if errs := contracts.ValidateProviderManifest(manifest); len(errs) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrInvalidManifest, strings.Join(errs, "; "))
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.services[manifest.Service.ID]; exists {
		return nil, ErrDuplicateService
	}
	for _, capability := range manifest.Capabilities {
		if _, exists := s.capabilities[capability.ID]; exists {
			return nil, ErrDuplicateCapability
		}
	}

	provider := manifest.Provider
	if provider.HealthPath == "" {
		provider.HealthPath = "/v1/provider/health"
	}
	s.services[manifest.Service.ID] = manifest.Service
	s.providers[manifest.Service.ID] = provider

	ids := make([]string, 0, len(manifest.Capabilities))
	for _, capability := range manifest.Capabilities {
		capability.ServiceID = manifest.Service.ID
		s.capabilities[capability.ID] = capability
		s.routes[capability.ID] = buildRoute(manifest.Service.ID, capability, provider)
		ids = append(ids, capability.ID)
	}
	sort.Strings(ids)
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *Store) ListServices() []contracts.Service {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := make([]string, 0, len(s.services))
	for id := range s.services {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	services := make([]contracts.Service, 0, len(ids))
	for _, id := range ids {
		services = append(services, s.services[id])
	}
	return services
}

func (s *Store) GetService(id string) (contracts.Service, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	service, ok := s.services[id]
	return service, ok
}

func (s *Store) ListCapabilities(filter CapabilityFilter) ([]contracts.CatalogCapabilityRecord, error) {
	if filter.Cursor != "" {
		return nil, ErrInvalidCursor
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := make([]string, 0, len(s.capabilities))
	for id := range s.capabilities {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	visible := map[string]struct{}{}
	for _, id := range filter.VisibleCapabilityIDs {
		visible[id] = struct{}{}
	}

	records := make([]contracts.CatalogCapabilityRecord, 0, len(ids))
	for _, id := range ids {
		capability := s.capabilities[id]
		if filter.CapabilityID != "" && capability.ID != filter.CapabilityID {
			continue
		}
		if len(visible) > 0 {
			if _, ok := visible[capability.ID]; !ok {
				continue
			}
		}
		if filter.ServiceID != "" && capability.ServiceID != filter.ServiceID {
			continue
		}
		if filter.Tag != "" && !contains(capability.Tags, filter.Tag) && !contains(s.services[capability.ServiceID].Tags, filter.Tag) {
			continue
		}
		if filter.ExecutionMode != "" && capability.ExecutionMode != filter.ExecutionMode {
			continue
		}
		if filter.ResourceSelector != "" && !hasResourceSelector(capability.ResourceHints, filter.ResourceSelector) {
			continue
		}
		records = append(records, contracts.CatalogCapabilityRecord{
			Capability: capability,
			Route:      s.routes[id],
			Service:    s.services[capability.ServiceID],
		})
	}
	if filter.Limit > 0 && len(records) > filter.Limit {
		records = records[:filter.Limit]
	}
	return records, nil
}

func (s *Store) GetCapability(id string) (contracts.CatalogCapabilityRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	capability, ok := s.capabilities[id]
	if !ok {
		return contracts.CatalogCapabilityRecord{}, false
	}
	return contracts.CatalogCapabilityRecord{
		Capability: capability,
		Route:      s.routes[id],
		Service:    s.services[capability.ServiceID],
	}, true
}

func (s *Store) GetRoute(capabilityID string) (contracts.CapabilityRoute, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	route, ok := s.routes[capabilityID]
	return route, ok
}

func (s *Store) Tags() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	seen := map[string]struct{}{}
	for _, service := range s.services {
		for _, tag := range service.Tags {
			seen[tag] = struct{}{}
		}
	}
	for _, capability := range s.capabilities {
		for _, tag := range capability.Tags {
			seen[tag] = struct{}{}
		}
	}
	tags := make([]string, 0, len(seen))
	for tag := range seen {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}

func buildRoute(serviceID string, capability contracts.Capability, provider contracts.Provider) contracts.CapabilityRoute {
	route := contracts.CapabilityRoute{
		CapabilityID:       capability.ID,
		ServiceID:          serviceID,
		ProviderEndpoint:   provider.Endpoint,
		ProviderHealthPath: provider.HealthPath,
		ProviderInvokePath: "/v1/provider/capabilities/" + capability.ID + "/invoke",
		NodeManaged:        provider.NodeID != "",
		ServiceStartMode:   "manual",
		ResourceHints:      capability.ResourceHints,
		ArtifactHints:      capability.ArtifactHints,
	}
	if provider.NodeID != "" {
		nodeID := provider.NodeID
		route.NodeID = &nodeID
		route.ServiceStartMode = "on_demand"
	}
	return route
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func hasResourceSelector(hints []contracts.ResourceHint, selector string) bool {
	for _, hint := range hints {
		if hint.Selector == selector {
			return true
		}
	}
	return false
}

func (s *Store) loadSnapshot(path string) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var snapshot snapshotFile
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return fmt.Errorf("%w: invalid catalog snapshot: %v", ErrInvalidManifest, err)
	}
	s.services = map[string]contracts.Service{}
	for id, service := range snapshot.Services {
		if service.ID == "" {
			service.ID = id
		}
		s.services[service.ID] = cloneService(service)
	}
	s.providers = map[string]contracts.Provider{}
	for id, provider := range snapshot.Providers {
		s.providers[id] = provider
	}
	s.capabilities = map[string]contracts.Capability{}
	s.routes = map[string]contracts.CapabilityRoute{}
	for id, capability := range snapshot.Capabilities {
		if capability.ID == "" {
			capability.ID = id
		}
		s.capabilities[capability.ID] = cloneCapability(capability)
		provider := s.providers[capability.ServiceID]
		if provider.HealthPath == "" {
			provider.HealthPath = "/v1/provider/health"
		}
		s.routes[capability.ID] = buildRoute(capability.ServiceID, capability, provider)
	}
	return nil
}

func (s *Store) saveLocked() error {
	if s.snapshotPath == "" {
		return nil
	}
	snapshot := snapshotFile{
		Version:      1,
		Services:     map[string]contracts.Service{},
		Providers:    map[string]contracts.Provider{},
		Capabilities: map[string]contracts.Capability{},
	}
	for id, service := range s.services {
		snapshot.Services[id] = cloneService(service)
	}
	for id, provider := range s.providers {
		snapshot.Providers[id] = provider
	}
	for id, capability := range s.capabilities {
		snapshot.Capabilities[id] = cloneCapability(capability)
	}
	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.snapshotPath), 0o755); err != nil {
		return err
	}
	tmpPath := s.snapshotPath + ".tmp"
	if err := os.WriteFile(tmpPath, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.snapshotPath)
}

func cloneService(service contracts.Service) contracts.Service {
	raw, _ := json.Marshal(service)
	var cloned contracts.Service
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}

func cloneCapability(capability contracts.Capability) contracts.Capability {
	raw, _ := json.Marshal(capability)
	var cloned contracts.Capability
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}

func backendLabel(path string) string {
	if path == "" {
		return "memory"
	}
	return "file_snapshot"
}
