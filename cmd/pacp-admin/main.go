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
	"path/filepath"
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
	case "policy":
		return policyCommand(cfg, httpClient, remaining[1:], stdout, stderr)
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
		return usage(stderr, "usage: pacp-admin [flags] leases <resources|resource|register-resource|inspect|request|create-request|cancel-request|lease|release> [id]")
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
		return usage(stderr, "usage: pacp-admin [flags] leases <resources|resource|register-resource|inspect|request|create-request|cancel-request|lease|release> [id]")
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
	fmt.Fprintln(w, "commands: health, catalog, jobs, leases, artifacts, policy, node")
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
