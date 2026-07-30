package gateway

import (
	"net/http"
	"strings"

	"github.com/uuos-ai/llmkit/identity"
	"github.com/uuos-ai/llmkit/managed"
)

func (s *Server) exchangeOIDC(writer http.ResponseWriter, request *http.Request) {
	var input managed.OIDCExchangeRequest
	if !s.decode(writer, request, &input) {
		return
	}
	if input.Assertion == "" || input.ClientInstanceID == "" {
		writeAPIError(writer, http.StatusBadRequest, "invalid_request", "assertion and client_instance_id are required")
		return
	}
	pair, err := s.config.IdentityService.ExchangeOIDC(request.Context(), input)
	if err != nil {
		writeAPIError(writer, http.StatusUnauthorized, "authentication", "OIDC exchange was rejected")
		return
	}
	writeJSON(writer, http.StatusOK, pair)
}

func (s *Server) refreshToken(writer http.ResponseWriter, request *http.Request) {
	if !requireScope(writer, request, identity.ScopeTokensRefresh) {
		return
	}
	var input struct {
		RefreshToken string `json:"refresh_token"`
	}
	if !s.decode(writer, request, &input) {
		return
	}
	idempotencyKey := request.Header.Get("Idempotency-Key")
	accessToken := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	if input.RefreshToken == "" || idempotencyKey == "" {
		writeAPIError(writer, http.StatusBadRequest, "invalid_request", "refresh_token and Idempotency-Key are required")
		return
	}
	pair, err := s.config.IdentityService.RefreshToken(request.Context(), managed.RefreshTokenRequest{AccessToken: accessToken, RefreshToken: input.RefreshToken, IdempotencyKey: idempotencyKey})
	if err != nil {
		writeAPIError(writer, http.StatusUnauthorized, "authentication", "token refresh was rejected")
		return
	}
	writeJSON(writer, http.StatusOK, pair)
}

func (s *Server) bindUser(writer http.ResponseWriter, request *http.Request) {
	if !requireScope(writer, request, identity.ScopeUsersBind) {
		return
	}
	var input struct {
		UserID                 string `json:"user_id"`
		ExpectedBindingVersion uint64 `json:"expected_binding_version"`
		Proof                  []byte `json:"proof,omitempty"`
	}
	if !s.decode(writer, request, &input) {
		return
	}
	if input.UserID == "" || request.Header.Get("Idempotency-Key") == "" {
		writeAPIError(writer, http.StatusBadRequest, "invalid_request", "user_id and Idempotency-Key are required")
		return
	}
	principal, _ := identity.FromContext(request.Context())
	tokens, binding, err := s.config.IdentityService.BindUser(request.Context(), managed.BindUserRequest{Principal: principal, UserID: input.UserID, ExpectedBindingVersion: input.ExpectedBindingVersion, IdempotencyKey: request.Header.Get("Idempotency-Key"), Proof: input.Proof})
	if err != nil {
		writeAPIError(writer, http.StatusConflict, "user_binding_conflict", "user binding could not be changed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"tokens": tokens, "binding": binding})
}
