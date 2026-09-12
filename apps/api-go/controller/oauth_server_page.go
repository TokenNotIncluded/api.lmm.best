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
	Language, Nonce, Mode, CSRF, Action, Resource, Account, Redirect string
	Groups                                                           []string
	Copy                                                             oauthPageCopy
}

var oauthPage = template.Must(template.New("oauth-consent").Parse(strings.TrimSpace(`<!doctype html>
<html lang="{{.Language}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Copy.Title}}</title>
{{if eq .Mode "complete"}}<meta http-equiv="refresh" content="0;url={{.Redirect}}">{{end}}
<style nonce="{{.Nonce}}">body{font:1rem/1.6 system-ui,sans-serif;max-width:44rem;margin:3rem auto;padding:0 1.25rem;color:#202124;background:#fff}h1{font-size:1.8rem}dt{font-weight:600}dd{margin:0 0 1rem;overflow-wrap:anywhere}button,a{font:inherit}button{padding:.65rem 1rem;margin:.5rem .5rem .5rem 0;cursor:pointer}button:focus-visible,a:focus-visible{outline:3px solid #1658bd;outline-offset:3px}small{display:block}ul{padding-left:1.3rem}</style></head><body><main>
<h1>{{.Copy.Title}}</h1>
{{if eq .Mode "failed"}}<p role="alert">{{.Copy.Failed}}</p>
{{else if eq .Mode "complete"}}<p>{{.Copy.Complete}}</p><a href="{{.Redirect}}" rel="noreferrer">{{.Copy.Return}}</a>
{{else}}<dl><dt>{{.Copy.Application}}</dt><dd>LMM for Pi</dd><dt>{{.Copy.Resource}}</dt><dd>{{.Resource}}</dd>
{{if eq .Mode "consent"}}<dt>{{.Copy.Account}}</dt><dd>{{.Account}}</dd><dt>{{.Copy.Groups}}</dt><dd><ul>{{range .Groups}}<li>{{.}}</li>{{end}}</ul></dd></dl>
<h2>{{.Copy.Permissions}}</h2><ul><li>{{.Copy.Invoke}}</li><li>{{.Copy.Catalog}}</li><li>{{.Copy.Balance}}</li></ul><p>{{.Copy.Snapshot}}</p>
<form method="post" action="{{.Action}}"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit" name="decision" value="allow">{{.Copy.Allow}}</button><button type="submit" name="decision" value="deny">{{.Copy.Cancel}}</button></form>
{{else}}</dl><p>{{.Copy.LoginHelp}}</p><a href="/login" target="_blank" rel="noopener noreferrer">{{.Copy.Login}}</a><form method="post" action="{{.Action}}"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit">{{.Copy.Continue}}</button></form>{{end}}{{end}}
</main></body></html>`)))
