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
	validateServerURLs(&report, doc)
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
	validateSuccessResponseTyping(&report, paths, components)
	validateCompatibilityPolicy(&report, doc, paths, components)
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

func validateServerURLs(report *FileReport, doc map[string]any) {
	servers, ok := doc["servers"].([]any)
	if !ok {
		return
	}
	for i, serverValue := range servers {
		location := fmt.Sprintf("/servers/%d", i)
		server, ok := asMap(serverValue)
		if !ok {
			report.add("server_invalid", location, "server entries must be objects")
			continue
		}
		url, _ := server["url"].(string)
		if isLocalhostURL(url) {
			report.add("server_url_localhost", location+"/url", "OpenAPI server URLs must be deployment-neutral; keep localhost URLs in development docs")
		}
		variables, _ := asMap(server["variables"])
		for variableName, variableValue := range variables {
			variable, ok := asMap(variableValue)
			if !ok {
				continue
			}
			defaultValue, _ := variable["default"].(string)
			if isLocalhostURL(defaultValue) {
				report.add("server_url_localhost", location+"/variables/"+escapePointer(variableName)+"/default", "OpenAPI server variable defaults must be deployment-neutral; keep localhost URLs in development docs")
			}
		}
	}
}

func isLocalhostURL(value string) bool {
	normalized := strings.ToLower(value)
	return strings.Contains(normalized, "localhost") ||
		strings.Contains(normalized, "127.0.0.1") ||
		strings.Contains(normalized, "[::1]") ||
		strings.Contains(normalized, "//::1")
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

func validateSuccessResponseTyping(report *FileReport, paths, components map[string]any) {
	responses, _ := asMap(components["responses"])
	for responseName, responseValue := range responses {
		location := "/components/responses/" + escapePointer(responseName)
		if responseName == "Success" {
			report.add("generic_success_response_component", location, "generic Success response components are not allowed; declare a typed, operation-specific response")
		}
		if responseUsesDirectGenericSuccessEnvelope(responseValue) {
			report.add("generic_success_response_schema", location, "success response components must specialize the data schema instead of returning the base SuccessEnvelope directly")
		}
	}

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
			responses, ok := asMap(operation["responses"])
			if !ok {
				continue
			}
			for status, responseValue := range responses {
				if !isSuccessStatus(status) {
					continue
				}
				location := "/paths/" + escapePointer(pathName) + "/" + method + "/responses/" + escapePointer(status)
				if ref, _ := responseRef(responseValue); ref == "#/components/responses/Success" {
					report.add("generic_success_response_ref", location+"/$ref", "2xx responses must reference a typed response component, not the generic Success response")
					continue
				}
				if responseUsesInlineBaseSuccessEnvelope(responseValue) {
					report.add("anonymous_success_response_schema", location, "2xx JSON success responses must use named typed envelopes or named typed response components")
				}
			}
		}
	}
}

func validateCompatibilityPolicy(report *FileReport, doc map[string]any, paths, components map[string]any) {
	validatePathVersioning(report, paths)
	validateEnvelopeCompatibility(report, components)
	validateAdditiveCompatibility(report, components)
	validateDeprecationWindows(report, doc)
}

func validatePathVersioning(report *FileReport, paths map[string]any) {
	for pathName := range paths {
		if pathName != "/v1" && !strings.HasPrefix(pathName, "/v1/") {
			report.add("path_version_missing", "/paths/"+escapePointer(pathName), "public API paths must use /v1 path versioning")
		}
	}
}

func validateEnvelopeCompatibility(report *FileReport, components map[string]any) {
	schemas, ok := asMap(components["schemas"])
	if !ok {
		return
	}
	meta, ok := asMap(schemas["Meta"])
	if !ok {
		report.add("meta_schema_missing", "/components/schemas/Meta", "Meta schema is required so responses can declare schema_version")
		return
	}
	if !requiredContains(meta, "schema_version") {
		report.add("schema_version_missing", "/components/schemas/Meta/required", "Meta schema must require schema_version")
	}
	metaProperties, _ := asMap(meta["properties"])
	schemaVersion, ok := asMap(metaProperties["schema_version"])
	if !ok {
		report.add("schema_version_property_missing", "/components/schemas/Meta/properties/schema_version", "Meta schema must define schema_version")
	} else if schemaType, _ := schemaVersion["type"].(string); schemaType != "" && schemaType != "string" {
		report.add("schema_version_type_invalid", "/components/schemas/Meta/properties/schema_version/type", "schema_version must be documented as a string")
	}

	for schemaName, schemaValue := range schemas {
		if !strings.HasSuffix(schemaName, "Envelope") {
			continue
		}
		schema, ok := asMap(schemaValue)
		if !ok {
			continue
		}
		location := "/components/schemas/" + escapePointer(schemaName)
		if !requiredContains(schema, "meta") {
			report.add("envelope_meta_missing", location+"/required", "response envelope schemas must require meta")
		}
		properties, _ := asMap(schema["properties"])
		metaProperty, ok := asMap(properties["meta"])
		if !ok {
			report.add("envelope_meta_property_missing", location+"/properties/meta", "response envelope schemas must define meta")
			continue
		}
		if ref, _ := metaProperty["$ref"].(string); ref != "#/components/schemas/Meta" {
			report.add("envelope_meta_not_standard", location+"/properties/meta", "response envelope meta must reference the shared Meta schema")
		}
	}
}

func validateAdditiveCompatibility(report *FileReport, components map[string]any) {
	schemas, ok := asMap(components["schemas"])
	if !ok {
		return
	}
	walk(schemas, "/components/schemas", func(location string, value any) {
		node, ok := asMap(value)
		if !ok {
			return
		}
		if additionalProperties, ok := node["additionalProperties"].(bool); ok && !additionalProperties {
			report.add("additional_properties_closed", location+"/additionalProperties", "public schemas must not forbid unknown fields; additive fields are backward compatible")
		}
	})
}

func validateDeprecationWindows(report *FileReport, doc map[string]any) {
	walk(doc, "", func(location string, value any) {
		node, ok := asMap(value)
		if !ok {
			return
		}
		deprecated, ok := node["deprecated"].(bool)
		if !ok || !deprecated {
			return
		}
		window, _ := node["x-compatibility-window"].(string)
		if strings.TrimSpace(window) == "" {
			report.add("deprecated_without_window", location+"/deprecated", "deprecated public contract entries must document x-compatibility-window")
		}
	})
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

func responseRef(response any) (string, bool) {
	responseMap, ok := asMap(response)
	if !ok {
		return "", false
	}
	ref, ok := responseMap["$ref"].(string)
	return ref, ok && ref != ""
}

func responseUsesDirectGenericSuccessEnvelope(response any) bool {
	responseMap, ok := asMap(response)
	if !ok {
		return false
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
	if !ok {
		return false
	}
	ref, _ := schema["$ref"].(string)
	return ref == "#/components/schemas/SuccessEnvelope"
}

func responseUsesInlineBaseSuccessEnvelope(response any) bool {
	if ref, ok := responseRef(response); ok {
		return ref == "#/components/responses/Success"
	}
	responseMap, ok := asMap(response)
	if !ok {
		return false
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
	if !ok {
		return false
	}
	return schemaContainsDirectSuccessEnvelopeRef(schema, 0)
}

func schemaContainsDirectSuccessEnvelopeRef(schema any, depth int) bool {
	if depth > 12 {
		return false
	}
	schemaMap, ok := asMap(schema)
	if !ok {
		return false
	}
	if ref, _ := schemaMap["$ref"].(string); ref == "#/components/schemas/SuccessEnvelope" {
		return true
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf"} {
		values, ok := schemaMap[key].([]any)
		if !ok {
			continue
		}
		for _, value := range values {
			if schemaContainsDirectSuccessEnvelopeRef(value, depth+1) {
				return true
			}
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

func requiredContains(schema map[string]any, field string) bool {
	for _, required := range stringList(schema["required"]) {
		if required == field {
			return true
		}
	}
	return false
}

func stringList(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
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
