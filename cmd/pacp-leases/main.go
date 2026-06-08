package main

import (
	"flag"
	"log"
	"net/http"

	"pacp/internal/components/leases"
)

func main() {
	addr := flag.String("addr", "localhost:18083", "listen address")
	stateFile := flag.String("state-file", "", "optional JSON state file for durable lease storage")
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
	if err := http.ListenAndServe(*addr, leases.NewHandler(store)); err != nil {
		log.Fatal(err)
	}
}
