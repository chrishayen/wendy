package distributedsmoke

import (
	"context"
	"testing"
	"time"
)

func TestRunDistributedSmokePassesPrimaryPlusNodeFlow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	report := Run(ctx)
	if !report.Passed() {
		t.Fatalf("distributed smoke failed: %#v", report.Checks)
	}
	if report.JobID == "" || report.ArtifactID == "" {
		t.Fatalf("report missing job or artifact id: %#v", report)
	}
	for _, name := range []string{
		"gateway.invoke",
		"runner.run_once",
		"jobs.succeeded",
		"artifacts.registered",
		"artifacts.metadata",
		"gateway.job_projection",
		"gateway.artifact_list",
		"gateway.artifact_content",
		"node.service_running",
		"node.start_metric",
		"leases.release_audit",
		"provider.invoked",
	} {
		if !hasDistributedCheck(report, name) {
			t.Fatalf("missing check %s in %#v", name, report.Checks)
		}
	}
}

func hasDistributedCheck(report DistributedSmokeReport, name string) bool {
	for _, check := range report.Checks {
		if check.Name == name && check.OK {
			return true
		}
	}
	return false
}
