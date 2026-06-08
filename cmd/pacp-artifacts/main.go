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
	flag.Parse()

	store, err := artifacts.NewStore(*root)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("serving artifacts addr=%s root=%s", *addr, *root)
	if err := http.ListenAndServe(*addr, artifacts.NewHandler(store)); err != nil {
		log.Fatal(err)
	}
}
