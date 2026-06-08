package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"pacp/internal/runner"
)

func main() {
	workerID := flag.String("worker-id", "runner_local", "worker id used for job claims")
	jobsURL := flag.String("jobs-url", os.Getenv("PACP_JOBS_URL"), "jobs service base URL")
	leasesURL := flag.String("leases-url", os.Getenv("PACP_LEASES_URL"), "lease service base URL")
	artifactsURL := flag.String("artifacts-url", os.Getenv("PACP_ARTIFACTS_URL"), "artifact service base URL")
	nodeURL := flag.String("node-url", os.Getenv("PACP_NODE_URL"), "optional node service base URL")
	credential := flag.String("credential", "", "component credential for downstream calls")
	nodeStartTimeout := flag.Duration("node-start-timeout", 30*time.Second, "maximum time to wait for node-managed service startup")
	nodeStartPoll := flag.Duration("node-start-poll", 500*time.Millisecond, "poll interval while waiting for node-managed service startup")
	once := flag.Bool("once", false, "process at most one queued job and exit")
	poll := flag.Duration("poll", time.Second, "poll interval")
	flag.Parse()
	requireURL("jobs-url", *jobsURL)
	requireURL("leases-url", *leasesURL)
	requireURL("artifacts-url", *artifactsURL)

	r := runner.New(runner.Config{
		WorkerID:            *workerID,
		JobsURL:             *jobsURL,
		LeasesURL:           *leasesURL,
		ArtifactsURL:        *artifactsURL,
		NodeURL:             *nodeURL,
		NodeStartTimeout:    *nodeStartTimeout,
		NodePollInterval:    *nodeStartPoll,
		ComponentCredential: *credential,
	})
	for {
		jobID, ok, err := r.RunOnce(context.Background())
		if err != nil {
			log.Printf("runner error job_id=%s err=%v", jobID, err)
			if *once {
				return
			}
		} else if ok {
			log.Printf("processed job_id=%s", jobID)
			if *once {
				return
			}
		} else if *once {
			log.Print("no queued job")
			return
		}
		time.Sleep(*poll)
	}
}

func requireURL(name, value string) {
	if value == "" {
		log.Fatalf("%s is required; set -%s or PACP_%s_URL", name, name, envServiceName(name))
	}
}

func envServiceName(flagName string) string {
	switch flagName {
	case "jobs-url":
		return "JOBS"
	case "leases-url":
		return "LEASES"
	case "artifacts-url":
		return "ARTIFACTS"
	default:
		return "SERVICE"
	}
}
