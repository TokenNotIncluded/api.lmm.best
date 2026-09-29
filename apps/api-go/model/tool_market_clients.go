package model

import (
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// ToolMarketClientDisconnect reports changes, not the account's private history.
type ToolMarketClientDisconnect struct {
	ClientID      string `json:"client_id"`
	TokensRevoked int64  `json:"tokens_revoked"`
	GrantsRevoked int64  `json:"grants_revoked"`
	ToolsUnloaded int64  `json:"tools_unloaded"`
}

// DisconnectToolMarketClient atomically removes a personal-token client's future
// access. It deliberately leaves calls, reservations, budgets and transfers alone:
// a disconnect is not evidence that an already dispatched business call failed.
// OAuth grants have their own revocation flow and must not be reported as revoked
// by an operation that only knows about personal marketplace connection tokens.
func DisconnectToolMarketClient(userID int, clientID string) (*ToolMarketClientDisconnect, error) {
	if userID <= 0 {
		return nil, ErrToolMarketDenied
	}
	if !marketClientValid(clientID) || clientID == ToolMarketWebClient || strings.HasPrefix(clientID, "oauth:") {
		return nil, ErrToolMarketInput
	}
	result := &ToolMarketClientDisconnect{ClientID: clientID}
	err := DB.Transaction(func(tx *gorm.DB) error {
		// Same lock order as token/grant creation, installation and call reservation.
		if err := marketLockUsers(tx, userID); err != nil {
			return err
		}
		if err := marketUser(tx, userID, common.RoleCommonUser); err != nil {
			return err
		}
		now := common.GetTimestamp()
		tokens := tx.Model(&ToolMarketToken{}).
			Where("user_id = ? AND client_id = ? AND revoked_at = 0", userID, clientID).
			Update("revoked_at", now)
		if tokens.Error != nil {
			return tokens.Error
		}
		result.TokensRevoked = tokens.RowsAffected
		grants := tx.Model(&ToolMarketGrant{}).
			Where("user_id = ? AND client_id = ? AND revoked_at = 0", userID, clientID).
			Update("revoked_at", now)
		if grants.Error != nil {
			return grants.Error
		}
		result.GrantsRevoked = grants.RowsAffected
		installations := tx.Where("user_id = ? AND client_id = ?", userID, clientID).
			Delete(&ToolMarketInstallation{})
		if installations.Error != nil {
			return installations.Error
		}
		result.ToolsUnloaded = installations.RowsAffected
		if result.TokensRevoked+result.GrantsRevoked+result.ToolsUnloaded == 0 {
			return nil // Repeating a disconnect does not create duplicate audit events.
		}
		return marketEvent(tx, userID, marketDigest(clientID), "client.disconnect", result)
	})
	if err != nil {
		return nil, err // Never report partial counts after a rolled-back transaction.
	}
	return result, nil
}
