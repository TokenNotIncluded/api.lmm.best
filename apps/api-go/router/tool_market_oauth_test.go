package router

import (
	"net/url"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/stretchr/testify/require"
)

func TestToolMarketOAuthRequiresNewConsentWithoutWideningLegacy(t *testing.T) {
	for _, extra := range []string{"", " market:discover", " market:discover market:invoke", " market:discover market:manage"} {
		t.Run(strings.ReplaceAll(extra, " ", "_"), func(t *testing.T) {
			h := setupOAuthHTTP(t)
			query, err := url.ParseQuery(h.query)
			require.NoError(t, err)
			query.Set("scope", query.Get("scope")+extra)
			h.query = query.Encode()
			token, _ := h.approve(t)
			for _, scope := range []string{service.OAuthMarketDiscoverScope, service.OAuthMarketInvokeScope, service.OAuthMarketManageScope} {
				if strings.Contains(extra, scope) {
					require.Contains(t, strings.Fields(token.Scope), scope)
				} else {
					require.NotContains(t, strings.Fields(token.Scope), scope)
				}
			}
		})
	}
	h := setupOAuthHTTP(t)
	query, err := url.ParseQuery(h.query)
	require.NoError(t, err)
	query.Set("scope", query.Get("scope")+" market:invoke")
	_, _, err = h.integration.ConsentQuery(query.Encode(), &h.user)
	require.ErrorIs(t, err, service.ErrOAuthDenied)
	query.Set("client_id", service.OAuthCLIClientID)
	query.Set("scope", "catalog:read balance:read market:discover")
	_, _, err = h.integration.ConsentQuery(query.Encode(), &h.user)
	require.ErrorIs(t, err, service.ErrOAuthDenied)
}
