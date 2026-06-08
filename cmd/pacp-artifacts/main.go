package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strings"

	"pacp/internal/components/artifacts"
	"pacp/internal/routeauth"
	"pacp/internal/transportauth"
)

func main() {
	addr := flag.String("addr", "localhost:18084", "listen address")
	root := flag.String("root", "/tmp/pacp-artifacts", "artifact storage root")
	stateFile := flag.String("state-file", "", "optional JSON state file for durable artifact metadata")
	componentToken := flag.String("component-token", os.Getenv("PACP_COMPONENT_TOKEN"), "optional bearer token required for component API calls")
	policyURL := flag.String("policy-url", os.Getenv("PACP_POLICY_URL"), "optional policy service base URL for route-aware artifact API auth")
	policyCredential := flag.String("policy-credential", componentCredentialDefault("PACP_ARTIFACTS_POLICY_CREDENTIAL"), "optional credential used when calling policy auth verify; defaults to PACP_ARTIFACTS_POLICY_CREDENTIAL or PACP_COMPONENT_TOKEN")
	flag.Parse()

	store, err := artifacts.NewStore(*root)
	if *stateFile != "" {
		store, err = artifacts.NewPersistentStore(*root, *stateFile)
	}
	if err != nil {
		log.Fatal(err)
	}
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
	log.Printf("serving artifacts addr=%s root=%s state_file=%s auth_mode=%s", *addr, *root, *stateFile, authMode)
	if err := http.ListenAndServe(*addr, handler); err != nil {
		log.Fatal(err)
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
