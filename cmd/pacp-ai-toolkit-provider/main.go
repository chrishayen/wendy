package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"pacp/internal/provider/aitoolkit"
)

type engineConfigFile struct {
	TrainCommand []string `json:"train_command"`
}

func main() {
	addr := flag.String("addr", "localhost:18092", "listen address")
	endpoint := flag.String("endpoint", os.Getenv("PACP_PROVIDER_ENDPOINT"), "provider endpoint advertised in the manifest")
	serviceID := flag.String("service-id", aitoolkit.DefaultServiceID, "provider service id")
	serviceName := flag.String("service-name", "AI-Toolkit Provider", "provider service name")
	workspaceRoot := flag.String("workspace", os.Getenv("PACP_AI_TOOLKIT_WORKSPACE"), "provider workspace root")
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
		Endpoint:      advertisedEndpoint,
		ServiceID:     *serviceID,
		ServiceName:   *serviceName,
		WorkspaceRoot: *workspaceRoot,
		DryRun:        *dryRun,
		TrainCommand:  engineConfig.TrainCommand,
		Timeout:       *timeout,
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
