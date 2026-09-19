package controller

import (
	"github.com/LIghtJUNction/api.lmm.best/service"
	"slices"
)

func marketOAuthPermissionLabels(language string, scopes []string) []string {
	copy := map[string][3]string{
		"en":    {"Discover tools available to your account.", "Call market tools only within your separate tool authorizations and spending limits.", "Load and unload tools for this client; this does not authorize spending."},
		"zh":    {"发现此账号有权访问的工具。", "仅在单独授予的工具权限和消费限额内调用市场工具。", "为此客户端加载和卸载工具；这不代表同意付费。"},
		"zh-TW": {"探索此帳號有權存取的工具。", "僅在個別授予的工具權限與消費限額內呼叫市集工具。", "為此用戶端載入與卸載工具；這不代表同意付費。"},
		"fr":    {"Découvrir les outils accessibles à votre compte.", "Appeler les outils dans les limites des autorisations et budgets accordés séparément.", "Charger et retirer des outils pour ce client, sans autoriser de dépenses."},
		"ja":    {"このアカウントがアクセスできるツールを検索します。", "個別に設定したツールの権限と支出上限の範囲内で呼び出します。", "このクライアントのツールを追加・削除します。支払いの許可は含みません。"},
		"ru":    {"Находить инструменты, доступные этому аккаунту.", "Вызывать инструменты только в рамках отдельных разрешений и лимитов расходов.", "Добавлять и удалять инструменты клиента без разрешения на расходы."},
		"vi":    {"Tìm công cụ mà tài khoản được phép truy cập.", "Chỉ gọi công cụ trong phạm vi quyền và hạn mức chi tiêu đã cấp riêng.", "Thêm hoặc gỡ công cụ cho ứng dụng này; không bao gồm quyền chi tiêu."},
	}
	labels, ok := copy[language]
	if !ok {
		labels = copy["en"]
	}
	result := []string{}
	for i, scope := range []string{service.OAuthMarketDiscoverScope, service.OAuthMarketInvokeScope, service.OAuthMarketManageScope} {
		if slices.Contains(scopes, scope) {
			result = append(result, labels[i])
		}
	}
	return result
}
