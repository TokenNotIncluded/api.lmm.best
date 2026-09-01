package model

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompanyBillingProfileSaveNormalizesAndScopesByOwner(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&CompanyBillingProfile{}))

	profile, err := SaveCompanyBillingProfile(41, CompanyBillingProfileInput{
		Country:        " us ",
		IsBusiness:     true,
		Postcode:       " 10001 ",
		State:          " NY ",
		BusinessName:   " Example Company ",
		TaxID:          " TAX-41 ",
		UseForInvoices: false,
	})
	require.NoError(t, err)
	require.Equal(t, "US", profile.Country)
	require.Equal(t, "10001", profile.Postcode)
	require.Equal(t, "NY", profile.State)
	require.Equal(t, "Example Company", profile.BusinessName)
	require.Equal(t, "TAX-41", profile.TaxID)
	require.False(t, profile.UseForInvoices)

	_, err = GetCompanyBillingProfile(42)
	require.ErrorIs(t, err, ErrCompanyBillingProfileNotFound)
	automatic, err := GetCompanyBillingProfileForAutomaticBilling(41)
	require.NoError(t, err)
	require.Nil(t, automatic)
}

func TestCompanyBillingProfileValidationNeverEchoesSensitiveValues(t *testing.T) {
	sensitiveName := strings.Repeat("N", CompanyBillingBusinessNameMaxRunes+1)
	_, err := NormalizeAndValidateCompanyBillingProfile(CompanyBillingProfileInput{
		Country:      "US",
		BusinessName: sensitiveName,
		TaxID:        "sensitive-tax-value",
	})
	require.Error(t, err)
	require.NotContains(t, err.Error(), sensitiveName)
	require.NotContains(t, err.Error(), "sensitive-tax-value")

	_, err = NormalizeAndValidateCompanyBillingProfile(CompanyBillingProfileInput{Country: "ZZ"})
	require.Error(t, err)
	var fieldError *CompanyBillingProfileFieldError
	require.True(t, errors.As(err, &fieldError))
	require.Equal(t, "country", fieldError.Field)
	require.Equal(t, "invalid_country", fieldError.Code)
}

func TestCompanyBillingProfileRequiredFieldsFailClosed(t *testing.T) {
	profile := &CompanyBillingProfile{
		Country:        "US",
		IsBusiness:     true,
		BusinessName:   "Example Company",
		TaxID:          "TAX-41",
		UseForInvoices: true,
	}

	err := ValidateCompanyBillingProfileRequiredFields(profile, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requiredFields")

	err = ValidateCompanyBillingProfileRequiredFields(profile, []string{"state"})
	require.Error(t, err)
	var fieldError *CompanyBillingProfileFieldError
	require.True(t, errors.As(err, &fieldError))
	require.Equal(t, "state", fieldError.Field)
	require.Equal(t, "required", fieldError.Code)

	err = ValidateCompanyBillingProfileRequiredFields(profile, []string{"providerInventedField"})
	require.Error(t, err)
	require.True(t, errors.As(err, &fieldError))
	require.Equal(t, "requiredFields", fieldError.Field)
	require.Equal(t, "unsupported_field", fieldError.Code)

	require.NoError(t, ValidateCompanyBillingProfileRequiredFields(profile, []string{}))
}
