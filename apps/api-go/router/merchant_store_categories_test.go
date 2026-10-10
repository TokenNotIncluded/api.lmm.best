package router

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func shopCustomCategoryRouter(t *testing.T) (*gin.Engine, *gorm.DB, string, model.User, string, model.User) {
	t.Helper()
	require.GreaterOrEqual(t, model.MerchantStoreWriterCapability, 6)
	engine, db, sellerToken, seller, rootToken, root := merchantStoreTestRouter(t)
	require.NoError(t, model.PrepareMerchantStoreSchema(db, 1))
	require.NoError(t, model.ActivateMerchantStoreVariants(db, 1))
	require.NoError(t, model.ActivateMerchantStoreProductLifecycle(db, 2))
	require.NoError(t, model.ActivateMerchantStoreRefunds(db, 3))
	require.NoError(t, model.ActivateMerchantStoreAccess(db, 4))
	require.NoError(t, model.PrepareMerchantStoreSchema(db, 5))
	require.NoError(t, model.ActivateMerchantStorePhaseSix(db, 5))
	_, err := model.SaveMerchantStoreSellerTerms(seller.Id, model.MerchantStoreTermsInput{Content: "The selected card is delivered through this merchant's store."})
	require.NoError(t, err)
	return engine, db, sellerToken, seller, rootToken, root
}

func shopCustomCategoryData(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.True(t, body.Success, response.Body.String())
	require.NotNil(t, body.Data)
	return body.Data
}

func shopCustomCategoryItems(t *testing.T, response *httptest.ResponseRecorder) []any {
	t.Helper()
	items, ok := shopCustomCategoryData(t, response)["items"].([]any)
	require.True(t, ok, response.Body.String())
	return items
}

func shopCustomCategoryCreate(t *testing.T, engine *gin.Engine, token, body string) string {
	t.Helper()
	response := shopRequest(engine, "POST", "/api/store/admin/categories", token, body)
	require.Equal(t, 200, response.Code, response.Body.String())
	id, ok := shopCustomCategoryData(t, response)["id"].(string)
	require.True(t, ok, response.Body.String())
	_, err := uuid.Parse(id)
	require.NoError(t, err)
	return id
}

func TestMerchantStoreCustomCategoryRoutesAllowCurrentL5L6AndActivePublicLists(t *testing.T) {
	engine, db, sellerToken, _, rootToken, _ := shopCustomCategoryRouter(t)
	adminToken, admin := shopTestModeActor(t, db, common.RoleAdminUser)
	input := `{"name":"  Cafe\u0301   cards  ","sort_order":10}`
	for _, token := range []string{"", sellerToken} {
		response := shopRequest(engine, "POST", "/api/store/admin/categories", token, input)
		require.NotEqual(t, 200, response.Code, response.Body.String())
		if token != "" {
			require.Equal(t, 403, response.Code)
		}
		response = shopRequest(engine, "GET", "/api/store/admin/categories", token, "")
		require.NotEqual(t, 200, response.Code, response.Body.String())
	}
	id := shopCustomCategoryCreate(t, engine, adminToken, input)
	disabledID := shopCustomCategoryCreate(t, engine, rootToken, `{"name":"Archived","sort_order":-1,"active":false}`)
	response := shopRequest(engine, "GET", "/api/store/categories", "", "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	publicData := shopCustomCategoryData(t, response)
	require.Len(t, publicData, 5)
	require.Equal(t, true, publicData["supported"])
	for _, field := range []string{"items", "offset", "limit", "has_more"} {
		require.Contains(t, publicData, field)
	}
	items := shopCustomCategoryItems(t, response)
	require.Len(t, items, 1)
	require.Equal(t, id, items[0].(map[string]any)["id"])
	require.Equal(t, "Café cards", items[0].(map[string]any)["name"])
	response = shopRequest(engine, "GET", "/api/store/admin/categories", adminToken, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	items = shopCustomCategoryItems(t, response)
	require.Len(t, items, 2)
	require.Equal(t, disabledID, items[0].(map[string]any)["id"])
	require.Equal(t, false, items[0].(map[string]any)["active"])
	response = shopRequest(engine, "POST", "/api/store/admin/categories", rootToken, `{"name":" CAFÉ\tCARDS "}`)
	require.Equal(t, 409, response.Code, response.Body.String())
	response = shopRequest(engine, "PUT", "/api/store/admin/categories/"+disabledID, adminToken, `{"name":"Archived renamed","sort_order":-1}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Equal(t, false, shopCustomCategoryData(t, response)["active"])
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", admin.Id).Update("role", common.RoleCommonUser).Error)
	response = shopRequest(engine, "PUT", "/api/store/admin/categories/"+id, adminToken, `{"name":"No longer an administrator"}`)
	require.Equal(t, 403, response.Code, response.Body.String())
}

func TestMerchantStoreCustomCategoryRoutesRejectMalformedInputWithoutPartialChanges(t *testing.T) {
	engine, db, _, _, rootToken, _ := shopCustomCategoryRouter(t)
	id := shopCustomCategoryCreate(t, engine, rootToken, `{"name":"Kept","active":true}`)
	for _, body := range []string{
		`{}`, `{"name":" "}`, `{"name":"private\u0000data"}`,
		`{"name":"Wrong type","active":"false"}`, `{"name":"Wrong type","sort_order":1.5}`,
		`{"name":"Too low","sort_order":-1000001}`, `{"name":"Too high","sort_order":1000001}`,
		`{"name":"Unknown field","normalized_name":"spoofed"}`,
		`{"name":"Unknown identity","id":"caller-selected"}`, `{"name":"Trailing"} {}`,
	} {
		response := shopRequest(engine, "PUT", "/api/store/admin/categories/"+id, rootToken, body)
		require.Equal(t, 422, response.Code, body+response.Body.String())
	}
	response := shopRequest(engine, "PUT", "/api/store/admin/categories/not-a-uuid", rootToken, `{"name":"Invalid identity"}`)
	require.Equal(t, 422, response.Code, response.Body.String())
	var category model.MerchantStoreCategory
	require.NoError(t, db.First(&category, "id = ?", id).Error)
	require.Equal(t, "Kept", category.Name)
	require.True(t, category.Active)
	var count int64
	require.NoError(t, db.Model(&model.MerchantStoreCategory{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestMerchantStoreCustomCategoryOwnerMutationKeepsPublishedProductAndPublicBadgeAfterDisable(t *testing.T) {
	engine, db, sellerToken, seller, rootToken, root := shopCustomCategoryRouter(t)
	product := shopPublishedProduct(t, db, seller, root)
	id := shopCustomCategoryCreate(t, engine, rootToken, `{"name":"Digital cards"}`)
	body := `{"category_id":"` + id + `"}`
	response := shopRequest(engine, "PUT", "/api/store/products/"+product.ID+"/category", rootToken, body)
	require.Equal(t, 403, response.Code, response.Body.String())
	var before model.MerchantStoreProduct
	require.NoError(t, db.First(&before, "id = ?", product.ID).Error)
	response = shopRequest(engine, "PUT", "/api/store/products/"+product.ID+"/category", sellerToken, body)
	require.Equal(t, 200, response.Code, response.Body.String())
	data := shopCustomCategoryData(t, response)
	require.Equal(t, id, data["category_id"])
	require.Equal(t, true, data["category"].(map[string]any)["active"])
	var assigned model.MerchantStoreProduct
	require.NoError(t, db.First(&assigned, "id = ?", product.ID).Error)
	require.Equal(t, "published", assigned.Status)
	require.Equal(t, before.ReviewNote, assigned.ReviewNote)
	require.Equal(t, before.ReviewedBy, assigned.ReviewedBy)
	require.Equal(t, before.ReviewedAt, assigned.ReviewedAt)
	response = shopRequest(engine, "PUT", "/api/store/admin/categories/"+id, rootToken, `{"name":"Retired digital cards","active":false}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	response = shopRequest(engine, "GET", "/api/store/products/"+product.ID, "", "")
	require.Equal(t, 200, response.Code, response.Body.String())
	data = shopCustomCategoryData(t, response)
	require.Equal(t, id, data["category_id"])
	badge := data["category"].(map[string]any)
	require.Equal(t, "Retired digital cards", badge["name"])
	require.NotContains(t, badge, "active")
	require.NotContains(t, response.Body.String(), "PRIVATE-CARD")
	require.NotContains(t, response.Body.String(), "internal-review-fixture")
	response = shopRequest(engine, "GET", "/api/store/my/products/"+product.ID, sellerToken, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Equal(t, false, shopCustomCategoryData(t, response)["category"].(map[string]any)["active"])
	response = shopRequest(engine, "PUT", "/api/store/products/"+product.ID+"/category", sellerToken, body)
	require.Equal(t, 200, response.Code, response.Body.String())
	disabledID := shopCustomCategoryCreate(t, engine, rootToken, `{"name":"Another disabled","active":false}`)
	for _, value := range []string{disabledID, uuid.NewString(), "not-a-uuid"} {
		response = shopRequest(engine, "PUT", "/api/store/products/"+product.ID+"/category", sellerToken, `{"category_id":"`+value+`"}`)
		require.Equal(t, 422, response.Code, response.Body.String())
	}
	for _, invalid := range []string{`{}`, `{"category_id":null}`, `{"category_id":1}`, `{"category_id":"","seller_id":999}`} {
		response = shopRequest(engine, "PUT", "/api/store/products/"+product.ID+"/category", sellerToken, invalid)
		require.Equal(t, 422, response.Code, invalid+response.Body.String())
	}
	var retained model.MerchantStoreProduct
	require.NoError(t, db.First(&retained, "id = ?", product.ID).Error)
	assigned.UpdatedAt = retained.UpdatedAt
	require.Equal(t, assigned, retained)
	response = shopRequest(engine, "PUT", "/api/store/products/"+product.ID+"/category", sellerToken, `{"category_id":""}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Equal(t, "", shopCustomCategoryData(t, response)["category_id"])
}

func TestMerchantStoreCustomCategoryFilterPrecedesPaginationAndRespectsVisibilityAndFloor(t *testing.T) {
	engine, db, sellerToken, seller, rootToken, root := shopCustomCategoryRouter(t)
	a := shopCustomCategoryCreate(t, engine, rootToken, `{"name":"A"}`)
	b := shopCustomCategoryCreate(t, engine, rootToken, `{"name":"B"}`)
	matching := shopPublishedProduct(t, db, seller, root)
	newestOther := shopPublishedProduct(t, db, seller, root)
	registered := shopPublishedProduct(t, db, seller, root)
	for _, item := range []struct {
		product *model.MerchantStoreProduct
		id      string
		created int64
	}{{matching, a, 1}, {newestOther, b, 2}, {registered, a, 3}} {
		response := shopRequest(engine, "PUT", "/api/store/products/"+item.product.ID+"/category", sellerToken, `{"category_id":"`+item.id+`"}`)
		require.Equal(t, 200, response.Code, response.Body.String())
		require.NoError(t, db.Model(&model.MerchantStoreProduct{}).Where("id = ?", item.product.ID).Update("created_at", item.created).Error)
	}
	require.NoError(t, db.Model(&model.MerchantStoreProduct{}).Where("id = ?", registered.ID).Update("visibility", "registered").Error)
	response := shopRequest(engine, "GET", "/api/store/products?category_id="+a+"&sort=newest&limit=1", "", "")
	require.Equal(t, 200, response.Code, response.Body.String())
	items := shopCustomCategoryItems(t, response)
	require.Len(t, items, 1)
	require.Equal(t, matching.ID, items[0].(map[string]any)["id"])
	require.Equal(t, a, items[0].(map[string]any)["category_id"])
	response = shopRequest(engine, "GET", "/api/store/products?category_id="+a+"&sort=newest", sellerToken, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Len(t, shopCustomCategoryItems(t, response), 2)
	for _, query := range []string{"category_id=", "category_id=" + url.QueryEscape("not-a-uuid"), "category_id=" + a + "&category_id=" + b, "category_id=" + a + "&category_id=" + a} {
		response = shopRequest(engine, "GET", "/api/store/products?"+query, "", "")
		require.Equal(t, 422, response.Code, query+response.Body.String())
	}
	response = shopRequest(engine, "PUT", "/api/store/admin/categories/"+b, rootToken, `{"name":"B","active":false}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	response = shopRequest(engine, "GET", "/api/store/products?category_id="+b, "", "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Empty(t, shopCustomCategoryItems(t, response))
	response = shopRequest(engine, "GET", "/api/store/products", "", "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Len(t, shopCustomCategoryItems(t, response), 2, "disabling a category must not hide its public products from unfiltered browsing")
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.MerchantStoreWriterCapabilityOption).Update("value", "5").Error)
	for _, request := range []struct {
		path  string
		token string
	}{{"/api/store/categories", ""}, {"/api/store/admin/categories", rootToken}} {
		response = shopRequest(engine, "GET", request.path, request.token, "")
		require.Equal(t, 200, response.Code, response.Body.String())
		data := shopCustomCategoryData(t, response)
		require.Len(t, data, 5)
		require.Equal(t, false, data["supported"])
		require.Empty(t, shopCustomCategoryItems(t, response))
	}
	response = shopRequest(engine, "GET", "/api/store/admin/categories", sellerToken, "")
	require.Equal(t, 403, response.Code, response.Body.String())
	response = shopRequest(engine, "POST", "/api/store/admin/categories", rootToken, `{"name":"Forbidden floor-five create"}`)
	require.Equal(t, 503, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "STORE_UPGRADE_IN_PROGRESS")
	response = shopRequest(engine, "PUT", "/api/store/products/"+matching.ID+"/category", sellerToken, `{"category_id":""}`)
	require.Equal(t, 503, response.Code, response.Body.String())
	response = shopRequest(engine, "GET", "/api/store/products?category_id="+a, "", "")
	require.Equal(t, 503, response.Code, "a new filter cannot silently be ignored by an older floor: %s", response.Body.String())
	response = shopRequest(engine, "GET", "/api/store/products", "", "")
	require.Equal(t, 200, response.Code, response.Body.String())
}
