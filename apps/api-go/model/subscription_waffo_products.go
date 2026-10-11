package model

func (p *SubscriptionPlan) WaffoPancakeBindings() []WaffoPancakePlanProduct {
	if p == nil {
		return []WaffoPancakePlanProduct{}
	}
	return WaffoPancakeProductsWithLegacy(p.WaffoPancakeProducts, p.WaffoPancakeProductId, p.WaffoPancakeProductType)
}

func (p *SubscriptionPlan) WaffoPancakePurchasePlan(productType string) (*SubscriptionPlan, error) {
	product, err := SelectWaffoPancakeProduct(p.WaffoPancakeBindings(), productType)
	if err != nil {
		return nil, err
	}
	// The order owns the selected product and terms. Later admin switches must
	// not change callback dispatch, renewal, refunds, or old paid entitlements.
	snapshot := *p
	snapshot.WaffoPancakeProducts = nil
	snapshot.WaffoPancakeProductId = product.ProductID
	snapshot.WaffoPancakeProductType = product.ProductType
	return &snapshot, nil
}
