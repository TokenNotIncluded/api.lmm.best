package model

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestWaffoPancakeDualProductValidation(t *testing.T) {
	one := WaffoPancakePlanProduct{ProductType: "one_time", ProductID: "PROD_once", Enabled: true}
	recurring := WaffoPancakePlanProduct{ProductType: "subscription", ProductID: "PROD_repeat", Enabled: true}
	for _, tc := range []struct {
		name     string
		products []WaffoPancakePlanProduct
		bad      bool
	}{
		{"both", []WaffoPancakePlanProduct{one, recurring}, false},
		{"one only", []WaffoPancakePlanProduct{one}, false},
		{"recurring only", []WaffoPancakePlanProduct{recurring}, false},
		{"explicit empty", []WaffoPancakePlanProduct{}, false},
		{"legacy nil", nil, false},
		{"duplicate mode", []WaffoPancakePlanProduct{one, one}, true},
		{"same product different modes", []WaffoPancakePlanProduct{one, {ProductType: "subscription", ProductID: "PROD_once", Enabled: true}}, true},
		{"missing product", []WaffoPancakePlanProduct{{ProductType: "one_time", Enabled: true}}, true},
		{"disabled missing product", []WaffoPancakePlanProduct{{ProductType: "one_time"}}, false},
		{"unknown mode", []WaffoPancakePlanProduct{{ProductType: "typo", ProductID: "PROD_bad", Enabled: true}}, true},
		{"too many", []WaffoPancakePlanProduct{one, recurring, one}, true},
		{"invalid ID", []WaffoPancakePlanProduct{{ProductType: "one_time", ProductID: "PROD_\nbad", Enabled: true}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateWaffoPancakeProducts(tc.products)
			if (err != nil) != tc.bad {
				t.Fatalf("bad=%v, error=%v", tc.bad, err)
			}
		})
	}
}

func TestWaffoPancakeDualProductSelection(t *testing.T) {
	both := []WaffoPancakePlanProduct{
		{ProductType: "one_time", ProductID: "PROD_once", Enabled: true},
		{ProductType: "subscription", ProductID: "PROD_repeat", Enabled: true},
	}
	for _, tc := range []struct {
		name, mode, id string
		err            error
	}{
		{"explicit once", "one_time", "PROD_once", nil},
		{"explicit recurring", "subscription", "PROD_repeat", nil},
		{"ambiguous", "", "", ErrWaffoPancakeModeRequired},
		{"typo never recurring", "subscrption", "", ErrWaffoPancakeModeDisabled},
		{"arbitrary product ID rejected", "PROD_repeat", "", ErrWaffoPancakeModeDisabled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SelectWaffoPancakeProduct(both, tc.mode)
			if !errors.Is(err, tc.err) || got.ProductID != tc.id {
				t.Fatalf("got=%+v err=%v", got, err)
			}
		})
	}
	both[1].Enabled = false
	if _, err := SelectWaffoPancakeProduct(both, "subscription"); !errors.Is(err, ErrWaffoPancakeModeDisabled) {
		t.Fatal(err)
	}
	if got, err := SelectWaffoPancakeProduct(both, ""); err != nil || got.ProductType != "one_time" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestWaffoPancakeDualProductsNullAndEmptyRoundTrip(t *testing.T) {
	for _, raw := range []string{"null", "[]"} {
		var products []WaffoPancakePlanProduct
		if err := json.Unmarshal([]byte(raw), &products); err != nil {
			t.Fatal(err)
		}
		products, err := ValidateWaffoPancakeProducts(products)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(products)
		if string(data) != raw {
			t.Fatalf("lost null/empty meaning: %s -> %s", raw, data)
		}
		resolved := WaffoPancakeProductsWithLegacy(products, "PROD_legacy", "subscription")
		if raw == "[]" && len(resolved) != 0 {
			t.Fatal("disabled setting fell back to old product")
		}
		if raw == "null" && len(resolved) != 1 {
			t.Fatal("lost legacy product")
		}
	}
}

func TestWaffoPancakeDualProductsDoNotMutateInput(t *testing.T) {
	input := []WaffoPancakePlanProduct{{ProductType: " ONE_TIME ", ProductID: " PROD_once ", Enabled: true}}
	before := append([]WaffoPancakePlanProduct(nil), input...)
	out, err := ValidateWaffoPancakeProducts(input)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].ProductType != "one_time" || out[0].ProductID != "PROD_once" {
		t.Fatal(out)
	}
	out[0].Enabled = false
	if !reflect.DeepEqual(input, before) {
		t.Fatal("mutated shared configuration")
	}
	copy := WaffoPancakeProductsWithLegacy(input, "ignored", "subscription")
	copy[0].ProductID = "changed"
	if !reflect.DeepEqual(input, before) {
		t.Fatal("shared slice alias")
	}
}
