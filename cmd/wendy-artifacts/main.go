package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"wendy/internal/components/artifacts"
	"wendy/internal/routeauth"
	"wendy/internal/transportauth"
)

func main() {
	addr := flag.String("addr", "localhost:18084", "listen address")
	root := flag.String("root", "/tmp/wendy-artifacts", "artifact storage root")
	stateFile := flag.String("state-file", "", "optional JSON state file for durable artifact metadata")
	artifactTTLRaw := flag.String("artifact-ttl", os.Getenv("WENDY_ARTIFACT_TTL"), "optional completed artifact retention TTL, such as 24h or 168h")
	componentToken := flag.String("component-token", os.Getenv("WENDY_COMPONENT_TOKEN"), "optional bearer token required for component API calls")
	policyURL := flag.String("policy-url", os.Getenv("WENDY_POLICY_URL"), "optional policy service base URL for route-aware artifact API auth")
	policyCredential := flag.String("policy-credential", componentCredentialDefault("WENDY_ARTIFACTS_POLICY_CREDENTIAL"), "optional credential used when calling policy auth verify; defaults to WENDY_ARTIFACTS_POLICY_CREDENTIAL or WENDY_COMPONENT_TOKEN")
	flag.Parse()

	store, err := artifacts.NewStore(*root)
	if *stateFile != "" {
		store, err = artifacts.NewPersistentStore(*root, *stateFile)
	}
	if err != nil {
		log.Fatal(err)
	}
	artifactTTL, err := optionalDuration(*artifactTTLRaw)
	if err != nil {
		log.Fatal(err)
	}
	store.SetArtifactTTL(artifactTTL)
	handler := artifacts.NewHandler(store)
	authMode := "open"
	if strings.TrimSpace(*policyURL) != "" {
		handler = transportauth.RequireVerifiedScopes(handler, transportauth.ScopeConfig{
			PolicyURL:        *policyURL,
			PolicyCredential: authorizationHeader(*policyCredential),
			Rules:            routeauth.ArtifactScopeRules(),
		})
		authMode = "policy"
	} else if *componentToken != "" {
		handler = transportauth.RequireBearer(handler, *componentToken)
		authMode = "component-token"
	}
	log.Printf("serving artifacts addr=%s root=%s state_file=%s artifact_ttl=%s auth_mode=%s", *addr, *root, *stateFile, artifactTTL, authMode)
	if err := http.ListenAndServe(*addr, handler); err != nil {
		log.Fatal(err)
	}
}

func optionalDuration(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil {
		return 0, err
	}
	if duration < 0 {
		return 0, fmt.Errorf("artifact-ttl must be non-negative")
	}
	return duration, nil
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
