package model

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// One buyer report per call. Billing evidence survives short-lived result
// cleanup, contains no arguments/provider credentials, and is admin-only.
type ToolMarketReport struct {
	CallID     string                 `json:"call_id" gorm:"primaryKey;size:64"`
	UserID     int                    `json:"user_id" gorm:"index"`
	ServiceID  string                 `json:"service_id" gorm:"size:36;index"`
	OwnerID    int                    `json:"owner_id"`
	Reason     string                 `json:"reason" gorm:"type:text"`
	Evidence   ToolMarketDeliveryData `json:"evidence"`
	Status     string                 `json:"status" gorm:"size:24;index"`
	ReviewNote string                 `json:"review_note" gorm:"type:text"`
	ReviewedBy int                    `json:"reviewed_by"`
	CreatedAt  int64                  `json:"created_at"`
	ReviewedAt int64                  `json:"reviewed_at"`
}

func ReportToolMarketCall(userID int, callID, reason string) (*ToolMarketReport, error) {
	reason = strings.TrimSpace(reason)
	if userID <= 0 || len(callID) != 64 || reason == "" || !utf8.ValidString(reason) || utf8.RuneCountInString(reason) > 2000 {
		return nil, ErrToolMarketInput
	}
	var report ToolMarketReport
	err := marketCallTx(callID, func(tx *gorm.DB, c *ToolMarketCall) error {
		if c.UserID != userID {
			return ErrToolMarketDenied
		}
		if c.SettlementStatus != "settled" && c.SettlementStatus != "released" {
			return ErrToolMarketConflict
		}
		evidence, err := json.Marshal(map[string]any{"version_id": c.VersionID, "tool_id": c.ToolID, "billing_mode": c.BillingMode, "billing_rules": c.BillingRules, "input_token_price_quota": c.InputTokenPriceQuota, "max_input_tokens": c.MaxInputTokens, "price_quota": c.PriceQuota, "fee_quota": c.FeeQuota, "fee_bps": c.FeeBPS, "usage_source": c.UsageSource, "usage_quantities": c.UsageQuantities, "usage_report": string(c.UsageReport), "result_digest": c.ResultDigest, "settlement_status": c.SettlementStatus})
		if err != nil {
			return err
		}
		report = ToolMarketReport{CallID: c.ID, UserID: userID, ServiceID: c.ServiceID, OwnerID: c.OwnerID, Reason: reason, Evidence: ToolMarketDeliveryData(evidence), Status: "pending", CreatedAt: common.GetTimestamp()}
		q := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&report)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected == 0 {
			if err := tx.First(&report, "call_id = ? AND user_id = ?", callID, userID).Error; err != nil {
				return err
			}
			if report.Reason != reason {
				return ErrToolMarketConflict
			}
			return nil
		}
		return marketEvent(tx, userID, callID, "call.report", map[string]any{"service_id": c.ServiceID})
	})
	return &report, err
}

func ListToolMarketReports(actor, offset, limit int) ([]ToolMarketReport, error) {
	if offset < 0 || offset > 10000 || limit < 1 || limit > 100 {
		return nil, ErrToolMarketInput
	}
	if err := marketUser(DB, actor, common.RoleAdminUser); err != nil {
		return nil, err
	}
	rows := []ToolMarketReport{}
	err := DB.Where("owner_id <> ? AND user_id <> ?", actor, actor).Order("CASE WHEN status = 'pending' THEN 0 ELSE 1 END, created_at DESC, call_id ASC").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, err
}

// Confirming fraud suspends the service transactionally. It does not silently
// rewrite settled balances or fabricate a refund after funds were transferred.
func ReviewToolMarketReport(actor int, callID string, confirmed bool, note string) error {
	note = strings.TrimSpace(note)
	if note == "" || !utf8.ValidString(note) || utf8.RuneCountInString(note) > 2000 {
		return ErrToolMarketInput
	}
	return marketTransaction(DB, func(tx *gorm.DB) error {
		if err := marketUser(tx, actor, common.RoleAdminUser); err != nil {
			return err
		}
		var report ToolMarketReport
		if err := tx.First(&report, "call_id = ?", callID).Error; err != nil {
			return err
		}
		// Match call/report submission lock order: service before report.
		var service ToolMarketService
		if err := lockForUpdate(tx).First(&service, "id = ?", report.ServiceID).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).First(&report, "call_id = ?", callID).Error; err != nil {
			return err
		}
		if report.OwnerID == actor || report.UserID == actor {
			return ErrToolMarketDenied
		}
		status := "dismissed"
		if confirmed {
			status = "confirmed"
		}
		if report.Status != "pending" {
			if report.Status == status && report.ReviewedBy == actor && report.ReviewNote == note {
				return nil
			}
			return ErrToolMarketConflict
		}
		if confirmed {
			if service.OwnerID == 0 {
				return ErrToolMarketDenied
			}
			service.Status = "suspended"
			service.UpdatedAt = common.GetTimestamp()
			if err := tx.Save(&service).Error; err != nil {
				return err
			}
		}
		report.Status = status
		report.ReviewNote = note
		report.ReviewedBy = actor
		report.ReviewedAt = common.GetTimestamp()
		if err := tx.Save(&report).Error; err != nil {
			return err
		}
		return marketEvent(tx, actor, callID, "call.report.review", map[string]any{"status": status, "service_id": report.ServiceID})
	})
}
