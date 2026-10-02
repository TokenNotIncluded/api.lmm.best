package model

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

var ErrWalletTransferUnavailable = errors.New("transfer is unavailable")
var ErrWalletTransferBalance = errors.New("insufficient wallet balance")
var ErrWalletTransferInvalid = errors.New("invalid transfer amount or request")

// The random token is a bearer credential. Only the sender's authenticated
// history exposes it; public inspection never exposes either user's identity.
type WalletTransfer struct {
	Id                int    `json:"id"`
	SenderID          int    `json:"sender_id" gorm:"uniqueIndex:idx_wallet_transfer_request;index"`
	RequestKey        string `json:"-" gorm:"size:64;uniqueIndex:idx_wallet_transfer_request"`
	Token             string `json:"token" gorm:"size:64;uniqueIndex"`
	Quota             int    `json:"quota" gorm:"type:bigint;not null"`
	Status            string `json:"status" gorm:"size:16;index;not null"`
	CreatedAt         int64  `json:"created_at"`
	ClaimedAt         int64  `json:"claimed_at"`
	CancelledAt       int64  `json:"cancelled_at"`
	RecipientID       int    `json:"recipient_id"`
	RecipientUsername string `json:"recipient_username" gorm:"size:64"`
	RecipientName     string `json:"recipient_name" gorm:"size:64"`
	RecipientEmail    string `json:"recipient_email" gorm:"size:256"`
}

// Do not let GORM render bearer credentials or contact snapshots into SQL logs.
func walletTransferDB() *gorm.DB {
	return DB.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
}

func validTransferToken(token string) bool {
	if len(token) != 64 {
		return false
	}
	_, err := hex.DecodeString(token)
	return err == nil
}

func CreateWalletTransfer(senderID, quota int, requestKey string) (*WalletTransfer, error) {
	return createWalletTransfer(senderID, quota, requestKey, 0, nil)
}

// CreateWalletTransferWithMCPConfirmation commits the exact confirmed action
// and its wallet debit together. Retries can only recover that same result.
func CreateWalletTransferWithMCPConfirmation(senderID, quota int, requestKey string, authVersion int64, operation OpenSourceBountyMCPConfirmedOperation) (*WalletTransfer, error) {
	if operation.ToolName != "wallet.transfer.create" || operation.State == "" || operation.PayloadHash == "" {
		return nil, ErrWalletTransferInvalid
	}
	return createWalletTransfer(senderID, quota, requestKey, authVersion, &operation)
}

func createWalletTransfer(senderID, quota int, requestKey string, authVersion int64, operation *OpenSourceBountyMCPConfirmedOperation) (*WalletTransfer, error) {
	if senderID <= 0 || quota <= 0 || common.ValidateWalletQuota(quota) != nil || len(requestKey) < 16 || len(requestKey) > 64 || strings.TrimSpace(requestKey) != requestKey {
		return nil, ErrWalletTransferInvalid
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	transfer := WalletTransfer{SenderID: senderID, RequestKey: requestKey, Token: hex.EncodeToString(secret), Quota: quota, Status: "pending", CreatedAt: common.GetTimestamp()}
	created := false
	err := walletTransferDB().Transaction(func(tx *gorm.DB) error {
		// Serialize creates/retries for this sender before checking the idempotency key.
		var sender User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status = ?", senderID, common.UserStatusEnabled).First(&sender).Error; err != nil {
			return err
		}
		if operation != nil {
			if sender.AuthVersion != authVersion {
				return ErrWalletTransferUnavailable
			}
			replay, err := walletMCPReplayTx(tx, senderID, *operation)
			if err != nil {
				return err
			}
			if replay {
				return tx.Where("sender_id = ? AND request_key = ? AND quota = ?", senderID, requestKey, quota).First(&transfer).Error
			}
			if err := validateOpenSourceBountyMCPConfirmationTx(tx, senderID, operation.ToolName, operation.PayloadHash, operation.State); err != nil {
				return err
			}
		}
		var existing WalletTransfer
		err := tx.Where("sender_id = ? AND request_key = ?", senderID, requestKey).First(&existing).Error
		if err == nil {
			if existing.Quota != quota {
				return ErrWalletTransferInvalid
			}
			if operation != nil {
				return ErrWalletTransferInvalid
			}
			transfer = existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		result := UpdateWalletQuotaByDelta(tx.Model(&User{}).Where("id = ? AND quota >= ? AND status = ?", senderID, quota, common.UserStatusEnabled), -quota)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrWalletTransferBalance
		}
		if err := tx.Create(&transfer).Error; err != nil {
			return err
		}
		if operation != nil {
			if err := completeOpenSourceBountyMCPOperationTx(tx, senderID, operation.ToolName, operation.PayloadHash, operation.State, map[string]any{"transfer_id": transfer.Id}); err != nil {
				return err
			}
		}
		created = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if created {
		syncUserQuotaDeltaCacheAsync(senderID, -quota, "create wallet transfer")
	}
	return &transfer, nil
}

func ListWalletTransfers(senderID, beforeID int) ([]WalletTransfer, error) {
	result := make([]WalletTransfer, 0)
	query := walletTransferDB().Where("sender_id = ?", senderID)
	if beforeID > 0 {
		query = query.Where("id < ?", beforeID)
	}
	err := query.Order("id DESC").Limit(50).Find(&result).Error
	return result, err
}

func InspectWalletTransfer(token string) (*WalletTransfer, error) {
	if !validTransferToken(token) {
		return nil, ErrWalletTransferUnavailable
	}
	var transfer WalletTransfer
	if err := walletTransferDB().Where("token = ?", token).First(&transfer).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWalletTransferUnavailable
		}
		return nil, err
	}
	return &transfer, nil
}

func ClaimWalletTransfer(token string, userID int) (*WalletTransfer, error) {
	if !validTransferToken(token) || userID <= 0 {
		return nil, ErrWalletTransferUnavailable
	}
	var transfer WalletTransfer
	credited := false
	err := walletTransferDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("token = ?", token).First(&transfer).Error; err != nil {
			return ErrWalletTransferUnavailable
		}
		if transfer.Status == "claimed" && transfer.RecipientID == userID {
			return nil
		}
		if transfer.Status != "pending" || transfer.SenderID == userID {
			return ErrWalletTransferUnavailable
		}
		var recipient User
		if err := tx.Where("id = ? AND status = ?", userID, common.UserStatusEnabled).First(&recipient).Error; err != nil {
			return ErrWalletTransferUnavailable
		}
		transfer.Status = "claimed"
		transfer.ClaimedAt = common.GetTimestamp()
		transfer.RecipientID = userID
		transfer.RecipientUsername = recipient.Username
		transfer.RecipientName = recipient.DisplayName
		transfer.RecipientEmail = recipient.Email
		// The conditional state change also protects engines without row locks.
		result := tx.Model(&WalletTransfer{}).Where("id = ? AND status = ?", transfer.Id, "pending").Updates(map[string]interface{}{
			"status": transfer.Status, "claimed_at": transfer.ClaimedAt, "recipient_id": userID,
			"recipient_username": transfer.RecipientUsername, "recipient_name": transfer.RecipientName, "recipient_email": transfer.RecipientEmail,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrWalletTransferUnavailable
		}
		result = UpdateWalletQuotaByDelta(tx.Model(&User{}).Where("id = ? AND status = ?", userID, common.UserStatusEnabled), transfer.Quota)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrWalletQuotaOutOfRange
		}
		credited = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if credited {
		syncUserQuotaDeltaCacheAsync(userID, transfer.Quota, "claim wallet transfer")
	}
	return &transfer, nil
}

func CancelWalletTransfer(id, senderID int) error {
	return cancelWalletTransfer(id, senderID, 0, nil)
}

func CancelWalletTransferWithMCPConfirmation(id, senderID int, authVersion int64, operation OpenSourceBountyMCPConfirmedOperation) error {
	if operation.ToolName != "wallet.transfer.cancel" || operation.State == "" || operation.PayloadHash == "" {
		return ErrWalletTransferInvalid
	}
	return cancelWalletTransfer(id, senderID, authVersion, &operation)
}

func cancelWalletTransfer(id, senderID int, authVersion int64, operation *OpenSourceBountyMCPConfirmedOperation) error {
	var transfer WalletTransfer
	refunded := false
	err := walletTransferDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND sender_id = ?", id, senderID).First(&transfer).Error; err != nil {
			return ErrWalletTransferUnavailable
		}
		if operation != nil {
			var sender User
			if err := lockForUpdate(tx).Where("id = ? AND status = ? AND auth_version = ?", senderID, common.UserStatusEnabled, authVersion).First(&sender).Error; err != nil {
				return ErrWalletTransferUnavailable
			}
			replay, err := walletMCPReplayTx(tx, senderID, *operation)
			if err != nil {
				return err
			}
			if replay {
				if transfer.Status == "cancelled" {
					return nil
				}
				return ErrWalletTransferUnavailable
			}
			if err := validateOpenSourceBountyMCPConfirmationTx(tx, senderID, operation.ToolName, operation.PayloadHash, operation.State); err != nil {
				return err
			}
		}
		if transfer.Status == "cancelled" {
			if operation != nil {
				return ErrWalletTransferUnavailable
			}
			return nil
		}
		if transfer.Status != "pending" {
			return ErrWalletTransferUnavailable
		}
		result := tx.Model(&WalletTransfer{}).Where("id = ? AND status = ?", id, "pending").Updates(map[string]interface{}{"status": "cancelled", "cancelled_at": common.GetTimestamp()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrWalletTransferUnavailable
		}
		if err := ApplyWalletQuotaDelta(tx, senderID, transfer.Quota); err != nil {
			return err
		}
		if operation != nil {
			if err := completeOpenSourceBountyMCPOperationTx(tx, senderID, operation.ToolName, operation.PayloadHash, operation.State, map[string]any{"transfer_id": transfer.Id}); err != nil {
				return err
			}
		}
		refunded = true
		return nil
	})
	if err == nil && refunded {
		syncUserQuotaDeltaCacheAsync(senderID, transfer.Quota, "cancel wallet transfer")
	}
	return err
}

func EnsureWalletTransferSchemaAtStartup() error {
	if DB == nil {
		return nil
	}
	if !DB.Migrator().HasTable(&WalletTransfer{}) {
		return fmt.Errorf("wallet transfer schema missing; run migrate --apply")
	}
	for _, column := range []string{"id", "sender_id", "request_key", "token", "quota", "status", "created_at", "claimed_at", "cancelled_at", "recipient_id", "recipient_username", "recipient_name", "recipient_email"} {
		if !DB.Migrator().HasColumn(&WalletTransfer{}, column) {
			return fmt.Errorf("wallet transfer schema missing column %s", column)
		}
	}
	return nil
}
