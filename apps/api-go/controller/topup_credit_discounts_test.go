package controller

import (
	"math"
	"reflect"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/shopspring/decimal"
)

func TestCreditDiscountCatalogPreservesPolicies(t *testing.T) {
	previousQPU := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = previousQPU })
	for _, tc := range []struct {
		name   string
		tokens bool
		input  operation_setting.PaymentAmountDiscount
		want   map[string]float64
	}{
		{"USD_presets", false, operation_setting.PaymentAmountDiscount{"1": 0.8, "3.5": 0.7}, map[string]float64{"500000": 0.8, "1750000": 0.7}},
		{"raw_credit_presets", true, operation_setting.PaymentAmountDiscount{"500000": 0.8, "1750000": 0.7}, map[string]float64{"500000": 0.8, "1750000": 0.7}},
		{"positive_surcharge_preserved", false, operation_setting.PaymentAmountDiscount{"2": 1.1}, map[string]float64{"1000000": 1.1}},
		{"equivalent_spelling", false, operation_setting.PaymentAmountDiscount{"3.5": 0.7, "3.50": 0.7}, map[string]float64{"1750000": 0.7}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := make(operation_setting.PaymentAmountDiscount, len(tc.input))
			for key, value := range tc.input {
				before[key] = value
			}
			got, err := topUpCreditDiscounts(tc.input, tc.tokens)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("discounts=%v error=%v want=%v", got, err, tc.want)
			}
			if !reflect.DeepEqual(before, tc.input) {
				t.Fatal("reading discounts changed the persisted configuration")
			}
		})
	}
}

func TestCreditDiscountCatalogRejectsConflictsWithoutPartialResult(t *testing.T) {
	previousQPU := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = previousQPU })
	for _, config := range []operation_setting.PaymentAmountDiscount{
		{"3.5": 0.7, "3.50": 0.8},
		{"1": 0.8, "bad": 0.5},
		{"1": math.NaN()}, {"1": math.Inf(1)}, {"1": 0}, {"1": -1},
	} {
		if got, err := topUpCreditDiscounts(config, false); err == nil || got != nil {
			t.Fatalf("invalid catalog yielded partial/default discounts: %v %v", got, err)
		}
	}
}

func TestCanonicalQuoteIgnoresLegacyBatchProjection(t *testing.T) {
	previousQPU := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = previousQPU })
	preserveChannelPricing(t)
	preservePaymentCreditAnchor(t, "500000")
	operation_setting.GetGeneralSetting().QuotaDisplayType = "CNY"
	operation_setting.GetPaymentSetting().AmountDiscount = operation_setting.PaymentAmountDiscount{"1": 0.8}
	if err := common.UpdateTopupGroupRatioByJSONString(`{"default":1,"ldc":0.14}`); err != nil {
		t.Fatal(err)
	}
	pricing := payMethodSettlementPricing{
		settlementCurrency: "LDC", usesSettlementUnitsPerPlatformUnit: true,
		settlementUnitsPerPlatformUnit: decimal.NewFromInt(10),
	}
	for _, projection := range []string{"0", "1", "99", "0.000001"} {
		original, paid, err := quoteTopUpResolvedSettlementAmounts(resolvedTopUpAmount{
			CreditedQuota: 500000, LegacyBatch: decimal.RequireFromString(projection),
		}, "ldc", pricing, decimal.RequireFromString("0.5"))
		if err != nil || original.String() != "0.7" || paid.String() != "0.56" {
			t.Fatalf("projection=%s original=%s paid=%s error=%v", projection, original, paid, err)
		}
	}
}

func TestCouponPreviewAndCheckoutCreditQualification(t *testing.T) {
	previousQPU := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = previousQPU })
	preserveChannelPricing(t)
	for _, display := range []string{"USD", "CNY", "TOKENS"} {
		operation_setting.GetGeneralSetting().QuotaDisplayType = display
		amount, err := topUpCouponAmountForCredits(1750000)
		want := "3.5"
		if display == "TOKENS" {
			want = "1750000"
		}
		if err != nil || amount.String() != want {
			t.Fatalf("display=%s amount=%s error=%v", display, amount, err)
		}
	}
	for _, invalid := range []int64{0, -1, common.MaxWalletQuota + 1} {
		if _, err := topUpCouponAmountForCredits(invalid); err == nil {
			t.Fatalf("accepted invalid credits %d", invalid)
		}
	}
}

func TestTopUpSettlementFactorsPreserveRoundingOrder(t *testing.T) {
	for _, tc := range []struct{ base, group, payment, discount, original, paid string }{
		{"10", "0.14", "0.5", "0.8", "0.7", "0.56"},
		{"0.015", "1", "1", "0.3", "0.02", "0"},
		{"10", "1", "1", "0.95", "10", "9.5"},
	} {
		original, paid := applyTopUpSettlementFactorsWithOriginal(
			decimal.RequireFromString(tc.base), decimal.RequireFromString(tc.group),
			decimal.RequireFromString(tc.payment), decimal.RequireFromString(tc.discount))
		if original.String() != tc.original || paid.String() != tc.paid {
			t.Fatalf("original=%s paid=%s want=%s/%s", original, paid, tc.original, tc.paid)
		}
	}
}
