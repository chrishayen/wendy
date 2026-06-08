package policy

import (
	"errors"
	"path/filepath"
	"testing"

	"pacp/internal/contracts"
)

func TestStoreCreatesVerifiesAndRevokesAPIKeys(t *testing.T) {
	store := NewStore()
	key, err := store.CreateAPIKey(contracts.CreateAPIKeyRequest{
		SubjectID: "sub_agent",
		Scopes:    []string{"agent"},
		Token:     "token_agent",
	})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	if key.Token != "token_agent" {
		t.Fatalf("create response token = %q", key.Token)
	}

	valid, err := store.VerifyCredential(contracts.VerifyCredentialRequest{Credential: "Bearer token_agent"})
	if err != nil {
		t.Fatalf("verify valid: %v", err)
	}
	if !valid.Valid || valid.SubjectID == nil || *valid.SubjectID != "sub_agent" || len(valid.Scopes) != 1 || valid.Scopes[0] != "agent" {
		t.Fatalf("valid response = %#v", valid)
	}

	unknown, err := store.VerifyCredential(contracts.VerifyCredentialRequest{Credential: "Bearer token_unknown"})
	if err != nil {
		t.Fatalf("verify unknown: %v", err)
	}
	if unknown.Valid || unknown.SubjectID != nil || len(unknown.Scopes) != 0 {
		t.Fatalf("unknown response = %#v", unknown)
	}

	rotated, err := store.RotateAPIKey(key.KeyID, contracts.RotateAPIKeyRequest{Token: "token_agent_rotated"})
	if err != nil {
		t.Fatalf("rotate key: %v", err)
	}
	if rotated.KeyID != key.KeyID || rotated.Token != "token_agent_rotated" || rotated.RotatedAt == "" {
		t.Fatalf("rotated response = %#v", rotated)
	}
	oldToken, err := store.VerifyCredential(contracts.VerifyCredentialRequest{Credential: "Bearer token_agent"})
	if err != nil {
		t.Fatalf("verify old token after rotate: %v", err)
	}
	if oldToken.Valid {
		t.Fatalf("old rotated credential verified: %#v", oldToken)
	}
	newToken, err := store.VerifyCredential(contracts.VerifyCredentialRequest{Credential: "Bearer token_agent_rotated"})
	if err != nil {
		t.Fatalf("verify new token after rotate: %v", err)
	}
	if !newToken.Valid || newToken.SubjectID == nil || *newToken.SubjectID != "sub_agent" {
		t.Fatalf("new rotated credential = %#v", newToken)
	}

	_, err = store.VerifyCredential(contracts.VerifyCredentialRequest{Credential: "bearer token_agent"})
	if !errors.Is(err, ErrMalformedCredential) {
		t.Fatalf("expected malformed credential, got %v", err)
	}

	revoked, err := store.RevokeAPIKey(key.KeyID)
	if err != nil {
		t.Fatalf("revoke key: %v", err)
	}
	if revoked.Token != "" || revoked.RevokedAt == "" {
		t.Fatalf("revoked response = %#v", revoked)
	}
	afterRevoke, err := store.VerifyCredential(contracts.VerifyCredentialRequest{Credential: "Bearer token_agent_rotated"})
	if err != nil {
		t.Fatalf("verify revoked: %v", err)
	}
	if afterRevoke.Valid {
		t.Fatalf("revoked credential verified: %#v", afterRevoke)
	}
	_, err = store.RotateAPIKey(key.KeyID, contracts.RotateAPIKeyRequest{Token: "token_after_revoke"})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected revoked key validation error, got %v", err)
	}
}

func TestStorePolicyOwnerContextAndJobState(t *testing.T) {
	store := NewStore()
	_, err := store.CreateAPIKey(contracts.CreateAPIKeyRequest{SubjectID: "sub_agent", Scopes: []string{"agent"}, Token: "token_agent"})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}

	allowed, err := store.CheckPolicy(contracts.PolicyCheckRequest{
		SubjectID: "sub_agent",
		Action:    "job.cancel",
		Resource:  "job_1",
		Context: map[string]any{
			"job_id":           "job_1",
			"owner_subject_id": "sub_agent",
			"requester_id":     "sub_agent",
			"job_state":        "queued",
		},
	})
	if err != nil {
		t.Fatalf("check queued cancel: %v", err)
	}
	if !allowed.Allowed {
		t.Fatalf("queued cancel denied: %#v", allowed)
	}

	claimed, err := store.CheckPolicy(contracts.PolicyCheckRequest{
		SubjectID: "sub_agent",
		Action:    "job.cancel",
		Resource:  "job_1",
		Context: map[string]any{
			"owner_subject_id": "sub_agent",
			"requester_id":     "sub_agent",
			"job_state":        "claimed",
		},
	})
	if err != nil {
		t.Fatalf("check claimed cancel: %v", err)
	}
	if claimed.Allowed || claimed.Reason != "policy_denied" {
		t.Fatalf("claimed cancel decision = %#v", claimed)
	}

	running, err := store.CheckPolicy(contracts.PolicyCheckRequest{
		SubjectID: "sub_agent",
		Action:    "job.cancel",
		Resource:  "job_1",
		Context: map[string]any{
			"owner_subject_id": "sub_agent",
			"requester_id":     "sub_agent",
			"job_state":        "running",
		},
	})
	if err != nil {
		t.Fatalf("check running cancel: %v", err)
	}
	if running.Allowed || running.Reason != "policy_denied" {
		t.Fatalf("running cancel decision = %#v", running)
	}

	succeeded, err := store.CheckPolicy(contracts.PolicyCheckRequest{
		SubjectID: "sub_agent",
		Action:    "job.cancel",
		Resource:  "job_1",
		Context: map[string]any{
			"owner_subject_id": "sub_agent",
			"requester_id":     "sub_agent",
			"job_state":        "succeeded",
		},
	})
	if err != nil {
		t.Fatalf("check succeeded cancel: %v", err)
	}
	if succeeded.Allowed || succeeded.Reason != "policy_denied" {
		t.Fatalf("succeeded cancel decision = %#v", succeeded)
	}

	missing, err := store.CheckPolicy(contracts.PolicyCheckRequest{
		SubjectID: "sub_agent",
		Action:    "artifact.read",
		Resource:  "art_1",
		Context:   map[string]any{"producer_ref": "job_1"},
	})
	if err != nil {
		t.Fatalf("check artifact missing context: %v", err)
	}
	if missing.Allowed || missing.Reason != "missing_context" {
		t.Fatalf("missing context decision = %#v", missing)
	}
}

func TestStorePolicyScopesAndExplicitRules(t *testing.T) {
	store := NewStore()
	_, err := store.CreateAPIKey(contracts.CreateAPIKeyRequest{SubjectID: "sub_runner", Scopes: []string{"worker"}, Token: "token_runner"})
	if err != nil {
		t.Fatalf("create runner key: %v", err)
	}
	worker, err := store.CheckPolicy(contracts.PolicyCheckRequest{
		SubjectID: "sub_runner",
		Action:    "lease.request",
		Resource:  "gpu",
		Context:   map[string]any{"holder_id": "job_1", "resource_selector": "gpu"},
	})
	if err != nil {
		t.Fatalf("worker policy: %v", err)
	}
	if !worker.Allowed {
		t.Fatalf("worker denied: %#v", worker)
	}
	for _, action := range []string{"lease.heartbeat", "lease.release", "lease.read", "lease.cancel", "node.read", "node.service.start", "node.service.touch", "node.service.stop"} {
		decision, err := store.CheckPolicy(contracts.PolicyCheckRequest{
			SubjectID: "sub_runner",
			Action:    action,
			Resource:  "lease_1",
		})
		if err != nil {
			t.Fatalf("worker policy %s: %v", action, err)
		}
		if !decision.Allowed {
			t.Fatalf("worker denied for %s: %#v", action, decision)
		}
	}
	_, err = store.CreateAPIKey(contracts.CreateAPIKeyRequest{SubjectID: "sub_component", Scopes: []string{"component"}, Token: "token_component"})
	if err != nil {
		t.Fatalf("create component key: %v", err)
	}
	for _, action := range []string{"lease.read", "lease.cancel", "lease.resource.register", "catalog.register", "artifact.retention.sweep"} {
		decision, err := store.CheckPolicy(contracts.PolicyCheckRequest{
			SubjectID: "sub_component",
			Action:    action,
			Resource:  "res_gpu_0",
		})
		if err != nil {
			t.Fatalf("component policy %s: %v", action, err)
		}
		if !decision.Allowed {
			t.Fatalf("component denied for %s: %#v", action, decision)
		}
	}
	componentRelease, err := store.CheckPolicy(contracts.PolicyCheckRequest{
		SubjectID: "sub_component",
		Action:    "lease.release",
		Resource:  "lease_1",
	})
	if err != nil {
		t.Fatalf("component release policy: %v", err)
	}
	if componentRelease.Allowed {
		t.Fatalf("component lease release allowed: %#v", componentRelease)
	}

	_, err = store.CreateRule(contracts.CreatePolicyRuleRequest{
		SubjectID: "sub_runner",
		Action:    "lease.request",
		Resource:  "gpu",
		Effect:    "deny",
		Reason:    "maintenance_window",
	})
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}
	denied, err := store.CheckPolicy(contracts.PolicyCheckRequest{
		SubjectID: "sub_runner",
		Action:    "lease.request",
		Resource:  "gpu",
	})
	if err != nil {
		t.Fatalf("worker policy after rule: %v", err)
	}
	if denied.Allowed || denied.Reason != "maintenance_window" {
		t.Fatalf("explicit rule decision = %#v", denied)
	}
}

func TestStoreSecretsAndRedaction(t *testing.T) {
	store := NewStore()
	_, err := store.CreateAPIKey(contracts.CreateAPIKeyRequest{SubjectID: "sub_component", Scopes: []string{"component"}, Token: "token_component"})
	if err != nil {
		t.Fatalf("create component key: %v", err)
	}
	_, err = store.CreateAPIKey(contracts.CreateAPIKeyRequest{SubjectID: "sub_agent", Scopes: []string{"agent"}, Token: "token_agent"})
	if err != nil {
		t.Fatalf("create agent key: %v", err)
	}
	secret, err := store.CreateSecret(contracts.CreateSecretRequest{Name: "provider_token", Value: "super-secret"})
	if err != nil {
		t.Fatalf("create secret: %v", err)
	}
	resolved, err := store.ResolveSecret(contracts.ResolveSecretRequest{SecretRef: secret.SecretRef, SubjectID: "sub_component"})
	if err != nil {
		t.Fatalf("resolve secret: %v", err)
	}
	if resolved.Value != "super-secret" {
		t.Fatalf("resolved = %#v", resolved)
	}
	_, err = store.ResolveSecret(contracts.ResolveSecretRequest{SecretRef: secret.SecretRef, SubjectID: "sub_agent"})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected forbidden secret resolution, got %v", err)
	}
	events := store.AuditEvents()
	if len(events) != 2 {
		t.Fatalf("secret audit events = %#v", events)
	}
	if events[0].EventType != "secret.resolve" ||
		events[0].SubjectID != "sub_component" ||
		events[0].Action != "secret.resolve" ||
		events[0].Resource != secret.SecretRef ||
		!events[0].Allowed ||
		events[0].Reason != "allowed_by_scope" {
		t.Fatalf("allowed secret audit = %#v", events[0])
	}
	if events[1].EventType != "secret.resolve" ||
		events[1].SubjectID != "sub_agent" ||
		events[1].Resource != secret.SecretRef ||
		events[1].Allowed ||
		events[1].Reason != "forbidden" {
		t.Fatalf("denied secret audit = %#v", events[1])
	}
	if events[0].Resource == "super-secret" || events[1].Resource == "super-secret" {
		t.Fatalf("secret value leaked into audit events = %#v", events)
	}
	redacted := store.Redact(contracts.RedactRequest{Text: "token is super-secret"})
	if redacted.Text != "token is [REDACTED]" {
		t.Fatalf("redacted = %#v", redacted)
	}
}

func TestPersistentStoreReloadsKeysRulesSecretsAndAudit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	store, err := NewPersistentStore(path)
	if err != nil {
		t.Fatalf("new persistent store: %v", err)
	}
	_, err = store.CreateAPIKey(contracts.CreateAPIKeyRequest{SubjectID: "sub_component", Scopes: []string{"component"}, Token: "token_component"})
	if err != nil {
		t.Fatalf("create component key: %v", err)
	}
	_, err = store.CreateRule(contracts.CreatePolicyRuleRequest{
		SubjectID: "sub_component",
		Action:    "artifact.read",
		Resource:  "art_1",
		Effect:    "deny",
		Reason:    "artifact_locked",
	})
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}
	secret, err := store.CreateSecret(contracts.CreateSecretRequest{Name: "provider_token", Value: "super-secret"})
	if err != nil {
		t.Fatalf("create secret: %v", err)
	}
	decision, err := store.CheckPolicy(contracts.PolicyCheckRequest{
		SubjectID: "sub_component",
		Action:    "artifact.read",
		Resource:  "art_1",
		Context:   map[string]any{"owner_subject_id": "sub_agent"},
	})
	if err != nil {
		t.Fatalf("check policy: %v", err)
	}
	if decision.Allowed || decision.Reason != "artifact_locked" {
		t.Fatalf("decision = %#v", decision)
	}

	reloaded, err := NewPersistentStore(path)
	if err != nil {
		t.Fatalf("reload persistent store: %v", err)
	}
	verified, err := reloaded.VerifyCredential(contracts.VerifyCredentialRequest{Credential: "Bearer token_component"})
	if err != nil {
		t.Fatalf("verify after reload: %v", err)
	}
	if !verified.Valid || verified.SubjectID == nil || *verified.SubjectID != "sub_component" {
		t.Fatalf("verified = %#v", verified)
	}
	reloadedDecision, err := reloaded.CheckPolicy(contracts.PolicyCheckRequest{
		SubjectID: "sub_component",
		Action:    "artifact.read",
		Resource:  "art_1",
		Context:   map[string]any{"owner_subject_id": "sub_agent"},
	})
	if err != nil {
		t.Fatalf("check policy after reload: %v", err)
	}
	if reloadedDecision.Allowed || reloadedDecision.Reason != "artifact_locked" {
		t.Fatalf("reloaded decision = %#v", reloadedDecision)
	}
	resolved, err := reloaded.ResolveSecret(contracts.ResolveSecretRequest{SecretRef: secret.SecretRef, SubjectID: "sub_component"})
	if err != nil {
		t.Fatalf("resolve after reload: %v", err)
	}
	if resolved.Value != "super-secret" {
		t.Fatalf("resolved = %#v", resolved)
	}
	redacted := reloaded.Redact(contracts.RedactRequest{Text: "token is super-secret"})
	if redacted.Text != "token is [REDACTED]" {
		t.Fatalf("redacted = %#v", redacted)
	}
	if events := reloaded.AuditEvents(); len(events) != 3 || events[2].EventType != "secret.resolve" {
		t.Fatalf("audit events = %#v", events)
	}
}
