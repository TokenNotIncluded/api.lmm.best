package oauthserver

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"gorm.io/gorm"
)

// BeginAuthorization validates the raw, unmerged query and opens a five-minute
// transaction. browserBinding MUST come from the trusted browser session, not
// a query/body field. It must be high-entropy and remain stable through consent.
// No user or authorization code is assigned at this stage.
func (s *Server) BeginAuthorization(ctx context.Context, rawQuery, browserBinding string) (*PendingAuthorization, error) {
	if !validBrowserBinding(browserBinding) {
		return nil, protocolError("invalid_request")
	}
	values, err := parseForm(rawQuery, "response_type", "client_id", "redirect_uri", "scope", "state", "code_challenge", "code_challenge_method", "resource")
	if err != nil {
		return nil, err
	}
	client, exists := s.clients[values.Get("client_id")]
	if !exists {
		return nil, protocolError("invalid_client")
	}
	redirect, state := values.Get("redirect_uri"), values.Get("state")
	if !validRedirect(redirect, client) {
		return nil, protocolError("invalid_request")
	}
	if values.Get("response_type") != "code" {
		return nil, s.authorizationError("unsupported_response_type", redirect, state)
	}
	if values.Get("code_challenge_method") != "S256" || !validChallenge(values.Get("code_challenge")) || len(state) < 16 || len(state) > 512 || !printableASCII(state) {
		return nil, s.authorizationError("invalid_request", redirect, state)
	}
	if !contains(client.Resources, values.Get("resource")) {
		return nil, s.authorizationError("invalid_target", redirect, state)
	}
	scopes, err := parseScopes(values.Get("scope"))
	if err != nil || !scopesWithin(scopes, client.Scopes) {
		return nil, s.authorizationError("invalid_scope", redirect, state)
	}
	handle, err := newSecret(transactionPrefix)
	if err != nil {
		return nil, storageError(err)
	}
	now := s.now()
	row := model.OAuthServerAuthorization{
		Digest: digest(handle), Issuer: s.issuer, ClientID: client.ID, RedirectURI: values.Get("redirect_uri"),
		Resource: values.Get("resource"), Scope: strings.Join(scopes, " "), State: state,
		CodeChallenge: values.Get("code_challenge"), BrowserDigest: browserDigest(browserBinding),
		CreatedAtMs: now.UnixMilli(), ExpiresAtMs: now.Add(AuthorizationTTL).UnixMilli(),
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return nil, storageError(err)
	}
	pending := s.pendingView(row, handle)
	return &pending, nil
}

// authorizationError is called only after exact redirect/client validation.
// Never construct an error redirect by reparsing an untrusted request in a
// generic HTTP error handler. Omit malformed/oversized state rather than echo it.
func (s *Server) authorizationError(code, redirect, state string) *ProtocolError {
	err := protocolError(code)
	query := url.Values{"error": {code}, "iss": {s.issuer}}
	if state != "" && len(state) <= 512 && printableASCII(state) {
		query.Set("state", state)
	}
	err.RedirectURI = redirect + "?" + query.Encode()
	return err
}

func (s *Server) pendingView(row model.OAuthServerAuthorization, handle string) PendingAuthorization {
	return PendingAuthorization{Transaction: handle, ClientID: row.ClientID, ClientName: s.clients[row.ClientID].Name,
		RedirectURI: row.RedirectURI, Resource: row.Resource, Scopes: strings.Split(row.Scope, " "), ExpiresAt: time.UnixMilli(row.ExpiresAtMs)}
}

// TrustedPrepareConsent is an INTERNAL browser-authentication boundary. The
// adapter must verify the current session identity (not a client-supplied user
// ID), protect against login CSRF, and bind the transaction to that session.
// Rebinding an existing transaction, including to the same user, is rejected.
// Account/session changes require a NEW transaction. Do not expose this as an
// externally callable approval API accepting just userID.
func (s *Server) TrustedPrepareConsent(ctx context.Context, transaction, browserBinding string, authenticatedUserID int64) (*Consent, error) {
	if authenticatedUserID <= 0 || !validSecret(transaction, transactionPrefix) || !validBrowserBinding(browserBinding) {
		return nil, protocolError("invalid_request")
	}
	var consent Consent
	err := s.transact(ctx, func(tx *gorm.DB) (*ProtocolError, error) {
		row, err := s.lockAuthorization(tx, transaction)
		if err != nil {
			return nil, err
		}
		if !s.authorizationLive(row, browserBinding) || row.UserID != 0 {
			return protocolError("invalid_request"), nil
		}
		row.UserID = authenticatedUserID
		family := authorizationGrant(*row)
		if !s.grantPermitted(tx, family, row.Scope) {
			return protocolError("access_denied"), nil
		}
		if !s.authorizationLive(row, browserBinding) {
			return protocolError("invalid_request"), nil
		}
		secret, err := newSecret(consentPrefix)
		if err != nil {
			return nil, err
		}
		if err := tx.Model(row).Updates(map[string]any{"user_id": authenticatedUserID, "consent_digest": digest(secret)}).Error; err != nil {
			return nil, err
		}
		consent = Consent{PendingAuthorization: s.pendingView(*row, transaction), UserID: authenticatedUserID, Secret: secret}
		return nil, nil
	})
	if err != nil {
		return nil, err
	}
	return &consent, nil
}

// TrustedApprove requires both the original browser binding and the one-time
// consent secret. It cannot accept an externally chosen user, scope, redirect,
// resource or client. The HTTP adapter still MUST enforce same-origin POST,
// CSRF, current session identity and an actual informed user decision.
func (s *Server) TrustedApprove(ctx context.Context, transaction, browserBinding, consentSecret string) (*AuthorizationResponse, error) {
	return s.decide(ctx, transaction, browserBinding, consentSecret, true)
}

func (s *Server) TrustedDeny(ctx context.Context, transaction, browserBinding, consentSecret string) (*AuthorizationResponse, error) {
	return s.decide(ctx, transaction, browserBinding, consentSecret, false)
}

func (s *Server) decide(ctx context.Context, transaction, browserBinding, consentSecret string, approve bool) (*AuthorizationResponse, error) {
	if !validSecret(transaction, transactionPrefix) || !validSecret(consentSecret, consentPrefix) || !validBrowserBinding(browserBinding) {
		return nil, protocolError("invalid_request")
	}
	var response AuthorizationResponse
	err := s.transact(ctx, func(tx *gorm.DB) (*ProtocolError, error) {
		row, err := s.lockAuthorization(tx, transaction)
		if err != nil {
			return nil, err
		}
		if !s.authorizationLive(row, browserBinding) || row.UserID <= 0 || !equalSecret(row.ConsentDigest, digest(consentSecret)) {
			return protocolError("invalid_request"), nil
		}
		family := authorizationGrant(*row)
		if approve && !s.grantPermitted(tx, family, row.Scope) {
			return protocolError("access_denied"), nil
		}
		now := s.now()
		if row.ExpiresAtMs <= now.UnixMilli() {
			return protocolError("invalid_request"), nil
		}
		if err := tx.Model(row).Update("consumed_at_ms", now.UnixMilli()).Error; err != nil {
			return nil, err
		}
		query := url.Values{"state": {row.State}, "iss": {s.issuer}}
		if approve {
			code, err := s.createCode(tx, &family, row.CodeChallenge, now)
			if err != nil {
				return nil, err
			}
			query.Set("code", code)
		} else {
			query.Set("error", "access_denied")
		}
		// Redirects have no pre-existing query and were validated before storage.
		response.RedirectURI = row.RedirectURI + "?" + query.Encode()
		return nil, nil
	})
	if err != nil {
		return nil, err
	}
	return &response, nil
}

func authorizationGrant(row model.OAuthServerAuthorization) model.OAuthServerGrant {
	return model.OAuthServerGrant{Issuer: row.Issuer, ClientID: row.ClientID, UserID: row.UserID,
		RedirectURI: row.RedirectURI, Resource: row.Resource, Scope: row.Scope}
}

func (s *Server) createCode(tx *gorm.DB, family *model.OAuthServerGrant, challenge string, now time.Time) (string, error) {
	id, err := newSecret("")
	if err != nil {
		return "", err
	}
	code, err := newSecret(codePrefix)
	if err != nil {
		return "", err
	}
	family.ID, family.CreatedAtMs = id, now.UnixMilli()
	family.AbsoluteExpiresAtMs = now.Add(s.absoluteTTL).UnixMilli()
	if err := tx.Create(family).Error; err != nil {
		return "", err
	}
	row := model.OAuthServerCode{Digest: digest(code), Issuer: s.issuer, FamilyID: id, CodeChallenge: challenge,
		CreatedAtMs: now.UnixMilli(), ExpiresAtMs: now.Add(CodeTTL).UnixMilli()}
	if err := tx.Create(&row).Error; err != nil {
		return "", err
	}
	return code, nil
}

func (s *Server) lockAuthorization(tx *gorm.DB, handle string) (*model.OAuthServerAuthorization, error) {
	where := tx.Model(&model.OAuthServerAuthorization{}).Where("digest = ? AND issuer = ?", digest(handle), s.issuer)
	locked := where.UpdateColumn("lock_version", gorm.Expr("lock_version + 1"))
	if locked.Error != nil {
		return nil, locked.Error
	}
	if locked.RowsAffected != 1 {
		return nil, nil
	}
	var row model.OAuthServerAuthorization
	err := tx.Where("digest = ? AND issuer = ?", digest(handle), s.issuer).Take(&row).Error
	return &row, err
}

func (s *Server) authorizationLive(row *model.OAuthServerAuthorization, binding string) bool {
	return row != nil && row.Issuer == s.issuer && row.ConsumedAtMs == 0 && row.ExpiresAtMs > s.now().UnixMilli() && equalSecret(row.BrowserDigest, browserDigest(binding))
}
