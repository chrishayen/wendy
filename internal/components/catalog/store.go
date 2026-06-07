package catalog

import (
	"errors"
	"fmt"
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

func NewStore() *Store {
	return &Store{
		services:     map[string]contracts.Service{},
		providers:    map[string]contracts.Provider{},
		capabilities: map[string]contracts.Capability{},
		routes:       map[string]contracts.CapabilityRoute{},
	}
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
