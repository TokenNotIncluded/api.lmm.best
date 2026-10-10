package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/setting"
	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

var ErrWaffoPancakePortalUnavailable = errors.New("Waffo Pancake customer portal is not configured")

// CreateWaffoPancakeCustomerPortalLink reuses the authenticated checkout identity.
// Store and environment come from server configuration, never browser input.
// Unlike new purchases, existing customers must not depend on a wallet product
// or a currently enabled payment method to manage their subscriptions.
func CreateWaffoPancakeCustomerPortalLink(ctx context.Context, userID int) (*WaffoPancakeCustomerPortalLink, error) {
	if userID <= 0 {
		return nil, errors.New("missing authenticated customer")
	}
	storeID := strings.TrimSpace(setting.WaffoPancakeStoreID)
	merchantID, privateKey := WaffoPancakeCredentials()
	if storeID == "" || merchantID == "" || privateKey == "" {
		return nil, ErrWaffoPancakePortalUnavailable
	}
	client, err := newWaffoPancakeClientFromCreds(merchantID, privateKey)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// Mint a fresh portal token on each click. A fixed idempotency key would
	// replay a previously issued bearer URL, potentially after its expiry.
	result, err := client.Auth.CreateCustomerPortalLink(ctx, pancake.CreateCustomerPortalLinkParams{
		StoreID: storeID, BuyerIdentity: WaffoPancakeBuyerIdentityFromUserID(userID),
	})
	if err != nil {
		return nil, err
	}
	var link *WaffoPancakeCustomerPortalLink
	if result != nil {
		link = &WaffoPancakeCustomerPortalLink{PortalURL: result.PortalURL, ExpiresAt: result.ExpiresAt}
	}
	if err := validateWaffoPancakeCustomerPortalLink(link, storeID, time.Now()); err != nil {
		return nil, err
	}
	return link, nil
}
