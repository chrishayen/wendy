package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"wendy/internal/observability"
	"wendy/internal/runner"
	"wendy/internal/transportauth"
)

func main() {
	workerID := flag.String("worker-id", "runner_local", "worker id used for job claims")
	catalogURL := flag.String("catalog-url", os.Getenv("WENDY_CATALOG_URL"), "optional catalog service base URL used to resolve routes for lean job execution plans")
	jobsURL := flag.String("jobs-url", os.Getenv("WENDY_JOBS_URL"), "jobs service base URL")
	leasesURL := flag.String("leases-url", os.Getenv("WENDY_LEASES_URL"), "lease service base URL")
	artifactsURL := flag.String("artifacts-url", os.Getenv("WENDY_ARTIFACTS_URL"), "artifact service base URL")
	policyURL := flag.String("policy-url", os.Getenv("WENDY_POLICY_URL"), "optional policy service base URL for provider.invoke checks")
	nodeURL := flag.String("node-url", os.Getenv("WENDY_NODE_URL"), "optional node service base URL")
	nodeURLsRaw := flag.String("node-urls", os.Getenv("WENDY_NODE_URLS"), "optional comma-separated node_id=URL mappings for node-managed services")
	nodeRegistryURL := flag.String("node-registry-url", nodeRegistryURLDefault(), "optional node registry service base URL used to resolve and trust-check node_id routes")
	nodeRegistryCredential := flag.String("node-registry-credential", componentCredentialDefault("WENDY_RUNNER_NODE_REGISTRY_CREDENTIAL"), "component credential for node registry service calls; defaults to WENDY_RUNNER_NODE_REGISTRY_CREDENTIAL or WENDY_COMPONENT_TOKEN")
	credential := flag.String("credential", componentCredentialDefault("WENDY_RUNNER_CREDENTIAL"), "component credential for downstream calls; defaults to WENDY_RUNNER_CREDENTIAL or WENDY_COMPONENT_TOKEN")
	policyCredential := flag.String("policy-credential", componentCredentialDefault("WENDY_RUNNER_POLICY_CREDENTIAL"), "component credential for policy service calls; defaults to WENDY_RUNNER_POLICY_CREDENTIAL or WENDY_COMPONENT_TOKEN")
	workerSubjectID := flag.String("worker-subject-id", os.Getenv("WENDY_RUNNER_SUBJECT_ID"), "optional worker subject id for policy checks; defaults to verifying the runner credential")
	actorSubjectID := flag.String("actor-subject-id", os.Getenv("WENDY_RUNNER_ACTOR_SUBJECT_ID"), "optional actor subject id for lease release audit; defaults to worker subject id")
	addr := flag.String("addr", "", "optional HTTP listen address for runner health and metrics")
	monitorToken := flag.String("monitor-token", os.Getenv("WENDY_RUNNER_MONITOR_TOKEN"), "optional bearer token required for runner health and metrics")
	nodeStartTimeout := flag.Duration("node-start-timeout", 30*time.Second, "maximum time to wait for node-managed service startup")
	nodeStartPoll := flag.Duration("node-start-poll", 500*time.Millisecond, "poll interval while waiting for node-managed service startup")
	leasePoll := flag.Duration("lease-poll", time.Second, "poll interval while waiting for pending resource leases")
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
		WorkerID:               *workerID,
		CatalogURL:             *catalogURL,
		JobsURL:                *jobsURL,
		LeasesURL:              *leasesURL,
		ArtifactsURL:           *artifactsURL,
		PolicyURL:              *policyURL,
		NodeURL:                *nodeURL,
		NodeURLs:               nodeURLs,
		NodeRegistryURL:        *nodeRegistryURL,
		NodeRegistryCredential: authorizationHeader(*nodeRegistryCredential),
		NodeStartTimeout:       *nodeStartTimeout,
		NodePollInterval:       *nodeStartPoll,
		LeasePollInterval:      *leasePoll,
		ComponentCredential:    authorizationHeader(*credential),
		PolicyCredential:       authorizationHeader(*policyCredential),
		WorkerSubjectID:        *workerSubjectID,
		ActorSubjectID:         *actorSubjectID,
	})
	logger := observability.NewStructuredLogger(os.Stderr, "runner", observability.WithRedactionValues(*credential, *policyCredential, *nodeRegistryCredential, *monitorToken))
	if strings.TrimSpace(*addr) != "" {
		go func() {
			ctx := observability.EnsureContextRequestID(context.Background(), "req_runner")
			logger.Info(ctx, "serving runner monitor", observability.Field("addr", *addr))
			if err := http.ListenAndServe(*addr, transportauth.RequireBearer(runner.NewHandler(r), *monitorToken)); err != nil {
				logger.Error(ctx, "runner monitor failed", err, observability.Field("addr", *addr))
				os.Exit(1)
			}
		}()
	}
	for {
		ctx := observability.EnsureContextRequestID(context.Background(), "req_runner")
		jobID, ok, err := r.RunOnce(ctx)
		if err != nil {
			logger.Error(ctx, "runner iteration failed", err, observability.Field("job_id", jobID))
			if *once {
				return
			}
		} else if ok {
			logger.Info(ctx, "processed job", observability.Field("job_id", jobID))
			if *once {
				return
			}
		} else if *once {
			logger.Info(ctx, "no queued job")
			return
		}
		time.Sleep(*poll)
	}
}

func componentCredentialDefault(primaryEnv string) string {
	if value := os.Getenv(primaryEnv); value != "" {
		return value
	}
	return os.Getenv("WENDY_COMPONENT_TOKEN")
}

func nodeRegistryURLDefault() string {
	return os.Getenv("WENDY_NODE_REGISTRY_URL")
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
		log.Fatalf("%s is required; set -%s or WENDY_%s_URL", name, name, envServiceName(name))
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
