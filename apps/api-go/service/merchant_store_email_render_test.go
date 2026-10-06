package service

import (
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestMerchantStorePickupEmailLayoutHasPlainAlternativeAndEscapesMerchantContent(t *testing.T) {
	merchantStoreSMTPTestSettings(t)
	tradeNo := "MS" + strings.Repeat("b", 30)
	email := merchantStorePickupEmail{
		destination: "buyer@example.com", tradeNo: tradeNo,
		pickupURL: "https://api.example.com/store/claim/" + strings.Repeat("a", 43),
		details: &model.MerchantStoreOrderPickupDetails{
			OrderID: "order-id", TradeNo: tradeNo, ProductID: "product-id",
			ProductTitle: `Merchant <img src=x onerror=alert(1)> product`,
			VariantName:  `独立GPU 90天／自定义 & <升级>`, Quantity: 3,
			ProductDescription: "First line\n<script>alert('unsafe')</script>",
			ProductLinks: []model.MerchantStoreLink{
				{Title: "Merchant <b>instructions</b>", URL: "https://merchant.example/read?first=1&second=2", Description: `Description <img src=x>`},
				{Title: "Unsafe JavaScript", URL: "javascript:alert(1)"},
				{Title: "Unsafe data", URL: "data:text/html,bad"},
				{Title: "Unsafe credentials", URL: "https://user:secret@merchant.example/private"},
				{Title: "Unsafe control", URL: "https://merchant.example/\x00"},
			},
		},
	}
	message, sender, recipient, err := merchantStorePickupEmailMessage(email)
	require.NoError(t, err)
	require.Equal(t, "sender@example.com", sender)
	require.Equal(t, email.destination, recipient)
	bodies := merchantStoreEmailTestBodies(t, message)
	require.Len(t, bodies, 2)
	plain, html := bodies["text/plain"], bodies["text/html"]
	for _, body := range []string{plain, html} {
		require.Contains(t, body, tradeNo)
		require.Contains(t, body, "购买数量：3")
		require.Contains(t, body, email.pickupURL)
		require.Contains(t, body, "商品说明")
		require.Contains(t, body, "商品链接")
		require.NotContains(t, body, "javascript:")
		require.NotContains(t, body, "data:text")
		require.NotContains(t, body, "user:secret")
		require.NotContains(t, body, "Unsafe control")
		require.NotContains(t, body, "CARD-SECRET")
	}
	require.Contains(t, plain, email.details.ProductTitle)
	require.Contains(t, plain, email.details.VariantName)
	require.Contains(t, plain, "First line\n")
	require.Contains(t, html, "Merchant &lt;img")
	require.Contains(t, html, "独立GPU 90天／自定义 &amp; &lt;升级&gt;")
	require.NotContains(t, html, "<img")
	require.NotContains(t, html, "<script")
	require.NotContains(t, html, "<b>instructions</b>")
	require.Contains(t, html, `href="https://merchant.example/read?first=1&amp;second=2"`)
	require.Contains(t, html, "查看并领取商品")
	require.Contains(t, html, "max-width:600px")
	require.Contains(t, html, `name="viewport"`)
}

func TestMerchantStorePickupEmailLayoutOmitsAbsentSectionsAndRejectsMismatchedDetails(t *testing.T) {
	merchantStoreSMTPTestSettings(t)
	tradeNo := "MS" + strings.Repeat("c", 30)
	email := merchantStorePickupEmail{destination: "buyer@example.com", tradeNo: tradeNo,
		pickupURL: "https://api.example.com/store/claim/" + strings.Repeat("b", 43),
		details:   &model.MerchantStoreOrderPickupDetails{TradeNo: tradeNo, ProductTitle: "Single product", Quantity: 1},
	}
	plain, html, err := renderMerchantStorePickupEmail(email)
	require.NoError(t, err)
	for _, body := range []string{plain, html} {
		require.NotContains(t, body, "规格：")
		require.NotContains(t, body, "商品说明")
		require.NotContains(t, body, "商品链接")
	}
	email.details.TradeNo = "MS" + strings.Repeat("d", 30)
	_, _, err = renderMerchantStorePickupEmail(email)
	require.Error(t, err)
	email.details.TradeNo = tradeNo
	email.details.Quantity = 0
	_, _, err = renderMerchantStorePickupEmail(email)
	require.Error(t, err)
	email.verificationCode = "123456"
	email.tradeNo, email.pickupURL = "", ""
	_, _, _, err = merchantStorePickupEmailMessage(email)
	require.Error(t, err, "ownership code must not include purchase details")
}
