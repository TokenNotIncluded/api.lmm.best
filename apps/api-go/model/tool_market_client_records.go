package model

import (
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

const (
	toolMarketGrantHiddenAction = "grant.hide"
	toolMarketTokenHiddenAction = "token.hide"
)

// Visibility is an account-owned audit event, not deletion of an authorization.
// In particular, token digests and revocation timestamps remain available to
// older binaries, which may show these rows again but must still reject them.
func marketVisibleAccountRecords(userID int, action string) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		outerID, hiddenID := "id", "object_id"
		if db.Dialector.Name() == "mysql" {
			// Visibility IDs must not inherit MySQL case/accent aliases either.
			outerID, hiddenID = "CAST(id AS BINARY)", "CAST(object_id AS BINARY)"
		}
		hidden := db.Session(&gorm.Session{NewDB: true}).Model(&ToolMarketEvent{}).
			Select(hiddenID).Where("actor_id = ? AND object_id IS NOT NULL", userID).
			Scopes(marketExactTextScope("action", action))
		return db.Where(outerID+" NOT IN (?)", hidden)
	}
}

func marketHideAccountRecord(tx *gorm.DB, userID int, id, action string) (bool, error) {
	var count int64
	if err := tx.Model(&ToolMarketEvent{}).Where("actor_id = ?", userID).
		Scopes(marketExactTextScope("object_id", id), marketExactTextScope("action", action)).Count(&count).Error; err != nil {
		return false, err
	}
	if count != 0 {
		return false, nil
	}
	return true, marketEvent(tx, userID, id, action, nil)
}

// Removing a revoked grant changes only its visibility. Already accepted calls
// retain the original grant and its reservations, limits and settlement history.
func RemoveToolMarketGrantRecord(userID int, id string) error {
	return marketTransaction(DB, func(tx *gorm.DB) error {
		if err := marketLockUsers(tx, userID); err != nil {
			return err
		}
		if err := marketUser(tx, userID, common.RoleCommonUser); err != nil {
			return err
		}
		var grant ToolMarketGrant
		if err := tx.Scopes(marketExactTextScope("id", id)).First(&grant, "user_id = ?", userID).Error; err != nil {
			return err
		}
		if grant.RevokedAt == 0 {
			return ErrToolMarketConflict
		}
		_, err := marketHideAccountRecord(tx, userID, grant.ID, toolMarketGrantHiddenAction)
		return err
	})
}

func RemoveToolMarketTokenRecord(userID int, id string) error {
	return marketTransaction(DB, func(tx *gorm.DB) error {
		if err := marketLockUsers(tx, userID); err != nil {
			return err
		}
		if err := marketUser(tx, userID, common.RoleCommonUser); err != nil {
			return err
		}
		var token ToolMarketToken
		if err := tx.Scopes(marketExactTextScope("id", id)).First(&token, "user_id = ?", userID).Error; err != nil {
			return err
		}
		if token.RevokedAt == 0 {
			return ErrToolMarketConflict
		}
		_, err := marketHideAccountRecord(tx, userID, token.ID, toolMarketTokenHiddenAction)
		return err
	})
}

type ToolMarketClientRemoval struct {
	ClientID      string `json:"client_id"`
	TokensHidden  int64  `json:"tokens_hidden"`
	GrantsHidden  int64  `json:"grants_hidden"`
	ToolsUnloaded int64  `json:"tools_unloaded"`
}

// A whole personal client can leave the list only after explicit revocation.
// New credentials use the same account lock and new IDs, so a concurrent setup
// cannot be mistaken for the historical records being removed here.
func RemoveToolMarketClient(userID int, clientID string) (*ToolMarketClientRemoval, error) {
	if userID <= 0 {
		return nil, ErrToolMarketDenied
	}
	if !marketClientValid(clientID) || clientID == ToolMarketWebClient || strings.HasPrefix(clientID, "oauth:") {
		return nil, ErrToolMarketInput
	}
	result := &ToolMarketClientRemoval{ClientID: clientID}
	err := marketTransaction(DB, func(tx *gorm.DB) error {
		if err := marketLockUsers(tx, userID); err != nil {
			return err
		}
		if err := marketUser(tx, userID, common.RoleCommonUser); err != nil {
			return err
		}
		var tokens []ToolMarketToken
		if err := tx.Where("user_id = ?", userID).Scopes(marketExactTextScope("client_id", clientID)).Find(&tokens).Error; err != nil {
			return err
		}
		var grants []ToolMarketGrant
		if err := tx.Where("user_id = ?", userID).Scopes(marketExactTextScope("client_id", clientID)).Find(&grants).Error; err != nil {
			return err
		}
		for _, token := range tokens {
			if token.RevokedAt == 0 {
				return ErrToolMarketConflict
			}
		}
		for _, grant := range grants {
			if grant.RevokedAt == 0 {
				return ErrToolMarketConflict
			}
		}
		for _, token := range tokens {
			hidden, err := marketHideAccountRecord(tx, userID, token.ID, toolMarketTokenHiddenAction)
			if err != nil {
				return err
			}
			if hidden {
				result.TokensHidden++
			}
		}
		for _, grant := range grants {
			hidden, err := marketHideAccountRecord(tx, userID, grant.ID, toolMarketGrantHiddenAction)
			if err != nil {
				return err
			}
			if hidden {
				result.GrantsHidden++
			}
		}
		installations := tx.Where("user_id = ?", userID).Scopes(marketExactTextScope("client_id", clientID)).Delete(&ToolMarketInstallation{})
		if installations.Error != nil {
			return installations.Error
		}
		result.ToolsUnloaded = installations.RowsAffected
		if len(tokens)+len(grants) == 0 && result.ToolsUnloaded == 0 {
			var previous int64
			if err := tx.Model(&ToolMarketEvent{}).Where("actor_id = ?", userID).
				Scopes(marketExactTextScope("object_id", marketDigest(clientID)), marketExactTextScope("action", "client.remove")).Count(&previous).Error; err != nil {
				return err
			}
			if previous == 0 {
				return gorm.ErrRecordNotFound
			}
			return nil
		}
		if result.TokensHidden+result.GrantsHidden+result.ToolsUnloaded == 0 {
			return nil
		}
		return marketEvent(tx, userID, marketDigest(clientID), "client.remove", result)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
