package policy

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"wendy/internal/contracts"
)

func TestLoadSeedFileAndApplySeedIdempotently(t *testing.T) {
	path := writePolicySeedFile(t, `{
  "api_keys": [
    {"subject_id": "sub_component", "scopes": ["component"], "token": "token_component"}
  ],
  "rules": [
    {"subject_id": "sub_component", "action": "artifact.read", "resource": "art_locked", "effect": "deny"}
  ],
  "secrets": [
    {"name": "provider_token", "value": "super-secret"}
  ]
}`)

	seed, err := LoadSeedFile(path)
	if err != nil {
		t.Fatalf("load seed: %v", err)
	}
	store := NewStore()
	result, err := store.ApplySeed(seed)
	if err != nil {
		t.Fatalf("apply seed: %v", err)
	}
	if result.APIKeysCreated != 1 || result.RulesCreated != 1 || result.SecretsCreated != 1 {
		t.Fatalf("first result = %#v", result)
	}

	reapplied, err := store.ApplySeed(seed)
	if err != nil {
		t.Fatalf("reapply seed: %v", err)
	}
	if reapplied.APIKeysSkipped != 1 || reapplied.RulesSkipped != 1 || reapplied.SecretsSkipped != 1 {
		t.Fatalf("second result = %#v", reapplied)
	}

	verified, err := store.VerifyCredential(contracts.VerifyCredentialRequest{Credential: "Bearer token_component"})
	if err != nil {
		t.Fatalf("verify seeded token: %v", err)
	}
	if !verified.Valid || verified.SubjectID == nil || *verified.SubjectID != "sub_component" {
		t.Fatalf("verified = %#v", verified)
	}
	decision, err := store.CheckPolicy(contracts.PolicyCheckRequest{
		SubjectID: "sub_component",
		Action:    "artifact.read",
		Resource:  "art_locked",
		Context:   map[string]any{"owner_subject_id": "sub_agent"},
	})
	if err != nil {
		t.Fatalf("check seeded rule: %v", err)
	}
	if decision.Allowed || decision.Reason != "rule_deny" {
		t.Fatalf("decision = %#v", decision)
	}
	redacted := store.Redact(contracts.RedactRequest{Text: "token is super-secret"})
	if redacted.Text != "token is [REDACTED]" {
		t.Fatalf("redacted = %#v", redacted)
	}
}

func TestLoadLocalSeedFixture(t *testing.T) {
	seed, err := LoadSeedFile(filepath.Join("..", "..", "..", "testdata", "policy", "local-seed.json"))
	if err != nil {
		t.Fatalf("load local seed fixture: %v", err)
	}
	if len(seed.APIKeys) != 3 {
		t.Fatalf("api key count = %d", len(seed.APIKeys))
	}
	if seed.APIKeys[0].Token != "token_agent" || seed.APIKeys[2].Token != "token_worker" {
		t.Fatalf("seed = %#v", seed)
	}
}

func TestApplySeedRejectsAPIKeyDrift(t *testing.T) {
	store := NewStore()
	_, err := store.CreateAPIKey(contracts.CreateAPIKeyRequest{
		SubjectID: "sub_agent",
		Scopes:    []string{"admin"},
		Token:     "token_agent",
	})
	if err != nil {
		t.Fatalf("create existing key: %v", err)
	}

	_, err = store.ApplySeed(SeedFile{APIKeys: []contracts.CreateAPIKeyRequest{{
		SubjectID: "sub_agent",
		Scopes:    []string{"agent"},
		Token:     "token_agent",
	}}})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
}

func TestApplySeedRejectsSecretDrift(t *testing.T) {
	store := NewStore()
	_, err := store.CreateSecret(contracts.CreateSecretRequest{Name: "provider_token", Value: "old-secret"})
	if err != nil {
		t.Fatalf("create existing secret: %v", err)
	}

	_, err = store.ApplySeed(SeedFile{Secrets: []contracts.CreateSecretRequest{{
		Name:  "provider_token",
		Value: "new-secret",
	}}})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
}

func TestLoadSeedFileRejectsGeneratedTokens(t *testing.T) {
	path := writePolicySeedFile(t, `{"api_keys": [{"subject_id": "sub_agent", "scopes": ["agent"]}]}`)

	_, err := LoadSeedFile(path)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("error = %v, want ErrValidation", err)
	}
}

func writePolicySeedFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy-seed.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write policy seed file: %v", err)
	}
	return path
}
