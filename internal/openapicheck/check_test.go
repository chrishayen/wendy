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
	doc := loadOpenAPIDoc(t, path)
	post := openAPIOperation(t, doc, "/v1/agent/jobs/{job_id}/cancel", "post")
	summary, _ := post["summary"].(string)
	normalized := strings.ToLower(summary)
	if !strings.Contains(normalized, "queued job") {
		t.Fatalf("cancel summary does not document queued-only cancellation: %q", summary)
	}
	if strings.Contains(normalized, "claimed") || strings.Contains(normalized, "running") {
		t.Fatalf("cancel summary advertises unsupported cancellation states: %q", summary)
	}
}

func TestComponentServiceAudienceMetadataForRouteAwareAuth(t *testing.T) {
	path := filepath.Join("..", "..", "openapi", "component-services.v1.yaml")
	doc := loadOpenAPIDoc(t, path)

	assertOperationAudience(t, doc, "/v1/catalog/manifests", "post", []string{"component"})
	assertOperationAudience(t, doc, "/v1/catalog/capabilities/{capability_id}/route", "get", []string{"component"})
	assertOperationAudience(t, doc, "/v1/jobs", "get", []string{"component", "worker"})
	assertOperationAudience(t, doc, "/v1/jobs/{job_id}", "get", []string{"component", "worker"})
	assertOperationAudience(t, doc, "/v1/resources", "post", []string{"component"})
	assertOperationAudience(t, doc, "/v1/lease-requests", "get", []string{"component", "worker"})
	assertOperationAudience(t, doc, "/v1/lease-requests", "post", []string{"worker"})
	assertOperationAudience(t, doc, "/v1/leases/{lease_id}/release", "post", []string{"worker"})
	assertOperationAudience(t, doc, "/v1/artifacts/register-local", "post", []string{"worker"})
	assertOperationAudience(t, doc, "/v1/artifacts/{artifact_id}/content", "get", []string{"component"})
	assertOperationPolicyAction(t, doc, "/v1/catalog/manifests", "post", "catalog.register")
	assertOperationPolicyAction(t, doc, "/v1/catalog/capabilities/{capability_id}/route", "get", "catalog.route.read")
	assertOperationPolicyAction(t, doc, "/v1/resources", "post", "lease.resource.register")
	assertOperationPolicyAction(t, doc, "/v1/lease-requests", "get", "lease.read")
	assertOperationPolicyAction(t, doc, "/v1/lease-requests", "post", "lease.request")
	assertOperationPolicyAction(t, doc, "/v1/leases/{lease_id}/release", "post", "lease.release")
	assertOperationPolicyAction(t, doc, "/v1/artifacts/register-local", "post", "artifact.register")
	assertOperationPolicyAction(t, doc, "/v1/artifacts/{artifact_id}/content", "get", "artifact.read")
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
      x-operation-audience: component
      x-policy-action: test.read
      responses:
        "200":
          $ref: "#/components/responses/Success"
        default:
          $ref: "#/components/responses/Error"
  /two:
    get:
      operationId: duplicate
      x-operation-audience: component
      x-policy-action: test.read
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
      x-operation-audience: component
      x-policy-action: test.read
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
      x-operation-audience: component
      x-policy-action: test.read
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

func TestValidateFileDetectsRawDefaultErrorResponse(t *testing.T) {
	path := writeContract(t, `
openapi: 3.1.0
info:
  title: Raw default
  version: v1
paths:
  /one:
    get:
      operationId: one
      x-operation-audience: component
      x-policy-action: test.read
      responses:
        "200":
          $ref: "#/components/responses/Success"
        default:
          description: Raw error response.
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/RawError"
components:
  responses:
    Success:
      description: Success.
      content:
        application/json:
          schema:
            $ref: "#/components/schemas/SuccessEnvelope"
  schemas:
    SuccessEnvelope:
      type: object
    RawError:
      type: object
`)
	report := ValidateFile(path)
	if !hasFinding(report, "default_response_not_error_enveloped") {
		t.Fatalf("expected default error envelope finding, got %#v", report.Findings)
	}
}

func TestValidateFileDetectsUnknownSecurityScheme(t *testing.T) {
	path := writeContract(t, `
openapi: 3.1.0
info:
  title: Unknown security
  version: v1
security:
  - typoAuth: []
paths:
  /one:
    get:
      operationId: one
      x-operation-audience: component
      x-policy-action: test.read
      responses:
        "200":
          $ref: "#/components/responses/Success"
        default:
          $ref: "#/components/responses/Error"
components:
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
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
	if !hasFinding(report, "security_scheme_unknown") {
		t.Fatalf("expected unknown security scheme finding, got %#v", report.Findings)
	}
}

func TestValidateFileDetectsInvalidSecurityScopes(t *testing.T) {
	path := writeContract(t, `
openapi: 3.1.0
info:
  title: Bad security scopes
  version: v1
paths:
  /one:
    get:
      operationId: one
      x-operation-audience: component
      x-policy-action: test.read
      security:
        - bearerAuth: read
      responses:
        "200":
          $ref: "#/components/responses/Success"
        default:
          $ref: "#/components/responses/Error"
components:
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
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
	if !hasFinding(report, "security_scopes_invalid") {
		t.Fatalf("expected invalid security scopes finding, got %#v", report.Findings)
	}
}

func TestValidateFileDetectsMissingOperationMetadata(t *testing.T) {
	path := writeContract(t, `
openapi: 3.1.0
info:
  title: Missing metadata
  version: v1
paths:
  /one:
    get:
      operationId: one
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
	if !hasFinding(report, "operation_audience_missing") {
		t.Fatalf("expected missing audience finding, got %#v", report.Findings)
	}
	if !hasFinding(report, "policy_action_missing") {
		t.Fatalf("expected missing policy action finding, got %#v", report.Findings)
	}
}

func TestValidateFileDetectsInvalidOperationMetadata(t *testing.T) {
	path := writeContract(t, `
openapi: 3.1.0
info:
  title: Invalid metadata
  version: v1
paths:
  /one:
    get:
      operationId: one
      x-operation-audience: ["component", ""]
      x-policy-action: []
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
	if !hasFinding(report, "operation_audience_invalid") {
		t.Fatalf("expected invalid audience finding, got %#v", report.Findings)
	}
	if !hasFinding(report, "policy_action_invalid") {
		t.Fatalf("expected invalid policy action finding, got %#v", report.Findings)
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
      x-operation-audience: component
      x-policy-action: artifact.read
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

func loadOpenAPIDoc(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read OpenAPI: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse OpenAPI: %v", err)
	}
	return doc
}

func openAPIOperation(t *testing.T, doc map[string]any, path, method string) map[string]any {
	t.Helper()
	paths, _ := asMap(doc["paths"])
	pathItem, _ := asMap(paths[path])
	operation, _ := asMap(pathItem[method])
	if operation == nil {
		t.Fatalf("operation %s %s not found", method, path)
	}
	return operation
}

func assertOperationAudience(t *testing.T, doc map[string]any, path, method string, want []string) {
	t.Helper()
	operation := openAPIOperation(t, doc, path, method)
	got := stringValues(operation["x-operation-audience"])
	if !sameStringSet(got, want) {
		t.Fatalf("%s %s audience=%#v want=%#v", method, path, got, want)
	}
}

func assertOperationPolicyAction(t *testing.T, doc map[string]any, path, method, want string) {
	t.Helper()
	operation := openAPIOperation(t, doc, path, method)
	got, _ := operation["x-policy-action"].(string)
	if got != want {
		t.Fatalf("%s %s policy action=%q want=%q", method, path, got, want)
	}
}

func stringValues(raw any) []string {
	switch typed := raw.(type) {
	case string:
		return []string{typed}
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			value, _ := item.(string)
			if value != "" {
				values = append(values, value)
			}
		}
		return values
	default:
		return nil
	}
}

func sameStringSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	counts := map[string]int{}
	for _, value := range got {
		counts[value]++
	}
	for _, value := range want {
		if counts[value] == 0 {
			return false
		}
		counts[value]--
	}
	return true
}

func hasFinding(report FileReport, code string) bool {
	for _, finding := range report.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
