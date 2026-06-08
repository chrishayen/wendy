package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"pacp/internal/contracts"
	"pacp/internal/observability"
)

type Config struct {
	WorkerID            string
	JobsURL             string
	LeasesURL           string
	ArtifactsURL        string
	PolicyURL           string
	NodeURL             string
	NodeURLs            map[string]string
	NodeStartTimeout    time.Duration
	NodePollInterval    time.Duration
	ComponentCredential string
	WorkerSubjectID     string
	ActorSubjectID      string
	Client              *http.Client
}

type Runner struct {
	cfg    Config
	client *http.Client
	stats  *runnerStats
}

type executionPlan struct {
	CapabilityID     string                    `json:"capability_id"`
	SubjectID        string                    `json:"subject_id"`
	Input            map[string]any            `json:"input"`
	Route            contracts.CapabilityRoute `json:"route"`
	ResourceSelector string                    `json:"resource_selector"`
	TimeoutSeconds   int                       `json:"timeout_seconds"`
}

func New(cfg Config) *Runner {
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.NodeStartTimeout <= 0 {
		cfg.NodeStartTimeout = 30 * time.Second
	}
	if cfg.NodePollInterval <= 0 {
		cfg.NodePollInterval = 500 * time.Millisecond
	}
	cfg.JobsURL = strings.TrimRight(cfg.JobsURL, "/")
	cfg.LeasesURL = strings.TrimRight(cfg.LeasesURL, "/")
	cfg.ArtifactsURL = strings.TrimRight(cfg.ArtifactsURL, "/")
	cfg.PolicyURL = strings.TrimRight(cfg.PolicyURL, "/")
	cfg.NodeURL = strings.TrimRight(cfg.NodeURL, "/")
	cfg.NodeURLs = normalizeNodeURLs(cfg.NodeURLs)
	if cfg.ActorSubjectID == "" {
		cfg.ActorSubjectID = cfg.WorkerSubjectID
	}
	return &Runner{cfg: cfg, client: client, stats: newRunnerStats()}
}

func normalizeNodeURLs(raw map[string]string) map[string]string {
	if len(raw) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(raw))
	for nodeID, nodeURL := range raw {
		nodeID = strings.TrimSpace(nodeID)
		nodeURL = strings.TrimRight(strings.TrimSpace(nodeURL), "/")
		if nodeID != "" && nodeURL != "" {
			out[nodeID] = nodeURL
		}
	}
	return out
}

func (r *Runner) RunOnce(ctx context.Context) (string, bool, error) {
	ctx = observability.EnsureContextRequestID(ctx, "req_runner")
	r.stats.RecordRunStart()
	job, ok, err := r.nextQueuedJob(ctx)
	if err != nil || !ok {
		if err != nil {
			r.stats.RecordRunResult("error", classifyRunnerError(err), err)
		} else {
			r.stats.RecordRunResult("idle", "", nil)
		}
		return "", ok, err
	}
	r.stats.BeginJob(job.JobID)
	defer r.stats.EndJob(job.JobID)
	if err := r.runJob(ctx, job); err != nil {
		r.stats.RecordRunResult("error", classifyRunnerError(err), err)
		return job.JobID, true, err
	}
	r.stats.RecordRunResult("success", "", nil)
	return job.JobID, true, nil
}

func (r *Runner) runJob(ctx context.Context, job contracts.Job) error {
	plan, err := parseExecutionPlan(job)
	if err != nil {
		_ = r.failJob(ctx, job.JobID, "invalid_execution_plan", err.Error())
		return err
	}
	claimed, err := r.claimJob(ctx, job.JobID)
	if err != nil {
		return err
	}
	_ = claimed
	if err := r.appendLog(ctx, job.JobID, "info", "claimed job"); err != nil {
		return err
	}
	if err := r.checkProviderInvokePolicy(ctx, job, plan); err != nil {
		_ = r.failJob(ctx, job.JobID, "policy_denied", err.Error())
		return err
	}

	var lease *contracts.Lease
	if plan.ResourceSelector != "" {
		lease, err = r.acquireLease(ctx, job.JobID, plan.ResourceSelector)
		if err != nil {
			_ = r.failJob(ctx, job.JobID, "resource_unavailable", err.Error())
			return err
		}
		defer func() {
			_, _ = r.releaseLease(context.Background(), lease.LeaseID, job.JobID, "job finished")
		}()
	}
	if plan.Route.NodeManaged {
		if err := r.ensureNodeService(ctx, plan.Route); err != nil {
			_ = r.failJob(ctx, job.JobID, "node_unavailable", err.Error())
			return err
		}
	}
	if err := r.heartbeatRunning(ctx, job.JobID); err != nil {
		return err
	}
	if err := r.appendLog(ctx, job.JobID, "info", "running provider invocation"); err != nil {
		return err
	}
	if terminal, err := r.jobTerminal(ctx, job.JobID); err != nil {
		return err
	} else if terminal {
		return nil
	}
	response, err := r.invokeProvider(ctx, job.JobID, plan, lease)
	if err != nil {
		_ = r.failJob(ctx, job.JobID, "provider_unavailable", err.Error())
		return err
	}
	if terminal, err := r.jobTerminal(ctx, job.JobID); err != nil {
		return err
	} else if terminal {
		return nil
	}
	artifactIDs, err := r.uploadArtifacts(ctx, job.JobID, plan.SubjectID, response.Artifacts)
	if err != nil {
		_ = r.failJob(ctx, job.JobID, "artifact_upload_failed", err.Error())
		return err
	}
	if err := r.completeJob(ctx, job.JobID, artifactIDs); err != nil {
		return err
	}
	_ = r.appendLog(ctx, job.JobID, "info", "job completed")
	return nil
}

func (r *Runner) nextQueuedJob(ctx context.Context) (contracts.Job, bool, error) {
	var data struct {
		Items []contracts.Job `json:"items"`
	}
	if err := r.getJSON(ctx, r.cfg.JobsURL+"/v1/jobs?state=queued&limit=1", &data); err != nil {
		return contracts.Job{}, false, err
	}
	if len(data.Items) == 0 {
		return contracts.Job{}, false, nil
	}
	return data.Items[0], true, nil
}

func (r *Runner) claimJob(ctx context.Context, jobID string) (contracts.Job, error) {
	var job contracts.Job
	err := r.postJSON(ctx, r.cfg.JobsURL+"/v1/jobs/"+url.PathEscape(jobID)+"/claim", contracts.JobClaimRequest{WorkerID: r.cfg.WorkerID, LeaseSeconds: 60}, "", &job)
	return job, err
}

func (r *Runner) heartbeatRunning(ctx context.Context, jobID string) error {
	var job contracts.Job
	if err := r.postJSON(ctx, r.cfg.JobsURL+"/v1/jobs/"+url.PathEscape(jobID)+"/heartbeat", contracts.JobHeartbeatRequest{WorkerID: r.cfg.WorkerID, TransitionTo: "running", StatusMessage: "running"}, "", &job); err != nil {
		return err
	}
	r.stats.RecordHeartbeat(jobID)
	return nil
}

func (r *Runner) completeJob(ctx context.Context, jobID string, artifactIDs []string) error {
	var job contracts.Job
	return r.postJSON(ctx, r.cfg.JobsURL+"/v1/jobs/"+url.PathEscape(jobID)+"/complete", contracts.JobCompleteRequest{WorkerID: r.cfg.WorkerID, ArtifactRefs: artifactIDs}, "", &job)
}

func (r *Runner) failJob(ctx context.Context, jobID, code, message string) error {
	var job contracts.Job
	return r.postJSON(ctx, r.cfg.JobsURL+"/v1/jobs/"+url.PathEscape(jobID)+"/fail", contracts.JobFailRequest{WorkerID: r.cfg.WorkerID, Error: contracts.ErrorObject{Code: code, Message: message, Retryable: false}}, "", &job)
}

func (r *Runner) appendLog(ctx context.Context, jobID, level, message string) error {
	var data map[string]any
	return r.postJSON(ctx, r.cfg.JobsURL+"/v1/jobs/"+url.PathEscape(jobID)+"/logs", contracts.AppendJobLogRequest{
		WorkerID: r.cfg.WorkerID,
		Entries: []contracts.JobLogEntry{{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Level:     level,
			Message:   message,
		}},
	}, "", &data)
}

func (r *Runner) jobTerminal(ctx context.Context, jobID string) (bool, error) {
	var job contracts.Job
	if err := r.getJSON(ctx, r.cfg.JobsURL+"/v1/jobs/"+url.PathEscape(jobID), &job); err != nil {
		return false, err
	}
	return isTerminalJobState(job.State), nil
}

func isTerminalJobState(state contracts.JobState) bool {
	return state == contracts.JobSucceeded || state == contracts.JobFailed || state == contracts.JobCanceled || state == contracts.JobExpired
}

func (r *Runner) acquireLease(ctx context.Context, jobID, selector string) (*contracts.Lease, error) {
	var request contracts.LeaseRequest
	err := r.postJSON(ctx, r.cfg.LeasesURL+"/v1/lease-requests", contracts.CreateLeaseRequest{
		RequesterID:             jobID,
		ResourceSelector:        selector,
		HeartbeatTimeoutSeconds: 60,
	}, "", &request)
	if err != nil {
		return nil, err
	}
	if request.Lease == nil {
		return nil, fmt.Errorf("lease request %s is %s", request.RequestID, request.State)
	}
	return request.Lease, nil
}

func (r *Runner) releaseLease(ctx context.Context, leaseID, holderID, reason string) (contracts.Lease, error) {
	var lease contracts.Lease
	headers := map[string]string{}
	if r.cfg.ActorSubjectID != "" {
		headers["X-Actor-Subject-ID"] = r.cfg.ActorSubjectID
	}
	err := r.postJSONWithHeaders(ctx, r.cfg.LeasesURL+"/v1/leases/"+url.PathEscape(leaseID)+"/release", contracts.LeaseReleaseRequest{HolderID: holderID, Reason: reason}, "runner-release-"+leaseID, headers, &lease)
	return lease, err
}

func (r *Runner) ensureNodeService(ctx context.Context, route contracts.CapabilityRoute) error {
	serviceID := route.ServiceID
	nodeURL, err := r.nodeURLForRoute(route)
	if err != nil {
		return err
	}
	waitCtx, cancel := context.WithTimeout(ctx, r.cfg.NodeStartTimeout)
	defer cancel()

	startIssued := false
	for {
		service, err := r.getNodeService(waitCtx, nodeURL, serviceID)
		if err != nil {
			return err
		}
		if service.Status == "running" {
			return nil
		}
		if !startIssued && service.Status != "starting" {
			service, err = r.startNodeService(waitCtx, nodeURL, serviceID)
			if err != nil {
				return err
			}
			startIssued = true
			if service.Status == "running" {
				return nil
			}
		}
		if startIssued && service.Status == "stopped" {
			return fmt.Errorf("node service %s stopped during startup", serviceID)
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("node service %s did not become running before timeout: %w", serviceID, waitCtx.Err())
		case <-time.After(r.cfg.NodePollInterval):
		}
	}
}

func (r *Runner) nodeURLForRoute(route contracts.CapabilityRoute) (string, error) {
	if route.NodeID != nil && *route.NodeID != "" {
		if nodeURL := r.cfg.NodeURLs[*route.NodeID]; nodeURL != "" {
			return nodeURL, nil
		}
		if r.cfg.NodeURL != "" {
			return r.cfg.NodeURL, nil
		}
		return "", fmt.Errorf("node URL is not configured for node_id %s", *route.NodeID)
	}
	if r.cfg.NodeURL != "" {
		return r.cfg.NodeURL, nil
	}
	if len(r.cfg.NodeURLs) == 1 {
		for _, nodeURL := range r.cfg.NodeURLs {
			return nodeURL, nil
		}
	}
	return "", errors.New("node URL is not configured for node-managed service")
}

func (r *Runner) getNodeService(ctx context.Context, nodeURL, serviceID string) (contracts.NodeService, error) {
	var service contracts.NodeService
	err := r.getJSON(ctx, nodeURL+"/v1/node/services/"+url.PathEscape(serviceID), &service)
	return service, err
}

func (r *Runner) startNodeService(ctx context.Context, nodeURL, serviceID string) (contracts.NodeService, error) {
	var service contracts.NodeService
	err := r.postJSON(ctx, nodeURL+"/v1/node/services/"+url.PathEscape(serviceID)+"/start", nil, "runner-start-"+serviceID, &service)
	return service, err
}

func (r *Runner) invokeProvider(ctx context.Context, jobID string, plan executionPlan, lease *contracts.Lease) (contracts.ProviderInvokeResponse, error) {
	invokeCtx := contracts.ProviderInvokeContext{
		SubjectID:       plan.SubjectID,
		JobID:           jobID,
		RequestID:       observability.RequestIDFromContext(ctx),
		ArtifactBaseURL: r.cfg.ArtifactsURL,
		DryRun:          false,
	}
	if lease != nil {
		invokeCtx.ResourceLeaseID = lease.LeaseID
	}
	var response contracts.ProviderInvokeResponse
	target := strings.TrimRight(plan.Route.ProviderEndpoint, "/") + plan.Route.ProviderInvokePath
	err := r.postJSON(ctx, target, contracts.ProviderInvokeRequest{Input: plan.Input, Context: invokeCtx}, "", &response)
	return response, err
}

func (r *Runner) checkProviderInvokePolicy(ctx context.Context, job contracts.Job, plan executionPlan) error {
	if r.cfg.PolicyURL == "" {
		return nil
	}
	subjectID := strings.TrimSpace(r.cfg.WorkerSubjectID)
	if subjectID == "" {
		var err error
		subjectID, err = r.verifyWorkerSubject(ctx)
		if err != nil {
			return err
		}
	}
	context := map[string]any{
		"job_id":           job.JobID,
		"owner_subject_id": plan.SubjectID,
		"capability_id":    plan.CapabilityID,
		"service_id":       plan.Route.ServiceID,
		"node_managed":     plan.Route.NodeManaged,
	}
	if plan.Route.NodeID != nil {
		context["node_id"] = *plan.Route.NodeID
	}
	var decision contracts.PolicyDecision
	if err := r.postJSON(ctx, r.cfg.PolicyURL+"/v1/policy/check", contracts.PolicyCheckRequest{
		SubjectID: subjectID,
		Action:    "provider.invoke",
		Resource:  plan.CapabilityID,
		Context:   context,
	}, "", &decision); err != nil {
		return err
	}
	if !decision.Allowed {
		reason := decision.Reason
		if reason == "" {
			reason = "policy_denied"
		}
		return fmt.Errorf("provider.invoke denied for %s: %s", plan.CapabilityID, reason)
	}
	return nil
}

func (r *Runner) verifyWorkerSubject(ctx context.Context) (string, error) {
	if r.cfg.ComponentCredential == "" {
		return "", errors.New("runner credential is required for provider.invoke policy checks")
	}
	var verification contracts.CredentialVerification
	if err := r.postJSON(ctx, r.cfg.PolicyURL+"/v1/auth/verify", contracts.VerifyCredentialRequest{
		Credential: r.cfg.ComponentCredential,
		Context:    map[string]any{"caller": "runner"},
	}, "", &verification); err != nil {
		return "", err
	}
	if !verification.Valid || verification.SubjectID == nil || *verification.SubjectID == "" {
		return "", errors.New("runner credential could not be verified for provider.invoke policy checks")
	}
	return *verification.SubjectID, nil
}

func (r *Runner) uploadArtifacts(ctx context.Context, jobID, ownerSubjectID string, artifacts []contracts.ProviderArtifact) ([]string, error) {
	ids := make([]string, 0, len(artifacts))
	for index, artifact := range artifacts {
		body, err := base64.StdEncoding.DecodeString(artifact.ContentBase64)
		if err != nil {
			return nil, err
		}
		checksum, digest := checksumAndDigest(body)
		if artifact.Checksum != "" && artifact.Checksum != checksum {
			return nil, fmt.Errorf("provider artifact checksum mismatch")
		}
		size := int64(len(body))
		name := artifact.Name
		if name == "" {
			name = "artifact-" + strconv.Itoa(index+1)
		}
		mediaType := artifact.MediaType
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		var upload contracts.ArtifactUploadSession
		create := contracts.CreateArtifactUploadRequest{
			Name:             name,
			MediaType:        mediaType,
			ProducerRef:      jobID,
			OwnerSubjectID:   ownerSubjectID,
			ExpectedSize:     &size,
			ExpectedChecksum: checksum,
		}
		keyPrefix := "runner-artifact-" + jobID + "-" + strconv.Itoa(index)
		if err := r.postJSON(ctx, r.cfg.ArtifactsURL+"/v1/artifact-uploads", create, keyPrefix+"-create", &upload); err != nil {
			return nil, err
		}
		if err := r.putBytes(ctx, r.cfg.ArtifactsURL+"/v1/artifact-uploads/"+url.PathEscape(upload.UploadID)+"/content", body, mediaType, digest, keyPrefix+"-content", &upload); err != nil {
			return nil, err
		}
		var completed contracts.Artifact
		if err := r.postJSON(ctx, r.cfg.ArtifactsURL+"/v1/artifact-uploads/"+url.PathEscape(upload.UploadID)+"/complete", contracts.CompleteArtifactUploadRequest{Checksum: checksum, Size: size}, keyPrefix+"-complete", &completed); err != nil {
			return nil, err
		}
		ids = append(ids, completed.ArtifactID)
	}
	return ids, nil
}

func parseExecutionPlan(job contracts.Job) (executionPlan, error) {
	rawPlan, ok := job.Metadata["execution_plan"]
	if !ok {
		return executionPlan{}, errors.New("job metadata is missing execution_plan")
	}
	raw, err := json.Marshal(rawPlan)
	if err != nil {
		return executionPlan{}, err
	}
	var plan executionPlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return executionPlan{}, err
	}
	if plan.CapabilityID == "" || plan.Route.ProviderEndpoint == "" || plan.Route.ProviderInvokePath == "" {
		return executionPlan{}, errors.New("execution_plan route is incomplete")
	}
	return plan, nil
}

func (r *Runner) getJSON(ctx context.Context, target string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	r.addAuth(req)
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeEnvelope(resp, out)
}

func (r *Runner) postJSON(ctx context.Context, target string, body any, idempotencyKey string, out any) error {
	return r.postJSONWithHeaders(ctx, target, body, idempotencyKey, nil, out)
}

func (r *Runner) postJSONWithHeaders(ctx context.Context, target string, body any, idempotencyKey string, headers map[string]string, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	r.addAuth(req)
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeEnvelope(resp, out)
}

func (r *Runner) putBytes(ctx context.Context, target string, body []byte, mediaType, digest, idempotencyKey string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mediaType)
	req.Header.Set("Digest", digest)
	req.Header.Set("Idempotency-Key", idempotencyKey)
	req.ContentLength = int64(len(body))
	r.addAuth(req)
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeEnvelope(resp, out)
}

func (r *Runner) addAuth(req *http.Request) {
	if r.cfg.ComponentCredential != "" {
		req.Header.Set("Authorization", r.cfg.ComponentCredential)
	}
	observability.PropagateRequestID(req.Context(), req)
}

func decodeEnvelope(resp *http.Response, out any) error {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope struct {
			Error contracts.ErrorObject `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&envelope)
		if envelope.Error.Message == "" {
			envelope.Error.Message = resp.Status
		}
		return errors.New(envelope.Error.Message)
	}
	var envelope struct {
		OK    bool                  `json:"ok"`
		Data  json.RawMessage       `json:"data"`
		Error contracts.ErrorObject `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return err
	}
	if !envelope.OK {
		return errors.New(envelope.Error.Message)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(envelope.Data, out)
}

func checksumAndDigest(body []byte) (string, string) {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), "sha-256=" + base64.StdEncoding.EncodeToString(sum[:])
}
