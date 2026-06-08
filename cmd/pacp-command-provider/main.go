package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"

	"pacp/internal/contracts"
	"pacp/internal/provider"
)

type routeConfigFile struct {
	Routes map[string]provider.CommandBridgeRoute `json:"routes"`
}

func main() {
	addr := flag.String("addr", "localhost:18088", "listen address")
	manifestPath := flag.String("manifest", "", "provider manifest JSON path")
	routesPath := flag.String("routes", "", "command route config JSON path")
	endpoint := flag.String("endpoint", "", "provider endpoint advertised in the manifest")
	flag.Parse()
	if *manifestPath == "" {
		log.Fatal("-manifest is required")
	}
	if *routesPath == "" {
		log.Fatal("-routes is required")
	}

	var manifest contracts.ProviderManifest
	if err := loadJSONFile(*manifestPath, &manifest); err != nil {
		log.Fatal(err)
	}
	if *endpoint != "" {
		manifest.Provider.Endpoint = *endpoint
	} else if manifest.Provider.Endpoint == "" {
		manifest.Provider.Endpoint = defaultEndpoint(*addr)
	}
	if manifest.Provider.HealthPath == "" {
		manifest.Provider.HealthPath = "/v1/provider/health"
	}

	var routeConfig routeConfigFile
	if err := loadJSONFile(*routesPath, &routeConfig); err != nil {
		log.Fatal(err)
	}
	server, err := provider.NewCommandBridgeServer(manifest, provider.CommandBridgeConfig{Routes: routeConfig.Routes})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("serving command bridge provider addr=%s", *addr)
	if err := http.ListenAndServe(*addr, server); err != nil {
		log.Fatal(err)
	}
}

func loadJSONFile(path string, out any) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

func defaultEndpoint(addr string) string {
	if strings.HasPrefix(addr, ":") {
		addr = "localhost" + addr
	}
	return "http://" + addr
}
