package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"wendy/internal/provider/comfyui"
)

func main() {
	addr := flag.String("addr", "localhost:18090", "listen address")
	endpoint := flag.String("endpoint", os.Getenv("WENDY_PROVIDER_ENDPOINT"), "provider endpoint advertised in the manifest")
	serviceID := flag.String("service-id", comfyui.DefaultServiceID, "provider service id")
	serviceName := flag.String("service-name", "ComfyUI Provider", "provider service name")
	capabilityID := flag.String("capability-id", comfyui.DefaultCapabilityID, "image generation capability id")
	comfyURL := flag.String("comfyui-url", os.Getenv("WENDY_COMFYUI_URL"), "ComfyUI base URL")
	workflowPath := flag.String("workflow", "", "ComfyUI workflow template JSON path")
	loraCatalogPath := flag.String("lora-catalog", "", "optional LoRA catalog JSON path")
	dryRun := flag.Bool("dry-run", false, "return a deterministic image artifact without contacting ComfyUI")
	timeout := flag.Duration("timeout", 2*time.Minute, "maximum generation wait time")
	poll := flag.Duration("poll", 500*time.Millisecond, "ComfyUI history poll interval")
	contentTTL := flag.Duration("content-ttl", 15*time.Minute, "provider-local content reference lifetime")
	runnerTokens := flag.String("runner-tokens", envFirst("WENDY_PROVIDER_RUNNER_TOKENS", "WENDY_RUNNER_CREDENTIAL"), "comma-separated runner bearer tokens allowed to invoke and fetch provider content")
	componentTokens := flag.String("component-tokens", envFirst("WENDY_PROVIDER_COMPONENT_TOKENS", "WENDY_COMPONENT_TOKEN"), "comma-separated component bearer tokens allowed to invoke and fetch provider content")
	agentTokens := flag.String("agent-tokens", os.Getenv("WENDY_PROVIDER_AGENT_TOKENS"), "comma-separated agent bearer tokens that are authenticated but forbidden")
	flag.Parse()

	advertisedEndpoint := *endpoint
	if advertisedEndpoint == "" {
		advertisedEndpoint = defaultEndpoint(*addr)
	}
	server, err := comfyui.NewServer(comfyui.Config{
		Endpoint:        advertisedEndpoint,
		ServiceID:       *serviceID,
		ServiceName:     *serviceName,
		CapabilityID:    *capabilityID,
		ComfyUIURL:      *comfyURL,
		WorkflowPath:    *workflowPath,
		LoraCatalogPath: *loraCatalogPath,
		DryRun:          *dryRun,
		Timeout:         *timeout,
		PollInterval:    *poll,
		ContentTTL:      *contentTTL,
		RunnerTokens:    tokenList(*runnerTokens),
		ComponentTokens: tokenList(*componentTokens),
		AgentTokens:     tokenList(*agentTokens),
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("serving ComfyUI provider addr=%s dry_run=%t", *addr, *dryRun)
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

func envFirst(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}

func tokenList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if token := strings.TrimSpace(part); token != "" {
			out = append(out, token)
		}
	}
	return out
}
