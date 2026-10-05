package controller

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/gin-gonic/gin"
)

func creditUnitMetadataFields() (gin.H, error) {
	units, err := common.CreditDenominationMetadata()
	if err != nil {
		return nil, err
	}
	return creditUnitMetadataFieldsFor(units), nil
}

func creditUnitMetadataFieldsFor(units common.CreditDenomination) gin.H {
	return gin.H{
		"credit_unit_schema_version":   units.CreditUnitSchemaVersion,
		"quota_unit":                   units.QuotaUnit,
		"public_credit_unit":           units.PublicCreditUnit,
		"legacy_credit_unit":           units.LegacyCreditUnit,
		"ledger_quota_per_usd":         units.LedgerQuotaPerUSD,
		"ledger_quota_per_usd_exact":   units.LedgerQuotaPerUSDExact,
		"public_credits_per_usd":       units.PublicCreditsPerUSD,
		"public_credits_per_usd_exact": units.PublicCreditsPerUSDExact,
	}
}

func addCreditUnitMetadata(fields gin.H) error {
	metadata, err := creditUnitMetadataFields()
	if err != nil {
		return err
	}
	for key, value := range metadata {
		fields[key] = value
	}
	return nil
}
