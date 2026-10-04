package billingexpr_test

import (
	"math"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func measuredDimensionValue(value float64) *float64 {
	return &value
}

func TestRunExpr_MeasuredCacheDimensionsHaveSeparatePrices(t *testing.T) {
	const expression = `tier("cache_modalities", p * 2 + c * 10 + cr_text * 0.2 + cr_img * 0.7 + cr_audio * 1.3)`
	params := billingexpr.TokenParams{
		P:       100,
		C:       20,
		CR:      99999,
		CRText:  measuredDimensionValue(10),
		CRImg:   measuredDimensionValue(20),
		CRAudio: measuredDimensionValue(30),
	}

	cost, trace, err := billingexpr.RunExpr(expression, params)
	require.NoError(t, err)
	// Each cache modality has its own price; the aggregate CR is not charged again.
	assert.InDelta(t, 455, cost, 1e-9)
	assert.Equal(t, "cache_modalities", trace.MatchedTier)
	assert.InDelta(t, cost, trace.Cost, 1e-9)
	usedVars := billingexpr.UsedVars(expression)
	for _, name := range []string{"cr_text", "cr_img", "cr_audio"} {
		assert.True(t, usedVars[name], "missing used variable %s", name)
	}
	assert.False(t, usedVars["cr"])
}

func TestRunExpr_MeasuredDimensionsRequireObservedValidValues(t *testing.T) {
	dimensions := []struct {
		name string
		set  func(*billingexpr.TokenParams, *float64)
	}{
		{"cr_text", func(params *billingexpr.TokenParams, value *float64) { params.CRText = value }},
		{"cr_img", func(params *billingexpr.TokenParams, value *float64) { params.CRImg = value }},
		{"cr_audio", func(params *billingexpr.TokenParams, value *float64) { params.CRAudio = value }},
		{"audio_s", func(params *billingexpr.TokenParams, value *float64) { params.AudioSeconds = value }},
	}
	values := []struct {
		name    string
		value   *float64
		want    float64
		wantErr bool
	}{
		{name: "missing", wantErr: true},
		{name: "observed zero", value: measuredDimensionValue(0)},
		{name: "observed fractional value", value: measuredDimensionValue(12.5), want: 25},
		{name: "negative", value: measuredDimensionValue(-0.5), wantErr: true},
		{name: "nan", value: measuredDimensionValue(math.NaN()), wantErr: true},
		{name: "positive infinity", value: measuredDimensionValue(math.Inf(1)), wantErr: true},
		{name: "negative infinity", value: measuredDimensionValue(math.Inf(-1)), wantErr: true},
	}

	for _, dimension := range dimensions {
		t.Run(dimension.name, func(t *testing.T) {
			for _, value := range values {
				t.Run(value.name, func(t *testing.T) {
					params := billingexpr.TokenParams{}
					dimension.set(&params, value.value)
					cost, _, err := billingexpr.RunExpr(dimension.name+" * 2", params)
					if value.wantErr {
						require.Error(t, err)
						assert.Contains(t, err.Error(), dimension.name)
						assert.Zero(t, cost)
						return
					}
					require.NoError(t, err)
					assert.InDelta(t, value.want, cost, 1e-9)
				})
			}
		})
	}
}

func TestRunExpr_MeasuredDimensionsGuardConditionalReferences(t *testing.T) {
	for _, name := range []string{"cr_text", "cr_img", "cr_audio", "audio_s"} {
		t.Run(name, func(t *testing.T) {
			// A request can take the other branch during estimation and later switch
			// branches at settlement. Missing measurements must never silently become 0.
			cost, _, err := billingexpr.RunExpr("p > 0 ? p : "+name, billingexpr.TokenParams{P: 100})
			require.Error(t, err)
			assert.Contains(t, err.Error(), name)
			assert.Zero(t, cost)
		})
	}
}

func TestRunExpr_UnusedMeasuredDimensionsPreserveLegacyBilling(t *testing.T) {
	const expression = `tier("legacy", p * 2 + c * 10 + cr * 0.3 + img * 5 + img_o * 8 + ai * 11 + ao * 13)`
	for _, measurement := range []struct {
		name  string
		value *float64
	}{
		{name: "unobserved"},
		{name: "negative", value: measuredDimensionValue(-10)},
		{name: "nan", value: measuredDimensionValue(math.NaN())},
		{name: "infinity", value: measuredDimensionValue(math.Inf(1))},
	} {
		t.Run(measurement.name, func(t *testing.T) {
			params := billingexpr.TokenParams{
				P:            2,
				C:            3,
				CR:           5,
				Img:          7,
				ImgO:         11,
				AI:           13,
				AO:           17,
				CRText:       measurement.value,
				CRImg:        measurement.value,
				CRAudio:      measurement.value,
				AudioSeconds: measurement.value,
			}
			cost, trace, err := billingexpr.RunExpr(expression, params)
			require.NoError(t, err)
			assert.InDelta(t, 522.5, cost, 1e-9)
			assert.Equal(t, "legacy", trace.MatchedTier)
		})
	}
}

func TestComputeTieredQuota_AudioSecondsConvertsPriceOnce(t *testing.T) {
	// 0.06 USD/minute is expressed in the v1 coefficient unit by multiplying
	// by 1M/60. Settlement then performs the existing single 1M conversion.
	const expression = `tier("audio_duration", audio_s * 0.06 * 1000000 / 60)`
	snapshot := &billingexpr.BillingSnapshot{
		BillingMode:   "tiered",
		ModelName:     "duration-model",
		ExprString:    expression,
		ExprHash:      billingexpr.ExprHashString(expression),
		GroupRatio:    1.5,
		QuotaPerUnit:  500000,
		ExprVersion:   1,
		EstimatedTier: "audio_duration",
	}
	params := billingexpr.TokenParams{AudioSeconds: measuredDimensionValue(90.5)}

	rawCost, trace, err := billingexpr.RunExpr(expression, params)
	require.NoError(t, err)
	assert.InDelta(t, 90500, rawCost, 1e-9)
	assert.Equal(t, "audio_duration", trace.MatchedTier)

	result, err := billingexpr.ComputeTieredQuota(snapshot, params)
	require.NoError(t, err)
	assert.InDelta(t, 45250, result.ActualQuotaBeforeGroup, 1e-9)
	assert.Equal(t, 67875, result.ActualQuotaAfterGroup)
	assert.False(t, result.CrossedTier)
	assert.Nil(t, result.Clamp)

	zero, err := billingexpr.ComputeTieredQuota(snapshot, billingexpr.TokenParams{AudioSeconds: measuredDimensionValue(0)})
	require.NoError(t, err)
	assert.Zero(t, zero.ActualQuotaBeforeGroup)
	assert.Zero(t, zero.ActualQuotaAfterGroup)

	missing, err := billingexpr.ComputeTieredQuota(snapshot, billingexpr.TokenParams{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audio_s")
	assert.Equal(t, billingexpr.TieredResult{}, missing)
}
