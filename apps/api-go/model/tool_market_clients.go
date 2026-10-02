package model

import (
	"sort"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// ToolMarketOAuthClient is an authorization target, not a token status or an
// introspection response. It contains no OAuth family IDs, scopes or digests.
type ToolMarketOAuthClient struct {
	ClientID string `json:"client_id"`
}

// ListToolMarketOAuthClients includes native clients which can invoke market
// tools, including those without the optional manage scope. Expired access
// tokens can still be refreshed, but used/expired refresh tokens, revoked
// families and narrowed token scopes cannot make a client eligible.
// The caller supplies the configured OAuth writer DB and trusted client policy.
// This query never creates market access or changes OAuth consent.
func ListToolMarketOAuthClients(db *gorm.DB, userID int, issuer, resource string, clientIDs, requiredScopes []string, offset, limit int) ([]ToolMarketOAuthClient, error) {
	if db == nil || issuer == "" || resource == "" || len(clientIDs) == 0 || len(requiredScopes) == 0 || userID <= 0 || offset < 0 || offset > 10000 || limit < 1 || limit > 100 {
		return nil, ErrToolMarketInput
	}
	if err := marketUser(db, userID, common.RoleCommonUser); err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	var candidates []struct {
		ClientID   string
		GrantScope string
		TokenScope string
	}
	err := db.Table("oauth_server_grants AS family").
		Select("DISTINCT family.client_id, family.scope AS grant_scope, tok.scope AS token_scope").
		Joins("JOIN oauth_server_tokens AS tok ON tok.family_id = family.id AND tok.issuer = family.issuer").
		Where("family.user_id = ? AND family.issuer = ? AND family.resource = ? AND family.client_id IN ?", userID, issuer, resource, clientIDs).
		Where("family.revoked_at_ms = 0 AND family.absolute_expires_at_ms > ? AND family.binding_method = '' AND family.binding_thumbprint = ''", now).
		Where("tok.kind IN ? AND tok.expires_at_ms > ? AND tok.used_at_ms = 0", []string{"access", "refresh"}, now).
		Scan(&candidates).Error
	if err != nil {
		return nil, err
	}
	eligible := map[string]bool{}
	for _, candidate := range candidates {
		allowed := marketClientValid("oauth:" + candidate.ClientID)
		for _, scope := range requiredScopes {
			allowed = allowed && containsOAuthScope(candidate.GrantScope, scope) && containsOAuthScope(candidate.TokenScope, scope)
		}
		if allowed {
			eligible[candidate.ClientID] = true
		}
	}
	ids := make([]string, 0, len(eligible))
	for id := range eligible {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	rows := []ToolMarketOAuthClient{}
	for i := offset; i < len(ids) && len(rows) < limit; i++ {
		rows = append(rows, ToolMarketOAuthClient{ClientID: "oauth:" + ids[i]})
	}
	return rows, nil
}

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
