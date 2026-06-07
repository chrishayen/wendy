package main

import (
	"flag"
	"log"
	"net/http"

	"pacp/internal/components/catalog"
)

func main() {
	addr := flag.String("addr", "localhost:18081", "HTTP listen address")
	manifestPath := flag.String("manifest", "", "provider manifest file or directory to load at startup")
	flag.Parse()

	store := catalog.NewStore()
	loaded := 0
	if *manifestPath != "" {
		manifests, err := catalog.LoadManifests(*manifestPath)
		if err != nil {
			log.Fatalf("load manifests: %v", err)
		}
		for _, manifest := range manifests {
			if _, err := store.RegisterManifest(manifest); err != nil {
				log.Fatalf("register manifest %s: %v", manifest.Service.ID, err)
			}
		}
		loaded = len(manifests)
	}

	log.Printf("serving catalog addr=%s manifests=%d", *addr, loaded)
	log.Fatal(http.ListenAndServe(*addr, catalog.NewHandler(store)))
}
