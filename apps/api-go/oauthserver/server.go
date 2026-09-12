package oauthserver

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"gorm.io/gorm"
)

// Server never registers routes or runs migrations. It must use the primary
// database, with no read-replica resolver and no authorization-result caching.
type Server struct {
	db          *gorm.DB
	issuer      string
	clients     map[string]NativeClient
	policy      Policy
	absoluteTTL time.Duration
	idleTTL     time.Duration
	now         func() time.Time
}

func New(db *gorm.DB, config Config, policy Policy) (*Server, error) {
	if db == nil || db.Error != nil || db.Statement == nil || policy == nil {
		return nil, fmt.Errorf("oauth server: a healthy writer database and policy are required")
	}
	// An outer transaction could roll back an already-reported revocation or
	// token issuance. The server must own the actual commit, not a savepoint.
	if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested || db.DryRun {
		return nil, fmt.Errorf("oauth server: a root, non-dry-run writer handle is required")
	}
	if name := db.Dialector.Name(); name != "sqlite" && name != "postgres" {
		return nil, fmt.Errorf("oauth server: unsupported database dialect %q", name)
	}
	if !validIssuer(config.Issuer) {
		return nil, fmt.Errorf("oauth server: issuer must be a canonical HTTPS DNS origin")
	}
	if config.RefreshAbsoluteTTL == 0 {
		config.RefreshAbsoluteTTL = DefaultRefreshAbsoluteTTL
	}
	if config.RefreshIdleTTL == 0 {
		config.RefreshIdleTTL = DefaultRefreshIdleTTL
	}
	if config.RefreshAbsoluteTTL < AccessTTL || config.RefreshAbsoluteTTL > 90*24*time.Hour || config.RefreshIdleTTL < AccessTTL || config.RefreshIdleTTL > config.RefreshAbsoluteTTL {
		return nil, fmt.Errorf("oauth server: refresh lifetimes must satisfy 10m <= idle <= absolute <= 90d")
	}
	if len(config.Clients) == 0 {
		return nil, fmt.Errorf("oauth server: at least one pre-registered native client is required")
	}
	s := &Server{db: db.Session(&gorm.Session{NewDB: true}), issuer: config.Issuer, clients: make(map[string]NativeClient), policy: policy, absoluteTTL: config.RefreshAbsoluteTTL, idleTTL: config.RefreshIdleTTL, now: time.Now}
	for _, client := range config.Clients {
		if err := validateClient(client); err != nil {
			return nil, err
		}
		if _, exists := s.clients[client.ID]; exists {
			return nil, fmt.Errorf("oauth server: duplicate client ID")
		}
		client.RedirectURIs = append([]string(nil), client.RedirectURIs...)
		client.Resources = append([]string(nil), client.Resources...)
		client.Scopes = append([]string(nil), client.Scopes...)
		s.clients[client.ID] = client
	}
	return s, nil
}

func validateClient(client NativeClient) error {
	if client.ID == "" || len(client.ID) > 128 || !printableASCII(client.ID) || client.Name == "" || len(client.Name) > 256 || len(client.RedirectURIs) == 0 || len(client.Resources) == 0 {
		return fmt.Errorf("oauth server: incomplete native client registration")
	}
	for _, redirect := range client.RedirectURIs {
		u, ok := loopbackURL(redirect)
		if !ok || u.Host != "127.0.0.1" {
			return fmt.Errorf("oauth server: registered redirects must be portless loopback templates")
		}
	}
	for _, resource := range client.Resources {
		if !validHTTPS(resource) {
			return fmt.Errorf("oauth server: invalid registered resource")
		}
	}
	scopes, err := parseScopes(strings.Join(client.Scopes, " "))
	if err != nil || len(scopes) != len(client.Scopes) || !scopesWithin(client.Scopes, scopes) {
		return fmt.Errorf("oauth server: invalid registered scopes")
	}
	return nil
}

type Metadata struct {
	Issuer                                     string   `json:"issuer"`
	AuthorizationEndpoint                      string   `json:"authorization_endpoint"`
	TokenEndpoint                              string   `json:"token_endpoint"`
	RevocationEndpoint                         string   `json:"revocation_endpoint"`
	ResponseTypesSupported                     []string `json:"response_types_supported"`
	ResponseModesSupported                     []string `json:"response_modes_supported"`
	GrantTypesSupported                        []string `json:"grant_types_supported"`
	ScopesSupported                            []string `json:"scopes_supported"`
	TokenEndpointAuthMethodsSupported          []string `json:"token_endpoint_auth_methods_supported"`
	RevocationEndpointAuthMethodsSupported     []string `json:"revocation_endpoint_auth_methods_supported"`
	CodeChallengeMethodsSupported              []string `json:"code_challenge_methods_supported"`
	AuthorizationResponseISSParameterSupported bool     `json:"authorization_response_iss_parameter_supported"`
}

// Metadata describes intended future endpoints, not a claim they are mounted.
// Serve only once the complete protected HTTP surface is ready.
func (s *Server) Metadata() Metadata {
	seen := make(map[string]bool)
	for _, client := range s.clients {
		for _, scope := range client.Scopes {
			seen[scope] = true
		}
	}
	scopes := make([]string, 0, len(seen))
	for scope := range seen {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	return Metadata{
		Issuer: s.issuer, AuthorizationEndpoint: s.issuer + "/oauth/authorize",
		TokenEndpoint: s.issuer + "/oauth/token", RevocationEndpoint: s.issuer + "/oauth/revoke",
		ResponseTypesSupported: []string{"code"}, ResponseModesSupported: []string{"query"},
		GrantTypesSupported: []string{"authorization_code", "refresh_token"}, ScopesSupported: scopes,
		TokenEndpointAuthMethodsSupported: []string{"none"}, RevocationEndpointAuthMethodsSupported: []string{"none"},
		CodeChallengeMethodsSupported: []string{"S256"}, AuthorizationResponseISSParameterSupported: true,
	}
}

func grantView(family model.OAuthServerGrant, scope string) Grant {
	return Grant{FamilyID: family.ID, UserID: family.UserID, ClientID: family.ClientID, Resource: family.Resource,
		Scopes: strings.Split(scope, " "), Binding: SenderBinding{Method: family.BindingMethod, Thumbprint: family.BindingThumbprint}}
}

func (s *Server) grantPermitted(tx *gorm.DB, family model.OAuthServerGrant, scope string) bool {
	client, exists := s.clients[family.ClientID]
	scopes, err := parseScopes(scope)
	maximum, maxErr := parseScopes(family.Scope)
	if !exists || family.Issuer != s.issuer || family.UserID <= 0 || err != nil || maxErr != nil || !scopesWithin(scopes, maximum) || !scopesWithin(scopes, client.Scopes) || !contains(client.Resources, family.Resource) || !validRedirect(family.RedirectURI, client) || !supportedBinding(SenderBinding{Method: family.BindingMethod, Thumbprint: family.BindingThumbprint}) {
		return false
	}
	// Clear query clauses without replacing the transaction's connection/context.
	return s.policy.Authorize(tx.Statement.Context, tx.Session(&gorm.Session{NewDB: true}), grantView(family, scope)) == nil
}

// transact separates protocol rejection from rollback. In particular a replay
// commits its family revocation even though the caller receives invalid_grant.
func (s *Server) transact(ctx context.Context, fn func(*gorm.DB) (*ProtocolError, error)) error {
	var rejected *ProtocolError
	options := &sql.TxOptions{}
	if s.db.Dialector.Name() == "postgres" {
		// Read a fresh member snapshot after waiting for the family write
		// lock, even if the installation's default isolation is stronger.
		options.Isolation = sql.LevelReadCommitted
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		rejected, err = fn(tx)
		return err
	}, options)
	if err != nil {
		return storageError(err)
	}
	if rejected != nil {
		return rejected
	}
	return nil
}

// lockFamily's FIRST statement is a write. PostgreSQL locks the family row
// across workers/processes; SQLite serializes writers without a deferred read
// transaction upgrade. Never replace this with a process-local mutex or a
// SELECT FOR UPDATE alone (which SQLite ignores). Read members AFTER this lock.
func (s *Server) lockFamily(tx *gorm.DB, digestValue string, code bool) (*model.OAuthServerGrant, error) {
	var source any = &model.OAuthServerToken{}
	if code {
		source = &model.OAuthServerCode{}
	}
	subquery := tx.Model(source).Select("family_id").Where("digest = ? AND issuer = ?", digestValue, s.issuer)
	locked := tx.Model(&model.OAuthServerGrant{}).Where("issuer = ? AND id IN (?)", s.issuer, subquery).
		UpdateColumn("lock_version", gorm.Expr("lock_version + 1"))
	if locked.Error != nil {
		return nil, locked.Error
	}
	if locked.RowsAffected != 1 {
		return nil, nil
	}
	var family model.OAuthServerGrant
	err := tx.Where("issuer = ? AND id IN (?)", s.issuer, subquery).Take(&family).Error
	if err == nil && family.Issuer != s.issuer {
		return nil, nil
	}
	return &family, err
}

func revokeFamily(tx *gorm.DB, family *model.OAuthServerGrant, now int64, reason string) error {
	return tx.Model(&model.OAuthServerGrant{}).Where("id = ? AND issuer = ? AND revoked_at_ms = 0", family.ID, family.Issuer).
		Updates(map[string]any{"revoked_at_ms": now, "revocation_reason": reason}).Error
}
