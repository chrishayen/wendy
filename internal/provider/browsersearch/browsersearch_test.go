package browsersearch

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchReturnsRankedIndexResults(t *testing.T) {
	indexPath := writeSearchIndex(t, []SearchItem{
		{Title: "Artifact uploads", URL: "https://docs.local/artifacts", Snippet: "Create upload sessions and complete artifact records.", Tags: []string{"artifacts"}},
		{Title: "Lease requests", URL: "https://docs.local/leases", Snippet: "Request GPU resources.", Tags: []string{"leases"}},
	})
	server := newTestServer(t, Config{Endpoint: "http://provider.local", SearchIndexPath: indexPath})

	data := invokeProvider(t, server, SearchCapabilityID, map[string]any{"query": "artifact", "limit": 5}, http.StatusOK)
	output := data["output"].(map[string]any)
	if output["count"].(float64) != 1 {
		t.Fatalf("output = %#v", output)
	}
	items := output["items"].([]any)
	first := items[0].(map[string]any)
	if first["url"] != "https://docs.local/artifacts" || first["title"] != "Artifact uploads" {
		t.Fatalf("first result = %#v", first)
	}
	safety := output["safety"].(map[string]any)
	if safety["filtered_count"].(float64) != 0 {
		t.Fatalf("safety = %#v", safety)
	}
}

func TestSearchHonorsSafetyOptions(t *testing.T) {
	indexPath := writeSearchIndex(t, []SearchItem{
		{Title: "Artifact uploads", URL: "https://docs.local/artifacts", Snippet: "Create upload sessions.", Tags: []string{"artifacts"}},
		{Title: "Artifact mirror", URL: "https://mirror.local/artifacts", Snippet: "Mirrored artifact notes.", Tags: []string{"artifacts"}},
		{Title: "Artifact HTTP", URL: "http://docs.local/artifacts", Snippet: "Insecure artifact notes.", Tags: []string{"artifacts"}},
	})
	server := newTestServer(t, Config{Endpoint: "http://provider.local", SearchIndexPath: indexPath})

	data := invokeProvider(t, server, SearchCapabilityID, map[string]any{
		"query":              "artifact",
		"limit":              10,
		"allowed_hosts":      []any{"docs.local"},
		"allow_http_results": false,
	}, http.StatusOK)
	output := data["output"].(map[string]any)
	if output["count"].(float64) != 1 {
		t.Fatalf("output = %#v", output)
	}
	items := output["items"].([]any)
	first := items[0].(map[string]any)
	if first["url"] != "https://docs.local/artifacts" {
		t.Fatalf("first = %#v", first)
	}
	safety := output["safety"].(map[string]any)
	if safety["filtered_count"].(float64) != 2 {
		t.Fatalf("safety = %#v", safety)
	}
}

func TestFetchExtractsAllowedHTMLPage(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>Docs</title></head><body><main>Hello <strong>Wendy</strong></main><a href="/next">Next page</a></body></html>`))
	}))
	defer page.Close()
	parsed, err := url.Parse(page.URL)
	if err != nil {
		t.Fatalf("parse test url: %v", err)
	}
	server := newTestServer(t, Config{
		Endpoint:     "http://provider.local",
		AllowedHosts: []string{parsed.Hostname()},
		AllowHTTP:    true,
	})

	data := invokeProvider(t, server, FetchCapabilityID, map[string]any{"action": "extract", "url": page.URL, "extract_text": true, "include_links": true}, http.StatusOK)
	output := data["output"].(map[string]any)
	if output["action"] != "extract" || output["status"].(float64) != 200 || output["title"] != "Docs" {
		t.Fatalf("output = %#v", output)
	}
	if !strings.Contains(output["text"].(string), "Hello Wendy") {
		t.Fatalf("text = %q", output["text"])
	}
	links := output["links"].([]any)
	if len(links) != 1 {
		t.Fatalf("links = %#v", links)
	}
	link := links[0].(map[string]any)
	if link["url"] != page.URL+"/next" || link["text"] != "Next page" {
		t.Fatalf("link = %#v", link)
	}
}

func TestFetchRejectsDisallowedHost(t *testing.T) {
	server := newTestServer(t, Config{Endpoint: "http://provider.local"})
	envelope := invokeProviderEnvelope(t, server, FetchCapabilityID, map[string]any{"url": "https://example.com"}, http.StatusBadRequest)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "validation_failed" {
		t.Fatalf("error = %#v", errObj)
	}
}

func TestFetchRejectsUnsupportedAction(t *testing.T) {
	server := newTestServer(t, Config{Endpoint: "http://provider.local"})
	envelope := invokeProviderEnvelope(t, server, FetchCapabilityID, map[string]any{"action": "click", "url": "http://localhost"}, http.StatusBadRequest)
	errObj := envelope["error"].(map[string]any)
	if errObj["code"] != "validation_failed" || !strings.Contains(errObj["message"].(string), "action") {
		t.Fatalf("error = %#v", errObj)
	}
}

func newTestServer(t *testing.T, cfg Config) http.Handler {
	t.Helper()
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("new browser search provider: %v", err)
	}
	return server
}

func invokeProvider(t *testing.T, handler http.Handler, capabilityID string, input map[string]any, wantStatus int) map[string]any {
	t.Helper()
	envelope := invokeProviderEnvelope(t, handler, capabilityID, input, wantStatus)
	if !envelope["ok"].(bool) {
		t.Fatalf("error envelope = %#v", envelope)
	}
	return envelope["data"].(map[string]any)
}

func invokeProviderEnvelope(t *testing.T, handler http.Handler, capabilityID string, input map[string]any, wantStatus int) map[string]any {
	t.Helper()
	var raw bytes.Buffer
	if err := json.NewEncoder(&raw).Encode(map[string]any{"input": input}); err != nil {
		t.Fatalf("encode invoke: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/provider/capabilities/"+capabilityID+"/invoke", &raw)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, wantStatus, rec.Body.String())
	}
	var envelope map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return envelope
}

func writeSearchIndex(t *testing.T, items []SearchItem) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.json")
	body, err := json.Marshal(map[string]any{"items": items})
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}
	return path
}
