package service

import (
	"context"
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
)

// OAuthBrowserIdentity is derived only from a verified HttpOnly refresh cookie.
// It is never accepted from a form, query, dashboard JWT, or caller-supplied ID.
// The cookie remains scoped to /api/user/auth; browser-only OAuth forms use a
// subpath there, avoiding any widening of the existing login cookie.
type OAuthBrowserIdentity struct {
	UserID         int64
	SessionID      string
	SessionVersion int64
	AuthVersion    int64
}

func (s *OAuthIntegration) BrowserIdentity(ctx context.Context, request *http.Request) (OAuthBrowserIdentity, *model.User, error) {
	var empty OAuthBrowserIdentity
	var raw string
	count := 0
	for _, cookie := range request.Cookies() {
		if cookie.Name == RefreshCookieName {
			count++
			raw = cookie.Value
		}
	}
	if count != 1 || len(raw) > 512 || len(request.Header.Values("Authorization")) != 0 {
		return empty, nil, ErrOAuthDenied
	}
	sid, secret, ok := splitRefreshToken(raw)
	if !ok {
		return empty, nil, ErrOAuthDenied
	}
	var session model.UserSession
	if err := s.DB.WithContext(ctx).First(&session, "sid = ?", sid).Error; err != nil {
		return empty, nil, ErrOAuthDenied
	}
	now := time.Now().Unix()
	if session.Status != model.UserSessionStatusActive || session.RevokedAt != 0 || session.ExpiresAt <= now {
		return empty, nil, ErrOAuthDenied
	}
	presented := hashRefreshSecret(secret)
	current := subtle.ConstantTimeCompare([]byte(presented), []byte(session.RefreshHash)) == 1
	previous := session.PreviousValidUntil >= now && session.PreviousRefreshHash != "" && subtle.ConstantTimeCompare([]byte(presented), []byte(session.PreviousRefreshHash)) == 1
	if !current && !previous {
		return empty, nil, ErrOAuthDenied
	}
	var user model.User
	if err := s.DB.WithContext(ctx).First(&user, "id = ?", session.UserID).Error; err != nil || user.Status != common.UserStatusEnabled || user.AuthVersion != session.UserAuthVersion {
		return empty, nil, ErrOAuthDenied
	}
	if err := enforceSessionAutoLogout(&session, user.GetSetting().IsSessionAutoLogoutEnabled(), now); err != nil {
		return empty, nil, ErrOAuthDenied
	}
	return OAuthBrowserIdentity{UserID: int64(user.Id), SessionID: session.SID, SessionVersion: session.Version, AuthVersion: user.AuthVersion}, &user, nil
}
