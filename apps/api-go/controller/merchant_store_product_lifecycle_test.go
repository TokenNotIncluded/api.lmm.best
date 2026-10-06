package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreLifecycleControllerIgnoresSuppliedActor(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(append(model.MerchantStoreModels(), &model.ModerationJob{})...))
	seller := model.User{Username: "lifecycle-owner", AffCode: "lifecycle-owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	stranger := model.User{Username: "lifecycle-stranger", AffCode: "lifecycle-stranger", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&seller).Error)
	require.NoError(t, db.Create(&stranger).Error)
	p := model.MerchantStoreProduct{ID: "lifecycle-product", SellerID: seller.Id, Title: "Retained listing", Status: "draft"}
	require.NoError(t, db.Create(&p).Error)
	for _, handler := range []gin.HandlerFunc{UnlistMerchantStoreProduct, DeleteMerchantStoreProduct} {
		response := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(response)
		c.Request = httptest.NewRequest("DELETE", "/api/store/products/lifecycle-product?seller_id=1", nil)
		c.Params = gin.Params{{Key: "id", Value: p.ID}}
		c.Set("id", stranger.Id)
		handler(c)
		require.Equal(t, 403, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), "STORE_ACCESS_DENIED")
	}
}
