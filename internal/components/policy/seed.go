package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"pacp/internal/contracts"
)

type SeedFile struct {
	APIKeys []contracts.CreateAPIKeyRequest     `json:"api_keys,omitempty"`
	Rules   []contracts.CreatePolicyRuleRequest `json:"rules,omitempty"`
	Secrets []contracts.CreateSecretRequest     `json:"secrets,omitempty"`
}

type SeedResult struct {
	APIKeysCreated int `json:"api_keys_created"`
	APIKeysSkipped int `json:"api_keys_skipped"`
	RulesCreated   int `json:"rules_created"`
	RulesSkipped   int `json:"rules_skipped"`
	SecretsCreated int `json:"secrets_created"`
	SecretsSkipped int `json:"secrets_skipped"`
}

func LoadSeedFile(path string) (SeedFile, error) {
	if path == "" {
		return SeedFile{}, fmt.Errorf("%w: seed path is required", ErrValidation)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return SeedFile{}, err
	}
	var seed SeedFile
	if err := json.Unmarshal(raw, &seed); err != nil {
		return SeedFile{}, fmt.Errorf("%w: invalid policy seed: %v", ErrValidation, err)
	}
	if err := validateSeed(seed); err != nil {
		return SeedFile{}, err
	}
	return seed, nil
}

func (s *Store) ApplySeed(seed SeedFile) (SeedResult, error) {
	if err := validateSeed(seed); err != nil {
		return SeedResult{}, err
	}
	result := SeedResult{}
	for i, req := range seed.APIKeys {
		created, err := s.ensureAPIKey(req)
		if err != nil {
			return result, fmt.Errorf("api_keys[%d]: %w", i, err)
		}
		if created {
			result.APIKeysCreated++
		} else {
			result.APIKeysSkipped++
		}
	}
	for i, req := range seed.Rules {
		created, err := s.ensurePolicyRule(req)
		if err != nil {
			return result, fmt.Errorf("rules[%d]: %w", i, err)
		}
		if created {
			result.RulesCreated++
		} else {
			result.RulesSkipped++
		}
	}
	for i, req := range seed.Secrets {
		created, err := s.ensureSecret(req)
		if err != nil {
			return result, fmt.Errorf("secrets[%d]: %w", i, err)
		}
		if created {
			result.SecretsCreated++
		} else {
			result.SecretsSkipped++
		}
	}
	return result, nil
}

func validateSeed(seed SeedFile) error {
	if len(seed.APIKeys) == 0 && len(seed.Rules) == 0 && len(seed.Secrets) == 0 {
		return fmt.Errorf("%w: seed must contain at least one api key, rule, or secret", ErrValidation)
	}
	for i, req := range seed.APIKeys {
		if req.SubjectID == "" {
			return fmt.Errorf("%w: api_keys[%d].subject_id is required", ErrValidation, i)
		}
		if len(req.Scopes) == 0 {
			return fmt.Errorf("%w: api_keys[%d].scopes are required", ErrValidation, i)
		}
		if req.Token == "" {
			return fmt.Errorf("%w: api_keys[%d].token is required for seed files", ErrValidation, i)
		}
		if tokenHasWhitespace(req.Token) {
			return fmt.Errorf("%w: api_keys[%d].token must not contain whitespace", ErrValidation, i)
		}
	}
	for i, req := range seed.Rules {
		if _, err := normalizePolicyRuleRequest(req); err != nil {
			return fmt.Errorf("rules[%d]: %w", i, err)
		}
	}
	for i, req := range seed.Secrets {
		if _, err := normalizeSecretRequest(req); err != nil {
			return fmt.Errorf("secrets[%d]: %w", i, err)
		}
	}
	return nil
}

func (s *Store) ensureAPIKey(req contracts.CreateAPIKeyRequest) (bool, error) {
	_, err := s.CreateAPIKey(req)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, ErrConflict) {
		return false, err
	}
	verification, verifyErr := s.VerifyCredential(contracts.VerifyCredentialRequest{Credential: "Bearer " + req.Token})
	if verifyErr == nil && verification.Valid && verification.SubjectID != nil && *verification.SubjectID == req.SubjectID && sameScopeSet(verification.Scopes, req.Scopes) {
		return false, nil
	}
	return false, err
}

func (s *Store) ensurePolicyRule(req contracts.CreatePolicyRuleRequest) (bool, error) {
	normalized, err := normalizePolicyRuleRequest(req)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rule := range s.rules {
		if samePolicyRule(rule, normalized) {
			return false, nil
		}
	}
	s.createRuleLocked(normalized)
	if err := s.saveLocked(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) ensureSecret(req contracts.CreateSecretRequest) (bool, error) {
	normalized, err := normalizeSecretRequest(req)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, secret := range s.secrets {
		if secret.record.Name != normalized.Name {
			continue
		}
		if secret.value == normalized.Value {
			return false, nil
		}
		return false, fmt.Errorf("%w: secret %q already exists with a different value", ErrConflict, normalized.Name)
	}
	s.createSecretLocked(normalized)
	if err := s.saveLocked(); err != nil {
		return false, err
	}
	return true, nil
}

func samePolicyRule(rule contracts.PolicyRule, req contracts.CreatePolicyRuleRequest) bool {
	return rule.SubjectID == req.SubjectID &&
		rule.Scope == req.Scope &&
		rule.Action == req.Action &&
		rule.Resource == req.Resource &&
		rule.Effect == req.Effect &&
		rule.Reason == req.Reason
}

func sameScopeSet(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	seen := map[string]int{}
	for _, scope := range actual {
		seen[scope]++
	}
	for _, scope := range expected {
		if seen[scope] == 0 {
			return false
		}
		seen[scope]--
	}
	for _, count := range seen {
		if count != 0 {
			return false
		}
	}
	return true
}
