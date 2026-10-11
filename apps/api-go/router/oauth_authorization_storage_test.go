package router

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oauthserver"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/stretchr/testify/require"
)

func TestOAuthHTTPAuthorizationStoragePressureReturns503(t *testing.T) {
	for _, stage := range []string{"preflight", "authenticated continuation"} {
		t.Run(stage, func(t *testing.T) {
			h := setupOAuthHTTP(t)
			var cookie *http.Cookie
			var csrf string
			seedCount := oauthserver.MaxAuthorizationRows
			if stage == "authenticated continuation" {
				cookie, csrf = h.begin(t)
				seedCount--
			}
			rows := make([]model.OAuthServerAuthorization, seedCount)
			for i := range rows {
				rows[i] = model.OAuthServerAuthorization{Digest: fmt.Sprintf("capacity-row-%d", i), Issuer: h.integration.Issuer, ExpiresAtMs: time.Now().Add(time.Minute).UnixMilli()}
			}
			require.NoError(t, h.db.CreateInBatches(rows, 100).Error)
			response := h.request("GET", "/api/oauth2/authorize?"+h.query, "", nil)
			if stage == "authenticated continuation" {
				response = h.request("POST", "/api/user/auth/oauth2/continue", url.Values{"csrf": {csrf}}.Encode(), map[string]string{
					"Origin": oauthTestIssuer, "Content-Type": "application/x-www-form-urlencoded",
				}, cookie, &http.Cookie{Name: service.RefreshCookieName, Value: h.login.RefreshToken})
			}
			require.Equal(t, http.StatusServiceUnavailable, response.Code)
			require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			var count int64
			require.NoError(t, h.db.Model(&model.OAuthServerAuthorization{}).Count(&count).Error)
			require.EqualValues(t, oauthserver.MaxAuthorizationRows, count)
			require.NoError(t, h.db.Model(&model.OAuthServerGrant{}).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}
