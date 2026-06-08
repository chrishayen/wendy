package contracts

type CreateAPIKeyRequest struct {
	SubjectID string   `json:"subject_id"`
	Scopes    []string `json:"scopes"`
	Token     string   `json:"token,omitempty"`
}

type APIKeyRecord struct {
	KeyID     string   `json:"key_id"`
	SubjectID string   `json:"subject_id"`
	Scopes    []string `json:"scopes"`
	Token     string   `json:"token,omitempty"`
	CreatedAt string   `json:"created_at"`
	RevokedAt string   `json:"revoked_at,omitempty"`
}

type VerifyCredentialRequest struct {
	Credential string         `json:"credential"`
	Context    map[string]any `json:"context,omitempty"`
}

type CredentialVerification struct {
	Valid     bool     `json:"valid"`
	SubjectID *string  `json:"subject_id"`
	Scopes    []string `json:"scopes"`
}

type PolicyCheckRequest struct {
	SubjectID string         `json:"subject_id"`
	Action    string         `json:"action"`
	Resource  string         `json:"resource"`
	Context   map[string]any `json:"context,omitempty"`
}

type PolicyDecision struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
}

type CreatePolicyRuleRequest struct {
	SubjectID string `json:"subject_id,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Action    string `json:"action"`
	Resource  string `json:"resource"`
	Effect    string `json:"effect"`
	Reason    string `json:"reason,omitempty"`
}

type PolicyRule struct {
	RuleID    string `json:"rule_id"`
	SubjectID string `json:"subject_id,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Action    string `json:"action"`
	Resource  string `json:"resource"`
	Effect    string `json:"effect"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"created_at"`
}

type CreateSecretRequest struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type SecretRecord struct {
	SecretRef string `json:"secret_ref"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

type ResolveSecretRequest struct {
	SecretRef string `json:"secret_ref"`
	SubjectID string `json:"subject_id"`
}

type ResolvedSecret struct {
	SecretRef string `json:"secret_ref"`
	Value     string `json:"value"`
}

type RedactRequest struct {
	Text string `json:"text"`
}

type RedactResponse struct {
	Text string `json:"text"`
}

type PolicyAuditEvent struct {
	EventType  string `json:"event_type"`
	SubjectID  string `json:"subject_id"`
	Action     string `json:"action,omitempty"`
	Resource   string `json:"resource,omitempty"`
	Allowed    bool   `json:"allowed,omitempty"`
	Reason     string `json:"reason,omitempty"`
	OccurredAt string `json:"occurred_at"`
}
