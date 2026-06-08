package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnsureRequestIDPreservesIncomingHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/tools", nil)
	req.Header.Set(RequestIDHeader, "req_inbound")

	ensured := EnsureRequestID(req, "req_gateway")
	if got := ensured.Header.Get(RequestIDHeader); got != "req_inbound" {
		t.Fatalf("header = %q", got)
	}
	if got := RequestIDFromContext(ensured.Context()); got != "req_inbound" {
		t.Fatalf("context request id = %q", got)
	}
}

func TestEnsureRequestIDGeneratesWhenMissing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/tools", nil)

	ensured := EnsureRequestID(req, "req_gateway")
	got := ensured.Header.Get(RequestIDHeader)
	if !strings.HasPrefix(got, "req_gateway_") {
		t.Fatalf("generated request id = %q", got)
	}
	if RequestIDFromContext(ensured.Context()) != got {
		t.Fatalf("context/header mismatch: context=%q header=%q", RequestIDFromContext(ensured.Context()), got)
	}
}

func TestPropagateRequestIDCopiesContextToRequest(t *testing.T) {
	ctx := WithRequestID(context.Background(), "req_trace")
	req := httptest.NewRequest(http.MethodGet, "/v1/jobs", nil)

	PropagateRequestID(ctx, req)
	if got := req.Header.Get(RequestIDHeader); got != "req_trace" {
		t.Fatalf("propagated header = %q", got)
	}
}
