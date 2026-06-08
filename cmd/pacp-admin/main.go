package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"pacp/internal/contracts"
)

type adminConfig struct {
	CatalogURL     string
	JobsURL        string
	LeasesURL      string
	ArtifactsURL   string
	PolicyURL      string
	GatewayURL     string
	NodeURL        string
	NodeURLs       string
	RunnerURL      string
	ComponentToken string
	GatewayToken   string
	NodeToken      string
	RunnerToken    string
	Timeout        time.Duration
}

type serviceTarget struct {
	Name        string
	Kind        string
	URL         string
	HealthPath  string
	MetricsPath string
	Credential  string
	Required    bool
	Description string
	ServiceID   string
	NodeID      string
	ConfigError string
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
	Kind       string `json:"kind,omitempty"`
	ServiceID  string `json:"service_id,omitempty"`
	NodeID     string `json:"node_id,omitempty"`
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

type metricsReport struct {
	OK    bool              `json:"ok"`
	Data  metricsReportData `json:"data"`
	Links map[string]any    `json:"links"`
	Meta  map[string]string `json:"meta"`
}

type metricsReportData struct {
	Items   []metricsItem  `json:"items"`
	Summary metricsSummary `json:"summary"`
}

type metricsItem struct {
	Name       string                   `json:"name"`
	Kind       string                   `json:"kind,omitempty"`
	ServiceID  string                   `json:"service_id,omitempty"`
	NodeID     string                   `json:"node_id,omitempty"`
	URL        string                   `json:"url"`
	MetricsURL string                   `json:"metrics_url"`
	Component  string                   `json:"component,omitempty"`
	HTTPStatus int                      `json:"http_status,omitempty"`
	Samples    []contracts.MetricSample `json:"samples,omitempty"`
	Error      string                   `json:"error,omitempty"`
}

type metricsSummary struct {
	Available   int `json:"available"`
	Unavailable int `json:"unavailable"`
	Skipped     int `json:"skipped"`
	Samples     int `json:"samples"`
}

type alertsReport struct {
	OK    bool              `json:"ok"`
	Data  alertsReportData  `json:"data"`
	Links map[string]any    `json:"links"`
	Meta  map[string]string `json:"meta"`
}

type alertsReportData struct {
	Findings []diagnosticFinding `json:"findings"`
	Summary  alertSummary        `json:"summary"`
	Health   healthReportData    `json:"health"`
	Metrics  metricsReportData   `json:"metrics"`
}

type alertSummary struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Info     int `json:"info"`
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
	flags.StringVar(&cfg.NodeURLs, "node-urls", os.Getenv("PACP_NODE_URLS"), "optional comma-separated node_id=URL entries")
	flags.StringVar(&cfg.RunnerURL, "runner-url", os.Getenv("PACP_RUNNER_URL"), "optional runner monitor base URL")
	flags.StringVar(&cfg.ComponentToken, "component-token", os.Getenv("PACP_COMPONENT_TOKEN"), "optional component API bearer token or raw token")
	flags.StringVar(&cfg.GatewayToken, "gateway-token", envFirst("PACP_GATEWAY_TOKEN", "PACP_AGENT_TOKEN"), "optional gateway API bearer token or raw token")
	flags.StringVar(&cfg.NodeToken, "node-token", os.Getenv("PACP_NODE_TOKEN"), "optional node API bearer token or raw token")
	flags.StringVar(&cfg.RunnerToken, "runner-token", os.Getenv("PACP_RUNNER_MONITOR_TOKEN"), "optional runner monitor bearer token or raw token")
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
		return healthCommand(cfg, httpClient, remaining[1:], stdout, stderr)
	case "metrics":
		return metricsCommand(cfg, httpClient, remaining[1:], stdout, stderr)
	case "alerts":
		return alertsCommand(cfg, httpClient, remaining[1:], stdout, stderr)
	case "catalog":
		return catalogCommand(cfg, httpClient, remaining[1:], stdout, stderr)
	case "jobs":
		return jobsCommand(cfg, httpClient, remaining[1:], stdout, stderr)
	case "leases":
		return leasesCommand(cfg, httpClient, remaining[1:], stdout, stderr)
	case "artifacts":
		return artifactsCommand(cfg, httpClient, remaining[1:], stdout, stderr)
	case "policy":
		return policyCommand(cfg, httpClient, remaining[1:], stdout, stderr)
	case "node":
		return nodeCommand(cfg, httpClient, remaining[1:], stdout, stderr)
	case "diagnose":
		return diagnoseCommand(cfg, httpClient, remaining[1:], stdout, stderr)
	default:
		printUsage(stderr)
		fmt.Fprintf(stderr, "unknown command %q\n", remaining[0])
		return 2
	}
}

type healthOptions struct {
	Providers bool
}

type alertOptions struct {
	QueueDepthThreshold       int
	RunnerHeartbeatStaleAfter time.Duration
	Now                       time.Time
}

type diagnosticFinding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

type jobDiagnosticData struct {
	Job              contracts.Job           `json:"job"`
	Logs             []contracts.JobLogEntry `json:"logs"`
	Findings         []diagnosticFinding     `json:"findings"`
	SuggestedActions []string                `json:"suggested_actions"`
}

func healthCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("health", flag.ContinueOnError)
	flags.SetOutput(stderr)
	providers := flags.Bool("providers", false, "check registered provider health through catalog routes")
	remaining, err := parseSubcommandFlags(flags, args)
	if err != nil {
		return 2
	}
	if len(remaining) != 0 {
		return usage(stderr, "usage: pacp-admin [flags] health [-providers]")
	}
	report := checkHealth(cfg, httpClient, healthOptions{Providers: *providers})
	if err := writeJSON(stdout, report); err != nil {
		fmt.Fprintf(stderr, "write health report: %v\n", err)
		return 1
	}
	if report.OK {
		return 0
	}
	return 1
}

func metricsCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("metrics", flag.ContinueOnError)
	flags.SetOutput(stderr)
	remaining, err := parseSubcommandFlags(flags, args)
	if err != nil {
		return 2
	}
	if len(remaining) != 0 {
		return usage(stderr, "usage: pacp-admin [flags] metrics")
	}
	report := collectMetrics(cfg, httpClient)
	if err := writeJSON(stdout, report); err != nil {
		fmt.Fprintf(stderr, "write metrics report: %v\n", err)
		return 1
	}
	if report.OK {
		return 0
	}
	return 1
}

func alertsCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("alerts", flag.ContinueOnError)
	flags.SetOutput(stderr)
	queueDepthThreshold := flags.Int("queue-depth-threshold", 0, "lease queue depth above this value produces a warning")
	runnerHeartbeatStaleAfter := flags.Duration("runner-heartbeat-stale-after", 0, "active runner heartbeat age above this duration produces a warning")
	remaining, err := parseSubcommandFlags(flags, args)
	if err != nil {
		return 2
	}
	if len(remaining) != 0 {
		return usage(stderr, "usage: pacp-admin [flags] alerts [-queue-depth-threshold n] [-runner-heartbeat-stale-after duration]")
	}
	if *queueDepthThreshold < 0 {
		return usage(stderr, "queue-depth-threshold must be zero or greater")
	}
	if *runnerHeartbeatStaleAfter < 0 {
		return usage(stderr, "runner-heartbeat-stale-after must be zero or greater")
	}
	health := checkHealth(cfg, httpClient, healthOptions{})
	metrics := collectMetrics(cfg, httpClient)
	report := buildAlertsReport(health, metrics, alertOptions{
		QueueDepthThreshold:       *queueDepthThreshold,
		RunnerHeartbeatStaleAfter: *runnerHeartbeatStaleAfter,
		Now:                       time.Now(),
	})
	if err := writeJSON(stdout, report); err != nil {
		fmt.Fprintf(stderr, "write alerts report: %v\n", err)
		return 1
	}
	if report.OK {
		return 0
	}
	return 1
}

func catalogCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: pacp-admin [flags] catalog <services|service|capabilities|capability|route|tags|import> [id]")
		return 2
	}
	switch args[0] {
	case "import":
		return catalogImportCommand(cfg, httpClient, args[1:], stdout, stderr)
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
		return usage(stderr, "usage: pacp-admin [flags] catalog <services|service|capabilities|capability|route|tags|import> [id]")
	}
}

func catalogImportCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return usage(stderr, "usage: pacp-admin [flags] catalog import <manifest-file-or-dir>")
	}
	manifests, err := loadManifestInputs(args[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	results := make([]any, 0, len(manifests))
	for _, manifest := range manifests {
		var envelope map[string]any
		status, err := postJSONDecode(cfg, httpClient, cfg.CatalogURL, "/v1/catalog/manifests", authorizationHeader(cfg.ComponentToken), "", manifest, &envelope)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		results = append(results, map[string]any{
			"status":     status,
			"service_id": manifest.Service.ID,
			"response":   envelope,
		})
		if status < 200 || status >= 300 {
			if err := writeJSON(stdout, map[string]any{"ok": false, "data": map[string]any{"items": results}, "links": map[string]any{}, "meta": map[string]string{"schema_version": "v1"}}); err != nil {
				fmt.Fprintln(stderr, err)
			}
			fmt.Fprintf(stderr, "component returned HTTP %d\n", status)
			return 1
		}
	}
	return writeCommandJSON(stdout, stderr, map[string]any{
		"ok":    true,
		"data":  map[string]any{"items": results},
		"links": map[string]any{},
		"meta":  map[string]string{"schema_version": "v1", "admin_command": "catalog import"},
	})
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
		return usage(stderr, "usage: pacp-admin [flags] leases <resources|resource|register-resource|inspect|requests|request|create-request|cancel-request|lease|release> [id]")
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
	case "register-resource":
		return leasesRegisterResourceCommand(cfg, httpClient, args[1:], stdout, stderr)
	case "inspect":
		if len(args) != 2 {
			return usage(stderr, "usage: pacp-admin [flags] leases inspect <resource-id>")
		}
		return getJSON(cfg, httpClient, cfg.LeasesURL, "/v1/resources/"+url.PathEscape(args[1])+"/inspection", authorizationHeader(cfg.ComponentToken), stdout, stderr)
	case "requests":
		return leasesRequestsCommand(cfg, httpClient, args[1:], stdout, stderr)
	case "request":
		if len(args) != 2 {
			return usage(stderr, "usage: pacp-admin [flags] leases request <request-id>")
		}
		return getJSON(cfg, httpClient, cfg.LeasesURL, "/v1/lease-requests/"+url.PathEscape(args[1]), authorizationHeader(cfg.ComponentToken), stdout, stderr)
	case "create-request":
		return leasesCreateRequestCommand(cfg, httpClient, args[1:], stdout, stderr)
	case "cancel-request":
		return leasesCancelRequestCommand(cfg, httpClient, args[1:], stdout, stderr)
	case "lease":
		if len(args) != 2 {
			return usage(stderr, "usage: pacp-admin [flags] leases lease <lease-id>")
		}
		return getJSON(cfg, httpClient, cfg.LeasesURL, "/v1/leases/"+url.PathEscape(args[1]), authorizationHeader(cfg.ComponentToken), stdout, stderr)
	case "release":
		return leasesReleaseCommand(cfg, httpClient, args[1:], stdout, stderr)
	default:
		return usage(stderr, "usage: pacp-admin [flags] leases <resources|resource|register-resource|inspect|requests|request|create-request|cancel-request|lease|release> [id]")
	}
}

func leasesRegisterResourceCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("leases register-resource", flag.ContinueOnError)
	flags.SetOutput(stderr)
	resourceID := flags.String("resource-id", "", "resource id")
	selector := flags.String("selector", "", "resource selector")
	displayName := flags.String("display-name", "", "display name")
	status := flags.String("status", "", "resource status")
	nodeID := flags.String("node-id", "", "owning node id")
	tags := flags.String("tags", "", "comma-separated resource tags")
	metadata := flags.String("metadata", "", "JSON object metadata")
	remaining, err := parseSubcommandFlags(flags, args)
	if err != nil {
		return 2
	}
	if len(remaining) != 0 {
		return usage(stderr, "usage: pacp-admin [flags] leases register-resource -selector <selector> [-resource-id id] [-node-id node] [-tags a,b] [-metadata JSON]")
	}
	if *selector == "" {
		return usage(stderr, "selector is required for leases register-resource")
	}
	metadataObject, err := optionalJSONObject(*metadata)
	if err != nil {
		fmt.Fprintf(stderr, "metadata: %v\n", err)
		return 2
	}
	req := contracts.RegisterResourceRequest{
		ResourceID:  *resourceID,
		Selector:    *selector,
		DisplayName: *displayName,
		NodeID:      *nodeID,
		Tags:        splitCSV(*tags),
		Metadata:    metadataObject,
	}
	if *status != "" {
		req.Status = contracts.ResourceStatus(*status)
	}
	return postJSONBody(cfg, httpClient, cfg.LeasesURL, "/v1/resources", authorizationHeader(cfg.ComponentToken), "", req, stdout, stderr)
}

func leasesRequestsCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("leases requests", flag.ContinueOnError)
	flags.SetOutput(stderr)
	requesterID := flags.String("requester-id", "", "requester or job id")
	remaining, err := parseSubcommandFlags(flags, args)
	if err != nil {
		return 2
	}
	if len(remaining) != 0 {
		return usage(stderr, "usage: pacp-admin [flags] leases requests -requester-id <id>")
	}
	if *requesterID == "" {
		return usage(stderr, "requester-id is required for leases requests")
	}
	path := "/v1/lease-requests?requester_id=" + url.QueryEscape(*requesterID)
	return getJSON(cfg, httpClient, cfg.LeasesURL, path, authorizationHeader(cfg.ComponentToken), stdout, stderr)
}

func leasesCreateRequestCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("leases create-request", flag.ContinueOnError)
	flags.SetOutput(stderr)
	requesterID := flags.String("requester-id", "", "requester or holder id")
	selector := flags.String("selector", "", "resource selector")
	priority := flags.Int("priority", 0, "request priority")
	heartbeatTimeout := flags.Int("heartbeat-timeout-seconds", 0, "heartbeat timeout in seconds")
	remaining, err := parseSubcommandFlags(flags, args)
	if err != nil {
		return 2
	}
	if len(remaining) != 0 {
		return usage(stderr, "usage: pacp-admin [flags] leases create-request -requester-id <id> -selector <selector> [-priority n] [-heartbeat-timeout-seconds n]")
	}
	if *requesterID == "" {
		return usage(stderr, "requester-id is required for leases create-request")
	}
	if *selector == "" {
		return usage(stderr, "selector is required for leases create-request")
	}
	req := contracts.CreateLeaseRequest{
		RequesterID:             *requesterID,
		ResourceSelector:        *selector,
		Priority:                *priority,
		HeartbeatTimeoutSeconds: *heartbeatTimeout,
	}
	return postJSONBody(cfg, httpClient, cfg.LeasesURL, "/v1/lease-requests", authorizationHeader(cfg.ComponentToken), "", req, stdout, stderr)
}

func leasesCancelRequestCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("leases cancel-request", flag.ContinueOnError)
	flags.SetOutput(stderr)
	reason := flags.String("reason", "", "cancel reason")
	remaining, err := parseSubcommandFlags(flags, args)
	if err != nil {
		return 2
	}
	if len(remaining) != 1 {
		return usage(stderr, "usage: pacp-admin [flags] leases cancel-request <request-id> [-reason text]")
	}
	body := map[string]any{}
	if *reason != "" {
		body["reason"] = *reason
	}
	path := "/v1/lease-requests/" + url.PathEscape(remaining[0]) + "/cancel"
	return postJSONBody(cfg, httpClient, cfg.LeasesURL, path, authorizationHeader(cfg.ComponentToken), "", body, stdout, stderr)
}

func leasesReleaseCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("leases release", flag.ContinueOnError)
	flags.SetOutput(stderr)
	holderID := flags.String("holder-id", "", "lease holder id")
	reason := flags.String("reason", "", "release reason")
	idempotencyKey := flags.String("idempotency-key", "", "idempotency key for this lease release")
	actorSubjectID := flags.String("actor-subject-id", "", "actor subject id for lease audit")
	remaining, err := parseSubcommandFlags(flags, args)
	if err != nil {
		return 2
	}
	if len(remaining) != 1 {
		return usage(stderr, "usage: pacp-admin [flags] leases release <lease-id> -holder-id <holder> -idempotency-key <key> [-reason text] [-actor-subject-id sub_admin]")
	}
	if *holderID == "" {
		return usage(stderr, "holder-id is required for leases release")
	}
	if *idempotencyKey == "" {
		return usage(stderr, "idempotency-key is required for leases release")
	}
	req := contracts.LeaseReleaseRequest{HolderID: *holderID, Reason: *reason}
	headers := map[string]string{}
	if *actorSubjectID != "" {
		headers["X-Actor-Subject-ID"] = *actorSubjectID
	}
	path := "/v1/leases/" + url.PathEscape(remaining[0]) + "/release"
	return postJSONBodyWithHeaders(cfg, httpClient, cfg.LeasesURL, path, authorizationHeader(cfg.ComponentToken), *idempotencyKey, headers, req, stdout, stderr)
}

func artifactsCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usage(stderr, "usage: pacp-admin [flags] artifacts <list|artifact|upload|create-upload|put-content|complete-upload|register-local> [id]")
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
	case "create-upload":
		return artifactsCreateUploadCommand(cfg, httpClient, args[1:], stdout, stderr)
	case "put-content":
		return artifactsPutContentCommand(cfg, httpClient, args[1:], stdout, stderr)
	case "complete-upload":
		return artifactsCompleteUploadCommand(cfg, httpClient, args[1:], stdout, stderr)
	case "register-local":
		return artifactsRegisterLocalCommand(cfg, httpClient, args[1:], stdout, stderr)
	default:
		return usage(stderr, "usage: pacp-admin [flags] artifacts <list|artifact|upload|create-upload|put-content|complete-upload|register-local> [id]")
	}
}

func artifactsCreateUploadCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("artifacts create-upload", flag.ContinueOnError)
	flags.SetOutput(stderr)
	name := flags.String("name", "", "artifact name")
	mediaType := flags.String("media-type", "", "artifact media type")
	producerRef := flags.String("producer-ref", "", "producer reference, usually a job id")
	ownerSubjectID := flags.String("owner-subject-id", "", "artifact owner subject id")
	expectedSize := flags.Int64("expected-size", -1, "expected artifact size in bytes")
	expectedChecksum := flags.String("expected-checksum", "", "expected sha256:<hex> checksum")
	metadata := flags.String("metadata", "", "JSON object metadata")
	idempotencyKey := flags.String("idempotency-key", "", "idempotency key for upload creation")
	remaining, err := parseSubcommandFlags(flags, args)
	if err != nil {
		return 2
	}
	if len(remaining) != 0 {
		return usage(stderr, "usage: pacp-admin [flags] artifacts create-upload -name <name> -media-type <type> -owner-subject-id <subject> -idempotency-key <key> [-producer-ref ref] [-expected-size bytes] [-expected-checksum sha256:hex] [-metadata JSON]")
	}
	if *name == "" {
		return usage(stderr, "name is required for artifacts create-upload")
	}
	if *mediaType == "" {
		return usage(stderr, "media-type is required for artifacts create-upload")
	}
	if *ownerSubjectID == "" {
		return usage(stderr, "owner-subject-id is required for artifacts create-upload")
	}
	if *idempotencyKey == "" {
		return usage(stderr, "idempotency-key is required for artifacts create-upload")
	}
	if *expectedSize < -1 {
		return usage(stderr, "expected-size must be zero or greater")
	}
	metadataObject, err := optionalJSONObject(*metadata)
	if err != nil {
		fmt.Fprintf(stderr, "metadata: %v\n", err)
		return 2
	}
	req := contracts.CreateArtifactUploadRequest{
		Name:             *name,
		MediaType:        *mediaType,
		ProducerRef:      *producerRef,
		OwnerSubjectID:   *ownerSubjectID,
		ExpectedChecksum: *expectedChecksum,
		Metadata:         metadataObject,
	}
	if *expectedSize >= 0 {
		req.ExpectedSize = expectedSize
	}
	return postJSONBody(cfg, httpClient, cfg.ArtifactsURL, "/v1/artifact-uploads", authorizationHeader(cfg.ComponentToken), *idempotencyKey, req, stdout, stderr)
}

func artifactsPutContentCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("artifacts put-content", flag.ContinueOnError)
	flags.SetOutput(stderr)
	filePath := flags.String("file", "", "file containing artifact bytes")
	mediaType := flags.String("media-type", "", "content media type")
	digest := flags.String("digest", "", "optional sha256:<hex> digest; computed from file when omitted")
	idempotencyKey := flags.String("idempotency-key", "", "idempotency key for content upload")
	remaining, err := parseSubcommandFlags(flags, args)
	if err != nil {
		return 2
	}
	if len(remaining) != 1 {
		return usage(stderr, "usage: pacp-admin [flags] artifacts put-content <upload-id> -file <path> -media-type <type> -idempotency-key <key> [-digest sha256:hex]")
	}
	if *filePath == "" {
		return usage(stderr, "file is required for artifacts put-content")
	}
	if *mediaType == "" {
		return usage(stderr, "media-type is required for artifacts put-content")
	}
	if *idempotencyKey == "" {
		return usage(stderr, "idempotency-key is required for artifacts put-content")
	}
	path := "/v1/artifact-uploads/" + url.PathEscape(remaining[0]) + "/content"
	return putFile(cfg, httpClient, cfg.ArtifactsURL, path, authorizationHeader(cfg.ComponentToken), *idempotencyKey, *filePath, *mediaType, *digest, stdout, stderr)
}

func artifactsCompleteUploadCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("artifacts complete-upload", flag.ContinueOnError)
	flags.SetOutput(stderr)
	checksum := flags.String("checksum", "", "sha256:<hex> artifact checksum")
	size := flags.Int64("size", -1, "artifact size in bytes")
	filePath := flags.String("file", "", "optional file used to derive checksum and size when omitted")
	idempotencyKey := flags.String("idempotency-key", "", "idempotency key for upload completion")
	remaining, err := parseSubcommandFlags(flags, args)
	if err != nil {
		return 2
	}
	if len(remaining) != 1 {
		return usage(stderr, "usage: pacp-admin [flags] artifacts complete-upload <upload-id> -idempotency-key <key> [-file path] [-checksum sha256:hex -size bytes]")
	}
	checksumValue := *checksum
	sizeValue := *size
	if *filePath != "" && (checksumValue == "" || sizeValue < 0) {
		derivedChecksum, derivedSize, err := fileChecksumAndSize(*filePath)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if checksumValue == "" {
			checksumValue = derivedChecksum
		}
		if sizeValue < 0 {
			sizeValue = derivedSize
		}
	}
	if checksumValue == "" {
		return usage(stderr, "checksum is required for artifacts complete-upload")
	}
	if sizeValue < 0 {
		return usage(stderr, "size is required for artifacts complete-upload")
	}
	if *idempotencyKey == "" {
		return usage(stderr, "idempotency-key is required for artifacts complete-upload")
	}
	req := contracts.CompleteArtifactUploadRequest{Checksum: checksumValue, Size: sizeValue}
	path := "/v1/artifact-uploads/" + url.PathEscape(remaining[0]) + "/complete"
	return postJSONBody(cfg, httpClient, cfg.ArtifactsURL, path, authorizationHeader(cfg.ComponentToken), *idempotencyKey, req, stdout, stderr)
}

func artifactsRegisterLocalCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("artifacts register-local", flag.ContinueOnError)
	flags.SetOutput(stderr)
	pathFlag := flags.String("path", "", "artifact path under the artifact store root")
	name := flags.String("name", "", "artifact name")
	mediaType := flags.String("media-type", "", "artifact media type")
	producerRef := flags.String("producer-ref", "", "producer reference, usually a job id")
	ownerSubjectID := flags.String("owner-subject-id", "", "artifact owner subject id")
	metadata := flags.String("metadata", "", "JSON object metadata")
	remaining, err := parseSubcommandFlags(flags, args)
	if err != nil {
		return 2
	}
	if len(remaining) != 0 {
		return usage(stderr, "usage: pacp-admin [flags] artifacts register-local -path <store-relative-path> -media-type <type> -owner-subject-id <subject> [-name name] [-producer-ref ref] [-metadata JSON]")
	}
	if *pathFlag == "" {
		return usage(stderr, "path is required for artifacts register-local")
	}
	if *mediaType == "" {
		return usage(stderr, "media-type is required for artifacts register-local")
	}
	if *ownerSubjectID == "" {
		return usage(stderr, "owner-subject-id is required for artifacts register-local")
	}
	metadataObject, err := optionalJSONObject(*metadata)
	if err != nil {
		fmt.Fprintf(stderr, "metadata: %v\n", err)
		return 2
	}
	req := contracts.RegisterLocalArtifactRequest{
		Path:           *pathFlag,
		Name:           *name,
		MediaType:      *mediaType,
		ProducerRef:    *producerRef,
		OwnerSubjectID: *ownerSubjectID,
		Metadata:       metadataObject,
	}
	return postJSONBody(cfg, httpClient, cfg.ArtifactsURL, "/v1/artifacts/register-local", authorizationHeader(cfg.ComponentToken), "", req, stdout, stderr)
}

func policyCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usage(stderr, "usage: pacp-admin [flags] policy <create-key|revoke-key|verify|check|create-rule|create-secret|redact>")
	}
	switch args[0] {
	case "create-key":
		return policyCreateKeyCommand(cfg, httpClient, args[1:], stdout, stderr)
	case "revoke-key":
		if len(args) != 2 {
			return usage(stderr, "usage: pacp-admin [flags] policy revoke-key <key-id>")
		}
		return postJSON(cfg, httpClient, cfg.PolicyURL, "/v1/api-keys/"+url.PathEscape(args[1])+"/revoke", authorizationHeader(cfg.ComponentToken), "", stdout, stderr)
	case "verify":
		return policyVerifyCommand(cfg, httpClient, args[1:], stdout, stderr)
	case "check":
		return policyCheckCommand(cfg, httpClient, args[1:], stdout, stderr)
	case "create-rule":
		return policyCreateRuleCommand(cfg, httpClient, args[1:], stdout, stderr)
	case "create-secret":
		return policyCreateSecretCommand(cfg, httpClient, args[1:], stdout, stderr)
	case "redact":
		return policyRedactCommand(cfg, httpClient, args[1:], stdout, stderr)
	default:
		return usage(stderr, "usage: pacp-admin [flags] policy <create-key|revoke-key|verify|check|create-rule|create-secret|redact>")
	}
}

func policyCreateKeyCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("policy create-key", flag.ContinueOnError)
	flags.SetOutput(stderr)
	subjectID := flags.String("subject-id", "", "subject id")
	scopes := flags.String("scopes", "", "comma-separated scopes")
	token := flags.String("token", "", "optional explicit token")
	remaining, err := parseSubcommandFlags(flags, args)
	if err != nil {
		return 2
	}
	if len(remaining) != 0 {
		return usage(stderr, "usage: pacp-admin [flags] policy create-key -subject-id <id> -scopes <scope[,scope]> [-token token]")
	}
	if *subjectID == "" {
		return usage(stderr, "subject-id is required for policy create-key")
	}
	scopeList := splitCSV(*scopes)
	if len(scopeList) == 0 {
		return usage(stderr, "scopes are required for policy create-key")
	}
	req := contracts.CreateAPIKeyRequest{SubjectID: *subjectID, Scopes: scopeList, Token: *token}
	return postJSONBody(cfg, httpClient, cfg.PolicyURL, "/v1/api-keys", authorizationHeader(cfg.ComponentToken), "", req, stdout, stderr)
}

func policyVerifyCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("policy verify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	credential := flags.String("credential", "", "credential to verify, usually 'Bearer <token>'")
	remaining, err := parseSubcommandFlags(flags, args)
	if err != nil {
		return 2
	}
	if len(remaining) != 0 {
		return usage(stderr, "usage: pacp-admin [flags] policy verify -credential 'Bearer <token>'")
	}
	if *credential == "" {
		return usage(stderr, "credential is required for policy verify")
	}
	req := contracts.VerifyCredentialRequest{Credential: *credential}
	return postJSONBody(cfg, httpClient, cfg.PolicyURL, "/v1/auth/verify", authorizationHeader(cfg.ComponentToken), "", req, stdout, stderr)
}

func policyCheckCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("policy check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	subjectID := flags.String("subject-id", "", "subject id")
	action := flags.String("action", "", "policy action")
	resource := flags.String("resource", "", "policy resource")
	contextRaw := flags.String("context", "", "JSON object context")
	remaining, err := parseSubcommandFlags(flags, args)
	if err != nil {
		return 2
	}
	if len(remaining) != 0 {
		return usage(stderr, "usage: pacp-admin [flags] policy check -subject-id <id> -action <action> -resource <resource> [-context JSON]")
	}
	if *subjectID == "" {
		return usage(stderr, "subject-id is required for policy check")
	}
	if *action == "" {
		return usage(stderr, "action is required for policy check")
	}
	if *resource == "" {
		return usage(stderr, "resource is required for policy check")
	}
	contextObject, err := optionalJSONObject(*contextRaw)
	if err != nil {
		fmt.Fprintf(stderr, "context: %v\n", err)
		return 2
	}
	req := contracts.PolicyCheckRequest{SubjectID: *subjectID, Action: *action, Resource: *resource, Context: contextObject}
	return postJSONBody(cfg, httpClient, cfg.PolicyURL, "/v1/policy/check", authorizationHeader(cfg.ComponentToken), "", req, stdout, stderr)
}

func policyCreateRuleCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("policy create-rule", flag.ContinueOnError)
	flags.SetOutput(stderr)
	subjectID := flags.String("subject-id", "", "subject id")
	scope := flags.String("scope", "", "scope")
	action := flags.String("action", "", "policy action")
	resource := flags.String("resource", "", "policy resource")
	effect := flags.String("effect", "", "allow or deny")
	reason := flags.String("reason", "", "human reason")
	remaining, err := parseSubcommandFlags(flags, args)
	if err != nil {
		return 2
	}
	if len(remaining) != 0 {
		return usage(stderr, "usage: pacp-admin [flags] policy create-rule [-subject-id id|-scope scope] -action <action> -resource <resource> -effect <allow|deny> [-reason text]")
	}
	if *subjectID == "" && *scope == "" {
		return usage(stderr, "subject-id or scope is required for policy create-rule")
	}
	if *action == "" {
		return usage(stderr, "action is required for policy create-rule")
	}
	if *resource == "" {
		return usage(stderr, "resource is required for policy create-rule")
	}
	if *effect == "" {
		return usage(stderr, "effect is required for policy create-rule")
	}
	req := contracts.CreatePolicyRuleRequest{SubjectID: *subjectID, Scope: *scope, Action: *action, Resource: *resource, Effect: *effect, Reason: *reason}
	return postJSONBody(cfg, httpClient, cfg.PolicyURL, "/v1/policy/rules", authorizationHeader(cfg.ComponentToken), "", req, stdout, stderr)
}

func policyCreateSecretCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("policy create-secret", flag.ContinueOnError)
	flags.SetOutput(stderr)
	name := flags.String("name", "", "secret name")
	value := flags.String("value", "", "secret value")
	valueEnv := flags.String("value-env", "", "environment variable containing the secret value")
	remaining, err := parseSubcommandFlags(flags, args)
	if err != nil {
		return 2
	}
	if len(remaining) != 0 {
		return usage(stderr, "usage: pacp-admin [flags] policy create-secret -name <name> (-value <secret>|-value-env ENV)")
	}
	if *name == "" {
		return usage(stderr, "name is required for policy create-secret")
	}
	secretValue := *value
	if *valueEnv != "" {
		if secretValue != "" {
			return usage(stderr, "use only one of -value or -value-env for policy create-secret")
		}
		secretValue = os.Getenv(*valueEnv)
		if secretValue == "" {
			return usage(stderr, "value-env did not resolve to a non-empty value for policy create-secret")
		}
	}
	if secretValue == "" {
		return usage(stderr, "value or value-env is required for policy create-secret")
	}
	req := contracts.CreateSecretRequest{Name: *name, Value: secretValue}
	return postJSONBody(cfg, httpClient, cfg.PolicyURL, "/v1/secrets", authorizationHeader(cfg.ComponentToken), "", req, stdout, stderr)
}

func policyRedactCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("policy redact", flag.ContinueOnError)
	flags.SetOutput(stderr)
	text := flags.String("text", "", "text to redact")
	remaining, err := parseSubcommandFlags(flags, args)
	if err != nil {
		return 2
	}
	if len(remaining) != 0 {
		return usage(stderr, "usage: pacp-admin [flags] policy redact -text <text>")
	}
	if *text == "" {
		return usage(stderr, "text is required for policy redact")
	}
	req := contracts.RedactRequest{Text: *text}
	return postJSONBody(cfg, httpClient, cfg.PolicyURL, "/v1/redact", authorizationHeader(cfg.ComponentToken), "", req, stdout, stderr)
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
		return nodeStopCommand(cfg, httpClient, args[1:], stdout, stderr)
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

func nodeStopCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("node stop", flag.ContinueOnError)
	flags.SetOutput(stderr)
	idempotencyKey := flags.String("idempotency-key", "", "idempotency key for this node stop")
	remaining, err := parseSubcommandFlags(flags, args)
	if err != nil {
		return 2
	}
	if len(remaining) != 1 {
		return usage(stderr, "usage: pacp-admin [flags] node stop <service-id> -idempotency-key <key>")
	}
	if *idempotencyKey == "" {
		return usage(stderr, "idempotency-key is required for node stop")
	}
	path := "/v1/node/services/" + url.PathEscape(remaining[0]) + "/stop"
	return postJSON(cfg, httpClient, cfg.NodeURL, path, authorizationHeader(cfg.NodeToken), *idempotencyKey, stdout, stderr)
}

func diagnoseCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usage(stderr, "usage: pacp-admin [flags] diagnose <job> [id]")
	}
	switch args[0] {
	case "job":
		return diagnoseJobCommand(cfg, httpClient, args[1:], stdout, stderr)
	default:
		return usage(stderr, "usage: pacp-admin [flags] diagnose <job> [id]")
	}
}

func diagnoseJobCommand(cfg adminConfig, httpClient *http.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return usage(stderr, "usage: pacp-admin [flags] diagnose job <job-id>")
	}
	jobID := args[0]
	var jobEnvelope struct {
		OK    bool                  `json:"ok"`
		Data  contracts.Job         `json:"data"`
		Error contracts.ErrorObject `json:"error"`
	}
	status, err := getJSONDecode(cfg, httpClient, cfg.JobsURL, "/v1/jobs/"+url.PathEscape(jobID), authorizationHeader(cfg.ComponentToken), &jobEnvelope)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if status < 200 || status >= 300 || !jobEnvelope.OK {
		message := jobEnvelope.Error.Message
		if message == "" {
			message = fmt.Sprintf("jobs service returned HTTP %d", status)
		}
		fmt.Fprintln(stderr, message)
		return 1
	}

	logs, logFinding := fetchJobLogs(cfg, httpClient, jobID)
	data := buildJobDiagnostics(jobEnvelope.Data, logs)
	if logFinding != nil {
		data.Findings = append(data.Findings, *logFinding)
		addSuggestedAction(&data.SuggestedActions, "retry log fetch or inspect job service health")
	}
	return writeCommandJSON(stdout, stderr, map[string]any{
		"ok":    true,
		"data":  data,
		"links": map[string]any{"job": "/v1/jobs/" + jobID, "logs": "/v1/jobs/" + jobID + "/logs"},
		"meta":  map[string]string{"schema_version": "v1", "admin_command": "diagnose job"},
	})
}

func fetchJobLogs(cfg adminConfig, httpClient *http.Client, jobID string) ([]contracts.JobLogEntry, *diagnosticFinding) {
	var logsEnvelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Items []contracts.JobLogEntry `json:"items"`
		} `json:"data"`
		Error contracts.ErrorObject `json:"error"`
	}
	status, err := getJSONDecode(cfg, httpClient, cfg.JobsURL, "/v1/jobs/"+url.PathEscape(jobID)+"/logs?limit=20", authorizationHeader(cfg.ComponentToken), &logsEnvelope)
	if err != nil {
		return nil, &diagnosticFinding{Severity: "warning", Code: "logs_unavailable", Message: err.Error()}
	}
	if status < 200 || status >= 300 || !logsEnvelope.OK {
		message := logsEnvelope.Error.Message
		if message == "" {
			message = fmt.Sprintf("jobs service returned HTTP %d for logs", status)
		}
		return nil, &diagnosticFinding{Severity: "warning", Code: "logs_unavailable", Message: message}
	}
	return logsEnvelope.Data.Items, nil
}

func buildJobDiagnostics(job contracts.Job, logs []contracts.JobLogEntry) jobDiagnosticData {
	data := jobDiagnosticData{
		Job:  job,
		Logs: logs,
	}
	plan := executionPlanMap(job.Metadata)
	resourceSelector, _ := plan["resource_selector"].(string)
	route, _ := plan["route"].(map[string]any)
	nodeManaged, _ := route["node_managed"].(bool)
	nodeID, _ := route["node_id"].(string)
	serviceID, _ := route["service_id"].(string)

	switch job.State {
	case contracts.JobQueued:
		data.Findings = append(data.Findings, diagnosticFinding{Severity: "info", Code: "job_queued", Message: "job is waiting for a runner claim"})
		addSuggestedAction(&data.SuggestedActions, "verify runner health and worker credentials")
		if resourceSelector != "" {
			addSuggestedAction(&data.SuggestedActions, "inspect lease resources and queue for selector "+resourceSelector)
		}
	case contracts.JobClaimed:
		data.Findings = append(data.Findings, claimFinding(job))
		addSuggestedAction(&data.SuggestedActions, "wait for claim heartbeat or expiry")
		addSuggestedAction(&data.SuggestedActions, "verify the claiming runner is still running")
	case contracts.JobRunning:
		data.Findings = append(data.Findings, diagnosticFinding{Severity: "info", Code: "job_running", Message: "job is currently running"})
		addSuggestedAction(&data.SuggestedActions, "wait for runner progress")
		if nodeManaged {
			addSuggestedAction(&data.SuggestedActions, "check node health and service status")
		}
	case contracts.JobSucceeded:
		data.Findings = append(data.Findings, diagnosticFinding{Severity: "info", Code: "job_succeeded", Message: "job completed successfully"})
	case contracts.JobFailed:
		message := "job failed"
		if job.TerminalError != nil && job.TerminalError.Message != "" {
			message = job.TerminalError.Code + ": " + job.TerminalError.Message
		}
		data.Findings = append(data.Findings, diagnosticFinding{Severity: "error", Code: "job_failed", Message: message})
		addSuggestedAction(&data.SuggestedActions, "inspect recent logs and fix the reported dependency before retrying")
	case contracts.JobCanceled:
		data.Findings = append(data.Findings, diagnosticFinding{Severity: "info", Code: "job_canceled", Message: "job was canceled"})
	case contracts.JobExpired:
		data.Findings = append(data.Findings, diagnosticFinding{Severity: "warning", Code: "job_expired", Message: "job expired before completion"})
		addSuggestedAction(&data.SuggestedActions, "retry after verifying runner and queue health")
	default:
		data.Findings = append(data.Findings, diagnosticFinding{Severity: "warning", Code: "unknown_job_state", Message: "job state is not recognized: " + string(job.State)})
	}

	if resourceSelector != "" {
		data.Findings = append(data.Findings, diagnosticFinding{Severity: "info", Code: "resource_selector", Message: "job requests resource selector " + resourceSelector})
	}
	if nodeManaged {
		message := "job route is node-managed"
		if nodeID != "" {
			message += " on node " + nodeID
		}
		if serviceID != "" {
			message += " for service " + serviceID
		}
		data.Findings = append(data.Findings, diagnosticFinding{Severity: "info", Code: "node_managed_route", Message: message})
	}
	if len(job.ArtifactRefs) > 0 {
		data.Findings = append(data.Findings, diagnosticFinding{Severity: "info", Code: "artifacts_registered", Message: fmt.Sprintf("job references %d artifact(s)", len(job.ArtifactRefs))})
	}
	if len(logs) == 0 && job.State != contracts.JobQueued {
		data.Findings = append(data.Findings, diagnosticFinding{Severity: "warning", Code: "no_recent_logs", Message: "job has no recent log entries"})
		addSuggestedAction(&data.SuggestedActions, "verify runner log appends can reach the job service")
	}
	return data
}

func claimFinding(job contracts.Job) diagnosticFinding {
	if job.Claim == nil {
		return diagnosticFinding{Severity: "warning", Code: "claimed_without_claim_details", Message: "job is claimed but claim details are missing"}
	}
	expiresAt, err := time.Parse(time.RFC3339, job.Claim.ExpiresAt)
	if err != nil {
		return diagnosticFinding{Severity: "warning", Code: "invalid_claim_expiry", Message: "claim expiry is invalid: " + job.Claim.ExpiresAt}
	}
	if time.Now().UTC().After(expiresAt) {
		return diagnosticFinding{Severity: "warning", Code: "claim_expired", Message: "claim by " + job.Claim.WorkerID + " expired at " + job.Claim.ExpiresAt}
	}
	return diagnosticFinding{Severity: "info", Code: "claim_active", Message: "job is claimed by " + job.Claim.WorkerID + " until " + job.Claim.ExpiresAt}
}

func executionPlanMap(metadata map[string]any) map[string]any {
	if metadata == nil {
		return map[string]any{}
	}
	plan, _ := metadata["execution_plan"].(map[string]any)
	if plan == nil {
		return map[string]any{}
	}
	return plan
}

func addSuggestedAction(actions *[]string, action string) {
	if action == "" {
		return
	}
	for _, existing := range *actions {
		if existing == action {
			return
		}
	}
	*actions = append(*actions, action)
}

func checkHealth(cfg adminConfig, httpClient *http.Client, opts healthOptions) healthReport {
	ctx := context.Background()
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}
	componentCredential := authorizationHeader(cfg.ComponentToken)
	targets := []serviceTarget{
		{Name: "catalog", Kind: "component", URL: cfg.CatalogURL, HealthPath: "/v1/catalog/health", Credential: componentCredential, Required: true},
		{Name: "jobs", Kind: "component", URL: cfg.JobsURL, HealthPath: "/v1/jobs/health", Credential: componentCredential, Required: true},
		{Name: "leases", Kind: "component", URL: cfg.LeasesURL, HealthPath: "/v1/leases/health", Credential: componentCredential, Required: true},
		{Name: "artifacts", Kind: "component", URL: cfg.ArtifactsURL, HealthPath: "/v1/artifacts/health", Credential: componentCredential, Required: true},
		{Name: "policy", Kind: "component", URL: cfg.PolicyURL, HealthPath: "/v1/policy/health", Credential: componentCredential, Required: true},
		{Name: "gateway", Kind: "component", URL: cfg.GatewayURL, HealthPath: "/v1/gateway/health", Required: true},
	}
	targets = append(targets, nodeHealthTargets(cfg)...)
	if target, ok := runnerHealthTarget(cfg); ok {
		targets = append(targets, target)
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
		addHealthItem(&report, item, target.Required || target.URL != "")
	}
	if opts.Providers {
		providerTargets, discoveryItem := discoverProviderHealthTargets(ctx, httpClient, cfg)
		if discoveryItem != nil {
			addHealthItem(&report, *discoveryItem, discoveryItem.Status != "skipped")
		}
		for _, target := range providerTargets {
			item := checkTarget(ctx, httpClient, target)
			addHealthItem(&report, item, true)
		}
	}
	return report
}

func collectMetrics(cfg adminConfig, httpClient *http.Client) metricsReport {
	ctx := context.Background()
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}
	targets := metricsTargets(cfg)
	report := metricsReport{
		OK:    true,
		Links: map[string]any{},
		Meta: map[string]string{
			"collected_at":    time.Now().UTC().Format(time.RFC3339),
			"schema_version":  "v1",
			"admin_command":   "metrics",
			"optional_target": "node",
		},
	}
	for _, target := range targets {
		item := checkMetricsTarget(ctx, httpClient, target)
		addMetricsItem(&report, item, target.Required || target.URL != "")
	}
	return report
}

func metricsTargets(cfg adminConfig) []serviceTarget {
	componentCredential := authorizationHeader(cfg.ComponentToken)
	targets := []serviceTarget{
		{Name: "catalog", Kind: "component", URL: cfg.CatalogURL, MetricsPath: "/v1/catalog/metrics", Credential: componentCredential, Required: true},
		{Name: "jobs", Kind: "component", URL: cfg.JobsURL, MetricsPath: "/v1/jobs/metrics", Credential: componentCredential, Required: true},
		{Name: "leases", Kind: "component", URL: cfg.LeasesURL, MetricsPath: "/v1/leases/metrics", Credential: componentCredential, Required: true},
		{Name: "artifacts", Kind: "component", URL: cfg.ArtifactsURL, MetricsPath: "/v1/artifacts/metrics", Credential: componentCredential, Required: true},
		{Name: "policy", Kind: "component", URL: cfg.PolicyURL, MetricsPath: "/v1/policy/metrics", Credential: componentCredential, Required: true},
		{Name: "gateway", Kind: "component", URL: cfg.GatewayURL, MetricsPath: "/v1/gateway/metrics", Required: true},
	}
	targets = append(targets, nodeMetricsTargets(cfg)...)
	if target, ok := runnerMetricsTarget(cfg); ok {
		targets = append(targets, target)
	}
	return targets
}

func runnerHealthTarget(cfg adminConfig) (serviceTarget, bool) {
	if strings.TrimSpace(cfg.RunnerURL) == "" {
		return serviceTarget{}, false
	}
	return serviceTarget{
		Name:       "runner",
		Kind:       "runner",
		URL:        cfg.RunnerURL,
		HealthPath: "/v1/runner/health",
		Credential: authorizationHeader(cfg.RunnerToken),
	}, true
}

func runnerMetricsTarget(cfg adminConfig) (serviceTarget, bool) {
	if strings.TrimSpace(cfg.RunnerURL) == "" {
		return serviceTarget{}, false
	}
	return serviceTarget{
		Name:        "runner",
		Kind:        "runner",
		URL:         cfg.RunnerURL,
		MetricsPath: "/v1/runner/metrics",
		Credential:  authorizationHeader(cfg.RunnerToken),
	}, true
}

func nodeMetricsTargets(cfg adminConfig) []serviceTarget {
	credential := authorizationHeader(cfg.NodeToken)
	targets := []serviceTarget{}
	seen := map[string]bool{}
	add := func(name, nodeID, rawURL, configError string, required bool) {
		key := name + "\x00" + rawURL
		if seen[key] {
			return
		}
		seen[key] = true
		targets = append(targets, serviceTarget{
			Name:        name,
			Kind:        "node",
			URL:         rawURL,
			MetricsPath: "/v1/node/metrics",
			Credential:  credential,
			Required:    required,
			NodeID:      nodeID,
			ConfigError: configError,
		})
	}
	if strings.TrimSpace(cfg.NodeURL) != "" {
		add("node", "", cfg.NodeURL, "", false)
	}
	for _, entry := range splitCSV(cfg.NodeURLs) {
		nodeID, rawURL, ok := strings.Cut(entry, "=")
		nodeID = strings.TrimSpace(nodeID)
		rawURL = strings.TrimSpace(rawURL)
		if !ok || nodeID == "" || rawURL == "" {
			add("node:"+entry, "", "", "node target must be formatted as node_id=url", true)
			continue
		}
		add("node:"+nodeID, nodeID, rawURL, "", true)
	}
	if len(targets) == 0 {
		add("node", "", "", "", false)
	}
	return targets
}

func checkMetricsTarget(ctx context.Context, httpClient *http.Client, target serviceTarget) metricsItem {
	baseURL := strings.TrimRight(strings.TrimSpace(target.URL), "/")
	item := metricsItem{
		Name:   target.Name,
		Kind:   target.Kind,
		NodeID: target.NodeID,
		URL:    baseURL,
	}
	if target.ConfigError != "" {
		item.Error = target.ConfigError
		return item
	}
	if baseURL == "" {
		if target.Required {
			item.Error = "service URL is required"
		}
		return item
	}
	item.MetricsURL = joinURLPath(baseURL, target.MetricsPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, item.MetricsURL, nil)
	if err != nil {
		item.Error = err.Error()
		return item
	}
	if target.Credential != "" {
		req.Header.Set("Authorization", target.Credential)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		item.Error = err.Error()
		return item
	}
	defer resp.Body.Close()
	item.HTTPStatus = resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		item.Error = resp.Status
		return item
	}
	var envelope struct {
		OK    bool                       `json:"ok"`
		Data  contracts.ComponentMetrics `json:"data"`
		Error contracts.ErrorObject      `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		item.Error = "invalid metrics response: " + err.Error()
		return item
	}
	if !envelope.OK {
		message := envelope.Error.Message
		if message == "" {
			message = "metrics response was not ok"
		}
		item.Error = message
		return item
	}
	item.Component = envelope.Data.Component
	item.Samples = envelope.Data.Samples
	return item
}

func addMetricsItem(report *metricsReport, item metricsItem, affectsOK bool) {
	report.Data.Items = append(report.Data.Items, item)
	switch {
	case item.URL == "" && item.Error == "":
		report.Data.Summary.Skipped++
	case item.Error != "":
		report.Data.Summary.Unavailable++
		if affectsOK {
			report.OK = false
		}
	default:
		report.Data.Summary.Available++
		report.Data.Summary.Samples += len(item.Samples)
	}
}

func buildAlertsReport(health healthReport, metrics metricsReport, opts alertOptions) alertsReport {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	report := alertsReport{
		OK: true,
		Data: alertsReportData{
			Health:  health.Data,
			Metrics: metrics.Data,
		},
		Links: map[string]any{},
		Meta: map[string]string{
			"checked_at":     time.Now().UTC().Format(time.RFC3339),
			"schema_version": "v1",
			"admin_command":  "alerts",
		},
	}
	for _, item := range health.Data.Items {
		if item.Status == "healthy" || item.Status == "skipped" {
			continue
		}
		message := fmt.Sprintf("%s health is %s", item.Name, item.Status)
		if item.Error != "" {
			message += ": " + item.Error
		}
		addAlertFinding(&report, diagnosticFinding{Severity: "error", Code: "target_unhealthy", Message: message})
	}
	for _, item := range metrics.Data.Items {
		if item.Error != "" && item.URL != "" {
			addAlertFinding(&report, diagnosticFinding{Severity: "warning", Code: "metrics_unavailable", Message: item.Name + " metrics unavailable: " + item.Error})
			continue
		}
		addMetricFindings(&report, item, opts)
	}
	if len(report.Data.Findings) == 0 {
		addAlertFinding(&report, diagnosticFinding{Severity: "info", Code: "no_alerts", Message: "no alert conditions were detected"})
	}
	return report
}

func addMetricFindings(report *alertsReport, item metricsItem, opts alertOptions) {
	runnerActiveJobs := 0.0
	runnerLastHeartbeatUnix := 0.0
	runnerHeartbeatSeen := false
	for _, sample := range item.Samples {
		switch sample.Name {
		case "runner_active_jobs":
			runnerActiveJobs = sample.Value
		case "runner_last_successful_heartbeat_unix_seconds":
			runnerLastHeartbeatUnix = sample.Value
			runnerHeartbeatSeen = true
		case "runner_dependency_reachable":
			if sample.Value == 0 {
				dependency := sample.Labels["dependency"]
				if dependency == "" {
					dependency = "unknown"
				}
				severity := "warning"
				if sample.Labels["required"] == "true" {
					severity = "error"
				}
				addAlertFinding(report, diagnosticFinding{Severity: severity, Code: "runner_dependency_unreachable", Message: fmt.Sprintf("%s dependency %s is unreachable", item.Name, dependency)})
			}
		case "jobs_by_state":
			state := sample.Labels["state"]
			switch {
			case state == string(contracts.JobFailed) && sample.Value > 0:
				addAlertFinding(report, diagnosticFinding{Severity: "error", Code: "jobs_failed", Message: fmt.Sprintf("%s has %.0f failed job(s)", item.Name, sample.Value)})
			case state == string(contracts.JobExpired) && sample.Value > 0:
				addAlertFinding(report, diagnosticFinding{Severity: "warning", Code: "jobs_expired", Message: fmt.Sprintf("%s has %.0f expired job(s)", item.Name, sample.Value)})
			case state == string(contracts.JobQueued) && sample.Value > 0:
				addAlertFinding(report, diagnosticFinding{Severity: "warning", Code: "jobs_queued", Message: fmt.Sprintf("%s has %.0f queued job(s)", item.Name, sample.Value)})
			case state == string(contracts.JobRunning) && sample.Value > 0:
				addAlertFinding(report, diagnosticFinding{Severity: "info", Code: "jobs_running", Message: fmt.Sprintf("%s has %.0f running job(s)", item.Name, sample.Value)})
			}
		case "jobs_active_claims":
			if sample.Value > 0 {
				addAlertFinding(report, diagnosticFinding{Severity: "info", Code: "jobs_claimed", Message: fmt.Sprintf("%s has %.0f active job claim(s)", item.Name, sample.Value)})
			}
		case "jobs_expired_claims":
			if sample.Value > 0 {
				addAlertFinding(report, diagnosticFinding{Severity: "warning", Code: "jobs_expired_claims", Message: fmt.Sprintf("%s has %.0f expired job claim(s)", item.Name, sample.Value)})
			}
		case "lease_queue_depth":
			if sample.Value > float64(opts.QueueDepthThreshold) {
				selector := sample.Labels["selector"]
				if selector == "" {
					selector = "unknown"
				}
				addAlertFinding(report, diagnosticFinding{Severity: "warning", Code: "lease_queue_depth", Message: fmt.Sprintf("%s selector %s queue depth is %.0f", item.Name, selector, sample.Value)})
			}
		case "leases_active_total":
			if sample.Value > 0 {
				addAlertFinding(report, diagnosticFinding{Severity: "info", Code: "leases_active", Message: fmt.Sprintf("%s has %.0f active lease(s)", item.Name, sample.Value)})
			}
		case "artifact_uploads_by_state":
			state := sample.Labels["state"]
			if (state == string(contracts.ArtifactUploadAborted) || state == string(contracts.ArtifactUploadExpired)) && sample.Value > 0 {
				addAlertFinding(report, diagnosticFinding{Severity: "warning", Code: "artifact_uploads_not_completed", Message: fmt.Sprintf("%s has %.0f %s artifact upload(s)", item.Name, sample.Value, state)})
			}
		case "policy_decisions_total":
			if sample.Labels["decision"] == "deny" && sample.Value > 0 {
				action := sample.Labels["action"]
				if action == "" {
					action = "unknown"
				}
				addAlertFinding(report, diagnosticFinding{Severity: "warning", Code: "policy_denies", Message: fmt.Sprintf("%s denied %.0f policy decision(s) for %s", item.Name, sample.Value, action)})
			}
		case "node_services_by_status":
			status := sample.Labels["status"]
			if status == "failed" && sample.Value > 0 {
				addAlertFinding(report, diagnosticFinding{Severity: "error", Code: "node_services_failed", Message: fmt.Sprintf("%s has %.0f failed service(s)", item.Name, sample.Value)})
			}
		case "gateway_downstream_configured":
			if sample.Value == 0 {
				downstream := sample.Labels["downstream"]
				if downstream == "" {
					downstream = "unknown"
				}
				addAlertFinding(report, diagnosticFinding{Severity: "error", Code: "gateway_downstream_missing", Message: fmt.Sprintf("%s downstream %s is not configured", item.Name, downstream)})
			}
		}
	}
	if isRunnerMetricsItem(item) && opts.RunnerHeartbeatStaleAfter > 0 && runnerActiveJobs > 0 {
		if !runnerHeartbeatSeen || runnerLastHeartbeatUnix <= 0 {
			addAlertFinding(report, diagnosticFinding{Severity: "warning", Code: "runner_heartbeat_missing", Message: fmt.Sprintf("%s has active work but no successful heartbeat metric", item.Name)})
			return
		}
		heartbeatAt := time.Unix(int64(runnerLastHeartbeatUnix), 0)
		age := opts.Now.Sub(heartbeatAt)
		if age > opts.RunnerHeartbeatStaleAfter {
			addAlertFinding(report, diagnosticFinding{Severity: "warning", Code: "runner_heartbeat_stale", Message: fmt.Sprintf("%s heartbeat is stale: age %s exceeds %s", item.Name, age.Truncate(time.Second), opts.RunnerHeartbeatStaleAfter)})
		}
	}
}

func isRunnerMetricsItem(item metricsItem) bool {
	return item.Kind == "runner" || item.Component == "runner" || item.Name == "runner"
}

func addAlertFinding(report *alertsReport, finding diagnosticFinding) {
	report.Data.Findings = append(report.Data.Findings, finding)
	switch finding.Severity {
	case "error":
		report.Data.Summary.Errors++
		report.OK = false
	case "warning":
		report.Data.Summary.Warnings++
	default:
		report.Data.Summary.Info++
	}
}

func checkTarget(ctx context.Context, httpClient *http.Client, target serviceTarget) healthItem {
	baseURL := strings.TrimRight(strings.TrimSpace(target.URL), "/")
	item := healthItem{
		Name:      target.Name,
		Kind:      target.Kind,
		ServiceID: target.ServiceID,
		NodeID:    target.NodeID,
		URL:       baseURL,
		Status:    "skipped",
	}
	if target.ConfigError != "" {
		item.Status = "unhealthy"
		item.Error = target.ConfigError
		return item
	}
	if baseURL == "" {
		if target.Required {
			item.Status = "unhealthy"
			item.Error = "service URL is required"
		}
		return item
	}
	item.HealthURL = joinURLPath(baseURL, target.HealthPath)
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

func addHealthItem(report *healthReport, item healthItem, affectsOK bool) {
	report.Data.Items = append(report.Data.Items, item)
	switch item.Status {
	case "healthy":
		report.Data.Summary.Healthy++
	case "skipped":
		report.Data.Summary.Skipped++
	default:
		report.Data.Summary.Unhealthy++
		if affectsOK {
			report.OK = false
		}
	}
}

func nodeHealthTargets(cfg adminConfig) []serviceTarget {
	credential := authorizationHeader(cfg.NodeToken)
	targets := []serviceTarget{}
	seen := map[string]bool{}
	add := func(name, nodeID, rawURL, configError string, required bool) {
		key := name + "\x00" + rawURL
		if seen[key] {
			return
		}
		seen[key] = true
		targets = append(targets, serviceTarget{
			Name:        name,
			Kind:        "node",
			URL:         rawURL,
			HealthPath:  "/v1/node/health",
			Credential:  credential,
			Required:    required,
			NodeID:      nodeID,
			ConfigError: configError,
		})
	}
	if strings.TrimSpace(cfg.NodeURL) != "" {
		add("node", "", cfg.NodeURL, "", false)
	}
	for _, entry := range splitCSV(cfg.NodeURLs) {
		nodeID, rawURL, ok := strings.Cut(entry, "=")
		nodeID = strings.TrimSpace(nodeID)
		rawURL = strings.TrimSpace(rawURL)
		if !ok || nodeID == "" || rawURL == "" {
			add("node:"+entry, "", "", "node target must be formatted as node_id=url", true)
			continue
		}
		add("node:"+nodeID, nodeID, rawURL, "", true)
	}
	if len(targets) == 0 {
		add("node", "", "", "", false)
	}
	return targets
}

func discoverProviderHealthTargets(ctx context.Context, httpClient *http.Client, cfg adminConfig) ([]serviceTarget, *healthItem) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.CatalogURL), "/")
	if baseURL == "" {
		return nil, &healthItem{
			Name:   "provider-discovery",
			Kind:   "provider",
			Status: "unhealthy",
			Error:  "catalog service URL is required",
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/catalog/capabilities", nil)
	if err != nil {
		return nil, &healthItem{
			Name:   "provider-discovery",
			Kind:   "provider",
			URL:    baseURL,
			Status: "unhealthy",
			Error:  err.Error(),
		}
	}
	if credential := authorizationHeader(cfg.ComponentToken); credential != "" {
		req.Header.Set("Authorization", credential)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, &healthItem{
			Name:   "provider-discovery",
			Kind:   "provider",
			URL:    baseURL,
			Status: "unhealthy",
			Error:  err.Error(),
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &healthItem{
			Name:       "provider-discovery",
			Kind:       "provider",
			URL:        baseURL,
			HealthURL:  baseURL + "/v1/catalog/capabilities",
			Status:     "unhealthy",
			HTTPStatus: resp.StatusCode,
			Error:      resp.Status,
		}
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Items []contracts.CatalogCapabilityRecord `json:"items"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, &healthItem{
			Name:       "provider-discovery",
			Kind:       "provider",
			URL:        baseURL,
			HealthURL:  baseURL + "/v1/catalog/capabilities",
			Status:     "unhealthy",
			HTTPStatus: resp.StatusCode,
			Error:      "invalid catalog response: " + err.Error(),
		}
	}
	if !envelope.OK {
		return nil, &healthItem{
			Name:       "provider-discovery",
			Kind:       "provider",
			URL:        baseURL,
			HealthURL:  baseURL + "/v1/catalog/capabilities",
			Status:     "unhealthy",
			HTTPStatus: resp.StatusCode,
			Error:      "catalog response was not ok",
		}
	}
	targets := []serviceTarget{}
	seen := map[string]bool{}
	for _, record := range envelope.Data.Items {
		route := record.Route
		serviceID := route.ServiceID
		if serviceID == "" {
			serviceID = record.Service.ID
		}
		if serviceID == "" {
			serviceID = record.Capability.ServiceID
		}
		if serviceID == "" {
			serviceID = "unknown"
		}
		healthPath := route.ProviderHealthPath
		if healthPath == "" {
			healthPath = "/v1/provider/health"
		}
		endpoint := strings.TrimSpace(route.ProviderEndpoint)
		nodeID := ""
		if route.NodeID != nil {
			nodeID = *route.NodeID
		}
		key := serviceID + "\x00" + endpoint + "\x00" + healthPath
		if seen[key] {
			continue
		}
		seen[key] = true
		target := serviceTarget{
			Name:       "provider:" + serviceID,
			Kind:       "provider",
			URL:        endpoint,
			HealthPath: healthPath,
			Required:   true,
			ServiceID:  serviceID,
			NodeID:     nodeID,
		}
		if endpoint == "" {
			target.ConfigError = "provider endpoint is empty"
		}
		targets = append(targets, target)
	}
	if len(targets) == 0 {
		return nil, &healthItem{
			Name:   "providers",
			Kind:   "provider",
			Status: "skipped",
			Error:  "catalog returned no provider endpoints",
		}
	}
	return targets, nil
}

func joinURLPath(baseURL, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return baseURL
	}
	if strings.HasPrefix(path, "/") {
		return baseURL + path
	}
	return baseURL + "/" + path
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

func getJSONDecode(cfg adminConfig, httpClient *http.Client, baseURL, path, credential string, out any) (int, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return 0, fmt.Errorf("service URL is required for %s", path)
	}
	ctx := context.Background()
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return 0, err
	}
	if credential != "" {
		req.Header.Set("Authorization", credential)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, err
		}
	} else if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}

func postJSON(cfg adminConfig, httpClient *http.Client, baseURL, path, credential, idempotencyKey string, stdout, stderr io.Writer) int {
	return postJSONBody(cfg, httpClient, baseURL, path, credential, idempotencyKey, nil, stdout, stderr)
}

func putFile(cfg adminConfig, httpClient *http.Client, baseURL, path, credential, idempotencyKey, filePath, mediaType, digest string, stdout, stderr io.Writer) int {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		fmt.Fprintf(stderr, "service URL is required for %s\n", path)
		return 2
	}
	file, err := os.Open(filePath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if info.IsDir() {
		fmt.Fprintf(stderr, "file %s is a directory\n", filePath)
		return 2
	}
	if digest == "" {
		digest, err = sha256Digest(file)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	ctx := context.Background()
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, baseURL+path, file)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	req.ContentLength = info.Size()
	req.Header.Set("Content-Type", mediaType)
	req.Header.Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	req.Header.Set("Digest", digest)
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

func sha256Digest(reader io.Reader) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, reader); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func fileChecksumAndSize(filePath string) (string, int64, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", 0, err
	}
	if info.IsDir() {
		return "", 0, fmt.Errorf("file %s is a directory", filePath)
	}
	checksum, err := sha256Digest(file)
	if err != nil {
		return "", 0, err
	}
	return checksum, info.Size(), nil
}

func postJSONBody(cfg adminConfig, httpClient *http.Client, baseURL, path, credential, idempotencyKey string, body any, stdout, stderr io.Writer) int {
	return postJSONBodyWithHeaders(cfg, httpClient, baseURL, path, credential, idempotencyKey, nil, body, stdout, stderr)
}

func postJSONBodyWithHeaders(cfg adminConfig, httpClient *http.Client, baseURL, path, credential, idempotencyKey string, headers map[string]string, body any, stdout, stderr io.Writer) int {
	var envelope any
	status, err := postJSONDecodeWithHeaders(cfg, httpClient, baseURL, path, credential, idempotencyKey, headers, body, &envelope)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := writePrettyJSON(stdout, raw); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if status < 200 || status >= 300 {
		fmt.Fprintf(stderr, "component returned HTTP %d\n", status)
		return 1
	}
	return 0
}

func postJSONDecode(cfg adminConfig, httpClient *http.Client, baseURL, path, credential, idempotencyKey string, body any, out any) (int, error) {
	return postJSONDecodeWithHeaders(cfg, httpClient, baseURL, path, credential, idempotencyKey, nil, body, out)
}

func postJSONDecodeWithHeaders(cfg adminConfig, httpClient *http.Client, baseURL, path, credential, idempotencyKey string, headers map[string]string, body any, out any) (int, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return 0, fmt.Errorf("service URL is required for %s", path)
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
			return 0, err
		}
		reader = strings.NewReader(string(raw))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, reader)
	if err != nil {
		return 0, err
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
	for key, value := range headers {
		if value != "" {
			req.Header.Set(key, value)
		}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, err
		}
	} else if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
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

func splitCSV(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
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

func optionalJSONObject(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, err
	}
	if decoded == nil {
		return nil, fmt.Errorf("must be a JSON object")
	}
	return decoded, nil
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

func writeCommandJSON(stdout, stderr io.Writer, body any) int {
	if err := writeJSON(stdout, body); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
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
	fmt.Fprintln(w, "commands: health, metrics, alerts, catalog, jobs, leases, artifacts, policy, node, diagnose")
}

func loadManifestInputs(path string) ([]contracts.ProviderManifest, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		manifest, err := loadManifestInputFile(path)
		if err != nil {
			return nil, err
		}
		return []contracts.ProviderManifest{manifest}, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	manifests := []contracts.ProviderManifest{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		manifest, err := loadManifestInputFile(filepath.Join(path, entry.Name()))
		if err != nil {
			return nil, err
		}
		manifests = append(manifests, manifest)
	}
	if len(manifests) == 0 {
		return nil, fmt.Errorf("manifest directory %s contains no .json files", path)
	}
	return manifests, nil
}

func loadManifestInputFile(path string) (contracts.ProviderManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return contracts.ProviderManifest{}, err
	}
	defer file.Close()
	var manifest contracts.ProviderManifest
	if err := json.NewDecoder(file).Decode(&manifest); err != nil {
		return contracts.ProviderManifest{}, fmt.Errorf("decode manifest %s: %w", path, err)
	}
	return manifest, nil
}
