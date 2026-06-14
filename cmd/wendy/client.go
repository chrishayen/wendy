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

	"wendy/internal/contracts"
	"wendy/internal/observability"
)

type gatewayClient struct {
	baseURL string
	token   string
	client  *http.Client
}

func newGatewayClient(cfg Config, httpClient *http.Client) gatewayClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return gatewayClient{
		baseURL: strings.TrimRight(cfg.gatewayURL(), "/"),
		token:   cfg.Credentials.Agent,
		client:  httpClient,
	}
}

func runStatus(cfg Config, stdout io.Writer, httpClient *http.Client) int {
	client := newGatewayClient(cfg, httpClient)
	raw, code, err := client.do(context.Background(), http.MethodGet, "/v1/gateway/health", nil, "")
	if err != nil {
		fmt.Fprintf(stdout, "gateway %s unreachable: %v\n", cfg.gatewayURL(), err)
		return 1
	}
	if code < 200 || code >= 300 {
		fmt.Fprintf(stdout, "gateway %s unhealthy: HTTP %d %s\n", cfg.gatewayURL(), code, strings.TrimSpace(string(raw)))
		return 1
	}
	fmt.Fprintf(stdout, "gateway %s healthy\n", cfg.gatewayURL())
	return 0
}

func runTools(cfg Config, stdout, stderr io.Writer, httpClient *http.Client) int {
	client := newGatewayClient(cfg, httpClient)
	raw, code, err := client.do(context.Background(), http.MethodGet, "/v1/tools", nil, "")
	return writeHTTPResult(stdout, stderr, raw, code, err)
}

func runInvoke(cfg Config, args []string, stdout, stderr io.Writer, httpClient *http.Client) int {
	capabilityID, flagArgs := splitLeadingArgument(args)
	flags := flag.NewFlagSet("invoke", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "{}", "JSON object input")
	mode := flags.String("mode", "", "preferred execution mode: sync or async")
	idempotencyKey := flags.String("idempotency-key", "", "optional idempotency key; generated when omitted")
	wait := flags.Bool("wait", false, "wait for async jobs to reach a terminal state")
	if err := flags.Parse(flagArgs); err != nil {
		return 2
	}
	remaining := appendPositional(capabilityID, flags.Args())
	if len(remaining) != 1 {
		fmt.Fprintln(stderr, "usage: wendy invoke <capability-id> [--input JSON] [--wait]")
		return 2
	}
	if *mode != "" && *mode != "sync" && *mode != "async" {
		fmt.Fprintln(stderr, "--mode must be sync or async")
		return 2
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(*input), &payload); err != nil {
		fmt.Fprintf(stderr, "input must be a JSON object: %v\n", err)
		return 2
	}
	if *idempotencyKey == "" {
		key, err := generatedID("invoke")
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		*idempotencyKey = key
	}
	req := contracts.InvokeToolRequest{Input: payload, PreferredMode: *mode}
	client := newGatewayClient(cfg, httpClient)
	raw, code, err := client.do(context.Background(), http.MethodPost, "/v1/tools/"+url.PathEscape(remaining[0])+"/invoke", req, *idempotencyKey)
	if err != nil || code < 200 || code >= 300 {
		return writeHTTPResult(stdout, stderr, raw, code, err)
	}
	if !*wait {
		return writePretty(stdout, raw)
	}
	jobID, modeValue, err := invokeResult(raw)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if modeValue != "async" || jobID == "" {
		return writePretty(stdout, raw)
	}
	final, err := waitForJob(client, jobID, 2*time.Second, 10*time.Minute)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return writePretty(stdout, final)
}

func runArtifacts(cfg Config, args []string, stdout, stderr io.Writer, httpClient *http.Client) int {
	jobID, flagArgs := splitLeadingArgument(args)
	flags := flag.NewFlagSet("artifacts", flag.ContinueOnError)
	flags.SetOutput(stderr)
	outDir := flags.String("out-dir", "", "optional directory to download artifact content")
	if err := flags.Parse(flagArgs); err != nil {
		return 2
	}
	remaining := appendPositional(jobID, flags.Args())
	if len(remaining) != 1 {
		fmt.Fprintln(stderr, "usage: wendy artifacts <job-id> [--out-dir dir]")
		return 2
	}
	client := newGatewayClient(cfg, httpClient)
	raw, code, err := client.do(context.Background(), http.MethodGet, "/v1/agent/jobs/"+url.PathEscape(remaining[0])+"/artifacts", nil, "")
	if err != nil || code < 200 || code >= 300 || *outDir == "" {
		return writeHTTPResult(stdout, stderr, raw, code, err)
	}
	artifacts, err := artifactItems(raw)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	results := make([]map[string]any, 0, len(artifacts))
	for _, artifact := range artifacts {
		filename := safeFilename(artifact.Name)
		if filename == "" {
			filename = artifact.ArtifactID
		}
		target := filepath.Join(*outDir, filename)
		content, code, err := client.do(context.Background(), http.MethodGet, "/v1/artifacts/"+url.PathEscape(artifact.ArtifactID)+"/content", nil, "")
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if code < 200 || code >= 300 {
			fmt.Fprintf(stderr, "artifact %s content request returned HTTP %d: %s\n", artifact.ArtifactID, code, strings.TrimSpace(string(content)))
			return 1
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		results = append(results, map[string]any{"artifact_id": artifact.ArtifactID, "path": target})
	}
	return writeJSON(stdout, map[string]any{"ok": true, "data": map[string]any{"items": results}})
}

func splitLeadingArgument(args []string) (string, []string) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "", args
	}
	return args[0], args[1:]
}

func appendPositional(first string, rest []string) []string {
	if first == "" {
		return rest
	}
	out := make([]string, 0, len(rest)+1)
	out = append(out, first)
	out = append(out, rest...)
	return out
}

func (c gatewayClient) do(ctx context.Context, method, path string, body any, idempotencyKey string) ([]byte, int, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", authorizationHeader(c.token))
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	req.Header.Set("X-Request-ID", observability.NewRequestID("req_wendy"))
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	return raw, resp.StatusCode, err
}

func writeHTTPResult(stdout, stderr io.Writer, raw []byte, code int, err error) int {
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if code < 200 || code >= 300 {
		fmt.Fprintf(stderr, "HTTP %d: %s\n", code, strings.TrimSpace(string(raw)))
		return 1
	}
	return writePretty(stdout, raw)
}

func writePretty(stdout io.Writer, raw []byte) int {
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		fmt.Fprintln(stdout, string(raw))
		return 0
	}
	return writeJSON(stdout, decoded)
}

func writeJSON(stdout io.Writer, value any) int {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return 1
	}
	return 0
}

func invokeResult(raw []byte) (jobID, mode string, err error) {
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			JobID string `json:"job_id"`
			Mode  string `json:"mode"`
		} `json:"data"`
		Error any `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", "", err
	}
	if !envelope.OK {
		return "", "", errors.New("invoke response was not ok")
	}
	return envelope.Data.JobID, envelope.Data.Mode, nil
}

func waitForJob(client gatewayClient, jobID string, interval, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	var last []byte
	for {
		raw, code, err := client.do(context.Background(), http.MethodGet, "/v1/agent/jobs/"+url.PathEscape(jobID), nil, "")
		if err != nil {
			return nil, err
		}
		if code < 200 || code >= 300 {
			return nil, fmt.Errorf("job status returned HTTP %d: %s", code, strings.TrimSpace(string(raw)))
		}
		last = raw
		state := jobState(raw)
		switch state {
		case "succeeded", "failed", "canceled", "expired":
			return raw, nil
		}
		if timeout > 0 && time.Now().After(deadline) {
			return last, fmt.Errorf("wait timed out after %s; last state=%s", timeout, state)
		}
		time.Sleep(interval)
	}
}

func jobState(raw []byte) string {
	var envelope struct {
		Data struct {
			State string `json:"state"`
		} `json:"data"`
	}
	_ = json.Unmarshal(raw, &envelope)
	return envelope.Data.State
}

type listedArtifact struct {
	ArtifactID string `json:"artifact_id"`
	Name       string `json:"name"`
}

func artifactItems(raw []byte) ([]listedArtifact, error) {
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Items []listedArtifact `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	if !envelope.OK {
		return nil, errors.New("artifact list response was not ok")
	}
	return envelope.Data.Items, nil
}

func safeFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', 0:
			return '-'
		default:
			return r
		}
	}, name)
	if name == "." || name == string(filepath.Separator) {
		return ""
	}
	return name
}

func generatedID(prefix string) (string, error) {
	token, err := generateToken()
	if err != nil {
		return "", err
	}
	return prefix + "-" + strings.TrimPrefix(token, "wendy_"), nil
}
