package model

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// These immutable child bases live inside the reviewed parent migration plan.
// Historical charges, gift decisions and tip ledgers retain their source units.
type walletFutureCreditBasis struct {
	Kind          string          `json:"kind"`
	SourceID      string          `json:"source_id"`
	UserID        int             `json:"user_id"`
	OriginalQuota int             `json:"original_quota"`
	RebasedQuota  int             `json:"rebased_quota"`
	Source        json.RawMessage `json:"source"`
}

func walletFutureCreditBasisTx(tx *gorm.DB, userID int, kind, sourceID string) (*walletFutureCreditBasis, int64, error) {
	if tx == nil {
		return nil, 0, nil
	}
	exists, err := walletCreditAuditTableExists(tx, "wallet_credit_rebases")
	if err != nil || !exists {
		return nil, 0, err
	}
	var audits []struct{ Plan string }
	if err := tx.Table("wallet_credit_rebases").Select("plan").Find(&audits).Error; err != nil {
		return nil, 0, err
	}
	var found *walletFutureCreditBasis
	var cutoff int64
	for _, audit := range audits {
		var plan struct {
			UserIDs            []int           `json:"user_ids"`
			SnapshotAt         int64           `json:"snapshot_at"`
			Divisor            string          `json:"divisor"`
			Rounding           string          `json:"rounding"`
			IncludeOtherRights bool            `json:"include_other_rights"`
			Bases              json.RawMessage `json:"other_credit_bases"`
		}
		if json.Unmarshal([]byte(audit.Plan), &plan) != nil {
			return nil, 0, fmt.Errorf("%w: invalid future credit audit", ErrWalletQuotaOutOfRange)
		}
		affected := kind == "grant_gift" && plan.IncludeOtherRights
		for _, id := range plan.UserIDs {
			if id == userID {
				affected = true
			}
		}
		if affected {
			if !plan.IncludeOtherRights || plan.SnapshotAt <= 0 || len(plan.Bases) == 0 || string(plan.Bases) == "null" {
				return nil, 0, fmt.Errorf("%w: future credit migration scope missing", ErrWalletQuotaOutOfRange)
			}
			if plan.SnapshotAt > cutoff {
				cutoff = plan.SnapshotAt
			}
		}
		var bases []walletFutureCreditBasis
		if len(plan.Bases) > 0 && json.Unmarshal(plan.Bases, &bases) != nil {
			return nil, 0, fmt.Errorf("%w: invalid future credit baselines", ErrWalletQuotaOutOfRange)
		}
		for _, base := range bases {
			if base.Kind != kind || base.SourceID != sourceID {
				continue
			}
			divisor, ok := new(big.Rat).SetString(plan.Divisor)
			owner := userID
			if kind == "grant_gift" {
				owner = 0
			}
			if found != nil || base.UserID != owner || base.OriginalQuota < 0 || base.OriginalQuota > common.MaxWalletQuota || base.RebasedQuota < 0 || base.RebasedQuota > base.OriginalQuota || !ok || divisor.Cmp(big.NewRat(1, 1)) <= 0 ||
				(plan.Rounding != "half-away-from-zero" && plan.Rounding != "toward-zero") || base.RebasedQuota != int(scaleReferralCredit(int64(base.OriginalQuota), divisor, plan.Rounding)) || len(base.Source) == 0 {
				return nil, 0, fmt.Errorf("%w: inconsistent future credit baseline", ErrWalletQuotaOutOfRange)
			}
			copy := base
			found = &copy
		}
	}
	return found, cutoff, nil
}

// WalletFutureCreditQuota applies only a stored integer basis. A migrated user's
// old source without its required child audit is an error, never a raw fallback.
func WalletFutureCreditQuota(tx *gorm.DB, userID int, kind, sourceID string, historical int, createdAt int64) (int, error) {
	base, cutoff, err := walletFutureCreditBasisTx(tx, userID, kind, sourceID)
	if err != nil {
		return 0, err
	}
	if base == nil && cutoff > 0 && createdAt <= cutoff {
		return 0, fmt.Errorf("%w: historical future credit baseline missing", ErrWalletQuotaOutOfRange)
	}
	return walletFutureCreditAmount(base, historical)
}

// WalletFutureCreditReceiptQuota projects an already fulfilled receipt. It never
// authorizes a wallet write, so pre-migration fulfilled receipts remain readable.
func WalletFutureCreditReceiptQuota(tx *gorm.DB, userID int, kind, sourceID string, historical int) (int, error) {
	base, _, err := walletFutureCreditBasisTx(tx, userID, kind, sourceID)
	if err != nil {
		return 0, err
	}
	return walletFutureCreditAmount(base, historical)
}

func walletFutureCreditAmount(base *walletFutureCreditBasis, historical int) (int, error) {
	if historical < 0 || historical > common.MaxWalletQuota {
		return 0, ErrWalletQuotaOutOfRange
	}
	if base == nil {
		return historical, nil
	}
	if base.OriginalQuota != historical {
		return 0, fmt.Errorf("%w: future credit source amount changed", ErrWalletQuotaOutOfRange)
	}
	return base.RebasedQuota, nil
}

func publicRelayAvailableCreditTx(tx *gorm.DB, item *PublicRelayContribution) (int64, error) {
	available := item.TipQuota - item.WithdrawnQuota
	if available < 0 || item.TipQuota > common.MaxWalletQuota || item.WithdrawnQuota < 0 {
		return 0, ErrWalletQuotaOutOfRange
	}
	base, cutoff, err := walletFutureCreditBasisTx(tx, item.UserId, "public_relay_tip_pool", strconv.Itoa(item.Id))
	if err != nil {
		return 0, err
	}
	if base == nil {
		if cutoff > 0 && item.CreatedAt <= cutoff && available > 0 {
			return 0, fmt.Errorf("%w: historical tip pool baseline missing", ErrWalletQuotaOutOfRange)
		}
		return available, nil
	}
	var source struct {
		TipQuota       int64 `json:"tip_quota"`
		WithdrawnQuota int64 `json:"withdrawn_quota"`
	}
	if json.Unmarshal(base.Source, &source) != nil || source.TipQuota-source.WithdrawnQuota != int64(base.OriginalQuota) || source.WithdrawnQuota < 0 || item.TipQuota < source.TipQuota || item.WithdrawnQuota < source.WithdrawnQuota {
		return 0, fmt.Errorf("%w: tip pool source changed", ErrWalletQuotaOutOfRange)
	}
	if item.WithdrawnQuota >= source.TipQuota {
		return available, nil // The full corrected old pool was already withdrawn.
	}
	if item.WithdrawnQuota != source.WithdrawnQuota {
		return 0, fmt.Errorf("%w: partial historical tip pool withdrawal", ErrWalletQuotaOutOfRange)
	}
	return available - int64(base.OriginalQuota) + int64(base.RebasedQuota), nil
}

func AssistantGiftCreditQuota(gift *AssistantNewUserGift) (int, error) {
	if gift == nil {
		return 0, nil
	}
	if gift.Status == AssistantGiftClaimed {
		return WalletFutureCreditReceiptQuota(DB, gift.UserId, "assistant_gift", strconv.FormatInt(gift.Id, 10), gift.Quota)
	}
	if gift.Status != AssistantGiftOffered {
		return gift.Quota, nil
	}
	return WalletFutureCreditQuota(DB, gift.UserId, "assistant_gift", strconv.FormatInt(gift.Id, 10), gift.Quota, gift.CreatedAt)
}
