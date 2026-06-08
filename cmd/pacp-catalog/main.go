package main

import (
	"errors"
	"flag"
	"log"
	"net/http"

	"pacp/internal/components/catalog"
)

func main() {
	addr := flag.String("addr", "localhost:18081", "HTTP listen address")
	manifestPath := flag.String("manifest", "", "provider manifest file or directory to load at startup")
	stateFile := flag.String("state-file", "", "optional JSON state file for durable catalog registrations")
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

	log.Printf("serving catalog addr=%s manifests_loaded=%d state_file=%s", *addr, loaded, *stateFile)
	log.Fatal(http.ListenAndServe(*addr, catalog.NewHandler(store)))
}
