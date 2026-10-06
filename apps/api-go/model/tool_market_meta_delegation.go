package model

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const toolMarketMetaPolicyAction = "meta.delegation.policy"

// The resource server supplies this identity. It is never decoded from MCP
// arguments; a delegation cannot change token scopes or the authenticated client.
type ToolMarketMetaSubject struct {
	UserID         int
	ClientID       string
	CredentialKind string
	CredentialID   string
	OAuthIssuer    string
	OAuthResource  string
	CanInvoke      bool
	CanManage      bool
}

type ToolMarketMetaDelegation struct {
	Enabled       bool  `json:"enabled"`
	MaxTotalQuota int   `json:"max_total_quota"`
	ExpiresAt     int64 `json:"expires_at"`
	UpdatedAt     int64 `json:"updated_at"`
}

type toolMarketMetaPolicy struct {
	ToolMarketMetaDelegation
	ClientID       string `json:"client_id"`
	CredentialKind string `json:"credential_kind"`
	CredentialID   string `json:"credential_id"`
	AuthVersion    int64  `json:"auth_version"`
}

func marketMetaPolicyID(subject ToolMarketMetaSubject) string {
	raw, _ := json.Marshal([]any{subject.UserID, subject.CredentialKind, subject.CredentialID})
	return uuid.NewSHA1(uuid.NameSpaceURL, append([]byte("lmm:metamcp:delegation:"), raw...)).String()
}

func marketMetaCredential(tx *gorm.DB, subject ToolMarketMetaSubject, requireInvoke bool) (int64, int64, error) {
	if !marketClientValid(subject.ClientID) || subject.CredentialID == "" || !subject.CanManage || (requireInvoke && !subject.CanInvoke) {
		return 0, 0, ErrToolMarketDenied
	}
	var user User
	if err := tx.First(&user, "id = ? AND status = ? AND role >= ?", subject.UserID, common.UserStatusEnabled, common.RoleCommonUser).Error; err != nil {
		return 0, 0, ErrToolMarketDenied
	}
	now := common.GetTimestamp()
	switch subject.CredentialKind {
	case "personal":
		var token ToolMarketToken
		if err := tx.First(&token, "id = ? AND user_id = ?", subject.CredentialID, subject.UserID).Error; err != nil {
			return 0, 0, ErrToolMarketDenied
		}
		if token.ClientID != subject.ClientID || token.AuthVersion != user.AuthVersion || token.RevokedAt != 0 || token.ExpiresAt <= now || !token.CanManage || (requireInvoke && !token.CanInvoke) {
			return 0, 0, ErrToolMarketDenied
		}
		return user.AuthVersion, token.ExpiresAt, nil
	case "oauth":
		if subject.OAuthIssuer == "" || subject.OAuthResource == "" {
			return 0, 0, ErrToolMarketDenied
		}
		var grant OAuthServerGrant
		if err := tx.First(&grant, "id = ? AND user_id = ? AND issuer = ?", subject.CredentialID, subject.UserID, subject.OAuthIssuer).Error; err != nil {
			return 0, 0, ErrToolMarketDenied
		}
		if "oauth:"+grant.ClientID != subject.ClientID || grant.Resource != subject.OAuthResource || grant.RevokedAtMs != 0 || grant.AbsoluteExpiresAtMs <= now*1000 || grant.BindingMethod != "" || grant.BindingThumbprint != "" ||
			!containsOAuthScope(grant.Scope, "market:discover") || !containsOAuthScope(grant.Scope, "market:manage") || (requireInvoke && !containsOAuthScope(grant.Scope, "market:invoke")) {
			return 0, 0, ErrToolMarketDenied
		}
		return user.AuthVersion, grant.AbsoluteExpiresAtMs / 1000, nil
	default:
		return 0, 0, ErrToolMarketDenied
	}
}

func marketMetaPolicy(tx *gorm.DB, subject ToolMarketMetaSubject) (*toolMarketMetaPolicy, error) {
	version, expiry, err := marketMetaCredential(tx, subject, true)
	if err != nil {
		return nil, err
	}
	var event ToolMarketEvent
	if err := tx.First(&event, "id = ? AND actor_id = ? AND action = ?", marketMetaPolicyID(subject), subject.UserID, toolMarketMetaPolicyAction).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrToolMarketDenied
		}
		return nil, err
	}
	var policy toolMarketMetaPolicy
	if json.Unmarshal([]byte(event.Details), &policy) != nil || !policy.Enabled || !marketQuotaValid(policy.MaxTotalQuota) || policy.AuthVersion != version || policy.ClientID != subject.ClientID ||
		policy.CredentialID != subject.CredentialID || policy.CredentialKind != subject.CredentialKind || policy.ExpiresAt <= common.GetTimestamp() || policy.ExpiresAt > expiry {
		return nil, ErrToolMarketDenied
	}
	return &policy, nil
}

func GetToolMarketMetaDelegation(subject ToolMarketMetaSubject) (*ToolMarketMetaDelegation, error) {
	policy, err := marketMetaPolicy(DB, subject)
	if errors.Is(err, ErrToolMarketDenied) {
		return &ToolMarketMetaDelegation{}, nil
	}
	if err != nil {
		return nil, err
	}
	view := policy.ToolMarketMetaDelegation
	return &view, nil
}

func marketMetaClientBudget(tx *gorm.DB, subject ToolMarketMetaSubject) (*ToolMarketBudget, error) {
	var row ToolMarketBudget
	err := tx.Where("user_id = ? AND scope = 'client'", subject.UserID).Scopes(marketExactTextScope("scope_id", subject.ClientID)).First(&row).Error
	return &row, err
}

// Only the owner-authenticated connection setup API calls this method. MCP
// cannot grant itself delegation, extend its expiry, or increase this ceiling.
// The current policy uses a stable existing Event ID; a separate append-only
// event records every change. No random-UUID ordering decides the latest value.
func SetToolMarketMetaDelegation(subject ToolMarketMetaSubject, input ToolMarketMetaDelegation) (*ToolMarketMetaDelegation, error) {
	views, err := SetToolMarketMetaDelegations([]ToolMarketMetaSubject{subject}, input)
	if err != nil {
		return nil, err
	}
	return &views[0], nil
}

// OAuth connection setup can bind all current eligible families atomically.
// Any ineligible or expired family aborts the whole request.
func SetToolMarketMetaDelegations(subjects []ToolMarketMetaSubject, input ToolMarketMetaDelegation) ([]ToolMarketMetaDelegation, error) {
	if len(subjects) == 0 || len(subjects) > 100 || !marketQuotaValid(input.MaxTotalQuota) || (!input.Enabled && (input.MaxTotalQuota != 0 || input.ExpiresAt != 0)) {
		return nil, ErrToolMarketInput
	}
	for _, subject := range subjects {
		if subject.UserID != subjects[0].UserID || subject.ClientID != subjects[0].ClientID {
			return nil, ErrToolMarketDenied
		}
	}
	views := make([]ToolMarketMetaDelegation, 0, len(subjects))
	err := marketTransaction(DB, func(tx *gorm.DB) error {
		if err := marketLockUsers(tx, subjects[0].UserID); err != nil {
			return err
		}
		for _, subject := range subjects {
			view, err := marketSetMetaDelegation(tx, subject, input)
			if err != nil {
				return err
			}
			views = append(views, *view)
		}
		return nil
	})
	return views, err
}

func marketSetMetaDelegation(tx *gorm.DB, subject ToolMarketMetaSubject, input ToolMarketMetaDelegation) (*ToolMarketMetaDelegation, error) {
	version, credentialExpiry, err := marketMetaCredential(tx, subject, true)
	if err != nil {
		return nil, err
	}
	now := common.GetTimestamp()
	if input.Enabled && input.ExpiresAt == 0 {
		input.ExpiresAt = credentialExpiry
	}
	if input.Enabled && (input.ExpiresAt <= now || input.ExpiresAt > credentialExpiry) {
		return nil, ErrToolMarketInput
	}
	if input.Enabled {
		budget, err := marketMetaClientBudget(tx, subject)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err == nil {
			input.MaxTotalQuota = min(input.MaxTotalQuota, budget.LimitQuota)
			if budget.SpentQuota > input.MaxTotalQuota || budget.ReservedQuota > input.MaxTotalQuota-budget.SpentQuota {
				return nil, ErrToolMarketBudget
			}
			budget.LimitQuota = input.MaxTotalQuota
			if err := tx.Model(&ToolMarketBudget{}).Where("user_id = ? AND scope = 'client'", subject.UserID).
				Scopes(marketExactTextScope("scope_id", subject.ClientID)).Update("limit_quota", budget.LimitQuota).Error; err != nil {
				return nil, err
			}
		} else {
			var totals struct{ Spent, Reserved int }
			if err := tx.Model(&ToolMarketCall{}).Where("user_id = ?", subject.UserID).Scopes(marketExactTextScope("client_id", subject.ClientID)).
				Select("COALESCE(SUM(CASE WHEN settlement_status = 'settled' THEN price_quota ELSE 0 END), 0) AS spent, COALESCE(SUM(CASE WHEN settlement_status = 'held' THEN price_quota ELSE 0 END), 0) AS reserved").Scan(&totals).Error; err != nil {
				return nil, err
			}
			if !marketQuotaValid(totals.Spent) || !marketQuotaValid(totals.Reserved) || totals.Spent > input.MaxTotalQuota || totals.Reserved > input.MaxTotalQuota-totals.Spent {
				return nil, ErrToolMarketBudget
			}
			budget = &ToolMarketBudget{UserID: subject.UserID, Scope: "client", ScopeID: subject.ClientID, LimitQuota: input.MaxTotalQuota, SpentQuota: totals.Spent, ReservedQuota: totals.Reserved}
			if err := tx.Create(budget).Error; err != nil {
				return nil, err
			}
		}
	}
	view := ToolMarketMetaDelegation{Enabled: input.Enabled, MaxTotalQuota: input.MaxTotalQuota, ExpiresAt: input.ExpiresAt, UpdatedAt: now}
	policy := toolMarketMetaPolicy{ToolMarketMetaDelegation: view, ClientID: subject.ClientID, CredentialKind: subject.CredentialKind, CredentialID: subject.CredentialID, AuthVersion: version}
	details, err := json.Marshal(policy)
	if err != nil {
		return nil, err
	}
	id := marketMetaPolicyID(subject)
	row := ToolMarketEvent{ID: id, ActorID: subject.UserID, ObjectID: id, Action: toolMarketMetaPolicyAction, Details: string(details), CreatedAt: now}
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"details", "created_at"})}).Create(&row).Error; err != nil {
		return nil, err
	}
	if err := marketEvent(tx, subject.UserID, id, "meta.delegation.set", view); err != nil {
		return nil, err
	}
	return &view, nil
}

// Meta budget changes only tighten the current client's existing cap. Zero is
// zero paid spending; absence or negative numbers never mean unlimited.
func SetToolMarketMetaClientBudget(subject ToolMarketMetaSubject, limit int) error {
	if !marketQuotaValid(limit) {
		return ErrToolMarketInput
	}
	return marketTransaction(DB, func(tx *gorm.DB) error {
		if err := marketLockUsers(tx, subject.UserID); err != nil {
			return err
		}
		policy, err := marketMetaPolicy(tx, subject)
		if err != nil {
			return err
		}
		budget, err := marketMetaClientBudget(tx, subject)
		if err != nil {
			return err
		}
		if limit > policy.MaxTotalQuota || limit > budget.LimitQuota || budget.SpentQuota > limit || budget.ReservedQuota > limit-budget.SpentQuota {
			return ErrToolMarketBudget
		}
		if err := tx.Model(&ToolMarketBudget{}).Where("user_id = ? AND scope = 'client'", subject.UserID).
			Scopes(marketExactTextScope("scope_id", subject.ClientID)).Update("limit_quota", limit).Error; err != nil {
			return err
		}
		return marketEvent(tx, subject.UserID, marketMetaPolicyID(subject), "meta.client_budget.set", map[string]any{"limit_quota": limit})
	})
}

func SetToolMarketMetaToolBudget(subject ToolMarketMetaSubject, grantID, toolID, versionID string, limit int) error {
	if grantID == "" || toolID == "" || versionID == "" || !marketQuotaValid(limit) {
		return ErrToolMarketInput
	}
	return marketTransaction(DB, func(tx *gorm.DB) error {
		if err := marketLockUsers(tx, subject.UserID); err != nil {
			return err
		}
		policy, err := marketMetaPolicy(tx, subject)
		if err != nil {
			return err
		}
		var grant ToolMarketGrant
		if err := tx.Scopes(marketExactTextScope("client_id", subject.ClientID)).First(&grant, "id = ? AND user_id = ? AND tool_id = ? AND version_id = ? AND revoked_at = 0 AND expires_at > ?", grantID, subject.UserID, toolID, versionID, common.GetTimestamp()).Error; err != nil {
			return err
		}
		if limit > policy.MaxTotalQuota || limit > grant.MaxTotalQuota || grant.SpentQuota > limit || grant.ReservedQuota > limit-grant.SpentQuota {
			return ErrToolMarketBudget
		}
		if err := tx.Model(&grant).Update("max_total_quota", limit).Error; err != nil {
			return err
		}
		return marketEvent(tx, subject.UserID, grant.ID, "meta.tool_budget.set", map[string]any{"max_total_quota": limit})
	})
}

func AuthorizeToolMarketMeta(subject ToolMarketMetaSubject, input ToolMarketGrant) (*ToolMarketGrant, error) {
	if input.ToolID == "" || input.VersionID == "" || !marketQuotaValid(input.MaxPriceQuota) || !marketQuotaValid(input.MaxTotalQuota) || input.MaxCalls <= 0 || input.MaxCalls > 1000000 || input.ExpiresAt <= common.GetTimestamp() {
		return nil, ErrToolMarketInput
	}
	var grant ToolMarketGrant
	err := marketTransaction(DB, func(tx *gorm.DB) error {
		if err := marketLockUsers(tx, subject.UserID); err != nil {
			return err
		}
		policy, err := marketMetaPolicy(tx, subject)
		if err != nil {
			return err
		}
		if input.ExpiresAt > policy.ExpiresAt || input.MaxPriceQuota > input.MaxTotalQuota || input.MaxTotalQuota > policy.MaxTotalQuota {
			return ErrToolMarketBudget
		}
		budget, err := marketMetaClientBudget(tx, subject)
		if err != nil {
			return err
		}
		if input.MaxTotalQuota > budget.LimitQuota {
			return ErrToolMarketBudget
		}
		_, tool, err := marketLiveTool(tx, subject.UserID, input.ToolID, input.VersionID)
		if err != nil {
			return err
		}
		if tool.PriceQuota > input.MaxPriceQuota || (tool.PriceQuota > 0 && policy.MaxTotalQuota == 0) {
			return ErrToolMarketBudget
		}
		// Repeated authorization is not a new spending allowance. An existing
		// active exact-version grant retains its counters and stricter limits.
		err = tx.Where("user_id = ? AND tool_id = ? AND version_id = ? AND revoked_at = 0 AND expires_at > ?", subject.UserID, input.ToolID, input.VersionID, common.GetTimestamp()).
			Scopes(marketExactTextScope("client_id", subject.ClientID)).Order("created_at DESC, id DESC").First(&grant).Error
		if err == nil {
			if input.MaxPriceQuota > grant.MaxPriceQuota || input.MaxTotalQuota > grant.MaxTotalQuota || input.MaxCalls > grant.MaxCalls || input.ExpiresAt > grant.ExpiresAt {
				return ErrToolMarketBudget
			}
			if grant.SpentQuota > input.MaxTotalQuota || grant.ReservedQuota > input.MaxTotalQuota-grant.SpentQuota || grant.SuccessfulCalls > input.MaxCalls || grant.ReservedCalls > input.MaxCalls-grant.SuccessfulCalls {
				return ErrToolMarketBudget
			}
			if grant.MaxPriceQuota != input.MaxPriceQuota || grant.MaxTotalQuota != input.MaxTotalQuota || grant.MaxCalls != input.MaxCalls || grant.ExpiresAt != input.ExpiresAt {
				if err := tx.Model(&grant).Updates(map[string]any{"max_price_quota": input.MaxPriceQuota, "max_total_quota": input.MaxTotalQuota, "max_calls": input.MaxCalls, "expires_at": input.ExpiresAt}).Error; err != nil {
					return err
				}
				grant.MaxPriceQuota, grant.MaxTotalQuota, grant.MaxCalls, grant.ExpiresAt = input.MaxPriceQuota, input.MaxTotalQuota, input.MaxCalls, input.ExpiresAt
				return marketEvent(tx, subject.UserID, grant.ID, "meta.grant.tighten", map[string]any{"max_price_quota": grant.MaxPriceQuota, "max_total_quota": grant.MaxTotalQuota, "max_calls": grant.MaxCalls, "expires_at": grant.ExpiresAt})
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		grant = ToolMarketGrant{ID: uuid.NewString(), UserID: subject.UserID, ClientID: subject.ClientID, ToolID: input.ToolID, VersionID: input.VersionID,
			MaxPriceQuota: input.MaxPriceQuota, MaxTotalQuota: input.MaxTotalQuota, MaxCalls: input.MaxCalls, ExpiresAt: input.ExpiresAt, CreatedAt: common.GetTimestamp()}
		if err := tx.Create(&grant).Error; err != nil {
			return err
		}
		return marketEvent(tx, subject.UserID, grant.ID, "meta.grant.create", map[string]any{"tool_id": grant.ToolID, "version_id": grant.VersionID, "max_price_quota": grant.MaxPriceQuota, "max_total_quota": grant.MaxTotalQuota, "max_calls": grant.MaxCalls, "expires_at": grant.ExpiresAt})
	})
	return &grant, err
}

// Owner-only resolution does not disclose a bearer token or OAuth family ID.
func ToolMarketMetaPersonalSubject(userID int, tokenID string) (ToolMarketMetaSubject, error) {
	var row ToolMarketToken
	if tokenID == "" || len(tokenID) > 128 || DB.First(&row, "id = ? AND user_id = ?", tokenID, userID).Error != nil {
		return ToolMarketMetaSubject{}, ErrToolMarketDenied
	}
	return ToolMarketMetaSubject{UserID: userID, ClientID: row.ClientID, CredentialKind: "personal", CredentialID: row.ID, CanInvoke: row.CanInvoke, CanManage: row.CanManage}, nil
}

// OAuth targets are selected only by configured issuer/resource and owner.
// No caller-selected family, credential scopes or another client's ID is used.
func ToolMarketMetaOAuthSubjects(userID int, clientID, issuer, resource string) ([]ToolMarketMetaSubject, error) {
	if userID <= 0 || !marketClientValid(clientID) || !strings.HasPrefix(clientID, "oauth:") || issuer == "" || resource == "" {
		return nil, ErrToolMarketInput
	}
	var rows []OAuthServerGrant
	if err := DB.Where("user_id = ? AND issuer = ? AND resource = ? AND revoked_at_ms = 0 AND absolute_expires_at_ms > ?", userID, issuer, resource, common.GetTimestamp()*1000).
		Scopes(marketExactTextScope("client_id", strings.TrimPrefix(clientID, "oauth:"))).Limit(101).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) > 100 {
		return nil, ErrToolMarketConflict
	}
	subjects := []ToolMarketMetaSubject{}
	for _, row := range rows {
		if !containsOAuthScope(row.Scope, "market:discover") || !containsOAuthScope(row.Scope, "market:invoke") || !containsOAuthScope(row.Scope, "market:manage") || row.BindingMethod != "" || row.BindingThumbprint != "" {
			continue
		}
		subjects = append(subjects, ToolMarketMetaSubject{UserID: userID, ClientID: clientID, CredentialKind: "oauth", CredentialID: row.ID, OAuthIssuer: issuer, OAuthResource: resource, CanInvoke: true, CanManage: true})
	}
	if len(subjects) == 0 {
		return nil, ErrToolMarketDenied
	}
	return subjects, nil
}
