package main

import (
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"

	"wendy/internal/components/catalog"
	"wendy/internal/routeauth"
	"wendy/internal/transportauth"
)

func main() {
	addr := flag.String("addr", "localhost:18081", "HTTP listen address")
	manifestPath := flag.String("manifest", "", "provider manifest file or directory to load at startup")
	stateFile := flag.String("state-file", "", "optional JSON state file for durable catalog registrations")
	componentToken := flag.String("component-token", os.Getenv("WENDY_COMPONENT_TOKEN"), "optional bearer token required for component API calls")
	policyURL := flag.String("policy-url", os.Getenv("WENDY_POLICY_URL"), "optional policy service base URL for route-aware catalog API auth")
	policyCredential := flag.String("policy-credential", componentCredentialDefault("WENDY_CATALOG_POLICY_CREDENTIAL"), "optional credential used when calling policy auth verify; defaults to WENDY_CATALOG_POLICY_CREDENTIAL or WENDY_COMPONENT_TOKEN")
	flag.Parse()

	store := catalog.NewStore()
	if *stateFile != "" {
		persistent, err := catalog.NewPersistentStore(*stateFile)
		if err != nil {
			log.Fatal(err)
		}
		store = persistent
	}
	loaded := 0
	if *manifestPath != "" {
		manifests, err := catalog.LoadManifests(*manifestPath)
		if err != nil {
			log.Fatalf("load manifests: %v", err)
		}
		for _, manifest := range manifests {
			if _, err := store.RegisterManifest(manifest); err != nil {
				if *stateFile != "" && errors.Is(err, catalog.ErrDuplicateService) {
					log.Printf("manifest %s already present in catalog state; skipping", manifest.Service.ID)
					continue
				}
				log.Fatalf("register manifest %s: %v", manifest.Service.ID, err)
			}
			loaded++
		}
	}

	handler := catalog.NewHandler(store)
	authMode := "open"
	if strings.TrimSpace(*policyURL) != "" {
		handler = transportauth.RequireVerifiedScopes(handler, transportauth.ScopeConfig{
			PolicyURL:        *policyURL,
			PolicyCredential: authorizationHeader(*policyCredential),
			Rules:            routeauth.CatalogScopeRules(),
		})
		authMode = "policy"
	} else if *componentToken != "" {
		handler = transportauth.RequireBearer(handler, *componentToken)
		authMode = "component-token"
	}
	log.Printf("serving catalog addr=%s manifests_loaded=%d state_file=%s auth_mode=%s", *addr, loaded, *stateFile, authMode)
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
