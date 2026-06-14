package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"wendy/internal/components/policy"
	"wendy/internal/contracts"
	"wendy/internal/observability"
	"wendy/internal/transportauth"
)

func TestPolicySecretResolverUsesPolicyAPI(t *testing.T) {
	store := policy.NewStore()
	if _, err := store.CreateAPIKey(contracts.CreateAPIKeyRequest{SubjectID: "sub_component", Scopes: []string{"component"}, Token: "token_component"}); err != nil {
		t.Fatalf("create component key: %v", err)
	}
	secret, err := store.CreateSecret(contracts.CreateSecretRequest{Name: "backend_token", Value: "resolved-secret"})
	if err != nil {
		t.Fatalf("create secret: %v", err)
	}
	handler := transportauth.RequireBearer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(observability.RequestIDHeader) != "req_secret_resolve" {
			t.Fatalf("request id = %q", r.Header.Get(observability.RequestIDHeader))
		}
		policy.NewHandler(store).ServeHTTP(w, r)
	}), "token_component")
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx := observability.WithRequestID(context.Background(), "req_secret_resolve")
	resolver := PolicySecretResolver{
		PolicyURL:  server.URL,
		Credential: "Bearer token_component",
		SubjectID:  "sub_component",
		Client:     server.Client(),
	}
	value, err := resolver.ResolveSecret(ctx, secret.SecretRef)
	if err != nil {
		t.Fatalf("resolve secret: %v", err)
	}
	if value != "resolved-secret" {
		t.Fatalf("value = %q", value)
	}
}

func TestPolicySecretResolverMapsPolicyDenial(t *testing.T) {
	store := policy.NewStore()
	if _, err := store.CreateAPIKey(contracts.CreateAPIKeyRequest{SubjectID: "sub_agent", Scopes: []string{"agent"}, Token: "token_component"}); err != nil {
		t.Fatalf("create agent key: %v", err)
	}
	secret, err := store.CreateSecret(contracts.CreateSecretRequest{Name: "backend_token", Value: "resolved-secret"})
	if err != nil {
		t.Fatalf("create secret: %v", err)
	}
	server := httptest.NewServer(policy.NewHandler(store))
	defer server.Close()

	resolver := PolicySecretResolver{
		PolicyURL: server.URL,
		SubjectID: "sub_agent",
		Client:    server.Client(),
	}
	_, err = resolver.ResolveSecret(context.Background(), secret.SecretRef)
	var invokeErr InvokeError
	if err == nil || !errors.As(err, &invokeErr) || invokeErr.Code != "forbidden" {
		t.Fatalf("error = %#v", err)
	}
}
