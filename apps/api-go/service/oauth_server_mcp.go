package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oauthserver"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const oauthMCPClientPrefix = "lmm-mcp-"

var ErrMCPRegistration = errors.New("invalid MCP client metadata")
var ErrMCPRegistryFull = errors.New("MCP registration capacity reached")

func OAuthMarketScopes() []string {
	return []string{OAuthMarketDiscoverScope, OAuthMarketInvokeScope, OAuthMarketManageScope}
}
func (s *OAuthIntegration) MarketResource() string { return s.Issuer + "/mcp/market" }

func validMCPScopes(scopes []string) bool {
	if !slices.Contains(scopes, OAuthMarketDiscoverScope) {
		return false
	}
	seen := make(map[string]bool)
	for _, scope := range scopes {
		if !slices.Contains(OAuthMarketScopes(), scope) || seen[scope] {
			return false
		}
		seen[scope] = true
	}
	return true
}

type MCPClientRegistration struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	Scope                   string   `json:"scope"`
}

type MCPRegisteredClient struct {
	MCPClientRegistration
	ClientID         string `json:"client_id"`
	ClientIDIssuedAt int64  `json:"client_id_issued_at"`
}

// RegisterMCPClient accepts only public native clients, code + PKCE and fixed
// marketplace scopes. It never fetches caller-supplied metadata URLs or keys.
func (s *OAuthIntegration) RegisterMCPClient(ctx context.Context, in MCPClientRegistration) (*MCPRegisteredClient, error) {
	if in.TokenEndpointAuthMethod != "" && in.TokenEndpointAuthMethod != "none" || len(in.RedirectURIs) < 1 || len(in.RedirectURIs) > 4 {
		return nil, ErrMCPRegistration
	}
	if len(in.ResponseTypes) != 0 && !slices.Equal(in.ResponseTypes, []string{"code"}) {
		return nil, ErrMCPRegistration
	}
	if len(in.GrantTypes) != 0 {
		grants := slices.Clone(in.GrantTypes)
		slices.Sort(grants)
		if !slices.Equal(grants, []string{"authorization_code", "refresh_token"}) {
			return nil, ErrMCPRegistration
		}
	}
	name := strings.TrimSpace(in.ClientName)
	if name == "" {
		name = "MCP client"
	}
	if len(name) > 96 || !utf8.ValidString(name) {
		return nil, ErrMCPRegistration
	}
	for _, r := range name {
		if r < 32 || r == 127 {
			return nil, ErrMCPRegistration
		}
	}
	scopes := OAuthMarketScopes()
	if in.Scope != "" {
		scopes = strings.Split(in.Scope, " ")
	}
	if !validMCPScopes(scopes) {
		return nil, ErrMCPRegistration
	}
	slices.Sort(scopes)
	redirects := make([]string, 0, len(in.RedirectURIs))
	for _, raw := range in.RedirectURIs {
		template, ok := oauthserver.NativeRedirectTemplate(raw)
		if !ok {
			return nil, ErrMCPRegistration
		}
		if !slices.Contains(redirects, template) {
			redirects = append(redirects, template)
		}
	}
	slices.Sort(redirects)
	// Prefix distinguishes an unverified external label from trusted adapters.
	row := model.OAuthServerMCPClient{Issuer: s.Issuer, Name: "External MCP: " + name, RedirectURIs: redirects, Scope: strings.Join(scopes, " "), CreatedAtMs: time.Now().UnixMilli()}
	identity, _ := json.Marshal([]any{row.Issuer, row.Name, row.RedirectURIs, row.Scope})
	digest := sha256.Sum256(identity)
	row.ID = oauthMCPClientPrefix + hex.EncodeToString(digest[:])
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		registry := model.OAuthServerMCPRegistry{Issuer: s.Issuer}
		// First statement acquires the writer lock, including on SQLite. The
		// no-op update serializes duplicate registrations without a second pool.
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "issuer"}}, DoUpdates: clause.Assignments(map[string]any{"count": gorm.Expr("oauth_server_mcp_registries.count")})}).Create(&registry).Error; err != nil {
			return err
		}
		var existing model.OAuthServerMCPClient
		err := tx.Where("id = ? AND issuer = ?", row.ID, s.Issuer).Take(&existing).Error
		if err == nil {
			row = existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		updated := tx.Model(&model.OAuthServerMCPRegistry{}).Where("issuer = ? AND count < ?", s.Issuer, 4096).UpdateColumn("count", gorm.Expr("count + 1"))
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrMCPRegistryFull
		}
		return tx.Create(&row).Error
	})
	if err != nil {
		return nil, err
	}
	return &MCPRegisteredClient{ClientID: row.ID, ClientIDIssuedAt: row.CreatedAtMs / 1000, MCPClientRegistration: MCPClientRegistration{ClientName: row.Name, RedirectURIs: slices.Clone(in.RedirectURIs), TokenEndpointAuthMethod: "none", GrantTypes: []string{"authorization_code", "refresh_token"}, ResponseTypes: []string{"code"}, Scope: row.Scope}}, nil
}

func (s *OAuthIntegration) lookupMCPClient(db *gorm.DB, id string) (oauthserver.NativeClient, bool) {
	if db == nil || !strings.HasPrefix(id, oauthMCPClientPrefix) || len(id) != len(oauthMCPClientPrefix)+64 {
		return oauthserver.NativeClient{}, false
	}
	var row model.OAuthServerMCPClient
	if db.Where("id = ? AND issuer = ?", id, s.Issuer).Take(&row).Error != nil {
		return oauthserver.NativeClient{}, false
	}
	scopes := strings.Split(row.Scope, " ")
	if !validMCPScopes(scopes) {
		return oauthserver.NativeClient{}, false
	}
	return oauthserver.NativeClient{ID: row.ID, Name: row.Name, RedirectURIs: row.RedirectURIs, Resources: []string{s.MarketResource()}, Scopes: scopes}, true
}

// Only registered MCP clients get defaults. Explicit resources/scopes are
// never widened; duplicate query fields remain for the core to reject.
func (s *OAuthIntegration) MCPAuthorizationQuery(ctx context.Context, raw string) (string, error) {
	if len(raw) > 16384 {
		return "", ErrMCPRegistration
	}
	q, err := url.ParseQuery(raw)
	if err != nil {
		return "", ErrMCPRegistration
	}
	if !strings.HasPrefix(q.Get("client_id"), oauthMCPClientPrefix) {
		return raw, nil
	}
	client, ok := s.lookupMCPClient(s.DB.WithContext(ctx), q.Get("client_id"))
	if !ok {
		return "", ErrMCPRegistration
	}
	if _, present := q["resource"]; !present {
		q.Set("resource", s.MarketResource())
	}
	if _, present := q["scope"]; !present {
		q.Set("scope", strings.Join(client.Scopes, " "))
	}
	return q.Encode(), nil
}

func oauthMCPUser(db *gorm.DB, userID int64) (*model.User, error) {
	if db == nil || userID <= 0 {
		return nil, ErrOAuthDenied
	}
	if _, transactional := db.Statement.ConnPool.(gorm.TxCommitter); transactional && db.Dialector.Name() == "postgres" {
		db = db.Clauses(clause.Locking{Strength: "SHARE"})
	}
	var user model.User
	if db.First(&user, "id = ?", userID).Error != nil || user.Status != common.UserStatusEnabled {
		return nil, ErrOAuthDenied
	}
	return &user, nil
}

// Market-only credentials must not authorize model relay or account resources.
// Existing trusted adapters retain their legacy audience for compatibility.
func (s *OAuthIntegration) ValidateMarketResource(ctx context.Context, token string, scopes ...string) (oauthserver.Grant, *model.User, error) {
	grant, err := s.Core.ValidateAccess(ctx, oauthserver.AccessRequest{Token: token, Resource: s.MarketResource(), RequiredScopes: scopes})
	if err != nil {
		return s.ValidateResource(ctx, token, scopes...)
	}
	user, err := oauthMCPUser(s.DB.WithContext(ctx), grant.UserID)
	return *grant, user, err
}
