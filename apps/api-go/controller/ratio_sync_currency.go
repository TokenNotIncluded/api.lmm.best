package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	"github.com/LIghtJUNction/api.lmm.best/setting/billing_setting"
	"github.com/shopspring/decimal"
)

var pricingSyncOptionKeys = map[string]string{
	"model_ratio": "ModelRatio", "completion_ratio": "CompletionRatio",
	"cache_ratio": "CacheRatio", "create_cache_ratio": "CreateCacheRatio",
	"image_ratio": "ImageRatio", "audio_ratio": "AudioRatio", "audio_completion_ratio": "AudioCompletionRatio",
	"model_price": "ModelPrice", billing_setting.BillingModeField: "billing_setting.billing_mode",
	billing_setting.BillingExprField: "billing_setting.billing_expr",
}

const syncUntrustedModels = "pricing_sync_untrusted_models"
const syncSkippedModels = "pricing_sync_skipped_models"
const syncSourceProviders = "pricing_sync_source_providers"

// The comparison and save payload share the existing USD CAS representation:
// ModelRatio is calibrated credits/token; monetary prices/expressions are USD;
// completion, cache and modality ratios remain dimensionless.
func pricingSyncDataFromConfig(config model.USDPriceConfig) (map[string]any, error) {
	data := make(map[string]any, len(pricingSyncFields))
	for _, field := range pricingSyncFields {
		var entries map[string]any
		if err := json.Unmarshal([]byte(config.Values[pricingSyncOptionKeys[field]]), &entries); err != nil || entries == nil {
			return nil, fmt.Errorf("invalid pricing snapshot field %s", field)
		}
		data[field] = entries
	}
	return data, nil
}

type pricingSyncEnvelope struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	PricingSyncMetadata
}

// A declared modern source must supply its unit identity. Only an entirely
// unmarked legacy NewAPI response uses its standard 500000-credit USD basis.
type PricingSyncMetadata struct {
	PricingSchemaVersion      *int     `json:"pricing_schema_version,omitempty"`
	SchemaVersion             *int     `json:"schema_version,omitempty"`
	PricingCurrency           string   `json:"pricing_currency,omitempty"`
	Currency                  string   `json:"currency,omitempty"`
	PricingStorageBasis       string   `json:"pricing_storage_basis,omitempty"`
	StorageBasis              string   `json:"storage_basis,omitempty"`
	CreditsPerUSD             *float64 `json:"credits_per_usd,omitempty"`
	LedgerQuotaPerUSD         *float64 `json:"ledger_quota_per_usd,omitempty"`
	LedgerQuotaPerUSDExact    string   `json:"ledger_quota_per_usd_exact,omitempty"`
	PublicCreditsPerUSD       *float64 `json:"public_credits_per_usd,omitempty"`
	PublicCreditsPerUSDExact  string   `json:"public_credits_per_usd_exact,omitempty"`
	LegacyCreditUnit          string   `json:"legacy_credit_unit,omitempty"`
	CreditUnitSchemaVersion   *int     `json:"credit_unit_schema_version,omitempty"`
	QuotaUnit                 string   `json:"quota_unit,omitempty"`
	PublicCreditUnit          string   `json:"public_credit_unit,omitempty"`
	ModelRatioUnit            string   `json:"model_ratio_unit,omitempty"`
	LegacyPricingQuotaPerUnit *float64 `json:"legacy_pricing_quota_per_unit,omitempty"`
	QuotaPerUnit              *float64 `json:"quota_per_unit,omitempty"`
	LegacyPricingUnitsPerUSD  *float64 `json:"legacy_pricing_units_per_usd,omitempty"`
}

type pricingSyncUnits struct {
	canonical         bool
	creditsPerUSD     float64
	legacyUnitsPerUSD float64
}

func (metadata PricingSyncMetadata) hasDenominationMetadata() bool {
	return metadata.LedgerQuotaPerUSD != nil || metadata.LedgerQuotaPerUSDExact != "" || metadata.PublicCreditsPerUSD != nil || metadata.PublicCreditsPerUSDExact != "" || metadata.CreditUnitSchemaVersion != nil || metadata.QuotaUnit != "" || metadata.PublicCreditUnit != "" || metadata.LegacyCreditUnit != ""
}

func syncSourceUnits(metadata PricingSyncMetadata) (pricingSyncUnits, error) {
	units := pricingSyncUnits{creditsPerUSD: 500000, legacyUnitsPerUSD: 1}
	if metadata.LedgerQuotaPerUSD != nil {
		if !validSyncUnit(*metadata.LedgerQuotaPerUSD) || (metadata.CreditsPerUSD != nil && *metadata.CreditsPerUSD != *metadata.LedgerQuotaPerUSD) {
			return units, fmt.Errorf("conflicting or invalid upstream ledger quota basis")
		}
		metadata.CreditsPerUSD = metadata.LedgerQuotaPerUSD
	}
	if metadata.hasDenominationMetadata() {
		if metadata.CreditUnitSchemaVersion == nil || *metadata.CreditUnitSchemaVersion != common.PublicCreditUnitSchemaVersion || metadata.LedgerQuotaPerUSD == nil || metadata.PublicCreditsPerUSD == nil || metadata.QuotaUnit != common.LedgerQuotaUnit || metadata.PublicCreditUnit != common.PublicCreditUnit {
			return units, fmt.Errorf("incomplete upstream credit denomination metadata")
		}
		if metadata.LegacyCreditUnit != "" && metadata.LegacyCreditUnit != common.LedgerQuotaUnit {
			return units, fmt.Errorf("unsupported upstream legacy credit unit")
		}
		if !validSyncUnit(*metadata.PublicCreditsPerUSD) {
			return units, fmt.Errorf("invalid upstream public credit denomination")
		}
		public := decimal.NewFromFloat(*metadata.PublicCreditsPerUSD)
		if common.ValidatePublicCreditsPerUSD(public) != nil {
			return units, fmt.Errorf("invalid upstream public credit denomination")
		}
		for _, pair := range []struct {
			exact  string
			number float64
		}{
			{metadata.LedgerQuotaPerUSDExact, *metadata.LedgerQuotaPerUSD},
			{metadata.PublicCreditsPerUSDExact, *metadata.PublicCreditsPerUSD},
		} {
			if pair.exact == "" {
				continue
			}
			value, err := decimal.NewFromString(pair.exact)
			if err != nil || !value.IsPositive() || len(pair.exact) > 80 || value.Exponent() < -18 || value.Exponent() > 18 || value.InexactFloat64() != pair.number {
				return units, fmt.Errorf("conflicting or invalid upstream exact credit basis")
			}
		}
	}
	if metadata.ModelRatioUnit != "" && metadata.ModelRatioUnit != "LEDGER_QUOTA_PER_TOKEN" {
		return units, fmt.Errorf("unsupported upstream model ratio unit")
	}
	version := 0
	for _, v := range []*int{metadata.PricingSchemaVersion, metadata.SchemaVersion} {
		if v != nil {
			if *v != 1 && *v != model.PricingSchemaUSD || version != 0 && version != *v {
				return units, fmt.Errorf("unsupported upstream pricing schema")
			}
			version = *v
		}
	}
	currency := metadata.PricingCurrency
	if metadata.Currency != "" {
		if currency != "" && currency != metadata.Currency {
			return units, fmt.Errorf("conflicting upstream pricing currencies")
		}
		currency = metadata.Currency
	}
	if currency != "" && currency != model.PricingCurrencyUSD && currency != model.PricingStorageLegacy {
		return units, fmt.Errorf("unsupported upstream pricing currency")
	}
	basis := metadata.PricingStorageBasis
	if metadata.StorageBasis != "" {
		if basis != "" && basis != metadata.StorageBasis {
			return units, fmt.Errorf("conflicting upstream storage bases")
		}
		basis = metadata.StorageBasis
	}
	if basis != "" && basis != model.PricingStorageLegacy && basis != model.PricingCurrencyUSD {
		return units, fmt.Errorf("unsupported upstream pricing storage basis")
	}
	units.canonical = version == model.PricingSchemaUSD
	if !units.canonical && (currency == model.PricingCurrencyUSD || basis == model.PricingCurrencyUSD) {
		return units, fmt.Errorf("upstream USD prices require pricing schema 2")
	}
	if units.canonical && currency != model.PricingCurrencyUSD {
		return units, fmt.Errorf("schema 2 upstream prices must declare USD")
	}
	declared := version != 0 || currency != "" || basis != "" || metadata.CreditsPerUSD != nil || metadata.hasDenominationMetadata() || metadata.ModelRatioUnit != "" || metadata.LegacyPricingQuotaPerUnit != nil || metadata.QuotaPerUnit != nil || metadata.LegacyPricingUnitsPerUSD != nil
	if !declared {
		return units, nil
	}
	if metadata.CreditsPerUSD != nil {
		if !validSyncUnit(*metadata.CreditsPerUSD) {
			return units, fmt.Errorf("invalid upstream credits_per_usd")
		}
		units.creditsPerUSD = *metadata.CreditsPerUSD
	} else {
		units.creditsPerUSD = 0 // Canonical list prices can use input_price without a raw-ratio anchor.
	}
	quota := metadata.LegacyPricingQuotaPerUnit
	if metadata.QuotaPerUnit != nil {
		if quota != nil && *quota != *metadata.QuotaPerUnit {
			return units, fmt.Errorf("conflicting upstream quota units")
		}
		quota = metadata.QuotaPerUnit
	}
	if quota != nil && !validSyncUnit(*quota) {
		return units, fmt.Errorf("invalid upstream quota_per_unit")
	}
	if metadata.LegacyPricingUnitsPerUSD != nil {
		if !validSyncUnit(*metadata.LegacyPricingUnitsPerUSD) {
			return units, fmt.Errorf("invalid upstream legacy price scale")
		}
		units.legacyUnitsPerUSD = *metadata.LegacyPricingUnitsPerUSD
	}
	if quota != nil && units.creditsPerUSD > 0 {
		scale := units.creditsPerUSD / *quota
		if !validSyncUnit(scale) {
			return units, fmt.Errorf("invalid upstream legacy price scale")
		}
		if metadata.LegacyPricingUnitsPerUSD != nil && math.Abs(scale-units.legacyUnitsPerUSD) > 1e-12*math.Max(scale, units.legacyUnitsPerUSD) {
			return units, fmt.Errorf("inconsistent upstream pricing units")
		}
		units.legacyUnitsPerUSD = scale
	}
	if !units.canonical && (units.creditsPerUSD == 0 || quota == nil && metadata.LegacyPricingUnitsPerUSD == nil) {
		return units, fmt.Errorf("declared legacy upstream prices require credits_per_usd and quota_per_unit or legacy_pricing_units_per_usd")
	}
	return units, nil
}

func isValidNonNegativeCost(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func syncFloatLiteral(raw string) (float64, error) {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("upstream numeric value is outside the representable range")
	}
	if value == 0 {
		// ParseFloat silently underflows very small nonzero literals. Examine
		// only the mantissa, so a valid zero such as 0e-400 stays zero.
		mantissa := strings.SplitN(strings.ToLower(raw), "e", 2)[0]
		if strings.ContainsAny(mantissa, "123456789") {
			return 0, fmt.Errorf("nonzero upstream numeric value underflows to zero")
		}
	}
	return value, nil
}

func validateSyncJSONNumbers(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var visit func(any) error
	visit = func(value any) error {
		switch typed := value.(type) {
		case json.Number:
			_, err := syncFloatLiteral(string(typed))
			return err
		case map[string]any:
			for _, item := range typed {
				if err := visit(item); err != nil {
					return err
				}
			}
		case []any:
			for _, item := range typed {
				if err := visit(item); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visit(value)
}

func validSyncUnit(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

// Decimal intermediates avoid overflow or six-decimal rounding that can turn
// a positive quoted price into a free rate.
func scaleSyncPrice(value, numerator, denominator float64) (float64, error) {
	if !isValidNonNegativeCost(value) || !validSyncUnit(numerator) || !validSyncUnit(denominator) {
		return 0, fmt.Errorf("invalid upstream price or currency unit")
	}
	result := decimal.NewFromFloat(value).Mul(decimal.NewFromFloat(numerator)).DivRound(decimal.NewFromFloat(denominator), 340).InexactFloat64()
	if !isValidNonNegativeCost(result) || value > 0 && result == 0 {
		return 0, fmt.Errorf("upstream price is outside the representable range")
	}
	return result, nil
}

func skipPricingSyncModel(data map[string]any, name, reason string) {
	skipped := valueMap(data[syncSkippedModels])
	if skipped == nil {
		skipped = map[string]any{}
	}
	data[syncSkippedModels] = skipped
	skipped[name] = reason
	for _, field := range pricingSyncFields {
		delete(valueMap(data[field]), name)
	}
}

func syncUSDExpression(expr string, units pricingSyncUnits) (string, error) {
	if strings.TrimSpace(expr) == "" {
		return expr, nil
	}
	if err := billing_setting.SmokeTestExpr(expr); err != nil {
		return "", fmt.Errorf("invalid upstream billing expression: %w", err)
	}
	if units.canonical || units.legacyUnitsPerUSD == 1 {
		return expr, nil
	}
	_, body := billingexpr.ParseExprVersion(expr)
	return expr[:len(expr)-len(body)] + "(" + body + ") / (" + decimal.NewFromFloat(units.legacyUnitsPerUSD).String() + ")", nil
}

// Canonical quotes may include the absolute rate even when a source's ratio
// was a default and omitted from JSON. Derive that factor from the same USD
// quote, and reject contradictory rates instead of fabricating a free model.
func canonicalSyncRatio(price *float64, baseline float64, declared *float64) (*float64, error) {
	if price == nil {
		return declared, nil
	}
	if !isValidNonNegativeCost(*price) || !isValidNonNegativeCost(baseline) {
		return nil, fmt.Errorf("invalid canonical upstream price")
	}
	if baseline == 0 {
		if *price > 0 {
			return nil, fmt.Errorf("positive canonical price cannot use a free ratio baseline")
		}
		return declared, nil // A free quote cannot establish an otherwise missing factor.
	}
	factor, err := scaleSyncPrice(*price, 1, baseline)
	if err != nil {
		return nil, err
	}
	if declared != nil {
		product, err := model.USDPriceProduct(baseline, *declared)
		if err != nil || !nearlyEqual(product, *price) {
			return nil, fmt.Errorf("canonical upstream price conflicts with its ratio")
		}
		return declared, nil
	}
	return &factor, nil
}

func normalizeUpstreamSyncData(data map[string]any, units pricingSyncUnits, targetK float64) (map[string]any, error) {
	result := make(map[string]any)
	if !units.canonical {
		for name, raw := range valueMap(data["model_ratio"]) {
			ratio, ratioOK := asFloat64(raw)
			completion, completionOK := asFloat64(valueMap(data["completion_ratio"])[name])
			if ratioOK && completionOK && nearlyEqual(ratio, 37.5) && nearlyEqual(completion, 1) {
				if result[syncUntrustedModels] == nil {
					result[syncUntrustedModels] = map[string]any{}
				}
				result[syncUntrustedModels].(map[string]any)[name] = true
			}
		}
	}
	for _, field := range pricingSyncFields {
		raw, exists := data[field]
		if !exists {
			continue
		}
		entries := valueMap(raw)
		if entries == nil {
			return nil, fmt.Errorf("invalid upstream %s map", field)
		}
		normalized := make(map[string]any, len(entries))
		for name, value := range entries {
			if strings.TrimSpace(name) == "" {
				return nil, fmt.Errorf("invalid upstream model name")
			}
			if numericPricingSyncFields[field] {
				number, ok := asFloat64(value)
				if !ok || !isValidNonNegativeCost(number) {
					return nil, fmt.Errorf("invalid upstream %s price for %s", field, name)
				}
				var err error
				if field == "model_ratio" {
					number, err = scaleSyncPrice(number, targetK, units.creditsPerUSD)
				}
				if field == "model_price" && !units.canonical {
					number, err = scaleSyncPrice(number, 1, units.legacyUnitsPerUSD)
				}
				if err != nil {
					return nil, err
				}
				normalized[name] = number
			} else {
				text, ok := value.(string)
				if !ok {
					return nil, fmt.Errorf("invalid upstream %s value", field)
				}
				if field == billing_setting.BillingExprField {
					var err error
					text, err = syncUSDExpression(text, units)
					if err != nil {
						return nil, err
					}
				} else if text != "" && text != billing_setting.BillingModeRatio && text != billing_setting.BillingModeTieredExpr {
					return nil, fmt.Errorf("invalid upstream billing mode")
				}
				normalized[name] = text
			}
		}
		result[field] = normalized
	}
	for name, mode := range valueMap(result[billing_setting.BillingModeField]) {
		expr, _ := valueMap(result[billing_setting.BillingExprField])[name].(string)
		if mode == billing_setting.BillingModeTieredExpr && strings.TrimSpace(expr) == "" {
			skipPricingSyncModel(result, name, "tiered source is missing its billing expression")
		}
	}
	for name, value := range valueMap(result[billing_setting.BillingExprField]) {
		expr, _ := value.(string)
		if strings.TrimSpace(expr) == "" {
			continue
		}
		mode := valueMap(result[billing_setting.BillingModeField])[name]
		if mode == nil {
			skipPricingSyncModel(result, name, "billing expression is missing its active billing mode")
		} else if mode != billing_setting.BillingModeTieredExpr {
			// A retained inactive expression does not establish the source's
			// active billing contract and must not become a selectable quote.
			delete(valueMap(result[billing_setting.BillingExprField]), name)
		}
	}
	return result, nil
}

func decodeUpstreamPricingData(bodyBytes []byte, targetK float64) (map[string]any, error) {
	if err := validateSyncJSONNumbers(bodyBytes); err != nil {
		return nil, err
	}
	var envelope pricingSyncEnvelope
	if err := json.Unmarshal(bodyBytes, &envelope); err != nil {
		return nil, err
	}
	if !envelope.Success {
		return nil, fmt.Errorf("upstream pricing request failed: %s", envelope.Message)
	}
	units, err := syncSourceUnits(envelope.PricingSyncMetadata)
	if err != nil {
		return nil, err
	}
	var maps map[string]any
	if json.Unmarshal(envelope.Data, &maps) == nil && maps != nil {
		return normalizeUpstreamSyncData(maps, units, targetK)
	}
	var rows []struct {
		ModelName            string   `json:"model_name"`
		QuotaType            int      `json:"quota_type"`
		ModelRatio           *float64 `json:"model_ratio"`
		ModelPrice           *float64 `json:"model_price"`
		CompletionRatio      *float64 `json:"completion_ratio"`
		InputPrice           *float64 `json:"input_price"`
		OutputPrice          *float64 `json:"output_price"`
		CacheReadPrice       *float64 `json:"cache_read_price"`
		CacheWritePrice      *float64 `json:"cache_write_price"`
		ImagePrice           *float64 `json:"image_price"`
		AudioInputPrice      *float64 `json:"audio_input_price"`
		AudioOutputPrice     *float64 `json:"audio_output_price"`
		CacheRatio           *float64 `json:"cache_ratio"`
		CreateCacheRatio     *float64 `json:"create_cache_ratio"`
		ImageRatio           *float64 `json:"image_ratio"`
		AudioRatio           *float64 `json:"audio_ratio"`
		AudioCompletionRatio *float64 `json:"audio_completion_ratio"`
		BillingMode          string   `json:"billing_mode"`
		BillingExpr          string   `json:"billing_expr"`
		PricingSyncMetadata
	}
	if err := json.Unmarshal(envelope.Data, &rows); err != nil || rows == nil {
		return nil, fmt.Errorf("unrecognized upstream pricing data")
	}
	data := make(map[string]any)
	seenModels := make(map[string]bool, len(rows))
	put := func(field, name string, value any) {
		if data[field] == nil {
			data[field] = make(map[string]any)
		}
		data[field].(map[string]any)[name] = value
	}
	for _, row := range rows {
		if strings.TrimSpace(row.ModelName) == "" {
			return nil, fmt.Errorf("invalid upstream model name")
		}
		if seenModels[row.ModelName] {
			return nil, fmt.Errorf("duplicate upstream model pricing row %s", row.ModelName)
		}
		seenModels[row.ModelName] = true
		rowUnits := units
		if row.PricingSchemaVersion != nil || row.SchemaVersion != nil || row.PricingCurrency != "" || row.Currency != "" || row.PricingStorageBasis != "" || row.StorageBasis != "" || row.CreditsPerUSD != nil || row.hasDenominationMetadata() || row.ModelRatioUnit != "" || row.LegacyPricingQuotaPerUnit != nil || row.QuotaPerUnit != nil || row.LegacyPricingUnitsPerUSD != nil {
			meta := row.PricingSyncMetadata
			if meta.PricingSchemaVersion == nil && meta.SchemaVersion == nil {
				meta.PricingSchemaVersion, meta.SchemaVersion = envelope.PricingSchemaVersion, envelope.SchemaVersion
			}
			if meta.PricingCurrency == "" && meta.Currency == "" {
				meta.PricingCurrency, meta.Currency = envelope.PricingCurrency, envelope.Currency
			}
			if meta.PricingStorageBasis == "" && meta.StorageBasis == "" {
				meta.PricingStorageBasis, meta.StorageBasis = envelope.PricingStorageBasis, envelope.StorageBasis
			}
			if meta.CreditsPerUSD == nil {
				meta.CreditsPerUSD = envelope.CreditsPerUSD
			}
			if meta.LedgerQuotaPerUSD == nil {
				meta.LedgerQuotaPerUSD = envelope.LedgerQuotaPerUSD
			}
			if meta.PublicCreditsPerUSD == nil {
				meta.PublicCreditsPerUSD = envelope.PublicCreditsPerUSD
			}
			if meta.LedgerQuotaPerUSDExact == "" {
				meta.LedgerQuotaPerUSDExact = envelope.LedgerQuotaPerUSDExact
			}
			if meta.PublicCreditsPerUSDExact == "" {
				meta.PublicCreditsPerUSDExact = envelope.PublicCreditsPerUSDExact
			}
			if meta.LegacyCreditUnit == "" {
				meta.LegacyCreditUnit = envelope.LegacyCreditUnit
			}
			if meta.CreditUnitSchemaVersion == nil {
				meta.CreditUnitSchemaVersion = envelope.CreditUnitSchemaVersion
			}
			if meta.QuotaUnit == "" {
				meta.QuotaUnit = envelope.QuotaUnit
			}
			if meta.PublicCreditUnit == "" {
				meta.PublicCreditUnit = envelope.PublicCreditUnit
			}
			if meta.ModelRatioUnit == "" {
				meta.ModelRatioUnit = envelope.ModelRatioUnit
			}
			if meta.LegacyPricingUnitsPerUSD == nil {
				meta.LegacyPricingUnitsPerUSD = envelope.LegacyPricingUnitsPerUSD
			}
			if meta.LegacyPricingQuotaPerUnit == nil && meta.QuotaPerUnit == nil {
				meta.LegacyPricingQuotaPerUnit, meta.QuotaPerUnit = envelope.LegacyPricingQuotaPerUnit, envelope.QuotaPerUnit
			}
			rowUnits, err = syncSourceUnits(meta)
			if err != nil {
				return nil, err
			}
			if (envelope.PricingSchemaVersion != nil || envelope.SchemaVersion != nil) && units.canonical != rowUnits.canonical {
				return nil, fmt.Errorf("conflicting upstream row schema")
			}
		}
		rowData := make(map[string]any)
		if row.BillingMode != "" && row.BillingMode != billing_setting.BillingModeRatio && row.BillingMode != billing_setting.BillingModeTieredExpr {
			return nil, fmt.Errorf("unsupported upstream billing mode")
		}
		add := func(field string, value any) { rowData[field] = map[string]any{row.ModelName: value} }
		if row.BillingMode == billing_setting.BillingModeTieredExpr {
			if strings.TrimSpace(row.BillingExpr) == "" {
				return nil, fmt.Errorf("upstream tiered price requires an expression")
			}
			add(billing_setting.BillingModeField, row.BillingMode)
			add(billing_setting.BillingExprField, row.BillingExpr)
		} else if row.QuotaType == 1 {
			if row.ModelPrice == nil {
				return nil, fmt.Errorf("missing upstream fixed price")
			}
			add("model_price", *row.ModelPrice)
		} else if row.QuotaType == 0 {
			if rowUnits.canonical {
				if row.InputPrice == nil {
					return nil, fmt.Errorf("missing canonical upstream input_price")
				}
				for _, quote := range []struct {
					price *float64
					ratio **float64
				}{{row.OutputPrice, &row.CompletionRatio}, {row.CacheReadPrice, &row.CacheRatio}, {row.CacheWritePrice, &row.CreateCacheRatio}, {row.ImagePrice, &row.ImageRatio}, {row.AudioInputPrice, &row.AudioRatio}} {
					*quote.ratio, err = canonicalSyncRatio(quote.price, *row.InputPrice, *quote.ratio)
					if err != nil {
						return nil, err
					}
				}
				audioInput := *row.InputPrice
				if row.AudioRatio != nil {
					audioInput, err = model.USDPriceProduct(audioInput, *row.AudioRatio)
					if err != nil {
						return nil, err
					}
				}
				row.AudioCompletionRatio, err = canonicalSyncRatio(row.AudioOutputPrice, audioInput, row.AudioCompletionRatio)
				if err != nil {
					return nil, err
				}
				ratio, err := scaleSyncPrice(*row.InputPrice, targetK, 1000000)
				if err != nil {
					return nil, err
				}
				// Already calibrated for this target; prevent a second source-K conversion.
				rowUnits.creditsPerUSD = targetK
				add("model_ratio", ratio)
			} else {
				if row.ModelRatio == nil {
					return nil, fmt.Errorf("missing upstream model ratio")
				}
				add("model_ratio", *row.ModelRatio)
			}
			if row.CompletionRatio == nil {
				return nil, fmt.Errorf("missing upstream completion ratio")
			}
			add("completion_ratio", *row.CompletionRatio)
		} else {
			return nil, fmt.Errorf("unsupported upstream quota type")
		}
		for field, value := range map[string]*float64{"cache_ratio": row.CacheRatio, "create_cache_ratio": row.CreateCacheRatio, "image_ratio": row.ImageRatio, "audio_ratio": row.AudioRatio, "audio_completion_ratio": row.AudioCompletionRatio} {
			if value != nil {
				add(field, *value)
			}
		}
		normalized, err := normalizeUpstreamSyncData(rowData, rowUnits, targetK)
		if err != nil {
			return nil, err
		}
		for field, raw := range normalized {
			for name, value := range valueMap(raw) {
				put(field, name, value)
			}
		}
	}
	return data, nil
}

// A flat source cannot establish a replacement for existing tiered/modality
// billing. Keep the complete local expression and report the gap instead of
// silently turning the model into a cheaper flat or partial quote.
func protectPricingSyncShapes(local, upstream map[string]any) {
	currentModes, currentExprs := valueMap(local[billing_setting.BillingModeField]), valueMap(local[billing_setting.BillingExprField])
	for name, mode := range currentModes {
		if mode != billing_setting.BillingModeTieredExpr {
			continue
		}
		oldExpr, _ := currentExprs[name].(string)
		if strings.TrimSpace(oldExpr) == "" {
			continue
		}
		present := false
		for _, field := range pricingSyncFields {
			if _, ok := valueMap(upstream[field])[name]; ok {
				present = true
				break
			}
		}
		if !present {
			continue
		}
		nextExpr, _ := valueMap(upstream[billing_setting.BillingExprField])[name].(string)
		reason := "numeric-only source cannot replace existing tiered billing"
		if valueMap(upstream[billing_setting.BillingModeField])[name] == billing_setting.BillingModeTieredExpr && strings.TrimSpace(nextExpr) != "" {
			reason = ""
			before, after := billingexpr.UsedVars(oldExpr), billingexpr.UsedVars(nextExpr)
			for _, dimension := range []string{"len", "cr", "cc", "cc1h", "img", "ai", "ao", "cr_text", "cr_img", "cr_audio", "audio_s"} {
				if before[dimension] && !after[dimension] {
					reason = "upstream expression omits existing billing dimension " + dimension
					break
				}
			}
		}
		if reason == "" {
			continue
		}
		skipPricingSyncModel(upstream, name, reason)
	}
}
