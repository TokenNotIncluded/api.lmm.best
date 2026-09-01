package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/stretchr/testify/require"
	waffoorder "github.com/waffo-com/waffo-go/types/order"
)

func enabledCompanyBillingProfile() *model.CompanyBillingProfile {
	return &model.CompanyBillingProfile{
		UserID:         41,
		Country:        "US",
		IsBusiness:     true,
		Postcode:       "10001",
		BusinessName:   "Example Company",
		TaxID:          "TAX-41",
		UseForInvoices: true,
	}
}

func TestCompanyBillingToggleOffDoesNotAttachOrPreview(t *testing.T) {
	profile := enabledCompanyBillingProfile()
	profile.UseForInvoices = false

	originalPancakePreview := previewWaffoPancakeCompanyBillingRules
	previewWaffoPancakeCompanyBillingRules = func(context.Context, *service.WaffoPancakeCheckoutSession, service.WaffoPancakeBillingDetail) ([]string, error) {
		t.Fatal("toggle-off profile must not call preview-tax")
		return nil, nil
	}
	t.Cleanup(func() {
		previewWaffoPancakeCompanyBillingRules = originalPancakePreview
	})

	params := &waffoorder.CreateOrderParams{}
	applyCompanyBillingToLegacyWaffoOrder(params, profile)
	require.Nil(t, params.AddressInfo)
	require.Nil(t, waffoPancakeBillingDetailFromProfile(profile))
	require.NoError(t, validateLegacyWaffoCompanyBilling(profile))
	require.NoError(t, validateWaffoPancakeCompanyBilling(context.Background(), nil, profile))
}

func TestCompanyBillingMissingProviderRequiredFieldRejectsPancake(t *testing.T) {
	profile := enabledCompanyBillingProfile()
	profile.State = ""

	originalPancakePreview := previewWaffoPancakeCompanyBillingRules
	previewWaffoPancakeCompanyBillingRules = func(_ context.Context, _ *service.WaffoPancakeCheckoutSession, billing service.WaffoPancakeBillingDetail) ([]string, error) {
		require.Equal(t, "US", billing.Country)
		return []string{"state"}, nil
	}
	t.Cleanup(func() {
		previewWaffoPancakeCompanyBillingRules = originalPancakePreview
	})

	err := validateWaffoPancakeCompanyBilling(context.Background(), &service.WaffoPancakeCheckoutSession{
		SessionID: "session", Token: "token",
	}, profile)
	require.Error(t, err)
	require.NotContains(t, err.Error(), profile.BusinessName)
	require.NotContains(t, err.Error(), profile.TaxID)
	require.Equal(t, model.PaymentOrderFailureCompanyBillingRequiredFields, waffoPancakeCompanyBillingFailureReason(err))
}

func TestCompanyBillingPreviewFailureAndUnknownRulesFailClosed(t *testing.T) {
	profile := enabledCompanyBillingProfile()
	originalPreview := previewWaffoPancakeCompanyBillingRules
	t.Cleanup(func() { previewWaffoPancakeCompanyBillingRules = originalPreview })

	previewWaffoPancakeCompanyBillingRules = func(context.Context, *service.WaffoPancakeCheckoutSession, service.WaffoPancakeBillingDetail) ([]string, error) {
		return nil, errors.New("provider unavailable")
	}
	require.Error(t, validateWaffoPancakeCompanyBilling(context.Background(), &service.WaffoPancakeCheckoutSession{}, profile))

	previewWaffoPancakeCompanyBillingRules = func(context.Context, *service.WaffoPancakeCheckoutSession, service.WaffoPancakeBillingDetail) ([]string, error) {
		return []string{"clientInventedRule"}, nil
	}
	require.Error(t, validateWaffoPancakeCompanyBilling(context.Background(), &service.WaffoPancakeCheckoutSession{}, profile))
}

func TestCompanyBillingToggleOnAttachesOnlyProviderSupportedFields(t *testing.T) {
	profile := enabledCompanyBillingProfile()
	profile.State = "NY"

	params := &waffoorder.CreateOrderParams{}
	require.NoError(t, validateLegacyWaffoCompanyBilling(profile))
	applyCompanyBillingToLegacyWaffoOrder(params, profile)
	require.NotNil(t, params.AddressInfo)
	require.Equal(t, "US", params.AddressInfo.BillingAddress.Country)
	require.Equal(t, "NY", params.AddressInfo.BillingAddress.State)
	require.Equal(t, "10001", params.AddressInfo.BillingAddress.PostalCode)
	// waffo-go v1.3.2 exposes no businessName/taxId/isBusiness fields.
	require.NotContains(t, common.GetJsonString(params.AddressInfo), profile.BusinessName)
	require.NotContains(t, common.GetJsonString(params.AddressInfo), profile.TaxID)

	billing := waffoPancakeBillingDetailFromProfile(profile)
	require.NotNil(t, billing)
	require.Equal(t, "Example Company", billing.BusinessName)
	require.Equal(t, "TAX-41", billing.TaxID)
}

func TestCompanyBillingFailureReasonClassificationIsStableAndNonSensitive(t *testing.T) {
	profile := enabledCompanyBillingProfile()
	profile.State = ""
	requiredErr := model.ValidateCompanyBillingProfileRequiredFields(profile, []string{"state"})
	require.Equal(t, model.PaymentOrderFailureCompanyBillingRequiredFields, waffoPancakeCompanyBillingFailureReason(requiredErr))
	require.Equal(t, model.PaymentOrderFailureCompanyBillingPreview, waffoPancakeCompanyBillingFailureReason(service.ErrWaffoPancakeTaxPreviewUnavailable))
	require.Equal(t, model.PaymentOrderFailureCompanyBillingRules, waffoPancakeCompanyBillingFailureReason(errors.New("deterministic provider rule mismatch")))
	for _, reason := range []model.PaymentOrderFailureReason{
		waffoPancakeCompanyBillingFailureReason(requiredErr),
		waffoPancakeCompanyBillingFailureReason(service.ErrWaffoPancakeTaxPreviewUnavailable),
	} {
		require.NotContains(t, string(reason), profile.BusinessName)
		require.NotContains(t, string(reason), profile.TaxID)
	}
}

func TestWaffoPancakeLateSettlementPolicyRejectsTerminalStates(t *testing.T) {
	require.False(t, waffoPancakeRejectsLateSettlement(common.TopUpStatusPending))
	require.False(t, waffoPancakeRejectsLateSettlement(common.TopUpStatusSuccess))
	require.True(t, waffoPancakeRejectsLateSettlement(common.TopUpStatusFailed))
	require.True(t, waffoPancakeRejectsLateSettlement(common.TopUpStatusExpired))
}
