package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/model"
	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

const (
	waffoPancakeTopUpExpiryGrace  = 15 * time.Minute
	waffoPancakeTopUpRecheckDelay = 15 * time.Minute
	waffoPancakeTopUpCheckTimeout = 8 * time.Second
	waffoPancakeTopUpExpiryBatch  = 30
)

type WaffoPancakeTopUpExpirySummary struct {
	Checked   int `json:"checked"`
	Failed    int `json:"failed"`
	Held      int `json:"held"`
	Succeeded int `json:"provider_succeeded"`
	Errors    int `json:"errors"`
}

type waffoPancakePayment struct {
	Status                  pancake.PaymentStatus `json:"status"`
	OrderMerchantExternalID *string               `json:"orderMerchantExternalId"`
}

// Query the merchant payment ledger before closing a local order. A successful
// or still-pending payment must stay recoverable through its signed webhook.
func waffoPancakePaymentsByTradeNo(ctx context.Context, client *pancake.Client, tradeNo string) ([]waffoPancakePayment, error) {
	response, err := client.GraphQL.Query(ctx, pancake.GraphQLParams{
		Query: `query ($ref: String!) {
			paymentsCount(filter: { orderMerchantExternalId: { eq: $ref } })
			payments(limit: 100, filter: { orderMerchantExternalId: { eq: $ref } }) {
				status orderMerchantExternalId
			}
		}`,
		Variables: map[string]any{"ref": tradeNo},
	})
	if err != nil {
		return nil, err
	}
	if response == nil || len(response.Errors) != 0 || len(response.Warnings) != 0 ||
		len(response.Data) == 0 || string(response.Data) == "null" {
		return nil, errors.New("incomplete Waffo Pancake payment query")
	}
	var data struct {
		PaymentsCount *int                   `json:"paymentsCount"`
		Payments      *[]waffoPancakePayment `json:"payments"`
	}
	if err := json.Unmarshal(response.Data, &data); err != nil {
		return nil, err
	}
	if data.PaymentsCount == nil || data.Payments == nil || *data.PaymentsCount != len(*data.Payments) {
		return nil, errors.New("incomplete Waffo Pancake payments")
	}
	for _, payment := range *data.Payments {
		if payment.OrderMerchantExternalID == nil || *payment.OrderMerchantExternalID != tradeNo {
			return nil, errors.New("Waffo Pancake payment order mismatch")
		}
	}
	return *data.Payments, nil
}

// An empty ledger or only terminal failed/canceled attempts is safe after the
// checkout expiry and grace period. Every other status is held for a webhook.
func waffoPancakePaymentDisposition(payments []waffoPancakePayment) (canFail, succeeded bool) {
	held := false
	for _, payment := range payments {
		switch payment.Status {
		case pancake.PaymentStatusFailed, pancake.PaymentStatusCanceled:
		case pancake.PaymentStatusSucceeded:
			succeeded = true
		default:
			held = true
		}
	}
	return !held && !succeeded, succeeded
}

func WaffoPancakeTopUpExpiryDue(ctx context.Context, now time.Time) (bool, error) {
	cutoff := now.Add(-time.Duration(WaffoPancakeCheckoutExpirySeconds)*time.Second - waffoPancakeTopUpExpiryGrace).Unix()
	checkedBefore := now.Add(-waffoPancakeTopUpRecheckDelay).Unix()
	return model.HasDueWaffoPancakeTopUps(ctx, cutoff, checkedBefore)
}

// ReconcileExpiredWaffoPancakeTopUps is called by the DB-leased system task.
// It does not mark an order failed if the provider cannot be read.
func ReconcileExpiredWaffoPancakeTopUps(ctx context.Context) (WaffoPancakeTopUpExpirySummary, error) {
	client, err := newWaffoPancakeClient()
	if err != nil {
		return WaffoPancakeTopUpExpirySummary{}, err
	}
	return reconcileExpiredWaffoPancakeTopUps(ctx, time.Now(), waffoPancakeTopUpExpiryBatch,
		func(checkCtx context.Context, tradeNo string) ([]waffoPancakePayment, error) {
			return waffoPancakePaymentsByTradeNo(checkCtx, client, tradeNo)
		})
}

func reconcileExpiredWaffoPancakeTopUps(
	ctx context.Context, now time.Time, limit int,
	check func(context.Context, string) ([]waffoPancakePayment, error),
) (WaffoPancakeTopUpExpirySummary, error) {
	var summary WaffoPancakeTopUpExpirySummary
	cutoff := now.Add(-time.Duration(WaffoPancakeCheckoutExpirySeconds)*time.Second - waffoPancakeTopUpExpiryGrace).Unix()
	checkedBefore := now.Add(-waffoPancakeTopUpRecheckDelay).Unix()
	orders, err := model.DueWaffoPancakeTopUps(ctx, cutoff, checkedBefore, limit)
	if err != nil {
		return summary, err
	}
	for _, order := range orders {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		checkCtx, cancel := context.WithTimeout(ctx, waffoPancakeTopUpCheckTimeout)
		payments, checkErr := check(checkCtx, order.TradeNo)
		cancel()
		summary.Checked++
		if checkErr != nil {
			summary.Errors++
			logger.LogWarn(ctx, fmt.Sprintf("Waffo Pancake expired top-up payment lookup failed trade_no=%s error_type=%T", order.TradeNo, checkErr))
		} else {
			canFail, succeeded := waffoPancakePaymentDisposition(payments)
			if canFail {
				failed, failErr := model.FailExpiredWaffoPancakeTopUp(ctx, order.Id, cutoff, now.Unix())
				if failErr != nil {
					return summary, failErr
				}
				if failed {
					summary.Failed++
					continue
				}
			} else {
				summary.Held++
				if succeeded {
					summary.Succeeded++
					logger.LogWarn(ctx, fmt.Sprintf("Waffo Pancake payment succeeded but local top-up remains pending trade_no=%s", order.TradeNo))
				}
			}
		}
		if err := model.MarkWaffoPancakeTopUpPaymentChecked(ctx, order.Id, now.Unix()); err != nil {
			return summary, err
		}
	}
	return summary, nil
}

func WaffoPancakeTopUpExpiryEnabled() bool {
	merchantID, privateKey := WaffoPancakeCredentials()
	return strings.TrimSpace(merchantID) != "" && strings.TrimSpace(privateKey) != ""
}
