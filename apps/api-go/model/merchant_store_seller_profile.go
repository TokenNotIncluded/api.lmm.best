package model

import (
	"net/mail"
	"strings"
	"unicode"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// MerchantStorePublicSeller is deliberately separate from User. Account email,
// balance, access tokens and other account settings are never public shop data.
type MerchantStorePublicSeller struct {
	ID           int    `json:"id"`
	Username     string `json:"username"`
	DisplayName  string `json:"display_name"`
	ContactEmail string `json:"contact_email,omitempty"`
}

func storePublicContactEmail(contact string) string {
	for _, r := range contact {
		if unicode.IsControl(r) {
			return ""
		}
	}
	contact = strings.TrimSpace(contact)
	if contact == "" || len(contact) > 320 {
		return ""
	}
	address, err := mail.ParseAddress(contact)
	if err != nil || !strings.Contains(address.Address, "@") {
		return ""
	}
	return address.Address
}

func storePublicSeller(tx *gorm.DB, id int, publicContact string) (*MerchantStorePublicSeller, error) {
	var seller User
	if err := tx.Select("id", "username", "display_name").Where("id = ? AND status = ? AND role >= ?", id, common.UserStatusEnabled, common.RoleCommonUser).First(&seller).Error; err != nil {
		return nil, err
	}
	return &MerchantStorePublicSeller{ID: seller.Id, Username: seller.Username,
		DisplayName: seller.DisplayName, ContactEmail: storePublicContactEmail(publicContact)}, nil
}

// The shop contact comes from the seller's newest public product, where Contact
// is already explicitly public. Missing/non-email contact stays absent; it never
// falls back to the account email or to a draft/test/deleted product.
func GetPublicMerchantStoreSellerProfile(id int) (*MerchantStorePublicSeller, error) {
	return GetMerchantStoreSellerProfileForViewer(0, id)
}

func GetMerchantStoreSellerProfileForViewer(actor, id int) (*MerchantStorePublicSeller, error) {
	if id < 1 || int64(id) > 2147483647 {
		return nil, ErrMerchantStoreInput
	}
	var product MerchantStoreProduct
	if err := MerchantStoreVisibleProductsForViewer(DB, actor).Select("contact").Where("seller_id = ?", id).
		Order("created_at DESC,id ASC").First(&product).Error; err != nil {
		return nil, err
	}
	return storePublicSeller(DB, id, product.Contact)
}
