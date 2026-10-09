package controller

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/internal/extore"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreExtoreMerchantKeyCallback(t *testing.T) {
	t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "merchant-extore-callback-fixture-20261009-123456789")
	t.Setenv("CRYPTO_SECRET", "")
	t.Setenv("SESSION_SECRET", "")
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AuthFlow{}))
	flow, err := extore.NewFlow("https://extore.example", "client", "https://shop.example/store/manage")
	require.NoError(t, err)
	raw, err := json.Marshal(flow)
	require.NoError(t, err)
	encrypted, err := service.EncryptMerchantStoreExtoreFlow(string(raw))
	require.NoError(t, err)
	require.NotContains(t, encrypted, flow.Verifier)
	state, _, err := model.CreateAuthFlow(model.AuthFlowCreate{Purpose: storeExtorePurpose, UserId: 7, SessionId: "session-a", Payload: encrypted, ExpiresAt: time.Now().Add(time.Minute)})
	require.NoError(t, err)
	callback := flow.RedirectURI + "?error=access_denied&state=" + state + "&iss=" + url.QueryEscape(flow.Origin)
	response := extoreCallbackRequest(t, 7, "session-a", callback)
	require.Equal(t, 403, response.Code, response.Body.String())
	require.Equal(t, 409, extoreCallbackRequest(t, 7, "session-a", callback).Code)
}

func TestMerchantStoreExtoreStorageErrorIsActionable(t *testing.T) {
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	storeExtoreError(c, common.ErrPersistentKeyUnavailable)
	require.Equal(t, 503, response.Code)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, "STORE_EXTORE_SECURE_STORAGE", body.Code)
	require.Contains(t, body.Message, "administrator")
	require.NotContains(t, body.Message, "later")
}
