package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

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
	providerURL := flags.String("provider-url", "", "optional live provider base URL to check instead of fixture simulation")
	capabilityID := flags.String("capability-id", "", "optional provider capability id to invoke")
	inputRaw := flags.String("input", "{}", "JSON object input for provider invocation")
	timeout := flags.Duration("timeout", 5*time.Second, "smoke check timeout")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *providerURL != "" {
		return runProviderSmoke(*providerURL, *capabilityID, *inputRaw, *timeout, stdout, stderr, httpClient)
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

func runProviderSmoke(providerURL, capabilityID, inputRaw string, timeout time.Duration, stdout, stderr io.Writer, httpClient *http.Client) int {
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
