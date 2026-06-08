package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
)

type LogField struct {
	Key   string
	Value any
}

type StructuredLogger struct {
	component string
	out       io.Writer
	now       func() time.Time
	redactor  *Redactor
}

type LoggerOption func(*StructuredLogger)

type Redactor struct {
	literals []string
}

var logSecretPatterns = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?i)(Bearer\s+)[A-Za-z0-9._~+/=-]+`), `${1}[REDACTED]`},
	{regexp.MustCompile(`(?i)((?:token|api_key|apikey|access_token|credential|secret|authorization)=)[^&\s]+`), `${1}[REDACTED]`},
	{regexp.MustCompile(`(?i)("(?:token|api_key|apikey|access_token|credential|secret|authorization)"\s*:\s*")[^"]+(")`), `${1}[REDACTED]${2}`},
}

func Field(key string, value any) LogField {
	return LogField{Key: key, Value: value}
}

func NewStructuredLogger(out io.Writer, component string, opts ...LoggerOption) *StructuredLogger {
	logger := &StructuredLogger{
		component: strings.TrimSpace(component),
		out:       out,
		now:       time.Now,
		redactor:  NewRedactor(),
	}
	if logger.component == "" {
		logger.component = "unknown"
	}
	for _, opt := range opts {
		opt(logger)
	}
	return logger
}

func WithRedactionValues(values ...string) LoggerOption {
	return func(logger *StructuredLogger) {
		logger.redactor = NewRedactor(values...)
	}
}

func WithClock(now func() time.Time) LoggerOption {
	return func(logger *StructuredLogger) {
		if now != nil {
			logger.now = now
		}
	}
}

func NewRedactor(values ...string) *Redactor {
	seen := map[string]bool{}
	literals := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || value == "[REDACTED]" || seen[value] {
			continue
		}
		seen[value] = true
		literals = append(literals, value)
	}
	sort.Slice(literals, func(i, j int) bool {
		return len(literals[i]) > len(literals[j])
	})
	return &Redactor{literals: literals}
}

func (r *Redactor) Redact(text string) string {
	if r == nil {
		r = NewRedactor()
	}
	for _, literal := range r.literals {
		text = strings.ReplaceAll(text, literal, "[REDACTED]")
	}
	for _, pattern := range logSecretPatterns {
		text = pattern.pattern.ReplaceAllString(text, pattern.replacement)
	}
	return text
}

func (r *Redactor) RedactValue(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return r.Redact(typed)
	case error:
		return r.Redact(typed.Error())
	case fmt.Stringer:
		return r.Redact(typed.String())
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			out[key] = r.RedactValue(value)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, value := range typed {
			out = append(out, r.RedactValue(value))
		}
		return out
	default:
		return value
	}
}

func (l *StructuredLogger) Info(ctx context.Context, message string, fields ...LogField) {
	l.Log(ctx, "info", message, fields...)
}

func (l *StructuredLogger) Error(ctx context.Context, message string, err error, fields ...LogField) {
	if err != nil {
		fields = append(fields, Field("error", err))
	}
	l.Log(ctx, "error", message, fields...)
}

func (l *StructuredLogger) Log(ctx context.Context, severity, message string, fields ...LogField) {
	if l == nil || l.out == nil {
		return
	}
	redactor := l.redactor
	if redactor == nil {
		redactor = NewRedactor()
	}
	entry := map[string]any{
		"timestamp": l.now().UTC().Format(time.RFC3339),
		"severity":  strings.TrimSpace(severity),
		"component": l.component,
		"message":   redactor.Redact(message),
	}
	if entry["severity"] == "" {
		entry["severity"] = "info"
	}
	if requestID := RequestIDFromContext(ctx); requestID != "" {
		entry["request_id"] = requestID
	}
	for _, field := range fields {
		key := strings.TrimSpace(field.Key)
		if key == "" {
			continue
		}
		entry[key] = redactor.RedactValue(field.Value)
	}
	_ = json.NewEncoder(l.out).Encode(entry)
}
