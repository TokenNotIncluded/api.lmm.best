package controller

import (
	"context"
	"errors"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	waffoorder "github.com/waffo-com/waffo-go/types/order"
)

var previewWaffoPancakeCompanyBillingRules = service.PreviewWaffoPancakeTaxRules

func loadAutomaticCompanyBillingProfile(userID int) (*model.CompanyBillingProfile, error) {
	return model.GetCompanyBillingProfileForAutomaticBilling(userID)
}

// Legacy waffo-go v1.3.2 supports only AddressInfo. It has no company identity
// fields and no preview-tax API, so production validates only the supported
// address fields and never sends businessName, taxId, or isBusiness.
func validateLegacyWaffoCompanyBilling(profile *model.CompanyBillingProfile) error {
	if profile == nil || !profile.UseForInvoices {
		return nil
	}
	return model.ValidateCompanyBillingAddress(profile.Country, profile.State, profile.Postcode)
}

func applyCompanyBillingToLegacyWaffoOrder(params *waffoorder.CreateOrderParams, profile *model.CompanyBillingProfile) {
	if params == nil || profile == nil || !profile.UseForInvoices {
		return
	}
	params.AddressInfo = &waffoorder.AddressInfo{
		BillingAddress: &waffoorder.Address{
			Country:    profile.Country,
			State:      profile.State,
			PostalCode: profile.Postcode,
		},
	}
}

func validateWaffoPancakeCompanyBilling(
	ctx context.Context,
	session *service.WaffoPancakeCheckoutSession,
	profile *model.CompanyBillingProfile,
) error {
	billing := waffoPancakeBillingDetailFromProfile(profile)
	if billing == nil {
		return nil
	}
	requiredFields, err := previewWaffoPancakeCompanyBillingRules(ctx, session, *billing)
	if err != nil {
		return err
	}
	return model.ValidateCompanyBillingProfileRequiredFields(profile, requiredFields)
}

func waffoPancakeCompanyBillingFailureReason(err error) model.PaymentOrderFailureReason {
	if errors.Is(err, service.ErrWaffoPancakeTaxPreviewUnavailable) {
		return model.PaymentOrderFailureCompanyBillingPreview
	}
	var fieldError *model.CompanyBillingProfileFieldError
	if errors.As(err, &fieldError) {
		switch fieldError.Code {
		case "required":
			return model.PaymentOrderFailureCompanyBillingRequiredFields
		case "preview_unavailable":
			return model.PaymentOrderFailureCompanyBillingPreview
		}
	}
	return model.PaymentOrderFailureCompanyBillingRules
}

// A locally failed/expired checkout is terminal. Signed provider events are
// acknowledged but cannot recover it, so wallet credit and subscription
// activation remain consistent and idempotent.
func waffoPancakeRejectsLateSettlement(status string) bool {
	return status != common.TopUpStatusPending && status != common.TopUpStatusSuccess
}

func waffoPancakeBillingDetailFromProfile(profile *model.CompanyBillingProfile) *service.WaffoPancakeBillingDetail {
	if profile == nil || !profile.UseForInvoices {
		return nil
	}
	return &service.WaffoPancakeBillingDetail{
		Country:      profile.Country,
		IsBusiness:   profile.IsBusiness,
		Postcode:     profile.Postcode,
		State:        profile.State,
		BusinessName: profile.BusinessName,
		TaxID:        profile.TaxID,
	}
}
