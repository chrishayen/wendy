package main

import (
	"flag"
	"log"
	"net/http"

	"pacp/internal/components/policy"
)

func main() {
	addr := flag.String("addr", "localhost:18085", "listen address")
	stateFile := flag.String("state-file", "", "optional JSON state file for durable policy storage; contains API tokens and secret values")
	flag.Parse()

	store := policy.NewStore()
	if *stateFile != "" {
		persistent, err := policy.NewPersistentStore(*stateFile)
		if err != nil {
			log.Fatal(err)
		}
		store = persistent
	}
	log.Printf("serving policy addr=%s state_file=%s", *addr, *stateFile)
	if err := http.ListenAndServe(*addr, policy.NewHandler(store)); err != nil {
		log.Fatal(err)
	}
}
