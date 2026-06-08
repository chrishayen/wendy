package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

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
