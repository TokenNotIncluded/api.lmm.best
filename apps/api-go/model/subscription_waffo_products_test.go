package model

import (
	"encoding/json"
	"testing"
)

func TestWaffoPancakePurchaseSnapshotPinsOneProduct(t *testing.T) {
	plan := &SubscriptionPlan{Id: 42, DurationUnit: "month", DurationValue: 1, TotalAmount: 500000,
		WaffoPancakeProducts: []WaffoPancakePlanProduct{
			{ProductType: "one_time", ProductID: "PROD_once", Enabled: true},
			{ProductType: "subscription", ProductID: "PROD_repeat", Enabled: true},
		},
	}
	snapshot, err := plan.WaffoPancakePurchasePlan("one_time")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.WaffoPancakeProducts != nil || snapshot.WaffoPancakeProductId != "PROD_once" || snapshot.WaffoPancakeProductType != "one_time" {
		t.Fatalf("incorrect order snapshot: %+v", snapshot)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	// Admin changes made after the order must not alter the saved purchase.
	plan.WaffoPancakeProducts[0].Enabled = false
	plan.WaffoPancakeProducts[1].ProductID = "PROD_replacement"
	var saved SubscriptionPlan
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.WaffoPancakeProductId != "PROD_once" || saved.WaffoPancakeProductType != "one_time" || saved.TotalAmount != 500000 || saved.DurationValue != 1 {
		t.Fatal("order terms changed")
	}
	if len(plan.WaffoPancakeProducts) != 2 {
		t.Fatal("checkout mutated the plan")
	}
}
