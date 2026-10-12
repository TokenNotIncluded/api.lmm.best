// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package store

import (
	"context"
	"encoding/json"
	"time"
)

// PendingMoneyOrders uses only the module's database. No Core credential,
// runtime DDL, or financial write is needed to locate recoverable operations.
func (p *Postgres) PendingMoneyOrders(ctx context.Context, after MoneyCursor, limit int) ([]LocatedOrder, error) {
	if !Page(limit) || (after != (MoneyCursor{}) && (!Text(after.ShopID, 128) || !Text(after.OrderID, 128))) {
		return nil, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := p.db.QueryContext(ctx, `SELECT shop_id,id,body FROM lmm_store.orders
WHERE (shop_id,id)>($1,$2) AND body->>'state' IN ('payment_pending','settlement_pending','refund_pending')
ORDER BY shop_id,id LIMIT $3`, after.ShopID, after.OrderID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]LocatedOrder, 0, limit)
	for rows.Next() {
		var row LocatedOrder
		var id string
		var raw []byte
		if err := rows.Scan(&row.ShopID, &id, &raw); err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &row.Order) != nil || row.Order.ID != id || !Text(row.ShopID, 128) || !Text(id, 128) {
			return nil, ErrUnavailable
		}
		if row.Order.Legacy != nil {
			row.ProductID = row.Order.Legacy.ProductID
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
