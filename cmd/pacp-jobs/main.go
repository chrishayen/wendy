package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strings"

	"pacp/internal/components/jobs"
	"pacp/internal/transportauth"
)

func main() {
	addr := flag.String("addr", "localhost:18082", "HTTP listen address")
	stateFile := flag.String("state-file", "", "optional JSON state file for durable job storage")
	componentToken := flag.String("component-token", os.Getenv("PACP_COMPONENT_TOKEN"), "optional bearer token required for component API calls")
	policyURL := flag.String("policy-url", os.Getenv("PACP_POLICY_URL"), "optional policy service base URL for route-aware job API auth")
	policyCredential := flag.String("policy-credential", componentCredentialDefault("PACP_JOBS_POLICY_CREDENTIAL"), "optional credential used when calling policy auth verify; defaults to PACP_JOBS_POLICY_CREDENTIAL or PACP_COMPONENT_TOKEN")
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
			Rules:            jobScopeRules(),
		})
		authMode = "policy"
	} else if *componentToken != "" {
		handler = transportauth.RequireBearer(handler, *componentToken)
		authMode = "component-token"
	}
	log.Printf("serving jobs addr=%s state_file=%s auth_mode=%s", *addr, *stateFile, authMode)
	log.Fatal(http.ListenAndServe(*addr, handler))
}

func jobScopeRules() []transportauth.ScopeRule {
	componentMessage := "job component operation requires a valid component credential"
	workerMessage := "job worker operation requires a valid runner credential"
	forbiddenMessage := "caller is not authorized for this job operation"
	return []transportauth.ScopeRule{
		{Method: http.MethodGet, Path: "/v1/jobs", Scopes: []string{"component", "worker"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/jobs", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/jobs/{job_id}", Scopes: []string{"component", "worker"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/jobs/{job_id}/policy-context", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/jobs/{job_id}/agent-projection", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/jobs/{job_id}/claim", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/jobs/{job_id}/heartbeat", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/jobs/{job_id}/complete", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/jobs/{job_id}/fail", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/jobs/{job_id}/cancel", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/jobs/{job_id}/logs", Scopes: []string{"component", "worker"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/jobs/{job_id}/logs", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
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
