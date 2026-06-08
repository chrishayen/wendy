package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	GatewayToken   string
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
	flags.StringVar(&cfg.GatewayToken, "gateway-token", envFirst("PACP_GATEWAY_TOKEN", "PACP_AGENT_TOKEN"), "optional gateway API bearer token or raw token")
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
	case "catalog":
		return catalogCommand(cfg, httpClient, remaining[1:], stdout, stderr)
	case "jobs":
		return jobsCommand(cfg, httpClient, remaining[1:], stdout, stderr)
	case "leases":
		return leasesCommand(cfg, httpClient, remaining[1:], stdout, stderr)
	case "artifacts":
		return artifactsCommand(cfg, httpClient, remaining[1:], stdout, stderr)
	case "node":
		return nodeCommand(cfg, httpClient, remaining[1:], stdout, stderr)
	default:
		printUsage(stderr)
		fmt.Fprintf(stderr, "unknown command %q\n", remaining[0])
		return 2
	}
}

func catalogCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: pacp-admin [flags] catalog <services|service|capabilities|capability|route|tags> [id]")
		return 2
	}
	switch args[0] {
	case "services":
		if len(args) != 1 {
			return usage(stderr, "usage: pacp-admin [flags] catalog services")
		}
		return getJSON(cfg, httpClient, cfg.CatalogURL, "/v1/catalog/services", authorizationHeader(cfg.ComponentToken), stdout, stderr)
	case "service":
		if len(args) != 2 {
			return usage(stderr, "usage: pacp-admin [flags] catalog service <service-id>")
		}
		return getJSON(cfg, httpClient, cfg.CatalogURL, "/v1/catalog/services/"+url.PathEscape(args[1]), authorizationHeader(cfg.ComponentToken), stdout, stderr)
	case "capabilities":
		if len(args) != 1 {
			return usage(stderr, "usage: pacp-admin [flags] catalog capabilities")
		}
		return getJSON(cfg, httpClient, cfg.CatalogURL, "/v1/catalog/capabilities", authorizationHeader(cfg.ComponentToken), stdout, stderr)
	case "capability":
		if len(args) != 2 {
			return usage(stderr, "usage: pacp-admin [flags] catalog capability <capability-id>")
		}
		return getJSON(cfg, httpClient, cfg.CatalogURL, "/v1/catalog/capabilities/"+url.PathEscape(args[1]), authorizationHeader(cfg.ComponentToken), stdout, stderr)
	case "route":
		if len(args) != 2 {
			return usage(stderr, "usage: pacp-admin [flags] catalog route <capability-id>")
		}
		return getJSON(cfg, httpClient, cfg.CatalogURL, "/v1/catalog/capabilities/"+url.PathEscape(args[1])+"/route", authorizationHeader(cfg.ComponentToken), stdout, stderr)
	case "tags":
		if len(args) != 1 {
			return usage(stderr, "usage: pacp-admin [flags] catalog tags")
		}
		return getJSON(cfg, httpClient, cfg.CatalogURL, "/v1/catalog/tags", authorizationHeader(cfg.ComponentToken), stdout, stderr)
	default:
		return usage(stderr, "usage: pacp-admin [flags] catalog <services|service|capabilities|capability|route|tags> [id]")
	}
}

func jobsCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usage(stderr, "usage: pacp-admin [flags] jobs <list|job|logs|cancel> [id]")
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return usage(stderr, "usage: pacp-admin [flags] jobs list")
		}
		return getJSON(cfg, httpClient, cfg.JobsURL, "/v1/jobs", authorizationHeader(cfg.ComponentToken), stdout, stderr)
	case "job":
		if len(args) != 2 {
			return usage(stderr, "usage: pacp-admin [flags] jobs job <job-id>")
		}
		return getJSON(cfg, httpClient, cfg.JobsURL, "/v1/jobs/"+url.PathEscape(args[1]), authorizationHeader(cfg.ComponentToken), stdout, stderr)
	case "logs":
		if len(args) != 2 {
			return usage(stderr, "usage: pacp-admin [flags] jobs logs <job-id>")
		}
		return getJSON(cfg, httpClient, cfg.JobsURL, "/v1/jobs/"+url.PathEscape(args[1])+"/logs", authorizationHeader(cfg.ComponentToken), stdout, stderr)
	case "cancel":
		return jobsCancelCommand(cfg, httpClient, args[1:], stdout, stderr)
	default:
		return usage(stderr, "usage: pacp-admin [flags] jobs <list|job|logs|cancel> [id]")
	}
}

func jobsCancelCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("jobs cancel", flag.ContinueOnError)
	flags.SetOutput(stderr)
	reason := flags.String("reason", "", "cancel reason")
	idempotencyKey := flags.String("idempotency-key", "", "idempotency key for this job cancellation")
	remaining, err := parseSubcommandFlags(flags, args)
	if err != nil {
		return 2
	}
	if len(remaining) != 1 {
		return usage(stderr, "usage: pacp-admin [flags] jobs cancel <job-id> -idempotency-key <key> [-reason text]")
	}
	if *idempotencyKey == "" {
		return usage(stderr, "idempotency-key is required for jobs cancel")
	}
	if cfg.GatewayToken == "" {
		return usage(stderr, "gateway-token is required for jobs cancel; set -gateway-token, PACP_GATEWAY_TOKEN, or PACP_AGENT_TOKEN")
	}
	body := map[string]any{}
	if *reason != "" {
		body["reason"] = *reason
	}
	path := "/v1/agent/jobs/" + url.PathEscape(remaining[0]) + "/cancel"
	return postJSONBody(cfg, httpClient, cfg.GatewayURL, path, authorizationHeader(cfg.GatewayToken), *idempotencyKey, body, stdout, stderr)
}

func leasesCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usage(stderr, "usage: pacp-admin [flags] leases <resources|resource|inspect|request|lease> [id]")
	}
	switch args[0] {
	case "resources":
		if len(args) != 1 {
			return usage(stderr, "usage: pacp-admin [flags] leases resources")
		}
		return getJSON(cfg, httpClient, cfg.LeasesURL, "/v1/resources", authorizationHeader(cfg.ComponentToken), stdout, stderr)
	case "resource":
		if len(args) != 2 {
			return usage(stderr, "usage: pacp-admin [flags] leases resource <resource-id>")
		}
		return getJSON(cfg, httpClient, cfg.LeasesURL, "/v1/resources/"+url.PathEscape(args[1]), authorizationHeader(cfg.ComponentToken), stdout, stderr)
	case "inspect":
		if len(args) != 2 {
			return usage(stderr, "usage: pacp-admin [flags] leases inspect <resource-id>")
		}
		return getJSON(cfg, httpClient, cfg.LeasesURL, "/v1/resources/"+url.PathEscape(args[1])+"/inspection", authorizationHeader(cfg.ComponentToken), stdout, stderr)
	case "request":
		if len(args) != 2 {
			return usage(stderr, "usage: pacp-admin [flags] leases request <request-id>")
		}
		return getJSON(cfg, httpClient, cfg.LeasesURL, "/v1/lease-requests/"+url.PathEscape(args[1]), authorizationHeader(cfg.ComponentToken), stdout, stderr)
	case "lease":
		if len(args) != 2 {
			return usage(stderr, "usage: pacp-admin [flags] leases lease <lease-id>")
		}
		return getJSON(cfg, httpClient, cfg.LeasesURL, "/v1/leases/"+url.PathEscape(args[1]), authorizationHeader(cfg.ComponentToken), stdout, stderr)
	default:
		return usage(stderr, "usage: pacp-admin [flags] leases <resources|resource|inspect|request|lease> [id]")
	}
}

func artifactsCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usage(stderr, "usage: pacp-admin [flags] artifacts <list|artifact|upload> [id]")
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return usage(stderr, "usage: pacp-admin [flags] artifacts list")
		}
		return getJSON(cfg, httpClient, cfg.ArtifactsURL, "/v1/artifacts", authorizationHeader(cfg.ComponentToken), stdout, stderr)
	case "artifact":
		if len(args) != 2 {
			return usage(stderr, "usage: pacp-admin [flags] artifacts artifact <artifact-id>")
		}
		return getJSON(cfg, httpClient, cfg.ArtifactsURL, "/v1/artifacts/"+url.PathEscape(args[1]), authorizationHeader(cfg.ComponentToken), stdout, stderr)
	case "upload":
		if len(args) != 2 {
			return usage(stderr, "usage: pacp-admin [flags] artifacts upload <upload-id>")
		}
		return getJSON(cfg, httpClient, cfg.ArtifactsURL, "/v1/artifact-uploads/"+url.PathEscape(args[1]), authorizationHeader(cfg.ComponentToken), stdout, stderr)
	default:
		return usage(stderr, "usage: pacp-admin [flags] artifacts <list|artifact|upload> [id]")
	}
}

func nodeCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	if cfg.NodeURL == "" {
		fmt.Fprintln(stderr, "node-url is required; set -node-url or PACP_NODE_URL")
		return 2
	}
	if len(args) == 0 {
		return usage(stderr, "usage: pacp-admin [flags] node <resources|services|service|start|stop> [id]")
	}
	switch args[0] {
	case "resources":
		if len(args) != 1 {
			return usage(stderr, "usage: pacp-admin [flags] node resources")
		}
		return getJSON(cfg, httpClient, cfg.NodeURL, "/v1/node/resources", authorizationHeader(cfg.NodeToken), stdout, stderr)
	case "services":
		if len(args) != 1 {
			return usage(stderr, "usage: pacp-admin [flags] node services")
		}
		return getJSON(cfg, httpClient, cfg.NodeURL, "/v1/node/services", authorizationHeader(cfg.NodeToken), stdout, stderr)
	case "service":
		if len(args) != 2 {
			return usage(stderr, "usage: pacp-admin [flags] node service <service-id>")
		}
		return getJSON(cfg, httpClient, cfg.NodeURL, "/v1/node/services/"+url.PathEscape(args[1]), authorizationHeader(cfg.NodeToken), stdout, stderr)
	case "start":
		return nodeStartCommand(cfg, httpClient, args[1:], stdout, stderr)
	case "stop":
		if len(args) != 2 {
			return usage(stderr, "usage: pacp-admin [flags] node stop <service-id>")
		}
		return postJSON(cfg, httpClient, cfg.NodeURL, "/v1/node/services/"+url.PathEscape(args[1])+"/stop", authorizationHeader(cfg.NodeToken), "", stdout, stderr)
	default:
		return usage(stderr, "usage: pacp-admin [flags] node <resources|services|service|start|stop> [id]")
	}
}

func nodeStartCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("node start", flag.ContinueOnError)
	flags.SetOutput(stderr)
	idempotencyKey := flags.String("idempotency-key", "", "idempotency key for this node start")
	remaining, err := parseSubcommandFlags(flags, args)
	if err != nil {
		return 2
	}
	if len(remaining) != 1 {
		return usage(stderr, "usage: pacp-admin [flags] node start <service-id> -idempotency-key <key>")
	}
	if *idempotencyKey == "" {
		return usage(stderr, "idempotency-key is required for node start")
	}
	path := "/v1/node/services/" + url.PathEscape(remaining[0]) + "/start"
	return postJSON(cfg, httpClient, cfg.NodeURL, path, authorizationHeader(cfg.NodeToken), *idempotencyKey, stdout, stderr)
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

func getJSON(cfg adminConfig, httpClient *http.Client, baseURL, path, credential string, stdout, stderr io.Writer) int {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		fmt.Fprintf(stderr, "service URL is required for %s\n", path)
		return 2
	}
	ctx := context.Background()
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if credential != "" {
		req.Header.Set("Authorization", credential)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := writePrettyJSON(stdout, raw); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(stderr, "component returned HTTP %d\n", resp.StatusCode)
		return 1
	}
	return 0
}

func postJSON(cfg adminConfig, httpClient *http.Client, baseURL, path, credential, idempotencyKey string, stdout, stderr io.Writer) int {
	return postJSONBody(cfg, httpClient, baseURL, path, credential, idempotencyKey, nil, stdout, stderr)
}

func postJSONBody(cfg adminConfig, httpClient *http.Client, baseURL, path, credential, idempotencyKey string, body any, stdout, stderr io.Writer) int {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		fmt.Fprintf(stderr, "service URL is required for %s\n", path)
		return 2
	}
	ctx := context.Background()
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		reader = strings.NewReader(string(raw))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, reader)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if credential != "" {
		req.Header.Set("Authorization", credential)
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := writePrettyJSON(stdout, raw); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(stderr, "component returned HTTP %d\n", resp.StatusCode)
		return 1
	}
	return 0
}

func parseSubcommandFlags(flags *flag.FlagSet, args []string) ([]string, error) {
	var leading string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		leading = args[0]
		args = args[1:]
	}
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	remaining := flags.Args()
	if leading != "" {
		remaining = append([]string{leading}, remaining...)
	}
	return remaining, nil
}

func usage(stderr io.Writer, message string) int {
	fmt.Fprintln(stderr, message)
	return 2
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envFirst(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
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

func writePrettyJSON(w io.Writer, raw []byte) error {
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		_, _ = w.Write(raw)
		if len(raw) == 0 || raw[len(raw)-1] != '\n' {
			_, _ = fmt.Fprintln(w)
		}
		return err
	}
	encoded, err := json.MarshalIndent(decoded, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(encoded))
	return err
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: pacp-admin [flags] <command>")
	fmt.Fprintln(w, "commands: health, catalog, jobs, leases, artifacts, node")
}
