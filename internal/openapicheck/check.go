package openapicheck

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Finding struct {
	File     string `json:"file"`
	Location string `json:"location,omitempty"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

type FileReport struct {
	Path       string    `json:"path"`
	OpenAPI    string    `json:"openapi,omitempty"`
	Operations int       `json:"operations"`
	Schemas    int       `json:"schemas"`
	References int       `json:"references"`
	Findings   []Finding `json:"findings,omitempty"`
}

type Report struct {
	Files      []FileReport `json:"files"`
	Findings   []Finding    `json:"findings,omitempty"`
	Operations int          `json:"operations"`
	Schemas    int          `json:"schemas"`
	References int          `json:"references"`
}

func (r Report) Passed() bool {
	return len(r.Findings) == 0
}

func (r FileReport) Passed() bool {
	return len(r.Findings) == 0
}

func ValidateFiles(paths []string) Report {
	report := Report{}
	for _, path := range paths {
		fileReport := ValidateFile(path)
		report.Files = append(report.Files, fileReport)
		report.Findings = append(report.Findings, fileReport.Findings...)
		report.Operations += fileReport.Operations
		report.Schemas += fileReport.Schemas
		report.References += fileReport.References
	}
	return report
}

func ValidateFile(path string) FileReport {
	report := FileReport{Path: path}
	raw, err := os.ReadFile(path)
	if err != nil {
		report.add("read_failed", "", err.Error())
		return report
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		report.add("yaml_invalid", "", err.Error())
		return report
	}
	openapi, _ := doc["openapi"].(string)
	report.OpenAPI = openapi
	if !strings.HasPrefix(openapi, "3.") {
		report.add("openapi_version_missing", "/openapi", "openapi 3.x version is required")
	}
	if _, ok := asMap(doc["info"]); !ok {
		report.add("info_missing", "/info", "info object is required")
	}
	paths, ok := asMap(doc["paths"])
	if !ok {
		report.add("paths_missing", "/paths", "paths object is required")
		return report
	}
	components, ok := asMap(doc["components"])
	if !ok {
		report.add("components_missing", "/components", "components object is required")
		return report
	}
	if schemas, ok := asMap(components["schemas"]); ok {
		report.Schemas = len(schemas)
	}
	report.References = validateReferences(&report, doc)
	validateSecurityRequirements(&report, doc, paths, components)
	validateOperations(&report, doc, paths)
	return report
}

func validateReferences(report *FileReport, doc map[string]any) int {
	count := 0
	walk(doc, "", func(location string, value any) {
		node, ok := asMap(value)
		if !ok {
			return
		}
		ref, ok := node["$ref"].(string)
		if !ok || ref == "" {
			return
		}
		count++
		if !strings.HasPrefix(ref, "#/") {
			report.add("external_ref_unsupported", location+"/$ref", "external refs are not supported: "+ref)
			return
		}
		if _, ok := resolveRef(doc, ref); !ok {
			report.add("unresolved_ref", location+"/$ref", "local ref does not resolve: "+ref)
		}
	})
	return count
}

func validateSecurityRequirements(report *FileReport, doc map[string]any, paths, components map[string]any) {
	securitySchemes, _ := asMap(components["securitySchemes"])
	validateSecurityRequirementList(report, "/security", doc["security"], securitySchemes)

	for pathName, pathValue := range paths {
		pathItem, ok := asMap(pathValue)
		if !ok {
			continue
		}
		for method, operationValue := range pathItem {
			if !isHTTPMethod(method) {
				continue
			}
			operation, ok := asMap(operationValue)
			if !ok {
				continue
			}
			if securityValue, exists := operation["security"]; exists {
				location := "/paths/" + escapePointer(pathName) + "/" + method + "/security"
				validateSecurityRequirementList(report, location, securityValue, securitySchemes)
			}
		}
	}
}

func validateSecurityRequirementList(report *FileReport, location string, raw any, securitySchemes map[string]any) {
	if raw == nil {
		return
	}
	requirements, ok := raw.([]any)
	if !ok {
		report.add("security_requirement_invalid", location, "security must be an array")
		return
	}
	for i, requirement := range requirements {
		requirementLocation := fmt.Sprintf("%s/%d", location, i)
		requirementMap, ok := asMap(requirement)
		if !ok {
			report.add("security_requirement_invalid", requirementLocation, "security requirement must be an object")
			continue
		}
		for schemeName, scopesRaw := range requirementMap {
			if _, ok := securitySchemes[schemeName]; !ok {
				report.add("security_scheme_unknown", requirementLocation+"/"+escapePointer(schemeName), "security scheme is not declared: "+schemeName)
			}
			if _, ok := scopesRaw.([]any); !ok {
				report.add("security_scopes_invalid", requirementLocation+"/"+escapePointer(schemeName), "security scheme scopes must be an array")
			}
		}
	}
}

func validateOperations(report *FileReport, doc map[string]any, paths map[string]any) {
	seenOperationIDs := map[string]string{}
	for pathName, pathValue := range paths {
		pathItem, ok := asMap(pathValue)
		if !ok {
			report.add("path_item_invalid", "/paths/"+escapePointer(pathName), "path item must be an object")
			continue
		}
		for method, operationValue := range pathItem {
			if !isHTTPMethod(method) {
				continue
			}
			location := "/paths/" + escapePointer(pathName) + "/" + method
			operation, ok := asMap(operationValue)
			if !ok {
				report.add("operation_invalid", location, "operation must be an object")
				continue
			}
			report.Operations++
			operationID, _ := operation["operationId"].(string)
			if operationID == "" {
				report.add("operation_id_missing", location, "operationId is required")
			} else if existing, ok := seenOperationIDs[operationID]; ok {
				report.add("operation_id_duplicate", location+"/operationId", fmt.Sprintf("operationId %q is already used at %s", operationID, existing))
			} else {
				seenOperationIDs[operationID] = location
			}
			validateOperationMetadata(report, location, operation)
			responses, ok := asMap(operation["responses"])
			if !ok {
				report.add("responses_missing", location+"/responses", "responses object is required")
				continue
			}
			defaultResponse, ok := responses["default"]
			if !ok {
				report.add("default_response_missing", location+"/responses", "default error response is required")
			} else if !responseUsesErrorEnvelope(doc, defaultResponse) {
				report.add("default_response_not_error_enveloped", location+"/responses/default", "default responses must use the standard error envelope")
			}
			for status, response := range responses {
				if !isSuccessStatus(status) {
					continue
				}
				if !responseUsesEnvelopeOrBinary(doc, response) {
					report.add("success_response_not_enveloped", location+"/responses/"+escapePointer(status), "2xx JSON responses must use a success envelope; binary responses must declare string/binary content")
				}
			}
		}
	}
}

func validateOperationMetadata(report *FileReport, location string, operation map[string]any) {
	validateStringOrStringList(report, location+"/x-operation-audience", operation["x-operation-audience"], "operation_audience_missing", "operation_audience_invalid", "x-operation-audience is required")
	validateStringOrStringList(report, location+"/x-policy-action", operation["x-policy-action"], "policy_action_missing", "policy_action_invalid", "x-policy-action is required")
}

func validateStringOrStringList(report *FileReport, location string, raw any, missingCode, invalidCode, missingMessage string) {
	switch typed := raw.(type) {
	case nil:
		report.add(missingCode, location, missingMessage)
	case string:
		if strings.TrimSpace(typed) == "" {
			report.add(invalidCode, location, "value must be a non-empty string or non-empty string array")
		}
	case []any:
		if len(typed) == 0 {
			report.add(invalidCode, location, "value must be a non-empty string or non-empty string array")
			return
		}
		for i, item := range typed {
			value, ok := item.(string)
			if !ok || strings.TrimSpace(value) == "" {
				report.add(invalidCode, fmt.Sprintf("%s/%d", location, i), "array items must be non-empty strings")
			}
		}
	default:
		report.add(invalidCode, location, "value must be a non-empty string or non-empty string array")
	}
}

func responseUsesEnvelopeOrBinary(doc map[string]any, response any) bool {
	return responseUsesEnvelopeOrBinaryDepth(doc, response, 0)
}

func responseUsesEnvelopeOrBinaryDepth(doc map[string]any, response any, depth int) bool {
	if depth > 8 {
		return false
	}
	responseMap, ok := asMap(response)
	if !ok {
		return false
	}
	if ref, _ := responseMap["$ref"].(string); ref != "" {
		resolved, ok := resolveRef(doc, ref)
		return ok && responseUsesEnvelopeOrBinaryDepth(doc, resolved, depth+1)
	}
	content, ok := asMap(responseMap["content"])
	if !ok {
		return false
	}
	if media, ok := asMap(content["application/json"]); ok {
		schema, ok := asMap(media["schema"])
		return ok && schemaContainsEnvelopeRef(doc, schema, depth+1)
	}
	for _, mediaValue := range content {
		media, ok := asMap(mediaValue)
		if !ok {
			continue
		}
		schema, ok := asMap(media["schema"])
		if ok && schemaIsBinary(schema) {
			return true
		}
	}
	return false
}

func schemaContainsEnvelopeRef(doc map[string]any, schema any, depth int) bool {
	if depth > 12 {
		return false
	}
	schemaMap, ok := asMap(schema)
	if !ok {
		return false
	}
	if ref, _ := schemaMap["$ref"].(string); ref != "" {
		if schemaRefName(ref) != "" && strings.HasSuffix(schemaRefName(ref), "Envelope") {
			return true
		}
		if resolved, ok := resolveRef(doc, ref); ok {
			return schemaContainsEnvelopeRef(doc, resolved, depth+1)
		}
	}
	for _, value := range schemaMap {
		switch typed := value.(type) {
		case []any:
			for _, item := range typed {
				if schemaContainsEnvelopeRef(doc, item, depth+1) {
					return true
				}
			}
		case map[string]any:
			if schemaContainsEnvelopeRef(doc, typed, depth+1) {
				return true
			}
		}
	}
	return false
}

func responseUsesErrorEnvelope(doc map[string]any, response any) bool {
	return responseUsesErrorEnvelopeDepth(doc, response, 0)
}

func responseUsesErrorEnvelopeDepth(doc map[string]any, response any, depth int) bool {
	if depth > 8 {
		return false
	}
	responseMap, ok := asMap(response)
	if !ok {
		return false
	}
	if ref, _ := responseMap["$ref"].(string); ref != "" {
		if schemaRefName(ref) == "ErrorEnvelope" {
			return true
		}
		resolved, ok := resolveRef(doc, ref)
		return ok && responseUsesErrorEnvelopeDepth(doc, resolved, depth+1)
	}
	content, ok := asMap(responseMap["content"])
	if !ok {
		return false
	}
	media, ok := asMap(content["application/json"])
	if !ok {
		return false
	}
	schema, ok := asMap(media["schema"])
	return ok && schemaContainsErrorEnvelopeRef(doc, schema, depth+1)
}

func schemaContainsErrorEnvelopeRef(doc map[string]any, schema any, depth int) bool {
	if depth > 12 {
		return false
	}
	schemaMap, ok := asMap(schema)
	if !ok {
		return false
	}
	if ref, _ := schemaMap["$ref"].(string); ref != "" {
		if schemaRefName(ref) == "ErrorEnvelope" {
			return true
		}
		if resolved, ok := resolveRef(doc, ref); ok {
			return schemaContainsErrorEnvelopeRef(doc, resolved, depth+1)
		}
	}
	for _, value := range schemaMap {
		switch typed := value.(type) {
		case []any:
			for _, item := range typed {
				if schemaContainsErrorEnvelopeRef(doc, item, depth+1) {
					return true
				}
			}
		case map[string]any:
			if schemaContainsErrorEnvelopeRef(doc, typed, depth+1) {
				return true
			}
		}
	}
	return false
}

func schemaIsBinary(schema map[string]any) bool {
	schemaType, _ := schema["type"].(string)
	format, _ := schema["format"].(string)
	return schemaType == "string" && format == "binary"
}

func schemaRefName(ref string) string {
	if !strings.HasPrefix(ref, "#/components/schemas/") {
		return ""
	}
	return strings.TrimPrefix(ref, "#/components/schemas/")
}

func resolveRef(doc any, ref string) (any, bool) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, false
	}
	current := doc
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		part = unescapePointer(part)
		switch typed := current.(type) {
		case map[string]any:
			next, ok := typed[part]
			if !ok {
				return nil, false
			}
			current = next
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, false
			}
			current = typed[index]
		default:
			return nil, false
		}
	}
	return current, true
}

func walk(value any, location string, visit func(string, any)) {
	visit(location, value)
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			walk(child, location+"/"+escapePointer(key), visit)
		}
	case []any:
		for index, child := range typed {
			walk(child, fmt.Sprintf("%s/%d", location, index), visit)
		}
	}
}

func asMap(value any) (map[string]any, bool) {
	typed, ok := value.(map[string]any)
	return typed, ok && typed != nil
}

func isHTTPMethod(value string) bool {
	switch value {
	case "get", "post", "put", "patch", "delete", "head", "options", "trace":
		return true
	default:
		return false
	}
}

func isSuccessStatus(value string) bool {
	return len(value) == 3 && value[0] == '2'
}

func escapePointer(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}

func unescapePointer(value string) string {
	value = strings.ReplaceAll(value, "~1", "/")
	return strings.ReplaceAll(value, "~0", "~")
}

func (r *FileReport) add(code, location, message string) {
	r.Findings = append(r.Findings, Finding{
		File:     r.Path,
		Location: location,
		Code:     code,
		Message:  message,
	})
}
