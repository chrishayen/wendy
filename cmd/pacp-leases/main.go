package main

import (
	"errors"
	"flag"
	"log"
	"net/http"
	"os"

	"pacp/internal/components/leases"
	"pacp/internal/transportauth"
)

func main() {
	addr := flag.String("addr", "localhost:18083", "listen address")
	stateFile := flag.String("state-file", "", "optional JSON state file for durable lease storage")
	resourcesPath := flag.String("resources", "", "optional lease resource registration JSON file")
	componentToken := flag.String("component-token", os.Getenv("PACP_COMPONENT_TOKEN"), "optional bearer token required for component API calls")
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
	log.Printf("serving leases addr=%s state_file=%s resources_loaded=%d", *addr, *stateFile, resourcesLoaded)
	if err := http.ListenAndServe(*addr, transportauth.RequireBearer(leases.NewHandler(store), *componentToken)); err != nil {
		log.Fatal(err)
	}
}
