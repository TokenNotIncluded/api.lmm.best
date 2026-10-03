package model

import (
	"errors"

	"gorm.io/gorm"
)

// The same state cannot authorize a different owner, tool or payload, including
// after a successful operation. Never use a replay as a new debit/refund.
func walletMCPReplayTx(tx *gorm.DB, userID int, operation OpenSourceBountyMCPConfirmedOperation) (bool, error) {
	var stored OpenSourceBountyMCPOperation
	err := tx.Where("id = ?", operation.State).First(&stored).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if stored.UserId != userID || stored.ToolName != operation.ToolName || stored.PayloadHash != operation.PayloadHash {
		return false, ErrWalletTransferInvalid
	}
	return true, nil
}
