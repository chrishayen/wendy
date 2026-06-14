package transportauth

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"wendy/internal/contracts"
)

func RequireBearer(next http.Handler, token string) http.Handler {
	if token == "" {
		return next
	}
	expected := authorizationHeader(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(expected)) != 1 {
			writeUnauthorized(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func authorizationHeader(token string) string {
	if strings.HasPrefix(token, "Bearer ") {
		return token
	}
	return "Bearer " + token
}

func writeUnauthorized(w http.ResponseWriter, r *http.Request) {
	writeAuthError(w, r, http.StatusUnauthorized, "unauthorized", "component bearer token is required", false)
}

func writeAuthError(w http.ResponseWriter, r *http.Request, status int, code, message string, retryable bool) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = "req_component"
	}
	_ = json.NewEncoder(w).Encode(contracts.ErrorEnvelope{
		OK: false,
		Error: contracts.ErrorObject{
			Code:      code,
			Message:   message,
			Retryable: retryable,
		},
		Links: map[string]any{},
		Meta:  map[string]string{"request_id": requestID, "schema_version": "v1"},
	})
}
