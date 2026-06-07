package testkit

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"pacp/internal/contracts"
)

type FixtureServer struct {
	packageDir string
	index      map[string]contracts.HTTPResponse
}

func NewFixtureServer(pkg FixturePackage) *FixtureServer {
	server := &FixtureServer{
		packageDir: filepath.Dir(pkg.AbsPath),
		index:      map[string]contracts.HTTPResponse{},
	}
	for _, fixture := range pkg.File.Fixtures {
		if fixture.Request == nil || fixture.Response == nil {
			continue
		}
		key := fixture.Request.Method + " " + fixture.Request.Path
		if _, exists := server.index[key]; !exists {
			server.index[key] = *fixture.Response
		}
	}
	return server
}

func (s *FixtureServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := r.Method + " " + r.URL.Path
	response, ok := s.index[key]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"ok": false,
			"error": map[string]any{
				"code": "fixture_not_found", "message": "no fixture matches request", "retryable": false,
			},
			"links": map[string]any{},
			"meta":  map[string]any{"request_id": "req_fixture_not_found", "schema_version": "v1"},
		})
		return
	}

	status := http.StatusOK
	if response.Status != nil {
		status = *response.Status
	}
	for key, value := range response.Headers {
		w.Header().Set(key, value)
	}

	if response.BodyFixture != "" {
		bytes, err := readBase64Fixture(filepath.Join(s.packageDir, response.BodyFixture))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"ok": false,
				"error": map[string]any{
					"code": "fixture_read_failed", "message": err.Error(), "retryable": false,
				},
				"links": map[string]any{},
				"meta":  map[string]any{"request_id": "req_fixture_read_failed", "schema_version": "v1"},
			})
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write(bytes)
		return
	}

	writeJSON(w, status, response.Body)
}

func readBase64Fixture(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(string(raw))
}

func writeJSON(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
