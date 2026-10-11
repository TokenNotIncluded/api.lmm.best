package store

import (
	"context"
	"encoding/json"
	"time"
)

const legacyProductSelect = `SELECT p.shop_id,s.body,p.body,COALESCE((SELECT jsonb_agg(v.body ORDER BY v.id) FROM lmm_store.variants v WHERE v.shop_id=p.shop_id AND v.body->>'product_id'=p.id),'[]'::jsonb), (SELECT jsonb_build_object('paid_quantity',COALESCE(SUM(CASE WHEN COALESCE(o.body->>'payment_ref','')<>'' OR o.body->>'state' IN ('paid','fulfilled','settled','refund_pending','refunded') THEN (o.body->>'quantity')::bigint ELSE 0 END),0),'reserved_quantity',COALESCE(SUM(CASE WHEN o.body->>'state' IN ('awaiting_payment','payment_pending') AND COALESCE(o.body->>'payment_ref','')='' THEN (o.body->>'quantity')::bigint ELSE 0 END),0),'net_paid_quantity',COALESCE(SUM(CASE WHEN o.body->>'state' IN ('paid','fulfilled','settled') THEN (o.body->>'quantity')::bigint ELSE 0 END),0)) FROM lmm_store.orders o JOIN lmm_store.variants ov ON ov.shop_id=o.shop_id AND ov.id=o.body->>'variant_id' WHERE o.shop_id=p.shop_id AND ov.body->>'product_id'=p.id) FROM lmm_store.products p JOIN lmm_store.shops s ON s.shop_id=p.shop_id AND s.id=p.shop_id `

func (p *Postgres) productRows(ctx context.Context, query string, args ...any) ([]LocatedProduct, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LocatedProduct{}
	for rows.Next() {
		var v LocatedProduct
		var shop, product, variants, stats []byte
		if err = rows.Scan(&v.ShopID, &shop, &product, &variants, &stats); err != nil {
			return nil, err
		}
		var s Shop
		if json.Unmarshal(shop, &s) != nil || json.Unmarshal(product, &v.Product) != nil || json.Unmarshal(variants, &v.Variants) != nil || json.Unmarshal(stats, &v.Stats) != nil {
			return nil, ErrUnavailable
		}
		v.Owner = s.Owner
		out = append(out, v)
	}
	return out, rows.Err()
}
func (p *Postgres) FindProduct(ctx context.Context, id string) (LocatedProduct, error) {
	if !Text(id, 128) {
		return LocatedProduct{}, ErrInvalid
	}
	rows, err := p.productRows(ctx, legacyProductSelect+`WHERE p.id=$1 LIMIT 2`, id)
	if err != nil {
		return LocatedProduct{}, err
	}
	return onlyOne(rows)
}
func (p *Postgres) FindOrder(ctx context.Context, id string) (LocatedOrder, error) {
	if !Text(id, 128) {
		return LocatedOrder{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := p.db.QueryContext(ctx, `SELECT o.shop_id,o.body,COALESCE(v.body->>'product_id','') FROM lmm_store.orders o LEFT JOIN lmm_store.variants v ON v.shop_id=o.shop_id AND v.id=o.body->>'variant_id' WHERE o.id=$1 LIMIT 2`, id)
	if err != nil {
		return LocatedOrder{}, err
	}
	defer rows.Close()
	var found []LocatedOrder
	for rows.Next() {
		var v LocatedOrder
		var body []byte
		if err = rows.Scan(&v.ShopID, &body, &v.ProductID); err != nil {
			return v, err
		}
		if err = json.Unmarshal(body, &v.Order); err != nil {
			return v, err
		}
		found = append(found, v)
	}
	if err = rows.Err(); err != nil {
		return LocatedOrder{}, err
	}
	return onlyOne(found)
}
func (p *Postgres) ListPublished(ctx context.Context, seller int64, query string, offset, limit int) ([]LocatedProduct, error) {
	return p.ListPublishedQuery(ctx, CatalogueQuery{Seller: seller, Text: query, Offset: offset, Limit: limit, Sort: "comprehensive"})
}
func (p *Postgres) ListPublishedQuery(ctx context.Context, q CatalogueQuery) ([]LocatedProduct, error) {
	// All ordering fragments are constants chosen from an allowlist. User text is
	// a parameter and matched literally, including percent/underscore/backslash.
	created := `COALESCE(p.body->'legacy'->>'created_at',p.body->'legacy_public'->>'created_at','0')::bigint`
	sales := `COALESCE((SELECT SUM((o.body->>'quantity')::bigint) FROM lmm_store.orders o JOIN lmm_store.variants ov ON ov.shop_id=o.shop_id AND ov.id=o.body->>'variant_id' WHERE o.shop_id=p.shop_id AND ov.body->>'product_id'=p.id AND o.body->>'state' IN ('paid','fulfilled','settled') AND COALESCE(o.body->'total'->>'amount_credits',o.body->'total'->>'amount_minor','0')::numeric>0),0)`
	tradable := `($11::boolean AND COALESCE(p.body->'legacy'->>'status',p.body->'legacy_public'->>'status','published')='published' AND NOT COALESCE((p.body->'legacy'->>'test_mode')::boolean,false) AND COALESCE((p.body->'legacy'->>'approved')::boolean,true) AND (p.body->'legacy'->>'sale_limit' IS NULL OR (p.body->'legacy'->>'sale_limit')::bigint>COALESCE((SELECT SUM((qv.body->>'claimed')::bigint) FROM lmm_store.variants qv WHERE qv.shop_id=p.shop_id AND qv.body->>'product_id'=p.id),0)) AND EXISTS (SELECT 1 FROM lmm_store.variants av WHERE av.shop_id=p.shop_id AND av.body->>'product_id'=p.id AND COALESCE((av.body->'legacy'->>'enabled')::boolean,true) AND (NOT (av.body->>'track_stock')::boolean OR (av.body->>'stock')::bigint>(av.body->>'reserved')::bigint) AND ((av.body->>'quota')::bigint<0 OR (av.body->>'quota')::bigint>(av.body->>'claimed')::bigint)))`
	order := created + ` DESC,p.id ASC`
	switch q.Sort {
	case "", "comprehensive":
		order = `CASE WHEN ` + tradable + ` THEN 0 ELSE 1 END,(` + sales + `::numeric*86400+` + created + `) DESC,p.id ASC`
	case "sales":
		order = sales + ` DESC,` + order
	case "newest":
	default:
		return nil, ErrInvalid
	}
	var auto, ai, guest any
	if q.AutoDelivery != nil {
		auto = *q.AutoDelivery
	}
	if q.AIProcessing != nil {
		ai = *q.AIProcessing
	}
	if q.GuestPurchase != nil {
		guest = *q.GuestPurchase
	}
	where := `WHERE (p.body->>'published')::boolean AND s.body->'owner'->>'kind'='personal' AND ($1::bigint=0 OR (s.body->'owner'->>'id')::bigint=$1) AND ($2='' OR position(lower($2) in lower(COALESCE(p.body->>'title','')||' '||COALESCE(p.body->'legacy'->>'description',p.body->'legacy_public'->>'description','')))>0) AND COALESCE(p.body->'legacy'->>'visibility',p.body->'legacy_public'->>'visibility','public') IN ('','public') AND ($5='' OR COALESCE(p.body->'legacy_public'->'display_tags','[]'::jsonb) ? $5) AND ($6::boolean IS NULL OR COALESCE((p.body->'legacy_public'->'catalogue'->>'auto_delivery')::boolean,false)=$6) AND ($7::boolean IS NULL OR COALESCE((p.body->'legacy_public'->'catalogue'->>'ai_processing')::boolean,false)=$7) AND ($8::boolean IS NULL OR NOT COALESCE((p.body->'legacy'->>'purchase_login_required')::boolean,(p.body->'legacy_public'->>'purchase_login_required')::boolean,true)=$8) AND ($9='' OR COALESCE(p.body->'legacy_public'->>'category_id','')=$9) AND ($10='' OR ` + tradable + `=($10='in_stock')) ORDER BY ` + order + ` OFFSET $3 LIMIT $4`
	return p.productRows(ctx, legacyProductSelect+where, q.Seller, q.Text, q.Offset, q.Limit, q.Tag, auto, ai, guest, q.CategoryID, q.Stock, q.FundsReady)
}
func (p *Postgres) ListLegacyProducts(ctx context.Context, seller int64, reviews bool, offset, limit int) ([]LocatedProduct, error) {
	return p.productRows(ctx, legacyProductSelect+`WHERE s.body->'owner'->>'kind'='personal' AND (($2::boolean AND COALESCE(p.body->'legacy'->>'status',p.body->'legacy_public'->>'status')='pending') OR (NOT $2 AND (s.body->'owner'->>'id')::bigint=$1)) AND COALESCE(p.body->'legacy'->>'status','')<>'deleted' ORDER BY COALESCE(p.body->'legacy'->>'created_at',p.body->'legacy_public'->>'created_at','0')::bigint DESC,p.id ASC OFFSET $3 LIMIT $4`, seller, reviews, offset, limit)
}
func (p *Postgres) ListLegacyOrders(ctx context.Context, actor Account, role string, offset, limit int) ([]LocatedOrder, error) {
	if role != "buyer" && role != "seller" {
		return nil, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := p.db.QueryContext(ctx, `SELECT o.shop_id,o.body,COALESCE(v.body->>'product_id','') FROM lmm_store.orders o LEFT JOIN lmm_store.variants v ON v.shop_id=o.shop_id AND v.id=o.body->>'variant_id' WHERE o.body->($1::text)->>'kind'=$2 AND (o.body->($1::text)->>'id')::bigint=$3 ORDER BY o.id OFFSET $4 LIMIT $5`, role, actor.Kind, actor.ID, offset, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LocatedOrder{}
	for rows.Next() {
		var v LocatedOrder
		var raw []byte
		if err = rows.Scan(&v.ShopID, &raw, &v.ProductID); err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &v.Order) != nil {
			return nil, ErrUnavailable
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (p *Postgres) PendingDeliveries(ctx context.Context, after string, limit int) ([]LocatedOrder, error) {
	if !Page(limit) {
		return nil, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := p.db.QueryContext(ctx, `SELECT shop_id,body FROM lmm_store.orders WHERE id>$1 AND body->'legacy'->>'delivery_template'='fixed-content' AND body->>'state' IN ('payment_pending','paid','fulfilled','settlement_pending') AND COALESCE(body->>'refund_state','')<>'requested' ORDER BY id LIMIT $2`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LocatedOrder{}
	for rows.Next() {
		var row LocatedOrder
		var raw []byte
		if err = rows.Scan(&row.ShopID, &raw); err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &row.Order) != nil {
			return nil, ErrUnavailable
		}
		row.ProductID = row.Order.Legacy.ProductID
		out = append(out, row)
	}
	return out, rows.Err()
}
