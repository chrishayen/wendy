package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"pacp/internal/observability"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, http.DefaultClient))
}

func run(args []string, stdout, stderr io.Writer, httpClient *http.Client) int {
	global := flag.NewFlagSet("pacp-control", flag.ContinueOnError)
	global.SetOutput(stderr)
	gatewayURL := global.String("gateway-url", os.Getenv("PACP_GATEWAY_URL"), "gateway service base URL")
	token := global.String("token", os.Getenv("PACP_AGENT_TOKEN"), "agent bearer token or raw token")
	requestID := global.String("request-id", os.Getenv("PACP_REQUEST_ID"), "optional request id propagated as X-Request-ID")
	timeout := global.Duration("timeout", 30*time.Second, "request timeout")
	if err := global.Parse(args); err != nil {
		return 2
	}
	remaining := global.Args()
	if len(remaining) == 0 {
		printUsage(stderr)
		return 2
	}
	if *gatewayURL == "" {
		fmt.Fprintln(stderr, "gateway-url is required; set -gateway-url or PACP_GATEWAY_URL")
		return 2
	}
	if *token == "" && commandRequiresToken(remaining[0]) {
		fmt.Fprintln(stderr, "token is required; set -token or PACP_AGENT_TOKEN")
		return 2
	}
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	auth := ""
	if *token != "" {
		auth = authorizationHeader(*token)
	}
	*requestID = strings.TrimSpace(*requestID)
	if *requestID == "" {
		*requestID = observability.NewRequestID("req_control")
	}
	client := gatewayClient{
		baseURL:   strings.TrimRight(*gatewayURL, "/"),
		auth:      auth,
		client:    httpClient,
		timeout:   *timeout,
		requestID: *requestID,
	}
	code, err := runCommand(client, remaining, stdout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return code
	}
	return code
}

func runCommand(client gatewayClient, args []string, stdout, stderr io.Writer) (int, error) {
	switch args[0] {
	case "health":
		return runJSONCommand(client, http.MethodGet, "/v1/gateway/health", nil, "", stdout, nil)
	case "tools":
		return runJSONCommand(client, http.MethodGet, "/v1/tools", nil, "", stdout, nil)
	case "tool":
		if len(args) != 2 {
			return 2, errors.New("usage: pacp-control tool <capability-id>")
		}
		return runJSONCommand(client, http.MethodGet, "/v1/tools/"+url.PathEscape(args[1]), nil, "", stdout, nil)
	case "invoke":
		return invokeCommand(client, args[1:], stdout, stderr)
	case "job":
		if len(args) != 2 {
			return 2, errors.New("usage: pacp-control job <job-id>")
		}
		return runJSONCommand(client, http.MethodGet, "/v1/agent/jobs/"+url.PathEscape(args[1]), nil, "", stdout, nil)
	case "wait":
		return waitCommand(client, args[1:], stdout, stderr)
	case "cancel":
		return cancelCommand(client, args[1:], stdout, stderr)
	case "logs":
		return logsCommand(client, args[1:], stdout, stderr)
	case "queue":
		if len(args) != 2 {
			return 2, errors.New("usage: pacp-control queue <resource-selector>")
		}
		return runJSONCommand(client, http.MethodGet, "/v1/agent/resources/queues/"+url.PathEscape(args[1]), nil, "", stdout, nil)
	case "artifacts":
		return artifactsCommand(client, args[1:], stdout, stderr)
	case "artifact-content":
		return contentCommand(client, args[1:], stdout, stderr)
	default:
		printUsage(stderr)
		return 2, fmt.Errorf("unknown command %q", args[0])
	}
}

func invokeCommand(client gatewayClient, args []string, stdout, stderr io.Writer) (int, error) {
	flags := flag.NewFlagSet("invoke", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "{}", "JSON object input")
	preferredMode := flags.String("mode", "", "preferred execution mode")
	dryRun := flags.Bool("dry-run", false, "request provider dry run")
	idempotencyKey := flags.String("idempotency-key", "", "idempotency key for this invocation")
	wait := flags.Bool("wait", false, "wait for async jobs to reach a terminal state")
	waitInterval := flags.Duration("wait-interval", 2*time.Second, "poll interval when -wait is set")
	waitTimeout := flags.Duration("wait-timeout", 10*time.Minute, "maximum wait time when -wait is set; zero waits forever")
	remaining, err := parseCommandFlags(flags, args)
	if err != nil {
		return 2, err
	}
	if len(remaining) != 1 {
		return 2, errors.New("usage: pacp-control invoke <capability-id> -idempotency-key <key> [-input JSON] [-wait]")
	}
	if *idempotencyKey == "" {
		return 2, errors.New("idempotency-key is required for invoke")
	}
	if *preferredMode != "" && *preferredMode != "sync" && *preferredMode != "async" {
		return 2, errors.New("mode must be sync or async")
	}
	if *wait {
		if *waitInterval <= 0 {
			return 2, errors.New("wait-interval must be greater than zero")
		}
		if *waitTimeout < 0 {
			return 2, errors.New("wait-timeout must be zero or greater")
		}
	}
	inputObject, err := decodeJSONObject(*input)
	if err != nil {
		return 2, fmt.Errorf("input: %w", err)
	}
	body := map[string]any{"input": inputObject}
	if *preferredMode != "" {
		body["preferred_mode"] = *preferredMode
	}
	if *dryRun {
		body["dry_run"] = true
	}
	path := "/v1/tools/" + url.PathEscape(remaining[0]) + "/invoke"
	if !*wait {
		return runJSONCommand(client, http.MethodPost, path, body, *idempotencyKey, stdout, nil)
	}
	status, raw, err := requestRaw(client, http.MethodPost, path, body, *idempotencyKey)
	if err != nil {
		return 1, err
	}
	if status < 200 || status >= 300 {
		if err := writePrettyJSON(stdout, raw); err != nil {
			return 1, err
		}
		return 1, fmt.Errorf("gateway returned HTTP %d", status)
	}
	mode, jobID, err := invokeResultFromEnvelope(raw)
	if err != nil {
		return 1, err
	}
	if mode != "async" || jobID == "" {
		if err := writePrettyJSON(stdout, raw); err != nil {
			return 1, err
		}
		return 0, nil
	}
	return waitForJob(client, jobID, waitOptions{Interval: *waitInterval, Timeout: *waitTimeout}, stdout)
}

func waitCommand(client gatewayClient, args []string, stdout, stderr io.Writer) (int, error) {
	flags := flag.NewFlagSet("wait", flag.ContinueOnError)
	flags.SetOutput(stderr)
	interval := flags.Duration("interval", 2*time.Second, "poll interval")
	timeout := flags.Duration("timeout", 10*time.Minute, "maximum wait time; zero waits forever")
	remaining, err := parseCommandFlags(flags, args)
	if err != nil {
		return 2, err
	}
	if len(remaining) != 1 {
		return 2, errors.New("usage: pacp-control wait <job-id> [-interval duration] [-timeout duration]")
	}
	if *interval <= 0 {
		return 2, errors.New("interval must be greater than zero")
	}
	if *timeout < 0 {
		return 2, errors.New("timeout must be zero or greater")
	}

	return waitForJob(client, remaining[0], waitOptions{Interval: *interval, Timeout: *timeout}, stdout)
}

type waitOptions struct {
	Interval time.Duration
	Timeout  time.Duration
}

func waitForJob(client gatewayClient, jobID string, opts waitOptions, stdout io.Writer) (int, error) {
	path := "/v1/agent/jobs/" + url.PathEscape(jobID)
	started := time.Now()
	for {
		status, raw, err := requestRaw(client, http.MethodGet, path, nil, "")
		if err != nil {
			return 1, err
		}
		if status < 200 || status >= 300 {
			if err := writePrettyJSON(stdout, raw); err != nil {
				return 1, err
			}
			return 1, fmt.Errorf("gateway returned HTTP %d", status)
		}
		state, err := jobStateFromEnvelope(raw)
		if err != nil {
			return 1, err
		}
		if isTerminalJobState(state) {
			if err := writePrettyJSON(stdout, raw); err != nil {
				return 1, err
			}
			return 0, nil
		}
		if opts.Timeout > 0 && time.Since(started) >= opts.Timeout {
			return 1, fmt.Errorf("wait timed out after %s; last state=%s", opts.Timeout.String(), state)
		}
		sleepFor := opts.Interval
		if opts.Timeout > 0 {
			remaining := opts.Timeout - time.Since(started)
			if remaining <= 0 {
				continue
			}
			if remaining < sleepFor {
				sleepFor = remaining
			}
		}
		time.Sleep(sleepFor)
	}
}

func cancelCommand(client gatewayClient, args []string, stdout, stderr io.Writer) (int, error) {
	flags := flag.NewFlagSet("cancel", flag.ContinueOnError)
	flags.SetOutput(stderr)
	reason := flags.String("reason", "", "cancel reason")
	idempotencyKey := flags.String("idempotency-key", "", "idempotency key for this cancellation")
	remaining, err := parseCommandFlags(flags, args)
	if err != nil {
		return 2, err
	}
	if len(remaining) != 1 {
		return 2, errors.New("usage: pacp-control cancel <job-id> -idempotency-key <key> [-reason text]")
	}
	if *idempotencyKey == "" {
		return 2, errors.New("idempotency-key is required for cancel")
	}
	body := map[string]any{}
	if *reason != "" {
		body["reason"] = *reason
	}
	path := "/v1/agent/jobs/" + url.PathEscape(remaining[0]) + "/cancel"
	return runJSONCommand(client, http.MethodPost, path, body, *idempotencyKey, stdout, nil)
}

func logsCommand(client gatewayClient, args []string, stdout, stderr io.Writer) (int, error) {
	flags := flag.NewFlagSet("logs", flag.ContinueOnError)
	flags.SetOutput(stderr)
	limit := flags.Int("limit", 50, "maximum log entries")
	cursor := flags.String("cursor", "", "pagination cursor")
	remaining, err := parseCommandFlags(flags, args)
	if err != nil {
		return 2, err
	}
	if len(remaining) != 1 {
		return 2, errors.New("usage: pacp-control logs <job-id> [-limit n] [-cursor cursor]")
	}
	query := url.Values{}
	query.Set("limit", fmt.Sprintf("%d", *limit))
	if *cursor != "" {
		query.Set("cursor", *cursor)
	}
	path := "/v1/agent/jobs/" + url.PathEscape(remaining[0]) + "/logs?" + query.Encode()
	return runJSONCommand(client, http.MethodGet, path, nil, "", stdout, nil)
}

func artifactsCommand(client gatewayClient, args []string, stdout, stderr io.Writer) (int, error) {
	flags := flag.NewFlagSet("artifacts", flag.ContinueOnError)
	flags.SetOutput(stderr)
	outDir := flags.String("out-dir", "", "download artifact bytes into this directory")
	remaining, err := parseCommandFlags(flags, args)
	if err != nil {
		return 2, err
	}
	if len(remaining) != 1 {
		return 2, errors.New("usage: pacp-control artifacts <job-id> [-out-dir dir]")
	}
	jobID := remaining[0]
	path := "/v1/agent/jobs/" + url.PathEscape(jobID) + "/artifacts"
	if *outDir == "" {
		return runJSONCommand(client, http.MethodGet, path, nil, "", stdout, nil)
	}

	status, raw, err := requestRaw(client, http.MethodGet, path, nil, "")
	if err != nil {
		return 1, err
	}
	if status < 200 || status >= 300 {
		if err := writePrettyJSON(stdout, raw); err != nil {
			return 1, err
		}
		return 1, fmt.Errorf("gateway returned HTTP %d", status)
	}
	artifacts, err := artifactListFromEnvelope(raw)
	if err != nil {
		return 1, err
	}
	if err := os.MkdirAll(*outDir, 0o700); err != nil {
		return 1, err
	}

	usedNames := map[string]int{}
	results := make([]map[string]any, 0, len(artifacts))
	for _, artifact := range artifacts {
		filename := uniqueArtifactFilename(artifact, usedNames)
		outPath := filepath.Join(*outDir, filename)
		result, err := downloadArtifactToFile(client, artifact.ArtifactID, outPath)
		if err != nil {
			return 1, err
		}
		result["name"] = artifact.Name
		results = append(results, result)
	}
	report := map[string]any{
		"ok": true,
		"data": map[string]any{
			"job_id": jobID,
			"items":  results,
		},
		"links": map[string]any{},
		"meta":  commandMeta(client),
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return 1, err
	}
	_, err = fmt.Fprintln(stdout, string(encoded))
	if err != nil {
		return 1, err
	}
	return 0, nil
}

func contentCommand(client gatewayClient, args []string, stdout, stderr io.Writer) (int, error) {
	flags := flag.NewFlagSet("artifact-content", flag.ContinueOnError)
	flags.SetOutput(stderr)
	outPath := flags.String("out", "", "write artifact bytes to this file instead of stdout")
	remaining, err := parseCommandFlags(flags, args)
	if err != nil {
		return 2, err
	}
	if len(remaining) != 1 {
		return 2, errors.New("usage: pacp-control artifact-content <artifact-id> [-out file]")
	}
	artifactID := remaining[0]
	resp, err := client.do(context.Background(), http.MethodGet, "/v1/artifacts/"+url.PathEscape(artifactID)+"/content", nil, "")
	if err != nil {
		return 1, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(stderr, resp.Body)
		return 1, fmt.Errorf("gateway returned HTTP %d", resp.StatusCode)
	}
	if *outPath != "" {
		file, err := os.OpenFile(*outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return 1, err
		}
		bytesWritten, copyErr := io.Copy(file, resp.Body)
		closeErr := file.Close()
		if copyErr != nil {
			return 1, copyErr
		}
		if closeErr != nil {
			return 1, closeErr
		}
		return writeArtifactFileResult(stdout, client, artifactID, *outPath, bytesWritten, resp.Header.Get("Content-Type"), resp.Header.Get("Digest"))
	}
	if _, err := io.Copy(stdout, resp.Body); err != nil {
		return 1, err
	}
	return 0, nil
}

func writeArtifactFileResult(stdout io.Writer, client gatewayClient, artifactID, path string, size int64, contentType, digest string) (int, error) {
	result := map[string]any{
		"ok": true,
		"data": map[string]any{
			"artifact_id":  artifactID,
			"path":         path,
			"bytes":        size,
			"content_type": contentType,
			"digest":       digest,
		},
		"links": map[string]any{},
		"meta":  commandMeta(client),
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return 1, err
	}
	_, err = fmt.Fprintln(stdout, string(encoded))
	if err != nil {
		return 1, err
	}
	return 0, nil
}

func commandMeta(client gatewayClient) map[string]string {
	meta := map[string]string{"schema_version": "v1"}
	if client.requestID != "" {
		meta["request_id"] = client.requestID
	}
	return meta
}

func downloadArtifactToFile(client gatewayClient, artifactID, outPath string) (map[string]any, error) {
	resp, err := client.do(context.Background(), http.MethodGet, "/v1/artifacts/"+url.PathEscape(artifactID)+"/content", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("artifact %s content request returned HTTP %d: %s", artifactID, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	file, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	bytesWritten, copyErr := io.Copy(file, resp.Body)
	closeErr := file.Close()
	if copyErr != nil {
		return nil, copyErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return map[string]any{
		"artifact_id":  artifactID,
		"path":         outPath,
		"bytes":        bytesWritten,
		"content_type": resp.Header.Get("Content-Type"),
		"digest":       resp.Header.Get("Digest"),
	}, nil
}

type listedArtifact struct {
	ArtifactID string `json:"artifact_id"`
	Name       string `json:"name"`
}

func artifactListFromEnvelope(raw []byte) ([]listedArtifact, error) {
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Items []listedArtifact `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode artifact list: %w", err)
	}
	if !envelope.OK {
		return nil, errors.New("artifact list response was not ok")
	}
	for _, artifact := range envelope.Data.Items {
		if artifact.ArtifactID == "" {
			return nil, errors.New("artifact list item missing artifact_id")
		}
	}
	return envelope.Data.Items, nil
}

func uniqueArtifactFilename(artifact listedArtifact, used map[string]int) string {
	name := safeArtifactFilename(artifact)
	used[name]++
	if used[name] == 1 {
		return name
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	return fmt.Sprintf("%s-%d%s", stem, used[name], ext)
}

func safeArtifactFilename(artifact listedArtifact) string {
	name := strings.TrimSpace(artifact.Name)
	if name == "" {
		name = artifact.ArtifactID
	}
	name = filepath.Base(name)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return artifact.ArtifactID
	}
	return name
}

func requestRaw(client gatewayClient, method, path string, body any, idempotencyKey string) (int, []byte, error) {
	resp, err := client.do(context.Background(), method, path, body, idempotencyKey)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, raw, nil
}

func runJSONCommand(client gatewayClient, method, path string, body any, idempotencyKey string, stdout io.Writer, stderr io.Writer) (int, error) {
	resp, err := client.do(context.Background(), method, path, body, idempotencyKey)
	if err != nil {
		return 1, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return 1, err
	}
	if err := writePrettyJSON(stdout, raw); err != nil {
		return 1, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 1, fmt.Errorf("gateway returned HTTP %d", resp.StatusCode)
	}
	return 0, nil
}

func invokeResultFromEnvelope(raw []byte) (string, string, error) {
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Mode  string `json:"mode"`
			JobID string `json:"job_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", "", fmt.Errorf("decode invoke response: %w", err)
	}
	if !envelope.OK {
		return "", "", errors.New("invoke response was not ok")
	}
	if envelope.Data.Mode == "" {
		return "", "", errors.New("invoke response missing mode")
	}
	return envelope.Data.Mode, envelope.Data.JobID, nil
}

func jobStateFromEnvelope(raw []byte) (string, error) {
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			State string `json:"state"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", fmt.Errorf("decode job response: %w", err)
	}
	if !envelope.OK {
		return "", errors.New("job response was not ok")
	}
	if envelope.Data.State == "" {
		return "", errors.New("job response missing state")
	}
	return envelope.Data.State, nil
}

func isTerminalJobState(state string) bool {
	switch state {
	case "succeeded", "failed", "canceled", "expired":
		return true
	default:
		return false
	}
}

type gatewayClient struct {
	baseURL   string
	auth      string
	client    *http.Client
	timeout   time.Duration
	requestID string
}

func (c gatewayClient) do(ctx context.Context, method, path string, body any, idempotencyKey string) (*http.Response, error) {
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	if c.auth != "" {
		req.Header.Set("Authorization", c.auth)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if c.requestID != "" {
		req.Header.Set(observability.RequestIDHeader, c.requestID)
	}
	return c.client.Do(req)
}

func decodeJSONObject(raw string) (map[string]any, error) {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, err
	}
	if decoded == nil {
		return nil, errors.New("must be a JSON object")
	}
	return decoded, nil
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

func parseCommandFlags(flags *flag.FlagSet, args []string) ([]string, error) {
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

func authorizationHeader(token string) string {
	if strings.HasPrefix(token, "Bearer ") {
		return token
	}
	return "Bearer " + token
}

func commandRequiresToken(command string) bool {
	return command != "health"
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: pacp-control -gateway-url URL [-token TOKEN] <command> [args]")
	fmt.Fprintln(w, "commands: health, tools, tool, invoke, job, wait, cancel, logs, queue, artifacts, artifact-content")
}
