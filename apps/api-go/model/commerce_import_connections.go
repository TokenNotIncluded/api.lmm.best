package model

import (
	"database/sql"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const commerceImportInternalLeaseSeconds int64 = 30

// A waiter must count committed connections after acquiring the account lock,
// including when PostgreSQL's server default uses a repeatable-read snapshot.
func commerceImportAccountTransaction(fn func(*gorm.DB) error) error {
	if DB.Dialector.Name() == "postgres" {
		return DB.Transaction(fn, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	}
	return marketTransaction(DB, fn)
}

func commerceImportConnectionLimit(tx *gorm.DB, actor int) error {
	if err := marketLockUsers(tx, actor); err != nil {
		return err
	}
	if _, err := storeUser(tx, actor, common.RoleCommonUser); err != nil {
		return err
	}
	var count int64
	if err := tx.Model(&MerchantStoreCommerceConnection{}).Where("seller_id = ? AND status IN ?", actor, []string{"pending", "active", "reauthorize"}).Count(&count).Error; err != nil {
		return err
	}
	if count >= 50 {
		return ErrMerchantStoreInput
	}
	return nil
}

func commerceImportScope(scope string) (string, bool) {
	seen := map[string]bool{}
	for _, value := range strings.Fields(scope) {
		if (value != "products.read" && value != "cards.issue") || seen[value] {
			return "", false
		}
		seen[value] = true
	}
	if len(seen) == 0 {
		return "", false
	}
	if seen["products.read"] && seen["cards.issue"] {
		return "products.read cards.issue", true
	}
	if seen["products.read"] {
		return "products.read", true
	}
	return "cards.issue", true
}

func commerceImportScopeSubset(scope, ceiling string) bool {
	if _, ok := commerceImportScope(scope); !ok {
		return false
	}
	if _, ok := commerceImportScope(ceiling); !ok {
		return false
	}
	allowed := map[string]bool{}
	for _, value := range strings.Fields(ceiling) {
		allowed[value] = true
	}
	for _, value := range strings.Fields(scope) {
		if !allowed[value] {
			return false
		}
	}
	return true
}

func commerceImportConnectionInputValid(in CommerceImportConnectionInput) bool {
	u, err := url.Parse(in.Issuer)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" &&
		len(in.Issuer) <= 512 && len(in.ClientID) > 0 && len(in.ClientID) <= 100 && storeURL(in.RedirectURI) &&
		len(in.MetadataJSON) <= 65536 && json.Valid([]byte(in.MetadataJSON)) && in.MaximumCardsPerRequest >= 1 && in.MaximumCardsPerRequest <= 100
}

func CreateCommerceImportConnection(actor int, in CommerceImportConnectionInput) (*MerchantStoreCommerceConnection, error) {
	if !commerceImportConnectionInputValid(in) {
		return nil, ErrMerchantStoreInput
	}
	var result MerchantStoreCommerceConnection
	err := commerceImportAccountTransaction(func(tx *gorm.DB) error {
		if err := storeRequireCommerceImportWriter(tx); err != nil {
			return err
		}
		if _, err := storeUser(tx, actor, common.RoleCommonUser); err != nil {
			return err
		}
		// Serialize the count and insert across application processes using the
		// seller's durable account row, rather than a process-local counter.
		if err := commerceImportConnectionLimit(tx, actor); err != nil {
			return err
		}
		now := common.GetTimestamp()
		result = MerchantStoreCommerceConnection{ID: uuid.NewString(), SellerID: actor, Issuer: in.Issuer, ClientID: in.ClientID, RedirectURI: in.RedirectURI,
			MetadataJSON: in.MetadataJSON, MaximumCardsPerRequest: in.MaximumCardsPerRequest, Status: "pending", CreatedAt: now, UpdatedAt: now}
		return tx.Create(&result).Error
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func ListCommerceImportConnections(actor int) ([]MerchantStoreCommerceConnection, error) {
	if err := storeRequireCommerceImportReadable(DB); err != nil {
		return nil, err
	}
	if _, err := storeUser(DB, actor, common.RoleCommonUser); err != nil {
		return nil, err
	}
	result := []MerchantStoreCommerceConnection{}
	err := DB.Where("seller_id = ?", actor).Order("created_at DESC, id ASC").Find(&result).Error
	return result, err
}

func GetCommerceImportConnection(actor int, id string) (*MerchantStoreCommerceConnection, error) {
	if err := storeRequireCommerceImportReadable(DB); err != nil {
		return nil, err
	}
	if _, err := storeUser(DB, actor, common.RoleCommonUser); err != nil {
		return nil, err
	}
	var result MerchantStoreCommerceConnection
	if err := DB.Where("id = ? AND seller_id = ?", id, actor).First(&result).Error; err != nil {
		return nil, err
	}
	return &result, nil
}

// Acquiring the database row in one conditional UPDATE provides a durable
// cross-process fence. No process-local mutex participates in ownership.
func commerceImportAcquireLease(tx *gorm.DB, actor int, id string, ttlSeconds int64) (*CommerceImportLease, error) {
	if id == "" || ttlSeconds < 1 || ttlSeconds > 3600 {
		return nil, ErrMerchantStoreInput
	}
	if err := storeRequireCommerceImportWriter(tx); err != nil {
		return nil, err
	}
	if _, err := storeUser(tx, actor, common.RoleCommonUser); err != nil {
		return nil, err
	}
	owner, err := storeToken()
	if err != nil {
		return nil, err
	}
	now := common.GetTimestamp()
	lease := CommerceImportLease{ConnectionID: id, Owner: owner, ExpiresAt: now + ttlSeconds}
	updated := tx.Model(&MerchantStoreCommerceConnection{}).Where("id = ? AND seller_id = ? AND (lease_owner = '' OR lease_expires_at <= ?)", id, actor, now).
		Updates(map[string]interface{}{"lease_owner": lease.Owner, "lease_expires_at": lease.ExpiresAt, "updated_at": now})
	if updated.Error != nil {
		return nil, updated.Error
	}
	if updated.RowsAffected != 1 {
		return nil, ErrCommerceImportLease
	}
	return &lease, nil
}

func AcquireCommerceImportLease(actor int, id string, ttlSeconds int64) (*CommerceImportLease, error) {
	var result *CommerceImportLease
	err := marketTransaction(DB, func(tx *gorm.DB) error {
		var err error
		result, err = commerceImportAcquireLease(tx, actor, id, ttlSeconds)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func commerceImportLeaseQuery(tx *gorm.DB, actor int, lease CommerceImportLease) *gorm.DB {
	return tx.Model(&MerchantStoreCommerceConnection{}).Where("id = ? AND seller_id = ? AND lease_owner = ? AND lease_expires_at = ? AND lease_expires_at > ?",
		lease.ConnectionID, actor, lease.Owner, lease.ExpiresAt, common.GetTimestamp())
}

// The conditional write both checks ownership and locks the row through the
// transaction. A late response from an expired owner cannot regain authority.
func commerceImportLeaseConnection(tx *gorm.DB, actor int, lease CommerceImportLease) (*MerchantStoreCommerceConnection, error) {
	if err := storeRequireCommerceImportWriter(tx); err != nil {
		return nil, err
	}
	if _, err := storeUser(tx, actor, common.RoleCommonUser); err != nil {
		return nil, err
	}
	if lease.ConnectionID == "" || lease.Owner == "" || lease.ExpiresAt <= common.GetTimestamp() {
		return nil, ErrCommerceImportLease
	}
	updated := commerceImportLeaseQuery(tx, actor, lease).Update("updated_at", common.GetTimestamp())
	if updated.Error != nil {
		return nil, updated.Error
	}
	if updated.RowsAffected != 1 {
		return nil, ErrCommerceImportLease
	}
	var result MerchantStoreCommerceConnection
	if err := tx.Where("id = ? AND seller_id = ?", lease.ConnectionID, actor).First(&result).Error; err != nil {
		return nil, err
	}
	return &result, nil
}

func commerceImportLeaseUpdate(tx *gorm.DB, actor int, lease CommerceImportLease, changes map[string]interface{}) error {
	changes["updated_at"] = common.GetTimestamp()
	updated := commerceImportLeaseQuery(tx, actor, lease).Updates(changes)
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return ErrCommerceImportLease
	}
	return nil
}

func ReleaseCommerceImportLease(actor int, lease CommerceImportLease) error {
	return marketTransaction(DB, func(tx *gorm.DB) error {
		if _, err := commerceImportLeaseConnection(tx, actor, lease); err != nil {
			return err
		}
		return commerceImportLeaseUpdate(tx, actor, lease, map[string]interface{}{"lease_owner": "", "lease_expires_at": int64(0)})
	})
}

func SaveCommerceImportSession(actor int, connectionID string, in CommerceImportSessionInput) (*MerchantStoreCommerceSession, error) {
	scope, ok := commerceImportScope(in.RequestedScope)
	if !ok || len(in.State) < 16 || len(in.State) > 512 || len(in.Verifier) < 43 || len(in.Verifier) > 128 || in.DashboardSessionID == "" || len(in.DashboardSessionID) > 512 ||
		in.ExpiresAt <= common.GetTimestamp() || in.ExpiresAt > common.GetTimestamp()+600 {
		return nil, ErrMerchantStoreInput
	}
	var result MerchantStoreCommerceSession
	err := marketTransaction(DB, func(tx *gorm.DB) error {
		lease, err := commerceImportAcquireLease(tx, actor, connectionID, commerceImportInternalLeaseSeconds)
		if err != nil {
			return err
		}
		result = MerchantStoreCommerceSession{ID: uuid.NewString(), ConnectionID: connectionID, SellerID: actor, StateHash: storeHash(in.State), Status: "pending", ExpiresAt: in.ExpiresAt, CreatedAt: common.GetTimestamp()}
		plain, err := json.Marshal(commerceImportSessionPayload{State: in.State, Verifier: in.Verifier, DashboardSessionID: in.DashboardSessionID, RequestedScope: scope})
		if err != nil {
			return err
		}
		result.SecretsCiphertext, err = storeEncrypt("commerce-session", result.ID, string(plain))
		if err != nil {
			return err
		}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		return commerceImportLeaseUpdate(tx, actor, *lease, map[string]interface{}{"lease_owner": "", "lease_expires_at": int64(0), "auth_session_id": result.ID})
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func commerceImportSessionSecrets(tx *gorm.DB, actor int, session MerchantStoreCommerceSession, dashboardID string) (*CommerceImportSessionSecrets, error) {
	if session.SellerID != actor || session.ExpiresAt <= common.GetTimestamp() || session.SecretsCiphertext == "" {
		return nil, ErrMerchantStoreDenied
	}
	plain, err := storeDecrypt("commerce-session", session.ID, session.SecretsCiphertext)
	if err != nil {
		return nil, err
	}
	var payload commerceImportSessionPayload
	if err := json.Unmarshal([]byte(plain), &payload); err != nil {
		return nil, err
	}
	if dashboardID == "" || payload.DashboardSessionID != dashboardID || storeHash(payload.State) != session.StateHash {
		return nil, ErrMerchantStoreDenied
	}
	var connection MerchantStoreCommerceConnection
	if err := tx.Where("id = ? AND seller_id = ?", session.ConnectionID, actor).First(&connection).Error; err != nil {
		return nil, err
	}
	if connection.AuthSessionID != session.ID {
		return nil, ErrMerchantStoreDenied
	}
	return &CommerceImportSessionSecrets{SessionID: session.ID, ConnectionID: connection.ID, Issuer: connection.Issuer, ClientID: connection.ClientID, RedirectURI: connection.RedirectURI,
		State: payload.State, Verifier: payload.Verifier, DashboardSessionID: payload.DashboardSessionID, RequestedScope: payload.RequestedScope}, nil
}

func GetCommerceImportSession(actor int, state, dashboardID string) (*CommerceImportSessionSecrets, error) {
	if err := storeRequireCommerceImportReadable(DB); err != nil {
		return nil, err
	}
	if _, err := storeUser(DB, actor, common.RoleCommonUser); err != nil {
		return nil, err
	}
	var session MerchantStoreCommerceSession
	if err := DB.Where("seller_id = ? AND state_hash = ? AND status = ? AND expires_at > ?", actor, storeHash(state), "pending", common.GetTimestamp()).First(&session).Error; err != nil {
		return nil, err
	}
	return commerceImportSessionSecrets(DB, actor, session, dashboardID)
}

// Commit the attempted marker before the caller exchanges the single-use code.
// An interrupted exchange is deliberately not eligible for another attempt.
func ConsumeCommerceImportSession(actor int, sessionID, dashboardID string) (*CommerceImportSessionSecrets, error) {
	return commerceImportConsumeSession(actor, nil, sessionID, dashboardID)
}

func ConsumeCommerceImportSessionWithLease(actor int, lease CommerceImportLease, sessionID, dashboardID string) (*CommerceImportSessionSecrets, error) {
	return commerceImportConsumeSession(actor, &lease, sessionID, dashboardID)
}

func commerceImportSessionLease(tx *gorm.DB, actor int, connectionID string, held *CommerceImportLease) (*CommerceImportLease, error) {
	if held == nil {
		return commerceImportAcquireLease(tx, actor, connectionID, commerceImportInternalLeaseSeconds)
	}
	if held.ConnectionID != connectionID {
		return nil, ErrCommerceImportLease
	}
	if _, err := commerceImportLeaseConnection(tx, actor, *held); err != nil {
		return nil, err
	}
	return held, nil
}

func commerceImportConsumeSession(actor int, held *CommerceImportLease, sessionID, dashboardID string) (*CommerceImportSessionSecrets, error) {
	var result *CommerceImportSessionSecrets
	err := marketTransaction(DB, func(tx *gorm.DB) error {
		if err := storeRequireCommerceImportWriter(tx); err != nil {
			return err
		}
		if _, err := storeUser(tx, actor, common.RoleCommonUser); err != nil {
			return err
		}
		var session MerchantStoreCommerceSession
		if err := tx.Where("id = ? AND seller_id = ? AND status = ? AND expires_at > ?", sessionID, actor, "pending", common.GetTimestamp()).First(&session).Error; err != nil {
			return err
		}
		lease, err := commerceImportSessionLease(tx, actor, session.ConnectionID, held)
		if err != nil {
			return err
		}
		result, err = commerceImportSessionSecrets(tx, actor, session, dashboardID)
		if err != nil {
			return err
		}
		updated := tx.Model(&MerchantStoreCommerceSession{}).Where("id = ? AND seller_id = ? AND connection_id = ? AND status = ? AND expires_at > ?", session.ID, actor, lease.ConnectionID, "pending", common.GetTimestamp()).Update("status", "attempted")
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrMerchantStoreConflict
		}
		changes := map[string]interface{}{}
		if held == nil {
			changes["lease_owner"], changes["lease_expires_at"] = "", int64(0)
		}
		return commerceImportLeaseUpdate(tx, actor, *lease, changes)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func commerceImportTokenValid(in CommerceImportTokenInput) bool {
	_, ok := commerceImportScope(in.Scope)
	now := common.GetTimestamp()
	return ok && in.AccessToken != "" && len(in.AccessToken) <= 16384 && in.RefreshToken != "" && len(in.RefreshToken) <= 16384 && in.GrantID != "" && len(in.GrantID) <= 100 &&
		in.AccessExpiresAt > now && in.GrantExpiresAt > now && in.AccessExpiresAt <= in.GrantExpiresAt
}

func commerceImportEncryptTokens(id string, in CommerceImportTokenInput) (string, error) {
	plain, err := json.Marshal(commerceImportTokenPayload{AccessToken: in.AccessToken, RefreshToken: in.RefreshToken, Scope: in.Scope, GrantID: in.GrantID, AccessExpiresAt: in.AccessExpiresAt, GrantExpiresAt: in.GrantExpiresAt})
	if err != nil {
		return "", err
	}
	return storeEncrypt("commerce-tokens", id, string(plain))
}

func commerceImportTokenChanges(ciphertext string, in CommerceImportTokenInput, version int64) map[string]interface{} {
	scope, _ := commerceImportScope(in.Scope)
	return map[string]interface{}{"tokens_ciphertext": ciphertext, "scope": scope, "grant_id": in.GrantID, "access_expires_at": in.AccessExpiresAt,
		"grant_expires_at": in.GrantExpiresAt, "token_version": version + 1, "refresh_attempt_at": int64(0), "status": "active"}
}

func CompleteCommerceImportAuthorization(actor int, sessionID string, in CommerceImportTokenInput) (*MerchantStoreCommerceConnection, error) {
	return commerceImportCompleteAuthorization(actor, nil, sessionID, in)
}

func CompleteCommerceImportAuthorizationWithLease(actor int, lease CommerceImportLease, sessionID string, in CommerceImportTokenInput) (*MerchantStoreCommerceConnection, error) {
	return commerceImportCompleteAuthorization(actor, &lease, sessionID, in)
}

func commerceImportCompleteAuthorization(actor int, held *CommerceImportLease, sessionID string, in CommerceImportTokenInput) (*MerchantStoreCommerceConnection, error) {
	if !commerceImportTokenValid(in) {
		return nil, ErrMerchantStoreInput
	}
	var result MerchantStoreCommerceConnection
	err := commerceImportAccountTransaction(func(tx *gorm.DB) error {
		if err := storeRequireCommerceImportWriter(tx); err != nil {
			return err
		}
		if _, err := storeUser(tx, actor, common.RoleCommonUser); err != nil {
			return err
		}
		var session MerchantStoreCommerceSession
		if err := tx.Where("id = ? AND seller_id = ? AND status = ? AND expires_at > ?", sessionID, actor, "attempted", common.GetTimestamp()).First(&session).Error; err != nil {
			return err
		}
		lease, err := commerceImportSessionLease(tx, actor, session.ConnectionID, held)
		if err != nil {
			return err
		}
		connection, err := commerceImportLeaseConnection(tx, actor, *lease)
		if err != nil {
			return err
		}
		if connection.AuthSessionID != session.ID {
			return ErrMerchantStoreDenied
		}
		plain, err := storeDecrypt("commerce-session", session.ID, session.SecretsCiphertext)
		if err != nil {
			return err
		}
		var payload commerceImportSessionPayload
		if err := json.Unmarshal([]byte(plain), &payload); err != nil {
			return err
		}
		if !commerceImportScopeSubset(in.Scope, payload.RequestedScope) {
			return ErrMerchantStoreDenied
		}
		if connection.Status == "disconnected" {
			if err := commerceImportConnectionLimit(tx, actor); err != nil {
				return err
			}
		}
		ciphertext, err := commerceImportEncryptTokens(connection.ID, in)
		if err != nil {
			return err
		}
		updated := tx.Model(&MerchantStoreCommerceSession{}).Where("id = ? AND seller_id = ? AND connection_id = ? AND status = ? AND expires_at > ?", session.ID, actor, connection.ID, "attempted", common.GetTimestamp()).Updates(map[string]interface{}{"status": "completed", "secrets_ciphertext": ""})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrMerchantStoreConflict
		}
		changes := commerceImportTokenChanges(ciphertext, in, connection.TokenVersion)
		changes["auth_session_id"] = ""
		if held == nil {
			changes["lease_owner"], changes["lease_expires_at"] = "", int64(0)
		}
		if err := commerceImportLeaseUpdate(tx, actor, *lease, changes); err != nil {
			return err
		}
		return tx.Where("id = ? AND seller_id = ?", connection.ID, actor).First(&result).Error
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func FinishCommerceImportDenied(actor int, sessionID string) error {
	return commerceImportFinishDenied(actor, nil, sessionID)
}

func FinishCommerceImportDeniedWithLease(actor int, lease CommerceImportLease, sessionID string) error {
	return commerceImportFinishDenied(actor, &lease, sessionID)
}

func commerceImportFinishDenied(actor int, held *CommerceImportLease, sessionID string) error {
	return marketTransaction(DB, func(tx *gorm.DB) error {
		if err := storeRequireCommerceImportWriter(tx); err != nil {
			return err
		}
		if _, err := storeUser(tx, actor, common.RoleCommonUser); err != nil {
			return err
		}
		var session MerchantStoreCommerceSession
		if err := tx.Where("id = ? AND seller_id = ? AND status IN ? AND expires_at > ?", sessionID, actor, []string{"pending", "attempted"}, common.GetTimestamp()).First(&session).Error; err != nil {
			return err
		}
		lease, err := commerceImportSessionLease(tx, actor, session.ConnectionID, held)
		if err != nil {
			return err
		}
		connection, err := commerceImportLeaseConnection(tx, actor, *lease)
		if err != nil {
			return err
		}
		if connection.AuthSessionID != session.ID {
			return ErrMerchantStoreDenied
		}
		updated := tx.Model(&MerchantStoreCommerceSession{}).Where("id = ? AND seller_id = ? AND connection_id = ? AND status IN ? AND expires_at > ?", session.ID, actor, connection.ID, []string{"pending", "attempted"}, common.GetTimestamp()).Updates(map[string]interface{}{"status": "denied", "secrets_ciphertext": ""})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrMerchantStoreConflict
		}
		changes := map[string]interface{}{"auth_session_id": ""}
		if held == nil {
			changes["lease_owner"], changes["lease_expires_at"] = "", int64(0)
		}
		return commerceImportLeaseUpdate(tx, actor, *lease, changes)
	})
}

func commerceImportDecryptTokens(connection *MerchantStoreCommerceConnection) (*CommerceImportTokenInput, error) {
	if connection.Status != "active" || connection.RefreshAttemptAt != 0 || connection.TokensCiphertext == "" || connection.GrantExpiresAt <= common.GetTimestamp() {
		return nil, ErrCommerceImportReauthorize
	}
	plain, err := storeDecrypt("commerce-tokens", connection.ID, connection.TokensCiphertext)
	if err != nil {
		return nil, err
	}
	var payload commerceImportTokenPayload
	if err := json.Unmarshal([]byte(plain), &payload); err != nil {
		return nil, err
	}
	if payload.AccessToken == "" || payload.RefreshToken == "" || payload.GrantID != connection.GrantID || !commerceImportScopeSubset(payload.Scope, connection.Scope) ||
		payload.AccessExpiresAt != connection.AccessExpiresAt || payload.GrantExpiresAt != connection.GrantExpiresAt {
		return nil, ErrCommerceImportReauthorize
	}
	return &CommerceImportTokenInput{AccessToken: payload.AccessToken, RefreshToken: payload.RefreshToken, Scope: payload.Scope, GrantID: payload.GrantID, AccessExpiresAt: payload.AccessExpiresAt, GrantExpiresAt: payload.GrantExpiresAt}, nil
}

func LoadCommerceImportTokens(actor int, lease CommerceImportLease) (*CommerceImportTokenInput, error) {
	var result *CommerceImportTokenInput
	err := marketTransaction(DB, func(tx *gorm.DB) error {
		connection, err := commerceImportLeaseConnection(tx, actor, lease)
		if err != nil {
			return err
		}
		result, err = commerceImportDecryptTokens(connection)
		if err != nil {
			return err
		}
		return commerceImportLeaseUpdate(tx, actor, lease, map[string]interface{}{})
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Rotation is single-use. Persist the uncertainty fence before returning the
// refresh credential; only a validated response can restore connected status.
func BeginCommerceImportRefresh(actor int, lease CommerceImportLease) (*CommerceImportTokenInput, error) {
	var result *CommerceImportTokenInput
	err := marketTransaction(DB, func(tx *gorm.DB) error {
		connection, err := commerceImportLeaseConnection(tx, actor, lease)
		if err != nil {
			return err
		}
		result, err = commerceImportDecryptTokens(connection)
		if err != nil {
			return err
		}
		return commerceImportLeaseUpdate(tx, actor, lease, map[string]interface{}{"status": "reauthorize", "refresh_attempt_at": common.GetTimestamp()})
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func CompleteCommerceImportRefresh(actor int, lease CommerceImportLease, in CommerceImportTokenInput) error {
	if !commerceImportTokenValid(in) {
		return ErrMerchantStoreInput
	}
	return marketTransaction(DB, func(tx *gorm.DB) error {
		connection, err := commerceImportLeaseConnection(tx, actor, lease)
		if err != nil {
			return err
		}
		if connection.Status != "reauthorize" || connection.RefreshAttemptAt == 0 || connection.TokensCiphertext == "" || in.GrantID != connection.GrantID ||
			!commerceImportScopeSubset(in.Scope, connection.Scope) || in.GrantExpiresAt > connection.GrantExpiresAt {
			return ErrMerchantStoreDenied
		}
		ciphertext, err := commerceImportEncryptTokens(connection.ID, in)
		if err != nil {
			return err
		}
		return commerceImportLeaseUpdate(tx, actor, lease, commerceImportTokenChanges(ciphertext, in, connection.TokenVersion))
	})
}

func BindCommerceImportShop(actor int, lease CommerceImportLease, grantID, shopID, shopName string) error {
	if grantID == "" || len(grantID) > 100 || shopID == "" || len(shopID) > 100 || len(shopName) > 200 {
		return ErrMerchantStoreInput
	}
	return marketTransaction(DB, func(tx *gorm.DB) error {
		connection, err := commerceImportLeaseConnection(tx, actor, lease)
		if err != nil {
			return err
		}
		if connection.Status != "active" || connection.GrantID != grantID || (connection.ShopID != "" && connection.ShopID != shopID) {
			return ErrMerchantStoreDenied
		}
		return commerceImportLeaseUpdate(tx, actor, lease, map[string]interface{}{"shop_id": shopID, "shop_name": shopName})
	})
}

func DisconnectCommerceImportConnection(actor int, lease CommerceImportLease) error {
	return marketTransaction(DB, func(tx *gorm.DB) error {
		if _, err := commerceImportLeaseConnection(tx, actor, lease); err != nil {
			return err
		}
		if err := tx.Where("connection_id = ? AND seller_id = ?", lease.ConnectionID, actor).Delete(&MerchantStoreCommerceSession{}).Error; err != nil {
			return err
		}
		// Keep batch identity/hash and request rows as duplicate-issuance tombstones.
		if err := tx.Model(&MerchantStoreCommerceCardBatch{}).Where("connection_id = ?", lease.ConnectionID).Updates(map[string]interface{}{"response_ciphertext": "", "recovery_expires_at": int64(0)}).Error; err != nil {
			return err
		}
		if err := tx.Model(&MerchantStoreCommerceRestockRequest{}).Where("connection_id = ? AND seller_id = ?", lease.ConnectionID, actor).Updates(map[string]interface{}{"response_ciphertext": "", "recovery_expires_at": int64(0)}).Error; err != nil {
			return err
		}
		return commerceImportLeaseUpdate(tx, actor, lease, map[string]interface{}{"tokens_ciphertext": "", "access_expires_at": int64(0), "grant_expires_at": int64(0),
			"refresh_attempt_at": int64(0), "token_version": gorm.Expr("token_version + 1"), "status": "disconnected", "auth_session_id": ""})
	})
}
