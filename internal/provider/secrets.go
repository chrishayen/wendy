package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"wendy/internal/contracts"
	"wendy/internal/observability"
)

type SecretResolver interface {
	ResolveSecret(context.Context, string) (string, error)
}

type PolicySecretResolver struct {
	PolicyURL  string
	Credential string
	SubjectID  string
	Client     *http.Client
}

func (r PolicySecretResolver) ResolveSecret(ctx context.Context, secretRef string) (string, error) {
	secretRef = strings.TrimSpace(secretRef)
	if secretRef == "" {
		return "", fmt.Errorf("%w: secret_ref is required", ErrValidation)
	}
	policyURL := strings.TrimRight(strings.TrimSpace(r.PolicyURL), "/")
	if policyURL == "" {
		return "", fmt.Errorf("%w: policy_url is required for secret resolution", ErrValidation)
	}
	subjectID := strings.TrimSpace(r.SubjectID)
	if subjectID == "" {
		return "", fmt.Errorf("%w: subject_id is required for secret resolution", ErrValidation)
	}
	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	body, err := json.Marshal(contracts.ResolveSecretRequest{SecretRef: secretRef, SubjectID: subjectID})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, policyURL+"/v1/secrets/resolve", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if r.Credential != "" {
		req.Header.Set("Authorization", r.Credential)
	}
	observability.PropagateRequestID(ctx, req)
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("%w: policy secret resolution timed out", ErrTimeout)
		}
		return "", fmt.Errorf("%w: resolve secret %s: %s", ErrBackend, secretRef, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if errObj, ok := decodeHTTPBridgeErrorEnvelope(data); ok {
			return "", InvokeError{ErrorObject: errObj, StatusCode: resp.StatusCode}
		}
		return "", fmt.Errorf("%w: resolve secret %s: HTTP %d", ErrBackend, secretRef, resp.StatusCode)
	}
	var envelope struct {
		OK    bool                  `json:"ok"`
		Data  json.RawMessage       `json:"data"`
		Error contracts.ErrorObject `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return "", err
	}
	if !envelope.OK {
		return "", InvokeError{ErrorObject: envelope.Error}
	}
	var resolved contracts.ResolvedSecret
	if err := json.Unmarshal(envelope.Data, &resolved); err != nil {
		return "", err
	}
	if resolved.Value == "" {
		return "", fmt.Errorf("%w: resolved secret %s is empty", ErrValidation, secretRef)
	}
	return resolved.Value, nil
}

func resolveSecretMap(ctx context.Context, resolver SecretResolver, label string, target map[string]string, refs map[string]string) error {
	if len(refs) == 0 {
		return nil
	}
	if resolver == nil {
		return fmt.Errorf("%w: %s requires a secret resolver", ErrValidation, label)
	}
	for name, secretRef := range refs {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("%w: %s name is required", ErrValidation, label)
		}
		if strings.TrimSpace(secretRef) == "" {
			return fmt.Errorf("%w: %s %s secret_ref is required", ErrValidation, label, name)
		}
		value, err := resolver.ResolveSecret(ctx, secretRef)
		if err != nil {
			return fmt.Errorf("%w: %s %s: %s", ErrValidation, label, name, err)
		}
		target[name] = value
	}
	return nil
}
