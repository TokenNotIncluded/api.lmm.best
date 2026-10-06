package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// ErrUsageProjectionUnavailable never authorizes a balance or history rewrite.
var ErrUsageProjectionUnavailable = errors.New("historical usage projection unavailable")

type UsageProjection struct {
	NormalizedUsedQuota       int
	HistoricalRawQuota        int
	HistoricalNormalizedQuota int
	PostMigrationQuota        int
	MigrationID               string
}

type usageSource struct {
	ID     int    `json:"id"`
	UserID int    `json:"user_id"`
	Used   *int64 `json:"used_quota"`
}
type usagePlan struct {
	Version     int                       `json:"version"`
	Kind        string                    `json:"kind"`
	MigrationID string                    `json:"migration_id"`
	UserIDs     []int                     `json:"user_ids"`
	Complete    bool                      `json:"has_complete_history"`
	Anchor      int                       `json:"usd_credit_conversion"`
	Divisor     string                    `json:"divisor"`
	Rounding    string                    `json:"rounding"`
	SnapshotAt  int64                     `json:"snapshot_at"`
	Users       []usageSource             `json:"user_sources"`
	Tokens      []usageSource             `json:"token_sources"`
	Bases       []walletFutureCreditBasis `json:"other_credit_bases"`
}
type usageBaseline struct {
	raw                             int64
	owner                           int
	divisor                         *big.Rat
	rounding, migration             string
	reversedRaw, reversedNormalized int64
}

// UsageProjector is an immutable read-only snapshot; reuse it across list DTOs.
type UsageProjector struct {
	users, tokens map[int]usageBaseline
	missingTokens map[int]bool
}

var usageDivisorPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]{1,18})?$`)

func usageError(reason string) error {
	return fmt.Errorf("%w: %s", ErrUsageProjectionUnavailable, reason)
}

// LoadUsageProjector reads audit facts only. It must not be used for settlement.
func LoadUsageProjector(tx *gorm.DB) (*UsageProjector, error) {
	if tx == nil {
		return nil, usageError("database unavailable")
	}
	result := &UsageProjector{users: map[int]usageBaseline{}, tokens: map[int]usageBaseline{}, missingTokens: map[int]bool{}}
	exists, err := walletCreditAuditTableExists(tx, "wallet_credit_rebases")
	if err != nil {
		return nil, err
	}
	if !exists {
		return result, nil
	}
	var audits []struct{ Plan string }
	if err := tx.Table("wallet_credit_rebases").Select("plan").Find(&audits).Error; err != nil {
		return nil, err
	}
	seenScopes := map[int]bool{}
	for _, audit := range audits {
		var plan usagePlan
		if json.Unmarshal([]byte(audit.Plan), &plan) != nil {
			return nil, usageError("invalid migration audit")
		}
		divisor, ok := new(big.Rat).SetString(plan.Divisor)
		if plan.Version != 1 || plan.Kind != "offline_credit_balance_rebase_preview" || plan.MigrationID == "" || !plan.Complete || plan.Anchor != 500000 || plan.SnapshotAt <= 0 || !usageDivisorPattern.MatchString(plan.Divisor) || !ok || divisor.Cmp(big.NewRat(1, 1)) <= 0 || (plan.Rounding != "half-away-from-zero" && plan.Rounding != "toward-zero") || plan.Users == nil || plan.Tokens == nil {
			return nil, usageError("unsupported or incomplete migration basis")
		}
		selected := map[int]bool{}
		for _, id := range plan.UserIDs {
			if id <= 0 || selected[id] {
				return nil, usageError("invalid migration scope")
			}
			if seenScopes[id] {
				return nil, usageError("duplicate user migration scope")
			}
			seenScopes[id] = true
			selected[id] = true
		}
		if len(selected) == 0 {
			return nil, usageError("empty migration scope")
		}
		planUsers := map[int]usageBaseline{}
		for _, source := range plan.Users {
			if source.ID <= 0 || !selected[source.ID] || source.Used == nil || *source.Used < 0 || *source.Used > common.MaxWalletQuota {
				return nil, usageError("invalid user historical baseline")
			}
			if _, duplicate := planUsers[source.ID]; duplicate {
				return nil, usageError("duplicate user migration baseline")
			}
			planUsers[source.ID] = usageBaseline{raw: *source.Used, owner: source.ID, divisor: divisor, rounding: plan.Rounding, migration: plan.MigrationID}
		}
		for id := range selected {
			if _, ok := planUsers[id]; !ok {
				return nil, usageError("selected user historical baseline missing")
			}
		}
		for id, base := range planUsers {
			result.users[id] = base
		}
		for _, source := range plan.Tokens {
			if source.ID <= 0 || !selected[source.UserID] || source.Used == nil || *source.Used < 0 || *source.Used > common.MaxWalletQuota {
				return nil, usageError("invalid token historical baseline")
			}
			if _, duplicate := result.tokens[source.ID]; duplicate {
				return nil, usageError("duplicate token migration baseline")
			}
			result.tokens[source.ID] = usageBaseline{raw: *source.Used, owner: source.UserID, divisor: divisor, rounding: plan.Rounding, migration: plan.MigrationID}
		}
		tokenTable, err := walletCreditAuditTableExists(tx, "tokens")
		if err != nil {
			return nil, err
		}
		if tokenTable {
			var currentTokens []struct {
				ID          int
				UserID      int
				CreatedTime int64
			}
			if err := tx.Table("tokens").Select("id,user_id,created_time").Where("user_id IN ?", plan.UserIDs).Find(&currentTokens).Error; err != nil {
				return nil, err
			}
			for _, token := range currentTokens {
				if _, ok := result.tokens[token.ID]; !ok && token.CreatedTime <= plan.SnapshotAt {
					result.missingTokens[token.ID] = true
				}
			}
		} else if len(plan.Tokens) > 0 {
			return nil, usageError("token source table missing")
		}
		if err := result.loadUsageReversals(tx, plan, selected); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (p *UsageProjector) loadUsageReversals(tx *gorm.DB, plan usagePlan, selected map[int]bool) error {
	seen := map[string]bool{}
	for _, base := range plan.Bases {
		if base.Kind != "violation_fee_refund" {
			continue
		}
		var source ViolationFeeRecord
		id, err := strconv.ParseUint(base.SourceID, 10, 64)
		if err != nil || id == 0 || seen[base.SourceID] || !selected[base.UserID] || json.Unmarshal(base.Source, &source) != nil || uint64(source.ID) != id || source.UserID != base.UserID || source.ChargedQuota != base.OriginalQuota || source.ChargedQuota <= 0 || source.ChargedQuota > common.MaxWalletQuota || source.Status != ViolationFeeRecordStatusCharged || source.ReversedAt != 0 || source.CreatedAt > plan.SnapshotAt {
			return usageError("invalid historical fee reversal basis")
		}
		seen[base.SourceID] = true
		baseline := p.users[base.UserID]
		if int64(base.RebasedQuota) != scaleReferralCredit(int64(base.OriginalQuota), baseline.divisor, baseline.rounding) {
			return usageError("inconsistent historical fee reversal units")
		}
		var current ViolationFeeRecord
		if err := tx.Where("id = ?", source.ID).First(&current).Error; err != nil {
			return usageError("historical fee reversal record missing")
		}
		if current.UserID != source.UserID || current.ChargedQuota != source.ChargedQuota || current.ErrorCode != source.ErrorCode || current.CreatedAt != source.CreatedAt {
			return usageError("historical fee reversal source changed")
		}
		switch current.Status {
		case ViolationFeeRecordStatusCharged:
			if current.ReversedAt != 0 {
				return usageError("inconsistent charged historical fee")
			}
		case ViolationFeeRecordStatusReversed:
			if current.ReversedAt < plan.SnapshotAt || current.ReversedBy <= 0 {
				return usageError("unproven historical fee reversal")
			}
			if !strings.HasPrefix(current.ErrorCode, "moderation.") {
				baseline.reversedRaw += int64(source.ChargedQuota)
				baseline.reversedNormalized += int64(base.RebasedQuota)
				if baseline.reversedRaw > baseline.raw {
					return usageError("historical fee reversals exceed baseline")
				}
				p.users[base.UserID] = baseline
			}
		default:
			return usageError("unsupported historical fee reversal state")
		}
	}
	exists, err := walletCreditAuditTableExists(tx, "violation_fee_records")
	if err != nil {
		return err
	}
	if exists {
		var reversed []ViolationFeeRecord
		if err := tx.Where("user_id IN ? AND created_at <= ? AND status = ? AND reversed_at >= ?", plan.UserIDs, plan.SnapshotAt, ViolationFeeRecordStatusReversed, plan.SnapshotAt).Find(&reversed).Error; err != nil {
			return err
		}
		for _, record := range reversed {
			if !strings.HasPrefix(record.ErrorCode, "moderation.") && !seen[strconv.FormatUint(uint64(record.ID), 10)] {
				return usageError("historical fee reversal audit missing")
			}
		}
	}
	return nil
}

func projectUsage(current int, base usageBaseline, migrated bool) (UsageProjection, error) {
	if current < 0 || current > common.MaxWalletQuota {
		return UsageProjection{}, usageError("current usage outside safe integer domain")
	}
	if !migrated {
		return UsageProjection{NormalizedUsedQuota: current, PostMigrationQuota: current}, nil
	}
	historical := scaleReferralCredit(base.raw, base.divisor, base.rounding) - base.reversedNormalized
	delta := int64(current) - base.raw + base.reversedRaw
	if delta < 0 {
		return UsageProjection{}, usageError("unproven historical usage counter reduction")
	}
	total := historical + delta
	if historical < 0 || total < 0 || total > common.MaxWalletQuota {
		return UsageProjection{}, usageError("normalized usage outside safe integer domain")
	}
	return UsageProjection{NormalizedUsedQuota: int(total), HistoricalRawQuota: int(base.raw - base.reversedRaw), HistoricalNormalizedQuota: int(historical), PostMigrationQuota: int(delta), MigrationID: base.migration}, nil
}
func (p *UsageProjector) User(userID, currentUsed int) (UsageProjection, error) {
	if p == nil || userID <= 0 {
		return UsageProjection{}, usageError("invalid user projection")
	}
	base, ok := p.users[userID]
	return projectUsage(currentUsed, base, ok)
}
func (p *UsageProjector) Token(tokenID, userID, currentUsed int) (UsageProjection, error) {
	if p == nil || tokenID <= 0 || userID <= 0 {
		return UsageProjection{}, usageError("invalid token projection")
	}
	if p.missingTokens[tokenID] {
		return UsageProjection{}, usageError("historical token baseline missing")
	}
	base, ok := p.tokens[tokenID]
	if ok && base.owner != userID {
		return UsageProjection{}, usageError("token historical owner changed")
	}
	return projectUsage(currentUsed, base, ok)
}
