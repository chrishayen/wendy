package main

import (
	"os"
	"path/filepath"
	"testing"

	"pacp/internal/provider"
)

func TestDefaultEndpoint(t *testing.T) {
	if got := defaultEndpoint(":18088"); got != "http://localhost:18088" {
		t.Fatalf("endpoint = %q", got)
	}
	if got := defaultEndpoint("127.0.0.1:18088"); got != "http://127.0.0.1:18088" {
		t.Fatalf("endpoint = %q", got)
	}
}

func TestProviderEndpointDefault(t *testing.T) {
	t.Setenv("PACP_PROVIDER_ENDPOINT", "http://provider.example")
	if got := providerEndpointDefault(); got != "http://provider.example" {
		t.Fatalf("endpoint default = %q", got)
	}
}

func TestLoadRouteConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routes.json")
	if err := os.WriteFile(path, []byte(`{"routes":{"cap_bridge_echo":{"command":["/bin/echo"],"timeout_seconds":5}}}`), 0o600); err != nil {
		t.Fatalf("write routes: %v", err)
	}
	var cfg routeConfigFile
	if err := loadJSONFile(path, &cfg); err != nil {
		t.Fatalf("load routes: %v", err)
	}
	route := cfg.Routes["cap_bridge_echo"]
	if len(route.Command) != 1 || route.Command[0] != "/bin/echo" || route.TimeoutSeconds != 5 {
		t.Fatalf("route = %#v", route)
	}
	_, ok := any(route).(provider.CommandBridgeRoute)
	if !ok {
		t.Fatal("route does not use provider.CommandBridgeRoute")
	}
}
