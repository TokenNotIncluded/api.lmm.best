package model

import (
	"errors"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

var ErrMerchantStoreEmailDeliveryClosed = errors.New("store email delivery obligation is closed")

// Only a worker holding the current outbox lease can obtain this private
// snapshot. It is never returned by a public controller or serialized to logs.
type MerchantStoreEmailDeliveryPayload struct {
	Destination string                           `json:"-"`
	PickupToken string                           `json:"-"`
	TradeNo     string                           `json:"-"`
	Details     *MerchantStoreOrderPickupDetails `json:"-"`
}

func GetMerchantStoreEmailDeliveryPayload(id, leaseToken string) (*MerchantStoreEmailDeliveryPayload, error) {
	if id == "" || !storeTokenValid(leaseToken) {
		return nil, ErrMerchantStoreDenied
	}
	var initial MerchantStoreEmailDelivery
	if err := DB.First(&initial, "id = ? AND state = ? AND lease_token = ? AND lease_until > ?", id, "sending", leaseToken, common.GetTimestamp()).Error; err != nil {
		return nil, ErrMerchantStoreDenied
	}
	var payload *MerchantStoreEmailDeliveryPayload
	err := storeOrderTx(initial.OrderID, func(tx *gorm.DB, order *MerchantStoreOrder) error {
		var row MerchantStoreEmailDelivery
		if err := lockForUpdate(tx).First(&row, "id = ? AND order_id = ? AND state = ? AND lease_token = ? AND lease_until > ?", id, order.ID, "sending", leaseToken, common.GetTimestamp()).Error; err != nil {
			return ErrMerchantStoreDenied
		}
		if row.ID != order.ID || row.BuyerID != order.BuyerID || (order.BuyerID > 0 && order.GuestID != "") || (order.BuyerID == 0 && len(order.GuestID) != 36) {
			return ErrMerchantStoreDenied
		}
		if !order.EmailPickupLink || (order.Status != "paid" && order.Status != "refund_pending") {
			return ErrMerchantStoreEmailDeliveryClosed
		}
		if order.BuyerID > 0 {
			// Preserve the former account getter's live enabled-account check;
			// an encrypted address snapshot does not override a disabled account.
			if _, err := storeUser(tx, order.BuyerID, common.RoleCommonUser); err != nil {
				return ErrMerchantStoreDenied
			}
		}
		email, err := storeOrderDeliveryEmail(tx, order)
		if err != nil {
			return err
		}
		token, err := storeDecrypt("pickup", order.ID, order.PickupTokenCiphertext)
		if err != nil || !storeTokenValid(token) || storeHash(token) != order.PickupTokenHash {
			return ErrMerchantStoreDenied
		}
		details, err := storeOrderPickupDetails(tx, order)
		if err != nil {
			return err
		}
		payload = &MerchantStoreEmailDeliveryPayload{Destination: email, PickupToken: token, TradeNo: order.TradeNo, Details: details}
		return nil
	})
	return payload, err
}

func CloseMerchantStoreEmailDelivery(id, leaseToken string) error {
	if !storeTokenValid(leaseToken) {
		return ErrMerchantStoreInput
	}
	r := DB.Model(&MerchantStoreEmailDelivery{}).Where("id = ? AND state = ? AND lease_token = ?", id, "sending", leaseToken).Updates(map[string]any{"state": "cancelled", "lease_until": 0, "lease_token": "", "last_error_code": "order_closed"})
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return ErrMerchantStoreConflict
	}
	return nil
}
