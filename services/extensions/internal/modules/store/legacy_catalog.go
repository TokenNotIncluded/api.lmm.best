// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"time"
)

type CatalogueQuery struct {
	Seller                                    int64
	Text, Sort, CategoryID, Tag, Stock        string
	Offset, Limit                             int
	AutoDelivery, AIProcessing, GuestPurchase *bool
	FundsReady                                bool
}
type LegacyRequest struct {
	Action, ID, ChildID string
	Body                json.RawMessage
	Offset, Limit       int
	Role                string
}
type LegacyLink struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

// Only public seller input belongs here. Delivery material remains private in
// the variant and is never copied into the reviewed public snapshot.
type LegacyProductMetadata struct {
	Description           string       `json:"description"`
	ImageURLs             []string     `json:"image_urls"`
	Contact               string       `json:"contact"`
	Links                 []LegacyLink `json:"links"`
	TestMode              bool         `json:"test_mode"`
	Visibility            string       `json:"visibility"`
	PurchaseLoginRequired bool         `json:"purchase_login_required"`
	MaxQuantityPerOrder   *int64       `json:"max_quantity_per_order"`
	MaxQuantityPerBuyer   *int64       `json:"max_quantity_per_buyer"`
	Template              string       `json:"template"`
	DeliveryStrategy      string       `json:"delivery_strategy"`
	PaymentMethods        []string     `json:"payment_methods"`
	PickupLoginRequired   bool         `json:"pickup_login_required"`
	PickupCodeRequired    bool         `json:"pickup_code_required"`
	EmailPickupLink       bool         `json:"email_pickup_link"`
	Status                string       `json:"status"`
	ReviewNote            string       `json:"review_note"`
	ReviewedBy            int64        `json:"reviewed_by"`
	ReviewedAt            int64        `json:"reviewed_at"`
	Approved              bool         `json:"approved"`
	CreatedAt             int64        `json:"created_at"`
	UpdatedAt             int64        `json:"updated_at"`
	DefaultVariantID      string       `json:"default_variant_id"`
	SaleLimit             *int64       `json:"sale_limit"`
}
type LegacyVariantMetadata struct {
	Template     string `json:"template"`
	Enabled      bool   `json:"enabled"`
	FixedContent string `json:"fixed_content,omitempty"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}
type LegacyVariantInput struct {
	Name         string  `json:"name"`
	PriceQuota   int64   `json:"price_quota"`
	Template     string  `json:"template"`
	Enabled      bool    `json:"enabled"`
	FixedContent *string `json:"fixed_content,omitempty"`
}
type LegacyProductInput struct {
	Variants              []LegacyVariantInput `json:"variants,omitempty"`
	CategoryID            *string              `json:"category_id,omitempty"`
	Title                 string               `json:"title"`
	Description           string               `json:"description"`
	ImageURLs             []string             `json:"image_urls"`
	Contact               string               `json:"contact"`
	Links                 []LegacyLink         `json:"links"`
	PriceQuota            int64                `json:"price_quota"`
	TestMode              *bool                `json:"test_mode,omitempty"`
	Visibility            *string              `json:"visibility,omitempty"`
	PurchaseLoginRequired *bool                `json:"purchase_login_required,omitempty"`
	MaxQuantityPerOrder   *int64               `json:"max_quantity_per_order,omitempty"`
	MaxQuantityPerBuyer   *int64               `json:"max_quantity_per_buyer,omitempty"`
	Template              string               `json:"template"`
	FixedContent          *string              `json:"fixed_content,omitempty"`
	DeliveryStrategy      string               `json:"delivery_strategy"`
	PaymentMethods        []string             `json:"payment_methods"`
	PickupLoginRequired   bool                 `json:"pickup_login_required"`
	PickupCodeRequired    bool                 `json:"pickup_code_required"`
	EmailPickupLink       bool                 `json:"email_pickup_link"`
}
type administrativeAuthority interface {
	CheckAdmin(context.Context, string, bool) (int64, error)
}
type legacyReadIndex interface {
	ListLegacyProducts(context.Context, int64, bool, int, int) ([]LocatedProduct, error)
	ListLegacyOrders(context.Context, Account, string, int, int) ([]LocatedOrder, error)
	ListPublishedQuery(context.Context, CatalogueQuery) ([]LocatedProduct, error)
}

func legacyDecode(raw []byte, out any) error {
	if len(raw) == 0 || len(raw) > 512<<10 {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	return nil
}
func legacyRandomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:], nil
}
func legacyPage(items any, count, offset, limit int) map[string]any {
	return map[string]any{"items": items, "offset": offset, "limit": limit, "has_more": count == limit}
}

// Config describes capabilities actually mounted by this service. It does not
// advertise external gateways, guest checkout, fees or unsupported read models.
func (s *Service) PublicConfig(ctx context.Context) (map[string]any, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	return map[string]any{"fee_bps": 0, "promotion_quota": 0, "minimum_unit_price_quota": int64(500000), "credits_per_usd": int64(500000), "external_minimum_quota": int64(5000000), "product_test_mode_supported": true, "store_catalogue_supported": true, "store_collections_supported": false, "store_categories_supported": false, "store_likes_supported": false, "store_svg_media_supported": false, "extore_import_supported": false, "extore_redirect_uri": "", "store_merchant_home_supported": false, "store_access_supported": false, "store_terms_supported": true, "product_purchase_limits_supported": false, "fixed_content_supported": false, "product_variants_create_supported": true, "product_link_presets": []any{}, "linuxdo_units_per_usd": "0", "disclaimer_version": "merchant-store-v1", "disclaimer_text": legacyDisclaimerText, "platform_payment_methods": []any{}, "platform_payment_catalog": []any{}}, nil
}

func (s *Service) PublicCatalogueQuery(ctx context.Context, q CatalogueQuery) ([]map[string]any, error) {
	if q.Seller < 0 || q.Seller > 2147483647 || len(q.Text) > 200 || q.Offset < 0 || q.Offset > 100000 || !Page(q.Limit) || len(q.Tag) > 512 || len(q.CategoryID) > 128 {
		return nil, ErrInvalid
	}
	if q.Sort == "" {
		q.Sort = "comprehensive"
	}
	if q.Sort != "comprehensive" && q.Sort != "sales" && q.Sort != "newest" {
		return nil, ErrInvalid
	}
	if q.Stock != "" && q.Stock != "in_stock" && q.Stock != "out_of_stock" {
		return nil, ErrInvalid
	}
	if q.CategoryID != "" {
		return nil, ErrUnavailable
	}
	idx, ok := s.repo.(legacyReadIndex)
	if !ok {
		if q.Sort == "comprehensive" && q.Tag == "" && q.Stock == "" && q.AutoDelivery == nil && q.AIProcessing == nil && q.GuestPurchase == nil {
			return s.PublicCatalogue(ctx, q.Seller, q.Text, q.Offset, q.Limit)
		}
		return nil, ErrUnavailable
	}
	q.FundsReady = s.funds.Available()
	rows, err := idx.ListPublishedQuery(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		v, err := s.publicLegacyView(row, true)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Service) legacyShop(ctx context.Context, actor Account) (Shop, error) {
	id := ID("legacy-web-shop-v1", actor)
	var shop Shop
	err := s.repo.Within(ctx, id, func(tx Tx) error {
		err := tx.Get("shops", id, &shop)
		if err == nil {
			if shop.Owner != actor {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		shop = Shop{ID: id, Owner: actor, Title: "Merchant store"}
		return tx.Put("shops", id, shop)
	})
	return shop, err
}
func variantsFor(tx Tx, id string) ([]Variant, error) {
	var out []Variant
	after := ""
	for page := 0; page < 100; page++ {
		rows, err := tx.List("variants", after, 100)
		if err != nil {
			return nil, err
		}
		for _, b := range rows {
			var v Variant
			if json.Unmarshal(b, &v) != nil {
				return nil, ErrUnavailable
			}
			after = v.ID
			if v.ProductID == id {
				out = append(out, v)
			}
		}
		if len(rows) < 100 {
			return out, nil
		}
	}
	return nil, ErrUnavailable
}

func (s *Service) Legacy(ctx context.Context, token string, actor Account, r LegacyRequest) (any, error) {
	if actor.Kind != "personal" || !actor.Valid() {
		return nil, ErrForbidden
	}
	if _, err := s.check(ctx, token, actor, Manage); err != nil {
		return nil, err
	}
	switch r.Action {
	case "submit_product", "unlist_product", "delete_product", "cancel_order", "reconcile_order":
		if len(r.Body) > 0 {
			var empty map[string]any
			if legacyDecode(r.Body, &empty) != nil || empty == nil || len(empty) != 0 {
				return nil, ErrInvalid
			}
		}
	}
	switch r.Action {
	case "accept_disclaimer":
		return s.acceptLegacyDisclaimer(ctx, actor, r)
	case "my_terms", "save_terms":
		return s.legacyTerms(ctx, actor, r)
	case "my_products", "reviews":
		if r.Offset < 0 || r.Offset > 100000 || !Page(r.Limit) {
			return nil, ErrInvalid
		}
		reviews := r.Action == "reviews"
		if reviews {
			a, ok := s.auth.(administrativeAuthority)
			if !ok {
				return nil, ErrUnavailable
			}
			if _, err := a.CheckAdmin(ctx, token, false); err != nil {
				return nil, err
			}
		}
		idx, ok := s.repo.(legacyReadIndex)
		if !ok {
			return nil, ErrUnavailable
		}
		rows, err := idx.ListLegacyProducts(ctx, actor.ID, reviews, r.Offset, r.Limit)
		if err != nil {
			return nil, err
		}
		out := []map[string]any{}
		for _, row := range rows {
			v, err := s.publicLegacyView(row, false)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return legacyPage(out, len(out), r.Offset, r.Limit), nil
	case "save_product":
		return s.saveLegacyProduct(ctx, token, actor, r)
	case "my_orders", "create_order", "get_order", "pay_order", "reconcile_order", "cancel_order", "refunds", "request_refund", "decide_refund":
		return s.legacyOrder(ctx, token, actor, r)
	}
	row, err := s.LocateProduct(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	reviewer := int64(0)
	if r.Action == "review_product" {
		a, ok := s.auth.(administrativeAuthority)
		if !ok {
			return nil, ErrUnavailable
		}
		reviewer, err = a.CheckAdmin(ctx, token, false)
		if err != nil {
			return nil, err
		}
	} else if row.Owner != actor {
		return nil, ErrForbidden
	}
	if row.Product.Legacy == nil {
		return nil, ErrUnavailable
	}
	if r.Action == "draft_product" || r.Action == "preview_product" {
		return s.publicLegacyView(row, false)
	}
	if r.Action == "create_variant" || r.Action == "save_variant" || r.Action == "variant_enabled" {
		return s.saveLegacyVariant(ctx, actor, row, r)
	}
	err = s.repo.Within(ctx, row.ShopID, func(tx Tx) error {
		var p Product
		if err := tx.Get("products", r.ID, &p); err != nil {
			return err
		}
		m := p.Legacy
		if m == nil {
			return ErrUnavailable
		}
		if m.Status == "deleted" {
			if r.Action == "delete_product" {
				return nil
			}
			return ErrNotFound
		}
		switch r.Action {
		case "submit_product":
			terms, err := readLegacyTerms(tx)
			if err != nil {
				return err
			}
			if terms == nil {
				return ErrSellerTermsNotConfigured
			}
			if m.TestMode {
				return ErrInvalid
			}
			if m.Status != "draft" && m.Status != "rejected" {
				return ErrConflict
			}
			m.Status = "pending"
			p.Published = false
		case "review_product":
			var in struct {
				Approve bool   `json:"approve"`
				Note    string `json:"note"`
			}
			if err := legacyDecode(r.Body, &in); err != nil {
				return err
			}
			if len(in.Note) > 4000 {
				return ErrInvalid
			}
			if m.Status != "pending" {
				return ErrConflict
			}
			if in.Approve {
				terms, err := readLegacyTerms(tx)
				if err != nil {
					return err
				}
				if terms == nil {
					return ErrSellerTermsNotConfigured
				}
				m.Status = "published"
				p.Published = true
				m.Approved = true
			} else {
				m.Status = "rejected"
				p.Published = false
				m.Approved = false
			}
			m.ReviewedBy = reviewer
			m.ReviewedAt = time.Now().Unix()
			m.ReviewNote = in.Note
		case "unlist_product":
			m.Status = "unlisted"
			p.Published = false
		case "delete_product":
			// Tombstoning preserves immutable order references and inventory history.
			if p.Published || m.Status == "pending" {
				return ErrConflict
			}
			m.Status = "deleted"
			p.Published = false
		case "paused":
			var in struct {
				Paused *bool `json:"paused"`
			}
			if legacyDecode(r.Body, &in) != nil || in.Paused == nil {
				return ErrInvalid
			}
			if !m.Approved || (m.Status != "published" && m.Status != "paused") {
				return ErrConflict
			}
			if *in.Paused {
				m.Status = "paused"
				p.Published = false
			} else {
				m.Status = "published"
				p.Published = true
			}
		case "listing":
			var in struct {
				Listed *bool `json:"listed"`
			}
			if legacyDecode(r.Body, &in) != nil || in.Listed == nil {
				return ErrInvalid
			}
			if *in.Listed {
				if m.Status != "unlisted" || m.ReviewedAt == 0 || !m.Approved {
					return ErrConflict
				}
				terms, err := readLegacyTerms(tx)
				if err != nil {
					return err
				}
				if terms == nil {
					return ErrSellerTermsNotConfigured
				}
				m.Status = "published"
				p.Published = true
			} else {
				m.Status = "unlisted"
				p.Published = false
			}
		case "sale_limit":
			var raw struct {
				SaleLimit json.RawMessage `json:"sale_limit"`
			}
			if legacyDecode(r.Body, &raw) != nil || len(raw.SaleLimit) == 0 {
				return ErrInvalid
			}
			var in struct{ SaleLimit *int64 }
			if !bytes.Equal(raw.SaleLimit, []byte("null")) {
				var value int64
				if json.Unmarshal(raw.SaleLimit, &value) != nil || value < 0 {
					return ErrInvalid
				}
				in.SaleLimit = &value
			}
			m.SaleLimit = in.SaleLimit
		default:
			return ErrUnavailable
		}
		m.UpdatedAt = time.Now().Unix()
		vs, err := variantsFor(tx, p.ID)
		if err != nil {
			return err
		}
		if err := snapshotLegacy(&p, row.Owner, vs); err != nil {
			return err
		}
		return tx.Put("products", p.ID, p)
	})
	return nil, err
}

func validateLegacyVariant(in LegacyVariantInput) error {
	if !Text(in.Name, 200) || in.PriceQuota < 500000 || in.PriceQuota > legacySafeInteger {
		return ErrInvalid
	}
	// Fixed-content delivery and finite private code inventory need their own
	// authenticated collection contract. Never claim delivery by just storing it.
	if in.Template != "text" && in.Template != "fixed-content" && in.Template != "card" && in.Template != "account" {
		return ErrInvalid
	}
	if in.Template != "fixed-content" {
		return ErrUnavailable
	}
	if in.FixedContent == nil || !Text(*in.FixedContent, 16000) {
		return ErrInvalid
	}
	return nil
}
func validateLegacyInput(in LegacyProductInput) error {
	if !Text(in.Title, 200) || len(in.Description) > 128000 || len(in.Contact) > 4000 || len(in.ImageURLs) > 30 || len(in.Links) > 30 || in.PriceQuota < 0 {
		return ErrInvalid
	}
	if in.CategoryID != nil && *in.CategoryID != "" {
		return ErrUnavailable
	}
	if in.Visibility != nil && *in.Visibility != "public" {
		return ErrUnavailable
	}
	if in.PurchaseLoginRequired != nil && !*in.PurchaseLoginRequired {
		return ErrUnavailable
	}
	if in.MaxQuantityPerBuyer != nil {
		return ErrUnavailable
	}
	if in.MaxQuantityPerOrder != nil && (*in.MaxQuantityPerOrder < 1 || *in.MaxQuantityPerOrder > 1000000) {
		return ErrInvalid
	}
	if in.EmailPickupLink || in.PickupCodeRequired || !in.PickupLoginRequired {
		return ErrUnavailable
	}
	if in.DeliveryStrategy != "" && in.DeliveryStrategy != "sequential" {
		return ErrUnavailable
	}
	for _, p := range in.PaymentMethods {
		if p != "balance" {
			return ErrUnavailable
		}
	}
	if len(in.PaymentMethods) != 1 {
		return ErrInvalid
	}
	for _, raw := range in.ImageURLs {
		u, err := url.Parse(raw)
		if err != nil || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return ErrInvalid
		}
	}
	for _, l := range in.Links {
		u, err := url.Parse(l.URL)
		if !Text(l.Title, 200) || len(l.Description) > 4000 || err != nil || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return ErrInvalid
		}
	}
	if len(in.Variants) > 30 {
		return ErrInvalid
	}
	return nil
}
func (s *Service) saveLegacyProduct(ctx context.Context, token string, actor Account, r LegacyRequest) (any, error) {
	var in LegacyProductInput
	if err := legacyDecode(r.Body, &in); err != nil {
		return nil, err
	}
	if err := validateLegacyInput(in); err != nil {
		return nil, err
	}
	var shop Shop
	var p Product
	if r.ID == "" {
		var err error
		shop, err = s.legacyShop(ctx, actor)
		if err != nil {
			return nil, err
		}
		p.ID, err = legacyRandomID()
		if err != nil {
			return nil, err
		}
	} else {
		row, err := s.LocateProduct(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		if row.Owner != actor {
			return nil, ErrForbidden
		}
		shop = Shop{ID: row.ShopID, Owner: row.Owner}
		p = row.Product
		if p.Legacy == nil {
			return nil, ErrUnavailable
		}
	}
	now := time.Now().Unix()
	err := s.repo.Within(ctx, shop.ID, func(tx Tx) error {
		if r.ID != "" {
			if err := tx.Get("products", p.ID, &p); err != nil {
				return err
			}
			if p.Legacy == nil || p.Legacy.Status == "deleted" {
				return ErrNotFound
			}
		}
		fresh := p.Legacy == nil
		old := p.Legacy
		m := &LegacyProductMetadata{Description: in.Description, ImageURLs: in.ImageURLs, Contact: in.Contact, Links: in.Links, Visibility: "public", PurchaseLoginRequired: true, MaxQuantityPerOrder: in.MaxQuantityPerOrder, Template: in.Template, DeliveryStrategy: "sequential", PaymentMethods: in.PaymentMethods, PickupLoginRequired: in.PickupLoginRequired, Status: "draft", CreatedAt: now, UpdatedAt: now}
		if m.ImageURLs == nil {
			m.ImageURLs = []string{}
		}
		if m.Links == nil {
			m.Links = []LegacyLink{}
		}
		if in.TestMode != nil {
			m.TestMode = *in.TestMode
		}
		if old != nil {
			m.CreatedAt = old.CreatedAt
			m.SaleLimit = old.SaleLimit
		}
		p.Title = in.Title
		p.Published = false
		p.Legacy = m
		if fresh {
			inputs := in.Variants
			if len(inputs) == 0 {
				inputs = []LegacyVariantInput{{Name: "Default", PriceQuota: in.PriceQuota, Template: in.Template, Enabled: true, FixedContent: in.FixedContent}}
			}
			for i, v := range inputs {
				if err := validateLegacyVariant(v); err != nil {
					return err
				}
				id, err := legacyRandomID()
				if err != nil {
					return err
				}
				variant := Variant{ID: id, ProductID: p.ID, Title: v.Name, Price: Price{Amount: v.PriceQuota, Unit: "credits"}, Quota: -1, Legacy: &LegacyVariantMetadata{Template: v.Template, Enabled: v.Enabled, FixedContent: *v.FixedContent, CreatedAt: now, UpdatedAt: now}}
				if i == 0 {
					m.DefaultVariantID = id
					m.Template = v.Template
				}
				if err := tx.Put("variants", id, variant); err != nil {
					return err
				}
			}
		} else {
			if len(in.Variants) != 0 {
				return ErrInvalid
			}
			m.DefaultVariantID = old.DefaultVariantID
			var v Variant
			if err := tx.Get("variants", m.DefaultVariantID, &v); err != nil {
				return err
			}
			if v.Legacy == nil {
				return ErrUnavailable
			}
			if in.FixedContent != nil {
				if !Text(*in.FixedContent, 16000) {
					return ErrInvalid
				}
				v.Legacy.FixedContent = *in.FixedContent
			}
			if in.Template != "" && in.Template != v.Legacy.Template {
				return ErrInvalid
			}
			if in.PriceQuota < 500000 || in.PriceQuota > legacySafeInteger {
				return ErrInvalid
			}
			v.Price = Price{Amount: in.PriceQuota, Unit: "credits"}
			v.Legacy.UpdatedAt = now
			if err := tx.Put("variants", v.ID, v); err != nil {
				return err
			}
		}
		vs, err := variantsFor(tx, p.ID)
		if err != nil {
			return err
		}
		if err := snapshotLegacy(&p, actor, vs); err != nil {
			return err
		}
		return tx.Put("products", p.ID, p)
	})
	if err != nil {
		return nil, err
	}
	row, err := s.LocateProduct(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	return s.publicLegacyView(row, false)
}

func (s *Service) saveLegacyVariant(ctx context.Context, actor Account, row LocatedProduct, r LegacyRequest) (any, error) {
	var out Variant
	err := s.repo.Within(ctx, row.ShopID, func(tx Tx) error {
		var p Product
		if err := tx.Get("products", row.Product.ID, &p); err != nil {
			return err
		}
		if p.Legacy == nil {
			return ErrUnavailable
		}
		if p.Legacy.Status == "deleted" {
			return ErrNotFound
		}
		now := time.Now().Unix()
		if r.Action == "create_variant" {
			var in LegacyVariantInput
			if legacyDecode(r.Body, &in) != nil {
				return ErrInvalid
			}
			if err := validateLegacyVariant(in); err != nil {
				return err
			}
			id, err := legacyRandomID()
			if err != nil {
				return err
			}
			out = Variant{ID: id, ProductID: p.ID, Title: in.Name, Price: Price{Amount: in.PriceQuota, Unit: "credits"}, Quota: -1, Legacy: &LegacyVariantMetadata{Template: in.Template, Enabled: in.Enabled, FixedContent: *in.FixedContent, CreatedAt: now, UpdatedAt: now}}
		} else {
			if err := tx.Get("variants", r.ChildID, &out); err != nil {
				return err
			}
			if out.ProductID != p.ID || out.Legacy == nil {
				return ErrForbidden
			}
			if r.Action == "variant_enabled" {
				var in struct {
					Enabled *bool `json:"enabled"`
				}
				if legacyDecode(r.Body, &in) != nil || in.Enabled == nil {
					return ErrInvalid
				}
				out.Legacy.Enabled = *in.Enabled
			} else {
				var in LegacyVariantInput
				if legacyDecode(r.Body, &in) != nil {
					return ErrInvalid
				}
				if in.FixedContent == nil {
					fixed := out.Legacy.FixedContent
					in.FixedContent = &fixed
				}
				if err := validateLegacyVariant(in); err != nil {
					return err
				}
				out.Title = in.Name
				out.Price = Price{Amount: in.PriceQuota, Unit: "credits"}
				out.Legacy.Template = in.Template
				out.Legacy.Enabled = in.Enabled
				out.Legacy.FixedContent = *in.FixedContent
			}
			out.Legacy.UpdatedAt = now
		}
		if err := tx.Put("variants", out.ID, out); err != nil {
			return err
		}
		p.Published = false
		p.Legacy.Status = "draft"
		p.Legacy.Approved = false
		p.Legacy.ReviewedAt = 0
		p.Legacy.ReviewedBy = 0
		p.Legacy.ReviewNote = ""
		p.Legacy.UpdatedAt = now
		vs, err := variantsFor(tx, p.ID)
		if err != nil {
			return err
		}
		if err := snapshotLegacy(&p, actor, vs); err != nil {
			return err
		}
		return tx.Put("products", p.ID, p)
	})
	if err != nil {
		return nil, err
	}
	return legacyVariantView(out, row.Product.Legacy.DefaultVariantID, false), nil
}

func snapshotLegacy(p *Product, owner Account, vs []Variant) error {
	view, err := legacyProductView(LocatedProduct{Owner: owner, Product: *p, Variants: vs}, false, false)
	if err != nil {
		return err
	}
	view["review_note"] = ""
	view["reviewed_by"] = 0
	raw, err := json.Marshal(view)
	if err != nil {
		return err
	}
	p.LegacyPublic = raw
	p.LegacyPublicVersion = 1
	return nil
}
func legacyVariantView(v Variant, defaultID string, ready bool) map[string]any {
	m := v.Legacy
	unlimited := !v.TrackStock
	available := v.Stock - v.Reserved
	if unlimited {
		available = -1
	}
	sale := available
	if v.Quota >= 0 && (sale < 0 || sale > v.Quota-v.Claimed) {
		sale = v.Quota - v.Claimed
	}
	if sale < 0 && v.Quota >= 0 {
		sale = 0
	}
	paused := !ready || !m.Enabled || (v.TrackStock && available <= 0) || (v.Quota >= 0 && v.Claimed >= v.Quota)
	return map[string]any{"id": v.ID, "product_id": v.ProductID, "name": v.Title, "price_quota": v.Price.Amount, "template": m.Template, "enabled": m.Enabled, "created_at": m.CreatedAt, "updated_at": m.UpdatedAt, "is_default": v.ID == defaultID, "unlimited_supply": unlimited, "inventory_total": v.Stock, "inventory_available": available, "reserved_stock": v.Reserved, "sale_available": sale, "trading_paused": paused}
}
func legacyProductView(row LocatedProduct, public, ready bool) (map[string]any, error) {
	p := row.Product
	m := p.Legacy
	if m == nil {
		// Versioned snapshots keep their strict legacy validation. Native
		// records have neither legacy metadata nor a legacy public snapshot.
		if p.LegacyPublicVersion != 0 || len(p.LegacyPublic) != 0 && string(p.LegacyPublic) != "null" {
			return publicView(row)
		}
		return nativeProductView(row, public)
	}
	if public && (!p.Published || !m.Approved || m.Status != "published" || m.TestMode || m.Visibility != "public") {
		return nil, ErrNotFound
	}
	if len(row.Variants) == 0 {
		return nil, ErrUnavailable
	}
	variants := []map[string]any{}
	var price, min, max, stock, inventoryTotal, reserved, claimed, quotaConsumed int64
	min = -1
	unlimited := false
	tradable := false
	for _, v := range row.Variants {
		if v.Legacy == nil || v.Price.Unit != "credits" || !v.Price.valid() || v.Price.Amount > legacySafeInteger || v.Claimed < 0 || quotaConsumed > legacySafeInteger-v.Claimed {
			return nil, ErrUnavailable
		}
		quotaConsumed += v.Claimed
		view := legacyVariantView(v, m.DefaultVariantID, ready)
		if v.ID == m.DefaultVariantID {
			price = v.Price.Amount
		}
		if v.Legacy.Enabled {
			if min < 0 || v.Price.Amount < min {
				min = v.Price.Amount
			}
			if v.Price.Amount > max {
				max = v.Price.Amount
			}
			unlimited = unlimited || !v.TrackStock
			tradable = tradable || !view["trading_paused"].(bool)
		}
		if v.TrackStock {
			if v.Stock-v.Reserved < 0 || stock > legacySafeInteger-(v.Stock-v.Reserved) || inventoryTotal > legacySafeInteger-v.Stock {
				return nil, ErrUnavailable
			}
			stock += v.Stock - v.Reserved
			inventoryTotal += v.Stock
		}
		variants = append(variants, view)
	}
	if row.Stats != nil {
		reserved = row.Stats.ReservedQuantity
		claimed = row.Stats.PaidQuantity
	}
	if min < 0 {
		min = price
	}
	paused := !tradable || !p.Published || m.TestMode || (m.SaleLimit != nil && quotaConsumed >= *m.SaleLimit)
	saleAvailable := stock
	if unlimited {
		saleAvailable = -1
	}
	if m.SaleLimit != nil {
		remaining := *m.SaleLimit - quotaConsumed
		if remaining < 0 {
			remaining = 0
		}
		if saleAvailable < 0 || saleAvailable > remaining {
			saleAvailable = remaining
		}
	}
	methods := []string{}
	if !paused {
		methods = append(methods, m.PaymentMethods...)
	}
	tags := []string{"out_of_stock"}
	if paused && (unlimited || stock > 0) {
		tags = []string{"trading_paused"}
	} else if !paused {
		tags = []string{"in_stock"}
	}
	reviewNote := m.ReviewNote
	reviewer := m.ReviewedBy
	if public {
		reviewNote = ""
		reviewer = 0
	}
	return map[string]any{"id": p.ID, "seller_id": row.Owner.ID, "category_id": "", "title": p.Title, "description": m.Description, "image_urls": m.ImageURLs, "contact": m.Contact, "links": m.Links, "price_quota": price, "test_mode": m.TestMode, "visibility": m.Visibility, "purchase_login_required": m.PurchaseLoginRequired, "sale_limit": m.SaleLimit, "max_quantity_per_order": m.MaxQuantityPerOrder, "max_quantity_per_buyer": m.MaxQuantityPerBuyer, "template": m.Template, "delivery_strategy": m.DeliveryStrategy, "payment_methods": methods, "pickup_login_required": m.PickupLoginRequired, "pickup_code_required": m.PickupCodeRequired, "email_pickup_link": m.EmailPickupLink, "status": m.Status, "review_note": reviewNote, "reviewed_by": reviewer, "reviewed_at": m.ReviewedAt, "promotion_expires_at": 0, "created_at": m.CreatedAt, "updated_at": m.UpdatedAt, "official": false, "unlimited_supply": unlimited, "available_stock": stock, "paid_quantity": claimed, "reserved_quantity": reserved, "sale_available": saleAvailable, "trading_paused": paused, "default_variant_id": m.DefaultVariantID, "variants": variants, "inventory_total": inventoryTotal, "inventory_available": stock, "price_min_quota": min, "price_max_quota": max, "display_tags": tags}, nil
}
func (s *Service) publicLegacyView(row LocatedProduct, public bool) (map[string]any, error) {
	view, err := legacyProductView(row, public, s.funds.Available())
	if err == nil && view["record_kind"] == nil {
		view["record_kind"] = "legacy"
	}
	return view, err
}

// The notice is the preserved main merchant-store-v1 public contract.
const legacyDisclaimerText = `Third-party products are sold and delivered by the listed merchant. Please read the description, delivery conditions, price and merchant contact details before ordering. The official badge identifies products sold by a current platform administrator; other products are independent merchant products.

The platform provides listing review, payment records and delivery links. A listing review is not a guarantee of product quality, suitability, legality or continued availability. Contact the merchant first about product issues, and keep your order number and payment record when requesting platform assistance. The platform may pause products or investigate reports.

Digital text and activation codes may be revealed immediately after confirmed payment. Do not share your private delivery link or pickup code. Check the merchant's stated terms before buying; any refund request must be handled according to the applicable order and payment terms.

Payments credited to a merchant's platform balance cannot be withdrawn and may only be used for consumption on the platform. External merchant gateways receive the payment directly, while the platform charges the merchant a service fee in credits.

By accepting, you confirm that you have read these terms and understand that you are purchasing from the named third-party merchant. You can reopen this notice at any time from the shop.`
