package billingexpr

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type officialPricingFixture struct {
	Hy3      string                 `json:"hy3_cny_expression"`
	Sonnet   string                 `json:"claude_sonnet_55_usd_expression"`
	Image    string                 `json:"gpt_image_2_usd_expression"`
	Holidays [][2]int               `json:"china_holiday_ranges_2026"`
	DeepSeek map[string]TokenParams `json:"deepseek_usd_rates"`
}

func readOfficialPricing(t *testing.T) officialPricingFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/official-pricing-20261009.json")
	require.NoError(t, err)
	var prices officialPricingFixture
	require.NoError(t, json.Unmarshal(raw, &prices))
	return prices
}

func TestOfficialPricingMeasuredRates(t *testing.T) {
	prices := readOfficialPricing(t)
	zero, million := float64(0), float64(1_000_000)
	for _, tc := range []struct {
		name, expression string
		params           TokenParams
		amount           float64
	}{
		{"Hy3 input CNY", prices.Hy3, TokenParams{P: million}, 1},
		{"Hy3 cached CNY", prices.Hy3, TokenParams{CR: million}, .25},
		{"Hy3 output CNY", prices.Hy3, TokenParams{C: million}, 4},
		{"Hy3 converted USD", "(" + prices.Hy3 + ") / 6.714466", TokenParams{P: million}, 1 / 6.714466},
		{"Sonnet cached USD", prices.Sonnet, TokenParams{CR: million}, .1},
		{"Sonnet cache write USD", prices.Sonnet, TokenParams{CC: million}, 2.5},
		{"Sonnet cache write 1h USD", prices.Sonnet, TokenParams{CC1h: million}, 4},
		{"Image text USD", prices.Image, TokenParams{P: million, CRText: &zero, CRImg: &zero}, 5},
		{"Image text cached USD", prices.Image, TokenParams{CRText: &million, CRImg: &zero}, 1.25},
		{"Image input USD", prices.Image, TokenParams{Img: million, CRText: &zero, CRImg: &zero}, 8},
		{"Image cached USD", prices.Image, TokenParams{CRText: &zero, CRImg: &million}, 2},
		{"Image output USD", prices.Image, TokenParams{ImgO: million, CRText: &zero, CRImg: &zero}, 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cost, _, err := RunExpr(tc.expression, tc.params)
			require.NoError(t, err)
			require.InDelta(t, tc.amount, cost/million, 1e-12)
		})
	}
	_, _, err := RunExpr(prices.Image, TokenParams{P: million, CR: million})
	require.ErrorContains(t, err, "measured dimension", "aggregate cache usage must not be guessed as text or image")
}

func TestOfficialDeepSeekPeakWindowsAndHolidays(t *testing.T) {
	prices := readOfficialPricing(t)
	var holidays []string
	for _, dates := range prices.Holidays {
		holidays = append(holidays, fmt.Sprintf(`(date("Asia/Shanghai") >= %d && date("Asia/Shanghai") <= %d)`, dates[0], dates[1]))
	}
	for _, family := range []string{"flash", "pro"} {
		peak, off := prices.DeepSeek[family+"_peak"], prices.DeepSeek[family+"_off_peak"]
		expression := fmt.Sprintf(`weekday("UTC") >= 1 && weekday("UTC") <= 5 && ((hour("UTC") >= 1 && hour("UTC") < 4) || (hour("UTC") >= 6 && hour("UTC") < 10)) && !(%s) ? tier("peak", p * %g + cr * %g + c * %g) : tier("off_peak", p * %g + cr * %g + c * %g)`, strings.Join(holidays, " || "), peak.P, peak.CR, peak.C, off.P, off.CR, off.C)
		entry, err := compileEntryFromCacheByHash(expression, ExprHashString(expression))
		require.NoError(t, err)
		for _, tc := range []struct {
			at   string
			peak bool
		}{
			{"2026-10-01T02:00:00Z", false},
			{"2026-10-07T07:00:00Z", false},
			{"2026-10-08T00:59:59Z", false},
			{"2026-10-08T01:00:00Z", true},
			{"2026-10-08T03:59:59Z", true},
			{"2026-10-08T04:00:00Z", false},
			{"2026-10-08T06:00:00Z", true},
			{"2026-10-08T09:59:59Z", true},
			{"2026-10-08T10:00:00Z", false},
			{"2026-10-10T02:00:00Z", false}, // Makeup Saturday remains outside Monday-Friday.
			{"2027-10-01T02:00:00Z", true},  // Test year binding only; the next calendar must be imported.
		} {
			t.Run(family+"/"+tc.at, func(t *testing.T) {
				at, err := time.Parse(time.RFC3339, tc.at)
				require.NoError(t, err)
				cost, trace, err := runProgramAt(entry.prog, entry.requestRules, entry.usedVars, TokenParams{P: 1_000_000, CR: 1_000_000, C: 1_000_000}, RequestInput{}, at)
				require.NoError(t, err)
				tier := "off_peak"
				if tc.peak {
					tier = "peak"
				}
				rate := prices.DeepSeek[family+"_"+tier]
				require.Equal(t, tier, trace.MatchedTier)
				require.InDelta(t, rate.P+rate.CR+rate.C, cost/1_000_000, 1e-12)
			})
		}
	}
}
