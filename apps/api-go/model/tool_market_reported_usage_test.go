package model

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func reportedMarketCall(t *testing.T, f marketFixture, key string) *ToolMarketCall {
	t.Helper()
	require.NoError(t, f.db.Model(&ToolMarketToolVersion{}).Where("version_id = ?", f.tool.VersionID).Updates(map[string]any{"billing_mode": "input_tokens", "input_token_price_quota": 1000000, "max_input_tokens": 100}).Error)
	c, _, err := ReserveToolMarketCall(f.input(key))
	require.NoError(t, err)
	_, err = StartToolMarketCall(c.ID)
	require.NoError(t, err)
	return c
}

func TestToolMarketReportedUsageInvalidRefundsAndNeverEstimates(t *testing.T) {
	for name, data := range map[string]string{
		"missing":                     `{"structuredContent":{"answer":true}}`,
		"null":                        `{"structuredContent":{"usage":{"input_tokens":null}}}`,
		"negative":                    `{"structuredContent":{"usage":{"input_tokens":-1}}}`,
		"fractional":                  `{"structuredContent":{"usage":{"input_tokens":1.5}}}`,
		"string":                      `{"structuredContent":{"usage":{"input_tokens":"5"}}}`,
		"over-cap":                    `{"structuredContent":{"usage":{"input_tokens":101}}}`,
		"overflow":                    `{"structuredContent":{"usage":{"input_tokens":9223372036854775808}}}`,
		"duplicate":                   `{"structuredContent":{"usage":{"input_tokens":1,"input_tokens":99}}}`,
		"contradictory":               `{"usage":{"input_tokens":1},"structuredContent":{"usage":{"input_tokens":99}}}`,
		"invalid-receipt-no-fallback": `{"_meta":{"lmm_metering":null},"usage":{"input_tokens":1}}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newMarketFixture(t, 100)
			c := reportedMarketCall(t, f, name)
			require.ErrorIs(t, RecordToolMarketResult(c.ID, true, json.RawMessage(data)), ErrToolMarketMetering)
			require.NoError(t, FinishToolMarketCall(c.ID, false))
			require.Equal(t, 1000, marketTestBalance(t, f.db, f.buyer.Id))
			require.Equal(t, 0, marketTestBalance(t, f.db, f.author.Id))
		})
	}
}

func TestToolMarketReportedUsageCompositeZeroAndReplay(t *testing.T) {
	for _, quantity := range []int{0, 500} {
		t.Run(fmt.Sprint(quantity), func(t *testing.T) {
			f := newMarketFixture(t, 300)
			tool := f.tool
			tool.BillingMode = "metered"
			tool.BillingRules = []ToolMarketBillingRule{{"cpu_core_milliseconds", 100, 1000}, {"memory_mib_seconds", 200, 1024}}
			require.NoError(t, f.db.Save(&tool).Error)
			c, _, err := ReserveToolMarketCall(f.input("composite"))
			require.NoError(t, err)
			_, err = StartToolMarketCall(c.ID)
			require.NoError(t, err)
			data := json.RawMessage(fmt.Sprintf(`{"_meta":{"lmm_usage":{"cpu_core_milliseconds":%d,"memory_mib_seconds":%d}},"structuredContent":{"value":true}}`, quantity, quantity))
			require.NoError(t, RecordToolMarketResult(c.ID, true, data))
			require.NoError(t, RecordToolMarketResult(c.ID, true, data))
			require.NoError(t, FinishToolMarketCall(c.ID, true))
			require.NoError(t, FinishToolMarketCall(c.ID, true))
			require.NoError(t, f.db.First(c, "id = ?", c.ID).Error)
			charged := 0
			if quantity > 0 {
				charged = 148
			}
			require.Equal(t, charged, c.PriceQuota)
			require.Equal(t, 1000-charged, marketTestBalance(t, f.db, f.buyer.Id))
			require.Equal(t, ToolMarketUsageReported, c.UsageSource)
			require.NotEmpty(t, c.UsageReport)
			require.Len(t, c.ResultDigest, 64)
			replay, err := LookupToolMarketReplay(f.input("composite"))
			require.NoError(t, err)
			require.Equal(t, c.PriceQuota, replay.PriceQuota)
		})
	}
}

func TestToolMarketReportEvidenceSurvivesResultCleanupAndSuspendsFraud(t *testing.T) {
	f := newMarketFixture(t, 100)
	c := reportedMarketCall(t, f, "report")
	require.NoError(t, RecordToolMarketResult(c.ID, true, json.RawMessage(`{"usage":{"input_tokens":30},"structuredContent":{"value":true}}`)))
	require.NoError(t, FinishToolMarketCall(c.ID, true))
	// Results expire; the billing ledger retains the exact report and price.
	require.NoError(t, f.db.Delete(&ToolMarketResult{}, "call_id = ?", c.ID).Error)
	_, err := ReportToolMarketCall(f.author.Id, c.ID, "fake usage")
	require.ErrorIs(t, err, ErrToolMarketDenied)
	report, err := ReportToolMarketCall(f.buyer.Id, c.ID, "fake usage")
	require.NoError(t, err)
	require.Contains(t, string(report.Evidence), `\"input_tokens\":30`)
	_, err = ReportToolMarketCall(f.buyer.Id, c.ID, "fake usage")
	require.NoError(t, err)
	_, err = ReportToolMarketCall(f.buyer.Id, c.ID, "different reason")
	require.ErrorIs(t, err, ErrToolMarketConflict)
	_, err = ListToolMarketReports(f.buyer.Id, 0, 20)
	require.ErrorIs(t, err, ErrToolMarketDenied)
	rows, err := ListToolMarketReports(f.root.Id, 0, 20)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	// A merchant with admin rights still cannot adjudicate their own service.
	require.NoError(t, f.db.Model(&User{}).Where("id = ?", f.author.Id).Update("role", common.RoleAdminUser).Error)
	require.ErrorIs(t, ReviewToolMarketReport(f.author.Id, c.ID, true, "confirmed"), ErrToolMarketDenied)
	require.NoError(t, ReviewToolMarketReport(f.root.Id, c.ID, true, "confirmed"))
	require.NoError(t, ReviewToolMarketReport(f.root.Id, c.ID, true, "confirmed"))
	_, _, err = ReserveToolMarketCall(f.input("after-fraud"))
	require.Error(t, err)
	require.ErrorIs(t, SetToolMarketPaused(f.author.Id, f.service.ID, false), ErrToolMarketDenied)
	require.Equal(t, 970, marketTestBalance(t, f.db, f.buyer.Id))
	var count int64
	require.NoError(t, f.db.Model(&ToolMarketReport{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}
