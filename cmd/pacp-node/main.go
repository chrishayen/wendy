package main

import (
	"flag"
	"log"
	"net/http"

	"pacp/internal/components/node"
)

func main() {
	addr := flag.String("addr", "localhost:18087", "listen address")
	configPath := flag.String("config", "", "node config JSON path")
	flag.Parse()
	if *configPath == "" {
		log.Fatal("-config is required")
	}
	cfg, err := node.LoadConfig(*configPath)
	if err != nil {
		log.Fatal(err)
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
