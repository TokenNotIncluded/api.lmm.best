package controller

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAssistantCatalogueToolsAreReadOnlyAndAvailableWithoutDeveloperAccess(t *testing.T) {
	for _, name := range []string{"get_store_products", "get_store_product", "get_tool_market_services", "get_tool_market_service"} {
		for _, state := range []assistantUserContext{{AccessLevel: "L0"}, {AccessLevel: "L1", DeveloperAccessGranted: true}, {AccessLevel: "ROOT", AdministratorMode: true}} {
			require.True(t, assistantToolAllowedForContext(name, state), name)
		}
		call := assistantOpenAIToolCall{}
		call.Function.Name = name
		require.True(t, assistantToolCallReadOnly(nil, call), name)
		found := false
		for _, definition := range buildAssistantTools() {
			if definition.Function.Name == name {
				found = true
			}
		}
		require.True(t, found, name)
	}
	for _, input := range []map[string]any{{"limit": 21}, {"offset": -1}, {"offset": 1.5}, {"limit": "10"}, {"query": strings.Repeat("中", 121)}} {
		_, _, _, err := assistantCatalogPage(input)
		require.Error(t, err)
	}
	query, offset, limit, err := assistantCatalogPage(map[string]any{"query": " 卡密 ", "offset": float64(5), "limit": float64(20)})
	require.NoError(t, err)
	require.Equal(t, "卡密", query)
	require.Equal(t, 5, offset)
	require.Equal(t, 20, limit)
}

func TestAssistantStoreCatalogueUsesPublishedVisibilityAndNoPrivateDeliveryData(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(model.MerchantStoreModels()...))
	root := model.User{Username: "catalog-root", AffCode: "catalog-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	seller := model.User{Username: "catalog-seller", AffCode: "catalog-seller", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 5000000}
	buyer := model.User{Username: "catalog-buyer", AffCode: "catalog-buyer", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&root).Error)
	require.NoError(t, db.Create(&seller).Error)
	require.NoError(t, db.Create(&buyer).Error)
	require.NoError(t, db.Create(&model.MerchantStoreConfig{ID: 1, RecipientID: root.Id, FeeBPS: 100, PromotionQuota: int(common.FixedCreditsPerUSD)}).Error)
	public := model.MerchantStoreProduct{ID: uuid.NewString(), SellerID: seller.Id, Title: "自定义软件卡", Description: "## 使用说明\nFAQ：按商家实际规格发货", Status: "published", PriceQuota: 750000,
		ReviewNote: "PRIVATE-REVIEW", Contact: "PRIVATE-CONTACT", ReviewedBy: root.Id}
	require.NoError(t, db.Create(&public).Error)
	require.NoError(t, db.Create(&model.MerchantStoreStock{ID: uuid.NewString(), ProductID: public.ID, Ciphertext: "PRIVATE-CARD-CIPHERTEXT", State: "available"}).Error)
	for _, status := range []string{"draft", "pending", "paused", "unlisted", "deleted"} {
		hidden := public
		hidden.ID, hidden.Status = uuid.NewString(), status
		require.NoError(t, db.Create(&hidden).Error)
		require.Equal(t, false, executeAssistantStoreCatalogTool(seller.Id, map[string]any{"product_id": hidden.ID}, true)["ok"], status)
	}
	result := executeAssistantStoreCatalogTool(buyer.Id, map[string]any{}, false)
	require.Equal(t, true, result["ok"])
	items := result["items"].([]assistantStoreProduct)
	require.Len(t, items, 1)
	view := items[0]
	require.Equal(t, public.ID, view.ID)
	require.Equal(t, public.Description, view.Description)
	require.Equal(t, "/store/products/"+public.ID, view.Href)
	require.Equal(t, int64(1), view.AvailableStock)
	require.Equal(t, 750000, view.PriceQuota)
	require.Equal(t, common.FixedCreditsPerUSD, view.CreditsPerUSD)
	require.False(t, view.Tradable, "no usable payment channel means no checkout")
	require.False(t, view.VariantStockKnown, "aggregate stock never supplies missing SKU stock")
	require.Empty(t, view.Variants, "no synthetic Standard/Premium specifications")
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	for _, secret := range []string{"PRIVATE-REVIEW", "PRIVATE-CONTACT", "PRIVATE-CARD-CIPHERTEXT", "seller_id", "reviewed_by", "ciphertext"} {
		require.NotContains(t, string(encoded), secret)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(assistantActorUserIDKey, buyer.Id)
	c.Set(assistantUserContextKey, assistantUserContext{AccessLevel: "L0"})
	call := assistantOpenAIToolCall{}
	call.Function.Name, call.Function.Arguments = "get_store_product", `{"product_id":"`+public.ID+`"}`
	require.Equal(t, true, executeAssistantTool(c, call)["ok"], "actual registry dispatch")
	navigation := executeAssistantNavigateTool(c, buyer.Id, map[string]any{"page": "store-product", "identifier": public.ID})
	require.Equal(t, "/store/products/"+public.ID, navigation["path"])
	for _, bad := range []string{"https://attacker.test", "../orders", public.ID + "/../orders", uuid.NewString()} {
		require.Equal(t, false, executeAssistantNavigateTool(c, buyer.Id, map[string]any{"page": "store-product", "identifier": bad})["ok"])
	}
	require.NoError(t, db.Model(&seller).Update("status", common.UserStatusDisabled).Error)
	require.Equal(t, false, executeAssistantStoreCatalogTool(buyer.Id, map[string]any{"product_id": public.ID}, true)["ok"])
	result = executeAssistantStoreCatalogTool(buyer.Id, map[string]any{}, false)
	require.Empty(t, result["items"].([]assistantStoreProduct))
}

func TestAssistantToolMarketCatalogueUsesVisiblePublishedSnapshotAndExactBilling(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.ToolMarketService{}, &model.ToolMarketVersion{}, &model.ToolMarketToolVersion{}, &model.ToolMarketAccess{}))
	owner := model.User{Username: "catalog-tool-owner", AffCode: "catalog-tool-owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	buyer := model.User{Username: "catalog-tool-buyer", AffCode: "catalog-tool-buyer", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&buyer).Error)
	var visibleID, privateID string
	for _, visibility := range []string{"public", "private", "shared"} {
		service := model.ToolMarketService{ID: uuid.NewString(), OwnerID: owner.Id, LiveVersionID: uuid.NewString(), DraftVersionID: "PRIVATE-DRAFT", Status: "published"}
		version := model.ToolMarketVersion{ID: service.LiveVersionID, ServiceID: service.ID, Status: "published", Visibility: visibility, Name: "真实工具 " + visibility, Description: "FAQ：读取公开商品资料", ExecutionType: "remote", Endpoint: "https://upstream.test?secret=PRIVATE-UPSTREAM", ReviewNote: "PRIVATE-REVIEW"}
		tool := model.ToolMarketToolVersion{VersionID: version.ID, ToolID: uuid.NewString(), Name: "read_catalog", Description: "按实际输入查找", InputSchema: `{"type":"object","properties":{"q":{"type":"string","examples":["卡密"]}}}`, Permissions: `["read"]`, PriceQuota: 0, BillingMode: "metered", BillingRules: []model.ToolMarketBillingRule{{Metric: "requests", RateQuota: 1234, MaxQuantity: 3}}}
		require.NoError(t, db.Create(&service).Error)
		require.NoError(t, db.Create(&version).Error)
		require.NoError(t, db.Create(&tool).Error)
		if visibility == "public" {
			visibleID = service.ID
		} else if visibility == "private" {
			privateID = service.ID
		}
	}
	result := executeAssistantMarketCatalogTool(nil, buyer.Id, map[string]any{}, false)
	require.Equal(t, true, result["ok"])
	items := result["items"].([]assistantMarketService)
	require.Len(t, items, 1)
	require.Equal(t, visibleID, items[0].ID)
	require.Equal(t, "tool_market_service", items[0].EntityKind)
	require.Equal(t, false, executeAssistantMarketCatalogTool(nil, buyer.Id, map[string]any{"service_id": privateID}, true)["ok"])
	require.Equal(t, true, executeAssistantMarketCatalogTool(nil, owner.Id, map[string]any{"service_id": privateID}, true)["ok"], "published private owner view follows actual API")
	detail := executeAssistantMarketCatalogTool(nil, buyer.Id, map[string]any{"service_id": visibleID}, true)
	view := detail["service"].(assistantMarketService)
	require.Equal(t, "metered", view.Tools[0].BillingMode)
	require.Equal(t, 1234, view.Tools[0].BillingRules[0].RateQuota)
	require.Equal(t, []string{"read"}, view.Tools[0].Permissions)
	require.Contains(t, string(view.Tools[0].InputSchema), "卡密")
	require.Equal(t, "/tool-market?service_id="+visibleID, view.Href)
	encoded, err := json.Marshal(detail)
	require.NoError(t, err)
	for _, secret := range []string{"PRIVATE-UPSTREAM", "PRIVATE-DRAFT", "PRIVATE-REVIEW", "endpoint", "allowed_users", "grant", "credential", `"pricing":"free"`} {
		require.NotContains(t, string(encoded), secret)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	navigation := executeAssistantNavigateTool(c, buyer.Id, map[string]any{"page": "tool-market-service", "identifier": visibleID})
	require.Equal(t, "/tool-market", navigation["path"])
	require.Equal(t, map[string]any{"service_id": visibleID}, navigation["query"])
	require.Equal(t, false, executeAssistantNavigateTool(c, buyer.Id, map[string]any{"page": "tool-market-service", "identifier": privateID})["ok"])
	require.NoError(t, db.Model(&model.ToolMarketService{}).Where("id = ?", visibleID).Update("status", "deleted").Error)
	require.Equal(t, false, executeAssistantMarketCatalogTool(nil, buyer.Id, map[string]any{"service_id": visibleID}, true)["ok"])
	require.Equal(t, false, executeAssistantNavigateTool(c, buyer.Id, map[string]any{"page": "tool-market-service", "identifier": visibleID})["ok"])
}
