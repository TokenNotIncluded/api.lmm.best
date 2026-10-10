package service

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/LIghtJUNction/api.lmm.best/model"
	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

// Query only the frozen original order. The order/payment tree supplies PAY
// ownership, and the execution list supplies actual PSP money, not ticket or
// list-price amounts. Both sets must agree completely; truncation fails closed.
// Fields: official SDK GraphQL guide sections 4 and 5. No mutation is used.
const merchantStorePancakeOrderRefundQuery = `query ($id: ID!, $ref: String!) {
    onetimeOrder(id: $id) {
        id currency testMode
        payments { id status refunds { id } }
    }
    refunds(limit: 100, filter: { orderMerchantExternalId: { eq: $ref } }) {
        id status orderMerchantExternalId refundTicketMerchantExternalId
        pspAmountDetails { amount currency }
    }
    refundsCount(filter: { orderMerchantExternalId: { eq: $ref } })
}`

type merchantStorePancakeOrderRefundData struct {
	OnetimeOrder *struct {
		ID       string `json:"id"`
		Currency string `json:"currency"`
		TestMode *bool  `json:"testMode"`
		Payments []struct {
			ID      string `json:"id"`
			Status  string `json:"status"`
			Refunds []struct {
				ID string `json:"id"`
			} `json:"refunds"`
		} `json:"payments"`
	} `json:"onetimeOrder"`
	Refunds      []merchantStorePancakeRefundQueryExecution `json:"refunds"`
	RefundsCount *int                                       `json:"refundsCount"`
}

type merchantStoreSyncedRefund struct {
	businessReference string
	evidence          model.MerchantStoreVerifiedRefundEvidence
}

func merchantStoreQueryOrderRefunds(ctx context.Context, d *model.MerchantStoreRefundDispatch, client *pancake.Client) ([]merchantStoreSyncedRefund, error) {
	if ctx == nil || ctx.Err() != nil || d == nil || client == nil {
		return nil, ErrMerchantStoreRefundProvider
	}
	// Use the existing strict frozen-order/basis validator without inventing an
	// external request amount. This private synthetic request is never sent.
	request := merchantStoreDispatchNative(d)
	request.RefundID, request.AmountMinor, request.Currency = "provider-sync", d.Basis.AmountMinor, d.Basis.Currency
	frozen, err := merchantStoreRefundValidateRequest(&d.Order, request)
	if err != nil {
		return nil, err
	}
	response, err := pancake.GraphQLQuery[merchantStorePancakeOrderRefundData](ctx, client, pancake.GraphQLParams{Query: merchantStorePancakeOrderRefundQuery, Variables: map[string]any{"id": d.Order.ProviderTradeID, "ref": d.Order.TradeNo}})
	if err != nil || response == nil || len(response.Errors) != 0 {
		return nil, ErrMerchantStoreRefundProvider
	}
	return merchantStoreVerifyOrderRefunds(d, frozen, response.Data)
}

func merchantStoreVerifyOrderRefunds(d *model.MerchantStoreRefundDispatch, frozen merchantStorePaymentContext, data merchantStorePancakeOrderRefundData) ([]merchantStoreSyncedRefund, error) {
	o := data.OnetimeOrder
	if o == nil || o.ID != d.Basis.ReceiptReference || o.Currency != d.Basis.Currency || o.TestMode == nil || *o.TestMode != (frozen.Config.Environment == "test") || len(o.Payments) < 1 || len(o.Payments) > 100 || data.RefundsCount == nil || *data.RefundsCount != len(data.Refunds) || len(data.Refunds) > 100 {
		return nil, ErrMerchantStoreRefundProvider
	}
	owners := map[string]string{}
	payments := map[string]bool{}
	for _, payment := range o.Payments {
		if !merchantStorePancakeShortID(payment.ID, "PAY") || payments[payment.ID] || len(payment.Refunds) > 100 {
			return nil, ErrMerchantStoreRefundProvider
		}
		payments[payment.ID] = true
		if payment.ID == d.Basis.PaymentReference && payment.Status != "succeeded" {
			return nil, ErrMerchantStoreRefundProvider
		}
		for _, refund := range payment.Refunds {
			if !merchantStoreProviderIDPattern.MatchString(refund.ID) || owners[refund.ID] != "" {
				return nil, ErrMerchantStoreRefundProvider
			}
			owners[refund.ID] = payment.ID
		}
	}
	if !payments[d.Basis.PaymentReference] || len(owners) != len(data.Refunds) {
		return nil, ErrMerchantStoreRefundProvider
	}
	results := []merchantStoreSyncedRefund{}
	seen := map[string]bool{}
	var total int64
	for _, execution := range data.Refunds {
		payment := owners[execution.ID]
		if payment == "" || seen[execution.ID] || execution.OrderMerchantExternalID != d.Order.TradeNo {
			return nil, ErrMerchantStoreRefundProvider
		}
		seen[execution.ID] = true
		if payment != d.Basis.PaymentReference || execution.Status != "succeeded" {
			continue
		}
		minor, err := merchantStoreMoneyToMinor(execution.PSPAmountDetails.Amount)
		if err != nil || minor > d.Basis.AmountMinor-total || execution.PSPAmountDetails.Currency != d.Basis.Currency {
			return nil, ErrMerchantStoreRefundProvider
		}
		total += minor
		facts, _ := json.Marshal([]any{d.Basis.EvidenceHash, d.Basis.ReceiptReference, payment, execution.ID, minor, d.Basis.Currency})
		proof := model.MerchantStoreVerifiedRefundEvidence{PaymentReference: payment, RefundReference: execution.ID, AmountMinor: minor, Currency: d.Basis.Currency,
			EvidenceHash: merchantStoreRefundEvidenceHash("pancake-external-execution-v1", &d.Order, "", facts)}
		results = append(results, merchantStoreSyncedRefund{businessReference: execution.RefundTicketMerchantExternalID, evidence: proof})
	}
	// Provider list ordering and repeated notifications cannot change the
	// receipt identity or lose the last fractional credit in partial refunds.
	sort.Slice(results, func(i, j int) bool { return results[i].evidence.RefundReference < results[j].evidence.RefundReference })
	return results, nil
}

func merchantStoreSyncOrderRefunds(ctx context.Context, orderID string) error {
	d, rows, err := model.GetMerchantStoreRefundSyncSnapshot(orderID)
	if err != nil {
		return err
	}
	frozen, err := loadMerchantStorePaymentContext(&d.Order)
	if err != nil {
		return err
	}
	client, err := newMerchantStorePancakeClient(frozen.Config, d.Order.ID)
	if err != nil {
		return ErrMerchantStoreRefundProvider
	}
	proofs, err := merchantStoreQueryOrderRefunds(ctx, d, client)
	if err != nil {
		return err
	}
	return merchantStoreApplySyncedRefunds(ctx, d, rows, proofs, client)
}

func merchantStoreApplySyncedRefunds(ctx context.Context, d *model.MerchantStoreRefundDispatch, rows []model.MerchantStoreRefund, proofs []merchantStoreSyncedRefund, client *pancake.Client) error {
	var failures []error
	for _, received := range proofs {
		proof := received.evidence
		var known *model.MerchantStoreRefund
		for i := range rows {
			r := &rows[i]
			if r.ProviderRefundReference != nil && *r.ProviderRefundReference == proof.RefundReference {
				known = r
				break
			}
		}
		// A provider can associate several execution records with one ticket.
		// Never collapse a distinct native receipt into a completed request, or
		// discard a real partial execution because its requested amount differs.
		// An overlapping in-flight request remains reserved by the model fence.
		if known == nil {
			for i := range rows {
				r := &rows[i]
				if r.ID == received.businessReference && r.ProviderRefundReference == nil && r.AmountMinor == proof.AmountMinor && r.Currency == proof.Currency && (r.Status == "awaiting_provider" || r.Status == "reconciliation_required") {
					known = r
					break
				}
			}
		}
		var err error
		if known != nil {
			if known.AmountMinor != proof.AmountMinor || known.Currency != proof.Currency {
				return ErrMerchantStoreRefundProvider
			}
			if known.ProviderRefundReference != nil {
				if *known.ProviderRefundReference != proof.RefundReference || known.ProviderEvidenceHash == "" {
					return ErrMerchantStoreRefundProvider
				}
				// Reuse the already durable proof hash from its original verifier.
				// A different notification/query envelope does not rewrite it.
				if known.RequestedRole == "provider" {
					err = model.ReconcileMerchantStoreExternalRefund(known.ID)
				} else {
					proof.EvidenceHash = known.ProviderEvidenceHash
					err = model.CompleteMerchantStoreVerifiedRefund(known.ID, proof)
				}
			} else {
				// Preserve the existing exact ticket/PAY/request validation for
				// platform-initiated refunds, including its stable evidence hash.
				copy := *d
				copy.Refund = *known
				observed, e := merchantStoreQueryPancakeRefundWithClient(ctx, &d.Order, merchantStoreDispatchNative(&copy), client)
				verified, ok := observed.VerifiedEvidence()
				if e != nil || !ok || verified.RefundReference != proof.RefundReference {
					return ErrMerchantStoreRefundProvider
				}
				err = model.CompleteMerchantStoreVerifiedRefund(known.ID, model.MerchantStoreVerifiedRefundEvidence(verified))
			}
		} else {
			var id string
			id, err = model.RecordMerchantStoreExternalRefund(d.Order.ID, proof)
			if err == nil {
				err = model.ReconcileMerchantStoreExternalRefund(id)
			}
		}
		// Provider success is already durable in these states. ACK it; the
		// existing worker retries local settlement without sending more money.
		if err != nil && !errors.Is(err, model.ErrMerchantStoreBalance) && !errors.Is(err, model.ErrMerchantStoreRefundReconciliation) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func SyncMerchantStoreRefunds(ctx context.Context, actor int, orderID string) (*model.MerchantStoreRefundView, error) {
	if err := model.AuthorizeMerchantStoreRefundSync(actor, orderID); err != nil {
		return nil, err
	}
	if err := merchantStoreSyncOrderRefunds(ctx, orderID); err != nil {
		return nil, err
	}
	return model.GetMerchantStoreRefunds(actor, orderID)
}

func merchantStoreHandleExternalRefundNotification(ctx context.Context, order *model.MerchantStoreOrder, payload []byte, signature string) error {
	return merchantStoreExternalRefundNotification(ctx, order, payload, signature, merchantStoreSyncOrderRefunds)
}

func merchantStoreExternalRefundNotification(ctx context.Context, order *model.MerchantStoreOrder, payload []byte, signature string, syncOrder func(context.Context, string) error) error {
	frozen, err := loadMerchantStorePaymentContext(order)
	if err != nil {
		return err
	}
	event, data, err := merchantStoreRefundVerifyWebhook(order, frozen, payload, signature)
	if err != nil || event == nil || (event.EventType != "refund.succeeded" && event.EventType != "refund.failed") {
		return ErrMerchantStorePaymentVerification
	}
	d, _, err := model.GetMerchantStoreRefundSyncSnapshot(order.ID)
	if err != nil {
		return err
	}
	if data.PaymentID != nil && *data.PaymentID != d.Basis.PaymentReference {
		return ErrMerchantStorePaymentVerification
	}
	if data.OriginalChargedAmount != nil {
		minor, e := merchantStoreMoneyToMinor(*data.OriginalChargedAmount)
		if e != nil || minor != d.Basis.AmountMinor {
			return ErrMerchantStorePaymentVerification
		}
	}
	// Failed notifications do not release any reservation. The authority read
	// imports succeeded execution records only. No delivery ID becomes money.
	return syncOrder(ctx, order.ID)
}
