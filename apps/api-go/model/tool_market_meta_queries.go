package model

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// MCP management views deliberately exclude account-wide data, arguments,
// results, credential digests, request keys and provider configuration.
type ToolMarketMetaCall struct {
	ID               string `json:"call_id"`
	ServiceID        string `json:"service_id"`
	ToolID           string `json:"tool_id"`
	VersionID        string `json:"version_id"`
	PriceQuota       int    `json:"price_quota"`
	ExecutionStatus  string `json:"execution_status"`
	SettlementStatus string `json:"settlement_status"`
	CreatedAt        int64  `json:"created_at"`
	StartedAt        int64  `json:"started_at"`
	FinishedAt       int64  `json:"finished_at"`
}

type ToolMarketMetaCalls struct {
	Calls  []ToolMarketMetaCall `json:"calls"`
	Offset int                  `json:"offset"`
	Limit  int                  `json:"limit"`
	More   bool                 `json:"more"`
}

func ListToolMarketMetaCalls(userID int, clientID string, offset, limit int) (*ToolMarketMetaCalls, error) {
	if !marketClientValid(clientID) || offset < 0 || offset > 10000 || limit < 1 || limit > 100 {
		return nil, ErrToolMarketInput
	}
	if err := marketUser(DB, userID, common.RoleCommonUser); err != nil {
		return nil, err
	}
	rows := []ToolMarketMetaCall{}
	err := DB.Model(&ToolMarketCall{}).Where("user_id = ?", userID).
		Scopes(marketExactTextScope("client_id", clientID)).
		Select("id, service_id, tool_id, version_id, price_quota, execution_status, settlement_status, created_at, started_at, finished_at").
		Order("created_at DESC, id").Offset(offset).Limit(limit + 1).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	return &ToolMarketMetaCalls{Calls: rows, Offset: offset, Limit: limit, More: more}, nil
}

type ToolMarketMetaBudget struct {
	LimitQuota    int `json:"limit_quota"`
	ReservedQuota int `json:"reserved_quota"`
	SpentQuota    int `json:"spent_quota"`
}

type ToolMarketMetaInstallation struct {
	ToolID    string `json:"tool_id"`
	VersionID string `json:"version_id"`
}

type ToolMarketMetaGrant struct {
	ID              string `json:"grant_id"`
	ToolID          string `json:"tool_id"`
	VersionID       string `json:"version_id"`
	MaxPriceQuota   int    `json:"max_price_quota"`
	MaxTotalQuota   int    `json:"max_total_quota"`
	MaxCalls        int    `json:"max_calls"`
	ReservedQuota   int    `json:"reserved_quota"`
	SpentQuota      int    `json:"spent_quota"`
	ReservedCalls   int    `json:"reserved_calls"`
	SuccessfulCalls int    `json:"successful_calls"`
	ExpiresAt       int64  `json:"expires_at"`
}

type ToolMarketMetaUsage struct {
	Budget        *ToolMarketMetaBudget        `json:"client_budget"`
	Installations []ToolMarketMetaInstallation `json:"installations"`
	Grants        []ToolMarketMetaGrant        `json:"grants"`
	Offset        int                          `json:"offset"`
	Limit         int                          `json:"limit"`
	MoreInstalled bool                         `json:"more_installations"`
	MoreGrants    bool                         `json:"more_grants"`
}

// An absent budget remains distinct from a zero budget. These counters are the
// existing ledger's held/settled credits, not a second floating-point total.
func GetToolMarketMetaUsage(userID int, clientID string, offset, limit int) (*ToolMarketMetaUsage, error) {
	if !marketClientValid(clientID) || offset < 0 || offset > 10000 || limit < 1 || limit > 100 {
		return nil, ErrToolMarketInput
	}
	view := &ToolMarketMetaUsage{Installations: []ToolMarketMetaInstallation{}, Grants: []ToolMarketMetaGrant{}, Offset: offset, Limit: limit}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := marketUser(tx, userID, common.RoleCommonUser); err != nil {
			return err
		}
		var budgets []ToolMarketMetaBudget
		if err := tx.Model(&ToolMarketBudget{}).Where("user_id = ? AND scope = 'client'", userID).
			Scopes(marketExactTextScope("scope_id", clientID)).Select("limit_quota, reserved_quota, spent_quota").Limit(1).Scan(&budgets).Error; err != nil {
			return err
		}
		if len(budgets) == 1 {
			view.Budget = &budgets[0]
		}
		if err := tx.Model(&ToolMarketInstallation{}).Where("user_id = ?", userID).
			Scopes(marketExactTextScope("client_id", clientID)).Select("tool_id, version_id").
			Order("tool_id").Offset(offset).Limit(limit + 1).Scan(&view.Installations).Error; err != nil {
			return err
		}
		if err := tx.Model(&ToolMarketGrant{}).Where("user_id = ? AND revoked_at = 0 AND expires_at > ?", userID, common.GetTimestamp()).
			Scopes(marketExactTextScope("client_id", clientID)).
			Select("id, tool_id, version_id, max_price_quota, max_total_quota, max_calls, reserved_quota, spent_quota, reserved_calls, successful_calls, expires_at").
			Order("created_at DESC, id").Offset(offset).Limit(limit + 1).Scan(&view.Grants).Error; err != nil {
			return err
		}
		view.MoreInstalled, view.MoreGrants = len(view.Installations) > limit, len(view.Grants) > limit
		if view.MoreInstalled {
			view.Installations = view.Installations[:limit]
		}
		if view.MoreGrants {
			view.Grants = view.Grants[:limit]
		}
		return nil
	})
	return view, err
}
