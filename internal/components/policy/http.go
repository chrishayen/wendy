package policy

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"pacp/internal/contracts"
)

type Handler struct {
	store *Store
}

func NewHandler(store *Store) http.Handler {
	return Handler{store: store}
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case path == "/v1/policy/health" && r.Method == http.MethodGet:
		writeSuccess(w, r, http.StatusOK, contracts.NewComponentHealth("policy", nil))
	case path == "/v1/api-keys" && r.Method == http.MethodPost:
		h.createAPIKey(w, r)
	case strings.HasPrefix(path, "/v1/api-keys/"):
		h.apiKeyRoute(w, r, strings.TrimPrefix(path, "/v1/api-keys/"))
	case path == "/v1/auth/verify" && r.Method == http.MethodPost:
		h.verifyCredential(w, r)
	case path == "/v1/policy/check" && r.Method == http.MethodPost:
		h.checkPolicy(w, r)
	case path == "/v1/policy/rules" && r.Method == http.MethodPost:
		h.createRule(w, r)
	case path == "/v1/secrets" && r.Method == http.MethodPost:
		h.createSecret(w, r)
	case path == "/v1/secrets/resolve" && r.Method == http.MethodPost:
		h.resolveSecret(w, r)
	case path == "/v1/redact" && r.Method == http.MethodPost:
		h.redact(w, r)
	default:
		writeError(w, r, http.StatusNotFound, "not_found", "policy route not found", false)
	}
}

func (h Handler) apiKeyRoute(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	if len(parts) == 2 && parts[1] == "revoke" && r.Method == http.MethodPost {
		record, err := h.store.RevokeAPIKey(parts[0])
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
		writeSuccess(w, r, http.StatusOK, record)
		return
	}
	writeError(w, r, http.StatusNotFound, "not_found", "api key route not found", false)
}

func (h Handler) createAPIKey(w http.ResponseWriter, r *http.Request) {
	var req contracts.CreateAPIKeyRequest
	if !decodeBody(w, r, &req) {
		return
	}
	record, err := h.store.CreateAPIKey(req)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusCreated, record)
}

func (h Handler) verifyCredential(w http.ResponseWriter, r *http.Request) {
	var req contracts.VerifyCredentialRequest
	if !decodeBody(w, r, &req) {
		return
	}
	result, err := h.store.VerifyCredential(req)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, result)
}

func (h Handler) checkPolicy(w http.ResponseWriter, r *http.Request) {
	var req contracts.PolicyCheckRequest
	if !decodeBody(w, r, &req) {
		return
	}
	decision, err := h.store.CheckPolicy(req)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, decision)
}

func (h Handler) createRule(w http.ResponseWriter, r *http.Request) {
	var req contracts.CreatePolicyRuleRequest
	if !decodeBody(w, r, &req) {
		return
	}
	rule, err := h.store.CreateRule(req)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusCreated, rule)
}

func (h Handler) createSecret(w http.ResponseWriter, r *http.Request) {
	var req contracts.CreateSecretRequest
	if !decodeBody(w, r, &req) {
		return
	}
	secret, err := h.store.CreateSecret(req)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusCreated, secret)
}

func (h Handler) resolveSecret(w http.ResponseWriter, r *http.Request) {
	var req contracts.ResolveSecretRequest
	if !decodeBody(w, r, &req) {
		return
	}
	secret, err := h.store.ResolveSecret(req)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, secret)
}

func (h Handler) redact(w http.ResponseWriter, r *http.Request) {
	var req contracts.RedactRequest
	if !decodeBody(w, r, &req) {
		return
	}
	writeSuccess(w, r, http.StatusOK, h.store.Redact(req))
}

func decodeBody(w http.ResponseWriter, r *http.Request, out any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		writeError(w, r, http.StatusBadRequest, "validation_failed", "request body is invalid JSON", false)
		return false
	}
	return true
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "policy resource not found", false)
	case errors.Is(err, ErrValidation):
		writeError(w, r, http.StatusBadRequest, "validation_failed", err.Error(), false)
	case errors.Is(err, ErrMalformedCredential):
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "credential could not be parsed", false)
	case errors.Is(err, ErrForbidden):
		writeError(w, r, http.StatusForbidden, "forbidden", "subject is not authorized for this secret", false)
	case errors.Is(err, ErrConflict):
		writeError(w, r, http.StatusConflict, "resource_conflict", "policy resource already exists", false)
	default:
		writeError(w, r, http.StatusInternalServerError, "internal_error", "policy operation failed", false)
	}
}

func writeSuccess(w http.ResponseWriter, r *http.Request, status int, data any) {
	writeJSON(w, status, contracts.SuccessEnvelope{
		OK:    true,
		Data:  data,
		Links: map[string]any{},
		Meta:  meta(r),
	})
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, retryable bool) {
	writeJSON(w, status, contracts.ErrorEnvelope{
		OK: false,
		Error: contracts.ErrorObject{
			Code: code, Message: message, Retryable: retryable,
		},
		Links: map[string]any{},
		Meta:  meta(r),
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func meta(r *http.Request) map[string]string {
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = "req_policy"
	}
	return map[string]string{"request_id": requestID, "schema_version": "v1"}
}
