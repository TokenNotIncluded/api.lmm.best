package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oauthserver"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	OAuthPiClientID       = "lmm-pi"
	OAuthPiClientName     = "LMM for Pi"
	OAuthPiRedirect       = "http://127.0.0.1/oauth/lmm/callback"
	OAuthCatalogScope     = "catalog:read"
	OAuthBalanceScope     = "balance:read"
	OAuthInvokeScope      = "models:invoke"
	OAuthMCPBountiesScope = "mcp:bounties"
	OAuthMCPDrawingScope  = "mcp:drawing"
	OAuthGroupHeader      = "X-LMM-Group"
)

func OAuthBuiltinMCPScopes() []string { return []string{OAuthMCPBountiesScope, OAuthMCPDrawingScope} }

var ErrOAuthDenied = errors.New("OAuth authorization is unavailable or not permitted")

// Configuration is immutable startup input. Never derive any of it from Host,
// forwarded headers, an option submitted by a browser, or a token claim.
type OAuthServerConfig struct {
	Enabled            bool
	Issuer             string
	Groups             []string
	TrustLoopbackProxy bool
}

type OAuthIntegration struct {
	Core               *oauthserver.Server
	DB                 *gorm.DB
	Issuer             string
	Resource           string
	TrustLoopbackProxy bool
	groups             []string
}

var oauthIntegration atomic.Pointer[OAuthIntegration]

func CurrentOAuthIntegration() *OAuthIntegration { return oauthIntegration.Load() }

func OAuthServerConfigFromEnv() (OAuthServerConfig, error) {
	value := os.Getenv("OAUTH_SERVER_ENABLED")
	if value == "" || value == "false" {
		return OAuthServerConfig{}, nil
	}
	if value != "true" {
		return OAuthServerConfig{}, errors.New("OAUTH_SERVER_ENABLED must be true or false")
	}
	cfg := OAuthServerConfig{Enabled: true, Issuer: os.Getenv("OAUTH_SERVER_ISSUER")}
	if err := json.Unmarshal([]byte(os.Getenv("OAUTH_SERVER_GROUPS")), &cfg.Groups); err != nil {
		return cfg, errors.New("OAUTH_SERVER_GROUPS must be an explicit JSON string array")
	}
	proxy := os.Getenv("OAUTH_SERVER_TRUST_LOOPBACK_PROXY")
	if proxy != "" && proxy != "false" && proxy != "true" {
		return cfg, errors.New("invalid OAuth proxy trust configuration")
	}
	cfg.TrustLoopbackProxy = proxy == "true"
	return cfg, nil
}

// ConfigureOAuthIntegration is startup-only, before serving requests. The flag
// remains false unless both trusted configuration and storage initialization pass.
func ConfigureOAuthIntegration(db *gorm.DB, cfg OAuthServerConfig) (*OAuthIntegration, error) {
	oauthIntegration.Store(nil)
	if !cfg.Enabled {
		return nil, nil
	}
	integration, err := NewOAuthIntegration(db, cfg)
	if err != nil {
		return nil, err
	}
	if err = model.MigrateOAuthBilling(db); err != nil {
		return nil, err
	}
	oauthIntegration.Store(integration)
	return integration, nil
}

func NewOAuthIntegration(db *gorm.DB, cfg OAuthServerConfig) (*OAuthIntegration, error) {
	if !cfg.Enabled || len(cfg.Groups) == 0 {
		return nil, errors.New("OAuth needs an explicit enabled group allowlist")
	}
	groups := slices.Clone(cfg.Groups)
	slices.Sort(groups)
	scopes := []string{OAuthCatalogScope, OAuthBalanceScope, OAuthInvokeScope}
	scopes = append(scopes, OAuthBuiltinMCPScopes()...)
	for i, group := range groups {
		if !validOAuthGroup(group) || i > 0 && groups[i-1] == group {
			return nil, errors.New("invalid or repeated OAuth group")
		}
		scopes = append(scopes, OAuthGroupScope(group))
	}
	integration := &OAuthIntegration{DB: db, Issuer: cfg.Issuer, Resource: cfg.Issuer + "/api/oauth2", TrustLoopbackProxy: cfg.TrustLoopbackProxy, groups: groups}
	core, err := oauthserver.New(db, oauthserver.Config{Issuer: cfg.Issuer, Clients: []oauthserver.NativeClient{{ID: OAuthPiClientID, Name: OAuthPiClientName, RedirectURIs: []string{OAuthPiRedirect}, Resources: []string{integration.Resource}, Scopes: scopes}}}, integration)
	if err != nil {
		return nil, err
	}
	integration.Core = core
	return integration, nil
}

func validOAuthGroup(group string) bool {
	if group == "" || group == "auto" || len(group) > 64 || !utf8.ValidString(group) || strings.TrimSpace(group) != group {
		return false
	}
	for _, r := range group {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func OAuthGroupID(group string) string    { return base64.RawURLEncoding.EncodeToString([]byte(group)) }
func OAuthGroupScope(group string) string { return "group:" + OAuthGroupID(group) }

func OAuthGroupFromID(id string) (string, error) {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(id)
	if err != nil || !validOAuthGroup(string(decoded)) || OAuthGroupID(string(decoded)) != id {
		return "", ErrOAuthDenied
	}
	return string(decoded), nil
}

func (s *OAuthIntegration) CurrentUser(ctx context.Context, userID int64) (*model.User, error) {
	return oauthCurrentUser(s.DB.WithContext(ctx), userID)
}

// oauthCurrentUser keeps ALL policy database reads on the supplied handle.
// In core authorization this is the live OAuth transaction, never s.DB.
func oauthCurrentUser(db *gorm.DB, userID int64) (*model.User, error) {
	if db == nil || userID <= 0 {
		return nil, ErrOAuthDenied
	}
	userDB := db
	if _, transactional := db.Statement.ConnPool.(gorm.TxCommitter); transactional && db.Dialector.Name() == "postgres" {
		// Keep status/group/explicit-level changes serialized until credential
		// commit. Paid activation facts are locked by the model helper below.
		userDB = db.Clauses(clause.Locking{Strength: "SHARE"})
	}
	var user model.User
	if userDB.First(&user, "id = ?", userID).Error != nil || user.Status != common.UserStatusEnabled {
		return nil, ErrOAuthDenied
	}
	access, err := model.GetDeveloperAccessStateForUserBaseWithTx(db, user.ToBaseUser(), model.CurrentDeveloperAccessPolicy())
	if err != nil || !access.Granted {
		return nil, ErrOAuthDenied
	}
	return &user, nil
}

func (s *OAuthIntegration) AllowedGroups(user *model.User) []string {
	result := make([]string, 0)
	for _, group := range s.groups {
		// Groups requiring repeated warning acknowledgement are deliberately not
		// available in this profile until that exact workflow can be preserved.
		_, warning := ratio_setting.GetGroupWarning(group)
		if !warning && IsOAuthSelectableGroup(user.Group, group) {
			result = append(result, group)
		}
	}
	return result
}

func (s *OAuthIntegration) GrantedGroups(user *model.User, grant oauthserver.Grant) []string {
	result := make([]string, 0)
	for _, group := range s.AllowedGroups(user) {
		if slices.Contains(grant.Scopes, OAuthGroupScope(group)) {
			result = append(result, group)
		}
	}
	return result
}

// Authorize is invoked by core on consent, exchange, refresh and every resource
// validation. Database/cache failures never imply access. It does not mutate
// OAuth tables or acquire a second pool connection while core owns a transaction.
func (s *OAuthIntegration) Authorize(ctx context.Context, tx *gorm.DB, grant oauthserver.Grant) error {
	if grant.ClientID != OAuthPiClientID || grant.Resource != s.Resource {
		return ErrOAuthDenied
	}
	if tx == nil {
		return ErrOAuthDenied
	}
	user, err := oauthCurrentUser(tx.WithContext(ctx), grant.UserID)
	if err != nil {
		return err
	}
	if len(s.GrantedGroups(user, grant)) == 0 {
		return ErrOAuthDenied
	}
	return nil
}

func (s *OAuthIntegration) ValidateResource(ctx context.Context, token string, scopes ...string) (oauthserver.Grant, *model.User, error) {
	grant, err := s.Core.ValidateAccess(ctx, oauthserver.AccessRequest{Token: token, Resource: s.Resource, RequiredScopes: scopes})
	if err != nil {
		return oauthserver.Grant{}, nil, err
	}
	user, err := s.CurrentUser(ctx, grant.UserID)
	return *grant, user, err
}

// ConsentQuery only expands the initial application scope into the exact group
// snapshot which is about to be shown for explicit consent. It cannot be called
// with a user-supplied scope snapshot or a wildcard. Core revalidates the result.
func (s *OAuthIntegration) ConsentQuery(raw string, user *model.User) (string, []string, error) {
	query, err := url.ParseQuery(raw)
	if err != nil {
		return "", nil, err
	}
	requested := strings.Split(query.Get("scope"), " ")
	expected := []string{OAuthCatalogScope, OAuthBalanceScope, OAuthInvokeScope}
	expected = append(expected, OAuthBuiltinMCPScopes()...)
	slices.Sort(requested)
	slices.Sort(expected)
	if !slices.Equal(requested, expected) {
		return "", nil, ErrOAuthDenied
	}
	groups := s.AllowedGroups(user)
	if len(groups) == 0 {
		return "", nil, ErrOAuthDenied
	}
	for _, group := range groups {
		requested = append(requested, OAuthGroupScope(group))
	}
	query.Set("scope", strings.Join(requested, " "))
	return query.Encode(), groups, nil
}
