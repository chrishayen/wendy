package contracts

import (
	"strings"
	"testing"
)

func TestValidateObjectAcceptsValidObject(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []any{"prompt", "width", "mode"},
		"properties": map[string]any{
			"prompt": map[string]any{"type": "string", "minLength": 3, "maxLength": 20},
			"width":  map[string]any{"type": "integer", "minimum": 64, "maximum": 2048},
			"mode":   map[string]any{"type": "string", "enum": []any{"draft", "quality"}},
		},
	}
	err := ValidateObject(map[string]any{
		"prompt": "red mug",
		"width":  float64(1024),
		"mode":   "quality",
	}, schema)
	if err != nil {
		t.Fatalf("validate object: %v", err)
	}
}

func TestValidateObjectRejectsMissingRequiredField(t *testing.T) {
	schema := map[string]any{"type": "object", "required": []any{"prompt"}}
	err := ValidateObject(map[string]any{}, schema)
	if err == nil || err.Error() != "prompt is required" {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateObjectRejectsTypeEnumAndBounds(t *testing.T) {
	tests := []struct {
		name   string
		value  map[string]any
		schema map[string]any
		want   string
	}{
		{
			name:  "wrong type",
			value: map[string]any{"prompt": 123},
			schema: map[string]any{"type": "object", "properties": map[string]any{
				"prompt": map[string]any{"type": "string"},
			}},
			want: "prompt must be string",
		},
		{
			name:  "enum",
			value: map[string]any{"mode": "slow"},
			schema: map[string]any{"type": "object", "properties": map[string]any{
				"mode": map[string]any{"type": "string", "enum": []any{"draft", "quality"}},
			}},
			want: "mode must be one of draft,quality",
		},
		{
			name:  "minimum",
			value: map[string]any{"width": float64(32)},
			schema: map[string]any{"type": "object", "properties": map[string]any{
				"width": map[string]any{"type": "integer", "minimum": 64},
			}},
			want: "width must be >= 64",
		},
		{
			name:  "max length",
			value: map[string]any{"prompt": "too long"},
			schema: map[string]any{"type": "object", "properties": map[string]any{
				"prompt": map[string]any{"type": "string", "maxLength": 3},
			}},
			want: "prompt length must be at most 3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateObject(tt.value, tt.schema)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateObjectAcceptsTypedGoSlicesAsArrays(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tags": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
	}
	if err := ValidateObject(map[string]any{"tags": []string{"gpu", "image"}}, schema); err != nil {
		t.Fatalf("ValidateObject returned error for typed slice: %v", err)
	}
}

func TestValidateObjectRejectsInvalidArrayItem(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"artifact_refs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
	}
	err := ValidateObject(map[string]any{"artifact_refs": []any{"art_1", 42}}, schema)
	if err == nil || err.Error() != "artifact_refs[1] must be string" {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateObjectRejectsInvalidNestedObjectProperty(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"metadata": map[string]any{
				"type":     "object",
				"required": []any{"source"},
				"properties": map[string]any{
					"source": map[string]any{"type": "string"},
				},
			},
		},
	}
	err := ValidateObject(map[string]any{"metadata": map[string]any{"source": 7}}, schema)
	if err == nil || err.Error() != "metadata.source must be string" {
		t.Fatalf("error = %v", err)
	}
}
