package distributedsmoke

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"pacp/internal/components/artifacts"
	"pacp/internal/components/catalog"
	"pacp/internal/components/gateway"
	"pacp/internal/components/jobs"
	"pacp/internal/components/leases"
	"pacp/internal/components/node"
	"pacp/internal/components/policy"
	"pacp/internal/contracts"
	"pacp/internal/provider"
	"pacp/internal/routeauth"
	"pacp/internal/runner"
	"pacp/internal/testkit"
	"pacp/internal/transportauth"
)

type rawSuccessEnvelope struct {
	OK   bool            `json:"ok"`
	Data json.RawMessage `json:"data"`
}

type DistributedSmokeReport struct {
	OK         bool                    `json:"ok"`
	Checks     []DistributedSmokeCheck `json:"checks"`
	JobID      string                  `json:"job_id,omitempty"`
	ArtifactID string                  `json:"artifact_id,omitempty"`
}

type DistributedSmokeCheck struct {
	Name       string `json:"name"`
	OK         bool   `json:"ok"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Error      string `json:"error,omitempty"`
}

func (r DistributedSmokeReport) Passed() bool {
	return r.OK
}

func Run(ctx context.Context) DistributedSmokeReport {
	report := DistributedSmokeReport{OK: true}
	client := &http.Client{Timeout: 5 * time.Second}
	const (
		agentToken     = "token_distributed_agent"
		componentToken = "token_distributed_component"
		runnerToken    = "token_distributed_runner"
		agentID        = "sub_distributed_agent"
		componentID    = "sub_distributed_component"
		runnerID       = "sub_distributed_runner"
		nodeID         = "node_linux_gpu"
		serviceID      = "svc_distributed_gpu"
		capability     = "cap_distributed_artifact"
		routeURL       = "http://node_linux_gpu:8188"
	)

	var providerInvocations atomic.Int32
	providerManifest := distributedProviderManifest(serviceID, capability, "http://provider.local")
	providerServer, err := provider.NewServer(providerManifest, map[string]provider.CapabilityHandler{
		capability: func(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
			providerInvocations.Add(1)
			body := []byte("distributed artifact for " + req.Context.JobID)
			sum := sha256.Sum256(body)
			return contracts.ProviderInvokeResponse{
				Output: map[string]any{"ok": true},
				Artifacts: []contracts.ProviderArtifact{{
					Name:          "distributed.txt",
					MediaType:     "text/plain",
					ContentBase64: base64.StdEncoding.EncodeToString(body),
					Checksum:      "sha256:" + hex.EncodeToString(sum[:]),
				}},
			}, nil
		},
	})
	if err != nil {
		report.add(DistributedSmokeCheck{Name: "provider.create", Error: err.Error()})
		return report
	}
	providerHTTP := httptest.NewServer(providerServer)
	defer providerHTTP.Close()

	manifest := distributedProviderManifest(serviceID, capability, routeURL)
	manifest.Provider.NodeID = nodeID

	catalogStore := catalog.NewStore()
	if _, err := catalogStore.RegisterManifest(manifest); err != nil {
		report.add(DistributedSmokeCheck{Name: "catalog.seed", Error: err.Error()})
		return report
	}

	jobStore := jobs.NewStore()

	leaseStore := leases.NewStore()
	if _, err := leaseStore.RegisterResource(contracts.RegisterResourceRequest{
		ResourceID: "res_gpu_0",
		Selector:   "gpu",
		Status:     contracts.ResourceAvailable,
		NodeID:     nodeID,
		Tags:       []string{"gpu", "gpu:0"},
	}); err != nil {
		report.add(DistributedSmokeCheck{Name: "leases.seed", Error: err.Error()})
		return report
	}

	artifactRoot, err := os.MkdirTemp("", "pacp-distributed-smoke-artifacts-*")
	if err != nil {
		report.add(DistributedSmokeCheck{Name: "artifacts.root", Error: err.Error()})
		return report
	}
	defer os.RemoveAll(artifactRoot)
	artifactStore, err := artifacts.NewStore(artifactRoot)
	if err != nil {
		report.add(DistributedSmokeCheck{Name: "artifacts.create", Error: err.Error()})
		return report
	}

	policyStore := policy.NewStore()
	if _, err := policyStore.CreateAPIKey(contracts.CreateAPIKeyRequest{SubjectID: agentID, Scopes: []string{"agent"}, Token: agentToken}); err != nil {
		report.add(DistributedSmokeCheck{Name: "policy.seed.agent", Error: err.Error()})
		return report
	}
	if _, err := policyStore.CreateAPIKey(contracts.CreateAPIKeyRequest{SubjectID: componentID, Scopes: []string{"component"}, Token: componentToken}); err != nil {
		report.add(DistributedSmokeCheck{Name: "policy.seed.component", Error: err.Error()})
		return report
	}
	if _, err := policyStore.CreateAPIKey(contracts.CreateAPIKeyRequest{SubjectID: runnerID, Scopes: []string{"worker"}, Token: runnerToken}); err != nil {
		report.add(DistributedSmokeCheck{Name: "policy.seed.runner", Error: err.Error()})
		return report
	}
	policyHTTP := httptest.NewServer(transportauth.RequireBearer(policy.NewHandler(policyStore), componentToken))
	defer policyHTTP.Close()
	policyCredential := "Bearer " + componentToken

	catalogHTTP := httptest.NewServer(transportauth.RequireVerifiedScopes(catalog.NewHandler(catalogStore), transportauth.ScopeConfig{
		PolicyURL:        policyHTTP.URL,
		PolicyCredential: policyCredential,
		Rules:            routeauth.CatalogScopeRules(),
		Client:           client,
	}))
	defer catalogHTTP.Close()

	jobsHTTP := httptest.NewServer(transportauth.RequireVerifiedScopes(jobs.NewHandler(jobStore), transportauth.ScopeConfig{
		PolicyURL:        policyHTTP.URL,
		PolicyCredential: policyCredential,
		Rules:            routeauth.JobScopeRules(),
		Client:           client,
	}))
	defer jobsHTTP.Close()

	leasesHTTP := httptest.NewServer(transportauth.RequireVerifiedScopes(leases.NewHandler(leaseStore), transportauth.ScopeConfig{
		PolicyURL:        policyHTTP.URL,
		PolicyCredential: policyCredential,
		Rules:            routeauth.LeaseScopeRules(),
		Client:           client,
	}))
	defer leasesHTTP.Close()

	artifactsHTTP := httptest.NewServer(transportauth.RequireVerifiedScopes(artifacts.NewHandler(artifactStore), transportauth.ScopeConfig{
		PolicyURL:        policyHTTP.URL,
		PolicyCredential: policyCredential,
		Rules:            routeauth.ArtifactScopeRules(),
		Client:           client,
	}))
	defer artifactsHTTP.Close()

	checkRouteAuthScopeSeparation(ctx, client, catalogHTTP.URL, leasesHTTP.URL, runnerToken, componentToken, capability, &report)

	nodeStore, err := node.NewStore(contracts.NodeConfig{
		NodeID: nodeID,
		Resources: []contracts.NodeResource{{
			ResourceID: "res_gpu_0",
			Tags:       []string{"gpu", "gpu:0"},
			Metadata:   map[string]any{"kind": "gpu"},
		}},
		Auth: []contracts.NodeAuthSubject{{
			Token:          runnerToken,
			SubjectID:      runnerID,
			Scopes:         []string{"worker"},
			AllowedActions: []string{"node.read", "node.service.start", "node.service.touch", "node.service.stop"},
		}},
		Services: []contracts.NodeServiceConfig{{
			ServiceID:        serviceID,
			DisplayName:      "Distributed GPU Provider",
			RuntimeAdapter:   "fake",
			ProviderEndpoint: providerHTTP.URL,
			InitialStatus:    "stopped",
			Manifest:         &manifest,
		}},
	})
	if err != nil {
		report.add(DistributedSmokeCheck{Name: "node.create", Error: err.Error()})
		return report
	}
	nodeHTTP := httptest.NewServer(node.NewHandler(nodeStore))
	defer nodeHTTP.Close()

	gatewayHTTP := httptest.NewServer(gateway.NewHandler(gateway.Config{
		CatalogURL:        catalogHTTP.URL,
		PolicyURL:         policyHTTP.URL,
		JobsURL:           jobsHTTP.URL,
		LeasesURL:         leasesHTTP.URL,
		ArtifactsURL:      artifactsHTTP.URL,
		GatewayCredential: policyCredential,
		Client:            client,
	}))
	defer gatewayHTTP.Close()

	checkLiveComponentContracts(ctx, client, []liveComponentContract{
		{Kind: "policy", URL: policyHTTP.URL, Credential: policyCredential},
		{Kind: "catalog", URL: catalogHTTP.URL, Credential: policyCredential},
		{Kind: "jobs", URL: jobsHTTP.URL, Credential: policyCredential},
		{Kind: "leases", URL: leasesHTTP.URL, Credential: policyCredential},
		{Kind: "artifacts", URL: artifactsHTTP.URL, Credential: policyCredential},
		{Kind: "node", URL: nodeHTTP.URL, Credential: "Bearer " + runnerToken},
		{Kind: "gateway", URL: gatewayHTTP.URL, Credential: "Bearer " + agentToken},
	}, &report)
	checkGatewayDependencyHealth(ctx, client, gatewayHTTP.URL, &report)

	jobID := invokeDistributedTool(ctx, client, gatewayHTTP.URL, agentToken, capability, &report)
	if jobID == "" {
		return report
	}
	report.JobID = jobID

	r := runner.New(runner.Config{
		WorkerID:            "runner_distributed_smoke",
		JobsURL:             jobsHTTP.URL,
		LeasesURL:           leasesHTTP.URL,
		ArtifactsURL:        artifactsHTTP.URL,
		PolicyURL:           policyHTTP.URL,
		NodeURLs:            map[string]string{nodeID: nodeHTTP.URL},
		NodeStartTimeout:    2 * time.Second,
		NodePollInterval:    10 * time.Millisecond,
		ComponentCredential: "Bearer " + runnerToken,
		PolicyCredential:    policyCredential,
		ActorSubjectID:      runnerID,
		Client:              client,
	})
	runJobID, ok, err := r.RunOnce(ctx)
	if err != nil {
		report.add(DistributedSmokeCheck{Name: "runner.run_once", Error: err.Error()})
		return report
	}
	if !ok || runJobID != jobID {
		report.add(DistributedSmokeCheck{Name: "runner.run_once", Error: fmt.Sprintf("run result job_id=%q ok=%t", runJobID, ok)})
		return report
	}
	report.add(DistributedSmokeCheck{Name: "runner.run_once", OK: true})

	job := readDistributedJob(ctx, client, jobsHTTP.URL, componentToken, jobID, &report)
	if job.JobID == "" {
		return report
	}
	if job.State != contracts.JobSucceeded {
		report.add(DistributedSmokeCheck{Name: "jobs.succeeded", Error: "job state is " + string(job.State)})
		return report
	}
	report.add(DistributedSmokeCheck{Name: "jobs.succeeded", OK: true})
	if len(job.ArtifactRefs) != 1 {
		report.add(DistributedSmokeCheck{Name: "artifacts.registered", Error: fmt.Sprintf("artifact_refs=%d", len(job.ArtifactRefs))})
		return report
	}
	report.ArtifactID = job.ArtifactRefs[0]
	report.add(DistributedSmokeCheck{Name: "artifacts.registered", OK: true})

	checkArtifactMetadata(ctx, client, artifactsHTTP.URL, componentToken, report.ArtifactID, jobID, agentID, capability, &report)
	checkGatewayProjection(ctx, client, gatewayHTTP.URL, agentToken, jobID, &report)
	checkGatewayArtifactList(ctx, client, gatewayHTTP.URL, agentToken, jobID, report.ArtifactID, &report)
	checkGatewayArtifactContent(ctx, client, gatewayHTTP.URL, agentToken, report.ArtifactID, &report)
	checkNodeService(ctx, client, nodeHTTP.URL, runnerToken, serviceID, &report)
	checkNodeStartMetric(ctx, client, nodeHTTP.URL, runnerToken, &report)
	checkNodeTouchMetric(ctx, client, nodeHTTP.URL, runnerToken, &report)
	checkLeaseReleaseAudit(leaseStore, runnerID, jobID, &report)
	if providerInvocations.Load() != 1 {
		report.add(DistributedSmokeCheck{Name: "provider.invoked", Error: fmt.Sprintf("invocations=%d", providerInvocations.Load())})
	} else {
		report.add(DistributedSmokeCheck{Name: "provider.invoked", OK: true})
	}
	return report
}

type liveComponentContract struct {
	Kind       string
	URL        string
	Credential string
}

func checkLiveComponentContracts(ctx context.Context, client *http.Client, components []liveComponentContract, report *DistributedSmokeReport) {
	for _, component := range components {
		componentReport := testkit.CheckComponent(ctx, client, testkit.ComponentCheckOptions{
			BaseURL:    component.URL,
			Kind:       component.Kind,
			Credential: component.Credential,
		})
		for _, result := range componentReport.Checks {
			report.add(DistributedSmokeCheck{
				Name:       distributedComponentCheckName(component.Kind, result.Name),
				OK:         result.OK,
				HTTPStatus: result.HTTPStatus,
				Error:      result.Error,
			})
		}
	}
}

func distributedComponentCheckName(kind, name string) string {
	surfacePrefix := "component.surface." + kind + "."
	if strings.HasPrefix(name, surfacePrefix) {
		return "component." + kind + ".surface." + strings.TrimPrefix(name, surfacePrefix)
	}
	return "component." + kind + "." + strings.TrimPrefix(name, "component.")
}

func checkGatewayDependencyHealth(ctx context.Context, client *http.Client, gatewayURL string, report *DistributedSmokeReport) {
	var healthEnvelope rawSuccessEnvelope
	status, err := requestJSON(ctx, client, http.MethodGet, joinURLPath(gatewayURL, "/v1/gateway/health"), nil, nil, &healthEnvelope)
	check := DistributedSmokeCheck{Name: "gateway.dependencies_healthy", HTTPStatus: status}
	if err != nil {
		check.Error = err.Error()
		report.add(check)
		return
	}
	if status != http.StatusOK || !healthEnvelope.OK {
		check.Error = fmt.Sprintf("HTTP %d ok=%t", status, healthEnvelope.OK)
		report.add(check)
		return
	}
	var health struct {
		Status  string `json:"status"`
		Details struct {
			Dependencies []smokeGatewayDependency `json:"dependencies"`
		} `json:"details"`
	}
	if err := json.Unmarshal(healthEnvelope.Data, &health); err != nil {
		check.Error = "decode gateway health: " + err.Error()
		report.add(check)
		return
	}
	if health.Status != "healthy" {
		check.Error = "gateway status is " + health.Status
		report.add(check)
		return
	}
	if missing := missingHealthyGatewayDependencies(health.Details.Dependencies); len(missing) > 0 {
		check.Error = "unhealthy dependencies: " + strings.Join(missing, ",")
		report.add(check)
		return
	}
	check.OK = true
	report.add(check)

	var metricsEnvelope rawSuccessEnvelope
	status, err = requestJSON(ctx, client, http.MethodGet, joinURLPath(gatewayURL, "/v1/gateway/metrics"), nil, nil, &metricsEnvelope)
	metricCheck := DistributedSmokeCheck{Name: "gateway.downstream_reachability_metrics", HTTPStatus: status}
	if err != nil {
		metricCheck.Error = err.Error()
		report.add(metricCheck)
		return
	}
	if status != http.StatusOK || !metricsEnvelope.OK {
		metricCheck.Error = fmt.Sprintf("HTTP %d ok=%t", status, metricsEnvelope.OK)
		report.add(metricCheck)
		return
	}
	var metrics contracts.ComponentMetrics
	if err := json.Unmarshal(metricsEnvelope.Data, &metrics); err != nil {
		metricCheck.Error = "decode gateway metrics: " + err.Error()
		report.add(metricCheck)
		return
	}
	if missing := missingGatewayReachabilityMetrics(metrics.Samples); len(missing) > 0 {
		metricCheck.Error = "missing reachable metrics: " + strings.Join(missing, ",")
		report.add(metricCheck)
		return
	}
	metricCheck.OK = true
	report.add(metricCheck)
}

type smokeGatewayDependency struct {
	Name       string
	Configured bool
	Reachable  bool
	Status     string
}

func missingHealthyGatewayDependencies(dependencies []smokeGatewayDependency) []string {
	required := map[string]bool{"catalog": false, "policy": false, "jobs": false, "leases": false, "artifacts": false}
	for _, dependency := range dependencies {
		if _, ok := required[dependency.Name]; !ok {
			continue
		}
		if dependency.Configured && dependency.Reachable && dependency.Status == "healthy" {
			required[dependency.Name] = true
		}
	}
	return missingSmokeNames(required)
}

func missingGatewayReachabilityMetrics(samples []contracts.MetricSample) []string {
	required := map[string]bool{"catalog": false, "policy": false, "jobs": false, "leases": false, "artifacts": false}
	for _, sample := range samples {
		if sample.Name != "gateway_downstream_reachable" || sample.Value != 1 {
			continue
		}
		downstream := sample.Labels["downstream"]
		if _, ok := required[downstream]; ok && sample.Labels["status"] == "healthy" {
			required[downstream] = true
		}
	}
	return missingSmokeNames(required)
}

func missingSmokeNames(required map[string]bool) []string {
	missing := []string{}
	for name, ok := range required {
		if !ok {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

func checkLeaseReleaseAudit(store *leases.Store, runnerID, jobID string, report *DistributedSmokeReport) {
	events := store.AuditEvents()
	if len(events) != 1 {
		report.add(DistributedSmokeCheck{Name: "leases.release_audit", Error: fmt.Sprintf("audit_events=%d", len(events))})
		return
	}
	event := events[0]
	if event.ActorSubjectID != runnerID || event.HolderID != jobID || event.EventType != "lease.released" || event.ReleaseReason != "job completed" {
		report.add(DistributedSmokeCheck{Name: "leases.release_audit", Error: fmt.Sprintf("event=%#v", event)})
		return
	}
	report.add(DistributedSmokeCheck{Name: "leases.release_audit", OK: true})
}

func checkRouteAuthScopeSeparation(ctx context.Context, client *http.Client, catalogURL, leasesURL, runnerToken, componentToken, capabilityID string, report *DistributedSmokeReport) {
	status, err := requestJSON(ctx, client, http.MethodGet, joinURLPath(catalogURL, "/v1/catalog/capabilities/"+url.PathEscape(capabilityID)+"/route"), nil, map[string]string{"Authorization": "Bearer " + runnerToken}, nil)
	check := DistributedSmokeCheck{Name: "auth.worker_forbidden_component_route", HTTPStatus: status}
	if err != nil {
		check.Error = err.Error()
		report.add(check)
	} else if status != http.StatusForbidden {
		check.Error = fmt.Sprintf("HTTP %d", status)
		report.add(check)
	} else {
		check.OK = true
		report.add(check)
	}

	status, err = requestJSON(ctx, client, http.MethodPost, joinURLPath(leasesURL, "/v1/leases/lease_auth_negative/heartbeat"), contracts.LeaseHeartbeatRequest{HolderID: "auth_negative"}, map[string]string{"Authorization": "Bearer " + componentToken}, nil)
	check = DistributedSmokeCheck{Name: "auth.component_forbidden_worker_route", HTTPStatus: status}
	if err != nil {
		check.Error = err.Error()
		report.add(check)
		return
	}
	if status != http.StatusForbidden {
		check.Error = fmt.Sprintf("HTTP %d", status)
		report.add(check)
		return
	}
	check.OK = true
	report.add(check)
}

func checkArtifactMetadata(ctx context.Context, client *http.Client, artifactsURL, componentToken, artifactID, jobID, ownerSubjectID, capabilityID string, report *DistributedSmokeReport) {
	var envelope rawSuccessEnvelope
	status, err := requestJSON(ctx, client, http.MethodGet, joinURLPath(artifactsURL, "/v1/artifacts/"+url.PathEscape(artifactID)), nil, map[string]string{"Authorization": "Bearer " + componentToken}, &envelope)
	check := DistributedSmokeCheck{Name: "artifacts.metadata", HTTPStatus: status}
	if err != nil {
		check.Error = err.Error()
		report.add(check)
		return
	}
	if status != http.StatusOK || !envelope.OK {
		check.Error = fmt.Sprintf("HTTP %d ok=%t", status, envelope.OK)
		report.add(check)
		return
	}
	var artifact contracts.Artifact
	if err := json.Unmarshal(envelope.Data, &artifact); err != nil {
		check.Error = "decode artifact: " + err.Error()
		report.add(check)
		return
	}
	if artifact.ArtifactID != artifactID || artifact.ProducerRef != jobID || artifact.OwnerSubjectID != ownerSubjectID {
		check.Error = fmt.Sprintf("artifact_id=%q producer_ref=%q owner_subject_id=%q", artifact.ArtifactID, artifact.ProducerRef, artifact.OwnerSubjectID)
		report.add(check)
		return
	}
	if artifact.Metadata["capability_id"] != capabilityID {
		check.Error = fmt.Sprintf("metadata=%#v", artifact.Metadata)
		report.add(check)
		return
	}
	check.OK = true
	report.add(check)
}

func distributedProviderManifest(serviceID, capabilityID, endpoint string) contracts.ProviderManifest {
	return contracts.ProviderManifest{
		SchemaVersion: "v1",
		Service: contracts.Service{
			ID:           serviceID,
			Name:         "Distributed GPU Provider",
			Description:  "Provider used by the distributed smoke suite.",
			Version:      "v1",
			ProviderKind: "fake",
			Tags:         []string{"distributed", "gpu"},
		},
		Provider: contracts.Provider{Endpoint: endpoint, HealthPath: "/v1/provider/health"},
		Capabilities: []contracts.Capability{{
			ID:            capabilityID,
			Name:          "Distributed artifact",
			Description:   "Produces one artifact through a node-managed provider.",
			ExecutionMode: "async",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []any{"prompt"},
				"properties": map[string]any{
					"prompt": map[string]any{"type": "string"},
				},
			},
			OutputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"ok": map[string]any{"type": "boolean"},
				},
			},
			Examples:      []map[string]any{},
			SideEffects:   "external",
			ResourceHints: []contracts.ResourceHint{{Selector: "gpu", Required: true, Quantity: 1}},
			ArtifactHints: []contracts.ArtifactHint{{MediaType: "text/plain", Count: "one"}},
			TimeoutHint:   "30s",
		}},
	}
}

func invokeDistributedTool(ctx context.Context, client *http.Client, gatewayURL, agentToken, capabilityID string, report *DistributedSmokeReport) string {
	body := contracts.InvokeToolRequest{Input: map[string]any{"prompt": "distributed smoke"}}
	var envelope rawSuccessEnvelope
	status, err := requestJSON(ctx, client, http.MethodPost, joinURLPath(gatewayURL, "/v1/tools/"+url.PathEscape(capabilityID)+"/invoke"), body, map[string]string{
		"Authorization":   "Bearer " + agentToken,
		"Idempotency-Key": "distributed-smoke-invoke",
	}, &envelope)
	check := DistributedSmokeCheck{Name: "gateway.invoke", HTTPStatus: status}
	if err != nil {
		check.Error = err.Error()
		report.add(check)
		return ""
	}
	if status != http.StatusAccepted || !envelope.OK {
		check.Error = fmt.Sprintf("HTTP %d ok=%t", status, envelope.OK)
		report.add(check)
		return ""
	}
	var response contracts.InvokeToolResponse
	if err := json.Unmarshal(envelope.Data, &response); err != nil {
		check.Error = "decode response: " + err.Error()
		report.add(check)
		return ""
	}
	if response.Mode != "async" || response.JobID == "" {
		check.Error = fmt.Sprintf("mode=%q job_id=%q", response.Mode, response.JobID)
		report.add(check)
		return ""
	}
	check.OK = true
	report.add(check)
	return response.JobID
}

func readDistributedJob(ctx context.Context, client *http.Client, jobsURL, componentToken, jobID string, report *DistributedSmokeReport) contracts.Job {
	var envelope rawSuccessEnvelope
	status, err := requestJSON(ctx, client, http.MethodGet, joinURLPath(jobsURL, "/v1/jobs/"+url.PathEscape(jobID)), nil, map[string]string{"Authorization": "Bearer " + componentToken}, &envelope)
	check := DistributedSmokeCheck{Name: "jobs.read", HTTPStatus: status}
	if err != nil {
		check.Error = err.Error()
		report.add(check)
		return contracts.Job{}
	}
	if status != http.StatusOK || !envelope.OK {
		check.Error = fmt.Sprintf("HTTP %d ok=%t", status, envelope.OK)
		report.add(check)
		return contracts.Job{}
	}
	var job contracts.Job
	if err := json.Unmarshal(envelope.Data, &job); err != nil {
		check.Error = "decode job: " + err.Error()
		report.add(check)
		return contracts.Job{}
	}
	check.OK = true
	report.add(check)
	return job
}

func checkGatewayProjection(ctx context.Context, client *http.Client, gatewayURL, agentToken, jobID string, report *DistributedSmokeReport) {
	var envelope rawSuccessEnvelope
	status, err := requestJSON(ctx, client, http.MethodGet, joinURLPath(gatewayURL, "/v1/agent/jobs/"+url.PathEscape(jobID)), nil, map[string]string{"Authorization": "Bearer " + agentToken}, &envelope)
	check := DistributedSmokeCheck{Name: "gateway.job_projection", HTTPStatus: status}
	if err != nil {
		check.Error = err.Error()
		report.add(check)
		return
	}
	if status != http.StatusOK || !envelope.OK {
		check.Error = fmt.Sprintf("HTTP %d ok=%t", status, envelope.OK)
		report.add(check)
		return
	}
	var job contracts.AgentJob
	if err := json.Unmarshal(envelope.Data, &job); err != nil {
		check.Error = "decode projection: " + err.Error()
		report.add(check)
		return
	}
	if job.JobID != jobID || job.State != contracts.JobSucceeded {
		check.Error = fmt.Sprintf("job_id=%q state=%q", job.JobID, job.State)
		report.add(check)
		return
	}
	check.OK = true
	report.add(check)
}

func checkGatewayArtifactList(ctx context.Context, client *http.Client, gatewayURL, agentToken, jobID, artifactID string, report *DistributedSmokeReport) {
	var envelope rawSuccessEnvelope
	status, err := requestJSON(ctx, client, http.MethodGet, joinURLPath(gatewayURL, "/v1/agent/jobs/"+url.PathEscape(jobID)+"/artifacts"), nil, map[string]string{"Authorization": "Bearer " + agentToken}, &envelope)
	check := DistributedSmokeCheck{Name: "gateway.artifact_list", HTTPStatus: status}
	if err != nil {
		check.Error = err.Error()
		report.add(check)
		return
	}
	if status != http.StatusOK || !envelope.OK {
		check.Error = fmt.Sprintf("HTTP %d ok=%t", status, envelope.OK)
		report.add(check)
		return
	}
	var data struct {
		Items []contracts.AgentArtifact `json:"items"`
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		check.Error = "decode artifacts: " + err.Error()
		report.add(check)
		return
	}
	if len(data.Items) != 1 || data.Items[0].ArtifactID != artifactID {
		check.Error = fmt.Sprintf("items=%d artifact_id=%q", len(data.Items), firstAgentArtifactID(data.Items))
		report.add(check)
		return
	}
	check.OK = true
	report.add(check)
}

func checkGatewayArtifactContent(ctx context.Context, client *http.Client, gatewayURL, agentToken, artifactID string, report *DistributedSmokeReport) {
	endpoint := joinURLPath(gatewayURL, "/v1/artifacts/"+url.PathEscape(artifactID)+"/content")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	check := DistributedSmokeCheck{Name: "gateway.artifact_content"}
	if err != nil {
		check.Error = err.Error()
		report.add(check)
		return
	}
	req.Header.Set("Authorization", "Bearer "+agentToken)
	resp, err := client.Do(req)
	if err != nil {
		check.Error = err.Error()
		report.add(check)
		return
	}
	defer resp.Body.Close()
	check.HTTPStatus = resp.StatusCode
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		check.Error = err.Error()
		report.add(check)
		return
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), "distributed artifact") {
		check.Error = fmt.Sprintf("HTTP %d body=%q", resp.StatusCode, string(raw))
		report.add(check)
		return
	}
	check.OK = true
	report.add(check)
}

func checkNodeService(ctx context.Context, client *http.Client, nodeURL, runnerToken, serviceID string, report *DistributedSmokeReport) {
	var envelope rawSuccessEnvelope
	status, err := requestJSON(ctx, client, http.MethodGet, joinURLPath(nodeURL, "/v1/node/services/"+url.PathEscape(serviceID)), nil, map[string]string{"Authorization": "Bearer " + runnerToken}, &envelope)
	check := DistributedSmokeCheck{Name: "node.service_running", HTTPStatus: status}
	if err != nil {
		check.Error = err.Error()
		report.add(check)
		return
	}
	if status != http.StatusOK || !envelope.OK {
		check.Error = fmt.Sprintf("HTTP %d ok=%t", status, envelope.OK)
		report.add(check)
		return
	}
	var service contracts.NodeService
	if err := json.Unmarshal(envelope.Data, &service); err != nil {
		check.Error = "decode service: " + err.Error()
		report.add(check)
		return
	}
	if service.Status != "running" {
		check.Error = "status is " + service.Status
		report.add(check)
		return
	}
	check.OK = true
	report.add(check)
}

func checkNodeStartMetric(ctx context.Context, client *http.Client, nodeURL, runnerToken string, report *DistributedSmokeReport) {
	var envelope rawSuccessEnvelope
	status, err := requestJSON(ctx, client, http.MethodGet, joinURLPath(nodeURL, "/v1/node/metrics"), nil, map[string]string{"Authorization": "Bearer " + runnerToken}, &envelope)
	check := DistributedSmokeCheck{Name: "node.start_metric", HTTPStatus: status}
	if err != nil {
		check.Error = err.Error()
		report.add(check)
		return
	}
	if status != http.StatusOK || !envelope.OK {
		check.Error = fmt.Sprintf("HTTP %d ok=%t", status, envelope.OK)
		report.add(check)
		return
	}
	var metrics contracts.ComponentMetrics
	if err := json.Unmarshal(envelope.Data, &metrics); err != nil {
		check.Error = "decode metrics: " + err.Error()
		report.add(check)
		return
	}
	for _, sample := range metrics.Samples {
		if sample.Name == "node_service_start_total" && sample.Value >= 1 {
			check.OK = true
			report.add(check)
			return
		}
	}
	check.Error = "node_service_start_total was not incremented"
	report.add(check)
}

func checkNodeTouchMetric(ctx context.Context, client *http.Client, nodeURL, runnerToken string, report *DistributedSmokeReport) {
	var envelope rawSuccessEnvelope
	status, err := requestJSON(ctx, client, http.MethodGet, joinURLPath(nodeURL, "/v1/node/metrics"), nil, map[string]string{"Authorization": "Bearer " + runnerToken}, &envelope)
	check := DistributedSmokeCheck{Name: "node.touch_metric", HTTPStatus: status}
	if err != nil {
		check.Error = err.Error()
		report.add(check)
		return
	}
	if status != http.StatusOK || !envelope.OK {
		check.Error = fmt.Sprintf("HTTP %d ok=%t", status, envelope.OK)
		report.add(check)
		return
	}
	var metrics contracts.ComponentMetrics
	if err := json.Unmarshal(envelope.Data, &metrics); err != nil {
		check.Error = "decode metrics: " + err.Error()
		report.add(check)
		return
	}
	for _, sample := range metrics.Samples {
		if sample.Name == "http_requests_total" && sample.Value >= 1 && metricLabelsInclude(sample.Labels, map[string]string{
			"method":       "POST",
			"route_group":  "/v1/node/services/{service_id}/touch",
			"status_class": "2xx",
		}) {
			check.OK = true
			report.add(check)
			return
		}
	}
	check.Error = "node touch HTTP metric was not incremented"
	report.add(check)
}

func metricLabelsInclude(labels, want map[string]string) bool {
	for key, value := range want {
		if labels[key] != value {
			return false
		}
	}
	return true
}

func firstAgentArtifactID(items []contracts.AgentArtifact) string {
	if len(items) == 0 {
		return ""
	}
	return items[0].ArtifactID
}

func requestJSON(ctx context.Context, client *http.Client, method, endpoint string, body any, headers map[string]string, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, err
		}
	} else {
		_, _ = io.Copy(io.Discard, resp.Body)
	}
	return resp.StatusCode, nil
}

func joinURLPath(baseURL, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return baseURL
	}
	if strings.HasPrefix(path, "/") {
		return baseURL + path
	}
	return baseURL + "/" + path
}

func (r *DistributedSmokeReport) add(check DistributedSmokeCheck) {
	if !check.OK {
		r.OK = false
	}
	r.Checks = append(r.Checks, check)
}
