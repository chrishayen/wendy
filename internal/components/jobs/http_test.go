package jobs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"pacp/internal/contracts"
)

func TestHTTPJobLifecycle(t *testing.T) {
	handler := NewHandler(NewStore())

	createResp := requestJSON(t, handler, http.MethodPost, "/v1/jobs", createRequest(), map[string]string{"Idempotency-Key": "create-http-1"})
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
	createResp := requestJSON(t, handler, http.MethodPost, "/v1/jobs", createRequest())
	if createResp.Code != http.StatusBadRequest {
		t.Fatalf("create without idempotency status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	if code := responseErrorCode(t, createResp); code != "missing_idempotency_key" {
		t.Fatalf("create without idempotency code=%s", code)
	}

	resp := requestJSON(t, handler, http.MethodGet, "/v1/jobs/job_missing", nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	metrics := requestJSON(t, handler, http.MethodGet, "/v1/jobs/metrics", nil)
	data := responseData(t, metrics)
	assertMetric(t, data, "http_errors_total", map[string]string{"method": "GET", "route_group": "/v1/jobs/{job_id}", "status_class": "4xx"}, 1)
}

func TestHTTPCancelRequiresIdempotencyAndReplays(t *testing.T) {
	handler := NewHandler(NewStore())
	createResp := requestJSON(t, handler, http.MethodPost, "/v1/jobs", createRequest(), map[string]string{"Idempotency-Key": "create-http-cancel"})
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	jobID := responseData(t, createResp)["job_id"].(string)
	cancelReq := contracts.CancelRequest{Reason: "stop requested"}

	missingID := requestJSON(t, handler, http.MethodPost, "/v1/jobs/"+jobID+"/cancel", cancelReq)
	if missingID.Code != http.StatusBadRequest {
		t.Fatalf("cancel without idempotency status=%d body=%s", missingID.Code, missingID.Body.String())
	}
	if code := responseErrorCode(t, missingID); code != "missing_idempotency_key" {
		t.Fatalf("cancel without idempotency code=%s", code)
	}

	canceled := requestJSON(t, handler, http.MethodPost, "/v1/jobs/"+jobID+"/cancel", cancelReq, map[string]string{"Idempotency-Key": "cancel-http-1"})
	if canceled.Code != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", canceled.Code, canceled.Body.String())
	}
	if data := responseData(t, canceled); data["state"] != string(contracts.JobCanceled) || data["status_message"] != "stop requested" {
		t.Fatalf("canceled = %#v", data)
	}

	replay := requestJSON(t, handler, http.MethodPost, "/v1/jobs/"+jobID+"/cancel", cancelReq, map[string]string{"Idempotency-Key": "cancel-http-1"})
	if replay.Code != http.StatusOK {
		t.Fatalf("cancel replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	if data := responseData(t, replay); data["status_message"] != "stop requested" {
		t.Fatalf("replay = %#v", data)
	}

	conflict := requestJSON(t, handler, http.MethodPost, "/v1/jobs/"+jobID+"/cancel", contracts.CancelRequest{Reason: "different"}, map[string]string{"Idempotency-Key": "cancel-http-1"})
	if conflict.Code != http.StatusConflict {
		t.Fatalf("cancel conflict status=%d body=%s", conflict.Code, conflict.Body.String())
	}
	if code := responseErrorCode(t, conflict); code != "idempotency_conflict" {
		t.Fatalf("cancel conflict code=%s", code)
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

func TestHTTPMetrics(t *testing.T) {
	handler := NewHandler(NewStore())
	createResp := requestJSON(t, handler, http.MethodPost, "/v1/jobs", createRequest(), map[string]string{"Idempotency-Key": "create-http-metrics"})
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	resp := requestJSON(t, handler, http.MethodGet, "/v1/jobs/metrics", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("metrics status=%d body=%s", resp.Code, resp.Body.String())
	}
	data := responseData(t, resp)
	if data["component"] != "jobs" {
		t.Fatalf("metrics = %#v", data)
	}
	assertMetric(t, data, "jobs_by_state", map[string]string{"state": "queued"}, 1)
	assertMetric(t, data, "http_requests_total", map[string]string{"method": "POST", "route_group": "/v1/jobs", "status_class": "2xx"}, 1)
}

func TestHTTPMetricsReportsExpiredClaims(t *testing.T) {
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	store := NewStore()
	store.SetClock(func() time.Time { return now })
	handler := NewHandler(store)

	createResp := requestJSON(t, handler, http.MethodPost, "/v1/jobs", createRequest(), map[string]string{"Idempotency-Key": "create-http-expired-claims"})
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	jobID := responseData(t, createResp)["job_id"].(string)

	claimResp := requestJSON(t, handler, http.MethodPost, "/v1/jobs/"+jobID+"/claim", contracts.JobClaimRequest{WorkerID: "runner_1", LeaseSeconds: 1})
	if claimResp.Code != http.StatusOK {
		t.Fatalf("claim status=%d body=%s", claimResp.Code, claimResp.Body.String())
	}
	now = now.Add(2 * time.Second)

	resp := requestJSON(t, handler, http.MethodGet, "/v1/jobs/metrics", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("metrics status=%d body=%s", resp.Code, resp.Body.String())
	}
	data := responseData(t, resp)
	assertMetric(t, data, "jobs_active_claims", nil, 0)
	assertMetric(t, data, "jobs_expired_claims", nil, 1)
}

func requestJSON(t *testing.T, handler http.Handler, method, path string, body any, headers ...map[string]string) *httptest.ResponseRecorder {
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
	for _, group := range headers {
		for name, value := range group {
			req.Header.Set(name, value)
		}
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

func responseErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	errObj, ok := envelope["error"].(map[string]any)
	if !ok {
		t.Fatalf("error missing from envelope: %+v", envelope)
	}
	code, _ := errObj["code"].(string)
	return code
}

func assertMetric(t *testing.T, data map[string]any, name string, labels map[string]string, value float64) {
	t.Helper()
	for _, rawSample := range data["samples"].([]any) {
		sample := rawSample.(map[string]any)
		if sample["name"] != name {
			continue
		}
		if !labelsMatch(sample["labels"], labels) {
			continue
		}
		if sample["value"] != value {
			t.Fatalf("metric %s value=%#v want=%v", name, sample["value"], value)
		}
		return
	}
	t.Fatalf("metric %s labels=%#v not found in %#v", name, labels, data["samples"])
}

func labelsMatch(raw any, want map[string]string) bool {
	if len(want) == 0 {
		return raw == nil
	}
	labels, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	for key, value := range want {
		if labels[key] != value {
			return false
		}
	}
	return true
}
