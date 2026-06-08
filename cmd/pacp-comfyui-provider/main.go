package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"pacp/internal/provider/comfyui"
)

func main() {
	addr := flag.String("addr", "localhost:18090", "listen address")
	endpoint := flag.String("endpoint", os.Getenv("PACP_PROVIDER_ENDPOINT"), "provider endpoint advertised in the manifest")
	serviceID := flag.String("service-id", comfyui.DefaultServiceID, "provider service id")
	serviceName := flag.String("service-name", "ComfyUI Provider", "provider service name")
	capabilityID := flag.String("capability-id", comfyui.DefaultCapabilityID, "image generation capability id")
	comfyURL := flag.String("comfyui-url", os.Getenv("PACP_COMFYUI_URL"), "ComfyUI base URL")
	workflowPath := flag.String("workflow", "", "ComfyUI workflow template JSON path")
	loraCatalogPath := flag.String("lora-catalog", "", "optional LoRA catalog JSON path")
	dryRun := flag.Bool("dry-run", false, "return a deterministic image artifact without contacting ComfyUI")
	timeout := flag.Duration("timeout", 2*time.Minute, "maximum generation wait time")
	poll := flag.Duration("poll", 500*time.Millisecond, "ComfyUI history poll interval")
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
