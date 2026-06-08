package contracts

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

func ValidateObject(value map[string]any, schema map[string]any) error {
	if schema == nil {
		return nil
	}
	if schemaType, _ := schema["type"].(string); schemaType != "" && schemaType != "object" {
		return fmt.Errorf("only object schemas are supported")
	}
	return validateObject("", value, schema)
}

func validateObject(prefix string, value map[string]any, schema map[string]any) error {
	for _, required := range SchemaStringSlice(schema["required"]) {
		if _, ok := value[required]; !ok {
			return fmt.Errorf("%s is required", schemaPath(prefix, required))
		}
	}
	properties, _ := schema["properties"].(map[string]any)
	for key, rawProperty := range properties {
		property, _ := rawProperty.(map[string]any)
		actual, exists := value[key]
		if !exists || actual == nil {
			continue
		}
		if err := validateSchemaValue(schemaPath(prefix, key), actual, property); err != nil {
			return err
		}
	}
	return nil
}

func validateSchemaValue(path string, value any, schema map[string]any) error {
	expected, _ := schema["type"].(string)
	if expected != "" && !matchesJSONType(value, expected) {
		return fmt.Errorf("%s must be %s", path, expected)
	}
	if enumValues := schemaList(schema["enum"]); len(enumValues) > 0 && !matchesEnumValue(value, enumValues) {
		return fmt.Errorf("%s must be one of %s", path, enumDescription(enumValues))
	}
	if err := validateStringBounds(path, value, schema); err != nil {
		return err
	}
	if err := validateNumberBounds(path, value, schema); err != nil {
		return err
	}
	if err := validateNestedObject(path, value, schema); err != nil {
		return err
	}
	if err := validateArrayItems(path, value, schema); err != nil {
		return err
	}
	return nil
}

func validateNestedObject(path string, value any, schema map[string]any) error {
	if _, hasProperties := schema["properties"]; !hasProperties {
		return nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return validateObject(path, object, schema)
}

func validateArrayItems(path string, value any, schema map[string]any) error {
	itemSchema, _ := schema["items"].(map[string]any)
	if itemSchema == nil || !matchesJSONType(value, "array") {
		return nil
	}
	items := reflect.ValueOf(value)
	for i := 0; i < items.Len(); i++ {
		if err := validateSchemaValue(fmt.Sprintf("%s[%d]", path, i), items.Index(i).Interface(), itemSchema); err != nil {
			return err
		}
	}
	return nil
}

func schemaPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

func SchemaStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func matchesJSONType(value any, expected string) bool {
	if value == nil {
		return false
	}
	switch expected {
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer":
		number, ok := jsonNumber(value)
		return ok && number == float64(int64(number))
	case "number":
		_, ok := jsonNumber(value)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		kind := reflect.TypeOf(value).Kind()
		return kind == reflect.Slice || kind == reflect.Array
	default:
		return true
	}
}

func validateStringBounds(key string, value any, schema map[string]any) error {
	text, ok := value.(string)
	if !ok {
		return nil
	}
	length := len(text)
	if min, ok := integerSchemaValue(schema["minLength"]); ok && length < min {
		return fmt.Errorf("%s length must be at least %d", key, min)
	}
	if max, ok := integerSchemaValue(schema["maxLength"]); ok && length > max {
		return fmt.Errorf("%s length must be at most %d", key, max)
	}
	return nil
}

func validateNumberBounds(key string, value any, schema map[string]any) error {
	number, ok := jsonNumber(value)
	if !ok {
		return nil
	}
	if min, ok := numberSchemaValue(schema["minimum"]); ok && number < min {
		return fmt.Errorf("%s must be >= %s", key, formatSchemaNumber(min))
	}
	if max, ok := numberSchemaValue(schema["maximum"]); ok && number > max {
		return fmt.Errorf("%s must be <= %s", key, formatSchemaNumber(max))
	}
	return nil
}

func schemaList(value any) []any {
	switch typed := value.(type) {
	case []any:
		return append([]any(nil), typed...)
	case []string:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, item)
		}
		return out
	default:
		return nil
	}
}

func matchesEnumValue(value any, enumValues []any) bool {
	for _, enumValue := range enumValues {
		if schemaValueEqual(value, enumValue) {
			return true
		}
	}
	return false
}

func schemaValueEqual(left, right any) bool {
	leftNumber, leftOK := jsonNumber(left)
	rightNumber, rightOK := jsonNumber(right)
	if leftOK && rightOK {
		return leftNumber == rightNumber
	}
	return reflect.DeepEqual(left, right)
}

func enumDescription(values []any) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprint(value))
	}
	return strings.Join(parts, ",")
}

func jsonNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	default:
		return 0, false
	}
}

func numberSchemaValue(value any) (float64, bool) {
	return jsonNumber(value)
}

func integerSchemaValue(value any) (int, bool) {
	number, ok := jsonNumber(value)
	if !ok || number != float64(int64(number)) {
		return 0, false
	}
	return int(number), true
}

func formatSchemaNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}
