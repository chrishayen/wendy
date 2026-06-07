package main

import (
	"flag"
	"log"
	"net/http"

	"pacp/internal/components/leases"
)

func main() {
	addr := flag.String("addr", "localhost:18083", "listen address")
	flag.Parse()

	store := leases.NewStore()
	log.Printf("serving leases addr=%s", *addr)
	if err := http.ListenAndServe(*addr, leases.NewHandler(store)); err != nil {
		log.Fatal(err)
	}
}
