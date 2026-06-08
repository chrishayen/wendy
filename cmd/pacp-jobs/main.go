package main

import (
	"flag"
	"log"
	"net/http"

	"pacp/internal/components/jobs"
)

func main() {
	addr := flag.String("addr", "localhost:18082", "HTTP listen address")
	stateFile := flag.String("state-file", "", "optional JSON state file for durable job storage")
	flag.Parse()

	store := jobs.NewStore()
	if *stateFile != "" {
		persistent, err := jobs.NewPersistentStore(*stateFile)
		if err != nil {
			log.Fatal(err)
		}
		store = persistent
	}
	log.Printf("serving jobs addr=%s state_file=%s", *addr, *stateFile)
	log.Fatal(http.ListenAndServe(*addr, jobs.NewHandler(store)))
}
