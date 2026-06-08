package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type adminConfig struct {
	CatalogURL     string
	JobsURL        string
	LeasesURL      string
	ArtifactsURL   string
	PolicyURL      string
	GatewayURL     string
	NodeURL        string
	ComponentToken string
	NodeToken      string
	Timeout        time.Duration
}

type serviceTarget struct {
	Name        string
	URL         string
	HealthPath  string
	Credential  string
	Required    bool
	Description string
}

type healthReport struct {
	OK    bool              `json:"ok"`
	Data  healthReportData  `json:"data"`
	Links map[string]any    `json:"links"`
	Meta  map[string]string `json:"meta"`
}

type healthReportData struct {
	Items   []healthItem  `json:"items"`
	Summary healthSummary `json:"summary"`
}

type healthItem struct {
	Name       string `json:"name"`
	URL        string `json:"url"`
	HealthURL  string `json:"health_url"`
	Status     string `json:"status"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Error      string `json:"error,omitempty"`
}

type healthSummary struct {
	Healthy   int `json:"healthy"`
	Unhealthy int `json:"unhealthy"`
	Skipped   int `json:"skipped"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, http.DefaultClient))
}

func run(args []string, stdout, stderr io.Writer, httpClient *http.Client) int {
	cfg := adminConfig{}
	flags := flag.NewFlagSet("pacp-admin", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&cfg.CatalogURL, "catalog-url", envOrDefault("PACP_CATALOG_URL", "http://localhost:18081"), "catalog service base URL")
	flags.StringVar(&cfg.JobsURL, "jobs-url", envOrDefault("PACP_JOBS_URL", "http://localhost:18082"), "jobs service base URL")
	flags.StringVar(&cfg.LeasesURL, "leases-url", envOrDefault("PACP_LEASES_URL", "http://localhost:18083"), "lease service base URL")
	flags.StringVar(&cfg.ArtifactsURL, "artifacts-url", envOrDefault("PACP_ARTIFACTS_URL", "http://localhost:18084"), "artifact service base URL")
	flags.StringVar(&cfg.PolicyURL, "policy-url", envOrDefault("PACP_POLICY_URL", "http://localhost:18085"), "policy service base URL")
	flags.StringVar(&cfg.GatewayURL, "gateway-url", envOrDefault("PACP_GATEWAY_URL", "http://localhost:18086"), "gateway service base URL")
	flags.StringVar(&cfg.NodeURL, "node-url", os.Getenv("PACP_NODE_URL"), "optional node service base URL")
	flags.StringVar(&cfg.ComponentToken, "component-token", os.Getenv("PACP_COMPONENT_TOKEN"), "optional component API bearer token or raw token")
	flags.StringVar(&cfg.NodeToken, "node-token", os.Getenv("PACP_NODE_TOKEN"), "optional node API bearer token or raw token")
	flags.DurationVar(&cfg.Timeout, "timeout", 5*time.Second, "per-command timeout")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	remaining := flags.Args()
	if len(remaining) == 0 {
		printUsage(stderr)
		return 2
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	switch remaining[0] {
	case "health":
		if len(remaining) != 1 {
			fmt.Fprintln(stderr, "usage: pacp-admin [flags] health")
			return 2
		}
		report := checkHealth(cfg, httpClient)
		if err := writeJSON(stdout, report); err != nil {
			fmt.Fprintf(stderr, "write health report: %v\n", err)
			return 1
		}
		if report.OK {
			return 0
		}
		return 1
	default:
		printUsage(stderr)
		fmt.Fprintf(stderr, "unknown command %q\n", remaining[0])
		return 2
	}
}

func checkHealth(cfg adminConfig, httpClient *http.Client) healthReport {
	ctx := context.Background()
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}
	componentCredential := authorizationHeader(cfg.ComponentToken)
	targets := []serviceTarget{
		{Name: "catalog", URL: cfg.CatalogURL, HealthPath: "/v1/catalog/health", Credential: componentCredential, Required: true},
		{Name: "jobs", URL: cfg.JobsURL, HealthPath: "/v1/jobs/health", Credential: componentCredential, Required: true},
		{Name: "leases", URL: cfg.LeasesURL, HealthPath: "/v1/leases/health", Credential: componentCredential, Required: true},
		{Name: "artifacts", URL: cfg.ArtifactsURL, HealthPath: "/v1/artifacts/health", Credential: componentCredential, Required: true},
		{Name: "policy", URL: cfg.PolicyURL, HealthPath: "/v1/policy/health", Credential: componentCredential, Required: true},
		{Name: "gateway", URL: cfg.GatewayURL, HealthPath: "/v1/gateway/health", Required: true},
		{Name: "node", URL: cfg.NodeURL, HealthPath: "/v1/node/health", Credential: authorizationHeader(cfg.NodeToken), Required: false},
	}
	report := healthReport{
		OK:    true,
		Links: map[string]any{},
		Meta: map[string]string{
			"checked_at":      time.Now().UTC().Format(time.RFC3339),
			"schema_version":  "v1",
			"admin_command":   "health",
			"optional_target": "node",
		},
	}
	for _, target := range targets {
		item := checkTarget(ctx, httpClient, target)
		report.Data.Items = append(report.Data.Items, item)
		switch item.Status {
		case "healthy":
			report.Data.Summary.Healthy++
		case "skipped":
			report.Data.Summary.Skipped++
		default:
			report.Data.Summary.Unhealthy++
			if target.Required || target.URL != "" {
				report.OK = false
			}
		}
	}
	return report
}

func checkTarget(ctx context.Context, httpClient *http.Client, target serviceTarget) healthItem {
	baseURL := strings.TrimRight(strings.TrimSpace(target.URL), "/")
	item := healthItem{Name: target.Name, URL: baseURL, Status: "skipped"}
	if baseURL == "" {
		if target.Required {
			item.Status = "unhealthy"
			item.Error = "service URL is required"
		}
		return item
	}
	item.HealthURL = baseURL + target.HealthPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, item.HealthURL, nil)
	if err != nil {
		item.Status = "unhealthy"
		item.Error = err.Error()
		return item
	}
	if target.Credential != "" {
		req.Header.Set("Authorization", target.Credential)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		item.Status = "unhealthy"
		item.Error = err.Error()
		return item
	}
	defer resp.Body.Close()
	item.HTTPStatus = resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		item.Status = "unhealthy"
		item.Error = resp.Status
		return item
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		item.Status = "unhealthy"
		item.Error = "invalid health response: " + err.Error()
		return item
	}
	if !envelope.OK || envelope.Data.Status == "" {
		item.Status = "unhealthy"
		item.Error = "health response was not ok"
		return item
	}
	item.Status = envelope.Data.Status
	if item.Status != "healthy" {
		item.Error = "reported status " + item.Status
	}
	return item
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func authorizationHeader(token string) string {
	if token == "" {
		return ""
	}
	if strings.HasPrefix(token, "Bearer ") {
		return token
	}
	return "Bearer " + token
}

func writeJSON(w io.Writer, body any) error {
	encoded, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(encoded))
	return err
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: pacp-admin [flags] <command>")
	fmt.Fprintln(w, "commands: health")
}
