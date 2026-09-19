package controller

import (
	"html/template"
	"strings"

	"golang.org/x/text/language"
)

type oauthPageCopy struct {
	Title, Application, Resource, Account, Groups, Permissions     string
	Invoke, Catalog, Balance, Snapshot, Continue, Login, LoginHelp string
	Allow, Cancel, Failed, Complete, Return                        string
}

var oauthPageCopies = map[string]oauthPageCopy{
	"en":    {"Authorize Pi", "Application", "Resource", "Account", "Allowed groups", "Permissions", "Call models in these groups and charge this account under its normal billing rules.", "Read available models, group multipliers and pricing information.", "Read your own account balance.", "Only the groups listed here are authorized. Future groups are not included. Prices may change; final usage is billed by the server.", "Continue", "Sign in", "Sign in in another tab, then return here and continue. No API key is needed.", "Allow access", "Cancel", "This request expired, your account changed, or access is not permitted. Start login again from Pi.", "Authorization complete", "Return to Pi"},
	"zh":    {"授权 Pi", "应用", "资源", "账号", "允许的分组", "权限", "调用这些分组的模型，按此账号的正常计费规则扣费。", "读取可用模型、分组倍率和价格信息。", "读取您自己的账户余额。", "仅授权此处列出的分组，不包含未来新增分组。价格可能变化，实际用量以服务端结算为准。", "继续", "登录", "请在另一个标签页登录，然后返回此处继续。无需创建 API Key。", "同意授权", "取消", "请求已过期、账号已切换或无权访问。请从 Pi 重新发起登录。", "授权完成", "返回 Pi"},
	"zh-TW": {"授權 Pi", "應用程式", "資源", "帳號", "允許的群組", "權限", "呼叫這些群組的模型，依此帳號的一般計費規則扣款。", "讀取可用模型、群組倍率與價格資訊。", "讀取您自己的帳戶餘額。", "僅授權此處列出的群組，不包含未來新增群組。價格可能變動，實際用量以伺服器結算為準。", "繼續", "登入", "請在另一個分頁登入，再返回此處繼續。不必建立 API Key。", "同意授權", "取消", "請求已過期、帳號已切換或沒有存取權。請從 Pi 重新發起登入。", "授權完成", "返回 Pi"},
	"fr":    {"Autoriser Pi", "Application", "Ressource", "Compte", "Groupes autorisés", "Autorisations", "Appeler les modèles de ces groupes et facturer ce compte selon ses règles habituelles.", "Consulter les modèles disponibles, les multiplicateurs des groupes et les tarifs.", "Consulter le solde de votre propre compte.", "Seuls les groupes affichés sont autorisés, pas les futurs groupes. Les prix peuvent changer ; le serveur calcule la facturation réelle.", "Continuer", "Se connecter", "Connectez-vous dans un autre onglet, puis revenez ici. Aucune clé API n’est nécessaire.", "Autoriser l’accès", "Annuler", "La demande a expiré, le compte a changé ou l’accès est refusé. Relancez la connexion depuis Pi.", "Autorisation terminée", "Retour à Pi"},
	"ja":    {"Pi を認可", "アプリ", "リソース", "アカウント", "許可するグループ", "権限", "これらのグループのモデルを呼び出し、このアカウントの通常の課金ルールに従って支払います。", "利用可能なモデル、グループ倍率、料金情報を読み取ります。", "自分のアカウント残高を読み取ります。", "ここに表示されたグループだけを許可します。今後追加されるグループは含みません。価格は変わる場合があり、実際の料金はサーバーで精算されます。", "続ける", "ログイン", "別のタブでログインしてから、ここに戻って続けてください。API キーは不要です。", "アクセスを許可", "キャンセル", "リクエストの期限切れ、アカウントの切り替え、または権限不足です。Pi からログインし直してください。", "認可が完了しました", "Pi に戻る"},
	"ru":    {"Разрешить доступ Pi", "Приложение", "Ресурс", "Аккаунт", "Разрешённые группы", "Разрешения", "Вызывать модели этих групп с оплатой по обычным правилам вашего аккаунта.", "Просматривать доступные модели, множители групп и цены.", "Просматривать баланс только вашего аккаунта.", "Доступ разрешается только к указанным группам, без будущих групп. Цены могут меняться; итоговую стоимость рассчитывает сервер.", "Продолжить", "Войти", "Войдите в другой вкладке и вернитесь сюда. Ключ API не нужен.", "Разрешить доступ", "Отмена", "Запрос истёк, аккаунт изменился или доступ запрещён. Начните вход заново из Pi.", "Доступ разрешён", "Вернуться в Pi"},
	"vi":    {"Cho phép Pi truy cập", "Ứng dụng", "Tài nguyên", "Tài khoản", "Nhóm được phép", "Quyền", "Gọi mô hình trong các nhóm này và tính phí theo quy tắc thông thường của tài khoản.", "Đọc các mô hình khả dụng, hệ số nhóm và thông tin giá.", "Đọc số dư của chính tài khoản bạn.", "Chỉ cấp quyền cho các nhóm được liệt kê, không bao gồm nhóm mới trong tương lai. Giá có thể thay đổi; máy chủ quyết toán mức sử dụng thực tế.", "Tiếp tục", "Đăng nhập", "Đăng nhập trong tab khác rồi quay lại đây để tiếp tục. Không cần tạo API key.", "Cho phép truy cập", "Hủy", "Yêu cầu đã hết hạn, tài khoản đã thay đổi hoặc bạn không có quyền truy cập. Hãy đăng nhập lại từ Pi.", "Đã cấp quyền", "Quay lại Pi"},
}

var oauthLanguages = []string{"en", "zh", "zh-TW", "fr", "ja", "ru", "vi"}
var oauthLanguageMatcher = language.NewMatcher([]language.Tag{language.English, language.SimplifiedChinese, language.TraditionalChinese, language.French, language.Japanese, language.Russian, language.Vietnamese})

func oauthPageLanguage(accept string) string {
	// Bound Accept-Language work; languages come from the browser, not a new
	// authorization parameter which the strict core would reject.
	if len(accept) > 1024 {
		return "en"
	}
	tags, _, err := language.ParseAcceptLanguage(accept)
	if err != nil || len(tags) == 0 {
		return "en"
	}
	_, index, _ := oauthLanguageMatcher.Match(tags...)
	return oauthLanguages[index]
}

type oauthPageData struct {
	CanInvoke                                                                    bool
	Language, Nonce, Mode, CSRF, Action, Resource, ClientName, Account, Redirect string
	Groups                                                                       []string
	Copy                                                                         oauthPageCopy
}

var oauthPage = template.Must(template.New("oauth-consent").Parse(strings.TrimSpace(`<!doctype html>
<html lang="{{.Language}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Copy.Title}}</title>
{{if eq .Mode "complete"}}<meta http-equiv="refresh" content="0;url={{.Redirect}}">{{end}}
<style nonce="{{.Nonce}}">
:root { color-scheme: light; --paper: #f6f3ed; --ink: #23221f; --muted: #706c64; --line: #d8d2c7; --accent: #b85d43; --accent-ink: #fffaf5; }
* { box-sizing: border-box; }
body { margin: 0; min-height: 100vh; background: var(--paper); color: var(--ink); font: 16px/1.6 'Public Sans', system-ui, -apple-system, 'Segoe UI', sans-serif; }
main { width: min(100% - 2rem, 42rem); margin: clamp(2rem, 8vw, 6rem) auto; padding: clamp(1rem, 5vw, 2.5rem) 0; }
header { display: flex; align-items: center; gap: .7rem; margin-bottom: 3rem; font-weight: 700; letter-spacing: -.02em; }
header svg { width: 2rem; height: 2rem; flex: none; } .mark { stroke: var(--accent); }
h1 { max-width: 18ch; margin: 0 0 2.5rem; font: 700 clamp(2.25rem, 6vw, 3.5rem)/1.02 Georgia, 'Times New Roman', serif; letter-spacing: -.04em; }
h2 { margin: 2.25rem 0 .9rem; font-size: .75rem; letter-spacing: .14em; text-transform: uppercase; color: var(--muted); }
dl { display: grid; grid-template-columns: minmax(7rem, 9rem) 1fr; gap: .7rem 1.5rem; margin: 0; padding: 1.25rem 0; border-block: 1px solid var(--line); }
dt { color: var(--muted); font-size: .85rem; } dd { margin: 0; overflow-wrap: anywhere; }
.groups { display: flex; flex-wrap: wrap; gap: .25rem .75rem; margin: 0; padding: 0; list-style: none; }
.groups li { white-space: nowrap; } p { margin: 1rem 0; color: var(--muted); }
.notice { margin: 0 0 1.5rem; color: var(--muted); }
.permissions { display: grid; gap: .85rem; margin: 0; padding: 0; list-style: none; }
.permissions li { display: grid; grid-template-columns: 1.35rem 1fr; gap: .65rem; align-items: start; margin: 0; }
.permissions svg { width: 1.15rem; height: 1.15rem; margin-top: .25rem; color: var(--accent); }
.actions { display: flex; flex-wrap: wrap; gap: .75rem; margin-top: 2rem; } form { display: flex; flex-wrap: wrap; gap: .75rem; }
button, a { font: inherit; } button, a.action { display: inline-flex; align-items: center; justify-content: center; min-height: 2.75rem; padding: .65rem 1.1rem; border: 1px solid var(--ink); border-radius: 7px; cursor: pointer; text-decoration: none; }
button[type=submit][value=allow], button[type=submit]:not([value]) { background: var(--ink); color: var(--accent-ink); }
button[type=submit][value=deny], a.action.secondary { background: transparent; color: var(--ink); border-color: var(--line); }
button:focus-visible, a:focus-visible { outline: 3px solid var(--accent); outline-offset: 3px; }
@media (max-width: 36rem) { main { width: min(100% - 2rem, 42rem); margin: 1rem auto; padding: 1rem 0 2rem; } header { margin-bottom: 2rem; } h1 { font-size: 2.5rem; margin-bottom: 2rem; } dl { grid-template-columns: 1fr; gap: .15rem; } dt { margin-top: .75rem; } .actions, form { width: 100%; } .actions > *, form > * { flex: 1 1 100%; } }
</style></head><body><main>
<header><svg viewBox="0 0 56 56" aria-hidden="true"><path d="M10 39V16l18 18 18-18v23" fill="none" stroke="currentColor" stroke-width="3.5" stroke-linejoin="round"/><path class="mark" d="M12 45h32" fill="none" stroke-width="2.5"/></svg><span>lmm.best</span></header><h1>{{.Copy.Title}}</h1>
{{if eq .Mode "failed"}}<p class="notice" role="alert">{{.Copy.Failed}}</p>
{{else if eq .Mode "complete"}}<p class="notice">{{.Copy.Complete}}</p><div class="actions"><a class="action" href="{{.Redirect}}" rel="noreferrer">{{.Copy.Return}}</a></div>
{{else}}<dl><dt>{{.Copy.Application}}</dt><dd>{{.ClientName}}</dd><dt>{{.Copy.Resource}}</dt><dd>{{.Resource}}</dd>
{{if eq .Mode "consent"}}<dt>{{.Copy.Account}}</dt><dd>{{.Account}}</dd><dt>{{.Copy.Groups}}</dt><dd><ul>{{range .Groups}}<li>{{.}}</li>{{end}}</ul></dd></dl>
<h2>{{.Copy.Permissions}}</h2><ul class="permissions">{{if .CanInvoke}}<li><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 12h14M12 5l7 7-7 7" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg><span>{{.Copy.Invoke}}</span></li>{{end}}<li><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16M4 12h16M4 17h10" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/></svg><span>{{.Copy.Catalog}}</span></li><li><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 4a8 8 0 1 0 0 16 8 8 0 0 0 0-16Zm0 4v4l2.5 2" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg><span>{{.Copy.Balance}}</span></li></ul><p>{{.Copy.Snapshot}}</p>
<div class="actions"><form method="post" action="{{.Action}}"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit" name="decision" value="allow">{{.Copy.Allow}}</button><button type="submit" name="decision" value="deny">{{.Copy.Cancel}}</button></form></div>
{{else}}</dl><p class="notice">{{.Copy.LoginHelp}}</p><div class="actions"><a class="action secondary" href="/login" target="_blank" rel="noopener noreferrer">{{.Copy.Login}}</a><form method="post" action="{{.Action}}"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit">{{.Copy.Continue}}</button></form></div>{{end}}{{end}}
</main></body></html>`)))
