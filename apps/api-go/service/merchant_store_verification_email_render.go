package service

import (
	"bytes"
	"html/template"

	"github.com/LIghtJUNction/api.lmm.best/common"
)

var merchantStoreVerificationMailTemplate = template.Must(template.New("store-verification").Parse(`<!doctype html><html lang="zh-CN"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head><body style="margin:0;padding:24px 12px;background:#f4f4f5;color:#18181b;font-family:system-ui,-apple-system,sans-serif"><table role="presentation" style="width:100%;max-width:520px;margin:auto;background:#fff;border:1px solid #e4e4e7;border-radius:16px"><tr><td style="padding:28px"><div style="font-size:13px;color:#71717a">{{.Brand}}</div><h1 style="font-size:22px;margin:12px 0 20px">{{.Title}}</h1><div style="padding:20px;background:#f4f4f5;border-radius:12px;text-align:center;font-size:32px;font-weight:700;letter-spacing:6px">{{.Code}}</div><p style="font-size:14px;line-height:1.7;color:#52525b;margin:20px 0 0">请在发起验证的页面填写此验证码。验证码仅用于这次邮箱验证，不包含订单内容或取货信息。</p></td></tr></table></body></html>`))

func renderMerchantStoreVerificationEmail(title, code string) (string, error) {
	var output bytes.Buffer
	err := merchantStoreVerificationMailTemplate.Execute(&output, struct{ Brand, Title, Code string }{common.SystemName, title, code})
	return output.String(), err
}
