package router

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func storeAccessRequest(engine *gin.Engine, method, path, accountToken, guestToken, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if accountToken != "" {
		req.Header.Set("Authorization", "Bearer "+accountToken)
	}
	if guestToken != "" {
		req.Header.Set("X-Store-Guest", guestToken)
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, req)
	return response
}

func TestMerchantStoreGuestRoutesDoNotBecomeMemberWithBearer(t *testing.T) {
	engine, db, sellerToken, seller, _, root := merchantStoreTestRouter(t)
	product := shopPublishedProduct(t, db, seller, root)
	raw := make([]byte, 32)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	guest := model.MerchantStoreGuest{ID: uuid.NewString(), TokenHash: hex.EncodeToString(hash[:]), CreatedAt: common.GetTimestamp(), ExpiresAt: common.GetTimestamp() + 3600}
	require.NoError(t, db.Create(&guest).Error)
	input := `{"product_id":"` + product.ID + `","quantity":1,"request_key":"mixed-authority","payment_method":"balance","pickup_code":"safe-private-code"}`
	forged := strings.Replace(input, `"quantity":1`, `"buyer_id":1,"quantity":1`, 1)
	response := storeAccessRequest(engine, "POST", "/api/store/guest/orders", sellerToken, token, forged)
	require.Equal(t, 422, response.Code, response.Body.String())
	response = storeAccessRequest(engine, "POST", "/api/store/guest/orders", sellerToken, token, input)
	require.Equal(t, 403, response.Code, response.Body.String())
	response = storeAccessRequest(engine, "POST", "/api/store/orders", sellerToken, token, input)
	require.Equal(t, 403, response.Code, response.Body.String())
	var count int64
	require.NoError(t, db.Model(&model.MerchantStoreOrder{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, model.AcceptMerchantStoreDisclaimer(seller.Id, model.MerchantStoreDisclaimerVersion))
	response = storeAccessRequest(engine, "GET", "/api/store/disclaimer", sellerToken, token, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"accepted":false`)
	response = storeAccessRequest(engine, "POST", "/api/store/guest/disclaimer/accept", sellerToken, token, `{"version":"`+model.MerchantStoreDisclaimerVersion+`","accepted":false}`)
	require.Equal(t, 422, response.Code, response.Body.String())
	response = storeAccessRequest(engine, "POST", "/api/store/guest/orders/lookup", sellerToken, token, `{"request_key":"absent"}`)
	require.Equal(t, 403, response.Code, response.Body.String())
	response = storeAccessRequest(engine, "POST", "/api/store/guest/orders/lookup", sellerToken, "", `{"request_key":"absent"}`)
	require.Equal(t, 403, response.Code, response.Body.String())
	require.NoError(t, db.Model(product).Updates(map[string]any{"visibility": "private", "test_mode": true}).Error)
	response = storeAccessRequest(engine, "GET", "/api/store/products/"+product.ID, sellerToken, token, "")
	require.Equal(t, 404, response.Code, response.Body.String())
	response = storeAccessRequest(engine, "GET", "/api/store/products/"+product.ID, sellerToken, "", "")
	require.Equal(t, 200, response.Code, response.Body.String())
}

func TestMerchantStoreAccessRoutesRemainFrozenBeforeActivation(t *testing.T) {
	engine, db, sellerToken, seller, _, root := merchantStoreTestRouter(t)
	product := shopPublishedProduct(t, db, seller, root)
	response := shopRequest(engine, "POST", "/api/store/guest/session", "", `{}`)
	require.Equal(t, 503, response.Code, response.Body.String())
	response = shopRequest(engine, "PUT", "/api/store/my/terms", sellerToken, `{"content":"Actual seller terms","expected_version":""}`)
	require.Equal(t, 503, response.Code, response.Body.String())
	response = shopRequest(engine, "PUT", "/api/store/my/terms", sellerToken, `{"content":" ","expected_version":""}`)
	require.Equal(t, 422, response.Code, response.Body.String())
	response = shopRequest(engine, "PUT", "/api/store/my/terms", sellerToken, `{"content":"Actual terms","expected_version":"","seller_id":999}`)
	require.Equal(t, 422, response.Code, response.Body.String())
	response = shopRequest(engine, "GET", "/api/store/products/"+product.ID+"/terms", "", "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"configured":false`)
	for _, row := range []any{&model.MerchantStoreGuest{}, &model.MerchantStoreSellerTerms{}, &model.MerchantStoreTermsAcceptance{}} {
		var count int64
		require.NoError(t, db.Model(row).Count(&count).Error)
		require.Zero(t, count)
	}
}
