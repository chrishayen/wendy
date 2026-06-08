package main

import (
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
	log.Printf("serving leases addr=%s state_file=%s", *addr, *stateFile)
	if err := http.ListenAndServe(*addr, transportauth.RequireBearer(leases.NewHandler(store), *componentToken)); err != nil {
		log.Fatal(err)
	}
}
