package controller

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/internal/extore"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func extoreCallbackRequest(t *testing.T, user int, session, callback string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"callback_url": callback})
	require.NoError(t, err)
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest("POST", "/api/store/extore/catalog", strings.NewReader(string(body)))
	c.Set("id", user)
	c.Set("session_id", session)
	c.Set("auth_version", int64(1))
	c.Set("session_version", int64(1))
	ReadMerchantStoreExtoreCatalog(c)
	return response
}

func TestMerchantStoreExtoreCallbackBindsAccountAndSessionAndConsumesOnce(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AuthFlow{}))
	t.Setenv("CRYPTO_SECRET", "store-extore-test-only-32-byte-encryption-secret")
	flow, err := extore.NewFlow("https://extore.example", "client", "https://shop.example/store/manage")
	require.NoError(t, err)
	raw, err := json.Marshal(flow)
	require.NoError(t, err)
	encrypted, err := common.EncryptPersistentString(storeExtorePurpose, "CRYPTO_SECRET", "SESSION_SECRET", string(raw))
	require.NoError(t, err)
	state, record, err := model.CreateAuthFlow(model.AuthFlowCreate{Purpose: storeExtorePurpose, UserId: 7, SessionId: "session-a", Payload: encrypted, ExpiresAt: time.Now().Add(time.Minute)})
	require.NoError(t, err)
	require.NotContains(t, record.Payload, flow.Verifier)
	// A denial avoids all external calls while exercising the same binding and consume path.
	callback := flow.RedirectURI + "?error=access_denied&state=" + state + "&iss=" + url.QueryEscape(flow.Origin)
	require.Equal(t, 401, extoreCallbackRequest(t, 7, "", callback).Code)
	require.Equal(t, 409, extoreCallbackRequest(t, 8, "session-a", callback).Code)
	require.Equal(t, 409, extoreCallbackRequest(t, 7, "session-b", callback).Code)
	require.Equal(t, 422, extoreCallbackRequest(t, 7, "session-a", callback+"&state=duplicate").Code)
	require.Equal(t, 422, extoreCallbackRequest(t, 7, "session-a", strings.Replace(callback, "extore.example", "other.example", 1)).Code)
	denied := extoreCallbackRequest(t, 7, "session-a", callback)
	require.Equal(t, 403, denied.Code, denied.Body.String())
	require.NotContains(t, denied.Body.String(), flow.Verifier)
	require.Equal(t, 409, extoreCallbackRequest(t, 7, "session-a", callback).Code)
}
