package model

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// The outbox intentionally holds no email address, URL or delivered card data.
// Workers resolve the current buyer email and encrypted token only while sending.
type MerchantStoreEmailDelivery struct {
	ID            string `json:"id" gorm:"primaryKey;size:64"`
	OrderID       string `json:"order_id" gorm:"size:64;not null;uniqueIndex"`
	BuyerID       int    `json:"-"`
	State         string `json:"state" gorm:"size:16;index"`
	Attempts      int    `json:"attempts"`
	NextAttempt   int64  `json:"next_attempt" gorm:"index"`
	LeaseUntil    int64  `json:"-"`
	LeaseToken    string `json:"-" gorm:"size:43"`
	LastErrorCode string `json:"-" gorm:"size:64"`
	CreatedAt     int64  `json:"created_at"`
	SentAt        int64  `json:"sent_at"`
}

func enqueueMerchantStoreEmail(tx *gorm.DB, o *MerchantStoreOrder) error {
	if !o.EmailPickupLink {
		return nil
	}
	return tx.Create(&MerchantStoreEmailDelivery{ID: o.ID, OrderID: o.ID, BuyerID: o.BuyerID, State: "pending", NextAttempt: common.GetTimestamp(), CreatedAt: common.GetTimestamp()}).Error
}
func ClaimMerchantStoreEmailDelivery(now int64) (*MerchantStoreEmailDelivery, error) {
	var row MerchantStoreEmailDelivery
	e := marketTransaction(DB, func(tx *gorm.DB) error {
		if e := lockForUpdate(tx).Where("attempts < ? AND ((state IN ? AND next_attempt <= ?) OR (state = ? AND lease_until <= ?))", 10, []string{"pending", "retry"}, now, "sending", now).Order("next_attempt ASC,id ASC").First(&row).Error; e != nil {
			return e
		}
		token, e := storeToken()
		if e != nil {
			return e
		}
		row.State = "sending"
		row.LeaseToken = token
		row.LeaseUntil = now + 600
		row.Attempts++
		return tx.Save(&row).Error
	})
	return &row, e
}
func AckMerchantStoreEmailDelivery(id, leaseToken string) error {
	if !storeTokenValid(leaseToken) {
		return ErrMerchantStoreInput
	}
	r := DB.Model(&MerchantStoreEmailDelivery{}).Where("id = ? AND state = ? AND lease_token = ?", id, "sending", leaseToken).Updates(map[string]any{"state": "sent", "sent_at": common.GetTimestamp(), "lease_until": 0, "lease_token": "", "last_error_code": ""})
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return ErrMerchantStoreConflict
	}
	return nil
}
func RetryMerchantStoreEmailDelivery(id, leaseToken string, now int64, code string) error {
	if !storeTokenValid(leaseToken) || code == "" || len(code) > 64 {
		return ErrMerchantStoreInput
	}
	for _, r := range code {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return ErrMerchantStoreInput
		}
	}
	return marketTransaction(DB, func(tx *gorm.DB) error {
		var row MerchantStoreEmailDelivery
		if e := lockForUpdate(tx).Where("id = ? AND state = ? AND lease_token = ?", id, "sending", leaseToken).First(&row).Error; e != nil {
			return ErrMerchantStoreConflict
		}
		row.State = "retry"
		if row.Attempts >= 10 {
			row.State = "failed"
		}
		delay := int64(30) << min(row.Attempts, 7)
		row.NextAttempt = now + delay
		row.LastErrorCode = code
		row.LeaseToken = ""
		row.LeaseUntil = 0
		return tx.Save(&row).Error
	})
}
