package transportauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"pacp/internal/contracts"
)

type ScopeRule struct {
	Method              string
	Path                string
	Scopes              []string
	UnauthorizedMessage string
	ForbiddenMessage    string
}

type ScopeConfig struct {
	PolicyURL        string
	PolicyCredential string
	Rules            []ScopeRule
	Client           *http.Client
}

func RequireVerifiedScopes(next http.Handler, cfg ScopeConfig) http.Handler {
	policyURL := strings.TrimRight(strings.TrimSpace(cfg.PolicyURL), "/")
	if policyURL == "" || len(cfg.Rules) == 0 {
		return next
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rule, ok := matchingScopeRule(r.Method, r.URL.Path, cfg.Rules)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		credential := r.Header.Get("Authorization")
		if credential == "" {
			writeAuthError(w, r, http.StatusUnauthorized, "unauthorized", unauthorizedMessage(rule), false)
			return
		}
		verification, err := verifyCredential(r.Context(), client, policyURL, cfg.PolicyCredential, credential, r.Header.Get("X-Request-ID"))
		if err != nil {
			writeAuthError(w, r, http.StatusServiceUnavailable, "policy_unavailable", err.Error(), true)
			return
		}
		if !verification.Valid || verification.SubjectID == nil || *verification.SubjectID == "" {
			writeAuthError(w, r, http.StatusUnauthorized, "unauthorized", unauthorizedMessage(rule), false)
			return
		}
		if !scopeAllowed(verification.Scopes, rule.Scopes) {
			writeAuthError(w, r, http.StatusForbidden, "forbidden", forbiddenMessage(rule), false)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func verifyCredential(ctx context.Context, client *http.Client, policyURL, policyCredential, credential, requestID string) (contracts.CredentialVerification, error) {
	raw, err := json.Marshal(contracts.VerifyCredentialRequest{
		Credential: credential,
		Context:    map[string]any{"caller": "component_api_auth"},
	})
	if err != nil {
		return contracts.CredentialVerification{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, policyURL+"/v1/auth/verify", bytes.NewReader(raw))
	if err != nil {
		return contracts.CredentialVerification{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if policyCredential != "" {
		req.Header.Set("Authorization", authorizationHeader(policyCredential))
	}
	if requestID != "" {
		req.Header.Set("X-Request-ID", requestID)
	}
	resp, err := client.Do(req)
	if err != nil {
		return contracts.CredentialVerification{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return contracts.CredentialVerification{}, err
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return contracts.CredentialVerification{Valid: false}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return contracts.CredentialVerification{}, fmt.Errorf("policy credential verification failed with %s", resp.Status)
	}
	var envelope struct {
		OK   bool                             `json:"ok"`
		Data contracts.CredentialVerification `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return contracts.CredentialVerification{}, err
	}
	if !envelope.OK {
		return contracts.CredentialVerification{Valid: false}, nil
	}
	return envelope.Data, nil
}

func matchingScopeRule(method, path string, rules []ScopeRule) (ScopeRule, bool) {
	path = strings.TrimSuffix(path, "/")
	for _, rule := range rules {
		if rule.Method != "" && rule.Method != method {
			continue
		}
		if pathPatternMatches(rule.Path, path) {
			return rule, true
		}
	}
	return ScopeRule{}, false
}

func pathPatternMatches(pattern, path string) bool {
	pattern = strings.Trim(strings.TrimSuffix(pattern, "/"), "/")
	path = strings.Trim(path, "/")
	if pattern == "" || path == "" {
		return pattern == path
	}
	patternParts := strings.Split(pattern, "/")
	pathParts := strings.Split(path, "/")
	if len(patternParts) != len(pathParts) {
		return false
	}
	for i := range patternParts {
		part := patternParts[i]
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			continue
		}
		if part != pathParts[i] {
			return false
		}
	}
	return true
}

func scopeAllowed(actual, allowed []string) bool {
	if hasScopeValue(actual, "admin") {
		return true
	}
	for _, scope := range allowed {
		if hasScopeValue(actual, scope) {
			return true
		}
	}
	return false
}

func hasScopeValue(scopes []string, want string) bool {
	for _, scope := range scopes {
		if scope == want {
			return true
		}
	}
	return false
}

func unauthorizedMessage(rule ScopeRule) string {
	if rule.UnauthorizedMessage != "" {
		return rule.UnauthorizedMessage
	}
	return "valid bearer credential is required"
}

func forbiddenMessage(rule ScopeRule) string {
	if rule.ForbiddenMessage != "" {
		return rule.ForbiddenMessage
	}
	return "caller is not authorized for this operation"
}
