package controller

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func storeDiscountControllerRequest(t *testing.T, actor int, product, promotion, query, body string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest("POST", "/api/store/products/"+product+"/promotions"+query, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: product}, {Key: "promotion_id", Value: promotion}}
	c.Set("id", actor)
	handler(c)
	return response
}

func TestMerchantStoreDiscountControllerPublicLinkHidesOwnerAndPrivateProducts(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	require.NoError(t, model.BootstrapMerchantStoreWriterGate(db))
	require.NoError(t, db.AutoMigrate(model.MerchantStoreModels()...))
	require.GreaterOrEqual(t, model.MerchantStoreWriterCapability, 4, "promotion positive tests require capability-4 integration")
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.MerchantStoreWriterCapabilityOption).Update("value", "4").Error)
	owner := model.User{Username: "discount-owner", AffCode: "discount-owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	other := model.User{Username: "discount-other", AffCode: "discount-other", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&other).Error)
	product := model.MerchantStoreProduct{ID: "001b258e-bc35-43a4-8653-6454e0d57caa", SellerID: owner.Id, Title: "Public product", Description: "PRIVATE-SELLER-NOTE", PriceQuota: 500000, PaymentMethods: []string{"balance"}, Status: "published"}
	require.NoError(t, db.Create(&product).Error)
	response := storeDiscountControllerRequest(t, other.Id, product.ID, "", "", `{"discount_bps":10000,"seller_id":`+strconv.Itoa(owner.Id)+`}`, SaveMerchantStoreDiscountCode)
	require.Equal(t, 403, response.Code)
	response = storeDiscountControllerRequest(t, owner.Id, product.ID, "", "", `{"discount_bps":10000}`, SaveMerchantStoreDiscountCode)
	require.Equal(t, 200, response.Code, response.Body.String())
	var created struct {
		Data model.MerchantStoreDiscountCode `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &created))
	require.NotEmpty(t, created.Data.Code)
	response = storeDiscountControllerRequest(t, 0, product.ID, "", "?code="+created.Data.Code, "", ResolveMerchantStoreDiscountCode)
	require.Equal(t, 200, response.Code, response.Body.String())
	for _, private := range []string{"seller_id", "uses_count", "reserved_count", "PRIVATE-SELLER-NOTE", "inventory", "pickup"} {
		require.NotContains(t, response.Body.String(), private)
	}
	require.NoError(t, db.Model(&product).Update("test_mode", true).Error)
	for _, actor := range []int{0, other.Id} {
		response = storeDiscountControllerRequest(t, actor, product.ID, "", "?code="+created.Data.Code, "", ResolveMerchantStoreDiscountCode)
		require.NotEqual(t, 200, response.Code)
		require.NotContains(t, response.Body.String(), created.Data.Code)
	}
	response = storeDiscountControllerRequest(t, owner.Id, product.ID, "", "?code="+created.Data.Code, "", ResolveMerchantStoreDiscountCode)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NoError(t, db.Model(&product).Updates(map[string]any{"test_mode": false, "status": "draft"}).Error)
	response = storeDiscountControllerRequest(t, 0, product.ID, "", "?code="+created.Data.Code, "", ResolveMerchantStoreDiscountCode)
	require.NotEqual(t, 200, response.Code)
	require.NotContains(t, response.Body.String(), created.Data.Code)
	response = storeDiscountControllerRequest(t, owner.Id, product.ID, "", "?limit=1", "", ListMerchantStoreDiscountCodes)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"has_more":false`)
}

func TestMerchantStoreDiscountMinimumErrorCarriesExactRecoveryFacts(t *testing.T) {
	for _, cancelled := range []bool{true, false} {
		status := "pending"
		if cancelled {
			status = "cancelled"
		}
		response := merchantStoreControllerRequest(t, 1, "{}", func(c *gin.Context) {
			merchantStoreRespond(c, nil, &service.MerchantStorePaymentMinimumError{OrderID: "private-order-id", OrderStatus: status, OrderCancelled: cancelled})
		})
		require.Equal(t, 422, response.Code)
		var body struct {
			Success        bool   `json:"success"`
			Code           string `json:"code"`
			OrderID        string `json:"order_id"`
			OrderStatus    string `json:"order_status"`
			OrderCancelled bool   `json:"order_cancelled"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		require.False(t, body.Success)
		require.Equal(t, "STORE_PAYMENT_MINIMUM", body.Code)
		require.Equal(t, "private-order-id", body.OrderID)
		require.Equal(t, status, body.OrderStatus)
		require.Equal(t, cancelled, body.OrderCancelled)
	}
}
