/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
/*
Copyright (C) 2026 LIghtJUNction
*/
import fs from 'node:fs/promises'
import path from 'node:path'

const LOCALES_DIR = path.resolve('src/i18n/locales')

function stableStringify(obj) {
  const serialized = JSON.stringify(obj, null, 2).replace(
    '"footer.newapi.projectAttributionSuffix":',
    '"footer.new\\u0061pi.projectAttributionSuffix":'
  )
  return `${serialized}\n`
}

const newKeys = {
  en: {
    'Runtime instances reporting from this deployment; slots on the same node are listed separately.':
      'Runtime instances reporting from this deployment; slots on the same node are listed separately.',
    'Clean up review history': 'Clean up review history',
    'Clean up automatic review history?': 'Clean up automatic review history?',
    'This will permanently delete {{count}} completed or failed automatic review runs while keeping the latest {{keep}}. Active runs and security audit evidence will not be deleted.':
      'This will permanently delete {{count}} completed or failed automatic review runs while keeping the latest {{keep}}. Active runs and security audit evidence will not be deleted.',
    'No completed automatic review runs are eligible for cleanup.':
      'No completed automatic review runs are eligible for cleanup.',
    'Automatic review history cleanup completed':
      'Automatic review history cleanup completed',
    'Automatic review history changed. Review the refreshed preview and confirm again.':
      'Automatic review history changed. Review the refreshed preview and confirm again.',
    'Failed to clean up automatic review history':
      'Failed to clean up automatic review history',
    'Clear exhausted codes': 'Clear exhausted codes',
    'Unable to clear exhausted discount codes':
      'Unable to clear exhausted discount codes',
    'Deleted {{count}} exhausted discount codes':
      'Deleted {{count}} exhausted discount codes',
    'Delete exhausted discount codes?': 'Delete exhausted discount codes?',
    'This permanently removes every finite-use discount code whose usage limit has been reached. Partially used and unlimited codes are kept.':
      'This permanently removes every finite-use discount code whose usage limit has been reached. Partially used and unlimited codes are kept.',
    'Delete exhausted codes': 'Delete exhausted codes',
    'No exhausted discount codes to delete':
      'No exhausted discount codes to delete',
    Guide: 'Guide',
    'Just one endpoint': 'Just one endpoint',
    'Connect the world’s most popular models':
      'Connect the world’s most popular models',
    'Pay as you go, no time limits, fast chat, transparent details, no hidden fees, and online recharge for access to every model.':
      'Pay as you go, no time limits, fast chat, transparent details, no hidden fees, and online recharge for access to every model.',
    'One platform, many uses': 'One platform, many uses',
    Statistics: 'Statistics',
    Years: 'Years',
    Web: 'Web',
    'Upstream returned no usage; no quota charged':
      'Upstream returned no usage; no quota charged',
    'View model pricing': 'View model pricing',
    'Browse open-source work': 'Browse open-source work',
    'Read the guide': 'Read the guide',
    'At a glance': 'At a glance',
    'One endpoint': 'One endpoint',
    'OpenAI and Anthropic-compatible routes.':
      'OpenAI and Anthropic-compatible routes.',
    'Clear pricing': 'Clear pricing',
    'Choose the model and group before you spend.':
      'Choose the model and group before you spend.',
    'Human review': 'Human review',
    'Support and access requests stay auditable.':
      'Support and access requests stay auditable.',
    'Use one clear API for your work, connect a client, or explore public open-source challenges.':
      'Use one clear API for your work, connect a client, or explore public open-source challenges.',
    'Financial overview': 'Financial overview',
    Expenses: 'Expenses',
    Profit: 'Profit',
    'Token economy': 'Token economy',
    'External expense': 'External expense',
    'Add expense': 'Add expense',
    'Record expense': 'Record expense',
    'Past 7 days': 'Past 7 days',
    'Past 30 days': 'Past 30 days',
    'Past 90 days': 'Past 90 days',
    'Payment method': 'Payment method',
    'No entries': 'No entries',
    Estimated: 'Estimated',
    'Unpriced requests': 'Unpriced requests',
    'View user': 'View user',
    'User spending': 'User spending',
    'Include revenue': 'Include revenue',
    'Save settings': 'Save settings',
    Reversal: 'Reversal',
    Reverse: 'Reverse',
    'Profit margin': 'Profit margin',
    'Classic chat': 'Classic chat',
    'Modern chat': 'Modern chat',
    'Use classic layout': 'Use classic layout',
    'Use modern layout': 'Use modern layout',
    'New conversation': 'New conversation',
    Examples: 'Examples',
    Capabilities: 'Capabilities',
    Limitations: 'Limitations',
    'Explain an API setup': 'Explain an API setup',
    'Compare live model pricing': 'Compare live model pricing',
    'Draft an access request': 'Draft an access request',
    'Live models and pricing': 'Live models and pricing',
    'Step-by-step setup guidance': 'Step-by-step setup guidance',
    'Confirm sensitive actions yourself': 'Confirm sensitive actions yourself',
    'Permissions still apply': 'Permissions still apply',
    'Never share secrets in chat': 'Never share secrets in chat',
    'Write actions need your confirmation':
      'Write actions need your confirmation',
    'Unable to load data': 'Unable to load data',
    'Append-only ledger': 'Append-only ledger',
    'Ledger entries are limited to durable ledger events. Use Financial overview for reconciled revenue.':
      'Ledger entries are limited to durable ledger events. Use Financial overview for reconciled revenue.',
    'Unable to load archive': 'Unable to load archive',
    'View L1 recommendation archive': 'View L1 recommendation archive',
    'L1 recommendation archive': 'L1 recommendation archive',
    'No approved recommendation archive yet.':
      'No approved recommendation archive yet.',
    Approved: 'Approved',
    Request: 'Request',
    'Administrator replied': 'Administrator replied',
    'AI recommendation (optional)': 'AI recommendation (optional)',
    'Submit for administrator review': 'Submit for administrator review',
    'Waiting for an administrator': 'Waiting for an administrator',
    'Unable to load your support tasks': 'Unable to load your support tasks',
    'The administrator marked this request resolved.':
      'The administrator marked this request resolved.',
    'Administrator note': 'Administrator note',
    'User skills': 'User skills',
    'Security reviews': 'Security reviews',
    'assistant.security_review': 'Assistant security review',
    'Security audit details': 'Security audit details',
    'Audit data is available to administrators only.':
      'Audit data is available to administrators only.',
    'Review results from deterministic rules and asynchronous AI audits. Prompt text, previews, matcher patterns, and credentials are never shown here.':
      'Review results from deterministic rules and asynchronous AI audits. Prompt text, previews, matcher patterns, and credentials are never shown here.',
    'Protected groups': 'Protected groups',
    'Only groups listed by an enabled rule are included. Rules do not apply globally.':
      'Only groups listed by an enabled rule are included. Rules do not apply globally.',
    'No groups are currently covered by enabled advanced security rules.':
      'No groups are currently covered by enabled advanced security rules.',
    'Only explicitly listed groups are covered by advanced security rules; rules do not apply globally.':
      'Only explicitly listed groups are covered by advanced security rules; rules do not apply globally.',
    'No protected groups are published yet.':
      'No protected groups are published yet.',
    'Deterministic rule': 'Deterministic rule',
    'All categories': 'All categories',
    'All groups': 'All groups',
    'All decisions': 'All decisions',
    'All sources': 'All sources',
    Violation: 'Violation',
    Clear: 'Clear',
    Reviews: 'Reviews',
    Abuse: 'Abuse',
    Occurred: 'Occurred',
    'Review source': 'Review source',
    'No security audit events match the current filters.':
      'No security audit events match the current filters.',
    'Group warning': 'Group warning',
    'Confirmation {{current}} of {{total}}':
      'Confirmation {{current}} of {{total}}',
    'I understand, continue': 'I understand, continue',
  },
  zh: {
    'Runtime instances reporting from this deployment; slots on the same node are listed separately.':
      '此部署中正在上报的运行实例；同一节点上的不同槽位会分开列出。',
    'Clean up review history': '清理复盘历史',
    'Clean up automatic review history?': '清理自动复盘历史？',
    'This will permanently delete {{count}} completed or failed automatic review runs while keeping the latest {{keep}}. Active runs and security audit evidence will not be deleted.':
      '此操作将永久删除 {{count}} 次已完成或失败的自动复盘记录，并保留最近 {{keep}} 次。运行中的任务和安全审计证据不会被删除。',
    'No completed automatic review runs are eligible for cleanup.':
      '没有可清理的已完成自动复盘记录。',
    'Automatic review history cleanup completed': '自动复盘历史清理完成',
    'Automatic review history changed. Review the refreshed preview and confirm again.':
      '自动复盘历史已发生变化。请查看刷新后的预览并重新确认。',
    'Failed to clean up automatic review history': '自动复盘历史清理失败',
    'Clear exhausted codes': '清理已用完优惠码',
    'Unable to clear exhausted discount codes': '无法清理已用完优惠码',
    'Deleted {{count}} exhausted discount codes':
      '已删除 {{count}} 个已用完优惠码',
    'Delete exhausted discount codes?': '删除已用完优惠码？',
    'This permanently removes every finite-use discount code whose usage limit has been reached. Partially used and unlimited codes are kept.':
      '此操作将永久删除所有已达到使用上限的有限次数优惠码。部分使用和不限次数的优惠码会保留。',
    'Delete exhausted codes': '删除已用完优惠码',
    'No exhausted discount codes to delete': '没有需要删除的已用完优惠码',
    Guide: '接入指南',
    'Just one endpoint': '仅需一个接口',
    'Connect the world’s most popular models': '连通全球最热门的模型',
    'Pay as you go, no time limits, fast chat, transparent details, no hidden fees, and online recharge for access to every model.':
      '按量计费、不限时间、极速对话、明细透明，无隐藏消费，在线充值后即可使用所有模型。',
    'One platform, many uses': '一个平台，多种用途',
    Statistics: '统计',
    Years: '年',
    Web: '前端',
    'Upstream returned no usage; no quota charged': '上游没有返回用量，未扣费',
    'View model pricing': '查看模型价格',
    'Browse open-source work': '浏览开源任务',
    'Read the guide': '阅读接入指南',
    'At a glance': '快速了解',
    'One endpoint': '一个接口',
    'OpenAI and Anthropic-compatible routes.':
      '兼容 OpenAI 与 Anthropic 的接口。',
    'Clear pricing': '价格透明',
    'Choose the model and group before you spend.':
      '先选择模型和分组，再开始使用。',
    'Human review': '人工审核',
    'Support and access requests stay auditable.': '支持与访问申请都可追溯。',
    'Use one clear API for your work, connect a client, or explore public open-source challenges.':
      '用一个清晰的 API 完成工作、连接客户端，或探索公开的开源任务。',
    'Financial overview': '财务概览',
    Expenses: '支出',
    Profit: '利润',
    'Token economy': 'Token 经济',
    'External expense': '外部支出',
    'Add expense': '添加支出',
    'Record expense': '记录支出',
    'Past 7 days': '近 7 天',
    'Past 30 days': '近 30 天',
    'Past 90 days': '近 90 天',
    'Payment method': '支付方式',
    'No entries': '暂无记录',
    Estimated: '估算',
    'Unpriced requests': '未定价请求',
    'View user': '查看用户',
    'User spending': '用户支出',
    'Include revenue': '计入收入',
    'Save settings': '保存设置',
    Reversal: '冲销',
    Reverse: '冲销',
    'Profit margin': '利润率',
    'Classic chat': '经典对话',
    'Modern chat': '现代对话',
    'Use classic layout': '使用经典布局',
    'Use modern layout': '使用现代布局',
    'New conversation': '新建对话',
    Examples: '示例',
    Capabilities: '可以做什么',
    Limitations: '边界说明',
    'Explain an API setup': '解释 API 配置步骤',
    'Compare live model pricing': '比较实时模型价格',
    'Draft an access request': '起草访问申请',
    'Live models and pricing': '实时模型与价格',
    'Step-by-step setup guidance': '按步骤指导配置',
    'Confirm sensitive actions yourself': '敏感操作由你亲自确认',
    'Permissions still apply': '权限规则仍然生效',
    'Never share secrets in chat': '不要在聊天中发送密钥',
    'Write actions need your confirmation': '写入操作需要你的确认',
    'Unable to load data': '无法加载数据',
    'Append-only ledger': '仅追加记账',
    'Ledger entries are limited to durable ledger events. Use Financial overview for reconciled revenue.':
      '账本明细仅包含已持久化的账本事件。请以财务概览的对账收入为准。',
    'Unable to load archive': '无法加载归档',
    'View L1 recommendation archive': '查看 L1 推荐信归档',
    'L1 recommendation archive': 'L1 推荐信归档',
    'No approved recommendation archive yet.': '暂无已批准的推荐信归档。',
    Approved: '已批准',
    Request: '申请',
    'Administrator replied': '管理员已回复',
    'AI recommendation (optional)': 'AI 推荐信（可选）',
    'Submit for administrator review': '提交管理员审核',
    'Waiting for an administrator': '等待管理员处理',
    'Unable to load your support tasks': '无法加载你的客服任务',
    'The administrator marked this request resolved.':
      '管理员已将此申请标记为已解决。',
    'Administrator note': '管理员意见',
    'User skills': '用户技能',
    'Security reviews': '安全巡检',
    'assistant.security_review': '助手安全巡检',
    'Security audit details': '安全审计详情',
    'Audit data is available to administrators only.': '审计数据仅管理员可见。',
    'Review results from deterministic rules and asynchronous AI audits. Prompt text, previews, matcher patterns, and credentials are never shown here.':
      '查看确定性规则和异步 AI 审计结果。此处不会显示提示词、预览、匹配模式或凭证。',
    'Protected groups': '受保护分组',
    'Only groups listed by an enabled rule are included. Rules do not apply globally.':
      '仅启用规则列出的分组会受到保护；规则不会全局生效。',
    'No groups are currently covered by enabled advanced security rules.':
      '当前没有分组受到已启用高级安全规则保护。',
    'Only explicitly listed groups are covered by advanced security rules; rules do not apply globally.':
      '只有明确列出的分组会受到高级安全规则保护；规则不会全局生效。',
    'No protected groups are published yet.': '暂未公布受保护分组。',
    'Deterministic rule': '确定性规则',
    'All categories': '全部分类',
    'All groups': '全部分组',
    'All decisions': '全部决策',
    'All sources': '全部来源',
    Violation: '违规',
    Clear: '通过',
    Reviews: '次审计',
    Abuse: '滥用',
    Occurred: '发生时间',
    'Review source': '审计来源',
    'No security audit events match the current filters.':
      '没有符合当前筛选条件的安全审计事件。',
    'Group warning': '分组警告',
    'Confirmation {{current}} of {{total}}': '第 {{current}}/{{total}} 次确认',
    'I understand, continue': '我已了解，继续',
  },
  'zh-TW': {
    'Runtime instances reporting from this deployment; slots on the same node are listed separately.':
      '此部署中正在回報的執行個體；同一節點上的不同槽位會分開列出。',
    'Clean up review history': '清理複盤紀錄',
    'Clean up automatic review history?': '清理自動複盤紀錄？',
    'This will permanently delete {{count}} completed or failed automatic review runs while keeping the latest {{keep}}. Active runs and security audit evidence will not be deleted.':
      '此操作將永久刪除 {{count}} 筆已完成或失敗的自動複盤執行紀錄，並保留最近 {{keep}} 筆。執行中的任務與安全稽核證據不會被刪除。',
    'No completed automatic review runs are eligible for cleanup.':
      '沒有可清理的已完成自動複盤紀錄。',
    'Automatic review history cleanup completed': '自動複盤紀錄清理完成',
    'Automatic review history changed. Review the refreshed preview and confirm again.':
      '自動複盤紀錄已變更。請查看更新後的預覽並重新確認。',
    'Failed to clean up automatic review history': '無法清理自動複盤紀錄',
    'Clear exhausted codes': '清理已用完優惠碼',
    'Unable to clear exhausted discount codes': '無法清理已用完優惠碼',
    'Deleted {{count}} exhausted discount codes':
      '已刪除 {{count}} 個已用完優惠碼',
    'Delete exhausted discount codes?': '刪除已用完優惠碼？',
    'This permanently removes every finite-use discount code whose usage limit has been reached. Partially used and unlimited codes are kept.':
      '此操作將永久刪除所有已達使用上限的有限次數優惠碼。部分使用與不限次數的優惠碼會保留。',
    'Delete exhausted codes': '刪除已用完優惠碼',
    'No exhausted discount codes to delete': '沒有需要刪除的已用完優惠碼',
    Guide: '接入指南',
    'Just one endpoint': '僅需一個介面',
    'Connect the world’s most popular models': '串連全球最熱門的模型',
    'Pay as you go, no time limits, fast chat, transparent details, no hidden fees, and online recharge for access to every model.':
      '按量計費、不限時間、极速對話、明細透明，無隱藏消費，線上充值後即可使用所有模型。',
    'One platform, many uses': '一個平台，多種用途',
    Statistics: '統計',
    Years: '年',
    Web: '前端',
    'Upstream returned no usage; no quota charged': '上游沒有返回用量，未扣費',
    'View model pricing': '查看模型價格',
    'Browse open-source work': '瀏覽開源任務',
    'Read the guide': '閱讀接入指南',
    'At a glance': '快速了解',
    'One endpoint': '一個介面',
    'OpenAI and Anthropic-compatible routes.':
      '相容 OpenAI 與 Anthropic 的介面。',
    'Clear pricing': '價格透明',
    'Choose the model and group before you spend.':
      '先選擇模型和分組，再開始使用。',
    'Human review': '人工審核',
    'Support and access requests stay auditable.': '支援與存取申請都可追溯。',
    'Use one clear API for your work, connect a client, or explore public open-source challenges.':
      '用一個清晰的 API 完成工作、連接客戶端，或探索公開的開源任務。',
    'Financial overview': '財務概覽',
    Expenses: '支出',
    Profit: '利潤',
    'Token economy': 'Token 經濟',
    'External expense': '外部支出',
    'Add expense': '新增支出',
    'Record expense': '記錄支出',
    'Past 7 days': '近 7 天',
    'Past 30 days': '近 30 天',
    'Past 90 days': '近 90 天',
    'Payment method': '付款方式',
    'No entries': '暫無記錄',
    Estimated: '估算',
    'Unpriced requests': '未定價請求',
    'View user': '查看使用者',
    'User spending': '使用者支出',
    'Include revenue': '計入收入',
    'Save settings': '儲存設定',
    Reversal: '沖銷',
    Reverse: '沖銷',
    'Profit margin': '利潤率',
    'Classic chat': '經典對話',
    'Modern chat': '現代對話',
    'Use classic layout': '使用經典版面',
    'Use modern layout': '使用現代版面',
    'New conversation': '新增對話',
    Examples: '範例',
    Capabilities: '可以做什麼',
    Limitations: '界線說明',
    'Explain an API setup': '解釋 API 設定步驟',
    'Compare live model pricing': '比較即時模型價格',
    'Draft an access request': '起草存取申請',
    'Live models and pricing': '即時模型與價格',
    'Step-by-step setup guidance': '按步驟引導設定',
    'Confirm sensitive actions yourself': '敏感操作由你親自確認',
    'Permissions still apply': '權限規則仍然有效',
    'Never share secrets in chat': '不要在對話中傳送密鑰',
    'Write actions need your confirmation': '寫入操作需要你的確認',
    'Unable to load data': '無法載入資料',
    'Append-only ledger': '僅追加記帳',
    'Ledger entries are limited to durable ledger events. Use Financial overview for reconciled revenue.':
      '帳本明細僅包含已持久化的帳本事件。請以財務概覽的對帳收入為準。',
    'Unable to load archive': '無法載入歸檔',
    'View L1 recommendation archive': '查看 L1 推薦信歸檔',
    'L1 recommendation archive': 'L1 推薦信歸檔',
    'No approved recommendation archive yet.': '尚無已核准的推薦信歸檔。',
    Approved: '已核准',
    Request: '申請',
    'Administrator replied': '管理員已回覆',
    'AI recommendation (optional)': 'AI 推薦信（選填）',
    'Submit for administrator review': '提交管理員審核',
    'Waiting for an administrator': '等待管理員處理',
    'Unable to load your support tasks': '無法載入你的客服任務',
    'The administrator marked this request resolved.':
      '管理員已將此申請標記為已解決。',
    'Administrator note': '管理員備註',
    'User skills': '使用者技能',
    'Security reviews': '安全巡檢',
    'assistant.security_review': '助手安全巡檢',
    'Security audit details': '安全稽核詳情',
    'Audit data is available to administrators only.': '稽核資料僅管理員可見。',
    'Review results from deterministic rules and asynchronous AI audits. Prompt text, previews, matcher patterns, and credentials are never shown here.':
      '檢視確定性規則與非同步 AI 稽核結果。此處不會顯示提示文字、預覽、比對模式或憑證。',
    'Protected groups': '受保護分組',
    'Only groups listed by an enabled rule are included. Rules do not apply globally.':
      '僅啟用規則列出的分組會受到保護；規則不會全域套用。',
    'No groups are currently covered by enabled advanced security rules.':
      '目前沒有分組受到已啟用的進階安全規則保護。',
    'Only explicitly listed groups are covered by advanced security rules; rules do not apply globally.':
      '只有明確列出的分組會受到進階安全規則保護；規則不會全域套用。',
    'No protected groups are published yet.': '尚未公布受保護分組。',
    'Deterministic rule': '確定性規則',
    'All categories': '全部分類',
    'All groups': '全部分組',
    'All decisions': '全部決策',
    'All sources': '全部來源',
    Violation: '違規',
    Clear: '通過',
    Reviews: '次稽核',
    Abuse: '濫用',
    Occurred: '發生時間',
    'Review source': '稽核來源',
    'No security audit events match the current filters.':
      '沒有符合目前篩選條件的安全稽核事件。',
  },
  fr: {
    'Runtime instances reporting from this deployment; slots on the same node are listed separately.':
      'Instances d’exécution signalées par ce déploiement ; les slots d’un même nœud sont affichés séparément.',
    'Clean up review history': 'Nettoyer l’historique des revues',
    'Clean up automatic review history?':
      'Nettoyer l’historique des revues automatiques ?',
    'This will permanently delete {{count}} completed or failed automatic review runs while keeping the latest {{keep}}. Active runs and security audit evidence will not be deleted.':
      'Cette action supprimera définitivement {{count}} exécutions de revue automatique terminées ou échouées, en conservant les {{keep}} plus récentes. Les exécutions actives et les preuves d’audit de sécurité ne seront pas supprimées.',
    'No completed automatic review runs are eligible for cleanup.':
      'Aucune exécution de revue automatique terminée ne peut être nettoyée.',
    'Automatic review history cleanup completed':
      'Nettoyage de l’historique des revues automatiques terminé',
    'Automatic review history changed. Review the refreshed preview and confirm again.':
      'L’historique des revues automatiques a changé. Vérifiez l’aperçu actualisé et confirmez à nouveau.',
    'Failed to clean up automatic review history':
      'Échec du nettoyage de l’historique des revues automatiques',
    'Clear exhausted codes': 'Nettoyer les codes épuisés',
    'Unable to clear exhausted discount codes':
      'Impossible de nettoyer les codes de réduction épuisés',
    'Deleted {{count}} exhausted discount codes':
      '{{count}} codes de réduction épuisés supprimés',
    'Delete exhausted discount codes?':
      'Supprimer les codes de réduction épuisés ?',
    'This permanently removes every finite-use discount code whose usage limit has been reached. Partially used and unlimited codes are kept.':
      'Cette action supprime définitivement tous les codes à usage limité ayant atteint leur limite. Les codes partiellement utilisés et illimités sont conservés.',
    'Delete exhausted codes': 'Supprimer les codes épuisés',
    'No exhausted discount codes to delete':
      'Aucun code de réduction épuisé à supprimer',
    Guide: 'Guide',
    'Just one endpoint': 'Un seul endpoint',
    'Connect the world’s most popular models':
      'Connectez les modèles les plus populaires',
    'Pay as you go, no time limits, fast chat, transparent details, no hidden fees, and online recharge for access to every model.':
      'Paiement à l’usage, sans limite de temps, chat rapide, détails transparents, aucun frais caché et recharge en ligne pour accéder à tous les modèles.',
    'One platform, many uses': 'Une plateforme, de nombreux usages',
    Statistics: 'Statistiques',
    Years: 'Ans',
    Web: 'Web',
    'Upstream returned no usage; no quota charged':
      'L’amont n’a renvoyé aucun usage ; aucun quota n’a été débité',
    'View model pricing': 'Voir les tarifs des modèles',
    'Browse open-source work': 'Parcourir les projets open source',
    'Read the guide': 'Lire le guide',
    'At a glance': 'En bref',
    'One endpoint': 'Un seul endpoint',
    'OpenAI and Anthropic-compatible routes.':
      'Routes compatibles avec OpenAI et Anthropic.',
    'Clear pricing': 'Tarifs clairs',
    'Choose the model and group before you spend.':
      'Choisissez le modèle et le groupe avant de dépenser.',
    'Human review': 'Relecture humaine',
    'Support and access requests stay auditable.':
      'Le support et les demandes d’accès restent auditables.',
    'Use one clear API for your work, connect a client, or explore public open-source challenges.':
      'Utilisez une API claire, connectez un client ou explorez des projets open source.',
    'Financial overview': 'Aperçu financier',
    Expenses: 'Dépenses',
    Profit: 'Bénéfice',
    'Token economy': 'Économie des tokens',
    'External expense': 'Dépense externe',
    'Add expense': 'Ajouter une dépense',
    'Record expense': 'Enregistrer la dépense',
    'Past 7 days': '7 derniers jours',
    'Past 30 days': '30 derniers jours',
    'Past 90 days': '90 derniers jours',
    'Payment method': 'Mode de paiement',
    'No entries': 'Aucun enregistrement',
    Estimated: 'Estimé',
    'Unpriced requests': 'Requêtes sans prix',
    'View user': 'Voir l’utilisateur',
    'User spending': 'Dépenses utilisateur',
    'Include revenue': 'Inclure dans les revenus',
    'Save settings': 'Enregistrer les paramètres',
    Reversal: 'Contrepassation',
    Reverse: 'Contrepasser',
    'Profit margin': 'Marge bénéficiaire',
    'Classic chat': 'Chat classique',
    'Modern chat': 'Chat moderne',
    'Use classic layout': 'Utiliser la mise en page classique',
    'Use modern layout': 'Utiliser la mise en page moderne',
    'New conversation': 'Nouvelle conversation',
    Examples: 'Exemples',
    Capabilities: 'Capacités',
    Limitations: 'Limites',
    'Explain an API setup': 'Expliquer une configuration API',
    'Compare live model pricing': 'Comparer les prix des modèles en direct',
    'Draft an access request': 'Rédiger une demande d’accès',
    'Live models and pricing': 'Modèles et tarifs en direct',
    'Step-by-step setup guidance': 'Guides de configuration étape par étape',
    'Confirm sensitive actions yourself':
      'Confirmer vous-même les actions sensibles',
    'Permissions still apply': 'Les permissions restent applicables',
    'Never share secrets in chat': 'Ne partagez jamais de secrets dans le chat',
    'Write actions need your confirmation':
      'Les actions d’écriture nécessitent votre confirmation',
    'Unable to load data': 'Impossible de charger les données',
    'Append-only ledger': 'Grand livre en ajout uniquement',
    'Ledger entries are limited to durable ledger events. Use Financial overview for reconciled revenue.':
      'Les écritures se limitent aux événements durables du grand livre. Utilisez la vue financière pour les revenus rapprochés.',
    'Unable to load archive': 'Impossible de charger les archives',
    'View L1 recommendation archive': 'Voir les archives de recommandations L1',
    'L1 recommendation archive': 'Archives de recommandations L1',
    'No approved recommendation archive yet.':
      'Aucune recommandation approuvée archivée.',
    Approved: 'Approuvée',
    Request: 'Demande',
    'Administrator replied': 'Réponse de l’administrateur',
    'AI recommendation (optional)': 'Recommandation IA (facultatif)',
    'Submit for administrator review':
      'Soumettre à l’examen de l’administrateur',
    'Waiting for an administrator': 'En attente d’un administrateur',
    'Unable to load your support tasks':
      'Impossible de charger vos tâches d’assistance',
    'The administrator marked this request resolved.':
      'L’administrateur a marqué cette demande comme résolue.',
    'Administrator note': 'Note de l’administrateur',
    'User skills': 'Compétences utilisateur',
    'Security reviews': 'Revues de sécurité',
    'assistant.security_review': 'Revue de sécurité de l’assistant',
    'Security audit details': 'Détails de l’audit de sécurité',
    'Audit data is available to administrators only.':
      'Les données d’audit sont réservées aux administrateurs.',
    'Review results from deterministic rules and asynchronous AI audits. Prompt text, previews, matcher patterns, and credentials are never shown here.':
      'Consultez les résultats des règles déterministes et des audits IA asynchrones. Les prompts, aperçus, motifs et identifiants ne sont jamais affichés ici.',
    'Protected groups': 'Groupes protégés',
    'Only groups listed by an enabled rule are included. Rules do not apply globally.':
      'Seuls les groupes listés par une règle active sont inclus ; les règles ne sont pas globales.',
    'No groups are currently covered by enabled advanced security rules.':
      'Aucun groupe n’est actuellement couvert par les règles de sécurité avancées actives.',
    'Only explicitly listed groups are covered by advanced security rules; rules do not apply globally.':
      'Seuls les groupes explicitement listés sont couverts par les règles avancées ; elles ne sont pas globales.',
    'No protected groups are published yet.': 'Aucun groupe protégé publié.',
    'Deterministic rule': 'Règle déterministe',
    'All categories': 'Toutes les catégories',
    'All groups': 'Tous les groupes',
    'All decisions': 'Toutes les décisions',
    'All sources': 'Toutes les sources',
    Violation: 'Violation',
    Clear: 'Aucun problème',
    Reviews: 'audits',
    Abuse: 'Abus',
    Occurred: 'Date',
    'Review source': 'Source de l’audit',
    'No security audit events match the current filters.':
      'Aucun événement d’audit de sécurité ne correspond aux filtres actuels.',
  },
  ja: {
    'Runtime instances reporting from this deployment; slots on the same node are listed separately.':
      'このデプロイから報告されるランタイムインスタンスです。同じノードのスロットは個別に表示されます。',
    'Clean up review history': 'レビュー履歴を整理',
    'Clean up automatic review history?': '自動レビュー履歴を整理しますか？',
    'This will permanently delete {{count}} completed or failed automatic review runs while keeping the latest {{keep}}. Active runs and security audit evidence will not be deleted.':
      '完了または失敗した自動レビュー実行 {{count}} 件を完全に削除し、最新の {{keep}} 件を保持します。実行中の処理とセキュリティ監査証跡は削除されません。',
    'No completed automatic review runs are eligible for cleanup.':
      '整理対象となる完了済みの自動レビュー実行はありません。',
    'Automatic review history cleanup completed':
      '自動レビュー履歴を整理しました',
    'Automatic review history changed. Review the refreshed preview and confirm again.':
      '自動レビュー履歴が変更されました。更新されたプレビューを確認し、もう一度確定してください。',
    'Failed to clean up automatic review history':
      '自動レビュー履歴を整理できませんでした',
    'Clear exhausted codes': '使用済みコードを整理',
    'Unable to clear exhausted discount codes':
      '使用済み割引コードを整理できません',
    'Deleted {{count}} exhausted discount codes':
      '使用済み割引コードを {{count}} 件削除しました',
    'Delete exhausted discount codes?': '使用済み割引コードを削除しますか？',
    'This permanently removes every finite-use discount code whose usage limit has been reached. Partially used and unlimited codes are kept.':
      '使用上限に達した有限回数の割引コードをすべて完全に削除します。一部使用済みおよび無制限のコードは保持されます。',
    'Delete exhausted codes': '使用済みコードを削除',
    'No exhausted discount codes to delete':
      '削除できる使用済み割引コードはありません',
    Guide: 'ガイド',
    'Just one endpoint': 'ひとつのエンドポイントだけ',
    'Connect the world’s most popular models':
      '世界で最も人気のあるモデルをひとつにつなぐ',
    'Pay as you go, no time limits, fast chat, transparent details, no hidden fees, and online recharge for access to every model.':
      '従量課金、時間制限なし、高速チャット、透明な明細、隠れた費用なし。オンラインチャージですべてのモデルを利用できます。',
    'One platform, many uses': 'ひとつのプラットフォーム、多彩な用途',
    Statistics: '統計',
    Years: '年',
    Web: 'Web',
    'Upstream returned no usage; no quota charged':
      '上流が使用量を返さなかったため、クォータは引かれていません',
    'View model pricing': 'モデル料金を見る',
    'Browse open-source work': 'オープンソースの仕事を見る',
    'Read the guide': 'ガイドを読む',
    'At a glance': '概要',
    'One endpoint': 'ひとつのエンドポイント',
    'OpenAI and Anthropic-compatible routes.':
      'OpenAI と Anthropic に対応したルート。',
    'Clear pricing': '明確な料金',
    'Choose the model and group before you spend.':
      '利用前にモデルとグループを選べます。',
    'Human review': '人による確認',
    'Support and access requests stay auditable.':
      'サポートとアクセス申請を監査可能に保ちます。',
    'Use one clear API for your work, connect a client, or explore public open-source challenges.':
      'ひとつの API で作業し、クライアントを接続し、公開オープンソースの課題を探せます。',
    'Financial overview': '財務概要',
    Expenses: '支出',
    Profit: '利益',
    'Token economy': 'Token 経済',
    'External expense': '外部支出',
    'Add expense': '支出を追加',
    'Record expense': '支出を記録',
    'Past 7 days': '過去 7 日間',
    'Past 30 days': '過去 30 日間',
    'Past 90 days': '過去 90 日間',
    'Payment method': '支払方法',
    'No entries': '記録なし',
    Estimated: '推定',
    'Unpriced requests': '価格未設定のリクエスト',
    'View user': 'ユーザーを表示',
    'User spending': 'ユーザー支出',
    'Include revenue': '収益に含める',
    'Save settings': '設定を保存',
    Reversal: '取消仕訳',
    Reverse: '取り消す',
    'Profit margin': '利益率',
    'Classic chat': 'クラシックチャット',
    'Modern chat': 'モダンチャット',
    'Use classic layout': 'クラシックレイアウトを使う',
    'Use modern layout': 'モダンレイアウトを使う',
    'New conversation': '新しい会話',
    Examples: '例',
    Capabilities: 'できること',
    Limitations: '制限事項',
    'Explain an API setup': 'API の設定を説明する',
    'Compare live model pricing': '現在のモデル価格を比較する',
    'Draft an access request': 'アクセス申請を下書きする',
    'Live models and pricing': 'ライブモデルと料金',
    'Step-by-step setup guidance': '手順に沿った設定案内',
    'Confirm sensitive actions yourself': '重要な操作は自分で確認する',
    'Permissions still apply': '権限ルールは適用されます',
    'Never share secrets in chat': 'チャットに秘密情報を送らない',
    'Write actions need your confirmation': '書き込み操作には確認が必要です',
    'Unable to load data': 'データを読み込めません',
    'Append-only ledger': '追記専用台帳',
    'Ledger entries are limited to durable ledger events. Use Financial overview for reconciled revenue.':
      '台帳明細には永続化された台帳イベントのみが含まれます。照合済み収益は財務概要で確認してください。',
    'Unable to load archive': 'アーカイブを読み込めません',
    'View L1 recommendation archive': 'L1 推薦文アーカイブを表示',
    'L1 recommendation archive': 'L1 推薦文アーカイブ',
    'No approved recommendation archive yet.':
      '承認済みの推薦文アーカイブはありません。',
    Approved: '承認済み',
    Request: '申請',
    'Administrator replied': '管理者からの返信',
    'AI recommendation (optional)': 'AI 推薦文（任意）',
    'Submit for administrator review': '管理者の審査に送信',
    'Waiting for an administrator': '管理者の対応待ち',
    'Unable to load your support tasks': 'サポートタスクを読み込めません',
    'The administrator marked this request resolved.':
      '管理者がこの申請を解決済みにしました。',
    'Administrator note': '管理者メモ',
    'User skills': 'ユーザースキル',
    'Security reviews': 'セキュリティレビュー',
    'assistant.security_review': 'アシスタントのセキュリティレビュー',
    'Security audit details': 'セキュリティ監査の詳細',
    'Audit data is available to administrators only.':
      '監査データは管理者のみ利用できます。',
    'Review results from deterministic rules and asynchronous AI audits. Prompt text, previews, matcher patterns, and credentials are never shown here.':
      '決定論的ルールと非同期 AI 監査の結果を確認します。プロンプト、プレビュー、照合パターン、認証情報は表示されません。',
    'Protected groups': '保護対象グループ',
    'Only groups listed by an enabled rule are included. Rules do not apply globally.':
      '有効なルールに記載されたグループだけが対象です。ルールは全体には適用されません。',
    'No groups are currently covered by enabled advanced security rules.':
      '現在、有効な高度なセキュリティルールの対象グループはありません。',
    'Only explicitly listed groups are covered by advanced security rules; rules do not apply globally.':
      '明示的に記載されたグループだけが高度なセキュリティルールの対象です。全体には適用されません。',
    'No protected groups are published yet.':
      '保護対象グループはまだ公開されていません。',
    'Deterministic rule': '決定論的ルール',
    'All categories': 'すべてのカテゴリ',
    'All groups': 'すべてのグループ',
    'All decisions': 'すべての判定',
    'All sources': 'すべてのソース',
    Violation: '違反',
    Clear: '問題なし',
    Reviews: '件の監査',
    Abuse: '悪用',
    Occurred: '発生日時',
    'Review source': '監査ソース',
    'No security audit events match the current filters.':
      '現在のフィルターに一致するセキュリティ監査イベントはありません。',
  },
  ru: {
    'Runtime instances reporting from this deployment; slots on the same node are listed separately.':
      'Экземпляры среды выполнения этого развёртывания; слоты одного узла отображаются отдельно.',
    'Clean up review history': 'Очистить историю проверок',
    'Clean up automatic review history?':
      'Очистить историю автоматических проверок?',
    'This will permanently delete {{count}} completed or failed automatic review runs while keeping the latest {{keep}}. Active runs and security audit evidence will not be deleted.':
      'Будет безвозвратно удалено {{count}} завершённых или завершившихся ошибкой запусков автоматической проверки; последние {{keep}} будут сохранены. Активные запуски и данные аудита безопасности не будут удалены.',
    'No completed automatic review runs are eligible for cleanup.':
      'Нет завершённых запусков автоматической проверки, доступных для очистки.',
    'Automatic review history cleanup completed':
      'История автоматических проверок очищена',
    'Automatic review history changed. Review the refreshed preview and confirm again.':
      'История автоматических проверок изменилась. Проверьте обновлённый предварительный просмотр и подтвердите действие снова.',
    'Failed to clean up automatic review history':
      'Не удалось очистить историю автоматических проверок',
    'Clear exhausted codes': 'Очистить исчерпанные коды',
    'Unable to clear exhausted discount codes':
      'Не удалось очистить исчерпанные промокоды',
    'Deleted {{count}} exhausted discount codes':
      'Удалено исчерпанных промокодов: {{count}}',
    'Delete exhausted discount codes?': 'Удалить исчерпанные промокоды?',
    'This permanently removes every finite-use discount code whose usage limit has been reached. Partially used and unlimited codes are kept.':
      'Все промокоды с ограниченным числом использований, достигшие лимита, будут удалены безвозвратно. Частично использованные и безлимитные коды сохранятся.',
    'Delete exhausted codes': 'Удалить исчерпанные коды',
    'No exhausted discount codes to delete':
      'Нет исчерпанных промокодов для удаления',
    Guide: 'Руководство',
    'Just one endpoint': 'Всего один endpoint',
    'Connect the world’s most popular models':
      'Доступ к самым популярным моделям мира',
    'Pay as you go, no time limits, fast chat, transparent details, no hidden fees, and online recharge for access to every model.':
      'Оплата по использованию, без ограничений по времени, быстрый чат, прозрачная детализация, никаких скрытых платежей и онлайн-пополнение для доступа ко всем моделям.',
    'One platform, many uses': 'Одна платформа — множество задач',
    Statistics: 'Статистика',
    Years: 'Лет',
    Web: 'Веб',
    'Upstream returned no usage; no quota charged':
      'Провайдер не вернул данные об использовании; квота не списана',
    'View model pricing': 'Посмотреть цены моделей',
    'Browse open-source work': 'Открытые проекты',
    'Read the guide': 'Открыть руководство',
    'At a glance': 'Коротко',
    'One endpoint': 'Одна точка доступа',
    'OpenAI and Anthropic-compatible routes.':
      'Маршруты, совместимые с OpenAI и Anthropic.',
    'Clear pricing': 'Понятные цены',
    'Choose the model and group before you spend.':
      'Выберите модель и группу до начала расходов.',
    'Human review': 'Проверка человеком',
    'Support and access requests stay auditable.':
      'Поддержка и запросы доступа остаются проверяемыми.',
    'Use one clear API for your work, connect a client, or explore public open-source challenges.':
      'Используйте единый API, подключайте клиент или изучайте открытые проекты.',
    'Financial overview': 'Финансовый обзор',
    Expenses: 'Расходы',
    Profit: 'Прибыль',
    'Token economy': 'Экономика токенов',
    'External expense': 'Внешний расход',
    'Add expense': 'Добавить расход',
    'Record expense': 'Записать расход',
    'Past 7 days': 'Последние 7 дней',
    'Past 30 days': 'Последние 30 дней',
    'Past 90 days': 'Последние 90 дней',
    'Payment method': 'Способ оплаты',
    'No entries': 'Записей нет',
    Estimated: 'Оценка',
    'Unpriced requests': 'Запросы без цены',
    'View user': 'Открыть пользователя',
    'User spending': 'Расходы пользователя',
    'Include revenue': 'Учитывать в доходе',
    'Save settings': 'Сохранить настройки',
    Reversal: 'Сторно',
    Reverse: 'Сторнировать',
    'Profit margin': 'Маржа',
    'Classic chat': 'Классический чат',
    'Modern chat': 'Современный чат',
    'Use classic layout': 'Использовать классический вид',
    'Use modern layout': 'Использовать современный вид',
    'New conversation': 'Новый разговор',
    Examples: 'Примеры',
    Capabilities: 'Возможности',
    Limitations: 'Ограничения',
    'Explain an API setup': 'Объяснить настройку API',
    'Compare live model pricing': 'Сравнить текущие цены моделей',
    'Draft an access request': 'Подготовить заявку на доступ',
    'Live models and pricing': 'Актуальные модели и цены',
    'Step-by-step setup guidance': 'Пошаговая настройка',
    'Confirm sensitive actions yourself': 'Подтверждайте важные действия сами',
    'Permissions still apply': 'Ограничения доступа сохраняются',
    'Never share secrets in chat': 'Не отправляйте секреты в чат',
    'Write actions need your confirmation':
      'Для изменений нужно ваше подтверждение',
    'Unable to load data': 'Не удалось загрузить данные',
    'Append-only ledger': 'Журнал только для добавления',
    'Ledger entries are limited to durable ledger events. Use Financial overview for reconciled revenue.':
      'Записи ограничены устойчивыми событиями журнала. Для сверенных доходов используйте финансовый обзор.',
    'Unable to load archive': 'Не удалось загрузить архив',
    'View L1 recommendation archive': 'Открыть архив рекомендаций L1',
    'L1 recommendation archive': 'Архив рекомендаций L1',
    'No approved recommendation archive yet.':
      'Архивов одобренных рекомендаций пока нет.',
    Approved: 'Одобрено',
    Request: 'Заявка',
    'Administrator replied': 'Ответ администратора',
    'AI recommendation (optional)': 'Рекомендация ИИ (необязательно)',
    'Submit for administrator review': 'Отправить администратору на проверку',
    'Waiting for an administrator': 'Ожидание администратора',
    'Unable to load your support tasks':
      'Не удалось загрузить задачи поддержки',
    'The administrator marked this request resolved.':
      'Администратор отметил этот запрос как решённый.',
    'Administrator note': 'Заметка администратора',
    'User skills': 'Навыки пользователя',
    'Security reviews': 'Проверки безопасности',
    'assistant.security_review': 'Проверка безопасности ассистента',
    'Security audit details': 'Подробности аудита безопасности',
    'Audit data is available to administrators only.':
      'Данные аудита доступны только администраторам.',
    'Review results from deterministic rules and asynchronous AI audits. Prompt text, previews, matcher patterns, and credentials are never shown here.':
      'Результаты детерминированных правил и асинхронных проверок ИИ. Промпты, превью, шаблоны и учётные данные здесь не отображаются.',
    'Protected groups': 'Защищённые группы',
    'Only groups listed by an enabled rule are included. Rules do not apply globally.':
      'Включаются только группы из активных правил; правила не применяются глобально.',
    'No groups are currently covered by enabled advanced security rules.':
      'Сейчас ни одна группа не покрыта активными расширенными правилами безопасности.',
    'Only explicitly listed groups are covered by advanced security rules; rules do not apply globally.':
      'Расширенные правила применяются только к явно указанным группам и не глобальны.',
    'No protected groups are published yet.':
      'Защищённые группы пока не опубликованы.',
    'Deterministic rule': 'Детерминированное правило',
    'All categories': 'Все категории',
    'All groups': 'Все группы',
    'All decisions': 'Все решения',
    'All sources': 'Все источники',
    Violation: 'Нарушение',
    Clear: 'Нарушений нет',
    Reviews: 'проверок',
    Abuse: 'Злоупотребление',
    Occurred: 'Время',
    'Review source': 'Источник проверки',
    'No security audit events match the current filters.':
      'Нет событий аудита безопасности, соответствующих текущим фильтрам.',
  },
  vi: {
    'Runtime instances reporting from this deployment; slots on the same node are listed separately.':
      'Các phiên bản runtime đang báo cáo từ bản triển khai này; các slot trên cùng một nút được liệt kê riêng.',
    'Clean up review history': 'Dọn lịch sử đánh giá',
    'Clean up automatic review history?': 'Dọn lịch sử đánh giá tự động?',
    'This will permanently delete {{count}} completed or failed automatic review runs while keeping the latest {{keep}}. Active runs and security audit evidence will not be deleted.':
      'Thao tác này sẽ xóa vĩnh viễn {{count}} lượt đánh giá tự động đã hoàn tất hoặc thất bại, đồng thời giữ lại {{keep}} lượt gần nhất. Các lượt đang chạy và bằng chứng kiểm tra bảo mật sẽ không bị xóa.',
    'No completed automatic review runs are eligible for cleanup.':
      'Không có lượt đánh giá tự động đã hoàn tất nào đủ điều kiện dọn dẹp.',
    'Automatic review history cleanup completed':
      'Đã dọn xong lịch sử đánh giá tự động',
    'Automatic review history changed. Review the refreshed preview and confirm again.':
      'Lịch sử đánh giá tự động đã thay đổi. Hãy xem bản xem trước đã cập nhật và xác nhận lại.',
    'Failed to clean up automatic review history':
      'Không thể dọn lịch sử đánh giá tự động',
    'Clear exhausted codes': 'Dọn mã đã dùng hết',
    'Unable to clear exhausted discount codes':
      'Không thể dọn các mã giảm giá đã dùng hết',
    'Deleted {{count}} exhausted discount codes':
      'Đã xóa {{count}} mã giảm giá đã dùng hết',
    'Delete exhausted discount codes?': 'Xóa các mã giảm giá đã dùng hết?',
    'This permanently removes every finite-use discount code whose usage limit has been reached. Partially used and unlimited codes are kept.':
      'Thao tác này xóa vĩnh viễn mọi mã có số lượt dùng hữu hạn đã đạt giới hạn. Mã mới dùng một phần và mã không giới hạn vẫn được giữ lại.',
    'Delete exhausted codes': 'Xóa mã đã dùng hết',
    'No exhausted discount codes to delete':
      'Không có mã giảm giá đã dùng hết để xóa',
    Guide: 'Hướng dẫn',
    'Just one endpoint': 'Chỉ một endpoint',
    'Connect the world’s most popular models':
      'Kết nối các mô hình phổ biến nhất thế giới',
    'Pay as you go, no time limits, fast chat, transparent details, no hidden fees, and online recharge for access to every model.':
      'Tính phí theo mức sử dụng, không giới hạn thời gian, trò chuyện nhanh, chi tiết minh bạch, không phí ẩn và nạp tiền trực tuyến để dùng mọi mô hình.',
    'One platform, many uses': 'Một nền tảng, nhiều mục đích sử dụng',
    Statistics: 'Thống kê',
    Years: 'Năm',
    Web: 'Web',
    'Upstream returned no usage; no quota charged':
      'Upstream không trả về mức sử dụng; không trừ quota',
    'View model pricing': 'Xem giá mô hình',
    'Browse open-source work': 'Xem dự án mã nguồn mở',
    'Read the guide': 'Đọc hướng dẫn',
    'At a glance': 'Tổng quan nhanh',
    'One endpoint': 'Một endpoint',
    'OpenAI and Anthropic-compatible routes.':
      'Các route tương thích với OpenAI và Anthropic.',
    'Clear pricing': 'Giá rõ ràng',
    'Choose the model and group before you spend.':
      'Chọn model và nhóm trước khi phát sinh chi phí.',
    'Human review': 'Đánh giá thủ công',
    'Support and access requests stay auditable.':
      'Hỗ trợ và yêu cầu truy cập luôn có thể kiểm tra.',
    'Use one clear API for your work, connect a client, or explore public open-source challenges.':
      'Dùng một API rõ ràng, kết nối client hoặc khám phá dự án mã nguồn mở.',
    'Financial overview': 'Tổng quan tài chính',
    Expenses: 'Chi phí',
    Profit: 'Lợi nhuận',
    'Token economy': 'Nền kinh tế token',
    'External expense': 'Chi phí bên ngoài',
    'Add expense': 'Thêm chi phí',
    'Record expense': 'Ghi nhận chi phí',
    'Past 7 days': '7 ngày qua',
    'Past 30 days': '30 ngày qua',
    'Past 90 days': '90 ngày qua',
    'Payment method': 'Phương thức thanh toán',
    'No entries': 'Chưa có bản ghi',
    Estimated: 'Ước tính',
    'Unpriced requests': 'Yêu cầu chưa có giá',
    'View user': 'Xem người dùng',
    'User spending': 'Chi tiêu của người dùng',
    'Include revenue': 'Tính vào doanh thu',
    'Save settings': 'Lưu cài đặt',
    Reversal: 'Đảo bút toán',
    Reverse: 'Đảo bút toán',
    'Profit margin': 'Biên lợi nhuận',
    'Classic chat': 'Trò chuyện cổ điển',
    'Modern chat': 'Trò chuyện hiện đại',
    'Use classic layout': 'Dùng bố cục cổ điển',
    'Use modern layout': 'Dùng bố cục hiện đại',
    'New conversation': 'Cuộc trò chuyện mới',
    Examples: 'Ví dụ',
    Capabilities: 'Có thể làm gì',
    Limitations: 'Giới hạn',
    'Explain an API setup': 'Giải thích cách cấu hình API',
    'Compare live model pricing': 'So sánh giá model hiện tại',
    'Draft an access request': 'Soạn yêu cầu cấp quyền',
    'Live models and pricing': 'Model và giá theo thời gian thực',
    'Step-by-step setup guidance': 'Hướng dẫn cài đặt từng bước',
    'Confirm sensitive actions yourself': 'Bạn tự xác nhận thao tác nhạy cảm',
    'Permissions still apply': 'Quyền truy cập vẫn được áp dụng',
    'Never share secrets in chat': 'Không gửi bí mật trong cuộc trò chuyện',
    'Write actions need your confirmation': 'Thao tác ghi cần bạn xác nhận',
    'Unable to load data': 'Không thể tải dữ liệu',
    'Append-only ledger': 'Sổ cái chỉ được ghi thêm',
    'Ledger entries are limited to durable ledger events. Use Financial overview for reconciled revenue.':
      'Các mục chỉ gồm sự kiện sổ cái đã được lưu bền vững. Hãy dùng Tổng quan tài chính cho doanh thu đã đối soát.',
    'Unable to load archive': 'Không thể tải kho lưu trữ',
    'View L1 recommendation archive': 'Xem kho lưu trữ đề xuất L1',
    'L1 recommendation archive': 'Kho lưu trữ đề xuất L1',
    'No approved recommendation archive yet.':
      'Chưa có đề xuất nào được phê duyệt trong kho lưu trữ.',
    Approved: 'Đã phê duyệt',
    Request: 'Yêu cầu',
    'Administrator replied': 'Quản trị viên đã phản hồi',
    'AI recommendation (optional)': 'Đề xuất của AI (không bắt buộc)',
    'Submit for administrator review': 'Gửi để quản trị viên xem xét',
    'Waiting for an administrator': 'Đang chờ quản trị viên',
    'Unable to load your support tasks': 'Không thể tải tác vụ hỗ trợ',
    'The administrator marked this request resolved.':
      'Quản trị viên đã đánh dấu yêu cầu này là đã giải quyết.',
    'Administrator note': 'Ghi chú của quản trị viên',
    'User skills': 'Kỹ năng người dùng',
    'Security reviews': 'Đánh giá bảo mật',
    'assistant.security_review': 'Đánh giá bảo mật của trợ lý',
    'Security audit details': 'Chi tiết kiểm tra bảo mật',
    'Audit data is available to administrators only.':
      'Dữ liệu kiểm tra chỉ dành cho quản trị viên.',
    'Review results from deterministic rules and asynchronous AI audits. Prompt text, previews, matcher patterns, and credentials are never shown here.':
      'Xem kết quả từ quy tắc xác định và kiểm tra AI không đồng bộ. Nội dung prompt, bản xem trước, mẫu khớp và thông tin xác thực không được hiển thị.',
    'Protected groups': 'Nhóm được bảo vệ',
    'Only groups listed by an enabled rule are included. Rules do not apply globally.':
      'Chỉ các nhóm được liệt kê trong quy tắc đang bật mới được áp dụng; quy tắc không áp dụng toàn cục.',
    'No groups are currently covered by enabled advanced security rules.':
      'Hiện chưa có nhóm nào được các quy tắc bảo mật nâng cao đang bật bảo vệ.',
    'Only explicitly listed groups are covered by advanced security rules; rules do not apply globally.':
      'Chỉ các nhóm được nêu rõ mới được quy tắc bảo mật nâng cao bảo vệ; quy tắc không áp dụng toàn cục.',
    'No protected groups are published yet.': 'Chưa công bố nhóm được bảo vệ.',
    'Deterministic rule': 'Quy tắc xác định',
    'All categories': 'Tất cả danh mục',
    'All groups': 'Tất cả nhóm',
    'All decisions': 'Tất cả quyết định',
    'All sources': 'Tất cả nguồn',
    Violation: 'Vi phạm',
    Clear: 'Không vi phạm',
    Reviews: 'lượt kiểm tra',
    Abuse: 'Lạm dụng',
    Occurred: 'Thời điểm',
    'Review source': 'Nguồn kiểm tra',
    'No security audit events match the current filters.':
      'Không có sự kiện kiểm tra bảo mật phù hợp với bộ lọc hiện tại.',
  },
}

const drawingTranslations = {
  en: {
    'Drawing studio': 'Drawing studio',
    'Create images through the same safe, group-aware relay used by the API.':
      'Create images through the same safe, group-aware relay used by the API.',
    'Describe an image': 'Describe an image',
    'Describe what you want to see...': 'Describe what you want to see...',
    'Routing group': 'Routing group',
    'The assistant uses this group and automatically chooses an enabled model from its live catalog.':
      'The assistant uses this group and automatically chooses an enabled model from its live catalog.',
    'Billing follows the selected group configuration.':
      'Billing follows the selected group configuration.',
    'Image model': 'Image model',
    'Size (optional)': 'Size (optional)',
    'Quality (optional)': 'Quality (optional)',
    'Generate image': 'Generate image',
    'Generating...': 'Generating...',
    'Your generated images will appear here.':
      'Your generated images will appear here.',
    'Image catalog unavailable': 'Image catalog unavailable',
    'No image-capable model and routing group is currently available.':
      'No image-capable model and routing group is currently available.',
    'Unable to generate the image': 'Unable to generate the image',
    'No images were returned': 'No images were returned',
    'Ready to generate an image': 'Ready to generate an image',
    'Review the prompt and routing choice before generating.':
      'Review the prompt and routing choice before generating.',
    Prompt: 'Prompt',
    Images: 'Images',
    'Image generated': 'Image generated',
    'The confirmation was consumed or the image request failed. Ask the assistant to prepare it again.':
      'The confirmation was consumed or the image request failed. Ask the assistant to prepare it again.',
  },
  zh: {
    'Drawing studio': '绘图工作台',
    'Create images through the same safe, group-aware relay used by the API.':
      '通过与 API 相同的安全分组路由创建图片。',
    'Describe an image': '描述图片',
    'Describe what you want to see...': '描述你想看到的内容……',
    'Routing group': '路由分组',
    'The assistant uses this group and automatically chooses an enabled model from its live catalog.':
      '助手会使用此分组，并从实时目录中自动选择已启用的模型。',
    'Billing follows the selected group configuration.':
      '费用按所选分组配置结算。',
    'Image model': '图片模型',
    'Size (optional)': '尺寸（可选）',
    'Quality (optional)': '质量（可选）',
    'Generate image': '生成图片',
    'Generating...': '生成中……',
    'Your generated images will appear here.': '生成的图片会显示在这里。',
    'Image catalog unavailable': '图片目录不可用',
    'No image-capable model and routing group is currently available.':
      '当前没有可用的图片模型和路由分组。',
    'Unable to generate the image': '无法生成图片',
    'No images were returned': '没有返回图片',
    'Ready to generate an image': '已准备生成图片',
    'Review the prompt and routing choice before generating.':
      '请在生成前检查提示词和路由选择。',
    Prompt: '提示词',
    Images: '图片数量',
    'Image generated': '图片已生成',
    'The confirmation was consumed or the image request failed. Ask the assistant to prepare it again.':
      '确认已使用或图片请求失败，请让助手重新准备。',
  },
  'zh-TW': {
    'Drawing studio': '繪圖工作台',
    'Create images through the same safe, group-aware relay used by the API.':
      '透過與 API 相同的安全分組路由建立圖片。',
    'Describe an image': '描述圖片',
    'Describe what you want to see...': '描述你想看到的內容……',
    'Routing group': '路由分組',
    'The assistant uses this group and automatically chooses an enabled model from its live catalog.':
      '助手會使用此分組，並從即時目錄中自動選擇已啟用的模型。',
    'Billing follows the selected group configuration.':
      '費用依所選分組設定結算。',
    'Image model': '圖片模型',
    'Size (optional)': '尺寸（選填）',
    'Quality (optional)': '品質（選填）',
    'Generate image': '產生圖片',
    'Generating...': '產生中……',
    'Your generated images will appear here.': '產生的圖片會顯示在這裡。',
    'Image catalog unavailable': '圖片目錄無法使用',
    'No image-capable model and routing group is currently available.':
      '目前沒有可用的圖片模型與路由分組。',
    'Unable to generate the image': '無法產生圖片',
    'No images were returned': '沒有回傳圖片',
    'Ready to generate an image': '已準備產生圖片',
    'Review the prompt and routing choice before generating.':
      '產生前請檢查提示詞與路由選擇。',
    Prompt: '提示詞',
    Images: '圖片數量',
    'Image generated': '圖片已產生',
    'The confirmation was consumed or the image request failed. Ask the assistant to prepare it again.':
      '確認已使用或圖片請求失敗，請讓助手重新準備。',
  },
  fr: {
    'Drawing studio': 'Atelier de dessin',
    'Create images through the same safe, group-aware relay used by the API.':
      'Créez des images via le même relais sécurisé et sensible aux groupes que l’API.',
    'Describe an image': 'Décrire une image',
    'Describe what you want to see...': 'Décrivez ce que vous voulez voir…',
    'Routing group': 'Groupe de routage',
    'The assistant uses this group and automatically chooses an enabled model from its live catalog.':
      'L’assistant utilise ce groupe et choisit automatiquement un modèle activé dans le catalogue en temps réel.',
    'Billing follows the selected group configuration.':
      'La facturation suit la configuration du groupe choisi.',
    'Image model': 'Modèle d’image',
    'Size (optional)': 'Taille (facultatif)',
    'Quality (optional)': 'Qualité (facultatif)',
    'Generate image': 'Générer l’image',
    'Generating...': 'Génération…',
    'Your generated images will appear here.': 'Vos images apparaîtront ici.',
    'Image catalog unavailable': 'Catalogue d’images indisponible',
    'No image-capable model and routing group is currently available.':
      'Aucun modèle d’image ni groupe de routage n’est disponible.',
    'Unable to generate the image': 'Impossible de générer l’image',
    'No images were returned': 'Aucune image reçue',
    'Ready to generate an image': 'Image prête à être générée',
    'Review the prompt and routing choice before generating.':
      'Vérifiez le prompt et le routage avant de générer.',
    Prompt: 'Prompt',
    Images: 'Images',
    'Image generated': 'Image générée',
    'The confirmation was consumed or the image request failed. Ask the assistant to prepare it again.':
      'La confirmation a été utilisée ou la demande a échoué. Demandez à l’assistant de la préparer à nouveau.',
  },
  ja: {
    'Drawing studio': '画像スタジオ',
    'Create images through the same safe, group-aware relay used by the API.':
      'API と同じ安全なグループ対応リレーで画像を作成します。',
    'Describe an image': '画像を説明',
    'Describe what you want to see...': '見たいものを説明してください…',
    'Routing group': 'ルーティンググループ',
    'The assistant uses this group and automatically chooses an enabled model from its live catalog.':
      'アシスタントはこのグループを使用し、リアルタイムのカタログから有効なモデルを自動選択します。',
    'Billing follows the selected group configuration.':
      '料金は選択したグループ設定に従います。',
    'Image model': '画像モデル',
    'Size (optional)': 'サイズ（任意）',
    'Quality (optional)': '品質（任意）',
    'Generate image': '画像を生成',
    'Generating...': '生成中…',
    'Your generated images will appear here.':
      '生成した画像がここに表示されます。',
    'Image catalog unavailable': '画像カタログを利用できません',
    'No image-capable model and routing group is currently available.':
      '利用可能な画像モデルとルーティンググループがありません。',
    'Unable to generate the image': '画像を生成できません',
    'No images were returned': '画像が返されませんでした',
    'Ready to generate an image': '画像を生成する準備ができました',
    'Review the prompt and routing choice before generating.':
      '生成前にプロンプトとルーティングを確認してください。',
    Prompt: 'プロンプト',
    Images: '画像数',
    'Image generated': '画像を生成しました',
    'The confirmation was consumed or the image request failed. Ask the assistant to prepare it again.':
      '確認が使用済みか画像リクエストに失敗しました。アシスタントに再準備を依頼してください。',
  },
  ru: {
    'Drawing studio': 'Студия изображений',
    'Create images through the same safe, group-aware relay used by the API.':
      'Создавайте изображения через тот же безопасный групповой релей, что и API.',
    'Describe an image': 'Опишите изображение',
    'Describe what you want to see...': 'Опишите, что хотите увидеть…',
    'Routing group': 'Группа маршрутизации',
    'The assistant uses this group and automatically chooses an enabled model from its live catalog.':
      'Ассистент использует эту группу и автоматически выбирает включённую модель из актуального каталога.',
    'Billing follows the selected group configuration.':
      'Расчёт выполняется по настройкам выбранной группы.',
    'Image model': 'Модель изображений',
    'Size (optional)': 'Размер (необязательно)',
    'Quality (optional)': 'Качество (необязательно)',
    'Generate image': 'Создать изображение',
    'Generating...': 'Создание…',
    'Your generated images will appear here.':
      'Созданные изображения появятся здесь.',
    'Image catalog unavailable': 'Каталог изображений недоступен',
    'No image-capable model and routing group is currently available.':
      'Нет доступной модели изображений и группы маршрутизации.',
    'Unable to generate the image': 'Не удалось создать изображение',
    'No images were returned': 'Изображения не получены',
    'Ready to generate an image': 'Изображение готово к созданию',
    'Review the prompt and routing choice before generating.':
      'Проверьте запрос и маршрут перед созданием.',
    Prompt: 'Запрос',
    Images: 'Изображения',
    'Image generated': 'Изображение создано',
    'The confirmation was consumed or the image request failed. Ask the assistant to prepare it again.':
      'Подтверждение использовано или запрос завершился ошибкой. Попросите ассистента подготовить его снова.',
  },
  vi: {
    'Drawing studio': 'Xưởng tạo ảnh',
    'Create images through the same safe, group-aware relay used by the API.':
      'Tạo ảnh qua cùng relay an toàn, hỗ trợ nhóm như API.',
    'Describe an image': 'Mô tả hình ảnh',
    'Describe what you want to see...': 'Mô tả điều bạn muốn thấy…',
    'Routing group': 'Nhóm định tuyến',
    'The assistant uses this group and automatically chooses an enabled model from its live catalog.':
      'Trợ lý dùng nhóm này và tự động chọn một model đang bật từ danh mục trực tiếp.',
    'Billing follows the selected group configuration.':
      'Chi phí tuân theo cấu hình nhóm đã chọn.',
    'Image model': 'Model hình ảnh',
    'Size (optional)': 'Kích thước (tuỳ chọn)',
    'Quality (optional)': 'Chất lượng (tuỳ chọn)',
    'Generate image': 'Tạo hình ảnh',
    'Generating...': 'Đang tạo…',
    'Your generated images will appear here.':
      'Ảnh được tạo sẽ xuất hiện ở đây.',
    'Image catalog unavailable': 'Danh mục hình ảnh không khả dụng',
    'No image-capable model and routing group is currently available.':
      'Hiện không có model hình ảnh và nhóm định tuyến khả dụng.',
    'Unable to generate the image': 'Không thể tạo hình ảnh',
    'No images were returned': 'Không có hình ảnh được trả về',
    'Ready to generate an image': 'Đã sẵn sàng tạo hình ảnh',
    'Review the prompt and routing choice before generating.':
      'Hãy kiểm tra prompt và lựa chọn định tuyến trước khi tạo.',
    Prompt: 'Prompt',
    Images: 'Số ảnh',
    'Image generated': 'Đã tạo hình ảnh',
    'The confirmation was consumed or the image request failed. Ask the assistant to prepare it again.':
      'Xác nhận đã được dùng hoặc yêu cầu tạo ảnh thất bại. Hãy yêu cầu trợ lý chuẩn bị lại.',
  },
}

for (const [locale, translations] of Object.entries(drawingTranslations)) {
  Object.assign(newKeys[locale], translations)
}

const todoTranslations = {
  en: {
    All: 'All',
    'Challenge reviews': 'Challenge reviews',
    'Bounty notifications': 'Bounty notifications',
    'Developer access': 'Developer access',
    'Account actions': 'Account actions',
    'Security incidents': 'Security incidents',
    'Mark all as read': 'Mark all as read',
    'Failed to load to-dos': 'Failed to load to-dos',
    Loading: 'Loading',
    Notification: 'Notification',
    'Submitted challenge work and account requests will appear here.':
      'Submitted challenge work and account requests will appear here.',
    'No pending to-dos': 'No pending to-dos',
    'Assistant support tasks': 'Assistant support tasks',
    'Pending work': 'Pending work',
    'Resolved history': 'Resolved history',
    'Insights and AI cost': 'Insights and AI cost',
    'support tasks waiting for review': 'support tasks waiting for review',
    'No pending support tasks.': 'No pending support tasks.',
    'Processing note': 'Processing note',
    'Completed at': 'Completed at',
    'Privacy-minimized request': 'Privacy-minimized request',
    'No request details provided.': 'No request details provided.',
    'Complete support task': 'Complete support task',
    'Completing...': 'Completing...',
    'Complete task': 'Complete task',
    Refreshing: 'Refreshing',
    'Action required': 'Action required',
    'Assistant support history and insights':
      'Assistant support history and insights',
    'Unable to load intent insights': 'Unable to load intent insights',
    'Unable to load profile insights': 'Unable to load profile insights',
    'Unable to load first-question insights':
      'Unable to load first-question insights',
    'No first-question data yet': 'No first-question data yet',
    'Top first questions': 'Top first questions',
    'Privacy-minimized real-user first questions counted from the first turn in the last 30 days.':
      'Privacy-minimized real-user first questions counted from the first turn in the last 30 days.',
    'Unable to load AI usage and cost': 'Unable to load AI usage and cost',
    'No recent usage': 'No recent usage',
    'Remaining quota units': 'Remaining quota units',
    'Turn explicit human-support requests into clear next actions.':
      'Turn explicit human-support requests into clear next actions.',
    'AI usage and cost': 'AI usage and cost',
    'All clear': 'All clear',
    'Intent signals': 'Intent signals',
    'No resolved support tasks.': 'No resolved support tasks.',
    'Pending support tasks are unavailable.':
      'Pending support tasks are unavailable.',
    'Privacy-minimized assistant intent counts for the last 30 days.':
      'Privacy-minimized assistant intent counts for the last 30 days.',
    'Privacy-minimized profile signals for the last 30 days.':
      'Privacy-minimized profile signals for the last 30 days.',
    'Resolved support history is unavailable.':
      'Resolved support history is unavailable.',
    'Support task completed': 'Support task completed',
    'Unable to complete support task': 'Unable to complete support task',
    'Unable to load assistant support tasks':
      'Unable to load assistant support tasks',
  },
  zh: {
    All: '全部',
    'Challenge reviews': '挑战审核',
    'Bounty notifications': '悬赏通知',
    'Developer access': '开发者访问',
    'Account actions': '账号操作',
    'Security incidents': '安全事件',
    'Mark all as read': '全部标为已读',
    'Failed to load to-dos': '待办加载失败',
    Loading: '加载中',
    Notification: '通知',
    'Submitted challenge work and account requests will appear here.':
      '已提交的挑战成果和账号申请会显示在这里。',
    'No pending to-dos': '暂无待办',
    'Assistant support tasks': 'AI 客服任务',
    'Pending work': '待处理',
    'Resolved history': '已处理记录',
    'Insights and AI cost': '洞察与 AI 成本',
    'support tasks waiting for review': '个客服任务等待审核',
    'No pending support tasks.': '暂无待处理的客服任务。',
    'Processing note': '处理备注',
    'Completed at': '完成于',
    'Privacy-minimized request': '已做隐私最小化的请求',
    'No request details provided.': '未提供请求详情。',
    'Complete support task': '完成客服任务',
    'Completing...': '完成中...',
    'Complete task': '完成任务',
    Refreshing: '刷新中',
    'Action required': '需要处理',
    'Assistant support history and insights': 'AI 客服历史与洞察',
    'Unable to load intent insights': '无法加载意图洞察',
    'Unable to load profile insights': '无法加载画像洞察',
    'Unable to load first-question insights': '无法加载首轮提问洞察',
    'No first-question data yet': '暂无首轮提问数据',
    'Top first questions': '首轮提问前十',
    'Privacy-minimized real-user first questions counted from the first turn in the last 30 days.':
      '最近 30 天按真实用户首轮提问统计的隐私最小化数据。',
    'Unable to load AI usage and cost': '无法加载 AI 用量与成本',
    'No recent usage': '暂无近期用量',
    'Remaining quota units': '剩余额度单位',
    'Turn explicit human-support requests into clear next actions.':
      '将明确的人工客服请求转成清晰的下一步。',
    'AI usage and cost': 'AI 用量与成本',
    'All clear': '全部处理完毕',
    'Intent signals': '意图信号',
    'No resolved support tasks.': '暂无已处理的客服任务。',
    'Pending support tasks are unavailable.': '暂时无法加载待处理的客服任务。',
    'Privacy-minimized assistant intent counts for the last 30 days.':
      '最近 30 天的隐私最小化客服意图统计。',
    'Privacy-minimized profile signals for the last 30 days.':
      '最近 30 天的隐私最小化用户画像信号。',
    'Resolved support history is unavailable.': '暂时无法加载客服处理记录。',
    'Support task completed': '客服任务已完成',
    'Unable to complete support task': '无法完成客服任务',
    'Unable to load assistant support tasks': '无法加载 AI 客服任务',
  },
  'zh-TW': {
    All: '全部',
    'Challenge reviews': '挑戰審核',
    'Bounty notifications': '懸賞通知',
    'Developer access': '開發者存取',
    'Account actions': '帳號操作',
    'Security incidents': '安全事件',
    'Mark all as read': '全部標為已讀',
    'Failed to load to-dos': '待辦載入失敗',
    Loading: '載入中',
    Notification: '通知',
    'Submitted challenge work and account requests will appear here.':
      '已提交的挑戰成果和帳號申請會顯示在這裡。',
    'No pending to-dos': '暫無待辦',
    'Assistant support tasks': 'AI 客服任務',
    'Pending work': '待處理',
    'Resolved history': '已處理記錄',
    'Insights and AI cost': '洞察與 AI 成本',
    'support tasks waiting for review': '個客服任務等待審核',
    'No pending support tasks.': '暫無待處理的客服任務。',
    'Processing note': '處理備註',
    'Completed at': '完成於',
    'Privacy-minimized request': '已做隱私最小化的請求',
    'No request details provided.': '未提供請求詳情。',
    'Complete support task': '完成客服任務',
    'Completing...': '完成中...',
    'Complete task': '完成任務',
    Refreshing: '重新整理中',
    'Action required': '需要處理',
    'Assistant support history and insights': 'AI 客服歷史與洞察',
    'Unable to load intent insights': '無法載入意圖洞察',
    'Unable to load profile insights': '無法載入使用者輪廓洞察',
    'Unable to load first-question insights': '無法載入首輪提問洞察',
    'No first-question data yet': '暫無首輪提問資料',
    'Top first questions': '首輪提問前十',
    'Privacy-minimized real-user first questions counted from the first turn in the last 30 days.':
      '最近 30 天按真實使用者首輪提問統計的隱私最小化資料。',
    'Unable to load AI usage and cost': '無法載入 AI 用量與成本',
    'No recent usage': '暫無近期用量',
    'Remaining quota units': '剩餘額度單位',
    'Turn explicit human-support requests into clear next actions.':
      '將明確的人工客服請求轉成清晰的下一步。',
    'AI usage and cost': 'AI 用量與成本',
    'All clear': '全部處理完畢',
    'Intent signals': '意圖訊號',
    'No resolved support tasks.': '暫無已處理的客服任務。',
    'Pending support tasks are unavailable.': '暫時無法載入待處理的客服任務。',
    'Privacy-minimized assistant intent counts for the last 30 days.':
      '最近 30 天的隱私最小化客服意圖統計。',
    'Privacy-minimized profile signals for the last 30 days.':
      '最近 30 天的隱私最小化使用者輪廓訊號。',
    'Resolved support history is unavailable.': '暫時無法載入客服處理記錄。',
    'Support task completed': '客服任務已完成',
    'Unable to complete support task': '無法完成客服任務',
    'Unable to load assistant support tasks': '無法載入 AI 客服任務',
  },
  fr: {
    All: 'Tout',
    'Challenge reviews': 'Révisions des défis',
    'Bounty notifications': 'Notifications de primes',
    'Developer access': 'Accès développeur',
    'Account actions': 'Actions sur le compte',
    'Security incidents': 'Incidents de sécurité',
    'Mark all as read': 'Tout marquer comme lu',
    'Failed to load to-dos': 'Échec du chargement des tâches',
    Loading: 'Chargement',
    Notification: 'Notification',
    'Submitted challenge work and account requests will appear here.':
      'Les travaux de défi soumis et les demandes de compte apparaîtront ici.',
    'No pending to-dos': 'Aucune tâche en attente',
    'Assistant support tasks': 'Tâches du support IA',
    'Pending work': 'À traiter',
    'Resolved history': 'Historique traité',
    'Insights and AI cost': 'Analyses et coût de l’IA',
    'support tasks waiting for review': 'tâches de support en attente de revue',
    'No pending support tasks.': 'Aucune tâche de support en attente.',
    'Processing note': 'Note de traitement',
    'Completed at': 'Terminé le',
    'Privacy-minimized request': 'Demande minimisée pour la confidentialité',
    'No request details provided.': 'Aucun détail de demande fourni.',
    'Complete support task': 'Terminer la tâche de support',
    'Completing...': 'Finalisation...',
    'Complete task': 'Terminer la tâche',
    Refreshing: 'Actualisation',
    'Action required': 'Action requise',
    'Assistant support history and insights':
      'Historique et analyses du support IA',
    'Unable to load intent insights': 'Impossible de charger les intentions',
    'Unable to load profile insights': 'Impossible de charger les profils',
    'Unable to load first-question insights':
      'Impossible de charger les premières questions',
    'No first-question data yet': 'Aucune première question pour le moment',
    'Top first questions': 'Top 10 des premières questions',
    'Privacy-minimized real-user first questions counted from the first turn in the last 30 days.':
      'Premières questions réelles, minimisées pour la confidentialité, comptées au premier tour sur les 30 derniers jours.',
    'Unable to load AI usage and cost':
      'Impossible de charger l’usage et le coût IA',
    'No recent usage': 'Aucun usage récent',
    'Remaining quota units': 'Unités de quota restantes',
    'Turn explicit human-support requests into clear next actions.':
      'Transformez les demandes explicites au support humain en prochaines étapes claires.',
    'AI usage and cost': 'Utilisation et coût de l’IA',
    'All clear': 'Tout est traité',
    'Intent signals': 'Signaux d’intention',
    'No resolved support tasks.': 'Aucune tâche de support traitée.',
    'Pending support tasks are unavailable.':
      'Les tâches de support en attente sont indisponibles.',
    'Privacy-minimized assistant intent counts for the last 30 days.':
      'Comptage des intentions du support, minimisé pour la confidentialité, sur 30 jours.',
    'Privacy-minimized profile signals for the last 30 days.':
      'Signaux de profil, minimisés pour la confidentialité, sur 30 jours.',
    'Resolved support history is unavailable.':
      'L’historique du support traité est indisponible.',
    'Support task completed': 'Tâche de support terminée',
    'Unable to complete support task':
      'Impossible de terminer la tâche de support',
    'Unable to load assistant support tasks':
      'Impossible de charger les tâches du support IA',
  },
  ja: {
    All: 'すべて',
    'Challenge reviews': 'チャレンジ審査',
    'Bounty notifications': '報奨金通知',
    'Developer access': '開発者アクセス',
    'Account actions': 'アカウント操作',
    'Security incidents': 'セキュリティインシデント',
    'Mark all as read': 'すべて既読にする',
    'Failed to load to-dos': '対応待ちを読み込めませんでした',
    Loading: '読み込み中',
    Notification: '通知',
    'Submitted challenge work and account requests will appear here.':
      '提出されたチャレンジ成果とアカウント申請がここに表示されます。',
    'No pending to-dos': '対応待ちはありません',
    'Assistant support tasks': 'AI サポートタスク',
    'Pending work': '対応待ち',
    'Resolved history': '対応済み履歴',
    'Insights and AI cost': '分析と AI コスト',
    'support tasks waiting for review': '件のサポートタスクが確認待ちです',
    'No pending support tasks.': '対応待ちのサポートタスクはありません。',
    'Processing note': '処理メモ',
    'Completed at': '完了日時',
    'Privacy-minimized request': 'プライバシー最小化済みの依頼',
    'No request details provided.': '依頼の詳細はありません。',
    'Complete support task': 'サポートタスクを完了',
    'Completing...': '完了処理中...',
    'Complete task': 'タスクを完了',
    Refreshing: '更新中',
    'Action required': '対応が必要',
    'Assistant support history and insights': 'AI サポート履歴と分析',
    'Unable to load intent insights': '意図分析を読み込めません',
    'Unable to load profile insights': 'プロフィール分析を読み込めません',
    'Unable to load first-question insights':
      '最初の質問の分析を読み込めません',
    'No first-question data yet': '最初の質問データはまだありません',
    'Top first questions': '最初の質問トップ10',
    'Privacy-minimized real-user first questions counted from the first turn in the last 30 days.':
      '過去30日間の実ユーザーの最初の質問を、初回ターンからプライバシー最小化して集計しています。',
    'Unable to load AI usage and cost': 'AI 使用量とコストを読み込めません',
    'No recent usage': '最近の使用履歴はありません',
    'Remaining quota units': '残りのクォータ単位',
    'Turn explicit human-support requests into clear next actions.':
      '明確な有人サポート依頼を次の行動に整理します。',
    'AI usage and cost': 'AI の使用量とコスト',
    'All clear': 'すべて対応済み',
    'Intent signals': '意図シグナル',
    'No resolved support tasks.': '対応済みのサポートタスクはありません。',
    'Pending support tasks are unavailable.':
      '対応待ちのサポートタスクを読み込めません。',
    'Privacy-minimized assistant intent counts for the last 30 days.':
      '過去 30 日間のプライバシー最小化済みサポート意図数。',
    'Privacy-minimized profile signals for the last 30 days.':
      '過去 30 日間のプライバシー最小化済みプロフィールシグナル。',
    'Resolved support history is unavailable.':
      '対応済みサポート履歴を読み込めません。',
    'Support task completed': 'サポートタスクを完了しました',
    'Unable to complete support task': 'サポートタスクを完了できません',
    'Unable to load assistant support tasks':
      'AI サポートタスクを読み込めません',
  },
  ru: {
    All: 'Все',
    'Challenge reviews': 'Проверка заданий',
    'Bounty notifications': 'Уведомления о наградах',
    'Developer access': 'Доступ разработчика',
    'Account actions': 'Действия с аккаунтом',
    'Security incidents': 'Инциденты безопасности',
    'Mark all as read': 'Отметить всё прочитанным',
    'Failed to load to-dos': 'Не удалось загрузить задачи',
    Loading: 'Загрузка',
    Notification: 'Уведомление',
    'Submitted challenge work and account requests will appear here.':
      'Здесь появятся отправленные решения заданий и запросы аккаунта.',
    'No pending to-dos': 'Нет ожидающих задач',
    'Assistant support tasks': 'Задачи поддержки ИИ',
    'Pending work': 'Ожидающие задачи',
    'Resolved history': 'История решённых задач',
    'Insights and AI cost': 'Аналитика и расходы на ИИ',
    'support tasks waiting for review': 'задач поддержки ожидают проверки',
    'No pending support tasks.': 'Нет ожидающих задач поддержки.',
    'Processing note': 'Заметка обработки',
    'Completed at': 'Завершено',
    'Privacy-minimized request': 'Запрос с минимизацией данных',
    'No request details provided.': 'Подробности запроса не указаны.',
    'Complete support task': 'Завершить задачу поддержки',
    'Completing...': 'Завершение...',
    'Complete task': 'Завершить задачу',
    Refreshing: 'Обновление',
    'Action required': 'Требуется действие',
    'Assistant support history and insights':
      'История и аналитика поддержки ИИ',
    'Unable to load intent insights':
      'Не удалось загрузить аналитику намерений',
    'Unable to load profile insights':
      'Не удалось загрузить аналитику профилей',
    'Unable to load first-question insights':
      'Не удалось загрузить аналитику первых вопросов',
    'No first-question data yet': 'Данных первых вопросов пока нет',
    'Top first questions': 'Топ-10 первых вопросов',
    'Privacy-minimized real-user first questions counted from the first turn in the last 30 days.':
      'Первые вопросы реальных пользователей с минимизацией данных за последние 30 дней.',
    'Unable to load AI usage and cost':
      'Не удалось загрузить расходы и использование ИИ',
    'No recent usage': 'Недавнего использования нет',
    'Remaining quota units': 'Оставшиеся единицы квоты',
    'Turn explicit human-support requests into clear next actions.':
      'Преобразуйте явные запросы к специалисту в понятные следующие шаги.',
    'AI usage and cost': 'Использование и расходы на ИИ',
    'All clear': 'Всё обработано',
    'Intent signals': 'Сигналы намерений',
    'No resolved support tasks.': 'Обработанных задач поддержки нет.',
    'Pending support tasks are unavailable.':
      'Ожидающие задачи поддержки недоступны.',
    'Privacy-minimized assistant intent counts for the last 30 days.':
      'Количество намерений поддержки с минимизацией данных за последние 30 дней.',
    'Privacy-minimized profile signals for the last 30 days.':
      'Сигналы профиля с минимизацией данных за последние 30 дней.',
    'Resolved support history is unavailable.':
      'История обработанных обращений поддержки недоступна.',
    'Support task completed': 'Задача поддержки выполнена',
    'Unable to complete support task': 'Не удалось завершить задачу поддержки',
    'Unable to load assistant support tasks':
      'Не удалось загрузить задачи поддержки ИИ',
  },
  vi: {
    All: 'Tất cả',
    'Challenge reviews': 'Duyệt thử thách',
    'Bounty notifications': 'Thông báo tiền thưởng',
    'Developer access': 'Quyền nhà phát triển',
    'Account actions': 'Thao tác tài khoản',
    'Security incidents': 'Sự cố bảo mật',
    'Mark all as read': 'Đánh dấu tất cả đã đọc',
    'Failed to load to-dos': 'Không thể tải việc cần làm',
    Loading: 'Đang tải',
    Notification: 'Thông báo',
    'Submitted challenge work and account requests will appear here.':
      'Bài làm thử thách và yêu cầu tài khoản đã gửi sẽ xuất hiện tại đây.',
    'No pending to-dos': 'Không có việc đang chờ',
    'Assistant support tasks': 'Tác vụ hỗ trợ AI',
    'Pending work': 'Việc đang chờ',
    'Resolved history': 'Lịch sử đã xử lý',
    'Insights and AI cost': 'Thông tin và chi phí AI',
    'support tasks waiting for review': 'tác vụ hỗ trợ đang chờ duyệt',
    'No pending support tasks.': 'Không có tác vụ hỗ trợ đang chờ.',
    'Processing note': 'Ghi chú xử lý',
    'Completed at': 'Hoàn tất lúc',
    'Privacy-minimized request': 'Yêu cầu đã tối giản dữ liệu riêng tư',
    'No request details provided.': 'Chưa có chi tiết yêu cầu.',
    'Complete support task': 'Hoàn tất tác vụ hỗ trợ',
    'Completing...': 'Đang hoàn tất...',
    'Complete task': 'Hoàn tất tác vụ',
    Refreshing: 'Đang làm mới',
    'Action required': 'Cần xử lý',
    'Assistant support history and insights': 'Lịch sử và thông tin hỗ trợ AI',
    'Unable to load intent insights': 'Không thể tải thông tin ý định',
    'Unable to load profile insights': 'Không thể tải thông tin hồ sơ',
    'Unable to load first-question insights':
      'Không thể tải thông tin câu hỏi đầu tiên',
    'No first-question data yet': 'Chưa có dữ liệu câu hỏi đầu tiên',
    'Top first questions': 'Top 10 câu hỏi đầu tiên',
    'Privacy-minimized real-user first questions counted from the first turn in the last 30 days.':
      'Câu hỏi đầu tiên của người dùng thật, tối giản dữ liệu riêng tư, được đếm từ lượt đầu trong 30 ngày qua.',
    'Unable to load AI usage and cost': 'Không thể tải mức dùng và chi phí AI',
    'No recent usage': 'Chưa có mức dùng gần đây',
    'Remaining quota units': 'Đơn vị hạn mức còn lại',
    'Turn explicit human-support requests into clear next actions.':
      'Chuyển yêu cầu hỗ trợ con người rõ ràng thành các bước tiếp theo.',
    'AI usage and cost': 'Mức dùng và chi phí AI',
    'All clear': 'Đã xử lý xong',
    'Intent signals': 'Tín hiệu ý định',
    'No resolved support tasks.': 'Không có tác vụ hỗ trợ đã xử lý.',
    'Pending support tasks are unavailable.':
      'Không thể tải tác vụ hỗ trợ đang chờ.',
    'Privacy-minimized assistant intent counts for the last 30 days.':
      'Số lượng ý định hỗ trợ đã tối giản dữ liệu riêng tư trong 30 ngày qua.',
    'Privacy-minimized profile signals for the last 30 days.':
      'Tín hiệu hồ sơ đã tối giản dữ liệu riêng tư trong 30 ngày qua.',
    'Resolved support history is unavailable.':
      'Không thể tải lịch sử hỗ trợ đã xử lý.',
    'Support task completed': 'Đã hoàn tất tác vụ hỗ trợ',
    'Unable to complete support task': 'Không thể hoàn tất tác vụ hỗ trợ',
    'Unable to load assistant support tasks': 'Không thể tải tác vụ hỗ trợ AI',
  },
}

const discountTranslations = {
  en: {
    'Discount Codes': 'Discount Codes',
    'Create discount code': 'Create discount code',
    'Edit discount code': 'Edit discount code',
    'Manage percentage discounts for checkout. Codes are validated and applied by the server.':
      'Manage percentage discounts for checkout. Codes are validated and applied by the server.',
    'Filter by code or name...': 'Filter by code or name...',
    Code: 'Code',
    Discount: 'Discount',
    Used: 'Used',
    Expires: 'Expires',
    'Unable to save discount code': 'Unable to save discount code',
    'Discount code saved': 'Discount code saved',
    'Unable to update discount code': 'Unable to update discount code',
    'Unable to delete discount code': 'Unable to delete discount code',
    'Discount code deleted': 'Discount code deleted',
    'No discount codes': 'No discount codes',
    'Delete this discount code?': 'Delete this discount code?',
    'Set a percentage discount. The server checks dates and minimum amount at checkout.':
      'Set a percentage discount. The server checks dates and minimum amount at checkout.',
    'Discount percent': 'Discount percent',
    'Minimum amount': 'Minimum amount',
    Starts: 'Starts',
    Apply: 'Apply',
    'Enter your discount code': 'Enter your discount code',
    'A valid discount code is applied at checkout and cannot be combined with another code.':
      'A valid discount code is applied at checkout and cannot be combined with another code.',
    'Discount applied: {{percent}}% off': 'Discount applied: {{percent}}% off',
  },
  zh: {
    'Discount Codes': '优惠码',
    'Create discount code': '创建优惠码',
    'Edit discount code': '编辑优惠码',
    'Manage percentage discounts for checkout. Codes are validated and applied by the server.':
      '管理结算时使用的百分比折扣。优惠码由服务器校验并应用。',
    'Filter by code or name...': '按代码或名称筛选…',
    Code: '代码',
    Discount: '折扣',
    Used: '已使用',
    Expires: '过期时间',
    'Unable to save discount code': '无法保存优惠码',
    'Discount code saved': '优惠码已保存',
    'Unable to update discount code': '无法更新优惠码',
    'Unable to delete discount code': '无法删除优惠码',
    'Discount code deleted': '优惠码已删除',
    'No discount codes': '暂无优惠码',
    'Delete this discount code?': '删除这个优惠码？',
    'Set a percentage discount. The server checks dates and minimum amount at checkout.':
      '设置百分比折扣。服务器会在结算时检查有效期和最低金额。',
    'Discount percent': '折扣百分比',
    'Minimum amount': '最低金额',
    Starts: '生效时间',
    Apply: '应用',
    'Enter your discount code': '输入优惠码',
    'A valid discount code is applied at checkout and cannot be combined with another code.':
      '有效优惠码会在结算时应用，且不能与其他优惠码叠加。',
    'Discount applied: {{percent}}% off': '已应用 {{percent}}% 折扣',
  },
  'zh-TW': {
    'Discount Codes': '優惠碼',
    'Create discount code': '建立優惠碼',
    'Edit discount code': '編輯優惠碼',
    'Manage percentage discounts for checkout. Codes are validated and applied by the server.':
      '管理結帳時使用的百分比折扣。優惠碼由伺服器驗證並套用。',
    'Filter by code or name...': '按代碼或名稱篩選…',
    Code: '代碼',
    Discount: '折扣',
    Used: '已使用',
    Expires: '到期時間',
    'Unable to save discount code': '無法儲存優惠碼',
    'Discount code saved': '優惠碼已儲存',
    'Unable to update discount code': '無法更新優惠碼',
    'Unable to delete discount code': '無法刪除優惠碼',
    'Discount code deleted': '優惠碼已刪除',
    'No discount codes': '暫無優惠碼',
    'Delete this discount code?': '刪除此優惠碼？',
    'Set a percentage discount. The server checks dates and minimum amount at checkout.':
      '設定百分比折扣。伺服器會在結帳時檢查有效期與最低金額。',
    'Discount percent': '折扣百分比',
    'Minimum amount': '最低金額',
    Starts: '生效時間',
    Apply: '套用',
    'Enter your discount code': '輸入優惠碼',
    'A valid discount code is applied at checkout and cannot be combined with another code.':
      '有效優惠碼會在結帳時套用，且不能與其他優惠碼疊加。',
    'Discount applied: {{percent}}% off': '已套用 {{percent}}% 折扣',
  },
  fr: {
    'Discount Codes': 'Codes promotionnels',
    'Create discount code': 'Créer un code promotionnel',
    'Edit discount code': 'Modifier le code promotionnel',
    'Manage percentage discounts for checkout. Codes are validated and applied by the server.':
      'Gérez les remises en pourcentage au paiement. Les codes sont validés et appliqués par le serveur.',
    'Filter by code or name...': 'Filtrer par code ou nom…',
    Code: 'Code',
    Discount: 'Remise',
    Used: 'Utilisé',
    Expires: 'Expire',
    'Unable to save discount code': 'Impossible d’enregistrer le code',
    'Discount code saved': 'Code promotionnel enregistré',
    'Unable to update discount code': 'Impossible de modifier le code',
    'Unable to delete discount code': 'Impossible de supprimer le code',
    'Discount code deleted': 'Code promotionnel supprimé',
    'No discount codes': 'Aucun code promotionnel',
    'Delete this discount code?': 'Supprimer ce code promotionnel ?',
    'Set a percentage discount. The server checks dates and minimum amount at checkout.':
      'Définissez une remise en pourcentage. Le serveur vérifie les dates et le montant minimal au paiement.',
    'Discount percent': 'Pourcentage de remise',
    'Minimum amount': 'Montant minimal',
    Starts: 'Début',
    Apply: 'Appliquer',
    'Enter your discount code': 'Saisissez votre code promotionnel',
    'A valid discount code is applied at checkout and cannot be combined with another code.':
      'Un code valide est appliqué au paiement et ne peut pas être combiné avec un autre code.',
    'Discount applied: {{percent}}% off': 'Remise appliquée : {{percent}} %',
  },
  ja: {
    'Discount Codes': '割引コード',
    'Create discount code': '割引コードを作成',
    'Edit discount code': '割引コードを編集',
    'Manage percentage discounts for checkout. Codes are validated and applied by the server.':
      '決済時のパーセント割引を管理します。コードはサーバーで検証・適用されます。',
    'Filter by code or name...': 'コードまたは名前で絞り込み…',
    Code: 'コード',
    Discount: '割引',
    Used: '使用数',
    Expires: '有効期限',
    'Unable to save discount code': '割引コードを保存できません',
    'Discount code saved': '割引コードを保存しました',
    'Unable to update discount code': '割引コードを更新できません',
    'Unable to delete discount code': '割引コードを削除できません',
    'Discount code deleted': '割引コードを削除しました',
    'No discount codes': '割引コードはありません',
    'Delete this discount code?': 'この割引コードを削除しますか？',
    'Set a percentage discount. The server checks dates and minimum amount at checkout.':
      'パーセント割引を設定します。決済時にサーバーが期間と最低金額を確認します。',
    'Discount percent': '割引率',
    'Minimum amount': '最低金額',
    Starts: '開始',
    Apply: '適用',
    'Enter your discount code': '割引コードを入力',
    'A valid discount code is applied at checkout and cannot be combined with another code.':
      '有効な割引コードは決済時に適用され、他のコードとは併用できません。',
    'Discount applied: {{percent}}% off':
      '割引を適用しました：{{percent}}% オフ',
  },
  ru: {
    'Discount Codes': 'Коды скидок',
    'Create discount code': 'Создать код скидки',
    'Edit discount code': 'Изменить код скидки',
    'Manage percentage discounts for checkout. Codes are validated and applied by the server.':
      'Управляйте процентными скидками при оплате. Коды проверяются и применяются сервером.',
    'Filter by code or name...': 'Фильтр по коду или названию…',
    Code: 'Код',
    Discount: 'Скидка',
    Used: 'Использован',
    Expires: 'Истекает',
    'Unable to save discount code': 'Не удалось сохранить код скидки',
    'Discount code saved': 'Код скидки сохранён',
    'Unable to update discount code': 'Не удалось обновить код скидки',
    'Unable to delete discount code': 'Не удалось удалить код скидки',
    'Discount code deleted': 'Код скидки удалён',
    'No discount codes': 'Кодов скидок нет',
    'Delete this discount code?': 'Удалить этот код скидки?',
    'Set a percentage discount. The server checks dates and minimum amount at checkout.':
      'Задайте процентную скидку. Сервер проверит даты и минимальную сумму при оплате.',
    'Discount percent': 'Процент скидки',
    'Minimum amount': 'Минимальная сумма',
    Starts: 'Начало',
    Apply: 'Применить',
    'Enter your discount code': 'Введите код скидки',
    'A valid discount code is applied at checkout and cannot be combined with another code.':
      'Действующий код применяется при оплате и не сочетается с другим кодом.',
    'Discount applied: {{percent}}% off': 'Скидка применена: {{percent}}%',
  },
  vi: {
    'Discount Codes': 'Mã giảm giá',
    'Create discount code': 'Tạo mã giảm giá',
    'Edit discount code': 'Chỉnh sửa mã giảm giá',
    'Manage percentage discounts for checkout. Codes are validated and applied by the server.':
      'Quản lý giảm giá theo phần trăm khi thanh toán. Mã được máy chủ xác thực và áp dụng.',
    'Filter by code or name...': 'Lọc theo mã hoặc tên…',
    Code: 'Mã',
    Discount: 'Giảm giá',
    Used: 'Đã dùng',
    Expires: 'Hết hạn',
    'Unable to save discount code': 'Không thể lưu mã giảm giá',
    'Discount code saved': 'Đã lưu mã giảm giá',
    'Unable to update discount code': 'Không thể cập nhật mã giảm giá',
    'Unable to delete discount code': 'Không thể xóa mã giảm giá',
    'Discount code deleted': 'Đã xóa mã giảm giá',
    'No discount codes': 'Chưa có mã giảm giá',
    'Delete this discount code?': 'Xóa mã giảm giá này?',
    'Set a percentage discount. The server checks dates and minimum amount at checkout.':
      'Đặt mức giảm theo phần trăm. Máy chủ kiểm tra ngày và số tiền tối thiểu khi thanh toán.',
    'Discount percent': 'Phần trăm giảm',
    'Minimum amount': 'Số tiền tối thiểu',
    Starts: 'Bắt đầu',
    Apply: 'Áp dụng',
    'Enter your discount code': 'Nhập mã giảm giá',
    'A valid discount code is applied at checkout and cannot be combined with another code.':
      'Mã hợp lệ được áp dụng khi thanh toán và không thể dùng cùng mã khác.',
    'Discount applied: {{percent}}% off': 'Đã áp dụng giảm {{percent}}%',
  },
}

const regionPolicyTranslations = {
  en: {
    'Regional access policy': 'Regional access policy',
    'Blocked country codes': 'Blocked country codes',
    'Require the edge policy to check blocked countries before requests reach the application.':
      'Require the edge policy to check blocked countries before requests reach the application.',
    'Comma-separated two-letter ISO codes, for example CN,US. Disable the policy to allow every region.':
      'Comma-separated two-letter ISO codes, for example CN,US. Disable the policy to allow every region.',
  },
  zh: {
    'Regional access policy': '地域访问限制',
    'Blocked country codes': '阻止的国家/地区代码',
    'Require the edge policy to check blocked countries before requests reach the application.':
      '要求边缘策略在请求进入应用前检查被阻止的国家或地区。',
    'Comma-separated two-letter ISO codes, for example CN,US. Disable the policy to allow every region.':
      '使用逗号分隔的两位 ISO 代码，例如 CN,US。关闭此策略即可允许所有地区。',
  },
  'zh-TW': {
    'Regional access policy': '地區存取限制',
    'Blocked country codes': '封鎖的國家／地區代碼',
    'Require the edge policy to check blocked countries before requests reach the application.':
      '要求邊緣策略在請求進入應用程式前檢查封鎖的國家或地區。',
    'Comma-separated two-letter ISO codes, for example CN,US. Disable the policy to allow every region.':
      '使用逗號分隔的兩位 ISO 代碼，例如 CN,US。關閉此策略即可允許所有地區。',
  },
  fr: {
    'Regional access policy': 'Politique d’accès régional',
    'Blocked country codes': 'Codes des pays bloqués',
    'Require the edge policy to check blocked countries before requests reach the application.':
      'Demander à la politique en périphérie de vérifier les pays bloqués avant l’application.',
    'Comma-separated two-letter ISO codes, for example CN,US. Disable the policy to allow every region.':
      'Codes ISO à deux lettres séparés par des virgules, par exemple CN,US. Désactivez la politique pour autoriser toutes les régions.',
  },
  ja: {
    'Regional access policy': '地域アクセス制限',
    'Blocked country codes': 'ブロックする国コード',
    'Require the edge policy to check blocked countries before requests reach the application.':
      'アプリケーションに到達する前にエッジポリシーでブロック対象国を確認します。',
    'Comma-separated two-letter ISO codes, for example CN,US. Disable the policy to allow every region.':
      'CN,US のように2文字の ISO コードをカンマ区切りで指定します。無効にすると全地域を許可します。',
  },
  ru: {
    'Regional access policy': 'Региональная политика доступа',
    'Blocked country codes': 'Коды заблокированных стран',
    'Require the edge policy to check blocked countries before requests reach the application.':
      'Проверять заблокированные страны на границе до передачи запроса приложению.',
    'Comma-separated two-letter ISO codes, for example CN,US. Disable the policy to allow every region.':
      'Двухбуквенные ISO-коды через запятую, например CN,US. Отключите политику, чтобы разрешить все регионы.',
  },
  vi: {
    'Regional access policy': 'Chính sách truy cập theo khu vực',
    'Blocked country codes': 'Mã quốc gia bị chặn',
    'Require the edge policy to check blocked countries before requests reach the application.':
      'Yêu cầu chính sách biên kiểm tra quốc gia bị chặn trước khi yêu cầu đến ứng dụng.',
    'Comma-separated two-letter ISO codes, for example CN,US. Disable the policy to allow every region.':
      'Nhập mã ISO hai chữ cái cách nhau bằng dấu phẩy, ví dụ CN,US. Tắt chính sách để cho phép mọi khu vực.',
  },
}

const advancedSecurityTranslations = {
  en: {
    'Each advanced security rule must include at least one explicit group.':
      'Each advanced security rule must include at least one explicit group.',
    'Rule groups must be non-empty explicit names of at most 64 characters; wildcard groups are not allowed.':
      'Rule groups must be non-empty explicit names of at most 64 characters; wildcard groups are not allowed.',
    'Every rule must list the API group or groups it applies to; rules never apply globally.':
      'Every rule must list the API group or groups it applies to; rules never apply globally.',
  },
  zh: {
    'Each advanced security rule must include at least one explicit group.':
      '每条高级安全规则至少要指定一个明确分组。',
    'Rule groups must be non-empty explicit names of at most 64 characters; wildcard groups are not allowed.':
      '规则分组必须是非空的明确名称，长度不超过 64 个字符；不允许使用通配符分组。',
    'Every rule must list the API group or groups it applies to; rules never apply globally.':
      '每条规则都必须列出它适用的 API 分组；规则不会全局生效。',
  },
  'zh-TW': {
    'Each advanced security rule must include at least one explicit group.':
      '每條進階安全規則至少要指定一個明確分組。',
    'Rule groups must be non-empty explicit names of at most 64 characters; wildcard groups are not allowed.':
      '規則分組必須是非空的明確名稱，長度不超過 64 個字元；不允許使用萬用字元分組。',
    'Every rule must list the API group or groups it applies to; rules never apply globally.':
      '每條規則都必須列出適用的 API 分組；規則不會全域生效。',
  },
  fr: {
    'Each advanced security rule must include at least one explicit group.':
      'Chaque règle de sécurité avancée doit spécifier au moins un groupe explicite.',
    'Rule groups must be non-empty explicit names of at most 64 characters; wildcard groups are not allowed.':
      'Les groupes doivent être des noms explicites non vides de 64 caractères maximum ; les groupes génériques sont interdits.',
    'Every rule must list the API group or groups it applies to; rules never apply globally.':
      'Chaque règle doit indiquer les groupes API auxquels elle s’applique ; elle ne s’applique jamais globalement.',
  },
  ja: {
    'Each advanced security rule must include at least one explicit group.':
      '高度なセキュリティルールごとに、明示的なグループを1つ以上指定してください。',
    'Rule groups must be non-empty explicit names of at most 64 characters; wildcard groups are not allowed.':
      'ルールのグループは空でない64文字以内の明示的な名前にしてください。ワイルドカードは使用できません。',
    'Every rule must list the API group or groups it applies to; rules never apply globally.':
      '各ルールには適用するAPIグループを指定してください。ルールが全体に適用されることはありません。',
  },
  ru: {
    'Each advanced security rule must include at least one explicit group.':
      'Для каждого расширенного правила безопасности укажите хотя бы одну явную группу.',
    'Rule groups must be non-empty explicit names of at most 64 characters; wildcard groups are not allowed.':
      'Группы правил должны быть непустыми явными именами длиной не более 64 символов; группы с подстановочными знаками запрещены.',
    'Every rule must list the API group or groups it applies to; rules never apply globally.':
      'Для каждого правила укажите группы API, к которым оно применяется; глобальное применение невозможно.',
  },
  vi: {
    'Each advanced security rule must include at least one explicit group.':
      'Mỗi quy tắc bảo mật nâng cao phải chỉ định ít nhất một nhóm cụ thể.',
    'Rule groups must be non-empty explicit names of at most 64 characters; wildcard groups are not allowed.':
      'Nhóm quy tắc phải là tên cụ thể không rỗng, dài tối đa 64 ký tự; không cho phép nhóm ký tự đại diện.',
    'Every rule must list the API group or groups it applies to; rules never apply globally.':
      'Mỗi quy tắc phải liệt kê các nhóm API mà nó áp dụng; quy tắc không bao giờ áp dụng toàn cục.',
  },
}

for (const [locale, translations] of Object.entries(discountTranslations)) {
  Object.assign(newKeys[locale], translations)
}
for (const [locale, translations] of Object.entries(regionPolicyTranslations)) {
  Object.assign(newKeys[locale], translations)
}
for (const [locale, translations] of Object.entries(
  advancedSecurityTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const aiProfileTranslations = {
  en: {
    'AI labels': 'AI labels',
    'Generated from assistant conversations':
      'Generated from assistant conversations',
    'Managed by an administrator': 'Managed by an administrator',
    'Continue with Google': 'Continue with Google',
    'Discount code': 'Discount code',
    'Discount code is invalid': 'Discount code is invalid',
    Or: 'Or',
    'Page {{page}} of {{total}}': 'Page {{page}} of {{total}}',
    'The drawing workbench is available after developer access is approved.':
      'The drawing workbench is available after developer access is approved.',
  },
  zh: {
    'AI labels': 'AI 标签',
    'Generated from assistant conversations': '根据助手对话生成',
    'Managed by an administrator': '由管理员管理',
    'Continue with Google': '使用 Google 继续',
    'Discount code': '优惠码',
    'Discount code is invalid': '优惠码无效',
    Or: '或',
    'Page {{page}} of {{total}}': '第 {{page}} / {{total}} 页',
    'The drawing workbench is available after developer access is approved.':
      '开发者访问获批后即可使用绘图工作台。',
  },
  'zh-TW': {
    'AI labels': 'AI 標籤',
    'Generated from assistant conversations': '根據助手對話產生',
    'Managed by an administrator': '由管理員管理',
    'Continue with Google': '使用 Google 繼續',
    'Discount code': '優惠碼',
    'Discount code is invalid': '優惠碼無效',
    Or: '或',
    'Page {{page}} of {{total}}': '第 {{page}} / {{total}} 頁',
    'The drawing workbench is available after developer access is approved.':
      '開發者存取獲核准後即可使用繪圖工作台。',
  },
  fr: {
    'AI labels': 'Étiquettes IA',
    'Generated from assistant conversations':
      'Générées à partir des conversations avec l’assistant',
    'Managed by an administrator': 'Gérées par un administrateur',
    'Continue with Google': 'Continuer avec Google',
    'Discount code': 'Code de réduction',
    'Discount code is invalid': 'Le code de réduction est invalide',
    Or: 'Ou',
    'Page {{page}} of {{total}}': 'Page {{page}} sur {{total}}',
    'The drawing workbench is available after developer access is approved.':
      'L’atelier de dessin est disponible après l’approbation de l’accès développeur.',
  },
  ja: {
    'AI labels': 'AIラベル',
    'Generated from assistant conversations': 'アシスタントとの会話から生成',
    'Managed by an administrator': '管理者が管理',
    'Continue with Google': 'Google で続行',
    'Discount code': '割引コード',
    'Discount code is invalid': '割引コードが無効です',
    Or: 'または',
    'Page {{page}} of {{total}}': '{{total}} ページ中 {{page}} ページ',
    'The drawing workbench is available after developer access is approved.':
      '開発者アクセスの承認後に描画ワークベンチを利用できます。',
  },
  ru: {
    'AI labels': 'Метки ИИ',
    'Generated from assistant conversations':
      'Созданы по диалогам с ассистентом',
    'Managed by an administrator': 'Управляются администратором',
    'Continue with Google': 'Продолжить через Google',
    'Discount code': 'Код скидки',
    'Discount code is invalid': 'Код скидки недействителен',
    Or: 'Или',
    'Page {{page}} of {{total}}': 'Страница {{page}} из {{total}}',
    'The drawing workbench is available after developer access is approved.':
      'Рабочая область рисования доступна после одобрения доступа разработчика.',
  },
  vi: {
    'AI labels': 'Nhãn AI',
    'Generated from assistant conversations':
      'Được tạo từ hội thoại với trợ lý',
    'Managed by an administrator': 'Do quản trị viên quản lý',
    'Continue with Google': 'Tiếp tục với Google',
    'Discount code': 'Mã giảm giá',
    'Discount code is invalid': 'Mã giảm giá không hợp lệ',
    Or: 'Hoặc',
    'Page {{page}} of {{total}}': 'Trang {{page}} / {{total}}',
    'The drawing workbench is available after developer access is approved.':
      'Có thể sử dụng bàn vẽ sau khi quyền nhà phát triển được phê duyệt.',
  },
}

for (const [locale, translations] of Object.entries(aiProfileTranslations)) {
  Object.assign(newKeys[locale], translations)
}

const channelMarketplaceTranslations = {
  en: {
    'Channel marketplace': 'Channel marketplace',
    'Share a channel': 'Share a channel',
    'All channels': 'All channels',
    'My channels': 'My channels',
    Review: 'Review',
    'Public group': 'Public group',
    'Public channel group': 'Public channel group',
    'Every submission is reviewed before it is listed.':
      'Every submission is reviewed before it is listed.',
    'The contributor account email is shown publicly.':
      'The contributor account email is shown publicly.',
    'All approved shared channels use this administrator-configured group.':
      'All approved shared channels use this administrator-configured group.',
    'Submission sent for review': 'Submission sent for review',
    'Report sent to administrators': 'Report sent to administrators',
    'Report channel': 'Report channel',
    Report: 'Report',
    'Close report': 'Close report',
    'Open reports': 'Open reports',
    'Send report': 'Send report',
    'Reason for reporting': 'Reason for reporting',
    'Review note (required when rejecting)':
      'Review note (required when rejecting)',
    'Comma-separated model IDs': 'Comma-separated model IDs',
    'No approved channels yet.': 'No approved channels yet.',
    'You have not uploaded a channel yet.':
      'You have not shared a channel yet.',
    'No description': 'No description',
    'The channel is assigned to the administrator-configured public group and is reviewed before publication. Do not submit credentials.':
      'The channel is assigned to the administrator-configured public group and is reviewed before publication. Do not submit credentials.',
    '{{count}} submissions waiting for review':
      '{{count}} submissions waiting for review',
    'Tell administrators what should be checked. The report will not immediately disable the channel.':
      'Tell administrators what should be checked. The report will not immediately disable the channel.',
  },
  zh: {
    'Channel marketplace': '渠道市场',
    'Share a channel': '分享渠道',
    'All channels': '所有渠道',
    'My channels': '我的渠道',
    Review: '审核',
    'Public group': '公开分组',
    'Public channel group': '公开渠道分组',
    'Every submission is reviewed before it is listed.':
      '所有提交都要审核通过后才会展示。',
    'The contributor account email is shown publicly.':
      '分享者的账号邮箱会公开显示。',
    'All approved shared channels use this administrator-configured group.':
      '所有通过审核的分享渠道都使用此管理员配置的分组。',
    'Submission sent for review': '已提交审核',
    'Report sent to administrators': '举报已发送给管理员',
    'Report channel': '举报渠道',
    Report: '举报',
    'Close report': '关闭举报',
    'Open reports': '待处理举报',
    'Send report': '发送举报',
    'Reason for reporting': '举报原因',
    'Review note (required when rejecting)': '审核备注（拒绝时必填）',
    'Comma-separated model IDs': '用逗号分隔模型 ID',
    'No approved channels yet.': '暂时没有已审核渠道。',
    'You have not uploaded a channel yet.': '你还没有分享渠道。',
    'No description': '暂无说明',
    'The channel is assigned to the administrator-configured public group and is reviewed before publication. Do not submit credentials.':
      '渠道会被分配到管理员配置的公开分组，审核通过后才会发布。请勿提交凭据。',
    '{{count}} submissions waiting for review': '{{count}} 个提交等待审核',
    'Tell administrators what should be checked. The report will not immediately disable the channel.':
      '请告诉管理员需要检查什么。举报不会立即停用渠道。',
  },
  'zh-TW': {
    'Channel marketplace': '渠道市集',
    'Share a channel': '分享渠道',
    'All channels': '所有渠道',
    'My channels': '我的渠道',
    Review: '審核',
    'Public group': '公開分組',
    'Public channel group': '公開渠道分組',
    'Every submission is reviewed before it is listed.':
      '所有提交都會在審核通過後才會顯示。',
    'The contributor account email is shown publicly.':
      '分享者的帳戶電子郵件會公開顯示。',
    'All approved shared channels use this administrator-configured group.':
      '所有通過審核的分享渠道都使用管理員設定的分組。',
    'Submission sent for review': '已提交審核',
    'Report sent to administrators': '舉報已送給管理員',
    'Report channel': '舉報渠道',
    Report: '舉報',
    'Close report': '關閉舉報',
    'Open reports': '待處理舉報',
    'Send report': '送出舉報',
    'Reason for reporting': '舉報原因',
    'Review note (required when rejecting)': '審核備註（拒絕時必填）',
    'Comma-separated model IDs': '以逗號分隔模型 ID',
    'No approved channels yet.': '目前沒有已審核渠道。',
    'You have not uploaded a channel yet.': '你尚未分享渠道。',
    'No description': '暫無說明',
    'The channel is assigned to the administrator-configured public group and is reviewed before publication. Do not submit credentials.':
      '渠道會被分配至管理員設定的公開分組，審核通過後才會發布。請勿提交憑證。',
    '{{count}} submissions waiting for review': '{{count}} 個提交等待審核',
    'Tell administrators what should be checked. The report will not immediately disable the channel.':
      '請告訴管理員需要檢查什麼。舉報不會立即停用渠道。',
  },
  fr: {
    'Channel marketplace': 'Marché des canaux',
    'Share a channel': 'Partager un canal',
    'All channels': 'Tous les canaux',
    'My channels': 'Mes canaux',
    Review: 'Révision',
    'Public group': 'Groupe public',
    'Public channel group': 'Groupe de canaux publics',
    'Every submission is reviewed before it is listed.':
      'Chaque soumission est vérifiée avant sa publication.',
    'The contributor account email is shown publicly.':
      'L’adresse e-mail du contributeur est affichée publiquement.',
    'All approved shared channels use this administrator-configured group.':
      'Tous les canaux partagés approuvés utilisent ce groupe configuré par l’administrateur.',
    'Submission sent for review': 'Soumission envoyée pour révision',
    'Report sent to administrators': 'Signalement envoyé aux administrateurs',
    'Report channel': 'Signaler un canal',
    Report: 'Signaler',
    'Close report': 'Fermer le signalement',
    'Open reports': 'Signalements ouverts',
    'Send report': 'Envoyer le signalement',
    'Reason for reporting': 'Motif du signalement',
    'Review note (required when rejecting)':
      'Note de révision (obligatoire en cas de refus)',
    'Comma-separated model IDs':
      'Identifiants de modèles séparés par des virgules',
    'No approved channels yet.': 'Aucun canal approuvé pour le moment.',
    'You have not uploaded a channel yet.':
      'Vous n’avez pas encore partagé de canal.',
    'No description': 'Aucune description',
    'The channel is assigned to the administrator-configured public group and is reviewed before publication. Do not submit credentials.':
      'Le canal utilise le groupe public configuré par l’administrateur et est vérifié avant publication. Ne fournissez pas d’identifiants.',
    '{{count}} submissions waiting for review':
      '{{count}} soumissions en attente de révision',
    'Tell administrators what should be checked. The report will not immediately disable the channel.':
      'Indiquez aux administrateurs ce qui doit être vérifié. Le signalement ne désactivera pas immédiatement le canal.',
  },
  ja: {
    'Channel marketplace': 'チャンネルマーケット',
    'Share a channel': 'チャンネルを共有',
    'All channels': 'すべてのチャンネル',
    'My channels': '自分のチャンネル',
    Review: '審査',
    'Public group': '公開グループ',
    'Public channel group': '公開チャンネルグループ',
    'Every submission is reviewed before it is listed.':
      'すべての投稿は審査後に掲載されます。',
    'The contributor account email is shown publicly.':
      '共有者のアカウントメールアドレスは公開表示されます。',
    'All approved shared channels use this administrator-configured group.':
      '承認された共有チャンネルはすべて管理者が設定したグループを使用します。',
    'Submission sent for review': '審査に送信しました',
    'Report sent to administrators': '管理者に報告しました',
    'Report channel': 'チャンネルを報告',
    Report: '報告',
    'Close report': '報告を閉じる',
    'Open reports': '未処理の報告',
    'Send report': '報告を送信',
    'Reason for reporting': '報告理由',
    'Review note (required when rejecting)': '審査メモ（却下時は必須）',
    'Comma-separated model IDs': 'モデル ID をカンマで区切って入力',
    'No approved channels yet.': '承認済みのチャンネルはまだありません。',
    'You have not uploaded a channel yet.':
      'まだチャンネルを共有していません。',
    'No description': '説明なし',
    'The channel is assigned to the administrator-configured public group and is reviewed before publication. Do not submit credentials.':
      'チャンネルは管理者設定の公開グループに割り当てられ、掲載前に審査されます。認証情報は送信しないでください。',
    '{{count}} submissions waiting for review':
      '{{count}} 件の投稿が審査待ちです',
    'Tell administrators what should be checked. The report will not immediately disable the channel.':
      '確認してほしい点を管理者に伝えてください。報告しても直ちにチャンネルは無効になりません。',
  },
  ru: {
    'Channel marketplace': 'Каталог каналов',
    'Share a channel': 'Поделиться каналом',
    'All channels': 'Все каналы',
    'My channels': 'Мои каналы',
    Review: 'Проверка',
    'Public group': 'Публичная группа',
    'Public channel group': 'Группа публичных каналов',
    'Every submission is reviewed before it is listed.':
      'Каждая заявка проверяется перед публикацией.',
    'The contributor account email is shown publicly.':
      'Электронная почта автора отображается публично.',
    'All approved shared channels use this administrator-configured group.':
      'Все одобренные каналы используют группу, настроенную администратором.',
    'Submission sent for review': 'Заявка отправлена на проверку',
    'Report sent to administrators': 'Жалоба отправлена администраторам',
    'Report channel': 'Пожаловаться на канал',
    Report: 'Пожаловаться',
    'Close report': 'Закрыть жалобу',
    'Open reports': 'Открытые жалобы',
    'Send report': 'Отправить жалобу',
    'Reason for reporting': 'Причина жалобы',
    'Review note (required when rejecting)':
      'Заметка проверки (обязательна при отклонении)',
    'Comma-separated model IDs': 'Идентификаторы моделей через запятую',
    'No approved channels yet.': 'Одобренных каналов пока нет.',
    'You have not uploaded a channel yet.': 'Вы ещё не поделились каналом.',
    'No description': 'Без описания',
    'The channel is assigned to the administrator-configured public group and is reviewed before publication. Do not submit credentials.':
      'Канал получает публичную группу, настроенную администратором, и проверяется перед публикацией. Не отправляйте учётные данные.',
    '{{count}} submissions waiting for review': 'Заявок на проверке: {{count}}',
    'Tell administrators what should be checked. The report will not immediately disable the channel.':
      'Укажите администраторам, что нужно проверить. Жалоба не отключит канал немедленно.',
  },
  vi: {
    'Channel marketplace': 'Chợ kênh',
    'Share a channel': 'Chia sẻ kênh',
    'All channels': 'Tất cả kênh',
    'My channels': 'Kênh của tôi',
    Review: 'Xét duyệt',
    'Public group': 'Nhóm công khai',
    'Public channel group': 'Nhóm kênh công khai',
    'Every submission is reviewed before it is listed.':
      'Mọi đề xuất đều được xét duyệt trước khi hiển thị.',
    'The contributor account email is shown publicly.':
      'Email tài khoản của người chia sẻ sẽ được hiển thị công khai.',
    'All approved shared channels use this administrator-configured group.':
      'Mọi kênh được duyệt đều dùng nhóm do quản trị viên cấu hình.',
    'Submission sent for review': 'Đã gửi đề xuất để xét duyệt',
    'Report sent to administrators': 'Đã gửi báo cáo cho quản trị viên',
    'Report channel': 'Báo cáo kênh',
    Report: 'Báo cáo',
    'Close report': 'Đóng báo cáo',
    'Open reports': 'Báo cáo đang mở',
    'Send report': 'Gửi báo cáo',
    'Reason for reporting': 'Lý do báo cáo',
    'Review note (required when rejecting)':
      'Ghi chú xét duyệt (bắt buộc khi từ chối)',
    'Comma-separated model IDs': 'ID model, phân tách bằng dấu phẩy',
    'No approved channels yet.': 'Chưa có kênh nào được duyệt.',
    'You have not uploaded a channel yet.': 'Bạn chưa chia sẻ kênh nào.',
    'No description': 'Không có mô tả',
    'The channel is assigned to the administrator-configured public group and is reviewed before publication. Do not submit credentials.':
      'Kênh được gán vào nhóm công khai do quản trị viên cấu hình và được xét duyệt trước khi đăng. Không gửi thông tin xác thực.',
    '{{count}} submissions waiting for review':
      '{{count}} đề xuất đang chờ xét duyệt',
    'Tell administrators what should be checked. The report will not immediately disable the channel.':
      'Cho quản trị viên biết cần kiểm tra điều gì. Báo cáo sẽ không vô hiệu hóa kênh ngay lập tức.',
  },
}

const assistantAndSecurityTranslations = {
  en: {
    'Sensitive details are hidden until confirmation and remain visible only to you.':
      'Sensitive details are hidden until confirmation and remain visible only to you.',
    'Do not send personal information, passwords, API keys, or credentials in chat. Site-issued credentials are shown only after your explicit confirmation, remain visible only to you, and stay out of the assistant context.':
      'Do not send personal information, passwords, API keys, or credentials in chat. Site-issued credentials are shown only after your explicit confirmation, remain visible only to you, and stay out of the assistant context.',
    'This history is available because the account has a lower access level. Credential details remain visible only to their owner.':
      'This history is available because the account has a lower access level. Credential details remain visible only to their owner.',
    'The credential is shown only after confirmation and is never added to chat history.':
      'The credential is shown only after confirmation and is never added to chat history.',
    'Create an API key first, then confirm the CC Switch import. The browser builds the link from the selected model and service root; the key never enters assistant chat.':
      'Create an API key first, then confirm the CC Switch import. The browser builds the link from the selected model and service root; the key never enters assistant chat.',
    'Click Import to CC Switch, then review and confirm the import dialog.':
      'Click Import to CC Switch, then review and confirm the import dialog.',
    'Please enter a message.': 'Please enter a message.',
    'Please enter a message other than a single punctuation mark.':
      'Please enter a message other than a single punctuation mark.',
    'The request is already in the administrator queue. An AI recommendation is optional and may be added after you continue the conversation.':
      'The request is already in the administrator queue. An AI recommendation is optional and may be added after you continue the conversation.',
    'You can submit for administrator review without an AI recommendation. The recommendation only gives the reviewer more context; it never decides access.':
      'You can submit for administrator review without an AI recommendation. The recommendation only gives the reviewer more context; it never decides access.',
    'Bind an email, enable 2FA, or set up a Passkey in your profile to unlock sensitive operations.':
      'Bind an email, enable 2FA, or set up a Passkey in your profile to unlock sensitive operations.',
    'Enter the code from your authenticator app or a backup code.':
      'Enter the code from your authenticator app or a backup code.',
    'Enter verification code or backup code':
      'Enter verification code or backup code',
  },
  zh: {
    'Sensitive details are hidden until confirmation and remain visible only to you.':
      '敏感信息已隐藏，确认后仅向你显示，并且只对你可见。',
    'Do not send personal information, passwords, API keys, or credentials in chat. Site-issued credentials are shown only after your explicit confirmation, remain visible only to you, and stay out of the assistant context.':
      '请勿在聊天中发送个人信息、密码、API 密钥或凭证。本站凭证仅在你明确确认后向你显示，只对你可见，并且不会进入助手上下文。',
    'This history is available because the account has a lower access level. Credential details remain visible only to their owner.':
      '由于该账号等级较低，你可以查看此历史；凭证详情仍仅对其所有者可见。',
    'The credential is shown only after confirmation and is never added to chat history.':
      '凭证仅在确认后显示，绝不会加入聊天记录。',
    'Create an API key first, then confirm the CC Switch import. The browser builds the link from the selected model and service root; the key never enters assistant chat.':
      '请先创建 API 密钥，然后确认导入 CC Switch。浏览器会根据所选模型和服务根地址生成链接，密钥不会进入助手对话。',
    'Click Import to CC Switch, then review and confirm the import dialog.':
      '点击“导入 CC Switch”，然后检查并确认导入对话框。',
    'Please enter a message.': '请输入消息。',
    'Please enter a message other than a single punctuation mark.':
      '请输入不只是单个标点符号的消息。',
    'The request is already in the administrator queue. An AI recommendation is optional and may be added after you continue the conversation.':
      '该申请已在管理员队列中。你可以继续对话，之后再补充 AI 推荐信；推荐信是可选的。',
    'You can submit for administrator review without an AI recommendation. The recommendation only gives the reviewer more context; it never decides access.':
      '你可以不附带 AI 推荐信，直接提交管理员审核。推荐信只为审核者提供更多背景，不会决定是否通过。',
    'Bind an email, enable 2FA, or set up a Passkey in your profile to unlock sensitive operations.':
      '请在个人资料中绑定邮箱、启用双重身份验证或设置 Passkey，以解锁敏感操作。',
    'Enter the code from your authenticator app or a backup code.':
      '请输入身份验证器应用中的代码或备用代码。',
    'Enter verification code or backup code': '请输入验证码或备用代码',
  },
  'zh-TW': {
    'Sensitive details are hidden until confirmation and remain visible only to you.':
      '敏感資訊已隱藏，確認後僅向你顯示，且只有你可見。',
    'Do not send personal information, passwords, API keys, or credentials in chat. Site-issued credentials are shown only after your explicit confirmation, remain visible only to you, and stay out of the assistant context.':
      '請勿在聊天中傳送個人資料、密碼、API 金鑰或憑證。本站憑證僅在你明確確認後向你顯示，只有你可見，且不會進入助理上下文。',
    'This history is available because the account has a lower access level. Credential details remain visible only to their owner.':
      '因該帳號等級較低，你可以查看此歷史；憑證詳情仍僅對其擁有者可見。',
    'The credential is shown only after confirmation and is never added to chat history.':
      '憑證僅在確認後顯示，絕不會加入聊天記錄。',
    'Create an API key first, then confirm the CC Switch import. The browser builds the link from the selected model and service root; the key never enters assistant chat.':
      '請先建立 API 金鑰，然後確認匯入 CC Switch。瀏覽器會根據所選模型和服務根網址產生連結，金鑰不會進入助理對話。',
    'Click Import to CC Switch, then review and confirm the import dialog.':
      '點擊「匯入 CC Switch」，然後檢查並確認匯入對話框。',
    'Please enter a message.': '請輸入訊息。',
    'Please enter a message other than a single punctuation mark.':
      '請輸入不只是單一標點符號的訊息。',
    'The request is already in the administrator queue. An AI recommendation is optional and may be added after you continue the conversation.':
      '此申請已在管理員佇列中。你可以繼續對話，之後再補充 AI 推薦信；推薦信為選填。',
    'You can submit for administrator review without an AI recommendation. The recommendation only gives the reviewer more context; it never decides access.':
      '你可以不附帶 AI 推薦信，直接提交管理員審核。推薦信只提供更多背景，不會決定是否核准。',
    'Bind an email, enable 2FA, or set up a Passkey in your profile to unlock sensitive operations.':
      '請在個人資料中綁定電子郵件、啟用雙重驗證或設定 Passkey，以解鎖敏感操作。',
    'Enter the code from your authenticator app or a backup code.':
      '請輸入驗證器應用程式中的代碼或備用代碼。',
    'Enter verification code or backup code': '請輸入驗證碼或備用代碼',
  },
  fr: {
    'Sensitive details are hidden until confirmation and remain visible only to you.':
      'Les informations sensibles sont masquées jusqu’à confirmation et restent visibles uniquement pour vous.',
    'Do not send personal information, passwords, API keys, or credentials in chat. Site-issued credentials are shown only after your explicit confirmation, remain visible only to you, and stay out of the assistant context.':
      'N’envoyez pas d’informations personnelles, de mots de passe, de clés API ou d’identifiants dans le chat. Les identifiants fournis par le site ne sont affichés qu’après votre confirmation explicite, restent visibles uniquement pour vous et restent hors du contexte de l’assistant.',
    'This history is available because the account has a lower access level. Credential details remain visible only to their owner.':
      'Cet historique est disponible car le compte a un niveau d’accès inférieur. Les détails d’identification restent visibles uniquement par leur propriétaire.',
    'The credential is shown only after confirmation and is never added to chat history.':
      'L’identifiant n’est affiché qu’après confirmation et n’est jamais ajouté à l’historique du chat.',
    'Create an API key first, then confirm the CC Switch import. The browser builds the link from the selected model and service root; the key never enters assistant chat.':
      'Créez d’abord une clé API, puis confirmez l’importation dans CC Switch. Le navigateur construit le lien avec le modèle et la racine du service sélectionnés ; la clé n’entre jamais dans le chat de l’assistant.',
    'Click Import to CC Switch, then review and confirm the import dialog.':
      'Cliquez sur « Importer dans CC Switch », puis vérifiez et confirmez la boîte de dialogue d’importation.',
    'Please enter a message.': 'Saisissez un message.',
    'Please enter a message other than a single punctuation mark.':
      'Saisissez un message autre qu’un simple signe de ponctuation.',
    'The request is already in the administrator queue. An AI recommendation is optional and may be added after you continue the conversation.':
      'La demande est déjà dans la file d’attente de l’administrateur. Une recommandation IA est facultative et peut être ajoutée après la poursuite de la conversation.',
    'You can submit for administrator review without an AI recommendation. The recommendation only gives the reviewer more context; it never decides access.':
      'Vous pouvez soumettre la demande à l’administrateur sans recommandation IA. Celle-ci apporte du contexte au réviseur, mais ne décide jamais de l’accès.',
    'Bind an email, enable 2FA, or set up a Passkey in your profile to unlock sensitive operations.':
      'Associez un e-mail, activez la 2FA ou configurez une Passkey dans votre profil pour débloquer les opérations sensibles.',
    'Enter the code from your authenticator app or a backup code.':
      'Saisissez le code de votre application d’authentification ou un code de secours.',
    'Enter verification code or backup code':
      'Saisissez le code de vérification ou de secours',
  },
  ja: {
    'Sensitive details are hidden until confirmation and remain visible only to you.':
      '機密情報は確認するまで非表示で、確認後もあなたにだけ表示されます。',
    'Do not send personal information, passwords, API keys, or credentials in chat. Site-issued credentials are shown only after your explicit confirmation, remain visible only to you, and stay out of the assistant context.':
      'チャットに個人情報、パスワード、API キー、認証情報を送信しないでください。サイトが発行する認証情報は明示的な確認後にのみ表示され、あなたにだけ表示され、アシスタントのコンテキストには入りません。',
    'This history is available because the account has a lower access level. Credential details remain visible only to their owner.':
      'この履歴はアカウントのアクセスレベルが低いため表示されています。認証情報の詳細は所有者だけに表示されます。',
    'The credential is shown only after confirmation and is never added to chat history.':
      '認証情報は確認後にのみ表示され、チャット履歴には追加されません。',
    'Create an API key first, then confirm the CC Switch import. The browser builds the link from the selected model and service root; the key never enters assistant chat.':
      'まず API キーを作成し、CC Switch へのインポートを確認してください。ブラウザーが選択したモデルとサービスルートからリンクを作成し、キーがアシスタントのチャットに入ることはありません。',
    'Click Import to CC Switch, then review and confirm the import dialog.':
      '「CC Switch にインポート」をクリックし、インポート確認ダイアログを確認して承認してください。',
    'Please enter a message.': 'メッセージを入力してください。',
    'Please enter a message other than a single punctuation mark.':
      '句読点1文字だけではないメッセージを入力してください。',
    'The request is already in the administrator queue. An AI recommendation is optional and may be added after you continue the conversation.':
      '申請はすでに管理者キューに入っています。AI 推薦文は任意で、会話を続けた後に追加できます。',
    'You can submit for administrator review without an AI recommendation. The recommendation only gives the reviewer more context; it never decides access.':
      'AI 推薦文なしで管理者審査に提出できます。推薦文は審査の参考情報であり、アクセス可否を決めるものではありません。',
    'Bind an email, enable 2FA, or set up a Passkey in your profile to unlock sensitive operations.':
      'プロフィールでメールアドレスを連携し、2FA を有効にするか Passkey を設定すると、機密操作を利用できます。',
    'Enter the code from your authenticator app or a backup code.':
      '認証アプリのコードまたはバックアップコードを入力してください。',
    'Enter verification code or backup code':
      '確認コードまたはバックアップコードを入力してください',
  },
  ru: {
    'Sensitive details are hidden until confirmation and remain visible only to you.':
      'Чувствительные данные скрыты до подтверждения и после него видны только вам.',
    'Do not send personal information, passwords, API keys, or credentials in chat. Site-issued credentials are shown only after your explicit confirmation, remain visible only to you, and stay out of the assistant context.':
      'Не отправляйте в чат личные данные, пароли, API-ключи или учётные данные. Выданные сайтом учётные данные показываются только после явного подтверждения, видны только вам и не попадают в контекст помощника.',
    'This history is available because the account has a lower access level. Credential details remain visible only to their owner.':
      'Эта история доступна из-за более низкого уровня доступа аккаунта. Сведения об учётных данных видны только их владельцу.',
    'The credential is shown only after confirmation and is never added to chat history.':
      'Учётные данные показываются только после подтверждения и никогда не добавляются в историю чата.',
    'Create an API key first, then confirm the CC Switch import. The browser builds the link from the selected model and service root; the key never enters assistant chat.':
      'Сначала создайте API-ключ, затем подтвердите импорт в CC Switch. Браузер создаёт ссылку на основе выбранной модели и корня сервиса; ключ никогда не попадает в чат помощника.',
    'Click Import to CC Switch, then review and confirm the import dialog.':
      'Нажмите «Импорт в CC Switch», затем проверьте и подтвердите диалог импорта.',
    'Please enter a message.': 'Введите сообщение.',
    'Please enter a message other than a single punctuation mark.':
      'Введите сообщение, состоящее не только из одного знака препинания.',
    'The request is already in the administrator queue. An AI recommendation is optional and may be added after you continue the conversation.':
      'Запрос уже находится в очереди администратора. Рекомендация ИИ необязательна и может быть добавлена после продолжения диалога.',
    'You can submit for administrator review without an AI recommendation. The recommendation only gives the reviewer more context; it never decides access.':
      'Можно отправить запрос администратору без рекомендации ИИ. Рекомендация лишь даёт проверяющему дополнительный контекст и не определяет доступ.',
    'Bind an email, enable 2FA, or set up a Passkey in your profile to unlock sensitive operations.':
      'Привяжите электронную почту, включите 2FA или настройте Passkey в профиле, чтобы разблокировать чувствительные операции.',
    'Enter the code from your authenticator app or a backup code.':
      'Введите код из приложения-аутентификатора или резервный код.',
    'Enter verification code or backup code':
      'Введите код подтверждения или резервный код',
  },
  vi: {
    'Sensitive details are hidden until confirmation and remain visible only to you.':
      'Thông tin nhạy cảm được ẩn cho đến khi xác nhận và chỉ hiển thị với bạn.',
    'Do not send personal information, passwords, API keys, or credentials in chat. Site-issued credentials are shown only after your explicit confirmation, remain visible only to you, and stay out of the assistant context.':
      'Không gửi thông tin cá nhân, mật khẩu, API key hoặc thông tin xác thực trong cuộc trò chuyện. Thông tin xác thực do trang cấp chỉ hiển thị sau khi bạn xác nhận rõ ràng, chỉ bạn có thể xem và không đi vào ngữ cảnh của trợ lý.',
    'This history is available because the account has a lower access level. Credential details remain visible only to their owner.':
      'Lịch sử này khả dụng vì tài khoản có cấp truy cập thấp hơn. Chi tiết thông tin xác thực chỉ hiển thị với chủ sở hữu.',
    'The credential is shown only after confirmation and is never added to chat history.':
      'Thông tin xác thực chỉ hiển thị sau khi xác nhận và không bao giờ được thêm vào lịch sử trò chuyện.',
    'Create an API key first, then confirm the CC Switch import. The browser builds the link from the selected model and service root; the key never enters assistant chat.':
      'Trước tiên hãy tạo API key, sau đó xác nhận việc nhập vào CC Switch. Trình duyệt tạo liên kết từ model và URL gốc của dịch vụ đã chọn; key không bao giờ đi vào cuộc trò chuyện với trợ lý.',
    'Click Import to CC Switch, then review and confirm the import dialog.':
      'Nhấp vào Nhập vào CC Switch, sau đó kiểm tra và xác nhận hộp thoại nhập.',
    'Please enter a message.': 'Vui lòng nhập tin nhắn.',
    'Please enter a message other than a single punctuation mark.':
      'Vui lòng nhập tin nhắn không chỉ gồm một dấu câu.',
    'The request is already in the administrator queue. An AI recommendation is optional and may be added after you continue the conversation.':
      'Yêu cầu đã nằm trong hàng đợi quản trị viên. Đề xuất AI là tùy chọn và có thể được thêm sau khi bạn tiếp tục cuộc trò chuyện.',
    'You can submit for administrator review without an AI recommendation. The recommendation only gives the reviewer more context; it never decides access.':
      'Bạn có thể gửi yêu cầu để quản trị viên xét duyệt mà không cần đề xuất AI. Đề xuất chỉ cung cấp thêm ngữ cảnh và không quyết định quyền truy cập.',
    'Bind an email, enable 2FA, or set up a Passkey in your profile to unlock sensitive operations.':
      'Hãy liên kết email, bật 2FA hoặc thiết lập Passkey trong hồ sơ để mở khóa các thao tác nhạy cảm.',
    'Enter the code from your authenticator app or a backup code.':
      'Nhập mã từ ứng dụng xác thực hoặc mã dự phòng.',
    'Enter verification code or backup code':
      'Nhập mã xác minh hoặc mã dự phòng',
  },
}

for (const [locale, translations] of Object.entries(
  assistantAndSecurityTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

for (const [locale, translations] of Object.entries(
  channelMarketplaceTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

// Routing/review strings are kept in one small shared vocabulary so every
// locale gets a complete key set even when a new marketplace control ships.
// The Chinese copy is overridden below; other locales intentionally fall back
// to the stable English label until a native translation is supplied.
const channelRoutingFallback = {
  'Channel routing': 'Channel routing',
  'Configure your own public pool. It affects only your requests; administrator routing priority remains unchanged.':
    'Configure your own public pool. It affects only your requests; administrator routing priority remains unchanged.',
  'Save routing': 'Save routing',
  Disabled: 'Disabled',
  Enabled: 'Enabled',
  'Move up': 'Move up',
  'Move down': 'Move down',
  'No linked public channels yet.': 'No linked public channels yet.',
  'Top rated': 'Top rated',
  'Recently updated': 'Recently updated',
  'Most models': 'Most models',
  'No models listed': 'No models listed',
  'Review channel': 'Review channel',
  Rating: 'Rating',
  'Write a comment (optional)': 'Write a comment (optional)',
  'Recent comments': 'Recent comments',
  'No reviews yet.': 'No reviews yet.',
  'Submit review': 'Submit review',
  'Review submitted': 'Review submitted',
  'Routing preferences saved': 'Routing preferences saved',
  'Tip contributor': 'Tip contributor',
  'Use your balance to thank this contributor. Tips are transferred immediately and cannot be reversed.':
    'Use your balance to thank this contributor. Tips are transferred immediately and cannot be reversed.',
  'Tip amount': 'Tip amount',
  'Custom tip amount': 'Custom tip amount',
  'Message (optional)': 'Message (optional)',
  'Leave a short thank-you message': 'Leave a short thank-you message',
  'Send tip': 'Send tip',
  'Tip sent': 'Tip sent',
  Tips: 'Tips',
  'Withdraw tips': 'Withdraw tips',
  'Tips withdrawn': 'Tips withdrawn',
  'Move available tips into your balance. Choose the group you want to use for future requests.':
    'Move available tips into your balance. Choose the group you want to use for future requests.',
  'Target group': 'Target group',
  'Select a group': 'Select a group',
  Withdraw: 'Withdraw',
  'Group warning': 'Group warning',
  'Confirmation {{current}} of {{total}}':
    'Confirmation {{current}} of {{total}}',
  'I understand, continue': 'I understand, continue',
}
for (const locale of Object.keys(newKeys)) {
  Object.assign(newKeys[locale], channelRoutingFallback)
}
Object.assign(newKeys.zh, {
  'Channel routing': '渠道路由',
  'Configure your own public pool. It affects only your requests; administrator routing priority remains unchanged.':
    '配置你自己的公开渠道池，只影响你的请求，不会改变管理员设置的全局路由优先级。',
  'Save routing': '保存路由',
  Disabled: '已禁用',
  Enabled: '已启用',
  'Move up': '上移',
  'Move down': '下移',
  'No linked public channels yet.': '还没有已关联的公开渠道。',
  'Top rated': '评分最高',
  'Recently updated': '最近更新',
  'Most models': '模型最多',
  'No models listed': '未列出模型',
  'Review channel': '评价渠道',
  Rating: '评分',
  'Write a comment (optional)': '写下评论（可选）',
  'Recent comments': '最近评论',
  'No reviews yet.': '暂无评论。',
  'Submit review': '提交评价',
  'Review submitted': '评价已提交',
  'Routing preferences saved': '路由偏好已保存',
  'Tip contributor': '打赏分享者',
  'Use your balance to thank this contributor. Tips are transferred immediately and cannot be reversed.':
    '使用你的余额感谢分享者。打赏会立即转账，且无法撤回。',
  'Tip amount': '打赏金额',
  'Custom tip amount': '自定义金额',
  'Message (optional)': '留言（可选）',
  'Leave a short thank-you message': '写一句感谢的话',
  'Send tip': '发送打赏',
  'Tip sent': '打赏已发送',
  Tips: '打赏收入',
  'Withdraw tips': '提取打赏',
  'Tips withdrawn': '打赏已提取',
  'Move available tips into your balance. Choose the group you want to use for future requests.':
    '将可用打赏转入你的余额，并选择之后使用的分组。',
  'Target group': '目标分组',
  'Select a group': '选择分组',
  Withdraw: '提取',
  'Group warning': '分组警告',
  'Confirmation {{current}} of {{total}}': '第 {{current}}/{{total}} 次确认',
  'I understand, continue': '我已了解，继续',
})

const platformSkillTranslations = {
  en: {
    'Platform skill files': 'Platform skill files',
    'These are bounded virtual files shared by the platform assistant. They never grant filesystem or tool permissions.':
      'These are bounded virtual files shared by the platform assistant. They never grant filesystem or tool permissions.',
    'Add file': 'Add file',
    'No platform skill files yet.': 'No platform skill files yet.',
    Off: 'Off',
    'Skill file path': 'Skill file path',
    'Delete skill file': 'Delete skill file',
    'Skill file content': 'Skill file content',
    'Use this platform skill': 'Use this platform skill',
    'Maximum 32 files / 32000 characters total':
      'Maximum 32 files / 32000 characters total',
    'Add a file to edit a platform skill.':
      'Add a file to edit a platform skill.',
  },
  zh: {
    'Platform skill files': '平台技能文件',
    'These are bounded virtual files shared by the platform assistant. They never grant filesystem or tool permissions.':
      '这些是平台助手共享的受限虚拟文件，不会授予文件系统或工具权限。',
    'Add file': '添加文件',
    'No platform skill files yet.': '暂时没有平台技能文件。',
    Off: '停用',
    'Skill file path': '技能文件路径',
    'Delete skill file': '删除技能文件',
    'Skill file content': '技能文件内容',
    'Use this platform skill': '启用此平台技能',
    'Maximum 32 files / 32000 characters total':
      '最多 32 个文件 / 总计 32000 个字符',
    'Add a file to edit a platform skill.': '添加文件后即可编辑平台技能。',
  },
  'zh-TW': {
    'Platform skill files': '平台技能檔案',
    'These are bounded virtual files shared by the platform assistant. They never grant filesystem or tool permissions.':
      '這些是平台助手共用的受限虛擬檔案，不會授予檔案系統或工具權限。',
    'Add file': '新增檔案',
    'No platform skill files yet.': '目前沒有平台技能檔案。',
    Off: '停用',
    'Skill file path': '技能檔案路徑',
    'Delete skill file': '刪除技能檔案',
    'Skill file content': '技能檔案內容',
    'Use this platform skill': '啟用此平台技能',
    'Maximum 32 files / 32000 characters total':
      '最多 32 個檔案 / 共 32000 個字元',
    'Add a file to edit a platform skill.': '新增檔案後即可編輯平台技能。',
  },
  fr: {
    'Platform skill files': 'Fichiers de compétences de la plateforme',
    'These are bounded virtual files shared by the platform assistant. They never grant filesystem or tool permissions.':
      "Ces fichiers virtuels limités sont partagés par l'assistant de la plateforme. Ils n'accordent aucun accès aux fichiers ni aux outils.",
    'Add file': 'Ajouter un fichier',
    'No platform skill files yet.':
      "Aucun fichier de compétence pour l'instant.",
    Off: 'Désactivé',
    'Skill file path': 'Chemin du fichier de compétence',
    'Delete skill file': 'Supprimer le fichier de compétence',
    'Skill file content': 'Contenu du fichier de compétence',
    'Use this platform skill': 'Utiliser cette compétence',
    'Maximum 32 files / 32000 characters total':
      'Maximum 32 fichiers / 32000 caractères au total',
    'Add a file to edit a platform skill.':
      'Ajoutez un fichier pour modifier une compétence.',
  },
  ja: {
    'Platform skill files': 'プラットフォームスキルファイル',
    'These are bounded virtual files shared by the platform assistant. They never grant filesystem or tool permissions.':
      'プラットフォーム助手が共有する上限付きの仮想ファイルです。ファイルシステムやツールの権限は付与しません。',
    'Add file': 'ファイルを追加',
    'No platform skill files yet.':
      'プラットフォームスキルファイルはまだありません。',
    Off: '無効',
    'Skill file path': 'スキルファイルのパス',
    'Delete skill file': 'スキルファイルを削除',
    'Skill file content': 'スキルファイルの内容',
    'Use this platform skill': 'このプラットフォームスキルを使用',
    'Maximum 32 files / 32000 characters total':
      '最大 32 ファイル / 合計 32000 文字',
    'Add a file to edit a platform skill.':
      'ファイルを追加するとスキルを編集できます。',
  },
  ru: {
    'Platform skill files': 'Файлы навыков платформы',
    'These are bounded virtual files shared by the platform assistant. They never grant filesystem or tool permissions.':
      'Это ограниченные виртуальные файлы общего помощника платформы. Они не дают доступа к файлам или инструментам.',
    'Add file': 'Добавить файл',
    'No platform skill files yet.': 'Файлов навыков платформы пока нет.',
    Off: 'Выкл.',
    'Skill file path': 'Путь к файлу навыка',
    'Delete skill file': 'Удалить файл навыка',
    'Skill file content': 'Содержимое файла навыка',
    'Use this platform skill': 'Использовать этот навык',
    'Maximum 32 files / 32000 characters total':
      'Не более 32 файлов / 32000 символов всего',
    'Add a file to edit a platform skill.':
      'Добавьте файл, чтобы изменить навык платформы.',
  },
  vi: {
    'Platform skill files': 'Tệp kỹ năng nền tảng',
    'These are bounded virtual files shared by the platform assistant. They never grant filesystem or tool permissions.':
      'Đây là các tệp ảo có giới hạn được trợ lý nền tảng dùng chung. Chúng không cấp quyền tệp hoặc công cụ.',
    'Add file': 'Thêm tệp',
    'No platform skill files yet.': 'Chưa có tệp kỹ năng nền tảng.',
    Off: 'Tắt',
    'Skill file path': 'Đường dẫn tệp kỹ năng',
    'Delete skill file': 'Xóa tệp kỹ năng',
    'Skill file content': 'Nội dung tệp kỹ năng',
    'Use this platform skill': 'Dùng kỹ năng nền tảng này',
    'Maximum 32 files / 32000 characters total':
      'Tối đa 32 tệp / tổng cộng 32000 ký tự',
    'Add a file to edit a platform skill.':
      'Thêm tệp để chỉnh sửa kỹ năng nền tảng.',
  },
}
for (const [locale, translations] of Object.entries(
  platformSkillTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

for (const [locale, translations] of Object.entries(todoTranslations)) {
  Object.assign(newKeys[locale], translations)
}

const drawingLayoutTranslations = {
  en: {
    'Be specific about the subject, mood, and style.':
      'Be specific about the subject, mood, and style.',
    'Generated images': 'Generated images',
    'Describe an image, choose a group, and generate a preview.':
      'Describe an image, choose a group, and generate a preview.',
    'Search by username, name, or email': 'Search by username, name, or email',
  },
  zh: {
    'Be specific about the subject, mood, and style.':
      '可以补充主体、氛围和风格，让结果更贴近你的想法。',
    'Generated images': '生成结果',
    'Describe an image, choose a group, and generate a preview.':
      '描述图片，选择分组，然后生成预览。',
    'Search by username, name, or email': '按用户名、姓名或邮箱搜索',
  },
  'zh-TW': {
    'Be specific about the subject, mood, and style.':
      '可以補充主體、氛圍和風格，讓結果更貼近你的想法。',
    'Generated images': '生成結果',
    'Describe an image, choose a group, and generate a preview.':
      '描述圖片、選擇分組，然後生成預覽。',
    'Search by username, name, or email': '按使用者名稱、姓名或電子郵件搜尋',
  },
  fr: {
    'Be specific about the subject, mood, and style.':
      'Précisez le sujet, l’ambiance et le style.',
    'Generated images': 'Images générées',
    'Describe an image, choose a group, and generate a preview.':
      'Décrivez une image, choisissez un groupe, puis générez un aperçu.',
    'Search by username, name, or email':
      "Rechercher par nom d'utilisateur, nom ou e-mail",
  },
  ja: {
    'Be specific about the subject, mood, and style.':
      '被写体、雰囲気、スタイルを具体的に指定してください。',
    'Generated images': '生成結果',
    'Describe an image, choose a group, and generate a preview.':
      '画像を説明し、グループを選んでプレビューを生成します。',
    'Search by username, name, or email':
      'ユーザー名、氏名、メールアドレスで検索',
  },
  ru: {
    'Be specific about the subject, mood, and style.':
      'Уточните объект, настроение и стиль.',
    'Generated images': 'Созданные изображения',
    'Describe an image, choose a group, and generate a preview.':
      'Опишите изображение, выберите группу и создайте предварительный просмотр.',
    'Search by username, name, or email':
      'Поиск по имени пользователя, имени или электронной почте',
  },
  vi: {
    'Be specific about the subject, mood, and style.':
      'Hãy nêu rõ chủ thể, không khí và phong cách.',
    'Generated images': 'Ảnh đã tạo',
    'Describe an image, choose a group, and generate a preview.':
      'Mô tả hình ảnh, chọn nhóm rồi tạo bản xem trước.',
    'Search by username, name, or email':
      'Tìm theo tên người dùng, tên hoặc email',
  },
}
for (const [locale, translations] of Object.entries(
  drawingLayoutTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const registrationChannelTranslations = {
  en: {
    'Registration channels': 'Registration channels',
    'Disable selected OAuth channels for new registrations only. Existing users can still sign in with them.':
      'Disable selected OAuth channels for new registrations only. Existing users can still sign in with them.',
    'Allow new accounts through {{method}}':
      'Allow new accounts through {{method}}',
  },
  zh: {
    'Registration channels': '注册渠道',
    'Disable selected OAuth channels for new registrations only. Existing users can still sign in with them.':
      '仅对新注册停用所选 OAuth 渠道，现有用户仍可使用这些渠道登录。',
    'Allow new accounts through {{method}}': '允许通过 {{method}} 创建新账号',
  },
  'zh-TW': {
    'Registration channels': '註冊管道',
    'Disable selected OAuth channels for new registrations only. Existing users can still sign in with them.':
      '僅對新註冊停用所選 OAuth 管道，現有使用者仍可使用這些管道登入。',
    'Allow new accounts through {{method}}': '允許透過 {{method}} 建立新帳號',
  },
  fr: {
    'Registration channels': "Canaux d'inscription",
    'Disable selected OAuth channels for new registrations only. Existing users can still sign in with them.':
      'Désactiver les canaux OAuth sélectionnés uniquement pour les nouvelles inscriptions. Les comptes existants peuvent toujours s’y connecter.',
    'Allow new accounts through {{method}}':
      'Autoriser la création de comptes via {{method}}',
  },
  ja: {
    'Registration channels': '登録チャネル',
    'Disable selected OAuth channels for new registrations only. Existing users can still sign in with them.':
      '選択した OAuth チャネルを新規登録にのみ無効化します。既存ユーザーは引き続きこれらのチャネルでログインできます。',
    'Allow new accounts through {{method}}':
      '{{method}} で新しいアカウントを作成可能',
  },
  ru: {
    'Registration channels': 'Каналы регистрации',
    'Disable selected OAuth channels for new registrations only. Existing users can still sign in with them.':
      'Отключает выбранные OAuth-каналы только для новых регистраций. Существующие пользователи по-прежнему могут входить через них.',
    'Allow new accounts through {{method}}':
      'Разрешить создание новых аккаунтов через {{method}}',
  },
  vi: {
    'Registration channels': 'Kênh đăng ký',
    'Disable selected OAuth channels for new registrations only. Existing users can still sign in with them.':
      'Tắt các kênh OAuth đã chọn chỉ đối với đăng ký mới. Người dùng hiện tại vẫn có thể đăng nhập bằng các kênh này.',
    'Allow new accounts through {{method}}':
      'Cho phép tạo tài khoản mới qua {{method}}',
  },
}
for (const [locale, translations] of Object.entries(
  registrationChannelTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const reasoningEffortTranslations = {
  en: {
    'Auto (model default)': 'Auto (model default)',
    'None (no reasoning)': 'None (no reasoning)',
    Low: 'Low',
    Medium: 'Medium',
    High: 'High',
    'Controls the default reasoning hint sent with assistant requests. Auto lets each model use its native default.':
      'Controls the default reasoning hint sent with assistant requests. Auto lets each model use its native default.',
  },
  zh: {
    'Auto (model default)': '自动（使用模型默认值）',
    'None (no reasoning)': '无（不启用思考）',
    Low: '低',
    Medium: '中',
    High: '高',
    'Controls the default reasoning hint sent with assistant requests. Auto lets each model use its native default.':
      '控制助手请求的默认思考提示。自动模式会使用每个模型的原生默认值。',
  },
  'zh-TW': {
    'Auto (model default)': '自動（使用模型預設值）',
    'None (no reasoning)': '無（不啟用推理）',
    Low: '低',
    Medium: '中',
    High: '高',
    'Controls the default reasoning hint sent with assistant requests. Auto lets each model use its native default.':
      '控制助手請求的預設推理提示。自動模式會使用每個模型的原生預設值。',
  },
  fr: {
    'Auto (model default)': 'Automatique (valeur du modèle)',
    'None (no reasoning)': 'Aucun (sans raisonnement)',
    Low: 'Faible',
    Medium: 'Moyen',
    High: 'Élevé',
    'Controls the default reasoning hint sent with assistant requests. Auto lets each model use its native default.':
      'Contrôle l’indication de raisonnement par défaut des requêtes. Le mode automatique utilise la valeur native de chaque modèle.',
  },
  ja: {
    'Auto (model default)': '自動（モデルの既定値）',
    'None (no reasoning)': 'なし（推論しない）',
    Low: '低',
    Medium: '中',
    High: '高',
    'Controls the default reasoning hint sent with assistant requests. Auto lets each model use its native default.':
      'アシスタント要求に送る既定の推論ヒントを制御します。自動では各モデルの既定値を使用します。',
  },
  ru: {
    'Auto (model default)': 'Авто (настройка модели)',
    'None (no reasoning)': 'Нет (без рассуждений)',
    Low: 'Низкая',
    Medium: 'Средняя',
    High: 'Высокая',
    'Controls the default reasoning hint sent with assistant requests. Auto lets each model use its native default.':
      'Задаёт подсказку для рассуждений в запросах помощника. Режим «Авто» использует значение модели.',
  },
  vi: {
    'Auto (model default)': 'Tự động (mặc định của mô hình)',
    'None (no reasoning)': 'Không (không suy luận)',
    Low: 'Thấp',
    Medium: 'Trung bình',
    High: 'Cao',
    'Controls the default reasoning hint sent with assistant requests. Auto lets each model use its native default.':
      'Điều khiển gợi ý suy luận mặc định trong yêu cầu trợ lý. Tự động sẽ dùng mặc định gốc của từng mô hình.',
  },
}
for (const [locale, translations] of Object.entries(
  reasoningEffortTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const requestReviewTranslations = {
  en: {
    Violations: 'Violations',
    'Assistant review logs': 'Assistant review logs',
    'Current violations': 'Current violations',
    'Unable to load review logs': 'Unable to load review logs',
    'Unable to reset violations': 'Unable to reset violations',
    'Violation count reset': 'Violation count reset',
    'No sampled reviews': 'No sampled reviews',
    Violation: 'Violation',
    'No violation': 'No violation',
    'Possible abuse': 'Possible abuse',
    'Default group': 'Default group',
    Rules: 'Rules',
    Explanation: 'Explanation',
    'Request preview': 'Request preview',
    'Resetting...': 'Resetting...',
    'Reset count': 'Reset count',
    'Per-request review probability (%)': 'Per-request review probability (%)',
    '0 disables sampled reviews. 1.0 means roughly one percent; reviews run in the background and never delay the response.':
      '0 disables sampled reviews. 1.0 means roughly one percent; reviews run in the background and never delay the response.',
    'Review model': 'Review model',
    'Select the routing group used by automatic reviews, then get its enabled model IDs.':
      'Select the routing group used by automatic reviews, then get its enabled model IDs.',
    'Automatic reviews send requests with this exact enabled model ID and the selected routing group.':
      'Automatic reviews send requests with this exact enabled model ID and the selected routing group.',
    'Controls the reasoning hint sent with automatic review requests. Auto lets each model use its native default.':
      'Controls the reasoning hint sent with automatic review requests. Auto lets each model use its native default.',
    'Use an exact billable model ID. The default is deepseek-v4-flash.':
      'Use an exact billable model ID. The default is deepseek-v4-flash.',
    'Per-group review policies': 'Per-group review policies',
    'Optional JSON keyed by routing group. Each value accepts probability 0–100 and intensity off, low, standard, or high. Unlisted groups use the global probability.':
      'Optional JSON keyed by routing group. Each value accepts probability 0–100 and intensity off, low, standard, or high. Unlisted groups use the global probability.',
  },
  zh: {
    Violations: '违规次数',
    'Assistant review logs': '助手审查日志',
    'Current violations': '当前违规次数',
    'Unable to load review logs': '无法加载审查日志',
    'Unable to reset violations': '无法重置违规次数',
    'Violation count reset': '违规次数已重置',
    'No sampled reviews': '暂无抽样审查记录',
    Violation: '违规',
    'No violation': '未发现违规',
    'Possible abuse': '可能滥用',
    'Default group': '默认分组',
    Rules: '规则',
    Explanation: '说明',
    'Request preview': '请求摘要',
    'Resetting...': '重置中……',
    'Reset count': '重置计数',
    'Per-request review probability (%)': '每请求审查概率（%）',
    '0 disables sampled reviews. 1.0 means roughly one percent; reviews run in the background and never delay the response.':
      '0 表示关闭抽样审查；1.0 约等于 1%。审查在后台运行，不会延迟响应。',
    'Review model': '审查模型',
    'Select the routing group used by automatic reviews, then get its enabled model IDs.':
      '选择自动审查使用的路由分组，然后获取该分组已启用的模型 ID。',
    'Automatic reviews send requests with this exact enabled model ID and the selected routing group.':
      '自动审查将使用这个已启用的准确模型 ID 和所选路由分组发送请求。',
    'Controls the reasoning hint sent with automatic review requests. Auto lets each model use its native default.':
      '控制自动审查请求发送的推理提示；auto 会让各模型使用原生默认值。',
    'Use an exact billable model ID. The default is deepseek-v4-flash.':
      '填写准确且已计费的模型 ID，默认使用 deepseek-v4-flash。',
    'Per-group review policies': '分组审查策略',
    'Optional JSON keyed by routing group. Each value accepts probability 0–100 and intensity off, low, standard, or high. Unlisted groups use the global probability.':
      '可选 JSON，键为路由分组。每项支持 0–100 的概率及 off、low、standard、high 强度；未列出的分组使用全局概率。',
  },
  'zh-TW': {
    Violations: '違規次數',
    'Assistant review logs': '助手審查日誌',
    'Current violations': '目前違規次數',
    'Unable to load review logs': '無法載入審查日誌',
    'Unable to reset violations': '無法重置違規次數',
    'Violation count reset': '違規次數已重置',
    'No sampled reviews': '尚無抽樣審查記錄',
    Violation: '違規',
    'No violation': '未發現違規',
    'Possible abuse': '可能濫用',
    'Default group': '預設分組',
    Rules: '規則',
    Explanation: '說明',
    'Request preview': '請求摘要',
    'Resetting...': '重置中……',
    'Reset count': '重置計數',
    'Per-request review probability (%)': '每次請求審查機率（%）',
    '0 disables sampled reviews. 1.0 means roughly one percent; reviews run in the background and never delay the response.':
      '0 表示停用抽樣審查；1.0 約等於 1%。審查在背景執行，不會延遲回應。',
    'Review model': '審查模型',
    'Select the routing group used by automatic reviews, then get its enabled model IDs.':
      '選擇自動審查使用的路由分組，然後取得該分組已啟用的模型 ID。',
    'Automatic reviews send requests with this exact enabled model ID and the selected routing group.':
      '自動審查會使用這個已啟用的準確模型 ID 與所選路由分組傳送請求。',
    'Controls the reasoning hint sent with automatic review requests. Auto lets each model use its native default.':
      '控制自動審查請求傳送的推理提示；auto 會讓各模型使用原生預設值。',
    'Use an exact billable model ID. The default is deepseek-v4-flash.':
      '請填寫準確且可計費的模型 ID，預設使用 deepseek-v4-flash。',
    'Per-group review policies': '分組審查策略',
    'Optional JSON keyed by routing group. Each value accepts probability 0–100 and intensity off, low, standard, or high. Unlisted groups use the global probability.':
      '可選 JSON，鍵為路由分組。每項支援 0–100 的機率及 off、low、standard、high 強度；未列出的分組使用全域機率。',
  },
  fr: {
    Violations: 'Infractions',
    'Assistant review logs': 'Journaux de contrôle de l’assistant',
    'Current violations': 'Infractions actuelles',
    'Unable to load review logs': 'Impossible de charger les journaux',
    'Unable to reset violations': 'Impossible de réinitialiser les infractions',
    'Violation count reset': 'Compteur d’infractions réinitialisé',
    'No sampled reviews': 'Aucun contrôle échantillonné',
    Violation: 'Infraction',
    'No violation': 'Aucune infraction',
    'Possible abuse': 'Abus possible',
    'Default group': 'Groupe par défaut',
    Rules: 'Règles',
    Explanation: 'Explication',
    'Request preview': 'Aperçu de la requête',
    'Resetting...': 'Réinitialisation…',
    'Reset count': 'Réinitialiser le compteur',
    'Per-request review probability (%)':
      'Probabilité de contrôle par requête (%)',
    '0 disables sampled reviews. 1.0 means roughly one percent; reviews run in the background and never delay the response.':
      '0 désactive les contrôles échantillonnés. 1,0 correspond à environ 1 % ; ils s’exécutent en arrière-plan sans retarder la réponse.',
    'Review model': 'Modèle de contrôle',
    'Select the routing group used by automatic reviews, then get its enabled model IDs.':
      'Sélectionnez le groupe de routage des contrôles automatiques, puis chargez ses identifiants de modèles actifs.',
    'Automatic reviews send requests with this exact enabled model ID and the selected routing group.':
      'Les contrôles automatiques envoient leurs requêtes avec cet identifiant de modèle actif exact et le groupe de routage sélectionné.',
    'Controls the reasoning hint sent with automatic review requests. Auto lets each model use its native default.':
      'Contrôle l’indication de raisonnement des requêtes de contrôle automatique ; auto laisse chaque modèle utiliser sa valeur native par défaut.',
    'Use an exact billable model ID. The default is deepseek-v4-flash.':
      'Utilisez un identifiant de modèle facturable exact. La valeur par défaut est deepseek-v4-flash.',
    'Per-group review policies': 'Politiques de contrôle par groupe',
    'Optional JSON keyed by routing group. Each value accepts probability 0–100 and intensity off, low, standard, or high. Unlisted groups use the global probability.':
      'JSON facultatif indexé par groupe de routage. Chaque valeur accepte une probabilité de 0 à 100 et une intensité off, low, standard ou high. Les groupes absents utilisent la probabilité globale.',
  },
  ja: {
    Violations: '違反回数',
    'Assistant review logs': 'アシスタント審査ログ',
    'Current violations': '現在の違反回数',
    'Unable to load review logs': '審査ログを読み込めません',
    'Unable to reset violations': '違反回数をリセットできません',
    'Violation count reset': '違反回数をリセットしました',
    'No sampled reviews': '抽出審査の記録はありません',
    Violation: '違反',
    'No violation': '違反なし',
    'Possible abuse': '不正利用の可能性',
    'Default group': '既定のグループ',
    Rules: 'ルール',
    Explanation: '説明',
    'Request preview': 'リクエスト概要',
    'Resetting...': 'リセット中…',
    'Reset count': '回数をリセット',
    'Per-request review probability (%)': 'リクエストごとの審査確率（%）',
    '0 disables sampled reviews. 1.0 means roughly one percent; reviews run in the background and never delay the response.':
      '0 で抽出審査を無効にします。1.0 は約 1% です。審査はバックグラウンドで実行され、応答を遅延させません。',
    'Review model': '審査モデル',
    'Select the routing group used by automatic reviews, then get its enabled model IDs.':
      '自動審査で使用するルーティンググループを選び、そのグループで有効なモデル ID を取得します。',
    'Automatic reviews send requests with this exact enabled model ID and the selected routing group.':
      '自動審査は、この有効な正確なモデル ID と選択したルーティンググループでリクエストを送信します。',
    'Controls the reasoning hint sent with automatic review requests. Auto lets each model use its native default.':
      '自動審査リクエストの推論ヒントを制御します。auto では各モデルのネイティブ既定値を使用します。',
    'Use an exact billable model ID. The default is deepseek-v4-flash.':
      '課金対象の正確なモデル ID を指定します。既定値は deepseek-v4-flash です。',
    'Per-group review policies': 'グループ別審査ポリシー',
    'Optional JSON keyed by routing group. Each value accepts probability 0–100 and intensity off, low, standard, or high. Unlisted groups use the global probability.':
      'ルーティンググループをキーにした任意の JSON です。確率 0～100 と強度 off、low、standard、high を指定できます。未指定のグループは全体の確率を使います。',
  },
  ru: {
    Violations: 'Нарушения',
    'Assistant review logs': 'Журналы проверки помощника',
    'Current violations': 'Текущие нарушения',
    'Unable to load review logs': 'Не удалось загрузить журналы',
    'Unable to reset violations': 'Не удалось сбросить нарушения',
    'Violation count reset': 'Счётчик нарушений сброшен',
    'No sampled reviews': 'Выборочных проверок нет',
    Violation: 'Нарушение',
    'No violation': 'Нарушений нет',
    'Possible abuse': 'Возможное злоупотребление',
    'Default group': 'Группа по умолчанию',
    Rules: 'Правила',
    Explanation: 'Пояснение',
    'Request preview': 'Предпросмотр запроса',
    'Resetting...': 'Сброс…',
    'Reset count': 'Сбросить счётчик',
    'Per-request review probability (%)': 'Вероятность проверки запроса (%)',
    '0 disables sampled reviews. 1.0 means roughly one percent; reviews run in the background and never delay the response.':
      '0 отключает выборочные проверки. 1,0 означает примерно 1%; проверки выполняются в фоне и не задерживают ответ.',
    'Review model': 'Модель проверки',
    'Select the routing group used by automatic reviews, then get its enabled model IDs.':
      'Выберите группу маршрутизации для автоматических проверок, затем загрузите включённые в ней идентификаторы моделей.',
    'Automatic reviews send requests with this exact enabled model ID and the selected routing group.':
      'Автоматические проверки отправляют запросы с этим точным идентификатором включённой модели и выбранной группой маршрутизации.',
    'Controls the reasoning hint sent with automatic review requests. Auto lets each model use its native default.':
      'Управляет подсказкой глубины рассуждений для автоматических проверок; auto оставляет нативное значение модели по умолчанию.',
    'Use an exact billable model ID. The default is deepseek-v4-flash.':
      'Укажите точный идентификатор оплачиваемой модели. По умолчанию используется deepseek-v4-flash.',
    'Per-group review policies': 'Политики проверки по группам',
    'Optional JSON keyed by routing group. Each value accepts probability 0–100 and intensity off, low, standard, or high. Unlisted groups use the global probability.':
      'Необязательный JSON с ключами групп маршрутизации. Для каждой записи задаются вероятность 0–100 и интенсивность off, low, standard или high. Для остальных групп используется общая вероятность.',
  },
  vi: {
    Violations: 'Số lần vi phạm',
    'Assistant review logs': 'Nhật ký kiểm duyệt trợ lý',
    'Current violations': 'Số vi phạm hiện tại',
    'Unable to load review logs': 'Không thể tải nhật ký kiểm duyệt',
    'Unable to reset violations': 'Không thể đặt lại số lần vi phạm',
    'Violation count reset': 'Đã đặt lại số lần vi phạm',
    'No sampled reviews': 'Chưa có kiểm duyệt lấy mẫu',
    Violation: 'Vi phạm',
    'No violation': 'Không vi phạm',
    'Possible abuse': 'Có thể lạm dụng',
    'Default group': 'Nhóm mặc định',
    Rules: 'Quy tắc',
    Explanation: 'Giải thích',
    'Request preview': 'Tóm tắt yêu cầu',
    'Resetting...': 'Đang đặt lại…',
    'Reset count': 'Đặt lại số lần',
    'Per-request review probability (%)': 'Xác suất kiểm duyệt mỗi yêu cầu (%)',
    '0 disables sampled reviews. 1.0 means roughly one percent; reviews run in the background and never delay the response.':
      '0 tắt kiểm duyệt lấy mẫu. 1.0 tương đương khoảng 1%; kiểm duyệt chạy nền và không làm chậm phản hồi.',
    'Review model': 'Model kiểm duyệt',
    'Select the routing group used by automatic reviews, then get its enabled model IDs.':
      'Chọn nhóm định tuyến dùng cho kiểm duyệt tự động, rồi tải các ID model đang bật của nhóm đó.',
    'Automatic reviews send requests with this exact enabled model ID and the selected routing group.':
      'Kiểm duyệt tự động gửi yêu cầu bằng đúng ID model đang bật này và nhóm định tuyến đã chọn.',
    'Controls the reasoning hint sent with automatic review requests. Auto lets each model use its native default.':
      'Điều khiển gợi ý mức suy luận cho yêu cầu kiểm duyệt tự động; auto để mỗi model dùng giá trị mặc định gốc.',
    'Use an exact billable model ID. The default is deepseek-v4-flash.':
      'Dùng đúng ID model có tính phí. Mặc định là deepseek-v4-flash.',
    'Per-group review policies': 'Chính sách kiểm duyệt theo nhóm',
    'Optional JSON keyed by routing group. Each value accepts probability 0–100 and intensity off, low, standard, or high. Unlisted groups use the global probability.':
      'JSON tùy chọn với khóa là nhóm định tuyến. Mỗi mục nhận xác suất 0–100 và cường độ off, low, standard hoặc high. Nhóm chưa liệt kê dùng xác suất toàn cục.',
  },
}

for (const [locale, translations] of Object.entries(
  requestReviewTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const assistantReviewSummaryTranslations = {
  en: {
    'No completed assistant review is available yet.':
      'No completed assistant review is available yet.',
    Profiles: 'Profiles',
    'Pending support': 'Pending support',
    'Clicks / conversations / approvals': 'Clicks / conversations / approvals',
    Commerce: 'Commerce',
    'Chat users': 'Chat users',
    'Paid users': 'Paid users',
    'Conversion rate': 'Conversion rate',
    Refunds: 'Refunds',
    'Security audit': 'Security audit',
    Matches: 'Matches',
    'This run contains aggregate assistant metrics only. Detailed security and commerce sections will appear after the backend update.':
      'This run contains aggregate assistant metrics only. Detailed security and commerce sections will appear after the backend update.',
    assistant_review: 'Assistant review',
    Decision: 'Decision',
  },
  zh: {
    'No completed assistant review is available yet.': '暂无已完成的 AI 复盘。',
    Profiles: '用户画像',
    'Pending support': '待处理客服',
    'Clicks / conversations / approvals': '点击 / 对话 / 推荐信 / 批准',
    Commerce: '业务转化',
    'Chat users': '对话用户',
    'Paid users': '付费用户',
    'Conversion rate': '转化率',
    Refunds: '退款',
    'Security audit': '安全审查',
    Matches: '匹配数',
    'This run contains aggregate assistant metrics only. Detailed security and commerce sections will appear after the backend update.':
      '本次复盘目前只有聚合指标；后端升级后会显示更详细的安全与业务数据。',
    assistant_review: 'AI 复盘',
    Decision: '判定',
  },
  'zh-TW': {
    'No completed assistant review is available yet.': '尚無已完成的 AI 複盤。',
    Profiles: '使用者畫像',
    'Pending support': '待處理客服',
    'Clicks / conversations / approvals': '點擊 / 對話 / 推薦信 / 核准',
    Commerce: '業務轉化',
    'Chat users': '對話使用者',
    'Paid users': '付費使用者',
    'Conversion rate': '轉化率',
    Refunds: '退款',
    'Security audit': '安全稽核',
    Matches: '匹配數',
    'This run contains aggregate assistant metrics only. Detailed security and commerce sections will appear after the backend update.':
      '本次複盤目前只有彙總指標；後端升級後會顯示更詳細的安全與業務資料。',
    assistant_review: 'AI 複盤',
    Decision: '判定',
  },
  fr: {
    'No completed assistant review is available yet.':
      'Aucune revue de l’assistant terminée pour le moment.',
    Profiles: 'Profils',
    'Pending support': 'Support en attente',
    'Clicks / conversations / approvals': 'Clics / conversations / validations',
    Commerce: 'Activité commerciale',
    'Chat users': 'Utilisateurs du chat',
    'Paid users': 'Utilisateurs payants',
    'Conversion rate': 'Taux de conversion',
    Refunds: 'Remboursements',
    'Security audit': 'Audit de sécurité',
    Matches: 'Correspondances',
    'This run contains aggregate assistant metrics only. Detailed security and commerce sections will appear after the backend update.':
      'Cette revue ne contient que des indicateurs agrégés ; les détails sécurité et commerce apparaîtront après la mise à jour du backend.',
    assistant_review: 'Revue de l’assistant',
    Decision: 'Décision',
  },
  ja: {
    'No completed assistant review is available yet.':
      '完了したアシスタントレビューはまだありません。',
    Profiles: 'ユーザープロファイル',
    'Pending support': '対応待ちサポート',
    'Clicks / conversations / approvals': 'クリック / 会話 / 推薦 / 承認',
    Commerce: '利用・購入状況',
    'Chat users': 'チャット利用者',
    'Paid users': '有料利用者',
    'Conversion rate': '転換率',
    Refunds: '返金',
    'Security audit': 'セキュリティ監査',
    Matches: '一致数',
    'This run contains aggregate assistant metrics only. Detailed security and commerce sections will appear after the backend update.':
      '今回のレビューは集計指標のみです。バックエンド更新後にセキュリティと利用状況の詳細が表示されます。',
    assistant_review: 'アシスタントレビュー',
    Decision: '判定',
  },
  ru: {
    'No completed assistant review is available yet.':
      'Завершённых проверок помощника пока нет.',
    Profiles: 'Профили',
    'Pending support': 'Ожидающая поддержка',
    'Clicks / conversations / approvals':
      'Клики / диалоги / рекомендации / одобрения',
    Commerce: 'Коммерция',
    'Chat users': 'Пользователи чата',
    'Paid users': 'Платящие пользователи',
    'Conversion rate': 'Конверсия',
    Refunds: 'Возвраты',
    'Security audit': 'Аудит безопасности',
    Matches: 'Совпадения',
    'This run contains aggregate assistant metrics only. Detailed security and commerce sections will appear after the backend update.':
      'Эта проверка содержит только агрегированные показатели; подробности безопасности и коммерции появятся после обновления backend.',
    assistant_review: 'Проверка помощника',
    Decision: 'Решение',
  },
  vi: {
    'No completed assistant review is available yet.':
      'Chưa có phiên đánh giá trợ lý nào hoàn tất.',
    Profiles: 'Hồ sơ',
    'Pending support': 'Hỗ trợ đang chờ',
    'Clicks / conversations / approvals':
      'Lượt nhấp / hội thoại / đề xuất / phê duyệt',
    Commerce: 'Thương mại',
    'Chat users': 'Người dùng trò chuyện',
    'Paid users': 'Người dùng trả phí',
    'Conversion rate': 'Tỷ lệ chuyển đổi',
    Refunds: 'Hoàn tiền',
    'Security audit': 'Kiểm toán bảo mật',
    Matches: 'Lượt khớp',
    'This run contains aggregate assistant metrics only. Detailed security and commerce sections will appear after the backend update.':
      'Lần đánh giá này chỉ có chỉ số tổng hợp; chi tiết bảo mật và thương mại sẽ xuất hiện sau khi backend được cập nhật.',
    assistant_review: 'Đánh giá trợ lý',
    Decision: 'Kết luận',
  },
}

for (const [locale, translations] of Object.entries(
  assistantReviewSummaryTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const sidebarPreferencesTranslations = {
  en: {
    'Sidebar density': 'Sidebar density',
    'Default page': 'Default page',
    'Use system default': 'Use system default',
    'Move section up': 'Move section up',
    'Move section down': 'Move section down',
    'Move item up': 'Move item up',
    'Move item down': 'Move item down',
    'Start here and review available work':
      'Start here and review available work',
    'Service guide and onboarding': 'Service guide and onboarding',
    'Browse shared channels': 'Browse shared channels',
    'Review pending tasks and notices': 'Review pending tasks and notices',
    'Challenges and community work': 'Challenges and community work',
    'Open challenges': 'Open challenges',
    'Review and manage conversations': 'Review and manage conversations',
    'Open a chat session': 'Open a chat session',
    'System overview': 'System overview',
    'Create and review images': 'Create and review images',
    'Balance and payment management': 'Balance and payment management',
    'Administrative tools': 'Administrative tools',
    'Manage channels': 'Manage channels',
    'Manage models': 'Manage models',
    'Manage users': 'Manage users',
    'Manage redemption codes': 'Manage redemption codes',
    'Manage discount codes': 'Manage discount codes',
    'Manage subscriptions': 'Manage subscriptions',
    'Inspect system information': 'Inspect system information',
    'Configure the service': 'Configure the service',
  },
  zh: {
    'Sidebar density': '侧栏密度',
    'Default page': '默认页面',
    'Use system default': '使用系统默认',
    'Move section up': '上移分组',
    'Move section down': '下移分组',
    'Move item up': '上移项目',
    'Move item down': '下移项目',
    'Start here and review available work': '从这里开始，查看可用功能',
    'Service guide and onboarding': '服务向导与入门引导',
    'Browse shared channels': '浏览共享渠道',
    'Review pending tasks and notices': '查看待办任务和通知',
    'Challenges and community work': '挑战与社区任务',
    'Open challenges': '公开挑战',
    'Review and manage conversations': '查看和管理对话',
    'Open a chat session': '打开聊天会话',
    'System overview': '系统概览',
    'Create and review images': '创建和查看图片',
    'Balance and payment management': '余额与支付管理',
    'Administrative tools': '管理工具',
    'Manage channels': '管理渠道',
    'Manage models': '管理模型',
    'Manage users': '管理用户',
    'Manage redemption codes': '管理兑换码',
    'Manage discount codes': '管理优惠码',
    'Manage subscriptions': '管理订阅',
    'Inspect system information': '查看系统信息',
    'Configure the service': '配置服务',
  },
  'zh-TW': {
    'Sidebar density': '側欄密度',
    'Default page': '預設頁面',
    'Use system default': '使用系統預設',
    'Move section up': '上移分組',
    'Move section down': '下移分組',
    'Move item up': '上移項目',
    'Move item down': '下移項目',
    'Start here and review available work': '從這裡開始，查看可用功能',
    'Service guide and onboarding': '服務導覽與入門引導',
    'Browse shared channels': '瀏覽共享渠道',
    'Review pending tasks and notices': '查看待辦任務與通知',
    'Challenges and community work': '挑戰與社群任務',
    'Open challenges': '公開挑戰',
    'Review and manage conversations': '查看與管理對話',
    'Open a chat session': '開啟聊天會話',
    'System overview': '系統概覽',
    'Create and review images': '建立與查看圖片',
    'Balance and payment management': '餘額與付款管理',
    'Administrative tools': '管理工具',
    'Manage channels': '管理渠道',
    'Manage models': '管理模型',
    'Manage users': '管理使用者',
    'Manage redemption codes': '管理兌換碼',
    'Manage discount codes': '管理折扣碼',
    'Manage subscriptions': '管理訂閱',
    'Inspect system information': '查看系統資訊',
    'Configure the service': '設定服務',
  },
  fr: {
    'Sidebar density': 'Densité de la barre latérale',
    'Default page': 'Page par défaut',
    'Use system default': 'Utiliser la valeur système',
    'Move section up': 'Monter la section',
    'Move section down': 'Descendre la section',
    'Move item up': 'Monter l’élément',
    'Move item down': 'Descendre l’élément',
    'Start here and review available work':
      'Commencer ici et voir le travail disponible',
    'Service guide and onboarding': 'Guide du service et démarrage',
    'Browse shared channels': 'Parcourir les canaux partagés',
    'Review pending tasks and notices':
      'Voir les tâches et notifications en attente',
    'Challenges and community work': 'Défis et travail communautaire',
    'Open challenges': 'Défis ouverts',
    'Review and manage conversations': 'Voir et gérer les conversations',
    'Open a chat session': 'Ouvrir une session de chat',
    'System overview': 'Vue d’ensemble du système',
    'Create and review images': 'Créer et consulter des images',
    'Balance and payment management': 'Solde et paiements',
    'Administrative tools': 'Outils d’administration',
    'Manage channels': 'Gérer les canaux',
    'Manage models': 'Gérer les modèles',
    'Manage users': 'Gérer les utilisateurs',
    'Manage redemption codes': 'Gérer les codes de rachat',
    'Manage discount codes': 'Gérer les codes promotionnels',
    'Manage subscriptions': 'Gérer les abonnements',
    'Inspect system information': 'Consulter les informations système',
    'Configure the service': 'Configurer le service',
  },
  ja: {
    'Sidebar density': 'サイドバーの密度',
    'Default page': '既定のページ',
    'Use system default': 'システム既定を使用',
    'Move section up': 'セクションを上へ移動',
    'Move section down': 'セクションを下へ移動',
    'Move item up': '項目を上へ移動',
    'Move item down': '項目を下へ移動',
    'Start here and review available work':
      'ここから始めて利用可能な機能を確認',
    'Service guide and onboarding': 'サービスガイドと初期設定',
    'Browse shared channels': '共有チャンネルを閲覧',
    'Review pending tasks and notices': '保留中のタスクと通知を確認',
    'Challenges and community work': 'チャレンジとコミュニティの作業',
    'Open challenges': '公開チャレンジ',
    'Review and manage conversations': '会話を確認・管理',
    'Open a chat session': 'チャットセッションを開く',
    'System overview': 'システム概要',
    'Create and review images': '画像を作成・確認',
    'Balance and payment management': '残高と支払いの管理',
    'Administrative tools': '管理ツール',
    'Manage channels': 'チャンネルを管理',
    'Manage models': 'モデルを管理',
    'Manage users': 'ユーザーを管理',
    'Manage redemption codes': '引き換えコードを管理',
    'Manage discount codes': '割引コードを管理',
    'Manage subscriptions': 'サブスクリプションを管理',
    'Inspect system information': 'システム情報を確認',
    'Configure the service': 'サービスを設定',
  },
  ru: {
    'Sidebar density': 'Плотность боковой панели',
    'Default page': 'Страница по умолчанию',
    'Use system default': 'Использовать системное значение',
    'Move section up': 'Переместить раздел вверх',
    'Move section down': 'Переместить раздел вниз',
    'Move item up': 'Переместить пункт вверх',
    'Move item down': 'Переместить пункт вниз',
    'Start here and review available work':
      'Начните здесь и просмотрите доступные функции',
    'Service guide and onboarding':
      'Руководство по сервису и начальная настройка',
    'Browse shared channels': 'Просмотреть общие каналы',
    'Review pending tasks and notices':
      'Просмотреть ожидающие задачи и уведомления',
    'Challenges and community work': 'Задания и работа сообщества',
    'Open challenges': 'Открытые задания',
    'Review and manage conversations': 'Просматривать и управлять диалогами',
    'Open a chat session': 'Открыть чат',
    'System overview': 'Обзор системы',
    'Create and review images': 'Создавать и просматривать изображения',
    'Balance and payment management': 'Баланс и платежи',
    'Administrative tools': 'Инструменты администрирования',
    'Manage channels': 'Управлять каналами',
    'Manage models': 'Управлять моделями',
    'Manage users': 'Управлять пользователями',
    'Manage redemption codes': 'Управлять кодами погашения',
    'Manage discount codes': 'Управлять кодами скидок',
    'Manage subscriptions': 'Управлять подписками',
    'Inspect system information': 'Просмотреть сведения о системе',
    'Configure the service': 'Настроить сервис',
  },
  vi: {
    'Sidebar density': 'Mật độ thanh bên',
    'Default page': 'Trang mặc định',
    'Use system default': 'Dùng mặc định của hệ thống',
    'Move section up': 'Đưa mục lên',
    'Move section down': 'Đưa mục xuống',
    'Move item up': 'Đưa mục con lên',
    'Move item down': 'Đưa mục con xuống',
    'Start here and review available work':
      'Bắt đầu tại đây và xem các tính năng khả dụng',
    'Service guide and onboarding': 'Hướng dẫn dịch vụ và bắt đầu sử dụng',
    'Browse shared channels': 'Duyệt các kênh được chia sẻ',
    'Review pending tasks and notices': 'Xem công việc và thông báo đang chờ',
    'Challenges and community work': 'Thử thách và công việc cộng đồng',
    'Open challenges': 'Thử thách mở',
    'Review and manage conversations': 'Xem và quản lý hội thoại',
    'Open a chat session': 'Mở phiên trò chuyện',
    'System overview': 'Tổng quan hệ thống',
    'Create and review images': 'Tạo và xem hình ảnh',
    'Balance and payment management': 'Quản lý số dư và thanh toán',
    'Administrative tools': 'Công cụ quản trị',
    'Manage channels': 'Quản lý kênh',
    'Manage models': 'Quản lý model',
    'Manage users': 'Quản lý người dùng',
    'Manage redemption codes': 'Quản lý mã đổi thưởng',
    'Manage discount codes': 'Quản lý mã giảm giá',
    'Manage subscriptions': 'Quản lý gói đăng ký',
    'Inspect system information': 'Xem thông tin hệ thống',
    'Configure the service': 'Cấu hình dịch vụ',
  },
}

for (const [locale, translations] of Object.entries(
  sidebarPreferencesTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const weeklyDiscountTranslations = {
  en: {
    'Weekly discount claimed': 'Weekly discount claimed',
    'Unable to claim weekly discount': 'Unable to claim weekly discount',
    "This week's decision is used": "This week's decision is used",
    'Claim discount code': 'Claim discount code',
    'Code hidden': 'Code hidden',
    'Discount code copied': 'Discount code copied',
    'Weekly recharge discount': 'Weekly recharge discount',
    'One claim per UTC week': 'One claim per UTC week',
    'Profit is unavailable for a payment-method filter':
      'Profit is unavailable for a payment-method filter',
    'Usage is unavailable for a payment-method filter':
      'Usage is unavailable for a payment-method filter',
  },
  fr: {
    'Weekly discount claimed': 'Remise hebdomadaire réclamée',
    'Unable to claim weekly discount':
      'Impossible de réclamer la remise hebdomadaire',
    "This week's decision is used": 'La décision de cette semaine est utilisée',
    'Claim discount code': 'Réclamer le code promo',
    'Code hidden': 'Code masqué',
    'Discount code copied': 'Code promo copié',
    'Weekly recharge discount': 'Remise de recharge hebdomadaire',
    'One claim per UTC week': 'Une réclamation par semaine UTC',
    'Profit is unavailable for a payment-method filter':
      'Le bénéfice est indisponible avec un filtre de moyen de paiement',
    'Usage is unavailable for a payment-method filter':
      "L'utilisation est indisponible avec un filtre de moyen de paiement",
  },
  ja: {
    'Weekly discount claimed': '毎週割引を受け取りました',
    'Unable to claim weekly discount': '毎週割引を受け取れません',
    "This week's decision is used": '今週の判定は使用済みです',
    'Claim discount code': '割引コードを受け取る',
    'Code hidden': 'コードは非表示です',
    'Discount code copied': '割引コードをコピーしました',
    'Weekly recharge discount': '毎週のチャージ割引',
    'One claim per UTC week': 'UTC週ごとに1回まで',
    'Profit is unavailable for a payment-method filter':
      '支払方法フィルター使用時は利益を表示できません',
    'Usage is unavailable for a payment-method filter':
      '支払方法フィルター使用時は使用量を表示できません',
  },
  ru: {
    'Weekly discount claimed': 'Еженедельная скидка получена',
    'Unable to claim weekly discount':
      'Не удалось получить еженедельную скидку',
    "This week's decision is used": 'Решение этой недели уже использовано',
    'Claim discount code': 'Получить код скидки',
    'Code hidden': 'Код скрыт',
    'Discount code copied': 'Код скидки скопирован',
    'Weekly recharge discount': 'Еженедельная скидка на пополнение',
    'One claim per UTC week': 'Один раз за неделю UTC',
    'Profit is unavailable for a payment-method filter':
      'При фильтре по способу оплаты прибыль недоступна',
    'Usage is unavailable for a payment-method filter':
      'При фильтре по способу оплаты использование недоступно',
  },
  vi: {
    'Weekly discount claimed': 'Đã nhận ưu đãi hàng tuần',
    'Unable to claim weekly discount': 'Không thể nhận ưu đãi hàng tuần',
    "This week's decision is used": 'Đã dùng quyết định của tuần này',
    'Claim discount code': 'Nhận mã giảm giá',
    'Code hidden': 'Mã đang ẩn',
    'Discount code copied': 'Đã sao chép mã giảm giá',
    'Weekly recharge discount': 'Ưu đãi nạp tiền hàng tuần',
    'One claim per UTC week': 'Mỗi tuần UTC chỉ nhận một lần',
    'Profit is unavailable for a payment-method filter':
      'Không thể tính lợi nhuận khi lọc theo phương thức thanh toán',
    'Usage is unavailable for a payment-method filter':
      'Không thể xác định mức sử dụng khi lọc theo phương thức thanh toán',
  },
  'zh-TW': {
    'Weekly discount claimed': '每週優惠已領取',
    'Unable to claim weekly discount': '無法領取每週優惠',
    "This week's decision is used": '本週評估已使用',
    'Claim discount code': '領取優惠碼',
    'Code hidden': '優惠碼暫不可見',
    'Discount code copied': '優惠碼已複製',
    'Weekly recharge discount': '每週充值折扣',
    'One claim per UTC week': '每個 UTC 週限領一次',
    'Profit is unavailable for a payment-method filter':
      '套用付款方式篩選時無法提供利潤',
    'Usage is unavailable for a payment-method filter':
      '套用付款方式篩選時無法提供用量',
  },
  zh: {
    'Weekly discount claimed': '每周优惠已领取',
    'Unable to claim weekly discount': '无法领取每周优惠',
    "This week's decision is used": '本周评估已使用',
    'Claim discount code': '领取优惠码',
    'Code hidden': '优惠码暂不可见',
    'Discount code copied': '优惠码已复制',
    'Weekly recharge discount': '每周充值折扣',
    'One claim per UTC week': '每个 UTC 周限领一次',
    'Profit is unavailable for a payment-method filter':
      '按支付方式筛选时无法计算利润',
    'Usage is unavailable for a payment-method filter':
      '按支付方式筛选时无法归因用量',
  },
}

for (const [locale, translations] of Object.entries(
  weeklyDiscountTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const ipAccessRoutingTranslations = {
  en: {
    'IP & Region Routing': 'IP & Region Routing',
    'At least one routing rule is required.':
      'At least one routing rule is required.',
    'Routing rules cannot exceed 16384 bytes.':
      'Routing rules cannot exceed 16384 bytes.',
    'Routing rules are invalid.': 'Routing rules are invalid.',
    'First matching rule wins': 'First matching rule wins',
    'Rules run from top to bottom; direct allows and reject blocks. Add fallback: direct or fallback: reject to set the default for unmatched requests; without it, unmatched requests use direct.':
      'Rules run from top to bottom; direct allows and reject blocks. Add fallback: direct or fallback: reject to set the default for unmatched requests; without it, unmatched requests use direct.',
    'Keep management access first': 'Keep management access first',
    'Put direct rules for trusted management IPs above broad reject rules so you do not lock yourself out.':
      'Put direct rules for trusted management IPs above broad reject rules so you do not lock yourself out.',
    'Routing rules': 'Routing rules',
    'Use one daed-style rule per line. Supported matchers: dip(IP, CIDR, geoip:xx, geoip:private), l4proto(tcp), and dport(port). Use # for comments.':
      'Use one daed-style rule per line. Supported matchers: dip(IP, CIDR, geoip:xx, geoip:private), l4proto(tcp), and dport(port). Use # for comments.',
    'Use Daed routing syntax. Matchers: domain/qname, dip/ip, sip, dport, sport, l4proto, ipversion, mac, pname, and dscp; supports ! negation, fallback, and direct/reject. Use # for comments.':
      'Use Daed routing syntax. Matchers: domain/qname, dip/ip, sip, dport, sport, l4proto, ipversion, mac, pname, and dscp; supports ! negation, fallback, and direct/reject. Use # for comments.',
    'Use one Daed-style rule per line. This HTTP edge supports domain(full/suffix/keyword/regex), dip/ip, sip, dport, sport, l4proto, ipversion, ! negation, fallback, and direct/reject. geosite/ext/qname/mac/pname/dscp need packet or DNS data and are rejected when saved. Use # for comments.':
      'Use one Daed-style rule per line. This HTTP edge supports domain(full/suffix/keyword/regex), dip/ip, sip, dport, sport, l4proto, ipversion, ! negation, fallback, and direct/reject. geosite/ext/qname/mac/pname/dscp need packet or DNS data and are rejected when saved. Use # for comments.',
  },
  zh: {
    'IP & Region Routing': 'IP 与地区路由',
    'At least one routing rule is required.': '至少需要一条路由规则。',
    'Routing rules cannot exceed 16384 bytes.': '路由规则不能超过 16384 字节。',
    'Routing rules are invalid.': '路由规则无效。',
    'First matching rule wins': '首条匹配规则生效',
    'Rules run from top to bottom; direct allows and reject blocks. Add fallback: direct or fallback: reject to set the default for unmatched requests; without it, unmatched requests use direct.':
      '规则从上到下执行；direct 允许请求，reject 拒绝请求。可使用 fallback: direct 或 fallback: reject 设置未命中规则时的默认行为；未设置时默认 direct。',
    'Keep management access first': '先保留管理访问',
    'Put direct rules for trusted management IPs above broad reject rules so you do not lock yourself out.':
      '请将可信管理 IP 的 direct 规则放在宽泛的 reject 规则之前，避免把自己锁在系统外。',
    'Routing rules': '路由规则',
    'Use one daed-style rule per line. Supported matchers: dip(IP, CIDR, geoip:xx, geoip:private), l4proto(tcp), and dport(port). Use # for comments.':
      '每行填写一条 daed 风格规则。支持 dip(IP、CIDR、geoip:xx、geoip:private)、l4proto(tcp) 和 dport(port)；使用 # 添加注释。',
    'Use Daed routing syntax. Matchers: domain/qname, dip/ip, sip, dport, sport, l4proto, ipversion, mac, pname, and dscp; supports ! negation, fallback, and direct/reject. Use # for comments.':
      '使用 Daed 路由语法。支持 domain/qname、dip/ip、sip、dport、sport、l4proto、ipversion、mac、pname、dscp；支持 ! 取反、fallback 以及 direct/reject。使用 # 添加注释。',
    'Use one Daed-style rule per line. This HTTP edge supports domain(full/suffix/keyword/regex), dip/ip, sip, dport, sport, l4proto, ipversion, ! negation, fallback, and direct/reject. geosite/ext/qname/mac/pname/dscp need packet or DNS data and are rejected when saved. Use # for comments.':
      '每行填写一条 Daed 风格规则。当前 HTTP 边缘支持 domain(full/suffix/keyword/regex)、dip/ip、sip、dport、sport、l4proto、ipversion、! 取反、fallback 以及 direct/reject。geosite/ext/qname/mac/pname/dscp 需要数据包或 DNS 数据，保存时会被拒绝。使用 # 添加注释。',
  },
  'zh-TW': {
    'IP & Region Routing': 'IP 與地區路由',
    'At least one routing rule is required.': '至少需要一條路由規則。',
    'Routing rules cannot exceed 16384 bytes.':
      '路由規則不能超過 16384 位元組。',
    'Routing rules are invalid.': '路由規則無效。',
    'First matching rule wins': '首條符合規則生效',
    'Rules run from top to bottom; direct allows and reject blocks. Add fallback: direct or fallback: reject to set the default for unmatched requests; without it, unmatched requests use direct.':
      '規則由上而下執行；direct 允許請求，reject 拒絕請求。可使用 fallback: direct 或 fallback: reject 設定未符合規則時的預設行為；未設定時預設 direct。',
    'Keep management access first': '先保留管理存取',
    'Put direct rules for trusted management IPs above broad reject rules so you do not lock yourself out.':
      '請將可信管理 IP 的 direct 規則放在廣泛的 reject 規則之前，避免將自己鎖在系統外。',
    'Routing rules': '路由規則',
    'Use one daed-style rule per line. Supported matchers: dip(IP, CIDR, geoip:xx, geoip:private), l4proto(tcp), and dport(port). Use # for comments.':
      '每行填寫一條 daed 風格規則。支援 dip(IP、CIDR、geoip:xx、geoip:private)、l4proto(tcp) 和 dport(port)；使用 # 加入註解。',
    'Use Daed routing syntax. Matchers: domain/qname, dip/ip, sip, dport, sport, l4proto, ipversion, mac, pname, and dscp; supports ! negation, fallback, and direct/reject. Use # for comments.':
      '使用 Daed 路由語法。支援 domain/qname、dip/ip、sip、dport、sport、l4proto、ipversion、mac、pname、dscp；支援 ! 取反、fallback 以及 direct/reject。使用 # 加入註解。',
    'Use one Daed-style rule per line. This HTTP edge supports domain(full/suffix/keyword/regex), dip/ip, sip, dport, sport, l4proto, ipversion, ! negation, fallback, and direct/reject. geosite/ext/qname/mac/pname/dscp need packet or DNS data and are rejected when saved. Use # for comments.':
      '每行填寫一條 Daed 風格規則。目前 HTTP 邊緣支援 domain(full/suffix/keyword/regex)、dip/ip、sip、dport、sport、l4proto、ipversion、! 取反、fallback 以及 direct/reject。geosite/ext/qname/mac/pname/dscp 需要封包或 DNS 資料，儲存時會被拒絕。使用 # 加入註解。',
  },
  fr: {
    'IP & Region Routing': 'Routage IP et régional',
    'At least one routing rule is required.':
      'Au moins une règle de routage est requise.',
    'Routing rules cannot exceed 16384 bytes.':
      'Les règles de routage ne peuvent pas dépasser 16 384 octets.',
    'Routing rules are invalid.': 'Les règles de routage sont invalides.',
    'First matching rule wins': 'La première règle correspondante s’applique',
    'Rules run from top to bottom; direct allows and reject blocks. Add fallback: direct or fallback: reject to set the default for unmatched requests; without it, unmatched requests use direct.':
      'Les règles sont évaluées de haut en bas : direct autorise et reject bloque. Ajoutez fallback: direct ou fallback: reject pour définir le comportement par défaut des requêtes sans correspondance ; sans cela, la valeur par défaut est direct.',
    'Keep management access first':
      'Préserver d’abord l’accès d’administration',
    'Put direct rules for trusted management IPs above broad reject rules so you do not lock yourself out.':
      'Placez les règles direct des IP d’administration approuvées avant les règles reject générales afin de ne pas bloquer votre propre accès.',
    'Routing rules': 'Règles de routage',
    'Use one daed-style rule per line. Supported matchers: dip(IP, CIDR, geoip:xx, geoip:private), l4proto(tcp), and dport(port). Use # for comments.':
      'Utilisez une règle de style daed par ligne. Prédicats pris en charge : dip(IP, CIDR, geoip:xx, geoip:private), l4proto(tcp) et dport(port). Utilisez # pour les commentaires.',
    'Use Daed routing syntax. Matchers: domain/qname, dip/ip, sip, dport, sport, l4proto, ipversion, mac, pname, and dscp; supports ! negation, fallback, and direct/reject. Use # for comments.':
      'Utilisez la syntaxe de routage Daed. Prédicats : domain/qname, dip/ip, sip, dport, sport, l4proto, ipversion, mac, pname et dscp ; la négation !, fallback et direct/reject sont pris en charge. Utilisez # pour les commentaires.',
    'Use one Daed-style rule per line. This HTTP edge supports domain(full/suffix/keyword/regex), dip/ip, sip, dport, sport, l4proto, ipversion, ! negation, fallback, and direct/reject. geosite/ext/qname/mac/pname/dscp need packet or DNS data and are rejected when saved. Use # for comments.':
      'Utilisez une règle Daed par ligne. Cette passerelle HTTP prend en charge domain(full/suffix/keyword/regex), dip/ip, sip, dport, sport, l4proto, ipversion, la négation !, fallback et direct/reject. geosite/ext/qname/mac/pname/dscp nécessitent des données de paquet ou DNS et sont refusés lors de l’enregistrement. Utilisez # pour les commentaires.',
  },
  ja: {
    'IP & Region Routing': 'IP・地域ルーティング',
    'At least one routing rule is required.':
      '少なくとも1つのルーティングルールが必要です。',
    'Routing rules cannot exceed 16384 bytes.':
      'ルーティングルールは16384バイト以内にしてください。',
    'Routing rules are invalid.': 'ルーティングルールが無効です。',
    'First matching rule wins': '最初に一致したルールを適用',
    'Rules run from top to bottom; direct allows and reject blocks. Add fallback: direct or fallback: reject to set the default for unmatched requests; without it, unmatched requests use direct.':
      'ルールは上から順に評価されます。direct は許可、reject は拒否です。未一致時の既定動作は fallback: direct または fallback: reject で指定でき、未指定時は direct になります。',
    'Keep management access first': '管理アクセスを先に確保',
    'Put direct rules for trusted management IPs above broad reject rules so you do not lock yourself out.':
      'ロックアウトを防ぐため、信頼済み管理IPの direct ルールを広範な reject ルールより上に配置してください。',
    'Routing rules': 'ルーティングルール',
    'Use one daed-style rule per line. Supported matchers: dip(IP, CIDR, geoip:xx, geoip:private), l4proto(tcp), and dport(port). Use # for comments.':
      '1行に1つの daed 形式ルールを記述します。対応条件は dip(IP, CIDR, geoip:xx, geoip:private)、l4proto(tcp)、dport(port) です。コメントには # を使用します。',
    'Use Daed routing syntax. Matchers: domain/qname, dip/ip, sip, dport, sport, l4proto, ipversion, mac, pname, and dscp; supports ! negation, fallback, and direct/reject. Use # for comments.':
      'Daed ルーティング構文を使用します。条件は domain/qname、dip/ip、sip、dport、sport、l4proto、ipversion、mac、pname、dscp に対応し、! の否定、fallback、direct/reject も使用できます。コメントには # を使います。',
    'Use one Daed-style rule per line. This HTTP edge supports domain(full/suffix/keyword/regex), dip/ip, sip, dport, sport, l4proto, ipversion, ! negation, fallback, and direct/reject. geosite/ext/qname/mac/pname/dscp need packet or DNS data and are rejected when saved. Use # for comments.':
      '1行に1つの Daed 形式ルールを記述します。この HTTP エッジは domain(full/suffix/keyword/regex)、dip/ip、sip、dport、sport、l4proto、ipversion、! の否定、fallback、direct/reject に対応します。geosite/ext/qname/mac/pname/dscp はパケットまたは DNS データが必要なため、保存時に拒否されます。コメントには # を使用します。',
  },
  ru: {
    'IP & Region Routing': 'Маршрутизация по IP и регионам',
    'At least one routing rule is required.':
      'Требуется хотя бы одно правило маршрутизации.',
    'Routing rules cannot exceed 16384 bytes.':
      'Правила маршрутизации не должны превышать 16 384 байта.',
    'Routing rules are invalid.': 'Правила маршрутизации недействительны.',
    'First matching rule wins': 'Применяется первое совпавшее правило',
    'Rules run from top to bottom; direct allows and reject blocks. Add fallback: direct or fallback: reject to set the default for unmatched requests; without it, unmatched requests use direct.':
      'Правила проверяются сверху вниз: direct разрешает, а reject блокирует запрос. Для поведения при отсутствии совпадения используйте fallback: direct или fallback: reject; без него применяется direct.',
    'Keep management access first': 'Сначала сохраните административный доступ',
    'Put direct rules for trusted management IPs above broad reject rules so you do not lock yourself out.':
      'Поместите правила direct для доверенных административных IP-адресов выше общих правил reject, чтобы не заблокировать себе доступ.',
    'Routing rules': 'Правила маршрутизации',
    'Use one daed-style rule per line. Supported matchers: dip(IP, CIDR, geoip:xx, geoip:private), l4proto(tcp), and dport(port). Use # for comments.':
      'Указывайте по одному правилу в стиле daed на строку. Поддерживаются dip(IP, CIDR, geoip:xx, geoip:private), l4proto(tcp) и dport(port). Для комментариев используйте #.',
    'Use Daed routing syntax. Matchers: domain/qname, dip/ip, sip, dport, sport, l4proto, ipversion, mac, pname, and dscp; supports ! negation, fallback, and direct/reject. Use # for comments.':
      'Используйте синтаксис маршрутизации Daed. Поддерживаются условия domain/qname, dip/ip, sip, dport, sport, l4proto, ipversion, mac и pname, dscp, а также отрицание !, fallback и direct/reject. Для комментариев используйте #.',
    'Use one Daed-style rule per line. This HTTP edge supports domain(full/suffix/keyword/regex), dip/ip, sip, dport, sport, l4proto, ipversion, ! negation, fallback, and direct/reject. geosite/ext/qname/mac/pname/dscp need packet or DNS data and are rejected when saved. Use # for comments.':
      'Указывайте по одному правилу Daed на строку. Этот HTTP-шлюз поддерживает domain(full/suffix/keyword/regex), dip/ip, sip, dport, sport, l4proto, ipversion, отрицание !, fallback и direct/reject. Для geosite/ext/qname/mac/pname/dscp нужны данные пакетов или DNS, поэтому при сохранении они отклоняются. Для комментариев используйте #.',
  },
  vi: {
    'IP & Region Routing': 'Định tuyến IP và khu vực',
    'At least one routing rule is required.':
      'Cần ít nhất một quy tắc định tuyến.',
    'Routing rules cannot exceed 16384 bytes.':
      'Quy tắc định tuyến không được vượt quá 16384 byte.',
    'Routing rules are invalid.': 'Quy tắc định tuyến không hợp lệ.',
    'First matching rule wins': 'Áp dụng quy tắc khớp đầu tiên',
    'Rules run from top to bottom; direct allows and reject blocks. Add fallback: direct or fallback: reject to set the default for unmatched requests; without it, unmatched requests use direct.':
      'Quy tắc được xét từ trên xuống; direct cho phép và reject chặn yêu cầu. Dùng fallback: direct hoặc fallback: reject để đặt hành vi mặc định khi không khớp; nếu bỏ qua thì mặc định là direct.',
    'Keep management access first': 'Ưu tiên giữ quyền truy cập quản trị',
    'Put direct rules for trusted management IPs above broad reject rules so you do not lock yourself out.':
      'Đặt quy tắc direct cho IP quản trị tin cậy phía trên các quy tắc reject rộng để tránh tự khóa quyền truy cập.',
    'Routing rules': 'Quy tắc định tuyến',
    'Use one daed-style rule per line. Supported matchers: dip(IP, CIDR, geoip:xx, geoip:private), l4proto(tcp), and dport(port). Use # for comments.':
      'Mỗi dòng dùng một quy tắc kiểu daed. Hỗ trợ dip(IP, CIDR, geoip:xx, geoip:private), l4proto(tcp) và dport(port). Dùng # cho chú thích.',
    'Use Daed routing syntax. Matchers: domain/qname, dip/ip, sip, dport, sport, l4proto, ipversion, mac, pname, and dscp; supports ! negation, fallback, and direct/reject. Use # for comments.':
      'Sử dụng cú pháp định tuyến Daed. Hỗ trợ các điều kiện domain/qname, dip/ip, sip, dport, sport, l4proto, ipversion, mac, pname và dscp; hỗ trợ phủ định !, fallback và direct/reject. Dùng # cho chú thích.',
    'Use one Daed-style rule per line. This HTTP edge supports domain(full/suffix/keyword/regex), dip/ip, sip, dport, sport, l4proto, ipversion, ! negation, fallback, and direct/reject. geosite/ext/qname/mac/pname/dscp need packet or DNS data and are rejected when saved. Use # for comments.':
      'Mỗi dòng dùng một quy tắc Daed. Edge HTTP này hỗ trợ domain(full/suffix/keyword/regex), dip/ip, sip, dport, sport, l4proto, ipversion, phủ định !, fallback và direct/reject. geosite/ext/qname/mac/pname/dscp cần dữ liệu gói tin hoặc DNS nên sẽ bị từ chối khi lưu. Dùng # cho chú thích.',
  },
}

for (const [locale, translations] of Object.entries(
  ipAccessRoutingTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const remainingStaticKeyTranslations = {
  en: {
    'Add the public description and confirm that this channel can be shared.':
      'Add the public description and confirm that this channel can be shared.',
    Approve: 'Approve',
    Canvas: 'Canvas',
    'Community rankings': 'Community rankings',
    'Complete the full channel configuration below. The submission remains pending until an administrator approves it.':
      'Complete the full channel configuration below. The submission remains pending until an administrator approves it.',
    'Configure per-group warnings and acknowledgement count (1–3). Use modal for the most prominent warning.':
      'Configure per-group warnings and acknowledgement count (1–3). Use modal for the most prominent warning.',
    Contributor: 'Contributor',
    'I confirm that these credentials are authorized for this shared channel.':
      'I confirm that these credentials are authorized for this shared channel.',
    Inspector: 'Inspector',
    'Settings saved': 'Settings saved',
    'Sharing information': 'Sharing information',
    'This information is shown in the public channel market after approval.':
      'This information is shown in the public channel market after approval.',
    'Top-up amount': 'Top-up amount',
    'Unable to update payment method': 'Unable to update payment method',
    'View user in user management': 'View user in user management',
  },
  zh: {
    'Add the public description and confirm that this channel can be shared.':
      '添加公开说明，并确认此渠道可以共享。',
    Approve: '批准',
    Canvas: '画布',
    'Community rankings': '社区排名',
    'Complete the full channel configuration below. The submission remains pending until an administrator approves it.':
      '请在下方完成完整渠道配置。提交内容在管理员批准前将保持待审核状态。',
    'Configure per-group warnings and acknowledgement count (1–3). Use modal for the most prominent warning.':
      '为各分组配置警告和确认次数（1–3 次）。最醒目的警告请使用弹窗模式。',
    Contributor: '贡献者',
    'I confirm that these credentials are authorized for this shared channel.':
      '我确认这些凭证已获授权用于此共享渠道。',
    Inspector: '检查器',
    'Settings saved': '设置已保存',
    'Sharing information': '共享信息',
    'This information is shown in the public channel market after approval.':
      '批准后，此信息将显示在公开渠道市场中。',
    'Top-up amount': '充值金额',
    'Unable to update payment method': '无法更新支付方式',
    'View user in user management': '在用户管理中查看用户',
  },
  'zh-TW': {
    'Add the public description and confirm that this channel can be shared.':
      '新增公開說明，並確認此渠道可以共享。',
    Approve: '核准',
    Canvas: '畫布',
    'Community rankings': '社群排名',
    'Complete the full channel configuration below. The submission remains pending until an administrator approves it.':
      '請在下方完成完整渠道設定。提交內容在管理員核准前將維持待審核狀態。',
    'Configure per-group warnings and acknowledgement count (1–3). Use modal for the most prominent warning.':
      '為各分組設定警告和確認次數（1–3 次）。最醒目的警告請使用彈窗模式。',
    Contributor: '貢獻者',
    'I confirm that these credentials are authorized for this shared channel.':
      '我確認這些憑證已獲授權用於此共享渠道。',
    Inspector: '檢查器',
    'Settings saved': '設定已儲存',
    'Sharing information': '共享資訊',
    'This information is shown in the public channel market after approval.':
      '核准後，此資訊將顯示在公開渠道市場中。',
    'Top-up amount': '充值金額',
    'Unable to update payment method': '無法更新付款方式',
    'View user in user management': '在使用者管理中查看使用者',
  },
  fr: {
    'Add the public description and confirm that this channel can be shared.':
      'Ajoutez la description publique et confirmez que ce canal peut être partagé.',
    Approve: 'Approuver',
    Canvas: 'Canevas',
    'Community rankings': 'Classements de la communauté',
    'Complete the full channel configuration below. The submission remains pending until an administrator approves it.':
      'Renseignez toute la configuration du canal ci-dessous. La soumission reste en attente jusqu’à son approbation par un administrateur.',
    'Configure per-group warnings and acknowledgement count (1–3). Use modal for the most prominent warning.':
      'Configurez les avertissements par groupe et le nombre de confirmations (1 à 3). Utilisez le mode modal pour l’avertissement le plus visible.',
    Contributor: 'Contributeur',
    'I confirm that these credentials are authorized for this shared channel.':
      'Je confirme que ces identifiants sont autorisés pour ce canal partagé.',
    Inspector: 'Inspecteur',
    'Settings saved': 'Paramètres enregistrés',
    'Sharing information': 'Informations de partage',
    'This information is shown in the public channel market after approval.':
      'Ces informations apparaîtront sur le marché public des canaux après approbation.',
    'Top-up amount': 'Montant de la recharge',
    'Unable to update payment method':
      'Impossible de mettre à jour le moyen de paiement',
    'View user in user management':
      'Voir l’utilisateur dans la gestion des utilisateurs',
  },
  ja: {
    'Add the public description and confirm that this channel can be shared.':
      '公開説明を追加し、このチャネルを共有できることを確認してください。',
    Approve: '承認',
    Canvas: 'キャンバス',
    'Community rankings': 'コミュニティランキング',
    'Complete the full channel configuration below. The submission remains pending until an administrator approves it.':
      '以下のチャネル設定をすべて入力してください。管理者が承認するまで申請は保留状態になります。',
    'Configure per-group warnings and acknowledgement count (1–3). Use modal for the most prominent warning.':
      'グループごとの警告と確認回数（1～3回）を設定します。最も目立たせる警告にはモーダルを使用してください。',
    Contributor: 'コントリビューター',
    'I confirm that these credentials are authorized for this shared channel.':
      'これらの認証情報がこの共有チャネルでの使用を許可されていることを確認します。',
    Inspector: 'インスペクター',
    'Settings saved': '設定を保存しました',
    'Sharing information': '共有情報',
    'This information is shown in the public channel market after approval.':
      '承認後、この情報は公開チャネルマーケットに表示されます。',
    'Top-up amount': 'チャージ金額',
    'Unable to update payment method': '支払方法を更新できません',
    'View user in user management': 'ユーザー管理でユーザーを表示',
  },
  ru: {
    'Add the public description and confirm that this channel can be shared.':
      'Добавьте публичное описание и подтвердите, что этот канал можно использовать совместно.',
    Approve: 'Одобрить',
    Canvas: 'Холст',
    'Community rankings': 'Рейтинг сообщества',
    'Complete the full channel configuration below. The submission remains pending until an administrator approves it.':
      'Заполните полную конфигурацию канала ниже. Заявка останется на рассмотрении до одобрения администратором.',
    'Configure per-group warnings and acknowledgement count (1–3). Use modal for the most prominent warning.':
      'Настройте предупреждения для групп и число подтверждений (1–3). Для наиболее заметного предупреждения используйте модальное окно.',
    Contributor: 'Участник',
    'I confirm that these credentials are authorized for this shared channel.':
      'Я подтверждаю, что эти учётные данные разрешено использовать для общего канала.',
    Inspector: 'Инспектор',
    'Settings saved': 'Настройки сохранены',
    'Sharing information': 'Сведения для публикации',
    'This information is shown in the public channel market after approval.':
      'После одобрения эти сведения появятся в каталоге общедоступных каналов.',
    'Top-up amount': 'Сумма пополнения',
    'Unable to update payment method': 'Не удалось обновить способ оплаты',
    'View user in user management': 'Открыть пользователя в разделе управления',
  },
  vi: {
    'Add the public description and confirm that this channel can be shared.':
      'Thêm mô tả công khai và xác nhận rằng kênh này có thể được chia sẻ.',
    Approve: 'Phê duyệt',
    Canvas: 'Khung vẽ',
    'Community rankings': 'Xếp hạng cộng đồng',
    'Complete the full channel configuration below. The submission remains pending until an administrator approves it.':
      'Hoàn tất toàn bộ cấu hình kênh bên dưới. Nội dung gửi sẽ ở trạng thái chờ cho đến khi quản trị viên phê duyệt.',
    'Configure per-group warnings and acknowledgement count (1–3). Use modal for the most prominent warning.':
      'Cấu hình cảnh báo theo nhóm và số lần xác nhận (1–3). Dùng hộp thoại cho cảnh báo nổi bật nhất.',
    Contributor: 'Người đóng góp',
    'I confirm that these credentials are authorized for this shared channel.':
      'Tôi xác nhận các thông tin xác thực này được phép dùng cho kênh chia sẻ.',
    Inspector: 'Trình kiểm tra',
    'Settings saved': 'Đã lưu cài đặt',
    'Sharing information': 'Thông tin chia sẻ',
    'This information is shown in the public channel market after approval.':
      'Thông tin này sẽ hiển thị trên chợ kênh công khai sau khi được phê duyệt.',
    'Top-up amount': 'Số tiền nạp',
    'Unable to update payment method':
      'Không thể cập nhật phương thức thanh toán',
    'View user in user management':
      'Xem người dùng trong phần quản lý người dùng',
  },
}

for (const [locale, translations] of Object.entries(
  remainingStaticKeyTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const pricingRedesignTranslations = {
  en: {
    'Dynamic Profit Pricing': 'Dynamic Profit Pricing',
    'Group Pricing stores the base cost multiplier. This page computes the live profit multiplier on top of that cost.':
      'Group Pricing stores the base cost multiplier. This page computes the live profit multiplier on top of that cost.',
    'Enable dynamic profit pricing': 'Enable dynamic profit pricing',
    'The final charge is the group cost multiplier multiplied by this dynamic profit multiplier.':
      'The final charge is the group cost multiplier multiplied by this dynamic profit multiplier.',
    'Minimum profit multiplier': 'Minimum profit multiplier',
    'The profit multiplier never falls below this value while dynamic pricing is enabled.':
      'The profit multiplier never falls below this value while dynamic pricing is enabled.',
    'Dynamic profit ceiling': 'Dynamic profit ceiling',
    'Caps the load-driven profit premium. Cost protection can still raise the effective multiplier when needed.':
      'Caps the load-driven profit premium. Cost protection can still raise the effective multiplier when needed.',
    'Reference model cost (USD / 1M tokens)':
      'Reference model cost (USD / 1M tokens)',
    'Use the model cost baseline used to compare upstream cost with the configured group cost multiplier.':
      'Use the model cost baseline used to compare upstream cost with the configured group cost multiplier.',
    'Cost protection margin': 'Cost protection margin',
    'Known upstream cost is multiplied by this margin before the cost floor is compared with profit pricing.':
      'Known upstream cost is multiplied by this margin before the cost floor is compared with profit pricing.',
    'Live profit multiplier preview': 'Live profit multiplier preview',
    'Current dynamic profit multiplier': 'Current dynamic profit multiplier',
    'Cost × profit pricing preview': 'Cost × profit pricing preview',
    'Group Pricing supplies the cost multiplier. Dynamic pricing supplies the profit multiplier. Final billing multiplies both.':
      'Group Pricing supplies the cost multiplier. Dynamic pricing supplies the profit multiplier. Final billing multiplies both.',
    Formula: 'Formula',
    'Final billing = group cost × dynamic profit':
      'Final billing = group cost × dynamic profit',
    'Pricing group': 'Pricing group',
    'Cost multiplier': 'Cost multiplier',
    'Profit multiplier': 'Profit multiplier',
    'Effective billing multiplier': 'Effective billing multiplier',
    'No pricing groups configured': 'No pricing groups configured',
    'Group cost multipliers': 'Group cost multipliers',
    'JSON map of group → cost multiplier used as the base for that billing group. Dynamic pricing adds the profit multiplier.':
      'JSON map of group → cost multiplier used as the base for that billing group. Dynamic pricing adds the profit multiplier.',
    'Understand how user groups, cost multipliers, profit pricing, and special rules work together.':
      'Understand how user groups, cost multipliers, profit pricing, and special rules work together.',
    'decides which channels are used and which base cost multiplier applies.':
      'decides which channels are used and which base cost multiplier applies.',
    'decides the top-up ratio, which groups the user can pick for tokens, and whether a cost override applies.':
      'decides the top-up ratio, which groups the user can pick for tokens, and whether a cost override applies.',
    'Find the cost multiplier.': 'Find the cost multiplier.',
    'Look for a special cost rule matching this user group and this billing group. If one exists, use its cost multiplier. Otherwise use the billing group base cost from the pricing table.':
      'Look for a special cost rule matching this user group and this billing group. If one exists, use its cost multiplier. Otherwise use the billing group base cost from the pricing table.',
    'Final charge = model base cost × group cost multiplier × dynamic profit multiplier.':
      'Final charge = model base cost × group cost multiplier × dynamic profit multiplier.',
    'The group value is a cost basis, not a personal discount. Dynamic pricing supplies the profit multiplier separately.':
      'The group value is a cost basis, not a personal discount. Dynamic pricing supplies the profit multiplier separately.',
    'Special cost rules': 'Special cost rules',
    'Cost basis = 10 × 0.3 = 3': 'Cost basis = 10 × 0.3 = 3',
    'Cost basis = 10 × 1.0 = 10': 'Cost basis = 10 × 1.0 = 10',
    'Cost basis = 10 × 0.8 = 8': 'Cost basis = 10 × 0.8 = 8',
    'Users of vip, when billed as premium, use cost multiplier':
      'Users of vip, when billed as premium, use cost multiplier',
    'No rule for vip billed as default → use the base cost of default, 1.0 (the 0.8 of vip is not used)':
      'No rule for vip billed as default → use the base cost of default, 1.0 (the 0.8 of vip is not used)',
    'No rule for vip billed as vip → use the base cost of vip, 0.8':
      'No rule for vip billed as vip → use the base cost of vip, 0.8',
    'Use the pricing group table to manage the cost multiplier and whether the group appears in the token creation dropdown.':
      'Use the pricing group table to manage the cost multiplier and whether the group appears in the token creation dropdown.',
    'In JSON, the user group is the outer key and the billing group is the inner key. The example below means: vip users use cost multiplier 0.8 when billed as standard, and 0.3 when billed as premium.':
      'In JSON, the user group is the outer key and the billing group is the inner key. The example below means: vip users use cost multiplier 0.8 when billed as standard, and 0.3 when billed as premium.',
    'Only configured combinations are overridden. All other calls keep the billing group base cost multiplier.':
      'Only configured combinations are overridden. All other calls keep the billing group base cost multiplier.',
    'Base cost multipliers': 'Base cost multipliers',
    "Manual order is preserved until you use Optimize. This changes the global order for every user, but runtime assignment still filters each user's visible groups. Optimize uses base cost multipliers by default; selecting a user group applies its exact special cost overrides before sorting.":
      "Manual order is preserved until you use Optimize. This changes the global order for every user, but runtime assignment still filters each user's visible groups. Optimize uses base cost multipliers by default; selecting a user group applies its exact special cost overrides before sorting.",
    'Cost multipliers must be finite numbers greater than or equal to zero.':
      'Cost multipliers must be finite numbers greater than or equal to zero.',
    'Optimize by effective cost': 'Optimize by effective cost',
    'Edit cost override': 'Edit cost override',
    'Add cost override': 'Add cost override',
    'Configure a custom cost multiplier for "{{userGroup}}" users when using a specific token group.':
      'Configure a custom cost multiplier for "{{userGroup}}" users when using a specific token group.',
    'Configure a custom cost multiplier for when users use a specific token group.':
      'Configure a custom cost multiplier for when users use a specific token group.',
    'Invalid cost multiplier': 'Invalid cost multiplier',
    'Cost multiplier applied when {{userGroup}} uses {{targetGroup}}':
      'Cost multiplier applied when {{userGroup}} uses {{targetGroup}}',
    'Save group pricing': 'Save group pricing',
    'Fixed by channel sharing settings': 'Fixed by channel sharing settings',
  },
  zh: {
    'Dynamic Profit Pricing': '动态利润定价',
    'Group Pricing stores the base cost multiplier. This page computes the live profit multiplier on top of that cost.':
      '分组定价保存成本基准倍率；本页在成本之上计算实时利润倍率。',
    'Enable dynamic profit pricing': '启用动态利润定价',
    'The final charge is the group cost multiplier multiplied by this dynamic profit multiplier.':
      '最终收费 = 分组成本倍率 × 动态利润倍率。',
    'Minimum profit multiplier': '最低利润倍率',
    'The profit multiplier never falls below this value while dynamic pricing is enabled.':
      '启用动态定价后，利润倍率不会低于此值。',
    'Dynamic profit ceiling': '动态利润上限',
    'Caps the load-driven profit premium. Cost protection can still raise the effective multiplier when needed.':
      '仅限制负载产生的利润溢价；成本保护需要时仍可提高最终倍率。',
    'Reference model cost (USD / 1M tokens)':
      '模型成本基准（美元 / 100 万 Token）',
    'Use the model cost baseline used to compare upstream cost with the configured group cost multiplier.':
      '用于将上游成本与分组成本倍率进行比较的模型成本基准。',
    'Cost protection margin': '成本保护裕量',
    'Known upstream cost is multiplied by this margin before the cost floor is compared with profit pricing.':
      '已知上游成本先乘以该裕量，再与利润定价的成本底线比较。',
    'Live profit multiplier preview': '实时利润倍率预览',
    'Current dynamic profit multiplier': '当前动态利润倍率',
    'Cost × profit pricing preview': '成本 × 利润定价预览',
    'Group Pricing supplies the cost multiplier. Dynamic pricing supplies the profit multiplier. Final billing multiplies both.':
      '分组定价提供成本倍率，动态定价提供利润倍率，最终收费将两者相乘。',
    Formula: '公式',
    'Final billing = group cost × dynamic profit':
      '最终收费 = 分组成本 × 动态利润',
    'Pricing group': '定价分组',
    'Cost multiplier': '成本倍率',
    'Profit multiplier': '利润倍率',
    'Effective billing multiplier': '最终收费倍率',
    'No pricing groups configured': '尚未配置定价分组',
    'Group cost multipliers': '分组成本倍率',
    'JSON map of group → cost multiplier used as the base for that billing group. Dynamic pricing adds the profit multiplier.':
      '分组到成本倍率的 JSON 映射，作为该计费分组的成本基准；动态定价会叠加利润倍率。',
    'Understand how user groups, cost multipliers, profit pricing, and special rules work together.':
      '了解用户组、成本倍率、利润定价和特殊规则如何共同生效。',
    'decides which channels are used and which base cost multiplier applies.':
      '决定使用哪些渠道以及采用哪个成本基准倍率。',
    'decides the top-up ratio, which groups the user can pick for tokens, and whether a cost override applies.':
      '决定充值倍率、用户创建令牌时可选的分组，以及是否应用成本覆盖规则。',
    'Find the cost multiplier.': '查找成本倍率。',
    'Look for a special cost rule matching this user group and this billing group. If one exists, use its cost multiplier. Otherwise use the billing group base cost from the pricing table.':
      '查找匹配用户组和计费组的特殊成本规则；若存在则使用其成本倍率，否则使用定价表中的计费组成本基准。',
    'Final charge = model base cost × group cost multiplier × dynamic profit multiplier.':
      '最终收费 = 模型基础成本 × 分组成本倍率 × 动态利润倍率。',
    'The group value is a cost basis, not a personal discount. Dynamic pricing supplies the profit multiplier separately.':
      '分组数值是成本基准，不是个人折扣；动态定价会单独提供利润倍率。',
    'Special cost rules': '特殊成本规则',
    'Cost basis = 10 × 0.3 = 3': '成本基准 = 10 × 0.3 = 3',
    'Cost basis = 10 × 1.0 = 10': '成本基准 = 10 × 1.0 = 10',
    'Cost basis = 10 × 0.8 = 8': '成本基准 = 10 × 0.8 = 8',
    'Users of vip, when billed as premium, use cost multiplier':
      'vip 用户按 premium 计费时使用成本倍率',
    'No rule for vip billed as default → use the base cost of default, 1.0 (the 0.8 of vip is not used)':
      'vip 按 default 计费没有特殊规则 → 使用 default 的成本基准 1.0（不会使用 vip 的 0.8）',
    'No rule for vip billed as vip → use the base cost of vip, 0.8':
      'vip 按 vip 计费没有特殊规则 → 使用 vip 的成本基准 0.8',
    'Use the pricing group table to manage the cost multiplier and whether the group appears in the token creation dropdown.':
      '使用定价分组表管理成本倍率，以及分组是否出现在令牌创建下拉框中。',
    'In JSON, the user group is the outer key and the billing group is the inner key. The example below means: vip users use cost multiplier 0.8 when billed as standard, and 0.3 when billed as premium.':
      'JSON 中外层键是用户组，内层键是计费组。下面示例表示：vip 用户按 standard 计费使用成本倍率 0.8，按 premium 计费使用 0.3。',
    'Only configured combinations are overridden. All other calls keep the billing group base cost multiplier.':
      '只有配置的组合会覆盖；其它请求继续使用计费组的成本基准倍率。',
    'Base cost multipliers': '基础成本倍率',
    "Manual order is preserved until you use Optimize. This changes the global order for every user, but runtime assignment still filters each user's visible groups. Optimize uses base cost multipliers by default; selecting a user group applies its exact special cost overrides before sorting.":
      '在点击优化前会保留手动顺序。优化会改变所有用户的全局顺序，但运行时仍会过滤每个用户可见的分组；默认按基础成本倍率排序，选择用户组后会先应用其特殊成本覆盖。',
    'Cost multipliers must be finite numbers greater than or equal to zero.':
      '成本倍率必须是大于等于 0 的有限数字。',
    'Optimize by effective cost': '按最终成本优化',
    'Edit cost override': '编辑成本覆盖',
    'Add cost override': '添加成本覆盖',
    'Configure a custom cost multiplier for "{{userGroup}}" users when using a specific token group.':
      '配置“{{userGroup}}”用户使用指定令牌组时的自定义成本倍率。',
    'Configure a custom cost multiplier for when users use a specific token group.':
      '配置用户使用指定令牌组时的自定义成本倍率。',
    'Invalid cost multiplier': '成本倍率无效',
    'Cost multiplier applied when {{userGroup}} uses {{targetGroup}}':
      '{{userGroup}} 使用 {{targetGroup}} 时应用的成本倍率',
    'Save group pricing': '保存分组定价',
    'Fixed by channel sharing settings': '由渠道共享设置固定',
  },
  'zh-TW': {
    'Dynamic Profit Pricing': '動態利潤定價',
    'Group Pricing stores the base cost multiplier. This page computes the live profit multiplier on top of that cost.':
      '分組定價儲存成本基準倍率；本頁在成本之上計算即時利潤倍率。',
    'Enable dynamic profit pricing': '啟用動態利潤定價',
    'The final charge is the group cost multiplier multiplied by this dynamic profit multiplier.':
      '最終收費 = 分組成本倍率 × 動態利潤倍率。',
    'Minimum profit multiplier': '最低利潤倍率',
    'The profit multiplier never falls below this value while dynamic pricing is enabled.':
      '啟用動態定價後，利潤倍率不會低於此值。',
    'Dynamic profit ceiling': '動態利潤上限',
    'Caps the load-driven profit premium. Cost protection can still raise the effective multiplier when needed.':
      '僅限制負載產生的利潤溢價；成本保護需要時仍可提高最終倍率。',
    'Reference model cost (USD / 1M tokens)':
      '模型成本基準（美元 / 100 萬 Token）',
    'Use the model cost baseline used to compare upstream cost with the configured group cost multiplier.':
      '用於將上游成本與分組成本倍率比較的模型成本基準。',
    'Cost protection margin': '成本保護裕量',
    'Known upstream cost is multiplied by this margin before the cost floor is compared with profit pricing.':
      '已知上游成本先乘以此裕量，再與利潤定價的成本底線比較。',
    'Live profit multiplier preview': '即時利潤倍率預覽',
    'Current dynamic profit multiplier': '目前動態利潤倍率',
    'Cost × profit pricing preview': '成本 × 利潤定價預覽',
    'Group Pricing supplies the cost multiplier. Dynamic pricing supplies the profit multiplier. Final billing multiplies both.':
      '分組定價提供成本倍率，動態定價提供利潤倍率，最終收費會將兩者相乘。',
    Formula: '公式',
    'Final billing = group cost × dynamic profit':
      '最終收費 = 分組成本 × 動態利潤',
    'Pricing group': '定價分組',
    'Cost multiplier': '成本倍率',
    'Profit multiplier': '利潤倍率',
    'Effective billing multiplier': '最終收費倍率',
    'No pricing groups configured': '尚未設定定價分組',
    'Group cost multipliers': '分組成本倍率',
    'JSON map of group → cost multiplier used as the base for that billing group. Dynamic pricing adds the profit multiplier.':
      '分組到成本倍率的 JSON 對映，作為該計費分組的成本基準；動態定價會疊加利潤倍率。',
    'Understand how user groups, cost multipliers, profit pricing, and special rules work together.':
      '了解使用者群組、成本倍率、利潤定價和特殊規則如何共同生效。',
    'decides which channels are used and which base cost multiplier applies.':
      '決定使用哪些渠道以及採用哪個成本基準倍率。',
    'decides the top-up ratio, which groups the user can pick for tokens, and whether a cost override applies.':
      '決定充值倍率、使用者建立 Token 時可選的分組，以及是否套用成本覆蓋規則。',
    'Find the cost multiplier.': '尋找成本倍率。',
    'Look for a special cost rule matching this user group and this billing group. If one exists, use its cost multiplier. Otherwise use the billing group base cost from the pricing table.':
      '尋找符合使用者群組和計費群組的特殊成本規則；若存在則使用其成本倍率，否則使用定價表中的計費群組成本基準。',
    'Final charge = model base cost × group cost multiplier × dynamic profit multiplier.':
      '最終收費 = 模型基礎成本 × 分組成本倍率 × 動態利潤倍率。',
    'The group value is a cost basis, not a personal discount. Dynamic pricing supplies the profit multiplier separately.':
      '分組數值是成本基準，不是個人折扣；動態定價會另外提供利潤倍率。',
    'Special cost rules': '特殊成本規則',
    'Cost basis = 10 × 0.3 = 3': '成本基準 = 10 × 0.3 = 3',
    'Cost basis = 10 × 1.0 = 10': '成本基準 = 10 × 1.0 = 10',
    'Cost basis = 10 × 0.8 = 8': '成本基準 = 10 × 0.8 = 8',
    'Users of vip, when billed as premium, use cost multiplier':
      'vip 使用者按 premium 計費時使用成本倍率',
    'No rule for vip billed as default → use the base cost of default, 1.0 (the 0.8 of vip is not used)':
      'vip 按 default 計費沒有特殊規則 → 使用 default 的成本基準 1.0（不會使用 vip 的 0.8）',
    'No rule for vip billed as vip → use the base cost of vip, 0.8':
      'vip 按 vip 計費沒有特殊規則 → 使用 vip 的成本基準 0.8',
    'Use the pricing group table to manage the cost multiplier and whether the group appears in the token creation dropdown.':
      '使用定價分組表管理成本倍率，以及分組是否出現在 Token 建立下拉選單中。',
    'In JSON, the user group is the outer key and the billing group is the inner key. The example below means: vip users use cost multiplier 0.8 when billed as standard, and 0.3 when billed as premium.':
      'JSON 中外層鍵是使用者群組，內層鍵是計費群組。以下範例表示：vip 使用者按 standard 計費使用成本倍率 0.8，按 premium 計費使用 0.3。',
    'Only configured combinations are overridden. All other calls keep the billing group base cost multiplier.':
      '只有設定的組合會覆蓋；其他請求繼續使用計費群組的成本基準倍率。',
    'Base cost multipliers': '基礎成本倍率',
    "Manual order is preserved until you use Optimize. This changes the global order for every user, but runtime assignment still filters each user's visible groups. Optimize uses base cost multipliers by default; selecting a user group applies its exact special cost overrides before sorting.":
      '在點擊最佳化前會保留手動順序。最佳化會改變所有使用者的全域順序，但執行時仍會過濾每個使用者可見的分組；預設按基礎成本倍率排序，選擇使用者群組後會先套用其特殊成本覆蓋。',
    'Cost multipliers must be finite numbers greater than or equal to zero.':
      '成本倍率必須是大於等於 0 的有限數字。',
    'Optimize by effective cost': '按最終成本最佳化',
    'Edit cost override': '編輯成本覆蓋',
    'Add cost override': '新增成本覆蓋',
    'Configure a custom cost multiplier for "{{userGroup}}" users when using a specific token group.':
      '設定「{{userGroup}}」使用者使用指定 Token 群組時的自訂成本倍率。',
    'Configure a custom cost multiplier for when users use a specific token group.':
      '設定使用者使用指定 Token 群組時的自訂成本倍率。',
    'Invalid cost multiplier': '成本倍率無效',
    'Cost multiplier applied when {{userGroup}} uses {{targetGroup}}':
      '{{userGroup}} 使用 {{targetGroup}} 時套用的成本倍率',
    'Save group pricing': '儲存分組定價',
    'Fixed by channel sharing settings': '由渠道共享設定固定',
  },
  fr: {
    'Dynamic Profit Pricing': 'Tarification dynamique du profit',
    'Group Pricing stores the base cost multiplier. This page computes the live profit multiplier on top of that cost.':
      'La tarification par groupe conserve le coefficient de coût de base ; cette page calcule le coefficient de profit en temps réel.',
    'Enable dynamic profit pricing':
      'Activer la tarification dynamique du profit',
    'The final charge is the group cost multiplier multiplied by this dynamic profit multiplier.':
      'Le montant final est le coefficient de coût du groupe multiplié par le coefficient de profit dynamique.',
    'Minimum profit multiplier': 'Coefficient de profit minimal',
    'The profit multiplier never falls below this value while dynamic pricing is enabled.':
      'Le coefficient de profit ne descend jamais sous cette valeur lorsque la tarification dynamique est active.',
    'Dynamic profit ceiling': 'Plafond du profit dynamique',
    'Caps the load-driven profit premium. Cost protection can still raise the effective multiplier when needed.':
      'Limite la prime de profit liée à la charge ; la protection des coûts peut toutefois augmenter le coefficient effectif.',
    'Reference model cost (USD / 1M tokens)':
      'Coût de référence du modèle (USD / 1 M de tokens)',
    'Use the model cost baseline used to compare upstream cost with the configured group cost multiplier.':
      'Utilisez le coût de référence du modèle pour comparer le coût amont au coefficient de coût du groupe.',
    'Cost protection margin': 'Marge de protection des coûts',
    'Known upstream cost is multiplied by this margin before the cost floor is compared with profit pricing.':
      'Le coût amont connu est multiplié par cette marge avant comparaison avec le plancher de coût.',
    'Live profit multiplier preview':
      'Aperçu du coefficient de profit en direct',
    'Current dynamic profit multiplier':
      'Coefficient de profit dynamique actuel',
    'Cost × profit pricing preview': 'Aperçu coût × profit',
    'Group Pricing supplies the cost multiplier. Dynamic pricing supplies the profit multiplier. Final billing multiplies both.':
      'La tarification par groupe fournit le coût ; la tarification dynamique fournit le profit. La facturation finale multiplie les deux.',
    Formula: 'Formule',
    'Final billing = group cost × dynamic profit':
      'Facturation finale = coût du groupe × profit dynamique',
    'Pricing group': 'Groupe tarifaire',
    'Cost multiplier': 'Coefficient de coût',
    'Profit multiplier': 'Coefficient de profit',
    'Effective billing multiplier': 'Coefficient de facturation effectif',
    'No pricing groups configured': 'Aucun groupe tarifaire configuré',
    'Group cost multipliers': 'Coefficients de coût des groupes',
    'JSON map of group → cost multiplier used as the base for that billing group. Dynamic pricing adds the profit multiplier.':
      'Carte JSON groupe → coefficient de coût utilisé comme base du groupe de facturation ; la tarification dynamique ajoute le profit.',
    'Understand how user groups, cost multipliers, profit pricing, and special rules work together.':
      'Comprenez le rôle des groupes utilisateurs, des coûts, du profit et des règles spéciales.',
    'decides which channels are used and which base cost multiplier applies.':
      'détermine les canaux utilisés et le coefficient de coût de base appliqué.',
    'decides the top-up ratio, which groups the user can pick for tokens, and whether a cost override applies.':
      'détermine le coefficient de recharge, les groupes disponibles pour les tokens et les éventuelles règles de coût.',
    'Find the cost multiplier.': 'Trouver le coefficient de coût.',
    'Look for a special cost rule matching this user group and this billing group. If one exists, use its cost multiplier. Otherwise use the billing group base cost from the pricing table.':
      'Cherchez une règle de coût correspondant au groupe utilisateur et au groupe de facturation ; sinon utilisez le coût de base du groupe tarifaire.',
    'Final charge = model base cost × group cost multiplier × dynamic profit multiplier.':
      'Facturation finale = coût de base du modèle × coût du groupe × profit dynamique.',
    'The group value is a cost basis, not a personal discount. Dynamic pricing supplies the profit multiplier separately.':
      'La valeur du groupe est une base de coût, pas une remise personnelle ; le profit dynamique est séparé.',
    'Special cost rules': 'Règles de coût spéciales',
    'Cost basis = 10 × 0.3 = 3': 'Base de coût = 10 × 0,3 = 3',
    'Cost basis = 10 × 1.0 = 10': 'Base de coût = 10 × 1,0 = 10',
    'Cost basis = 10 × 0.8 = 8': 'Base de coût = 10 × 0,8 = 8',
    'Users of vip, when billed as premium, use cost multiplier':
      'Les utilisateurs vip facturés en premium utilisent le coefficient de coût',
    'No rule for vip billed as default → use the base cost of default, 1.0 (the 0.8 of vip is not used)':
      'Sans règle vip facturé en default → coût de base default, 1,0 (le 0,8 de vip ne s’applique pas)',
    'No rule for vip billed as vip → use the base cost of vip, 0.8':
      'Sans règle vip facturé en vip → coût de base vip, 0,8',
    'Use the pricing group table to manage the cost multiplier and whether the group appears in the token creation dropdown.':
      'Gérez le coefficient de coût et la visibilité du groupe dans la liste de création des tokens.',
    'In JSON, the user group is the outer key and the billing group is the inner key. The example below means: vip users use cost multiplier 0.8 when billed as standard, and 0.3 when billed as premium.':
      'Dans le JSON, le groupe utilisateur est la clé externe et le groupe de facturation la clé interne ; vip utilise 0,8 en standard et 0,3 en premium.',
    'Only configured combinations are overridden. All other calls keep the billing group base cost multiplier.':
      'Seules les combinaisons configurées sont remplacées ; les autres gardent le coût de base du groupe.',
    'Base cost multipliers': 'Coefficients de coût de base',
    "Manual order is preserved until you use Optimize. This changes the global order for every user, but runtime assignment still filters each user's visible groups. Optimize uses base cost multipliers by default; selecting a user group applies its exact special cost overrides before sorting.":
      'L’ordre manuel est conservé jusqu’à l’optimisation. Celle-ci applique les remplacements de coût du groupe utilisateur avant le tri.',
    'Cost multipliers must be finite numbers greater than or equal to zero.':
      'Les coefficients de coût doivent être des nombres finis supérieurs ou égaux à zéro.',
    'Optimize by effective cost': 'Optimiser par coût effectif',
    'Edit cost override': 'Modifier le remplacement de coût',
    'Add cost override': 'Ajouter un remplacement de coût',
    'Configure a custom cost multiplier for "{{userGroup}}" users when using a specific token group.':
      'Configurez un coefficient de coût personnalisé pour les utilisateurs « {{userGroup}} » avec un groupe de tokens donné.',
    'Configure a custom cost multiplier for when users use a specific token group.':
      'Configurez un coefficient de coût personnalisé pour un groupe de tokens donné.',
    'Invalid cost multiplier': 'Coefficient de coût invalide',
    'Cost multiplier applied when {{userGroup}} uses {{targetGroup}}':
      'Coefficient appliqué quand {{userGroup}} utilise {{targetGroup}}',
    'Save group pricing': 'Enregistrer la tarification des groupes',
    'Fixed by channel sharing settings':
      'Fixé par les paramètres de partage du canal',
  },
  ja: {
    'Dynamic Profit Pricing': '動的利益価格設定',
    'Group Pricing stores the base cost multiplier. This page computes the live profit multiplier on top of that cost.':
      'グループ料金は基本コスト倍率を保存し、このページでその上にリアルタイム利益倍率を計算します。',
    'Enable dynamic profit pricing': '動的利益価格設定を有効化',
    'The final charge is the group cost multiplier multiplied by this dynamic profit multiplier.':
      '最終料金はグループのコスト倍率と動的利益倍率の積です。',
    'Minimum profit multiplier': '最小利益倍率',
    'The profit multiplier never falls below this value while dynamic pricing is enabled.':
      '動的料金設定中、利益倍率はこの値を下回りません。',
    'Dynamic profit ceiling': '動的利益上限',
    'Caps the load-driven profit premium. Cost protection can still raise the effective multiplier when needed.':
      '負荷による利益プレミアムを制限します。コスト保護が必要な場合は実効倍率が上限を超えることがあります。',
    'Reference model cost (USD / 1M tokens)':
      'モデル基準コスト（USD / 100万トークン）',
    'Use the model cost baseline used to compare upstream cost with the configured group cost multiplier.':
      '上流コストとグループコスト倍率を比較するモデル基準コストです。',
    'Cost protection margin': 'コスト保護マージン',
    'Known upstream cost is multiplied by this margin before the cost floor is compared with profit pricing.':
      '既知の上流コストにこのマージンを掛けて、利益料金のコスト下限と比較します。',
    'Live profit multiplier preview': 'リアルタイム利益倍率プレビュー',
    'Current dynamic profit multiplier': '現在の動的利益倍率',
    'Cost × profit pricing preview': 'コスト × 利益料金プレビュー',
    'Group Pricing supplies the cost multiplier. Dynamic pricing supplies the profit multiplier. Final billing multiplies both.':
      'グループ料金がコスト倍率を、動的料金が利益倍率を提供し、最終請求では両方を掛け合わせます。',
    Formula: '計算式',
    'Final billing = group cost × dynamic profit':
      '最終請求 = グループコスト × 動的利益',
    'Pricing group': '料金グループ',
    'Cost multiplier': 'コスト倍率',
    'Profit multiplier': '利益倍率',
    'Effective billing multiplier': '実効請求倍率',
    'No pricing groups configured': '料金グループが設定されていません',
    'Group cost multipliers': 'グループコスト倍率',
    'JSON map of group → cost multiplier used as the base for that billing group. Dynamic pricing adds the profit multiplier.':
      '請求グループの基準となるグループ→コスト倍率の JSON マップです。動的料金が利益倍率を加えます。',
    'Understand how user groups, cost multipliers, profit pricing, and special rules work together.':
      'ユーザーグループ、コスト倍率、利益料金、特殊ルールの連携を確認します。',
    'decides which channels are used and which base cost multiplier applies.':
      '使用するチャネルと適用する基本コスト倍率を決めます。',
    'decides the top-up ratio, which groups the user can pick for tokens, and whether a cost override applies.':
      'チャージ倍率、トークンで選べるグループ、コスト上書きの有無を決めます。',
    'Find the cost multiplier.': 'コスト倍率を確認します。',
    'Look for a special cost rule matching this user group and this billing group. If one exists, use its cost multiplier. Otherwise use the billing group base cost from the pricing table.':
      'ユーザーグループと請求グループに一致する特殊コストルールを探し、なければ料金表の基本コストを使います。',
    'Final charge = model base cost × group cost multiplier × dynamic profit multiplier.':
      '最終請求 = モデル基本コスト × グループコスト倍率 × 動的利益倍率。',
    'The group value is a cost basis, not a personal discount. Dynamic pricing supplies the profit multiplier separately.':
      'グループ値はコスト基準であり個人割引ではありません。利益倍率は動的料金が別に提供します。',
    'Special cost rules': '特殊コストルール',
    'Cost basis = 10 × 0.3 = 3': 'コスト基準 = 10 × 0.3 = 3',
    'Cost basis = 10 × 1.0 = 10': 'コスト基準 = 10 × 1.0 = 10',
    'Cost basis = 10 × 0.8 = 8': 'コスト基準 = 10 × 0.8 = 8',
    'Users of vip, when billed as premium, use cost multiplier':
      'vip ユーザーが premium で請求される場合のコスト倍率',
    'No rule for vip billed as default → use the base cost of default, 1.0 (the 0.8 of vip is not used)':
      'vip を default で請求するルールがないため default の基本コスト 1.0 を使います（vip の 0.8 は使いません）。',
    'No rule for vip billed as vip → use the base cost of vip, 0.8':
      'vip を vip で請求するルールがないため vip の基本コスト 0.8 を使います。',
    'Use the pricing group table to manage the cost multiplier and whether the group appears in the token creation dropdown.':
      '料金グループ表でコスト倍率とトークン作成リストへの表示を管理します。',
    'In JSON, the user group is the outer key and the billing group is the inner key. The example below means: vip users use cost multiplier 0.8 when billed as standard, and 0.3 when billed as premium.':
      'JSON の外側キーはユーザーグループ、内側キーは請求グループです。例では vip が standard で 0.8、premium で 0.3 を使います。',
    'Only configured combinations are overridden. All other calls keep the billing group base cost multiplier.':
      '設定した組み合わせだけが上書きされ、その他は請求グループの基本コスト倍率を使います。',
    'Base cost multipliers': '基本コスト倍率',
    "Manual order is preserved until you use Optimize. This changes the global order for every user, but runtime assignment still filters each user's visible groups. Optimize uses base cost multipliers by default; selecting a user group applies its exact special cost overrides before sorting.":
      '最適化するまで手動順序を保持します。最適化では基本コスト倍率を使い、ユーザーグループを選ぶと特殊コスト上書きを適用して並べ替えます。',
    'Cost multipliers must be finite numbers greater than or equal to zero.':
      'コスト倍率は 0 以上の有限数値である必要があります。',
    'Optimize by effective cost': '実効コストで最適化',
    'Edit cost override': 'コスト上書きを編集',
    'Add cost override': 'コスト上書きを追加',
    'Configure a custom cost multiplier for "{{userGroup}}" users when using a specific token group.':
      '「{{userGroup}}」ユーザーが指定トークングループを使う際のカスタムコスト倍率を設定します。',
    'Configure a custom cost multiplier for when users use a specific token group.':
      '指定トークングループを使う場合のカスタムコスト倍率を設定します。',
    'Invalid cost multiplier': '無効なコスト倍率',
    'Cost multiplier applied when {{userGroup}} uses {{targetGroup}}':
      '{{userGroup}} が {{targetGroup}} を使う場合のコスト倍率',
    'Save group pricing': 'グループ料金を保存',
    'Fixed by channel sharing settings': 'チャネル共有設定で固定',
  },
  ru: {
    'Dynamic Profit Pricing': 'Динамическое ценообразование прибыли',
    'Group Pricing stores the base cost multiplier. This page computes the live profit multiplier on top of that cost.':
      'Групповая тарификация хранит базовый коэффициент затрат, а эта страница рассчитывает поверх него коэффициент прибыли в реальном времени.',
    'Enable dynamic profit pricing': 'Включить динамическую прибыль',
    'The final charge is the group cost multiplier multiplied by this dynamic profit multiplier.':
      'Итоговая сумма равна коэффициенту затрат группы, умноженному на динамический коэффициент прибыли.',
    'Minimum profit multiplier': 'Минимальный коэффициент прибыли',
    'The profit multiplier never falls below this value while dynamic pricing is enabled.':
      'При включённом динамическом ценообразовании коэффициент прибыли не опускается ниже этого значения.',
    'Dynamic profit ceiling': 'Верхняя граница динамической прибыли',
    'Caps the load-driven profit premium. Cost protection can still raise the effective multiplier when needed.':
      'Ограничивает надбавку прибыли от нагрузки; защита затрат при необходимости может повысить итоговый коэффициент.',
    'Reference model cost (USD / 1M tokens)':
      'Базовая стоимость модели (USD / 1 млн токенов)',
    'Use the model cost baseline used to compare upstream cost with the configured group cost multiplier.':
      'Базовая стоимость модели для сравнения затрат upstream с коэффициентом затрат группы.',
    'Cost protection margin': 'Запас защиты затрат',
    'Known upstream cost is multiplied by this margin before the cost floor is compared with profit pricing.':
      'Известная стоимость upstream умножается на этот запас перед сравнением с нижней границей цены.',
    'Live profit multiplier preview': 'Предпросмотр прибыли в реальном времени',
    'Current dynamic profit multiplier':
      'Текущий динамический коэффициент прибыли',
    'Cost × profit pricing preview': 'Предпросмотр: затраты × прибыль',
    'Group Pricing supplies the cost multiplier. Dynamic pricing supplies the profit multiplier. Final billing multiplies both.':
      'Групповая тарификация задаёт затраты, динамическая тарификация — прибыль; итоговая сумма перемножает оба коэффициента.',
    Formula: 'Формула',
    'Final billing = group cost × dynamic profit':
      'Итоговая сумма = затраты группы × динамическая прибыль',
    'Pricing group': 'Тарифная группа',
    'Cost multiplier': 'Коэффициент затрат',
    'Profit multiplier': 'Коэффициент прибыли',
    'Effective billing multiplier': 'Итоговый коэффициент тарификации',
    'No pricing groups configured': 'Тарифные группы не настроены',
    'Group cost multipliers': 'Коэффициенты затрат групп',
    'JSON map of group → cost multiplier used as the base for that billing group. Dynamic pricing adds the profit multiplier.':
      'JSON-карта группа → коэффициент затрат, используемая как база группы тарификации; динамическая тарификация добавляет прибыль.',
    'Understand how user groups, cost multipliers, profit pricing, and special rules work together.':
      'Узнайте, как работают группы пользователей, затраты, прибыль и специальные правила.',
    'decides which channels are used and which base cost multiplier applies.':
      'определяет используемые каналы и базовый коэффициент затрат.',
    'decides the top-up ratio, which groups the user can pick for tokens, and whether a cost override applies.':
      'определяет коэффициент пополнения, доступные для токенов группы и применение переопределения затрат.',
    'Find the cost multiplier.': 'Найдите коэффициент затрат.',
    'Look for a special cost rule matching this user group and this billing group. If one exists, use its cost multiplier. Otherwise use the billing group base cost from the pricing table.':
      'Найдите специальное правило для группы пользователя и группы тарификации; иначе используйте базовую стоимость группы из таблицы.',
    'Final charge = model base cost × group cost multiplier × dynamic profit multiplier.':
      'Итоговая сумма = базовая стоимость модели × затраты группы × динамическая прибыль.',
    'The group value is a cost basis, not a personal discount. Dynamic pricing supplies the profit multiplier separately.':
      'Значение группы — это база затрат, а не персональная скидка; коэффициент прибыли задаётся отдельно.',
    'Special cost rules': 'Специальные правила затрат',
    'Cost basis = 10 × 0.3 = 3': 'База затрат = 10 × 0,3 = 3',
    'Cost basis = 10 × 1.0 = 10': 'База затрат = 10 × 1,0 = 10',
    'Cost basis = 10 × 0.8 = 8': 'База затрат = 10 × 0,8 = 8',
    'Users of vip, when billed as premium, use cost multiplier':
      'Пользователи vip при тарификации premium используют коэффициент затрат',
    'No rule for vip billed as default → use the base cost of default, 1.0 (the 0.8 of vip is not used)':
      'Для vip в default нет правила → используется базовая стоимость default 1,0 (0,8 vip не используется).',
    'No rule for vip billed as vip → use the base cost of vip, 0.8':
      'Для vip в vip нет правила → используется базовая стоимость vip 0,8.',
    'Use the pricing group table to manage the cost multiplier and whether the group appears in the token creation dropdown.':
      'Управляйте коэффициентом затрат и видимостью группы в списке создания токена через таблицу тарифов.',
    'In JSON, the user group is the outer key and the billing group is the inner key. The example below means: vip users use cost multiplier 0.8 when billed as standard, and 0.3 when billed as premium.':
      'Во внешнем ключе JSON указана группа пользователя, во внутреннем — группа тарификации; vip использует 0,8 для standard и 0,3 для premium.',
    'Only configured combinations are overridden. All other calls keep the billing group base cost multiplier.':
      'Переопределяются только настроенные комбинации; остальные запросы используют базовый коэффициент группы.',
    'Base cost multipliers': 'Базовые коэффициенты затрат',
    "Manual order is preserved until you use Optimize. This changes the global order for every user, but runtime assignment still filters each user's visible groups. Optimize uses base cost multipliers by default; selecting a user group applies its exact special cost overrides before sorting.":
      'Ручной порядок сохраняется до оптимизации. По умолчанию оптимизация сортирует по базовым затратам и перед сортировкой применяет специальные правила выбранной группы пользователя.',
    'Cost multipliers must be finite numbers greater than or equal to zero.':
      'Коэффициенты затрат должны быть конечными числами не меньше нуля.',
    'Optimize by effective cost': 'Оптимизировать по эффективной стоимости',
    'Edit cost override': 'Изменить переопределение затрат',
    'Add cost override': 'Добавить переопределение затрат',
    'Configure a custom cost multiplier for "{{userGroup}}" users when using a specific token group.':
      'Настройте собственный коэффициент затрат для пользователей «{{userGroup}}» при использовании группы токена.',
    'Configure a custom cost multiplier for when users use a specific token group.':
      'Настройте собственный коэффициент затрат для выбранной группы токена.',
    'Invalid cost multiplier': 'Недопустимый коэффициент затрат',
    'Cost multiplier applied when {{userGroup}} uses {{targetGroup}}':
      'Коэффициент затрат, когда {{userGroup}} использует {{targetGroup}}',
    'Save group pricing': 'Сохранить тарифы групп',
    'Fixed by channel sharing settings': 'Задано настройками общего канала',
  },
  vi: {
    'Dynamic Profit Pricing': 'Định giá lợi nhuận động',
    'Group Pricing stores the base cost multiplier. This page computes the live profit multiplier on top of that cost.':
      'Định giá theo nhóm lưu hệ số chi phí cơ bản; trang này tính hệ số lợi nhuận theo thời gian thực trên chi phí đó.',
    'Enable dynamic profit pricing': 'Bật định giá lợi nhuận động',
    'The final charge is the group cost multiplier multiplied by this dynamic profit multiplier.':
      'Phí cuối cùng bằng hệ số chi phí nhóm nhân với hệ số lợi nhuận động.',
    'Minimum profit multiplier': 'Hệ số lợi nhuận tối thiểu',
    'The profit multiplier never falls below this value while dynamic pricing is enabled.':
      'Khi định giá động bật, hệ số lợi nhuận không thấp hơn giá trị này.',
    'Dynamic profit ceiling': 'Mức trần lợi nhuận động',
    'Caps the load-driven profit premium. Cost protection can still raise the effective multiplier when needed.':
      'Giới hạn phần lợi nhuận do tải; bảo vệ chi phí vẫn có thể tăng hệ số hiệu dụng khi cần.',
    'Reference model cost (USD / 1M tokens)':
      'Chi phí cơ sở của model (USD / 1 triệu token)',
    'Use the model cost baseline used to compare upstream cost with the configured group cost multiplier.':
      'Chi phí cơ sở dùng để so sánh chi phí upstream với hệ số chi phí nhóm.',
    'Cost protection margin': 'Biên bảo vệ chi phí',
    'Known upstream cost is multiplied by this margin before the cost floor is compared with profit pricing.':
      'Chi phí upstream đã biết được nhân với biên này trước khi so sánh với sàn chi phí.',
    'Live profit multiplier preview': 'Xem trước hệ số lợi nhuận trực tiếp',
    'Current dynamic profit multiplier': 'Hệ số lợi nhuận động hiện tại',
    'Cost × profit pricing preview': 'Xem trước định giá chi phí × lợi nhuận',
    'Group Pricing supplies the cost multiplier. Dynamic pricing supplies the profit multiplier. Final billing multiplies both.':
      'Định giá nhóm cung cấp chi phí, định giá động cung cấp lợi nhuận; phí cuối cùng nhân cả hai hệ số.',
    Formula: 'Công thức',
    'Final billing = group cost × dynamic profit':
      'Phí cuối = chi phí nhóm × lợi nhuận động',
    'Pricing group': 'Nhóm định giá',
    'Cost multiplier': 'Hệ số chi phí',
    'Profit multiplier': 'Hệ số lợi nhuận',
    'Effective billing multiplier': 'Hệ số tính phí hiệu dụng',
    'No pricing groups configured': 'Chưa cấu hình nhóm định giá',
    'Group cost multipliers': 'Hệ số chi phí nhóm',
    'JSON map of group → cost multiplier used as the base for that billing group. Dynamic pricing adds the profit multiplier.':
      'Bản đồ JSON nhóm → hệ số chi phí làm cơ sở cho nhóm tính phí; định giá động sẽ cộng phần lợi nhuận.',
    'Understand how user groups, cost multipliers, profit pricing, and special rules work together.':
      'Tìm hiểu nhóm người dùng, hệ số chi phí, lợi nhuận và quy tắc đặc biệt phối hợp như thế nào.',
    'decides which channels are used and which base cost multiplier applies.':
      'quyết định kênh được dùng và hệ số chi phí cơ bản áp dụng.',
    'decides the top-up ratio, which groups the user can pick for tokens, and whether a cost override applies.':
      'quyết định hệ số nạp, nhóm người dùng có thể chọn cho token và việc áp dụng ghi đè chi phí.',
    'Find the cost multiplier.': 'Tìm hệ số chi phí.',
    'Look for a special cost rule matching this user group and this billing group. If one exists, use its cost multiplier. Otherwise use the billing group base cost from the pricing table.':
      'Tìm quy tắc chi phí khớp nhóm người dùng và nhóm tính phí; nếu không có thì dùng chi phí cơ bản trong bảng định giá.',
    'Final charge = model base cost × group cost multiplier × dynamic profit multiplier.':
      'Phí cuối = chi phí cơ sở model × hệ số chi phí nhóm × hệ số lợi nhuận động.',
    'The group value is a cost basis, not a personal discount. Dynamic pricing supplies the profit multiplier separately.':
      'Giá trị nhóm là cơ sở chi phí, không phải giảm giá cá nhân; hệ số lợi nhuận được định giá động cung cấp riêng.',
    'Special cost rules': 'Quy tắc chi phí đặc biệt',
    'Cost basis = 10 × 0.3 = 3': 'Cơ sở chi phí = 10 × 0,3 = 3',
    'Cost basis = 10 × 1.0 = 10': 'Cơ sở chi phí = 10 × 1,0 = 10',
    'Cost basis = 10 × 0.8 = 8': 'Cơ sở chi phí = 10 × 0,8 = 8',
    'Users of vip, when billed as premium, use cost multiplier':
      'Người dùng vip khi tính phí theo premium dùng hệ số chi phí',
    'No rule for vip billed as default → use the base cost of default, 1.0 (the 0.8 of vip is not used)':
      'Không có quy tắc vip theo default → dùng chi phí cơ bản default 1,0 (không dùng 0,8 của vip).',
    'No rule for vip billed as vip → use the base cost of vip, 0.8':
      'Không có quy tắc vip theo vip → dùng chi phí cơ bản vip 0,8.',
    'Use the pricing group table to manage the cost multiplier and whether the group appears in the token creation dropdown.':
      'Dùng bảng nhóm định giá để quản lý hệ số chi phí và việc nhóm có xuất hiện trong danh sách tạo token hay không.',
    'In JSON, the user group is the outer key and the billing group is the inner key. The example below means: vip users use cost multiplier 0.8 when billed as standard, and 0.3 when billed as premium.':
      'Trong JSON, khóa ngoài là nhóm người dùng và khóa trong là nhóm tính phí; ví dụ vip dùng 0,8 khi tính theo standard và 0,3 khi tính theo premium.',
    'Only configured combinations are overridden. All other calls keep the billing group base cost multiplier.':
      'Chỉ các tổ hợp được cấu hình mới bị ghi đè; các yêu cầu khác giữ hệ số chi phí cơ bản của nhóm.',
    'Base cost multipliers': 'Hệ số chi phí cơ bản',
    "Manual order is preserved until you use Optimize. This changes the global order for every user, but runtime assignment still filters each user's visible groups. Optimize uses base cost multipliers by default; selecting a user group applies its exact special cost overrides before sorting.":
      'Thứ tự thủ công được giữ đến khi bạn tối ưu. Mặc định tối ưu theo hệ số chi phí cơ bản và áp dụng ghi đè chi phí của nhóm người dùng trước khi sắp xếp.',
    'Cost multipliers must be finite numbers greater than or equal to zero.':
      'Hệ số chi phí phải là số hữu hạn lớn hơn hoặc bằng 0.',
    'Optimize by effective cost': 'Tối ưu theo chi phí hiệu dụng',
    'Edit cost override': 'Sửa ghi đè chi phí',
    'Add cost override': 'Thêm ghi đè chi phí',
    'Configure a custom cost multiplier for "{{userGroup}}" users when using a specific token group.':
      'Cấu hình hệ số chi phí tùy chỉnh cho người dùng “{{userGroup}}” khi dùng nhóm token cụ thể.',
    'Configure a custom cost multiplier for when users use a specific token group.':
      'Cấu hình hệ số chi phí tùy chỉnh khi người dùng dùng nhóm token cụ thể.',
    'Invalid cost multiplier': 'Hệ số chi phí không hợp lệ',
    'Cost multiplier applied when {{userGroup}} uses {{targetGroup}}':
      'Hệ số chi phí áp dụng khi {{userGroup}} dùng {{targetGroup}}',
    'Save group pricing': 'Lưu định giá nhóm',
    'Fixed by channel sharing settings':
      'Được cố định bởi cài đặt chia sẻ kênh',
  },
}

for (const [locale, translations] of Object.entries(
  pricingRedesignTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const discountLinkTranslations = {
  en: {
    Applied: 'Applied',
    'Copy all generated links': 'Copy all generated links',
    'Copy selected links': 'Copy selected links',
    'Copy share link': 'Copy share link',
    'Copy these generated links now for distribution.':
      'Copy these generated links now for distribution.',
    'Discount code from URL': 'Discount code from URL',
    'Discount code saves {{amount}}': 'Discount code saves {{amount}}',
    'This code came from the checkout link and cannot be edited.':
      'This code came from the checkout link and cannot be edited.',
  },
  zh: {
    Applied: '已应用',
    'Copy all generated links': '复制全部生成链接',
    'Copy selected links': '复制选中链接',
    'Copy share link': '复制分享链接',
    'Copy these generated links now for distribution.':
      '复制以下生成的链接以便分发。',
    'Discount code from URL': '来自 URL 的优惠码',
    'Discount code saves {{amount}}': '优惠码已减免 {{amount}}',
    'This code came from the checkout link and cannot be edited.':
      '此优惠码来自结算链接，无法编辑。',
  },
  'zh-TW': {
    Applied: '已套用',
    'Copy all generated links': '複製全部產生的連結',
    'Copy selected links': '複製選取的連結',
    'Copy share link': '複製分享連結',
    'Copy these generated links now for distribution.':
      '複製以下產生的連結以便分發。',
    'Discount code from URL': '來自 URL 的優惠碼',
    'Discount code saves {{amount}}': '優惠碼已減免 {{amount}}',
    'This code came from the checkout link and cannot be edited.':
      '此優惠碼來自結帳連結，無法編輯。',
  },
  fr: {
    Applied: 'Appliqué',
    'Copy all generated links': 'Copier tous les liens générés',
    'Copy selected links': 'Copier les liens sélectionnés',
    'Copy share link': 'Copier le lien de partage',
    'Copy these generated links now for distribution.':
      'Copiez maintenant ces liens générés pour les distribuer.',
    'Discount code from URL': 'Code promo depuis l’URL',
    'Discount code saves {{amount}}': 'Le code promo économise {{amount}}',
    'This code came from the checkout link and cannot be edited.':
      'Ce code provient du lien de paiement et ne peut pas être modifié.',
  },
  ja: {
    Applied: '適用済み',
    'Copy all generated links': '生成したリンクをすべてコピー',
    'Copy selected links': '選択したリンクをコピー',
    'Copy share link': '共有リンクをコピー',
    'Copy these generated links now for distribution.':
      '配布用に生成したリンクをコピーしてください。',
    'Discount code from URL': 'URL からの割引コード',
    'Discount code saves {{amount}}': '割引コードの割引額: {{amount}}',
    'This code came from the checkout link and cannot be edited.':
      'このコードは決済リンクから提供されたため編集できません。',
  },
  ru: {
    Applied: 'Применено',
    'Copy all generated links': 'Копировать все созданные ссылки',
    'Copy selected links': 'Копировать выбранные ссылки',
    'Copy share link': 'Копировать ссылку для доступа',
    'Copy these generated links now for distribution.':
      'Скопируйте созданные ссылки для распространения.',
    'Discount code from URL': 'Промокод из URL',
    'Discount code saves {{amount}}': 'Промокод экономит {{amount}}',
    'This code came from the checkout link and cannot be edited.':
      'Этот код получен из ссылки оплаты и не может быть изменён.',
  },
  vi: {
    Applied: 'Đã áp dụng',
    'Copy all generated links': 'Sao chép tất cả liên kết đã tạo',
    'Copy selected links': 'Sao chép các liên kết đã chọn',
    'Copy share link': 'Sao chép liên kết chia sẻ',
    'Copy these generated links now for distribution.':
      'Sao chép các liên kết đã tạo để phân phối.',
    'Discount code from URL': 'Mã giảm giá từ URL',
    'Discount code saves {{amount}}': 'Mã giảm giá tiết kiệm {{amount}}',
    'This code came from the checkout link and cannot be edited.':
      'Mã này được cung cấp từ liên kết thanh toán và không thể chỉnh sửa.',
  },
}

for (const [locale, translations] of Object.entries(discountLinkTranslations)) {
  Object.assign(newKeys[locale], translations)
}

const registrationStatusTranslations = {
  en: {
    'Unable to load registration settings':
      'Unable to load registration settings',
    'The server did not return registration capabilities. Check your connection and try again.':
      'The server did not return registration capabilities. Check your connection and try again.',
  },
  zh: {
    'Unable to load registration settings': '无法加载注册配置',
    'The server did not return registration capabilities. Check your connection and try again.':
      '服务器没有返回注册能力配置，请检查网络后重试。',
  },
  'zh-TW': {
    'Unable to load registration settings': '無法載入註冊設定',
    'The server did not return registration capabilities. Check your connection and try again.':
      '伺服器沒有返回註冊能力設定，請檢查網路後重試。',
  },
  fr: {
    'Unable to load registration settings':
      'Impossible de charger les paramètres d’inscription',
    'The server did not return registration capabilities. Check your connection and try again.':
      'Le serveur n’a pas renvoyé les capacités d’inscription. Vérifiez votre connexion et réessayez.',
  },
  ja: {
    'Unable to load registration settings': '登録設定を読み込めません',
    'The server did not return registration capabilities. Check your connection and try again.':
      'サーバーから登録機能の情報が返りませんでした。接続を確認して再試行してください。',
  },
  ru: {
    'Unable to load registration settings':
      'Не удалось загрузить настройки регистрации',
    'The server did not return registration capabilities. Check your connection and try again.':
      'Сервер не вернул сведения о регистрации. Проверьте подключение и повторите попытку.',
  },
  vi: {
    'Unable to load registration settings': 'Không thể tải cài đặt đăng ký',
    'The server did not return registration capabilities. Check your connection and try again.':
      'Máy chủ không trả về khả năng đăng ký. Hãy kiểm tra kết nối rồi thử lại.',
  },
}

for (const [locale, translations] of Object.entries(
  registrationStatusTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const legalConsentTranslations = {
  en: { 'and the': 'and the' },
  zh: { 'and the': '和' },
  'zh-TW': { 'and the': '和' },
  fr: { 'and the': 'et la' },
  ja: { 'and the': 'および' },
  ru: { 'and the': 'и' },
  vi: { 'and the': 'và' },
}

for (const [locale, translations] of Object.entries(legalConsentTranslations)) {
  Object.assign(newKeys[locale], translations)
}

const assistantToolTranslations = {
  en: { '{{count}} input parameters': '{{count}} input parameters' },
  zh: { '{{count}} input parameters': '{{count}} 个输入参数' },
  'zh-TW': { '{{count}} input parameters': '{{count}} 個輸入參數' },
  fr: { '{{count}} input parameters': '{{count}} paramètres d’entrée' },
  ja: { '{{count}} input parameters': '入力パラメータ {{count}} 個' },
  ru: { '{{count}} input parameters': 'Входные параметры: {{count}}' },
  vi: { '{{count}} input parameters': '{{count}} tham số đầu vào' },
}

for (const [locale, translations] of Object.entries(
  assistantToolTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const drawingMcpTranslations = {
  en: {
    'Drawing MCP': 'Drawing MCP',
    'Connect an Agent to this drawing workbench with the dedicated MCP endpoint. Generation keeps the same group permissions and billing as this page.':
      'Connect an Agent to this drawing workbench with the dedicated MCP endpoint. Generation keeps the same group permissions and billing as this page.',
    'Generate or rotate the personal MCP token? Existing MCP agents using the old token will stop working immediately.':
      'Generate or rotate the personal MCP token? Existing MCP agents using the old token will stop working immediately.',
    'Drawing MCP configuration copied.': 'Drawing MCP configuration copied.',
    'Unable to copy the drawing MCP configuration.':
      'Unable to copy the drawing MCP configuration.',
    'Unable to create the drawing MCP configuration.':
      'Unable to create the drawing MCP configuration.',
    'Copy drawing MCP config': 'Copy drawing MCP config',
    'Generate token and copy config': 'Generate token and copy config',
    'Agent configuration': 'Agent configuration',
    'The personal token is shown only in this session. Store the copied configuration in your Agent securely.':
      'The personal token is shown only in this session. Store the copied configuration in your Agent securely.',
  },
  zh: {
    'Drawing MCP': '绘图 MCP',
    'Connect an Agent to this drawing workbench with the dedicated MCP endpoint. Generation keeps the same group permissions and billing as this page.':
      '将 Agent 连接到此绘图工作台，使用专用 MCP 端点。生成操作沿用本页面的分组权限和计费规则。',
    'Generate or rotate the personal MCP token? Existing MCP agents using the old token will stop working immediately.':
      '生成或轮换个人 MCP 令牌？使用旧令牌的现有 MCP Agent 将立即停止工作。',
    'Drawing MCP configuration copied.': '绘图 MCP 配置已复制。',
    'Unable to copy the drawing MCP configuration.': '无法复制绘图 MCP 配置。',
    'Unable to create the drawing MCP configuration.':
      '无法创建绘图 MCP 配置。',
    'Copy drawing MCP config': '复制绘图 MCP 配置',
    'Generate token and copy config': '生成令牌并复制配置',
    'Agent configuration': 'Agent 配置',
    'The personal token is shown only in this session. Store the copied configuration in your Agent securely.':
      '个人令牌仅在本次会话中显示。请将复制的配置安全地保存到 Agent。',
  },
  'zh-TW': {
    'Drawing MCP': '繪圖 MCP',
    'Connect an Agent to this drawing workbench with the dedicated MCP endpoint. Generation keeps the same group permissions and billing as this page.':
      '將 Agent 連接到此繪圖工作台，使用專用 MCP 端點。生成操作沿用本頁的分組權限與計費規則。',
    'Generate or rotate the personal MCP token? Existing MCP agents using the old token will stop working immediately.':
      '要生成或輪換個人 MCP 權杖嗎？使用舊權杖的現有 MCP Agent 會立即停止運作。',
    'Drawing MCP configuration copied.': '繪圖 MCP 設定已複製。',
    'Unable to copy the drawing MCP configuration.': '無法複製繪圖 MCP 設定。',
    'Unable to create the drawing MCP configuration.':
      '無法建立繪圖 MCP 設定。',
    'Copy drawing MCP config': '複製繪圖 MCP 設定',
    'Generate token and copy config': '生成權杖並複製設定',
    'Agent configuration': 'Agent 設定',
    'The personal token is shown only in this session. Store the copied configuration in your Agent securely.':
      '個人權杖僅在本次工作階段顯示。請將複製的設定安全地儲存到 Agent。',
  },
  fr: {
    'Drawing MCP': 'MCP de dessin',
    'Connect an Agent to this drawing workbench with the dedicated MCP endpoint. Generation keeps the same group permissions and billing as this page.':
      'Connectez un Agent à cet atelier de dessin via le point de terminaison MCP dédié. La génération conserve les mêmes droits de groupe et la même facturation que cette page.',
    'Generate or rotate the personal MCP token? Existing MCP agents using the old token will stop working immediately.':
      'Générer ou renouveler le jeton MCP personnel ? Les Agents MCP utilisant l’ancien jeton cesseront immédiatement de fonctionner.',
    'Drawing MCP configuration copied.':
      'Configuration du MCP de dessin copiée.',
    'Unable to copy the drawing MCP configuration.':
      'Impossible de copier la configuration du MCP de dessin.',
    'Unable to create the drawing MCP configuration.':
      'Impossible de créer la configuration du MCP de dessin.',
    'Copy drawing MCP config': 'Copier la configuration MCP de dessin',
    'Generate token and copy config':
      'Générer le jeton et copier la configuration',
    'Agent configuration': 'Configuration de l’Agent',
    'The personal token is shown only in this session. Store the copied configuration in your Agent securely.':
      'Le jeton personnel est affiché uniquement pendant cette session. Conservez la configuration copiée en sécurité dans votre Agent.',
  },
  ja: {
    'Drawing MCP': '描画 MCP',
    'Connect an Agent to this drawing workbench with the dedicated MCP endpoint. Generation keeps the same group permissions and billing as this page.':
      '専用 MCP エンドポイントで Agent をこの描画ワークベンチに接続します。生成にはこのページと同じグループ権限と料金が適用されます。',
    'Generate or rotate the personal MCP token? Existing MCP agents using the old token will stop working immediately.':
      '個人 MCP トークンを生成またはローテーションしますか？古いトークンを使う既存の MCP Agent は直ちに利用できなくなります。',
    'Drawing MCP configuration copied.': '描画 MCP 設定をコピーしました。',
    'Unable to copy the drawing MCP configuration.':
      '描画 MCP 設定をコピーできません。',
    'Unable to create the drawing MCP configuration.':
      '描画 MCP 設定を作成できません。',
    'Copy drawing MCP config': '描画 MCP 設定をコピー',
    'Generate token and copy config': 'トークンを生成して設定をコピー',
    'Agent configuration': 'Agent 設定',
    'The personal token is shown only in this session. Store the copied configuration in your Agent securely.':
      '個人トークンはこのセッションでのみ表示されます。コピーした設定は Agent に安全に保存してください。',
  },
  ru: {
    'Drawing MCP': 'MCP для рисования',
    'Connect an Agent to this drawing workbench with the dedicated MCP endpoint. Generation keeps the same group permissions and billing as this page.':
      'Подключите Agent к этой рабочей области рисования через отдельную конечную точку MCP. Генерация использует те же права группы и тарификацию, что и эта страница.',
    'Generate or rotate the personal MCP token? Existing MCP agents using the old token will stop working immediately.':
      'Создать или обновить персональный MCP-токен? Существующие MCP Agent со старым токеном сразу перестанут работать.',
    'Drawing MCP configuration copied.':
      'Конфигурация MCP для рисования скопирована.',
    'Unable to copy the drawing MCP configuration.':
      'Не удалось скопировать конфигурацию MCP для рисования.',
    'Unable to create the drawing MCP configuration.':
      'Не удалось создать конфигурацию MCP для рисования.',
    'Copy drawing MCP config': 'Скопировать конфигурацию MCP для рисования',
    'Generate token and copy config':
      'Создать токен и скопировать конфигурацию',
    'Agent configuration': 'Конфигурация Agent',
    'The personal token is shown only in this session. Store the copied configuration in your Agent securely.':
      'Персональный токен отображается только в этой сессии. Надёжно сохраните скопированную конфигурацию в Agent.',
  },
  vi: {
    'Drawing MCP': 'MCP vẽ ảnh',
    'Connect an Agent to this drawing workbench with the dedicated MCP endpoint. Generation keeps the same group permissions and billing as this page.':
      'Kết nối Agent với bàn vẽ này qua endpoint MCP riêng. Việc tạo ảnh dùng cùng quyền nhóm và cách tính phí như trang này.',
    'Generate or rotate the personal MCP token? Existing MCP agents using the old token will stop working immediately.':
      'Tạo hoặc xoay vòng token MCP cá nhân? Các MCP Agent đang dùng token cũ sẽ ngừng hoạt động ngay lập tức.',
    'Drawing MCP configuration copied.': 'Đã sao chép cấu hình MCP vẽ ảnh.',
    'Unable to copy the drawing MCP configuration.':
      'Không thể sao chép cấu hình MCP vẽ ảnh.',
    'Unable to create the drawing MCP configuration.':
      'Không thể tạo cấu hình MCP vẽ ảnh.',
    'Copy drawing MCP config': 'Sao chép cấu hình MCP vẽ ảnh',
    'Generate token and copy config': 'Tạo token và sao chép cấu hình',
    'Agent configuration': 'Cấu hình Agent',
    'The personal token is shown only in this session. Store the copied configuration in your Agent securely.':
      'Token cá nhân chỉ hiển thị trong phiên này. Hãy lưu cấu hình đã sao chép an toàn trong Agent.',
  },
}

for (const [locale, translations] of Object.entries(drawingMcpTranslations)) {
  Object.assign(newKeys[locale], translations)
}

const automaticReviewTranslations = {
  en: {
    'You can submit without an AI recommendation. The automatic review agent handles clear requests; uncertain cases remain available for human fallback.':
      'You can submit without an AI recommendation. The automatic review agent handles clear requests; uncertain cases remain available for human fallback.',
    'Your AI recommendation was submitted to the automatic review agent. L1 remains locked until automatic review approves it or human fallback completes.':
      'Your AI recommendation was submitted to the automatic review agent. L1 remains locked until automatic review approves it or human fallback completes.',
    'L0 access is restricted. Ask the assistant to prepare an L1 recommendation; after automatic review approval or human fallback, return here to create a key.':
      'L0 access is restricted. Ask the assistant to prepare an L1 recommendation; after automatic review approval or human fallback, return here to create a key.',
    'Connection values and API key creation unlock after automatic review approves L1; uncertain cases use human fallback.':
      'Connection values and API key creation unlock after automatic review approves L1; uncertain cases use human fallback.',
    'You can compare live plans and discounts now. Checkout and payment remain locked until automatic review approves L1 or human fallback completes.':
      'You can compare live plans and discounts now. Checkout and payment remain locked until automatic review approves L1 or human fallback completes.',
  },
  zh: {
    'You can submit without an AI recommendation. The automatic review agent handles clear requests; uncertain cases remain available for human fallback.':
      '你可以不附带 AI 推荐信直接提交。自动审核 Agent 会处理证据清晰的申请；不确定的申请会保留给人工兜底。',
    'Your AI recommendation was submitted to the automatic review agent. L1 remains locked until automatic review approves it or human fallback completes.':
      'AI 推荐信已提交给自动审核 Agent。自动审核通过或人工兜底完成前，L1 仍保持锁定。',
    'L0 access is restricted. Ask the assistant to prepare an L1 recommendation; after automatic review approval or human fallback, return here to create a key.':
      'L0 无法使用此功能。请让助手准备 L1 推荐信；自动审核通过或人工兜底完成后，再回来创建密钥。',
    'Connection values and API key creation unlock after automatic review approves L1; uncertain cases use human fallback.':
      '自动审核通过 L1 后即可解锁连接信息和 API 密钥创建；不确定的申请会转人工兜底。',
    'You can compare live plans and discounts now. Checkout and payment remain locked until automatic review approves L1 or human fallback completes.':
      '你现在可以比较实时套餐和优惠；自动审核通过 L1 或人工兜底完成前，结算和支付仍保持锁定。',
  },
  'zh-TW': {
    'You can submit without an AI recommendation. The automatic review agent handles clear requests; uncertain cases remain available for human fallback.':
      '你可以不附帶 AI 推薦信直接提交。自動審核 Agent 會處理證據清楚的申請；不確定的申請會保留給人工兜底。',
    'Your AI recommendation was submitted to the automatic review agent. L1 remains locked until automatic review approves it or human fallback completes.':
      'AI 推薦信已提交給自動審核 Agent。自動審核通過或人工兜底完成前，L1 仍保持鎖定。',
    'L0 access is restricted. Ask the assistant to prepare an L1 recommendation; after automatic review approval or human fallback, return here to create a key.':
      'L0 無法使用此功能。請讓助手準備 L1 推薦信；自動審核通過或人工兜底完成後，再回來建立金鑰。',
    'Connection values and API key creation unlock after automatic review approves L1; uncertain cases use human fallback.':
      '自動審核通過 L1 後即可解鎖連線資訊和 API 金鑰建立；不確定的申請會轉人工兜底。',
    'You can compare live plans and discounts now. Checkout and payment remain locked until automatic review approves L1 or human fallback completes.':
      '你現在可以比較即時方案和優惠；自動審核通過 L1 或人工兜底完成前，結帳和付款仍保持鎖定。',
  },
  fr: {
    'You can submit without an AI recommendation. The automatic review agent handles clear requests; uncertain cases remain available for human fallback.':
      'Vous pouvez envoyer la demande sans recommandation IA. Les cas clairs sont traités automatiquement ; les cas incertains restent disponibles pour une revue humaine.',
    'Your AI recommendation was submitted to the automatic review agent. L1 remains locked until automatic review approves it or human fallback completes.':
      'Votre recommandation IA a été envoyée à la revue automatique. L1 reste verrouillé jusqu’à son approbation ou la fin de la revue humaine.',
    'L0 access is restricted. Ask the assistant to prepare an L1 recommendation; after automatic review approval or human fallback, return here to create a key.':
      'L’accès L0 est limité. Demandez une recommandation L1, puis créez une clé après la revue automatique ou humaine.',
    'Connection values and API key creation unlock after automatic review approves L1; uncertain cases use human fallback.':
      'Les informations de connexion et la création de clé se débloquent après l’approbation automatique de L1 ; les cas incertains passent en revue humaine.',
    'You can compare live plans and discounts now. Checkout and payment remain locked until automatic review approves L1 or human fallback completes.':
      'Vous pouvez comparer les offres actuelles ; le paiement reste verrouillé jusqu’à l’approbation automatique de L1 ou la revue humaine.',
  },
  ja: {
    'You can submit without an AI recommendation. The automatic review agent handles clear requests; uncertain cases remain available for human fallback.':
      'AI 推薦文なしで申請できます。明確な申請は自動審査が処理し、不確かな申請は人による審査に回せます。',
    'Your AI recommendation was submitted to the automatic review agent. L1 remains locked until automatic review approves it or human fallback completes.':
      'AI 推薦文を自動審査に送信しました。自動承認または人による審査が完了するまで L1 はロックされます。',
    'L0 access is restricted. Ask the assistant to prepare an L1 recommendation; after automatic review approval or human fallback, return here to create a key.':
      'L0 では利用できません。L1 推薦文を作成し、自動審査または人による審査の後にキーを作成してください。',
    'Connection values and API key creation unlock after automatic review approves L1; uncertain cases use human fallback.':
      '自動審査で L1 が承認されると接続情報と API キー作成が解放されます。不確かな申請は人による審査に回ります。',
    'You can compare live plans and discounts now. Checkout and payment remain locked until automatic review approves L1 or human fallback completes.':
      'プランと割引は比較できます。自動審査または人による審査が完了するまで決済はロックされます。',
  },
  ru: {
    'You can submit without an AI recommendation. The automatic review agent handles clear requests; uncertain cases remain available for human fallback.':
      'Заявку можно отправить без рекомендации ИИ. Ясные случаи обработает автоматическая проверка, а сомнительные останутся для человека.',
    'Your AI recommendation was submitted to the automatic review agent. L1 remains locked until automatic review approves it or human fallback completes.':
      'Рекомендация ИИ отправлена на автоматическую проверку. L1 останется заблокированным до одобрения или проверки человеком.',
    'L0 access is restricted. Ask the assistant to prepare an L1 recommendation; after automatic review approval or human fallback, return here to create a key.':
      'Для L0 доступ ограничен. Подготовьте рекомендацию L1 и создайте ключ после автоматической или человеческой проверки.',
    'Connection values and API key creation unlock after automatic review approves L1; uncertain cases use human fallback.':
      'Данные подключения и создание API-ключа откроются после автоматического одобрения L1; сомнительные случаи передаются человеку.',
    'You can compare live plans and discounts now. Checkout and payment remain locked until automatic review approves L1 or human fallback completes.':
      'Можно сравнить планы и скидки; оплата останется заблокирована до автоматического или человеческого одобрения L1.',
  },
  vi: {
    'You can submit without an AI recommendation. The automatic review agent handles clear requests; uncertain cases remain available for human fallback.':
      'Bạn có thể gửi yêu cầu mà không cần đề xuất AI. Agent tự động xử lý hồ sơ rõ ràng; hồ sơ chưa chắc chắn sẽ chuyển sang người xét duyệt.',
    'Your AI recommendation was submitted to the automatic review agent. L1 remains locked until automatic review approves it or human fallback completes.':
      'Đề xuất AI đã được gửi cho agent tự động. L1 vẫn khóa cho đến khi tự động duyệt hoặc xét duyệt thủ công hoàn tất.',
    'L0 access is restricted. Ask the assistant to prepare an L1 recommendation; after automatic review approval or human fallback, return here to create a key.':
      'L0 bị giới hạn. Hãy nhờ trợ lý chuẩn bị đề xuất L1 rồi tạo key sau khi tự động duyệt hoặc xét duyệt thủ công.',
    'Connection values and API key creation unlock after automatic review approves L1; uncertain cases use human fallback.':
      'Thông tin kết nối và tạo API key mở sau khi tự động duyệt L1; hồ sơ chưa chắc chắn sẽ chuyển sang người xét duyệt.',
    'You can compare live plans and discounts now. Checkout and payment remain locked until automatic review approves L1 or human fallback completes.':
      'Bạn có thể so sánh gói và ưu đãi; thanh toán vẫn khóa cho đến khi tự động duyệt L1 hoặc xét duyệt thủ công hoàn tất.',
  },
}

for (const [locale, translations] of Object.entries(
  automaticReviewTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const assistantRoutingTranslations = {
  en: {
    'Select the routing group used by the assistant. Choose the exact model ID in the field beside it.':
      'Select the routing group used by the assistant. Choose the exact model ID in the field beside it.',
    'The built-in AI assistant is under maintenance. Please try again later.':
      'The built-in AI assistant is under maintenance. Please try again later.',
    'Get model list': 'Get model list',
    'Choose a group, then click Get model list to load its enabled model IDs.':
      'Choose a group, then click Get model list to load its enabled model IDs.',
    'Loading model list...': 'Loading model list...',
    'Select the routing group used by the assistant, then get its enabled model IDs.':
      'Select the routing group used by the assistant, then get its enabled model IDs.',
    'Assistant model ID': 'Assistant model ID',
    'Select a model ID': 'Select a model ID',
    'not enabled': 'not enabled',
    'Unable to enumerate model IDs for this group. Check the live model catalog and try again.':
      'Unable to enumerate model IDs for this group. Check the live model catalog and try again.',
    'This group has no enabled model IDs.':
      'This group has no enabled model IDs.',
    'The assistant sends requests with this exact enabled model ID and the selected routing group.':
      'The assistant sends requests with this exact enabled model ID and the selected routing group.',
    'Assistant routing is unavailable': 'Assistant routing is unavailable',
    'The selected assistant group or model ID is unavailable. Ask an administrator to choose an enabled group and exact model ID, then retry.':
      'The selected assistant group or model ID is unavailable. Ask an administrator to choose an enabled group and exact model ID, then retry.',
    'Assistant routing is unavailable. Check the configured group and model ID, then retry.':
      'Assistant routing is unavailable. Check the configured group and model ID, then retry.',
    'The assistant model catalog is temporarily unavailable. Check the model catalog and retry.':
      'The assistant model catalog is temporarily unavailable. Check the model catalog and retry.',
    'The assistant is busy right now. Please retry shortly.':
      'The assistant is busy right now. Please retry shortly.',
    'The AI assistant could not answer right now. Try again or contact support. (Error: {{code}}.)':
      'The AI assistant could not answer right now. Try again or contact support. (Error: {{code}}.)',
  },
  zh: {
    'Select the routing group used by the assistant. Choose the exact model ID in the field beside it.':
      '选择助手使用的路由分组，再在旁边选择准确的模型 ID。',
    'The built-in AI assistant is under maintenance. Please try again later.':
      '内置AI助手正在维护中，请稍后再试。',
    'Get model list': '获取模型列表',
    'Choose a group, then click Get model list to load its enabled model IDs.':
      '选择分组，然后点击“获取模型列表”加载该分组已启用的模型 ID。',
    'Loading model list...': '正在加载模型列表……',
    'Select the routing group used by the assistant, then get its enabled model IDs.':
      '选择助手使用的路由分组，然后获取其中已启用的模型 ID。',
    'Assistant model ID': '助手模型 ID',
    'Select a model ID': '选择模型 ID',
    'not enabled': '未启用',
    'Unable to enumerate model IDs for this group. Check the live model catalog and try again.':
      '无法枚举该分组的模型 ID，请检查实时模型目录后重试。',
    'This group has no enabled model IDs.': '该分组没有已启用的模型 ID。',
    'The assistant sends requests with this exact enabled model ID and the selected routing group.':
      '助手会使用这个准确的已启用模型 ID 和所选路由分组发送请求。',
    'Assistant routing is unavailable': '助手路由不可用',
    'The selected assistant group or model ID is unavailable. Ask an administrator to choose an enabled group and exact model ID, then retry.':
      '所选助手分组或模型 ID 不可用，请让管理员选择已启用的分组和准确模型 ID 后重试。',
    'Assistant routing is unavailable. Check the configured group and model ID, then retry.':
      '助手路由不可用，请检查配置的分组和模型 ID 后重试。',
    'The assistant model catalog is temporarily unavailable. Check the model catalog and retry.':
      '助手模型目录暂时不可用，请检查模型目录后重试。',
    'The assistant is busy right now. Please retry shortly.':
      '助手当前繁忙，请稍后重试。',
    'The AI assistant could not answer right now. Try again or contact support. (Error: {{code}}.)':
      'AI 助手暂时无法回答，请重试或联系人工支持。（错误：{{code}}。）',
  },
  'zh-TW': {
    'Select the routing group used by the assistant. Choose the exact model ID in the field beside it.':
      '選擇助手使用的路由分組，再在旁邊選擇準確的模型 ID。',
    'The built-in AI assistant is under maintenance. Please try again later.':
      '內建 AI 助手正在維護中，請稍後再試。',
    'Get model list': '取得模型清單',
    'Choose a group, then click Get model list to load its enabled model IDs.':
      '選擇分組，然後點擊「取得模型清單」載入該分組已啟用的模型 ID。',
    'Loading model list...': '正在載入模型清單……',
    'Select the routing group used by the assistant, then get its enabled model IDs.':
      '選擇助手使用的路由分組，然後取得其中已啟用的模型 ID。',
    'Assistant model ID': '助手模型 ID',
    'Select a model ID': '選擇模型 ID',
    'not enabled': '未啟用',
    'Unable to enumerate model IDs for this group. Check the live model catalog and try again.':
      '無法列出此分組的模型 ID，請檢查即時模型目錄後重試。',
    'This group has no enabled model IDs.': '此分組沒有已啟用的模型 ID。',
    'The assistant sends requests with this exact enabled model ID and the selected routing group.':
      '助手會使用這個準確的已啟用模型 ID 與所選路由分組發送請求。',
    'Assistant routing is unavailable': '助手路由不可用',
    'The selected assistant group or model ID is unavailable. Ask an administrator to choose an enabled group and exact model ID, then retry.':
      '所選助手分組或模型 ID 不可用，請讓管理員選擇已啟用的分組和準確模型 ID 後重試。',
    'Assistant routing is unavailable. Check the configured group and model ID, then retry.':
      '助手路由不可用，請檢查設定的分組和模型 ID 後重試。',
    'The assistant model catalog is temporarily unavailable. Check the model catalog and retry.':
      '助手模型目錄暫時不可用，請檢查模型目錄後重試。',
    'The assistant is busy right now. Please retry shortly.':
      '助手目前繁忙，請稍後重試。',
    'The AI assistant could not answer right now. Try again or contact support. (Error: {{code}}.)':
      'AI 助手暫時無法回答，請重試或聯絡人工支援。（錯誤：{{code}}。）',
  },
  fr: {
    'Select the routing group used by the assistant. Choose the exact model ID in the field beside it.':
      'Sélectionnez le groupe de routage de l’assistant, puis l’identifiant exact du modèle à côté.',
    'The built-in AI assistant is under maintenance. Please try again later.':
      'L’assistant IA intégré est en maintenance. Veuillez réessayer plus tard.',
    'Get model list': 'Charger la liste des modèles',
    'Choose a group, then click Get model list to load its enabled model IDs.':
      'Choisissez un groupe, puis cliquez sur « Charger la liste des modèles » pour charger ses identifiants activés.',
    'Loading model list...': 'Chargement de la liste des modèles…',
    'Select the routing group used by the assistant, then get its enabled model IDs.':
      'Sélectionnez le groupe de routage de l’assistant, puis chargez ses identifiants de modèle activés.',
    'Assistant model ID': 'Identifiant du modèle de l’assistant',
    'Select a model ID': 'Sélectionner un identifiant de modèle',
    'not enabled': 'non activé',
    'Unable to enumerate model IDs for this group. Check the live model catalog and try again.':
      'Impossible de lister les identifiants de modèle de ce groupe. Vérifiez le catalogue en direct et réessayez.',
    'This group has no enabled model IDs.':
      'Ce groupe ne possède aucun identifiant de modèle activé.',
    'The assistant sends requests with this exact enabled model ID and the selected routing group.':
      'L’assistant utilise cet identifiant de modèle activé exact et le groupe de routage sélectionné.',
    'Assistant routing is unavailable': 'Routage de l’assistant indisponible',
    'The selected assistant group or model ID is unavailable. Ask an administrator to choose an enabled group and exact model ID, then retry.':
      'Le groupe ou l’identifiant de modèle sélectionné est indisponible. Demandez à un administrateur de choisir un groupe et un identifiant activés, puis réessayez.',
    'Assistant routing is unavailable. Check the configured group and model ID, then retry.':
      'Le routage de l’assistant est indisponible. Vérifiez le groupe et l’identifiant configurés, puis réessayez.',
    'The assistant model catalog is temporarily unavailable. Check the model catalog and retry.':
      'Le catalogue de modèles de l’assistant est temporairement indisponible. Vérifiez-le et réessayez.',
    'The assistant is busy right now. Please retry shortly.':
      'L’assistant est actuellement occupé. Réessayez dans un instant.',
    'The AI assistant could not answer right now. Try again or contact support. (Error: {{code}}.)':
      'L’assistant IA ne peut pas répondre pour le moment. Réessayez ou contactez l’assistance. (Erreur : {{code}}.)',
  },
  ja: {
    'Select the routing group used by the assistant. Choose the exact model ID in the field beside it.':
      'アシスタントが使用するルーティンググループを選び、隣で正確なモデル ID を選択してください。',
    'The built-in AI assistant is under maintenance. Please try again later.':
      '内蔵 AI アシスタントはメンテナンス中です。後でもう一度お試しください。',
    'Get model list': 'モデル一覧を取得',
    'Choose a group, then click Get model list to load its enabled model IDs.':
      'グループを選択し、「モデル一覧を取得」をクリックして有効なモデル ID を読み込みます。',
    'Loading model list...': 'モデル一覧を読み込み中…',
    'Select the routing group used by the assistant, then get its enabled model IDs.':
      'アシスタントが使用するルーティンググループを選択し、有効なモデル ID を取得してください。',
    'Assistant model ID': 'アシスタントモデル ID',
    'Select a model ID': 'モデル ID を選択',
    'not enabled': '未有効',
    'Unable to enumerate model IDs for this group. Check the live model catalog and try again.':
      'このグループのモデル ID を列挙できません。ライブモデルカタログを確認して再試行してください。',
    'This group has no enabled model IDs.':
      'このグループには有効なモデル ID がありません。',
    'The assistant sends requests with this exact enabled model ID and the selected routing group.':
      'アシスタントは、選択したルーティンググループでこの有効なモデル ID に正確にリクエストを送信します。',
    'Assistant routing is unavailable':
      'アシスタントのルーティングを利用できません',
    'The selected assistant group or model ID is unavailable. Ask an administrator to choose an enabled group and exact model ID, then retry.':
      '選択したグループまたはモデル ID を利用できません。管理者に有効なグループと正確なモデル ID を選んでもらい、再試行してください。',
    'Assistant routing is unavailable. Check the configured group and model ID, then retry.':
      'アシスタントのルーティングを利用できません。設定したグループとモデル ID を確認して再試行してください。',
    'The assistant model catalog is temporarily unavailable. Check the model catalog and retry.':
      'アシスタントのモデルカタログを一時的に利用できません。カタログを確認して再試行してください。',
    'The assistant is busy right now. Please retry shortly.':
      'アシスタントは現在混み合っています。少し待ってから再試行してください。',
    'The AI assistant could not answer right now. Try again or contact support. (Error: {{code}}.)':
      'AI アシスタントは現在回答できません。再試行するかサポートにお問い合わせください。（エラー：{{code}}。）',
  },
  ru: {
    'Select the routing group used by the assistant. Choose the exact model ID in the field beside it.':
      'Выберите группу маршрутизации помощника, затем точный идентификатор модели рядом.',
    'The built-in AI assistant is under maintenance. Please try again later.':
      'Встроенный ИИ-помощник находится на обслуживании. Повторите попытку позже.',
    'Get model list': 'Получить список моделей',
    'Choose a group, then click Get model list to load its enabled model IDs.':
      'Выберите группу и нажмите «Получить список моделей», чтобы загрузить включённые идентификаторы моделей.',
    'Loading model list...': 'Загрузка списка моделей…',
    'Select the routing group used by the assistant, then get its enabled model IDs.':
      'Выберите группу маршрутизации помощника, затем загрузите включённые идентификаторы моделей.',
    'Assistant model ID': 'Идентификатор модели помощника',
    'Select a model ID': 'Выберите идентификатор модели',
    'not enabled': 'не включён',
    'Unable to enumerate model IDs for this group. Check the live model catalog and try again.':
      'Не удалось перечислить идентификаторы моделей этой группы. Проверьте актуальный каталог и повторите попытку.',
    'This group has no enabled model IDs.':
      'В этой группе нет включённых идентификаторов моделей.',
    'The assistant sends requests with this exact enabled model ID and the selected routing group.':
      'Помощник отправляет запросы с этим точным включённым идентификатором модели и выбранной группой маршрутизации.',
    'Assistant routing is unavailable': 'Маршрутизация помощника недоступна',
    'The selected assistant group or model ID is unavailable. Ask an administrator to choose an enabled group and exact model ID, then retry.':
      'Выбранная группа или идентификатор модели недоступны. Попросите администратора выбрать включённые группу и точный идентификатор, затем повторите попытку.',
    'Assistant routing is unavailable. Check the configured group and model ID, then retry.':
      'Маршрутизация помощника недоступна. Проверьте настроенные группу и идентификатор модели, затем повторите попытку.',
    'The assistant model catalog is temporarily unavailable. Check the model catalog and retry.':
      'Каталог моделей помощника временно недоступен. Проверьте каталог и повторите попытку.',
    'The assistant is busy right now. Please retry shortly.':
      'Помощник сейчас занят. Повторите попытку чуть позже.',
    'The AI assistant could not answer right now. Try again or contact support. (Error: {{code}}.)':
      'ИИ-помощник сейчас не может ответить. Повторите попытку или обратитесь в поддержку. (Ошибка: {{code}}.)',
  },
  vi: {
    'Select the routing group used by the assistant. Choose the exact model ID in the field beside it.':
      'Chọn nhóm định tuyến cho trợ lý, sau đó chọn đúng model ID ở bên cạnh.',
    'The built-in AI assistant is under maintenance. Please try again later.':
      'Trợ lý AI tích hợp đang được bảo trì. Vui lòng thử lại sau.',
    'Get model list': 'Lấy danh sách model',
    'Choose a group, then click Get model list to load its enabled model IDs.':
      'Chọn một nhóm, sau đó bấm Lấy danh sách model để tải các model ID đang bật của nhóm.',
    'Loading model list...': 'Đang tải danh sách model…',
    'Select the routing group used by the assistant, then get its enabled model IDs.':
      'Chọn nhóm định tuyến cho trợ lý, sau đó lấy các model ID đang bật.',
    'Assistant model ID': 'Model ID của trợ lý',
    'Select a model ID': 'Chọn model ID',
    'not enabled': 'chưa bật',
    'Unable to enumerate model IDs for this group. Check the live model catalog and try again.':
      'Không thể liệt kê model ID của nhóm này. Hãy kiểm tra danh mục model trực tiếp rồi thử lại.',
    'This group has no enabled model IDs.':
      'Nhóm này không có model ID nào đang bật.',
    'The assistant sends requests with this exact enabled model ID and the selected routing group.':
      'Trợ lý gửi yêu cầu bằng đúng model ID đang bật này và nhóm định tuyến đã chọn.',
    'Assistant routing is unavailable': 'Định tuyến trợ lý không khả dụng',
    'The selected assistant group or model ID is unavailable. Ask an administrator to choose an enabled group and exact model ID, then retry.':
      'Nhóm hoặc model ID của trợ lý đã chọn không khả dụng. Hãy nhờ quản trị viên chọn nhóm và model ID đang bật rồi thử lại.',
    'Assistant routing is unavailable. Check the configured group and model ID, then retry.':
      'Định tuyến trợ lý không khả dụng. Hãy kiểm tra nhóm và model ID đã cấu hình rồi thử lại.',
    'The assistant model catalog is temporarily unavailable. Check the model catalog and retry.':
      'Danh mục model của trợ lý tạm thời không khả dụng. Hãy kiểm tra danh mục rồi thử lại.',
    'The assistant is busy right now. Please retry shortly.':
      'Trợ lý đang bận. Vui lòng thử lại sau ít phút.',
    'The AI assistant could not answer right now. Try again or contact support. (Error: {{code}}.)':
      'Trợ lý AI hiện không thể trả lời. Hãy thử lại hoặc liên hệ hỗ trợ. (Lỗi: {{code}}.)',
  },
}

for (const [locale, translations] of Object.entries(
  assistantRoutingTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const assistantKeyCreationTranslations = {
  en: {
    'Unable to load selectable key groups. Try again.':
      'Unable to load selectable key groups. Try again.',
    'No selectable key groups are available for this account.':
      'No selectable key groups are available for this account.',
    'The selected key group is no longer available. Choose a current group and prepare again.':
      'The selected key group is no longer available. Choose a current group and prepare again.',
    'The server returned an invalid key preparation. Refresh the page and try again.':
      'The server returned an invalid key preparation. Refresh the page and try again.',
    'Unable to prepare API key': 'Unable to prepare API key',
    'Preparing key creation...': 'Preparing key creation...',
    'Only required when two-factor authentication is enabled.':
      'Only required when two-factor authentication is enabled.',
  },
  zh: {
    'Unable to load selectable key groups. Try again.':
      '无法加载可选密钥分组，请重试。',
    'No selectable key groups are available for this account.':
      '当前账户没有可选的密钥分组。',
    'The selected key group is no longer available. Choose a current group and prepare again.':
      '所选密钥分组已不可用。请选择当前可用的分组并重新准备。',
    'The server returned an invalid key preparation. Refresh the page and try again.':
      '服务端返回了无效的密钥准备结果。请刷新页面后重试。',
    'Unable to prepare API key': '无法准备 API 密钥',
    'Preparing key creation...': '正在准备创建密钥……',
    'Only required when two-factor authentication is enabled.':
      '仅在已启用双重身份验证时需要。',
  },
  'zh-TW': {
    'Unable to load selectable key groups. Try again.':
      '無法載入可選的金鑰分組，請重試。',
    'No selectable key groups are available for this account.':
      '此帳戶目前沒有可選的金鑰分組。',
    'The selected key group is no longer available. Choose a current group and prepare again.':
      '所選的金鑰分組已無法使用。請選擇目前可用的分組並重新準備。',
    'The server returned an invalid key preparation. Refresh the page and try again.':
      '伺服器傳回無效的金鑰準備結果。請重新整理頁面後再試一次。',
    'Unable to prepare API key': '無法準備 API 金鑰',
    'Preparing key creation...': '正在準備建立金鑰……',
    'Only required when two-factor authentication is enabled.':
      '僅在已啟用雙重驗證時需要。',
  },
  fr: {
    'Unable to load selectable key groups. Try again.':
      'Impossible de charger les groupes de clés sélectionnables. Réessayez.',
    'No selectable key groups are available for this account.':
      'Aucun groupe de clés sélectionnable n’est disponible pour ce compte.',
    'The selected key group is no longer available. Choose a current group and prepare again.':
      'Le groupe de clés sélectionné n’est plus disponible. Choisissez un groupe actuel et recommencez la préparation.',
    'The server returned an invalid key preparation. Refresh the page and try again.':
      'Le serveur a renvoyé une préparation de clé non valide. Actualisez la page et réessayez.',
    'Unable to prepare API key': 'Impossible de préparer la clé API',
    'Preparing key creation...': 'Préparation de la création de la clé…',
    'Only required when two-factor authentication is enabled.':
      'Requis uniquement lorsque l’authentification à deux facteurs est activée.',
  },
  ja: {
    'Unable to load selectable key groups. Try again.':
      '選択可能なキーグループを読み込めませんでした。もう一度お試しください。',
    'No selectable key groups are available for this account.':
      'このアカウントで選択できるキーグループはありません。',
    'The selected key group is no longer available. Choose a current group and prepare again.':
      '選択したキーグループは利用できなくなりました。現在のグループを選び直して、もう一度準備してください。',
    'The server returned an invalid key preparation. Refresh the page and try again.':
      'サーバーから無効なキー準備情報が返されました。ページを再読み込みして、もう一度お試しください。',
    'Unable to prepare API key': 'API キーを準備できません',
    'Preparing key creation...': 'キー作成を準備しています…',
    'Only required when two-factor authentication is enabled.':
      '2 要素認証が有効な場合のみ必要です。',
  },
  ru: {
    'Unable to load selectable key groups. Try again.':
      'Не удалось загрузить доступные для выбора группы ключей. Повторите попытку.',
    'No selectable key groups are available for this account.':
      'Для этой учётной записи нет доступных для выбора групп ключей.',
    'The selected key group is no longer available. Choose a current group and prepare again.':
      'Выбранная группа ключей больше недоступна. Выберите актуальную группу и повторите подготовку.',
    'The server returned an invalid key preparation. Refresh the page and try again.':
      'Сервер вернул недопустимые данные подготовки ключа. Обновите страницу и повторите попытку.',
    'Unable to prepare API key': 'Не удалось подготовить API-ключ',
    'Preparing key creation...': 'Подготовка создания ключа…',
    'Only required when two-factor authentication is enabled.':
      'Требуется только при включённой двухфакторной аутентификации.',
  },
  vi: {
    'Unable to load selectable key groups. Try again.':
      'Không thể tải các nhóm khóa có thể chọn. Hãy thử lại.',
    'No selectable key groups are available for this account.':
      'Tài khoản này không có nhóm khóa nào có thể chọn.',
    'The selected key group is no longer available. Choose a current group and prepare again.':
      'Nhóm khóa đã chọn không còn khả dụng. Hãy chọn một nhóm hiện có và chuẩn bị lại.',
    'The server returned an invalid key preparation. Refresh the page and try again.':
      'Máy chủ trả về dữ liệu chuẩn bị khóa không hợp lệ. Hãy tải lại trang và thử lại.',
    'Unable to prepare API key': 'Không thể chuẩn bị khóa API',
    'Preparing key creation...': 'Đang chuẩn bị tạo khóa…',
    'Only required when two-factor authentication is enabled.':
      'Chỉ bắt buộc khi xác thực hai yếu tố được bật.',
  },
}

for (const [locale, translations] of Object.entries(
  assistantKeyCreationTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const setupRootVerificationTranslations = {
  en: {
    'Verify the existing administrator account to finish setup.':
      'Verify the existing administrator account to finish setup.',
    'Enter the existing administrator password':
      'Enter the existing administrator password',
    'Please enter the existing administrator password':
      'Please enter the existing administrator password',
  },
  zh: {
    'Verify the existing administrator account to finish setup.':
      '请验证现有管理员账号以完成初始化。',
    'Enter the existing administrator password': '输入现有管理员密码',
    'Please enter the existing administrator password': '请输入现有管理员密码',
  },
  'zh-TW': {
    'Verify the existing administrator account to finish setup.':
      '請驗證現有管理員帳號以完成初始化。',
    'Enter the existing administrator password': '輸入現有管理員密碼',
    'Please enter the existing administrator password': '請輸入現有管理員密碼',
  },
  fr: {
    'Verify the existing administrator account to finish setup.':
      'Vérifiez le compte administrateur existant pour terminer l’installation.',
    'Enter the existing administrator password':
      'Saisissez le mot de passe administrateur existant',
    'Please enter the existing administrator password':
      'Veuillez saisir le mot de passe administrateur existant',
  },
  ja: {
    'Verify the existing administrator account to finish setup.':
      'セットアップを完了するには、既存の管理者アカウントを確認してください。',
    'Enter the existing administrator password': '既存の管理者パスワードを入力',
    'Please enter the existing administrator password':
      '既存の管理者パスワードを入力してください',
  },
  ru: {
    'Verify the existing administrator account to finish setup.':
      'Подтвердите существующую учётную запись администратора, чтобы завершить установку.',
    'Enter the existing administrator password':
      'Введите существующий пароль администратора',
    'Please enter the existing administrator password':
      'Введите существующий пароль администратора',
  },
  vi: {
    'Verify the existing administrator account to finish setup.':
      'Xác minh tài khoản quản trị hiện có để hoàn tất cài đặt.',
    'Enter the existing administrator password':
      'Nhập mật khẩu quản trị hiện có',
    'Please enter the existing administrator password':
      'Vui lòng nhập mật khẩu quản trị hiện có',
  },
}

for (const [locale, translations] of Object.entries(
  setupRootVerificationTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const affiliateInvitationTranslations = {
  en: {
    'Invite friends': 'Invite friends',
    'Invite friends and earn account credit when they join.':
      'Invite friends and earn account credit when they join.',
    'Invite friends to {{systemName}}': 'Invite friends to {{systemName}}',
    "Enter a friend's email and we'll send your personal invitation link through the configured mail server.":
      "Enter a friend's email and we'll send your personal invitation link through the configured mail server.",
    "Friend's email": "Friend's email",
    'Invitation sent to {{email}}': 'Invitation sent to {{email}}',
    'Invitation not sent': 'Invitation not sent',
    'The invitation email could not be sent. Try again later or copy your invitation link.':
      'The invitation email could not be sent. Try again later or copy your invitation link.',
    'Copy invitation link': 'Copy invitation link',
    'Sending invitation...': 'Sending invitation...',
    'Send invitation': 'Send invitation',
  },
  zh: {
    'Invite friends': '邀请好友',
    'Invite friends and earn account credit when they join.':
      '邀请好友注册，成功后可获得账户额度奖励。',
    'Invite friends to {{systemName}}': '邀请好友加入 {{systemName}}',
    "Enter a friend's email and we'll send your personal invitation link through the configured mail server.":
      '输入好友邮箱，我们将通过已配置的邮件服务器发送你的专属邀请链接。',
    "Friend's email": '好友邮箱',
    'Invitation sent to {{email}}': '邀请邮件已发送至 {{email}}',
    'Invitation not sent': '邀请未发送',
    'The invitation email could not be sent. Try again later or copy your invitation link.':
      '邀请邮件发送失败。请稍后重试，或复制邀请链接。',
    'Copy invitation link': '复制邀请链接',
    'Sending invitation...': '正在发送邀请…',
    'Send invitation': '发送邀请',
  },
  'zh-TW': {
    'Invite friends': '邀請好友',
    'Invite friends and earn account credit when they join.':
      '邀請好友註冊，成功後可獲得帳戶額度獎勵。',
    'Invite friends to {{systemName}}': '邀請好友加入 {{systemName}}',
    "Enter a friend's email and we'll send your personal invitation link through the configured mail server.":
      '輸入好友的電子郵件，我們會透過已設定的郵件伺服器傳送你的專屬邀請連結。',
    "Friend's email": '好友的電子郵件',
    'Invitation sent to {{email}}': '邀請郵件已傳送至 {{email}}',
    'Invitation not sent': '邀請未傳送',
    'The invitation email could not be sent. Try again later or copy your invitation link.':
      '邀請郵件無法傳送。請稍後再試，或複製邀請連結。',
    'Copy invitation link': '複製邀請連結',
    'Sending invitation...': '正在傳送邀請…',
    'Send invitation': '傳送邀請',
  },
  fr: {
    'Invite friends': 'Inviter des amis',
    'Invite friends and earn account credit when they join.':
      'Invitez des amis et recevez du crédit sur votre compte lorsqu’ils nous rejoignent.',
    'Invite friends to {{systemName}}':
      'Inviter des amis à rejoindre {{systemName}}',
    "Enter a friend's email and we'll send your personal invitation link through the configured mail server.":
      'Saisissez l’adresse e-mail d’un ami et nous lui enverrons votre lien d’invitation personnel via le serveur de messagerie configuré.',
    "Friend's email": 'Adresse e-mail de votre ami',
    'Invitation sent to {{email}}': 'Invitation envoyée à {{email}}',
    'Invitation not sent': 'Invitation non envoyée',
    'The invitation email could not be sent. Try again later or copy your invitation link.':
      'L’e-mail d’invitation n’a pas pu être envoyé. Réessayez plus tard ou copiez votre lien d’invitation.',
    'Copy invitation link': 'Copier le lien d’invitation',
    'Sending invitation...': 'Envoi de l’invitation…',
    'Send invitation': 'Envoyer l’invitation',
  },
  ja: {
    'Invite friends': '友だちを招待',
    'Invite friends and earn account credit when they join.':
      '友だちを招待すると、参加後にアカウントクレジットを獲得できます。',
    'Invite friends to {{systemName}}': '{{systemName}} に友だちを招待',
    "Enter a friend's email and we'll send your personal invitation link through the configured mail server.":
      '友だちのメールアドレスを入力すると、設定済みのメールサーバーからあなた専用の招待リンクを送信します。',
    "Friend's email": '友だちのメールアドレス',
    'Invitation sent to {{email}}': '{{email}} に招待メールを送信しました',
    'Invitation not sent': '招待を送信できませんでした',
    'The invitation email could not be sent. Try again later or copy your invitation link.':
      '招待メールを送信できませんでした。後でもう一度試すか、招待リンクをコピーしてください。',
    'Copy invitation link': '招待リンクをコピー',
    'Sending invitation...': '招待を送信中…',
    'Send invitation': '招待を送信',
  },
  ru: {
    'Invite friends': 'Пригласить друзей',
    'Invite friends and earn account credit when they join.':
      'Приглашайте друзей и получайте средства на баланс после их регистрации.',
    'Invite friends to {{systemName}}': 'Пригласить друзей в {{systemName}}',
    "Enter a friend's email and we'll send your personal invitation link through the configured mail server.":
      'Введите адрес электронной почты друга, и мы отправим вашу личную ссылку через настроенный почтовый сервер.',
    "Friend's email": 'Электронная почта друга',
    'Invitation sent to {{email}}': 'Приглашение отправлено на {{email}}',
    'Invitation not sent': 'Приглашение не отправлено',
    'The invitation email could not be sent. Try again later or copy your invitation link.':
      'Не удалось отправить письмо с приглашением. Повторите попытку позже или скопируйте ссылку.',
    'Copy invitation link': 'Скопировать ссылку-приглашение',
    'Sending invitation...': 'Отправка приглашения…',
    'Send invitation': 'Отправить приглашение',
  },
  vi: {
    'Invite friends': 'Mời bạn bè',
    'Invite friends and earn account credit when they join.':
      'Mời bạn bè và nhận tín dụng tài khoản khi họ tham gia.',
    'Invite friends to {{systemName}}': 'Mời bạn bè tham gia {{systemName}}',
    "Enter a friend's email and we'll send your personal invitation link through the configured mail server.":
      'Nhập email của bạn bè, chúng tôi sẽ gửi liên kết mời riêng của bạn qua máy chủ thư đã cấu hình.',
    "Friend's email": 'Email của bạn bè',
    'Invitation sent to {{email}}': 'Đã gửi lời mời tới {{email}}',
    'Invitation not sent': 'Chưa gửi được lời mời',
    'The invitation email could not be sent. Try again later or copy your invitation link.':
      'Không thể gửi email mời. Hãy thử lại sau hoặc sao chép liên kết mời của bạn.',
    'Copy invitation link': 'Sao chép liên kết mời',
    'Sending invitation...': 'Đang gửi lời mời…',
    'Send invitation': 'Gửi lời mời',
  },
}

for (const [locale, translations] of Object.entries(
  affiliateInvitationTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const profileActivityTranslations = {
  en: {
    Cumulative: 'Cumulative',
    'Daily activity': 'Daily',
    'Current streak': 'Current streak',
    'Longest streak this year': 'Longest streak this year',
    'No token activity in the past year': 'No token activity in the past year',
    'Peak daily tokens': 'Peak daily tokens',
    'Token activity': 'Token activity',
    'Token activity for the past year, with {{count}} active days':
      'Token activity for the past year, with {{count}} active days',
    'Tokens in the past year': 'Tokens in the past year',
    'Try loading the activity again.': 'Try loading the activity again.',
    'Unable to load token activity': 'Unable to load token activity',
  },
  zh: {
    Cumulative: '累计',
    'Daily activity': '每日',
    'Current streak': '当前连续天数',
    'Longest streak this year': '近一年最长连续天数',
    'No token activity in the past year': '过去一年暂无 Token 活动',
    'Peak daily tokens': '单日峰值 Token 数',
    'Token activity': 'Token 活动',
    'Token activity for the past year, with {{count}} active days':
      '过去一年的 Token 活动，共活跃 {{count}} 天',
    'Tokens in the past year': '近一年 Token 数',
    'Try loading the activity again.': '请重试加载活动数据。',
    'Unable to load token activity': '无法加载 Token 活动',
  },
  'zh-TW': {
    Cumulative: '累計',
    'Daily activity': '每日',
    'Current streak': '目前連續天數',
    'Longest streak this year': '近一年最長連續天數',
    'No token activity in the past year': '過去一年暫無 Token 活動',
    'Peak daily tokens': '單日峰值 Token 數',
    'Token activity': 'Token 活動',
    'Token activity for the past year, with {{count}} active days':
      '過去一年的 Token 活動，共活躍 {{count}} 天',
    'Tokens in the past year': '近一年 Token 數',
    'Try loading the activity again.': '請重試載入活動資料。',
    'Unable to load token activity': '無法載入 Token 活動',
  },
  fr: {
    Cumulative: 'Cumul',
    'Daily activity': 'Quotidien',
    'Current streak': 'Série actuelle',
    'Longest streak this year': 'Plus longue série de l’année',
    'No token activity in the past year':
      'Aucune activité de jetons au cours de l’année écoulée',
    'Peak daily tokens': 'Pic quotidien de jetons',
    'Token activity': 'Activité des jetons',
    'Token activity for the past year, with {{count}} active days':
      'Activité des jetons sur l’année écoulée, avec {{count}} jours actifs',
    'Tokens in the past year': 'Jetons sur l’année écoulée',
    'Try loading the activity again.':
      'Réessayez de charger les données d’activité.',
    'Unable to load token activity':
      'Impossible de charger l’activité des jetons',
  },
  ja: {
    Cumulative: '累計',
    'Daily activity': '日別',
    'Current streak': '現在の連続日数',
    'Longest streak this year': '過去1年の最長連続日数',
    'No token activity in the past year': '過去1年間のトークン利用はありません',
    'Peak daily tokens': '1日の最大トークン数',
    'Token activity': 'トークンアクティビティ',
    'Token activity for the past year, with {{count}} active days':
      '過去1年間のトークンアクティビティ（アクティブ {{count}} 日）',
    'Tokens in the past year': '過去1年のトークン数',
    'Try loading the activity again.':
      'アクティビティを再読み込みしてください。',
    'Unable to load token activity': 'トークンアクティビティを読み込めません',
  },
  ru: {
    Cumulative: 'Накопительно',
    'Daily activity': 'По дням',
    'Current streak': 'Текущая серия',
    'Longest streak this year': 'Самая длинная серия за год',
    'No token activity in the past year':
      'За прошедший год активности токенов нет',
    'Peak daily tokens': 'Пиковое число токенов за день',
    'Token activity': 'Активность токенов',
    'Token activity for the past year, with {{count}} active days':
      'Активность токенов за прошедший год: активных дней — {{count}}',
    'Tokens in the past year': 'Токены за прошедший год',
    'Try loading the activity again.':
      'Попробуйте загрузить активность ещё раз.',
    'Unable to load token activity': 'Не удалось загрузить активность токенов',
  },
  vi: {
    Cumulative: 'Tích lũy',
    'Daily activity': 'Hằng ngày',
    'Current streak': 'Chuỗi hiện tại',
    'Longest streak this year': 'Chuỗi dài nhất trong năm qua',
    'No token activity in the past year':
      'Không có hoạt động token trong năm qua',
    'Peak daily tokens': 'Lượng token cao nhất trong ngày',
    'Token activity': 'Hoạt động token',
    'Token activity for the past year, with {{count}} active days':
      'Hoạt động token trong năm qua, với {{count}} ngày hoạt động',
    'Tokens in the past year': 'Token trong năm qua',
    'Try loading the activity again.': 'Hãy thử tải lại dữ liệu hoạt động.',
    'Unable to load token activity': 'Không thể tải hoạt động token',
  },
}
for (const [locale, translations] of Object.entries(
  profileActivityTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const currencyTerminologyTranslations = {
  en: {
    '(Platform amount, unit: USD)': '($ (Platform))',
    'Credited amount (unit: USD)': 'Platform credit ($ (Platform))',
    'Custom credited amount': 'Custom platform credit ($ (Platform))',
    'Custom credited amount in US dollars': 'Custom platform credit',
    'Gateway price per 1 USD (optional)':
      'Gateway price per 1 platform dollar (optional)',
    'Gateway price per 1 platform dollar (optional)':
      'Gateway price per 1 platform dollar (optional)',
    'The server uses this rate to quote and verify payment. It is the settlement-currency amount for one $ (Platform).':
      'The server uses this rate to quote and verify payment. It is the settlement-currency amount for one $ (Platform).',
    'Enter the settlement-currency amount charged for one $ (Platform). The displayed payment is the platform amount multiplied by this rate.':
      'Enter the settlement-currency amount charged for one $ (Platform). The displayed payment is the platform amount multiplied by this rate.',
    'Settlement preview: 1 platform USD = {{price}} {{unit}}':
      'Settlement preview: 1 $ (Platform) = {{price}} {{unit}}',
    'Settlement preview: 1 $ (Platform) = {{price}} {{unit}}':
      'Settlement preview: 1 $ (Platform) = {{price}} {{unit}}',
    'Top up {{amount}}; pay {{payment}}': 'Credit {{amount}}; pay {{payment}}',
    'Maximum top-up amount: {{amount}} USD credited':
      'Maximum top-up amount: {{amount}}',
    'Maximum: {{amount}} USD credited': 'Maximum: {{amount}}',
  },
  zh: {
    '(Platform amount, unit: USD)': '（$（平台））',
    'Credited amount (unit: USD)': '平台金额（$（平台））',
    'Custom credited amount': '自定义平台金额（$（平台））',
    'Custom credited amount in US dollars': '自定义平台金额',
    'Gateway price per 1 USD (optional)': '每 1 个平台美元的网关单价（可选）',
    'Gateway price per 1 platform dollar (optional)':
      '每 1 个平台美元的网关单价（可选）',
    'The server uses this rate to quote and verify payment. It is the settlement-currency amount for one $ (Platform).':
      '服务器使用此汇率计算并校验支付金额。它表示 1 个 $（平台）对应的结算货币金额。',
    'Enter the settlement-currency amount charged for one $ (Platform). The displayed payment is the platform amount multiplied by this rate.':
      '请输入 1 个 $（平台）所收取的结算货币金额。页面显示的实际支付金额等于平台金额乘以此单价。',
    'Settlement preview: 1 platform USD = {{price}} {{unit}}':
      '结算预览：1 个 $（平台）= {{price}} {{unit}}',
    'Settlement preview: 1 $ (Platform) = {{price}} {{unit}}':
      '结算预览：1 个 $（平台）= {{price}} {{unit}}',
    'Top up {{amount}}; pay {{payment}}':
      '到账 {{amount}}；实际支付 {{payment}}',
    'Maximum top-up amount: {{amount}} USD credited': '充值上限：{{amount}}',
    'Maximum: {{amount}} USD credited': '上限：{{amount}}',
  },
  'zh-TW': {
    '(Platform amount, unit: USD)': '（$（平台））',
    'Credited amount (unit: USD)': '平台金額（$（平台））',
    'Custom credited amount': '自訂平台金額（$（平台））',
    'Custom credited amount in US dollars': '自訂平台金額',
    'Gateway price per 1 USD (optional)': '每 1 個平台美元的閘道單價（可選）',
    'Gateway price per 1 platform dollar (optional)':
      '每 1 個平台美元的閘道單價（可選）',
    'The server uses this rate to quote and verify payment. It is the settlement-currency amount for one $ (Platform).':
      '伺服器使用此匯率計算並驗證付款金額。這是 1 個 $（平台）對應的結算貨幣金額。',
    'Enter the settlement-currency amount charged for one $ (Platform). The displayed payment is the platform amount multiplied by this rate.':
      '請輸入 1 個 $（平台）所收取的結算貨幣金額。頁面顯示的實際支付金額等於平台金額乘以此單價。',
    'Settlement preview: 1 platform USD = {{price}} {{unit}}':
      '結算預覽：1 個 $（平台）= {{price}} {{unit}}',
    'Settlement preview: 1 $ (Platform) = {{price}} {{unit}}':
      '結算預覽：1 個 $（平台）= {{price}} {{unit}}',
    'Top up {{amount}}; pay {{payment}}':
      '入帳 {{amount}}；實際支付 {{payment}}',
    'Maximum top-up amount: {{amount}} USD credited': '儲值上限：{{amount}}',
    'Maximum: {{amount}} USD credited': '上限：{{amount}}',
  },
  fr: {
    '(Platform amount, unit: USD)': '($ (Plateforme))',
    'Credited amount (unit: USD)': 'Crédit de plateforme ($ (Plateforme))',
    'Custom credited amount':
      'Crédit de plateforme personnalisé ($ (Plateforme))',
    'Custom credited amount in US dollars': 'Crédit de plateforme personnalisé',
    'Gateway price per 1 USD (optional)':
      'Prix du canal par dollar de plateforme (facultatif)',
    'Gateway price per 1 platform dollar (optional)':
      'Prix du canal par dollar de plateforme (facultatif)',
    'The server uses this rate to quote and verify payment. It is the settlement-currency amount for one $ (Platform).':
      'Le serveur utilise ce taux pour calculer et vérifier le paiement. Il s’agit du montant dans la devise de règlement pour 1 $ (Plateforme).',
    'Enter the settlement-currency amount charged for one $ (Platform). The displayed payment is the platform amount multiplied by this rate.':
      'Saisissez le montant dans la devise de règlement pour 1 $ (Plateforme). Le paiement affiché correspond au montant de la plateforme multiplié par ce taux.',
    'Settlement preview: 1 platform USD = {{price}} {{unit}}':
      'Aperçu du règlement : 1 $ (Plateforme) = {{price}} {{unit}}',
    'Settlement preview: 1 $ (Platform) = {{price}} {{unit}}':
      'Aperçu du règlement : 1 $ (Plateforme) = {{price}} {{unit}}',
    'Top up {{amount}}; pay {{payment}}':
      'Crédit {{amount}} ; paiement {{payment}}',
    'Maximum top-up amount: {{amount}} USD credited':
      'Montant maximal rechargé : {{amount}}',
    'Maximum: {{amount}} USD credited': 'Maximum : {{amount}}',
  },
  ja: {
    '(Platform amount, unit: USD)': '（$（プラットフォーム））',
    'Credited amount (unit: USD)':
      'プラットフォーム残高（$（プラットフォーム））',
    'Custom credited amount':
      'カスタムのプラットフォーム残高（$（プラットフォーム））',
    'Custom credited amount in US dollars': 'カスタムのプラットフォーム残高',
    'Gateway price per 1 USD (optional)':
      'プラットフォーム 1 ドルあたりの決済チャネル単価（任意）',
    'Gateway price per 1 platform dollar (optional)':
      'プラットフォーム 1 ドルあたりの決済チャネル単価（任意）',
    'The server uses this rate to quote and verify payment. It is the settlement-currency amount for one $ (Platform).':
      'サーバーはこのレートで支払額を計算・検証します。$（プラットフォーム）1 単位に対する決済通貨の金額です。',
    'Enter the settlement-currency amount charged for one $ (Platform). The displayed payment is the platform amount multiplied by this rate.':
      '$（プラットフォーム）1 単位に対する決済通貨の金額を入力してください。表示される支払額はプラットフォーム金額にこのレートを掛けた値です。',
    'Settlement preview: 1 platform USD = {{price}} {{unit}}':
      '決済プレビュー：$（プラットフォーム）1 単位 = {{price}} {{unit}}',
    'Settlement preview: 1 $ (Platform) = {{price}} {{unit}}':
      '決済プレビュー：$（プラットフォーム）1 単位 = {{price}} {{unit}}',
    'Top up {{amount}}; pay {{payment}}':
      '付与額 {{amount}}；支払額 {{payment}}',
    'Maximum top-up amount: {{amount}} USD credited':
      'チャージ上限：{{amount}}',
    'Maximum: {{amount}} USD credited': '上限：{{amount}}',
  },
  ru: {
    '(Platform amount, unit: USD)': '($ (Платформа))',
    'Credited amount (unit: USD)': 'Платформенный кредит ($ (Платформа))',
    'Custom credited amount':
      'Пользовательский платформенный кредит ($ (Платформа))',
    'Custom credited amount in US dollars':
      'Пользовательский платформенный кредит',
    'Gateway price per 1 USD (optional)':
      'Цена шлюза за 1 платформенный доллар (необязательно)',
    'Gateway price per 1 platform dollar (optional)':
      'Цена шлюза за 1 платформенный доллар (необязательно)',
    'The server uses this rate to quote and verify payment. It is the settlement-currency amount for one $ (Platform).':
      'Сервер использует этот курс для расчёта и проверки платежа. Это сумма в валюте расчёта за 1 $ (Платформа).',
    'Enter the settlement-currency amount charged for one $ (Platform). The displayed payment is the platform amount multiplied by this rate.':
      'Укажите сумму в валюте расчёта за 1 $ (Платформа). Отображаемый платёж равен платформенной сумме, умноженной на этот курс.',
    'Settlement preview: 1 platform USD = {{price}} {{unit}}':
      'Предпросмотр расчёта: 1 $ (Платформа) = {{price}} {{unit}}',
    'Settlement preview: 1 $ (Platform) = {{price}} {{unit}}':
      'Предпросмотр расчёта: 1 $ (Платформа) = {{price}} {{unit}}',
    'Top up {{amount}}; pay {{payment}}':
      'Зачисление {{amount}}; оплата {{payment}}',
    'Maximum top-up amount: {{amount}} USD credited':
      'Максимальная сумма пополнения: {{amount}}',
    'Maximum: {{amount}} USD credited': 'Максимум: {{amount}}',
  },
  vi: {
    '(Platform amount, unit: USD)': '($ (Nền tảng))',
    'Credited amount (unit: USD)': 'Tín dụng nền tảng ($ (Nền tảng))',
    'Custom credited amount': 'Tín dụng nền tảng tùy chỉnh ($ (Nền tảng))',
    'Custom credited amount in US dollars': 'Tín dụng nền tảng tùy chỉnh',
    'Gateway price per 1 USD (optional)':
      'Đơn giá cổng thanh toán cho 1 đô la nền tảng (tùy chọn)',
    'Gateway price per 1 platform dollar (optional)':
      'Đơn giá cổng thanh toán cho 1 đô la nền tảng (tùy chọn)',
    'The server uses this rate to quote and verify payment. It is the settlement-currency amount for one $ (Platform).':
      'Máy chủ dùng tỷ giá này để báo giá và xác minh thanh toán. Đây là số tiền theo đơn vị quyết toán cho 1 $ (Nền tảng).',
    'Enter the settlement-currency amount charged for one $ (Platform). The displayed payment is the platform amount multiplied by this rate.':
      'Nhập số tiền quyết toán cho 1 $ (Nền tảng). Khoản thanh toán hiển thị bằng số tiền nền tảng nhân với tỷ giá này.',
    'Settlement preview: 1 platform USD = {{price}} {{unit}}':
      'Xem trước quyết toán: 1 $ (Nền tảng) = {{price}} {{unit}}',
    'Settlement preview: 1 $ (Platform) = {{price}} {{unit}}':
      'Xem trước quyết toán: 1 $ (Nền tảng) = {{price}} {{unit}}',
    'Top up {{amount}}; pay {{payment}}':
      'Được cộng {{amount}}; thanh toán {{payment}}',
    'Maximum top-up amount: {{amount}} USD credited':
      'Số tiền nạp tối đa: {{amount}}',
    'Maximum: {{amount}} USD credited': 'Tối đa: {{amount}}',
  },
}
for (const [locale, translations] of Object.entries(
  currencyTerminologyTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const exchangeRateTranslations = {
  en: {
    Sync: 'Sync',
    'Sync USD exchange rate': 'Sync USD exchange rate',
    'Set a supported local currency before syncing the rate':
      'Set a supported local currency before syncing the rate',
    'Failed to sync exchange rate': 'Failed to sync exchange rate',
    'The exchange-rate provider returned an invalid rate':
      'The exchange-rate provider returned an invalid rate',
    'Exchange rate synced: 1 USD = {{rate}} {{currency}}':
      'Exchange rate synced: 1 USD = {{rate}} {{currency}}',
    'Multiple fiat currencies': 'Multiple fiat currencies',
    'Custom Currency Code': 'Custom Currency Code',
    'ISO 4217 code used for live exchange-rate sync':
      'ISO 4217 code used for live exchange-rate sync',
    'Custom currency ISO code is required':
      'Custom currency ISO code is required',
    'Payment rate must be finite': 'Payment rate must be finite',
  },
  zh: {
    Sync: '同步',
    'Sync USD exchange rate': '同步美元汇率',
    'Set a supported local currency before syncing the rate':
      '请先设置受支持的本地货币，再同步汇率',
    'Failed to sync exchange rate': '同步汇率失败',
    'The exchange-rate provider returned an invalid rate':
      '汇率服务返回了无效汇率',
    'Exchange rate synced: 1 USD = {{rate}} {{currency}}':
      '汇率已同步：1 USD = {{rate}} {{currency}}',
    'Multiple fiat currencies': '多种法币',
    'Custom Currency Code': '自定义货币代码',
    'ISO 4217 code used for live exchange-rate sync':
      '用于实时同步汇率的 ISO 4217 代码',
    'Custom currency ISO code is required': '必须填写自定义货币 ISO 代码',
    'Payment rate must be finite': '支付汇率和充值比例必须是有限数值',
  },
  'zh-TW': {
    Sync: '同步',
    'Sync USD exchange rate': '同步美元匯率',
    'Set a supported local currency before syncing the rate':
      '請先設定支援的本地貨幣，再同步匯率',
    'Failed to sync exchange rate': '同步匯率失敗',
    'The exchange-rate provider returned an invalid rate':
      '匯率服務回傳了無效匯率',
    'Exchange rate synced: 1 USD = {{rate}} {{currency}}':
      '匯率已同步：1 USD = {{rate}} {{currency}}',
    'Multiple fiat currencies': '多種法幣',
    'Custom Currency Code': '自訂貨幣代碼',
    'ISO 4217 code used for live exchange-rate sync':
      '用於即時同步匯率的 ISO 4217 代碼',
    'Custom currency ISO code is required': '必須填寫自訂貨幣 ISO 代碼',
    'Payment rate must be finite': '支付匯率與儲值比例必須是有限數值',
  },
  fr: {
    Sync: 'Synchroniser',
    'Sync USD exchange rate': 'Synchroniser le taux USD',
    'Set a supported local currency before syncing the rate':
      'Définissez une devise locale prise en charge avant la synchronisation',
    'Failed to sync exchange rate': 'Échec de la synchronisation du taux',
    'The exchange-rate provider returned an invalid rate':
      'Le fournisseur de taux a renvoyé un taux invalide',
    'Exchange rate synced: 1 USD = {{rate}} {{currency}}':
      'Taux synchronisé : 1 USD = {{rate}} {{currency}}',
    'Multiple fiat currencies': 'Plusieurs devises fiduciaires',
    'Custom Currency Code': 'Code de devise personnalisé',
    'ISO 4217 code used for live exchange-rate sync':
      'Code ISO 4217 utilisé pour synchroniser le taux en direct',
    'Custom currency ISO code is required':
      'Le code ISO de la devise personnalisée est requis',
    'Payment rate must be finite':
      'Le taux de paiement doit être un nombre fini',
  },
  ja: {
    Sync: '同期',
    'Sync USD exchange rate': '米ドル為替レートを同期',
    'Set a supported local currency before syncing the rate':
      '同期する前に対応する現地通貨を設定してください',
    'Failed to sync exchange rate': '為替レートの同期に失敗しました',
    'The exchange-rate provider returned an invalid rate':
      '為替レートサービスが無効なレートを返しました',
    'Exchange rate synced: 1 USD = {{rate}} {{currency}}':
      'レートを同期しました：1 USD = {{rate}} {{currency}}',
    'Multiple fiat currencies': '複数の法定通貨',
    'Custom Currency Code': 'カスタム通貨コード',
    'ISO 4217 code used for live exchange-rate sync':
      '最新の為替レート同期に使用する ISO 4217 コード',
    'Custom currency ISO code is required':
      'カスタム通貨の ISO コードを入力してください',
    'Payment rate must be finite':
      '支払いレートは有限の数値である必要があります',
  },
  ru: {
    Sync: 'Синхронизировать',
    'Sync USD exchange rate': 'Синхронизировать курс USD',
    'Set a supported local currency before syncing the rate':
      'Перед синхронизацией укажите поддерживаемую местную валюту',
    'Failed to sync exchange rate': 'Не удалось синхронизировать курс',
    'The exchange-rate provider returned an invalid rate':
      'Поставщик курсов вернул недействительный курс',
    'Exchange rate synced: 1 USD = {{rate}} {{currency}}':
      'Курс синхронизирован: 1 USD = {{rate}} {{currency}}',
    'Multiple fiat currencies': 'Несколько фиатных валют',
    'Custom Currency Code': 'Код пользовательской валюты',
    'ISO 4217 code used for live exchange-rate sync':
      'Код ISO 4217 для синхронизации актуального курса',
    'Custom currency ISO code is required':
      'Требуется код ISO пользовательской валюты',
    'Payment rate must be finite':
      'Платёжный коэффициент должен быть конечным числом',
  },
  vi: {
    Sync: 'Đồng bộ',
    'Sync USD exchange rate': 'Đồng bộ tỷ giá USD',
    'Set a supported local currency before syncing the rate':
      'Hãy đặt loại tiền địa phương được hỗ trợ trước khi đồng bộ tỷ giá',
    'Failed to sync exchange rate': 'Không thể đồng bộ tỷ giá',
    'The exchange-rate provider returned an invalid rate':
      'Nhà cung cấp tỷ giá trả về tỷ giá không hợp lệ',
    'Exchange rate synced: 1 USD = {{rate}} {{currency}}':
      'Đã đồng bộ tỷ giá: 1 USD = {{rate}} {{currency}}',
    'Multiple fiat currencies': 'Nhiều loại tiền pháp định',
    'Custom Currency Code': 'Mã tiền tệ tùy chỉnh',
    'ISO 4217 code used for live exchange-rate sync':
      'Mã ISO 4217 dùng để đồng bộ tỷ giá trực tiếp',
    'Custom currency ISO code is required':
      'Cần nhập mã ISO của tiền tệ tùy chỉnh',
    'Payment rate must be finite': 'Tỷ lệ thanh toán phải là một số hữu hạn',
  },
}
for (const [locale, translations] of Object.entries(exchangeRateTranslations)) {
  Object.assign(newKeys[locale], translations)
}

const platformAmountTranslations = {
  en: {
    'Recharge Amount': 'Platform credit ($ (Platform))',
    'Recharge Amount (USD)': 'Platform credit ($ (Platform))',
    'Maximum credited amount per payment (USD, optional)':
      'Maximum platform credit per payment ($ (Platform), optional)',
    'Minimum top-up (USD)': 'Minimum platform credit ($ (Platform))',
    'Minimum recharge amount in USD': 'Minimum platform credit ($ (Platform))',
    'Smallest USD amount users can recharge (Epay)':
      'Smallest platform credit users can receive ($ (Platform), Epay)',
    'Expected monthly API credit (USD)':
      'Expected monthly platform credit ($ (Platform))',
    'Top-up credit to compare (USD)':
      'Platform credit to compare ($ (Platform))',
    'Credited API balance': 'Credited platform balance ($ (Platform))',
    'Expected monthly platform credit':
      'Expected monthly platform credit ($ (Platform))',
    'Platform credit to compare': 'Platform credit to compare ($ (Platform))',
    'Credited platform balance': 'Credited platform balance ($ (Platform))',
  },
  zh: {
    'Recharge Amount': '平台金额（$（平台））',
    'Recharge Amount (USD)': '平台金额（$（平台））',
    'Maximum credited amount per payment (USD, optional)':
      '单笔最高平台金额（$（平台），可选）',
    'Minimum top-up (USD)': '最低平台金额（$（平台））',
    'Minimum recharge amount in USD': '最低平台金额（$（平台））',
    'Smallest USD amount users can recharge (Epay)':
      '用户可获得的最低平台金额（$（平台），Epay）',
    'Expected monthly API credit (USD)': '预计每月平台金额（$（平台））',
    'Top-up credit to compare (USD)': '要对比的平台金额（$（平台））',
    'Credited API balance': '到账平台金额（$（平台））',
    'Expected monthly platform credit': '预计每月平台金额（$（平台））',
    'Platform credit to compare': '要对比的平台金额（$（平台））',
    'Credited platform balance': '到账平台金额（$（平台））',
  },
  'zh-TW': {
    'Recharge Amount': '平台金額（$（平台））',
    'Recharge Amount (USD)': '平台金額（$（平台））',
    'Maximum credited amount per payment (USD, optional)':
      '單筆最高平台金額（$（平台），選填）',
    'Minimum top-up (USD)': '最低平台金額（$（平台））',
    'Minimum recharge amount in USD': '最低平台金額（$（平台））',
    'Smallest USD amount users can recharge (Epay)':
      '使用者可獲得的最低平台金額（$（平台），Epay）',
    'Expected monthly API credit (USD)': '預計每月平台金額（$（平台））',
    'Top-up credit to compare (USD)': '要比較的平台金額（$（平台））',
    'Credited API balance': '入帳平台金額（$（平台））',
    'Expected monthly platform credit': '預計每月平台金額（$（平台））',
    'Platform credit to compare': '要比較的平台金額（$（平台））',
    'Credited platform balance': '入帳平台金額（$（平台））',
  },
  fr: {
    'Recharge Amount': 'Crédit de plateforme ($ (Plateforme))',
    'Recharge Amount (USD)': 'Crédit de plateforme ($ (Plateforme))',
    'Maximum credited amount per payment (USD, optional)':
      'Crédit de plateforme maximal par paiement ($ (Plateforme), facultatif)',
    'Minimum top-up (USD)': 'Crédit de plateforme minimal ($ (Plateforme))',
    'Minimum recharge amount in USD':
      'Crédit de plateforme minimal ($ (Plateforme))',
    'Smallest USD amount users can recharge (Epay)':
      'Crédit de plateforme minimal reçu ($ (Plateforme), Epay)',
    'Expected monthly API credit (USD)':
      'Crédit de plateforme mensuel prévu ($ (Plateforme))',
    'Top-up credit to compare (USD)':
      'Crédit de plateforme à comparer ($ (Plateforme))',
    'Credited API balance': 'Solde de plateforme crédité ($ (Plateforme))',
    'Expected monthly platform credit':
      'Crédit de plateforme mensuel prévu ($ (Plateforme))',
    'Platform credit to compare':
      'Crédit de plateforme à comparer ($ (Plateforme))',
    'Credited platform balance': 'Solde de plateforme crédité ($ (Plateforme))',
  },
  ja: {
    'Recharge Amount': 'プラットフォーム残高（$（プラットフォーム））',
    'Recharge Amount (USD)': 'プラットフォーム残高（$（プラットフォーム））',
    'Maximum credited amount per payment (USD, optional)':
      '1回あたりの最大プラットフォーム残高（$（プラットフォーム）、任意）',
    'Minimum top-up (USD)': '最小プラットフォーム残高（$（プラットフォーム））',
    'Minimum recharge amount in USD':
      '最小プラットフォーム残高（$（プラットフォーム））',
    'Smallest USD amount users can recharge (Epay)':
      'ユーザーが受け取れる最小プラットフォーム残高（$（プラットフォーム）、Epay）',
    'Expected monthly API credit (USD)':
      '月間プラットフォーム残高の見込み（$（プラットフォーム））',
    'Top-up credit to compare (USD)':
      '比較するプラットフォーム残高（$（プラットフォーム））',
    'Credited API balance':
      '付与されるプラットフォーム残高（$（プラットフォーム））',
    'Expected monthly platform credit':
      '月間プラットフォーム残高の見込み（$（プラットフォーム））',
    'Platform credit to compare':
      '比較するプラットフォーム残高（$（プラットフォーム））',
    'Credited platform balance':
      '付与されるプラットフォーム残高（$（プラットフォーム））',
  },
  ru: {
    'Recharge Amount': 'Платформенный кредит ($ (Платформа))',
    'Recharge Amount (USD)': 'Платформенный кредит ($ (Платформа))',
    'Maximum credited amount per payment (USD, optional)':
      'Максимальный платформенный кредит за платёж ($ (Платформа), необязательно)',
    'Minimum top-up (USD)': 'Минимальный платформенный кредит ($ (Платформа))',
    'Minimum recharge amount in USD':
      'Минимальный платформенный кредит ($ (Платформа))',
    'Smallest USD amount users can recharge (Epay)':
      'Минимальный получаемый платформенный кредит ($ (Платформа), Epay)',
    'Expected monthly API credit (USD)':
      'Ожидаемый месячный платформенный кредит ($ (Платформа))',
    'Top-up credit to compare (USD)':
      'Платформенный кредит для сравнения ($ (Платформа))',
    'Credited API balance': 'Зачисленный платформенный баланс ($ (Платформа))',
    'Expected monthly platform credit':
      'Ожидаемый месячный платформенный кредит ($ (Платформа))',
    'Platform credit to compare':
      'Платформенный кредит для сравнения ($ (Платформа))',
    'Credited platform balance':
      'Зачисленный платформенный баланс ($ (Платформа))',
  },
  vi: {
    'Recharge Amount': 'Tín dụng nền tảng ($ (Nền tảng))',
    'Recharge Amount (USD)': 'Tín dụng nền tảng ($ (Nền tảng))',
    'Maximum credited amount per payment (USD, optional)':
      'Tín dụng nền tảng tối đa mỗi lần thanh toán ($ (Nền tảng), tùy chọn)',
    'Minimum top-up (USD)': 'Tín dụng nền tảng tối thiểu ($ (Nền tảng))',
    'Minimum recharge amount in USD':
      'Tín dụng nền tảng tối thiểu ($ (Nền tảng))',
    'Smallest USD amount users can recharge (Epay)':
      'Tín dụng nền tảng tối thiểu người dùng nhận được ($ (Nền tảng), Epay)',
    'Expected monthly API credit (USD)':
      'Tín dụng nền tảng dự kiến mỗi tháng ($ (Nền tảng))',
    'Top-up credit to compare (USD)':
      'Tín dụng nền tảng để so sánh ($ (Nền tảng))',
    'Credited API balance': 'Số dư nền tảng được cộng ($ (Nền tảng))',
    'Expected monthly platform credit':
      'Tín dụng nền tảng dự kiến mỗi tháng ($ (Nền tảng))',
    'Platform credit to compare': 'Tín dụng nền tảng để so sánh ($ (Nền tảng))',
    'Credited platform balance': 'Số dư nền tảng được cộng ($ (Nền tảng))',
  },
}
for (const [locale, translations] of Object.entries(
  platformAmountTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const auditedCurrencyTranslations = {
  en: {
    'Currency unavailable': 'Currency unavailable',
    '1 USD provider cost → {{price}} platform price':
      '1 USD provider cost → {{price}} platform price',
    'Chat with AI to earn a $0–$10 (Platform) new-user gift':
      'Chat with AI to earn a $0–$10 (Platform) new-user gift',
    'Valid reports earn at least 5 USD after review. Submission does not guarantee a reward.':
      'Valid reports earn at least 5 USD after review. Submission does not guarantee a reward.',
    'The bound Product powers wallet top-ups: when a user enters any amount, this platform runs the checkout against this single Pancake product and overrides the price per session — no need to pre-create 1 USD / 5 USD / 10 USD SKUs.':
      'The bound Product powers wallet top-ups: when a user enters any amount, this platform runs the checkout against this single Pancake product and overrides the price per session — no need to pre-create 1 USD / 5 USD / 10 USD SKUs.',
  },
  zh: {
    'Currency unavailable': '币种不可用',
    '1 USD provider cost → {{price}} platform price':
      '提供商成本 1 USD → 平台价格 {{price}}',
    'Chat with AI to earn a $0–$10 (Platform) new-user gift':
      '与 AI 对话，赢取 $0–$10（平台）新用户礼金',
    'Valid reports earn at least 5 USD after review. Submission does not guarantee a reward.':
      '有效报告通过审核后至少可获得 5 USD；提交报告不保证一定获得奖励。',
    'The bound Product powers wallet top-ups: when a user enters any amount, this platform runs the checkout against this single Pancake product and overrides the price per session — no need to pre-create 1 USD / 5 USD / 10 USD SKUs.':
      '绑定的产品用于钱包充值：用户输入任意金额后，平台会使用同一个 Pancake 产品结账，并为本次会话覆盖价格，无需预先创建 1 USD / 5 USD / 10 USD SKU。',
  },
  'zh-TW': {
    'Currency unavailable': '幣種不可用',
    '1 USD provider cost → {{price}} platform price':
      '供應商成本 1 USD → 平台價格 {{price}}',
    'Chat with AI to earn a $0–$10 (Platform) new-user gift':
      '與 AI 對話，獲得 $0–$10（平台）新使用者禮金',
    'Valid reports earn at least 5 USD after review. Submission does not guarantee a reward.':
      '有效回報通過審核後至少可獲得 5 USD；提交回報不保證一定獲得獎勵。',
    'The bound Product powers wallet top-ups: when a user enters any amount, this platform runs the checkout against this single Pancake product and overrides the price per session — no need to pre-create 1 USD / 5 USD / 10 USD SKUs.':
      '綁定的產品用於錢包儲值：使用者輸入任意金額後，平台會使用同一個 Pancake 產品結帳，並為本次工作階段覆寫價格，無需預先建立 1 USD / 5 USD / 10 USD SKU。',
  },
  fr: {
    'Currency unavailable': 'Devise indisponible',
    '1 USD provider cost → {{price}} platform price':
      'Coût fournisseur de 1 USD → prix plateforme {{price}}',
    'Chat with AI to earn a $0–$10 (Platform) new-user gift':
      'Discutez avec l’IA pour gagner un cadeau de 0 à 10 $ (Plateforme)',
    'Valid reports earn at least 5 USD after review. Submission does not guarantee a reward.':
      'Les rapports valides rapportent au moins 5 USD après examen. Aucun gain n’est garanti.',
    'The bound Product powers wallet top-ups: when a user enters any amount, this platform runs the checkout against this single Pancake product and overrides the price per session — no need to pre-create 1 USD / 5 USD / 10 USD SKUs.':
      'Le produit lié alimente les recharges : le paiement utilise ce produit Pancake et remplace son prix pour la session, sans créer de SKU à 1 USD, 5 USD ou 10 USD.',
  },
  ja: {
    'Currency unavailable': '通貨情報なし',
    '1 USD provider cost → {{price}} platform price':
      'プロバイダー原価 1 USD → プラットフォーム価格 {{price}}',
    'Chat with AI to earn a $0–$10 (Platform) new-user gift':
      'AI と対話して $0〜$10（プラットフォーム）の新規特典を獲得',
    'Valid reports earn at least 5 USD after review. Submission does not guarantee a reward.':
      '有効な報告は審査後に最低 5 USD の対象です。報告しても報酬は保証されません。',
    'The bound Product powers wallet top-ups: when a user enters any amount, this platform runs the checkout against this single Pancake product and overrides the price per session — no need to pre-create 1 USD / 5 USD / 10 USD SKUs.':
      '紐付けた製品でウォレットをチャージします。任意額の入力時に同じ Pancake 製品を使い、セッションごとに価格を上書きするため、1 USD / 5 USD / 10 USD の SKU は不要です。',
  },
  ru: {
    'Currency unavailable': 'Валюта недоступна',
    '1 USD provider cost → {{price}} platform price':
      'Стоимость провайдера 1 USD → цена платформы {{price}}',
    'Chat with AI to earn a $0–$10 (Platform) new-user gift':
      'Общайтесь с ИИ и получите подарок $0–$10 (Платформа)',
    'Valid reports earn at least 5 USD after review. Submission does not guarantee a reward.':
      'За подтверждённые отчёты начисляется не менее 5 USD. Награда не гарантируется.',
    'The bound Product powers wallet top-ups: when a user enters any amount, this platform runs the checkout against this single Pancake product and overrides the price per session — no need to pre-create 1 USD / 5 USD / 10 USD SKUs.':
      'Привязанный продукт используется для пополнений: платформа оформляет платёж через один продукт Pancake и переопределяет цену на сеанс, поэтому SKU на 1 USD, 5 USD и 10 USD не нужны.',
  },
  vi: {
    'Currency unavailable': 'Không có thông tin tiền tệ',
    '1 USD provider cost → {{price}} platform price':
      'Chi phí nhà cung cấp 1 USD → giá nền tảng {{price}}',
    'Chat with AI to earn a $0–$10 (Platform) new-user gift':
      'Trò chuyện với AI để nhận quà người dùng mới $0–$10 (Nền tảng)',
    'Valid reports earn at least 5 USD after review. Submission does not guarantee a reward.':
      'Báo cáo hợp lệ nhận ít nhất 5 USD sau khi duyệt. Gửi báo cáo không đảm bảo có thưởng.',
    'The bound Product powers wallet top-ups: when a user enters any amount, this platform runs the checkout against this single Pancake product and overrides the price per session — no need to pre-create 1 USD / 5 USD / 10 USD SKUs.':
      'Sản phẩm đã liên kết dùng cho nạp ví: nền tảng thanh toán qua cùng một sản phẩm Pancake và ghi đè giá theo phiên, không cần tạo trước SKU 1 USD / 5 USD / 10 USD.',
  },
}
for (const [locale, translations] of Object.entries(
  auditedCurrencyTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const walletTerminologyTranslations = {
  en: {
    'Platform credit': 'Platform credit',
    'Custom platform credit': 'Custom platform credit',
    'Maximum platform credit per payment: {{amount}}':
      'Maximum platform credit per payment: {{amount}}',
    'Maximum: {{amount}}': 'Maximum: {{amount}}',
    'Credit {{amount}}; pay {{payment}}': 'Credit {{amount}}; pay {{payment}}',
  },
  zh: {
    'Platform credit': '平台额度',
    'Custom platform credit': '自定义平台额度',
    'Maximum platform credit per payment: {{amount}}':
      '单笔最高平台金额：{{amount}}',
    'Maximum: {{amount}}': '上限：{{amount}}',
    'Credit {{amount}}; pay {{payment}}':
      '到账 {{amount}}；实际支付 {{payment}}',
  },
  'zh-TW': {
    'Platform credit': '平台額度',
    'Custom platform credit': '自訂平台額度',
    'Maximum platform credit per payment: {{amount}}':
      '單筆最高平台金額：{{amount}}',
    'Maximum: {{amount}}': '上限：{{amount}}',
    'Credit {{amount}}; pay {{payment}}':
      '入帳 {{amount}}；實際支付 {{payment}}',
  },
  fr: {
    'Platform credit': 'Crédit de plateforme',
    'Custom platform credit': 'Crédit de plateforme personnalisé',
    'Maximum platform credit per payment: {{amount}}':
      'Crédit de plateforme maximal par paiement : {{amount}}',
    'Maximum: {{amount}}': 'Maximum : {{amount}}',
    'Credit {{amount}}; pay {{payment}}':
      'Crédit {{amount}} ; paiement {{payment}}',
  },
  ja: {
    'Platform credit': 'プラットフォームクレジット',
    'Custom platform credit': '任意のプラットフォームクレジット',
    'Maximum platform credit per payment: {{amount}}':
      '1回あたりの最大プラットフォーム残高：{{amount}}',
    'Maximum: {{amount}}': '上限：{{amount}}',
    'Credit {{amount}}; pay {{payment}}':
      '付与額 {{amount}}；支払額 {{payment}}',
  },
  ru: {
    'Platform credit': 'Платформенный кредит',
    'Custom platform credit': 'Другая сумма кредита платформы',
    'Maximum platform credit per payment: {{amount}}':
      'Максимальный платформенный кредит за платёж: {{amount}}',
    'Maximum: {{amount}}': 'Максимум: {{amount}}',
    'Credit {{amount}}; pay {{payment}}':
      'Зачисление {{amount}}; оплата {{payment}}',
  },
  vi: {
    'Platform credit': 'Tín dụng nền tảng',
    'Custom platform credit': 'Tín dụng nền tảng tùy chỉnh',
    'Maximum platform credit per payment: {{amount}}':
      'Tín dụng nền tảng tối đa mỗi lần thanh toán: {{amount}}',
    'Maximum: {{amount}}': 'Tối đa: {{amount}}',
    'Credit {{amount}}; pay {{payment}}':
      'Được cộng {{amount}}; thanh toán {{payment}}',
  },
}
for (const [locale, translations] of Object.entries(
  walletTerminologyTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const exchangeRateLoadTranslations = {
  en: {
    'Enter a three-letter ISO 4217 currency code':
      'Enter a three-letter ISO 4217 currency code',
    'Failed to load exchange rate': 'Failed to load exchange rate',
    'Latest exchange rate loaded: 1 USD = {{rate}} {{currency}}. Save changes to apply it.':
      'Latest exchange rate loaded: 1 USD = {{rate}} {{currency}}. Save changes to apply it.',
  },
  zh: {
    'Enter a three-letter ISO 4217 currency code':
      '请输入三字母 ISO 4217 货币代码',
    'Failed to load exchange rate': '加载汇率失败',
    'Latest exchange rate loaded: 1 USD = {{rate}} {{currency}}. Save changes to apply it.':
      '已加载最新汇率：1 USD = {{rate}} {{currency}}。保存更改后生效。',
  },
  'zh-TW': {
    'Enter a three-letter ISO 4217 currency code':
      '請輸入三字母 ISO 4217 貨幣代碼',
    'Failed to load exchange rate': '載入匯率失敗',
    'Latest exchange rate loaded: 1 USD = {{rate}} {{currency}}. Save changes to apply it.':
      '已載入最新匯率：1 USD = {{rate}} {{currency}}。儲存變更後生效。',
  },
  fr: {
    'Enter a three-letter ISO 4217 currency code':
      'Saisissez un code de devise ISO 4217 à trois lettres',
    'Failed to load exchange rate': 'Échec du chargement du taux de change',
    'Latest exchange rate loaded: 1 USD = {{rate}} {{currency}}. Save changes to apply it.':
      'Dernier taux chargé : 1 USD = {{rate}} {{currency}}. Enregistrez pour l’appliquer.',
  },
  ja: {
    'Enter a three-letter ISO 4217 currency code':
      '3文字の ISO 4217 通貨コードを入力してください',
    'Failed to load exchange rate': '為替レートの読み込みに失敗しました',
    'Latest exchange rate loaded: 1 USD = {{rate}} {{currency}}. Save changes to apply it.':
      '最新レートを読み込みました：1 USD = {{rate}} {{currency}}。保存すると適用されます。',
  },
  ru: {
    'Enter a three-letter ISO 4217 currency code':
      'Введите трёхбуквенный код валюты ISO 4217',
    'Failed to load exchange rate': 'Не удалось загрузить курс валют',
    'Latest exchange rate loaded: 1 USD = {{rate}} {{currency}}. Save changes to apply it.':
      'Загружен актуальный курс: 1 USD = {{rate}} {{currency}}. Сохраните изменения для применения.',
  },
  vi: {
    'Enter a three-letter ISO 4217 currency code':
      'Nhập mã tiền tệ ISO 4217 gồm ba chữ cái',
    'Failed to load exchange rate': 'Không thể tải tỷ giá',
    'Latest exchange rate loaded: 1 USD = {{rate}} {{currency}}. Save changes to apply it.':
      'Đã tải tỷ giá mới nhất: 1 USD = {{rate}} {{currency}}. Hãy lưu để áp dụng.',
  },
}
for (const [locale, translations] of Object.entries(
  exchangeRateLoadTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const settlementContractTranslations = {
  en: {
    'Settlement currency must be a three-letter ISO code':
      'Settlement currency must be a three-letter ISO code',
    'USD settlement rate must be a positive decimal number':
      'USD settlement rate must be a positive decimal number',
    'Legacy direct rate must be a positive decimal number':
      'Legacy direct rate must be a positive decimal number',
    'Set the amount charged for each real USD':
      'Set the amount charged for each real USD',
    'Set the ISO settlement currency for this USD rate':
      'Set the ISO settlement currency for this USD rate',
    'Remove legacy direct pricing before using a real-USD rate':
      'Remove legacy direct pricing before using a real-USD rate',
    'Legacy direct-rate fields must match':
      'Legacy direct-rate fields must match',
    'Settlement currency (ISO code)': 'Settlement currency (ISO code)',
    'The actual fiat currency charged by this gateway.':
      'The actual fiat currency charged by this gateway.',
    'Settlement amount per 1 real USD': 'Settlement amount per 1 real USD',
    'Example: enter 1 for USD or 6.8 when 1 USD equals 6.8 CNY.':
      'Example: enter 1 for USD or 6.8 when 1 USD equals 6.8 CNY.',
    'Checkout first converts the platform amount to real USD using the synchronized USD rate, then converts USD to the gateway settlement currency.':
      'Checkout first converts the platform amount to real USD using the synchronized USD rate, then converts USD to the gateway settlement currency.',
    'Settlement preview: 1 USD = {{rate}} {{currency}}':
      'Settlement preview: 1 USD = {{rate}} {{currency}}',
    'Legacy direct pricing is preserved until you enter and save the real-USD settlement fields above.':
      'Legacy direct pricing is preserved until you enter and save the real-USD settlement fields above.',
  },
  zh: {
    'Settlement currency must be a three-letter ISO code':
      '结算货币必须是三字母 ISO 代码',
    'USD settlement rate must be a positive decimal number':
      '每 USD 结算金额必须为正小数',
    'Legacy direct rate must be a positive decimal number':
      '旧版直连单价必须为正小数',
    'Set the amount charged for each real USD': '请填写每 1 USD 的实际扣款金额',
    'Set the ISO settlement currency for this USD rate':
      '请填写此美元汇率对应的 ISO 结算货币',
    'Remove legacy direct pricing before using a real-USD rate':
      '使用真实 USD 汇率前，请先移除旧版直连计价',
    'Legacy direct-rate fields must match': '旧版直连计价字段必须一致',
    'Settlement currency (ISO code)': '结算货币（ISO 代码）',
    'The actual fiat currency charged by this gateway.':
      '此网关实际扣款使用的法币。',
    'Settlement amount per 1 real USD': '每 1 USD 的结算金额',
    'Example: enter 1 for USD or 6.8 when 1 USD equals 6.8 CNY.':
      '示例：USD 填 1；若 1 USD = 6.8 CNY，则填 6.8。',
    'Checkout first converts the platform amount to real USD using the synchronized USD rate, then converts USD to the gateway settlement currency.':
      '结账时先按已同步的美元汇率把平台金额换算为真实 USD，再换算为网关结算货币。',
    'Settlement preview: 1 USD = {{rate}} {{currency}}':
      '结算预览：1 USD = {{rate}} {{currency}}',
    'Legacy direct pricing is preserved until you enter and save the real-USD settlement fields above.':
      '在填写并保存上方真实 USD 结算字段前，系统会继续保留旧版直连计价。',
  },
  'zh-TW': {
    'Settlement currency must be a three-letter ISO code':
      '結算貨幣必須是三字母 ISO 代碼',
    'USD settlement rate must be a positive decimal number':
      '每 USD 結算金額必須為正小數',
    'Legacy direct rate must be a positive decimal number':
      '舊版直連單價必須為正小數',
    'Set the amount charged for each real USD': '請填寫每 1 USD 的實際扣款金額',
    'Set the ISO settlement currency for this USD rate':
      '請填寫此美元匯率對應的 ISO 結算貨幣',
    'Remove legacy direct pricing before using a real-USD rate':
      '使用真實 USD 匯率前，請先移除舊版直連計價',
    'Legacy direct-rate fields must match': '舊版直連計價欄位必須一致',
    'Settlement currency (ISO code)': '結算貨幣（ISO 代碼）',
    'The actual fiat currency charged by this gateway.':
      '此閘道實際扣款使用的法幣。',
    'Settlement amount per 1 real USD': '每 1 USD 的結算金額',
    'Example: enter 1 for USD or 6.8 when 1 USD equals 6.8 CNY.':
      '範例：USD 填 1；若 1 USD = 6.8 CNY，則填 6.8。',
    'Checkout first converts the platform amount to real USD using the synchronized USD rate, then converts USD to the gateway settlement currency.':
      '結帳時先按已同步的美元匯率把平台金額換算為真實 USD，再換算為閘道結算貨幣。',
    'Settlement preview: 1 USD = {{rate}} {{currency}}':
      '結算預覽：1 USD = {{rate}} {{currency}}',
    'Legacy direct pricing is preserved until you enter and save the real-USD settlement fields above.':
      '在填寫並儲存上方真實 USD 結算欄位前，系統會繼續保留舊版直連計價。',
  },
  fr: {
    'Settlement currency must be a three-letter ISO code':
      'La devise de règlement doit être un code ISO à trois lettres',
    'USD settlement rate must be a positive decimal number':
      'Le taux de règlement USD doit être un nombre décimal positif',
    'Legacy direct rate must be a positive decimal number':
      'L’ancien taux direct doit être un nombre décimal positif',
    'Set the amount charged for each real USD':
      'Indiquez le montant facturé pour chaque USD réel',
    'Set the ISO settlement currency for this USD rate':
      'Indiquez la devise ISO correspondant à ce taux USD',
    'Remove legacy direct pricing before using a real-USD rate':
      'Supprimez l’ancien tarif direct avant d’utiliser un taux USD réel',
    'Legacy direct-rate fields must match':
      'Les anciens champs de taux direct doivent correspondre',
    'Settlement currency (ISO code)': 'Devise de règlement (code ISO)',
    'The actual fiat currency charged by this gateway.':
      'La devise fiduciaire réellement facturée par cette passerelle.',
    'Settlement amount per 1 real USD': 'Montant de règlement pour 1 USD réel',
    'Example: enter 1 for USD or 6.8 when 1 USD equals 6.8 CNY.':
      'Exemple : saisissez 1 pour USD, ou 6,8 si 1 USD = 6,8 CNY.',
    'Checkout first converts the platform amount to real USD using the synchronized USD rate, then converts USD to the gateway settlement currency.':
      'Le paiement convertit d’abord le montant de plateforme en USD réels avec le taux synchronisé, puis dans la devise de règlement.',
    'Settlement preview: 1 USD = {{rate}} {{currency}}':
      'Aperçu du règlement : 1 USD = {{rate}} {{currency}}',
    'Legacy direct pricing is preserved until you enter and save the real-USD settlement fields above.':
      'L’ancien tarif direct est conservé jusqu’à l’enregistrement des champs USD réels ci-dessus.',
  },
  ja: {
    'Settlement currency must be a three-letter ISO code':
      '決済通貨は3文字の ISO コードで指定してください',
    'USD settlement rate must be a positive decimal number':
      'USD 決済レートは正の小数で指定してください',
    'Legacy direct rate must be a positive decimal number':
      '旧形式の直接レートは正の小数で指定してください',
    'Set the amount charged for each real USD':
      '実 USD 1 単位あたりの請求額を入力してください',
    'Set the ISO settlement currency for this USD rate':
      'この USD レートに対応する ISO 決済通貨を入力してください',
    'Remove legacy direct pricing before using a real-USD rate':
      '実 USD レートを使う前に旧形式の直接価格を削除してください',
    'Legacy direct-rate fields must match':
      '旧形式の直接レート項目を一致させてください',
    'Settlement currency (ISO code)': '決済通貨（ISO コード）',
    'The actual fiat currency charged by this gateway.':
      'このゲートウェイが実際に請求する法定通貨です。',
    'Settlement amount per 1 real USD': '実 USD 1 単位あたりの決済額',
    'Example: enter 1 for USD or 6.8 when 1 USD equals 6.8 CNY.':
      '例：USD は 1、1 USD = 6.8 CNY の場合は 6.8 を入力します。',
    'Checkout first converts the platform amount to real USD using the synchronized USD rate, then converts USD to the gateway settlement currency.':
      '決済時は同期済み USD レートでプラットフォーム金額を実 USD に換算し、さらにゲートウェイの決済通貨へ換算します。',
    'Settlement preview: 1 USD = {{rate}} {{currency}}':
      '決済プレビュー：1 USD = {{rate}} {{currency}}',
    'Legacy direct pricing is preserved until you enter and save the real-USD settlement fields above.':
      '上記の実 USD 決済項目を保存するまで、旧形式の直接価格は保持されます。',
  },
  ru: {
    'Settlement currency must be a three-letter ISO code':
      'Валюта расчёта должна быть трёхбуквенным кодом ISO',
    'USD settlement rate must be a positive decimal number':
      'Курс расчёта USD должен быть положительным десятичным числом',
    'Legacy direct rate must be a positive decimal number':
      'Старый прямой курс должен быть положительным десятичным числом',
    'Set the amount charged for each real USD':
      'Укажите сумму списания за каждый реальный USD',
    'Set the ISO settlement currency for this USD rate':
      'Укажите ISO-код валюты расчёта для этого курса USD',
    'Remove legacy direct pricing before using a real-USD rate':
      'Удалите старую прямую цену перед использованием курса реального USD',
    'Legacy direct-rate fields must match':
      'Поля старого прямого курса должны совпадать',
    'Settlement currency (ISO code)': 'Валюта расчёта (код ISO)',
    'The actual fiat currency charged by this gateway.':
      'Фиатная валюта, фактически списываемая этим шлюзом.',
    'Settlement amount per 1 real USD': 'Сумма расчёта за 1 реальный USD',
    'Example: enter 1 for USD or 6.8 when 1 USD equals 6.8 CNY.':
      'Пример: для USD укажите 1; если 1 USD = 6,8 CNY, укажите 6,8.',
    'Checkout first converts the platform amount to real USD using the synchronized USD rate, then converts USD to the gateway settlement currency.':
      'Сначала платёж переводит платформенную сумму в реальные USD по синхронизированному курсу, затем — в валюту расчёта шлюза.',
    'Settlement preview: 1 USD = {{rate}} {{currency}}':
      'Предпросмотр расчёта: 1 USD = {{rate}} {{currency}}',
    'Legacy direct pricing is preserved until you enter and save the real-USD settlement fields above.':
      'Старая прямая цена сохраняется до ввода и сохранения полей расчёта в реальных USD.',
  },
  vi: {
    'Settlement currency must be a three-letter ISO code':
      'Tiền tệ quyết toán phải là mã ISO gồm ba chữ cái',
    'USD settlement rate must be a positive decimal number':
      'Tỷ giá quyết toán USD phải là số thập phân dương',
    'Legacy direct rate must be a positive decimal number':
      'Tỷ giá trực tiếp cũ phải là số thập phân dương',
    'Set the amount charged for each real USD':
      'Nhập số tiền tính cho mỗi USD thực',
    'Set the ISO settlement currency for this USD rate':
      'Nhập tiền tệ ISO cho tỷ giá USD này',
    'Remove legacy direct pricing before using a real-USD rate':
      'Xóa giá trực tiếp cũ trước khi dùng tỷ giá USD thực',
    'Legacy direct-rate fields must match':
      'Các trường tỷ giá trực tiếp cũ phải khớp',
    'Settlement currency (ISO code)': 'Tiền tệ quyết toán (mã ISO)',
    'The actual fiat currency charged by this gateway.':
      'Tiền pháp định mà cổng thanh toán này thực sự tính.',
    'Settlement amount per 1 real USD': 'Số tiền quyết toán cho 1 USD thực',
    'Example: enter 1 for USD or 6.8 when 1 USD equals 6.8 CNY.':
      'Ví dụ: nhập 1 cho USD, hoặc 6,8 khi 1 USD = 6,8 CNY.',
    'Checkout first converts the platform amount to real USD using the synchronized USD rate, then converts USD to the gateway settlement currency.':
      'Thanh toán trước tiên đổi số tiền nền tảng sang USD thực theo tỷ giá đã đồng bộ, rồi đổi sang tiền tệ quyết toán của cổng.',
    'Settlement preview: 1 USD = {{rate}} {{currency}}':
      'Xem trước quyết toán: 1 USD = {{rate}} {{currency}}',
    'Legacy direct pricing is preserved until you enter and save the real-USD settlement fields above.':
      'Giá trực tiếp cũ được giữ lại cho đến khi bạn nhập và lưu các trường quyết toán USD thực ở trên.',
  },
}
for (const [locale, translations] of Object.entries(
  settlementContractTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const subscriptionResetUserFilterTranslations = {
  en: {
    'User IDs': 'User IDs',
    'For example: 12, 34, 56': 'For example: 12, 34, 56',
    'User IDs must be positive integers separated by commas, with at most {{count}} entries.':
      'User IDs must be positive integers separated by commas, with at most {{count}} entries.',
    'Optional comma-separated user ID filter.':
      'Optional comma-separated user ID filter.',
  },
  zh: {
    'User IDs': '用户 ID',
    'For example: 12, 34, 56': '例如：12, 34, 56',
    'User IDs must be positive integers separated by commas, with at most {{count}} entries.':
      '用户 ID 必须为用逗号分隔的正整数，最多 {{count}} 个。',
    'Optional comma-separated user ID filter.':
      '可选：用逗号分隔的用户 ID 筛选条件。',
  },
  'zh-TW': {
    'User IDs': '使用者 ID',
    'For example: 12, 34, 56': '例如：12, 34, 56',
    'User IDs must be positive integers separated by commas, with at most {{count}} entries.':
      '使用者 ID 必須是以逗號分隔的正整數，最多 {{count}} 個。',
    'Optional comma-separated user ID filter.':
      '選填：以逗號分隔的使用者 ID 篩選條件。',
  },
  fr: {
    'User IDs': 'ID utilisateur',
    'For example: 12, 34, 56': 'Par exemple : 12, 34, 56',
    'User IDs must be positive integers separated by commas, with at most {{count}} entries.':
      'Les ID utilisateur doivent être des entiers positifs séparés par des virgules, avec un maximum de {{count}} entrées.',
    'Optional comma-separated user ID filter.':
      'Filtre facultatif d’ID utilisateur séparés par des virgules.',
  },
  ja: {
    'User IDs': 'ユーザー ID',
    'For example: 12, 34, 56': '例：12, 34, 56',
    'User IDs must be positive integers separated by commas, with at most {{count}} entries.':
      'ユーザー ID はカンマ区切りの正の整数で、最大 {{count}} 件までです。',
    'Optional comma-separated user ID filter.':
      '任意：カンマ区切りのユーザー ID フィルター。',
  },
  ru: {
    'User IDs': 'ID пользователей',
    'For example: 12, 34, 56': 'Например: 12, 34, 56',
    'User IDs must be positive integers separated by commas, with at most {{count}} entries.':
      'ID пользователей должны быть положительными целыми числами, разделёнными запятыми; не более {{count}}.',
    'Optional comma-separated user ID filter.':
      'Необязательный фильтр по ID пользователей через запятую.',
  },
  vi: {
    'User IDs': 'ID người dùng',
    'For example: 12, 34, 56': 'Ví dụ: 12, 34, 56',
    'User IDs must be positive integers separated by commas, with at most {{count}} entries.':
      'ID người dùng phải là số nguyên dương, phân tách bằng dấu phẩy, tối đa {{count}} mục.',
    'Optional comma-separated user ID filter.':
      'Bộ lọc ID người dùng tùy chọn, phân tách bằng dấu phẩy.',
  },
}
for (const [locale, translations] of Object.entries(
  subscriptionResetUserFilterTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const statusDetectionTranslations = {
  en: {
    'Average TTFT': 'Average TTFT',
    Degraded: 'Degraded',
    'Fastest first token': 'Fastest first token',
    'First-token trend': 'First-token trend',
    'Highest success rate': 'Highest success rate',
    'Sort groups': 'Sort groups',
    'Service observability': 'Service observability',
    'Status detection': 'Status detection',
    'Check recent availability and performance for each model group.':
      'Check recent availability and performance for each model group.',
    'Unable to load status data': 'Unable to load status data',
    'Please try again in a moment.': 'Please try again in a moment.',
    'No status data is available yet.': 'No status data is available yet.',
    'Groups monitored': 'Groups monitored',
    'Active groups with recent traffic': 'Active groups with recent traffic',
    'Groups at 90% success or higher': 'Groups at 90% success or higher',
    'Groups needing attention': 'Groups needing attention',
    'Models checked': 'Models checked',
    'Top models by recent traffic': 'Top models by recent traffic',
    '{{count}} model checks could not be completed.':
      '{{count}} model checks could not be completed.',
    'Group status': 'Group status',
    'Last 24 hours': 'Last 24 hours',
    '{{count}} groups': '{{count}} groups',
    'Success rate trend': 'Success rate trend',
    '{{count}} models reporting': '{{count}} models reporting',
  },
  zh: {
    'Average TTFT': '平均首字延迟',
    Degraded: '服务降级',
    'Fastest first token': '最快首字响应',
    'First-token trend': '首字延迟趋势',
    'Highest success rate': '成功率最高',
    'Sort groups': '分组排序',
    'Service observability': '服务可观测性',
    'Status detection': '状态检测',
    'Check recent availability and performance for each model group.':
      '查看每个模型分组最近的可用性与性能。',
    'Unable to load status data': '无法加载状态数据',
    'Please try again in a moment.': '请稍后重试。',
    'No status data is available yet.': '暂时没有可用的状态数据。',
    'Groups monitored': '监测分组',
    'Active groups with recent traffic': '近期有流量的活跃分组',
    'Groups at 90% success or higher': '成功率达到 90% 及以上的分组',
    'Groups needing attention': '需要关注的分组',
    'Models checked': '已检测模型',
    'Top models by recent traffic': '按近期流量排序的模型',
    '{{count}} model checks could not be completed.':
      '有 {{count}} 个模型检测未完成。',
    'Group status': '分组状态',
    'Last 24 hours': '最近 24 小时',
    '{{count}} groups': '{{count}} 个分组',
    'Success rate trend': '成功率趋势',
    '{{count}} models reporting': '{{count}} 个模型有数据',
  },
  'zh-TW': {
    'Average TTFT': '平均首字延遲',
    Degraded: '服務降級',
    'Fastest first token': '最快首字回應',
    'First-token trend': '首字延遲趨勢',
    'Highest success rate': '成功率最高',
    'Sort groups': '分組排序',
    'Service observability': '服務可觀測性',
    'Status detection': '狀態檢測',
    'Check recent availability and performance for each model group.':
      '查看每個模型分組最近的可用性與效能。',
    'Unable to load status data': '無法載入狀態資料',
    'Please try again in a moment.': '請稍後再試。',
    'No status data is available yet.': '目前沒有可用的狀態資料。',
    'Groups monitored': '監測分組',
    'Active groups with recent traffic': '近期有流量的活躍分組',
    'Groups at 90% success or higher': '成功率達 90% 以上的分組',
    'Groups needing attention': '需要關注的分組',
    'Models checked': '已檢測模型',
    'Top models by recent traffic': '依近期流量排序的模型',
    '{{count}} model checks could not be completed.':
      '有 {{count}} 個模型檢測未完成。',
    'Group status': '分組狀態',
    'Last 24 hours': '最近 24 小時',
    '{{count}} groups': '{{count}} 個群組',
    'Success rate trend': '成功率趨勢',
    '{{count}} models reporting': '{{count}} 個模型有資料',
  },
  fr: {
    'Average TTFT': 'TTFT moyen',
    Degraded: 'Dégradé',
    'Fastest first token': 'Premier jeton le plus rapide',
    'First-token trend': 'Évolution du premier jeton',
    'Highest success rate': 'Meilleur taux de réussite',
    'Sort groups': 'Trier les groupes',
    'Service observability': 'Observabilité du service',
    'Status detection': 'Détection de l’état',
    'Check recent availability and performance for each model group.':
      'Consultez la disponibilité et les performances récentes de chaque groupe de modèles.',
    'Unable to load status data': 'Impossible de charger les données d’état',
    'Please try again in a moment.': 'Réessayez dans un instant.',
    'No status data is available yet.':
      'Aucune donnée d’état disponible pour le moment.',
    'Groups monitored': 'Groupes surveillés',
    'Active groups with recent traffic': 'Groupes actifs avec du trafic récent',
    'Groups at 90% success or higher': 'Groupes avec au moins 90 % de réussite',
    'Groups needing attention': 'Groupes nécessitant une attention',
    'Models checked': 'Modèles vérifiés',
    'Top models by recent traffic': 'Modèles selon le trafic récent',
    '{{count}} model checks could not be completed.':
      '{{count}} vérifications de modèles n’ont pas abouti.',
    'Group status': 'État des groupes',
    'Last 24 hours': 'Dernières 24 heures',
    '{{count}} groups': '{{count}} groupes',
    'Success rate trend': 'Tendance du taux de réussite',
    '{{count}} models reporting': '{{count}} modèles avec des données',
  },
  ja: {
    'Average TTFT': '平均 TTFT',
    Degraded: 'パフォーマンス低下',
    'Fastest first token': '最速の最初のトークン',
    'First-token trend': '最初のトークンの推移',
    'Highest success rate': '成功率が高い順',
    'Sort groups': 'グループを並べ替え',
    'Service observability': 'サービスの可観測性',
    'Status detection': 'ステータス検出',
    'Check recent availability and performance for each model group.':
      'モデルグループごとの最近の可用性とパフォーマンスを確認します。',
    'Unable to load status data': 'ステータスデータを読み込めません',
    'Please try again in a moment.': 'しばらくしてからもう一度お試しください。',
    'No status data is available yet.':
      '利用可能なステータスデータはまだありません。',
    'Groups monitored': '監視グループ',
    'Active groups with recent traffic':
      '最近トラフィックがあるアクティブグループ',
    'Groups at 90% success or higher': '成功率 90% 以上のグループ',
    'Groups needing attention': '要注意のグループ',
    'Models checked': '確認済みモデル',
    'Top models by recent traffic': '最近のトラフィック上位モデル',
    '{{count}} model checks could not be completed.':
      '{{count}} 件のモデル確認を完了できませんでした。',
    'Group status': 'グループステータス',
    'Last 24 hours': '過去 24 時間',
    '{{count}} groups': '{{count}} グループ',
    'Success rate trend': '成功率の推移',
    '{{count}} models reporting': '{{count}} モデルが報告',
  },
  ru: {
    'Average TTFT': 'Средний TTFT',
    Degraded: 'Работа ухудшена',
    'Fastest first token': 'Самый быстрый первый токен',
    'First-token trend': 'Динамика первого токена',
    'Highest success rate': 'По успешности',
    'Sort groups': 'Сортировка групп',
    'Service observability': 'Наблюдаемость сервиса',
    'Status detection': 'Проверка состояния',
    'Check recent availability and performance for each model group.':
      'Проверяйте недавнюю доступность и производительность каждой группы моделей.',
    'Unable to load status data': 'Не удалось загрузить данные о состоянии',
    'Please try again in a moment.': 'Повторите попытку через некоторое время.',
    'No status data is available yet.': 'Данные о состоянии пока недоступны.',
    'Groups monitored': 'Группы под наблюдением',
    'Active groups with recent traffic': 'Активные группы с недавним трафиком',
    'Groups at 90% success or higher': 'Группы с успешностью от 90%',
    'Groups needing attention': 'Группы, требующие внимания',
    'Models checked': 'Проверено моделей',
    'Top models by recent traffic': 'Модели с наибольшим недавним трафиком',
    '{{count}} model checks could not be completed.':
      'Не удалось проверить {{count}} моделей.',
    'Group status': 'Состояние групп',
    'Last 24 hours': 'Последние 24 часа',
    '{{count}} groups': '{{count}} групп',
    'Success rate trend': 'Динамика успешности',
    '{{count}} models reporting': '{{count}} моделей с данными',
  },
  vi: {
    'Average TTFT': 'TTFT trung bình',
    Degraded: 'Suy giảm',
    'Fastest first token': 'Token đầu tiên nhanh nhất',
    'First-token trend': 'Xu hướng token đầu tiên',
    'Highest success rate': 'Tỷ lệ thành công cao nhất',
    'Sort groups': 'Sắp xếp nhóm',
    'Service observability': 'Khả năng quan sát dịch vụ',
    'Status detection': 'Kiểm tra trạng thái',
    'Check recent availability and performance for each model group.':
      'Kiểm tra khả dụng và hiệu năng gần đây của từng nhóm mô hình.',
    'Unable to load status data': 'Không thể tải dữ liệu trạng thái',
    'Please try again in a moment.': 'Vui lòng thử lại sau ít phút.',
    'No status data is available yet.': 'Chưa có dữ liệu trạng thái.',
    'Groups monitored': 'Nhóm được giám sát',
    'Active groups with recent traffic':
      'Nhóm đang hoạt động có lưu lượng gần đây',
    'Groups at 90% success or higher': 'Nhóm có tỷ lệ thành công từ 90%',
    'Groups needing attention': 'Nhóm cần được chú ý',
    'Models checked': 'Mô hình đã kiểm tra',
    'Top models by recent traffic': 'Mô hình theo lưu lượng gần đây',
    '{{count}} model checks could not be completed.':
      'Không thể hoàn tất kiểm tra {{count}} mô hình.',
    'Group status': 'Trạng thái nhóm',
    'Last 24 hours': '24 giờ qua',
    '{{count}} groups': '{{count}} nhóm',
    'Success rate trend': 'Xu hướng tỷ lệ thành công',
    '{{count}} models reporting': '{{count}} mô hình có dữ liệu',
  },
}
for (const [locale, translations] of Object.entries(
  statusDetectionTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const checkinCalendarTranslations = {
  en: {
    'Next month': 'Next month',
    'Previous month': 'Previous month',
  },
  zh: {
    'Next month': '下个月',
    'Previous month': '上个月',
  },
  'zh-TW': {
    'Next month': '下個月',
    'Previous month': '上個月',
  },
  fr: {
    'Next month': 'Mois suivant',
    'Previous month': 'Mois précédent',
  },
  ja: {
    'Next month': '次の月',
    'Previous month': '前の月',
  },
  ru: {
    'Next month': 'Следующий месяц',
    'Previous month': 'Предыдущий месяц',
  },
  vi: {
    'Next month': 'Tháng sau',
    'Previous month': 'Tháng trước',
  },
}
for (const [locale, translations] of Object.entries(
  checkinCalendarTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const bountyLifecycleTranslations = {
  en: {
    'Rejected challenges can still be appealed. Wait until the seven-day appeal window ends unless a dispute is opened.':
      'Rejected challenges can still be appealed. Wait until the seven-day appeal window ends unless a dispute is opened.',
    'Resolve all open disputes before closing this bounty or refunding escrow.':
      'Resolve all open disputes before closing this bounty or refunding escrow.',
    'Bounty status summary': 'Bounty status summary',
    Participants: 'Participants',
    'In progress': 'In progress',
    'Awaiting review': 'Awaiting review',
    'In appeal window': 'In appeal window',
    'Open disputes': 'Open disputes',
    'Why closing is unavailable': 'Why closing is unavailable',
    'This bounty cannot be closed yet. Resolve the blockers below:':
      'This bounty cannot be closed yet. Resolve the blockers below:',
    'In progress: {{accepted}} · Awaiting review: {{submitted}}':
      'In progress: {{accepted}} · Awaiting review: {{submitted}}',
    'Challenges still in the appeal window: {{count}}. Latest deadline: {{date}}.':
      'Challenges still in the appeal window: {{count}}. Latest deadline: {{date}}.',
    'Open disputes: {{count}}. A third-party administrator must resolve them before escrow can be refunded.':
      'Open disputes: {{count}}. A third-party administrator must resolve them before escrow can be refunded.',
  },
  zh: {
    'Rejected challenges can still be appealed. Wait until the seven-day appeal window ends unless a dispute is opened.':
      '被拒绝的挑战仍可申诉。请等待 7 天申诉期结束；若有人发起争议，则需先处理争议。',
    'Resolve all open disputes before closing this bounty or refunding escrow.':
      '关闭悬赏或退回托管额度前，请先解决所有未结争议。',
    'Bounty status summary': '悬赏状态概览',
    Participants: '参与人数',
    'In progress': '进行中',
    'Awaiting review': '等待审核',
    'In appeal window': '申诉期内',
    'Open disputes': '未结争议',
    'Why closing is unavailable': '为什么暂时无法关闭',
    'This bounty cannot be closed yet. Resolve the blockers below:':
      '此悬赏暂时无法关闭，请先处理以下事项：',
    'In progress: {{accepted}} · Awaiting review: {{submitted}}':
      '进行中：{{accepted}} · 等待审核：{{submitted}}',
    'Challenges still in the appeal window: {{count}}. Latest deadline: {{date}}.':
      '仍在申诉期内的挑战：{{count}} 个。最晚截止时间：{{date}}。',
    'Open disputes: {{count}}. A third-party administrator must resolve them before escrow can be refunded.':
      '未结争议：{{count}} 个。必须由第三方管理员解决后，才能退回托管额度。',
  },
  'zh-TW': {
    'Rejected challenges can still be appealed. Wait until the seven-day appeal window ends unless a dispute is opened.':
      '遭拒絕的挑戰仍可申訴。請等待 7 天申訴期結束；若有人提出爭議，則需先處理爭議。',
    'Resolve all open disputes before closing this bounty or refunding escrow.':
      '關閉懸賞或退回託管額度前，請先解決所有未結爭議。',
    'Bounty status summary': '懸賞狀態概覽',
    Participants: '參與人數',
    'In progress': '進行中',
    'Awaiting review': '等待審核',
    'In appeal window': '申訴期內',
    'Open disputes': '未結爭議',
    'Why closing is unavailable': '為什麼暫時無法關閉',
    'This bounty cannot be closed yet. Resolve the blockers below:':
      '此懸賞暫時無法關閉，請先處理以下事項：',
    'In progress: {{accepted}} · Awaiting review: {{submitted}}':
      '進行中：{{accepted}} · 等待審核：{{submitted}}',
    'Challenges still in the appeal window: {{count}}. Latest deadline: {{date}}.':
      '仍在申訴期內的挑戰：{{count}} 個。最晚截止時間：{{date}}。',
    'Open disputes: {{count}}. A third-party administrator must resolve them before escrow can be refunded.':
      '未結爭議：{{count}} 個。必須由第三方管理員解決後，才能退回託管額度。',
  },
  fr: {
    'Rejected challenges can still be appealed. Wait until the seven-day appeal window ends unless a dispute is opened.':
      'Les défis rejetés peuvent encore faire l’objet d’un recours. Attendez la fin du délai de sept jours, sauf si un litige est ouvert.',
    'Resolve all open disputes before closing this bounty or refunding escrow.':
      'Résolvez tous les litiges ouverts avant de clôturer cette prime ou de rembourser les fonds bloqués.',
    'Bounty status summary': 'Résumé de l’état de la prime',
    Participants: 'Participants',
    'In progress': 'En cours',
    'Awaiting review': 'En attente de validation',
    'In appeal window': 'Dans le délai de recours',
    'Open disputes': 'Litiges ouverts',
    'Why closing is unavailable': 'Pourquoi la clôture est indisponible',
    'This bounty cannot be closed yet. Resolve the blockers below:':
      'Cette prime ne peut pas encore être clôturée. Résolvez d’abord les blocages suivants :',
    'In progress: {{accepted}} · Awaiting review: {{submitted}}':
      'En cours : {{accepted}} · En attente de validation : {{submitted}}',
    'Challenges still in the appeal window: {{count}}. Latest deadline: {{date}}.':
      'Défis encore dans le délai de recours : {{count}}. Échéance la plus tardive : {{date}}.',
    'Open disputes: {{count}}. A third-party administrator must resolve them before escrow can be refunded.':
      'Litiges ouverts : {{count}}. Un administrateur tiers doit les résoudre avant le remboursement des fonds bloqués.',
  },
  ja: {
    'Rejected challenges can still be appealed. Wait until the seven-day appeal window ends unless a dispute is opened.':
      '却下されたチャレンジにはまだ異議を申し立てられます。異議が開始された場合を除き、7 日間の申立期間が終了するまでお待ちください。',
    'Resolve all open disputes before closing this bounty or refunding escrow.':
      '懸賞を終了またはエスクローを返金する前に、未解決の異議をすべて解決してください。',
    'Bounty status summary': '懸賞ステータスの概要',
    Participants: '参加者数',
    'In progress': '進行中',
    'Awaiting review': 'レビュー待ち',
    'In appeal window': '異議申立期間中',
    'Open disputes': '未解決の異議',
    'Why closing is unavailable': '終了できない理由',
    'This bounty cannot be closed yet. Resolve the blockers below:':
      'この懸賞はまだ終了できません。以下の阻害要因を解消してください：',
    'In progress: {{accepted}} · Awaiting review: {{submitted}}':
      '進行中：{{accepted}}・レビュー待ち：{{submitted}}',
    'Challenges still in the appeal window: {{count}}. Latest deadline: {{date}}.':
      '異議申立期間中のチャレンジ：{{count}}件。最も遅い期限：{{date}}。',
    'Open disputes: {{count}}. A third-party administrator must resolve them before escrow can be refunded.':
      '未解決の異議：{{count}}件。エスクローを返金するには、第三者の管理者による解決が必要です。',
  },
  ru: {
    'Rejected challenges can still be appealed. Wait until the seven-day appeal window ends unless a dispute is opened.':
      'Отклонённые заявки ещё можно обжаловать. Дождитесь окончания семидневного срока, если спор не будет открыт.',
    'Resolve all open disputes before closing this bounty or refunding escrow.':
      'Разрешите все открытые споры перед закрытием награды или возвратом средств из эскроу.',
    'Bounty status summary': 'Сводка статуса награды',
    Participants: 'Участники',
    'In progress': 'В работе',
    'Awaiting review': 'Ожидают проверки',
    'In appeal window': 'В периоде обжалования',
    'Open disputes': 'Открытые споры',
    'Why closing is unavailable': 'Почему закрытие недоступно',
    'This bounty cannot be closed yet. Resolve the blockers below:':
      'Эту награду пока нельзя закрыть. Устраните следующие препятствия:',
    'In progress: {{accepted}} · Awaiting review: {{submitted}}':
      'В работе: {{accepted}} · Ожидают проверки: {{submitted}}',
    'Challenges still in the appeal window: {{count}}. Latest deadline: {{date}}.':
      'Заявки в периоде обжалования: {{count}}. Самый поздний срок: {{date}}.',
    'Open disputes: {{count}}. A third-party administrator must resolve them before escrow can be refunded.':
      'Открытые споры: {{count}}. Сторонний администратор должен разрешить их до возврата средств из эскроу.',
  },
  vi: {
    'Rejected challenges can still be appealed. Wait until the seven-day appeal window ends unless a dispute is opened.':
      'Thử thách bị từ chối vẫn có thể được kháng nghị. Hãy chờ hết thời hạn bảy ngày, trừ khi có tranh chấp được mở.',
    'Resolve all open disputes before closing this bounty or refunding escrow.':
      'Hãy giải quyết mọi tranh chấp đang mở trước khi đóng tiền thưởng hoặc hoàn lại khoản ký quỹ.',
    'Bounty status summary': 'Tóm tắt trạng thái tiền thưởng',
    Participants: 'Người tham gia',
    'In progress': 'Đang thực hiện',
    'Awaiting review': 'Đang chờ duyệt',
    'In appeal window': 'Trong thời hạn kháng nghị',
    'Open disputes': 'Tranh chấp đang mở',
    'Why closing is unavailable': 'Lý do chưa thể đóng',
    'This bounty cannot be closed yet. Resolve the blockers below:':
      'Tiền thưởng này chưa thể đóng. Hãy xử lý các trở ngại sau:',
    'In progress: {{accepted}} · Awaiting review: {{submitted}}':
      'Đang thực hiện: {{accepted}} · Đang chờ duyệt: {{submitted}}',
    'Challenges still in the appeal window: {{count}}. Latest deadline: {{date}}.':
      'Thử thách vẫn trong thời hạn kháng nghị: {{count}}. Hạn muộn nhất: {{date}}.',
    'Open disputes: {{count}}. A third-party administrator must resolve them before escrow can be refunded.':
      'Tranh chấp đang mở: {{count}}. Quản trị viên bên thứ ba phải giải quyết chúng trước khi hoàn lại khoản ký quỹ.',
  },
}
for (const [locale, translations] of Object.entries(
  bountyLifecycleTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const deprecatedCurrencyKeys = new Set([
  'Price (local currency / USD)',
  'Use global price',
  'Use global price reciprocal',
])

async function main() {
  let totalAdded = 0
  for (const [locale, translations] of Object.entries(newKeys)) {
    const filePath = path.join(LOCALES_DIR, `${locale}.json`)
    const json = JSON.parse(await fs.readFile(filePath, 'utf8'))
    let count = 0
    for (const key of deprecatedCurrencyKeys) {
      if (Object.hasOwn(json.translation, key)) {
        delete json.translation[key]
        count++
      }
    }
    if ('Routing rules cannot exceed 16384 characters.' in json.translation) {
      delete json.translation['Routing rules cannot exceed 16384 characters.']
      count++
    }
    for (const [key, value] of Object.entries(translations)) {
      if (json.translation[key] !== value) {
        json.translation[key] = value
        count++
      }
    }
    if (count > 0) {
      json.translation = Object.fromEntries(
        Object.entries(json.translation).sort(([a], [b]) => a.localeCompare(b))
      )
      await fs.writeFile(filePath, stableStringify(json), 'utf8')
    }
    console.log(`${locale}: ${count} translations applied`)
    totalAdded += count
  }
  console.log(`Total: ${totalAdded} translations applied`)
}

main().catch((error) => {
  console.error(error)
  process.exitCode = 1
})
