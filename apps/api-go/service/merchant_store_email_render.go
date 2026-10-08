package service

import (
	"bytes"
	"html/template"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
)

var merchantStorePickupMailTemplate = template.Must(template.New("store-pickup").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta name="viewport" content="width=device-width, initial-scale=1"><meta charset="utf-8"><title>商店订单取货</title></head>
<body style="margin:0;padding:0;background:#f5f5f4;color:#1c1c1b;font-family:Arial,'Noto Sans',sans-serif">
<table role="presentation" width="100%" cellspacing="0" cellpadding="0"><tr><td align="center" style="padding:24px 12px">
<table role="presentation" width="600" cellspacing="0" cellpadding="0" style="width:100%;max-width:600px;background:#fff;border:1px solid #e4e4e1;border-radius:8px">
<tr><td style="padding:28px 24px 20px;overflow-wrap:anywhere;word-break:break-word">
<p style="margin:0 0 18px;font-size:13px;color:#666">{{.SystemName}} · 商店订单</p>
<p style="margin:0 0 8px;font-size:14px;color:#666">支付成功，可以取货了</p>
{{if .ProductTitle}}<h1 style="margin:0 0 12px;font-size:24px;line-height:1.4">{{.ProductTitle}}</h1>{{end}}
{{if .VariantName}}<p style="margin:0 0 16px;font-size:16px">规格：{{.VariantName}}</p>{{end}}
<p style="margin:0 0 8px;font-size:14px;line-height:1.6">订单号：{{.TradeNo}}</p>
{{if .Quantity}}<p style="margin:0 0 20px;font-size:14px">购买数量：{{.Quantity}}</p>{{end}}
<p style="margin:24px 0"><a href="{{.PickupURL}}" style="display:inline-block;padding:14px 24px;background:#1c1c1b;color:#fff;text-decoration:none;border-radius:6px;font-size:16px;font-weight:600">查看并领取商品</a></p>
<p style="margin:0;color:#666;font-size:12px;line-height:1.7">如果按钮无法打开，可复制下方取货链接：<br><a href="{{.PickupURL}}" style="color:#444;overflow-wrap:anywhere;word-break:break-all">{{.PickupURL}}</a></p>
</td></tr>
{{if .ProductDescription}}<tr><td style="padding:20px 24px;border-top:1px solid #e4e4e1;overflow-wrap:anywhere;word-break:break-word"><h2 style="margin:0 0 12px;font-size:16px">商品说明</h2><p style="margin:0;white-space:pre-wrap;font-size:14px;line-height:1.8">{{.ProductDescription}}</p></td></tr>{{end}}
{{if .ProductLinks}}<tr><td style="padding:20px 24px;border-top:1px solid #e4e4e1;overflow-wrap:anywhere;word-break:break-word"><h2 style="margin:0 0 12px;font-size:16px">商品链接</h2>{{range .ProductLinks}}<p style="margin:0 0 8px;font-size:14px;line-height:1.6"><a href="{{.URL}}" style="color:#222;font-weight:600">{{.Title}}</a>{{if .Description}}<br><span style="color:#666">{{.Description}}</span>{{end}}</p>{{end}}</td></tr>{{end}}
<tr><td style="padding:18px 24px;border-top:1px solid #e4e4e1;color:#666;font-size:12px;line-height:1.7">请妥善保管取货链接。如订单设有取件码或登录保护，取货时仍需验证。</td></tr>
</table></td></tr></table></body></html>`))

type merchantStorePickupMailView struct {
	SystemName         string
	TradeNo            string
	PickupURL          string
	ProductTitle       string
	VariantName        string
	Quantity           int
	ProductDescription string
	ProductLinks       []model.MerchantStoreLink
}

func merchantStorePickupMailLinkSafe(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || len(raw) > 4096 {
		return false
	}
	for _, r := range raw {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func renderMerchantStorePickupEmail(email merchantStorePickupEmail) (string, string, error) {
	if !merchantStoreTradeNoPattern.MatchString(email.tradeNo) {
		return "", "", errMerchantStoreEmail
	}
	if _, err := merchantStorePublicHTTPSURL(email.pickupURL, false); err != nil {
		return "", "", errMerchantStoreEmail
	}
	view := merchantStorePickupMailView{SystemName: common.SystemName, TradeNo: email.tradeNo, PickupURL: email.pickupURL}
	if details := email.details; details != nil {
		if details.TradeNo != email.tradeNo || details.Quantity < 1 || details.Quantity > 1000 {
			return "", "", errMerchantStoreEmail
		}
		view.ProductTitle, view.VariantName, view.Quantity = details.ProductTitle, details.VariantName, details.Quantity
		view.ProductDescription = details.ProductDescription
		for _, link := range details.ProductLinks {
			if merchantStorePickupMailLinkSafe(link.URL) {
				view.ProductLinks = append(view.ProductLinks, link)
			}
		}
	}
	var plain strings.Builder
	plain.WriteString("你的商店订单已完成支付。\r\n\r\n")
	if view.ProductTitle != "" {
		plain.WriteString("商品：" + view.ProductTitle + "\r\n")
	}
	if view.VariantName != "" {
		plain.WriteString("规格：" + view.VariantName + "\r\n")
	}
	plain.WriteString("订单号：" + view.TradeNo + "\r\n")
	if view.Quantity != 0 {
		plain.WriteString("购买数量：" + strconv.Itoa(view.Quantity) + "\r\n")
	}
	plain.WriteString("\r\n取货链接：" + view.PickupURL + "\r\n")
	if view.ProductDescription != "" {
		plain.WriteString("\r\n商品说明\r\n" + view.ProductDescription + "\r\n")
	}
	if len(view.ProductLinks) != 0 {
		plain.WriteString("\r\n商品链接\r\n")
		for _, link := range view.ProductLinks {
			plain.WriteString(link.Title + "\r\n" + link.URL + "\r\n")
			if link.Description != "" {
				plain.WriteString(link.Description + "\r\n")
			}
		}
	}
	plain.WriteString("\r\n请妥善保管取货链接。如订单设有取件码或登录保护，取货时仍需验证。\r\n")
	var html bytes.Buffer
	if err := merchantStorePickupMailTemplate.Execute(&html, view); err != nil {
		return "", "", errMerchantStoreEmail
	}
	return plain.String(), html.String(), nil
}
