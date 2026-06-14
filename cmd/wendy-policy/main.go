package main

import (
	"flag"
	"log"
	"net/http"
	"os"

	"wendy/internal/components/policy"
	"wendy/internal/transportauth"
)

func main() {
	addr := flag.String("addr", "localhost:18085", "listen address")
	stateFile := flag.String("state-file", "", "optional JSON state file for durable policy storage; contains API tokens and secret values")
	seedPath := flag.String("seed", "", "optional policy seed JSON file for API keys, policy rules, and secrets")
	componentToken := flag.String("component-token", os.Getenv("WENDY_COMPONENT_TOKEN"), "optional bearer token required for component API calls")
	flag.Parse()

	store := policy.NewStore()
	if *stateFile != "" {
		persistent, err := policy.NewPersistentStore(*stateFile)
		if err != nil {
			log.Fatal(err)
		}
		store = persistent
	}
	seedResult := policy.SeedResult{}
	if *seedPath != "" {
		seed, err := policy.LoadSeedFile(*seedPath)
		if err != nil {
			log.Fatalf("load policy seed: %v", err)
		}
		seedResult, err = store.ApplySeed(seed)
		if err != nil {
			log.Fatalf("apply policy seed: %v", err)
		}
	}
	log.Printf("serving policy addr=%s state_file=%s seed=%s api_keys_created=%d api_keys_skipped=%d rules_created=%d rules_skipped=%d secrets_created=%d secrets_skipped=%d", *addr, *stateFile, *seedPath, seedResult.APIKeysCreated, seedResult.APIKeysSkipped, seedResult.RulesCreated, seedResult.RulesSkipped, seedResult.SecretsCreated, seedResult.SecretsSkipped)
	if err := http.ListenAndServe(*addr, transportauth.RequireBearer(policy.NewHandler(store), *componentToken)); err != nil {
		log.Fatal(err)
	}
}
