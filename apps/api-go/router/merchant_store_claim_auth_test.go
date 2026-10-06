package router

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type merchantStoreCookieRouterFixture struct {
	engine                           *gin.Engine
	db                               *gorm.DB
	buyer                            model.User
	buyerPAT, sellerPAT, pickupToken string
	order                            *model.MerchantStoreOrder
	bundle                           *service.AuthBundle
}

func merchantStoreCookieRouter(t *testing.T) merchantStoreCookieRouterFixture {
	t.Helper()
	engine, db, sellerPAT, seller, _, root := merchantStoreTestRouter(t)
	oldOrigin, oldSecret, oldSecure := system_setting.ServerAddress, common.SessionSecret, common.SessionCookieSecure
	system_setting.ServerAddress, common.SessionSecret, common.SessionCookieSecure = "https://api.example.com", "shop-cookie-router-offline-key", true
	t.Cleanup(func() {
		system_setting.ServerAddress, common.SessionSecret, common.SessionCookieSecure = oldOrigin, oldSecret, oldSecure
	})
	require.NoError(t, db.AutoMigrate(&model.UserSession{}))
	product := shopPublishedProduct(t, db, seller, root)
	buyerPAT := "shop-cookie-router-buyer"
	buyer := model.User{Username: "cookie-buyer", AffCode: "cookie-buyer", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AccessToken: &buyerPAT, Quota: 2000000}
	require.NoError(t, db.Create(&buyer).Error)
	require.NoError(t, model.AcceptMerchantStoreDisclaimer(buyer.Id, model.MerchantStoreDisclaimerVersion))
	order, _, err := model.CreateMerchantStoreOrder(model.MerchantStoreCheckoutInput{BuyerID: buyer.Id, ProductID: product.ID, Quantity: 1, RequestKey: "cookie-router-order", PaymentMethod: "balance", PickupCode: "private-pickup-code"})
	require.NoError(t, err)
	pickupToken, err := model.GetMerchantStoreOrderPickupToken(buyer.Id, order.ID)
	require.NoError(t, err)
	// Construct an existing login offline; pickup handlers themselves must never
	// create/refresh a session or return this global bundle.
	bundle, err := service.CreateLoginSession(buyer.Id, "password", "192.0.2.1", "prior-browser")
	require.NoError(t, err)
	return merchantStoreCookieRouterFixture{engine, db, buyer, buyerPAT, sellerPAT, pickupToken, order, bundle}
}

func merchantStoreCookieRouterRequest(f merchantStoreCookieRouterFixture, method, path, authorization string, supplied bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "https://api.example.com"+path, strings.NewReader(`{"pickup_code":"private-pickup-code"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if method == http.MethodPost {
		req.Header.Set("Origin", "https://api.example.com")
	}
	if supplied {
		req.Header.Set("Authorization", authorization)
	}
	req.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: f.bundle.RefreshToken})
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	return w
}

func merchantStoreCookieRouterSatisfied(t *testing.T, response *httptest.ResponseRecorder, expected bool) {
	t.Helper()
	require.Equal(t, 200, response.Code, response.Body.String())
	var body struct {
		Data struct {
			Satisfied bool `json:"pickup_login_satisfied"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, expected, body.Data.Satisfied)
}

func TestMerchantStoreClaimRouterColdCookieJarOnlyAuthorizesOwnPickup(t *testing.T) {
	f := merchantStoreCookieRouter(t)
	server := httptest.NewTLSServer(f.engine)
	t.Cleanup(server.Close)
	system_setting.ServerAddress = server.URL
	client := server.Client()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client.Jar = jar
	cookieURL, err := url.Parse(server.URL + "/api/user/auth/refresh")
	require.NoError(t, err)
	jar.SetCookies(cookieURL, []*http.Cookie{{Name: service.RefreshCookieName, Value: f.bundle.RefreshToken, Path: "/api/user/auth", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode}})
	claimPath := "/api/user/auth/store-claim/" + f.pickupToken
	var before model.UserSession
	require.NoError(t, f.db.First(&before, "sid = ?", f.bundle.Session.SID).Error)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		req, err := http.NewRequest(method, server.URL+claimPath, strings.NewReader(`{"pickup_code":"private-pickup-code"}`))
		require.NoError(t, err)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Content-Type", "application/json")
		if method == http.MethodPost {
			req.Header.Set("Origin", server.URL)
		}
		require.Empty(t, req.Header.Get("Authorization"))
		response, err := client.Do(req)
		require.NoError(t, err)
		payload, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		require.Equal(t, 200, response.StatusCode, string(payload))
		require.Empty(t, response.Header.Values("Set-Cookie"))
		require.Equal(t, "no-store", response.Header.Get("Cache-Control"))
		require.Equal(t, "no-referrer", response.Header.Get("Referrer-Policy"))
		for _, secret := range []string{f.bundle.RefreshToken, f.bundle.AccessToken, f.bundle.Session.SID, before.RefreshHash, "user_id", "session_id", "buyer_id"} {
			require.NotContains(t, string(payload), secret)
		}
		if method == http.MethodGet {
			require.Contains(t, string(payload), `"pickup_login_satisfied":true`)
			require.NotContains(t, string(payload), "PRIVATE-CARD")
		} else {
			require.Contains(t, string(payload), "PRIVATE-CARD-ONE")
			require.NotContains(t, string(payload), "PRIVATE-CARD-TWO")
		}
	}
	var after model.UserSession
	require.NoError(t, f.db.First(&after, "sid = ?", before.SID).Error)
	require.Equal(t, before, after)
	// The browser's existing cookie Path remains narrow; even a manually sent
	// cookie does not authorize ordinary shop APIs or the original claim route.
	otherURL, err := url.Parse(server.URL + "/api/store/my/orders")
	require.NoError(t, err)
	require.Empty(t, jar.Cookies(otherURL))
	response := merchantStoreCookieRouterRequest(f, http.MethodGet, "/api/store/my/orders", "", false)
	require.Equal(t, 401, response.Code, response.Body.String())
	response = merchantStoreCookieRouterRequest(f, http.MethodPost, "/api/store/claim/"+f.pickupToken, "", false)
	require.Equal(t, 403, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "PRIVATE-CARD")
}

func TestMerchantStoreClaimRouterBearerPriorityPreventsCookieCompensation(t *testing.T) {
	f := merchantStoreCookieRouter(t)
	path := "/api/user/auth/store-claim/" + f.pickupToken
	response := merchantStoreCookieRouterRequest(f, http.MethodGet, path, "Bearer "+f.sellerPAT, true)
	merchantStoreCookieRouterSatisfied(t, response, false)
	response = merchantStoreCookieRouterRequest(f, http.MethodPost, path, "Bearer "+f.sellerPAT, true)
	require.Equal(t, 403, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "PRIVATE-CARD")
	for _, authorization := range []string{"", " ", "garbage", "Bearer unknown"} {
		response = merchantStoreCookieRouterRequest(f, http.MethodGet, path, authorization, true)
		if response.Code == 200 {
			merchantStoreCookieRouterSatisfied(t, response, false)
		}
		response = merchantStoreCookieRouterRequest(f, http.MethodPost, path, authorization, true)
		require.NotEqual(t, 200, response.Code, response.Body.String())
		require.NotContains(t, response.Body.String(), "PRIVATE-CARD")
	}
	// Valid bearer authentication keeps its existing behavior without requiring
	// cookie-specific Origin/Sec-Fetch evidence.
	req := httptest.NewRequest(http.MethodPost, "https://api.example.com"+path, strings.NewReader(`{"pickup_code":"private-pickup-code"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+f.buyerPAT)
	req.Header.Set("Origin", "https://unrelated.example.com")
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "PRIVATE-CARD-ONE")
}

func TestMerchantStoreClaimRouterCrossSiteCookieDoesNotRevealOrDeliver(t *testing.T) {
	f := merchantStoreCookieRouter(t)
	path := "/api/user/auth/store-claim/" + f.pickupToken
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		req := httptest.NewRequest(method, "https://api.example.com"+path, strings.NewReader(`{"pickup_code":"private-pickup-code"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://attacker.example.com")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: f.bundle.RefreshToken})
		w := httptest.NewRecorder()
		f.engine.ServeHTTP(w, req)
		if method == http.MethodGet {
			merchantStoreCookieRouterSatisfied(t, w, false)
		} else {
			require.Equal(t, 403, w.Code, w.Body.String())
		}
		require.NotContains(t, w.Body.String(), "PRIVATE-CARD")
		require.Empty(t, w.Header().Values("Set-Cookie"))
	}
}

func TestMerchantStoreClaimRouterRevokedCookieCannotReuseMetadataPermission(t *testing.T) {
	f := merchantStoreCookieRouter(t)
	path := "/api/user/auth/store-claim/" + f.pickupToken
	merchantStoreCookieRouterSatisfied(t, merchantStoreCookieRouterRequest(f, http.MethodGet, path, "", false), true)
	require.NoError(t, f.db.Model(&model.UserSession{}).Where("sid = ?", f.bundle.Session.SID).Update("status", model.UserSessionStatusRevoked).Error)
	merchantStoreCookieRouterSatisfied(t, merchantStoreCookieRouterRequest(f, http.MethodGet, path, "", false), false)
	w := merchantStoreCookieRouterRequest(f, http.MethodPost, path, "", false)
	require.Equal(t, 403, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), "PRIVATE-CARD")
}

func TestMerchantStoreClaimRouterExactPathInEveryAuthenticationMode(t *testing.T) {
	f := merchantStoreCookieRouter(t)
	// A public pickup protected only by a pickup code still uses the exact new
	// route boundary, even though it intentionally needs no login credential.
	require.NoError(t, f.db.Model(f.order).Update("pickup_login_required", false).Error)
	prefix := "/api/user/auth/store-claim/"
	encoded := fmt.Sprintf("%s%%%02X%s", prefix, f.pickupToken[0], f.pickupToken[1:])
	for _, auth := range []struct {
		name   string
		bearer bool
		cookie bool
	}{{"bearer", true, false}, {"cookie", false, true}, {"anonymous_code_only", false, false}} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			for _, path := range []string{encoded, prefix + f.pickupToken[:42], prefix + f.pickupToken + "/extra"} {
				req := httptest.NewRequest(method, "https://api.example.com"+path, strings.NewReader(`{"pickup_code":"private-pickup-code"}`))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Sec-Fetch-Site", "same-origin")
				if method == http.MethodPost {
					req.Header.Set("Origin", "https://api.example.com")
				}
				if auth.bearer {
					req.Header.Set("Authorization", "Bearer "+f.buyerPAT)
				}
				if auth.cookie {
					req.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: f.bundle.RefreshToken})
				}
				w := httptest.NewRecorder()
				f.engine.ServeHTTP(w, req)
				require.NotEqual(t, 200, w.Code, auth.name+" "+method+" "+path)
				require.NotContains(t, w.Body.String(), "PRIVATE-CARD")
			}
		}
	}
	response := merchantStoreCookieRouterRequest(f, http.MethodGet, prefix+f.pickupToken+"?view=metadata", "", false)
	merchantStoreCookieRouterSatisfied(t, response, true)
}
