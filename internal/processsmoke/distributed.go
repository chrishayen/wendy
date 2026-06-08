package processsmoke

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"pacp/internal/contracts"
)

type Config struct {
	RepoRoot string
	GoBinary string
	Timeout  time.Duration
}

type Report struct {
	OK         bool    `json:"ok"`
	Checks     []Check `json:"checks"`
	JobID      string  `json:"job_id,omitempty"`
	ArtifactID string  `json:"artifact_id,omitempty"`
}

type Check struct {
	Name       string `json:"name"`
	OK         bool   `json:"ok"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Error      string `json:"error,omitempty"`
}

type processHandle struct {
	name   string
	cmd    *exec.Cmd
	stdout bytes.Buffer
	stderr bytes.Buffer
	done   chan error
}

type smokePorts struct {
	catalog      string
	jobs         string
	leases       string
	artifacts    string
	policy       string
	gateway      string
	nodeRegistry string
	node         string
	provider     string
}

const (
	processAgentToken     = "token_process_agent"
	processComponentToken = "token_process_component"
	processRunnerToken    = "token_process_runner"
	processAgentID        = "sub_process_agent"
	processComponentID    = "sub_process_component"
	processRunnerID       = "sub_process_runner"
	processNodeID         = "node_linux_gpu"
	processServiceID      = "svc_fake_provider"
	processCapabilityID   = "cap_fake_image"
)

func RunDistributed(ctx context.Context, cfg Config) Report {
	report := Report{OK: true}
	if cfg.RepoRoot == "" {
		cfg.RepoRoot = "."
	}
	if cfg.GoBinary == "" {
		cfg.GoBinary = "go"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 20 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	root, err := filepath.Abs(cfg.RepoRoot)
	if err != nil {
		report.add(Check{Name: "process.repo_root", Error: err.Error()})
		return report
	}
	workDir, err := os.MkdirTemp("", "pacp-process-distributed-*")
	if err != nil {
		report.add(Check{Name: "process.workdir", Error: err.Error()})
		return report
	}
	defer os.RemoveAll(workDir)

	ports, err := reservePorts(9)
	if err != nil {
		report.add(Check{Name: "process.ports", Error: err.Error()})
		return report
	}
	p := smokePorts{
		catalog:      ports[0],
		jobs:         ports[1],
		leases:       ports[2],
		artifacts:    ports[3],
		policy:       ports[4],
		gateway:      ports[5],
		nodeRegistry: ports[6],
		node:         ports[7],
		provider:     ports[8],
	}
	files, err := writeInputs(workDir, p)
	if err != nil {
		report.add(Check{Name: "process.inputs", Error: err.Error()})
		return report
	}

	processes := []*processHandle{}
	defer stopProcesses(processes)
	start := func(name string, args ...string) bool {
		proc, err := startGoProcess(runCtx, root, cfg.GoBinary, name, args...)
		if err != nil {
			report.add(Check{Name: "process.start." + name, Error: err.Error()})
			return false
		}
		processes = append(processes, proc)
		report.add(Check{Name: "process.start." + name, OK: true})
		return true
	}

	providerURL := "http://" + p.provider
	nodeURL := "http://" + p.node
	primaryURLs := primaryProcessURLs(p)
	if !start("provider", "run", "./cmd/pacp-fake-provider", "-addr", p.provider, "-endpoint", providerURL, "-provider-credential", processRunnerToken) {
		return report
	}
	if !waitHealth(runCtx, providerURL+"/v1/provider/health", "", "provider.health", &report) {
		return report
	}
	if !start("primary", "run", "./cmd/pacp-primary",
		"-catalog-addr", p.catalog,
		"-jobs-addr", p.jobs,
		"-leases-addr", p.leases,
		"-artifacts-addr", p.artifacts,
		"-policy-addr", p.policy,
		"-gateway-addr", p.gateway,
		"-node-registry-addr", p.nodeRegistry,
		"-artifact-root", filepath.Join(workDir, "artifacts"),
		"-state-dir", filepath.Join(workDir, "primary-state"),
		"-manifest", files.manifest,
		"-resources", files.resources,
		"-policy-seed", files.policySeed,
		"-component-token", processComponentToken,
		"-gateway-credential", processComponentToken,
		"-disable-runner",
		"-route-aware-component-auth",
	) {
		return report
	}
	if !waitHealth(runCtx, primaryURLs.gateway+"/v1/gateway/health", "", "gateway.health", &report) {
		return report
	}
	if !waitHealth(runCtx, primaryURLs.nodeRegistry+"/v1/node-registry/health", "Bearer "+processComponentToken, "node_registry.health", &report) {
		return report
	}
	if !start("node", "run", "./cmd/pacp-node",
		"-addr", p.node,
		"-config", files.nodeConfig,
		"-node-registry-url", primaryURLs.nodeRegistry,
		"-node-registry-credential", processComponentToken,
		"-node-public-url", nodeURL,
		"-node-registry-register",
		"-node-registry-heartbeat", "250ms",
	) {
		return report
	}
	if !waitHealth(runCtx, nodeURL+"/v1/node/health", "Bearer "+processRunnerToken, "node.health", &report) {
		return report
	}
	if !waitNodeRegistered(runCtx, primaryURLs.nodeRegistry, nodeURL, &report) {
		return report
	}
	if !trustNode(runCtx, primaryURLs.nodeRegistry, &report) {
		return report
	}

	jobID, ok := invokeGateway(runCtx, primaryURLs.gateway, &report)
	if !ok {
		return report
	}
	report.JobID = jobID

	runnerProc, err := startGoProcess(runCtx, root, cfg.GoBinary, "runner", "run", "./cmd/pacp-runner",
		"-once",
		"-worker-id", "runner_process_smoke",
		"-actor-subject-id", processRunnerID,
		"-worker-subject-id", processRunnerID,
		"-catalog-url", primaryURLs.catalog,
		"-jobs-url", primaryURLs.jobs,
		"-leases-url", primaryURLs.leases,
		"-artifacts-url", primaryURLs.artifacts,
		"-policy-url", primaryURLs.policy,
		"-node-registry-url", primaryURLs.nodeRegistry,
		"-credential", processRunnerToken,
		"-policy-credential", processComponentToken,
		"-node-registry-credential", processComponentToken,
		"-node-start-timeout", "5s",
		"-node-start-poll", "50ms",
		"-lease-poll", "50ms",
	)
	if err != nil {
		report.add(Check{Name: "process.start.runner", Error: err.Error()})
		return report
	}
	processes = append(processes, runnerProc)
	if err := waitProcess(runCtx, runnerProc); err != nil {
		report.add(Check{Name: "runner.once", Error: processError(runnerProc, err)})
		return report
	}
	report.add(Check{Name: "runner.once", OK: true})

	if !waitJobSucceeded(runCtx, primaryURLs.gateway, jobID, &report) {
		return report
	}
	artifactID, ok := firstArtifact(runCtx, primaryURLs.gateway, jobID, &report)
	if !ok {
		return report
	}
	report.ArtifactID = artifactID
	content, ok := artifactContent(runCtx, primaryURLs.gateway, artifactID, &report)
	if !ok {
		return report
	}
	if string(content) != "artifact bytes" {
		report.add(Check{Name: "artifact.content.verify", Error: fmt.Sprintf("content=%q", string(content))})
		return report
	}
	report.add(Check{Name: "artifact.content.verify", OK: true})
	return report
}

type inputFiles struct {
	manifest   string
	resources  string
	policySeed string
	nodeConfig string
}

type primaryURLs struct {
	catalog      string
	jobs         string
	leases       string
	artifacts    string
	policy       string
	gateway      string
	nodeRegistry string
}

func primaryProcessURLs(p smokePorts) primaryURLs {
	return primaryURLs{
		catalog:      "http://" + p.catalog,
		jobs:         "http://" + p.jobs,
		leases:       "http://" + p.leases,
		artifacts:    "http://" + p.artifacts,
		policy:       "http://" + p.policy,
		gateway:      "http://" + p.gateway,
		nodeRegistry: "http://" + p.nodeRegistry,
	}
}

func writeInputs(root string, ports smokePorts) (inputFiles, error) {
	files := inputFiles{
		manifest:   filepath.Join(root, "catalog", "fake-provider.json"),
		resources:  filepath.Join(root, "leases", "resources.json"),
		policySeed: filepath.Join(root, "policy", "policy-seed.json"),
		nodeConfig: filepath.Join(root, "node", "node.json"),
	}
	providerURL := "http://" + ports.provider
	manifest := processFakeManifest("http://node-linux-gpu.local:8188")
	resources := map[string]any{"resources": []contracts.RegisterResourceRequest{{
		ResourceID:  "res_process_gpu",
		Selector:    "gpu",
		DisplayName: "Process smoke GPU",
		Status:      contracts.ResourceAvailable,
		NodeID:      processNodeID,
		Tags:        []string{"gpu", "process-smoke"},
	}}}
	policySeed := map[string]any{"api_keys": []contracts.CreateAPIKeyRequest{
		{SubjectID: processAgentID, Scopes: []string{"agent"}, Token: processAgentToken},
		{SubjectID: processRunnerID, Scopes: []string{"worker"}, Token: processRunnerToken},
		{SubjectID: processComponentID, Scopes: []string{"component"}, Token: processComponentToken},
	}}
	nodeConfig := contracts.NodeConfig{
		NodeID: processNodeID,
		Resources: []contracts.NodeResource{{
			ResourceID: "res_process_gpu",
			Tags:       []string{"gpu", "process-smoke"},
			Metadata:   map[string]any{"kind": "gpu"},
		}},
		Auth: []contracts.NodeAuthSubject{{
			Token:          processRunnerToken,
			SubjectID:      processRunnerID,
			Scopes:         []string{"worker"},
			AllowedActions: []string{"node.read", "node.service.start", "node.service.touch", "node.service.stop"},
		}},
		Services: []contracts.NodeServiceConfig{{
			ServiceID:        processServiceID,
			DisplayName:      "Process Fake Provider",
			RuntimeAdapter:   "fake",
			ProviderEndpoint: providerURL,
			InitialStatus:    "stopped",
		}},
	}
	if err := writeJSON(files.manifest, manifest); err != nil {
		return inputFiles{}, err
	}
	if err := writeJSON(files.resources, resources); err != nil {
		return inputFiles{}, err
	}
	if err := writeJSON(files.policySeed, policySeed); err != nil {
		return inputFiles{}, err
	}
	if err := writeJSON(files.nodeConfig, nodeConfig); err != nil {
		return inputFiles{}, err
	}
	return files, nil
}

func processFakeManifest(endpoint string) contracts.ProviderManifest {
	nodeID := processNodeID
	return contracts.ProviderManifest{
		SchemaVersion: "v1",
		Service: contracts.Service{
			ID:           processServiceID,
			Name:         "Process Fake Provider",
			Description:  "Fake provider used by the process-level distributed smoke.",
			Version:      "0.1.0",
			ProviderKind: "fake",
			Tags:         []string{"fake", "process-smoke"},
		},
		Provider: contracts.Provider{Endpoint: endpoint, HealthPath: "/v1/provider/health", NodeID: nodeID},
		Capabilities: []contracts.Capability{{
			ID:            processCapabilityID,
			Name:          "Process fake image artifact",
			Description:   "Return a deterministic fake artifact payload.",
			Tags:          []string{"test", "artifact"},
			ExecutionMode: "async",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []any{"prompt"},
				"properties": map[string]any{
					"prompt": map[string]any{"type": "string"},
				},
			},
			OutputSchema: map[string]any{
				"type":     "object",
				"required": []any{"artifact_count"},
				"properties": map[string]any{
					"artifact_count": map[string]any{"type": "integer"},
				},
			},
			Examples:      []map[string]any{{"prompt": "red mug"}},
			SideEffects:   "external",
			ResourceHints: []contracts.ResourceHint{{Selector: "gpu", Required: true, Quantity: 1}},
			ArtifactHints: []contracts.ArtifactHint{{MediaType: "text/plain", Count: "one"}},
			TimeoutHint:   "30s",
		}},
	}
}

func reservePorts(count int) ([]string, error) {
	addrs := make([]string, 0, count)
	listeners := make([]net.Listener, 0, count)
	defer func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}()
	for i := 0; i < count; i++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		listeners = append(listeners, listener)
		addrs = append(addrs, listener.Addr().String())
	}
	return addrs, nil
}

func startGoProcess(ctx context.Context, repoRoot, goBinary, name string, args ...string) (*processHandle, error) {
	cmd := exec.CommandContext(ctx, goBinary, args...)
	cmd.Dir = repoRoot
	cmd.Env = processEnv(os.Environ())
	proc := &processHandle{name: name, cmd: cmd, done: make(chan error, 1)}
	cmd.Stdout = &proc.stdout
	cmd.Stderr = &proc.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		proc.done <- cmd.Wait()
	}()
	return proc, nil
}

func processEnv(base []string) []string {
	hasGoCache := false
	for _, value := range base {
		if strings.HasPrefix(value, "GOCACHE=") {
			hasGoCache = true
			break
		}
	}
	if hasGoCache {
		return base
	}
	return append(base, "GOCACHE=/tmp/go-build-cache")
}

func waitProcess(ctx context.Context, proc *processHandle) error {
	select {
	case err := <-proc.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func stopProcesses(processes []*processHandle) {
	for i := len(processes) - 1; i >= 0; i-- {
		proc := processes[i]
		if proc == nil || proc.cmd == nil || proc.cmd.Process == nil {
			continue
		}
		select {
		case <-proc.done:
			continue
		default:
		}
		_ = proc.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-proc.done:
		case <-time.After(2 * time.Second):
			_ = proc.cmd.Process.Kill()
			<-proc.done
		}
	}
}

func waitHealth(ctx context.Context, target, credential, name string, report *Report) bool {
	status, err := pollJSON(ctx, http.MethodGet, target, credential, nil, nil)
	if err != nil {
		report.add(Check{Name: name, HTTPStatus: status, Error: err.Error()})
		return false
	}
	report.add(Check{Name: name, OK: true, HTTPStatus: status})
	return true
}

func waitNodeRegistered(ctx context.Context, registryURL, nodeURL string, report *Report) bool {
	target := registryURL + "/v1/node-registry/nodes/" + processNodeID
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(10 * time.Second)
	}
	var lastErr error
	var lastStatus int
	for time.Now().Before(deadline) {
		var record contracts.NodeRecord
		status, err := requestJSON(ctx, http.MethodGet, target, "Bearer "+processComponentToken, "", nil, &record)
		lastStatus = status
		if err == nil {
			switch {
			case record.NodeID != processNodeID:
				lastErr = fmt.Errorf("node_id=%q", record.NodeID)
			case strings.TrimRight(record.URL, "/") != strings.TrimRight(nodeURL, "/"):
				lastErr = fmt.Errorf("url=%q", record.URL)
			case record.TrustState != contracts.NodeTrustUntrusted:
				lastErr = fmt.Errorf("trust_state=%q", record.TrustState)
			default:
				report.add(Check{Name: "node_registry.self_register", OK: true, HTTPStatus: status})
				return true
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			report.add(Check{Name: "node_registry.self_register", HTTPStatus: lastStatus, Error: ctx.Err().Error()})
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
	if lastErr == nil {
		lastErr = errors.New("timed out")
	}
	report.add(Check{Name: "node_registry.self_register", HTTPStatus: lastStatus, Error: lastErr.Error()})
	return false
}

func trustNode(ctx context.Context, registryURL string, report *Report) bool {
	var record contracts.NodeRecord
	status, err := requestJSON(ctx, http.MethodPost, registryURL+"/v1/node-registry/nodes/"+processNodeID+"/trust", "Bearer "+processComponentToken, "", contracts.UpdateNodeTrustRequest{
		TrustState: contracts.NodeTrustTrusted,
		Reason:     "process smoke trust promotion",
	}, &record)
	if err != nil {
		report.add(Check{Name: "node_registry.trust", HTTPStatus: status, Error: err.Error()})
		return false
	}
	if record.TrustState != contracts.NodeTrustTrusted {
		report.add(Check{Name: "node_registry.trust", HTTPStatus: status, Error: fmt.Sprintf("trust_state=%q", record.TrustState)})
		return false
	}
	report.add(Check{Name: "node_registry.trust", OK: true, HTTPStatus: status})
	return true
}

func invokeGateway(ctx context.Context, gatewayURL string, report *Report) (string, bool) {
	var response contracts.InvokeToolResponse
	status, err := requestJSON(ctx, http.MethodPost, gatewayURL+"/v1/tools/"+processCapabilityID+"/invoke", "Bearer "+processAgentToken, "process-smoke-invoke", contracts.InvokeToolRequest{
		Input:         map[string]any{"prompt": "process smoke"},
		PreferredMode: "async",
	}, &response)
	if err != nil {
		report.add(Check{Name: "gateway.invoke", HTTPStatus: status, Error: err.Error()})
		return "", false
	}
	if response.JobID == "" || response.Mode != "async" {
		report.add(Check{Name: "gateway.invoke", HTTPStatus: status, Error: fmt.Sprintf("response=%#v", response)})
		return "", false
	}
	report.add(Check{Name: "gateway.invoke", OK: true, HTTPStatus: status})
	return response.JobID, true
}

func waitJobSucceeded(ctx context.Context, gatewayURL, jobID string, report *Report) bool {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(10 * time.Second)
	}
	for time.Now().Before(deadline) {
		var job contracts.AgentJob
		status, err := requestJSON(ctx, http.MethodGet, gatewayURL+"/v1/agent/jobs/"+jobID, "Bearer "+processAgentToken, "", nil, &job)
		if err != nil {
			report.add(Check{Name: "gateway.job.status", HTTPStatus: status, Error: err.Error()})
			return false
		}
		switch job.State {
		case contracts.JobSucceeded:
			report.add(Check{Name: "gateway.job.succeeded", OK: true, HTTPStatus: status})
			return true
		case contracts.JobFailed, contracts.JobCanceled:
			report.add(Check{Name: "gateway.job.succeeded", HTTPStatus: status, Error: fmt.Sprintf("state=%s error=%#v", job.State, job.TerminalError)})
			return false
		}
		select {
		case <-ctx.Done():
			report.add(Check{Name: "gateway.job.succeeded", Error: ctx.Err().Error()})
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
	report.add(Check{Name: "gateway.job.succeeded", Error: "timed out waiting for job success"})
	return false
}

func firstArtifact(ctx context.Context, gatewayURL, jobID string, report *Report) (string, bool) {
	var data struct {
		Items []contracts.AgentArtifact `json:"items"`
	}
	status, err := requestJSON(ctx, http.MethodGet, gatewayURL+"/v1/agent/jobs/"+jobID+"/artifacts", "Bearer "+processAgentToken, "", nil, &data)
	if err != nil {
		report.add(Check{Name: "gateway.artifacts", HTTPStatus: status, Error: err.Error()})
		return "", false
	}
	if len(data.Items) == 0 || data.Items[0].ArtifactID == "" {
		report.add(Check{Name: "gateway.artifacts", HTTPStatus: status, Error: fmt.Sprintf("items=%#v", data.Items)})
		return "", false
	}
	report.add(Check{Name: "gateway.artifacts", OK: true, HTTPStatus: status})
	return data.Items[0].ArtifactID, true
}

func artifactContent(ctx context.Context, gatewayURL, artifactID string, report *Report) ([]byte, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gatewayURL+"/v1/artifacts/"+artifactID+"/content", nil)
	if err != nil {
		report.add(Check{Name: "gateway.artifact.content", Error: err.Error()})
		return nil, false
	}
	req.Header.Set("Authorization", "Bearer "+processAgentToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		report.add(Check{Name: "gateway.artifact.content", Error: err.Error()})
		return nil, false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		report.add(Check{Name: "gateway.artifact.content", HTTPStatus: resp.StatusCode, Error: err.Error()})
		return nil, false
	}
	if resp.StatusCode != http.StatusOK {
		report.add(Check{Name: "gateway.artifact.content", HTTPStatus: resp.StatusCode, Error: string(body)})
		return nil, false
	}
	report.add(Check{Name: "gateway.artifact.content", OK: true, HTTPStatus: resp.StatusCode})
	return body, true
}

func pollJSON(ctx context.Context, method, target, credential string, body any, out any) (int, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(10 * time.Second)
	}
	var lastErr error
	var lastStatus int
	for time.Now().Before(deadline) {
		status, err := requestJSON(ctx, method, target, credential, "", body, out)
		if err == nil {
			return status, nil
		}
		lastErr = err
		lastStatus = status
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return lastStatus, lastErr
			}
			return lastStatus, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if lastErr == nil {
		lastErr = errors.New("timed out")
	}
	return lastStatus, lastErr
}

func requestJSON(ctx context.Context, method, target, credential, idempotencyKey string, body any, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return 0, err
	}
	if credential != "" {
		req.Header.Set("Authorization", credential)
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		return resp.StatusCode, nil
	}
	var envelope struct {
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error any             `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return resp.StatusCode, err
	}
	if !envelope.OK {
		return resp.StatusCode, fmt.Errorf("response not ok: %s", strings.TrimSpace(string(raw)))
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

func processError(proc *processHandle, err error) string {
	return fmt.Sprintf("%v stdout=%q stderr=%q", err, proc.stdout.String(), proc.stderr.String())
}

func (r *Report) add(check Check) {
	if !check.OK {
		r.OK = false
	}
	r.Checks = append(r.Checks, check)
}
