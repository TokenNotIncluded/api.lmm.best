package oauthserver

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"gorm.io/gorm"
)

// ValidateAccess is an INTERNAL resource-server API, not a public introspection
// endpoint. The resource and required scopes must be supplied by trusted route
// policy, NOT selected from client request parameters. Validate on each request
// against the writer DB; never feed these tokens to ordinary API-key parsing.
func (s *Server) ValidateAccess(ctx context.Context, request AccessRequest) (*Grant, error) {
	if !validSecret(request.Token, accessPrefix) || !supportedBinding(request.Binding) || !validHTTPS(request.Resource) {
		return nil, protocolError("invalid_token")
	}
	if len(request.RequiredScopes) > 0 {
		if _, err := parseScopes(strings.Join(request.RequiredScopes, " ")); err != nil {
			return nil, protocolError("invalid_token")
		}
	}
	// Token/family and all Policy reads share one connection and snapshot. Do
	// not move Policy outside this transaction: that could mix a revoked grant
	// with newer permissions, or require another connection from a full pool.
	// Policy may lock permission facts (e.g. paid activation), so do not mark
	// the SQL transaction READ ONLY; the core itself performs no writes here.
	options := &sql.TxOptions{}
	if s.db.Dialector.Name() == "postgres" {
		options.Isolation = sql.LevelRepeatableRead
	}
	var grant *Grant
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		grant, err = s.validateAccess(tx, request)
		return err
	}, options)
	if err != nil {
		return nil, storageError(err)
	}
	return grant, nil
}

func (s *Server) validateAccess(tx *gorm.DB, request AccessRequest) (*Grant, error) {
	var active struct {
		model.OAuthServerGrant
		TokenScope       string
		TokenExpiresAtMs int64
	}
	// One statement gives a consistent token/family revocation snapshot. A
	// request already in flight before revocation cannot be retroactively undone.
	err := tx.Table("oauth_server_tokens AS tok").
		Select("family.*, tok.scope AS token_scope, tok.expires_at_ms AS token_expires_at_ms").
		Joins("JOIN oauth_server_grants AS family ON family.id = tok.family_id AND family.issuer = tok.issuer").
		Where("tok.digest = ? AND tok.issuer = ? AND tok.kind = ? AND family.resource = ? AND family.revoked_at_ms = 0", digest(request.Token), s.issuer, "access", request.Resource).
		Take(&active).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, protocolError("invalid_token")
	}
	if err != nil {
		return nil, storageError(err)
	}
	// Keep the resource comparison exact even with an unusual DB collation.
	if active.Resource != request.Resource || !scopesWithin(request.RequiredScopes, strings.Split(active.TokenScope, " ")) || !s.grantPermitted(tx, active.OAuthServerGrant, active.TokenScope) {
		return nil, protocolError("invalid_token")
	}
	now := s.now()
	if active.TokenExpiresAtMs <= now.UnixMilli() || !familyLive(&active.OAuthServerGrant, now) {
		return nil, protocolError("invalid_token")
	}
	grant := grantView(active.OAuthServerGrant, active.TokenScope)
	return &grant, nil
}
