package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strings"

	"wendy/internal/components/catalog"
	"wendy/internal/components/gateway"
	"wendy/internal/contracts"
)

func main() {
	addr := flag.String("addr", "localhost:18086", "listen address")
	catalogURL := flag.String("catalog-url", os.Getenv("WENDY_CATALOG_URL"), "catalog service base URL")
	policyURL := flag.String("policy-url", os.Getenv("WENDY_POLICY_URL"), "policy service base URL")
	jobsURL := flag.String("jobs-url", os.Getenv("WENDY_JOBS_URL"), "jobs service base URL")
	leasesURL := flag.String("leases-url", os.Getenv("WENDY_LEASES_URL"), "optional lease service base URL for agent-visible resource queue status")
	artifactsURL := flag.String("artifacts-url", os.Getenv("WENDY_ARTIFACTS_URL"), "artifact service base URL")
	manifestPath := flag.String("manifest", os.Getenv("WENDY_MANIFEST"), "optional provider manifest file or directory used as a static catalog when catalog-url is empty")
	gatewayCredential := flag.String("gateway-credential", componentCredentialDefault("WENDY_GATEWAY_CREDENTIAL"), "component credential for downstream calls; defaults to WENDY_GATEWAY_CREDENTIAL or WENDY_COMPONENT_TOKEN")
	idempotencyStateFile := flag.String("idempotency-state-file", "", "optional JSON state file for public invocation idempotency")
	flag.Parse()

	manifests := loadStaticManifests(*manifestPath)
	if *catalogURL == "" && len(manifests) == 0 {
		requireURL("catalog-url", *catalogURL)
	}
	requireURL("policy-url", *policyURL)
	requireURL("jobs-url", *jobsURL)
	requireURL("artifacts-url", *artifactsURL)

	handler, err := gateway.NewPersistentHandler(gateway.Config{
		CatalogURL:        *catalogURL,
		PolicyURL:         *policyURL,
		JobsURL:           *jobsURL,
		LeasesURL:         *leasesURL,
		ArtifactsURL:      *artifactsURL,
		GatewayCredential: authorizationHeader(*gatewayCredential),
		StaticManifests:   manifests,
	}, *idempotencyStateFile)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("serving gateway addr=%s", *addr)
	if err := http.ListenAndServe(*addr, handler); err != nil {
		log.Fatal(err)
	}
}

func loadStaticManifests(path string) []contracts.ProviderManifest {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	manifests, err := catalog.LoadManifests(path)
	if err != nil {
		log.Fatalf("load gateway manifests: %v", err)
	}
	return manifests
}

func componentCredentialDefault(primaryEnv string) string {
	if value := os.Getenv(primaryEnv); value != "" {
		return value
	}
	return os.Getenv("WENDY_COMPONENT_TOKEN")
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

func requireURL(name, value string) {
	if value == "" {
		log.Fatalf("%s is required; set -%s or WENDY_%s_URL", name, name, envServiceName(name))
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
