package service

import (
	"context"
	"errors"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

type merchantStoreRefundBroker struct {
	query    func(context.Context, *model.MerchantStoreOrder, MerchantStoreRefundNativeRequest) (MerchantStoreRefundProviderResult, error)
	customer func(context.Context, *model.MerchantStoreRefundDispatch) (*pancake.CustomerSession, error)
	linuxdo  func(context.Context, *model.MerchantStoreOrder, MerchantStoreRefundNativeRequest) (MerchantStoreRefundProviderResult, error)
}

func merchantStoreDefaultRefundBroker() merchantStoreRefundBroker {
	return merchantStoreRefundBroker{query: QueryMerchantStorePancakeRefund, customer: merchantStoreRefundCustomer, linuxdo: merchantStoreSubmitLinuxDORefund}
}

func merchantStoreDispatchNative(d *model.MerchantStoreRefundDispatch) MerchantStoreRefundNativeRequest {
	return MerchantStoreRefundNativeRequest{RefundID: d.Refund.ID, OrderID: d.Order.ID, AmountMinor: d.Refund.AmountMinor, Currency: d.Refund.Currency, Payment: MerchantStoreRefundNativePayment{ReceiptReference: d.Basis.ReceiptReference, PaymentReference: d.Basis.PaymentReference, AmountMinor: d.Basis.AmountMinor, Currency: d.Basis.Currency, EvidenceHash: d.Basis.EvidenceHash}}
}

func merchantStoreReconcileSavedRefund(d *model.MerchantStoreRefundDispatch) error {
	a := d.Attempt
	if a.State == "succeeded" && a.RefundReference != "" && a.EvidenceHash != "" {
		if d.Refund.RequestedRole == "provider" {
			return model.ReconcileMerchantStoreExternalRefund(d.Refund.ID)
		}
		return model.CompleteMerchantStoreVerifiedRefund(d.Refund.ID, model.MerchantStoreVerifiedRefundEvidence{PaymentReference: d.Basis.PaymentReference, RefundReference: a.RefundReference, AmountMinor: d.Refund.AmountMinor, Currency: d.Refund.Currency, EvidenceHash: a.EvidenceHash})
	}
	if a.State == "failed" && a.RefundReference != "" && a.EvidenceHash != "" {
		return model.RejectMerchantStoreVerifiedRefund(d.Refund.ID, model.MerchantStoreVerifiedRefundFailure{PaymentReference: d.Basis.PaymentReference, RefundReference: a.RefundReference, RequestedAmountMinor: d.Refund.AmountMinor, Currency: d.Refund.Currency, EvidenceHash: a.EvidenceHash})
	}
	return nil
}

func merchantStoreRecordRefundResult(d *model.MerchantStoreRefundDispatch, result MerchantStoreRefundProviderResult, now int64) error {
	state, code, reference, hash := string(result.State), result.Code, "", ""
	if proof, ok := result.VerifiedEvidence(); ok {
		reference, hash = proof.RefundReference, proof.EvidenceHash
	} else if proof, ok := result.VerifiedFailure(); ok {
		reference, hash = proof.RefundReference, proof.EvidenceHash
	} else if result.State == MerchantStoreRefundProviderSucceeded || result.State == MerchantStoreRefundProviderFailed {
		// Ticket accepted/rejected is still not a PSP execution proof.
		state = "pending"
	}
	if e := model.RecordMerchantStoreRefundObservation(d.Refund.ID, d.Attempt.LeaseToken, d.Attempt.RequestHash, state, code, result.TicketReference, reference, hash, now); e != nil {
		return e
	}
	d.Attempt.State, d.Attempt.RefundReference, d.Attempt.EvidenceHash = state, reference, hash
	return merchantStoreReconcileSavedRefund(d)
}

func (broker merchantStoreRefundBroker) process(ctx context.Context, id string) error {
	if ctx == nil || ctx.Err() != nil {
		return ErrMerchantStoreRefundProvider
	}
	now := time.Now().Unix()
	d, e := model.PrepareMerchantStoreRefundDispatch(id, now)
	if e != nil || d == nil {
		return e
	}
	if d.Attempt.State == "succeeded" || d.Attempt.State == "failed" {
		// Retry only local settlement with the already durable native proof.
		result := merchantStoreReconcileSavedRefund(d)
		_ = model.RecordMerchantStoreRefundObservation(id, d.Attempt.LeaseToken, d.Attempt.RequestHash, d.Attempt.State, "local_reconciliation", d.Attempt.TicketReference, d.Attempt.RefundReference, d.Attempt.EvidenceHash, now)
		return result
	}
	request := merchantStoreDispatchNative(d)
	if _, e = merchantStoreRefundValidateRequest(&d.Order, request); e != nil {
		_ = model.RecordMerchantStoreRefundObservation(id, d.Attempt.LeaseToken, d.Attempt.RequestHash, "manual", "original_payment_unavailable", "", "", "", now)
		return e
	}
	switch d.Order.PaymentMethod {
	case MerchantStorePlatformPancake, MerchantStoreExternalPancake:
		observed, queryErr := broker.query(ctx, &d.Order, request)
		if queryErr != nil && d.Attempt.SubmitCount == 0 && d.Attempt.State == "ready" {
			return model.RecordMerchantStoreRefundObservation(id, d.Attempt.LeaseToken, d.Attempt.RequestHash, "ready", "provider_query_unavailable", "", "", "", now)
		}
		if queryErr != nil || observed.Code != "refund_not_found" || d.Attempt.SubmitCount == 1 {
			if queryErr != nil {
				observed = MerchantStoreRefundProviderResult{State: MerchantStoreRefundProviderUnknown, Code: "provider_query_unavailable"}
			}
			return merchantStoreRecordRefundResult(d, observed, now)
		}
		params, e := BuildMerchantStorePancakeRefundTicket(&d.Order, request, d.Refund.Reason)
		if e != nil {
			return model.RecordMerchantStoreRefundObservation(id, d.Attempt.LeaseToken, d.Attempt.RequestHash, "manual", "invalid_frozen_request", "", "", "", now)
		}
		customer, e := broker.customer(ctx, d)
		if e != nil || customer == nil {
			// No refund POST has happened: preserve the ready state for a safe
			// auth/query retry, while retaining the same operation identity.
			return model.RecordMerchantStoreRefundObservation(id, d.Attempt.LeaseToken, d.Attempt.RequestHash, "ready", "customer_session_unavailable", "", "", "", now)
		}
		claimed, e := model.ClaimMerchantStoreRefundSubmit(id, d.Attempt.LeaseToken, d.Attempt.RequestHash, time.Now().Unix())
		if e != nil || !claimed {
			return e
		}
		ticket, e := customer.CreateRefundTicket(ctx, params)
		observed = MerchantStoreRefundProviderResult{State: MerchantStoreRefundProviderUnknown, Code: "submission_unknown"}
		if e == nil {
			observed, e = ObserveMerchantStorePancakeRefundTicket(&d.Order, request, ticket)
		}
		if e != nil {
			observed = MerchantStoreRefundProviderResult{State: MerchantStoreRefundProviderUnknown, Code: "submission_unknown"}
		}
		return merchantStoreRecordRefundResult(d, observed, time.Now().Unix())
	case MerchantStorePlatformLinuxDO:
		if d.Attempt.SubmitCount == 1 {
			return model.RecordMerchantStoreRefundObservation(id, d.Attempt.LeaseToken, d.Attempt.RequestHash, "manual", "manual_verification_required", "", "", "", now)
		}
		if request.AmountMinor != request.Payment.AmountMinor {
			return model.RecordMerchantStoreRefundObservation(id, d.Attempt.LeaseToken, d.Attempt.RequestHash, "manual", "provider_full_refund_only", "", "", "", now)
		}
		claimed, e := model.ClaimMerchantStoreRefundSubmit(id, d.Attempt.LeaseToken, d.Attempt.RequestHash, time.Now().Unix())
		if e != nil || !claimed {
			return e
		}
		observed, e := broker.linuxdo(ctx, &d.Order, request)
		if e != nil {
			observed = MerchantStoreRefundProviderResult{State: MerchantStoreRefundProviderUnknown, Code: "manual_verification_required"}
		}
		return merchantStoreRecordRefundResult(d, observed, time.Now().Unix())
	default:
		return model.RecordMerchantStoreRefundObservation(id, d.Attempt.LeaseToken, d.Attempt.RequestHash, "manual", "provider_refund_unavailable", "", "", "", now)
	}
}

func reconcileMerchantStoreRefundBatch(ctx context.Context, limit int) error {
	return merchantStoreDefaultRefundBroker().batch(ctx, limit)
}

func (broker merchantStoreRefundBroker) batch(ctx context.Context, limit int) error {
	ids, e := model.DueMerchantStoreRefundDispatches(ctx, time.Now().Unix(), limit)
	if e != nil {
		return e
	}
	var firstError error
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		requestCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		e = broker.process(requestCtx, id)
		cancel()
		if e != nil && !errors.Is(e, model.ErrMerchantStoreRefundUnsupported) && !errors.Is(e, model.ErrMerchantStoreWriterFrozen) {
			if firstError == nil {
				firstError = e
			}
		}
	}
	return firstError
}

// Only the seller/root can explicitly wake a provider lookup. The approved
// immutable refund row still controls whether a first submit is authorized.
func ReconcileMerchantStoreRefund(ctx context.Context, actor int, orderID, id string) (*model.MerchantStoreRefundView, error) {
	if e := model.AuthorizeMerchantStoreRefundReconciliation(actor, orderID, id); e != nil {
		return nil, e
	}
	if e := model.WakeMerchantStoreRefundDispatch(id); e != nil {
		return nil, e
	}
	e := merchantStoreDefaultRefundBroker().process(ctx, id)
	if e != nil {
		return nil, e
	}
	return model.GetMerchantStoreRefunds(actor, orderID)
}
