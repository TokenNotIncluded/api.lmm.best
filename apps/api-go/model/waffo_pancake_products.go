package model

import (
	"errors"
	"fmt"
	"strings"
)

// These are Pancake product families, not capabilities of other gateways.
const (
	WaffoPancakeProductTypeOneTime      = "one_time"
	WaffoPancakeProductTypeSubscription = "subscription"
)

// NormalizeWaffoPancakeProductType preserves historical order snapshots.
// New configuration and checkout input must use the strict helpers below.
func NormalizeWaffoPancakeProductType(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), WaffoPancakeProductTypeOneTime) {
		return WaffoPancakeProductTypeOneTime
	}
	return WaffoPancakeProductTypeSubscription
}

type WaffoPancakePlanProduct struct {
	ProductType string `json:"product_type"`
	ProductID   string `json:"product_id"`
	Enabled     bool   `json:"enabled"`
}

var (
	ErrWaffoPancakeModeRequired = errors.New("请选择一次性购买或自动续费")
	ErrWaffoPancakeModeDisabled = errors.New("该 Waffo Pancake 购买方式未启用")
)

// nil is an old single-product plan. [] is an explicit disable-all setting.
// Return a copy so a cached plan cannot be changed by validation or checkout.
func ValidateWaffoPancakeProducts(products []WaffoPancakePlanProduct) ([]WaffoPancakePlanProduct, error) {
	if products == nil {
		return nil, nil
	}
	if len(products) > 2 {
		return nil, errors.New("Waffo Pancake 最多配置两种商品")
	}
	out := make([]WaffoPancakePlanProduct, 0, len(products))
	types := make(map[string]bool, 2)
	ids := make(map[string]bool, 2)
	for _, product := range products {
		product.ProductType = strings.ToLower(strings.TrimSpace(product.ProductType))
		product.ProductID = strings.TrimSpace(product.ProductID)
		if product.ProductType != WaffoPancakeProductTypeOneTime && product.ProductType != WaffoPancakeProductTypeSubscription {
			return nil, fmt.Errorf("无效的 Waffo Pancake 商品类型: %q", product.ProductType)
		}
		if types[product.ProductType] {
			return nil, errors.New("同一种 Waffo Pancake 商品只能配置一次")
		}
		types[product.ProductType] = true
		if product.Enabled && product.ProductID == "" {
			return nil, errors.New("请先创建或选择已启用方式对应的 Waffo Pancake 商品")
		}
		if product.ProductID != "" {
			if len(product.ProductID) > 128 || strings.ContainsAny(product.ProductID, " \t\r\n\x00") {
				return nil, errors.New("无效的 Waffo Pancake 商品 ID")
			}
			if ids[product.ProductID] {
				return nil, errors.New("一次性购买和自动续费不能使用同一个 Waffo Pancake 商品")
			}
			ids[product.ProductID] = true
		}
		out = append(out, product)
	}
	return out, nil
}

func WaffoPancakeProductsWithLegacy(products []WaffoPancakePlanProduct, legacyID, legacyType string) []WaffoPancakePlanProduct {
	if products != nil {
		out := make([]WaffoPancakePlanProduct, len(products))
		copy(out, products)
		return out
	}
	if strings.TrimSpace(legacyID) == "" {
		return []WaffoPancakePlanProduct{}
	}
	return []WaffoPancakePlanProduct{{
		ProductType: NormalizeWaffoPancakeProductType(legacyType),
		ProductID:   strings.TrimSpace(legacyID),
		Enabled:     true,
	}}
}

func EnabledWaffoPancakeProducts(products []WaffoPancakePlanProduct) []WaffoPancakePlanProduct {
	valid, err := ValidateWaffoPancakeProducts(products)
	if err != nil {
		return []WaffoPancakePlanProduct{}
	}
	out := make([]WaffoPancakePlanProduct, 0, len(valid))
	for _, product := range valid {
		if product.Enabled {
			out = append(out, product)
		}
	}
	return out
}

func SelectWaffoPancakeProduct(products []WaffoPancakePlanProduct, requestedType string) (WaffoPancakePlanProduct, error) {
	available := EnabledWaffoPancakeProducts(products)
	requestedType = strings.ToLower(strings.TrimSpace(requestedType))
	if requestedType == "" {
		if len(available) == 1 {
			return available[0], nil
		}
		if len(available) > 1 {
			return WaffoPancakePlanProduct{}, ErrWaffoPancakeModeRequired
		}
	} else {
		for _, product := range available {
			if product.ProductType == requestedType {
				return product, nil
			}
		}
	}
	return WaffoPancakePlanProduct{}, ErrWaffoPancakeModeDisabled
}
