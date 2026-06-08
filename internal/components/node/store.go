package node

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"pacp/internal/contracts"
)

var (
	ErrNotFound            = errors.New("node resource not found")
	ErrValidation          = errors.New("validation failed")
	ErrUnauthorized        = errors.New("unauthorized")
	ErrForbidden           = errors.New("forbidden")
	ErrRuntimeUnavailable  = errors.New("runtime adapter unavailable")
	ErrIdempotencyConflict = errors.New("idempotency conflict")
)

type Store struct {
	mu          sync.RWMutex
	now         func() time.Time
	config      contracts.NodeConfig
	authByToken map[string]contracts.NodeAuthSubject
	services    map[string]*serviceRecord
	idempotency map[string]idempotentStart
}

type serviceRecord struct {
	config contracts.NodeServiceConfig
	status string
}

type idempotentStart struct {
	fingerprint string
	serviceID   string
}

func LoadConfig(path string) (contracts.NodeConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return contracts.NodeConfig{}, err
	}
	var cfg contracts.NodeConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return contracts.NodeConfig{}, err
	}
	return cfg, nil
}

func NewStore(cfg contracts.NodeConfig) (*Store, error) {
	if cfg.NodeID == "" {
		return nil, fmt.Errorf("%w: node_id is required", ErrValidation)
	}
	if len(cfg.Services) == 0 {
		return nil, fmt.Errorf("%w: at least one service is required", ErrValidation)
	}
	store := &Store{
		now:         time.Now,
		config:      cfg,
		authByToken: map[string]contracts.NodeAuthSubject{},
		services:    map[string]*serviceRecord{},
		idempotency: map[string]idempotentStart{},
	}
	for _, subject := range cfg.Auth {
		if subject.Token == "" || subject.SubjectID == "" {
			return nil, fmt.Errorf("%w: auth token and subject_id are required", ErrValidation)
		}
		store.authByToken[subject.Token] = subject
	}
	for _, service := range cfg.Services {
		if service.ServiceID == "" {
			return nil, fmt.Errorf("%w: service_id is required", ErrValidation)
		}
		if service.RuntimeAdapter == "" {
			service.RuntimeAdapter = "fake"
		}
		if service.ProviderEndpoint == "" {
			return nil, fmt.Errorf("%w: provider_endpoint is required", ErrValidation)
		}
		status := service.InitialStatus
		if status == "" {
			status = "stopped"
		}
		store.services[service.ServiceID] = &serviceRecord{config: service, status: status}
	}
	return store, nil
}

func (s *Store) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

func (s *Store) CheckAuth(credential, action string) error {
	token, err := parseBearer(credential)
	if err != nil {
		return err
	}
	s.mu.RLock()
	subject, ok := s.authByToken[token]
	s.mu.RUnlock()
	if !ok {
		return ErrUnauthorized
	}
	for _, allowed := range subject.AllowedActions {
		if allowed == action || allowed == "*" {
			return nil
		}
	}
	return ErrForbidden
}

func (s *Store) Health() contracts.NodeHealth {
	return contracts.NodeHealth{
		Status:    "healthy",
		Version:   "v1",
		CheckedAt: s.formatNow(),
		Details:   map[string]any{"node_id": s.config.NodeID},
	}
}

func (s *Store) Resources() []contracts.NodeResource {
	s.mu.RLock()
	defer s.mu.RUnlock()
	resources := make([]contracts.NodeResource, len(s.config.Resources))
	copy(resources, s.config.Resources)
	return resources
}

func (s *Store) ListServices() []contracts.NodeService {
	s.mu.Lock()
	defer s.mu.Unlock()
	services := make([]contracts.NodeService, 0, len(s.services))
	for _, rec := range s.services {
		s.advanceFakeRuntimeLocked(rec)
		services = append(services, serviceProjection(rec))
	}
	return services
}

func (s *Store) GetService(serviceID string) (contracts.NodeService, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.services[serviceID]
	if !ok {
		return contracts.NodeService{}, ErrNotFound
	}
	s.advanceFakeRuntimeLocked(rec)
	return serviceProjection(rec), nil
}

func (s *Store) StartService(serviceID, idempotencyKey string) (contracts.NodeService, int, error) {
	fingerprint := "start:" + serviceID
	s.mu.Lock()
	defer s.mu.Unlock()
	if idempotencyKey != "" {
		if existing, ok := s.idempotency[idempotencyKey]; ok {
			if existing.fingerprint != fingerprint {
				return contracts.NodeService{}, 0, ErrIdempotencyConflict
			}
		}
	}
	rec, ok := s.services[serviceID]
	if !ok {
		return contracts.NodeService{}, 0, ErrNotFound
	}
	if rec.config.RuntimeAdapter != "fake" && rec.config.RuntimeAdapter != "process" && rec.config.RuntimeAdapter != "docker" {
		return contracts.NodeService{}, 0, ErrRuntimeUnavailable
	}
	status := 200
	switch rec.status {
	case "running":
		status = 200
	case "starting":
		status = 202
	default:
		rec.status = "starting"
		status = 202
	}
	if idempotencyKey != "" {
		s.idempotency[idempotencyKey] = idempotentStart{fingerprint: fingerprint, serviceID: serviceID}
	}
	return serviceProjection(rec), status, nil
}

func (s *Store) StopService(serviceID string) (contracts.NodeService, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.services[serviceID]
	if !ok {
		return contracts.NodeService{}, ErrNotFound
	}
	rec.status = "stopped"
	return serviceProjection(rec), nil
}

func (s *Store) advanceFakeRuntimeLocked(rec *serviceRecord) {
	if rec.config.RuntimeAdapter == "fake" && rec.status == "starting" {
		rec.status = "running"
	}
}

func (s *Store) formatNow() string {
	return s.now().UTC().Format(time.RFC3339)
}

func parseBearer(credential string) (string, error) {
	parts := strings.Split(credential, " ")
	if len(parts) != 2 || parts[0] != "Bearer" || parts[1] == "" || strings.ContainsAny(parts[1], " \t\r\n") {
		return "", ErrUnauthorized
	}
	return parts[1], nil
}

func serviceProjection(rec *serviceRecord) contracts.NodeService {
	return contracts.NodeService{
		ServiceID:        rec.config.ServiceID,
		Status:           rec.status,
		RuntimeAdapter:   rec.config.RuntimeAdapter,
		ProviderEndpoint: rec.config.ProviderEndpoint,
		Manifest:         rec.config.Manifest,
		Links:            serviceLinks(rec.config.ServiceID, rec.status),
	}
}

func serviceLinks(serviceID, status string) map[string]any {
	switch status {
	case "stopped":
		return map[string]any{"start": map[string]any{"method": "POST", "href": "/v1/node/services/" + serviceID + "/start", "description": "Start service."}}
	case "starting":
		return map[string]any{"status": map[string]any{"method": "GET", "href": "/v1/node/services/" + serviceID, "description": "Poll service status."}}
	default:
		return map[string]any{"stop": map[string]any{"method": "POST", "href": "/v1/node/services/" + serviceID + "/stop", "description": "Stop service."}}
	}
}
