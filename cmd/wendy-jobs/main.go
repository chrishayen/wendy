package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strings"

	"wendy/internal/components/jobs"
	"wendy/internal/routeauth"
	"wendy/internal/transportauth"
)

func main() {
	addr := flag.String("addr", "localhost:18082", "HTTP listen address")
	stateFile := flag.String("state-file", "", "optional JSON state file for durable job storage")
	componentToken := flag.String("component-token", os.Getenv("WENDY_COMPONENT_TOKEN"), "optional bearer token required for component API calls")
	policyURL := flag.String("policy-url", os.Getenv("WENDY_POLICY_URL"), "optional policy service base URL for route-aware job API auth")
	policyCredential := flag.String("policy-credential", componentCredentialDefault("WENDY_JOBS_POLICY_CREDENTIAL"), "optional credential used when calling policy auth verify; defaults to WENDY_JOBS_POLICY_CREDENTIAL or WENDY_COMPONENT_TOKEN")
	flag.Parse()

	store := jobs.NewStore()
	if *stateFile != "" {
		persistent, err := jobs.NewPersistentStore(*stateFile)
		if err != nil {
			log.Fatal(err)
		}
		store = persistent
	}
	handler := jobs.NewHandler(store)
	authMode := "open"
	if strings.TrimSpace(*policyURL) != "" {
		handler = transportauth.RequireVerifiedScopes(handler, transportauth.ScopeConfig{
			PolicyURL:        *policyURL,
			PolicyCredential: authorizationHeader(*policyCredential),
			Rules:            routeauth.JobScopeRules(),
		})
		authMode = "policy"
	} else if *componentToken != "" {
		handler = transportauth.RequireBearer(handler, *componentToken)
		authMode = "component-token"
	}
	log.Printf("serving jobs addr=%s state_file=%s auth_mode=%s", *addr, *stateFile, authMode)
	log.Fatal(http.ListenAndServe(*addr, handler))
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
