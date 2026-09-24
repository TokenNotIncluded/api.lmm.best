package controller

import (
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/model"
)

const profileShareDestination = "https://api.lmm.best"

var profileShareHexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

type profileShareSVGOptions struct {
	Width, Height, Radius                         int
	Layout, Period, Format                        string
	Theme, Font, Animation, Lang                  string
	Background, Foreground, Accent, Muted, Border string
	Title, Label, Footer, RequestLabel            string
	AvatarURL, AvatarDataURI                       string
	ShowRequests                                  bool
	CustomTitle, CustomLabel, CustomFooter        bool
}

type profileShareSVGLanguage struct {
	Title, Label, Requests string
	Periods                map[string]string
}

var profileShareSVGLanguages = map[string]profileShareSVGLanguage{
	"en":    {"My LMM Best activity", "Tokens used", "API requests", map[string]string{"7d": "Last 7 days", "30d": "Last 30 days", "365d": "Last 365 days", "all": "All time"}},
	"zh":    {"我在 LMM Best 的使用记录", "已用 Token", "API 请求", map[string]string{"7d": "近 7 天", "30d": "近 30 天", "365d": "近 365 天", "all": "全部时间"}},
	"zh-TW": {"我在 LMM Best 的使用紀錄", "已用 Token", "API 請求", map[string]string{"7d": "近 7 天", "30d": "近 30 天", "365d": "近 365 天", "all": "全部時間"}},
	"fr":    {"Mon activité sur LMM Best", "Tokens utilisés", "Requêtes API", map[string]string{"7d": "7 derniers jours", "30d": "30 derniers jours", "365d": "365 derniers jours", "all": "Depuis le début"}},
	"ja":    {"LMM Best の利用状況", "使用済みトークン", "API リクエスト", map[string]string{"7d": "過去 7 日", "30d": "過去 30 日", "365d": "過去 365 日", "all": "全期間"}},
	"ru":    {"Моя активность в LMM Best", "Использовано токенов", "Запросы API", map[string]string{"7d": "За 7 дней", "30d": "За 30 дней", "365d": "За 365 дней", "all": "За всё время"}},
	"vi":    {"Hoạt động của tôi trên LMM Best", "Token đã dùng", "Yêu cầu API", map[string]string{"7d": "7 ngày qua", "30d": "30 ngày qua", "365d": "365 ngày qua", "all": "Từ trước đến nay"}},
}

func profileShareCleanText(value string, maxRunes int) (string, error) {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) > maxRunes {
		return "", errors.New("text is too long")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", errors.New("text contains control characters")
		}
	}
	return value, nil
}

func profileShareColor(query url.Values, key, fallback string) (string, error) {
	value := query.Get(key)
	if value == "" {
		return fallback, nil
	}
	if !profileShareHexColor.MatchString(value) {
		return "", fmt.Errorf("invalid %s color", key)
	}
	return value, nil
}

func profileShareInteger(query url.Values, key string, fallback, min, max int) (int, error) {
	value := query.Get(key)
	if value == "" {
		return fallback, nil
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < min || number > max {
		return 0, fmt.Errorf("invalid %s", key)
	}
	return number, nil
}

func profileShareAvatarURL(query url.Values) (string, error) {
	value := strings.TrimSpace(query.Get("avatar"))
	if value == "" {
		return "", nil
	}
	if utf8.RuneCountInString(value) > 512 {
		return "", errors.New("avatar URL is too long")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", errors.New("invalid avatar URL")
	}
	return parsed.String(), nil
}

func parseProfileShareSVGOptions(query url.Values) (profileShareSVGOptions, error) {
	for key := range query {
		switch key {
		case "layout", "width", "height", "radius", "period", "format", "theme", "font", "animation", "bg", "fg", "accent", "muted", "border", "title", "label", "footer", "avatar", "requests", "lang":
		default:
			return profileShareSVGOptions{}, fmt.Errorf("unknown option %s", key)
		}
	}
	options := profileShareSVGOptions{Layout: "profile", Period: "365d", Format: "compact", Theme: "dark", Font: "sans", Animation: "wave", ShowRequests: true}
	if layout := query.Get("layout"); layout != "" {
		if layout != "profile" && layout != "badge" {
			return profileShareSVGOptions{}, errors.New("invalid layout")
		}
		options.Layout = layout
	}
	if options.Layout == "badge" {
		options.Period, options.Theme = "30d", "paper"
	}
	for _, field := range []struct {
		key     string
		target  *string
		allowed string
	}{
		{"period", &options.Period, "7d,30d,365d,all"},
		{"format", &options.Format, "compact,full"},
		{"theme", &options.Theme, "paper,dark,transparent"},
		{"font", &options.Font, "sans,mono,serif"},
		{"animation", &options.Animation, "wave,pulse,none"},
	} {
		if value := query.Get(field.key); value != "" {
			if !strings.Contains(","+field.allowed+",", ","+value+",") {
				return profileShareSVGOptions{}, fmt.Errorf("invalid %s", field.key)
			}
			*field.target = value
		}
	}
	if options.Layout == "profile" && options.Period != "365d" {
		return profileShareSVGOptions{}, errors.New("profile layout requires period=365d")
	}
	lang := query.Get("lang")
	if lang == "" {
		lang = "en"
	}
	copy, ok := profileShareSVGLanguages[lang]
	if !ok {
		return profileShareSVGOptions{}, errors.New("invalid lang")
	}
	options.Title, options.Label, options.RequestLabel = copy.Title, copy.Label, copy.Requests
	options.Lang = lang
	options.Footer = copy.Periods[options.Period]
	if options.Theme == "dark" {
		options.Background, options.Foreground, options.Accent, options.Muted, options.Border = "#202020", "#f3f3f3", "#dedede", "#aaa9a8", "#363636"
	} else {
		options.Background, options.Foreground, options.Accent, options.Muted, options.Border = "#faf9f5", "#17231f", "#d97757", "#6c746d", "#d9ded5"
	}
	if options.Theme == "transparent" {
		options.Background = "none"
	}
	var err error
	defaultWidth, defaultHeight, defaultRadius := 1200, 865, 0
	if options.Layout == "badge" {
		defaultWidth, defaultHeight, defaultRadius = 800, 240, 24
	}
	if options.Width, err = profileShareInteger(query, "width", defaultWidth, 480, 1600); err != nil {
		return profileShareSVGOptions{}, err
	}
	if options.Height, err = profileShareInteger(query, "height", defaultHeight, 200, 1200); err != nil {
		return profileShareSVGOptions{}, err
	}
	if options.Radius, err = profileShareInteger(query, "radius", defaultRadius, 0, 48); err != nil {
		return profileShareSVGOptions{}, err
	}
	if options.Background, err = profileShareColor(query, "bg", options.Background); err != nil {
		return profileShareSVGOptions{}, err
	}
	if options.Foreground, err = profileShareColor(query, "fg", options.Foreground); err != nil {
		return profileShareSVGOptions{}, err
	}
	if options.Accent, err = profileShareColor(query, "accent", options.Accent); err != nil {
		return profileShareSVGOptions{}, err
	}
	if options.Muted, err = profileShareColor(query, "muted", options.Muted); err != nil {
		return profileShareSVGOptions{}, err
	}
	if options.Border, err = profileShareColor(query, "border", options.Border); err != nil {
		return profileShareSVGOptions{}, err
	}
	if value := query.Get("requests"); value != "" {
		if value != "0" && value != "1" {
			return profileShareSVGOptions{}, errors.New("invalid requests")
		}
		options.ShowRequests = value == "1"
	}
	if options.AvatarURL, err = profileShareAvatarURL(query); err != nil {
		return profileShareSVGOptions{}, err
	}
	for _, field := range []struct {
		key    string
		target *string
		limit  int
	}{
		{"title", &options.Title, 60}, {"label", &options.Label, 32}, {"footer", &options.Footer, 48},
	} {
		if value := query.Get(field.key); value != "" {
			if *field.target, err = profileShareCleanText(value, field.limit); err != nil {
				return profileShareSVGOptions{}, fmt.Errorf("invalid %s: %w", field.key, err)
			}
			switch field.key {
			case "title":
				options.CustomTitle = true
			case "label":
				options.CustomLabel = true
			case "footer":
				options.CustomFooter = true
			}
		}
	}
	return options, nil
}

func profileSharePeriodStart(period string, now time.Time) int64 {
	switch period {
	case "7d":
		return now.Add(-7 * 24 * time.Hour).Unix()
	case "30d":
		return now.Add(-30 * 24 * time.Hour).Unix()
	case "365d":
		return now.Add(-365 * 24 * time.Hour).Unix()
	default:
		return 0
	}
}

func profileShareFormatNumber(value int64, format, lang string) string {
	if format == "full" {
		return strconv.FormatInt(value, 10)
	}
	units := []struct {
		threshold float64
		suffix    string
	}{}
	if lang == "zh" || lang == "zh-TW" || lang == "ja" {
		units = []struct {
			threshold float64
			suffix    string
		}{{1e8, "亿"}, {1e4, "万"}}
		if lang == "zh-TW" {
			units[0].suffix = "億"
		}
		if lang == "ja" {
			units[0].suffix, units[1].suffix = "億", "万"
		}
	} else {
		units = []struct {
			threshold float64
			suffix    string
		}{{1e12, "T"}, {1e9, "B"}, {1e6, "M"}, {1e3, "K"}}
	}
	for _, unit := range units {
		if float64(value) >= unit.threshold {
			formatted := strconv.FormatFloat(float64(value)/unit.threshold, 'f', 1, 64)
			return strings.TrimSuffix(formatted, ".0") + unit.suffix
		}
	}
	return strconv.FormatInt(value, 10)
}

func renderProfileShareSVG(options profileShareSVGOptions, usage model.ProfileShareUsage) string {
	fontFamily := "Arial, sans-serif"
	switch options.Font {
	case "mono":
		fontFamily = "ui-monospace, monospace"
	case "serif":
		fontFamily = "Georgia, serif"
	}
	tokenText := profileShareFormatNumber(usage.Tokens, options.Format, options.Lang)
	requestText := profileShareFormatNumber(usage.Requests, options.Format, options.Lang)
	fontSize := 76
	if len([]rune(tokenText)) > 11 {
		fontSize = 60
	}
	if len([]rune(tokenText)) > 16 {
		fontSize = 48
	}
	baseline := options.Height - 65
	if options.Height > 290 {
		baseline = options.Height - 90
	}
	var svg strings.Builder
	fmt.Fprintf(&svg, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="%s">`, options.Width, options.Height, options.Width, options.Height, html.EscapeString(options.Title))
	if options.Animation != "none" {
		svg.WriteString(`<style>`)
		if options.Animation == "wave" {
			svg.WriteString(`@keyframes flow{0%{transform:translateX(-28px);opacity:.15}50%{opacity:.75}100%{transform:translateX(28px);opacity:.15}}.flow{animation:flow 3s ease-in-out infinite}.flow:nth-child(2){animation-delay:-1s}.flow:nth-child(3){animation-delay:-2s}`)
		} else {
			svg.WriteString(`@keyframes breathe{0%,100%{opacity:.25}50%{opacity:1}}.flow{animation:breathe 2.8s ease-in-out infinite}`)
		}
		svg.WriteString(`@media(prefers-reduced-motion:reduce){.flow{animation:none!important;opacity:.55}}`)
		svg.WriteString(`</style>`)
	}
	fmt.Fprintf(&svg, `<rect x="1" y="1" width="%d" height="%d" rx="%d" fill="%s" stroke="%s" stroke-width="2"/>`, options.Width-2, options.Height-2, options.Radius, options.Background, options.Border)
	fmt.Fprintf(&svg, `<g font-family="%s"><text x="36" y="39" font-size="15" font-weight="700" fill="%s">LMM Best</text>`, fontFamily, options.Accent)
	fmt.Fprintf(&svg, `<text x="%d" y="39" text-anchor="end" font-size="14" fill="%s">api.lmm.best</text>`, options.Width-36, options.Muted)
	fmt.Fprintf(&svg, `<text x="36" y="83" font-size="25" font-weight="600" fill="%s">%s</text>`, options.Foreground, html.EscapeString(options.Title))
	fmt.Fprintf(&svg, `<text x="34" y="%d" font-size="%d" font-weight="700" fill="%s">%s</text>`, baseline, fontSize, options.Foreground, html.EscapeString(tokenText))
	fmt.Fprintf(&svg, `<text x="36" y="%d" font-size="18" fill="%s">%s · %s</text>`, baseline+32, options.Muted, html.EscapeString(options.Label), html.EscapeString(options.Footer))
	if options.ShowRequests {
		requestY := baseline - 2
		if options.Width < 650 {
			requestY = baseline - 8
		}
		fmt.Fprintf(&svg, `<text x="%d" y="%d" text-anchor="end" font-size="29" font-weight="600" fill="%s">%s</text>`, options.Width-38, requestY, options.Foreground, html.EscapeString(requestText))
		fmt.Fprintf(&svg, `<text x="%d" y="%d" text-anchor="end" font-size="15" fill="%s">%s</text>`, options.Width-38, requestY+25, options.Muted, html.EscapeString(options.RequestLabel))
	}
	fmt.Fprintf(&svg, `<rect x="36" y="%d" width="%d" height="4" rx="2" fill="%s"/>`, options.Height-22, options.Width-72, options.Accent)
	if options.Animation != "none" {
		x := options.Width - 96
		for i := 0; i < 3; i++ {
			fmt.Fprintf(&svg, `<circle class="flow" cx="%d" cy="%d" r="%d" fill="%s" opacity=".55"/>`, x+i*16, 84+i*8, 5+i*2, options.Accent)
		}
	}
	svg.WriteString(`</g></svg>`)
	return svg.String()
}
