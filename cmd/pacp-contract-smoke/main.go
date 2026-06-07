package main

import (
	"flag"
	"fmt"
	"os"

	"pacp/internal/testkit"
)

func main() {
	root := flag.String("root", "testdata/contract-sim", "contract simulation root")
	manifest := flag.String("manifest", "fixtures/S003/manifest.json", "manifest path relative to root")
	flag.Parse()

	scenario, err := testkit.LoadScenario(*root, *manifest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load failed: %v\n", err)
		os.Exit(1)
	}
	report := testkit.ValidateScenario(scenario)
	fmt.Printf("scenario=%s status=%s packages=%d files=%d fixtures=%d\n",
		scenario.Manifest.ScenarioID, scenario.Manifest.Status, len(scenario.Packages), report.Files, report.Fixtures)
	if report.Passed() {
		fmt.Println("contract-smoke=pass")
		return
	}
	for _, finding := range report.Findings {
		if finding.Fixture == "" {
			fmt.Fprintf(os.Stderr, "%s: %s: %s\n", finding.File, finding.Code, finding.Message)
			continue
		}
		fmt.Fprintf(os.Stderr, "%s:%s: %s: %s\n", finding.File, finding.Fixture, finding.Code, finding.Message)
	}
	os.Exit(1)
}
