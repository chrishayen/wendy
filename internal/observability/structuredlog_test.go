package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestStructuredLoggerWritesRequiredFieldsAndRedactsSecrets(t *testing.T) {
	var buf bytes.Buffer
	logger := NewStructuredLogger(&buf, "runner",
		WithClock(func() time.Time { return time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC) }),
		WithRedactionValues("super-secret"),
	)
	ctx := WithRequestID(context.Background(), "req_test")

	logger.Error(ctx, "runner failed with token=abc123 and super-secret", errors.New("backend rejected Authorization=Bearer token-xyz"),
		Field("job_id", "job_000001"),
		Field("subject_id", "sub_agent"),
		Field("credential", "Bearer token-xyz"),
		Field("nested", map[string]any{"secret": "super-secret"}),
	)

	var entry map[string]any
	if err := json.NewDecoder(&buf).Decode(&entry); err != nil {
		t.Fatalf("decode log: %v", err)
	}
	for _, key := range []string{"timestamp", "severity", "component", "request_id", "job_id", "subject_id", "message", "error"} {
		if entry[key] == "" {
			t.Fatalf("missing log field %s in %#v", key, entry)
		}
	}
	raw := buf.String()
	for _, leaked := range []string{"super-secret", "abc123", "token-xyz"} {
		if strings.Contains(raw, leaked) {
			t.Fatalf("log leaked %q: %s", leaked, raw)
		}
	}
	if entry["credential"] != "Bearer [REDACTED]" {
		t.Fatalf("credential = %#v", entry["credential"])
	}
	nested := entry["nested"].(map[string]any)
	if nested["secret"] != "[REDACTED]" {
		t.Fatalf("nested = %#v", nested)
	}
}

func TestRedactorRedactsJSONCredentialFields(t *testing.T) {
	redactor := NewRedactor()
	got := redactor.Redact(`{"credential":"Bearer token-abc","api_key":"key-123","safe":"ok"}`)
	if strings.Contains(got, "token-abc") || strings.Contains(got, "key-123") {
		t.Fatalf("redacted text leaked credential: %s", got)
	}
	if !strings.Contains(got, `"safe":"ok"`) {
		t.Fatalf("redacted text lost safe field: %s", got)
	}
}
