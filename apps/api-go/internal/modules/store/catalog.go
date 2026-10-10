package store

import (
	"context"
	"encoding/json"
	"errors"
	"math"
)

// CatalogChange changes exactly one resource. Inventory adjustments require a
// reason and an idempotency key. TrackStock cannot change after variant creation.
type CatalogChange struct {
	Action     string  `json:"action"`
	ShopID     string  `json:"shop_id"`
	ID         string  `json:"id"`
	Key        string  `json:"key"`
	Owner      Account `json:"owner"`
	ProductID  string  `json:"product_id"`
	Title      string  `json:"title"`
	Price      Price   `json:"price"`
	Published  bool    `json:"published"`
	TrackStock bool    `json:"track_stock"`
	Delta      int64   `json:"delta"`
	Quota      int64   `json:"quota"`
	Reason     string  `json:"reason"`
}
type replay struct {
	Hash   string          `json:"hash"`
	Result json.RawMessage `json:"result"`
}

func (s *Service) Catalog(ctx context.Context, token string, c CatalogChange) (json.RawMessage, error) {
	if !Key(c.Key) {
		return nil, ErrInvalid
	}
	owner := c.Owner
	if c.Action == "create_shop" {
		if !owner.Valid() || !Text(c.Title, 200) {
			return nil, ErrInvalid
		}
		c.ShopID = ID("shop", owner, c.Key)
	} else {
		var err error
		owner, err = s.Owner(ctx, c.ShopID)
		if err != nil {
			return nil, err
		}
	}
	actor, err := s.check(ctx, token, owner, Manage)
	if err != nil {
		return nil, err
	}
	commandID := ID("catalog", actor, c.Key)
	hash := ID(c)
	var output json.RawMessage
	err = s.repo.Within(ctx, c.ShopID, func(tx Tx) error {
		var old replay
		if err := tx.Get("commands", commandID, &old); err == nil {
			if old.Hash != hash {
				return ErrConflict
			}
			output = old.Result
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		resourceID := c.ID
		if resourceID == "" {
			resourceID = ID("resource", actor, c.Key)
		}
		var result any
		switch c.Action {
		case "create_shop":
			var old Shop
			if err := tx.Get("shops", c.ShopID, &old); err == nil {
				return ErrConflict
			} else if !errors.Is(err, ErrNotFound) {
				return err
			}
			shop := Shop{ID: c.ShopID, Owner: owner, Title: c.Title}
			if err := tx.Put("shops", shop.ID, shop); err != nil {
				return err
			}
			result = shop
		case "create_product":
			if !Text(c.Title, 200) || c.ID != "" {
				return ErrInvalid
			}
			p := Product{ID: resourceID, Title: c.Title}
			if err := tx.Put("products", p.ID, p); err != nil {
				return err
			}
			result = p
		case "product_status", "product_title":
			var p Product
			if err := tx.Get("products", c.ID, &p); err != nil {
				return err
			}
			if c.Action == "product_status" {
				p.Published = c.Published
			} else {
				if !Text(c.Title, 200) {
					return ErrInvalid
				}
				p.Title = c.Title
			}
			if err := tx.Put("products", p.ID, p); err != nil {
				return err
			}
			result = p
		case "create_variant":
			if !Text(c.Title, 200) || !c.Price.valid() || c.ID != "" || c.Quota < -1 {
				return ErrInvalid
			}
			var p Product
			if err := tx.Get("products", c.ProductID, &p); err != nil {
				return err
			}
			v := Variant{ID: resourceID, ProductID: p.ID, Title: c.Title, Price: c.Price, TrackStock: c.TrackStock, Quota: c.Quota}
			if err := tx.Put("variants", v.ID, v); err != nil {
				return err
			}
			result = v
		case "variant_details", "stock_adjust", "quota_set":
			var v Variant
			if err := tx.Get("variants", c.ID, &v); err != nil {
				return err
			}
			switch c.Action {
			case "variant_details":
				if !Text(c.Title, 200) || !c.Price.valid() {
					return ErrInvalid
				}
				v.Title = c.Title
				v.Price = c.Price
			case "quota_set":
				if c.Quota < -1 {
					return ErrInvalid
				}
				v.Quota = c.Quota
			case "stock_adjust":
				if !v.TrackStock || c.Delta == 0 || c.Delta == math.MinInt64 || !Text(c.Reason, 500) {
					return ErrInvalid
				}
				if c.Delta > 0 && v.Stock > math.MaxInt64-c.Delta {
					return ErrInvalid
				}
				if c.Delta < 0 && v.Stock < -c.Delta {
					return ErrConflict
				}
				if v.Stock+c.Delta < v.Reserved {
					return ErrConflict
				}
				v.Stock += c.Delta
			}
			if err := tx.Put("variants", v.ID, v); err != nil {
				return err
			}
			result = v
		default:
			return ErrInvalid
		}
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		output = data
		// Keep the full change for stock audit; it never contains user credentials.
		if c.Action == "stock_adjust" {
			if err := tx.Put("stock_movements", commandID, struct {
				Actor  int64         `json:"actor"`
				Change CatalogChange `json:"change"`
			}{actor, c}); err != nil {
				return err
			}
		}
		return tx.Put("commands", commandID, replay{hash, output})
	})
	return output, err
}

// Listing includes the last SCANNED ID, including hidden records, so filtering
// cannot trap a client on an empty page. Never request more than 100 records.
type Listing[T any] struct {
	Items []T    `json:"items"`
	Next  string `json:"next"`
}

func (s *Service) Browse(ctx context.Context, shopID, after string, limit int) (Listing[Product], error) {
	out := Listing[Product]{Items: []Product{}}
	if !Page(limit) {
		return out, ErrInvalid
	}
	err := s.repo.Within(ctx, shopID, func(tx Tx) error {
		var shop Shop
		if err := tx.Get("shops", shopID, &shop); err != nil {
			return err
		}
		rows, err := tx.List("products", after, limit)
		if err != nil {
			return err
		}
		for _, b := range rows {
			var p Product
			if err = json.Unmarshal(b, &p); err != nil {
				return err
			}
			out.Next = p.ID
			if p.Published {
				out.Items = append(out.Items, p)
			}
		}
		if len(rows) < limit {
			out.Next = ""
		}
		return nil
	})
	return out, err
}

type VariantListing struct {
	ID        string `json:"id"`
	ProductID string `json:"product_id"`
	Title     string `json:"title"`
	Price     Price  `json:"price"`
	Available bool   `json:"available"`
}

func (s *Service) Variants(ctx context.Context, shopID, after string, limit int) (Listing[VariantListing], error) {
	out := Listing[VariantListing]{Items: []VariantListing{}}
	if !Page(limit) {
		return out, ErrInvalid
	}
	err := s.repo.Within(ctx, shopID, func(tx Tx) error {
		rows, err := tx.List("variants", after, limit)
		if err != nil {
			return err
		}
		for _, b := range rows {
			var v Variant
			if err = json.Unmarshal(b, &v); err != nil {
				return err
			}
			out.Next = v.ID
			var p Product
			if err = tx.Get("products", v.ProductID, &p); err != nil {
				return err
			}
			if p.Published {
				available := (!v.TrackStock || v.Stock > v.Reserved) && (v.Quota < 0 || v.Claimed < v.Quota)
				out.Items = append(out.Items, VariantListing{v.ID, v.ProductID, v.Title, v.Price, available})
			}
		}
		if len(rows) < limit {
			out.Next = ""
		}
		return nil
	})
	return out, err
}
func (s *Service) ManageList(ctx context.Context, token, shopID, kind, after string, limit int) (Listing[json.RawMessage], error) {
	out := Listing[json.RawMessage]{Items: []json.RawMessage{}}
	if !Page(limit) || (kind != "products" && kind != "variants" && kind != "orders") {
		return out, ErrInvalid
	}
	owner, err := s.Owner(ctx, shopID)
	if err != nil {
		return out, err
	}
	if _, err = s.check(ctx, token, owner, Manage); err != nil {
		return out, err
	}
	err = s.repo.Within(ctx, shopID, func(tx Tx) error {
		var err error
		out.Items, err = tx.List(kind, after, limit)
		if err != nil {
			return err
		}
		if len(out.Items) == limit {
			var last struct {
				ID string `json:"id"`
			}
			if err = json.Unmarshal(out.Items[len(out.Items)-1], &last); err != nil {
				return err
			}
			out.Next = last.ID
		}
		return nil
	})
	return out, err
}
