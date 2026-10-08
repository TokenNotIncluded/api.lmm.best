package model

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MerchantStoreGuest struct {
	ID        string `json:"-" gorm:"primaryKey;size:36"`
	TokenHash string `json:"-" gorm:"size:64;not null;uniqueIndex"`
	CreatedAt int64  `json:"-"`
	ExpiresAt int64  `json:"-" gorm:"type:bigint;not null;index"`
}

type MerchantStoreGuestSession struct {
	GuestID   string `json:"guest_id"`
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
}

func CreateMerchantStoreGuestSession() (*MerchantStoreGuestSession, error) {
	token, e := storeToken()
	if e != nil {
		return nil, e
	}
	now := common.GetTimestamp()
	row := MerchantStoreGuest{ID: uuid.NewString(), TokenHash: storeHash(token), CreatedAt: now, ExpiresAt: now + 90*86400}
	e = marketTransaction(DB, func(tx *gorm.DB) error {
		if e := MerchantStoreAccessRequiresWriter(tx); e != nil {
			return e
		}
		return tx.Create(&row).Error
	})
	return &MerchantStoreGuestSession{GuestID: row.ID, Token: token, ExpiresAt: row.ExpiresAt}, e
}

// The raw bearer is never stored, echoed by order APIs, or accepted as a buyer
// ID. Every use resolves the exact durable identity and expiration again.
func ResolveMerchantStoreGuest(tx *gorm.DB, token string) (*MerchantStoreGuest, error) {
	if !storeTokenValid(token) || !storeAccessActive(tx) {
		return nil, ErrMerchantStoreDenied
	}
	var row MerchantStoreGuest
	if e := tx.Where("token_hash = ? AND expires_at > ?", storeHash(token), common.GetTimestamp()).First(&row).Error; e != nil {
		return nil, ErrMerchantStoreDenied
	}
	if len(row.ID) != 36 {
		return nil, ErrMerchantStoreDenied
	}
	return &row, nil
}

func GetMerchantStoreGuestOrder(token, id string) (*MerchantStoreOrder, error) {
	guest, e := ResolveMerchantStoreGuest(DB, token)
	if e != nil {
		return nil, e
	}
	var order MerchantStoreOrder
	if e := DB.Where("id = ? AND buyer_id = 0 AND guest_id = ?", id, guest.ID).First(&order).Error; e != nil {
		return nil, ErrMerchantStoreDenied
	}
	order.PaymentIssued = order.GatewaySnapshot != "" || order.ProviderSessionID != ""
	if e := storeOrderEmailViews(DB, 0, []*MerchantStoreOrder{&order}); e != nil {
		return nil, e
	}
	return &order, nil
}

func GetMerchantStoreGuestPickupToken(token, id string) (string, error) {
	o, e := GetMerchantStoreGuestOrder(token, id)
	if e != nil {
		return "", e
	}
	if o.PickupLoginRequired || (o.Status != "paid" && o.Status != "refund_pending") {
		return "", ErrMerchantStoreDenied
	}
	return storeDecrypt("pickup", o.ID, o.PickupTokenCiphertext)
}

func CancelMerchantStoreGuestOrder(token, id string) error {
	guest, e := ResolveMerchantStoreGuest(DB, token)
	if e != nil {
		return e
	}
	return closeMerchantStoreOrderAuthorized(id, 0, false, false, "", func(tx *gorm.DB, o *MerchantStoreOrder) error {
		current, e := ResolveMerchantStoreGuest(tx, token)
		if e != nil || current.ID != guest.ID || o.BuyerID != 0 || o.GuestID != current.ID {
			return ErrMerchantStoreDenied
		}
		return nil
	})
}

// A frozen guest identity is distinct from every other anonymous buyer and
// from real account identities. The provider callback compares this exact value.
func MerchantStoreOrderBuyerIdentity(o *MerchantStoreOrder) string {
	if o.BuyerID > 0 {
		return "new-api-user-" + fmtStoreActor(o.BuyerID)
	}
	if len(o.GuestID) == 36 {
		return "new-api-store-guest-" + o.GuestID
	}
	return ""
}

// Every checkout query uses a real account or one exact token-backed guest.
// An empty guest ID never identifies all anonymous orders.
func storeCheckoutBuyerOrders(tx *gorm.DB, buyerID int, guestID string) *gorm.DB {
	q := tx.Model(&MerchantStoreOrder{}).Where("buyer_id = ?", buyerID)
	if buyerID > 0 {
		if !storeAccessActive(tx) {
			return q
		}
		return q.Where("COALESCE(guest_id,'') = ''")
	}
	if len(guestID) != 36 {
		return q.Where("1 = 0")
	}
	return q.Where("guest_id = ?", guestID)
}

func FindMerchantStoreOrderByRequestKey(actor int, key string) (*MerchantStoreOrder, error) {
	if actor < 1 || len(key) < 1 || len(key) > 128 {
		return nil, ErrMerchantStoreInput
	}
	return GetMerchantStoreOrder(actor, storeHash("order:"+fmtStoreActor(actor)+":"+key))
}

func FindMerchantStoreGuestOrderByRequestKey(token, key string) (*MerchantStoreOrder, error) {
	if len(key) < 1 || len(key) > 128 {
		return nil, ErrMerchantStoreInput
	}
	guest, e := ResolveMerchantStoreGuest(DB, token)
	if e != nil {
		return nil, e
	}
	id := storeHash("order:guest:" + guest.ID + ":" + key)
	var row MerchantStoreOrder
	if e := DB.Where("id = ? AND buyer_id = 0 AND guest_id = ?", id, guest.ID).First(&row).Error; e != nil {
		return nil, e
	}
	return GetMerchantStoreGuestOrder(token, id)
}

func HasMerchantStoreGuestDisclaimerAcceptance(token string) (bool, error) {
	guest, e := ResolveMerchantStoreGuest(DB, token)
	if e != nil {
		return false, nil
	}
	var count int64
	e = DB.Model(&MerchantStoreTermsAcceptance{}).Where("id = ? AND kind = 'platform' AND seller_id = 0", storeAgreementAcceptanceID("platform", "guest:"+guest.ID, 0, MerchantStoreDisclaimerVersion)).Count(&count).Error
	return count == 1, e
}

func AcceptMerchantStoreGuestDisclaimer(token, version string) error {
	if version != MerchantStoreDisclaimerVersion {
		return ErrMerchantStoreDisclaimer
	}
	return marketTransaction(DB, func(tx *gorm.DB) error {
		if e := MerchantStoreAccessRequiresWriter(tx); e != nil {
			return e
		}
		guest, e := ResolveMerchantStoreGuest(tx, token)
		if e != nil {
			return e
		}
		subject := "guest:" + guest.ID
		row := MerchantStoreTermsAcceptance{ID: storeAgreementAcceptanceID("platform", subject, 0, version), Kind: "platform", Subject: subject, SellerID: 0, Version: version, AcceptedAt: common.GetTimestamp()}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
	})
}
