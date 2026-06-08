package catalog

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"testing"

	"pacp/internal/contracts"
	"pacp/internal/testkit"
)

func TestHTTPMatchesS003CatalogFixtures(t *testing.T) {
	handler := NewHandler(sampleStore(t))
	fixturePackage := loadCatalogFixturePackage(t)

	for _, fixture := range fixturePackage.File.Fixtures {
		if fixture.Request == nil || fixture.Response == nil {
			continue
		}
		t.Run(fixture.ID, func(t *testing.T) {
			req := requestFromFixture(t, fixture)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			resp := rec.Result()
			defer resp.Body.Close()
			if fixture.Response.Status == nil {
				t.Fatalf("fixture missing status")
			}
			if resp.StatusCode != *fixture.Response.Status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, *fixture.Response.Status)
			}

			var got map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			normalizeMeta(got)
			want := normalizeMap(fixture.Response.Body)
			normalizeMeta(want)
			if !reflect.DeepEqual(got, want) {
				gotJSON, _ := json.MarshalIndent(got, "", "  ")
				wantJSON, _ := json.MarshalIndent(want, "", "  ")
				t.Fatalf("body mismatch\ngot: %s\nwant: %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestRegisterManifestHTTP(t *testing.T) {
	handler := NewHandler(NewStore())
	body := marshalReader(t, sampleManifest(t))
	req := httptest.NewRequest(http.MethodPost, "/v1/catalog/manifests", body)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHealthHTTP(t *testing.T) {
	handler := NewHandler(NewStore())
	req := httptest.NewRequest(http.MethodGet, "/v1/catalog/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	data := decodeData(t, rec.Body)
	details := data["details"].(map[string]any)
	if data["status"] != "healthy" || details["component"] != "catalog" {
		t.Fatalf("health = %#v", data)
	}
	if details["store_backend"] != "memory" || details["service_count"] != float64(0) || details["capability_count"] != float64(0) {
		t.Fatalf("health = %#v", data)
	}
}

func TestMetricsHTTP(t *testing.T) {
	handler := NewHandler(NewStore())
	healthReq := httptest.NewRequest(http.MethodGet, "/v1/catalog/health", nil)
	healthRec := httptest.NewRecorder()
	handler.ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200; body=%s", healthRec.Code, healthRec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/catalog/metrics", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	data := decodeData(t, rec.Body)
	if data["component"] != "catalog" {
		t.Fatalf("metrics = %#v", data)
	}
	assertMetric(t, data, "catalog_capabilities_total", nil, 0)
	assertMetric(t, data, "http_requests_total", map[string]string{"method": "GET", "route_group": "/v1/catalog/health", "status_class": "2xx"}, 1)
}

func TestExportHTTP(t *testing.T) {
	handler := NewHandler(sampleStore(t))
	req := httptest.NewRequest(http.MethodGet, "/v1/catalog/export", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	data := decodeData(t, rec.Body)
	manifests := data["manifests"].([]any)
	if data["schema_version"] != "v1" || len(manifests) != 1 {
		t.Fatalf("export = %#v", data)
	}
}

func TestListServicesHTTPPaginates(t *testing.T) {
	store := sampleStore(t)
	manifest := sampleManifest(t)
	manifest.Service.ID = "svc_catalog_second"
	manifest.Service.Name = "Second Catalog Provider"
	manifest.Provider.Endpoint = "http://second-provider.local:18088"
	manifest.Capabilities[0].ID = "cap_catalog_second"
	if _, err := store.RegisterManifest(manifest); err != nil {
		t.Fatalf("register second manifest: %v", err)
	}
	handler := NewHandler(store)

	first := requestCatalogData(t, handler, "/v1/catalog/services?limit=1", http.StatusOK)
	items := first["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != "svc_catalog_second" {
		t.Fatalf("first services page = %#v", first)
	}
	cursor, ok := first["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("first services page missing cursor = %#v", first)
	}

	second := requestCatalogData(t, handler, "/v1/catalog/services?limit=1&cursor="+cursor, http.StatusOK)
	items = second["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != "svc_comfyui_gpu" || second["next_cursor"] != nil {
		t.Fatalf("second services page = %#v", second)
	}

	invalidLimit := requestCatalogEnvelope(t, handler, "/v1/catalog/services?limit=0", http.StatusBadRequest)
	if invalidLimit["error"].(map[string]any)["code"] != "validation_failed" {
		t.Fatalf("invalid service limit error = %#v", invalidLimit)
	}
	invalidCursor := requestCatalogEnvelope(t, handler, "/v1/catalog/services?cursor=cursor_catalog_tags_000001", http.StatusBadRequest)
	if invalidCursor["error"].(map[string]any)["code"] != "invalid_cursor" {
		t.Fatalf("invalid service cursor error = %#v", invalidCursor)
	}
}

func TestListTagsHTTPPaginates(t *testing.T) {
	handler := NewHandler(sampleStore(t))

	first := requestCatalogData(t, handler, "/v1/catalog/tags?limit=1", http.StatusOK)
	items := first["items"].([]any)
	if len(items) != 1 || items[0] != "gpu" {
		t.Fatalf("first tags page = %#v", first)
	}
	cursor, ok := first["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("first tags page missing cursor = %#v", first)
	}

	second := requestCatalogData(t, handler, "/v1/catalog/tags?limit=1&cursor="+cursor, http.StatusOK)
	items = second["items"].([]any)
	if len(items) != 1 || items[0] != "image" || second["next_cursor"] != nil {
		t.Fatalf("second tags page = %#v", second)
	}

	invalidLimit := requestCatalogEnvelope(t, handler, "/v1/catalog/tags?limit=0", http.StatusBadRequest)
	if invalidLimit["error"].(map[string]any)["code"] != "validation_failed" {
		t.Fatalf("invalid tag limit error = %#v", invalidLimit)
	}
	invalidCursor := requestCatalogEnvelope(t, handler, "/v1/catalog/tags?cursor=cursor_catalog_services_000001", http.StatusBadRequest)
	if invalidCursor["error"].(map[string]any)["code"] != "invalid_cursor" {
		t.Fatalf("invalid tag cursor error = %#v", invalidCursor)
	}
}

func TestLoadManifestsFromDirectory(t *testing.T) {
	manifests, err := LoadManifests(filepath.Join("..", "..", "..", "testdata", "manifests"))
	if err != nil {
		t.Fatalf("load manifests: %v", err)
	}
	serviceIDs := map[string]bool{}
	for _, manifest := range manifests {
		serviceIDs[manifest.Service.ID] = true
	}
	for _, serviceID := range []string{"svc_comfyui_gpu", "svc_sample_comfyui_gpu"} {
		if !serviceIDs[serviceID] {
			t.Fatalf("loaded service ids = %#v, missing %s", serviceIDs, serviceID)
		}
	}
}

func decodeData(t *testing.T, body io.Reader) map[string]any {
	t.Helper()
	var envelope map[string]any
	if err := json.NewDecoder(body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("data missing from envelope: %#v", envelope)
	}
	return data
}

func requestCatalogData(t *testing.T, handler http.Handler, path string, wantStatus int) map[string]any {
	t.Helper()
	envelope := requestCatalogEnvelope(t, handler, path, wantStatus)
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("data missing from envelope: %#v", envelope)
	}
	return data
}

func requestCatalogEnvelope(t *testing.T, handler http.Handler, path string, wantStatus int) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("GET %s status=%d want=%d body=%s", path, rec.Code, wantStatus, rec.Body.String())
	}
	var envelope map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return envelope
}

func loadCatalogFixturePackage(t *testing.T) testkit.FixturePackage {
	t.Helper()
	scenario, err := testkit.LoadScenario(filepath.Join("..", "..", "..", "testdata", "contract-sim"), filepath.Join("fixtures", "S003", "manifest.json"))
	if err != nil {
		t.Fatalf("load scenario: %v", err)
	}
	pkg, ok := testkit.FindPackage(scenario, "c03-service-catalog")
	if !ok {
		t.Fatalf("catalog fixture package not found")
	}
	return pkg
}

func requestFromFixture(t *testing.T, fixture contracts.Fixture) *http.Request {
	t.Helper()
	path := fixture.Request.Path
	if fixture.Request.WireQuery != "" {
		path += "?" + fixture.Request.WireQuery
	}
	req := httptest.NewRequest(fixture.Request.Method, path, nil)
	if fixture.Request.WireQuery != "" {
		return req
	}
	query := url.Values{}
	for key, value := range fixture.Request.Query {
		switch typed := value.(type) {
		case string:
			query.Set(key, typed)
		case []any:
			for _, item := range typed {
				if text, ok := item.(string); ok {
					query.Add(key, text)
				}
			}
		default:
			t.Fatalf("unsupported query fixture type for %s", key)
		}
	}
	req.URL.RawQuery = query.Encode()
	return req
}

func normalizeMap(value map[string]any) map[string]any {
	raw, _ := json.Marshal(value)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func normalizeMeta(body map[string]any) {
	meta, ok := body["meta"].(map[string]any)
	if !ok {
		return
	}
	meta["request_id"] = "normalized"
}

func marshalReader(t *testing.T, value any) io.Reader {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	return bytes.NewReader(raw)
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
