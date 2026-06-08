package noderegistry

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pacp/internal/contracts"
)

func TestHandlerNodeRegistryLifecycle(t *testing.T) {
	store := NewStore()
	handler := NewHandler(store)

	health := doJSON(t, handler, http.MethodGet, "/v1/node-registry/health", nil, http.StatusOK)
	if health["status"] != "healthy" {
		t.Fatalf("health = %#v", health)
	}

	registered := doJSON(t, handler, http.MethodPost, "/v1/node-registry/nodes", contracts.RegisterNodeRequest{
		NodeID: "node_linux_gpu",
		URL:    "http://linux-box:18087",
	}, http.StatusOK)
	if registered["trust_state"] != contracts.NodeTrustUntrusted {
		t.Fatalf("registered = %#v", registered)
	}

	trusted := doJSON(t, handler, http.MethodPost, "/v1/node-registry/nodes/node_linux_gpu/trust", contracts.UpdateNodeTrustRequest{
		TrustState: contracts.NodeTrustTrusted,
	}, http.StatusOK)
	if trusted["trust_state"] != contracts.NodeTrustTrusted {
		t.Fatalf("trusted = %#v", trusted)
	}

	reachable := doJSON(t, handler, http.MethodPost, "/v1/node-registry/nodes/node_linux_gpu/heartbeat", contracts.NodeHeartbeatRequest{
		Status: contracts.NodeStatusReachable,
	}, http.StatusOK)
	if reachable["status"] != contracts.NodeStatusReachable || reachable["last_seen_at"] == "" {
		t.Fatalf("reachable = %#v", reachable)
	}

	list := doJSON(t, handler, http.MethodGet, "/v1/node-registry/nodes", nil, http.StatusOK)
	items := list["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
}

func TestHandlerNodeRegistryPaginationAndErrors(t *testing.T) {
	store := NewStore()
	for _, nodeID := range []string{"node_b", "node_a"} {
		if _, err := store.Register(contracts.RegisterNodeRequest{NodeID: nodeID, URL: "http://" + strings.ReplaceAll(nodeID, "_", "-") + ".local:18087"}); err != nil {
			t.Fatalf("register %s: %v", nodeID, err)
		}
	}
	handler := NewHandler(store)

	first := doJSON(t, handler, http.MethodGet, "/v1/node-registry/nodes?limit=1", nil, http.StatusOK)
	items := first["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["node_id"] != "node_a" || first["next_cursor"] == nil {
		t.Fatalf("first page = %#v", first)
	}
	cursor := first["next_cursor"].(string)
	second := doJSON(t, handler, http.MethodGet, "/v1/node-registry/nodes?limit=1&cursor="+cursor, nil, http.StatusOK)
	items = second["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["node_id"] != "node_b" {
		t.Fatalf("second page = %#v", second)
	}
	invalidCursor := doJSONEnvelope(t, handler, http.MethodGet, "/v1/node-registry/nodes?cursor=cursor_jobs_000001", nil, http.StatusBadRequest)
	errObj := invalidCursor["error"].(map[string]any)
	if errObj["code"] != "invalid_cursor" {
		t.Fatalf("invalid cursor = %#v", errObj)
	}
	missing := doJSONEnvelope(t, handler, http.MethodGet, "/v1/node-registry/nodes/no_such_node", nil, http.StatusNotFound)
	errObj = missing["error"].(map[string]any)
	if errObj["code"] != "not_found" {
		t.Fatalf("missing = %#v", errObj)
	}
}

func doJSON(t *testing.T, handler http.Handler, method, path string, body any, wantStatus int) map[string]any {
	t.Helper()
	envelope := doJSONEnvelope(t, handler, method, path, body, wantStatus)
	if !envelope["ok"].(bool) {
		t.Fatalf("error response for %s %s: %#v", method, path, envelope)
	}
	return envelope["data"].(map[string]any)
}

func doJSONEnvelope(t *testing.T, handler http.Handler, method, path string, body any, wantStatus int) map[string]any {
	t.Helper()
	var raw bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&raw).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &raw)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, rec.Code, wantStatus, rec.Body.String())
	}
	var envelope map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return envelope
}
