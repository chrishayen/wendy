package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"pacp/internal/runner"
)

func main() {
	workerID := flag.String("worker-id", "runner_local", "worker id used for job claims")
	jobsURL := flag.String("jobs-url", os.Getenv("PACP_JOBS_URL"), "jobs service base URL")
	leasesURL := flag.String("leases-url", os.Getenv("PACP_LEASES_URL"), "lease service base URL")
	artifactsURL := flag.String("artifacts-url", os.Getenv("PACP_ARTIFACTS_URL"), "artifact service base URL")
	nodeURL := flag.String("node-url", os.Getenv("PACP_NODE_URL"), "optional node service base URL")
	nodeURLsRaw := flag.String("node-urls", os.Getenv("PACP_NODE_URLS"), "optional comma-separated node_id=URL mappings for node-managed services")
	credential := flag.String("credential", componentCredentialDefault("PACP_RUNNER_CREDENTIAL"), "component credential for downstream calls; defaults to PACP_RUNNER_CREDENTIAL or PACP_COMPONENT_TOKEN")
	nodeStartTimeout := flag.Duration("node-start-timeout", 30*time.Second, "maximum time to wait for node-managed service startup")
	nodeStartPoll := flag.Duration("node-start-poll", 500*time.Millisecond, "poll interval while waiting for node-managed service startup")
	once := flag.Bool("once", false, "process at most one queued job and exit")
	poll := flag.Duration("poll", time.Second, "poll interval")
	flag.Parse()
	requireURL("jobs-url", *jobsURL)
	requireURL("leases-url", *leasesURL)
	requireURL("artifacts-url", *artifactsURL)
	nodeURLs, err := parseNodeURLMap(*nodeURLsRaw)
	if err != nil {
		log.Fatal(err)
	}

	r := runner.New(runner.Config{
		WorkerID:            *workerID,
		JobsURL:             *jobsURL,
		LeasesURL:           *leasesURL,
		ArtifactsURL:        *artifactsURL,
		NodeURL:             *nodeURL,
		NodeURLs:            nodeURLs,
		NodeStartTimeout:    *nodeStartTimeout,
		NodePollInterval:    *nodeStartPoll,
		ComponentCredential: authorizationHeader(*credential),
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

func componentCredentialDefault(primaryEnv string) string {
	if value := os.Getenv(primaryEnv); value != "" {
		return value
	}
	return os.Getenv("PACP_COMPONENT_TOKEN")
}

func authorizationHeader(token string) string {
	if token == "" {
		return ""
	}
	if strings.HasPrefix(token, "Bearer ") {
		return token
	}
	return "Bearer " + token
}

func parseNodeURLMap(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	out := map[string]string{}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("node URL mapping %q must be node_id=URL", entry)
		}
		nodeID := strings.TrimSpace(parts[0])
		nodeURL := strings.TrimRight(strings.TrimSpace(parts[1]), "/")
		if nodeID == "" || nodeURL == "" {
			return nil, fmt.Errorf("node URL mapping %q must include node_id and URL", entry)
		}
		out[nodeID] = nodeURL
	}
	return out, nil
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
