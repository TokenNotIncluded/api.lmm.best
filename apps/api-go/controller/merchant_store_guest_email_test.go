package controller

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func guestEmailControllerRequest(t *testing.T, token, body string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request = httptest.NewRequest("POST", "/api/store/guest/email", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("X-Store-Guest", token)
	handler(c)
	return r
}

func TestMerchantStoreGuestEmailControllerHeaderIdentityAndPrivateResponse(t *testing.T) {
	db := setupManageUserTestDB(t)
	t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "guest-email-controller-test-key-20261007-123456789")
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	require.NoError(t, model.BootstrapMerchantStoreWriterGate(db))
	require.NoError(t, db.AutoMigrate(model.MerchantStoreModels()...))
	require.GreaterOrEqual(t, model.MerchantStoreWriterCapability, 5)
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.MerchantStoreWriterCapabilityOption).Update("value", "5").Error)
	a, err := model.CreateMerchantStoreGuestSession()
	require.NoError(t, err)
	b, err := model.CreateMerchantStoreGuestSession()
	require.NoError(t, err)
	old := merchantStoreGuestVerificationSender
	t.Cleanup(func() { merchantStoreGuestVerificationSender = old })
	code, address := "", ""
	merchantStoreGuestVerificationSender = func(_ context.Context, token, email, challengeID, sentCode string) error {
		require.Equal(t, a.Token, token)
		require.NoError(t, model.ValidateMerchantStoreGuestEmailChallenge(token, email, challengeID, sentCode))
		code, address = sentCode, email
		return nil
	}
	for _, body := range []string{`{"email":"a@example.test","user_id":0}`, `{"email":"a@example.test","guest_id":"fake"}`, `{"email":"a@example.test"} {}`} {
		r := guestEmailControllerRequest(t, a.Token, body, SendMerchantStoreGuestEmailVerification)
		require.Equal(t, 422, r.Code)
	}
	r := guestEmailControllerRequest(t, "", `{"email":"a@example.test"}`, SendMerchantStoreGuestEmailVerification)
	require.Equal(t, 403, r.Code)
	r = guestEmailControllerRequest(t, a.Token, `{"email":"a@example.test"}`, SendMerchantStoreGuestEmailVerification)
	require.Equal(t, 200, r.Code, r.Body.String())
	require.Equal(t, "no-store", r.Header().Get("Cache-Control"))
	require.NotContains(t, r.Body.String(), code)
	require.NotContains(t, r.Body.String(), address)
	require.NotContains(t, r.Body.String(), a.Token)
	var result struct {
		Data struct {
			ChallengeID string `json:"challenge_id"`
			ExpiresAt   int64  `json:"expires_at"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(r.Body.Bytes(), &result))
	require.Len(t, result.Data.ChallengeID, 36)
	confirmation := `{"email":"a@example.test","challenge_id":"` + result.Data.ChallengeID + `","code":"` + code + `"}`
	r = guestEmailControllerRequest(t, b.Token, confirmation, ConfirmMerchantStoreGuestEmailVerification)
	require.Equal(t, 422, r.Code)
	r = guestEmailControllerRequest(t, a.Token, confirmation, ConfirmMerchantStoreGuestEmailVerification)
	require.Equal(t, 200, r.Code, r.Body.String())
	require.Contains(t, r.Body.String(), `"verified":true`)
	r = guestEmailControllerRequest(t, b.Token, `{"email":"a@example.test"}`, GetMerchantStoreGuestEmailStatus)
	require.Equal(t, 200, r.Code)
	require.Contains(t, r.Body.String(), `"verified":false`)
	r = guestEmailControllerRequest(t, a.Token, `{"email":"a@example.test"}`, GetMerchantStoreGuestEmailStatus)
	require.Equal(t, 200, r.Code)
	require.Contains(t, r.Body.String(), `"verified":true`)
}
