package main

import (
	"flag"
	"log"
	"net/http"

	"pacp/internal/components/gateway"
)

func main() {
	addr := flag.String("addr", "localhost:18086", "listen address")
	catalogURL := flag.String("catalog-url", "http://localhost:18081", "C03 catalog base URL")
	policyURL := flag.String("policy-url", "http://localhost:18085", "C08 policy base URL")
	jobsURL := flag.String("jobs-url", "http://localhost:18082", "C05 jobs base URL")
	artifactsURL := flag.String("artifacts-url", "http://localhost:18084", "C07 artifacts base URL")
	gatewayCredential := flag.String("gateway-credential", "", "component credential for downstream calls")
	flag.Parse()

	handler := gateway.NewHandler(gateway.Config{
		CatalogURL:        *catalogURL,
		PolicyURL:         *policyURL,
		JobsURL:           *jobsURL,
		ArtifactsURL:      *artifactsURL,
		GatewayCredential: *gatewayCredential,
	})
	log.Printf("serving gateway addr=%s", *addr)
	if err := http.ListenAndServe(*addr, handler); err != nil {
		log.Fatal(err)
	}
}
