package controller

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// Capture the denomination once per response or write boundary. Administrators
// can change the public denomination while a request is in flight; the ledger
// basis, converted input and its receipt must still describe the same amount.
type creditBoundaryBasis struct {
	Metadata common.CreditDenomination
}

func captureCreditBoundaryBasis() (creditBoundaryBasis, error) {
	units, err := common.CreditDenominationMetadata()
	if err != nil {
		return creditBoundaryBasis{}, err
	}
	if _, err := units.ProjectLedgerQuota(0); err != nil {
		return creditBoundaryBasis{}, err
	}
	return creditBoundaryBasis{Metadata: units}, nil
}

func (basis creditBoundaryBasis) publicAmount(quota int64) (decimal.Decimal, error) {
	return basis.Metadata.ProjectLedgerQuota(quota)
}

func (basis creditBoundaryBasis) addMetadata(fields gin.H) {
	for key, value := range creditUnitMetadataFieldsFor(basis.Metadata) {
		fields[key] = value
	}
}

func (basis creditBoundaryBasis) ledgerAmount(unit string, amount decimal.Decimal) (int, error) {
	if !amount.IsPositive() || amount.Exponent() < -18 || amount.Exponent() > 18 || len(amount.Coefficient().String()) > 80 {
		return 0, model.ErrWalletTransferInvalid
	}
	var quota decimal.Decimal
	switch unit {
	case common.PublicCreditUnit:
		// Match the public input helper's floor policy. Dust may not grant more
		// ledger value than the explicitly requested public amount.
		value, err := basis.Metadata.ResolvePublicCredits(amount)
		if err != nil {
			if errors.Is(err, common.ErrCreditUnitsUnavailable) {
				return 0, err
			}
			return 0, model.ErrWalletTransferInvalid
		}
		quota = decimal.NewFromInt(value)
	case common.LedgerQuotaUnit:
		if !amount.IsInteger() {
			return 0, model.ErrWalletTransferInvalid
		}
		quota = amount
	default:
		return 0, model.ErrWalletTransferInvalid
	}
	if !quota.IsPositive() || quota.GreaterThan(decimal.NewFromInt(common.MaxWalletQuota)) {
		return 0, model.ErrWalletTransferInvalid
	}
	value := quota.IntPart()
	if int64(int(value)) != value || common.ValidateWalletQuota(int(value)) != nil {
		return 0, model.ErrWalletTransferInvalid
	}
	return int(value), nil
}

type walletTransferCreateInput struct {
	Quota                       json.RawMessage `json:"quota"`
	SchemaVersion               json.RawMessage `json:"schema_version"`
	Amount                      json.RawMessage `json:"amount"`
	Unit                        json.RawMessage `json:"unit"`
	ExpectedPublicCreditsPerUSD json.RawMessage `json:"expected_public_credits_per_usd_exact"`
	RequestKey                  string          `json:"request_key"`
}

var errPublicCreditDenominationChanged = errors.New("public credit denomination changed")

func (input *walletTransferCreateInput) UnmarshalJSON(data []byte) error {
	fields, err := canonicalModelsDevObject(data)
	if err != nil {
		return model.ErrWalletTransferInvalid
	}
	versioned := false
	for key := range fields {
		switch strings.ToLower(key) {
		case "schema_version", "amount", "unit", "expected_public_credits_per_usd_exact":
			versioned = true
		}
	}
	if versioned {
		for key := range fields {
			switch key {
			case "quota", "schema_version", "amount", "unit", "request_key", "expected_public_credits_per_usd_exact":
			default:
				return model.ErrWalletTransferInvalid
			}
		}
	}
	type plainInput walletTransferCreateInput
	return json.Unmarshal(data, (*plainInput)(input))
}

func (input walletTransferCreateInput) ledgerQuota(basis creditBoundaryBasis) (int, error) {
	versioned := len(input.SchemaVersion) != 0 || len(input.Amount) != 0 || len(input.Unit) != 0 || len(input.ExpectedPublicCreditsPerUSD) != 0
	if !versioned {
		var quota int
		if len(input.Quota) == 0 || string(input.Quota) == "null" || json.Unmarshal(input.Quota, &quota) != nil || quota <= 0 || common.ValidateWalletQuota(quota) != nil {
			return 0, model.ErrWalletTransferInvalid
		}
		return quota, nil
	}
	if len(input.Quota) != 0 {
		return 0, model.ErrWalletTransferInvalid
	}
	var version int
	var amount, unit string
	if json.Unmarshal(input.SchemaVersion, &version) != nil || version != common.PublicCreditUnitSchemaVersion || json.Unmarshal(input.Amount, &amount) != nil || json.Unmarshal(input.Unit, &unit) != nil || amount == "" || len(amount) > 128 || amount != strings.TrimSpace(amount) {
		return 0, model.ErrWalletTransferInvalid
	}
	if unit == common.PublicCreditUnit {
		var expected string
		if json.Unmarshal(input.ExpectedPublicCreditsPerUSD, &expected) != nil || expected == "" {
			return 0, model.ErrWalletTransferInvalid
		}
		if expected != basis.Metadata.PublicCreditsPerUSDExact {
			return 0, errPublicCreditDenominationChanged
		}
	} else if len(input.ExpectedPublicCreditsPerUSD) != 0 {
		return 0, model.ErrWalletTransferInvalid
	}
	value, err := decimal.NewFromString(amount)
	if err != nil {
		return 0, model.ErrWalletTransferInvalid
	}
	return basis.ledgerAmount(unit, value)
}
