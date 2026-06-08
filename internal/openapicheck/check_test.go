package openapicheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestValidateRepositoryOpenAPIContracts(t *testing.T) {
	report := ValidateFiles([]string{
		filepath.Join("..", "..", "openapi", "public-gateway.v1.yaml"),
		filepath.Join("..", "..", "openapi", "component-services.v1.yaml"),
	})
	if !report.Passed() {
		t.Fatalf("findings = %#v", report.Findings)
	}
	if report.Operations == 0 || report.Schemas == 0 || report.References == 0 {
		t.Fatalf("report did not count contract contents: %#v", report)
	}
}

func TestPublicGatewayCancelSummaryDocumentsQueuedOnly(t *testing.T) {
	path := filepath.Join("..", "..", "openapi", "public-gateway.v1.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read public gateway OpenAPI: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse public gateway OpenAPI: %v", err)
	}
	paths, _ := asMap(doc["paths"])
	pathItem, _ := asMap(paths["/v1/agent/jobs/{job_id}/cancel"])
	post, _ := asMap(pathItem["post"])
	summary, _ := post["summary"].(string)
	normalized := strings.ToLower(summary)
	if !strings.Contains(normalized, "queued job") {
		t.Fatalf("cancel summary does not document queued-only cancellation: %q", summary)
	}
	if strings.Contains(normalized, "claimed") || strings.Contains(normalized, "running") {
		t.Fatalf("cancel summary advertises unsupported cancellation states: %q", summary)
	}
}

func TestValidateFileDetectsDuplicateOperationID(t *testing.T) {
	path := writeContract(t, `
openapi: 3.1.0
info:
  title: Duplicate operation id
  version: v1
paths:
  /one:
    get:
      operationId: duplicate
      responses:
        "200":
          $ref: "#/components/responses/Success"
        default:
          $ref: "#/components/responses/Error"
  /two:
    get:
      operationId: duplicate
      responses:
        "200":
          $ref: "#/components/responses/Success"
        default:
          $ref: "#/components/responses/Error"
components:
  responses:
    Success:
      description: Success.
      content:
        application/json:
          schema:
            $ref: "#/components/schemas/SuccessEnvelope"
    Error:
      description: Error.
      content:
        application/json:
          schema:
            $ref: "#/components/schemas/ErrorEnvelope"
  schemas:
    SuccessEnvelope:
      type: object
    ErrorEnvelope:
      type: object
`)
	report := ValidateFile(path)
	if !hasFinding(report, "operation_id_duplicate") {
		t.Fatalf("expected duplicate operation id finding, got %#v", report.Findings)
	}
}

func TestValidateFileDetectsUnresolvedRef(t *testing.T) {
	path := writeContract(t, `
openapi: 3.1.0
info:
  title: Bad ref
  version: v1
paths:
  /one:
    get:
      operationId: one
      responses:
        "200":
          $ref: "#/components/responses/Missing"
        default:
          $ref: "#/components/responses/Error"
components:
  responses:
    Error:
      description: Error.
      content:
        application/json:
          schema:
            $ref: "#/components/schemas/ErrorEnvelope"
  schemas:
    ErrorEnvelope:
      type: object
`)
	report := ValidateFile(path)
	if !hasFinding(report, "unresolved_ref") {
		t.Fatalf("expected unresolved ref finding, got %#v", report.Findings)
	}
}

func TestValidateFileDetectsNonEnvelopeSuccessResponse(t *testing.T) {
	path := writeContract(t, `
openapi: 3.1.0
info:
  title: Raw success
  version: v1
paths:
  /one:
    get:
      operationId: one
      responses:
        "200":
          description: Raw response.
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/RawObject"
        default:
          $ref: "#/components/responses/Error"
components:
  responses:
    Error:
      description: Error.
      content:
        application/json:
          schema:
            $ref: "#/components/schemas/ErrorEnvelope"
  schemas:
    RawObject:
      type: object
    ErrorEnvelope:
      type: object
`)
	report := ValidateFile(path)
	if !hasFinding(report, "success_response_not_enveloped") {
		t.Fatalf("expected envelope finding, got %#v", report.Findings)
	}
}

func TestValidateFileAllowsBinarySuccessResponse(t *testing.T) {
	path := writeContract(t, `
openapi: 3.1.0
info:
  title: Binary
  version: v1
paths:
  /content:
    get:
      operationId: readContent
      responses:
        "200":
          description: Bytes.
          content:
            "*/*":
              schema:
                type: string
                format: binary
        default:
          $ref: "#/components/responses/Error"
components:
  responses:
    Error:
      description: Error.
      content:
        application/json:
          schema:
            $ref: "#/components/schemas/ErrorEnvelope"
  schemas:
    ErrorEnvelope:
      type: object
`)
	report := ValidateFile(path)
	if !report.Passed() {
		t.Fatalf("findings = %#v", report.Findings)
	}
}

func writeContract(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "openapi.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write contract: %v", err)
	}
	return path
}

func hasFinding(report FileReport, code string) bool {
	for _, finding := range report.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
