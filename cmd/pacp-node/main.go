package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"

	"pacp/internal/components/node"
)

func main() {
	addr := flag.String("addr", "localhost:18087", "listen address")
	configPath := flag.String("config", "", "node config JSON path")
	exportLeaseResources := flag.Bool("export-lease-resources", false, "print lease resource registration JSON for this node config and exit")
	flag.Parse()
	if *configPath == "" {
		log.Fatal("-config is required")
	}
	cfg, err := node.LoadConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	if *exportLeaseResources {
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"resources": node.LeaseResourceRegistrations(cfg)}); err != nil {
			log.Fatal(err)
		}
		return
	}
	store, err := node.NewStore(cfg)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("serving node addr=%s node_id=%s", *addr, cfg.NodeID)
	if err := http.ListenAndServe(*addr, node.NewHandler(store)); err != nil {
		log.Fatal(err)
	}
}
