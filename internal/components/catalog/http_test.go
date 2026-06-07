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
	store, err := NewS003Store()
	if err != nil {
		t.Fatalf("seed store: %v", err)
	}
	handler := NewHandler(store)
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
	body := marshalReader(t, S003Manifest())
	req := httptest.NewRequest(http.MethodPost, "/v1/catalog/manifests", body)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
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
	req := httptest.NewRequest(fixture.Request.Method, fixture.Request.Path, nil)
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
