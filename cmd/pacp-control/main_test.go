package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHealthDoesNotRequireToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/gateway/health" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization = %q", got)
		}
		writeTestJSON(t, w, http.StatusOK, map[string]any{
			"ok":   true,
			"data": map[string]any{"status": "healthy"},
		})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"-gateway-url", server.URL, "health"}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"status": "healthy"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestToolsRequiresToken(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-gateway-url", "http://gateway.invalid", "tools"}, &stdout, &stderr, http.DefaultClient)
	if code != 2 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "token is required") {
		t.Fatalf("stderr = %s", stderr.String())
	}
}

func TestToolsSendsBearerTokenAndPrintsEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/tools" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token_agent" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-Request-ID"); got != "req_control_trace" {
			t.Fatalf("X-Request-ID = %q", got)
		}
		writeTestJSON(t, w, http.StatusOK, map[string]any{
			"ok":   true,
			"data": map[string]any{"items": []any{}},
		})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"-gateway-url", server.URL, "-token", "token_agent", "-request-id", "req_control_trace", "tools"}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"ok": true`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestInvokePostsInputAndIdempotencyKey(t *testing.T) {
	seenRequestID := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/tools/cap_echo/invoke" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token_agent" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "invoke-1" {
			t.Fatalf("Idempotency-Key = %q", got)
		}
		seenRequestID = r.Header.Get("X-Request-ID")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		input := body["input"].(map[string]any)
		if input["message"] != "hello" || body["preferred_mode"] != "sync" {
			t.Fatalf("body = %#v", body)
		}
		writeTestJSON(t, w, http.StatusCreated, map[string]any{
			"ok":   true,
			"data": map[string]any{"mode": "sync", "output": map[string]any{"message": "hello"}},
		})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-gateway-url", server.URL,
		"-token", "Bearer token_agent",
		"invoke", "cap_echo",
		"-idempotency-key", "invoke-1",
		"-input", `{"message":"hello"}`,
		"-mode", "sync",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"mode": "sync"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
	if !strings.HasPrefix(seenRequestID, "req_control_") {
		t.Fatalf("generated X-Request-ID = %q", seenRequestID)
	}
}

func TestInvokeRequiresIdempotencyKey(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-gateway-url", "http://gateway.invalid",
		"-token", "token_agent",
		"invoke", "cap_echo",
		"-input", `{"message":"hello"}`,
	}, &stdout, &stderr, http.DefaultClient)
	if code != 2 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "idempotency-key is required") {
		t.Fatalf("stderr = %s", stderr.String())
	}
}

func TestInvokeWaitsForAsyncJob(t *testing.T) {
	invokeRequests := 0
	jobRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/tools/cap_async/invoke":
			if r.Method != http.MethodPost {
				t.Fatalf("invoke method = %s", r.Method)
			}
			invokeRequests++
			if got := r.Header.Get("Idempotency-Key"); got != "invoke-async-1" {
				t.Fatalf("Idempotency-Key = %q", got)
			}
			writeTestJSON(t, w, http.StatusCreated, map[string]any{
				"ok":   true,
				"data": map[string]any{"mode": "async", "job_id": "job_1"},
			})
		case "/v1/agent/jobs/job_1":
			if r.Method != http.MethodGet {
				t.Fatalf("job method = %s", r.Method)
			}
			jobRequests++
			state := "running"
			if jobRequests >= 2 {
				state = "succeeded"
			}
			writeTestJSON(t, w, http.StatusOK, map[string]any{
				"ok": true,
				"data": map[string]any{
					"job_id": "job_1",
					"state":  state,
				},
			})
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-gateway-url", server.URL,
		"-token", "token_agent",
		"invoke", "cap_async",
		"-idempotency-key", "invoke-async-1",
		"-mode", "async",
		"-wait",
		"-wait-interval", "1ms",
		"-wait-timeout", "1s",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if invokeRequests != 1 || jobRequests < 2 {
		t.Fatalf("invokeRequests=%d jobRequests=%d", invokeRequests, jobRequests)
	}
	output := stdout.String()
	if strings.Contains(output, `"mode": "async"`) || !strings.Contains(output, `"state": "succeeded"`) {
		t.Fatalf("stdout = %s", output)
	}
}

func TestArtifactContentStreamsBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/artifacts/art_1/content" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("artifact bytes"))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"-gateway-url", server.URL, "-token", "token_agent", "artifact-content", "art_1"}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if stdout.String() != "artifact bytes" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestArtifactContentWritesOutputFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/artifacts/art_1/content" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token_agent" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-Request-ID"); got != "req_artifact_trace" {
			t.Fatalf("X-Request-ID = %q", got)
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Digest", "sha256=testdigest")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("artifact bytes"))
	}))
	defer server.Close()

	outPath := filepath.Join(t.TempDir(), "artifact.txt")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-gateway-url", server.URL,
		"-token", "token_agent",
		"-request-id", "req_artifact_trace",
		"artifact-content", "art_1",
		"-out", outPath,
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	written, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if string(written) != "artifact bytes" {
		t.Fatalf("file body = %q", string(written))
	}
	output := stdout.String()
	for _, expected := range []string{
		`"artifact_id": "art_1"`,
		`"bytes": 14`,
		`"content_type": "text/plain"`,
		`"digest": "sha256=testdigest"`,
		`"request_id": "req_artifact_trace"`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("stdout missing %q:\n%s", expected, output)
		}
	}
}

func TestArtifactsDownloadsJobArtifacts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token_agent" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-Request-ID"); got != "req_artifacts_trace" {
			t.Fatalf("X-Request-ID = %q", got)
		}
		switch r.URL.Path {
		case "/v1/agent/jobs/job_1/artifacts":
			if r.Method != http.MethodGet {
				t.Fatalf("method = %s", r.Method)
			}
			writeTestJSON(t, w, http.StatusOK, map[string]any{
				"ok": true,
				"data": map[string]any{
					"items": []map[string]any{
						{"artifact_id": "art_1", "name": "result.txt"},
						{"artifact_id": "art_2", "name": "../result.txt"},
					},
					"next_cursor": nil,
				},
			})
		case "/v1/artifacts/art_1/content":
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Digest", "sha256=one")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("one"))
		case "/v1/artifacts/art_2/content":
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Digest", "sha256=two")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("two"))
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	outDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-gateway-url", server.URL,
		"-token", "token_agent",
		"-request-id", "req_artifacts_trace",
		"artifacts", "job_1",
		"-out-dir", outDir,
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	first, err := os.ReadFile(filepath.Join(outDir, "result.txt"))
	if err != nil {
		t.Fatalf("read first artifact: %v", err)
	}
	second, err := os.ReadFile(filepath.Join(outDir, "result-2.txt"))
	if err != nil {
		t.Fatalf("read second artifact: %v", err)
	}
	if string(first) != "one" || string(second) != "two" {
		t.Fatalf("downloads = %q %q", string(first), string(second))
	}
	output := stdout.String()
	for _, expected := range []string{
		`"job_id": "job_1"`,
		`"artifact_id": "art_1"`,
		`"artifact_id": "art_2"`,
		`"path": "` + filepath.Join(outDir, "result-2.txt") + `"`,
		`"request_id": "req_artifacts_trace"`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("stdout missing %q:\n%s", expected, output)
		}
	}
}

func TestWaitPollsUntilTerminalJob(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/agent/jobs/job_1" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		requests++
		state := "running"
		if requests >= 2 {
			state = "succeeded"
		}
		writeTestJSON(t, w, http.StatusOK, map[string]any{
			"ok": true,
			"data": map[string]any{
				"job_id": "job_1",
				"state":  state,
			},
		})
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-gateway-url", server.URL,
		"-token", "token_agent",
		"wait", "job_1",
		"-interval", "1ms",
		"-timeout", "1s",
	}, &stdout, &stderr, server.Client())
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if requests < 2 {
		t.Fatalf("requests=%d", requests)
	}
	output := stdout.String()
	if strings.Contains(output, `"state": "running"`) || !strings.Contains(output, `"state": "succeeded"`) {
		t.Fatalf("stdout = %s", output)
	}
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, status int, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
