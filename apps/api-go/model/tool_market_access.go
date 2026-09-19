package model

import (
	"sort"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// All account-scoped mutations use the existing wallet row as their lock root.
// Multi-account settlement locks sorted IDs to avoid opposite-transfer deadlocks.
func marketLockUsers(tx *gorm.DB, ids ...int) error {
	sort.Ints(ids)
	last := 0
	for _, id := range ids {
		if id <= 0 || id == last {
			continue
		}
		last = id
		var user User
		if err := lockForUpdate(tx).Select("id").First(&user, id).Error; err != nil {
			return err
		}
	}
	return nil
}

func marketInvalidate(ids ...int) {
	for _, id := range ids {
		if id > 0 {
			if err := invalidateUserCache(id); err != nil {
				common.SysLog("tool market user cache invalidation failed")
			}
		}
	}
}

func SetToolMarketConfig(actor int, input ToolMarketConfig) error {
	if input.FeeBPS < 0 || input.FeeBPS > 10000 || input.RecipientID <= 0 {
		return ErrToolMarketInput
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := marketUser(tx, actor, common.RoleRootUser); err != nil {
			return err
		}
		if err := marketUser(tx, input.RecipientID, common.RoleRootUser); err != nil {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ToolMarketConfig{ID: 1}).Error; err != nil {
			return err
		}
		var before ToolMarketConfig
		if err := lockForUpdate(tx).First(&before, 1).Error; err != nil {
			return err
		}
		input.ID, input.UpdatedBy, input.UpdatedAt = 1, actor, common.GetTimestamp()
		if err := tx.Save(&input).Error; err != nil {
			return err
		}
		return marketEvent(tx, actor, "config", "config.update", map[string]any{"before": before, "after": input})
	})
}

func SetToolMarketFavorite(userID int, serviceID string, favorite bool) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := marketUser(tx, userID, common.RoleCommonUser); err != nil {
			return err
		}
		if !favorite {
			return tx.Where("user_id = ? AND service_id = ?", userID, serviceID).Delete(&ToolMarketFavorite{}).Error
		}
		var service ToolMarketService
		if err := tx.First(&service, "id = ? AND status = ?", serviceID, "published").Error; err != nil {
			return err
		}
		var version ToolMarketVersion
		if err := tx.First(&version, "id = ?", service.LiveVersionID).Error; err != nil {
			return err
		}
		if !marketCanView(service, version, userID) {
			return gorm.ErrRecordNotFound
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ToolMarketFavorite{UserID: userID, ServiceID: serviceID, CreatedAt: common.GetTimestamp()}).Error
	})
}

func marketClientValid(id string) bool {
	return strings.TrimSpace(id) == id && id != "" && len(id) <= 128
}

func SetToolMarketInstallation(userID int, clientID, toolID, versionID string, loaded bool) error {
	if !marketClientValid(clientID) || toolID == "" {
		return ErrToolMarketInput
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := marketLockUsers(tx, userID); err != nil {
			return err
		}
		if err := marketUser(tx, userID, common.RoleCommonUser); err != nil {
			return err
		}
		query := tx.Where("user_id = ? AND client_id = ? AND tool_id = ?", userID, clientID, toolID)
		if loaded {
			if _, _, err := marketLiveTool(tx, userID, toolID, versionID); err != nil {
				return err
			}
			row := ToolMarketInstallation{UserID: userID, ClientID: clientID, ToolID: toolID, VersionID: versionID, CreatedAt: common.GetTimestamp()}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}, {Name: "client_id"}, {Name: "tool_id"}}, DoUpdates: clause.AssignmentColumns([]string{"version_id", "created_at"})}).Create(&row).Error; err != nil {
				return err
			}
		} else if err := query.Delete(&ToolMarketInstallation{}).Error; err != nil {
			return err
		}
		return marketEvent(tx, userID, toolID, "installation.set", map[string]any{"client_id": clientID, "version_id": versionID, "loaded": loaded})
	})
}

// Grants are explicit bounded authorizations; loading alone never creates one.
// Reauthorization creates a new row so in-flight calls retain their old limits.
func CreateToolMarketGrant(userID int, input ToolMarketGrant) (*ToolMarketGrant, error) {
	if !marketClientValid(input.ClientID) || !marketQuotaValid(input.MaxPriceQuota) || !marketQuotaValid(input.MaxTotalQuota) || input.MaxCalls <= 0 || input.MaxCalls > 1000000 || input.ExpiresAt <= common.GetTimestamp() {
		return nil, ErrToolMarketInput
	}
	grant := ToolMarketGrant{ID: uuid.NewString(), UserID: userID, ClientID: input.ClientID, ToolID: input.ToolID, VersionID: input.VersionID,
		MaxPriceQuota: input.MaxPriceQuota, MaxTotalQuota: input.MaxTotalQuota, MaxCalls: input.MaxCalls, ExpiresAt: input.ExpiresAt, CreatedAt: common.GetTimestamp()}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := marketLockUsers(tx, userID); err != nil {
			return err
		}
		if err := marketUser(tx, userID, common.RoleCommonUser); err != nil {
			return err
		}
		if _, _, err := marketLiveTool(tx, userID, grant.ToolID, grant.VersionID); err != nil {
			return err
		}
		if err := tx.Create(&grant).Error; err != nil {
			return err
		}
		return marketEvent(tx, userID, grant.ID, "grant.create", grant)
	})
	return &grant, err
}

func RevokeToolMarketGrant(userID int, id string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := marketLockUsers(tx, userID); err != nil {
			return err
		}
		if err := marketUser(tx, userID, common.RoleCommonUser); err != nil {
			return err
		}
		var grant ToolMarketGrant
		if err := tx.First(&grant, "id = ? AND user_id = ?", id, userID).Error; err != nil {
			return err
		}
		if grant.RevokedAt != 0 {
			return nil
		}
		if err := tx.Model(&grant).Update("revoked_at", common.GetTimestamp()).Error; err != nil {
			return err
		}
		return marketEvent(tx, userID, id, "grant.revoke", nil)
	})
}

func SetToolMarketBudget(userID int, scope, scopeID string, limit int) error {
	if !marketQuotaValid(limit) {
		return ErrToolMarketInput
	}
	switch scope {
	case "account":
		if scopeID != "" {
			return ErrToolMarketInput
		}
	case "client":
		if !marketClientValid(scopeID) {
			return ErrToolMarketInput
		}
	case "tool":
		if _, err := uuid.Parse(scopeID); err != nil {
			return ErrToolMarketInput
		}
	default:
		return ErrToolMarketInput
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := marketLockUsers(tx, userID); err != nil {
			return err
		}
		if err := marketUser(tx, userID, common.RoleCommonUser); err != nil {
			return err
		}
		// Creating or replacing a budget never forgets existing charges/holds.
		q := tx.Model(&ToolMarketCall{}).Where("user_id = ?", userID)
		if scope == "client" {
			q = q.Where("client_id = ?", scopeID)
		}
		if scope == "tool" {
			q = q.Where("tool_id = ?", scopeID)
		}
		var totals struct {
			Reserved int
			Spent    int
		}
		if err := q.Select("COALESCE(SUM(CASE WHEN settlement_status = 'held' THEN price_quota ELSE 0 END), 0) AS reserved, COALESCE(SUM(CASE WHEN settlement_status = 'settled' THEN price_quota ELSE 0 END), 0) AS spent").Scan(&totals).Error; err != nil {
			return err
		}
		if totals.Spent > limit || totals.Reserved > limit-totals.Spent {
			return ErrToolMarketBudget
		}
		row := ToolMarketBudget{UserID: userID, Scope: scope, ScopeID: scopeID, LimitQuota: limit, ReservedQuota: totals.Reserved, SpentQuota: totals.Spent}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}, {Name: "scope"}, {Name: "scope_id"}}, DoUpdates: clause.AssignmentColumns([]string{"limit_quota", "reserved_quota", "spent_quota"})}).Create(&row).Error; err != nil {
			return err
		}
		return marketEvent(tx, userID, scope+":"+scopeID, "budget.set", row)
	})
}

func marketBudgets(tx *gorm.DB, call ToolMarketCall) ([]ToolMarketBudget, error) {
	rows := []ToolMarketBudget{}
	err := tx.Where("user_id = ? AND ((scope = 'account' AND scope_id = '') OR (scope = 'client' AND scope_id = ?) OR (scope = 'tool' AND scope_id = ?))", call.UserID, call.ClientID, call.ToolID).Find(&rows).Error
	return rows, err
}

func marketSaveBudget(tx *gorm.DB, budget ToolMarketBudget) error {
	// The account budget deliberately has an empty scope_id, so GORM Save
	// would mistake its composite primary key for an uninitialized new row.
	q := tx.Model(&ToolMarketBudget{}).Where("user_id = ? AND scope = ? AND scope_id = ?", budget.UserID, budget.Scope, budget.ScopeID).
		Updates(map[string]any{"reserved_quota": budget.ReservedQuota, "spent_quota": budget.SpentQuota})
	return q.Error
}

func SetToolMarketPaused(actor int, serviceID string, paused bool) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := marketUser(tx, actor, common.RoleCommonUser); err != nil {
			return err
		}
		var service ToolMarketService
		if err := lockForUpdate(tx).First(&service, "id = ?", serviceID).Error; err != nil {
			return err
		}
		if service.OwnerID != actor {
			if err := marketUser(tx, actor, common.RoleAdminUser); err != nil {
				return err
			}
		}
		if service.LiveVersionID == "" {
			return ErrToolMarketConflict
		}
		// An author cannot undo an administrator's suspension.
		if !paused && service.Status == "suspended" {
			if err := marketUser(tx, actor, common.RoleAdminUser); err != nil {
				return err
			}
		}
		status := "published"
		if paused {
			status = "paused"
			if actor != service.OwnerID {
				status = "suspended"
			}
		}
		if service.Status == "suspended" && paused {
			status = "suspended"
		}
		before := service.Status
		if err := tx.Model(&service).Updates(map[string]any{"status": status, "updated_at": common.GetTimestamp()}).Error; err != nil {
			return err
		}
		return marketEvent(tx, actor, serviceID, "service.status", map[string]string{"before": before, "after": status})
	})
}
