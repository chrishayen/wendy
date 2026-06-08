package main

import (
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"

	"pacp/internal/components/leases"
	"pacp/internal/transportauth"
)

func main() {
	addr := flag.String("addr", "localhost:18083", "listen address")
	stateFile := flag.String("state-file", "", "optional JSON state file for durable lease storage")
	resourcesPath := flag.String("resources", "", "optional lease resource registration JSON file")
	componentToken := flag.String("component-token", os.Getenv("PACP_COMPONENT_TOKEN"), "optional bearer token required for component API calls")
	policyURL := flag.String("policy-url", os.Getenv("PACP_POLICY_URL"), "optional policy service base URL for route-aware lease API auth")
	policyCredential := flag.String("policy-credential", componentCredentialDefault("PACP_LEASES_POLICY_CREDENTIAL"), "optional credential used when calling policy auth verify; defaults to PACP_LEASES_POLICY_CREDENTIAL or PACP_COMPONENT_TOKEN")
	flag.Parse()

	store := leases.NewStore()
	if *stateFile != "" {
		persistent, err := leases.NewPersistentStore(*stateFile)
		if err != nil {
			log.Fatal(err)
		}
		store = persistent
	}
	resourcesLoaded := 0
	if *resourcesPath != "" {
		resources, err := leases.LoadResourceRegistrations(*resourcesPath)
		if err != nil {
			log.Fatalf("load resources: %v", err)
		}
		for _, resource := range resources {
			if _, err := store.RegisterResource(resource); err != nil {
				if *stateFile != "" && errors.Is(err, leases.ErrResourceConflict) {
					log.Printf("resource %s already present in lease state; skipping", resource.ResourceID)
					continue
				}
				log.Fatalf("register resource %s: %v", resource.ResourceID, err)
			}
			resourcesLoaded++
		}
	}
	handler := leases.NewHandler(store)
	authMode := "open"
	if strings.TrimSpace(*policyURL) != "" {
		handler = transportauth.RequireVerifiedScopes(handler, transportauth.ScopeConfig{
			PolicyURL:        *policyURL,
			PolicyCredential: authorizationHeader(*policyCredential),
			Rules:            leaseScopeRules(),
		})
		authMode = "policy"
	} else if *componentToken != "" {
		handler = transportauth.RequireBearer(handler, *componentToken)
		authMode = "component-token"
	}
	log.Printf("serving leases addr=%s state_file=%s resources_loaded=%d auth_mode=%s", *addr, *stateFile, resourcesLoaded, authMode)
	if err := http.ListenAndServe(*addr, handler); err != nil {
		log.Fatal(err)
	}
}

func leaseScopeRules() []transportauth.ScopeRule {
	componentMessage := "lease component operation requires a valid component credential"
	workerMessage := "lease worker operation requires a valid runner credential"
	mixedMessage := "lease operation requires a valid component or runner credential"
	forbiddenMessage := "caller is not authorized for this lease operation"
	return []transportauth.ScopeRule{
		{Method: http.MethodGet, Path: "/v1/resources", Scopes: []string{"component", "worker"}, UnauthorizedMessage: mixedMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/resources", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/resources/{resource_id}", Scopes: []string{"component", "worker"}, UnauthorizedMessage: mixedMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/resources/{resource_id}/inspection", Scopes: []string{"component", "worker"}, UnauthorizedMessage: mixedMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/lease-requests", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/lease-requests/{request_id}", Scopes: []string{"component", "worker"}, UnauthorizedMessage: mixedMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/lease-requests/{request_id}/cancel", Scopes: []string{"component", "worker"}, UnauthorizedMessage: mixedMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/leases/{lease_id}", Scopes: []string{"component", "worker"}, UnauthorizedMessage: mixedMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/leases/{lease_id}/heartbeat", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/leases/{lease_id}/release", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
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
