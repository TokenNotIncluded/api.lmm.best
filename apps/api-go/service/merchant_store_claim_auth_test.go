package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type merchantStoreClaimAuthFixture struct {
	merchantStoreServiceFixture
	order   *model.MerchantStoreOrder
	session model.UserSession
	token   string
	secret  string
}

func merchantStoreClaimAuthDB(t *testing.T) merchantStoreClaimAuthFixture {
	t.Helper()
	f := merchantStoreClaimAuthFixture{merchantStoreServiceFixture: merchantStoreServiceDB(t, MerchantStoreBalance)}
	oldOrigin, oldSecret, oldSecure := system_setting.ServerAddress, common.SessionSecret, common.SessionCookieSecure
	system_setting.ServerAddress, common.SessionSecret, common.SessionCookieSecure = "https://api.example.com", "shop-claim-isolated-test-key", true
	t.Cleanup(func() {
		system_setting.ServerAddress, common.SessionSecret, common.SessionCookieSecure = oldOrigin, oldSecret, oldSecure
	})
	require.NoError(t, model.DB.AutoMigrate(&model.UserSession{}))
	require.NoError(t, model.DB.Model(f.product).Updates(map[string]any{"pickup_login_required": true, "pickup_code_required": true}).Error)
	var err error
	f.order, _, err = model.CreateMerchantStoreOrder(model.MerchantStoreCheckoutInput{BuyerID: f.buyer.Id, ProductID: f.product.ID, Quantity: 1, RequestKey: "claim-cookie-order", PaymentMethod: MerchantStoreBalance, PickupCode: "private-pickup-code", PickupEmail: f.buyer.Email})
	require.NoError(t, err)
	require.Equal(t, "paid", f.order.Status)
	f.token, err = model.GetMerchantStoreOrderPickupToken(f.buyer.Id, f.order.ID)
	require.NoError(t, err)
	require.NoError(t, model.DB.First(&f.buyer, f.buyer.Id).Error)
	f.secret = strings.Repeat("a", 64)
	now := time.Now().Unix()
	f.session = model.UserSession{SID: uuid.NewString(), UserID: f.buyer.Id, Version: 3, UserAuthVersion: f.buyer.AuthVersion, Status: model.UserSessionStatusActive, RefreshHash: hashRefreshSecret(f.secret), CreatedAt: now - 60, LastActiveAt: now - 40, ExpiresAt: now + 3600, LoginMethod: "password", IP: "192.0.2.1", UserAgent: "prior-browser"}
	require.NoError(t, model.DB.Create(&f.session).Error)
	return f
}

func merchantStoreClaimAuthRequest(f merchantStoreClaimAuthFixture, method string) *http.Request {
	req := httptest.NewRequest(method, "https://api.example.com"+merchantStoreCookieClaimPrefix+f.token, nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if method == http.MethodPost {
		req.Header.Set("Origin", "https://api.example.com")
	}
	req.AddCookie(&http.Cookie{Name: RefreshCookieName, Value: f.session.SID + "." + f.secret})
	return req
}

func merchantStoreClaimSessionUnchanged(t *testing.T, before model.UserSession) {
	t.Helper()
	var after model.UserSession
	require.NoError(t, model.DB.First(&after, "sid = ?", before.SID).Error)
	require.Equal(t, before, after, "pickup authentication must not rotate, revoke or touch activity/IP")
}

func TestMerchantStoreClaimCookieColdGetAndTransactionalDelivery(t *testing.T) {
	f := merchantStoreClaimAuthDB(t)
	req := merchantStoreClaimAuthRequest(f, http.MethodGet)
	require.Empty(t, req.Header.Get("Authorization"))
	require.Empty(t, req.Header.Get("Origin"))
	require.Empty(t, req.Header.Get("Referer"), "no-referrer cold navigation still has explicit Sec-Fetch-Site proof")
	buyerID, err := ValidateMerchantStoreClaimCookie(req, f.token)
	require.NoError(t, err)
	require.Equal(t, f.buyer.Id, buyerID)
	claim, err := ClaimMerchantStoreOrderWithCookie(merchantStoreClaimAuthRequest(f, http.MethodPost), f.token, "private-pickup-code")
	require.NoError(t, err)
	require.Equal(t, []string{"PRIVATE-CARD-ONE"}, claim.Items)
	merchantStoreClaimSessionUnchanged(t, f.session)
}

func TestMerchantStoreClaimCookieRejectsLiveSessionAndUserChanges(t *testing.T) {
	for _, test := range []struct {
		name    string
		session map[string]any
		user    map[string]any
	}{
		{"revoked", map[string]any{"status": model.UserSessionStatusRevoked}, nil},
		{"revoking", map[string]any{"status": model.UserSessionStatusRevoking}, nil},
		{"revoked_timestamp", map[string]any{"revoked_at": time.Now().Unix()}, nil},
		{"expired", map[string]any{"expires_at": time.Now().Unix() - 1}, nil},
		{"bad_session_version", map[string]any{"version": 0}, nil},
		{"bad_session_auth_version", map[string]any{"user_auth_version": 0}, nil},
		{"security_changed", nil, map[string]any{"auth_version": 99}},
		{"disabled", nil, map[string]any{"status": common.UserStatusDisabled}},
		{"wrong_buyer", map[string]any{"user_id": 999}, nil},
		{"unknown_secret", map[string]any{"refresh_hash": hashRefreshSecret("different-secret")}, nil},
		{"weekly_signout", map[string]any{"created_at": time.Now().Add(-8 * 24 * time.Hour).Unix()}, map[string]any{"setting": `{"session_auto_logout":true}`}},
		{"future_created", map[string]any{"created_at": time.Now().Unix() + 3600}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := merchantStoreClaimAuthDB(t)
			if test.session != nil {
				require.NoError(t, model.DB.Model(&model.UserSession{}).Where("sid = ?", f.session.SID).Updates(test.session).Error)
			}
			if test.user != nil {
				require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", f.buyer.Id).Updates(test.user).Error)
			}
			var before model.UserSession
			require.NoError(t, model.DB.First(&before, "sid = ?", f.session.SID).Error)
			buyerID, err := ValidateMerchantStoreClaimCookie(merchantStoreClaimAuthRequest(f, http.MethodGet), f.token)
			require.ErrorIs(t, err, model.ErrMerchantStoreDenied)
			require.Zero(t, buyerID)
			_, err = ClaimMerchantStoreOrderWithCookie(merchantStoreClaimAuthRequest(f, http.MethodPost), f.token, "private-pickup-code")
			require.ErrorIs(t, err, model.ErrMerchantStoreDenied)
			merchantStoreClaimSessionUnchanged(t, before)
		})
	}
}

func TestMerchantStoreClaimCookieRespectsExistingRotationGrace(t *testing.T) {
	f := merchantStoreClaimAuthDB(t)
	nextSecret := deriveNextRefreshSecret(f.session.SID, f.secret)
	require.NoError(t, model.DB.Model(&model.UserSession{}).Where("sid = ?", f.session.SID).Updates(map[string]any{"refresh_hash": hashRefreshSecret(nextSecret), "previous_refresh_hash": f.session.RefreshHash, "previous_valid_until": time.Now().Unix() + 20}).Error)
	_, err := ValidateMerchantStoreClaimCookie(merchantStoreClaimAuthRequest(f, http.MethodGet), f.token)
	require.NoError(t, err, "the real immediately previous credential is allowed only inside existing grace")
	current := f
	current.secret = nextSecret
	_, err = ValidateMerchantStoreClaimCookie(merchantStoreClaimAuthRequest(current, http.MethodGet), current.token)
	require.NoError(t, err)
	for _, update := range []map[string]any{
		{"previous_valid_until": time.Now().Unix() - 1},
		{"previous_valid_until": time.Now().Unix() + 600},
		{"previous_valid_until": time.Now().Unix() + 20, "refresh_hash": hashRefreshSecret("unrelated-rotation")},
	} {
		require.NoError(t, model.DB.Model(&model.UserSession{}).Where("sid = ?", f.session.SID).Updates(update).Error)
		var before model.UserSession
		require.NoError(t, model.DB.First(&before, "sid = ?", f.session.SID).Error)
		_, err = ValidateMerchantStoreClaimCookie(merchantStoreClaimAuthRequest(f, http.MethodGet), f.token)
		require.ErrorIs(t, err, model.ErrMerchantStoreDenied)
		merchantStoreClaimSessionUnchanged(t, before)
	}
}

func TestMerchantStoreClaimCookieCannotCompensateForAuthorization(t *testing.T) {
	f := merchantStoreClaimAuthDB(t)
	for _, authorization := range []string{"", " ", "garbage", "Bearer wrong", "Bearer other-account"} {
		req := merchantStoreClaimAuthRequest(f, http.MethodGet)
		req.Header.Set("Authorization", authorization)
		_, err := ValidateMerchantStoreClaimCookie(req, f.token)
		require.ErrorIs(t, err, model.ErrMerchantStoreDenied)
	}
	merchantStoreClaimSessionUnchanged(t, f.session)
}

func TestMerchantStoreClaimCookieRequiresExactPathAndSameOrigin(t *testing.T) {
	f := merchantStoreClaimAuthDB(t)
	for _, test := range []struct {
		name string
		edit func(*http.Request)
	}{
		{"standard_refresh", func(r *http.Request) { r.URL.Path = "/api/user/auth/refresh" }},
		{"old_claim", func(r *http.Request) { r.URL.Path = "/api/store/claim/" + f.token }},
		{"other_shop", func(r *http.Request) { r.URL.Path = "/api/store/my/orders" }},
		{"extra_segment", func(r *http.Request) { r.URL.Path += "/extra" }},
		{"wrong_method", func(r *http.Request) { r.Method = http.MethodDelete }},
		{"encoded_path", func(r *http.Request) {
			r.URL.RawPath = fmt.Sprintf("%s%%%02X%s", merchantStoreCookieClaimPrefix, f.token[0], f.token[1:])
		}},
		{"no_evidence", func(r *http.Request) { r.Header.Del("Sec-Fetch-Site") }},
		{"cross_site", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }},
		{"same_site", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-site") }},
		{"duplicate_fetch", func(r *http.Request) { r.Header.Add("Sec-Fetch-Site", "same-origin") }},
		{"wrong_origin", func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example.com") }},
		{"null_origin", func(r *http.Request) { r.Header.Set("Origin", "null") }},
		{"empty_origin", func(r *http.Request) { r.Header.Set("Origin", "") }},
		{"duplicate_origin", func(r *http.Request) {
			r.Header.Add("Origin", "https://api.example.com")
			r.Header.Add("Origin", "https://api.example.com")
		}},
		{"wrong_referer", func(r *http.Request) { r.Header.Set("Referer", "https://attacker.example.com/store/claim/"+f.token) }},
		{"forged_proxy", func(r *http.Request) {
			r.Host = "attacker.example.com"
			r.Header.Set("X-Forwarded-Host", "api.example.com")
			r.Header.Set("X-Forwarded-Proto", "https")
		}},
		{"missing_cookie", func(r *http.Request) { r.Header.Del("Cookie") }},
		{"wrong_cookie", func(r *http.Request) { r.Header.Set("Cookie", RefreshCookieName+"=not-valid") }},
		{"duplicate_cookie", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: RefreshCookieName, Value: f.session.SID + "." + f.secret})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := merchantStoreClaimAuthRequest(f, http.MethodGet)
			test.edit(req)
			_, err := ValidateMerchantStoreClaimCookie(req, f.token)
			require.ErrorIs(t, err, model.ErrMerchantStoreDenied)
		})
	}
	for _, evidence := range []string{"Origin", "Referer"} {
		req := merchantStoreClaimAuthRequest(f, http.MethodGet)
		req.Header.Del("Sec-Fetch-Site")
		value := "https://api.example.com"
		if evidence == "Referer" {
			value += "/store/claim/" + f.token
		}
		req.Header.Set(evidence, value)
		_, err := ValidateMerchantStoreClaimCookie(req, f.token)
		require.NoError(t, err)
	}
	merchantStoreClaimSessionUnchanged(t, f.session)
}

func TestMerchantStoreClaimCookieRechecksLogoutAfterMetadata(t *testing.T) {
	f := merchantStoreClaimAuthDB(t)
	_, err := ValidateMerchantStoreClaimCookie(merchantStoreClaimAuthRequest(f, http.MethodGet), f.token)
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.UserSession{}).Where("sid = ?", f.session.SID).Updates(map[string]any{"status": model.UserSessionStatusRevoked, "revoked_at": time.Now().Unix()}).Error)
	_, err = ClaimMerchantStoreOrderWithCookie(merchantStoreClaimAuthRequest(f, http.MethodPost), f.token, "private-pickup-code")
	require.ErrorIs(t, err, model.ErrMerchantStoreDenied)
	var order model.MerchantStoreOrder
	require.NoError(t, model.DB.First(&order, "id = ?", f.order.ID).Error)
	require.Zero(t, order.ClaimedAt)
}

func TestMerchantStoreClaimCookieKeepsPickupCodeAndPaidOrderChecks(t *testing.T) {
	f := merchantStoreClaimAuthDB(t)
	_, err := ClaimMerchantStoreOrderWithCookie(merchantStoreClaimAuthRequest(f, http.MethodPost), f.token, "wrong-code")
	require.ErrorIs(t, err, model.ErrMerchantStoreDenied)
	require.NoError(t, model.DB.Model(f.order).Update("status", "pending").Error)
	_, err = ValidateMerchantStoreClaimCookie(merchantStoreClaimAuthRequest(f, http.MethodGet), f.token)
	require.ErrorIs(t, err, model.ErrMerchantStoreDenied)
	_, err = ClaimMerchantStoreOrderWithCookie(merchantStoreClaimAuthRequest(f, http.MethodPost), f.token, "private-pickup-code")
	require.ErrorIs(t, err, model.ErrMerchantStoreDenied)
	merchantStoreClaimSessionUnchanged(t, f.session)
}
