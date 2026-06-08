package main

import (
	"flag"
	"log"
	"net/http"
	"os"

	"pacp/internal/components/artifacts"
	"pacp/internal/transportauth"
)

func main() {
	addr := flag.String("addr", "localhost:18084", "listen address")
	root := flag.String("root", "/tmp/pacp-artifacts", "artifact storage root")
	stateFile := flag.String("state-file", "", "optional JSON state file for durable artifact metadata")
	componentToken := flag.String("component-token", os.Getenv("PACP_COMPONENT_TOKEN"), "optional bearer token required for component API calls")
	flag.Parse()

	store, err := artifacts.NewStore(*root)
	if *stateFile != "" {
		store, err = artifacts.NewPersistentStore(*root, *stateFile)
	}
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("serving artifacts addr=%s root=%s state_file=%s", *addr, *root, *stateFile)
	if err := http.ListenAndServe(*addr, transportauth.RequireBearer(artifacts.NewHandler(store), *componentToken)); err != nil {
		log.Fatal(err)
	}
}
