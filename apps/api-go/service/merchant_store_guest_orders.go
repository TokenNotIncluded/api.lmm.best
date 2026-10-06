package service

import (
	"context"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
)

func CreateMerchantStoreGuestPaymentSession(ctx context.Context, token, orderID, currency string) (*MerchantStorePaymentSession, error) {
	o, e := model.GetMerchantStoreGuestOrder(token, orderID)
	if e != nil || o == nil || o.PaymentMethod == MerchantStoreBalance {
		return nil, ErrMerchantStorePaymentAccess
	}
	return createMerchantStorePaymentSessionForOrder(ctx, o, currency)
}

func ReconcileMerchantStoreGuestPayment(ctx context.Context, token, orderID string) (*model.MerchantStoreOrder, error) {
	o, e := model.GetMerchantStoreGuestOrder(token, orderID)
	if e != nil {
		return nil, e
	}
	if e := reconcileMerchantStorePayment(ctx, o, time.Now().Unix()); e != nil {
		return nil, e
	}
	return model.GetMerchantStoreGuestOrder(token, orderID)
}
