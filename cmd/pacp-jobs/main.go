package main

import (
	"flag"
	"log"
	"net/http"

	"pacp/internal/components/jobs"
)

func main() {
	addr := flag.String("addr", "localhost:18082", "HTTP listen address")
	flag.Parse()

	log.Printf("serving jobs addr=%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, jobs.NewHandler(jobs.NewStore())))
}
