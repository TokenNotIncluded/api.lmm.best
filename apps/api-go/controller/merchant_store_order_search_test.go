package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func storeSearchControllerFixture(t *testing.T) (model.User, *model.MerchantStoreOrder, *model.MerchantStoreOrder) {
	t.Helper()
	db := setupManageUserTestDB(t)
	t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "store-controller-fixture-encryption-key-20261006-123456789")
	require.NoError(t, db.AutoMigrate(&model.ModerationJob{}, &model.Option{}))
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	require.NoError(t, model.BootstrapMerchantStoreWriterGate(db))
	require.NoError(t, db.AutoMigrate(model.MerchantStoreModels()...))
	users := []model.User{
		{Username: "search-buyer", AffCode: "search-buyer", Email: "account@example.test", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 2000000},
		{Username: "search-seller", AffCode: "search-seller", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 2000000},
		{Username: "search-root", AffCode: "search-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled},
	}
	for i := range users {
		require.NoError(t, db.Create(&users[i]).Error)
	}
	buyer, seller, root := users[0], users[1], users[2]
	require.NoError(t, model.SetMerchantStoreConfig(root.Id, model.MerchantStoreConfig{FeeBPS: 100, RecipientID: root.Id, PromotionQuota: 500000}))
	require.NoError(t, model.SetMerchantStorePaymentCategories(seller.Id, model.MerchantStorePaymentCategories{PlatformEnabled: true}))
	_, err := model.SaveMerchantStoreGateway(seller.Id, "balance", true, "")
	require.NoError(t, err)
	p, err := model.SaveMerchantStoreProduct(seller.Id, "", model.MerchantStoreProductInput{Title: "Searchable product", PriceQuota: 500000, PaymentMethods: []string{"balance"}})
	require.NoError(t, err)
	_, err = model.AddMerchantStoreStock(seller.Id, p.ID, []string{"PRIVATE-FIRST", "PRIVATE-SECOND"})
	require.NoError(t, err)
	require.NoError(t, model.SubmitMerchantStoreProduct(seller.Id, p.ID))
	require.NoError(t, model.ReviewMerchantStoreProduct(root.Id, p.ID, true, ""))
	require.NoError(t, model.AcceptMerchantStoreDisclaimer(buyer.Id, model.MerchantStoreDisclaimerVersion))
	one, _, err := model.CreateMerchantStoreOrder(model.MerchantStoreCheckoutInput{BuyerID: buyer.Id, ProductID: p.ID, Quantity: 1, RequestKey: "first", PaymentMethod: "balance", PickupEmail: "first@example.test", PickupCode: "private-first-code"})
	require.NoError(t, err)
	two, _, err := model.CreateMerchantStoreOrder(model.MerchantStoreCheckoutInput{BuyerID: buyer.Id, ProductID: p.ID, Quantity: 1, RequestKey: "second", PaymentMethod: "balance", PickupEmail: "second@example.test"})
	require.NoError(t, err)
	origin := system_setting.ServerAddress
	system_setting.ServerAddress = "https://api.example.test"
	t.Cleanup(func() { system_setting.ServerAddress = origin })
	return buyer, one, two
}

func storeSearchData(t *testing.T, response *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	require.Equal(t, 200, response.Code, response.Body.String())
	var body struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	return body.Data
}

func TestMerchantStoreOrderSearchControllerMailboxProofScopesResultsAndHidesPrivateFields(t *testing.T) {
	_, one, two := storeSearchControllerFixture(t)
	previous := merchantStoreOrderSearchSender
	t.Cleanup(func() { merchantStoreOrderSearchSender = previous })
	codes := map[string]string{}
	merchantStoreOrderSearchSender = func(_ context.Context, email, code string) error {
		codes[email] = code
		return nil
	}
	var challengeID string
	for _, email := range []string{"first@example.test", "unknown@example.test"} {
		r := merchantStoreControllerRequest(t, 0, `{"email":"`+email+`"}`, SendMerchantStoreOrderSearchVerification)
		data := storeSearchData(t, r)
		require.Len(t, data, 3)
		require.Contains(t, data, "challenge_id")
		require.Contains(t, data, "expires_in")
		require.Contains(t, data, "resend_after")
		require.NotContains(t, r.Body.String(), email)
		require.NotContains(t, r.Body.String(), codes[email])
		require.NotContains(t, r.Body.String(), one.TradeNo)
		if email == "first@example.test" {
			require.NoError(t, json.Unmarshal(data["challenge_id"], &challengeID))
		}
	}
	r := merchantStoreControllerRequest(t, 0, `{"search_token":"`+challengeID+`","email":"first@example.test"}`, ListMerchantStoreOrdersByEmail)
	require.Equal(t, 403, r.Code)
	r = merchantStoreControllerRequest(t, 0, `{"challenge_id":"`+challengeID+`","code":"`+codes["first@example.test"]+`"}`, ConfirmMerchantStoreOrderSearchVerification)
	data := storeSearchData(t, r)
	var searchToken string
	require.NoError(t, json.Unmarshal(data["search_token"], &searchToken))
	r = merchantStoreControllerRequest(t, 0, `{"search_token":"`+searchToken+`","email":"second@example.test","buyer_id":999}`, ListMerchantStoreOrdersByEmail)
	data = storeSearchData(t, r)
	var items []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data["items"], &items))
	require.Len(t, items, 1)
	require.JSONEq(t, `"`+one.TradeNo+`"`, string(items[0]["id"]))
	require.Contains(t, items[0], "pickup_url")
	require.JSONEq(t, `true`, string(items[0]["pickup_code_required"]))
	for _, secret := range []string{two.TradeNo, one.ID, "first@example.test", "second@example.test", "PRIVATE-FIRST", "private-first-code", "price_quota", "buyer_id", "seller_id", "pickup_token", "ciphertext"} {
		require.NotContains(t, r.Body.String(), secret)
	}
	require.Equal(t, "no-store", r.Header().Get("Cache-Control"))
	require.Equal(t, "no-referrer", r.Header().Get("Referrer-Policy"))
	r = merchantStoreControllerRequest(t, 0, `{"challenge_id":"`+challengeID+`","code":"`+codes["first@example.test"]+`"}`, ConfirmMerchantStoreOrderSearchVerification)
	require.Equal(t, 422, r.Code)
	r = merchantStoreControllerRequest(t, 0, `{"search_token":"`+searchToken+`","offset":-1}`, ListMerchantStoreOrdersByEmail)
	require.Equal(t, 422, r.Code)
}

func TestMerchantStoreOrderNumberSearchOnlyBuyerMayGetPickupLink(t *testing.T) {
	buyer, one, _ := storeSearchControllerFixture(t)
	for _, actor := range []int{0, buyer.Id + 999, buyer.Id} {
		r := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(r)
		c.Request = httptest.NewRequest("GET", "/api/store/order-search/"+one.TradeNo, nil)
		c.Set("id", actor)
		c.Params = gin.Params{{Key: "trade_no", Value: one.TradeNo}}
		GetMerchantStoreOrderByNumber(c)
		data := storeSearchData(t, r)
		if actor == buyer.Id {
			require.Contains(t, data, "pickup_url")
		} else {
			require.NotContains(t, data, "pickup_url")
		}
		for _, secret := range []string{one.ID, "first@example.test", "PRIVATE-FIRST", "private-first-code", "price_quota", "buyer_id", "seller_id"} {
			require.NotContains(t, r.Body.String(), secret)
		}
	}
}

func TestMerchantStoreOrderSearchSenderErrorsStayPrivate(t *testing.T) {
	storeSearchControllerFixture(t)
	previous := merchantStoreOrderSearchSender
	t.Cleanup(func() { merchantStoreOrderSearchSender = previous })
	merchantStoreOrderSearchSender = func(context.Context, string, string) error {
		return errors.New("SMTP-password first@example.test private-code")
	}
	r := merchantStoreControllerRequest(t, 0, `{"email":"first@example.test"}`, SendMerchantStoreOrderSearchVerification)
	require.Equal(t, 500, r.Code)
	for _, secret := range []string{"SMTP", "password", "first@example.test", "private-code"} {
		require.NotContains(t, r.Body.String(), secret)
	}
}
