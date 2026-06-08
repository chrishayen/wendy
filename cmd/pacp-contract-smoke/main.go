package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"pacp/internal/contracts"
	"pacp/internal/distributedsmoke"
	"pacp/internal/openapicheck"
	"pacp/internal/testkit"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, http.DefaultClient))
}

func run(args []string, stdout, stderr io.Writer, httpClient *http.Client) int {
	flags := flag.NewFlagSet("pacp-contract-smoke", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", "testdata/contract-sim", "contract simulation root")
	scenarioManifest := flags.String("manifest", "fixtures/S003/manifest.json", "manifest path relative to root")
	componentURL := flags.String("component-url", "", "optional live component base URL to check instead of fixture simulation")
	componentKind := flags.String("component-kind", "", "component kind for -component-url: artifacts, catalog, gateway, jobs, leases, node, policy, or runner")
	componentCredential := flags.String("component-credential", "", "optional bearer credential for protected component checks")
	providerURL := flags.String("provider-url", "", "optional live provider base URL to check instead of fixture simulation")
	providerCredential := flags.String("provider-credential", "", "optional bearer credential for protected provider checks")
	capabilityID := flags.String("capability-id", "", "optional provider capability id to invoke")
	inputRaw := flags.String("input", "{}", "JSON object input for provider invocation")
	openAPIPaths := flags.String("openapi", "", "optional comma-separated OpenAPI files to validate")
	distributed := flags.Bool("distributed", false, "run the primary-plus-node distributed smoke suite")
	fakePublicAPIs := flags.Bool("fake-public-apis", false, "run contract checks against reusable C15 fake public APIs")
	timeout := flags.Duration("timeout", 5*time.Second, "smoke check timeout")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *openAPIPaths != "" {
		return runOpenAPICheck(*openAPIPaths, stdout, stderr)
	}
	if *distributed {
		return runDistributedSmoke(*timeout, stdout, stderr)
	}
	if *fakePublicAPIs {
		return runFakePublicAPISmoke(*timeout, stdout, stderr)
	}
	if *componentURL != "" {
		return runComponentSmoke(*componentURL, *componentKind, *componentCredential, *timeout, stdout, stderr, httpClient)
	}
	if *providerURL != "" {
		return runProviderSmoke(*providerURL, *providerCredential, *capabilityID, *inputRaw, *timeout, stdout, stderr, httpClient)
	}

	scenario, err := testkit.LoadScenario(*root, *scenarioManifest)
	if err != nil {
		fmt.Fprintf(stderr, "load failed: %v\n", err)
		return 1
	}
	report := testkit.ValidateScenario(scenario)
	fmt.Fprintf(stdout, "scenario=%s status=%s packages=%d files=%d fixtures=%d\n",
		scenario.Manifest.ScenarioID, scenario.Manifest.Status, len(scenario.Packages), report.Files, report.Fixtures)
	if report.Passed() {
		fmt.Fprintln(stdout, "contract-smoke=pass")
		return 0
	}
	for _, finding := range report.Findings {
		if finding.Fixture == "" {
			fmt.Fprintf(stderr, "%s: %s: %s\n", finding.File, finding.Code, finding.Message)
			continue
		}
		fmt.Fprintf(stderr, "%s:%s: %s: %s\n", finding.File, finding.Fixture, finding.Code, finding.Message)
	}
	return 1
}

func runDistributedSmoke(timeout time.Duration, stdout, stderr io.Writer) int {
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	report := distributedsmoke.Run(ctx)
	fmt.Fprintf(stdout, "distributed-smoke=checked checks=%d job_id=%s artifact_id=%s\n", len(report.Checks), report.JobID, report.ArtifactID)
	for _, check := range report.Checks {
		status := "fail"
		if check.OK {
			status = "pass"
		}
		if check.HTTPStatus != 0 {
			fmt.Fprintf(stdout, "check=%s status=%s http_status=%d\n", check.Name, status, check.HTTPStatus)
			continue
		}
		fmt.Fprintf(stdout, "check=%s status=%s\n", check.Name, status)
	}
	if report.Passed() {
		fmt.Fprintln(stdout, "distributed-smoke=pass")
		return 0
	}
	for _, check := range report.Checks {
		if !check.OK {
			fmt.Fprintf(stderr, "%s: %s\n", check.Name, check.Error)
		}
	}
	return 1
}

type fakePublicAPICheck struct {
	Name       string
	OK         bool
	HTTPStatus int
	Error      string
}

func runFakePublicAPISmoke(timeout time.Duration, stdout, stderr io.Writer) int {
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	checks := []fakePublicAPICheck{}
	componentKinds := []string{"artifacts", "catalog", "gateway", "jobs", "leases", "node", "policy", "runner"}
	for _, kind := range componentKinds {
		handler, err := testkit.NewFakeComponentHandler(testkit.FakeComponentConfig{Kind: kind})
		if err != nil {
			checks = append(checks, fakePublicAPICheck{Name: "fake.component." + kind + ".create", Error: err.Error()})
			continue
		}
		server := httptest.NewServer(handler)
		report := testkit.CheckComponent(ctx, server.Client(), testkit.ComponentCheckOptions{
			BaseURL:   server.URL,
			Kind:      kind,
			RequestID: "req_contract_fake_" + kind,
		})
		server.Close()
		for _, check := range report.Checks {
			checks = append(checks, fakePublicAPICheck{
				Name:       "fake.component." + kind + "." + check.Name,
				OK:         check.OK,
				HTTPStatus: check.HTTPStatus,
				Error:      check.Error,
			})
		}
	}

	appendFakeArtifactsChecks(ctx, &checks)
	appendFakeJobsChecks(ctx, &checks)
	appendFakeLeasesChecks(ctx, &checks)

	handler, err := testkit.NewFakeProviderHandler(testkit.FakeProviderConfig{Endpoint: "http://provider.fake"})
	if err != nil {
		checks = append(checks, fakePublicAPICheck{Name: "fake.provider.create", Error: err.Error()})
	} else {
		server := httptest.NewServer(handler)
		echoReport := testkit.CheckProvider(ctx, server.Client(), testkit.ProviderCheckOptions{
			BaseURL:      server.URL,
			CapabilityID: "cap_echo",
			Input:        map[string]any{"message": "hello"},
			RequestID:    "req_contract_fake_provider",
		})
		appendFakeProviderChecks(&checks, "fake.provider.echo.", echoReport)
		artifactReport := testkit.CheckProvider(ctx, server.Client(), testkit.ProviderCheckOptions{
			BaseURL:      server.URL,
			CapabilityID: "cap_artifact",
			Input:        map[string]any{"prompt": "hello"},
			RequestID:    "req_contract_fake_provider_artifact",
		})
		appendFakeProviderChecks(&checks, "fake.provider.artifact.", artifactReport)
		asyncReport := testkit.CheckProvider(ctx, server.Client(), testkit.ProviderCheckOptions{
			BaseURL:      server.URL,
			CapabilityID: "cap_async_accept",
			Input:        map[string]any{},
			RequestID:    "req_contract_fake_provider_async",
		})
		appendFakeProviderChecks(&checks, "fake.provider.async.", asyncReport)
		errorReport := testkit.CheckProviderExpectedError(ctx, server.Client(), testkit.ProviderExpectedErrorOptions{
			BaseURL:        server.URL,
			CapabilityID:   "cap_fail",
			WantHTTPStatus: http.StatusServiceUnavailable,
			WantCode:       "provider_unavailable",
			RequestID:      "req_contract_fake_provider_failure",
		})
		appendFakeProviderChecks(&checks, "fake.provider.failure.", errorReport)
		server.Close()
	}

	appendFakeNodeChecks(ctx, &checks)
	appendFakePolicyChecks(ctx, &checks)

	passed := true
	fmt.Fprintf(stdout, "fake-public-apis=checked components=%d checks=%d\n", len(componentKinds), len(checks))
	for _, check := range checks {
		status := "fail"
		if check.OK {
			status = "pass"
		} else {
			passed = false
		}
		if check.HTTPStatus != 0 {
			fmt.Fprintf(stdout, "check=%s status=%s http_status=%d\n", check.Name, status, check.HTTPStatus)
			continue
		}
		fmt.Fprintf(stdout, "check=%s status=%s\n", check.Name, status)
	}
	if passed {
		fmt.Fprintln(stdout, "fake-public-apis=pass")
		return 0
	}
	for _, check := range checks {
		if !check.OK {
			fmt.Fprintf(stderr, "%s: %s\n", check.Name, check.Error)
		}
	}
	return 1
}

type fakePolicyEnvelope struct {
	OK    bool                  `json:"ok"`
	Data  json.RawMessage       `json:"data"`
	Error contracts.ErrorObject `json:"error"`
}

func appendFakeArtifactsChecks(ctx context.Context, checks *[]fakePublicAPICheck) {
	handler, err := testkit.NewFakeArtifactsHandler(testkit.FakeArtifactsConfig{})
	if err != nil {
		*checks = append(*checks, fakePublicAPICheck{Name: "fake.artifacts.create", Error: err.Error()})
		return
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	*checks = append(*checks,
		checkFakeArtifactsAvailable(ctx, server.Client(), server.URL),
		checkFakeArtifactContent(ctx, server.Client(), server.URL),
		requestFakeArtifactsExpectedError(ctx, server.Client(), server.URL, http.MethodGet, "/v1/artifacts/art_fake_denied", "fake.artifacts.denied.forbidden", http.StatusForbidden, "forbidden", nil, nil),
		requestFakeArtifactsExpectedError(ctx, server.Client(), server.URL, http.MethodGet, "/v1/artifacts/art_fake_expired", "fake.artifacts.expired", http.StatusGone, "artifact_expired", nil, nil),
		requestFakeArtifactsExpectedError(ctx, server.Client(), server.URL, http.MethodGet, "/v1/artifacts/art_missing", "fake.artifacts.missing.not_found", http.StatusNotFound, "not_found", nil, nil),
		checkFakeArtifactUploadLifecycle(ctx, server.Client(), server.URL),
	)

	unavailable, err := testkit.NewFakeArtifactsHandler(testkit.FakeArtifactsConfig{Behavior: testkit.FakeComponentUnavailable})
	if err != nil {
		*checks = append(*checks, fakePublicAPICheck{Name: "fake.artifacts.unavailable.create", Error: err.Error()})
		return
	}
	unavailableServer := httptest.NewServer(unavailable)
	defer unavailableServer.Close()
	*checks = append(*checks, requestFakeArtifactsExpectedError(ctx, unavailableServer.Client(), unavailableServer.URL, http.MethodGet, "/v1/artifacts/health", "fake.artifacts.unavailable.component_unavailable", http.StatusServiceUnavailable, "component_unavailable", nil, nil))
}

func checkFakeArtifactsAvailable(ctx context.Context, client *http.Client, baseURL string) fakePublicAPICheck {
	var list struct {
		Items []contracts.Artifact `json:"items"`
	}
	check := requestFakeArtifactsJSON(ctx, client, baseURL, http.MethodGet, "/v1/artifacts?producer_ref=job_fake_001", "fake.artifacts.available.metadata", nil, nil, &list)
	if !check.OK {
		return check
	}
	if len(list.Items) != 1 || list.Items[0].ArtifactID != "art_fake_available" {
		check.OK = false
		check.Error = fmt.Sprintf("list = %#v", list.Items)
		return check
	}
	var artifact contracts.Artifact
	check = requestFakeArtifactsJSON(ctx, client, baseURL, http.MethodGet, "/v1/artifacts/art_fake_available", "fake.artifacts.available.metadata", nil, nil, &artifact)
	if !check.OK {
		return check
	}
	if artifact.OwnerSubjectID != "sub_fake_agent" {
		check.OK = false
		check.Error = fmt.Sprintf("artifact = %#v", artifact)
	}
	return check
}

func checkFakeArtifactContent(ctx context.Context, client *http.Client, baseURL string) fakePublicAPICheck {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/artifacts/art_fake_available/content", nil)
	if err != nil {
		return fakePublicAPICheck{Name: "fake.artifacts.available.content", Error: err.Error()}
	}
	req.Header.Set("X-Request-ID", "req_contract_fake_artifacts")
	resp, err := client.Do(req)
	if err != nil {
		return fakePublicAPICheck{Name: "fake.artifacts.available.content", Error: err.Error()}
	}
	defer resp.Body.Close()
	check := fakePublicAPICheck{Name: "fake.artifacts.available.content", HTTPStatus: resp.StatusCode}
	if resp.StatusCode != http.StatusOK {
		check.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return check
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		check.Error = err.Error()
		return check
	}
	if string(body) != "fake artifact body" || resp.Header.Get("Digest") != checksumStringForSmoke(body) {
		check.Error = "artifact content body or digest mismatch"
		return check
	}
	check.OK = true
	return check
}

func checkFakeArtifactUploadLifecycle(ctx context.Context, client *http.Client, baseURL string) fakePublicAPICheck {
	var upload contracts.ArtifactUploadSession
	check := requestFakeArtifactsJSON(ctx, client, baseURL, http.MethodPost, "/v1/artifact-uploads", "fake.artifacts.upload.lifecycle", map[string]string{
		"Idempotency-Key": "fake-artifacts-create",
	}, contracts.CreateArtifactUploadRequest{
		Name:           "smoke.txt",
		MediaType:      "text/plain",
		ProducerRef:    "job_smoke",
		OwnerSubjectID: "sub_smoke",
	}, &upload)
	if !check.OK {
		return check
	}
	body := []byte("smoke artifact")
	check = putFakeArtifactContent(ctx, client, baseURL, upload.UploadID, "fake.artifacts.upload.lifecycle", body)
	if !check.OK {
		return check
	}
	var artifact contracts.Artifact
	check = requestFakeArtifactsJSON(ctx, client, baseURL, http.MethodPost, "/v1/artifact-uploads/"+upload.UploadID+"/complete", "fake.artifacts.upload.lifecycle", map[string]string{
		"Idempotency-Key": "fake-artifacts-complete",
	}, contracts.CompleteArtifactUploadRequest{
		Checksum: checksumStringForSmoke(body),
		Size:     int64(len(body)),
	}, &artifact)
	if !check.OK {
		return check
	}
	if artifact.ProducerRef != "job_smoke" || artifact.OwnerSubjectID != "sub_smoke" {
		check.OK = false
		check.Error = fmt.Sprintf("artifact = %#v", artifact)
	}
	return check
}

func putFakeArtifactContent(ctx context.Context, client *http.Client, baseURL, uploadID, name string, body []byte) fakePublicAPICheck {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, baseURL+"/v1/artifact-uploads/"+uploadID+"/content", bytes.NewReader(body))
	if err != nil {
		return fakePublicAPICheck{Name: name, Error: err.Error()}
	}
	req.Header.Set("X-Request-ID", "req_contract_fake_artifacts")
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Idempotency-Key", "fake-artifacts-content")
	resp, err := client.Do(req)
	if err != nil {
		return fakePublicAPICheck{Name: name, Error: err.Error()}
	}
	defer resp.Body.Close()
	check := fakePublicAPICheck{Name: name, HTTPStatus: resp.StatusCode}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		check.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return check
	}
	var envelope fakePolicyEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		check.Error = err.Error()
		return check
	}
	if !envelope.OK {
		check.Error = envelope.Error.Message
		return check
	}
	check.OK = true
	return check
}

func requestFakeArtifactsJSON(ctx context.Context, client *http.Client, baseURL, method, path, name string, headers map[string]string, body any, out any) fakePublicAPICheck {
	req, err := newFakeJSONRequest(ctx, method, baseURL+path, body)
	if err != nil {
		return fakePublicAPICheck{Name: name, Error: err.Error()}
	}
	req.Header.Set("X-Request-ID", "req_contract_fake_artifacts")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fakePublicAPICheck{Name: name, Error: err.Error()}
	}
	defer resp.Body.Close()
	check := fakePublicAPICheck{Name: name, HTTPStatus: resp.StatusCode}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		check.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return check
	}
	var envelope fakePolicyEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		check.Error = err.Error()
		return check
	}
	if !envelope.OK {
		check.Error = envelope.Error.Message
		return check
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		check.Error = err.Error()
		return check
	}
	check.OK = true
	return check
}

func requestFakeArtifactsExpectedError(ctx context.Context, client *http.Client, baseURL, method, path, name string, wantStatus int, wantCode string, headers map[string]string, body any) fakePublicAPICheck {
	req, err := newFakeJSONRequest(ctx, method, baseURL+path, body)
	if err != nil {
		return fakePublicAPICheck{Name: name, Error: err.Error()}
	}
	req.Header.Set("X-Request-ID", "req_contract_fake_artifacts")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fakePublicAPICheck{Name: name, Error: err.Error()}
	}
	defer resp.Body.Close()
	check := fakePublicAPICheck{Name: name, HTTPStatus: resp.StatusCode}
	var envelope fakePolicyEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		check.Error = err.Error()
		return check
	}
	if resp.StatusCode != wantStatus {
		check.Error = fmt.Sprintf("HTTP %d, want %d", resp.StatusCode, wantStatus)
		return check
	}
	if envelope.OK || envelope.Error.Code != wantCode {
		check.Error = fmt.Sprintf("error code = %q, want %q", envelope.Error.Code, wantCode)
		return check
	}
	check.OK = true
	return check
}

func checksumStringForSmoke(body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf("sha256:%x", sum)
}

func appendFakeJobsChecks(ctx context.Context, checks *[]fakePublicAPICheck) {
	handler, err := testkit.NewFakeJobsHandler(testkit.FakeJobsConfig{})
	if err != nil {
		*checks = append(*checks, fakePublicAPICheck{Name: "fake.jobs.create", Error: err.Error()})
		return
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	*checks = append(*checks,
		checkFakeJobsStates(ctx, server.Client(), server.URL),
		checkFakeJobsCreateIdempotency(ctx, server.Client(), server.URL),
		checkFakeJobsLifecycle(ctx, server.Client(), server.URL),
		checkFakeJobsCancel(ctx, server.Client(), server.URL),
		requestFakeJobsExpectedError(ctx, server.Client(), server.URL, http.MethodGet, "/v1/jobs/job_fake_missing", "fake.jobs.missing.not_found", http.StatusNotFound, "not_found", nil, nil),
	)

	unavailable, err := testkit.NewFakeJobsHandler(testkit.FakeJobsConfig{Behavior: testkit.FakeComponentUnavailable})
	if err != nil {
		*checks = append(*checks, fakePublicAPICheck{Name: "fake.jobs.unavailable.create", Error: err.Error()})
		return
	}
	unavailableServer := httptest.NewServer(unavailable)
	defer unavailableServer.Close()
	*checks = append(*checks, requestFakeJobsExpectedError(ctx, unavailableServer.Client(), unavailableServer.URL, http.MethodGet, "/v1/jobs/health", "fake.jobs.unavailable.component_unavailable", http.StatusServiceUnavailable, "component_unavailable", nil, nil))
}

func checkFakeJobsStates(ctx context.Context, client *http.Client, baseURL string) fakePublicAPICheck {
	var list struct {
		Items []contracts.Job `json:"items"`
	}
	check := requestFakeJobsJSON(ctx, client, baseURL, http.MethodGet, "/v1/jobs", "fake.jobs.states", nil, nil, &list)
	if !check.OK {
		return check
	}
	states := map[contracts.JobState]bool{}
	for _, job := range list.Items {
		states[job.State] = true
	}
	for _, want := range []contracts.JobState{
		contracts.JobQueued,
		contracts.JobClaimed,
		contracts.JobRunning,
		contracts.JobSucceeded,
		contracts.JobFailed,
		contracts.JobCanceled,
		contracts.JobExpired,
	} {
		if !states[want] {
			check.OK = false
			check.Error = "job state missing: " + string(want)
			return check
		}
	}
	return check
}

func checkFakeJobsCreateIdempotency(ctx context.Context, client *http.Client, baseURL string) fakePublicAPICheck {
	request := contracts.CreateJobRequest{RequesterID: "sub_fake_agent", CapabilityID: "cap_fake"}
	var created contracts.Job
	check := requestFakeJobsJSON(ctx, client, baseURL, http.MethodPost, "/v1/jobs", "fake.jobs.create_idempotency", map[string]string{
		"Idempotency-Key": "fake-jobs-create",
	}, request, &created)
	if !check.OK {
		return check
	}
	var replayed contracts.Job
	replay := requestFakeJobsJSON(ctx, client, baseURL, http.MethodPost, "/v1/jobs", "fake.jobs.create_idempotency", map[string]string{
		"Idempotency-Key": "fake-jobs-create",
	}, request, &replayed)
	if !replay.OK {
		return replay
	}
	if created.JobID == "" || replayed.JobID != created.JobID {
		check.OK = false
		check.Error = fmt.Sprintf("created job %q replayed as %q", created.JobID, replayed.JobID)
		return check
	}
	return check
}

func checkFakeJobsLifecycle(ctx context.Context, client *http.Client, baseURL string) fakePublicAPICheck {
	var job contracts.Job
	check := requestFakeJobsJSON(ctx, client, baseURL, http.MethodPost, "/v1/jobs/job_fake_queued/claim", "fake.jobs.lifecycle.succeed", nil, contracts.JobClaimRequest{
		WorkerID:     "runner_fake_smoke",
		LeaseSeconds: 30,
	}, &job)
	if !check.OK {
		return check
	}
	if job.State != contracts.JobClaimed {
		check.OK = false
		check.Error = "claim did not set claimed state"
		return check
	}
	check = requestFakeJobsJSON(ctx, client, baseURL, http.MethodPost, "/v1/jobs/job_fake_queued/heartbeat", "fake.jobs.lifecycle.succeed", nil, contracts.JobHeartbeatRequest{
		WorkerID:     "runner_fake_smoke",
		TransitionTo: string(contracts.JobRunning),
	}, &job)
	if !check.OK {
		return check
	}
	if job.State != contracts.JobRunning {
		check.OK = false
		check.Error = "heartbeat did not set running state"
		return check
	}
	check = requestFakeJobsJSON(ctx, client, baseURL, http.MethodPost, "/v1/jobs/job_fake_queued/complete", "fake.jobs.lifecycle.succeed", nil, contracts.JobCompleteRequest{
		WorkerID:     "runner_fake_smoke",
		ArtifactRefs: []string{"art_fake_smoke"},
	}, &job)
	if !check.OK {
		return check
	}
	if job.State != contracts.JobSucceeded || len(job.ArtifactRefs) != 1 {
		check.OK = false
		check.Error = "complete did not set succeeded state and artifact refs"
	}
	return check
}

func checkFakeJobsCancel(ctx context.Context, client *http.Client, baseURL string) fakePublicAPICheck {
	var job contracts.Job
	check := requestFakeJobsJSON(ctx, client, baseURL, http.MethodPost, "/v1/jobs/job_fake_cancelable/cancel", "fake.jobs.cancel", map[string]string{
		"Idempotency-Key": "fake-jobs-cancel",
	}, contracts.CancelRequest{Reason: "stop fake job"}, &job)
	if !check.OK {
		return check
	}
	if job.State != contracts.JobCanceled || job.StatusMessage != "stop fake job" {
		check.OK = false
		check.Error = fmt.Sprintf("canceled job = %#v", job)
		return check
	}
	var replayed contracts.Job
	replay := requestFakeJobsJSON(ctx, client, baseURL, http.MethodPost, "/v1/jobs/job_fake_cancelable/cancel", "fake.jobs.cancel", map[string]string{
		"Idempotency-Key": "fake-jobs-cancel",
	}, contracts.CancelRequest{Reason: "stop fake job"}, &replayed)
	if !replay.OK {
		return replay
	}
	if replayed.State != contracts.JobCanceled {
		check.OK = false
		check.Error = "cancel replay did not return canceled job"
	}
	return check
}

func requestFakeJobsJSON(ctx context.Context, client *http.Client, baseURL, method, path, name string, headers map[string]string, body any, out any) fakePublicAPICheck {
	req, err := newFakeJSONRequest(ctx, method, baseURL+path, body)
	if err != nil {
		return fakePublicAPICheck{Name: name, Error: err.Error()}
	}
	req.Header.Set("X-Request-ID", "req_contract_fake_jobs")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fakePublicAPICheck{Name: name, Error: err.Error()}
	}
	defer resp.Body.Close()
	check := fakePublicAPICheck{Name: name, HTTPStatus: resp.StatusCode}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		check.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return check
	}
	var envelope fakePolicyEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		check.Error = err.Error()
		return check
	}
	if !envelope.OK {
		check.Error = envelope.Error.Message
		return check
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		check.Error = err.Error()
		return check
	}
	check.OK = true
	return check
}

func requestFakeJobsExpectedError(ctx context.Context, client *http.Client, baseURL, method, path, name string, wantStatus int, wantCode string, headers map[string]string, body any) fakePublicAPICheck {
	req, err := newFakeJSONRequest(ctx, method, baseURL+path, body)
	if err != nil {
		return fakePublicAPICheck{Name: name, Error: err.Error()}
	}
	req.Header.Set("X-Request-ID", "req_contract_fake_jobs")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fakePublicAPICheck{Name: name, Error: err.Error()}
	}
	defer resp.Body.Close()
	check := fakePublicAPICheck{Name: name, HTTPStatus: resp.StatusCode}
	var envelope fakePolicyEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		check.Error = err.Error()
		return check
	}
	if resp.StatusCode != wantStatus {
		check.Error = fmt.Sprintf("HTTP %d, want %d", resp.StatusCode, wantStatus)
		return check
	}
	if envelope.OK || envelope.Error.Code != wantCode {
		check.Error = fmt.Sprintf("error code = %q, want %q", envelope.Error.Code, wantCode)
		return check
	}
	check.OK = true
	return check
}

func newFakeJSONRequest(ctx context.Context, method, endpoint string, body any) (*http.Request, error) {
	if body == nil {
		return http.NewRequestWithContext(ctx, method, endpoint, nil)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func appendFakeLeasesChecks(ctx context.Context, checks *[]fakePublicAPICheck) {
	handler, err := testkit.NewFakeLeasesHandler(testkit.FakeLeasesConfig{})
	if err != nil {
		*checks = append(*checks, fakePublicAPICheck{Name: "fake.leases.create", Error: err.Error()})
		return
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	*checks = append(*checks,
		checkFakeLeasesResourceStates(ctx, server.Client(), server.URL),
		checkFakeLeasesRequestStates(ctx, server.Client(), server.URL),
		checkFakeLeasesCreateGrant(ctx, server.Client(), server.URL),
		checkFakeLeasesReleasePromotes(ctx, server.Client(), server.URL),
		requestFakeLeasesExpectedError(ctx, server.Client(), server.URL, http.MethodPost, "/v1/lease-requests", "fake.leases.denied.resource_unavailable", http.StatusConflict, "resource_unavailable", nil, contracts.CreateLeaseRequest{
			RequesterID:      "job_denied",
			ResourceSelector: "missing",
		}),
	)

	unavailable, err := testkit.NewFakeLeasesHandler(testkit.FakeLeasesConfig{Behavior: testkit.FakeComponentUnavailable})
	if err != nil {
		*checks = append(*checks, fakePublicAPICheck{Name: "fake.leases.unavailable.create", Error: err.Error()})
		return
	}
	unavailableServer := httptest.NewServer(unavailable)
	defer unavailableServer.Close()
	*checks = append(*checks, requestFakeLeasesExpectedError(ctx, unavailableServer.Client(), unavailableServer.URL, http.MethodGet, "/v1/leases/health", "fake.leases.unavailable.component_unavailable", http.StatusServiceUnavailable, "component_unavailable", nil, nil))
}

func checkFakeLeasesResourceStates(ctx context.Context, client *http.Client, baseURL string) fakePublicAPICheck {
	var list struct {
		Items []contracts.ResourceRecord `json:"items"`
	}
	check := requestFakeLeasesJSON(ctx, client, baseURL, http.MethodGet, "/v1/resources", "fake.leases.resources.states", nil, nil, &list)
	if !check.OK {
		return check
	}
	statuses := map[contracts.ResourceStatus]bool{}
	for _, resource := range list.Items {
		statuses[resource.Status] = true
	}
	if !statuses[contracts.ResourceAvailable] || !statuses[contracts.ResourceUnavailable] {
		check.OK = false
		check.Error = "available and unavailable resources are required"
	}
	return check
}

func checkFakeLeasesRequestStates(ctx context.Context, client *http.Client, baseURL string) fakePublicAPICheck {
	for requesterID, wantState := range map[string]contracts.LeaseRequestState{
		"job_fake_holder":   contracts.LeaseRequestGranted,
		"job_fake_waiting":  contracts.LeaseRequestPending,
		"job_fake_expired":  contracts.LeaseRequestExpired,
		"job_fake_canceled": contracts.LeaseRequestCanceled,
	} {
		var list struct {
			Items []contracts.LeaseRequest `json:"items"`
		}
		check := requestFakeLeasesJSON(ctx, client, baseURL, http.MethodGet, "/v1/lease-requests?requester_id="+requesterID, "fake.leases.requests.states", nil, nil, &list)
		if !check.OK {
			return check
		}
		if len(list.Items) != 1 || list.Items[0].State != wantState {
			check.OK = false
			check.Error = fmt.Sprintf("requester %s state = %#v, want %s", requesterID, list.Items, wantState)
			return check
		}
	}
	return fakePublicAPICheck{Name: "fake.leases.requests.states", OK: true, HTTPStatus: http.StatusOK}
}

func checkFakeLeasesCreateGrant(ctx context.Context, client *http.Client, baseURL string) fakePublicAPICheck {
	var request contracts.LeaseRequest
	check := requestFakeLeasesJSON(ctx, client, baseURL, http.MethodPost, "/v1/lease-requests", "fake.leases.create.grant", nil, contracts.CreateLeaseRequest{
		RequesterID:      "job_cpu_smoke",
		ResourceSelector: "cpu",
	}, &request)
	if !check.OK {
		return check
	}
	if request.State != contracts.LeaseRequestGranted || request.Lease == nil || request.Lease.HolderID != "job_cpu_smoke" {
		check.OK = false
		check.Error = fmt.Sprintf("request = %#v", request)
	}
	return check
}

func checkFakeLeasesReleasePromotes(ctx context.Context, client *http.Client, baseURL string) fakePublicAPICheck {
	var released contracts.Lease
	check := requestFakeLeasesJSON(ctx, client, baseURL, http.MethodPost, "/v1/leases/lease_fake_active/release", "fake.leases.release.promotes", map[string]string{
		"Idempotency-Key":    "fake-leases-release",
		"X-Actor-Subject-ID": "sub_runner",
	}, contracts.LeaseReleaseRequest{
		HolderID: "job_fake_holder",
		Reason:   "done",
	}, &released)
	if !check.OK {
		return check
	}
	if released.ReleasedBy != "sub_runner" {
		check.OK = false
		check.Error = fmt.Sprintf("released lease = %#v", released)
		return check
	}
	var request contracts.LeaseRequest
	check = requestFakeLeasesJSON(ctx, client, baseURL, http.MethodGet, "/v1/lease-requests/lease_req_fake_pending", "fake.leases.release.promotes", nil, nil, &request)
	if !check.OK {
		return check
	}
	if request.State != contracts.LeaseRequestGranted || request.Lease == nil || request.Lease.HolderID != "job_fake_waiting" {
		check.OK = false
		check.Error = fmt.Sprintf("promoted request = %#v", request)
	}
	return check
}

func requestFakeLeasesJSON(ctx context.Context, client *http.Client, baseURL, method, path, name string, headers map[string]string, body any, out any) fakePublicAPICheck {
	req, err := newFakeJSONRequest(ctx, method, baseURL+path, body)
	if err != nil {
		return fakePublicAPICheck{Name: name, Error: err.Error()}
	}
	req.Header.Set("X-Request-ID", "req_contract_fake_leases")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fakePublicAPICheck{Name: name, Error: err.Error()}
	}
	defer resp.Body.Close()
	check := fakePublicAPICheck{Name: name, HTTPStatus: resp.StatusCode}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		check.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return check
	}
	var envelope fakePolicyEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		check.Error = err.Error()
		return check
	}
	if !envelope.OK {
		check.Error = envelope.Error.Message
		return check
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		check.Error = err.Error()
		return check
	}
	check.OK = true
	return check
}

func requestFakeLeasesExpectedError(ctx context.Context, client *http.Client, baseURL, method, path, name string, wantStatus int, wantCode string, headers map[string]string, body any) fakePublicAPICheck {
	req, err := newFakeJSONRequest(ctx, method, baseURL+path, body)
	if err != nil {
		return fakePublicAPICheck{Name: name, Error: err.Error()}
	}
	req.Header.Set("X-Request-ID", "req_contract_fake_leases")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fakePublicAPICheck{Name: name, Error: err.Error()}
	}
	defer resp.Body.Close()
	check := fakePublicAPICheck{Name: name, HTTPStatus: resp.StatusCode}
	var envelope fakePolicyEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		check.Error = err.Error()
		return check
	}
	if resp.StatusCode != wantStatus {
		check.Error = fmt.Sprintf("HTTP %d, want %d", resp.StatusCode, wantStatus)
		return check
	}
	if envelope.OK || envelope.Error.Code != wantCode {
		check.Error = fmt.Sprintf("error code = %q, want %q", envelope.Error.Code, wantCode)
		return check
	}
	check.OK = true
	return check
}

func appendFakeNodeChecks(ctx context.Context, checks *[]fakePublicAPICheck) {
	handler, err := testkit.NewFakeNodeHandler(testkit.FakeNodeConfig{})
	if err != nil {
		*checks = append(*checks, fakePublicAPICheck{Name: "fake.node.create", Error: err.Error()})
		return
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	*checks = append(*checks,
		checkFakeNodeServiceStates(ctx, server.Client(), server.URL),
		checkFakeNodeServiceDetail(ctx, server.Client(), server.URL),
		checkFakeNodeMissingIdempotency(ctx, server.Client(), server.URL),
		checkFakeNodeLifecycle(ctx, server.Client(), server.URL, "start"),
		checkFakeNodeLifecycle(ctx, server.Client(), server.URL, "stop"),
	)

	unavailable, err := testkit.NewFakeNodeHandler(testkit.FakeNodeConfig{Behavior: testkit.FakeComponentUnavailable})
	if err != nil {
		*checks = append(*checks, fakePublicAPICheck{Name: "fake.node.unreachable.create", Error: err.Error()})
		return
	}
	unavailableServer := httptest.NewServer(unavailable)
	defer unavailableServer.Close()
	*checks = append(*checks, requestFakeNodeExpectedError(ctx, unavailableServer.Client(), unavailableServer.URL, http.MethodGet, "/v1/node/health", "fake.node.unreachable.component_unavailable", http.StatusServiceUnavailable, "component_unavailable", nil))
}

func checkFakeNodeServiceStates(ctx context.Context, client *http.Client, baseURL string) fakePublicAPICheck {
	var list struct {
		Items []contracts.NodeService `json:"items"`
	}
	check := requestFakeNodeJSON(ctx, client, baseURL, http.MethodGet, "/v1/node/services", "fake.node.services.states", nil, &list)
	if !check.OK {
		return check
	}
	statuses := map[string]bool{}
	for _, service := range list.Items {
		statuses[service.Status] = true
	}
	for _, want := range []string{"running", "stopped", "starting", "failed"} {
		if !statuses[want] {
			check.OK = false
			check.Error = "service status missing: " + want
			return check
		}
	}
	return check
}

func checkFakeNodeServiceDetail(ctx context.Context, client *http.Client, baseURL string) fakePublicAPICheck {
	var service contracts.NodeService
	check := requestFakeNodeJSON(ctx, client, baseURL, http.MethodGet, "/v1/node/services/svc_fake_failed", "fake.node.service.failed_detail", nil, &service)
	if !check.OK {
		return check
	}
	if service.ServiceID != "svc_fake_failed" || service.Status != "failed" {
		check.OK = false
		check.Error = fmt.Sprintf("service = %#v", service)
	}
	return check
}

func checkFakeNodeMissingIdempotency(ctx context.Context, client *http.Client, baseURL string) fakePublicAPICheck {
	return requestFakeNodeExpectedError(ctx, client, baseURL, http.MethodPost, "/v1/node/services/svc_fake_stopped/start", "fake.node.lifecycle.missing_idempotency", http.StatusBadRequest, "missing_idempotency_key", nil)
}

func checkFakeNodeLifecycle(ctx context.Context, client *http.Client, baseURL, operation string) fakePublicAPICheck {
	path := "/v1/node/services/svc_fake_stopped/" + operation
	wantStatus := "starting"
	if operation == "stop" {
		wantStatus = "stopped"
	}
	var service contracts.NodeService
	check := requestFakeNodeJSON(ctx, client, baseURL, http.MethodPost, path, "fake.node.lifecycle."+operation, map[string]string{
		"Idempotency-Key": "fake-node-" + operation,
	}, &service)
	if !check.OK {
		return check
	}
	if service.Status != wantStatus {
		check.OK = false
		check.Error = fmt.Sprintf("service status = %q, want %q", service.Status, wantStatus)
	}
	return check
}

func requestFakeNodeJSON(ctx context.Context, client *http.Client, baseURL, method, path, name string, headers map[string]string, out any) fakePublicAPICheck {
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, nil)
	if err != nil {
		return fakePublicAPICheck{Name: name, Error: err.Error()}
	}
	req.Header.Set("X-Request-ID", "req_contract_fake_node")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fakePublicAPICheck{Name: name, Error: err.Error()}
	}
	defer resp.Body.Close()
	check := fakePublicAPICheck{Name: name, HTTPStatus: resp.StatusCode}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		check.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return check
	}
	var envelope fakePolicyEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		check.Error = err.Error()
		return check
	}
	if !envelope.OK {
		check.Error = envelope.Error.Message
		return check
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		check.Error = err.Error()
		return check
	}
	check.OK = true
	return check
}

func requestFakeNodeExpectedError(ctx context.Context, client *http.Client, baseURL, method, path, name string, wantStatus int, wantCode string, headers map[string]string) fakePublicAPICheck {
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, nil)
	if err != nil {
		return fakePublicAPICheck{Name: name, Error: err.Error()}
	}
	req.Header.Set("X-Request-ID", "req_contract_fake_node")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fakePublicAPICheck{Name: name, Error: err.Error()}
	}
	defer resp.Body.Close()
	check := fakePublicAPICheck{Name: name, HTTPStatus: resp.StatusCode}
	var envelope fakePolicyEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		check.Error = err.Error()
		return check
	}
	if resp.StatusCode != wantStatus {
		check.Error = fmt.Sprintf("HTTP %d, want %d", resp.StatusCode, wantStatus)
		return check
	}
	if envelope.OK || envelope.Error.Code != wantCode {
		check.Error = fmt.Sprintf("error code = %q, want %q", envelope.Error.Code, wantCode)
		return check
	}
	check.OK = true
	return check
}

func appendFakePolicyChecks(ctx context.Context, checks *[]fakePublicAPICheck) {
	allowServer := httptest.NewServer(testkit.NewFakePolicyHandler(testkit.FakePolicyConfig{
		ValidCredential: "token_fake_policy",
		SubjectID:       "sub_fake_policy",
		Scopes:          []string{"component", "worker"},
		Decision:        contracts.PolicyDecision{Allowed: true, Reason: "fake_allow"},
		Secrets:         map[string]string{"secret_fake": "super-secret"},
	}))
	defer allowServer.Close()

	*checks = append(*checks,
		checkFakePolicyAuth(ctx, allowServer.Client(), allowServer.URL, "fake.policy.auth.allow", "Bearer token_fake_policy", true),
		checkFakePolicyAuth(ctx, allowServer.Client(), allowServer.URL, "fake.policy.auth.failure", "Bearer wrong-token", false),
		checkFakePolicyDecision(ctx, allowServer.Client(), allowServer.URL, "fake.policy.check.allow", true),
		checkFakePolicySecret(ctx, allowServer.Client(), allowServer.URL),
		checkFakePolicyRedact(ctx, allowServer.Client(), allowServer.URL),
	)

	denyServer := httptest.NewServer(testkit.NewFakePolicyHandler(testkit.FakePolicyConfig{
		Decision: contracts.PolicyDecision{Allowed: false, Reason: "fake_deny"},
	}))
	defer denyServer.Close()
	*checks = append(*checks, checkFakePolicyDecision(ctx, denyServer.Client(), denyServer.URL, "fake.policy.check.deny", false))
}

func checkFakePolicyAuth(ctx context.Context, client *http.Client, baseURL, name, credential string, wantValid bool) fakePublicAPICheck {
	var verification contracts.CredentialVerification
	check := postFakePolicyJSON(ctx, client, baseURL, "/v1/auth/verify", name, contracts.VerifyCredentialRequest{Credential: credential}, &verification)
	if !check.OK {
		return check
	}
	if verification.Valid != wantValid {
		check.OK = false
		check.Error = fmt.Sprintf("valid = %v, want %v", verification.Valid, wantValid)
		return check
	}
	if wantValid && (verification.SubjectID == nil || *verification.SubjectID == "") {
		check.OK = false
		check.Error = "valid verification missing subject_id"
	}
	return check
}

func checkFakePolicyDecision(ctx context.Context, client *http.Client, baseURL, name string, wantAllowed bool) fakePublicAPICheck {
	var decision contracts.PolicyDecision
	check := postFakePolicyJSON(ctx, client, baseURL, "/v1/policy/check", name, contracts.PolicyCheckRequest{
		SubjectID: "sub_fake_policy",
		Action:    "tool.invoke",
		Resource:  "cap_fake",
	}, &decision)
	if !check.OK {
		return check
	}
	if decision.Allowed != wantAllowed {
		check.OK = false
		check.Error = fmt.Sprintf("allowed = %v, want %v", decision.Allowed, wantAllowed)
	}
	return check
}

func checkFakePolicySecret(ctx context.Context, client *http.Client, baseURL string) fakePublicAPICheck {
	var secret contracts.ResolvedSecret
	check := postFakePolicyJSON(ctx, client, baseURL, "/v1/secrets/resolve", "fake.policy.secret.resolve", contracts.ResolveSecretRequest{
		SecretRef: "secret_fake",
		SubjectID: "sub_fake_policy",
	}, &secret)
	if !check.OK {
		return check
	}
	if secret.Value != "super-secret" {
		check.OK = false
		check.Error = "secret value mismatch"
	}
	return check
}

func checkFakePolicyRedact(ctx context.Context, client *http.Client, baseURL string) fakePublicAPICheck {
	var redacted contracts.RedactResponse
	check := postFakePolicyJSON(ctx, client, baseURL, "/v1/redact", "fake.policy.redact", contracts.RedactRequest{Text: "token is super-secret"}, &redacted)
	if !check.OK {
		return check
	}
	if redacted.Text != "token is [REDACTED]" {
		check.OK = false
		check.Error = "redacted text mismatch"
	}
	return check
}

func postFakePolicyJSON(ctx context.Context, client *http.Client, baseURL, path, name string, request any, out any) fakePublicAPICheck {
	raw, err := json.Marshal(request)
	if err != nil {
		return fakePublicAPICheck{Name: name, Error: err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return fakePublicAPICheck{Name: name, Error: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "req_contract_fake_policy")
	resp, err := client.Do(req)
	if err != nil {
		return fakePublicAPICheck{Name: name, Error: err.Error()}
	}
	defer resp.Body.Close()
	check := fakePublicAPICheck{Name: name, HTTPStatus: resp.StatusCode}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		check.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return check
	}
	var envelope fakePolicyEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		check.Error = err.Error()
		return check
	}
	if !envelope.OK {
		check.Error = envelope.Error.Message
		return check
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		check.Error = err.Error()
		return check
	}
	check.OK = true
	return check
}

func appendFakeProviderChecks(checks *[]fakePublicAPICheck, prefix string, report testkit.ProviderCheckReport) {
	for _, check := range report.Checks {
		*checks = append(*checks, fakePublicAPICheck{
			Name:       prefix + check.Name,
			OK:         check.OK,
			HTTPStatus: check.HTTPStatus,
			Error:      check.Error,
		})
	}
}

func runOpenAPICheck(pathsRaw string, stdout, stderr io.Writer) int {
	paths := splitCSV(pathsRaw)
	if len(paths) == 0 {
		fmt.Fprintln(stderr, "openapi requires at least one file path")
		return 2
	}
	report := openapicheck.ValidateFiles(paths)
	fmt.Fprintf(stdout, "openapi=checked files=%d operations=%d schemas=%d refs=%d\n", len(report.Files), report.Operations, report.Schemas, report.References)
	for _, fileReport := range report.Files {
		fmt.Fprintf(stdout, "file=%s operations=%d schemas=%d refs=%d\n", fileReport.Path, fileReport.Operations, fileReport.Schemas, fileReport.References)
	}
	if report.Passed() {
		fmt.Fprintln(stdout, "openapi=pass")
		return 0
	}
	for _, finding := range report.Findings {
		location := finding.Location
		if location == "" {
			location = "/"
		}
		fmt.Fprintf(stderr, "%s:%s: %s: %s\n", finding.File, location, finding.Code, finding.Message)
	}
	return 1
}

func runComponentSmoke(componentURL, componentKind, credential string, timeout time.Duration, stdout, stderr io.Writer, httpClient *http.Client) int {
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	report := testkit.CheckComponent(ctx, httpClient, testkit.ComponentCheckOptions{
		BaseURL:    componentURL,
		Kind:       componentKind,
		Credential: authorizationHeader(credential),
		RequestID:  "req_contract_component",
	})
	fmt.Fprintf(stdout, "component=%s kind=%s checks=%d\n", componentURL, componentKind, len(report.Checks))
	for _, check := range report.Checks {
		status := "fail"
		if check.OK {
			status = "pass"
		}
		if check.HTTPStatus != 0 {
			fmt.Fprintf(stdout, "check=%s status=%s http_status=%d\n", check.Name, status, check.HTTPStatus)
			continue
		}
		fmt.Fprintf(stdout, "check=%s status=%s\n", check.Name, status)
	}
	if report.Passed() {
		fmt.Fprintln(stdout, "contract-smoke=pass")
		return 0
	}
	for _, check := range report.Checks {
		if !check.OK {
			fmt.Fprintf(stderr, "%s: %s\n", check.Name, check.Error)
		}
	}
	return 1
}

func runProviderSmoke(providerURL, credential, capabilityID, inputRaw string, timeout time.Duration, stdout, stderr io.Writer, httpClient *http.Client) int {
	input, err := parseInput(inputRaw)
	if err != nil {
		fmt.Fprintf(stderr, "input: %v\n", err)
		return 2
	}
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	report := testkit.CheckProvider(ctx, httpClient, testkit.ProviderCheckOptions{
		BaseURL:      providerURL,
		CapabilityID: capabilityID,
		Input:        input,
		Credential:   authorizationHeader(credential),
		RequestID:    "req_contract_provider",
	})
	fmt.Fprintf(stdout, "provider=%s checks=%d\n", providerURL, len(report.Checks))
	for _, check := range report.Checks {
		status := "fail"
		if check.OK {
			status = "pass"
		}
		if check.HTTPStatus != 0 {
			fmt.Fprintf(stdout, "check=%s status=%s http_status=%d\n", check.Name, status, check.HTTPStatus)
			continue
		}
		fmt.Fprintf(stdout, "check=%s status=%s\n", check.Name, status)
	}
	if report.Passed() {
		fmt.Fprintln(stdout, "contract-smoke=pass")
		return 0
	}
	for _, check := range report.Checks {
		if !check.OK {
			fmt.Fprintf(stderr, "%s: %s\n", check.Name, check.Error)
		}
	}
	return 1
}

func authorizationHeader(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if strings.HasPrefix(token, "Bearer ") {
		return token
	}
	return "Bearer " + token
}

func parseInput(raw string) (map[string]any, error) {
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, err
	}
	input, ok := decoded.(map[string]any)
	if !ok || input == nil {
		return nil, fmt.Errorf("must be a JSON object")
	}
	return input, nil
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}
