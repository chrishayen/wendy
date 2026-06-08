package observability

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
)

const RequestIDHeader = "X-Request-ID"

type requestIDKey struct{}

var requestIDCounter atomic.Uint64

func EnsureRequestID(r *http.Request, prefix string) *http.Request {
	id := strings.TrimSpace(r.Header.Get(RequestIDHeader))
	if id == "" {
		id = NewRequestID(prefix)
	}
	ctx := WithRequestID(r.Context(), id)
	clone := r.WithContext(ctx)
	clone.Header = r.Header.Clone()
	clone.Header.Set(RequestIDHeader, id)
	return clone
}

func EnsureContextRequestID(ctx context.Context, prefix string) context.Context {
	if RequestIDFromContext(ctx) != "" {
		return ctx
	}
	return WithRequestID(ctx, NewRequestID(prefix))
}

func WithRequestID(ctx context.Context, requestID string) context.Context {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDKey{}, requestID)
}

func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	requestID, _ := ctx.Value(requestIDKey{}).(string)
	return strings.TrimSpace(requestID)
}

func RequestIDFromRequest(r *http.Request, fallbackPrefix string) string {
	if r == nil {
		return NewRequestID(fallbackPrefix)
	}
	if requestID := RequestIDFromContext(r.Context()); requestID != "" {
		return requestID
	}
	if requestID := strings.TrimSpace(r.Header.Get(RequestIDHeader)); requestID != "" {
		return requestID
	}
	return NewRequestID(fallbackPrefix)
}

func PropagateRequestID(ctx context.Context, req *http.Request) {
	if req == nil {
		return
	}
	if requestID := RequestIDFromContext(ctx); requestID != "" {
		req.Header.Set(RequestIDHeader, requestID)
	}
}

func NewRequestID(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "req"
	}
	return fmt.Sprintf("%s_%06d", prefix, requestIDCounter.Add(1))
}
