package model

import (
	"errors"
	"unicode"

	"gorm.io/gorm"
)

// MerchantStoreOrderPickupDetails contains no inventory, buyer address or
// payment credentials. The title and quantity are frozen on the order; the
// description and links are the seller's current retained product content.
// Variant identity is always the actual frozen selection, never current product content.
type MerchantStoreOrderPickupDetails struct {
	OrderID            string              `json:"order_id"`
	TradeNo            string              `json:"trade_no"`
	ProductID          string              `json:"product_id"`
	ProductTitle       string              `json:"product_title"`
	ProductDescription string              `json:"product_description,omitempty"`
	ProductLinks       []MerchantStoreLink `json:"product_links,omitempty"`
	Quantity           int                 `json:"quantity"`
	VariantID          string              `json:"variant_id,omitempty"`
	VariantName        string              `json:"variant_name,omitempty"`
}

func merchantStorePickupLinkSafe(raw string) bool {
	if !storeURL(raw) {
		return false
	}
	for _, r := range raw {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func storeOrderPickupDetails(tx *gorm.DB, order *MerchantStoreOrder) (*MerchantStoreOrderPickupDetails, error) {
	details := &MerchantStoreOrderPickupDetails{
		OrderID: order.ID, TradeNo: order.TradeNo, ProductID: order.ProductID,
		ProductTitle: order.ProductTitle, Quantity: order.Quantity,
		VariantID: order.VariantID, VariantName: order.VariantName,
	}
	var product MerchantStoreProduct
	err := tx.Select("id", "description", "links").First(&product, "id = ?", order.ProductID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// A missing legacy product must not erase a paid order's identity.
		return details, nil
	}
	if err != nil {
		return nil, err
	}
	details.ProductDescription = product.Description
	for _, link := range product.Links {
		if merchantStorePickupLinkSafe(link.URL) {
			details.ProductLinks = append(details.ProductLinks, link)
		}
	}
	return details, nil
}

// GetMerchantStoreOrderPickupDetails is used by the verified pickup-mail outbox.
// It requires an exact paid order owned by the supplied buyer and never reads
// stock plaintext or relaxes the separate pickup/email authorization rules.
func GetMerchantStoreOrderPickupDetails(buyerID int, orderID string) (*MerchantStoreOrderPickupDetails, error) {
	if buyerID <= 0 || orderID == "" {
		return nil, ErrMerchantStoreDenied
	}
	var order MerchantStoreOrder
	if err := DB.Where("id = ? AND buyer_id = ? AND status = ?", orderID, buyerID, "paid").First(&order).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMerchantStoreDenied
		}
		return nil, err
	}
	return storeOrderPickupDetails(DB, &order)
}
