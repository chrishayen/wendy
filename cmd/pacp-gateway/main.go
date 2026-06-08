package main

import (
	"flag"
	"log"
	"net/http"
	"os"

	"pacp/internal/components/gateway"
)

func main() {
	addr := flag.String("addr", "localhost:18086", "listen address")
	catalogURL := flag.String("catalog-url", os.Getenv("PACP_CATALOG_URL"), "catalog service base URL")
	policyURL := flag.String("policy-url", os.Getenv("PACP_POLICY_URL"), "policy service base URL")
	jobsURL := flag.String("jobs-url", os.Getenv("PACP_JOBS_URL"), "jobs service base URL")
	artifactsURL := flag.String("artifacts-url", os.Getenv("PACP_ARTIFACTS_URL"), "artifact service base URL")
	gatewayCredential := flag.String("gateway-credential", "", "component credential for downstream calls")
	idempotencyStateFile := flag.String("idempotency-state-file", "", "optional JSON state file for public invocation idempotency")
	flag.Parse()
	requireURL("catalog-url", *catalogURL)
	requireURL("policy-url", *policyURL)
	requireURL("jobs-url", *jobsURL)
	requireURL("artifacts-url", *artifactsURL)

	handler, err := gateway.NewPersistentHandler(gateway.Config{
		CatalogURL:        *catalogURL,
		PolicyURL:         *policyURL,
		JobsURL:           *jobsURL,
		ArtifactsURL:      *artifactsURL,
		GatewayCredential: *gatewayCredential,
	}, *idempotencyStateFile)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("serving gateway addr=%s", *addr)
	if err := http.ListenAndServe(*addr, handler); err != nil {
		log.Fatal(err)
	}
}

func requireURL(name, value string) {
	if value == "" {
		log.Fatalf("%s is required; set -%s or PACP_%s_URL", name, name, envServiceName(name))
	}
}

func envServiceName(flagName string) string {
	switch flagName {
	case "catalog-url":
		return "CATALOG"
	case "policy-url":
		return "POLICY"
	case "jobs-url":
		return "JOBS"
	case "artifacts-url":
		return "ARTIFACTS"
	default:
		return "SERVICE"
	}
}
