package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/LIghtJUNction/api.lmm.best/internal/extore"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/shopspring/decimal"
	"strings"
	"time"
)

type MerchantStoreExtoreSyncResult struct {
	SourceID     string `json:"source_id"`
	ProductID    string `json:"product_id,omitempty"`
	Success      bool   `json:"success"`
	Message      string `json:"message"`
	LastSyncedAt int64  `json:"last_synced_at,omitempty"`
}

func MerchantStoreExtoreConnectionCatalog(ctx context.Context, seller int, id string) (*extore.Catalog, error) {
	row, secret, err := model.GetMerchantStoreExtoreConnection(seller, id)
	if err != nil {
		return nil, err
	}
	var credentials extore.Credentials
	if json.Unmarshal([]byte(secret), &credentials) != nil {
		return nil, extore.ErrProtocol
	}
	if credentials.AccessExpires <= time.Now().Unix()+30 {
		if err := model.BeginMerchantStoreExtoreRefresh(row); err != nil {
			return nil, err
		}
		next, err := MerchantStoreExtoreClient.RefreshConnection(ctx, row.Origin, row.ClientID, credentials)
		// A lost rotation response leaves the durable marker set. Never retry it.
		if err != nil {
			return nil, model.ErrMerchantStoreExtoreReauthorize
		}
		raw, _ := json.Marshal(next)
		if err := model.FinishMerchantStoreExtoreRefresh(row, string(raw)); err != nil {
			return nil, err
		}
		credentials = *next
	}
	return MerchantStoreExtoreClient.ConnectionCatalog(ctx, row.Origin, credentials)
}
func MerchantStoreExtoreSyncListing(seller int, connectionID string, raw json.RawMessage) (*model.MerchantStoreExtoreProduct, error) {
	var listing struct {
		ID            string `json:"id"`
		Revision      string `json:"revision"`
		SyncRevision  string `json:"sync_revision"`
		RedemptionURL string `json:"redemption_url"`
		Product       struct {
			Name         string          `json:"name"`
			Description  string          `json:"description"`
			Logo         string          `json:"logo"`
			Image        string          `json:"image"`
			Public       bool            `json:"public"`
			SupportEmail string          `json:"support_email"`
			Marketplace  json.RawMessage `json:"marketplace"`
		} `json:"product"`
		Variants []struct {
			ID          string  `json:"id"`
			Name        string  `json:"name"`
			Enabled     bool    `json:"enabled"`
			Price       *string `json:"price"`
			Currency    string  `json:"currency"`
			Marketplace struct {
				PriceQuota   *int    `json:"price_quota"`
				Template     string  `json:"template"`
				FixedContent *string `json:"fixed_content"`
			} `json:"marketplace"`
		} `json:"variants"`
		CardSummary json.RawMessage `json:"card_summary"`
	}
	if json.Unmarshal(raw, &listing) != nil || listing.ID == "" || len(listing.Variants) == 0 || len(listing.Product.Marketplace) == 0 {
		return nil, extore.ErrProtocol
	}
	var in model.MerchantStoreProductInput
	if json.Unmarshal(listing.Product.Marketplace, &in) != nil {
		return nil, extore.ErrProtocol
	}
	var extra struct {
		SaleLimit *int64 `json:"sale_limit"`
	}
	if json.Unmarshal(listing.Product.Marketplace, &extra) != nil {
		return nil, extore.ErrProtocol
	}
	if in.Title == "" {
		in.Title = listing.Product.Name
	}
	if in.Description == "" {
		in.Description = listing.Product.Description
	}
	if in.ImageURLs == nil {
		in.ImageURLs = []string{listing.Product.Logo, listing.Product.Image}
	}
	if in.Contact == "" {
		in.Contact = listing.Product.SupportEmail
	}
	if in.Links == nil {
		in.Links = []model.MerchantStoreLink{{Title: "Extore", URL: listing.RedemptionURL}}
	}
	if in.Visibility == nil {
		visibility := "private"
		if listing.Product.Public {
			visibility = "public"
		}
		in.Visibility = &visibility
	}
	if in.CategoryID == nil {
		empty := ""
		in.CategoryID = &empty
	}
	variants := make([]model.MerchantStoreExtoreVariantInput, 0, len(listing.Variants))
	for _, variant := range listing.Variants {
		price := 0
		if variant.Marketplace.PriceQuota != nil {
			price = *variant.Marketplace.PriceQuota
		} else if variant.Price != nil && variant.Currency == "USD" {
			value, err := decimal.NewFromString(*variant.Price)
			if err != nil {
				return nil, model.ErrMerchantStoreInput
			}
			credits := value.Mul(decimal.NewFromInt(model.MerchantStoreCreditsPerUSD))
			if !credits.Equal(credits.Truncate(0)) || credits.GreaterThan(decimal.NewFromInt(9007199254740991)) {
				return nil, model.ErrMerchantStoreInput
			}
			price = int(credits.IntPart())
		}
		if price <= 0 {
			return nil, errors.New("Configure the selling price for every variant in Extore before syncing")
		}
		template := variant.Marketplace.Template
		if template == "" {
			template = "card-key"
		}
		variants = append(variants, model.MerchantStoreExtoreVariantInput{SourceID: variant.ID, Fields: model.MerchantStoreVariantInput{Name: variant.Name, PriceQuota: price, Template: template, Enabled: variant.Enabled, FixedContent: variant.Marketplace.FixedContent}})
	}
	revision := listing.SyncRevision
	if revision == "" {
		revision = listing.Revision
	}
	summary := listing.CardSummary
	if len(summary) == 0 {
		summary = json.RawMessage(`{}`)
	}
	return model.SyncMerchantStoreExtoreProduct(seller, connectionID, listing.ID, revision, in, variants, extra.SaleLimit, summary)
}
func MerchantStoreExtoreSyncMessage(err error) string {
	switch {
	case errors.Is(err, model.ErrMerchantStoreMinimumPrice):
		return "The Extore selling price is below the store minimum"
	case errors.Is(err, model.ErrMerchantStorePaymentSelection), errors.Is(err, model.ErrMerchantStorePaymentCategoryDisabled):
		return "Enable the payment methods selected in Extore for this store first"
	case errors.Is(err, model.ErrMerchantStoreInput):
		return "Check the product and all variant sales settings in Extore"
	case errors.Is(err, model.ErrMerchantStoreWriterFrozen):
		return "Store synchronization is temporarily unavailable during an upgrade"
	case errors.Is(err, model.ErrMerchantStoreConflict):
		return "This product is under review; finish or cancel the review before syncing"
	case err != nil && strings.HasPrefix(err.Error(), "Configure the selling price"):
		return "Configure the selling price for every variant in Extore before syncing"
	default:
		return "The product could not be synchronized; try again after checking the connection"
	}
}
