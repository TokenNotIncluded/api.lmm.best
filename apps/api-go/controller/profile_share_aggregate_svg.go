package controller

import (
	"fmt"
	"math"
	"net/url"
	"strings"
)

func parseProfileShareAggregateSVGOptions(query url.Values) (profileShareSVGOptions, error) {
	appearance := make(url.Values, len(query))
	for key, values := range query {
		appearance[key] = append([]string(nil), values...)
	}
	appearance.Set("layout", "badge")
	for key, value := range map[string]string{"width": "1200", "height": "900", "radius": "20", "theme": "dark"} {
		if appearance.Get(key) == "" {
			appearance.Set(key, value)
		}
	}
	options, err := parseProfileShareSVGOptions(appearance)
	options.Layout = "aggregate"
	// The renderer derives height from the bounded source count unless the
	// caller explicitly selects a canvas height, which scales the whole image.
	if query.Get("height") == "" {
		options.Height = 0
	}
	return options, err
}

type profileAggregateCopy struct{ Title, Tokens, Requests, Messages, Snapshot, Live, Unknown, Login, Unsupported, Disabled, Observed, Fetched, All, Windows string }

var profileAggregateLanguages = map[string]profileAggregateCopy{
	"en":    {"My AI usage", "tokens", "requests", "messages", "Owner snapshot", "Live public data", "Unavailable", "Sign-in required", "Snapshot required", "Disabled", "Observed", "Fetched", "Lifetime", "Each source keeps its own period; totals are not combined."},
	"zh":    {"我的 AI 用量", "Token", "请求", "消息", "用户快照", "公开数据", "不可用", "需登录查看", "需要导入快照", "未启用", "观察于", "读取于", "累计", "各来源保留各自统计期间，不合并成一个总数。"},
	"zh-TW": {"我的 AI 用量", "Token", "請求", "訊息", "使用者快照", "公開資料", "無法取得", "需登入查看", "需要匯入快照", "未啟用", "觀察於", "讀取於", "累計", "各來源保留各自統計期間，不合併成一個總數。"},
	"fr":    {"Mon utilisation IA", "tokens", "requêtes", "messages", "Instantané du propriétaire", "Données publiques", "Indisponible", "Connexion requise", "Instantané requis", "Désactivé", "Observé", "Consulté", "Cumul", "Chaque source conserve sa période ; les totaux ne sont pas additionnés."},
	"ja":    {"AI 利用状況", "トークン", "リクエスト", "メッセージ", "所有者のスナップショット", "公開データ", "取得不可", "ログインが必要", "スナップショットが必要", "未有効", "確認日時", "取得日時", "累計", "各サービスの集計期間を保持し、合計しません。"},
	"ru":    {"Использование ИИ", "токенов", "запросов", "сообщений", "Снимок владельца", "Открытые данные", "Недоступно", "Нужен вход", "Нужен снимок", "Отключено", "Наблюдение", "Получено", "За всё время", "У каждого источника свой период; итоги не складываются."},
	"vi":    {"Mức dùng AI của tôi", "token", "yêu cầu", "tin nhắn", "Ảnh chụp của chủ hồ sơ", "Dữ liệu công khai", "Không có dữ liệu", "Cần đăng nhập", "Cần nhập ảnh chụp", "Đã tắt", "Quan sát", "Đã lấy", "Tích lũy", "Mỗi nguồn giữ kỳ riêng; không cộng các tổng."},
}

func renderProfileShareAggregateSVG(options profileShareSVGOptions, sources []profileAggregateSource) string {
	copy, ok := profileAggregateLanguages[options.Lang]
	if !ok {
		copy = profileAggregateLanguages["en"]
	}
	if len(sources) > profileShareMaxLinkedProfiles+1 {
		sources = sources[:profileShareMaxLinkedProfiles+1]
	}
	contentHeight := 190 + len(sources)*140
	height := options.Height
	if height == 0 {
		height = int(math.Ceil(float64(contentHeight) * float64(options.Width) / 1200))
		if height > 1200 {
			height = 1200
		}
		if height < 200 {
			height = 200
		}
	}
	scale := math.Min(float64(options.Width)/1200, float64(height)/float64(contentHeight))
	x, y := (float64(options.Width)-1200*scale)/2, (float64(height)-float64(contentHeight)*scale)/2
	title := copy.Title
	if options.CustomTitle {
		title = options.Title
	}
	font := "Arial, Noto Sans CJK SC, sans-serif"
	if options.Font == "mono" {
		font = "ui-monospace, Noto Sans Mono CJK SC, monospace"
	} else if options.Font == "serif" {
		font = "Georgia, Noto Serif CJK SC, serif"
	}
	var svg strings.Builder
	fmt.Fprintf(&svg, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-labelledby="aggregate-title aggregate-desc"><title id="aggregate-title">%s</title><desc id="aggregate-desc">%s</desc><rect x="1" y="1" width="%d" height="%d" rx="%d" fill="%s" stroke="%s"/><g transform="translate(%.4f %.4f) scale(%.6f)" font-family="%s">`, options.Width, height, options.Width, height, profileShareModelText(title), profileShareModelText(copy.Windows), options.Width-2, height-2, options.Radius, options.Background, options.Border, x, y, scale, font)
	text := func(x, y, size int, anchor, color, value string) {
		fmt.Fprintf(&svg, `<text x="%d" y="%d" font-size="%d" text-anchor="%s" fill="%s">%s</text>`, x, y, size, anchor, color, profileShareModelText(value))
	}
	text(48, 65, 30, "start", options.Foreground, profileShareModelShortText(title, 58))
	text(1152, 63, 18, "end", options.Accent, "LMM Best")
	text(48, 99, 15, "start", options.Muted, profileShareModelShortText(copy.Windows, 120))
	for i, row := range sources {
		y := 125 + i*140
		fmt.Fprintf(&svg, `<path d="M48 %dH1152" stroke="%s"/><g><title>%s · %s · %s</title>`, y, options.Border, profileShareModelText(row.Label), profileShareModelText(row.URL), profileShareModelText(row.SnapshotSource))
		text(48, y+36, 22, "start", options.Foreground, profileShareModelShortText(row.Label, 38))
		status := copy.Unknown
		switch row.Status {
		case "live":
			status = copy.Live
		case "snapshot":
			status = copy.Snapshot
		case "login_required":
			status = copy.Login
		case "unsupported":
			status = copy.Unsupported
		case "disabled":
			status = copy.Disabled
		}
		text(48, y+66, 15, "start", options.Muted, status)
		period := row.Period
		if row.Period == "all" {
			period = copy.All
		} else if row.Period == "reported" {
			period = row.PeriodStart + " — " + row.PeriodEnd + " (TZ ? )"
		} else if row.PeriodStart != "" && row.PeriodEnd != "" {
			period = row.PeriodStart + " — " + row.PeriodEnd
			if row.PeriodTimezone == "UTC" {
				period += " UTC"
			}
		} else if row.Status == "snapshot" && row.ObservedAt != "" && row.Period != "unknown" {
			// A fixed historical snapshot never becomes "Last 30 days" relative
			// to whoever views the badge months after the original observation.
			period = row.Period + " @ " + row.ObservedAt[:10]
		} else if p, ok := profileShareSVGLanguages[options.Lang].Periods[row.Period]; ok {
			period = p
		}
		text(48, y+96, 13, "start", options.Muted, profileShareModelShortText(period, 68))
		metric := func(value *int64, label string) string {
			if value == nil {
				return "— " + label
			}
			prefix := ""
			if row.Approximate {
				prefix = "≈"
			}
			return prefix + profileShareFormatNumber(*value, options.Format, options.Lang) + " " + label
		}
		mainMetric := metric(row.Tokens, copy.Tokens)
		if row.Tokens == nil && row.Messages != nil {
			mainMetric = metric(row.Messages, copy.Messages)
		} else if row.Tokens == nil && row.Requests != nil && options.ShowRequests {
			mainMetric = metric(row.Requests, copy.Requests)
		}
		text(1152, y+39, int(math.Min(31, 600/math.Max(1, float64(len([]rune(mainMetric)))*.62))), "end", options.Accent, mainMetric)
		extra := []string{}
		if options.ShowRequests && row.Requests != nil && (row.Tokens != nil || row.Messages != nil) {
			extra = append(extra, metric(row.Requests, copy.Requests))
		}
		if row.Messages != nil && row.Tokens != nil {
			extra = append(extra, metric(row.Messages, copy.Messages))
		}
		if len(extra) > 0 {
			text(1152, y+66, 15, "end", options.Foreground, profileShareModelShortText(strings.Join(extra, " · "), 68))
		}
		stamp := ""
		if row.ObservedAt != "" {
			stamp = copy.Observed + " " + row.ObservedAt
		} else if row.FetchedAt != "" {
			stamp = copy.Fetched + " " + row.FetchedAt
		}
		text(1152, y+96, 13, "end", options.Muted, stamp)
		// The source note makes provider scope (for example Codex lifetime tokens)
		// visible instead of hiding it behind an apparently real-time number.
		if row.SnapshotSource != "" {
			text(48, y+123, 12, "start", options.Muted, profileShareModelShortText(row.SnapshotSource, 150))
		}
		svg.WriteString(`</g>`)
	}
	footer := "api.lmm.best · profile-share"
	if options.CustomFooter {
		footer = options.Footer
	}
	text(48, contentHeight-25, 13, "start", options.Muted, profileShareModelShortText(footer, 80))
	svg.WriteString(`</g></svg>`)
	return svg.String()
}
