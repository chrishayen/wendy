package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"pacp/internal/contracts"
)

var (
	ErrNotFound            = errors.New("policy resource not found")
	ErrValidation          = errors.New("validation failed")
	ErrMalformedCredential = errors.New("credential could not be parsed")
	ErrForbidden           = errors.New("forbidden")
	ErrConflict            = errors.New("policy resource conflict")
)

type Store struct {
	mu           sync.RWMutex
	now          func() time.Time
	nextKeyID    int
	nextRuleID   int
	nextSecretID int
	keysByID     map[string]*apiKeyRecord
	keysByToken  map[string]*apiKeyRecord
	rules        map[string]contracts.PolicyRule
	secrets      map[string]*secretRecord
	audit        []contracts.PolicyAuditEvent
	snapshotPath string
}

type apiKeyRecord struct {
	record  contracts.APIKeyRecord
	token   string
	revoked bool
}

type secretRecord struct {
	record contracts.SecretRecord
	value  string
}

type snapshotFile struct {
	Version      int                             `json:"version"`
	NextKeyID    int                             `json:"next_key_id"`
	NextRuleID   int                             `json:"next_rule_id"`
	NextSecretID int                             `json:"next_secret_id"`
	APIKeys      map[string]apiKeySnapshot       `json:"api_keys"`
	Rules        map[string]contracts.PolicyRule `json:"rules"`
	Secrets      map[string]secretSnapshot       `json:"secrets"`
	Audit        []contracts.PolicyAuditEvent    `json:"audit,omitempty"`
}

type apiKeySnapshot struct {
	Record  contracts.APIKeyRecord `json:"record"`
	Token   string                 `json:"token"`
	Revoked bool                   `json:"revoked"`
}

type secretSnapshot struct {
	Record contracts.SecretRecord `json:"record"`
	Value  string                 `json:"value"`
}

func NewPersistentStore(path string) (*Store, error) {
	store := NewStore()
	store.snapshotPath = path
	if path == "" {
		return store, nil
	}
	if err := store.loadSnapshot(path); err != nil {
		return nil, err
	}
	return store, nil
}

func NewStore() *Store {
	return &Store{
		now:          time.Now,
		nextKeyID:    1,
		nextRuleID:   1,
		nextSecretID: 1,
		keysByID:     map[string]*apiKeyRecord{},
		keysByToken:  map[string]*apiKeyRecord{},
		rules:        map[string]contracts.PolicyRule{},
		secrets:      map[string]*secretRecord{},
	}
}

func (s *Store) HealthDetails() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	activeKeys := 0
	revokedKeys := 0
	for _, key := range s.keysByID {
		if key.revoked {
			revokedKeys++
			continue
		}
		activeKeys++
	}
	return map[string]any{
		"store_backend":         backendLabel(s.snapshotPath),
		"api_key_count":         len(s.keysByID),
		"active_api_key_count":  activeKeys,
		"revoked_api_key_count": revokedKeys,
		"policy_rule_count":     len(s.rules),
		"secret_ref_count":      len(s.secrets),
		"secret_backend":        "local_state_redacted",
		"audit_event_count":     len(s.audit),
		"schema_version":        "v1",
	}
}

func (s *Store) Metrics() contracts.ComponentMetrics {
	s.mu.RLock()
	defer s.mu.RUnlock()
	samples := []contracts.MetricSample{
		contracts.CountMetric("policy_api_keys_total", len(s.keysByID), nil),
		contracts.CountMetric("policy_rules_total", len(s.rules), nil),
		contracts.CountMetric("policy_secret_refs_total", len(s.secrets), nil),
		contracts.CountMetric("policy_audit_events_total", len(s.audit), nil),
	}
	decisions := map[string]int{}
	for _, event := range s.audit {
		if event.EventType != "policy.decision" {
			continue
		}
		decision := "deny"
		if event.Allowed {
			decision = "allow"
		}
		key := event.Action + "\x00" + decision
		decisions[key]++
	}
	for key, count := range decisions {
		action, decision, _ := strings.Cut(key, "\x00")
		if action == "" {
			action = "unknown"
		}
		samples = append(samples, contracts.CountMetric("policy_decisions_total", count, map[string]string{"action": action, "decision": decision}))
	}
	return contracts.NewComponentMetrics("policy", samples)
}

func (s *Store) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

func (s *Store) CreateAPIKey(req contracts.CreateAPIKeyRequest) (contracts.APIKeyRecord, error) {
	if req.SubjectID == "" {
		return contracts.APIKeyRecord{}, fmt.Errorf("%w: subject_id is required", ErrValidation)
	}
	if len(req.Scopes) == 0 {
		return contracts.APIKeyRecord{}, fmt.Errorf("%w: scopes are required", ErrValidation)
	}
	token := req.Token
	if token == "" {
		token = fmt.Sprintf("token_%06d", s.nextKeyID)
	}
	if tokenHasWhitespace(token) {
		return contracts.APIKeyRecord{}, fmt.Errorf("%w: token must not contain whitespace", ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.keysByToken[token]; exists {
		return contracts.APIKeyRecord{}, ErrConflict
	}
	keyID := fmt.Sprintf("key_%06d", s.nextKeyID)
	s.nextKeyID++
	record := contracts.APIKeyRecord{
		KeyID:     keyID,
		SubjectID: req.SubjectID,
		Scopes:    append([]string(nil), req.Scopes...),
		Token:     token,
		CreatedAt: s.formatNow(),
	}
	stored := &apiKeyRecord{record: record, token: token}
	s.keysByID[keyID] = stored
	s.keysByToken[token] = stored
	if err := s.saveLocked(); err != nil {
		return contracts.APIKeyRecord{}, err
	}
	return cloneAPIKey(record), nil
}

func (s *Store) VerifyCredential(req contracts.VerifyCredentialRequest) (contracts.CredentialVerification, error) {
	token, err := parseBearer(req.Credential)
	if err != nil {
		return contracts.CredentialVerification{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.keysByToken[token]
	if !ok || record.revoked {
		return contracts.CredentialVerification{Valid: false, SubjectID: nil, Scopes: []string{}}, nil
	}
	subjectID := record.record.SubjectID
	return contracts.CredentialVerification{
		Valid:     true,
		SubjectID: &subjectID,
		Scopes:    append([]string(nil), record.record.Scopes...),
	}, nil
}

func (s *Store) RevokeAPIKey(keyID string) (contracts.APIKeyRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.keysByID[keyID]
	if !ok {
		return contracts.APIKeyRecord{}, ErrNotFound
	}
	if record.revoked {
		out := cloneAPIKey(record.record)
		out.Token = ""
		return out, nil
	}
	record.revoked = true
	record.record.RevokedAt = s.formatNow()
	if err := s.saveLocked(); err != nil {
		return contracts.APIKeyRecord{}, err
	}
	out := cloneAPIKey(record.record)
	out.Token = ""
	return out, nil
}

func (s *Store) CreateRule(req contracts.CreatePolicyRuleRequest) (contracts.PolicyRule, error) {
	normalized, err := normalizePolicyRuleRequest(req)
	if err != nil {
		return contracts.PolicyRule{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	rule := s.createRuleLocked(normalized)
	if err := s.saveLocked(); err != nil {
		return contracts.PolicyRule{}, err
	}
	return cloneRule(rule), nil
}

func normalizePolicyRuleRequest(req contracts.CreatePolicyRuleRequest) (contracts.CreatePolicyRuleRequest, error) {
	if req.SubjectID == "" && req.Scope == "" {
		return contracts.CreatePolicyRuleRequest{}, fmt.Errorf("%w: subject_id or scope is required", ErrValidation)
	}
	if req.Action == "" {
		return contracts.CreatePolicyRuleRequest{}, fmt.Errorf("%w: action is required", ErrValidation)
	}
	if req.Resource == "" {
		return contracts.CreatePolicyRuleRequest{}, fmt.Errorf("%w: resource is required", ErrValidation)
	}
	if req.Effect != "allow" && req.Effect != "deny" {
		return contracts.CreatePolicyRuleRequest{}, fmt.Errorf("%w: effect must be allow or deny", ErrValidation)
	}
	if req.Reason == "" {
		req.Reason = "rule_" + req.Effect
	}
	return req, nil
}

func (s *Store) createRuleLocked(req contracts.CreatePolicyRuleRequest) contracts.PolicyRule {
	ruleID := fmt.Sprintf("rule_%06d", s.nextRuleID)
	s.nextRuleID++
	rule := contracts.PolicyRule{
		RuleID:    ruleID,
		SubjectID: req.SubjectID,
		Scope:     req.Scope,
		Action:    req.Action,
		Resource:  req.Resource,
		Effect:    req.Effect,
		Reason:    req.Reason,
		CreatedAt: s.formatNow(),
	}
	s.rules[ruleID] = rule
	return rule
}

func (s *Store) CheckPolicy(req contracts.PolicyCheckRequest) (contracts.PolicyDecision, error) {
	if req.SubjectID == "" {
		return contracts.PolicyDecision{}, fmt.Errorf("%w: subject_id is required", ErrValidation)
	}
	if req.Action == "" {
		return contracts.PolicyDecision{}, fmt.Errorf("%w: action is required", ErrValidation)
	}
	if req.Resource == "" {
		decision := contracts.PolicyDecision{Allowed: false, Reason: "unknown_resource"}
		s.recordDecision(req, decision)
		return decision, nil
	}

	s.mu.RLock()
	scopes := s.scopesForSubjectLocked(req.SubjectID)
	rules := make([]contracts.PolicyRule, 0, len(s.rules))
	for _, rule := range s.rules {
		rules = append(rules, rule)
	}
	s.mu.RUnlock()

	if len(scopes) == 0 {
		decision := contracts.PolicyDecision{Allowed: false, Reason: "unknown_subject"}
		s.recordDecision(req, decision)
		return decision, nil
	}
	if rule, ok := matchingRule(req, scopes, rules); ok {
		decision := decisionFromRule(rule)
		s.recordDecision(req, decision)
		return decision, nil
	}
	decision := builtinDecision(req, scopes)
	s.recordDecision(req, decision)
	return decision, nil
}

func (s *Store) CreateSecret(req contracts.CreateSecretRequest) (contracts.SecretRecord, error) {
	normalized, err := normalizeSecretRequest(req)
	if err != nil {
		return contracts.SecretRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.createSecretLocked(normalized)
	if err := s.saveLocked(); err != nil {
		return contracts.SecretRecord{}, err
	}
	return record, nil
}

func normalizeSecretRequest(req contracts.CreateSecretRequest) (contracts.CreateSecretRequest, error) {
	if req.Name == "" {
		return contracts.CreateSecretRequest{}, fmt.Errorf("%w: name is required", ErrValidation)
	}
	if req.Value == "" {
		return contracts.CreateSecretRequest{}, fmt.Errorf("%w: value is required", ErrValidation)
	}
	return req, nil
}

func (s *Store) createSecretLocked(req contracts.CreateSecretRequest) contracts.SecretRecord {
	secretRef := fmt.Sprintf("secret_%06d", s.nextSecretID)
	s.nextSecretID++
	record := contracts.SecretRecord{
		SecretRef: secretRef,
		Name:      req.Name,
		CreatedAt: s.formatNow(),
	}
	s.secrets[secretRef] = &secretRecord{record: record, value: req.Value}
	return record
}

func (s *Store) ResolveSecret(req contracts.ResolveSecretRequest) (contracts.ResolvedSecret, error) {
	if req.SecretRef == "" || req.SubjectID == "" {
		return contracts.ResolvedSecret{}, fmt.Errorf("%w: secret_ref and subject_id are required", ErrValidation)
	}
	s.mu.RLock()
	secret, ok := s.secrets[req.SecretRef]
	scopes := s.scopesForSubjectLocked(req.SubjectID)
	s.mu.RUnlock()
	if !ok {
		return contracts.ResolvedSecret{}, ErrNotFound
	}
	if !hasAnyScope(scopes, "admin", "component") {
		return contracts.ResolvedSecret{}, ErrForbidden
	}
	return contracts.ResolvedSecret{SecretRef: req.SecretRef, Value: secret.value}, nil
}

func (s *Store) Redact(req contracts.RedactRequest) contracts.RedactResponse {
	s.mu.RLock()
	values := make([]string, 0, len(s.secrets))
	for _, secret := range s.secrets {
		values = append(values, secret.value)
	}
	s.mu.RUnlock()
	text := req.Text
	for _, value := range values {
		if value == "" {
			continue
		}
		text = strings.ReplaceAll(text, value, "[REDACTED]")
	}
	return contracts.RedactResponse{Text: text}
}

func (s *Store) AuditEvents() []contracts.PolicyAuditEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]contracts.PolicyAuditEvent(nil), s.audit...)
}

func (s *Store) scopesForSubjectLocked(subjectID string) []string {
	scopeSet := map[string]bool{}
	for _, key := range s.keysByID {
		if key.revoked || key.record.SubjectID != subjectID {
			continue
		}
		for _, scope := range key.record.Scopes {
			scopeSet[scope] = true
		}
	}
	scopes := make([]string, 0, len(scopeSet))
	for scope := range scopeSet {
		scopes = append(scopes, scope)
	}
	return scopes
}

func (s *Store) recordDecision(req contracts.PolicyCheckRequest, decision contracts.PolicyDecision) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audit = append(s.audit, contracts.PolicyAuditEvent{
		EventType:  "policy.decision",
		SubjectID:  req.SubjectID,
		Action:     req.Action,
		Resource:   req.Resource,
		Allowed:    decision.Allowed,
		Reason:     decision.Reason,
		OccurredAt: s.formatNow(),
	})
	_ = s.saveLocked()
}

func (s *Store) formatNow() string {
	return s.now().UTC().Format(time.RFC3339)
}

func parseBearer(credential string) (string, error) {
	if credential == "" {
		return "", ErrMalformedCredential
	}
	parts := strings.Split(credential, " ")
	if len(parts) != 2 || parts[0] != "Bearer" || parts[1] == "" || tokenHasWhitespace(parts[1]) {
		return "", ErrMalformedCredential
	}
	return parts[1], nil
}

func tokenHasWhitespace(token string) bool {
	for _, r := range token {
		if unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

func matchingRule(req contracts.PolicyCheckRequest, scopes []string, rules []contracts.PolicyRule) (contracts.PolicyRule, bool) {
	for _, rule := range rules {
		if !ruleMatchesIdentity(rule, req.SubjectID, scopes) {
			continue
		}
		if rule.Action != req.Action && rule.Action != "*" {
			continue
		}
		if rule.Resource != req.Resource && rule.Resource != "*" {
			continue
		}
		return rule, true
	}
	return contracts.PolicyRule{}, false
}

func ruleMatchesIdentity(rule contracts.PolicyRule, subjectID string, scopes []string) bool {
	if rule.SubjectID != "" && rule.SubjectID != subjectID {
		return false
	}
	if rule.Scope != "" && !hasScope(scopes, rule.Scope) {
		return false
	}
	return true
}

func decisionFromRule(rule contracts.PolicyRule) contracts.PolicyDecision {
	return contracts.PolicyDecision{
		Allowed: rule.Effect == "allow",
		Reason:  rule.Reason,
	}
}

func builtinDecision(req contracts.PolicyCheckRequest, scopes []string) contracts.PolicyDecision {
	if hasScope(scopes, "admin") {
		return allow("allowed_by_admin_scope")
	}
	switch req.Action {
	case "tool.discover":
		if hasScope(scopes, "agent") {
			return allow("allowed_by_agent_scope")
		}
	case "tool.invoke":
		if hasScope(scopes, "agent") && strings.HasPrefix(req.Resource, "cap_") {
			return allow("allowed_by_agent_scope")
		}
	case "job.read":
		if hasScope(scopes, "component") {
			return ownerScoped(req.SubjectID, req.Context, true)
		}
		if hasScope(scopes, "agent") {
			return ownerScoped(req.SubjectID, req.Context, false)
		}
	case "job.cancel":
		if !hasScope(scopes, "agent") {
			return deny("policy_denied")
		}
		if _, ok := contextString(req.Context, "job_state"); !ok {
			return deny("missing_context")
		}
		state, _ := contextString(req.Context, "job_state")
		if state != "queued" {
			return deny("policy_denied")
		}
		return ownerScoped(req.SubjectID, req.Context, false)
	case "artifact.read":
		if hasScope(scopes, "component") {
			return ownerScoped(req.SubjectID, req.Context, true)
		}
		if hasScope(scopes, "agent") {
			return ownerScoped(req.SubjectID, req.Context, false)
		}
	case "lease.read", "lease.cancel":
		if hasScope(scopes, "component") {
			return allow("allowed_by_component_scope")
		}
		if hasScope(scopes, "worker") {
			return allow("allowed_by_worker_scope")
		}
	case "lease.resource.register":
		if hasScope(scopes, "component") {
			return allow("allowed_by_component_scope")
		}
	case "auth.verify", "policy.check", "catalog.read", "catalog.route.read", "catalog.register", "job.create":
		if hasScope(scopes, "component") {
			return allow("allowed_by_component_scope")
		}
	case "job.execute", "lease.request", "lease.heartbeat", "lease.release", "artifact.register", "node.read", "node.service.start", "provider.invoke":
		if hasScope(scopes, "worker") {
			return allow("allowed_by_worker_scope")
		}
	default:
		return deny("unknown_action")
	}
	return deny("policy_denied")
}

func ownerScoped(subjectID string, context map[string]any, componentRead bool) contracts.PolicyDecision {
	owner, ok := contextString(context, "owner_subject_id")
	if !ok || owner == "" {
		return deny("missing_context")
	}
	if componentRead {
		return allow("allowed_by_component_scope")
	}
	if owner == subjectID {
		return allow("allowed_by_owner_context")
	}
	if requester, ok := contextString(context, "requester_id"); ok && requester == subjectID {
		return allow("allowed_by_requester_context")
	}
	return deny("policy_denied")
}

func contextString(context map[string]any, key string) (string, bool) {
	if context == nil {
		return "", false
	}
	value, ok := context[key]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}

func allow(reason string) contracts.PolicyDecision {
	return contracts.PolicyDecision{Allowed: true, Reason: reason}
}

func deny(reason string) contracts.PolicyDecision {
	return contracts.PolicyDecision{Allowed: false, Reason: reason}
}

func hasScope(scopes []string, expected string) bool {
	for _, scope := range scopes {
		if scope == expected {
			return true
		}
	}
	return false
}

func hasAnyScope(scopes []string, expected ...string) bool {
	for _, scope := range expected {
		if hasScope(scopes, scope) {
			return true
		}
	}
	return false
}

func cloneAPIKey(record contracts.APIKeyRecord) contracts.APIKeyRecord {
	raw, _ := json.Marshal(record)
	var cloned contracts.APIKeyRecord
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}

func cloneRule(rule contracts.PolicyRule) contracts.PolicyRule {
	raw, _ := json.Marshal(rule)
	var cloned contracts.PolicyRule
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}

func (s *Store) loadSnapshot(path string) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var snapshot snapshotFile
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return fmt.Errorf("%w: invalid policy snapshot: %v", ErrValidation, err)
	}
	s.nextKeyID = positiveOrDefault(snapshot.NextKeyID, 1)
	s.nextRuleID = positiveOrDefault(snapshot.NextRuleID, 1)
	s.nextSecretID = positiveOrDefault(snapshot.NextSecretID, 1)
	s.keysByID = map[string]*apiKeyRecord{}
	s.keysByToken = map[string]*apiKeyRecord{}
	for keyID, rec := range snapshot.APIKeys {
		record := cloneAPIKey(rec.Record)
		if record.KeyID == "" {
			record.KeyID = keyID
		}
		token := rec.Token
		if token == "" {
			token = record.Token
		}
		stored := &apiKeyRecord{record: record, token: token, revoked: rec.Revoked}
		s.keysByID[record.KeyID] = stored
		if token != "" {
			s.keysByToken[token] = stored
		}
	}
	s.rules = map[string]contracts.PolicyRule{}
	for ruleID, rule := range snapshot.Rules {
		cloned := cloneRule(rule)
		if cloned.RuleID == "" {
			cloned.RuleID = ruleID
		}
		s.rules[cloned.RuleID] = cloned
	}
	s.secrets = map[string]*secretRecord{}
	for secretRef, rec := range snapshot.Secrets {
		record := rec.Record
		if record.SecretRef == "" {
			record.SecretRef = secretRef
		}
		s.secrets[record.SecretRef] = &secretRecord{record: record, value: rec.Value}
	}
	s.audit = append([]contracts.PolicyAuditEvent(nil), snapshot.Audit...)
	return nil
}

func (s *Store) saveLocked() error {
	if s.snapshotPath == "" {
		return nil
	}
	snapshot := snapshotFile{
		Version:      1,
		NextKeyID:    s.nextKeyID,
		NextRuleID:   s.nextRuleID,
		NextSecretID: s.nextSecretID,
		APIKeys:      map[string]apiKeySnapshot{},
		Rules:        map[string]contracts.PolicyRule{},
		Secrets:      map[string]secretSnapshot{},
		Audit:        append([]contracts.PolicyAuditEvent(nil), s.audit...),
	}
	for keyID, rec := range s.keysByID {
		snapshot.APIKeys[keyID] = apiKeySnapshot{Record: cloneAPIKey(rec.record), Token: rec.token, Revoked: rec.revoked}
	}
	for ruleID, rule := range s.rules {
		snapshot.Rules[ruleID] = cloneRule(rule)
	}
	for secretRef, rec := range s.secrets {
		snapshot.Secrets[secretRef] = secretSnapshot{Record: rec.record, Value: rec.value}
	}
	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.snapshotPath), 0o755); err != nil {
		return err
	}
	tmpPath := s.snapshotPath + ".tmp"
	if err := os.WriteFile(tmpPath, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.snapshotPath)
}

func positiveOrDefault(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func backendLabel(path string) string {
	if path == "" {
		return "memory"
	}
	return "file_snapshot"
}
