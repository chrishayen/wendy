package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"wendy/internal/provider/aitoolkit"
)

type engineConfigFile struct {
	TrainCommand []string `json:"train_command"`
}

func main() {
	addr := flag.String("addr", "localhost:18092", "listen address")
	endpoint := flag.String("endpoint", os.Getenv("WENDY_PROVIDER_ENDPOINT"), "provider endpoint advertised in the manifest")
	serviceID := flag.String("service-id", aitoolkit.DefaultServiceID, "provider service id")
	serviceName := flag.String("service-name", "AI-Toolkit Provider", "provider service name")
	workspaceRoot := flag.String("workspace", os.Getenv("WENDY_AI_TOOLKIT_WORKSPACE"), "provider workspace root")
	providerCredential := flag.String("provider-credential", envFirst("WENDY_PROVIDER_CREDENTIAL", "WENDY_PROVIDER_TOKEN"), "optional provider bearer credential required for invoke and content routes")
	engineConfigPath := flag.String("engine-config", "", "optional training engine command config JSON path")
	dryRun := flag.Bool("dry-run", false, "simulate training without invoking AI-Toolkit")
	timeout := flag.Duration("timeout", time.Hour, "training command timeout")
	flag.Parse()

	engineConfig, err := loadEngineConfig(*engineConfigPath)
	if err != nil {
		log.Fatal(err)
	}
	advertisedEndpoint := *endpoint
	if advertisedEndpoint == "" {
		advertisedEndpoint = defaultEndpoint(*addr)
	}
	server, err := aitoolkit.NewServer(aitoolkit.Config{
		Endpoint:       advertisedEndpoint,
		ServiceID:      *serviceID,
		ServiceName:    *serviceName,
		AuthCredential: *providerCredential,
		WorkspaceRoot:  *workspaceRoot,
		DryRun:         *dryRun,
		TrainCommand:   engineConfig.TrainCommand,
		Timeout:        *timeout,
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("serving AI-Toolkit provider addr=%s dry_run=%t workspace=%s", *addr, *dryRun, *workspaceRoot)
	if err := http.ListenAndServe(*addr, server); err != nil {
		log.Fatal(err)
	}
}

func loadEngineConfig(path string) (engineConfigFile, error) {
	if strings.TrimSpace(path) == "" {
		return engineConfigFile{}, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return engineConfigFile{}, err
	}
	var cfg engineConfigFile
	if err := json.Unmarshal(body, &cfg); err != nil {
		return engineConfigFile{}, err
	}
	return cfg, nil
}

func defaultEndpoint(addr string) string {
	if strings.HasPrefix(addr, ":") {
		addr = "localhost" + addr
	}
	return "http://" + addr
}

func envFirst(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}
