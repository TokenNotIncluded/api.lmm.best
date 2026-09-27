package controller

import (
	"errors"
	"fmt"
	"html"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
)

// Reuse the existing, strict SVG appearance parser. Only the model layout's
// bounded row count and defaults differ; unknown or unsafe options still fail.
func parseProfileShareModelsSVGOptions(query url.Values) (profileShareSVGOptions, int, error) {
	var empty profileShareSVGOptions
	if query.Get("layout") != "models" || query.Get("period") == "all" {
		return empty, 0, errors.New("model layout requires period=7d,30d,365d")
	}
	top, err := profileShareInteger(query, "top", 6, 3, 12)
	if err != nil {
		return empty, 0, err
	}
	appearance := make(url.Values, len(query))
	for key, values := range query {
		appearance[key] = append([]string(nil), values...)
	}
	appearance.Del("top")
	appearance.Set("layout", "badge")
	for key, value := range map[string]string{"width": "1200", "height": "900", "radius": "20", "theme": "dark"} {
		if appearance.Get(key) == "" {
			appearance.Set(key, value)
		}
	}
	options, err := parseProfileShareSVGOptions(appearance)
	options.Layout = "models"
	return options, top, err
}

type profileShareModelCopy struct {
	Title, Tokens, Requests, Spend, Models string
	QuotaShare, TokenShare, RequestShare   string
	Other, Unknown, Empty                  string
}

var profileShareModelLanguages = map[string]profileShareModelCopy{
	"en":    {"Model usage", "Total tokens", "API requests", "Platform spend ($)", "Models used", "Spend by model", "Tokens by model", "Requests by model", "Other models", "Unknown model", "No usage in this range yet"},
	"zh":    {"模型用量", "总 Token", "API 请求", "平台用量 ($)", "使用模型数", "模型费用占比", "模型 Token 占比", "模型请求占比", "其他模型", "未知模型", "这段时间还没有用量"},
	"zh-TW": {"模型用量", "總 Token", "API 請求", "平台用量 ($)", "使用模型數", "模型費用佔比", "模型 Token 佔比", "模型請求佔比", "其他模型", "未知模型", "這段時間還沒有用量"},
	"fr":    {"Utilisation par modèle", "Total de tokens", "Requêtes API", "Dépenses plateforme ($)", "Modèles utilisés", "Dépenses par modèle", "Tokens par modèle", "Requêtes par modèle", "Autres modèles", "Modèle inconnu", "Aucune utilisation sur cette période"},
	"ja":    {"モデル別利用状況", "合計トークン", "API リクエスト", "プラットフォーム利用額 ($)", "利用モデル数", "モデル別利用額", "モデル別トークン", "モデル別リクエスト", "その他のモデル", "不明なモデル", "この期間の利用はありません"},
	"ru":    {"Использование моделей", "Всего токенов", "Запросы API", "Расходы платформы ($)", "Использовано моделей", "Расходы по моделям", "Токены по моделям", "Запросы по моделям", "Другие модели", "Неизвестная модель", "За этот период пока нет данных"},
	"vi":    {"Mức dùng theo mô hình", "Tổng token", "Yêu cầu API", "Chi phí nền tảng ($)", "Số mô hình đã dùng", "Chi phí theo mô hình", "Token theo mô hình", "Yêu cầu theo mô hình", "Các mô hình khác", "Mô hình không xác định", "Chưa có lượt sử dụng trong khoảng này"},
}

// Model names originate in usage data, not the stricter custom-text parser.
// Drop XML-invalid code points before escaping, including controls in aliases.
func profileShareModelText(value string) string {
	return html.EscapeString(strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' || (r >= 0x20 && r <= 0xd7ff) || (r >= 0xe000 && r <= 0xfffd) || (r >= 0x10000 && r <= 0x10ffff) {
			return r
		}
		return -1
	}, value))
}

func profileShareModelShortText(value string, maxWidth int) string {
	width := 0
	var result strings.Builder
	for _, r := range value {
		step := 1
		if r > 0x2e7f {
			step = 2
		}
		if width+step > maxWidth {
			result.WriteRune('…')
			break
		}
		result.WriteRune(r)
		width += step
	}
	return result.String()
}

// A theme-derived scale, never a hard-coded list of model names or colors.
func profileShareModelColor(accent, muted string, index, count int) string {
	mix := float64(index) / math.Max(1, float64(count-1)) * .8
	var channels [3]int
	for channel := range channels {
		start := 1 + channel*2
		a, _ := strconv.ParseInt(accent[start:start+2], 16, 64)
		b, _ := strconv.ParseInt(muted[start:start+2], 16, 64)
		channels[channel] = int(math.Round(float64(a)*(1-mix) + float64(b)*mix))
	}
	return fmt.Sprintf("#%02x%02x%02x", channels[0], channels[1], channels[2])
}

func profileShareModelQuota(quota int64) string {
	if common.QuotaPerUnit <= 0 || math.IsNaN(common.QuotaPerUnit) || math.IsInf(common.QuotaPerUnit, 0) {
		return "—"
	}
	amount := float64(quota) / common.QuotaPerUnit
	precision := 4
	if amount > 0 && amount < .01 {
		precision = 6
	}
	return "$" + strconv.FormatFloat(amount, 'f', precision, 64)
}

func renderProfileShareModelsSVG(options profileShareSVGOptions, usage model.ProfileShareModelUsage, start, end int64) string {
	copy, ok := profileShareModelLanguages[options.Lang]
	if !ok {
		copy = profileShareModelLanguages["en"]
	}
	title, tokenLabel := copy.Title, copy.Tokens
	if options.CustomTitle {
		title = options.Title
	}
	if options.CustomLabel {
		tokenLabel = options.Label
	}
	rows := append([]model.ProfileShareModelRow(nil), usage.Models...)
	other := model.ProfileShareModelRow{ModelName: copy.Other, Tokens: usage.Tokens, Requests: usage.Requests, Quota: usage.Quota}
	for _, row := range rows {
		other.Tokens -= row.Tokens
		other.Requests -= row.Requests
		other.Quota -= row.Quota
	}
	if usage.ModelCount > int64(len(rows)) {
		rows = append(rows, other)
	}
	shareLabel := copy.QuotaShare
	totalWeight := usage.Quota
	weight := func(row model.ProfileShareModelRow) int64 { return row.Quota }
	if totalWeight == 0 {
		shareLabel, totalWeight = copy.TokenShare, usage.Tokens
		weight = func(row model.ProfileShareModelRow) int64 { return row.Tokens }
	}
	if totalWeight == 0 {
		shareLabel, totalWeight = copy.RequestShare, usage.Requests
		weight = func(row model.ProfileShareModelRow) int64 { return row.Requests }
	}
	share := func(row model.ProfileShareModelRow) float64 {
		if totalWeight <= 0 {
			return 0
		}
		return math.Max(0, math.Min(1, float64(weight(row))/float64(totalWeight)))
	}
	font := "Arial, Noto Sans CJK SC, Microsoft YaHei, sans-serif"
	if options.Font == "mono" {
		font = "ui-monospace, Noto Sans Mono CJK SC, monospace"
	} else if options.Font == "serif" {
		font = "Georgia, Noto Serif CJK SC, serif"
	}
	rowCount := len(rows)
	if rowCount == 0 {
		rowCount = 1
	}
	height := 544 + rowCount*56
	scale := math.Min(float64(options.Width)/1200, float64(options.Height)/float64(height))
	x := (float64(options.Width) - 1200*scale) / 2
	y := (float64(options.Height) - float64(height)*scale) / 2
	var svg strings.Builder
	fmt.Fprintf(&svg, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-labelledby="model-title model-desc"><title id="model-title">%s</title><desc id="model-desc">%s · %s</desc>`, options.Width, options.Height, options.Width, options.Height, profileShareModelText(title), profileShareModelText(shareLabel), profileShareModelText(options.Footer))
	if options.Animation != "none" {
		svg.WriteString(`<style>@keyframes model-breathe{0%,100%{opacity:.6}50%{opacity:1}}`)
		if options.Animation == "wave" {
			svg.WriteString(`.model-segment{animation:model-breathe 4s ease-in-out infinite}`)
		} else {
			svg.WriteString(`.model-ring{animation:model-breathe 3s ease-in-out infinite}`)
		}
		svg.WriteString(`@media(prefers-reduced-motion:reduce){.model-ring,.model-segment{animation:none!important;opacity:1}}</style>`)
	}
	fmt.Fprintf(&svg, `<rect x="1" y="1" width="%d" height="%d" rx="%d" fill="%s" stroke="%s" stroke-width="2"/><g transform="translate(%.4f %.4f) scale(%.6f)"><g font-family="%s" fill="%s">`, options.Width-2, options.Height-2, options.Radius, options.Background, options.Border, x, y, scale, font, options.Foreground)
	text := func(x, y, size int, anchor, color, value string) {
		fmt.Fprintf(&svg, `<text x="%d" y="%d" font-size="%d" text-anchor="%s" fill="%s">%s</text>`, x, y, size, anchor, color, profileShareModelText(value))
	}
	text(56, 64, 28, "start", options.Foreground, profileShareModelShortText(title, 48))
	dateRange := time.Unix(start, 0).UTC().Format("2006-01-02") + " — " + time.Unix(end, 0).UTC().Format("2006-01-02") + " UTC"
	text(56, 97, 15, "start", options.Muted, dateRange)
	text(1144, 63, 20, "end", options.Accent, "LMM Best")
	text(1144, 94, 14, "end", options.Muted, "api.lmm.best")
	fmt.Fprintf(&svg, `<g class="model-ring" fill="none" stroke-width="26"><circle cx="226" cy="266" r="116" stroke="%s"/>`, options.Border)
	offset := 0.0
	for index, row := range rows {
		percent := share(row) * 100
		gap := math.Min(.65, percent*.12)
		if percent > 0 {
			fmt.Fprintf(&svg, `<circle class="model-segment" cx="226" cy="266" r="116" pathLength="100" transform="rotate(-90 226 266)" stroke="%s" stroke-dasharray="%.6f %.6f" stroke-dashoffset="%.6f" style="animation-delay:-%.2fs"><title>%s · %.1f%%</title></circle>`, profileShareModelColor(options.Accent, options.Muted, index, len(rows)), percent-gap, 100-percent+gap, -offset-gap/2, float64(index)*.4, profileShareModelText(row.ModelName), percent)
		}
		offset += percent
	}
	svg.WriteString(`</g>`)
	format := func(value int64) string { return profileShareFormatNumber(value, options.Format, options.Lang) }
	fontSize := 34
	if len([]rune(format(usage.Tokens))) > 12 {
		fontSize = int(180 / (float64(len([]rune(format(usage.Tokens)))) * .62))
	}
	text(226, 266, fontSize, "middle", options.Foreground, format(usage.Tokens))
	text(226, 296, 15, "middle", options.Muted, profileShareModelShortText(tokenLabel, 24))
	text(226, 413, 15, "middle", options.Muted, shareLabel)
	stat := func(x, y int, label, value string) {
		text(x, y, 15, "start", options.Muted, profileShareModelShortText(label, 30))
		size := int(math.Min(29, 270/math.Max(1, float64(len([]rune(value)))*.62)))
		text(x, y+38, size, "start", options.Foreground, value)
	}
	stat(488, 191, tokenLabel, format(usage.Tokens))
	stat(488, 303, copy.Spend, profileShareModelQuota(usage.Quota))
	if options.ShowRequests {
		stat(840, 191, copy.Requests, format(usage.Requests))
	}
	stat(840, 303, copy.Models, format(usage.ModelCount))
	tokenX := 752
	if !options.ShowRequests {
		tokenX = 888
	}
	text(56, 444, 14, "start", options.Muted, shareLabel)
	text(tokenX, 444, 14, "end", options.Muted, profileShareModelShortText(tokenLabel, 22))
	if options.ShowRequests {
		text(928, 444, 14, "end", options.Muted, copy.Requests)
	}
	text(1144, 444, 14, "end", options.Muted, profileShareModelShortText(copy.Spend, 24))
	fmt.Fprintf(&svg, `<path d="M56 456H1144" stroke="%s"/>`, options.Border)
	for index, row := range rows {
		y := 456 + index*56
		name := row.ModelName
		if name == "unknown" {
			name = copy.Unknown
		}
		color := profileShareModelColor(options.Accent, options.Muted, index, len(rows))
		fmt.Fprintf(&svg, `<g><title>%s</title><circle cx="63" cy="%d" r="5" fill="%s"/>`, profileShareModelText(name), y+25, color)
		text(82, y+30, 17, "start", options.Foreground, profileShareModelShortText(name, 38))
		text(542, y+30, 15, "end", options.Muted, fmt.Sprintf("%.1f%%", share(row)*100))
		fmt.Fprintf(&svg, `<rect x="82" y="%d" width="%.3f" height="3" rx="1.5" fill="%s"/>`, y+41, 352*share(row), color)
		text(tokenX, y+30, int(math.Min(16, 180/math.Max(1, float64(len([]rune(format(row.Tokens))))*.62))), "end", options.Foreground, format(row.Tokens))
		if options.ShowRequests {
			text(928, y+30, int(math.Min(16, 150/math.Max(1, float64(len([]rune(format(row.Requests))))*.62))), "end", options.Foreground, format(row.Requests))
		}
		text(1144, y+30, 16, "end", options.Foreground, profileShareModelQuota(row.Quota))
		fmt.Fprintf(&svg, `<path d="M56 %dH1144" stroke="%s" opacity=".6"/></g>`, y+56, options.Border)
	}
	if len(rows) == 0 {
		text(600, 493, 18, "middle", options.Muted, copy.Empty)
	}
	text(56, height-36, 14, "start", options.Muted, profileShareModelShortText(options.Footer, 64))
	text(1144, height-36, 14, "end", options.Muted, "https://api.lmm.best")
	svg.WriteString(`</g></g></svg>`)
	return svg.String()
}
