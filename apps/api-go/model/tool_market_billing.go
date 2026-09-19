package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

type ToolMarketCall struct {
	ID               string `json:"id" gorm:"primaryKey;size:64"`
	UserID           int    `json:"user_id" gorm:"not null;index"`
	ClientID         string `json:"client_id" gorm:"size:128;not null"`
	ServiceID        string `json:"service_id" gorm:"size:36;not null;index"`
	ToolID           string `json:"tool_id" gorm:"size:36;not null;index"`
	VersionID        string `json:"version_id" gorm:"size:36;not null"`
	GrantID          string `json:"grant_id" gorm:"size:36;not null"`
	InputDigest      string `json:"-" gorm:"size:64;not null"`
	OwnerID          int    `json:"owner_id"`
	RecipientID      int    `json:"recipient_id"`
	PriceQuota       int    `json:"price_quota"`
	FeeBPS           int    `json:"fee_bps"`
	FeeQuota         int    `json:"fee_quota"`
	ExecutionStatus  string `json:"execution_status" gorm:"size:24;index"`
	SettlementStatus string `json:"settlement_status" gorm:"size:24;index"`
	CreatedAt        int64  `json:"created_at"`
	StartedAt        int64  `json:"started_at"`
	FinishedAt       int64  `json:"finished_at"`
	ResolveBy        int64  `json:"resolve_by" gorm:"index"`
}

// Each successful call has at most two append-only transfers. A zero-price
// call creates no income. The payer's balance was already reduced by the hold.
type ToolMarketTransfer struct {
	ID         string `json:"id" gorm:"primaryKey;size:80"`
	CallID     string `json:"call_id" gorm:"size:64;not null;index"`
	FromUserID int    `json:"from_user_id" gorm:"index"`
	ToUserID   int    `json:"to_user_id" gorm:"index"`
	Kind       string `json:"kind" gorm:"size:24"`
	Quota      int    `json:"quota"`
	FeeBPS     int    `json:"fee_bps"`
	CreatedAt  int64  `json:"created_at"`
}

type ToolMarketReserveInput struct {
	UserID     int
	ClientID   string
	RequestKey string
	ToolID     string
	VersionID  string
	GrantID    string
	Arguments  json.RawMessage
	// Supplied by the trusted execution adapter, never directly by a client.
	ResolveBy int64
}

func marketArgumentDigest(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || len(raw) > 128<<10 {
		return "", ErrToolMarketInput
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if decoder.Decode(&value) != nil || value == nil {
		return "", ErrToolMarketInput
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return "", ErrToolMarketInput
	}
	return marketDigest(value), nil
}

// Splitting before multiplying keeps wallet-safe integers below int64 limits.
func marketFee(price, bps int) int { return (price/10000)*bps + ((price%10000)*bps)/10000 }

// ReserveToolMarketCall is an internal execution primitive, not a public API.
// The adapter must validate arguments against the reviewed schema before this
// function, then acquire StartToolMarketCall before producing any side effect.
func ReserveToolMarketCall(in ToolMarketReserveInput) (*ToolMarketCall, bool, error) {
	if in.UserID <= 0 || !marketClientValid(in.ClientID) || in.RequestKey == "" || len(in.RequestKey) > 128 {
		return nil, false, ErrToolMarketInput
	}
	digest, err := marketArgumentDigest(in.Arguments)
	if err != nil {
		return nil, false, err
	}
	id := marketDigest([]any{in.UserID, in.ClientID, in.RequestKey})
	var call ToolMarketCall
	created := false
	err = DB.Transaction(func(tx *gorm.DB) error {
		// The service is the publication/dispatch lock; the user serializes all
		// client and tool budgets, including requests for different services.
		var tool ToolMarketTool
		if err := tx.First(&tool, "id = ?", in.ToolID).Error; err != nil {
			return err
		}
		var service ToolMarketService
		if err := lockForUpdate(tx).First(&service, "id = ?", tool.ServiceID).Error; err != nil {
			return err
		}
		if err := marketLockUsers(tx, in.UserID); err != nil {
			return err
		}
		if err := marketUser(tx, in.UserID, common.RoleCommonUser); err != nil {
			return err
		}
		if err := tx.First(&call, "id = ?", id).Error; err == nil {
			if call.ToolID != in.ToolID || call.VersionID != in.VersionID || call.GrantID != in.GrantID || call.InputDigest != digest {
				return ErrToolMarketConflict
			}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		now := common.GetTimestamp()
		if in.ResolveBy <= now || in.ResolveBy > now+86400 {
			return ErrToolMarketInput
		}
		var config ToolMarketConfig
		if err := lockForShare(tx).First(&config, 1).Error; err != nil {
			return err
		}
		if !config.Enabled || config.FeeBPS < 0 || config.FeeBPS > 10000 {
			return ErrToolMarketDenied
		}
		if err := marketUser(tx, config.RecipientID, common.RoleRootUser); err != nil {
			return err
		}
		if err := marketUser(tx, service.OwnerID, common.RoleCommonUser); err != nil {
			return err
		}
		_, version, err := marketLiveTool(tx, in.UserID, in.ToolID, in.VersionID)
		if err != nil {
			return err
		}
		var installation ToolMarketInstallation
		if err := tx.First(&installation, "user_id = ? AND client_id = ? AND tool_id = ? AND version_id = ?", in.UserID, in.ClientID, in.ToolID, in.VersionID).Error; err != nil {
			return err
		}
		var grant ToolMarketGrant
		if err := tx.First(&grant, "id = ? AND user_id = ? AND client_id = ? AND tool_id = ? AND version_id = ?", in.GrantID, in.UserID, in.ClientID, in.ToolID, in.VersionID).Error; err != nil {
			return err
		}
		if grant.RevokedAt != 0 || grant.ExpiresAt <= now {
			return ErrToolMarketDenied
		}
		price := version.PriceQuota
		if !marketQuotaValid(price) {
			return ErrToolMarketInput
		}
		if price > grant.MaxPriceQuota || grant.SpentQuota > grant.MaxTotalQuota || grant.ReservedQuota > grant.MaxTotalQuota-grant.SpentQuota || price > grant.MaxTotalQuota-grant.SpentQuota-grant.ReservedQuota || grant.SuccessfulCalls >= grant.MaxCalls || grant.ReservedCalls >= grant.MaxCalls-grant.SuccessfulCalls {
			return ErrToolMarketBudget
		}
		call = ToolMarketCall{ID: id, UserID: in.UserID, ClientID: in.ClientID, ServiceID: service.ID, ToolID: in.ToolID, VersionID: in.VersionID, GrantID: in.GrantID,
			InputDigest: digest, OwnerID: service.OwnerID, RecipientID: config.RecipientID, PriceQuota: price, FeeBPS: config.FeeBPS, FeeQuota: marketFee(price, config.FeeBPS),
			ExecutionStatus: "reserved", SettlementStatus: "held", CreatedAt: now, ResolveBy: in.ResolveBy}
		budgets, err := marketBudgets(tx, call)
		if err != nil {
			return err
		}
		for _, budget := range budgets {
			if budget.SpentQuota > budget.LimitQuota || budget.ReservedQuota > budget.LimitQuota-budget.SpentQuota || price > budget.LimitQuota-budget.SpentQuota-budget.ReservedQuota {
				return ErrToolMarketBudget
			}
			budget.ReservedQuota += price
			if err := marketSaveBudget(tx, budget); err != nil {
				return err
			}
		}
		if price > 0 {
			q := UpdateWalletQuotaByDelta(tx.Model(&User{}).Where("id = ? AND quota >= ?", in.UserID, price), -price)
			if q.Error != nil {
				return q.Error
			}
			if q.RowsAffected != 1 {
				return ErrToolMarketBalance
			}
		}
		grant.ReservedQuota += price
		grant.ReservedCalls++
		if err := tx.Save(&grant).Error; err != nil {
			return err
		}
		if err := tx.Create(&call).Error; err != nil {
			return err
		}
		created = true
		return nil
	})
	if err == nil && created {
		marketInvalidate(in.UserID)
	}
	return &call, created && err == nil, err
}

// Lock order: service -> call -> wallets (sorted). No caller executes remotely
// until this transaction commits. Only the winner receives started=true.
func marketCallTx(id string, fn func(*gorm.DB, *ToolMarketCall) error) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var call ToolMarketCall
		if err := tx.First(&call, "id = ?", id).Error; err != nil {
			return err
		}
		var service ToolMarketService
		if err := lockForUpdate(tx).First(&service, "id = ?", call.ServiceID).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).First(&call, "id = ?", id).Error; err != nil {
			return err
		}
		return fn(tx, &call)
	})
}

func StartToolMarketCall(id string) (bool, error) {
	started := false
	err := marketCallTx(id, func(tx *gorm.DB, call *ToolMarketCall) error {
		if call.ExecutionStatus != "reserved" || call.SettlementStatus != "held" {
			return nil
		}
		if err := marketLockUsers(tx, call.UserID); err != nil {
			return err
		}
		if err := marketUser(tx, call.UserID, common.RoleCommonUser); err != nil {
			return err
		}
		if call.ResolveBy <= common.GetTimestamp() {
			return ErrToolMarketDenied
		}
		var config ToolMarketConfig
		if err := lockForShare(tx).First(&config, 1).Error; err != nil {
			return err
		}
		if !config.Enabled {
			return ErrToolMarketDenied
		}
		service, _, err := marketLiveTool(tx, call.UserID, call.ToolID, call.VersionID)
		if err != nil {
			return err
		}
		if err := marketUser(tx, service.OwnerID, common.RoleCommonUser); err != nil {
			return err
		}
		var grant ToolMarketGrant
		if err := tx.First(&grant, "id = ?", call.GrantID).Error; err != nil {
			return err
		}
		if grant.RevokedAt != 0 || grant.ExpiresAt <= common.GetTimestamp() {
			return ErrToolMarketDenied
		}
		var installation ToolMarketInstallation
		if err := tx.First(&installation, "user_id = ? AND client_id = ? AND tool_id = ? AND version_id = ?", call.UserID, call.ClientID, call.ToolID, call.VersionID).Error; err != nil {
			return err
		}
		call.ExecutionStatus, call.StartedAt = "running", common.GetTimestamp()
		if err := tx.Save(call).Error; err != nil {
			return err
		}
		started = true
		return nil
	})
	return started && err == nil, err
}

func MarkToolMarketCallUnknown(id string) error {
	return marketCallTx(id, func(tx *gorm.DB, call *ToolMarketCall) error {
		if call.ExecutionStatus == "unknown" {
			return nil
		}
		if call.ExecutionStatus != "running" || call.SettlementStatus != "held" {
			return ErrToolMarketConflict
		}
		return tx.Model(call).Update("execution_status", "unknown").Error
	})
}

// FinishToolMarketCall is trusted-backend-only. Success means the execution
// adapter validated the final business result, not merely an HTTP 200.
func FinishToolMarketCall(id string, success bool) error {
	return finishToolMarketCall(id, success, false)
}

// ExpireToolMarketCall releases holds after the recorded deadline, without
// claiming that a running remote operation was cancelled or failed.
func ExpireToolMarketCall(id string) error { return finishToolMarketCall(id, false, true) }

func finishToolMarketCall(id string, success, expire bool) error {
	var affected []int
	err := marketCallTx(id, func(tx *gorm.DB, call *ToolMarketCall) error {
		if call.SettlementStatus == "settled" {
			if success || expire {
				return nil
			}
			return ErrToolMarketConflict
		}
		if call.SettlementStatus == "released" {
			if !success {
				return nil
			}
			return ErrToolMarketConflict
		}
		if call.SettlementStatus != "held" {
			return ErrToolMarketConflict
		}
		if expire && common.GetTimestamp() < call.ResolveBy {
			return ErrToolMarketConflict
		}
		if success && (call.ExecutionStatus != "running" && call.ExecutionStatus != "unknown") {
			return ErrToolMarketConflict
		}
		if success && common.GetTimestamp() >= call.ResolveBy {
			return ErrToolMarketConflict
		}
		affected = []int{call.UserID, call.OwnerID, call.RecipientID}
		if err := marketLockUsers(tx, append([]int(nil), affected...)...); err != nil {
			return err
		}
		var grant ToolMarketGrant
		if err := tx.First(&grant, "id = ?", call.GrantID).Error; err != nil {
			return err
		}
		if grant.ReservedCalls <= 0 || grant.ReservedQuota < call.PriceQuota {
			return ErrToolMarketConflict
		}
		grant.ReservedCalls--
		grant.ReservedQuota -= call.PriceQuota
		if success {
			grant.SuccessfulCalls++
			grant.SpentQuota += call.PriceQuota
		}
		if err := tx.Save(&grant).Error; err != nil {
			return err
		}
		budgets, err := marketBudgets(tx, *call)
		if err != nil {
			return err
		}
		for _, budget := range budgets {
			if budget.ReservedQuota < call.PriceQuota {
				return ErrToolMarketConflict
			}
			budget.ReservedQuota -= call.PriceQuota
			if success {
				budget.SpentQuota += call.PriceQuota
			}
			if err := marketSaveBudget(tx, budget); err != nil {
				return err
			}
		}
		if success {
			for _, part := range []struct {
				kind      string
				recipient int
				amount    int
			}{{"author", call.OwnerID, call.PriceQuota - call.FeeQuota}, {"platform", call.RecipientID, call.FeeQuota}} {
				if part.amount == 0 {
					continue
				}
				if err := ApplyWalletQuotaDelta(tx, part.recipient, part.amount); err != nil {
					return err
				}
				entry := ToolMarketTransfer{ID: call.ID + ":" + part.kind, CallID: call.ID, FromUserID: call.UserID, ToUserID: part.recipient, Kind: part.kind, Quota: part.amount, FeeBPS: call.FeeBPS, CreatedAt: common.GetTimestamp()}
				if err := tx.Create(&entry).Error; err != nil {
					return err
				}
			}
			call.ExecutionStatus, call.SettlementStatus = "succeeded", "settled"
		} else {
			if err := ApplyWalletQuotaDelta(tx, call.UserID, call.PriceQuota); err != nil {
				return err
			}
			if expire && call.ExecutionStatus != "reserved" {
				call.ExecutionStatus = "unknown"
				var outcome ToolMarketResult
				if err := tx.First(&outcome, "call_id = ?", call.ID).Error; err == nil {
					if outcome.Success {
						call.ExecutionStatus = "succeeded"
					} else {
						call.ExecutionStatus = "failed"
					}
				} else if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
			} else if call.ExecutionStatus == "reserved" {
				call.ExecutionStatus = "cancelled"
			} else {
				call.ExecutionStatus = "failed"
			}
			call.SettlementStatus = "released"
		}
		call.FinishedAt = common.GetTimestamp()
		if err := tx.Save(call).Error; err != nil {
			return err
		}
		return marketEvent(tx, 0, call.ID, "call."+call.SettlementStatus, map[string]any{"execution_status": call.ExecutionStatus, "price_quota": call.PriceQuota})
	})
	if err == nil {
		marketInvalidate(affected...)
	}
	return err
}
