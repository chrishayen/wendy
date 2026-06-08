package main

import (
	"flag"
	"log"
	"net/http"

	"pacp/internal/components/policy"
)

func main() {
	addr := flag.String("addr", "localhost:18085", "listen address")
	flag.Parse()

	store := policy.NewStore()
	log.Printf("serving policy addr=%s", *addr)
	if err := http.ListenAndServe(*addr, policy.NewHandler(store)); err != nil {
		log.Fatal(err)
	}
}
