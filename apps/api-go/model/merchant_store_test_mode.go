package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
)

var ErrMerchantStoreTestMode = errors.New("exit product test mode before submitting or publishing")

// An omitted flag preserves the existing mode; JSON null is not a mode choice.
func (in *MerchantStoreProductInput) UnmarshalJSON(data []byte) error {
	type plain MerchantStoreProductInput
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for key, value := range fields {
		if strings.EqualFold(key, "test_mode") && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return ErrMerchantStoreInput
		}
	}
	*in = MerchantStoreProductInput(decoded)
	return nil
}

// Test mode only changes visibility and who may open a new order. Financial,
// inventory, channel and minimum-price checks remain on the common checkout.
func storeProductPurchaseStatus(p *MerchantStoreProduct) bool {
	if p.TestMode {
		return p.Status == "draft" || p.Status == "pending" || p.Status == "published"
	}
	return p.Status == "published"
}

func storeProductNewBuyer(p *MerchantStoreProduct, buyerID int) error {
	if p.TestMode {
		if buyerID != p.SellerID {
			return ErrMerchantStoreDenied
		}
	}
	if !storeProductPurchaseStatus(p) {
		return ErrMerchantStoreUnavailable
	}
	return nil
}

// Unlike the draft editor, this owner-only display never permits an
// administrator to preview another merchant's product.
func GetMerchantStoreProductPreview(actor int, id string) (*MerchantStoreProduct, error) {
	if _, err := storeUser(DB, actor, common.RoleCommonUser); err != nil {
		return nil, err
	}
	var p MerchantStoreProduct
	if err := DB.Where("id = ? AND seller_id = ?", id, actor).First(&p).Error; err != nil {
		return nil, err
	}
	if err := populateMerchantStoreProduct(DB, &p, true); err != nil {
		return nil, err
	}
	p.ReviewNote, p.ReviewedBy = "", 0
	return &p, nil
}
