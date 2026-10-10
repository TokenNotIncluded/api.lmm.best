package controller

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
)

func TestWaffoPancakeDualProductsRecreation(t *testing.T) {
	before := &model.SubscriptionPlan{PriceAmount: 9, Currency: "USD", DurationUnit: "month", DurationValue: 1,
		WaffoPancakeProducts: []model.WaffoPancakePlanProduct{
			{ProductType: "one_time", ProductID: "once", Enabled: true},
			{ProductType: "subscription", ProductID: "repeat", Enabled: true},
		},
	}
	next := *before
	next.WaffoPancakeProducts = before.WaffoPancakeBindings()
	next.WaffoPancakeProducts[1].Enabled = false
	if waffoPancakeProductMustBeRecreated(before, &next) {
		t.Fatal("disable must not create a replacement")
	}
	next.PriceAmount = 10
	if !waffoPancakeProductMustBeRecreated(before, &next) {
		t.Fatal("price change reused old product")
	}
	next.WaffoPancakeProducts[0].ProductID = "new_once"
	next.WaffoPancakeProducts[1].ProductID = ""
	if waffoPancakeProductMustBeRecreated(before, &next) {
		t.Fatal("fresh product is valid")
	}
}
