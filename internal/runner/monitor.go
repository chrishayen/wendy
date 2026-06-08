package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"pacp/internal/contracts"
)

type runnerStats struct {
	mu                        sync.RWMutex
	runCounts                 map[string]int
	errorCounts               map[string]int
	activeJobs                map[string]struct{}
	lastPollAt                time.Time
	lastSuccessfulHeartbeatAt time.Time
	lastErrorAt               time.Time
	lastError                 string
	heartbeats                int
}

type dependencyStatus struct {
	Name       string `json:"name"`
	Required   bool   `json:"required"`
	Configured bool   `json:"configured"`
	Reachable  bool   `json:"reachable"`
	Status     string `json:"status"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Error      string `json:"error,omitempty"`
}

type dependencyTarget struct {
	name     string
	baseURL  string
	path     string
	required bool
}

func newRunnerStats() *runnerStats {
	return &runnerStats{
		runCounts:   map[string]int{},
		errorCounts: map[string]int{},
		activeJobs:  map[string]struct{}{},
	}
}

func (s *runnerStats) RecordRunStart() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastPollAt = time.Now().UTC()
}

func (s *runnerStats) BeginJob(jobID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeJobs[jobID] = struct{}{}
}

func (s *runnerStats) EndJob(jobID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.activeJobs, jobID)
}

func (s *runnerStats) RecordRunResult(result, errorCode string, err error) {
	if s == nil {
		return
	}
	result = strings.TrimSpace(result)
	if result == "" {
		result = "unknown"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runCounts[result]++
	if err != nil {
		errorCode = strings.TrimSpace(errorCode)
		if errorCode == "" {
			errorCode = "runner_error"
		}
		s.errorCounts[errorCode]++
		s.lastErrorAt = time.Now().UTC()
		s.lastError = err.Error()
	}
}

func (s *runnerStats) RecordHeartbeat(jobID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.heartbeats++
	s.lastSuccessfulHeartbeatAt = time.Now().UTC()
}

func (s *runnerStats) HealthDetails(workerID string) map[string]any {
	if s == nil {
		return map[string]any{"worker_id": workerID}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	details := map[string]any{
		"worker_id":   workerID,
		"active_jobs": len(s.activeJobs),
		"run_counts":  copyIntMap(s.runCounts),
	}
	if !s.lastPollAt.IsZero() {
		details["last_poll_at"] = s.lastPollAt.Format(time.RFC3339)
	}
	if !s.lastSuccessfulHeartbeatAt.IsZero() {
		details["last_successful_heartbeat_at"] = s.lastSuccessfulHeartbeatAt.Format(time.RFC3339)
	}
	if !s.lastErrorAt.IsZero() {
		details["last_error_at"] = s.lastErrorAt.Format(time.RFC3339)
		details["last_error"] = s.lastError
	}
	return details
}

func (s *runnerStats) Samples() []contracts.MetricSample {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	samples := []contracts.MetricSample{
		contracts.GaugeMetric("runner_active_jobs", float64(len(s.activeJobs)), "count", nil),
		contracts.CountMetric("runner_job_heartbeats_total", s.heartbeats, nil),
	}
	if !s.lastSuccessfulHeartbeatAt.IsZero() {
		samples = append(samples, contracts.GaugeMetric("runner_last_successful_heartbeat_unix_seconds", float64(s.lastSuccessfulHeartbeatAt.Unix()), "seconds", nil))
	}
	results := sortedKeys(s.runCounts)
	for _, result := range results {
		samples = append(samples, contracts.CountMetric("runner_run_once_total", s.runCounts[result], map[string]string{"result": result}))
	}
	codes := sortedKeys(s.errorCounts)
	for _, code := range codes {
		samples = append(samples, contracts.CountMetric("runner_errors_total", s.errorCounts[code], map[string]string{"code": code}))
	}
	return samples
}

func (r *Runner) Health(ctx context.Context) contracts.ComponentHealth {
	dependencies := r.dependencyStatuses(ctx)
	details := r.stats.HealthDetails(r.cfg.WorkerID)
	details["dependencies"] = dependencies
	health := contracts.NewComponentHealth("runner", details)
	for _, dependency := range dependencies {
		if dependency.Required && !dependency.Reachable {
			health.Status = "unhealthy"
			return health
		}
		if dependency.Configured && !dependency.Reachable {
			health.Status = "degraded"
		}
	}
	return health
}

func (r *Runner) Metrics(ctx context.Context) contracts.ComponentMetrics {
	samples := r.stats.Samples()
	for _, dependency := range r.dependencyStatuses(ctx) {
		labels := map[string]string{
			"dependency": dependency.Name,
			"required":   boolLabel(dependency.Required),
			"status":     dependency.Status,
		}
		samples = append(samples, contracts.GaugeMetric("runner_dependency_configured", metricBool(dependency.Configured), "boolean", labels))
		samples = append(samples, contracts.GaugeMetric("runner_dependency_reachable", metricBool(dependency.Reachable), "boolean", labels))
	}
	return contracts.NewComponentMetrics("runner", samples)
}

func (r *Runner) dependencyStatuses(ctx context.Context) []dependencyStatus {
	targets := []dependencyTarget{
		{name: "jobs", baseURL: r.cfg.JobsURL, path: "/v1/jobs/health", required: true},
		{name: "leases", baseURL: r.cfg.LeasesURL, path: "/v1/leases/health", required: true},
		{name: "artifacts", baseURL: r.cfg.ArtifactsURL, path: "/v1/artifacts/health", required: true},
	}
	if r.cfg.PolicyURL != "" {
		targets = append(targets, dependencyTarget{name: "policy", baseURL: r.cfg.PolicyURL, path: "/v1/policy/health"})
	}
	if r.cfg.NodeURL != "" {
		targets = append(targets, dependencyTarget{name: "node", baseURL: r.cfg.NodeURL, path: "/v1/node/health"})
	}
	nodeIDs := make([]string, 0, len(r.cfg.NodeURLs))
	for nodeID := range r.cfg.NodeURLs {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Strings(nodeIDs)
	for _, nodeID := range nodeIDs {
		targets = append(targets, dependencyTarget{name: "node:" + nodeID, baseURL: r.cfg.NodeURLs[nodeID], path: "/v1/node/health"})
	}

	statuses := make([]dependencyStatus, 0, len(targets))
	for _, target := range targets {
		statuses = append(statuses, r.checkDependency(ctx, target))
	}
	return statuses
}

func (r *Runner) checkDependency(ctx context.Context, target dependencyTarget) dependencyStatus {
	status := dependencyStatus{Name: target.name, Required: target.required}
	baseURL := strings.TrimRight(strings.TrimSpace(target.baseURL), "/")
	if baseURL == "" {
		status.Status = "missing"
		status.Error = "base URL is not configured"
		return status
	}
	status.Configured = true
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+target.path, nil)
	if err != nil {
		status.Status = "invalid"
		status.Error = err.Error()
		return status
	}
	r.addAuth(req)
	resp, err := r.client.Do(req)
	if err != nil {
		status.Status = "unreachable"
		status.Error = err.Error()
		return status
	}
	defer resp.Body.Close()
	status.HTTPStatus = resp.StatusCode

	var envelope struct {
		OK    bool                      `json:"ok"`
		Data  contracts.ComponentHealth `json:"data"`
		Error contracts.ErrorObject     `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		status.Status = "invalid_response"
		status.Error = err.Error()
		return status
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status.Status = "unreachable"
		status.Error = envelope.Error.Message
		if status.Error == "" {
			status.Error = resp.Status
		}
		return status
	}
	if !envelope.OK || envelope.Data.Status == "" {
		status.Status = "invalid_response"
		status.Error = "health response was not ok"
		return status
	}
	status.Status = envelope.Data.Status
	status.Reachable = envelope.Data.Status == "healthy"
	if !status.Reachable {
		status.Error = fmt.Sprintf("reported status %s", envelope.Data.Status)
	}
	return status
}

func classifyRunnerError(err error) string {
	if err == nil {
		return ""
	}
	var componentErr componentError
	if errors.As(err, &componentErr) && componentErr.Code != "" {
		return componentErr.Code
	}
	var keepaliveErr keepaliveFailure
	if errors.As(err, &keepaliveErr) && keepaliveErr.Code != "" {
		return keepaliveErr.Code
	}
	if errors.Is(err, context.Canceled) {
		return "context_canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "context_deadline_exceeded"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "execution_plan"):
		return "invalid_execution_plan"
	case strings.Contains(message, "policy"):
		return "policy_denied"
	case strings.Contains(message, "lease") || strings.Contains(message, "resource"):
		return "resource_unavailable"
	case strings.Contains(message, "node"):
		return "node_unavailable"
	case strings.Contains(message, "provider"):
		return "provider_unavailable"
	case strings.Contains(message, "artifact"):
		return "artifact_upload_failed"
	default:
		return "runner_error"
	}
}

func copyIntMap(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func sortedKeys(in map[string]int) []string {
	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func boolLabel(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func metricBool(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
