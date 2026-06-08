package main

import (
	"flag"
	"log"
	"net/http"

	"pacp/internal/components/artifacts"
)

func main() {
	addr := flag.String("addr", "localhost:18084", "listen address")
	root := flag.String("root", "/tmp/pacp-artifacts", "artifact storage root")
	stateFile := flag.String("state-file", "", "optional JSON state file for durable artifact metadata")
	flag.Parse()

	store, err := artifacts.NewStore(*root)
	if *stateFile != "" {
		store, err = artifacts.NewPersistentStore(*root, *stateFile)
	}
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("serving artifacts addr=%s root=%s state_file=%s", *addr, *root, *stateFile)
	if err := http.ListenAndServe(*addr, artifacts.NewHandler(store)); err != nil {
		log.Fatal(err)
	}
}
