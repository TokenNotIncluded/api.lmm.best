// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package model

import (
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"
)

// These checks cover pure rules only. They do not test DB grants, balance
// writes, claims, reservations, refunds, account access, or concurrent requests.
func TestBI07GiftOfferAndClaimCaps(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	minInt := -maxInt - 1
	for _, test := range []struct {
		name        string
		credits     int
		cap         int
		newErr      error
		existingErr error
	}{
		{"zero with positive cap", 0, 1, nil, nil},
		{"smallest exclusive boundary", 1, 1, ErrAssistantGiftLimit, nil},
		{"below lowered cap", 9, 10, nil, nil},
		{"equal lowered cap preserves existing rule", 10, 10, ErrAssistantGiftLimit, nil},
		{"above lowered cap", 11, 10, ErrAssistantGiftLimit, ErrAssistantGiftLimit},
		{"disabled with zero grant", 0, 0, ErrAssistantGiftDisabled, ErrAssistantGiftDisabled},
		{"disabled with positive grant", 1, 0, ErrAssistantGiftDisabled, ErrAssistantGiftDisabled},
		{"negative cap", 0, -1, ErrAssistantGiftDisabled, ErrAssistantGiftDisabled},
		{"minimum integer rejected", minInt, 100, ErrAssistantGiftInvalid, ErrAssistantGiftInvalid},
		{"negative grant rejected", -1, 100, ErrAssistantGiftInvalid, ErrAssistantGiftInvalid},
		{"maximum integer rejected above cap", maxInt, 100, ErrAssistantGiftLimit, ErrAssistantGiftLimit},
		{"maximum cap without arithmetic overflow", maxInt - 1, maxInt, nil, nil},
		{"maximum integer equality", maxInt, maxInt, ErrAssistantGiftLimit, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, mode := range []struct {
				name  string
				check func(int, int) error
				want  error
			}{
				{"new", CheckAssistantGiftCreditLimit, test.newErr},
				{"existing", CheckAssistantExistingGiftCreditLimit, test.existingErr},
			} {
				err := mode.check(test.credits, test.cap)
				if !errors.Is(err, mode.want) {
					t.Fatalf("%s(%d,%d)=%v, want %v", mode.name, test.credits, test.cap, err, mode.want)
				}
				wantCode := ""
				switch mode.want {
				case ErrAssistantGiftInvalid:
					wantCode = "invalid_decision"
				case ErrAssistantGiftDisabled:
					wantCode = "gift_disabled"
				case ErrAssistantGiftLimit:
					wantCode = "gift_limit_exceeded"
				}
				if code := AssistantGiftErrorCode(err); code != wantCode {
					t.Fatalf("%s error code=%q, want %q", mode.name, code, wantCode)
				}
			}
		})
	}
}

func TestBI07WeeklyPeriodUsesUTC(t *testing.T) {
	for _, test := range []struct{ now, start string }{
		{"2026-10-11T23:59:59.999999999Z", "2026-10-05T00:00:00Z"},
		{"2026-10-12T00:00:00Z", "2026-10-12T00:00:00Z"},
		{"2026-10-12T00:00:00+14:00", "2026-10-05T00:00:00Z"},
		{"2026-10-11T17:00:00-07:00", "2026-10-12T00:00:00Z"},
		{"2026-11-01T01:30:00-07:00", "2026-10-26T00:00:00Z"},
		{"2026-11-01T01:30:00-08:00", "2026-10-26T00:00:00Z"},
		{"2026-03-08T01:59:59-08:00", "2026-03-02T00:00:00Z"},
		{"2026-03-08T03:00:00-07:00", "2026-03-02T00:00:00Z"},
		{"2027-01-01T00:00:00Z", "2026-12-28T00:00:00Z"},
		{"2028-02-29T12:00:00Z", "2028-02-28T00:00:00Z"},
	} {
		t.Run(test.now, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339Nano, test.now)
			if err != nil {
				t.Fatal(err)
			}
			want, err := time.Parse(time.RFC3339Nano, test.start)
			if err != nil {
				t.Fatal(err)
			}
			if got := assistantWeekStart(now); got != want.Unix() {
				t.Fatalf("week start=%s, want %s", time.Unix(got, 0).UTC(), want)
			}
			for _, offset := range []int{-12 * 3600, -8 * 3600, 0, 5*3600 + 1800, 14 * 3600} {
				if got := assistantWeekStart(now.In(time.FixedZone("fixture", offset))); got != want.Unix() {
					t.Fatalf("same instant in offset %d changed the period", offset)
				}
			}
			end := want.Add(7 * 24 * time.Hour)
			if assistantWeekStart(end.Add(-time.Nanosecond)) != want.Unix() || assistantWeekStart(end) != end.Unix() {
				t.Fatal("UTC period did not change at the exact boundary")
			}
		})
	}
}

// Big integers provide an independent reference. Inputs stay within the
// documented JS-safe non-negative quota range and the 0..100 percent range.
func TestBI07ReferralPenaltyMatchesExactArithmetic(t *testing.T) {
	quotas := []int64{0, 1, 99, 100, 101, 9007199254740990, 9007199254740991}
	for _, quota := range quotas {
		if int64(int(quota)) != quota {
			t.Fatal("this test requires a 64-bit int")
		}
		for _, percent := range []int{0, 1, 20, 99, 100} {
			for _, cap := range []int{0, 1, 100, int(quota)} {
				want := new(big.Int).Mul(big.NewInt(quota), big.NewInt(int64(percent)))
				want.Quo(want, big.NewInt(100))
				if cap > 0 && want.Cmp(big.NewInt(int64(cap))) > 0 {
					want.SetInt64(int64(cap))
				}
				got := referralPenalty(int(quota), percent, cap)
				if int64(got) != want.Int64() || got < 0 || int64(got) > quota {
					t.Fatalf("quota=%d percent=%d cap=%d got=%d want=%s", quota, percent, cap, got, want)
				}
			}
		}
	}
}

func TestBI07CouponDefinitionBoundaries(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, percent := range []int{-maxInt - 1, -1, 0, 1, 99, 100, maxInt} {
		t.Run(fmt.Sprintf("percent=%d", percent), func(t *testing.T) {
			err := ValidateDiscountCodeTerms(percent, 0, 0, 0)
			valid := percent >= 1 && percent <= 99
			if (err == nil) != valid {
				t.Fatalf("percent=%d error=%v", percent, err)
			}
		})
	}
	for _, test := range []struct {
		name                string
		minimum, start, end int64
		valid               bool
	}{
		{"zero minimum and no expiry", 0, 0, 0, true},
		{"positive window", 1, 100, 101, true},
		{"negative minimum", -1, 0, 0, false},
		{"negative start", 0, -1, 0, false},
		{"negative expiry", 0, 0, -1, false},
		{"empty positive window", 0, 100, 100, false},
		{"reversed window", 0, 101, 100, false},
		{"largest valid time", 0, 9223372036854775806, 9223372036854775807, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateDiscountCodeTerms(10, test.minimum, test.start, test.end); (err == nil) != test.valid {
				t.Fatalf("valid=%v error=%v", test.valid, err)
			}
		})
	}
	for _, uses := range []int64{-9223372036854775808, -1, 0, 1, 9223372036854775807} {
		if err := ValidateDiscountCodeMaxUses(uses); (err == nil) != (uses >= 0) {
			t.Fatalf("max uses=%d error=%v", uses, err)
		}
	}
}
