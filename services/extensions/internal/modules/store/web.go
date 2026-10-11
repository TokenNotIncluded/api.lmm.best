package store

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
)

type LocatedProduct struct {
	ShopID   string
	Owner    Account
	Product  Product
	Variants []Variant
	Stats    *CatalogueStats
}
type CatalogueStats struct {
	PaidQuantity     int64 `json:"paid_quantity"`
	ReservedQuantity int64 `json:"reserved_quantity"`
	NetPaidQuantity  int64 `json:"net_paid_quantity"`
}
type LocatedOrder struct {
	ShopID    string
	ProductID string
	Order     Order
}

// CatalogueIndex reads only the store repository. Resource access is still
// decided by Service; a location lookup is never an authorization result.
type CatalogueIndex interface {
	FindProduct(context.Context, string) (LocatedProduct, error)
	FindOrder(context.Context, string) (LocatedOrder, error)
	ListPublished(context.Context, int64, string, int, int) ([]LocatedProduct, error)
}

func (s *Service) LocateProduct(ctx context.Context, id string) (LocatedProduct, error) {
	index, ok := s.repo.(CatalogueIndex)
	if !ok {
		return LocatedProduct{}, ErrUnavailable
	}
	return index.FindProduct(ctx, id)
}
func (s *Service) LocateOrder(ctx context.Context, id string) (LocatedOrder, error) {
	index, ok := s.repo.(CatalogueIndex)
	if !ok {
		return LocatedOrder{}, ErrUnavailable
	}
	return index.FindOrder(ctx, id)
}

// PublicCatalogue returns only reviewed, published public snapshots. A missing
// credit-denominated DTO is unavailable, never a fabricated zero/free price.
func (s *Service) PublicCatalogue(ctx context.Context, seller int64, query string, offset, limit int) ([]map[string]any, error) {
	if seller < 0 || len(query) > 200 || offset < 0 || offset > 100000 || !Page(limit) {
		return nil, ErrInvalid
	}
	index, ok := s.repo.(CatalogueIndex)
	if !ok {
		return nil, ErrUnavailable
	}
	rows, err := index.ListPublished(ctx, seller, query, offset, limit)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		view, err := s.publicLegacyView(row, true)
		if err != nil {
			return nil, err
		}
		out = append(out, view)
	}
	return out, nil
}
func (s *Service) PublicProduct(ctx context.Context, id string) (map[string]any, error) {
	row, err := s.LocateProduct(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.publicLegacyView(row, true)
}

func publicView(row LocatedProduct) (map[string]any, error) {
	if !row.Product.Published {
		return nil, ErrNotFound
	}
	if row.Owner.Kind != "personal" || row.Owner.ID <= 0 || row.Product.LegacyPublicVersion != 1 || len(row.Product.LegacyPublic) == 0 {
		return nil, ErrUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(row.Product.LegacyPublic))
	decoder.UseNumber()
	var value map[string]any
	if decoder.Decode(&value) != nil || value == nil || decoder.Decode(new(any)) != io.EOF {
		return nil, ErrUnavailable
	}
	allowed := map[string]bool{}
	for _, key := range []string{"id", "seller_id", "category_id", "title", "description", "image_urls", "contact", "links", "price_quota", "test_mode", "visibility", "purchase_login_required", "sale_limit", "max_quantity_per_order", "max_quantity_per_buyer", "template", "delivery_strategy", "payment_methods", "pickup_login_required", "pickup_code_required", "email_pickup_link", "status", "review_note", "reviewed_by", "reviewed_at", "promotion_expires_at", "created_at", "updated_at", "official", "unlimited_supply", "available_stock", "paid_quantity", "reserved_quantity", "sale_available", "trading_paused", "default_variant_id", "variants", "inventory_total", "inventory_available", "price_min_quota", "price_max_quota", "display_tags"} {
		allowed[key] = true
	}
	for key, v := range value {
		if !allowed[key] || !publicField(key, v) {
			return nil, ErrUnavailable
		}
	}
	price, ok := value["price_quota"].(json.Number)
	if !ok {
		return nil, ErrUnavailable
	}
	units, err := price.Int64()
	if err != nil || units < 0 || units > legacySafeInteger {
		return nil, ErrUnavailable
	}
	if raw, exists := value["visibility"]; exists {
		visibility, ok := raw.(string)
		if !ok {
			return nil, ErrUnavailable
		}
		if visibility != "" && visibility != "public" {
			return nil, ErrNotFound
		}
	}
	if id, ok := value["id"].(string); !ok || id != row.Product.ID {
		return nil, ErrUnavailable
	}
	seller, ok := value["seller_id"].(json.Number)
	if !ok {
		return nil, ErrUnavailable
	}
	sellerID, err := seller.Int64()
	if err != nil || sellerID != row.Owner.ID {
		return nil, ErrUnavailable
	}
	// The persisted owner/title are authoritative. No private review metadata or
	// payable method is advertised while the actual funds adapter is disabled.
	value["title"] = row.Product.Title
	value["review_note"] = ""
	value["reviewed_by"] = 0
	value["trading_paused"] = true
	value["payment_methods"] = []string{}
	if variants, ok := value["variants"].([]any); ok {
		for _, item := range variants {
			item.(map[string]any)["trading_paused"] = true
		}
	}
	return value, nil
}

func publicField(key string, v any) bool {
	if v == nil {
		return key != "visibility"
	}
	switch key {
	case "image_urls", "payment_methods", "display_tags":
		items, ok := v.([]any)
		if !ok {
			return false
		}
		for _, item := range items {
			if _, ok := item.(string); !ok {
				return false
			}
		}
		return true
	case "links", "variants":
		items, ok := v.([]any)
		if !ok || len(items) > 200 {
			return false
		}
		for _, item := range items {
			object, ok := item.(map[string]any)
			if !ok {
				return false
			}
			for field, value := range object {
				if key == "links" {
					if field != "title" && field != "url" && field != "description" {
						return false
					}
					if _, ok := value.(string); !ok {
						return false
					}
					continue
				}
				allowed := false
				for _, name := range []string{"id", "product_id", "name", "price_quota", "template", "enabled", "created_at", "updated_at", "is_default", "unlimited_supply", "inventory_total", "inventory_available", "reserved_stock", "sale_available", "trading_paused"} {
					if field == name {
						allowed = true
						break
					}
				}
				if !allowed || !publicScalar(value) {
					return false
				}
				if field == "price_quota" {
					n, ok := value.(json.Number)
					if !ok {
						return false
					}
					amount, err := n.Int64()
					if err != nil || amount < 0 || amount > legacySafeInteger {
						return false
					}
				}
			}
		}
		return true
	default:
		return publicScalar(v)
	}
}
func publicScalar(v any) bool {
	switch v.(type) {
	case nil, string, bool, json.Number:
		return true
	default:
		return false
	}
}

func onlyOne[T any](rows []T) (T, error) {
	var zero T
	if len(rows) == 0 {
		return zero, ErrNotFound
	}
	if len(rows) != 1 {
		return zero, ErrConflict
	}
	return rows[0], nil
}
