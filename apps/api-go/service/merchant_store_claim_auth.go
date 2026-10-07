package service

import (
	"crypto/hmac"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const merchantStoreCookieClaimPrefix = "/api/user/auth/store-claim/"

// ValidateMerchantStoreClaimPath applies the same exact endpoint boundary to
// bearer, cookie and anonymous pickup modes. Encoded aliases are not accepted.
func ValidateMerchantStoreClaimPath(request *http.Request, token string) error {
	if request == nil || request.URL == nil ||
		(request.Method != http.MethodGet && request.Method != http.MethodPost) ||
		len(token) != 43 || request.URL.Path != merchantStoreCookieClaimPrefix+token ||
		request.URL.EscapedPath() != request.URL.Path {
		return model.ErrMerchantStoreDenied
	}
	for _, r := range token {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return model.ErrMerchantStoreDenied
		}
	}
	return nil
}

// MerchantStoreClaimHasAuthorization distinguishes an absent header from a
// malformed or empty credential. Neither may be replaced by a refresh cookie.
func MerchantStoreClaimHasAuthorization(request *http.Request) bool {
	if request == nil {
		return false
	}
	for name := range request.Header {
		if strings.EqualFold(name, "Authorization") {
			return true
		}
	}
	return false
}

// MerchantStoreClaimBuyerMatches is only an order-scoped ownership check. It
// never grants an identity to other APIs and returns no account information.
func MerchantStoreClaimBuyerMatches(token string, buyerID int) bool {
	order, err := merchantStoreClaimPaidOrder(token)
	if err != nil || buyerID <= 0 || order.BuyerID != buyerID {
		return false
	}
	var user model.User
	return model.DB.Where("id = ? AND status = ?", buyerID, common.UserStatusEnabled).First(&user).Error == nil
}

func merchantStoreClaimPaidOrder(token string) (*model.MerchantStoreOrder, error) {
	metadata, err := model.InspectMerchantStoreClaim(token)
	if err != nil {
		return nil, model.ErrMerchantStoreDenied
	}
	order, err := model.GetMerchantStorePaymentOrder(metadata.OrderID)
	if err != nil || order.Status != "paid" || order.BuyerID <= 0 {
		return nil, model.ErrMerchantStoreDenied
	}
	return order, nil
}

// ValidateMerchantStoreClaimCookie reads the current session directly from the
// database. It does not rotate refresh credentials, issue tokens, populate a
// global auth context, or update session activity/IP. The result is valid only
// for this paid order; delivery must recheck through the transactional helper.
func ValidateMerchantStoreClaimCookie(request *http.Request, token string) (int, error) {
	sid, secret, err := merchantStoreClaimCookieCredential(request, token)
	if err != nil {
		return 0, err
	}
	order, err := merchantStoreClaimPaidOrder(token)
	if err != nil {
		return 0, err
	}
	if err = merchantStoreClaimSessionValid(model.DB.WithContext(request.Context()), order.BuyerID, sid, secret, false); err != nil {
		return 0, err
	}
	return order.BuyerID, nil
}

// ClaimMerchantStoreOrderWithCookie revalidates the owner and refresh session
// under the delivery transaction's locks. A successful earlier metadata read
// cannot survive a committed logout or account-security change.
func ClaimMerchantStoreOrderWithCookie(request *http.Request, token, code string) (*model.MerchantStoreClaim, error) {
	sid, secret, err := merchantStoreClaimCookieCredential(request, token)
	if err != nil {
		return nil, err
	}
	order, err := merchantStoreClaimPaidOrder(token)
	if err != nil {
		return nil, err
	}
	return model.ClaimMerchantStoreOrderWithAuthorization(token, code, order.BuyerID, func(tx *gorm.DB, current *model.MerchantStoreOrder) error {
		if current.ID != order.ID || current.BuyerID != order.BuyerID {
			return model.ErrMerchantStoreDenied
		}
		return merchantStoreClaimSessionValid(tx.WithContext(request.Context()), current.BuyerID, sid, secret, true)
	})
}

func merchantStoreClaimCookieCredential(request *http.Request, token string) (string, string, error) {
	if ValidateMerchantStoreClaimPath(request, token) != nil || MerchantStoreClaimHasAuthorization(request) || !merchantStoreClaimSameOrigin(request) {
		return "", "", model.ErrMerchantStoreDenied
	}
	var raw string
	count := 0
	for _, cookie := range request.Cookies() {
		if cookie.Name == RefreshCookieName {
			raw = cookie.Value
			count++
		}
	}
	if count != 1 || len(raw) > 512 {
		return "", "", model.ErrMerchantStoreDenied
	}
	sid, secret, ok := splitRefreshToken(raw)
	if !ok {
		return "", "", model.ErrMerchantStoreDenied
	}
	return sid, secret, nil
}

func merchantStoreClaimSameOrigin(request *http.Request) bool {
	expected, err := common.NormalizeOrigin(system_setting.ServerAddress)
	if err != nil || common.SessionCookieSecure && !strings.HasPrefix(expected, "https://") {
		return false
	}
	parsed, err := url.Parse(expected)
	if err != nil {
		return false
	}
	// The canonical server origin defines the scheme behind the reverse proxy;
	// forwarded host/proto headers are deliberately ignored.
	requestOrigin, err := common.NormalizeOrigin(parsed.Scheme + "://" + request.Host)
	if err != nil || requestOrigin != expected {
		return false
	}
	values := request.Header.Values("Sec-Fetch-Site")
	if len(values) > 1 || len(values) == 1 && strings.TrimSpace(values[0]) != "same-origin" {
		return false
	}
	proved := len(values) == 1
	origins := request.Header.Values("Origin")
	if len(origins) > 1 {
		return false
	}
	if len(origins) == 1 {
		origin, err := common.NormalizeOrigin(origins[0])
		if err != nil || origin != expected {
			return false
		}
		proved = true
	}
	referers := request.Header.Values("Referer")
	if len(referers) > 1 {
		return false
	}
	if len(referers) == 1 {
		referer, err := url.Parse(strings.TrimSpace(referers[0]))
		if err != nil || referer.User != nil || referer.Host == "" {
			return false
		}
		origin, err := common.NormalizeOrigin(referer.Scheme + "://" + referer.Host)
		if err != nil || origin != expected {
			return false
		}
		proved = true
	}
	return proved
}

func merchantStoreClaimSessionValid(tx *gorm.DB, buyerID int, sid, secret string, lock bool) error {
	query := func() *gorm.DB {
		if lock && tx.Dialector.Name() != "sqlite" {
			return tx.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		return tx
	}
	// User then session matches the account-security mutation lock order.
	var user model.User
	if query().Where("id = ? AND status = ?", buyerID, common.UserStatusEnabled).First(&user).Error != nil || user.AuthVersion <= 0 {
		return model.ErrMerchantStoreDenied
	}
	var session model.UserSession
	if query().Where("sid = ? AND user_id = ?", sid, buyerID).First(&session).Error != nil {
		return model.ErrMerchantStoreDenied
	}
	now := time.Now().Unix()
	if session.Status != model.UserSessionStatusActive || session.RevokedAt != 0 || session.ExpiresAt <= now ||
		session.Version <= 0 || session.UserAuthVersion <= 0 || session.UserAuthVersion != user.AuthVersion ||
		session.CreatedAt <= 0 || session.CreatedAt > now ||
		user.GetSetting().IsSessionAutoLogoutEnabled() && session.CreatedAt < now-int64(model.UserSessionAutoLogoutAge/time.Second) {
		return model.ErrMerchantStoreDenied
	}
	presented := hashRefreshSecret(secret)
	if hmac.Equal([]byte(presented), []byte(session.RefreshHash)) {
		return nil
	}
	// Accept only the existing explicit rotation grace and the same secret
	// lineage used by RefreshLoginSession. This endpoint never extends it.
	if session.PreviousRefreshHash != "" && now <= session.PreviousValidUntil &&
		session.PreviousValidUntil <= now+int64(RefreshReplayWindow/time.Second) &&
		hmac.Equal([]byte(presented), []byte(session.PreviousRefreshHash)) &&
		hmac.Equal([]byte(hashRefreshSecret(deriveNextRefreshSecret(sid, secret))), []byte(session.RefreshHash)) {
		return nil
	}
	return model.ErrMerchantStoreDenied
}
