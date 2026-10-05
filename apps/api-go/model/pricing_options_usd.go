package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"reflect"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrPricingRevisionConflict = errors.New("pricing changed; reload current prices before saving")
var ErrPricingUnitsStale = errors.New("pricing currency cache does not match stored calibration; wait for settings reload")

// The durable baseline is mandatory after initialization. The shared DB row
// lock also catches an old rolling node writing the current calibration.
func validateAuthoritativePricingUnits(db *gorm.DB) error {
	if db == nil {
		return common.ErrCreditUnitsUnavailable
	}
	if math.IsNaN(common.QuotaPerUnit) || math.IsInf(common.QuotaPerUnit, 0) || common.QuotaPerUnit <= 0 {
		return ErrPricingUnitsStale
	}
	// Acquire the shared calibration fence first, matching initializer and
	// ordinary option writers' QPU-before-price-policy lock ordering.
	var calibration Option
	if err := db.Clauses(clause.Locking{Strength: "SHARE"}).Where("key = ?", "QuotaPerUnit").First(&calibration).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrPricingUnitsStale
		}
		return err
	}
	legacy, err := decimal.NewFromString(calibration.Value)
	if err != nil || !legacy.IsPositive() {
		return ErrPricingUnitsStale
	}
	var rows []Option
	if err := db.Clauses(clause.Locking{Strength: "SHARE"}).Where("key IN ?", []string{CreditsPerUSDOptionKey, LegacyPricingQuotaPerUnitOptionKey}).Find(&rows).Error; err != nil {
		return err
	}
	var durableAnchor, durableBaseline decimal.Decimal
	anchorFound, baselineFound := false, false
	for _, row := range rows {
		value, err := decimal.NewFromString(row.Value)
		if err != nil || !value.IsPositive() {
			return ErrPricingUnitsStale
		}
		if row.Key == CreditsPerUSDOptionKey {
			durableAnchor = value
			anchorFound = true
		} else {
			durableBaseline = value
			baselineFound = true
		}
	}
	anchor, err := common.CreditsPerUSD()
	if err != nil || !anchorFound {
		return common.ErrCreditUnitsUnavailable
	}
	baseline, err := common.LegacyPricingQuotaPerUnit()
	if err != nil || !baselineFound {
		return common.ErrCreditUnitsUnavailable
	}
	if !anchor.Equal(durableAnchor) || !baseline.Equal(durableBaseline) || !legacy.Equal(durableBaseline) || !legacy.Equal(decimal.NewFromFloat(common.QuotaPerUnit)) {
		return ErrPricingUnitsStale
	}
	return nil
}

// This endpoint is separate from legacy /option writers so an old binary
// cannot silently accept a USD payload as legacy pricing units.
type USDPriceConfig struct {
	common.CreditDenomination
	ModelRatioUnit           string             `json:"model_ratio_unit"`
	SchemaVersion            int                `json:"schema_version"`
	Currency                 string             `json:"currency"`
	StorageBasis             string             `json:"storage_basis"`
	Revision                 string             `json:"revision"`
	CreditsPerUSD            float64            `json:"credits_per_usd"`
	LegacyPricingUnitsPerUSD float64            `json:"legacy_pricing_units_per_usd"`
	ModelRatioUSDPerMillion  float64            `json:"model_ratio_usd_per_million"`
	Values                   map[string]string  `json:"values"`
	ToolPriceDefaults        map[string]float64 `json:"tool_price_defaults"`
}

type USDPriceUpdate struct {
	SchemaVersion    int               `json:"schema_version"`
	Currency         string            `json:"currency"`
	ExpectedRevision string            `json:"expected_revision"`
	Values           map[string]string `json:"values"`
}

func validateUSDPriceRequest(request USDPriceUpdate) error {
	if request.SchemaVersion != PricingSchemaUSD || request.Currency != PricingCurrencyUSD {
		return errors.New("schema_version:2 and currency:USD are required")
	}
	if len(request.ExpectedRevision) != 64 {
		return errors.New("expected_revision from the current pricing response is required")
	}
	if _, err := hex.DecodeString(request.ExpectedRevision); err != nil {
		return errors.New("invalid pricing revision")
	}
	if len(request.Values) == 0 || len(request.Values) > 12 {
		return errors.New("values must contain pricing option maps")
	}
	for key := range request.Values {
		if !isModelPriceOption(key) && key != ModelPriceLocksOptionKey {
			return fmt.Errorf("%s is not a pricing option", key)
		}
	}
	return nil
}

func priceSnapshotRevision(values map[string]string) (string, error) {
	anchor, err := common.CreditsPerUSD()
	if err != nil {
		return "", err
	}
	if _, err = common.LegacyPricingUnitsPerUSD(); err != nil {
		return "", err
	}
	baseline, err := common.LegacyPricingQuotaPerUnit()
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(struct {
		Values                       map[string]string
		Anchor, LegacyCreditsPerUnit string
	}{values, anchor.String(), baseline.String()})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func jsonEquivalent(a, b json.RawMessage) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

func rawPriceMap(text string) (map[string]json.RawMessage, error) {
	var values map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &values) != nil || values == nil {
		return nil, errors.New("pricing values must be JSON objects")
	}
	return values, nil
}

func monetaryPriceOption(key string) bool {
	return key == "ModelPrice" || key == operation_setting.ToolPriceOptionKey
}

func usdPriceConfig(values map[string]string) (USDPriceConfig, error) {
	anchor, err := common.CreditsPerUSD()
	if err != nil {
		return USDPriceConfig{}, err
	}
	if _, err = common.LegacyPricingUnitsPerUSD(); err != nil {
		return USDPriceConfig{}, err
	}
	baseline, err := common.LegacyPricingQuotaPerUnit()
	if err != nil {
		return USDPriceConfig{}, err
	}
	scale := anchor.DivRound(baseline, 64)
	revision, err := priceSnapshotRevision(values)
	if err != nil {
		return USDPriceConfig{}, err
	}
	result := USDPriceConfig{SchemaVersion: PricingSchemaUSD, Currency: PricingCurrencyUSD, StorageBasis: PricingStorageLegacy, Revision: revision, CreditsPerUSD: anchor.InexactFloat64(), LegacyPricingUnitsPerUSD: scale.InexactFloat64(), ModelRatioUSDPerMillion: decimal.NewFromInt(1_000_000).DivRound(anchor, 64).InexactFloat64(), Values: maps.Clone(values)}
	result.CreditDenomination, err = common.CreditDenominationMetadata()
	if err != nil {
		return USDPriceConfig{}, err
	}
	result.ModelRatioUnit = "LEDGER_QUOTA_PER_TOKEN"
	result.ToolPriceDefaults = make(map[string]float64)
	for name, price := range operation_setting.GetToolPriceDefaultsCopy() {
		usd, err := LegacyPricingAmountUSD(price)
		if err != nil {
			return USDPriceConfig{}, err
		}
		result.ToolPriceDefaults[name] = usd
	}
	for key, text := range values {
		if !monetaryPriceOption(key) && key != "billing_setting.billing_expr" {
			continue
		}
		entries, err := rawPriceMap(text)
		if err != nil {
			return USDPriceConfig{}, fmt.Errorf("%s: %w", key, err)
		}
		for name, raw := range entries {
			if monetaryPriceOption(key) {
				amount, err := decimal.NewFromString(string(raw))
				if err != nil || amount.IsNegative() {
					return USDPriceConfig{}, fmt.Errorf("invalid %s price for %s", key, name)
				}
				usd := amount.Mul(baseline).DivRound(anchor, 64)
				if amount.IsPositive() && usd.IsZero() {
					return USDPriceConfig{}, fmt.Errorf("positive %s price for %s is outside the representable range", key, name)
				}
				if _, err := pricingFiniteFloat(usd); err != nil {
					return USDPriceConfig{}, err
				}
				entries[name] = json.RawMessage(usd.String())
			} else {
				var expr string
				if json.Unmarshal(raw, &expr) != nil {
					return USDPriceConfig{}, fmt.Errorf("invalid expression for %s", name)
				}
				normalized, err := USDExpression(expr)
				if err != nil {
					return USDPriceConfig{}, err
				}
				entries[name], _ = json.Marshal(normalized)
			}
		}
		encoded, err := json.Marshal(entries)
		if err != nil {
			return USDPriceConfig{}, err
		}
		result.Values[key] = string(encoded)
	}
	return result, nil
}

// Only intentional edits cross the USD/storage bridge. Unchanged entries keep
// their original raw JSON numbers/expressions; unchanged maps are not written.
func convertUSDPriceValues(request USDPriceUpdate, current map[string]string) (map[string]string, error) {
	if err := validateUSDPriceRequest(request); err != nil {
		return nil, err
	}
	canonical, err := usdPriceConfig(current)
	if err != nil {
		return nil, err
	}
	if request.ExpectedRevision != canonical.Revision {
		return nil, ErrPricingRevisionConflict
	}
	anchor, err := common.CreditsPerUSD()
	if err != nil {
		return nil, err
	}
	baseline, err := common.LegacyPricingQuotaPerUnit()
	if err != nil {
		return nil, err
	}
	scale := anchor.DivRound(baseline, 64)
	accepted := make(map[string]string)
	for key, text := range request.Values {
		proposed, err := rawPriceMap(text)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		oldCanonical, err := rawPriceMap(canonical.Values[key])
		if err != nil {
			return nil, err
		}
		if jsonEquivalent(json.RawMessage(text), json.RawMessage(canonical.Values[key])) {
			continue
		}
		oldRaw, err := rawPriceMap(current[key])
		if err != nil {
			return nil, err
		}
		for name, raw := range proposed {
			if previous, ok := oldCanonical[name]; ok && jsonEquivalent(raw, previous) {
				proposed[name] = oldRaw[name]
				continue
			}
			if monetaryPriceOption(key) {
				usd, err := decimal.NewFromString(string(raw))
				if err != nil || usd.IsNegative() {
					return nil, fmt.Errorf("invalid USD %s for %s", key, name)
				}
				legacy := usd.Mul(anchor).DivRound(baseline, 64)
				if usd.IsPositive() && legacy.IsZero() {
					return nil, fmt.Errorf("positive USD %s for %s is outside the representable range", key, name)
				}
				if _, err := pricingFiniteFloat(legacy); err != nil {
					return nil, err
				}
				proposed[name] = json.RawMessage(legacy.String())
			} else if key == "billing_setting.billing_expr" {
				var expr string
				if json.Unmarshal(raw, &expr) != nil {
					return nil, fmt.Errorf("invalid USD expression for %s", name)
				}
				if expr != "" {
					expr = wrapPricingExpression(expr, "*", scale.String())
				}
				proposed[name], _ = json.Marshal(expr)
			}
		}
		encoded, err := json.Marshal(proposed)
		if err != nil {
			return nil, err
		}
		accepted[key] = string(encoded)
	}
	return accepted, nil
}

func GetUSDPriceConfig() (USDPriceConfig, error) {
	optionUpdateMutex.Lock()
	defer optionUpdateMutex.Unlock()
	if DB == nil {
		return USDPriceConfig{}, common.ErrCreditUnitsUnavailable
	}
	var result USDPriceConfig
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := validateAuthoritativePricingUnits(tx); err != nil {
			return err
		}
		values, err := loadPriceOptionSnapshot(tx)
		if err != nil {
			return err
		}
		units, err := CreditDenominationSnapshotForDB(tx)
		if err != nil {
			return err
		}
		result, err = usdPriceConfig(values)
		result.CreditDenomination = units
		return err
	})
	return result, err
}

func ValidateUSDPriceConfig(request USDPriceUpdate) (USDPriceConfig, OptionUpdateResult, error) {
	optionUpdateMutex.Lock()
	defer optionUpdateMutex.Unlock()
	if err := validateAuthoritativePricingUnits(DB); err != nil {
		return USDPriceConfig{}, OptionUpdateResult{}, err
	}
	current, err := loadPriceOptionSnapshot(DB)
	if err != nil {
		return USDPriceConfig{}, OptionUpdateResult{}, err
	}
	converted, err := convertUSDPriceValues(request, current)
	if err != nil {
		return USDPriceConfig{}, OptionUpdateResult{}, err
	}
	accepted, result, err := filterLockedModelPriceChanges(converted, current)
	if err != nil {
		return USDPriceConfig{}, result, err
	}
	if len(accepted) > 0 {
		if err = validateOptionValues(accepted); err != nil {
			return USDPriceConfig{}, result, err
		}
	}
	if err = validateModelPriceValues(accepted); err != nil {
		return USDPriceConfig{}, result, err
	}
	preview := maps.Clone(current)
	maps.Copy(preview, accepted)
	units, err := CreditDenominationSnapshot()
	if err != nil {
		return USDPriceConfig{}, result, err
	}
	config, err := usdPriceConfig(preview)
	config.CreditDenomination = units
	return config, result, err
}

func UpdateUSDPriceConfig(request USDPriceUpdate) (USDPriceConfig, OptionUpdateResult, error) {
	if err := validateUSDPriceRequest(request); err != nil {
		return USDPriceConfig{}, OptionUpdateResult{}, err
	}
	result, err := updateOptionsWithPriceLocksUSD(request.Values, "", false, &request)
	if err != nil {
		return USDPriceConfig{}, result, err
	}
	units, err := CreditDenominationSnapshot()
	if err != nil {
		return USDPriceConfig{}, result, err
	}
	config, err := usdPriceConfig(result.Pricing)
	config.CreditDenomination = units
	return config, result, err
}
