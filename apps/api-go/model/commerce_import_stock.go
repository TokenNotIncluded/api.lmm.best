package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func commerceImportRequestBody(in CommerceImportRestockInput) bool {
	if len(in.Body) == 0 || len(in.Body) > 8192 || len(in.IdempotencyKey) < 8 || len(in.IdempotencyKey) > 200 || in.Count < 1 || in.Count > 100 || !commerceImportRevision.MatchString(in.Revision) {
		return false
	}
	for _, r := range in.IdempotencyKey {
		if r < 33 || r > 126 {
			return false
		}
	}
	if utf8.RuneCountInString(in.Label) > 100 {
		return false
	}
	for _, r := range in.Label {
		if r < 32 || r == 127 {
			return false
		}
	}
	var body struct {
		ProductID string   `json:"product_id"`
		VariantID string   `json:"variant_id"`
		Count     int      `json:"count"`
		Label     string   `json:"label,omitempty"`
		Revision  string   `json:"expected_revision"`
		Expires   *float64 `json:"expires,omitempty"`
	}
	d := json.NewDecoder(bytes.NewBufferString(in.Body))
	d.DisallowUnknownFields()
	if d.Decode(&body) != nil || d.Decode(new(any)) != io.EOF {
		return false
	}
	return body.ProductID == in.ExternalProductID && body.VariantID == in.ExternalVariantID && body.Count == in.Count && body.Label == in.Label && body.Revision == in.Revision && (body.Expires == nil || (!math.IsInf(*body.Expires, 0) && !math.IsNaN(*body.Expires) && *body.Expires > 0))
}

func commerceImportDecryptRequest(row *MerchantStoreCommerceRestockRequest) error {
	var err error
	row.Body, err = storeDecrypt("commerce-request-body", row.ID, row.BodyCiphertext)
	if err != nil {
		return err
	}
	row.IdempotencyKey, err = storeDecrypt("commerce-request-key", row.ID, row.KeyCiphertext)
	return err
}

func commerceImportRequest(tx *gorm.DB, actor int, conn *MerchantStoreCommerceConnection, id string) (*MerchantStoreCommerceRestockRequest, error) {
	var row MerchantStoreCommerceRestockRequest
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND seller_id = ? AND connection_id = ?", id, actor, conn.ID).First(&row).Error; err != nil {
		return nil, err
	}
	if row.GrantID != conn.GrantID {
		return nil, ErrCommerceImportReauthorize
	}
	if err := commerceImportDecryptRequest(&row); err != nil {
		return nil, err
	}
	return &row, nil
}

func PrepareCommerceImportRestock(actor int, lease CommerceImportLease, in CommerceImportRestockInput) (*MerchantStoreCommerceRestockRequest, bool, error) {
	if !commerceImportRequestBody(in) {
		return nil, false, ErrMerchantStoreInput
	}
	var result MerchantStoreCommerceRestockRequest
	created := false
	err := marketTransaction(DB, func(tx *gorm.DB) error {
		conn, err := commerceImportLeaseConnection(tx, actor, lease)
		if err != nil {
			return err
		}
		if conn.Status != "active" || conn.GrantExpiresAt <= common.GetTimestamp() || !commerceImportHasScope(conn.Scope, "cards.issue") {
			return ErrCommerceImportReauthorize
		}
		// Never reuse an old grant's key under a new authorization: a server
		// would treat that as a fresh issuance, rather than recovery.
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("connection_id = ? AND key_hash = ?", conn.ID, storeHash(in.IdempotencyKey)).First(&result).Error
		if err == nil {
			if result.GrantID != conn.GrantID {
				return ErrCommerceImportReauthorize
			}
			if result.BodyHash != storeHash(in.Body) || result.ExternalProductID != in.ExternalProductID || result.ExternalVariantID != in.ExternalVariantID || result.Count != in.Count || result.Revision != in.Revision {
				return ErrMerchantStoreConflict
			}
			return commerceImportDecryptRequest(&result)
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		// Any unresolved issuance of this SKU must be recovered or reconciled
		// before creating a new key, including after reauthorization.
		var unresolved int64
		if err := tx.Model(&MerchantStoreCommerceRestockRequest{}).Where("seller_id = ? AND external_product_id = ? AND external_variant_id = ? AND product_mapping_id IN (?) AND status IN ?", actor, in.ExternalProductID, in.ExternalVariantID, tx.Model(&MerchantStoreCommerceProductMapping{}).Select("id").Where("seller_id = ? AND issuer = ? AND shop_id = ?", actor, conn.Issuer, conn.ShopID), []string{"pending", "received", "manual_recovery"}).Where("NOT (status = ? AND issuance_uncertain = ? AND error_code IN ?)", "manual_recovery", false, commerceImportNoIssuanceErrors).Count(&unresolved).Error; err != nil {
			return err
		}
		if unresolved != 0 {
			return ErrMerchantStoreConflict
		}
		var mapped MerchantStoreCommerceProductMapping
		if err := tx.Where("seller_id = ? AND issuer = ? AND shop_id = ? AND external_product_id = ? AND connection_id = ?", actor, conn.Issuer, conn.ShopID, in.ExternalProductID, conn.ID).First(&mapped).Error; err != nil {
			return err
		}
		if mapped.Revision != in.Revision {
			return ErrMerchantStoreConflict
		}
		if mapped.Mode == "stock" {
			return ErrMerchantStoreUnavailable
		}
		p, err := storeProductOwner(tx, actor, mapped.LocalProductID)
		if err != nil {
			return err
		}
		if p.Status == "paused" || p.Status == "off_shelf" || p.Status == "unlisted" {
			return ErrMerchantStoreUnavailable
		}
		var vm MerchantStoreCommerceVariantMapping
		if err := tx.Where("product_mapping_id = ? AND external_id = ?", mapped.ID, in.ExternalVariantID).First(&vm).Error; err != nil {
			return err
		}
		variant, err := storeVariant(tx, p, vm.LocalVariantID)
		if err != nil {
			return err
		}
		if !vm.Enabled || !variant.Enabled || variant.Template == MerchantStoreFixedContentTemplate {
			return ErrMerchantStoreUnavailable
		}
		maximum := conn.MaximumCardsPerRequest
		if maximum < 1 || maximum > 100 || in.Count > maximum {
			return ErrMerchantStoreInput
		}
		now := common.GetTimestamp()
		result = MerchantStoreCommerceRestockRequest{ID: uuid.NewString(), ConnectionID: conn.ID, SellerID: actor, GrantID: conn.GrantID, GrantExpiresAt: conn.GrantExpiresAt, IdempotencyKey: in.IdempotencyKey, KeyHash: storeHash(in.IdempotencyKey), Body: in.Body, BodyHash: storeHash(in.Body), ProductMappingID: mapped.ID, VariantMappingID: vm.ID, ProductID: p.ID, VariantID: variant.ID, ExternalProductID: in.ExternalProductID, ExternalVariantID: in.ExternalVariantID, Revision: in.Revision, Count: in.Count, Label: in.Label, Status: "pending", CreatedAt: now, UpdatedAt: now}
		result.BodyCiphertext, err = storeEncrypt("commerce-request-body", result.ID, in.Body)
		if err != nil {
			return err
		}
		result.KeyCiphertext, err = storeEncrypt("commerce-request-key", result.ID, in.IdempotencyKey)
		if err != nil {
			return err
		}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		created = true
		return nil
	})
	return &result, created, err
}

// BeginCommerceImportIssue commits an attempt marker before any network call.
// Only the first definitive rejection can prove that no issuance occurred;
// rejection during recovery cannot erase an earlier uncertain attempt.
func BeginCommerceImportIssue(actor int, lease CommerceImportLease, requestID string) (*MerchantStoreCommerceRestockRequest, error) {
	var result *MerchantStoreCommerceRestockRequest
	err := marketTransaction(DB, func(tx *gorm.DB) error {
		conn, err := commerceImportLeaseConnection(tx, actor, lease)
		if err != nil {
			return err
		}
		if conn.Status != "active" || conn.GrantExpiresAt <= common.GetTimestamp() || !commerceImportHasScope(conn.Scope, "cards.issue") {
			return ErrCommerceImportReauthorize
		}
		row, err := commerceImportRequest(tx, actor, conn, requestID)
		if err != nil {
			return err
		}
		if row.Status != "pending" || row.ResponseHash != "" || row.IssueAttempts >= 10000 {
			return ErrMerchantStoreConflict
		}
		row.IssueAttempts++
		row.IssuanceUncertain = true
		row.UpdatedAt = common.GetTimestamp()
		if err := tx.Save(row).Error; err != nil {
			return err
		}
		result = row
		return nil
	})
	return result, err
}

func LoadCommerceImportRestock(actor int, lease CommerceImportLease, requestID string) (*MerchantStoreCommerceRestockRequest, error) {
	var result *MerchantStoreCommerceRestockRequest
	err := marketTransaction(DB, func(tx *gorm.DB) error {
		conn, err := commerceImportLeaseConnection(tx, actor, lease)
		if err != nil {
			return err
		}
		if conn.Status != "active" || conn.GrantExpiresAt <= common.GetTimestamp() || !commerceImportHasScope(conn.Scope, "cards.issue") {
			return ErrCommerceImportReauthorize
		}
		result, err = commerceImportRequest(tx, actor, conn, requestID)
		return err
	})
	return result, err
}
func ListCommerceImportRestockRequests(actor int, connectionID string) ([]MerchantStoreCommerceRestockRequest, error) {
	if _, err := GetCommerceImportConnection(actor, connectionID); err != nil {
		return nil, err
	}
	var rows []MerchantStoreCommerceRestockRequest
	err := DB.Where("seller_id = ? AND connection_id = ?", actor, connectionID).Order("created_at DESC,id DESC").Limit(200).Find(&rows).Error
	return rows, err
}
func GetCommerceImportRestockRequest(actor int, connectionID, requestID string) (*MerchantStoreCommerceRestockRequest, error) {
	if _, err := GetCommerceImportConnection(actor, connectionID); err != nil {
		return nil, err
	}
	var row MerchantStoreCommerceRestockRequest
	err := DB.Where("id = ? AND seller_id = ? AND connection_id = ?", requestID, actor, connectionID).First(&row).Error
	return &row, err
}

func commerceImportBatchHash(in CommerceImportBatchInput) (string, bool) {
	if !commerceImportIdentity(in.BatchID) || !commerceImportIdentity(in.GrantID) || in.Count < 1 || in.Count > 100 || len(in.Codes) != in.Count || len(in.RawJSON) == 0 || len(in.RawJSON) > 2<<20 || !json.Valid([]byte(in.RawJSON)) || in.RecoveryExpiresAt <= 0 {
		return "", false
	}
	var wire struct {
		Schema          string   `json:"schema"`
		GrantID         string   `json:"grant_id"`
		ProductID       string   `json:"product_id"`
		VariantID       string   `json:"variant_id"`
		BatchID         string   `json:"batch_id"`
		Count           int      `json:"count"`
		Codes           []string `json:"codes"`
		RecoveryExpires float64  `json:"recovery_expires"`
	}
	// Response extensions are case sensitive. encoding/json otherwise lets
	// an additive "CODES" or "COUNT" field overwrite the canonical value.
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(in.RawJSON), &object) != nil {
		return "", false
	}
	canonical := make(map[string]json.RawMessage, 8)
	for _, key := range []string{"schema", "grant_id", "product_id", "variant_id", "batch_id", "count", "codes", "recovery_expires"} {
		if value, exists := object[key]; exists {
			canonical[key] = value
		}
	}
	projected, err := json.Marshal(canonical)
	if err != nil {
		return "", false
	}
	if json.Unmarshal(projected, &wire) != nil || wire.Schema != "extore.card-batch.v1" || wire.GrantID != in.GrantID || wire.ProductID != in.ExternalProductID || wire.VariantID != in.ExternalVariantID || wire.BatchID != in.BatchID || wire.Count != in.Count || len(wire.Codes) != len(in.Codes) || math.IsNaN(wire.RecoveryExpires) || math.IsInf(wire.RecoveryExpires, 0) || int64(wire.RecoveryExpires) != in.RecoveryExpiresAt {
		return "", false
	}
	for i := range wire.Codes {
		if wire.Codes[i] != in.Codes[i] {
			return "", false
		}
	}
	seen := map[string]bool{}
	for _, code := range in.Codes {
		if len(strings.TrimSpace(code)) == 0 || len(code) > 32768 || seen[code] {
			return "", false
		}
		seen[code] = true
	}
	// Hash only validated issuance semantics. Whitespace, numeric spelling
	// and additive fields do not change a batch's durable identity.
	semantics, err := json.Marshal(wire)
	if err != nil {
		return "", false
	}
	return storeHash(string(semantics)), true
}

func commerceImportValidateBatch(in CommerceImportBatchInput) bool {
	_, valid := commerceImportBatchHash(in)
	return valid
}

// First retain the encrypted receipt on the request. The second transaction
// commits the unique batch, card stock and imported state atomically. A crash
// or stock-validation failure leaves a recoverable receipt, never extra stock.
func ReceiveCommerceImportBatch(actor int, lease CommerceImportLease, requestID string, in CommerceImportBatchInput) (*MerchantStoreCommerceRestockRequest, bool, error) {
	hash, valid := commerceImportBatchHash(in)
	if !valid {
		return nil, false, ErrMerchantStoreInput
	}
	var result *MerchantStoreCommerceRestockRequest
	err := marketTransaction(DB, func(tx *gorm.DB) error {
		conn, err := commerceImportLeaseConnection(tx, actor, lease)
		if err != nil {
			return err
		}
		if conn.Status != "active" || conn.GrantExpiresAt <= common.GetTimestamp() || !commerceImportHasScope(conn.Scope, "cards.issue") {
			return ErrCommerceImportReauthorize
		}
		row, err := commerceImportRequest(tx, actor, conn, requestID)
		if err != nil {
			return err
		}
		result = row
		if row.GrantID != in.GrantID || row.ExternalProductID != in.ExternalProductID || row.ExternalVariantID != in.ExternalVariantID || row.Count != in.Count {
			return ErrMerchantStoreConflict
		}
		if row.ResponseHash != "" {
			if row.ResponseHash != hash || row.BatchID != in.BatchID {
				return ErrMerchantStoreConflict
			}
			return nil
		}
		cipher, err := storeEncrypt("commerce-response", row.ID, in.RawJSON)
		if err != nil {
			return err
		}
		row.IssuanceUncertain = true
		row.Status, row.BatchID, row.ResponseCiphertext, row.ResponseHash, row.RecoveryExpiresAt, row.UpdatedAt = "received", in.BatchID, cipher, hash, in.RecoveryExpiresAt, common.GetTimestamp()
		return tx.Save(row).Error
	})
	if err != nil {
		return result, false, err
	}
	created := false
	err = marketTransaction(DB, func(tx *gorm.DB) error {
		conn, err := commerceImportLeaseConnection(tx, actor, lease)
		if err != nil {
			return err
		}
		if conn.Status != "active" || conn.GrantExpiresAt <= common.GetTimestamp() || !commerceImportHasScope(conn.Scope, "cards.issue") {
			return ErrCommerceImportReauthorize
		}
		row, err := commerceImportRequest(tx, actor, conn, requestID)
		if err != nil {
			return err
		}
		result = row
		var batch MerchantStoreCommerceCardBatch
		err = tx.Where("connection_id = ? AND grant_id = ? AND batch_id = ?", conn.ID, in.GrantID, in.BatchID).First(&batch).Error
		if err == nil {
			if batch.RequestID != row.ID || batch.ResponseHash != hash || row.Status != "imported" {
				return ErrMerchantStoreConflict
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if row.Status == "imported" {
			return ErrMerchantStoreConflict
		}
		var mapped MerchantStoreCommerceProductMapping
		if err := tx.Where("id = ? AND seller_id = ? AND issuer = ? AND shop_id = ? AND local_product_id = ?", row.ProductMappingID, actor, conn.Issuer, conn.ShopID, row.ProductID).First(&mapped).Error; err != nil {
			return err
		}
		var vm MerchantStoreCommerceVariantMapping
		if err := tx.Where("id = ? AND product_mapping_id = ? AND local_variant_id = ? AND external_id = ?", row.VariantMappingID, mapped.ID, row.VariantID, row.ExternalVariantID).First(&vm).Error; err != nil {
			return err
		}
		p, err := storeProductOwner(tx, actor, row.ProductID)
		if err != nil {
			return err
		}
		if p.Status == "paused" || p.Status == "off_shelf" || p.Status == "unlisted" {
			return ErrMerchantStoreUnavailable
		}
		variant, err := storeVariant(tx, p, row.VariantID)
		if err != nil {
			return err
		}
		if !vm.Enabled || !variant.Enabled {
			return ErrMerchantStoreUnavailable
		}
		if _, err := storeAddVariantStock(tx, actor, p, row.VariantID, in.Codes); err != nil {
			return err
		}
		batch = MerchantStoreCommerceCardBatch{ID: uuid.NewString(), ConnectionID: conn.ID, GrantID: in.GrantID, GrantExpiresAt: row.GrantExpiresAt, BatchID: in.BatchID, RequestID: row.ID, ResponseCiphertext: row.ResponseCiphertext, ResponseHash: hash, RecoveryExpiresAt: in.RecoveryExpiresAt, Count: in.Count, CreatedAt: common.GetTimestamp()}
		if err := tx.Create(&batch).Error; err != nil {
			return err
		}
		row.Status, row.ErrorCode, row.UpdatedAt = "imported", "", common.GetTimestamp()
		if err := tx.Save(row).Error; err != nil {
			return err
		}
		created = true
		return nil
	})
	return result, created, err
}

func LoadCommerceImportBatchResponse(actor int, lease CommerceImportLease, requestID string) (string, error) {
	var result string
	err := marketTransaction(DB, func(tx *gorm.DB) error {
		conn, err := commerceImportLeaseConnection(tx, actor, lease)
		if err != nil {
			return err
		}
		if conn.Status != "active" || conn.GrantExpiresAt <= common.GetTimestamp() {
			return ErrCommerceImportReauthorize
		}
		row, err := commerceImportRequest(tx, actor, conn, requestID)
		if err != nil {
			return err
		}
		if row.ResponseHash == "" {
			return nil
		}
		if row.ResponseCiphertext == "" || row.RecoveryExpiresAt <= common.GetTimestamp() {
			return ErrMerchantStoreConflict
		}
		result, err = storeDecrypt("commerce-response", row.ID, row.ResponseCiphertext)
		return err
	})
	return result, err
}
func SetCommerceImportRestockRecovery(actor int, lease CommerceImportLease, requestID, errorCode string) error {
	if len(errorCode) == 0 || len(errorCode) > 64 {
		return ErrMerchantStoreInput
	}
	for _, r := range errorCode {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_') {
			return ErrMerchantStoreInput
		}
	}
	return marketTransaction(DB, func(tx *gorm.DB) error {
		conn, err := commerceImportLeaseConnection(tx, actor, lease)
		if err != nil {
			return err
		}
		row, err := commerceImportRequest(tx, actor, conn, requestID)
		if err != nil {
			return err
		}
		if row.Status == "imported" {
			return nil
		}
		status := "manual_recovery"
		switch errorCode {
		case "network_error", "server_error", "rate_limited":
			status = "pending"
			if row.ResponseCiphertext != "" {
				status = "received"
			}
		case "inventory_recovery_required":
			if row.ResponseCiphertext != "" {
				status = "received"
			}
		}
		uncertain := row.IssuanceUncertain
		if commerceImportErrorMeansNoIssuance(errorCode) && row.IssueAttempts <= 1 && row.ErrorCode == "" && row.ResponseHash == "" {
			uncertain = false
		}
		if !commerceImportErrorMeansNoIssuance(errorCode) {
			uncertain = true
		}
		return tx.Model(row).Updates(map[string]any{"status": status, "error_code": errorCode, "issuance_uncertain": uncertain, "updated_at": common.GetTimestamp()}).Error
	})
}

var commerceImportNoIssuanceErrors = []string{"quota_exceeded", "catalog_changed", "product_unavailable", "variant_unavailable", "unsupported_product", "review_changed", "insufficient_scope", "invalid_request", "invalid_scope", "access_denied", "invalid_token", "invalid_grant"}

func commerceImportErrorMeansNoIssuance(code string) bool {
	for _, allowed := range commerceImportNoIssuanceErrors {
		if code == allowed {
			return true
		}
	}
	return false
}

func ClearExpiredCommerceImportSecrets(limit int) error {
	if limit < 1 || limit > 200 {
		return ErrMerchantStoreInput
	}
	return marketTransaction(DB, func(tx *gorm.DB) error {
		if err := storeRequireCommerceImportWriter(tx); err != nil {
			return err
		}
		now := common.GetTimestamp()
		var ids []string
		if err := tx.Model(&MerchantStoreCommerceSession{}).Where("expires_at <= ?", now).Order("expires_at ASC").Limit(limit).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) > 0 {
			if err := tx.Where("id IN ?", ids).Delete(&MerchantStoreCommerceSession{}).Error; err != nil {
				return err
			}
		}
		ids = nil
		if err := tx.Model(&MerchantStoreCommerceRestockRequest{}).Where("recovery_expires_at > 0 AND recovery_expires_at <= ? AND response_ciphertext <> ''", now).Order("recovery_expires_at ASC").Limit(limit).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) > 0 {
			if err := tx.Model(&MerchantStoreCommerceRestockRequest{}).Where("id IN ? AND status <> ?", ids, "imported").Updates(map[string]any{"status": "manual_recovery", "error_code": "issuance_expired", "updated_at": now}).Error; err != nil {
				return err
			}
			if err := tx.Model(&MerchantStoreCommerceRestockRequest{}).Where("id IN ?", ids).Update("response_ciphertext", "").Error; err != nil {
				return err
			}
		}
		ids = nil
		if err := tx.Model(&MerchantStoreCommerceCardBatch{}).Where("recovery_expires_at <= ? AND response_ciphertext <> ''", now).Order("recovery_expires_at ASC").Limit(limit).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) > 0 {
			if err := tx.Model(&MerchantStoreCommerceCardBatch{}).Where("id IN ?", ids).Update("response_ciphertext", "").Error; err != nil {
				return err
			}
		}
		ids = nil
		// Expired grants cannot be refreshed; retain the connection identity
		// and business mappings while removing its credential material.
		if err := tx.Model(&MerchantStoreCommerceConnection{}).Where("grant_expires_at > 0 AND grant_expires_at <= ? AND tokens_ciphertext <> '' AND lease_expires_at <= ?", now, now).Order("grant_expires_at ASC").Limit(limit).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) > 0 {
			if err := tx.Model(&MerchantStoreCommerceConnection{}).Where("id IN ? AND grant_expires_at > 0 AND grant_expires_at <= ? AND tokens_ciphertext <> '' AND status IN ? AND lease_expires_at <= ?", ids, now, []string{"pending", "active", "reauthorize"}, now).Updates(map[string]any{"tokens_ciphertext": "", "status": "reauthorize", "auth_session_id": "", "updated_at": now}).Error; err != nil {
				return err
			}
		}
		ids = nil
		cutoff := now - 7*86400
		// Requests freeze their original grant deadline. Reauthorization
		// cannot extend private receipt retention or resurrect an old key.
		if err := tx.Model(&MerchantStoreCommerceRestockRequest{}).Where("grant_expires_at > 0 AND grant_expires_at <= ? AND (body_ciphertext <> '' OR key_ciphertext <> '')", cutoff).Order("grant_expires_at ASC").Limit(limit).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) > 0 {
			if err := tx.Model(&MerchantStoreCommerceRestockRequest{}).Where("id IN ?", ids).Updates(map[string]any{"body_ciphertext": "", "key_ciphertext": "", "response_ciphertext": ""}).Error; err != nil {
				return err
			}
			// Uncertain issuance retains a nonsecret audit and SKU blocker.
			if err := tx.Where("id IN ? AND (status = ? OR (status = ? AND issuance_uncertain = ? AND error_code IN ?))", ids, "imported", "manual_recovery", false, commerceImportNoIssuanceErrors).Delete(&MerchantStoreCommerceRestockRequest{}).Error; err != nil {
				return err
			}
		}
		ids = nil
		if err := tx.Model(&MerchantStoreCommerceCardBatch{}).Where("grant_expires_at > 0 AND grant_expires_at <= ?", cutoff).Order("grant_expires_at ASC").Limit(limit).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) > 0 {
			if err := tx.Where("id IN ?", ids).Delete(&MerchantStoreCommerceCardBatch{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
