package main

import (
	"flag"
	"log"
	"net/http"

	"pacp/internal/components/catalog"
)

func main() {
	addr := flag.String("addr", "localhost:18081", "HTTP listen address")
	seed := flag.String("seed", "s003", "seed catalog data: s003 or empty")
	flag.Parse()

	store := catalog.NewStore()
	if *seed == "s003" {
		if _, err := store.RegisterManifest(catalog.S003Manifest()); err != nil {
			log.Fatalf("seed catalog: %v", err)
		}
	}

	log.Printf("serving catalog addr=%s seed=%s", *addr, *seed)
	log.Fatal(http.ListenAndServe(*addr, catalog.NewHandler(store)))
}
