package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"pacp/internal/testkit"
)

func main() {
	addr := flag.String("addr", "localhost:18080", "HTTP listen address")
	owner := flag.String("owner", "", "fixture owner to serve")
	root := flag.String("root", "testdata/contract-sim", "contract simulation root")
	manifest := flag.String("manifest", "fixtures/S003/manifest.json", "manifest path relative to root")
	flag.Parse()

	if *owner == "" {
		fmt.Fprintln(os.Stderr, "-owner is required")
		os.Exit(2)
	}

	scenario, err := testkit.LoadScenario(*root, *manifest)
	if err != nil {
		log.Fatalf("load scenario: %v", err)
	}
	pkg, ok := testkit.FindPackage(scenario, *owner)
	if !ok {
		log.Fatalf("owner %q not found in fixture manifest", *owner)
	}

	log.Printf("serving owner=%s addr=%s", *owner, *addr)
	log.Fatal(http.ListenAndServe(*addr, testkit.NewFixtureServer(pkg)))
}
