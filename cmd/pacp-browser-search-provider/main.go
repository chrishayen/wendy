package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"pacp/internal/provider/browsersearch"
)

func main() {
	addr := flag.String("addr", "localhost:18089", "listen address")
	endpoint := flag.String("endpoint", os.Getenv("PACP_PROVIDER_ENDPOINT"), "provider endpoint advertised in the manifest")
	serviceID := flag.String("service-id", "svc_browser_search", "provider service id")
	serviceName := flag.String("service-name", "Browser Search Provider", "provider service name")
	searchIndex := flag.String("search-index", "", "optional JSON search index path")
	allowedHosts := flag.String("allowed-hosts", envOrDefault("PACP_BROWSER_ALLOWED_HOSTS", "localhost,127.0.0.1,::1"), "comma-separated allowed browser hosts; use * only for trusted deployments")
	allowHTTP := flag.Bool("allow-http", false, "allow non-loopback http URLs")
	timeout := flag.Duration("timeout", 30*time.Second, "browser request timeout")
	flag.Parse()

	advertisedEndpoint := *endpoint
	if advertisedEndpoint == "" {
		advertisedEndpoint = defaultEndpoint(*addr)
	}
	server, err := browsersearch.NewServer(browsersearch.Config{
		Endpoint:        advertisedEndpoint,
		ServiceID:       *serviceID,
		ServiceName:     *serviceName,
		SearchIndexPath: *searchIndex,
		AllowedHosts:    splitCSV(*allowedHosts),
		AllowHTTP:       *allowHTTP,
		Timeout:         *timeout,
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("serving browser/search provider addr=%s", *addr)
	if err := http.ListenAndServe(*addr, server); err != nil {
		log.Fatal(err)
	}
}

func defaultEndpoint(addr string) string {
	if strings.HasPrefix(addr, ":") {
		addr = "localhost" + addr
	}
	return "http://" + addr
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
