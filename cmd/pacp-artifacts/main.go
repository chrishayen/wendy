package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strings"

	"pacp/internal/components/artifacts"
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
			Rules:            artifactScopeRules(),
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

func artifactScopeRules() []transportauth.ScopeRule {
	componentMessage := "artifact component operation requires a valid component credential"
	workerMessage := "artifact worker operation requires a valid runner credential"
	forbiddenMessage := "caller is not authorized for this artifact operation"
	return []transportauth.ScopeRule{
		{Method: http.MethodPost, Path: "/v1/artifact-uploads", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/artifact-uploads/{upload_id}", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPut, Path: "/v1/artifact-uploads/{upload_id}/content", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/artifact-uploads/{upload_id}/complete", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/artifacts", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/artifacts/register-local", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/artifacts/{artifact_id}", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/artifacts/{artifact_id}/policy-context", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/artifacts/{artifact_id}/content", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
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
