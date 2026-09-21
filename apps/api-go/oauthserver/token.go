package oauthserver

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"gorm.io/gorm"
)

// Exchange accepts only the ORIGINAL form body, never Request.Form (which
// merges URL parameters). The HTTP adapter must use ReadPublicClientForm and
// reject credentials in headers/URL. Public client IDs are identifiers, not
// proof of client identity. PKCE/refresh possession is required below.
func (s *Server) Exchange(ctx context.Context, rawBody string, binding SenderBinding) (*TokenResponse, error) {
	if !supportedBinding(binding) {
		return nil, protocolError("invalid_request")
	}
	values, err := parseForm(rawBody, "grant_type", "client_id", "redirect_uri", "code", "code_verifier", "resource", "refresh_token", "scope")
	if err != nil {
		return nil, err
	}
	if _, exists := s.clients[values.Get("client_id")]; !exists {
		return nil, protocolError("invalid_client")
	}
	switch values.Get("grant_type") {
	case "authorization_code":
		if values.Has("refresh_token") || values.Has("scope") {
			return nil, protocolError("invalid_request")
		}
		return s.exchangeCode(ctx, values)
	case "refresh_token":
		if values.Has("code") || values.Has("code_verifier") || values.Has("redirect_uri") {
			return nil, protocolError("invalid_request")
		}
		return s.exchangeRefresh(ctx, values)
	default:
		return nil, protocolError("unsupported_grant_type")
	}
}

func (s *Server) exchangeCode(ctx context.Context, values url.Values) (*TokenResponse, error) {
	if !validSecret(values.Get("code"), codePrefix) || !validVerifier(values.Get("code_verifier")) {
		return nil, protocolError("invalid_grant")
	}
	var response *TokenResponse
	err := s.transact(ctx, func(tx *gorm.DB) (*ProtocolError, error) {
		family, err := s.lockFamily(tx, digest(values.Get("code")), true)
		if err != nil {
			return nil, err
		}
		if family == nil || family.ClientID != values.Get("client_id") || family.RedirectURI != values.Get("redirect_uri") || family.Resource != values.Get("resource") {
			return protocolError("invalid_grant"), nil
		}
		var code model.OAuthServerCode
		if err := tx.Where("digest = ? AND issuer = ?", digest(values.Get("code")), s.issuer).Take(&code).Error; err != nil {
			return nil, err
		}
		if !matchesChallenge(values.Get("code_verifier"), code.CodeChallenge) {
			return protocolError("invalid_grant"), nil
		}
		now := s.now()
		if code.UsedAtMs != 0 {
			return protocolError("invalid_grant"), revokeFamily(tx, family, now.UnixMilli(), "authorization_code_reuse")
		}
		if code.ExpiresAtMs <= now.UnixMilli() || !familyLive(family, now) || !s.grantPermitted(tx, *family, family.Scope) {
			return protocolError("invalid_grant"), nil
		}
		// A slow policy/permission lookup must not extend the code lifetime.
		now = s.now()
		if code.ExpiresAtMs <= now.UnixMilli() || !familyLive(family, now) {
			return protocolError("invalid_grant"), nil
		}
		if err := tx.Model(&code).Update("used_at_ms", now.UnixMilli()).Error; err != nil {
			return nil, err
		}
		response, err = s.issuePair(tx, family, family.Scope, now)
		return nil, err
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

func (s *Server) exchangeRefresh(ctx context.Context, values url.Values) (*TokenResponse, error) {
	if !validSecret(values.Get("refresh_token"), refreshPrefix) {
		return nil, protocolError("invalid_grant")
	}
	var response *TokenResponse
	err := s.transact(ctx, func(tx *gorm.DB) (*ProtocolError, error) {
		family, err := s.lockFamily(tx, digest(values.Get("refresh_token")), false)
		if err != nil {
			return nil, err
		}
		if family == nil || family.ClientID != values.Get("client_id") || family.Resource != values.Get("resource") {
			return protocolError("invalid_grant"), nil
		}
		var token model.OAuthServerToken
		if err := tx.Where("digest = ? AND issuer = ?", digest(values.Get("refresh_token")), s.issuer).Take(&token).Error; err != nil {
			return nil, err
		}
		if token.Kind != "refresh" {
			return protocolError("invalid_grant"), nil
		}
		now := s.now()
		// Check replay BEFORE expiry and scope negotiation: used tombstones
		// remain effective even after their original idle TTL. Wrong
		// client/resource cannot revoke; scope changes cannot hide a replay.
		if token.UsedAtMs != 0 {
			return protocolError("invalid_grant"), revokeFamily(tx, family, now.UnixMilli(), "refresh_reuse")
		}
		scope, rejection := refreshScope(values, token.Scope)
		if rejection != nil {
			return rejection, nil
		}
		if token.ExpiresAtMs <= now.UnixMilli() || !familyLive(family, now) || !s.grantPermitted(tx, *family, scope) {
			return protocolError("invalid_grant"), nil
		}
		now = s.now()
		if token.ExpiresAtMs <= now.UnixMilli() || !familyLive(family, now) {
			return protocolError("invalid_grant"), nil
		}
		if err := tx.Model(&token).Update("used_at_ms", now.UnixMilli()).Error; err != nil {
			return nil, err
		}
		response, err = s.issuePair(tx, family, scope, now)
		return nil, err
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

func refreshScope(values url.Values, previous string) (string, *ProtocolError) {
	if !values.Has("scope") {
		return previous, nil
	}
	scopes, err := parseScopes(values.Get("scope"))
	if err != nil || !scopesWithin(scopes, strings.Split(previous, " ")) {
		return "", protocolError("invalid_scope")
	}
	// Narrowing is monotonic: the replacement refresh token stores this
	// subset, so no later member can restore scopes dropped from the chain.
	return strings.Join(scopes, " "), nil
}

func familyLive(family *model.OAuthServerGrant, now time.Time) bool {
	return family.RevokedAtMs == 0 && family.AbsoluteExpiresAtMs > now.UnixMilli()
}

func (s *Server) issuePair(tx *gorm.DB, family *model.OAuthServerGrant, scope string, now time.Time) (*TokenResponse, error) {
	access, err := newSecret(accessPrefix)
	if err != nil {
		return nil, err
	}
	refresh, err := newSecret(refreshPrefix)
	if err != nil {
		return nil, err
	}
	accessExpiry := min(now.Add(AccessTTL).UnixMilli(), family.AbsoluteExpiresAtMs)
	refreshExpiry := min(now.Add(s.idleTTL).UnixMilli(), family.AbsoluteExpiresAtMs)
	rows := []model.OAuthServerToken{
		{Digest: digest(access), Issuer: s.issuer, FamilyID: family.ID, Kind: "access", Scope: scope, CreatedAtMs: now.UnixMilli(), ExpiresAtMs: accessExpiry},
		{Digest: digest(refresh), Issuer: s.issuer, FamilyID: family.ID, Kind: "refresh", Scope: scope, CreatedAtMs: now.UnixMilli(), ExpiresAtMs: refreshExpiry},
	}
	if err := tx.Create(&rows).Error; err != nil {
		return nil, err
	}
	return &TokenResponse{AccessToken: access, TokenType: "Bearer", ExpiresIn: (accessExpiry - now.UnixMilli()) / 1000, RefreshToken: refresh, Scope: scope}, nil
}

// Revoke follows RFC 7009's non-oracle behavior for unknown/foreign tokens.
// Either token type revokes the ENTIRE family, including used refresh members.
// Unsupported token_type_hint values are ignored, not trusted for dispatch.
// Storage failure is an error, NEVER a false successful revocation response.
func (s *Server) Revoke(ctx context.Context, rawBody string) error {
	values, err := parseForm(rawBody, "client_id", "token", "token_type_hint")
	if err != nil {
		return err
	}
	if _, exists := s.clients[values.Get("client_id")]; !exists {
		return protocolError("invalid_client")
	}
	token := values.Get("token")
	if token == "" {
		return protocolError("invalid_request")
	}
	if !validSecret(token, accessPrefix) && !validSecret(token, refreshPrefix) {
		return nil
	}
	return s.transact(ctx, func(tx *gorm.DB) (*ProtocolError, error) {
		family, err := s.lockFamily(tx, digest(token), false)
		if err != nil {
			return nil, err
		}
		if family == nil || family.ClientID != values.Get("client_id") {
			return nil, nil
		}
		return nil, revokeFamily(tx, family, s.now().UnixMilli(), "client_revocation")
	})
}
