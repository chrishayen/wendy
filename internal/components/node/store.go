package node

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"pacp/internal/contracts"
)

var (
	ErrNotFound            = errors.New("node resource not found")
	ErrValidation          = errors.New("validation failed")
	ErrUnauthorized        = errors.New("unauthorized")
	ErrForbidden           = errors.New("forbidden")
	ErrRuntimeUnavailable  = errors.New("runtime adapter unavailable")
	ErrMissingIdempotency  = errors.New("missing idempotency key")
	ErrIdempotencyConflict = errors.New("idempotency conflict")
)

type Store struct {
	mu          sync.RWMutex
	now         func() time.Time
	config      contracts.NodeConfig
	authByToken map[string]contracts.NodeAuthSubject
	services    map[string]*serviceRecord
	idempotency map[string]idempotentStart
	startCount  int
	stopCount   int
}

type serviceRecord struct {
	config              contracts.NodeServiceConfig
	status              string
	process             *processRuntime
	dockerReadyDeadline time.Time
}

type idempotentStart struct {
	fingerprint string
	serviceID   string
}

type processRuntime struct {
	cmd           *exec.Cmd
	done          chan error
	readyDeadline time.Time
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
		if err := validateRuntimeConfig(service); err != nil {
			return nil, err
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

func (s *Store) Metrics() contracts.ComponentMetrics {
	s.mu.Lock()
	defer s.mu.Unlock()
	samples := []contracts.MetricSample{
		contracts.CountMetric("node_resources_total", len(s.config.Resources), map[string]string{"node_id": s.config.NodeID}),
		contracts.CountMetric("node_services_total", len(s.services), map[string]string{"node_id": s.config.NodeID}),
		contracts.CountMetric("node_service_start_total", s.startCount, map[string]string{"node_id": s.config.NodeID}),
		contracts.CountMetric("node_service_stop_total", s.stopCount, map[string]string{"node_id": s.config.NodeID}),
	}
	servicesByStatus := map[string]int{}
	servicesByAdapter := map[string]int{}
	for _, rec := range s.services {
		s.advanceRuntimeLocked(rec)
		servicesByStatus[rec.status]++
		servicesByAdapter[rec.config.RuntimeAdapter]++
	}
	for status, count := range servicesByStatus {
		samples = append(samples, contracts.CountMetric("node_services_by_status", count, map[string]string{"node_id": s.config.NodeID, "status": status}))
	}
	for adapter, count := range servicesByAdapter {
		samples = append(samples, contracts.CountMetric("node_services_by_adapter", count, map[string]string{"node_id": s.config.NodeID, "adapter": adapter}))
	}
	return contracts.NewComponentMetrics("node", samples)
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
		s.advanceRuntimeLocked(rec)
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
	s.advanceRuntimeLocked(rec)
	return serviceProjection(rec), nil
}

func (s *Store) StartService(serviceID, idempotencyKey string) (contracts.NodeService, int, error) {
	if idempotencyKey == "" {
		return contracts.NodeService{}, 0, ErrMissingIdempotency
	}
	fingerprint := "start:" + serviceID
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.idempotency[idempotencyKey]; ok {
		if existing.fingerprint != fingerprint {
			return contracts.NodeService{}, 0, ErrIdempotencyConflict
		}
	}
	rec, ok := s.services[serviceID]
	if !ok {
		return contracts.NodeService{}, 0, ErrNotFound
	}
	s.startCount++
	s.advanceRuntimeLocked(rec)
	status := 200
	switch rec.config.RuntimeAdapter {
	case "fake":
		switch rec.status {
		case "running":
			status = 200
		case "starting":
			status = 202
		default:
			rec.status = "starting"
			status = 202
		}
	case "docker":
		switch rec.status {
		case "running":
			status = 200
		case "starting":
			status = 202
		default:
			if err := startDockerRuntime(rec.config); err != nil {
				rec.status = "failed"
				return contracts.NodeService{}, 0, err
			}
			rec.status = "starting"
			rec.dockerReadyDeadline = s.now().Add(dockerReadyTimeout(rec.config))
			status = 202
		}
	case "process":
		switch rec.status {
		case "running":
			status = 200
		case "starting":
			status = 202
		default:
			process, err := startProcessRuntime(rec.config, s.now())
			if err != nil {
				rec.status = "failed"
				return contracts.NodeService{}, 0, err
			}
			rec.process = process
			rec.status = "starting"
			status = 202
		}
	default:
		return contracts.NodeService{}, 0, ErrRuntimeUnavailable
	}
	s.idempotency[idempotencyKey] = idempotentStart{fingerprint: fingerprint, serviceID: serviceID}
	return serviceProjection(rec), status, nil
}

func (s *Store) StopService(serviceID string) (contracts.NodeService, error) {
	s.mu.Lock()
	rec, ok := s.services[serviceID]
	if !ok {
		s.mu.Unlock()
		return contracts.NodeService{}, ErrNotFound
	}
	s.advanceRuntimeLocked(rec)
	if rec.config.RuntimeAdapter == "docker" {
		config := rec.config
		s.mu.Unlock()
		if err := stopDockerRuntime(config); err != nil {
			return contracts.NodeService{}, err
		}
		s.mu.Lock()
		rec = s.services[serviceID]
		rec.status = "stopped"
		rec.dockerReadyDeadline = time.Time{}
		s.stopCount++
		service := serviceProjection(rec)
		s.mu.Unlock()
		return service, nil
	}
	process := rec.process
	rec.process = nil
	rec.status = "stopped"
	s.stopCount++
	service := serviceProjection(rec)
	timeout := processStopTimeout(rec.config)
	s.mu.Unlock()
	stopProcessRuntime(process, timeout)
	return service, nil
}

func (s *Store) advanceRuntimeLocked(rec *serviceRecord) {
	switch rec.config.RuntimeAdapter {
	case "fake":
		if rec.status == "starting" {
			rec.status = "running"
		}
	case "docker":
		s.advanceDockerRuntimeLocked(rec)
	case "process":
		s.advanceProcessRuntimeLocked(rec)
	}
}

func (s *Store) advanceDockerRuntimeLocked(rec *serviceRecord) {
	if rec.status != "starting" && rec.status != "running" {
		return
	}
	running, err := dockerContainerRunning(rec.config)
	if err != nil {
		if rec.status == "starting" && !rec.dockerReadyDeadline.IsZero() && s.now().After(rec.dockerReadyDeadline) {
			rec.status = "failed"
		}
		return
	}
	if !running {
		rec.status = "stopped"
		return
	}
	if rec.status == "running" {
		return
	}
	if rec.config.Docker == nil || rec.config.Docker.ReadyURL == "" || processReady(rec.config.Docker.ReadyURL) {
		rec.status = "running"
		return
	}
	if !rec.dockerReadyDeadline.IsZero() && s.now().After(rec.dockerReadyDeadline) {
		rec.status = "failed"
	}
}

func (s *Store) advanceProcessRuntimeLocked(rec *serviceRecord) {
	if rec.process == nil {
		return
	}
	select {
	case err := <-rec.process.done:
		rec.process = nil
		if rec.status != "stopped" {
			if err == nil {
				rec.status = "stopped"
			} else {
				rec.status = "failed"
			}
		}
		return
	default:
	}
	if rec.status != "starting" {
		return
	}
	if rec.config.Process == nil || rec.config.Process.ReadyURL == "" {
		rec.status = "running"
		return
	}
	if processReady(rec.config.Process.ReadyURL) {
		rec.status = "running"
		return
	}
	if !rec.process.readyDeadline.IsZero() && s.now().After(rec.process.readyDeadline) {
		if rec.process.cmd != nil && rec.process.cmd.Process != nil {
			_ = rec.process.cmd.Process.Kill()
		}
		rec.process = nil
		rec.status = "failed"
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

func validateRuntimeConfig(service contracts.NodeServiceConfig) error {
	switch service.RuntimeAdapter {
	case "fake":
		return nil
	case "docker":
		if service.Docker == nil || service.Docker.ContainerName == "" {
			return fmt.Errorf("%w: docker container_name is required", ErrValidation)
		}
		return nil
	case "process":
		if service.Process == nil || len(service.Process.Command) == 0 || service.Process.Command[0] == "" {
			return fmt.Errorf("%w: process command is required", ErrValidation)
		}
		return nil
	default:
		return ErrRuntimeUnavailable
	}
}

func startProcessRuntime(service contracts.NodeServiceConfig, now time.Time) (*processRuntime, error) {
	cfg := service.Process
	if cfg == nil || len(cfg.Command) == 0 || cfg.Command[0] == "" {
		return nil, fmt.Errorf("%w: process command is required", ErrRuntimeUnavailable)
	}
	cmd := exec.Command(cfg.Command[0], cfg.Command[1:]...)
	if cfg.WorkingDirectory != "" {
		cmd.Dir = cfg.WorkingDirectory
	}
	cmd.Env = os.Environ()
	for key, value := range cfg.Environment {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRuntimeUnavailable, err)
	}
	runtime := &processRuntime{
		cmd:           cmd,
		done:          make(chan error, 1),
		readyDeadline: now.Add(processReadyTimeout(service)),
	}
	go func() {
		runtime.done <- cmd.Wait()
	}()
	return runtime, nil
}

func stopProcessRuntime(runtime *processRuntime, timeout time.Duration) {
	if runtime == nil || runtime.cmd == nil || runtime.cmd.Process == nil {
		return
	}
	_ = runtime.cmd.Process.Signal(syscall.SIGTERM)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-runtime.done:
		return
	case <-timer.C:
		_ = runtime.cmd.Process.Kill()
		<-runtime.done
	}
}

func processReady(rawURL string) bool {
	client := http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(rawURL)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func processReadyTimeout(service contracts.NodeServiceConfig) time.Duration {
	if service.Process == nil || service.Process.ReadyTimeoutSeconds <= 0 {
		return 15 * time.Second
	}
	return time.Duration(service.Process.ReadyTimeoutSeconds) * time.Second
}

func processStopTimeout(service contracts.NodeServiceConfig) time.Duration {
	if service.Process == nil || service.Process.StopTimeoutSeconds <= 0 {
		return 5 * time.Second
	}
	return time.Duration(service.Process.StopTimeoutSeconds) * time.Second
}

func startDockerRuntime(service contracts.NodeServiceConfig) error {
	cfg := service.Docker
	if cfg == nil || cfg.ContainerName == "" {
		return fmt.Errorf("%w: docker container_name is required", ErrRuntimeUnavailable)
	}
	if _, err := runDockerCommand(service, "start", cfg.ContainerName); err != nil {
		return err
	}
	return nil
}

func stopDockerRuntime(service contracts.NodeServiceConfig) error {
	cfg := service.Docker
	if cfg == nil || cfg.ContainerName == "" {
		return fmt.Errorf("%w: docker container_name is required", ErrRuntimeUnavailable)
	}
	args := []string{"stop"}
	if cfg.StopTimeoutSeconds > 0 {
		args = append(args, "--time", fmt.Sprintf("%d", cfg.StopTimeoutSeconds))
	}
	args = append(args, cfg.ContainerName)
	_, err := runDockerCommand(service, args...)
	return err
}

func dockerContainerRunning(service contracts.NodeServiceConfig) (bool, error) {
	cfg := service.Docker
	if cfg == nil || cfg.ContainerName == "" {
		return false, fmt.Errorf("%w: docker container_name is required", ErrRuntimeUnavailable)
	}
	output, err := runDockerCommand(service, "inspect", "--format", "{{.State.Running}}", cfg.ContainerName)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) == "true", nil
}

func runDockerCommand(service contracts.NodeServiceConfig, args ...string) (string, error) {
	binary := "docker"
	if service.Docker != nil && service.Docker.Binary != "" {
		binary = service.Docker.Binary
	}
	cmd := exec.Command(binary, args...)
	raw, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(raw))
	if err != nil {
		if output == "" {
			output = err.Error()
		}
		return "", fmt.Errorf("%w: docker %s failed: %s", ErrRuntimeUnavailable, strings.Join(args, " "), output)
	}
	return output, nil
}

func dockerReadyTimeout(service contracts.NodeServiceConfig) time.Duration {
	if service.Docker == nil || service.Docker.ReadyTimeoutSeconds <= 0 {
		return 30 * time.Second
	}
	return time.Duration(service.Docker.ReadyTimeoutSeconds) * time.Second
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
