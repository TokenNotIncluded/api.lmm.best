package controller

import (
	"fmt"
	"html"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/model"
)

type profileShareProfileLabels struct {
	YearTokens, PeakTokens, Requests, CurrentStreak, LongestStreak string
	Activity, Days, Member, Admin, SuperAdmin                      string
}

var profileShareProfileCopy = map[string]profileShareProfileLabels{
	"en":    {"Tokens in the past year", "Peak daily tokens", "API requests", "Current streak", "Longest streak this year", "Token activity", "days", "Member", "Admin", "Super Admin"},
	"zh":    {"近一年 Token 数", "单日峰值 Token 数", "API 请求", "当前连续天数", "近一年最长连续天数", "Token 活动", "天", "用户", "管理员", "超级管理员"},
	"zh-TW": {"近一年 Token 數", "單日峰值 Token 數", "API 請求", "目前連續天數", "近一年最長連續天數", "Token 活動", "天", "使用者", "管理員", "超級管理員"},
	"fr":    {"Tokens sur un an", "Pic quotidien de tokens", "Requêtes API", "Série actuelle", "Plus longue série sur un an", "Activité des tokens", "jours", "Membre", "Admin", "Super admin"},
	"ja":    {"過去 1 年のトークン数", "1 日の最大トークン数", "API リクエスト", "現在の連続日数", "過去 1 年の最長連続日数", "トークン利用状況", "日", "ユーザー", "管理者", "スーパー管理者"},
	"ru":    {"Токены за год", "Пик токенов за день", "Запросы API", "Текущая серия", "Макс. серия за год", "Активность токенов", "дн.", "Участник", "Администратор", "Суперадминистратор"},
	"vi":    {"Token trong năm qua", "Đỉnh token mỗi ngày", "Yêu cầu API", "Chuỗi ngày hiện tại", "Chuỗi dài nhất trong năm", "Hoạt động token", "ngày", "Thành viên", "Quản trị viên", "Quản trị cấp cao"},
}

type profileShareYearSummary struct {
	Tokens, Peak     int64
	Current, Longest int
	Levels           [371]int
}

func buildProfileShareYearSummary(rows []model.ProfileShareDay, startDay int64) profileShareYearSummary {
	var summary profileShareYearSummary
	var daily [371]int64
	for _, row := range rows {
		index := int((row.Day - startDay) / 86400)
		if row.Day < startDay || index < 0 || index >= len(daily) {
			continue
		}
		if row.Tokens > 0 {
			daily[index] += row.Tokens
		}
	}
	running := 0
	for _, tokens := range daily {
		summary.Tokens += tokens
		if tokens > summary.Peak {
			summary.Peak = tokens
		}
		if tokens > 0 {
			running++
			if running > summary.Longest {
				summary.Longest = running
			}
		} else {
			running = 0
		}
	}
	for index, tokens := range daily {
		if tokens > 0 && summary.Peak > 0 {
			level := int(math.Ceil(float64(tokens) / float64(summary.Peak) * 5))
			if level < 1 {
				level = 1
			}
			if level > 5 {
				level = 5
			}
			summary.Levels[index] = level
		}
	}
	end := len(daily) - 1
	if daily[end] == 0 {
		end--
	}
	for end >= 0 && daily[end] > 0 {
		summary.Current++
		end--
	}
	return summary
}

func profileShareMonthLabel(month time.Month, lang string) string {
	switch lang {
	case "zh", "zh-TW", "ja":
		return fmt.Sprintf("%d月", month)
	case "vi":
		return fmt.Sprintf("Th %d", month)
	case "fr":
		return [...]string{"", "janv.", "févr.", "mars", "avr.", "mai", "juin", "juil.", "août", "sept.", "oct.", "nov.", "déc."}[month]
	case "ru":
		return [...]string{"", "янв.", "февр.", "мар.", "апр.", "май", "июн.", "июл.", "авг.", "сент.", "окт.", "нояб.", "дек."}[month]
	default:
		return month.String()[:3]
	}
}

func profileShareProfileRole(role int, labels profileShareProfileLabels) string {
	if role >= 100 {
		return labels.SuperAdmin
	}
	if role >= 10 {
		return labels.Admin
	}
	return labels.Member
}

func profileShareProfileFontSize(value string) int {
	length := utf8.RuneCountInString(value)
	if length > 13 {
		return 22
	}
	if length > 9 {
		return 26
	}
	return 31
}

func renderProfileShareProfileSVG(
	options profileShareSVGOptions,
	owner *model.User,
	rows []model.ProfileShareDay,
	startDay int64,
) string {
	labels := profileShareProfileCopy[options.Lang]
	summary := buildProfileShareYearSummary(rows, startDay)
	fontFamily := "Arial, sans-serif"
	switch options.Font {
	case "mono":
		fontFamily = "ui-monospace, monospace"
	case "serif":
		fontFamily = "Georgia, serif"
	}
	name := owner.DisplayName
	if name == "" {
		name = owner.Username
	}
	if options.CustomTitle {
		name = options.Title
	}
	if cleaned, err := profileShareCleanText(name, 60); err != nil {
		name = owner.Username
	} else {
		name = cleaned
	}
	if utf8.RuneCountInString(name) > 30 {
		name = string([]rune(name)[:29]) + "…"
	}
	nameFontSize := 42
	if utf8.RuneCountInString(name) > 20 {
		nameFontSize = 32
	}
	first := "?"
	if name != "" {
		first = string([]rune(name)[0])
	}
	yearLabel := labels.YearTokens
	if options.CustomLabel {
		yearLabel = options.Label
	}
	footer := "LMM Best"
	if options.CustomFooter {
		footer = options.Footer
	}
	role := profileShareProfileRole(owner.Role, labels)
	current := fmt.Sprintf("%d %s", summary.Current, labels.Days)
	longest := fmt.Sprintf("%d %s", summary.Longest, labels.Days)
	stats := [][2]string{
		{profileShareFormatNumber(summary.Tokens, options.Format, options.Lang), yearLabel},
		{profileShareFormatNumber(summary.Peak, options.Format, options.Lang), labels.PeakTokens},
		{current, labels.CurrentStreak},
		{longest, labels.LongestStreak},
	}
	if options.ShowRequests {
		stats = append(stats[:2], append([][2]string{{profileShareFormatNumber(int64(owner.RequestCount), options.Format, options.Lang), labels.Requests}}, stats[2:]...)...)
	}
	var svg strings.Builder
	fmt.Fprintf(&svg, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 1200 865" preserveAspectRatio="none" role="img" aria-label="%s">`, options.Width, options.Height, html.EscapeString(name+" · "+labels.Activity))
	if options.Animation != "none" {
		svg.WriteString(`<style>`)
		if options.Animation == "wave" {
			svg.WriteString(`@keyframes heat{0%,100%{opacity:.55}50%{opacity:1}}.heat{animation:heat 4s ease-in-out infinite}`)
		} else {
			svg.WriteString(`@keyframes heat{0%,100%{opacity:.65}50%{opacity:1}}.heat{animation:heat 2.8s ease-in-out infinite}`)
		}
		svg.WriteString(`@media(prefers-reduced-motion:reduce){.heat{animation:none!important;opacity:1}}`)
		svg.WriteString(`</style>`)
	}
	fmt.Fprintf(&svg, `<rect width="1200" height="865" rx="%d" fill="%s"/>`, options.Radius, options.Background)
	fmt.Fprintf(&svg, `<g font-family="%s">`, fontFamily)
	fmt.Fprintf(&svg, `<circle cx="600" cy="136" r="70" fill="%s"/><text x="600" y="157" text-anchor="middle" font-size="64" font-weight="600" fill="%s">%s</text>`, options.Accent, options.Background, html.EscapeString(first))
	fmt.Fprintf(&svg, `<text x="600" y="283" text-anchor="middle" font-size="%d" font-weight="500" fill="%s">%s</text>`, nameFontSize, options.Foreground, html.EscapeString(name))
	fmt.Fprintf(&svg, `<text x="585" y="332" text-anchor="end" font-size="22" fill="%s">@%s</text>`, options.Muted, html.EscapeString(owner.Username))
	fmt.Fprintf(&svg, `<rect x="604" y="307" width="190" height="36" rx="18" fill="none" stroke="%s"/><text x="699" y="331" text-anchor="middle" font-size="18" fill="%s">%s</text>`, options.Border, options.Muted, html.EscapeString(role))
	fmt.Fprintf(&svg, `<rect x="40" y="379" width="1120" height="112" rx="44" fill="none" stroke="%s"/>`, options.Border)
	statWidth := 1120 / len(stats)
	for index, stat := range stats {
		x := 40 + index*statWidth + statWidth/2
		if index > 0 {
			fmt.Fprintf(&svg, `<path d="M%d 380V490" stroke="%s"/>`, 40+index*statWidth, options.Border)
		}
		fmt.Fprintf(&svg, `<text x="%d" y="429" text-anchor="middle" font-size="%d" font-weight="500" fill="%s">%s</text>`, x, profileShareProfileFontSize(stat[0]), options.Foreground, html.EscapeString(stat[0]))
		fmt.Fprintf(&svg, `<text x="%d" y="463" text-anchor="middle" font-size="17" fill="%s">%s</text>`, x, options.Muted, html.EscapeString(stat[1]))
	}
	fmt.Fprintf(&svg, `<text x="40" y="571" font-size="28" font-weight="500" fill="%s">%s</text>`, options.Foreground, html.EscapeString(labels.Activity))
	for week := 0; week < 53; week++ {
		for day := 0; day < 7; day++ {
			level := summary.Levels[week*7+day]
			x, y := 40+week*21, 607+day*21
			if level == 0 {
				fmt.Fprintf(&svg, `<rect x="%d" y="%d" width="16" height="16" rx="4" fill="%s" opacity=".7"/>`, x, y, options.Border)
			} else {
				class := ""
				if options.Animation != "none" {
					class = ` class="heat"`
				}
				fmt.Fprintf(&svg, `<rect%s x="%d" y="%d" width="16" height="16" rx="4" fill="%s" opacity="%.2f" style="animation-delay:-%.1fs"/>`, class, x, y, options.Accent, .25+float64(level)*.15, float64(week%7)*.45)
			}
		}
	}
	lastMonth := time.Month(0)
	type monthLabel struct {
		week  int
		month time.Month
	}
	var months []monthLabel
	for week := 0; week < 53; week++ {
		month := time.Unix(startDay+int64(week*7+3)*86400, 0).UTC().Month()
		if month == lastMonth {
			continue
		}
		lastMonth = month
		months = append(months, monthLabel{week, month})
	}
	if len(months) > 1 && months[1].week-months[0].week < 4 {
		months = months[1:]
	}
	for _, month := range months {
		fmt.Fprintf(&svg, `<text x="%d" y="785" font-size="15" fill="%s">%s</text>`, 40+month.week*21, options.Muted, html.EscapeString(profileShareMonthLabel(month.month, options.Lang)))
	}
	fmt.Fprintf(&svg, `<text x="40" y="834" font-size="17" fill="%s">%s</text><text x="1160" y="834" text-anchor="end" font-size="17" fill="%s">%s</text>`, options.Muted, html.EscapeString(footer), options.Foreground, profileShareDestination)
	svg.WriteString(`</g></svg>`)
	return svg.String()
}
