// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package store

import (
	"context"
	"errors"
)

// MoneyCursor follows the repository's composite key. Order IDs alone are not
// unique across shops, so a page boundary must retain both fields.
type MoneyCursor struct {
	ShopID  string
	OrderID string
}

type moneyRecoveryIndex interface {
	PendingMoneyOrders(context.Context, MoneyCursor, int) ([]LocatedOrder, error)
}

// RecoverMoney is a bounded, trusted worker port. It queries only operations
// that already exist; it never creates a charge, refunds money, or delivers.
// A missing Core receipt stays pending with the original operation key.
func (s *Service) RecoverMoney(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	funds, ok := s.funds.(recoveryFunds)
	if !ok || Missing(funds) || !funds.ReconcileAvailable() {
		return nil
	}
	idx, ok := s.repo.(moneyRecoveryIndex)
	if !ok || Missing(idx) {
		return ErrUnavailable
	}
	// A previous tick already owns this Service's cursors. Do not queue workers
	// behind a slow Core request while the host is draining.
	if !s.recoveryMu.TryLock() {
		return nil
	}
	defer s.recoveryMu.Unlock()
	rows, err := idx.PendingMoneyOrders(ctx, s.moneyRecoveryAfter, 20)
	if err != nil {
		return err
	}
	if len(rows) > 20 {
		return ErrUnavailable
	}
	var result error
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return errors.Join(result, err)
		}
		_, err := s.reconcileOrderMoney(ctx, row.ShopID, row.Order.ID)
		// Advance past UNKNOWN so one slow order cannot starve other shops.
		// A later pass wraps to the start and queries the same original key.
		s.moneyRecoveryAfter = MoneyCursor{ShopID: row.ShopID, OrderID: row.Order.ID}
		if err != nil && !errors.Is(err, ErrPending) {
			result = errors.Join(result, err)
		}
	}
	if len(rows) < 20 {
		s.moneyRecoveryAfter = MoneyCursor{}
	}
	return result
}
