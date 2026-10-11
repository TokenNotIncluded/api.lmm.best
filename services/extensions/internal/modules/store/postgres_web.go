package store

import (
	"context"
	"encoding/json"
	"time"
)

func (p *Postgres) FindProduct(ctx context.Context, id string) (LocatedProduct, error) {
	if !Text(id, 128) {
		return LocatedProduct{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := p.db.QueryContext(ctx, `SELECT p.shop_id,s.body,p.body FROM lmm_store.products p JOIN lmm_store.shops s ON s.shop_id=p.shop_id AND s.id=p.shop_id WHERE p.id=$1 LIMIT 2`, id)
	if err != nil {
		return LocatedProduct{}, err
	}
	defer rows.Close()
	var found []LocatedProduct
	for rows.Next() {
		var v LocatedProduct
		var shop, product []byte
		if err = rows.Scan(&v.ShopID, &shop, &product); err != nil {
			return v, err
		}
		var s Shop
		if err = json.Unmarshal(shop, &s); err != nil {
			return v, err
		}
		v.Owner = s.Owner
		if err = json.Unmarshal(product, &v.Product); err != nil {
			return v, err
		}
		found = append(found, v)
	}
	if err = rows.Err(); err != nil {
		return LocatedProduct{}, err
	}
	return onlyOne(found)
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
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := p.db.QueryContext(ctx, `SELECT p.shop_id,s.body,p.body FROM lmm_store.products p JOIN lmm_store.shops s ON s.shop_id=p.shop_id AND s.id=p.shop_id WHERE (p.body->>'published')::boolean AND s.body->'owner'->>'kind'='personal' AND ($1::bigint=0 OR (s.body->'owner'->>'id')::bigint=$1) AND ($2='' OR position(lower($2) in lower(p.body->>'title'))>0) AND COALESCE(p.body->'legacy_public'->>'visibility','public') IN ('','public') ORDER BY p.id OFFSET $3 LIMIT $4`, seller, query, offset, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LocatedProduct{}
	for rows.Next() {
		var v LocatedProduct
		var shop, product []byte
		if err = rows.Scan(&v.ShopID, &shop, &product); err != nil {
			return nil, err
		}
		var s Shop
		if err = json.Unmarshal(shop, &s); err != nil {
			return nil, err
		}
		v.Owner = s.Owner
		if err = json.Unmarshal(product, &v.Product); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
