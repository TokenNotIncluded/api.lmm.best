package service

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"

	"github.com/LIghtJUNction/api.lmm.best/model"
)

// Missing actual channel charge is recoverable/manual; never substitute the
// quote, current exchange rate, total/list-price fallback or an ORD for a PAY.
func merchantStoreRecordPancakeRefundBasis(orderID string, payload []byte, signature string) error {
	// This helper runs only after the ordinary signature/scope/quote validator.
	// Older valid order events lack the new native-payment evidence fields and
	// must keep their payment ACK, without acquiring refund capabilities.
	var envelope struct {
		Data merchantStoreRefundWebhookData `json:"data"`
	}
	if json.Unmarshal(payload, &envelope) != nil {
		return ErrMerchantStoreRefundProvider
	}
	if envelope.Data.ChargedAmount == nil || envelope.Data.PaymentID == nil || envelope.Data.PaymentStatus == nil {
		return nil
	}
	o, e := model.GetMerchantStorePaymentOrder(orderID)
	if e != nil {
		return e
	}
	proof, e := VerifyMerchantStorePancakeRefundPaymentBasis(o, payload, signature)
	if errors.Is(e, ErrMerchantStoreRefundBasisUnavailable) {
		return nil
	}
	if e != nil {
		return e
	}
	p, ok := proof.VerifiedPayment()
	if !ok {
		return ErrMerchantStoreRefundProvider
	}
	e = model.RecordMerchantStoreRefundPaymentBasis(orderID, model.VerifiedMerchantStoreRefundPaymentBasis{ReceiptReference: p.ReceiptReference, PaymentReference: p.PaymentReference, AmountMinor: p.AmountMinor, Currency: p.Currency, EvidenceHash: p.EvidenceHash})
	if errors.Is(e, model.ErrMerchantStoreWriterFrozen) {
		return nil
	}
	return e
}

// Invoked only after the existing signed callback or authenticated query has
// proved exactly this native Linux DO charge. Its protocol has no tax/list-price
// distinction and identifies the original payment by trade_no, not Pancake PAY.
func merchantStoreRecordLinuxDORefundBasis(orderID, receipt string, verifiedMinor int64) error {
	o, e := model.GetMerchantStorePaymentOrder(orderID)
	if e != nil {
		return e
	}
	if o.PaymentMethod != MerchantStorePlatformLinuxDO || !merchantStoreRefundPaymentOrder(o) || o.ProviderTradeID != receipt || o.AmountMinor != verifiedMinor || o.Currency != "LDC" {
		return ErrMerchantStoreRefundProvider
	}
	frozen, e := loadMerchantStorePaymentContext(o)
	if e != nil {
		return e
	}
	facts, _ := json.Marshal([]any{receipt, verifiedMinor, o.Currency, o.TradeNo, frozen.Config.PartnerID, "linuxdo-verified-original-payment"})
	hash := merchantStoreRefundEvidenceHash("linuxdo-original-charge-v1", o, "", facts)
	e = model.RecordMerchantStoreRefundPaymentBasis(orderID, model.VerifiedMerchantStoreRefundPaymentBasis{ReceiptReference: receipt, PaymentReference: receipt, AmountMinor: verifiedMinor, Currency: o.Currency, EvidenceHash: hash})
	if errors.Is(e, model.ErrMerchantStoreWriterFrozen) {
		return nil
	}
	return e
}

func merchantStoreHandleRefundNotification(ctx context.Context, order *model.MerchantStoreOrder, payload []byte, signature, refundID string) error {
	d, e := model.GetMerchantStoreRefundDispatchSnapshot(refundID)
	if errors.Is(e, model.ErrMerchantStoreWriterFrozen) {
		return ErrMerchantStorePaymentIgnored
	}
	if errors.Is(e, gorm.ErrRecordNotFound) || errors.Is(e, model.ErrMerchantStoreInput) || errors.Is(e, model.ErrMerchantStoreConflict) {
		return merchantStoreHandleExternalRefundNotification(ctx, order, payload, signature)
	}
	if e != nil || d == nil || d.Order.ID != order.ID {
		return ErrMerchantStorePaymentVerification
	}
	_, e = VerifyMerchantStorePancakeRefundNotification(order, merchantStoreDispatchNative(d), payload, signature)
	if e != nil {
		return ErrMerchantStorePaymentVerification
	}
	// Only schedule an authenticated authority query. Neither a signed delivery
	// ID nor a ticket acceptance grants a native-completion proof.
	return model.NoteMerchantStoreRefundNotification(refundID)
}
