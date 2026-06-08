package jobs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"pacp/internal/contracts"
)

func TestHTTPJobLifecycle(t *testing.T) {
	handler := NewHandler(NewStore())

	createResp := requestJSON(t, handler, http.MethodPost, "/v1/jobs", createRequest())
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	jobID := responseData(t, createResp)["job_id"].(string)

	claimResp := requestJSON(t, handler, http.MethodPost, "/v1/jobs/"+jobID+"/claim", contracts.JobClaimRequest{WorkerID: "runner_1", LeaseSeconds: 60})
	if claimResp.Code != http.StatusOK {
		t.Fatalf("claim status=%d body=%s", claimResp.Code, claimResp.Body.String())
	}

	heartbeatResp := requestJSON(t, handler, http.MethodPost, "/v1/jobs/"+jobID+"/heartbeat", contracts.JobHeartbeatRequest{WorkerID: "runner_1", TransitionTo: "running"})
	if heartbeatResp.Code != http.StatusOK {
		t.Fatalf("heartbeat status=%d body=%s", heartbeatResp.Code, heartbeatResp.Body.String())
	}

	logResp := requestJSON(t, handler, http.MethodPost, "/v1/jobs/"+jobID+"/logs", contracts.AppendJobLogRequest{
		WorkerID: "runner_1",
		Entries:  []contracts.JobLogEntry{{Timestamp: "2026-06-05T20:00:01Z", Level: "info", Message: "running"}},
	})
	if logResp.Code != http.StatusOK {
		t.Fatalf("log status=%d body=%s", logResp.Code, logResp.Body.String())
	}

	completeResp := requestJSON(t, handler, http.MethodPost, "/v1/jobs/"+jobID+"/complete", contracts.JobCompleteRequest{WorkerID: "runner_1", ArtifactRefs: []string{"art_1"}})
	if completeResp.Code != http.StatusOK {
		t.Fatalf("complete status=%d body=%s", completeResp.Code, completeResp.Body.String())
	}
	data := responseData(t, completeResp)
	if data["state"] != string(contracts.JobSucceeded) {
		t.Fatalf("state=%v", data["state"])
	}

	projectionResp := requestJSON(t, handler, http.MethodGet, "/v1/jobs/"+jobID+"/agent-projection", nil)
	if projectionResp.Code != http.StatusOK {
		t.Fatalf("projection status=%d body=%s", projectionResp.Code, projectionResp.Body.String())
	}
	projection := responseData(t, projectionResp)
	if _, hasMetadata := projection["metadata"]; hasMetadata {
		t.Fatalf("agent projection leaked metadata: %+v", projection)
	}
}

func TestHTTPErrors(t *testing.T) {
	handler := NewHandler(NewStore())
	resp := requestJSON(t, handler, http.MethodGet, "/v1/jobs/job_missing", nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestHTTPHealth(t *testing.T) {
	handler := NewHandler(NewStore())
	resp := requestJSON(t, handler, http.MethodGet, "/v1/jobs/health", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", resp.Code, resp.Body.String())
	}
	data := responseData(t, resp)
	details := data["details"].(map[string]any)
	if data["status"] != "healthy" || details["component"] != "jobs" {
		t.Fatalf("health = %#v", data)
	}
	if details["store_backend"] != "memory" || details["job_count"] != float64(0) || details["active_claim_count"] != float64(0) {
		t.Fatalf("health = %#v", data)
	}
}

func requestJSON(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func responseData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("data missing from envelope: %+v", envelope)
	}
	return data
}
