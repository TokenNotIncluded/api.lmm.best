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

import { acquisitionCopy as acquisitionLinkCopy } from './acquisition-copy.mjs'
import { aiDirectoryCopy } from './ai-directory-copy.mjs'
import { apiKeySourceCopy } from './api-key-source-copy.mjs'
import { assistantSettingsCopy } from './assistant-settings-copy.mjs'
import { assistantToolCopy } from './assistant-tool-copy.mjs'
import { drawingMcpExtraCopy } from './drawing-mcp-extra-copy.mjs'
import { drawingWalletCopy } from './drawing-wallet-copy.mjs'
import { dshGuideCopy } from './dsh-guide-copy.mjs'
import { forgeRefreshCopy } from './forge-refresh-copy.mjs'
import { homeEditorialCopy } from './home-editorial-copy.mjs'
import { homeTokenCopy } from './home-token-copy.mjs'
import { passkeyCopy } from './passkey-copy.mjs'
import { paymentPricingCopy } from './payment-pricing-copy.mjs'
import { piGuideCopy } from './pi-guide-copy.mjs'
import { piOAuthCopy } from './pi-oauth-copy.mjs'
import { profileShareCopy } from './profile-share-copy.mjs'
import { remoteControlCopy } from './remote-control-copy.mjs'
import { toolMarketCopy } from './tool-market-copy.mjs'
import { waitCompanionCopy } from './wait-companion-copy.mjs'

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
    'Pricing group': 'Pricing group',
    'Cost multiplier': 'Cost multiplier',
    'Group cost multipliers': 'Group cost multipliers',
    'Understand how user groups, cost multipliers, profit pricing, and special rules work together.':
      'Understand how user groups, cost multipliers, profit pricing, and special rules work together.',
    'decides which channels are used and which base cost multiplier applies.':
      'decides which channels are used and which base cost multiplier applies.',
    'decides the top-up ratio, which groups the user can pick for tokens, and whether a cost override applies.':
      'decides the top-up ratio, which groups the user can pick for tokens, and whether a cost override applies.',
    'Find the cost multiplier.': 'Find the cost multiplier.',
    'Look for a special cost rule matching this user group and this billing group. If one exists, use its cost multiplier. Otherwise use the billing group base cost from the pricing table.':
      'Look for a special cost rule matching this user group and this billing group. If one exists, use its cost multiplier. Otherwise use the billing group base cost from the pricing table.',
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
    'Pricing group': '定价分组',
    'Cost multiplier': '成本倍率',
    'Group cost multipliers': '分组成本倍率',
    'Understand how user groups, cost multipliers, profit pricing, and special rules work together.':
      '了解用户组、成本倍率、利润定价和特殊规则如何共同生效。',
    'decides which channels are used and which base cost multiplier applies.':
      '决定使用哪些渠道以及采用哪个成本基准倍率。',
    'decides the top-up ratio, which groups the user can pick for tokens, and whether a cost override applies.':
      '决定充值倍率、用户创建令牌时可选的分组，以及是否应用成本覆盖规则。',
    'Find the cost multiplier.': '查找成本倍率。',
    'Look for a special cost rule matching this user group and this billing group. If one exists, use its cost multiplier. Otherwise use the billing group base cost from the pricing table.':
      '查找匹配用户组和计费组的特殊成本规则；若存在则使用其成本倍率，否则使用定价表中的计费组成本基准。',
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
    'Pricing group': '定價分組',
    'Cost multiplier': '成本倍率',
    'Group cost multipliers': '分組成本倍率',
    'Understand how user groups, cost multipliers, profit pricing, and special rules work together.':
      '了解使用者群組、成本倍率、利潤定價和特殊規則如何共同生效。',
    'decides which channels are used and which base cost multiplier applies.':
      '決定使用哪些渠道以及採用哪個成本基準倍率。',
    'decides the top-up ratio, which groups the user can pick for tokens, and whether a cost override applies.':
      '決定充值倍率、使用者建立 Token 時可選的分組，以及是否套用成本覆蓋規則。',
    'Find the cost multiplier.': '尋找成本倍率。',
    'Look for a special cost rule matching this user group and this billing group. If one exists, use its cost multiplier. Otherwise use the billing group base cost from the pricing table.':
      '尋找符合使用者群組和計費群組的特殊成本規則；若存在則使用其成本倍率，否則使用定價表中的計費群組成本基準。',
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
    'Pricing group': 'Groupe tarifaire',
    'Cost multiplier': 'Coefficient de coût',
    'Group cost multipliers': 'Coefficients de coût des groupes',
    'Understand how user groups, cost multipliers, profit pricing, and special rules work together.':
      'Comprenez le rôle des groupes utilisateurs, des coûts, du profit et des règles spéciales.',
    'decides which channels are used and which base cost multiplier applies.':
      'détermine les canaux utilisés et le coefficient de coût de base appliqué.',
    'decides the top-up ratio, which groups the user can pick for tokens, and whether a cost override applies.':
      'détermine le coefficient de recharge, les groupes disponibles pour les tokens et les éventuelles règles de coût.',
    'Find the cost multiplier.': 'Trouver le coefficient de coût.',
    'Look for a special cost rule matching this user group and this billing group. If one exists, use its cost multiplier. Otherwise use the billing group base cost from the pricing table.':
      'Cherchez une règle de coût correspondant au groupe utilisateur et au groupe de facturation ; sinon utilisez le coût de base du groupe tarifaire.',
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
    'Pricing group': '料金グループ',
    'Cost multiplier': 'コスト倍率',
    'Group cost multipliers': 'グループコスト倍率',
    'Understand how user groups, cost multipliers, profit pricing, and special rules work together.':
      'ユーザーグループ、コスト倍率、利益料金、特殊ルールの連携を確認します。',
    'decides which channels are used and which base cost multiplier applies.':
      '使用するチャネルと適用する基本コスト倍率を決めます。',
    'decides the top-up ratio, which groups the user can pick for tokens, and whether a cost override applies.':
      'チャージ倍率、トークンで選べるグループ、コスト上書きの有無を決めます。',
    'Find the cost multiplier.': 'コスト倍率を確認します。',
    'Look for a special cost rule matching this user group and this billing group. If one exists, use its cost multiplier. Otherwise use the billing group base cost from the pricing table.':
      'ユーザーグループと請求グループに一致する特殊コストルールを探し、なければ料金表の基本コストを使います。',
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
    'Pricing group': 'Тарифная группа',
    'Cost multiplier': 'Коэффициент затрат',
    'Group cost multipliers': 'Коэффициенты затрат групп',
    'Understand how user groups, cost multipliers, profit pricing, and special rules work together.':
      'Узнайте, как работают группы пользователей, затраты, прибыль и специальные правила.',
    'decides which channels are used and which base cost multiplier applies.':
      'определяет используемые каналы и базовый коэффициент затрат.',
    'decides the top-up ratio, which groups the user can pick for tokens, and whether a cost override applies.':
      'определяет коэффициент пополнения, доступные для токенов группы и применение переопределения затрат.',
    'Find the cost multiplier.': 'Найдите коэффициент затрат.',
    'Look for a special cost rule matching this user group and this billing group. If one exists, use its cost multiplier. Otherwise use the billing group base cost from the pricing table.':
      'Найдите специальное правило для группы пользователя и группы тарификации; иначе используйте базовую стоимость группы из таблицы.',
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
    'Pricing group': 'Nhóm định giá',
    'Cost multiplier': 'Hệ số chi phí',
    'Group cost multipliers': 'Hệ số chi phí nhóm',
    'Understand how user groups, cost multipliers, profit pricing, and special rules work together.':
      'Tìm hiểu nhóm người dùng, hệ số chi phí, lợi nhuận và quy tắc đặc biệt phối hợp như thế nào.',
    'decides which channels are used and which base cost multiplier applies.':
      'quyết định kênh được dùng và hệ số chi phí cơ bản áp dụng.',
    'decides the top-up ratio, which groups the user can pick for tokens, and whether a cost override applies.':
      'quyết định hệ số nạp, nhóm người dùng có thể chọn cho token và việc áp dụng ghi đè chi phí.',
    'Find the cost multiplier.': 'Tìm hệ số chi phí.',
    'Look for a special cost rule matching this user group and this billing group. If one exists, use its cost multiplier. Otherwise use the billing group base cost from the pricing table.':
      'Tìm quy tắc chi phí khớp nhóm người dùng và nhóm tính phí; nếu không có thì dùng chi phí cơ bản trong bảng định giá.',
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

const externalNavigationTranslations = {
  en: {
    'Unable to open CC Switch': 'Unable to open CC Switch',
    'Unable to open link': 'Unable to open link',
    'Unable to open email app': 'Unable to open email app',
    'Unable to open email app. Copy the request and email it to {{email}}.':
      'Unable to open email app. Copy the request and email it to {{email}}.',
  },
  zh: {
    'Unable to open CC Switch': '无法打开 CC Switch',
    'Unable to open link': '无法打开链接',
    'Unable to open email app': '无法打开邮件应用',
    'Unable to open email app. Copy the request and email it to {{email}}.':
      '无法打开邮件应用。请复制请求并发送至 {{email}}。',
  },
  'zh-TW': {
    'Unable to open CC Switch': '無法開啟 CC Switch',
    'Unable to open link': '無法開啟連結',
    'Unable to open email app': '無法開啟郵件應用程式',
    'Unable to open email app. Copy the request and email it to {{email}}.':
      '無法開啟郵件應用程式。請複製請求並傳送至 {{email}}。',
  },
  fr: {
    'Unable to open CC Switch': "Impossible d'ouvrir CC Switch",
    'Unable to open link': "Impossible d'ouvrir le lien",
    'Unable to open email app':
      "Impossible d'ouvrir l'application de messagerie",
    'Unable to open email app. Copy the request and email it to {{email}}.':
      "Impossible d'ouvrir l'application de messagerie. Copiez la demande et envoyez-la à {{email}}.",
  },
  ja: {
    'Unable to open CC Switch': 'CC Switchを開けません',
    'Unable to open link': 'リンクを開けません',
    'Unable to open email app': 'メールアプリを開けません',
    'Unable to open email app. Copy the request and email it to {{email}}.':
      'メールアプリを開けません。リクエストをコピーして {{email}} 宛てに送信してください。',
  },
  ru: {
    'Unable to open CC Switch': 'Не удалось открыть CC Switch',
    'Unable to open link': 'Не удалось открыть ссылку',
    'Unable to open email app': 'Не удалось открыть почтовое приложение',
    'Unable to open email app. Copy the request and email it to {{email}}.':
      'Не удалось открыть почтовое приложение. Скопируйте запрос и отправьте его на адрес {{email}}.',
  },
  vi: {
    'Unable to open CC Switch': 'Không thể mở CC Switch',
    'Unable to open link': 'Không thể mở liên kết',
    'Unable to open email app': 'Không thể mở ứng dụng email',
    'Unable to open email app. Copy the request and email it to {{email}}.':
      'Không thể mở ứng dụng email. Hãy sao chép yêu cầu và gửi đến {{email}}.',
  },
}
for (const [locale, translations] of Object.entries(
  externalNavigationTranslations
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

const bountyDescriptionTranslations = {
  en: {
    'Expand description': 'Expand description',
    'Collapse description': 'Collapse description',
  },
  zh: {
    'Expand description': '展开简介',
    'Collapse description': '收起简介',
  },
  'zh-TW': {
    'Expand description': '展開簡介',
    'Collapse description': '收起簡介',
  },
  fr: {
    'Expand description': 'Afficher la description complète',
    'Collapse description': 'Réduire la description',
  },
  ja: {
    'Expand description': '説明を展開',
    'Collapse description': '説明を折りたたむ',
  },
  ru: {
    'Expand description': 'Развернуть описание',
    'Collapse description': 'Свернуть описание',
  },
  vi: {
    'Expand description': 'Mở rộng mô tả',
    'Collapse description': 'Thu gọn mô tả',
  },
}
for (const [locale, translations] of Object.entries(
  bountyDescriptionTranslations
)) {
  Object.assign(newKeys[locale], translations)
}

const roundStatusTranslations = {
  en: {
    'All models': 'All models',
    'All providers': 'All providers',
    'Click a bar to filter logs to that time window.':
      'Click a bar to filter logs to that time window.',
    'Data coverage: {{reported}} of {{total}} models reported.':
      'Data coverage: {{reported}} of {{total}} models reported.',
    'Performance data may be delayed. The latest sample is more than 6 hours old.':
      'Performance data may be delayed. The latest sample is more than 6 hours old.',
    'Last 3 days': 'Last 3 days',
    'Last 7 days': 'Last 7 days',
    'Last 30 days': 'Last 30 days',
    'Latest data: {{time}}': 'Latest data: {{time}}',
    'Performance window: last {{hours}} hours':
      'Performance window: last {{hours}} hours',
    'Unable to load usage trend': 'Unable to load usage trend',
    'No usage trend data in this range': 'No usage trend data in this range',
    'Loading performance data': 'Loading performance data',
    'Performance data unavailable': 'Performance data unavailable',
    'Models are still available without live performance data.':
      'Models are still available without live performance data.',
    'Usage trend for the selected time range':
      'Usage trend for the selected time range',
    'The new activation is open in the details panel.':
      'The new activation is open in the details panel.',
    'Your purchase is being submitted. Do not submit another order.':
      'Your purchase is being submitted. Do not submit another order.',
    'Your reorder is being submitted. Do not submit another order.':
      'Your reorder is being submitted. Do not submit another order.',
  },
  zh: {
    'All models': '全部模型',
    'All providers': '全部供应商',
    'Click a bar to filter logs to that time window.':
      '点击柱状图可将日志筛选到对应时间段。',
    'Data coverage: {{reported}} of {{total}} models reported.':
      '数据覆盖：{{reported}} / {{total}} 个模型已上报。',
    'Performance data may be delayed. The latest sample is more than 6 hours old.':
      '性能数据可能存在延迟，最新样本已超过 6 小时。',
    'Last 3 days': '最近 3 天',
    'Last 7 days': '最近 7 天',
    'Last 30 days': '最近 30 天',
    'Latest data: {{time}}': '最新数据：{{time}}',
    'Performance window: last {{hours}} hours':
      '性能统计窗口：最近 {{hours}} 小时',
    'Unable to load usage trend': '无法加载用量趋势',
    'No usage trend data in this range': '该时间范围内暂无用量趋势数据',
    'Loading performance data': '正在加载性能数据',
    'Performance data unavailable': '性能数据暂不可用',
    'Models are still available without live performance data.':
      '没有实时性能数据，但模型仍可使用。',
    'Usage trend for the selected time range': '所选时间范围的用量趋势',
    'The new activation is open in the details panel.':
      '新的激活记录已在详情面板中打开。',
    'Your purchase is being submitted. Do not submit another order.':
      '正在提交购买请求，请勿重复提交。',
    'Your reorder is being submitted. Do not submit another order.':
      '正在提交重新购买请求，请勿重复提交。',
  },
  'zh-TW': {
    'All models': '所有模型',
    'All providers': '所有供應商',
    'Click a bar to filter logs to that time window.':
      '點選柱狀圖可將日誌篩選到對應時間範圍。',
    'Data coverage: {{reported}} of {{total}} models reported.':
      '資料涵蓋：{{reported}} / {{total}} 個模型已回報。',
    'Performance data may be delayed. The latest sample is more than 6 hours old.':
      '效能資料可能有延遲，最新樣本已超過 6 小時。',
    'Last 3 days': '最近 3 天',
    'Last 7 days': '最近 7 天',
    'Last 30 days': '最近 30 天',
    'Latest data: {{time}}': '最新資料：{{time}}',
    'Performance window: last {{hours}} hours':
      '效能統計視窗：最近 {{hours}} 小時',
    'Unable to load usage trend': '無法載入用量趨勢',
    'No usage trend data in this range': '此時間範圍內沒有用量趨勢資料',
    'Loading performance data': '正在載入效能資料',
    'Performance data unavailable': '效能資料暫不可用',
    'Models are still available without live performance data.':
      '沒有即時效能資料，但模型仍可使用。',
    'Usage trend for the selected time range': '所選時間範圍的用量趨勢',
    'The new activation is open in the details panel.':
      '新的啟用記錄已在詳細資料面板中開啟。',
    'Your purchase is being submitted. Do not submit another order.':
      '正在提交購買要求，請勿重複提交。',
    'Your reorder is being submitted. Do not submit another order.':
      '正在提交重新購買要求，請勿重複提交。',
  },
  fr: {
    'All models': 'Tous les modèles',
    'All providers': 'Tous les fournisseurs',
    'Click a bar to filter logs to that time window.':
      'Cliquez sur une barre pour filtrer les journaux sur cette période.',
    'Data coverage: {{reported}} of {{total}} models reported.':
      'Couverture : {{reported}} modèles signalés sur {{total}}.',
    'Performance data may be delayed. The latest sample is more than 6 hours old.':
      'Les données de performance peuvent être retardées ; le dernier échantillon date de plus de 6 heures.',
    'Last 3 days': '3 derniers jours',
    'Last 7 days': '7 derniers jours',
    'Last 30 days': '30 derniers jours',
    'Latest data: {{time}}': 'Dernières données : {{time}}',
    'Performance window: last {{hours}} hours':
      'Fenêtre de performance : {{hours}} dernières heures',
    'Unable to load usage trend':
      'Impossible de charger la tendance d’utilisation',
    'No usage trend data in this range':
      'Aucune donnée de tendance d’utilisation sur cette période',
    'Loading performance data': 'Chargement des données de performance',
    'Performance data unavailable': 'Données de performance indisponibles',
    'Models are still available without live performance data.':
      'Les modèles restent disponibles sans données de performance en direct.',
    'Usage trend for the selected time range':
      'Tendance d’utilisation pour la période sélectionnée',
    'The new activation is open in the details panel.':
      'La nouvelle activation est ouverte dans le panneau de détails.',
    'Your purchase is being submitted. Do not submit another order.':
      'Votre achat est en cours d’envoi. Ne soumettez pas une autre commande.',
    'Your reorder is being submitted. Do not submit another order.':
      'Votre nouvelle commande est en cours d’envoi. Ne soumettez pas une autre commande.',
  },
  ja: {
    'All models': 'すべてのモデル',
    'All providers': 'すべてのプロバイダー',
    'Click a bar to filter logs to that time window.':
      '棒をクリックすると、その時間帯でログを絞り込めます。',
    'Data coverage: {{reported}} of {{total}} models reported.':
      'データ範囲：{{total}} モデル中 {{reported}} モデルが報告済み',
    'Performance data may be delayed. The latest sample is more than 6 hours old.':
      'パフォーマンスデータが遅延している可能性があります。最新サンプルは 6 時間以上前のものです。',
    'Last 3 days': '過去 3 日間',
    'Last 7 days': '過去 7 日間',
    'Last 30 days': '過去 30 日間',
    'Latest data: {{time}}': '最新データ：{{time}}',
    'Performance window: last {{hours}} hours':
      'パフォーマンス集計期間：過去 {{hours}} 時間',
    'Unable to load usage trend': '使用量の推移を読み込めません',
    'No usage trend data in this range':
      'この期間の使用量推移データはありません',
    'Loading performance data': 'パフォーマンスデータを読み込んでいます',
    'Performance data unavailable': 'パフォーマンスデータを利用できません',
    'Models are still available without live performance data.':
      'ライブのパフォーマンスデータがなくてもモデルは利用できます。',
    'Usage trend for the selected time range': '選択期間の使用量推移',
    'The new activation is open in the details panel.':
      '新しいアクティベーションを詳細パネルで開きました。',
    'Your purchase is being submitted. Do not submit another order.':
      '購入を送信しています。重複して注文しないでください。',
    'Your reorder is being submitted. Do not submit another order.':
      '再注文を送信しています。重複して注文しないでください。',
  },
  ru: {
    'All models': 'Все модели',
    'All providers': 'Все поставщики',
    'Click a bar to filter logs to that time window.':
      'Нажмите на столбец, чтобы отфильтровать логи по этому интервалу.',
    'Data coverage: {{reported}} of {{total}} models reported.':
      'Охват данных: отчёты получены для {{reported}} из {{total}} моделей.',
    'Performance data may be delayed. The latest sample is more than 6 hours old.':
      'Данные о производительности могут поступать с задержкой: последний образец старше 6 часов.',
    'Last 3 days': 'Последние 3 дня',
    'Last 7 days': 'Последние 7 дней',
    'Last 30 days': 'Последние 30 дней',
    'Latest data: {{time}}': 'Последние данные: {{time}}',
    'Performance window: last {{hours}} hours':
      'Окно производительности: последние {{hours}} ч.',
    'Unable to load usage trend': 'Не удалось загрузить динамику использования',
    'No usage trend data in this range':
      'За этот период нет данных о динамике использования',
    'Loading performance data': 'Загрузка данных о производительности',
    'Performance data unavailable': 'Данные о производительности недоступны',
    'Models are still available without live performance data.':
      'Модели доступны даже без актуальных данных о производительности.',
    'Usage trend for the selected time range':
      'Динамика использования за выбранный период',
    'The new activation is open in the details panel.':
      'Новая активация открыта на панели подробностей.',
    'Your purchase is being submitted. Do not submit another order.':
      'Покупка отправляется. Не отправляйте ещё один заказ.',
    'Your reorder is being submitted. Do not submit another order.':
      'Повторная покупка отправляется. Не отправляйте ещё один заказ.',
  },
  vi: {
    'All models': 'Tất cả mô hình',
    'All providers': 'Tất cả nhà cung cấp',
    'Click a bar to filter logs to that time window.':
      'Nhấp vào cột để lọc nhật ký theo khoảng thời gian đó.',
    'Data coverage: {{reported}} of {{total}} models reported.':
      'Phạm vi dữ liệu: {{reported}}/{{total}} mô hình đã báo cáo.',
    'Performance data may be delayed. The latest sample is more than 6 hours old.':
      'Dữ liệu hiệu năng có thể bị trễ; mẫu mới nhất đã cũ hơn 6 giờ.',
    'Last 3 days': '3 ngày qua',
    'Last 7 days': '7 ngày qua',
    'Last 30 days': '30 ngày qua',
    'Latest data: {{time}}': 'Dữ liệu mới nhất: {{time}}',
    'Performance window: last {{hours}} hours':
      'Khoảng hiệu năng: {{hours}} giờ qua',
    'Unable to load usage trend': 'Không thể tải xu hướng sử dụng',
    'No usage trend data in this range':
      'Không có dữ liệu xu hướng sử dụng trong khoảng này',
    'Loading performance data': 'Đang tải dữ liệu hiệu năng',
    'Performance data unavailable': 'Dữ liệu hiệu năng không khả dụng',
    'Models are still available without live performance data.':
      'Mô hình vẫn khả dụng khi không có dữ liệu hiệu năng trực tiếp.',
    'Usage trend for the selected time range':
      'Xu hướng sử dụng trong khoảng thời gian đã chọn',
    'The new activation is open in the details panel.':
      'Kích hoạt mới đã mở trong bảng chi tiết.',
    'Your purchase is being submitted. Do not submit another order.':
      'Đang gửi yêu cầu mua. Không gửi thêm đơn khác.',
    'Your reorder is being submitted. Do not submit another order.':
      'Đang gửi yêu cầu mua lại. Không gửi thêm đơn khác.',
  },
}
for (const [locale, translations] of Object.entries(roundStatusTranslations)) {
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

const uiDocTranslationFixes = {
  zh: {
    'Choose any start and end time within one year.':
      '你可以选择任意起始和结束时间，但时间跨度不能超过一年。',
    'Common user': '普通用户',
    'Two-factor authentication': '两因素认证',
    'View related page': '查看相关页面',
    'L{{level}}': '等级{{level}}',
  },
  'zh-TW': {
    'Choose any start and end time within one year.':
      '你可以選擇任意起始和結束時間，但時間跨度不能超過一年。',
    'Common user': '一般使用者',
    'Two-factor authentication': '兩因素認證',
    'View related page': '查看相關頁面',
    'Channel routing': '渠道路由',
    'Configure your own public pool. It affects only your requests; administrator routing priority remains unchanged.':
      '配置你自己的公開頻道池，只會影響你的請求，不會改變管理員設置的全局路由優先級。',
    'Custom tip amount': '自訂金額',
    Disabled: '已停用',
    Enabled: '已啟用',
    'Group warning': '群組警告',
    'I understand, continue': '我明白，繼續',
    'L{{level}}': 'L{{level}}',
    'Leave a short thank-you message': '寫一則簡短感謝訊息',
    'Message (optional)': '訊息（可選）',
    'Most models': '模型數最多',
    'Move available tips into your balance. Choose the group you want to use for future requests.':
      '把可用的小費移到你的餘額，並選擇你之後想用的群組。',
    'Move down': '下移',
    'Move up': '上移',
    'No linked public channels yet.': '尚未連結任何公開頻道。',
    'No models listed': '未列出模型',
    'No reviews yet.': '目前尚無評論',
    Rating: '評分',
    'Recent comments': '近期留言',
    'Recently updated': '最近更新',
    'Review channel': '評價頻道',
    'Review submitted': '已送出評價',
    'Routing preferences saved': '路由偏好已保存',
    'Save routing': '保存路由',
    'Select a group': '選擇群組',
    'Send tip': '送出小費',
    'Submit review': '提交評價',
    'Target group': '目標群組',
    'Tip amount': '小費金額',
    'Tip contributor': '打賞分享者',
    'Tip sent': '打賞已送出',
    Tips: '小費',
    'Tips withdrawn': '打賞已提取',
    'Top rated': '評分最高',
    'Use your balance to thank this contributor. Tips are transferred immediately and cannot be reversed.':
      '使用你的餘額感謝該貢獻者。小費會立即轉帳且無法撤回。',
    Withdraw: '提現',
    'Withdraw tips': '提取小費',
    'Write a comment (optional)': '填寫評論（可選）',
    'Confirmation {{current}} of {{total}}': '第 {{current}}/{{total}} 次確認',
  },
  fr: {
    'Channel routing': 'Routage du canal',
    'Configure your own public pool. It affects only your requests; administrator routing priority remains unchanged.':
      'Configurez votre propre pool public. Cela n’affecte que vos requêtes ; la priorité de routage définie par l’administrateur reste inchangée.',
    'Common user': 'Utilisateur standard',
    'Custom tip amount': 'Montant du pourboire personnalisé',
    Disabled: 'Désactivé',
    Enabled: 'Activé',
    'Group warning': 'Avertissement de groupe',
    'I understand, continue': 'Je comprends, continuer',
    'L{{level}}': 'Niveau {{level}}',
    'Leave a short thank-you message':
      'Laissez un court message de remerciement',
    'Most models': 'Les modèles principaux',
    'Choose any start and end time within one year.':
      "Vous pouvez choisir n'importe quelle date de début et de fin, dans une période maximale d'un an.",
    'Message (optional)': 'Message (facultatif)',
    'Move available tips into your balance. Choose the group you want to use for future requests.':
      'Transférez les pourboires disponibles vers votre solde. Choisissez le groupe à utiliser pour vos prochaines requêtes.',
    'Move down': 'Descendre',
    'Move up': 'Monter',
    'No linked public channels yet.':
      'Aucun canal public n’est encore associé.',
    'No models listed': 'Aucun modèle répertorié',
    'No reviews yet.': 'Aucune évaluation pour l’instant.',
    Rating: 'Note',
    'Recent comments': 'Commentaires récents',
    'Recently updated': 'Récemment mis à jour',
    'Review channel': 'Revoir le canal',
    'Review submitted': 'Avis envoyé',
    'Routing preferences saved': 'Préférences de routage enregistrées',
    'Save routing': 'Enregistrer le routage',
    'Select a group': 'Sélectionner un groupe',
    'Send tip': 'Envoyer un pourboire',
    'Submit review': 'Soumettre l’avis',
    'Target group': 'Groupe cible',
    'Tip amount': 'Montant du pourboire',
    'Tip contributor': 'Destinataire du pourboire',
    'Tip sent': 'Pourboire envoyé',
    Tips: 'Pourboires',
    'Tips withdrawn': 'Pourboires retirés',
    'Top rated': 'Le mieux noté',
    'Two-factor authentication': 'Authentification à deux facteurs',
    'Use your balance to thank this contributor. Tips are transferred immediately and cannot be reversed.':
      'Utilisez votre solde pour remercier ce contributeur. Les pourboires sont transférés immédiatement et ne peuvent pas être annulés.',
    'View related page': 'Voir la page liée',
    Withdraw: 'Retirer',
    'Withdraw tips': 'Retirer les pourboires',
    'Write a comment (optional)': 'Écrire un commentaire (facultatif)',
    'Confirmation {{current}} of {{total}}':
      'Confirmation {{current}} / {{total}}',
  },
  ja: {
    'Channel routing': 'チャネルルーティング',
    'Choose any start and end time within one year.':
      'この期間は開始日と終了日を1年以内で自由に選択できます。',
    'Common user': '一般ユーザー',
    'Configure your own public pool. It affects only your requests; administrator routing priority remains unchanged.':
      '自分の公開プールを設定します。これはあなたのリクエストのみに影響し、管理者のルーティング優先度は変更されません。',
    'Confirmation {{current}} of {{total}}': '確認 {{current}} / {{total}}',
    'Custom tip amount': 'カスタムチップ金額',
    Disabled: '無効',
    Enabled: '有効',
    'Group warning': 'グループ警告',
    'I understand, continue': '理解しました、続行',
    'L{{level}}': 'レベル{{level}}',
    'Leave a short thank-you message': '短いお礼メッセージを残す',
    'Message (optional)': 'メッセージ（任意）',
    'Most models': '主要モデル',
    'Move available tips into your balance. Choose the group you want to use for future requests.':
      '利用可能なチップを残高に移動します。今後のリクエストで使用するグループを選択してください。',
    'Move down': '下へ移動',
    'Move up': '上へ移動',
    'No linked public channels yet.':
      'まだ公開チャンネルはリンクされていません。',
    'No models listed': 'モデルが登録されていません',
    'No reviews yet.': 'まだレビューがありません。',
    Rating: '評価',
    'Recent comments': '最近のコメント',
    'Recently updated': '最近の更新',
    'Review channel': 'チャネルをレビュー',
    'Review submitted': 'レビューが送信されました',
    'Routing preferences saved': 'ルーティング設定を保存しました',
    'Save routing': 'ルーティングを保存',
    'Select a group': 'グループを選択',
    'Send tip': 'チップを送信',
    'Submit review': 'レビューを送信',
    'Target group': '対象グループ',
    'Tip amount': 'チップ金額',
    'Tip contributor': 'チップ受け取り先',
    'Tip sent': 'チップが送信されました',
    'Tips withdrawn': 'チップを引き出しました',
    'Top rated': '高評価',
    'Two-factor authentication': '二要素認証',
    'Use your balance to thank this contributor. Tips are transferred immediately and cannot be reversed.':
      '残高を使ってこの貢献者に感謝してください。チップは即時に移転され、取り消すことはできません。',
    'View related page': '関連ページを表示',
    Withdraw: '引き出す',
    'Withdraw tips': 'チップを引き出す',
    'Write a comment (optional)': 'コメントを書く（任意）',
  },
  ru: {
    'Channel routing': 'Маршрутизация каналов',
    'Choose any start and end time within one year.':
      'Вы можете выбрать любые даты начала и окончания, но интервал не должен превышать одного года.',
    'Common user': 'Обычный пользователь',
    'Configure your own public pool. It affects only your requests; administrator routing priority remains unchanged.':
      'Настройте собственный публичный пул. Это влияет только на ваши запросы; приоритет маршрутизации администратора остается неизменным.',
    'Confirmation {{current}} of {{total}}':
      'Подтверждение {{current}} из {{total}}',
    'Custom tip amount': 'Произвольная сумма чаевых',
    Disabled: 'Отключено',
    Enabled: 'Включено',
    'Group warning': 'Предупреждение группы',
    'I understand, continue': 'Я понимаю, продолжаю',
    'L{{level}}': 'Уровень {{level}}',
    'Leave a short thank-you message':
      'Оставьте короткое сообщение с благодарностью',
    'Message (optional)': 'Сообщение (необязательно)',
    'Most models': 'Лучшие модели',
    'Move available tips into your balance. Choose the group you want to use for future requests.':
      'Перенесите доступные чаевые на ваш баланс. Выберите группу для будущих запросов.',
    'Move down': 'Переместить вниз',
    'Move up': 'Переместить вверх',
    'MCP (Streamable HTTP)': 'MCP (потоковый HTTP)',
    'No linked public channels yet.': 'Публичные каналы пока не подключены.',
    'No models listed': 'Модели не указаны.',
    'No reviews yet.': 'Отзывов пока нет.',
    Rating: 'Рейтинг',
    'Recent comments': 'Последние комментарии',
    'Recently updated': 'Недавно обновлено',
    'Review channel': 'Проверить канал',
    'Review submitted': 'Отзыв отправлен',
    'Routing preferences saved': 'Настройки маршрутизации сохранены',
    'Save routing': 'Сохранить маршрутизацию',
    'Select a group': 'Выберите группу',
    'Send tip': 'Отправить чаевые',
    'Submit review': 'Отправить отзыв',
    'Target group': 'Целевая группа',
    'Tip amount': 'Сумма чаевых',
    'Tip contributor': 'Получатель чаевых',
    'Tip sent': 'Чаевые отправлены',
    'Tips withdrawn': 'Чаевые отозваны',
    'Top rated': 'В топе рейтинга',
    'Two-factor authentication': 'Двухфакторная аутентификация',
    'Use your balance to thank this contributor. Tips are transferred immediately and cannot be reversed.':
      'Используйте свой баланс, чтобы поблагодарить этого участника. Чаевые перечисляются сразу и не могут быть отменены.',
    'View related page': 'Посмотреть связанную страницу',
    Withdraw: 'Вывести',
    'Withdraw tips': 'Вывести чаевые',
    'Write a comment (optional)': 'Написать комментарий (необязательно)',
  },
  vi: {
    'Channel routing': 'Định tuyến kênh',
    'Common user': 'Người dùng thường',
    'Configure your own public pool. It affects only your requests; administrator routing priority remains unchanged.':
      'Cấu hình nhóm công khai của riêng bạn. Điều này chỉ ảnh hưởng đến các yêu cầu của bạn; thứ tự định tuyến do quản trị viên đặt vẫn không thay đổi.',
    'Custom tip amount': 'Số tiền tip tùy chỉnh',
    Disabled: 'Đã tắt',
    Enabled: 'Đã bật',
    'Group warning': 'Cảnh báo nhóm',
    'I understand, continue': 'Tôi hiểu, tiếp tục',
    'L{{level}}': 'Mức {{level}}',
    'Leave a short thank-you message': 'Để lại một lời cảm ơn ngắn',
    'Most models': 'Các mô hình phổ biến nhất',
    'Choose any start and end time within one year.':
      'Bạn có thể chọn bất kỳ thời điểm bắt đầu và kết thúc nào, nhưng thời gian không vượt quá một năm.',
    'Message (optional)': 'Tin nhắn (không bắt buộc)',
    'Move available tips into your balance. Choose the group you want to use for future requests.':
      'Chuyển tiền tip có sẵn vào số dư. Chọn nhóm bạn muốn dùng cho các yêu cầu tương lai.',
    'Move down': 'Di chuyển xuống',
    'Move up': 'Di chuyển lên',
    'No linked public channels yet.':
      'Chưa có kênh công khai nào được liên kết.',
    'No models listed': 'Chưa liệt kê mô hình nào',
    'No reviews yet.': 'Chưa có đánh giá.',
    Rating: 'Đánh giá',
    'Recent comments': 'Bình luận gần đây',
    'Recently updated': 'Cập nhật gần đây',
    'Review channel': 'Đánh giá kênh',
    'Review submitted': 'Đã gửi đánh giá',
    'Routing preferences saved': 'Đã lưu tùy chọn định tuyến',
    'Save routing': 'Lưu định tuyến',
    'Select a group': 'Chọn một nhóm',
    'Send tip': 'Gửi tip',
    'Submit review': 'Gửi đánh giá',
    'Target group': 'Nhóm mục tiêu',
    'Tip amount': 'Số tiền tip',
    'Tip contributor': 'Người nhận tip',
    'Tip sent': 'Đã gửi tip',
    Tips: 'Tip',
    'Tips withdrawn': 'Tip đã rút',
    'Top rated': 'Được đánh giá cao',
    'Two-factor authentication': 'Xác thực hai yếu tố',
    'Use your balance to thank this contributor. Tips are transferred immediately and cannot be reversed.':
      'Dùng số dư của bạn để cảm ơn người đóng góp này. Tiền tip được chuyển ngay và không thể hoàn lại.',
    'View related page': 'Xem trang liên quan',
    Withdraw: 'Rút',
    'Withdraw tips': 'Rút tip',
    'Write a comment (optional)': 'Viết bình luận (không bắt buộc)',
    'Confirmation {{current}} of {{total}}': 'Xác nhận {{current}} / {{total}}',
  },
}

for (const [locale, translations] of Object.entries(uiDocTranslationFixes)) {
  Object.assign(newKeys[locale], translations)
}

const targetedUiTranslationFixes = {
  fr: {
    End: 'Fin',
    Pay: 'Payer',
    Root: 'Racine',
    Query: 'Requête',
    Prompt: 'Invite',
    Quota: 'Quota',
    'Quota:': 'Quota :',
  },
  ru: {
    End: 'Завершить',
    Pay: 'Оплатить',
    Tips: 'Чаевые',
  },
  vi: {
    End: 'Kết thúc',
    Pay: 'Thanh toán',
    Add: 'Thêm',
    Breadcrumb: 'Đường dẫn',
    Prompt: 'Lời nhắc',
    Tips: 'Tiền boa',
    'API URL': 'URL API',
    'Webhook URL (Production):': 'URL webhook (sản xuất):',
    'Webhook URL (Test):': 'URL webhook (kiểm thử):',
    'N/A': 'Không áp dụng',
    Query: 'Truy vấn',
    and: 'và',
    'of 3:': 'trong 3:',
    of: 'trong',
    Asc: 'Tăng dần',
    Seed: 'Hạt giống',
    Slug: 'Định danh',
    Tag: 'Nhãn',
    off: 'giảm',
  },
  ja: {
    Pay: '支払い',
    Tips: 'チップ',
  },
  'zh-TW': {
    Pay: '付款',
    Query: '查詢',
    'Quota:': '配額:',
    Root: '根目錄',
  },
  zh: {
    Pay: '支付',
    Query: '查询',
    'Quota:': '配额:',
    Root: '根',
  },
}

const walletUiFixes = {
  en: {
    'Validating discount...': 'Validating discount...',
    'Unable to calculate payment quote. Please retry.':
      'Unable to calculate payment quote. Please retry.',
    'Payment quote unavailable': 'Payment quote unavailable',
  },
  zh: {
    'Validating discount...': '正在验证优惠...',
    'Unable to calculate payment quote. Please retry.':
      '暂时无法获取报价，尚未发起付款。请重试。',
    'Payment quote unavailable': '暂时无法获取付款报价',
  },
  'zh-TW': {
    'Validating discount...': '正在驗證優惠...',
    'Unable to calculate payment quote. Please retry.':
      '暫時無法獲取報價，尚未發起付款。請重試。',
    'Payment quote unavailable': '暫時無法獲取付款報價',
  },
  fr: {
    'Validating discount...': 'Validation de la réduction...',
    'Unable to calculate payment quote. Please retry.':
      'Impossible de calculer le devis de paiement. Veuillez réessayer.',
    'Payment quote unavailable': 'Devis de paiement indisponible',
  },
  ja: {
    'Validating discount...': '割引を確認中...',
    'Unable to calculate payment quote. Please retry.':
      '支払い見積もりを計算できません。もう一度お試しください。',
    'Payment quote unavailable': '支払い見積もりを利用できません',
  },
  ru: {
    'Validating discount...': 'Проверка скидки...',
    'Unable to calculate payment quote. Please retry.':
      'Не удалось рассчитать стоимость платежа. Повторите попытку.',
    'Payment quote unavailable': 'Стоимость платежа недоступна',
  },
  vi: {
    'Validating discount...': 'Đang xác thực giảm giá...',
    'Unable to calculate payment quote. Please retry.':
      'Không thể tính báo giá thanh toán. Vui lòng thử lại.',
    'Payment quote unavailable': 'Báo giá thanh toán không khả dụng',
  },
}

for (const [locale, translations] of Object.entries(
  targetedUiTranslationFixes
)) {
  Object.assign(newKeys[locale], translations)
}

for (const [locale, translations] of Object.entries(walletUiFixes)) {
  Object.assign(newKeys[locale], translations)
}

const drawingExperienceFixes = {
  en: {
    'Stop waiting': 'Stop waiting',
    'Stopped waiting. If the server is already processing, the generated image may appear in your history later.':
      'Stopped waiting. If the server is already processing, the generated image may appear in your history later.',
    'Request submitted · Waiting {{seconds}}s':
      'Request submitted · Waiting {{seconds}}s',
    'Waiting for image generation result...':
      'Waiting for image generation result...',
    'You can continue waiting, or stop waiting. Results will also be saved to history.':
      'You can continue waiting, or stop waiting. Results will also be saved to history.',
    'Play while waiting': 'Play while waiting',
    'Play dot-matrix snake while waiting':
      'Play dot-matrix snake while waiting',
    'Collapse minigame': 'Collapse minigame',
    'Expand minigame': 'Expand minigame',
    'Score: {{score}}': 'Score: {{score}}',
    'High score: {{score}}': 'High score: {{score}}',
    'Swipe or use arrow keys to control': 'Swipe or use arrow keys to control',
    'Generation settings': 'Generation settings',
    'Copy error details': 'Copy error details',
    'Error details copied': 'Error details copied',
    'The drawing workbench encountered an error, but your draft has been saved.':
      'The drawing workbench encountered an error, but your draft has been saved.',
    'Recover workbench': 'Recover workbench',
    'More tools': 'More tools',
    'Clear image history?': 'Clear image history?',
  },
  zh: {
    'Stop waiting': '停止等待',
    'Stopped waiting. If the server is already processing, the generated image may appear in your history later.':
      '已停止等待。如果服务端已在处理，生成的图片可能稍后会在历史记录中显示。',
    'Request submitted · Waiting {{seconds}}s':
      '请求已提交 · 已等待 {{seconds}} 秒',
    'Waiting for image generation result...': '正在等待图片生成结果...',
    'You can continue waiting, or stop waiting. Results will also be saved to history.':
      '您可以继续等待或停止等待。生成成功后也会自动保存到历史记录。',
    'Play while waiting': '等待时玩一下',
    'Play dot-matrix snake while waiting': '等待时玩点阵贪吃蛇',
    'Collapse minigame': '收起小游戏',
    'Expand minigame': '展开小游戏',
    'Score: {{score}}': '得分：{{score}}',
    'High score: {{score}}': '最高分：{{score}}',
    'Swipe or use arrow keys to control': '滑动或使用方向键控制',
    'Generation settings': '生成设置',
    'Copy error details': '复制错误详情',
    'Error details copied': '错误详情已复制',
    'The drawing workbench encountered an error, but your draft has been saved.':
      '绘画工作台遇到问题，但您的草稿已保存。',
    'Recover workbench': '恢复工作台',
    'More tools': '更多工具',
    'Clear image history?': '清空绘画历史？',
  },
  'zh-TW': {
    'Stop waiting': '停止等待',
    'Stopped waiting. If the server is already processing, the generated image may appear in your history later.':
      '已停止等待。如果伺服器已在處理，產生的圖片可能稍後會在歷史記錄中顯示。',
    'Request submitted · Waiting {{seconds}}s':
      '請求已提交 · 已等待 {{seconds}} 秒',
    'Waiting for image generation result...': '正在等待圖片生成結果...',
    'You can continue waiting, or stop waiting. Results will also be saved to history.':
      '您可以繼續等待或停止等待。生成成功後也會自動保存到歷史記錄。',
    'Play while waiting': '等待時玩一下',
    'Play dot-matrix snake while waiting': '等待時玩點陣貪食蛇',
    'Collapse minigame': '收起小遊戲',
    'Expand minigame': '展開小遊戲',
    'Score: {{score}}': '得分：{{score}}',
    'High score: {{score}}': '最高分：{{score}}',
    'Swipe or use arrow keys to control': '滑動或使用方向鍵控制',
    'Generation settings': '生成設定',
    'Copy error details': '複製錯誤詳情',
    'Error details copied': '錯誤詳情已複製',
    'The drawing workbench encountered an error, but your draft has been saved.':
      '繪畫工作台遇到問題，但您的草稿已儲存。',
    'Recover workbench': '恢復工作台',
    'More tools': '更多工具',
    'Clear image history?': '清空繪畫歷史？',
  },
  fr: {
    'Stop waiting': "Arrêter l'attente",
    'Stopped waiting. If the server is already processing, the generated image may appear in your history later.':
      "Attente arrêtée. Si le serveur traite déjà la requête, l'image générée pourra apparaître ultérieurement dans votre historique.",
    'Request submitted · Waiting {{seconds}}s':
      'Demande envoyée · En attente depuis {{seconds}} s',
    'Waiting for image generation result...':
      "En attente du résultat de la génération d'image...",
    'You can continue waiting, or stop waiting. Results will also be saved to history.':
      "Vous pouvez continuer d'attendre ou arrêter l'attente. Les résultats seront aussi enregistrés dans l'historique.",
    'Play while waiting': 'Jouer en attendant',
    'Play dot-matrix snake while waiting':
      'Jouer au serpent matriciel en attendant',
    'Collapse minigame': 'Réduire le mini-jeu',
    'Expand minigame': 'Agrandir le mini-jeu',
    'Score: {{score}}': 'Score : {{score}}',
    'High score: {{score}}': 'Meilleur score : {{score}}',
    'Swipe or use arrow keys to control':
      'Glissez ou utilisez les touches fléchées pour contrôler',
    'Generation settings': 'Paramètres de génération',
    'Copy error details': "Copier les détails de l'erreur",
    'Error details copied': "Détails de l'erreur copiés",
    'The drawing workbench encountered an error, but your draft has been saved.':
      "L'atelier de dessin a rencontré une erreur, mais votre brouillon a été enregistré.",
    'Recover workbench': "Restaurer l'atelier",
    'More tools': "Plus d'outils",
    'Clear image history?': "Effacer l'historique des images ?",
  },
  ja: {
    'Stop waiting': '待機を停止',
    'Stopped waiting. If the server is already processing, the generated image may appear in your history later.':
      '待機を停止しました。サーバーで既に処理中の場合、生成された画像は後ほど履歴に表示されることがあります。',
    'Request submitted · Waiting {{seconds}}s':
      'リクエスト送信済み · 待機中 {{seconds}} 秒',
    'Waiting for image generation result...': '画像生成の結果を待機中...',
    'You can continue waiting, or stop waiting. Results will also be saved to history.':
      '待機を続けることも停止することもできます。完了した結果は履歴にも保存されます。',
    'Play while waiting': '待機中にミニゲームで遊ぶ',
    'Play dot-matrix snake while waiting': '待機中にドットスネークゲームで遊ぶ',
    'Collapse minigame': 'ミニゲームを折りたたむ',
    'Expand minigame': 'ミニゲームを展開',
    'Score: {{score}}': 'スコア: {{score}}',
    'High score: {{score}}': 'ハイスコア: {{score}}',
    'Swipe or use arrow keys to control': 'スワイプまたは矢印キーで操作',
    'Generation settings': '生成設定',
    'Copy error details': 'エラー詳細をコピー',
    'Error details copied': 'エラー詳細をコピーしました',
    'The drawing workbench encountered an error, but your draft has been saved.':
      '描画スタジオで問題が発生しましたが、下書きは保存されています。',
    'Recover workbench': 'スタジオを復旧',
    'More tools': 'その他のツール',
    'Clear image history?': '画像履歴を消去しますか？',
  },
  ru: {
    'Stop waiting': 'Прекратить ожидание',
    'Stopped waiting. If the server is already processing, the generated image may appear in your history later.':
      'Ожидание прекращено. Если сервер уже обрабатывает запрос, созданное изображение может позже появиться в истории.',
    'Request submitted · Waiting {{seconds}}s':
      'Запрос отправлен · Ожидание: {{seconds}} с',
    'Waiting for image generation result...':
      'Ожидание результата генерации изображения...',
    'You can continue waiting, or stop waiting. Results will also be saved to history.':
      'Вы можете продолжить или прекратить ожидание. Результаты также сохраняются в истории.',
    'Play while waiting': 'Сыграть во время ожидания',
    'Play dot-matrix snake while waiting':
      'Сыграть в точечную змейку во время ожидания',
    'Collapse minigame': 'Свернуть мини-игру',
    'Expand minigame': 'Развернуть мини-игру',
    'Score: {{score}}': 'Счет: {{score}}',
    'High score: {{score}}': 'Рекорд: {{score}}',
    'Swipe or use arrow keys to control':
      'Проведите пальцем или используйте стрелки для управления',
    'Generation settings': 'Настройки генерации',
    'Copy error details': 'Скопировать сведения об ошибке',
    'Error details copied': 'Сведения об ошибке скопированы',
    'The drawing workbench encountered an error, but your draft has been saved.':
      'В рабочей области рисования произошла ошибка, но ваш черновик сохранен.',
    'Recover workbench': 'Восстановить область',
    'More tools': 'Другие инструменты',
    'Clear image history?': 'Очистить историю изображений?',
  },
  vi: {
    'Stop waiting': 'Dừng chờ',
    'Stopped waiting. If the server is already processing, the generated image may appear in your history later.':
      'Đã dừng chờ. Nếu máy chủ đang xử lý, ảnh tạo ra có thể xuất hiện trong lịch sử sau đó.',
    'Request submitted · Waiting {{seconds}}s':
      'Đã gửi yêu cầu · Đang chờ {{seconds}} giây',
    'Waiting for image generation result...': 'Đang chờ kết quả tạo ảnh...',
    'You can continue waiting, or stop waiting. Results will also be saved to history.':
      'Bạn có thể tiếp tục chờ hoặc dừng chờ. Kết quả cũng sẽ được lưu vào lịch sử.',
    'Play while waiting': 'Chơi trong khi chờ',
    'Play dot-matrix snake while waiting':
      'Chơi rắn săn mồi ma trận điểm trong khi chờ',
    'Collapse minigame': 'Thu gọn trò chơi',
    'Expand minigame': 'Mở rộng trò chơi',
    'Score: {{score}}': 'Điểm: {{score}}',
    'High score: {{score}}': 'Điểm cao: {{score}}',
    'Swipe or use arrow keys to control':
      'Vuốt hoặc dùng phím mũi tên để điều khiển',
    'Generation settings': 'Cài đặt tạo ảnh',
    'Copy error details': 'Sao chép chi tiết lỗi',
    'Error details copied': 'Đã sao chép chi tiết lỗi',
    'The drawing workbench encountered an error, but your draft has been saved.':
      'Không gian vẽ gặp sự cố, nhưng bản nháp của bạn đã được lưu.',
    'Recover workbench': 'Khôi phục bàn làm việc',
    'More tools': 'Thêm công cụ',
    'Clear image history?': 'Xóa lịch sử ảnh?',
  },
}

for (const [locale, translations] of Object.entries(drawingExperienceFixes)) {
  Object.assign(newKeys[locale], translations)
}

const keyAndDrawingFixes = {
  en: {
    'API address unavailable': 'API address unavailable',
    'Base URL copied': 'Base URL copied',
    'Failed to copy Base URL': 'Failed to copy Base URL',
    'Copy Base URL': 'Copy Base URL',
    'Includes /v1. Do not append /v1 again in your client. Copy the API key separately.':
      'Includes /v1. Do not append /v1 again in your client. Copy the API key separately.',
    'Generation continues in this tab. You can switch pages; do not reload or close this tab until the result is saved.':
      'Generation continues in this tab. You can switch pages; do not reload or close this tab until the result is saved.',
    'Image generation completed': 'Image generation completed',
    'Query API key quota (read-only)': 'Query API key quota (read-only)',
    'GET /v1/usage reports this API key only (scope=token, currency=USD), not your account balance or subscription.':
      'GET /v1/usage reports this API key only (scope=token, currency=USD), not your account balance or subscription.',
    'Copy quota query': 'Copy quota query',
    'Replace API_KEY locally with your key. This example never includes your real key.':
      'Replace API_KEY locally with your key. This example never includes your real key.',
    'Response fields: valid, currency, remaining, used_today, used_total, total_quota, unlimited, updated_at. With unlimited=true, remaining and total_quota are null. used_today uses retained UTC logs; it is null when logging is disabled and may be incomplete after log deletion or interruptions.':
      'Response fields: valid, currency, remaining, used_today, used_total, total_quota, unlimited, updated_at. With unlimited=true, remaining and total_quota are null. used_today uses retained UTC logs; it is null when logging is disabled and may be incomplete after log deletion or interruptions.',
    'Model prices remain available at GET /v1/pricing.':
      'Model prices remain available at GET /v1/pricing.',
  },
  zh: {
    'API address unavailable': 'API 地址暂不可用',
    'Base URL copied': 'Base URL 已复制',
    'Failed to copy Base URL': 'Base URL 复制失败',
    'Copy Base URL': '复制 Base URL',
    'Includes /v1. Do not append /v1 again in your client. Copy the API key separately.':
      '地址已包含 /v1，请勿在客户端重复添加。API 密钥请单独复制。',
    'Generation continues in this tab. You can switch pages; do not reload or close this tab until the result is saved.':
      '图片仍在此标签页中生成。可以切换站内页面；结果保存前请勿刷新或关闭此标签页。',
    'Image generation completed': '图片生成完成',
    'Query API key quota (read-only)': '查询密钥额度（只读）',
    'GET /v1/usage reports this API key only (scope=token, currency=USD), not your account balance or subscription.':
      'GET /v1/usage 仅返回当前 API 密钥的额度（scope=token，currency=USD），不代表账户余额或订阅额度。',
    'Copy quota query': '复制额度查询命令',
    'Replace API_KEY locally with your key. This example never includes your real key.':
      '请在本地将 API_KEY 替换为你的密钥。此示例不会包含真实密钥。',
    'Response fields: valid, currency, remaining, used_today, used_total, total_quota, unlimited, updated_at. With unlimited=true, remaining and total_quota are null. used_today uses retained UTC logs; it is null when logging is disabled and may be incomplete after log deletion or interruptions.':
      '响应字段：valid、currency、remaining、used_today、used_total、total_quota、unlimited、updated_at。unlimited=true 时，remaining 和 total_quota 为 null。used_today 按保留的 UTC 日志统计；日志关闭时为 null，删除日志或记录中断会导致统计不完整。',
    'Model prices remain available at GET /v1/pricing.':
      '模型价格仍可通过 GET /v1/pricing 查询。',
  },
  'zh-TW': {
    'API address unavailable': 'API 位址暫不可用',
    'Base URL copied': 'Base URL 已複製',
    'Failed to copy Base URL': 'Base URL 複製失敗',
    'Copy Base URL': '複製 Base URL',
    'Includes /v1. Do not append /v1 again in your client. Copy the API key separately.':
      '位址已包含 /v1，請勿在用戶端重複加入。API 金鑰請另行複製。',
    'Generation continues in this tab. You can switch pages; do not reload or close this tab until the result is saved.':
      '圖片仍在此分頁中產生。可以切換站內頁面；結果儲存前請勿重新整理或關閉此分頁。',
    'Image generation completed': '圖片產生完成',
    'Query API key quota (read-only)': '查詢金鑰額度（唯讀）',
    'GET /v1/usage reports this API key only (scope=token, currency=USD), not your account balance or subscription.':
      'GET /v1/usage 僅傳回目前 API 金鑰的額度（scope=token，currency=USD），不代表帳戶餘額或訂閱額度。',
    'Copy quota query': '複製額度查詢指令',
    'Replace API_KEY locally with your key. This example never includes your real key.':
      '請在本機將 API_KEY 替換為你的金鑰。此範例不會包含真實金鑰。',
    'Response fields: valid, currency, remaining, used_today, used_total, total_quota, unlimited, updated_at. With unlimited=true, remaining and total_quota are null. used_today uses retained UTC logs; it is null when logging is disabled and may be incomplete after log deletion or interruptions.':
      '回應欄位：valid、currency、remaining、used_today、used_total、total_quota、unlimited、updated_at。unlimited=true 時，remaining 和 total_quota 為 null。used_today 按保留的 UTC 日誌統計；日誌關閉時為 null，刪除日誌或記錄中斷會導致統計不完整。',
    'Model prices remain available at GET /v1/pricing.':
      '模型價格仍可透過 GET /v1/pricing 查詢。',
  },
  fr: {
    'API address unavailable': 'Adresse API indisponible',
    'Base URL copied': 'URL de base copiée',
    'Failed to copy Base URL': 'Échec de la copie de l’URL de base',
    'Copy Base URL': 'Copier l’URL de base',
    'Includes /v1. Do not append /v1 again in your client. Copy the API key separately.':
      'Inclut /v1. Ne rajoutez pas /v1 dans votre client. Copiez la clé API séparément.',
    'Generation continues in this tab. You can switch pages; do not reload or close this tab until the result is saved.':
      'La génération continue dans cet onglet. Vous pouvez changer de page dans le site ; gardez cet onglet ouvert sans le recharger jusqu’à l’enregistrement du résultat.',
    'Image generation completed': 'Génération de l’image terminée',
    'Query API key quota (read-only)':
      'Consulter le quota de la clé API (lecture seule)',
    'GET /v1/usage reports this API key only (scope=token, currency=USD), not your account balance or subscription.':
      'GET /v1/usage concerne uniquement cette clé API (scope=token, currency=USD), pas le solde du compte ni l’abonnement.',
    'Copy quota query': 'Copier la commande de quota',
    'Replace API_KEY locally with your key. This example never includes your real key.':
      'Remplacez API_KEY localement par votre clé. Cet exemple ne contient jamais votre vraie clé.',
    'Response fields: valid, currency, remaining, used_today, used_total, total_quota, unlimited, updated_at. With unlimited=true, remaining and total_quota are null. used_today uses retained UTC logs; it is null when logging is disabled and may be incomplete after log deletion or interruptions.':
      'Champs : valid, currency, remaining, used_today, used_total, total_quota, unlimited, updated_at. Avec unlimited=true, remaining et total_quota valent null. used_today utilise les journaux UTC conservés ; il vaut null si la journalisation est désactivée et peut être incomplet après suppression ou interruption des journaux.',
    'Model prices remain available at GET /v1/pricing.':
      'Les prix des modèles restent consultables via GET /v1/pricing.',
  },
  ja: {
    'API address unavailable': 'API アドレスを利用できません',
    'Base URL copied': 'Base URL をコピーしました',
    'Failed to copy Base URL': 'Base URL をコピーできませんでした',
    'Copy Base URL': 'Base URL をコピー',
    'Includes /v1. Do not append /v1 again in your client. Copy the API key separately.':
      '/v1 を含みます。クライアントで /v1 を重複して追加しないでください。API キーは別途コピーしてください。',
    'Generation continues in this tab. You can switch pages; do not reload or close this tab until the result is saved.':
      'このタブで生成を続けます。サイト内のページは移動できますが、結果の保存までタブを閉じたり再読み込みしたりしないでください。',
    'Image generation completed': '画像の生成が完了しました',
    'Query API key quota (read-only)':
      'API キーの割り当てを照会（読み取り専用）',
    'GET /v1/usage reports this API key only (scope=token, currency=USD), not your account balance or subscription.':
      'GET /v1/usage はこの API キーの割り当てのみを返します（scope=token、currency=USD）。アカウント残高やサブスクリプションとは異なります。',
    'Copy quota query': '割り当て照会コマンドをコピー',
    'Replace API_KEY locally with your key. This example never includes your real key.':
      'ローカルで API_KEY を自分のキーに置き換えてください。この例に実際のキーは含まれません。',
    'Response fields: valid, currency, remaining, used_today, used_total, total_quota, unlimited, updated_at. With unlimited=true, remaining and total_quota are null. used_today uses retained UTC logs; it is null when logging is disabled and may be incomplete after log deletion or interruptions.':
      '応答フィールド：valid、currency、remaining、used_today、used_total、total_quota、unlimited、updated_at。unlimited=true の場合、remaining と total_quota は null です。used_today は保持された UTC ログで集計されます。ログが無効な場合は null となり、ログの削除や記録の中断により不完全になる場合があります。',
    'Model prices remain available at GET /v1/pricing.':
      'モデル料金は引き続き GET /v1/pricing で確認できます。',
  },
  ru: {
    'API address unavailable': 'Адрес API недоступен',
    'Base URL copied': 'Базовый URL скопирован',
    'Failed to copy Base URL': 'Не удалось скопировать базовый URL',
    'Copy Base URL': 'Копировать базовый URL',
    'Includes /v1. Do not append /v1 again in your client. Copy the API key separately.':
      'Адрес уже содержит /v1. Не добавляйте /v1 повторно в клиенте. Копируйте API-ключ отдельно.',
    'Generation continues in this tab. You can switch pages; do not reload or close this tab until the result is saved.':
      'Генерация продолжается в этой вкладке. Можно переходить между страницами сайта; не закрывайте и не обновляйте вкладку до сохранения результата.',
    'Image generation completed': 'Генерация изображения завершена',
    'Query API key quota (read-only)':
      'Проверить квоту API-ключа (только чтение)',
    'GET /v1/usage reports this API key only (scope=token, currency=USD), not your account balance or subscription.':
      'GET /v1/usage возвращает квоту только этого API-ключа (scope=token, currency=USD), а не баланс аккаунта или подписки.',
    'Copy quota query': 'Копировать запрос квоты',
    'Replace API_KEY locally with your key. This example never includes your real key.':
      'Замените API_KEY своим ключом локально. В примере никогда не содержится ваш настоящий ключ.',
    'Response fields: valid, currency, remaining, used_today, used_total, total_quota, unlimited, updated_at. With unlimited=true, remaining and total_quota are null. used_today uses retained UTC logs; it is null when logging is disabled and may be incomplete after log deletion or interruptions.':
      'Поля ответа: valid, currency, remaining, used_today, used_total, total_quota, unlimited, updated_at. При unlimited=true поля remaining и total_quota равны null. used_today рассчитывается по сохранённым журналам UTC; при отключении журналов значение равно null, а удаление или перерывы записи могут привести к неполным данным.',
    'Model prices remain available at GET /v1/pricing.':
      'Цены моделей по-прежнему доступны через GET /v1/pricing.',
  },
  vi: {
    'API address unavailable': 'Địa chỉ API không khả dụng',
    'Base URL copied': 'Đã sao chép URL cơ sở',
    'Failed to copy Base URL': 'Không thể sao chép URL cơ sở',
    'Copy Base URL': 'Sao chép URL cơ sở',
    'Includes /v1. Do not append /v1 again in your client. Copy the API key separately.':
      'Đã bao gồm /v1. Không thêm /v1 lần nữa trong ứng dụng. Sao chép khóa API riêng.',
    'Generation continues in this tab. You can switch pages; do not reload or close this tab until the result is saved.':
      'Ảnh tiếp tục được tạo trong thẻ này. Bạn có thể chuyển trang trong trang web; không tải lại hoặc đóng thẻ cho đến khi kết quả được lưu.',
    'Image generation completed': 'Đã tạo ảnh xong',
    'Query API key quota (read-only)': 'Tra cứu hạn mức khóa API (chỉ đọc)',
    'GET /v1/usage reports this API key only (scope=token, currency=USD), not your account balance or subscription.':
      'GET /v1/usage chỉ trả về hạn mức của khóa API này (scope=token, currency=USD), không phải số dư tài khoản hay gói đăng ký.',
    'Copy quota query': 'Sao chép lệnh tra cứu hạn mức',
    'Replace API_KEY locally with your key. This example never includes your real key.':
      'Thay API_KEY bằng khóa của bạn trên máy cục bộ. Ví dụ này không bao giờ chứa khóa thật.',
    'Response fields: valid, currency, remaining, used_today, used_total, total_quota, unlimited, updated_at. With unlimited=true, remaining and total_quota are null. used_today uses retained UTC logs; it is null when logging is disabled and may be incomplete after log deletion or interruptions.':
      'Các trường phản hồi: valid, currency, remaining, used_today, used_total, total_quota, unlimited, updated_at. Khi unlimited=true, remaining và total_quota là null. used_today được tính theo nhật ký UTC còn lưu; giá trị là null khi tắt nhật ký và có thể không đầy đủ nếu nhật ký bị xóa hoặc ghi gián đoạn.',
    'Model prices remain available at GET /v1/pricing.':
      'Giá mô hình vẫn có thể được tra cứu qua GET /v1/pricing.',
  },
}

for (const [locale, translations] of Object.entries(keyAndDrawingFixes)) {
  Object.assign(newKeys[locale], translations)
}

const ratioNotificationCopy = {
  en: {
    'Rate changes': 'Rate changes',
    'Old value': 'Old value',
    'New value': 'New value',
    'Effective at {{time}}': 'Effective at {{time}}',
    'Unable to load rate changes': 'Unable to load rate changes',
    'Load older rate changes': 'Load older rate changes',
    'Limit: 30 queries per minute per account. Poll once per minute; on HTTP 429, respect Retry-After. Quota queries require a valid key but no developer level; pricing uses normal relay authentication.':
      'Limit: 30 queries per minute per account. Poll once per minute; on HTTP 429, respect Retry-After. Quota queries require a valid key but no developer level; pricing uses normal relay authentication.',
  },
  zh: {
    'Rate changes': '倍率变更',
    'Old value': '旧值',
    'New value': '新值',
    'Effective at {{time}}': '生效时间：{{time}}',
    'Unable to load rate changes': '无法加载倍率变更',
    'Load older rate changes': '加载更早的倍率变更',
    'Limit: 30 queries per minute per account. Poll once per minute; on HTTP 429, respect Retry-After. Quota queries require a valid key but no developer level; pricing uses normal relay authentication.':
      '每账户每分钟限 30 次查询，建议每分钟轮询一次；HTTP 429 时请遵循 Retry-After。额度查询需要有效密钥，不要求开发者等级；价格查询沿用正常的转发鉴权。',
  },
  'zh-TW': {
    'Rate changes': '倍率變更',
    'Old value': '舊值',
    'New value': '新值',
    'Effective at {{time}}': '生效時間：{{time}}',
    'Unable to load rate changes': '無法載入倍率變更',
    'Load older rate changes': '載入更早的倍率變更',
    'Limit: 30 queries per minute per account. Poll once per minute; on HTTP 429, respect Retry-After. Quota queries require a valid key but no developer level; pricing uses normal relay authentication.':
      '每帳戶每分鐘限 30 次查詢，建議每分鐘輪詢一次；HTTP 429 時請遵循 Retry-After。額度查詢需要有效金鑰，不要求開發者等級；價格查詢沿用正常的轉送驗證。',
  },
  fr: {
    'Rate changes': 'Changements de tarifs',
    'Old value': 'Ancienne valeur',
    'New value': 'Nouvelle valeur',
    'Effective at {{time}}': 'Prend effet à {{time}}',
    'Unable to load rate changes':
      'Impossible de charger les changements de tarifs',
    'Load older rate changes': 'Charger les changements antérieurs',
    'Limit: 30 queries per minute per account. Poll once per minute; on HTTP 429, respect Retry-After. Quota queries require a valid key but no developer level; pricing uses normal relay authentication.':
      'Limite : 30 requêtes par minute et par compte. Interrogez une fois par minute ; en cas de HTTP 429, respectez Retry-After. Le quota exige une clé valide, sans niveau développeur ; les prix utilisent l’authentification habituelle du relais.',
  },
  ja: {
    'Rate changes': '倍率の変更',
    'Old value': '変更前',
    'New value': '変更後',
    'Effective at {{time}}': '適用日時：{{time}}',
    'Unable to load rate changes': '倍率の変更を読み込めません',
    'Load older rate changes': '以前の倍率変更を読み込む',
    'Limit: 30 queries per minute per account. Poll once per minute; on HTTP 429, respect Retry-After. Quota queries require a valid key but no developer level; pricing uses normal relay authentication.':
      'アカウントごとに毎分 30 回までです。照会は毎分 1 回とし、HTTP 429 では Retry-After に従ってください。割り当て照会には有効なキーが必要ですが、開発者レベルは不要です。料金照会には通常のリレー認証が適用されます。',
  },
  ru: {
    'Rate changes': 'Изменения коэффициентов',
    'Old value': 'Прежнее значение',
    'New value': 'Новое значение',
    'Effective at {{time}}': 'Вступает в силу: {{time}}',
    'Unable to load rate changes':
      'Не удалось загрузить изменения коэффициентов',
    'Load older rate changes': 'Загрузить более ранние изменения',
    'Limit: 30 queries per minute per account. Poll once per minute; on HTTP 429, respect Retry-After. Quota queries require a valid key but no developer level; pricing uses normal relay authentication.':
      'Лимит: 30 запросов в минуту на аккаунт. Опрашивайте раз в минуту; при HTTP 429 соблюдайте Retry-After. Для квоты нужен действующий ключ, но не уровень разработчика; для цен действует обычная авторизация ретрансляции.',
  },
  vi: {
    'Rate changes': 'Thay đổi hệ số',
    'Old value': 'Giá trị cũ',
    'New value': 'Giá trị mới',
    'Effective at {{time}}': 'Có hiệu lực lúc {{time}}',
    'Unable to load rate changes': 'Không thể tải thay đổi hệ số',
    'Load older rate changes': 'Tải thay đổi hệ số trước đó',
    'Limit: 30 queries per minute per account. Poll once per minute; on HTTP 429, respect Retry-After. Quota queries require a valid key but no developer level; pricing uses normal relay authentication.':
      'Giới hạn: 30 truy vấn mỗi phút cho mỗi tài khoản. Nên truy vấn mỗi phút một lần; khi gặp HTTP 429, tuân thủ Retry-After. Tra cứu hạn mức cần khóa hợp lệ nhưng không yêu cầu cấp nhà phát triển; tra cứu giá dùng xác thực chuyển tiếp thông thường.',
  },
}
for (const [locale, translations] of Object.entries(ratioNotificationCopy)) {
  Object.assign(newKeys[locale], translations)
}

const resetVoucherHistoryCopy = {
  en: {
    'Show used or expired vouchers ({{count}} loaded)':
      'Show used or expired vouchers ({{count}} loaded)',
    'Hide used or expired vouchers ({{count}} loaded)':
      'Hide used or expired vouchers ({{count}} loaded)',
    'Voucher expiry': 'Voucher expiry',
    'Choose a future expiry time in your local time zone. Existing vouchers are unchanged.':
      'Choose a future expiry time in your local time zone. Existing vouchers are unchanged.',
    'Issue one single-use voucher for each of {{count}} previewed user-plan pairs? Each voucher expires at {{time}}.':
      'Issue one single-use voucher for each of {{count}} previewed user-plan pairs? Each voucher expires at {{time}}.',
  },
  zh: {
    'Show used or expired vouchers ({{count}} loaded)':
      '展开已使用或已过期券（已加载 {{count}} 张）',
    'Hide used or expired vouchers ({{count}} loaded)':
      '收起已使用或已过期券（已加载 {{count}} 张）',
    'Voucher expiry': '券到期时间',
    'Choose a future expiry time in your local time zone. Existing vouchers are unchanged.':
      '请按本地时区选择未来的到期时间。已发放的券不受影响。',
    'Issue one single-use voucher for each of {{count}} previewed user-plan pairs? Each voucher expires at {{time}}.':
      '为预览中的 {{count}} 个用户与套餐组合各发放一张一次性券？每张券的到期时间为 {{time}}。',
  },
  'zh-TW': {
    'Show used or expired vouchers ({{count}} loaded)':
      '展開已使用或已到期券（已載入 {{count}} 張）',
    'Hide used or expired vouchers ({{count}} loaded)':
      '收起已使用或已到期券（已載入 {{count}} 張）',
    'Voucher expiry': '券到期時間',
    'Choose a future expiry time in your local time zone. Existing vouchers are unchanged.':
      '請依本地時區選擇未來的到期時間。已發放的券不受影響。',
    'Issue one single-use voucher for each of {{count}} previewed user-plan pairs? Each voucher expires at {{time}}.':
      '為預覽中的 {{count}} 個使用者與方案組合各發放一張一次性券？每張券的到期時間為 {{time}}。',
  },
  fr: {
    'Show used or expired vouchers ({{count}} loaded)':
      'Afficher les bons utilisés ou expirés ({{count}} chargés)',
    'Hide used or expired vouchers ({{count}} loaded)':
      'Masquer les bons utilisés ou expirés ({{count}} chargés)',
    'Voucher expiry': 'Expiration du bon',
    'Choose a future expiry time in your local time zone. Existing vouchers are unchanged.':
      'Choisissez une date d’expiration future dans votre fuseau horaire. Les bons existants restent inchangés.',
    'Issue one single-use voucher for each of {{count}} previewed user-plan pairs? Each voucher expires at {{time}}.':
      'Émettre un bon à usage unique pour chacune des {{count}} paires utilisateur-forfait de l’aperçu ? Chaque bon expire le {{time}}.',
  },
  ja: {
    'Show used or expired vouchers ({{count}} loaded)':
      '使用済み・期限切れの券を表示（{{count}} 件読み込み済み）',
    'Hide used or expired vouchers ({{count}} loaded)':
      '使用済み・期限切れの券を隠す（{{count}} 件読み込み済み）',
    'Voucher expiry': '券の有効期限',
    'Choose a future expiry time in your local time zone. Existing vouchers are unchanged.':
      'お使いのタイムゾーンで未来の有効期限を選択してください。発行済みの券は変更されません。',
    'Issue one single-use voucher for each of {{count}} previewed user-plan pairs? Each voucher expires at {{time}}.':
      'プレビューした {{count}} 組のユーザーとプランの組み合わせに、それぞれ 1 回限りの券を発行しますか？各券の有効期限は {{time}} です。',
  },
  ru: {
    'Show used or expired vouchers ({{count}} loaded)':
      'Показать использованные и истёкшие ваучеры (загружено: {{count}})',
    'Hide used or expired vouchers ({{count}} loaded)':
      'Скрыть использованные и истёкшие ваучеры (загружено: {{count}})',
    'Voucher expiry': 'Срок действия ваучера',
    'Choose a future expiry time in your local time zone. Existing vouchers are unchanged.':
      'Выберите будущий срок действия в вашем часовом поясе. Уже выданные ваучеры не изменятся.',
    'Issue one single-use voucher for each of {{count}} previewed user-plan pairs? Each voucher expires at {{time}}.':
      'Выдать по одноразовому ваучеру для каждой из {{count}} пар «пользователь — тариф» в предпросмотре? Каждый ваучер истекает {{time}}.',
  },
  vi: {
    'Show used or expired vouchers ({{count}} loaded)':
      'Hiện phiếu đã dùng hoặc hết hạn (đã tải {{count}})',
    'Hide used or expired vouchers ({{count}} loaded)':
      'Ẩn phiếu đã dùng hoặc hết hạn (đã tải {{count}})',
    'Voucher expiry': 'Thời điểm hết hạn phiếu',
    'Choose a future expiry time in your local time zone. Existing vouchers are unchanged.':
      'Chọn thời điểm hết hạn trong tương lai theo múi giờ địa phương. Các phiếu đã phát hành không thay đổi.',
    'Issue one single-use voucher for each of {{count}} previewed user-plan pairs? Each voucher expires at {{time}}.':
      'Phát một phiếu dùng một lần cho mỗi cặp người dùng–gói trong {{count}} cặp đã xem trước? Mỗi phiếu hết hạn lúc {{time}}.',
  },
}
for (const [locale, translations] of Object.entries(resetVoucherHistoryCopy)) {
  Object.assign(newKeys[locale], translations)
}

const expiredResetPreviewCopy = {
  en: {
    'The preview or voucher expiry has passed. Prepare a new preview.':
      'The preview or voucher expiry has passed. Prepare a new preview.',
  },
  zh: {
    'The preview or voucher expiry has passed. Prepare a new preview.':
      '预览或券已过期，请重新生成预览。',
  },
  'zh-TW': {
    'The preview or voucher expiry has passed. Prepare a new preview.':
      '預覽或券已到期，請重新產生預覽。',
  },
  fr: {
    'The preview or voucher expiry has passed. Prepare a new preview.':
      'L’aperçu ou le bon a expiré. Préparez un nouvel aperçu.',
  },
  ja: {
    'The preview or voucher expiry has passed. Prepare a new preview.':
      'プレビューまたは券の有効期限が切れました。新しいプレビューを作成してください。',
  },
  ru: {
    'The preview or voucher expiry has passed. Prepare a new preview.':
      'Срок действия предпросмотра или ваучера истёк. Подготовьте новый предпросмотр.',
  },
  vi: {
    'The preview or voucher expiry has passed. Prepare a new preview.':
      'Bản xem trước hoặc phiếu đã hết hạn. Hãy tạo bản xem trước mới.',
  },
}
for (const [locale, translations] of Object.entries(expiredResetPreviewCopy)) {
  Object.assign(newKeys[locale], translations)
}

const deprecatedCurrencyKeys = new Set([
  'Price (local currency / USD)',
  'Use global price',
  'Use global price reciprocal',
])

const scriptsCopy = {
  en: {
    Scripts: 'Scripts',
    'Unable to load script': 'Unable to load script',
    'Unable to load scripts': 'Unable to load scripts',
    'Name is required': 'Name is required',
    'Latest version': 'Latest version',
    'Update available': 'Update available',
    'Up to date': 'Up to date',
    'GitHub release': 'GitHub release',
    'Linux / macOS': 'Linux / macOS',
    Windows: 'Windows',
    'Failed to delete': 'Failed to delete',
    'Public scripts': 'Public scripts',
    'Back to home': 'Back to home',
    'Browse maintained setup scripts and copy the command for your system.':
      'Browse maintained setup scripts and copy the command for your system.',
    'Open public script page': 'Open public script page',
    'Unknown update time': 'Unknown update time',
    fetches: 'fetches',
    'Command copied': 'Command copied',
    'Unable to copy command': 'Unable to copy command',
    'No scripts published yet': 'No scripts published yet',
    'Script repository': 'Script repository',
    'Scripts are read from this repository and exposed on the public scripts page.':
      'Scripts are read from this repository and exposed on the public scripts page.',
    'Repository URL': 'Repository URL',
    Branch: 'Branch',
    'GitHub key (optional)': 'GitHub key (optional)',
    'Key is configured; leave blank to keep it':
      'Key is configured; leave blank to keep it',
    'Only needed for a private repository':
      'Only needed for a private repository',
    'Remove the stored GitHub key': 'Remove the stored GitHub key',
    'Save repository': 'Save repository',
    'Pull updates': 'Pull updates',
    'Copy public page link': 'Copy public page link',
    'Last pulled': 'Last pulled',
    'Published scripts': 'Published scripts',
    scripts: 'scripts',
    'Pull a repository to publish scripts.':
      'Pull a repository to publish scripts.',
    'Script repository settings saved': 'Script repository settings saved',
    'Failed to pull repository': 'Failed to pull repository',
    'Scripts updated from repository': 'Scripts updated from repository',
    Saving: 'Saving...',
    Pulling: 'Pulling...',
  },
  zh: {
    Scripts: '脚本',
    'Unable to load script': '无法加载脚本',
    'Unable to load scripts': '无法加载脚本列表',
    'Name is required': '请输入名称',
    'Latest version': '最新版本',
    'Update available': '有可用更新',
    'Up to date': '已是最新',
    'GitHub release': 'GitHub 发布',
    'Linux / macOS': 'Linux / macOS',
    Windows: 'Windows',
    'Failed to delete': '删除失败',
    'Public scripts': '公开脚本',
    'Back to home': '返回首页',
    'Browse maintained setup scripts and copy the command for your system.':
      '浏览维护中的安装脚本，复制适用于你系统的命令。',
    'Open public script page': '打开公开脚本页',
    'Unknown update time': '更新时间未知',
    fetches: '次获取',
    'Command copied': '命令已复制',
    'Unable to copy command': '无法复制命令',
    'No scripts published yet': '暂未发布脚本',
    'Script repository': '脚本仓库',
    'Scripts are read from this repository and exposed on the public scripts page.':
      '脚本从此仓库读取，并展示在公开脚本页。',
    'Repository URL': '仓库地址',
    Branch: '分支',
    'GitHub key (optional)': 'GitHub Key（可选）',
    'Key is configured; leave blank to keep it': 'Key 已配置，留空即可保留',
    'Only needed for a private repository': '仅私有仓库需要填写',
    'Remove the stored GitHub key': '移除已保存的 GitHub Key',
    'Save repository': '保存仓库配置',
    'Pull updates': '拉取更新',
    'Copy public page link': '复制公开页面链接',
    'Last pulled': '上次拉取',
    'Published scripts': '已发布脚本',
    scripts: '个脚本',
    'Pull a repository to publish scripts.': '拉取仓库后，脚本会在这里发布。',
    'Script repository settings saved': '脚本仓库配置已保存',
    'Failed to pull repository': '拉取仓库失败',
    'Scripts updated from repository': '脚本已从仓库更新',
    Saving: '保存中...',
    Pulling: '拉取中...',
  },
  'zh-TW': {
    Scripts: '腳本',
    'Unable to load script': '無法載入腳本',
    'Unable to load scripts': '無法載入腳本清單',
    'Name is required': '請輸入名稱',
    'Latest version': '最新版本',
    'Update available': '有可用更新',
    'Up to date': '已是最新',
    'GitHub release': 'GitHub 發佈',
    'Linux / macOS': 'Linux / macOS',
    Windows: 'Windows',
    'Failed to delete': '刪除失敗',
    'Public scripts': '公開腳本',
    'Back to home': '返回首頁',
    'Browse maintained setup scripts and copy the command for your system.':
      '瀏覽維護中的安裝腳本，複製適用於你系統的指令。',
    'Open public script page': '開啟公開腳本頁',
    'Unknown update time': '更新時間未知',
    fetches: '次取得',
    'Command copied': '指令已複製',
    'Unable to copy command': '無法複製指令',
    'No scripts published yet': '尚未發佈腳本',
    'Script repository': '腳本儲存庫',
    'Scripts are read from this repository and exposed on the public scripts page.':
      '腳本從此儲存庫讀取，並顯示在公開腳本頁。',
    'Repository URL': '儲存庫網址',
    Branch: '分支',
    'GitHub key (optional)': 'GitHub Key（選填）',
    'Key is configured; leave blank to keep it': 'Key 已設定，留白即可保留',
    'Only needed for a private repository': '僅私有儲存庫需要填寫',
    'Remove the stored GitHub key': '移除已儲存的 GitHub Key',
    'Save repository': '儲存儲存庫設定',
    'Pull updates': '拉取更新',
    'Copy public page link': '複製公開頁面連結',
    'Last pulled': '上次拉取',
    'Published scripts': '已發佈腳本',
    scripts: '個腳本',
    'Pull a repository to publish scripts.': '拉取儲存庫後，腳本會在這裡發佈。',
    'Script repository settings saved': '腳本儲存庫設定已儲存',
    'Failed to pull repository': '拉取儲存庫失敗',
    'Scripts updated from repository': '腳本已從儲存庫更新',
    Saving: '儲存中...',
    Pulling: '拉取中...',
  },
  fr: {
    Scripts: 'Scripts',
    'Unable to load script': 'Impossible de charger le script',
    'Unable to load scripts': 'Impossible de charger les scripts',
    'Name is required': 'Le nom est requis',
    'Latest version': 'Dernière version',
    'Update available': 'Mise à jour disponible',
    'Up to date': 'À jour',
    'GitHub release': 'Publication GitHub',
    'Linux / macOS': 'Linux / macOS',
    Windows: 'Windows',
    'Failed to delete': 'Échec de la suppression',
    'Public scripts': 'Scripts publics',
    'Back to home': "Retour à l'accueil",
    'Browse maintained setup scripts and copy the command for your system.':
      'Parcourez les scripts maintenus et copiez la commande adaptée à votre système.',
    'Open public script page': 'Ouvrir la page des scripts publics',
    'Unknown update time': 'Heure de mise à jour inconnue',
    fetches: 'récupérations',
    'Command copied': 'Commande copiée',
    'Unable to copy command': 'Impossible de copier la commande',
    'No scripts published yet': 'Aucun script publié',
    'Script repository': 'Dépôt de scripts',
    'Scripts are read from this repository and exposed on the public scripts page.':
      'Les scripts sont lus depuis ce dépôt et publiés sur la page publique.',
    'Repository URL': 'URL du dépôt',
    Branch: 'Branche',
    'GitHub key (optional)': 'Clé GitHub (facultative)',
    'Key is configured; leave blank to keep it':
      'Clé configurée ; laissez vide pour la conserver',
    'Only needed for a private repository':
      'Nécessaire uniquement pour un dépôt privé',
    'Remove the stored GitHub key': 'Supprimer la clé GitHub enregistrée',
    'Save repository': 'Enregistrer le dépôt',
    'Pull updates': 'Récupérer les mises à jour',
    'Copy public page link': 'Copier le lien public',
    'Last pulled': 'Dernière récupération',
    'Published scripts': 'Scripts publiés',
    scripts: 'scripts',
    'Pull a repository to publish scripts.':
      'Récupérez un dépôt pour publier des scripts.',
    'Script repository settings saved': 'Configuration du dépôt enregistrée',
    'Failed to pull repository': 'Échec de la récupération du dépôt',
    'Scripts updated from repository': 'Scripts mis à jour depuis le dépôt',
    Saving: 'Enregistrement...',
    Pulling: 'Récupération...',
  },
  ja: {
    Scripts: 'スクリプト',
    'Unable to load script': 'スクリプトを読み込めません',
    'Unable to load scripts': 'スクリプトを読み込めません',
    'Name is required': '名前を入力してください',
    'Latest version': '最新バージョン',
    'Update available': '更新があります',
    'Up to date': '最新です',
    'GitHub release': 'GitHub リリース',
    'Linux / macOS': 'Linux / macOS',
    Windows: 'Windows',
    'Failed to delete': '削除に失敗しました',
    'Public scripts': '公開スクリプト',
    'Back to home': 'ホームに戻る',
    'Browse maintained setup scripts and copy the command for your system.':
      '管理されているセットアップスクリプトを確認し、システム用のコマンドをコピーできます。',
    'Open public script page': '公開スクリプトページを開く',
    'Unknown update time': '更新時刻不明',
    fetches: '取得',
    'Command copied': 'コマンドをコピーしました',
    'Unable to copy command': 'コマンドをコピーできません',
    'No scripts published yet': '公開スクリプトはありません',
    'Script repository': 'スクリプトリポジトリ',
    'Scripts are read from this repository and exposed on the public scripts page.':
      'スクリプトはこのリポジトリから読み込まれ、公開ページに表示されます。',
    'Repository URL': 'リポジトリ URL',
    Branch: 'ブランチ',
    'GitHub key (optional)': 'GitHub キー（任意）',
    'Key is configured; leave blank to keep it':
      'キーは設定済みです。保持する場合は空欄にしてください',
    'Only needed for a private repository':
      'プライベートリポジトリの場合のみ必要です',
    'Remove the stored GitHub key': '保存した GitHub キーを削除',
    'Save repository': 'リポジトリを保存',
    'Pull updates': '更新を取得',
    'Copy public page link': '公開ページのリンクをコピー',
    'Last pulled': '最終取得',
    'Published scripts': '公開スクリプト',
    scripts: 'スクリプト',
    'Pull a repository to publish scripts.':
      'リポジトリを取得するとスクリプトが公開されます。',
    'Script repository settings saved':
      'スクリプトリポジトリ設定を保存しました',
    'Failed to pull repository': 'リポジトリの取得に失敗しました',
    'Scripts updated from repository': 'リポジトリからスクリプトを更新しました',
    Saving: '保存中...',
    Pulling: '取得中...',
  },
  ru: {
    Scripts: 'Скрипты',
    'Unable to load script': 'Не удалось загрузить скрипт',
    'Unable to load scripts': 'Не удалось загрузить скрипты',
    'Name is required': 'Введите имя',
    'Latest version': 'Последняя версия',
    'Update available': 'Доступно обновление',
    'Up to date': 'Установлена последняя версия',
    'GitHub release': 'Релиз GitHub',
    'Linux / macOS': 'Linux / macOS',
    Windows: 'Windows',
    'Failed to delete': 'Не удалось удалить',
    'Public scripts': 'Публичные скрипты',
    'Back to home': 'На главную',
    'Browse maintained setup scripts and copy the command for your system.':
      'Просматривайте поддерживаемые скрипты установки и копируйте команду для своей системы.',
    'Open public script page': 'Открыть страницу скриптов',
    'Unknown update time': 'Время обновления неизвестно',
    fetches: 'загрузок',
    'Command copied': 'Команда скопирована',
    'Unable to copy command': 'Не удалось скопировать команду',
    'No scripts published yet': 'Скрипты пока не опубликованы',
    'Script repository': 'Репозиторий скриптов',
    'Scripts are read from this repository and exposed on the public scripts page.':
      'Скрипты читаются из этого репозитория и публикуются на открытой странице.',
    'Repository URL': 'URL репозитория',
    Branch: 'Ветка',
    'GitHub key (optional)': 'Ключ GitHub (необязательно)',
    'Key is configured; leave blank to keep it':
      'Ключ настроен; оставьте поле пустым, чтобы сохранить его',
    'Only needed for a private repository':
      'Нужен только для приватного репозитория',
    'Remove the stored GitHub key': 'Удалить сохранённый ключ GitHub',
    'Save repository': 'Сохранить репозиторий',
    'Pull updates': 'Получить обновления',
    'Copy public page link': 'Копировать ссылку',
    'Last pulled': 'Последняя загрузка',
    'Published scripts': 'Опубликованные скрипты',
    scripts: 'скриптов',
    'Pull a repository to publish scripts.':
      'Получите репозиторий, чтобы опубликовать скрипты.',
    'Script repository settings saved': 'Настройки репозитория сохранены',
    'Failed to pull repository': 'Не удалось получить репозиторий',
    'Scripts updated from repository': 'Скрипты обновлены из репозитория',
    Saving: 'Сохранение...',
    Pulling: 'Загрузка...',
  },
  vi: {
    Scripts: 'Tập lệnh',
    'Unable to load script': 'Không thể tải tập lệnh',
    'Unable to load scripts': 'Không thể tải danh sách tập lệnh',
    'Name is required': 'Cần nhập tên',
    'Latest version': 'Phiên bản mới nhất',
    'Update available': 'Có bản cập nhật',
    'Up to date': 'Đã cập nhật',
    'GitHub release': 'Bản phát hành GitHub',
    'Linux / macOS': 'Linux / macOS',
    Windows: 'Windows',
    'Failed to delete': 'Xóa thất bại',
    'Public scripts': 'Tập lệnh công khai',
    'Back to home': 'Về trang chủ',
    'Browse maintained setup scripts and copy the command for your system.':
      'Xem các tập lệnh cài đặt được duy trì và sao chép lệnh phù hợp với hệ thống của bạn.',
    'Open public script page': 'Mở trang tập lệnh công khai',
    'Unknown update time': 'Chưa biết thời gian cập nhật',
    fetches: 'lượt tải',
    'Command copied': 'Đã sao chép lệnh',
    'Unable to copy command': 'Không thể sao chép lệnh',
    'No scripts published yet': 'Chưa có tập lệnh được công bố',
    'Script repository': 'Kho tập lệnh',
    'Scripts are read from this repository and exposed on the public scripts page.':
      'Tập lệnh được đọc từ kho này và hiển thị trên trang công khai.',
    'Repository URL': 'URL kho',
    Branch: 'Nhánh',
    'GitHub key (optional)': 'Khóa GitHub (tùy chọn)',
    'Key is configured; leave blank to keep it':
      'Khóa đã được cấu hình; để trống để giữ nguyên',
    'Only needed for a private repository': 'Chỉ cần cho kho riêng tư',
    'Remove the stored GitHub key': 'Xóa khóa GitHub đã lưu',
    'Save repository': 'Lưu kho',
    'Pull updates': 'Kéo bản cập nhật',
    'Copy public page link': 'Sao chép liên kết công khai',
    'Last pulled': 'Lần kéo gần nhất',
    'Published scripts': 'Tập lệnh đã công bố',
    scripts: 'tập lệnh',
    'Pull a repository to publish scripts.': 'Kéo một kho để công bố tập lệnh.',
    'Script repository settings saved': 'Đã lưu cài đặt kho tập lệnh',
    'Failed to pull repository': 'Không thể kéo kho',
    'Scripts updated from repository': 'Đã cập nhật tập lệnh từ kho',
    Saving: 'Đang lưu...',
    Pulling: 'Đang kéo...',
  },
}

const experienceCopy = {
  en: {
    'Sign in to get started': 'Sign in to get started',
    'View access request status': 'View access request status',
    'Revise access request': 'Revise access request',
    'Request API access': 'Request API access',
    'Check API access status': 'Check API access status',
    'Open dashboard': 'Open dashboard',
    'Choose your client': 'Choose your client',
    'Create your first API key': 'Create your first API key',
    'Continue client setup': 'Continue client setup',
    'API access enabled': 'API access enabled',
    'Unable to load access status': 'Unable to load access status',
    'Access request rejected': 'Access request rejected',
    'Refresh account status': 'Refresh account status',
    'Not requested': 'Not requested',
    'API access': 'API access',
    'Not created': 'Not created',
    'First successful request': 'First successful request',
    'Not completed': 'Not completed',
    'Account status': 'Account status',
    'Reload account status': 'Reload account status',
    'Sign in and authorize': 'Sign in and authorize',
    'Use Pi or a client that supports LMM OAuth. No manual API key is needed.':
      'Use Pi or a client that supports LMM OAuth. No manual API key is needed.',
    'Choose a model': 'Choose a model',
    'After access approval, choose a model available to your account.':
      'After access approval, choose a model available to your account.',
    'Choose your client to get started. Available models and account pricing are shown after access approval.':
      'Choose your client to get started. Available models and account pricing are shown after access approval.',
    'Connection method': 'Connection method',
    'Pi / LMM OAuth': 'Pi / LMM OAuth',
    'Other clients / API key': 'Other clients / API key',
    'Unable to load announcements': 'Unable to load announcements',
    'Required announcement': 'Required announcement',
    'Announcement {{current}} of {{total}}':
      'Announcement {{current}} of {{total}}',
    'Read to the bottom, then confirm to continue.':
      'Read to the bottom, then confirm to continue.',
    'Announcement content': 'Announcement content',
    'Reading progress': 'Reading progress',
    'Unable to confirm reading. Please try again.':
      'Unable to confirm reading. Please try again.',
    'I have read and continue': 'I have read and continue',
    'Require reading before entering the console':
      'Require reading before entering the console',
    'Announcement reading status': 'Announcement reading status',
    'No required announcements': 'No required announcements',
    'Not read': 'Not read',
  },
  zh: {
    'Sign in to get started': '登录并开始使用',
    'View access request status': '查看申请状态',
    'Revise access request': '修改访问申请',
    'Request API access': '申请 API 访问权限',
    'Check API access status': '查看 API 访问状态',
    'Open dashboard': '进入控制台',
    'Choose your client': '选择使用的软件',
    'Create your first API key': '创建第一把 API Key',
    'Continue client setup': '继续配置客户端',
    'API access enabled': 'API 访问已开通',
    'Unable to load access status': '无法加载访问状态',
    'Access request rejected': '访问申请未通过',
    'Refresh account status': '刷新账号状态',
    'Not requested': '未申请',
    'API access': 'API 访问权限',
    'Not created': '未创建',
    'First successful request': '首次成功请求',
    'Not completed': '未完成',
    'Account status': '账号状态',
    'Reload account status': '重新加载账号状态',
    'Sign in and authorize': '登录并授权',
    'Use Pi or a client that supports LMM OAuth. No manual API key is needed.':
      '使用 Pi 或支持 LMM OAuth 的客户端，无需手动创建 API Key。',
    'Choose a model': '选择模型',
    'After access approval, choose a model available to your account.':
      '访问通过后，选择当前账号可用的模型。',
    'Choose your client to get started. Available models and account pricing are shown after access approval.':
      '选择使用的软件，开始接入。访问通过后可查看可用模型与账号实际价格。',
    'Connection method': '接入方式',
    'Pi / LMM OAuth': 'Pi / LMM OAuth',
    'Other clients / API key': '其他客户端 / API Key',
    'Unable to load announcements': '无法加载公告',
    'Required announcement': '必读公告',
    'Announcement {{current}} of {{total}}':
      '第 {{current}} 条，共 {{total}} 条公告',
    'Read to the bottom, then confirm to continue.':
      '请阅读至底部，然后确认继续。',
    'Announcement content': '公告内容',
    'Reading progress': '阅读进度',
    'Unable to confirm reading. Please try again.': '阅读确认失败，请重试。',
    'I have read and continue': '我已阅读并继续',
    'Require reading before entering the console': '进入控制台前必须阅读',
    'Announcement reading status': '公告阅读状态',
    'No required announcements': '暂无必读公告',
    'Not read': '未阅读',
  },
  'zh-TW': {
    'Sign in to get started': '登入並開始使用',
    'View access request status': '查看申請狀態',
    'Revise access request': '修改存取申請',
    'Request API access': '申請 API 存取權限',
    'Check API access status': '查看 API 存取狀態',
    'Open dashboard': '進入控制台',
    'Choose your client': '選擇使用的軟體',
    'Create your first API key': '建立第一把 API Key',
    'Continue client setup': '繼續設定用戶端',
    'API access enabled': 'API 存取已開通',
    'Unable to load access status': '無法載入存取狀態',
    'Access request rejected': '存取申請未通過',
    'Refresh account status': '重新整理帳號狀態',
    'Not requested': '未申請',
    'API access': 'API 存取權限',
    'Not created': '未建立',
    'First successful request': '首次成功請求',
    'Not completed': '未完成',
    'Account status': '帳號狀態',
    'Reload account status': '重新載入帳號狀態',
    'Sign in and authorize': '登入並授權',
    'Use Pi or a client that supports LMM OAuth. No manual API key is needed.':
      '使用 Pi 或支援 LMM OAuth 的用戶端，無須手動建立 API Key。',
    'Choose a model': '選擇模型',
    'After access approval, choose a model available to your account.':
      '存取通過後，選擇目前帳號可用的模型。',
    'Choose your client to get started. Available models and account pricing are shown after access approval.':
      '選擇使用的軟體，開始接入。存取通過後可查看可用模型與帳號實際價格。',
    'Connection method': '接入方式',
    'Pi / LMM OAuth': 'Pi / LMM OAuth',
    'Other clients / API key': '其他用戶端 / API Key',
    'Unable to load announcements': '無法載入公告',
    'Required announcement': '必讀公告',
    'Announcement {{current}} of {{total}}':
      '第 {{current}} 則，共 {{total}} 則公告',
    'Read to the bottom, then confirm to continue.':
      '請閱讀至底部，然後確認繼續。',
    'Announcement content': '公告內容',
    'Reading progress': '閱讀進度',
    'Unable to confirm reading. Please try again.': '閱讀確認失敗，請重試。',
    'I have read and continue': '我已閱讀並繼續',
    'Require reading before entering the console': '進入控制台前必須閱讀',
    'Announcement reading status': '公告閱讀狀態',
    'No required announcements': '暫無必讀公告',
    'Not read': '未閱讀',
  },
  fr: {
    'Sign in to get started': 'Se connecter pour commencer',
    'View access request status': 'Voir le statut de la demande',
    'Revise access request': 'Modifier la demande d’accès',
    'Request API access': 'Demander l’accès API',
    'Check API access status': 'Vérifier l’accès API',
    'Open dashboard': 'Ouvrir le tableau de bord',
    'Choose your client': 'Choisir votre application',
    'Create your first API key': 'Créer votre première clé API',
    'Continue client setup': 'Continuer la configuration',
    'API access enabled': 'Accès API activé',
    'Unable to load access status': 'Impossible de charger le statut',
    'Access request rejected': 'Demande d’accès refusée',
    'Refresh account status': 'Actualiser le statut du compte',
    'Not requested': 'Non demandé',
    'API access': 'Accès API',
    'Not created': 'Non créé',
    'First successful request': 'Première requête réussie',
    'Not completed': 'Non terminé',
    'Account status': 'Statut du compte',
    'Reload account status': 'Recharger le statut du compte',
    'Sign in and authorize': 'Se connecter et autoriser',
    'Use Pi or a client that supports LMM OAuth. No manual API key is needed.':
      'Utilisez Pi ou une application compatible LMM OAuth, sans créer de clé API manuellement.',
    'Choose a model': 'Choisir un modèle',
    'After access approval, choose a model available to your account.':
      'Après approbation de l’accès, choisissez un modèle disponible pour votre compte.',
    'Choose your client to get started. Available models and account pricing are shown after access approval.':
      'Choisissez votre application. Les modèles disponibles et vos tarifs s’affichent après approbation de l’accès.',
    'Connection method': 'Mode de connexion',
    'Pi / LMM OAuth': 'Pi / LMM OAuth',
    'Other clients / API key': 'Autres applications / clé API',
    'Unable to load announcements': 'Impossible de charger les annonces',
    'Required announcement': 'Annonce à lire obligatoirement',
    'Announcement {{current}} of {{total}}':
      'Annonce {{current}} sur {{total}}',
    'Read to the bottom, then confirm to continue.':
      'Lisez jusqu’en bas, puis confirmez pour continuer.',
    'Announcement content': 'Contenu de l’annonce',
    'Reading progress': 'Progression de lecture',
    'Unable to confirm reading. Please try again.':
      'Impossible de confirmer la lecture. Réessayez.',
    'I have read and continue': 'J’ai lu, continuer',
    'Require reading before entering the console':
      'Lecture obligatoire avant l’accès à la console',
    'Announcement reading status': 'Statut de lecture des annonces',
    'No required announcements': 'Aucune annonce obligatoire',
    'Not read': 'Non lu',
  },
  ja: {
    'Sign in to get started': 'ログインして始める',
    'View access request status': '申請状況を確認',
    'Revise access request': 'アクセス申請を修正',
    'Request API access': 'API アクセスを申請',
    'Check API access status': 'API アクセス状況を確認',
    'Open dashboard': 'ダッシュボードを開く',
    'Choose your client': '利用するアプリを選択',
    'Create your first API key': '最初の API キーを作成',
    'Continue client setup': 'アプリの設定を続ける',
    'API access enabled': 'API アクセス有効',
    'Unable to load access status': 'アクセス状況を読み込めません',
    'Access request rejected': 'アクセス申請は却下されました',
    'Refresh account status': 'アカウント状況を更新',
    'Not requested': '未申請',
    'API access': 'API アクセス',
    'Not created': '未作成',
    'First successful request': '最初のリクエスト成功',
    'Not completed': '未完了',
    'Account status': 'アカウント状況',
    'Reload account status': 'アカウント状況を再読み込み',
    'Sign in and authorize': 'ログインして認可',
    'Use Pi or a client that supports LMM OAuth. No manual API key is needed.':
      'Pi または LMM OAuth 対応アプリを利用します。API キーの手動作成は不要です。',
    'Choose a model': 'モデルを選択',
    'After access approval, choose a model available to your account.':
      'アクセス承認後、アカウントで利用可能なモデルを選択します。',
    'Choose your client to get started. Available models and account pricing are shown after access approval.':
      '利用するアプリを選んで始めましょう。アクセス承認後に利用可能なモデルとアカウントの料金を確認できます。',
    'Connection method': '接続方法',
    'Pi / LMM OAuth': 'Pi / LMM OAuth',
    'Other clients / API key': 'その他のアプリ / API キー',
    'Unable to load announcements': 'お知らせを読み込めません',
    'Required announcement': '必読のお知らせ',
    'Announcement {{current}} of {{total}}': 'お知らせ {{current}} / {{total}}',
    'Read to the bottom, then confirm to continue.':
      '最後まで読んでから確認して続行してください。',
    'Announcement content': 'お知らせの内容',
    'Reading progress': '読了までの進捗',
    'Unable to confirm reading. Please try again.':
      '既読の確認に失敗しました。再試行してください。',
    'I have read and continue': '読了して続行',
    'Require reading before entering the console':
      'コンソールに入る前に閲覧を必須にする',
    'Announcement reading status': 'お知らせの閲覧状況',
    'No required announcements': '必読のお知らせはありません',
    'Not read': '未読',
  },
  ru: {
    'Sign in to get started': 'Войти и начать',
    'View access request status': 'Статус заявки',
    'Revise access request': 'Изменить заявку',
    'Request API access': 'Запросить доступ к API',
    'Check API access status': 'Проверить доступ к API',
    'Open dashboard': 'Открыть панель',
    'Choose your client': 'Выбрать приложение',
    'Create your first API key': 'Создать первый ключ API',
    'Continue client setup': 'Продолжить настройку',
    'API access enabled': 'Доступ к API открыт',
    'Unable to load access status': 'Не удалось загрузить статус доступа',
    'Access request rejected': 'Заявка отклонена',
    'Refresh account status': 'Обновить статус аккаунта',
    'Not requested': 'Заявка не подана',
    'API access': 'Доступ к API',
    'Not created': 'Не создан',
    'First successful request': 'Первый успешный запрос',
    'Not completed': 'Не завершено',
    'Account status': 'Статус аккаунта',
    'Reload account status': 'Перезагрузить статус аккаунта',
    'Sign in and authorize': 'Войти и разрешить доступ',
    'Use Pi or a client that supports LMM OAuth. No manual API key is needed.':
      'Используйте Pi или приложение с поддержкой LMM OAuth. Создавать ключ API вручную не нужно.',
    'Choose a model': 'Выбрать модель',
    'After access approval, choose a model available to your account.':
      'После одобрения доступа выберите модель, доступную вашему аккаунту.',
    'Choose your client to get started. Available models and account pricing are shown after access approval.':
      'Выберите приложение. Доступные модели и тарифы аккаунта появятся после одобрения доступа.',
    'Connection method': 'Способ подключения',
    'Pi / LMM OAuth': 'Pi / LMM OAuth',
    'Other clients / API key': 'Другие приложения / ключ API',
    'Unable to load announcements': 'Не удалось загрузить объявления',
    'Required announcement': 'Обязательное объявление',
    'Announcement {{current}} of {{total}}':
      'Объявление {{current}} из {{total}}',
    'Read to the bottom, then confirm to continue.':
      'Прочитайте до конца и подтвердите, чтобы продолжить.',
    'Announcement content': 'Текст объявления',
    'Reading progress': 'Прогресс чтения',
    'Unable to confirm reading. Please try again.':
      'Не удалось подтвердить прочтение. Повторите попытку.',
    'I have read and continue': 'Прочитано, продолжить',
    'Require reading before entering the console':
      'Обязательное чтение перед входом в панель',
    'Announcement reading status': 'Статус прочтения объявлений',
    'No required announcements': 'Нет обязательных объявлений',
    'Not read': 'Не прочитано',
  },
  vi: {
    'Sign in to get started': 'Đăng nhập để bắt đầu',
    'View access request status': 'Xem trạng thái đăng ký',
    'Revise access request': 'Sửa yêu cầu truy cập',
    'Request API access': 'Yêu cầu quyền truy cập API',
    'Check API access status': 'Kiểm tra quyền truy cập API',
    'Open dashboard': 'Mở bảng điều khiển',
    'Choose your client': 'Chọn ứng dụng',
    'Create your first API key': 'Tạo khóa API đầu tiên',
    'Continue client setup': 'Tiếp tục cấu hình ứng dụng',
    'API access enabled': 'Đã mở quyền truy cập API',
    'Unable to load access status': 'Không tải được trạng thái truy cập',
    'Access request rejected': 'Yêu cầu truy cập bị từ chối',
    'Refresh account status': 'Làm mới trạng thái tài khoản',
    'Not requested': 'Chưa yêu cầu',
    'API access': 'Quyền truy cập API',
    'Not created': 'Chưa tạo',
    'First successful request': 'Yêu cầu thành công đầu tiên',
    'Not completed': 'Chưa hoàn tất',
    'Account status': 'Trạng thái tài khoản',
    'Reload account status': 'Tải lại trạng thái tài khoản',
    'Sign in and authorize': 'Đăng nhập và cấp quyền',
    'Use Pi or a client that supports LMM OAuth. No manual API key is needed.':
      'Dùng Pi hoặc ứng dụng hỗ trợ LMM OAuth. Không cần tạo khóa API thủ công.',
    'Choose a model': 'Chọn mô hình',
    'After access approval, choose a model available to your account.':
      'Sau khi được duyệt quyền truy cập, chọn mô hình khả dụng cho tài khoản.',
    'Choose your client to get started. Available models and account pricing are shown after access approval.':
      'Chọn ứng dụng để bắt đầu. Mô hình khả dụng và giá của tài khoản sẽ hiển thị sau khi quyền truy cập được duyệt.',
    'Connection method': 'Phương thức kết nối',
    'Pi / LMM OAuth': 'Pi / LMM OAuth',
    'Other clients / API key': 'Ứng dụng khác / khóa API',
    'Unable to load announcements': 'Không tải được thông báo',
    'Required announcement': 'Thông báo bắt buộc đọc',
    'Announcement {{current}} of {{total}}':
      'Thông báo {{current}} / {{total}}',
    'Read to the bottom, then confirm to continue.':
      'Đọc đến cuối rồi xác nhận để tiếp tục.',
    'Announcement content': 'Nội dung thông báo',
    'Reading progress': 'Tiến độ đọc',
    'Unable to confirm reading. Please try again.':
      'Không xác nhận được việc đọc. Vui lòng thử lại.',
    'I have read and continue': 'Tôi đã đọc, tiếp tục',
    'Require reading before entering the console':
      'Bắt buộc đọc trước khi vào bảng điều khiển',
    'Announcement reading status': 'Trạng thái đọc thông báo',
    'No required announcements': 'Không có thông báo bắt buộc đọc',
    'Not read': 'Chưa đọc',
  },
}

const acquisitionCopy = {
  en: {
    'Source privacy': 'Source privacy',
    'Allow source analytics? We retain source labels and entry pages for 90 days, and account attribution for 365 days. No API keys, messages, IP addresses or device fingerprints. Optional; this does not affect access.':
      'Allow source analytics? We retain source labels and entry pages for 90 days, and account attribution for 365 days. No API keys, messages, IP addresses or device fingerprints. Optional; this does not affect access.',
    'Allow source analytics': 'Allow source analytics',
    'Do not collect': 'Do not collect',
    'Unable to save promotion link': 'Unable to save promotion link',
    'User acquisition': 'User acquisition',
    'Registration-cohort attribution. Payments are observed through the report update time, grouped by currency. Attribution does not prove causation. API success and retention are not yet available.':
      'Registration-cohort attribution. Payments are observed through the report update time, grouped by currency. Attribution does not prove causation. API success and retention are not yet available.',
    'Past {{days}} days': 'Past {{days}} days',
    'Reload report': 'Reload report',
    'Source coverage: {{identified}} / {{total}} registered accounts':
      'Source coverage: {{identified}} / {{total}} registered accounts',
    'Direct / unknown source': 'Direct / unknown source',
    'No accounts registered in this period.':
      'No accounts registered in this period.',
    'Unknown means no reliable source was recorded; it does not mean the address was typed manually.':
      'Unknown means no reliable source was recorded; it does not mean the address was typed manually.',
    'Promotion links': 'Promotion links',
    'Each link keeps a stable identity. Create a new link for a different campaign; renaming or archiving does not rewrite past attribution.':
      'Each link keeps a stable identity. Create a new link for a different campaign; renaming or archiving does not rewrite past attribution.',
    'Target page': 'Target page',
    'Create promotion link': 'Create promotion link',
    'No promotion links yet.': 'No promotion links yet.',
    'QR code': 'QR code',
    'Test link': 'Test link',
    Registrations: 'Registrations',
    'Actual payments': 'Actual payments',
    'Net payments': 'Net payments',
    'Source platform': 'Source platform',
    'Promotion method': 'Promotion method',
    Campaign: 'Campaign',
    'Content label': 'Content label',
    'View acquisition summaries': 'View acquisition summaries',
    'View aggregate channel and campaign results.':
      'View aggregate channel and campaign results.',
    'Manage promotion links': 'Manage promotion links',
    'Create and archive promotion links.':
      'Create and archive promotion links.',
    'View acquisition account details': 'View acquisition account details',
    'View account-level source records.': 'View account-level source records.',
    'Export acquisition details': 'Export acquisition details',
    'Export account-level source records.':
      'Export account-level source records.',
    'Historical source not recorded': 'Historical source not recorded',
    'Data collection started': 'Data collection started',
    'Operations analytics': 'Operations analytics',
    'Attribution lookback: {{days}} days':
      'Attribution lookback: {{days}} days',
    '{{count}} payment records have incomplete amount or currency evidence and are excluded.':
      '{{count}} payment records have incomplete amount or currency evidence and are excluded.',
    '{{count}} payment records have incomplete settlement evidence and are excluded.':
      '{{count}} payment records have incomplete settlement evidence and are excluded.',
    'Promotion link marker': 'Promotion link marker',
    'Campaign parameters': 'Campaign parameters',
    'Browser-provided source website': 'Browser-provided source website',
    'No identifiable source': 'No identifiable source',
    'Promotion markers identify the link used, not necessarily the platform where it was seen.':
      'Promotion markers identify the link used, not necessarily the platform where it was seen.',
  },
  zh: {
    'Source privacy': '来源统计隐私',
    'Allow source analytics? We retain source labels and entry pages for 90 days, and account attribution for 365 days. No API keys, messages, IP addresses or device fingerprints. Optional; this does not affect access.':
      '允许统计访问来源吗？来源标记和入口页面保留 90 天，账号来源归属保留 365 天。不收集 API Key、消息、IP 地址或设备指纹。是否允许不影响使用。',
    'Allow source analytics': '允许来源统计',
    'Do not collect': '不收集',
    'Unable to save promotion link': '无法保存推广链接',
    'User acquisition': '用户来源',
    'Registration-cohort attribution. Payments are observed through the report update time, grouped by currency. Attribution does not prove causation. API success and retention are not yet available.':
      '本报表按所选时段注册的账号归属来源，付款统计截至报表更新时间，并按币种区分。来源归属不代表因果关系。成功接入和留存数据暂未提供。',
    'Past {{days}} days': '近 {{days}} 天',
    'Reload report': '重新加载报表',
    'Source coverage: {{identified}} / {{total}} registered accounts':
      '来源识别覆盖：{{identified}} / {{total}} 个注册账号',
    'Direct / unknown source': '直接访问 / 来源未知',
    'No accounts registered in this period.': '此期间没有注册账号。',
    'Unknown means no reliable source was recorded; it does not mean the address was typed manually.':
      '未知表示没有可靠的来源记录，不代表用户一定手动输入了网址。',
    'Promotion links': '推广链接',
    'Each link keeps a stable identity. Create a new link for a different campaign; renaming or archiving does not rewrite past attribution.':
      '每条链接都有固定标识。不同活动请新建链接；重命名或归档不会改写历史来源。',
    'Target page': '目标页面',
    'Create promotion link': '生成推广链接',
    'No promotion links yet.': '尚未创建推广链接。',
    'QR code': '二维码',
    'Test link': '测试此链接',
    Registrations: '注册账号数',
    'Actual payments': '实际支付',
    'Net payments': '净实付',
    'Source platform': '来源平台',
    'Promotion method': '推广方式',
    Campaign: '推广活动',
    'Content label': '推广内容名称',
    'View acquisition summaries': '查看来源汇总',
    'View aggregate channel and campaign results.':
      '查看渠道和活动的汇总结果。',
    'Manage promotion links': '管理推广链接',
    'Create and archive promotion links.': '创建和归档推广链接。',
    'View acquisition account details': '查看账号来源明细',
    'View account-level source records.': '查看账号级来源记录。',
    'Export acquisition details': '导出账号来源明细',
    'Export account-level source records.': '导出账号级来源记录。',
    'Historical source not recorded': '历史来源未记录',
    'Data collection started': '统计开始时间',
    'Operations analytics': '运营分析',
    'Attribution lookback: {{days}} days': '来源归属回看：{{days}} 天',
    '{{count}} payment records have incomplete amount or currency evidence and are excluded.':
      '{{count}} 条付款记录缺少完整金额或币种依据，未计入统计。',
    '{{count}} payment records have incomplete settlement evidence and are excluded.':
      '{{count}} 条付款记录的结算依据不完整，未计入统计。',
    'Promotion link marker': '推广链接标记',
    'Campaign parameters': '推广活动参数',
    'Browser-provided source website': '浏览器提供的来源网站',
    'No identifiable source': '未获取到可识别的来源',
    'Promotion markers identify the link used, not necessarily the platform where it was seen.':
      '推广标记只能说明使用了这条链接，不代表一定在最初投放的平台看到了它。',
  },
  'zh-TW': {
    'Source privacy': '來源統計隱私',
    'Allow source analytics? We retain source labels and entry pages for 90 days, and account attribution for 365 days. No API keys, messages, IP addresses or device fingerprints. Optional; this does not affect access.':
      '允許統計造訪來源嗎？來源標記和入口頁面保留 90 天，帳號來源歸屬保留 365 天。不收集 API Key、訊息、IP 位址或裝置指紋。是否允許不影響使用。',
    'Allow source analytics': '允許來源統計',
    'Do not collect': '不收集',
    'Unable to save promotion link': '無法儲存推廣連結',
    'User acquisition': '使用者來源',
    'Registration-cohort attribution. Payments are observed through the report update time, grouped by currency. Attribution does not prove causation. API success and retention are not yet available.':
      '本報表按所選時段註冊的帳號歸屬來源，付款統計截至報表更新時間，並按幣別區分。來源歸屬不代表因果關係。成功接入和留存資料暫未提供。',
    'Past {{days}} days': '近 {{days}} 天',
    'Reload report': '重新載入報表',
    'Source coverage: {{identified}} / {{total}} registered accounts':
      '來源識別涵蓋：{{identified}} / {{total}} 個註冊帳號',
    'Direct / unknown source': '直接造訪 / 來源未知',
    'No accounts registered in this period.': '此期間沒有註冊帳號。',
    'Unknown means no reliable source was recorded; it does not mean the address was typed manually.':
      '未知表示沒有可靠的來源記錄，不代表使用者一定手動輸入了網址。',
    'Promotion links': '推廣連結',
    'Each link keeps a stable identity. Create a new link for a different campaign; renaming or archiving does not rewrite past attribution.':
      '每條連結都有固定識別碼。不同活動請新建連結；重新命名或封存不會改寫歷史來源。',
    'Target page': '目標頁面',
    'Create promotion link': '產生推廣連結',
    'No promotion links yet.': '尚未建立推廣連結。',
    'QR code': 'QR 碼',
    'Test link': '測試此連結',
    Registrations: '註冊帳號數',
    'Actual payments': '實際付款',
    'Net payments': '淨實付',
    'Source platform': '來源平台',
    'Promotion method': '推廣方式',
    Campaign: '推廣活動',
    'Content label': '推廣內容名稱',
    'View acquisition summaries': '查看來源彙總',
    'View aggregate channel and campaign results.':
      '查看管道和活動的彙總結果。',
    'Manage promotion links': '管理推廣連結',
    'Create and archive promotion links.': '建立和封存推廣連結。',
    'View acquisition account details': '查看帳號來源明細',
    'View account-level source records.': '查看帳號層級來源記錄。',
    'Export acquisition details': '匯出帳號來源明細',
    'Export account-level source records.': '匯出帳號層級來源記錄。',
    'Historical source not recorded': '歷史來源未記錄',
    'Data collection started': '統計開始時間',
    'Operations analytics': '營運分析',
    'Attribution lookback: {{days}} days': '來源歸屬回溯：{{days}} 天',
    '{{count}} payment records have incomplete amount or currency evidence and are excluded.':
      '{{count}} 筆付款記錄缺少完整金額或幣別依據，未計入統計。',
    '{{count}} payment records have incomplete settlement evidence and are excluded.':
      '{{count}} 筆付款記錄的結算依據不完整，未計入統計。',
    'Promotion link marker': '推廣連結標記',
    'Campaign parameters': '推廣活動參數',
    'Browser-provided source website': '瀏覽器提供的來源網站',
    'No identifiable source': '未取得可識別的來源',
    'Promotion markers identify the link used, not necessarily the platform where it was seen.':
      '推廣標記只能說明使用了這條連結，不代表一定在最初投放的平台看到了它。',
  },
  fr: {
    'Source privacy': 'Confidentialité des sources',
    'Allow source analytics? We retain source labels and entry pages for 90 days, and account attribution for 365 days. No API keys, messages, IP addresses or device fingerprints. Optional; this does not affect access.':
      'Autoriser les statistiques de provenance ? Les sources et pages d’entrée sont conservées 90 jours, l’attribution des comptes 365 jours. Aucune clé API, aucun message, aucune adresse IP ni empreinte d’appareil. Facultatif, sans effet sur l’accès.',
    'Allow source analytics': 'Autoriser les statistiques',
    'Do not collect': 'Ne pas collecter',
    'Unable to save promotion link': 'Impossible d’enregistrer le lien',
    'User acquisition': 'Provenance des utilisateurs',
    'Registration-cohort attribution. Payments are observed through the report update time, grouped by currency. Attribution does not prove causation. API success and retention are not yet available.':
      'Attribution selon la période d’inscription. Les paiements sont observés jusqu’à la mise à jour, par devise. L’attribution ne prouve pas la causalité. L’activation API et la rétention ne sont pas encore disponibles.',
    'Past {{days}} days': '{{days}} derniers jours',
    'Reload report': 'Recharger le rapport',
    'Source coverage: {{identified}} / {{total}} registered accounts':
      'Sources identifiées : {{identified}} / {{total}} comptes inscrits',
    'Direct / unknown source': 'Accès direct / source inconnue',
    'No accounts registered in this period.':
      'Aucune inscription sur cette période.',
    'Unknown means no reliable source was recorded; it does not mean the address was typed manually.':
      'Une source inconnue indique l’absence de données fiables, pas forcément une adresse saisie manuellement.',
    'Promotion links': 'Liens de promotion',
    'Each link keeps a stable identity. Create a new link for a different campaign; renaming or archiving does not rewrite past attribution.':
      'Chaque lien conserve son identifiant. Créez un nouveau lien pour une autre campagne ; renommer ou archiver ne modifie pas l’historique.',
    'Target page': 'Page de destination',
    'Create promotion link': 'Créer un lien de promotion',
    'No promotion links yet.': 'Aucun lien de promotion.',
    'QR code': 'Code QR',
    'Test link': 'Tester ce lien',
    Registrations: 'Inscriptions',
    'Actual payments': 'Paiements réels',
    'Net payments': 'Paiements nets',
    'Source platform': 'Plateforme source',
    'Promotion method': 'Méthode de promotion',
    Campaign: 'Campagne',
    'Content label': 'Libellé du contenu',
    'View acquisition summaries': 'Voir les résumés des sources',
    'View aggregate channel and campaign results.':
      'Voir les résultats agrégés par canal et campagne.',
    'Manage promotion links': 'Gérer les liens de promotion',
    'Create and archive promotion links.':
      'Créer et archiver les liens de promotion.',
    'View acquisition account details': 'Voir les sources par compte',
    'View account-level source records.':
      'Voir les enregistrements de source par compte.',
    'Export acquisition details': 'Exporter les détails des sources',
    'Export account-level source records.':
      'Exporter les enregistrements de source par compte.',
    'Historical source not recorded': 'Source historique non enregistrée',
    'Data collection started': 'Début de la collecte',
    'Operations analytics': 'Analyse opérationnelle',
    'Attribution lookback: {{days}} days':
      'Fenêtre d’attribution : {{days}} jours',
    '{{count}} payment records have incomplete amount or currency evidence and are excluded.':
      '{{count}} paiements sont exclus faute de montant ou de devise fiable.',
    '{{count}} payment records have incomplete settlement evidence and are excluded.':
      '{{count}} paiements sont exclus faute de justificatifs de règlement complets.',
    'Promotion link marker': 'Marqueur de lien promotionnel',
    'Campaign parameters': 'Paramètres de campagne',
    'Browser-provided source website': 'Site source fourni par le navigateur',
    'No identifiable source': 'Aucune source identifiable',
    'Promotion markers identify the link used, not necessarily the platform where it was seen.':
      'Les marqueurs identifient le lien utilisé, pas nécessairement la plateforme où il a été vu.',
  },
  ja: {
    'Source privacy': '流入元データのプライバシー',
    'Allow source analytics? We retain source labels and entry pages for 90 days, and account attribution for 365 days. No API keys, messages, IP addresses or device fingerprints. Optional; this does not affect access.':
      '流入元の分析を許可しますか？流入元ラベルと入口ページは90日間、アカウントとの関連付けは365日間保持します。APIキー、メッセージ、IPアドレス、端末の識別情報は収集しません。任意であり、利用権限に影響しません。',
    'Allow source analytics': '流入元の分析を許可',
    'Do not collect': '収集しない',
    'Unable to save promotion link': '紹介リンクを保存できません',
    'User acquisition': 'ユーザーの流入元',
    'Registration-cohort attribution. Payments are observed through the report update time, grouped by currency. Attribution does not prove causation. API success and retention are not yet available.':
      '選択期間に登録したアカウントの流入元を集計します。支払いは更新時点まで通貨別に集計します。関連付けは因果関係を証明するものではありません。API利用開始と継続利用のデータは未提供です。',
    'Past {{days}} days': '過去{{days}}日間',
    'Reload report': 'レポートを再読み込み',
    'Source coverage: {{identified}} / {{total}} registered accounts':
      '流入元を識別：登録{{total}}アカウント中{{identified}}',
    'Direct / unknown source': '直接アクセス / 流入元不明',
    'No accounts registered in this period.':
      'この期間に登録したアカウントはありません。',
    'Unknown means no reliable source was recorded; it does not mean the address was typed manually.':
      '不明は信頼できる流入元の記録がないことを意味し、URLを手入力したとは限りません。',
    'Promotion links': '紹介リンク',
    'Each link keeps a stable identity. Create a new link for a different campaign; renaming or archiving does not rewrite past attribution.':
      '各リンクの識別子は固定です。別のキャンペーンには新しいリンクを作成してください。名前変更やアーカイブで過去の関連付けは変わりません。',
    'Target page': 'リンク先ページ',
    'Create promotion link': '紹介リンクを作成',
    'No promotion links yet.': '紹介リンクはまだありません。',
    'QR code': 'QRコード',
    'Test link': 'リンクをテスト',
    Registrations: '登録数',
    'Actual payments': '実際の支払額',
    'Net payments': '純支払額',
    'Source platform': '流入元プラットフォーム',
    'Promotion method': '紹介方法',
    Campaign: 'キャンペーン',
    'Content label': 'コンテンツ名',
    'View acquisition summaries': '流入元の集計を表示',
    'View aggregate channel and campaign results.':
      'チャネルとキャンペーンの集計結果を表示します。',
    'Manage promotion links': '紹介リンクを管理',
    'Create and archive promotion links.':
      '紹介リンクを作成・アーカイブします。',
    'View acquisition account details': 'アカウント別の流入元を表示',
    'View account-level source records.':
      'アカウント単位の流入元の記録を表示します。',
    'Export acquisition details': '流入元の明細をエクスポート',
    'Export account-level source records.':
      'アカウント単位の流入元の記録をエクスポートします。',
    'Historical source not recorded': '過去の流入元は未記録',
    'Data collection started': '集計開始日時',
    'Operations analytics': '運営分析',
    'Attribution lookback: {{days}} days': '流入元の参照期間：{{days}}日',
    '{{count}} payment records have incomplete amount or currency evidence and are excluded.':
      '金額または通貨の根拠が不十分な支払い{{count}}件を集計から除外しています。',
    '{{count}} payment records have incomplete settlement evidence and are excluded.':
      '決済の根拠が不十分な支払い{{count}}件を集計から除外しています。',
    'Promotion link marker': '紹介リンクの識別子',
    'Campaign parameters': 'キャンペーンパラメーター',
    'Browser-provided source website': 'ブラウザーが提供した参照元サイト',
    'No identifiable source': '識別できる流入元なし',
    'Promotion markers identify the link used, not necessarily the platform where it was seen.':
      '紹介マーカーは使用されたリンクを示しますが、見た場所が元の掲載先とは限りません。',
  },
  ru: {
    'Source privacy': 'Конфиденциальность источников',
    'Allow source analytics? We retain source labels and entry pages for 90 days, and account attribution for 365 days. No API keys, messages, IP addresses or device fingerprints. Optional; this does not affect access.':
      'Разрешить аналитику источников? Метки источников и страницы входа хранятся 90 дней, атрибуция аккаунтов — 365 дней. Ключи API, сообщения, IP-адреса и отпечатки устройств не собираются. Выбор не влияет на доступ.',
    'Allow source analytics': 'Разрешить аналитику',
    'Do not collect': 'Не собирать',
    'Unable to save promotion link': 'Не удалось сохранить ссылку',
    'User acquisition': 'Источники пользователей',
    'Registration-cohort attribution. Payments are observed through the report update time, grouped by currency. Attribution does not prove causation. API success and retention are not yet available.':
      'Атрибуция по периоду регистрации. Платежи учитываются до обновления отчёта, отдельно по валютам. Атрибуция не доказывает причинную связь. Данные подключения API и удержания пока недоступны.',
    'Past {{days}} days': 'Последние {{days}} дн.',
    'Reload report': 'Перезагрузить отчёт',
    'Source coverage: {{identified}} / {{total}} registered accounts':
      'Источники определены: {{identified}} из {{total}} зарегистрированных аккаунтов',
    'Direct / unknown source': 'Прямой вход / источник неизвестен',
    'No accounts registered in this period.': 'За этот период нет регистраций.',
    'Unknown means no reliable source was recorded; it does not mean the address was typed manually.':
      'Неизвестный источник означает отсутствие надёжной записи, а не обязательно ручной ввод адреса.',
    'Promotion links': 'Рекламные ссылки',
    'Each link keeps a stable identity. Create a new link for a different campaign; renaming or archiving does not rewrite past attribution.':
      'Идентификатор ссылки постоянен. Для другой кампании создайте новую ссылку. Переименование и архивирование не меняют историю.',
    'Target page': 'Целевая страница',
    'Create promotion link': 'Создать рекламную ссылку',
    'No promotion links yet.': 'Рекламных ссылок пока нет.',
    'QR code': 'QR-код',
    'Test link': 'Проверить ссылку',
    Registrations: 'Регистрации',
    'Actual payments': 'Фактические платежи',
    'Net payments': 'Чистые платежи',
    'Source platform': 'Платформа-источник',
    'Promotion method': 'Способ продвижения',
    Campaign: 'Кампания',
    'Content label': 'Название материала',
    'View acquisition summaries': 'Просмотр сводки источников',
    'View aggregate channel and campaign results.':
      'Просмотр сводных результатов каналов и кампаний.',
    'Manage promotion links': 'Управление рекламными ссылками',
    'Create and archive promotion links.':
      'Создание и архивирование рекламных ссылок.',
    'View acquisition account details': 'Просмотр источников аккаунтов',
    'View account-level source records.':
      'Просмотр записей источников отдельных аккаунтов.',
    'Export acquisition details': 'Экспорт сведений об источниках',
    'Export account-level source records.':
      'Экспорт записей источников отдельных аккаунтов.',
    'Historical source not recorded': 'Исторический источник не записан',
    'Data collection started': 'Начало сбора данных',
    'Operations analytics': 'Операционная аналитика',
    'Attribution lookback: {{days}} days': 'Окно атрибуции: {{days}} дн.',
    '{{count}} payment records have incomplete amount or currency evidence and are excluded.':
      'Исключено {{count}} платежей с неполными данными о сумме или валюте.',
    '{{count}} payment records have incomplete settlement evidence and are excluded.':
      'Исключено {{count}} платежей с неполными данными о расчётах.',
    'Promotion link marker': 'Метка рекламной ссылки',
    'Campaign parameters': 'Параметры кампании',
    'Browser-provided source website': 'Сайт-источник, указанный браузером',
    'No identifiable source': 'Источник не определён',
    'Promotion markers identify the link used, not necessarily the platform where it was seen.':
      'Метки определяют использованную ссылку, но не обязательно площадку, где её увидели.',
  },
  vi: {
    'Source privacy': 'Quyền riêng tư nguồn truy cập',
    'Allow source analytics? We retain source labels and entry pages for 90 days, and account attribution for 365 days. No API keys, messages, IP addresses or device fingerprints. Optional; this does not affect access.':
      'Cho phép phân tích nguồn truy cập? Nhãn nguồn và trang vào được lưu 90 ngày, thông tin quy nguồn tài khoản 365 ngày. Không thu thập khóa API, tin nhắn, địa chỉ IP hay dấu vân tay thiết bị. Lựa chọn không ảnh hưởng quyền truy cập.',
    'Allow source analytics': 'Cho phép phân tích nguồn',
    'Do not collect': 'Không thu thập',
    'Unable to save promotion link': 'Không lưu được liên kết quảng bá',
    'User acquisition': 'Nguồn người dùng',
    'Registration-cohort attribution. Payments are observed through the report update time, grouped by currency. Attribution does not prove causation. API success and retention are not yet available.':
      'Quy nguồn theo nhóm tài khoản đăng ký trong kỳ. Thanh toán được tính đến thời điểm cập nhật, tách theo tiền tệ. Quy nguồn không chứng minh quan hệ nhân quả. Chưa có dữ liệu kết nối API thành công và duy trì sử dụng.',
    'Past {{days}} days': '{{days}} ngày qua',
    'Reload report': 'Tải lại báo cáo',
    'Source coverage: {{identified}} / {{total}} registered accounts':
      'Đã xác định nguồn: {{identified}} / {{total}} tài khoản đăng ký',
    'Direct / unknown source': 'Truy cập trực tiếp / nguồn chưa rõ',
    'No accounts registered in this period.':
      'Không có tài khoản đăng ký trong kỳ.',
    'Unknown means no reliable source was recorded; it does not mean the address was typed manually.':
      'Nguồn chưa rõ nghĩa là không có dữ liệu đáng tin cậy, không nhất thiết do nhập địa chỉ thủ công.',
    'Promotion links': 'Liên kết quảng bá',
    'Each link keeps a stable identity. Create a new link for a different campaign; renaming or archiving does not rewrite past attribution.':
      'Mỗi liên kết có mã cố định. Hãy tạo liên kết mới cho chiến dịch khác; đổi tên hoặc lưu trữ không thay đổi nguồn đã ghi nhận.',
    'Target page': 'Trang đích',
    'Create promotion link': 'Tạo liên kết quảng bá',
    'No promotion links yet.': 'Chưa có liên kết quảng bá.',
    'QR code': 'Mã QR',
    'Test link': 'Kiểm tra liên kết',
    Registrations: 'Số đăng ký',
    'Actual payments': 'Thanh toán thực tế',
    'Net payments': 'Thanh toán ròng',
    'Source platform': 'Nền tảng nguồn',
    'Promotion method': 'Phương thức quảng bá',
    Campaign: 'Chiến dịch',
    'Content label': 'Tên nội dung quảng bá',
    'View acquisition summaries': 'Xem tổng hợp nguồn',
    'View aggregate channel and campaign results.':
      'Xem kết quả tổng hợp theo kênh và chiến dịch.',
    'Manage promotion links': 'Quản lý liên kết quảng bá',
    'Create and archive promotion links.': 'Tạo và lưu trữ liên kết quảng bá.',
    'View acquisition account details': 'Xem chi tiết nguồn tài khoản',
    'View account-level source records.':
      'Xem bản ghi nguồn của từng tài khoản.',
    'Export acquisition details': 'Xuất chi tiết nguồn',
    'Export account-level source records.':
      'Xuất bản ghi nguồn của từng tài khoản.',
    'Historical source not recorded': 'Chưa ghi nhận nguồn trước đây',
    'Data collection started': 'Bắt đầu thu thập dữ liệu',
    'Operations analytics': 'Phân tích vận hành',
    'Attribution lookback: {{days}} days': 'Khoảng truy nguồn: {{days}} ngày',
    '{{count}} payment records have incomplete amount or currency evidence and are excluded.':
      'Đã loại {{count}} bản ghi thanh toán do thiếu bằng chứng về số tiền hoặc tiền tệ.',
    '{{count}} payment records have incomplete settlement evidence and are excluded.':
      'Đã loại {{count}} bản ghi thanh toán do thiếu bằng chứng quyết toán đầy đủ.',
    'Promotion link marker': 'Nhãn liên kết quảng bá',
    'Campaign parameters': 'Tham số chiến dịch',
    'Browser-provided source website': 'Trang nguồn do trình duyệt cung cấp',
    'No identifiable source': 'Không xác định được nguồn',
    'Promotion markers identify the link used, not necessarily the platform where it was seen.':
      'Nhãn quảng bá chỉ xác định liên kết đã dùng, không nhất thiết là nền tảng nơi người dùng thấy liên kết.',
  },
}

const acquisitionActivityCopy = {
  en: {
    'API activation and retention': 'API activation and retention',
    'Only new accounts within this observation period that explicitly allowed expanded analytics are included. Success requires a delivered text API response with output and no recorded stream or request failure. Creating a key or authorizing OAuth is not success.':
      'Only new accounts within this observation period that explicitly allowed expanded analytics are included. Success requires a delivered text API response with output and no recorded stream or request failure. Creating a key or authorizing OAuth is not success.',
    'Day 7 retention uses UTC days: accounts with another qualifying request on the seventh day after their first observed success, divided by successful accounts whose full seventh day has been processed. Accounts still under observation are excluded from that denominator.':
      'Day 7 retention uses UTC days: accounts with another qualifying request on the seventh day after their first observed success, divided by successful accounts whose full seventh day has been processed. Accounts still under observation are excluded from that denominator.',
    'Activity statistics have not started yet.':
      'Activity statistics have not started yet.',
    'Processed through': 'Processed through',
    'No eligible accounts in this registration period.':
      'No eligible accounts in this registration period.',
    'Unable to rebuild activity statistics. Please retry.':
      'Unable to rebuild activity statistics. Please retry.',
    'Channel accounts': 'Channel accounts',
    'Reload source records': 'Reload source records',
    'No accounts match this source and registration period.':
      'No accounts match this source and registration period.',
    'Source and conversion': 'Source and conversion',
    Registered: 'Registered',
    'First observed source': 'First observed source',
    'Registration source': 'Registration source',
    'First observed successful API response':
      'First observed successful API response',
    'Only qualifying API success records are shown here. Missing records do not prove that an account never used the API.':
      'Only qualifying API success records are shown here. Missing records do not prove that an account never used the API.',
    'Return to channel report': 'Return to channel report',
    'Recent source observations': 'Recent source observations',
    'No retained source observations are available for this account.':
      'No retained source observations are available for this account.',
    'Allow analysis of sources, registrations, successful API use and payments? Entry records are kept for 90 days; account attribution and daily activity for 365 days. No API keys, messages, IP addresses or device fingerprints. Optional; access is unchanged.':
      'Allow analysis of sources, registrations, successful API use and payments? Entry records are kept for 90 days; account attribution and daily activity for 365 days. No API keys, messages, IP addresses or device fingerprints. Optional; access is unchanged.',
    'Unable to save privacy settings. Please retry to stop server-side analytics.':
      'Unable to save privacy settings. Please retry to stop server-side analytics.',
    'Registration-cohort attribution. Payments are observed through the report update time, grouped by currency. Attribution does not prove causation. API activity has its own processing watermark below.':
      'Registration-cohort attribution. Payments are observed through the report update time, grouped by currency. Attribution does not prove causation. API activity has its own processing watermark below.',
    'Eligible accounts': 'Eligible accounts',
    'Observed successful accounts': 'Observed successful accounts',
    'Under observation': 'Under observation',
    'Day 7 retained / eligible': 'Day 7 retained / eligible',
    'Day 7 retention': 'Day 7 retention',
    'Operational log coverage is incomplete. Retention rates are unavailable.':
      'Operational log coverage is incomplete. Retention rates are unavailable.',
    'Activity statistics are catching up or temporarily unavailable. Displayed successes are confirmed observations, not a complete current total.':
      'Activity statistics are catching up or temporarily unavailable. Displayed successes are confirmed observations, not a complete current total.',
    'Rebuilding activity statistics': 'Rebuilding activity statistics',
    'Rebuild activity statistics': 'Rebuild activity statistics',
    'Successful API response observed': 'Successful API response observed',
    'No successful API response observed yet':
      'No successful API response observed yet',
    'Attributed from an earlier source observation':
      'Attributed from an earlier source observation',
    'Source observed in the current visit':
      'Source observed in the current visit',
    'Export summary': 'Export summary',
    'Recorded attribution windows: {{days}} days':
      'Recorded attribution windows: {{days}} days',
    'Lookback days for new registrations':
      'Lookback days for new registrations',
    'Changing the default does not rewrite existing source attributions.':
      'Changing the default does not rewrite existing source attributions.',
    'Operational log coverage is incomplete. Affected accounts are excluded from retention rates.':
      'Operational log coverage is incomplete. Affected accounts are excluded from retention rates.',
    'Missing retention data': 'Missing retention data',
    'Counting rules: text API, UTC day 7':
      'Counting rules: text API, UTC day 7',
  },
  zh: {
    'API activation and retention': 'API 接入与留存',
    'Only new accounts within this observation period that explicitly allowed expanded analytics are included. Success requires a delivered text API response with output and no recorded stream or request failure. Creating a key or authorizing OAuth is not success.':
      '仅统计观察期内新注册、且明确同意扩展统计的账号。成功调用必须已返回文本 API 响应、包含输出，且没有记录到请求或流式错误。创建 Key 或完成 OAuth 授权不算接入成功。',
    'Day 7 retention uses UTC days: accounts with another qualifying request on the seventh day after their first observed success, divided by successful accounts whose full seventh day has been processed. Accounts still under observation are excluded from that denominator.':
      '7 日留存按 UTC 自然日计算：首次观察到成功调用后的第 7 天仍有符合条件调用的账号数，除以第 7 天已完整统计的成功接入账号数。仍在观察中的账号不计入分母。',
    'Activity statistics have not started yet.': '调用统计尚未开始。',
    'Processed through': '已处理至',
    'No eligible accounts in this registration period.':
      '此注册期间没有符合统计条件的账号。',
    'Unable to rebuild activity statistics. Please retry.':
      '无法重建调用统计，请重试。',
    'Channel accounts': '渠道账号',
    'Reload source records': '重新加载来源记录',
    'No accounts match this source and registration period.':
      '此来源和注册期间没有匹配账号。',
    'Source and conversion': '来源与转化',
    Registered: '注册时间',
    'First observed source': '首次观察到的来源',
    'Registration source': '注册来源',
    'First observed successful API response': '首次观察到成功 API 响应',
    'Only qualifying API success records are shown here. Missing records do not prove that an account never used the API.':
      '此处仅显示符合统计条件的成功调用。没有记录不代表账号从未使用 API。',
    'Return to channel report': '返回渠道报表',
    'Recent source observations': '最近的来源记录',
    'No retained source observations are available for this account.':
      '该账号暂无保留中的来源记录。',
    'Allow analysis of sources, registrations, successful API use and payments? Entry records are kept for 90 days; account attribution and daily activity for 365 days. No API keys, messages, IP addresses or device fingerprints. Optional; access is unchanged.':
      '允许分析来源、注册、成功 API 调用和付款记录吗？入口记录保留 90 天，账号归属和每日活跃记录保留 365 天。不收集 API Key、消息、IP 地址或设备指纹。是否允许不影响使用。',
    'Unable to save privacy settings. Please retry to stop server-side analytics.':
      '隐私设置未保存，请重试以停止服务端统计。',
    'Registration-cohort attribution. Payments are observed through the report update time, grouped by currency. Attribution does not prove causation. API activity has its own processing watermark below.':
      '按注册批次归属来源，付款统计截至报表更新时间，并按币种区分。归属不代表因果关系。API 调用统计的处理进度见下方。',
    'Eligible accounts': '符合条件的账号',
    'Observed successful accounts': '已观察到成功调用的账号',
    'Under observation': '观察中',
    'Day 7 retained / eligible': '第 7 天活跃 / 满观察期账号',
    'Day 7 retention': '7 日留存率',
    'Operational log coverage is incomplete. Retention rates are unavailable.':
      '调用日志不完整，暂不计算留存率。',
    'Activity statistics are catching up or temporarily unavailable. Displayed successes are confirmed observations, not a complete current total.':
      '调用统计正在追赶进度或暂时不可用。当前成功数量仅包含已确认的记录，不代表此刻的完整总量。',
    'Rebuilding activity statistics': '正在重建调用统计',
    'Rebuild activity statistics': '重建调用统计',
    'Successful API response observed': '已观察到成功 API 响应',
    'No successful API response observed yet': '尚未观察到成功 API 响应',
    'Attributed from an earlier source observation': '根据此前来源记录归属',
    'Source observed in the current visit': '本次访问直接观察到的来源',
    'Export summary': '导出汇总',
    'Recorded attribution windows: {{days}} days':
      '已记录的归属回看范围：{{days}} 天',
    'Lookback days for new registrations': '新注册账号的回看天数',
    'Changing the default does not rewrite existing source attributions.':
      '修改默认天数不会重写已有来源归属。',
    'Operational log coverage is incomplete. Affected accounts are excluded from retention rates.':
      '调用日志存在缺失，受影响账号不计入留存率分母。',
    'Missing retention data': '留存数据缺失',
    'Counting rules: text API, UTC day 7': '统计口径：文本 API、UTC 第 7 天',
  },
  'zh-TW': {
    'API activation and retention': 'API 接入與留存',
    'Only new accounts within this observation period that explicitly allowed expanded analytics are included. Success requires a delivered text API response with output and no recorded stream or request failure. Creating a key or authorizing OAuth is not success.':
      '僅統計觀察期內新註冊、且明確同意擴充統計的帳號。成功呼叫必須已傳回文字 API 回應、包含輸出，且未記錄請求或串流錯誤。建立 Key 或完成 OAuth 授權不算接入成功。',
    'Day 7 retention uses UTC days: accounts with another qualifying request on the seventh day after their first observed success, divided by successful accounts whose full seventh day has been processed. Accounts still under observation are excluded from that denominator.':
      '7 日留存按 UTC 自然日計算：首次觀察到成功呼叫後第 7 天仍有符合條件呼叫的帳號數，除以第 7 天已完整統計的成功接入帳號數。仍在觀察中的帳號不計入分母。',
    'Activity statistics have not started yet.': '呼叫統計尚未開始。',
    'Processed through': '已處理至',
    'No eligible accounts in this registration period.':
      '此註冊期間沒有符合統計條件的帳號。',
    'Unable to rebuild activity statistics. Please retry.':
      '無法重建呼叫統計，請重試。',
    'Channel accounts': '管道帳號',
    'Reload source records': '重新載入來源記錄',
    'No accounts match this source and registration period.':
      '此來源和註冊期間沒有符合的帳號。',
    'Source and conversion': '來源與轉換',
    Registered: '註冊時間',
    'First observed source': '首次觀察到的來源',
    'Registration source': '註冊來源',
    'First observed successful API response': '首次觀察到成功 API 回應',
    'Only qualifying API success records are shown here. Missing records do not prove that an account never used the API.':
      '此處僅顯示符合統計條件的成功呼叫。沒有記錄不代表帳號從未使用 API。',
    'Return to channel report': '返回管道報表',
    'Recent source observations': '最近的來源記錄',
    'No retained source observations are available for this account.':
      '此帳號目前沒有保留中的來源記錄。',
    'Allow analysis of sources, registrations, successful API use and payments? Entry records are kept for 90 days; account attribution and daily activity for 365 days. No API keys, messages, IP addresses or device fingerprints. Optional; access is unchanged.':
      '允許分析來源、註冊、成功 API 呼叫和付款記錄嗎？入口記錄保留 90 天，帳號歸屬和每日活躍記錄保留 365 天。不收集 API Key、訊息、IP 位址或裝置指紋。是否允許不影響使用。',
    'Unable to save privacy settings. Please retry to stop server-side analytics.':
      '隱私設定未儲存，請重試以停止伺服器端統計。',
    'Registration-cohort attribution. Payments are observed through the report update time, grouped by currency. Attribution does not prove causation. API activity has its own processing watermark below.':
      '按註冊批次歸屬來源，付款統計截至報表更新時間，並按幣別區分。歸屬不代表因果關係。API 呼叫統計的處理進度見下方。',
    'Eligible accounts': '符合條件的帳號',
    'Observed successful accounts': '已觀察到成功呼叫的帳號',
    'Under observation': '觀察中',
    'Day 7 retained / eligible': '第 7 天活躍 / 滿觀察期帳號',
    'Day 7 retention': '7 日留存率',
    'Operational log coverage is incomplete. Retention rates are unavailable.':
      '呼叫日誌不完整，暫不計算留存率。',
    'Activity statistics are catching up or temporarily unavailable. Displayed successes are confirmed observations, not a complete current total.':
      '呼叫統計正在追趕進度或暫時無法使用。目前成功數量僅包含已確認的記錄，不代表此刻的完整總量。',
    'Rebuilding activity statistics': '正在重建呼叫統計',
    'Rebuild activity statistics': '重建呼叫統計',
    'Successful API response observed': '已觀察到成功 API 回應',
    'No successful API response observed yet': '尚未觀察到成功 API 回應',
    'Attributed from an earlier source observation': '根據先前來源記錄歸屬',
    'Source observed in the current visit': '本次造訪直接觀察到的來源',
    'Export summary': '匯出彙總',
    'Recorded attribution windows: {{days}} days':
      '已記錄的歸屬回溯範圍：{{days}} 天',
    'Lookback days for new registrations': '新註冊帳號的回溯天數',
    'Changing the default does not rewrite existing source attributions.':
      '修改預設天數不會重寫既有來源歸屬。',
    'Operational log coverage is incomplete. Affected accounts are excluded from retention rates.':
      '呼叫日誌存在缺失，受影響帳號不計入留存率分母。',
    'Missing retention data': '留存資料缺失',
    'Counting rules: text API, UTC day 7': '統計口徑：文字 API、UTC 第 7 天',
  },
  fr: {
    'API activation and retention': 'Activation API et rétention',
    'Only new accounts within this observation period that explicitly allowed expanded analytics are included. Success requires a delivered text API response with output and no recorded stream or request failure. Creating a key or authorizing OAuth is not success.':
      'Seuls les nouveaux comptes ayant accepté ces statistiques sont inclus. Le succès exige une réponse API texte transmise, avec sortie et sans erreur de requête ou de flux enregistrée. Créer une clé ou autoriser OAuth ne suffit pas.',
    'Day 7 retention uses UTC days: accounts with another qualifying request on the seventh day after their first observed success, divided by successful accounts whose full seventh day has been processed. Accounts still under observation are excluded from that denominator.':
      'La rétention J7 suit les jours UTC : comptes avec une nouvelle requête valide au septième jour après le premier succès observé, divisés par les comptes dont ce septième jour a été entièrement traité. Les comptes encore en observation sont exclus du dénominateur.',
    'Activity statistics have not started yet.':
      'Les statistiques d’activité n’ont pas encore démarré.',
    'Processed through': 'Traité jusqu’au',
    'No eligible accounts in this registration period.':
      'Aucun compte éligible dans cette période d’inscription.',
    'Unable to rebuild activity statistics. Please retry.':
      'Impossible de recalculer l’activité. Réessayez.',
    'Channel accounts': 'Comptes du canal',
    'Reload source records': 'Recharger les sources',
    'No accounts match this source and registration period.':
      'Aucun compte pour cette source et cette période.',
    'Source and conversion': 'Source et conversion',
    Registered: 'Inscription',
    'First observed source': 'Première source observée',
    'Registration source': 'Source d’inscription',
    'First observed successful API response':
      'Première réponse API réussie observée',
    'Only qualifying API success records are shown here. Missing records do not prove that an account never used the API.':
      'Seuls les succès API éligibles sont affichés. L’absence de données ne prouve pas que le compte n’a jamais utilisé l’API.',
    'Return to channel report': 'Retour au rapport du canal',
    'Recent source observations': 'Sources récemment observées',
    'No retained source observations are available for this account.':
      'Aucune source conservée pour ce compte.',
    'Allow analysis of sources, registrations, successful API use and payments? Entry records are kept for 90 days; account attribution and daily activity for 365 days. No API keys, messages, IP addresses or device fingerprints. Optional; access is unchanged.':
      'Autoriser l’analyse des sources, inscriptions, appels API réussis et paiements ? Les entrées sont conservées 90 jours, l’attribution et l’activité quotidienne 365 jours. Aucune clé API, aucun message, aucune adresse IP ni empreinte d’appareil. Facultatif, sans effet sur l’accès.',
    'Unable to save privacy settings. Please retry to stop server-side analytics.':
      'Préférences non enregistrées. Réessayez pour arrêter l’analyse côté serveur.',
    'Registration-cohort attribution. Payments are observed through the report update time, grouped by currency. Attribution does not prove causation. API activity has its own processing watermark below.':
      'Attribution par période d’inscription. Paiements observés jusqu’à la mise à jour, par devise. L’attribution ne prouve pas la causalité. L’avancement du traitement API est indiqué ci-dessous.',
    'Eligible accounts': 'Comptes éligibles',
    'Observed successful accounts': 'Comptes avec succès observé',
    'Under observation': 'En observation',
    'Day 7 retained / eligible': 'Actifs J7 / comptes observés',
    'Day 7 retention': 'Rétention J7',
    'Operational log coverage is incomplete. Retention rates are unavailable.':
      'Les journaux sont incomplets. Le taux de rétention est indisponible.',
    'Activity statistics are catching up or temporarily unavailable. Displayed successes are confirmed observations, not a complete current total.':
      'L’activité est en cours de rattrapage ou temporairement indisponible. Les succès affichés sont confirmés mais ne représentent pas un total actuel complet.',
    'Rebuilding activity statistics': 'Recalcul de l’activité',
    'Rebuild activity statistics': 'Recalculer l’activité',
    'Successful API response observed': 'Réponse API réussie observée',
    'No successful API response observed yet': 'Aucun succès API observé',
    'Attributed from an earlier source observation':
      'Attribué à une source antérieure',
    'Source observed in the current visit':
      'Source observée lors de cette visite',
    'Export summary': 'Exporter le résumé',
    'Recorded attribution windows: {{days}} days':
      'Fenêtres d’attribution enregistrées : {{days}} jours',
    'Lookback days for new registrations':
      'Fenêtre pour les nouvelles inscriptions',
    'Changing the default does not rewrite existing source attributions.':
      'Modifier la valeur par défaut ne réécrit pas les attributions existantes.',
    'Operational log coverage is incomplete. Affected accounts are excluded from retention rates.':
      'Les journaux sont incomplets. Les comptes concernés sont exclus du calcul de rétention.',
    'Missing retention data': 'Données de rétention manquantes',
    'Counting rules: text API, UTC day 7': 'Règles : API texte, jour 7 UTC',
  },
  ja: {
    'API activation and retention': 'API 利用開始と継続利用',
    'Only new accounts within this observation period that explicitly allowed expanded analytics are included. Success requires a delivered text API response with output and no recorded stream or request failure. Creating a key or authorizing OAuth is not success.':
      '観測期間中に登録し、拡張分析を明示的に許可したアカウントのみ対象です。出力を含むテキスト API 応答が送信され、リクエストやストリームのエラーが記録されていない場合を成功とします。キー作成や OAuth 認可だけでは成功に数えません。',
    'Day 7 retention uses UTC days: accounts with another qualifying request on the seventh day after their first observed success, divided by successful accounts whose full seventh day has been processed. Accounts still under observation are excluded from that denominator.':
      '7日目の継続率は UTC の日付で計算します。初めて成功を観測した日の7日後に対象リクエストがあったアカウント数を、7日目の集計が完了した成功アカウント数で割ります。観測中のアカウントは分母に含めません。',
    'Activity statistics have not started yet.':
      '利用状況の集計はまだ開始されていません。',
    'Processed through': '処理済みの日時',
    'No eligible accounts in this registration period.':
      'この登録期間に集計対象のアカウントはありません。',
    'Unable to rebuild activity statistics. Please retry.':
      '利用統計を再集計できません。再試行してください。',
    'Channel accounts': 'チャネルのアカウント',
    'Reload source records': '流入元の記録を再読み込み',
    'No accounts match this source and registration period.':
      'この流入元と登録期間に一致するアカウントはありません。',
    'Source and conversion': '流入元と利用状況',
    Registered: '登録日時',
    'First observed source': '最初に観測した流入元',
    'Registration source': '登録時の流入元',
    'First observed successful API response': '最初に観測した API 応答成功',
    'Only qualifying API success records are shown here. Missing records do not prove that an account never used the API.':
      'ここには集計条件を満たす API 成功記録のみ表示します。記録がなくても、API を一度も使っていないとは限りません。',
    'Return to channel report': 'チャネルレポートに戻る',
    'Recent source observations': '最近の流入元の記録',
    'No retained source observations are available for this account.':
      'このアカウントに保持中の流入元の記録はありません。',
    'Allow analysis of sources, registrations, successful API use and payments? Entry records are kept for 90 days; account attribution and daily activity for 365 days. No API keys, messages, IP addresses or device fingerprints. Optional; access is unchanged.':
      '流入元、登録、API 呼び出しの成功、支払い記録の分析を許可しますか？入口の記録は90日間、アカウントとの関連付けと日別の利用記録は365日間保持します。API キー、メッセージ、IP アドレス、端末の識別情報は収集しません。任意であり、利用権限に影響しません。',
    'Unable to save privacy settings. Please retry to stop server-side analytics.':
      'プライバシー設定を保存できません。サーバー側の分析を停止するには再試行してください。',
    'Registration-cohort attribution. Payments are observed through the report update time, grouped by currency. Attribution does not prove causation. API activity has its own processing watermark below.':
      '登録期間に基づく流入元の集計です。支払いは更新時点まで通貨別に集計します。関連付けは因果関係を証明しません。API 利用統計の処理済み日時は以下に表示します。',
    'Eligible accounts': '対象アカウント',
    'Observed successful accounts': '成功を観測したアカウント',
    'Under observation': '観測中',
    'Day 7 retained / eligible': '7日目の利用継続 / 観測完了',
    'Day 7 retention': '7日目の継続率',
    'Operational log coverage is incomplete. Retention rates are unavailable.':
      '呼び出しログが不完全なため、継続率は算出できません。',
    'Activity statistics are catching up or temporarily unavailable. Displayed successes are confirmed observations, not a complete current total.':
      '利用統計は処理中、または一時的に利用できません。表示中の成功数は確認済みの記録であり、現時点の完全な合計ではありません。',
    'Rebuilding activity statistics': '利用統計を再集計中',
    'Rebuild activity statistics': '利用統計を再集計',
    'Successful API response observed': 'API 応答の成功を観測済み',
    'No successful API response observed yet': 'API 応答の成功は未観測',
    'Attributed from an earlier source observation':
      '以前の流入元の記録から関連付け',
    'Source observed in the current visit': '今回の訪問で観測した流入元',
    'Export summary': '集計をエクスポート',
    'Recorded attribution windows: {{days}} days':
      '記録済みの参照期間：{{days}}日',
    'Lookback days for new registrations': '新規登録の参照日数',
    'Changing the default does not rewrite existing source attributions.':
      '既定の日数を変更しても既存の流入元の関連付けは変更しません。',
    'Operational log coverage is incomplete. Affected accounts are excluded from retention rates.':
      '呼び出しログに欠落があるため、該当アカウントは継続率の分母から除外します。',
    'Missing retention data': '継続利用データ欠落',
    'Counting rules: text API, UTC day 7':
      '集計ルール：テキスト API、UTC 7日目',
  },
  ru: {
    'API activation and retention': 'Подключение API и удержание',
    'Only new accounts within this observation period that explicitly allowed expanded analytics are included. Success requires a delivered text API response with output and no recorded stream or request failure. Creating a key or authorizing OAuth is not success.':
      'Учитываются только новые аккаунты, явно разрешившие расширенную аналитику. Успех — переданный текстовый ответ API с выводом, без зарегистрированной ошибки запроса или потока. Создание ключа или авторизация OAuth не считаются подключением.',
    'Day 7 retention uses UTC days: accounts with another qualifying request on the seventh day after their first observed success, divided by successful accounts whose full seventh day has been processed. Accounts still under observation are excluded from that denominator.':
      'Удержание D7 считается по дням UTC: аккаунты с повторным подходящим запросом на седьмой день после первого наблюдаемого успеха делятся на успешные аккаунты с полностью обработанным седьмым днём. Ещё наблюдаемые аккаунты исключены из знаменателя.',
    'Activity statistics have not started yet.':
      'Статистика активности ещё не запущена.',
    'Processed through': 'Обработано до',
    'No eligible accounts in this registration period.':
      'В этом периоде регистрации нет подходящих аккаунтов.',
    'Unable to rebuild activity statistics. Please retry.':
      'Не удалось пересчитать активность. Повторите попытку.',
    'Channel accounts': 'Аккаунты канала',
    'Reload source records': 'Перезагрузить записи источников',
    'No accounts match this source and registration period.':
      'Нет аккаунтов для этого источника и периода регистрации.',
    'Source and conversion': 'Источник и конверсия',
    Registered: 'Дата регистрации',
    'First observed source': 'Первый наблюдаемый источник',
    'Registration source': 'Источник регистрации',
    'First observed successful API response':
      'Первый наблюдаемый успешный ответ API',
    'Only qualifying API success records are shown here. Missing records do not prove that an account never used the API.':
      'Здесь показаны только подходящие записи об успехе API. Отсутствие записей не означает, что аккаунт никогда не использовал API.',
    'Return to channel report': 'Вернуться к отчёту канала',
    'Recent source observations': 'Последние наблюдения источников',
    'No retained source observations are available for this account.':
      'Для этого аккаунта нет сохранённых наблюдений источника.',
    'Allow analysis of sources, registrations, successful API use and payments? Entry records are kept for 90 days; account attribution and daily activity for 365 days. No API keys, messages, IP addresses or device fingerprints. Optional; access is unchanged.':
      'Разрешить анализ источников, регистраций, успешных вызовов API и платежей? Входы хранятся 90 дней, атрибуция и ежедневная активность — 365 дней. Без ключей API, сообщений, IP-адресов и отпечатков устройств. Выбор не влияет на доступ.',
    'Unable to save privacy settings. Please retry to stop server-side analytics.':
      'Не удалось сохранить настройки. Повторите попытку, чтобы остановить серверную аналитику.',
    'Registration-cohort attribution. Payments are observed through the report update time, grouped by currency. Attribution does not prove causation. API activity has its own processing watermark below.':
      'Атрибуция по периоду регистрации. Платежи учтены до обновления отчёта, отдельно по валютам. Это не доказывает причинную связь. Срок обработки API указан ниже.',
    'Eligible accounts': 'Подходящие аккаунты',
    'Observed successful accounts': 'Аккаунты с наблюдаемым успехом',
    'Under observation': 'Наблюдение продолжается',
    'Day 7 retained / eligible': 'Активны D7 / полный период',
    'Day 7 retention': 'Удержание D7',
    'Operational log coverage is incomplete. Retention rates are unavailable.':
      'Журналы неполные. Удержание рассчитать нельзя.',
    'Activity statistics are catching up or temporarily unavailable. Displayed successes are confirmed observations, not a complete current total.':
      'Статистика догоняет данные или временно недоступна. Показаны подтверждённые успехи, а не полный текущий итог.',
    'Rebuilding activity statistics': 'Пересчёт активности',
    'Rebuild activity statistics': 'Пересчитать активность',
    'Successful API response observed': 'Наблюдался успешный ответ API',
    'No successful API response observed yet':
      'Успешный ответ API ещё не наблюдался',
    'Attributed from an earlier source observation':
      'Атрибуция по предыдущему наблюдению',
    'Source observed in the current visit':
      'Источник наблюдался в этом посещении',
    'Export summary': 'Экспортировать сводку',
    'Recorded attribution windows: {{days}} days':
      'Записанные окна атрибуции: {{days}} дн.',
    'Lookback days for new registrations': 'Окно для новых регистраций',
    'Changing the default does not rewrite existing source attributions.':
      'Изменение значения по умолчанию не меняет существующую атрибуцию.',
    'Operational log coverage is incomplete. Affected accounts are excluded from retention rates.':
      'В журналах есть пробелы. Затронутые аккаунты исключены из расчёта удержания.',
    'Missing retention data': 'Нет данных об удержании',
    'Counting rules: text API, UTC day 7':
      'Правила: текстовый API, день 7 по UTC',
  },
  vi: {
    'API activation and retention': 'Kết nối API và duy trì sử dụng',
    'Only new accounts within this observation period that explicitly allowed expanded analytics are included. Success requires a delivered text API response with output and no recorded stream or request failure. Creating a key or authorizing OAuth is not success.':
      'Chỉ tính tài khoản mới trong kỳ đã đồng ý mở rộng phân tích. Thành công phải là phản hồi API văn bản đã gửi, có đầu ra và không ghi nhận lỗi yêu cầu hay luồng. Tạo khóa hoặc cấp quyền OAuth chưa được tính là kết nối thành công.',
    'Day 7 retention uses UTC days: accounts with another qualifying request on the seventh day after their first observed success, divided by successful accounts whose full seventh day has been processed. Accounts still under observation are excluded from that denominator.':
      'Duy trì ngày 7 tính theo ngày UTC: số tài khoản có yêu cầu hợp lệ vào ngày thứ 7 sau lần thành công đầu tiên được ghi nhận, chia cho số tài khoản thành công đã thống kê trọn ngày thứ 7. Không đưa tài khoản đang theo dõi vào mẫu số.',
    'Activity statistics have not started yet.':
      'Thống kê hoạt động chưa bắt đầu.',
    'Processed through': 'Đã xử lý đến',
    'No eligible accounts in this registration period.':
      'Không có tài khoản đủ điều kiện trong kỳ đăng ký này.',
    'Unable to rebuild activity statistics. Please retry.':
      'Không thể tính lại thống kê hoạt động. Vui lòng thử lại.',
    'Channel accounts': 'Tài khoản theo kênh',
    'Reload source records': 'Tải lại bản ghi nguồn',
    'No accounts match this source and registration period.':
      'Không có tài khoản khớp nguồn và kỳ đăng ký này.',
    'Source and conversion': 'Nguồn và chuyển đổi',
    Registered: 'Ngày đăng ký',
    'First observed source': 'Nguồn đầu tiên ghi nhận',
    'Registration source': 'Nguồn đăng ký',
    'First observed successful API response':
      'Phản hồi API thành công đầu tiên ghi nhận',
    'Only qualifying API success records are shown here. Missing records do not prove that an account never used the API.':
      'Chỉ hiển thị lần gọi API thành công đủ điều kiện. Không có bản ghi không đồng nghĩa tài khoản chưa từng dùng API.',
    'Return to channel report': 'Về báo cáo kênh',
    'Recent source observations': 'Nguồn truy cập ghi nhận gần đây',
    'No retained source observations are available for this account.':
      'Không có bản ghi nguồn còn được lưu cho tài khoản này.',
    'Allow analysis of sources, registrations, successful API use and payments? Entry records are kept for 90 days; account attribution and daily activity for 365 days. No API keys, messages, IP addresses or device fingerprints. Optional; access is unchanged.':
      'Cho phép phân tích nguồn, đăng ký, lần gọi API thành công và thanh toán? Lưu bản ghi truy cập 90 ngày, quy nguồn tài khoản và hoạt động hằng ngày 365 ngày. Không thu thập khóa API, tin nhắn, địa chỉ IP hay dấu vân tay thiết bị. Lựa chọn không ảnh hưởng quyền truy cập.',
    'Unable to save privacy settings. Please retry to stop server-side analytics.':
      'Không lưu được cài đặt riêng tư. Hãy thử lại để dừng phân tích trên máy chủ.',
    'Registration-cohort attribution. Payments are observed through the report update time, grouped by currency. Attribution does not prove causation. API activity has its own processing watermark below.':
      'Quy nguồn theo kỳ đăng ký; thanh toán tính đến lúc cập nhật và tách theo tiền tệ. Quy nguồn không chứng minh quan hệ nhân quả. Tiến độ xử lý hoạt động API nằm bên dưới.',
    'Eligible accounts': 'Tài khoản đủ điều kiện',
    'Observed successful accounts': 'Tài khoản đã ghi nhận thành công',
    'Under observation': 'Đang theo dõi',
    'Day 7 retained / eligible': 'Hoạt động ngày 7 / đủ kỳ quan sát',
    'Day 7 retention': 'Tỷ lệ duy trì ngày 7',
    'Operational log coverage is incomplete. Retention rates are unavailable.':
      'Nhật ký gọi API chưa đầy đủ, chưa thể tính tỷ lệ duy trì.',
    'Activity statistics are catching up or temporarily unavailable. Displayed successes are confirmed observations, not a complete current total.':
      'Thống kê đang cập nhật hoặc tạm không khả dụng. Số thành công hiển thị là bản ghi đã xác nhận, chưa phải tổng đầy đủ hiện tại.',
    'Rebuilding activity statistics': 'Đang tính lại thống kê hoạt động',
    'Rebuild activity statistics': 'Tính lại thống kê hoạt động',
    'Successful API response observed': 'Đã ghi nhận phản hồi API thành công',
    'No successful API response observed yet':
      'Chưa ghi nhận phản hồi API thành công',
    'Attributed from an earlier source observation':
      'Quy nguồn từ bản ghi trước đó',
    'Source observed in the current visit':
      'Nguồn ghi nhận trực tiếp trong lượt này',
    'Export summary': 'Xuất tổng hợp',
    'Recorded attribution windows: {{days}} days':
      'Khoảng truy nguồn đã ghi nhận: {{days}} ngày',
    'Lookback days for new registrations': 'Số ngày truy nguồn cho đăng ký mới',
    'Changing the default does not rewrite existing source attributions.':
      'Đổi mặc định không ghi lại nguồn đã được quy trước đó.',
    'Operational log coverage is incomplete. Affected accounts are excluded from retention rates.':
      'Nhật ký có phần thiếu. Loại tài khoản bị ảnh hưởng khỏi mẫu số tính duy trì.',
    'Missing retention data': 'Thiếu dữ liệu duy trì',
    'Counting rules: text API, UTC day 7': 'Quy tắc: API văn bản, ngày 7 UTC',
  },
}

const requestEstimateCopy = {
  en: {
    'Request cost estimate': 'Request cost estimate',
    'Cached input tokens': 'Cached input tokens',
    'Estimate unavailable': 'Estimate unavailable',
    'No available groups': 'No available groups',
    'One request, selected group multiplier included. Cached tokens are part of input. Excludes cache writes, media and tool fees; actual charges may differ.':
      'One request, selected group multiplier included. Cached tokens are part of input. Excludes cache writes, media and tool fees; actual charges may differ.',
    'Check token counts and group prices. Dynamic or special billing requires the pricing rules above.':
      'Check token counts and group prices. Dynamic or special billing requires the pricing rules above.',
  },
  zh: {
    'Request cost estimate': '单次请求费用估算',
    'Cached input tokens': '缓存命中的输入 Tokens',
    'Estimate unavailable': '暂时无法估算',
    'No available groups': '没有可用分组',
    'One request, selected group multiplier included. Cached tokens are part of input. Excludes cache writes, media and tool fees; actual charges may differ.':
      '按一次请求估算，已包含所选分组倍率。缓存 Tokens 属于输入的一部分。不含缓存写入、媒体和工具费用，实际扣费可能不同。',
    'Check token counts and group prices. Dynamic or special billing requires the pricing rules above.':
      '请检查 Tokens 数量及分组价格。动态或特殊计费请参照上方规则。',
  },
  'zh-TW': {
    'Request cost estimate': '單次請求費用估算',
    'Cached input tokens': '快取命中的輸入 Tokens',
    'Estimate unavailable': '暫時無法估算',
    'No available groups': '沒有可用分組',
    'One request, selected group multiplier included. Cached tokens are part of input. Excludes cache writes, media and tool fees; actual charges may differ.':
      '按一次請求估算，已包含所選分組倍率。快取 Tokens 屬於輸入的一部分。不含快取寫入、媒體和工具費用，實際扣費可能不同。',
    'Check token counts and group prices. Dynamic or special billing requires the pricing rules above.':
      '請檢查 Tokens 數量及分組價格。動態或特殊計費請參照上方規則。',
  },
  fr: {
    'Request cost estimate': 'Coût estimé par requête',
    'Cached input tokens': 'Tokens d’entrée en cache',
    'Estimate unavailable': 'Estimation indisponible',
    'No available groups': 'Aucun groupe disponible',
    'One request, selected group multiplier included. Cached tokens are part of input. Excludes cache writes, media and tool fees; actual charges may differ.':
      'Une requête, multiplicateur du groupe inclus. Le cache fait partie de l’entrée. Hors écriture du cache, médias et outils ; le montant réel peut varier.',
    'Check token counts and group prices. Dynamic or special billing requires the pricing rules above.':
      'Vérifiez les tokens et les tarifs du groupe. Pour les tarifs dynamiques ou spéciaux, consultez les règles ci-dessus.',
  },
  ja: {
    'Request cost estimate': 'リクエスト費用の見積もり',
    'Cached input tokens': 'キャッシュ済み入力トークン',
    'Estimate unavailable': '見積もりできません',
    'No available groups': '利用可能なグループがありません',
    'One request, selected group multiplier included. Cached tokens are part of input. Excludes cache writes, media and tool fees; actual charges may differ.':
      '1回分の見積もりで、選択したグループの倍率を含みます。キャッシュは入力の一部です。キャッシュ書き込み、メディア、ツールの料金は含まず、実際の請求額は異なる場合があります。',
    'Check token counts and group prices. Dynamic or special billing requires the pricing rules above.':
      'トークン数とグループ料金を確認してください。動的・特殊料金は上記のルールを参照してください。',
  },
  ru: {
    'Request cost estimate': 'Стоимость одного запроса',
    'Cached input tokens': 'Входные токены из кэша',
    'Estimate unavailable': 'Оценка недоступна',
    'No available groups': 'Нет доступных групп',
    'One request, selected group multiplier included. Cached tokens are part of input. Excludes cache writes, media and tool fees; actual charges may differ.':
      'Один запрос с учётом множителя группы. Кэш входит во входные токены. Без записи в кэш, медиа и инструментов; итоговая сумма может отличаться.',
    'Check token counts and group prices. Dynamic or special billing requires the pricing rules above.':
      'Проверьте число токенов и тарифы группы. Для динамической или особой тарификации смотрите правила выше.',
  },
  vi: {
    'Request cost estimate': 'Ước tính phí mỗi yêu cầu',
    'Cached input tokens': 'Token đầu vào từ bộ nhớ đệm',
    'Estimate unavailable': 'Chưa thể ước tính',
    'No available groups': 'Không có nhóm khả dụng',
    'One request, selected group multiplier included. Cached tokens are part of input. Excludes cache writes, media and tool fees; actual charges may differ.':
      'Một yêu cầu, đã tính hệ số nhóm. Token đệm là một phần đầu vào. Chưa gồm phí ghi bộ nhớ đệm, đa phương tiện và công cụ; phí thực tế có thể khác.',
    'Check token counts and group prices. Dynamic or special billing requires the pricing rules above.':
      'Kiểm tra số token và giá nhóm. Với phí động hoặc đặc biệt, xem quy tắc ở trên.',
  },
}

const parallelExperienceCopy = {
  en: {
    'Use AI': 'Use AI',
    'Models and pricing': 'Models and pricing',
    Developers: 'Developers',
    Ecosystem: 'Ecosystem',
    'Other services': 'Other services',
    'Client setup and API usage': 'Client setup and API usage',
    'Getting started and AI tools': 'Getting started and AI tools',
    'Current escrow: {{amount}} in API account balance.':
      'Current escrow: {{amount}} in API account balance.',
    'Delivery timeline': 'Delivery timeline',
    'Dispute opened': 'Dispute opened',
    'Evidence: Issue or PR; follow the acceptance rules.':
      'Evidence: Issue or PR; follow the acceptance rules.',
    'If rejected, open a dispute within 7 days. A platform administrator reviews the evidence.':
      'If rejected, open a dispute within 7 days. A platform administrator reviews the evidence.',
    'Next: complete the work and submit GitHub evidence.':
      'Next: complete the work and submit GitHub evidence.',
    'Reserved slots': 'Reserved slots',
    'Reviewed by the publisher: {{name}}':
      'Reviewed by the publisher: {{name}}',
    'Reward credited to API balance': 'Reward credited to API balance',
    'Rewards are credited to your API account balance.':
      'Rewards are credited to your API account balance.',
    'Waiting for publisher review': 'Waiting for publisher review',
    'API account balance': 'API account balance',
    'Active deliveries': 'Active deliveries',
  },
  zh: {
    'Use AI': '使用 AI',
    'Models and pricing': '模型与价格',
    Developers: '开发者',
    Ecosystem: '生态',
    'Other services': '其他服务',
    'Client setup and API usage': '客户端配置与 API 调用',
    'Getting started and AI tools': '新手引导与 AI 工具',
    'Current escrow: {{amount}} in API account balance.':
      '当前托管：{{amount}} API 账户余额。',
    'Delivery timeline': '任务进度',
    'Dispute opened': '已发起申诉',
    'Evidence: Issue or PR; follow the acceptance rules.':
      '提交 Issue 或 PR 作为凭据，具体以验收规则为准。',
    'If rejected, open a dispute within 7 days. A platform administrator reviews the evidence.':
      '被拒绝后可在 7 天内申诉，由平台管理员审核证据。',
    'Next: complete the work and submit GitHub evidence.':
      '下一步：完成任务并提交 GitHub 凭据。',
    'Reserved slots': '已占名额',
    'Reviewed by the publisher: {{name}}': '审核发布者：{{name}}',
    'Reward credited to API balance': '奖励已转入 API 余额',
    'Rewards are credited to your API account balance.':
      '奖励发放到 API 账户余额。',
    'Waiting for publisher review': '等待发布者审核',
    'API account balance': 'API 账户余额',
    'Active deliveries': '进行中的任务',
  },
  'zh-TW': {
    'Use AI': '使用 AI',
    'Models and pricing': '模型與價格',
    Developers: '開發者',
    Ecosystem: '生態',
    'Other services': '其他服務',
    'Client setup and API usage': '用戶端設定與 API 呼叫',
    'Getting started and AI tools': '入門引導與 AI 工具',
    'Current escrow: {{amount}} in API account balance.':
      '目前託管：{{amount}} API 帳戶餘額。',
    'Delivery timeline': '任務進度',
    'Dispute opened': '已提出申訴',
    'Evidence: Issue or PR; follow the acceptance rules.':
      '提交 Issue 或 PR 作為憑據，具體以驗收規則為準。',
    'If rejected, open a dispute within 7 days. A platform administrator reviews the evidence.':
      '被拒絕後可在 7 天內申訴，由平台管理員審核證據。',
    'Next: complete the work and submit GitHub evidence.':
      '下一步：完成任務並提交 GitHub 憑據。',
    'Reserved slots': '已佔名額',
    'Reviewed by the publisher: {{name}}': '審核發布者：{{name}}',
    'Reward credited to API balance': '獎勵已轉入 API 餘額',
    'Rewards are credited to your API account balance.':
      '獎勵發放至 API 帳戶餘額。',
    'Waiting for publisher review': '等待發布者審核',
    'API account balance': 'API 帳戶餘額',
    'Active deliveries': '進行中的任務',
  },
  fr: {
    'Use AI': 'Utiliser l’IA',
    'Models and pricing': 'Modèles et tarifs',
    Developers: 'Développeurs',
    Ecosystem: 'Écosystème',
    'Other services': 'Autres services',
    'Client setup and API usage':
      'Configuration des clients et utilisation de l’API',
    'Getting started and AI tools': 'Premiers pas et outils IA',
    'Current escrow: {{amount}} in API account balance.':
      'Séquestre actuel : {{amount}} de solde API.',
    'Delivery timeline': 'Suivi de la tâche',
    'Dispute opened': 'Litige ouvert',
    'Evidence: Issue or PR; follow the acceptance rules.':
      'Preuve : Issue ou PR, selon les critères d’acceptation.',
    'If rejected, open a dispute within 7 days. A platform administrator reviews the evidence.':
      'En cas de refus, ouvrez un litige sous 7 jours. Un administrateur examinera les preuves.',
    'Next: complete the work and submit GitHub evidence.':
      'Ensuite : terminez le travail et soumettez une preuve GitHub.',
    'Reserved slots': 'Places réservées',
    'Reviewed by the publisher: {{name}}': 'Évaluation par l’auteur : {{name}}',
    'Reward credited to API balance': 'Récompense créditée au solde API',
    'Rewards are credited to your API account balance.':
      'Les récompenses sont créditées au solde de votre compte API.',
    'Waiting for publisher review': 'En attente de l’évaluation de l’auteur',
    'API account balance': 'Solde du compte API',
    'Active deliveries': 'Tâches en cours',
  },
  ja: {
    'Use AI': 'AI を使う',
    'Models and pricing': 'モデルと料金',
    Developers: '開発者向け',
    Ecosystem: 'エコシステム',
    'Other services': 'その他のサービス',
    'Client setup and API usage': 'クライアント設定と API 利用',
    'Getting started and AI tools': 'はじめ方と AI ツール',
    'Current escrow: {{amount}} in API account balance.':
      '現在の預託額：API アカウント残高 {{amount}}。',
    'Delivery timeline': 'タスクの進捗',
    'Dispute opened': '異議申し立て済み',
    'Evidence: Issue or PR; follow the acceptance rules.':
      '証拠として Issue または PR を提出してください。詳細は受け入れ条件に従います。',
    'If rejected, open a dispute within 7 days. A platform administrator reviews the evidence.':
      '却下された場合は7日以内に異議を申し立てられます。プラットフォーム管理者が証拠を確認します。',
    'Next: complete the work and submit GitHub evidence.':
      '次の手順：作業を完了し、GitHub の証拠を提出します。',
    'Reserved slots': '予約済み枠',
    'Reviewed by the publisher: {{name}}': '審査担当の公開者：{{name}}',
    'Reward credited to API balance': '報酬を API 残高に反映済み',
    'Rewards are credited to your API account balance.':
      '報酬は API アカウント残高に加算されます。',
    'Waiting for publisher review': '公開者の審査待ち',
    'API account balance': 'API アカウント残高',
    'Active deliveries': '進行中のタスク',
  },
  ru: {
    'Use AI': 'Использование ИИ',
    'Models and pricing': 'Модели и цены',
    Developers: 'Разработчикам',
    Ecosystem: 'Экосистема',
    'Other services': 'Другие сервисы',
    'Client setup and API usage': 'Настройка клиентов и использование API',
    'Getting started and AI tools': 'Начало работы и инструменты ИИ',
    'Current escrow: {{amount}} in API account balance.':
      'В эскроу: {{amount}} баланса API.',
    'Delivery timeline': 'Ход выполнения',
    'Dispute opened': 'Спор открыт',
    'Evidence: Issue or PR; follow the acceptance rules.':
      'Подтверждение: Issue или PR согласно условиям приёмки.',
    'If rejected, open a dispute within 7 days. A platform administrator reviews the evidence.':
      'При отказе откройте спор в течение 7 дней. Администратор платформы проверит доказательства.',
    'Next: complete the work and submit GitHub evidence.':
      'Далее: завершите работу и предоставьте подтверждение на GitHub.',
    'Reserved slots': 'Занятые места',
    'Reviewed by the publisher: {{name}}': 'Проверяет автор задания: {{name}}',
    'Reward credited to API balance': 'Награда зачислена на баланс API',
    'Rewards are credited to your API account balance.':
      'Награды зачисляются на баланс вашего аккаунта API.',
    'Waiting for publisher review': 'Ожидает проверки автором',
    'API account balance': 'Баланс аккаунта API',
    'Active deliveries': 'Активные задания',
  },
  vi: {
    'Use AI': 'Sử dụng AI',
    'Models and pricing': 'Mô hình và giá',
    Developers: 'Nhà phát triển',
    Ecosystem: 'Hệ sinh thái',
    'Other services': 'Dịch vụ khác',
    'Client setup and API usage': 'Cấu hình ứng dụng và sử dụng API',
    'Getting started and AI tools': 'Bắt đầu và công cụ AI',
    'Current escrow: {{amount}} in API account balance.':
      'Đang ký quỹ: {{amount}} số dư tài khoản API.',
    'Delivery timeline': 'Tiến độ nhiệm vụ',
    'Dispute opened': 'Đã mở khiếu nại',
    'Evidence: Issue or PR; follow the acceptance rules.':
      'Bằng chứng: Issue hoặc PR theo tiêu chí nghiệm thu.',
    'If rejected, open a dispute within 7 days. A platform administrator reviews the evidence.':
      'Nếu bị từ chối, hãy khiếu nại trong 7 ngày. Quản trị viên nền tảng sẽ xem xét bằng chứng.',
    'Next: complete the work and submit GitHub evidence.':
      'Tiếp theo: hoàn thành công việc và gửi bằng chứng GitHub.',
    'Reserved slots': 'Suất đã giữ',
    'Reviewed by the publisher: {{name}}': 'Người đăng xét duyệt: {{name}}',
    'Reward credited to API balance': 'Đã cộng thưởng vào số dư API',
    'Rewards are credited to your API account balance.':
      'Phần thưởng được cộng vào số dư tài khoản API.',
    'Waiting for publisher review': 'Chờ người đăng xét duyệt',
    'API account balance': 'Số dư tài khoản API',
    'Active deliveries': 'Nhiệm vụ đang thực hiện',
  },
}

const modelStatusCopy = {
  zh: {
    'Loading status': '正在加载状态',
    'Model status could not be loaded': '无法加载模型状态',
    'Recent calls succeeded': '近期调用成功',
    'Recent calls include failures': '近期调用中有失败',
    'No recent model status': '暂无近期模型状态',
    'Sign in to check model access': '登录后查看模型权限',
    'API access is required': '需要 API 访问权限',
    'Your account has an eligible model group': '当前账号有可用的模型分组',
    'No eligible model group for this account':
      '当前账号没有符合条件的模型分组',
    'Model availability': '模型可用情况',
    'Latest observed request interval': '最近观测到的请求时段',
    'Recent means within one hour. Historical calls across groups do not guarantee your next request; key restrictions and balance still apply.':
      '近期指一小时内。各分组的历史调用不保证下一次请求成功；仍受 Key 限制和余额影响。',
    'Model catalog access is required': '需要模型目录访问权限',
    'Model catalog could not be loaded': '无法加载模型目录',
    'Model is not in this catalog': '当前目录中没有此模型',
    'This model may be unavailable to your account or absent from the catalog. Check your model ID and account access.':
      '此模型可能不在当前账号权限范围内，或未列入目录。请检查模型 ID 和账号权限。',
    'Check the model ID or sign in to see your account model catalog.':
      '请检查模型 ID，或登录查看当前账号的模型目录。',
    'This saved reply is no longer available. Send a new message to continue.':
      '已保存的回复已不可用。请发送新消息继续。',
  },
  'zh-TW': {
    'Loading status': '正在載入狀態',
    'Model status could not be loaded': '無法載入模型狀態',
    'Recent calls succeeded': '近期呼叫成功',
    'Recent calls include failures': '近期呼叫中有失敗',
    'No recent model status': '暫無近期模型狀態',
    'Sign in to check model access': '登入後查看模型權限',
    'API access is required': '需要 API 存取權限',
    'Your account has an eligible model group': '目前帳號有可用的模型群組',
    'No eligible model group for this account':
      '目前帳號沒有符合條件的模型群組',
    'Model availability': '模型可用情況',
    'Latest observed request interval': '最近觀測到的請求時段',
    'Recent means within one hour. Historical calls across groups do not guarantee your next request; key restrictions and balance still apply.':
      '近期指一小時內。各群組的歷史呼叫不保證下一次請求成功；仍受 Key 限制和餘額影響。',
    'Model catalog access is required': '需要模型目錄存取權限',
    'Model catalog could not be loaded': '無法載入模型目錄',
    'Model is not in this catalog': '目前目錄中沒有此模型',
    'This model may be unavailable to your account or absent from the catalog. Check your model ID and account access.':
      '此模型可能不在目前帳號權限範圍內，或未列入目錄。請檢查模型 ID 和帳號權限。',
    'Check the model ID or sign in to see your account model catalog.':
      '請檢查模型 ID，或登入查看目前帳號的模型目錄。',
    'This saved reply is no longer available. Send a new message to continue.':
      '已儲存的回覆已無法使用。請傳送新訊息繼續。',
  },
  fr: {
    'Loading status': 'Chargement de l’état',
    'Model status could not be loaded':
      'Impossible de charger l’état du modèle',
    'Recent calls succeeded': 'Appels récents réussis',
    'Recent calls include failures': 'Des appels récents ont échoué',
    'No recent model status': 'Aucun état récent du modèle',
    'Sign in to check model access':
      'Connectez-vous pour vérifier l’accès au modèle',
    'API access is required': 'Accès API requis',
    'Your account has an eligible model group':
      'Votre compte dispose d’un groupe admissible',
    'No eligible model group for this account':
      'Aucun groupe admissible pour ce compte',
    'Model availability': 'Disponibilité du modèle',
    'Latest observed request interval':
      'Dernier intervalle de requêtes observé',
    'Recent means within one hour. Historical calls across groups do not guarantee your next request; key restrictions and balance still apply.':
      'Récent signifie moins d’une heure. Les appels passés des groupes ne garantissent pas votre prochaine requête ; les restrictions de clé et le solde restent applicables.',
    'Model catalog access is required': 'Accès au catalogue de modèles requis',
    'Model catalog could not be loaded': 'Impossible de charger le catalogue',
    'Model is not in this catalog': 'Modèle absent de ce catalogue',
    'This model may be unavailable to your account or absent from the catalog. Check your model ID and account access.':
      'Ce modèle peut être inaccessible à votre compte ou absent du catalogue. Vérifiez son identifiant et vos droits d’accès.',
    'Check the model ID or sign in to see your account model catalog.':
      'Vérifiez l’identifiant du modèle ou connectez-vous pour consulter votre catalogue.',
    'This saved reply is no longer available. Send a new message to continue.':
      'Cette réponse enregistrée n’est plus disponible. Envoyez un nouveau message pour continuer.',
  },
  ja: {
    'Loading status': '状態を読み込み中',
    'Model status could not be loaded': 'モデルの状態を読み込めません',
    'Recent calls succeeded': '最近の呼び出しは成功しました',
    'Recent calls include failures': '最近の呼び出しに失敗があります',
    'No recent model status': '最近のモデル状態はありません',
    'Sign in to check model access': 'ログインしてモデル権限を確認',
    'API access is required': 'API 利用権限が必要です',
    'Your account has an eligible model group':
      'このアカウントには対象モデルのグループがあります',
    'No eligible model group for this account':
      'このアカウントには対象グループがありません',
    'Model availability': 'モデルの利用状況',
    'Latest observed request interval': '最後に観測したリクエスト時間帯',
    'Recent means within one hour. Historical calls across groups do not guarantee your next request; key restrictions and balance still apply.':
      '最近とは1時間以内です。各グループの過去の呼び出しは次のリクエストの成功を保証しません。キーの制限と残高も適用されます。',
    'Model catalog access is required': 'モデルカタログの閲覧権限が必要です',
    'Model catalog could not be loaded': 'モデルカタログを読み込めません',
    'Model is not in this catalog': 'このカタログにモデルはありません',
    'This model may be unavailable to your account or absent from the catalog. Check your model ID and account access.':
      'このモデルはアカウントの権限外か、カタログに未掲載の可能性があります。モデル ID と権限を確認してください。',
    'Check the model ID or sign in to see your account model catalog.':
      'モデル ID を確認するか、ログインしてアカウントのモデルカタログをご覧ください。',
    'This saved reply is no longer available. Send a new message to continue.':
      '保存された返信は利用できなくなりました。新しいメッセージを送信してください。',
  },
  ru: {
    'Loading status': 'Загрузка статуса',
    'Model status could not be loaded': 'Не удалось загрузить статус модели',
    'Recent calls succeeded': 'Недавние вызовы успешны',
    'Recent calls include failures': 'Среди недавних вызовов есть ошибки',
    'No recent model status': 'Нет свежих данных о модели',
    'Sign in to check model access': 'Войдите для проверки доступа к модели',
    'API access is required': 'Требуется доступ к API',
    'Your account has an eligible model group':
      'У аккаунта есть подходящая группа моделей',
    'No eligible model group for this account':
      'У аккаунта нет подходящей группы моделей',
    'Model availability': 'Доступность модели',
    'Latest observed request interval':
      'Последний наблюдаемый интервал запросов',
    'Recent means within one hour. Historical calls across groups do not guarantee your next request; key restrictions and balance still apply.':
      'Недавние данные — за последний час. История вызовов по группам не гарантирует успех следующего запроса; ограничения ключа и баланс по-прежнему учитываются.',
    'Model catalog access is required': 'Требуется доступ к каталогу моделей',
    'Model catalog could not be loaded': 'Не удалось загрузить каталог моделей',
    'Model is not in this catalog': 'Модели нет в этом каталоге',
    'This model may be unavailable to your account or absent from the catalog. Check your model ID and account access.':
      'Модель может быть недоступна вашему аккаунту или отсутствовать в каталоге. Проверьте ID модели и права доступа.',
    'Check the model ID or sign in to see your account model catalog.':
      'Проверьте ID модели или войдите, чтобы увидеть каталог своего аккаунта.',
    'This saved reply is no longer available. Send a new message to continue.':
      'Сохранённый ответ больше недоступен. Отправьте новое сообщение, чтобы продолжить.',
  },
  vi: {
    'Loading status': 'Đang tải trạng thái',
    'Model status could not be loaded': 'Không thể tải trạng thái mô hình',
    'Recent calls succeeded': 'Các lệnh gọi gần đây thành công',
    'Recent calls include failures': 'Có lệnh gọi gần đây thất bại',
    'No recent model status': 'Chưa có trạng thái mô hình gần đây',
    'Sign in to check model access':
      'Đăng nhập để kiểm tra quyền truy cập mô hình',
    'API access is required': 'Cần quyền truy cập API',
    'Your account has an eligible model group':
      'Tài khoản có nhóm mô hình đủ điều kiện',
    'No eligible model group for this account':
      'Tài khoản chưa có nhóm mô hình đủ điều kiện',
    'Model availability': 'Khả năng sử dụng mô hình',
    'Latest observed request interval':
      'Khoảng thời gian yêu cầu được ghi nhận gần nhất',
    'Recent means within one hour. Historical calls across groups do not guarantee your next request; key restrictions and balance still apply.':
      'Gần đây là trong một giờ. Lịch sử gọi của các nhóm không bảo đảm yêu cầu tiếp theo thành công; giới hạn khóa và số dư vẫn áp dụng.',
    'Model catalog access is required': 'Cần quyền xem danh mục mô hình',
    'Model catalog could not be loaded': 'Không thể tải danh mục mô hình',
    'Model is not in this catalog': 'Mô hình không có trong danh mục này',
    'This model may be unavailable to your account or absent from the catalog. Check your model ID and account access.':
      'Mô hình có thể ngoài quyền truy cập của tài khoản hoặc không có trong danh mục. Hãy kiểm tra ID mô hình và quyền truy cập.',
    'Check the model ID or sign in to see your account model catalog.':
      'Kiểm tra ID mô hình hoặc đăng nhập để xem danh mục mô hình của tài khoản.',
    'This saved reply is no longer available. Send a new message to continue.':
      'Câu trả lời đã lưu không còn khả dụng. Hãy gửi tin nhắn mới để tiếp tục.',
  },
  en: {
    'Loading status': 'Loading status',
    'Model status could not be loaded': 'Model status could not be loaded',
    'Recent calls succeeded': 'Recent calls succeeded',
    'Recent calls include failures': 'Recent calls include failures',
    'No recent model status': 'No recent model status',
    'Sign in to check model access': 'Sign in to check model access',
    'API access is required': 'API access is required',
    'Your account has an eligible model group':
      'Your account has an eligible model group',
    'No eligible model group for this account':
      'No eligible model group for this account',
    'Model availability': 'Model availability',
    'Latest observed request interval': 'Latest observed request interval',
    'Recent means within one hour. Historical calls across groups do not guarantee your next request; key restrictions and balance still apply.':
      'Recent means within one hour. Historical calls across groups do not guarantee your next request; key restrictions and balance still apply.',
    'Model catalog access is required': 'Model catalog access is required',
    'Model catalog could not be loaded': 'Model catalog could not be loaded',
    'Model is not in this catalog': 'Model is not in this catalog',
    'This model may be unavailable to your account or absent from the catalog. Check your model ID and account access.':
      'This model may be unavailable to your account or absent from the catalog. Check your model ID and account access.',
    'Check the model ID or sign in to see your account model catalog.':
      'Check the model ID or sign in to see your account model catalog.',
    'This saved reply is no longer available. Send a new message to continue.':
      'This saved reply is no longer available. Send a new message to continue.',
  },
}

const logRecoveryCopy = {
  en: {
    'A request limit was reached. This record does not identify whether the limit belongs to your account or the upstream provider.':
      'A request limit was reached. This record does not identify whether the limit belongs to your account or the upstream provider.',
    'Account balance or plan quota is insufficient. Check your billing source.':
      'Account balance or plan quota is insufficient. Check your billing source.',
    'Authentication or access was rejected. Check the API key and model permissions.':
      'Authentication or access was rejected. Check the API key and model permissions.',
    'Cache read tokens': 'Cache read tokens',
    'Cache write tokens': 'Cache write tokens',
    'Charge recorded in this entry': 'Charge recorded in this entry',
    'Check API key': 'Check API key',
    'Check balance and plan': 'Check balance and plan',
    'Check client configuration': 'Check client configuration',
    'Copy Request ID': 'Copy Request ID',
    'Copy safe error details': 'Copy safe error details',
    'Diagnose request limit': 'Diagnose request limit',
    'Help me diagnose this API request.': 'Help me diagnose this API request.',
    'Not recorded': 'Not recorded',
    'Plan quota used in this entry': 'Plan quota used in this entry',
    'Refund recorded in this entry': 'Refund recorded in this entry',
    'Request and billing summary': 'Request and billing summary',
    'Review the request details with the assistant before retrying.':
      'Review the request details with the assistant before retrying.',
    'The API key quota could not be reserved. Check the key limit and availability.':
      'The API key quota could not be reserved. Check the key limit and availability.',
    'The endpoint or model was not found. Check the client configuration and model access.':
      'The endpoint or model was not found. Check the client configuration and model access.',
    'This entry does not reconcile other charges or refunds for the same request. Final net charge is not available here.':
      'This entry does not reconcile other charges or refunds for the same request. Final net charge is not available here.',
    'Only diagnostic metadata is copied; raw error bodies are omitted.':
      'Only diagnostic metadata is copied; raw error bodies are omitted.',
  },
  zh: {
    'A request limit was reached. This record does not identify whether the limit belongs to your account or the upstream provider.':
      '请求触发了限制。这条记录无法确定限制来自你的账户还是上游服务。',
    'Account balance or plan quota is insufficient. Check your billing source.':
      '账户余额或套餐额度不足，请检查当前扣费来源。',
    'Authentication or access was rejected. Check the API key and model permissions.':
      '身份验证或访问被拒绝，请检查 API Key 和模型权限。',
    'Cache read tokens': '缓存读取 Tokens',
    'Cache write tokens': '缓存写入 Tokens',
    'Charge recorded in this entry': '本条记录扣费',
    'Check API key': '检查 API Key',
    'Check balance and plan': '检查余额与套餐',
    'Check client configuration': '检查客户端配置',
    'Copy Request ID': '复制 Request ID',
    'Copy safe error details': '复制脱敏错误详情',
    'Diagnose request limit': '排查请求限制',
    'Help me diagnose this API request.': '帮我排查这次 API 请求。',
    'Not recorded': '未记录',
    'Plan quota used in this entry': '本条记录消耗的套餐额度',
    'Refund recorded in this entry': '本条记录退款',
    'Request and billing summary': '请求与费用摘要',
    'Review the request details with the assistant before retrying.':
      '重试前，让助手一起检查请求详情。',
    'The API key quota could not be reserved. Check the key limit and availability.':
      '无法预扣 API Key 额度，请检查 Key 的额度限制和可用状态。',
    'The endpoint or model was not found. Check the client configuration and model access.':
      '未找到接口或模型，请检查客户端配置和模型访问权限。',
    'This entry does not reconcile other charges or refunds for the same request. Final net charge is not available here.':
      '此处未合并同一请求的其他扣费或退款记录，无法确定最终净扣费。',
    'Only diagnostic metadata is copied; raw error bodies are omitted.':
      '仅复制排查所需的元数据，原始错误正文不会被复制。',
  },
  'zh-TW': {
    'A request limit was reached. This record does not identify whether the limit belongs to your account or the upstream provider.':
      '請求觸發了限制。這筆紀錄無法確定限制來自你的帳戶還是上游服務。',
    'Account balance or plan quota is insufficient. Check your billing source.':
      '帳戶餘額或方案額度不足，請檢查目前的扣款來源。',
    'Authentication or access was rejected. Check the API key and model permissions.':
      '身分驗證或存取遭拒，請檢查 API Key 和模型權限。',
    'Cache read tokens': '快取讀取 Tokens',
    'Cache write tokens': '快取寫入 Tokens',
    'Charge recorded in this entry': '本筆紀錄扣款',
    'Check API key': '檢查 API Key',
    'Check balance and plan': '檢查餘額與方案',
    'Check client configuration': '檢查用戶端設定',
    'Copy Request ID': '複製 Request ID',
    'Copy safe error details': '複製已遮蔽敏感資訊的錯誤詳情',
    'Diagnose request limit': '排查請求限制',
    'Help me diagnose this API request.': '幫我排查這次 API 請求。',
    'Not recorded': '未記錄',
    'Plan quota used in this entry': '本筆紀錄使用的方案額度',
    'Refund recorded in this entry': '本筆紀錄退款',
    'Request and billing summary': '請求與費用摘要',
    'Review the request details with the assistant before retrying.':
      '重試前，請助手一起檢查請求詳情。',
    'The API key quota could not be reserved. Check the key limit and availability.':
      '無法預扣 API Key 額度，請檢查 Key 的額度限制與可用狀態。',
    'The endpoint or model was not found. Check the client configuration and model access.':
      '找不到端點或模型，請檢查用戶端設定和模型存取權限。',
    'This entry does not reconcile other charges or refunds for the same request. Final net charge is not available here.':
      '此處未合併同一請求的其他扣款或退款紀錄，無法確定最終淨扣款。',
    'Only diagnostic metadata is copied; raw error bodies are omitted.':
      '僅複製排查所需的中繼資料，不複製原始錯誤正文。',
  },
  fr: {
    'A request limit was reached. This record does not identify whether the limit belongs to your account or the upstream provider.':
      'Une limite de requêtes a été atteinte. Cette entrée ne permet pas de déterminer si elle provient de votre compte ou du fournisseur en amont.',
    'Account balance or plan quota is insufficient. Check your billing source.':
      'Le solde du compte ou le quota du forfait est insuffisant. Vérifiez la source de facturation.',
    'Authentication or access was rejected. Check the API key and model permissions.':
      'Authentification ou accès refusé. Vérifiez la clé API et les droits sur le modèle.',
    'Cache read tokens': 'Tokens lus en cache',
    'Cache write tokens': 'Tokens écrits en cache',
    'Charge recorded in this entry': 'Débit enregistré ici',
    'Check API key': 'Vérifier la clé API',
    'Check balance and plan': 'Vérifier le solde et le forfait',
    'Check client configuration': 'Vérifier la configuration du client',
    'Copy Request ID': 'Copier le Request ID',
    'Copy safe error details': 'Copier les détails expurgés',
    'Diagnose request limit': 'Diagnostiquer la limite',
    'Help me diagnose this API request.':
      'Aidez-moi à diagnostiquer cette requête API.',
    'Not recorded': 'Non enregistré',
    'Plan quota used in this entry': 'Quota du forfait utilisé ici',
    'Refund recorded in this entry': 'Remboursement enregistré ici',
    'Request and billing summary': 'Résumé de la requête et des frais',
    'Review the request details with the assistant before retrying.':
      'Examinez les détails avec l’assistant avant de réessayer.',
    'The API key quota could not be reserved. Check the key limit and availability.':
      'Impossible de réserver le quota de la clé API. Vérifiez sa limite et sa disponibilité.',
    'The endpoint or model was not found. Check the client configuration and model access.':
      'Point de terminaison ou modèle introuvable. Vérifiez la configuration du client et l’accès au modèle.',
    'This entry does not reconcile other charges or refunds for the same request. Final net charge is not available here.':
      'Cette entrée ne rapproche pas les autres débits ou remboursements de la même requête. Le débit net final n’est pas disponible ici.',
    'Only diagnostic metadata is copied; raw error bodies are omitted.':
      'Seules les métadonnées de diagnostic sont copiées ; le corps brut des erreurs est omis.',
  },
  ja: {
    'A request limit was reached. This record does not identify whether the limit belongs to your account or the upstream provider.':
      'リクエスト制限に達しました。この記録だけでは、アカウント側と上流プロバイダー側のどちらの制限か判断できません。',
    'Account balance or plan quota is insufficient. Check your billing source.':
      'アカウント残高またはプランの利用枠が不足しています。課金元を確認してください。',
    'Authentication or access was rejected. Check the API key and model permissions.':
      '認証またはアクセスが拒否されました。API キーとモデルの権限を確認してください。',
    'Cache read tokens': 'キャッシュ読み取りトークン',
    'Cache write tokens': 'キャッシュ書き込みトークン',
    'Charge recorded in this entry': 'この記録の請求額',
    'Check API key': 'API キーを確認',
    'Check balance and plan': '残高とプランを確認',
    'Check client configuration': 'クライアント設定を確認',
    'Copy Request ID': 'Request ID をコピー',
    'Copy safe error details': '機密情報を除いたエラー詳細をコピー',
    'Diagnose request limit': 'リクエスト制限を調べる',
    'Help me diagnose this API request.':
      'この API リクエストの問題を調べてください。',
    'Not recorded': '記録なし',
    'Plan quota used in this entry': 'この記録のプラン利用量',
    'Refund recorded in this entry': 'この記録の返金額',
    'Request and billing summary': 'リクエストと料金の概要',
    'Review the request details with the assistant before retrying.':
      '再試行する前に、アシスタントとリクエストの詳細を確認してください。',
    'The API key quota could not be reserved. Check the key limit and availability.':
      'API キーの利用枠を仮確保できませんでした。キーの上限と利用状態を確認してください。',
    'The endpoint or model was not found. Check the client configuration and model access.':
      'エンドポイントまたはモデルが見つかりません。クライアント設定とモデルのアクセス権を確認してください。',
    'This entry does not reconcile other charges or refunds for the same request. Final net charge is not available here.':
      'ここでは同じリクエストの他の請求や返金と照合していないため、最終的な差引請求額は確認できません。',
    'Only diagnostic metadata is copied; raw error bodies are omitted.':
      '診断用のメタデータのみコピーし、エラーの元の本文は除外します。',
  },
  ru: {
    'A request limit was reached. This record does not identify whether the limit belongs to your account or the upstream provider.':
      'Достигнут лимит запросов. Эта запись не позволяет определить, установлен ли он для вашего аккаунта или вышестоящим провайдером.',
    'Account balance or plan quota is insufficient. Check your billing source.':
      'Недостаточно средств или квоты тарифа. Проверьте источник оплаты.',
    'Authentication or access was rejected. Check the API key and model permissions.':
      'В аутентификации или доступе отказано. Проверьте API-ключ и права на модель.',
    'Cache read tokens': 'Токены чтения кэша',
    'Cache write tokens': 'Токены записи кэша',
    'Charge recorded in this entry': 'Списание в этой записи',
    'Check API key': 'Проверить API-ключ',
    'Check balance and plan': 'Проверить баланс и тариф',
    'Check client configuration': 'Проверить настройки клиента',
    'Copy Request ID': 'Скопировать Request ID',
    'Copy safe error details': 'Скопировать очищенные сведения об ошибке',
    'Diagnose request limit': 'Проверить лимит запросов',
    'Help me diagnose this API request.':
      'Помогите разобраться с этим API-запросом.',
    'Not recorded': 'Не записано',
    'Plan quota used in this entry':
      'Квота тарифа, использованная в этой записи',
    'Refund recorded in this entry': 'Возврат в этой записи',
    'Request and billing summary': 'Сводка запроса и оплаты',
    'Review the request details with the assistant before retrying.':
      'Перед повторной попыткой проверьте сведения о запросе с помощником.',
    'The API key quota could not be reserved. Check the key limit and availability.':
      'Не удалось зарезервировать квоту API-ключа. Проверьте лимит и доступность ключа.',
    'The endpoint or model was not found. Check the client configuration and model access.':
      'Конечная точка или модель не найдена. Проверьте настройки клиента и доступ к модели.',
    'This entry does not reconcile other charges or refunds for the same request. Final net charge is not available here.':
      'Другие списания и возвраты по этому запросу здесь не сопоставляются. Итоговое списание за вычетом возвратов недоступно.',
    'Only diagnostic metadata is copied; raw error bodies are omitted.':
      'Копируются только диагностические метаданные; исходное тело ошибки исключается.',
  },
  vi: {
    'A request limit was reached. This record does not identify whether the limit belongs to your account or the upstream provider.':
      'Đã đạt giới hạn yêu cầu. Bản ghi này không xác định được giới hạn thuộc tài khoản của bạn hay nhà cung cấp phía trên.',
    'Account balance or plan quota is insufficient. Check your billing source.':
      'Số dư tài khoản hoặc hạn mức gói không đủ. Hãy kiểm tra nguồn thanh toán.',
    'Authentication or access was rejected. Check the API key and model permissions.':
      'Xác thực hoặc quyền truy cập bị từ chối. Hãy kiểm tra khóa API và quyền dùng mô hình.',
    'Cache read tokens': 'Token đọc bộ nhớ đệm',
    'Cache write tokens': 'Token ghi bộ nhớ đệm',
    'Charge recorded in this entry': 'Khoản trừ trong bản ghi này',
    'Check API key': 'Kiểm tra khóa API',
    'Check balance and plan': 'Kiểm tra số dư và gói',
    'Check client configuration': 'Kiểm tra cấu hình ứng dụng',
    'Copy Request ID': 'Sao chép Request ID',
    'Copy safe error details': 'Sao chép lỗi đã ẩn thông tin nhạy cảm',
    'Diagnose request limit': 'Kiểm tra giới hạn yêu cầu',
    'Help me diagnose this API request.':
      'Hãy giúp tôi chẩn đoán yêu cầu API này.',
    'Not recorded': 'Chưa ghi nhận',
    'Plan quota used in this entry': 'Hạn mức gói đã dùng trong bản ghi này',
    'Refund recorded in this entry': 'Khoản hoàn trong bản ghi này',
    'Request and billing summary': 'Tóm tắt yêu cầu và chi phí',
    'Review the request details with the assistant before retrying.':
      'Kiểm tra chi tiết yêu cầu cùng trợ lý trước khi thử lại.',
    'The API key quota could not be reserved. Check the key limit and availability.':
      'Không thể giữ trước hạn mức khóa API. Hãy kiểm tra giới hạn và trạng thái sử dụng của khóa.',
    'The endpoint or model was not found. Check the client configuration and model access.':
      'Không tìm thấy điểm cuối hoặc mô hình. Hãy kiểm tra cấu hình ứng dụng và quyền truy cập mô hình.',
    'This entry does not reconcile other charges or refunds for the same request. Final net charge is not available here.':
      'Bản ghi này không đối chiếu các khoản trừ hoặc hoàn khác của cùng yêu cầu. Chưa thể xác định khoản trừ ròng cuối cùng tại đây.',
    'Only diagnostic metadata is copied; raw error bodies are omitted.':
      'Chỉ sao chép siêu dữ liệu chẩn đoán; nội dung lỗi gốc được loại bỏ.',
  },
}

const sourceFeedbackCopy = {
  en: {
    'How did you first hear about LMM? (optional)':
      'How did you first hear about LMM? (optional)',
    'Content title or platform (optional, no URLs)':
      'Content title or platform (optional, no URLs)',
    Skip: 'Skip',
    'Unable to save source feedback. Please retry.':
      'Unable to save source feedback. Please retry.',
    'Search engine': 'Search engine',
    Community: 'Community',
    'Social media': 'Social media',
    Documentation: 'Documentation',
    'Friend recommendation': 'Friend recommendation',
    'Client recommendation': 'Client recommendation',
    'Self-reported source': 'Self-reported source',
    'Optional source feedback is kept for 365 days, separate from automatic attribution. Skip or delete it anytime. Do not include personal details or credentials.':
      'Optional source feedback is kept for 365 days, separate from automatic attribution. Skip or delete it anytime. Do not include personal details or credentials.',
    'Original request submitted': 'Original request submitted',
    'Review completed': 'Review completed',
    'Describe your intended API use in 5–2000 characters. Do not include credentials.':
      'Describe your intended API use in 5–2000 characters. Do not include credentials.',
    'Submitting replaces this application and sends it for review again. The previous review note is cleared; the AI recommendation is retained.':
      'Submitting replaces this application and sends it for review again. The previous review note is cleared; the AI recommendation is retained.',
    'Could not submit the access request. Your text is preserved; check the status and try again.':
      'Could not submit the access request. Your text is preserved; check the status and try again.',
    'Confirm and submit application': 'Confirm and submit application',
    'You can browse challenges and ask the AI assistant to apply for API access.':
      'You can browse challenges and ask the AI assistant to apply for API access.',
    'Describe your intended API use to the AI assistant. Eligible applications may be approved automatically; others remain under review.':
      'Describe your intended API use to the AI assistant. Eligible applications may be approved automatically; others remain under review.',
    'For example: help me apply for API access and configure CC Switch':
      'For example: help me apply for API access and configure CC Switch',
    'No external referrer was observed in this preview. A tagged link does not prove where someone saw it.':
      'No external referrer was observed in this preview. A tagged link does not prove where someone saw it.',
    'Not provided': 'Not provided',
    'Open target in test mode': 'Open target in test mode',
    'Preview requests and test links are excluded from acquisition statistics.':
      'Preview requests and test links are excluded from acquisition statistics.',
    'Preview source recognition without recording a visit.':
      'Preview source recognition without recording a visit.',
    'Promotion link preview': 'Promotion link preview',
    'Recognition basis: saved promotion link identifier.':
      'Recognition basis: saved promotion link identifier.',
    'Unable to preview this promotion link.':
      'Unable to preview this promotion link.',
  },
  zh: {
    'How did you first hear about LMM? (optional)':
      '你最初从哪里知道 LMM？（可跳过）',
    'Content title or platform (optional, no URLs)':
      '内容标题或平台（可不填，请勿输入网址）',
    Skip: '跳过',
    'Unable to save source feedback. Please retry.':
      '来源说明保存失败，请重试。',
    'Search engine': '搜索引擎',
    Community: '社区',
    'Social media': '社交平台',
    Documentation: '项目文档',
    'Friend recommendation': '朋友推荐',
    'Client recommendation': '客户端推荐',
    'Self-reported source': '用户自述来源',
    'Optional source feedback is kept for 365 days, separate from automatic attribution. Skip or delete it anytime. Do not include personal details or credentials.':
      '来源说明自愿填写，保存 365 天，与自动归属分开记录。可随时跳过或删除，请勿填写个人隐私或凭据。',
    'Original request submitted': '首次申请时间',
    'Review completed': '审核完成时间',
    'Describe your intended API use in 5–2000 characters. Do not include credentials.':
      '请用 5–2000 个字符说明 API 用途，不要包含凭证。',
    'Submitting replaces this application and sends it for review again. The previous review note is cleared; the AI recommendation is retained.':
      '提交后将替换当前申请并重新审核。此前的审核备注会被清除，AI 建议会保留。',
    'Could not submit the access request. Your text is preserved; check the status and try again.':
      '申请未能提交。已保留你的文字，请检查状态后重试。',
    'Confirm and submit application': '确认并提交申请',
    'You can browse challenges and ask the AI assistant to apply for API access.':
      '你可以浏览挑战，并请 AI 助手协助申请 API 访问权限。',
    'Describe your intended API use to the AI assistant. Eligible applications may be approved automatically; others remain under review.':
      '向 AI 助手说明 API 用途。符合条件的申请可能自动通过，其他申请继续等待审核。',
    'For example: help me apply for API access and configure CC Switch':
      '例如：帮我申请 API 访问权限并配置 CC Switch',
    'No external referrer was observed in this preview. A tagged link does not prove where someone saw it.':
      '此预览没有观察到外部来源网站。使用带标记的链接，并不能证明用户在哪里看到它。',
    'Not provided': '未提供',
    'Open target in test mode': '以测试模式打开目标页面',
    'Preview requests and test links are excluded from acquisition statistics.':
      '预览请求和测试链接不计入来源统计。',
    'Preview source recognition without recording a visit.':
      '预览来源识别结果，不记录访问。',
    'Promotion link preview': '推广链接预览',
    'Recognition basis: saved promotion link identifier.':
      '识别依据：已保存的推广链接标识。',
    'Unable to preview this promotion link.': '无法预览此推广链接。',
  },
  'zh-TW': {
    'How did you first hear about LMM? (optional)':
      '你最初從哪裡知道 LMM？（可略過）',
    'Content title or platform (optional, no URLs)':
      '內容標題或平台（可不填，請勿輸入網址）',
    Skip: '略過',
    'Unable to save source feedback. Please retry.':
      '來源說明儲存失敗，請重試。',
    'Search engine': '搜尋引擎',
    Community: '社群',
    'Social media': '社群平台',
    Documentation: '專案文件',
    'Friend recommendation': '朋友推薦',
    'Client recommendation': '用戶端推薦',
    'Self-reported source': '使用者自述來源',
    'Optional source feedback is kept for 365 days, separate from automatic attribution. Skip or delete it anytime. Do not include personal details or credentials.':
      '來源說明自願填寫，保存 365 天，與自動歸屬分開記錄。可隨時略過或刪除，請勿填寫個人隱私或憑據。',
    'Original request submitted': '首次申請時間',
    'Review completed': '審核完成時間',
    'Describe your intended API use in 5–2000 characters. Do not include credentials.':
      '請用 5–2000 個字元說明 API 用途，不要包含憑證。',
    'Submitting replaces this application and sends it for review again. The previous review note is cleared; the AI recommendation is retained.':
      '提交後將替換目前申請並重新審核。先前的審核備註會被清除，AI 建議會保留。',
    'Could not submit the access request. Your text is preserved; check the status and try again.':
      '申請未能提交。已保留你的文字，請檢查狀態後重試。',
    'Confirm and submit application': '確認並提交申請',
    'You can browse challenges and ask the AI assistant to apply for API access.':
      '你可以瀏覽挑戰，並請 AI 助手協助申請 API 存取權限。',
    'Describe your intended API use to the AI assistant. Eligible applications may be approved automatically; others remain under review.':
      '向 AI 助手說明 API 用途。符合條件的申請可能自動通過，其他申請繼續等待審核。',
    'For example: help me apply for API access and configure CC Switch':
      '例如：幫我申請 API 存取權限並設定 CC Switch',
    'No external referrer was observed in this preview. A tagged link does not prove where someone saw it.':
      '此預覽未觀察到外部來源網站。使用帶標記的連結，不能證明使用者在哪裡看到它。',
    'Not provided': '未提供',
    'Open target in test mode': '以測試模式開啟目標頁面',
    'Preview requests and test links are excluded from acquisition statistics.':
      '預覽請求和測試連結不計入來源統計。',
    'Preview source recognition without recording a visit.':
      '預覽來源辨識結果，不記錄造訪。',
    'Promotion link preview': '推廣連結預覽',
    'Recognition basis: saved promotion link identifier.':
      '辨識依據：已儲存的推廣連結識別碼。',
    'Unable to preview this promotion link.': '無法預覽此推廣連結。',
  },
  fr: {
    'How did you first hear about LMM? (optional)':
      'Comment avez-vous découvert LMM ? (facultatif)',
    'Content title or platform (optional, no URLs)':
      'Titre ou plateforme (facultatif, sans URL)',
    Skip: 'Passer',
    'Unable to save source feedback. Please retry.':
      'Impossible d’enregistrer la réponse. Réessayez.',
    'Search engine': 'Moteur de recherche',
    Community: 'Communauté',
    'Social media': 'Réseaux sociaux',
    Documentation: 'Documentation',
    'Friend recommendation': 'Recommandation d’un proche',
    'Client recommendation': 'Recommandation d’un client logiciel',
    'Self-reported source': 'Source déclarée',
    'Optional source feedback is kept for 365 days, separate from automatic attribution. Skip or delete it anytime. Do not include personal details or credentials.':
      'Réponse facultative conservée 365 jours, séparément de l’attribution automatique. Vous pouvez passer ou la supprimer. N’indiquez aucune donnée personnelle ni aucun identifiant secret.',
    'Original request submitted': 'Première demande envoyée',
    'Review completed': 'Examen terminé',
    'Describe your intended API use in 5–2000 characters. Do not include credentials.':
      'Décrivez votre usage de l’API en 5 à 2000 caractères. Ne saisissez aucun identifiant secret.',
    'Submitting replaces this application and sends it for review again. The previous review note is cleared; the AI recommendation is retained.':
      'L’envoi remplace cette demande et relance son examen. La note précédente est effacée ; la recommandation IA est conservée.',
    'Could not submit the access request. Your text is preserved; check the status and try again.':
      'Impossible d’envoyer la demande. Votre texte est conservé ; vérifiez le statut et réessayez.',
    'Confirm and submit application': 'Confirmer et envoyer la demande',
    'You can browse challenges and ask the AI assistant to apply for API access.':
      'Vous pouvez parcourir les défis et demander à l’assistant IA de vous aider à obtenir l’accès API.',
    'Describe your intended API use to the AI assistant. Eligible applications may be approved automatically; others remain under review.':
      'Décrivez votre usage prévu de l’API à l’assistant IA. Les demandes admissibles peuvent être approuvées automatiquement ; les autres restent en examen.',
    'For example: help me apply for API access and configure CC Switch':
      'Par exemple : aidez-moi à demander l’accès API et à configurer CC Switch',
    'No external referrer was observed in this preview. A tagged link does not prove where someone saw it.':
      'Aucun site référent externe n’a été observé dans cet aperçu. Un lien balisé ne prouve pas où il a été vu.',
    'Not provided': 'Non fourni',
    'Open target in test mode': 'Ouvrir la cible en mode test',
    'Preview requests and test links are excluded from acquisition statistics.':
      'Les aperçus et les liens de test sont exclus des statistiques d’acquisition.',
    'Preview source recognition without recording a visit.':
      'Prévisualisez l’identification de la source sans enregistrer de visite.',
    'Promotion link preview': 'Aperçu du lien promotionnel',
    'Recognition basis: saved promotion link identifier.':
      'Base d’identification : identifiant du lien promotionnel enregistré.',
    'Unable to preview this promotion link.':
      'Impossible de prévisualiser ce lien promotionnel.',
  },
  ja: {
    'How did you first hear about LMM? (optional)':
      'LMM を最初に知ったきっかけは？（任意）',
    'Content title or platform (optional, no URLs)':
      'コンテンツ名またはプラットフォーム（任意、URL 不可）',
    Skip: 'スキップ',
    'Unable to save source feedback. Please retry.':
      '回答を保存できませんでした。再試行してください。',
    'Search engine': '検索エンジン',
    Community: 'コミュニティ',
    'Social media': 'SNS',
    Documentation: 'ドキュメント',
    'Friend recommendation': '友人の紹介',
    'Client recommendation': 'クライアントアプリの紹介',
    'Self-reported source': 'ユーザー申告の流入元',
    'Optional source feedback is kept for 365 days, separate from automatic attribution. Skip or delete it anytime. Do not include personal details or credentials.':
      '任意の回答は自動判定とは別に365日間保存されます。いつでもスキップまたは削除できます。個人情報や認証情報は入力しないでください。',
    'Original request submitted': '初回申請日時',
    'Review completed': '審査完了日時',
    'Describe your intended API use in 5–2000 characters. Do not include credentials.':
      'API の用途を5～2000文字で記入してください。認証情報は含めないでください。',
    'Submitting replaces this application and sends it for review again. The previous review note is cleared; the AI recommendation is retained.':
      '送信すると現在の申請を更新して再審査します。以前の審査メモは消去され、AI の推薦文は保持されます。',
    'Could not submit the access request. Your text is preserved; check the status and try again.':
      '申請を送信できませんでした。入力内容は保持されています。状態を確認して再試行してください。',
    'Confirm and submit application': '確認して申請を送信',
    'You can browse challenges and ask the AI assistant to apply for API access.':
      'チャレンジを閲覧し、AI アシスタントに API 利用権限の申請を相談できます。',
    'Describe your intended API use to the AI assistant. Eligible applications may be approved automatically; others remain under review.':
      'AI アシスタントに API の用途を説明してください。条件を満たす申請は自動承認される場合があり、それ以外は審査を待ちます。',
    'For example: help me apply for API access and configure CC Switch':
      '例：API 利用権限の申請と CC Switch の設定を手伝ってください',
    'No external referrer was observed in this preview. A tagged link does not prove where someone saw it.':
      'このプレビューでは外部参照元は観測されていません。タグ付きリンクだけでは、そのリンクを見た場所を証明できません。',
    'Not provided': '未提供',
    'Open target in test mode': 'テストモードで対象ページを開く',
    'Preview requests and test links are excluded from acquisition statistics.':
      'プレビューのリクエストとテストリンクは流入統計に含まれません。',
    'Preview source recognition without recording a visit.':
      '訪問を記録せずに流入元の識別結果を確認します。',
    'Promotion link preview': 'プロモーションリンクのプレビュー',
    'Recognition basis: saved promotion link identifier.':
      '識別の根拠：保存済みプロモーションリンクの識別子。',
    'Unable to preview this promotion link.':
      'このプロモーションリンクをプレビューできません。',
  },
  ru: {
    'How did you first hear about LMM? (optional)':
      'Как вы впервые узнали о LMM? (необязательно)',
    'Content title or platform (optional, no URLs)':
      'Название материала или платформа (необязательно, без URL)',
    Skip: 'Пропустить',
    'Unable to save source feedback. Please retry.':
      'Не удалось сохранить ответ. Повторите попытку.',
    'Search engine': 'Поисковая система',
    Community: 'Сообщество',
    'Social media': 'Социальные сети',
    Documentation: 'Документация',
    'Friend recommendation': 'Рекомендация знакомых',
    'Client recommendation': 'Рекомендация приложения',
    'Self-reported source': 'Источник со слов пользователя',
    'Optional source feedback is kept for 365 days, separate from automatic attribution. Skip or delete it anytime. Do not include personal details or credentials.':
      'Добровольный ответ хранится 365 дней отдельно от автоматической атрибуции. Его можно пропустить или удалить. Не указывайте личные данные или учётные секреты.',
    'Original request submitted': 'Дата первой заявки',
    'Review completed': 'Рассмотрение завершено',
    'Describe your intended API use in 5–2000 characters. Do not include credentials.':
      'Опишите предполагаемое использование API в 5–2000 символах. Не указывайте секретные данные.',
    'Submitting replaces this application and sends it for review again. The previous review note is cleared; the AI recommendation is retained.':
      'Отправка заменит текущую заявку и запустит повторное рассмотрение. Предыдущее примечание будет удалено, рекомендация ИИ сохранится.',
    'Could not submit the access request. Your text is preserved; check the status and try again.':
      'Не удалось отправить заявку. Текст сохранён; проверьте статус и повторите попытку.',
    'Confirm and submit application': 'Подтвердить и отправить заявку',
    'You can browse challenges and ask the AI assistant to apply for API access.':
      'Вы можете просматривать задания и обратиться к ИИ-помощнику для подачи заявки на доступ к API.',
    'Describe your intended API use to the AI assistant. Eligible applications may be approved automatically; others remain under review.':
      'Опишите ИИ-помощнику предполагаемое использование API. Подходящие заявки могут одобряться автоматически; остальные остаются на рассмотрении.',
    'For example: help me apply for API access and configure CC Switch':
      'Например: помоги подать заявку на доступ к API и настроить CC Switch',
    'No external referrer was observed in this preview. A tagged link does not prove where someone saw it.':
      'В этом предпросмотре внешний источник перехода не наблюдался. Метка ссылки не доказывает, где пользователь её увидел.',
    'Not provided': 'Не указано',
    'Open target in test mode': 'Открыть целевую страницу в тестовом режиме',
    'Preview requests and test links are excluded from acquisition statistics.':
      'Предпросмотры и тестовые ссылки исключены из статистики привлечения.',
    'Preview source recognition without recording a visit.':
      'Проверьте определение источника без записи посещения.',
    'Promotion link preview': 'Предпросмотр рекламной ссылки',
    'Recognition basis: saved promotion link identifier.':
      'Основание определения: сохранённый идентификатор рекламной ссылки.',
    'Unable to preview this promotion link.':
      'Не удалось открыть предпросмотр рекламной ссылки.',
  },
  vi: {
    'How did you first hear about LMM? (optional)':
      'Bạn biết đến LMM lần đầu từ đâu? (không bắt buộc)',
    'Content title or platform (optional, no URLs)':
      'Tên nội dung hoặc nền tảng (tùy chọn, không nhập URL)',
    Skip: 'Bỏ qua',
    'Unable to save source feedback. Please retry.':
      'Không lưu được câu trả lời. Vui lòng thử lại.',
    'Search engine': 'Công cụ tìm kiếm',
    Community: 'Cộng đồng',
    'Social media': 'Mạng xã hội',
    Documentation: 'Tài liệu dự án',
    'Friend recommendation': 'Bạn bè giới thiệu',
    'Client recommendation': 'Ứng dụng giới thiệu',
    'Self-reported source': 'Nguồn do người dùng khai báo',
    'Optional source feedback is kept for 365 days, separate from automatic attribution. Skip or delete it anytime. Do not include personal details or credentials.':
      'Câu trả lời tự nguyện được lưu 365 ngày, tách biệt với nguồn tự động. Có thể bỏ qua hoặc xóa bất cứ lúc nào. Không nhập thông tin cá nhân hay thông tin xác thực.',
    'Original request submitted': 'Thời điểm gửi đơn đầu tiên',
    'Review completed': 'Đã hoàn tất xét duyệt',
    'Describe your intended API use in 5–2000 characters. Do not include credentials.':
      'Mô tả mục đích dùng API bằng 5–2000 ký tự. Không đưa thông tin xác thực vào.',
    'Submitting replaces this application and sends it for review again. The previous review note is cleared; the AI recommendation is retained.':
      'Gửi sẽ thay thế đơn hiện tại và xét duyệt lại. Ghi chú xét duyệt cũ sẽ bị xóa; đề xuất AI được giữ lại.',
    'Could not submit the access request. Your text is preserved; check the status and try again.':
      'Không thể gửi đơn. Nội dung của bạn được giữ lại; hãy kiểm tra trạng thái và thử lại.',
    'Confirm and submit application': 'Xác nhận và gửi đơn',
    'You can browse challenges and ask the AI assistant to apply for API access.':
      'Bạn có thể xem thử thách và nhờ trợ lý AI hỗ trợ xin quyền truy cập API.',
    'Describe your intended API use to the AI assistant. Eligible applications may be approved automatically; others remain under review.':
      'Mô tả mục đích dùng API cho trợ lý AI. Đơn đủ điều kiện có thể được duyệt tự động; các đơn khác tiếp tục chờ xét duyệt.',
    'For example: help me apply for API access and configure CC Switch':
      'Ví dụ: giúp tôi xin quyền truy cập API và cấu hình CC Switch',
    'No external referrer was observed in this preview. A tagged link does not prove where someone saw it.':
      'Không ghi nhận trang giới thiệu bên ngoài trong bản xem trước này. Liên kết có gắn thẻ không chứng minh người dùng đã thấy nó ở đâu.',
    'Not provided': 'Chưa cung cấp',
    'Open target in test mode': 'Mở trang đích ở chế độ thử nghiệm',
    'Preview requests and test links are excluded from acquisition statistics.':
      'Yêu cầu xem trước và liên kết thử nghiệm không được tính vào thống kê nguồn khách hàng.',
    'Preview source recognition without recording a visit.':
      'Xem trước kết quả nhận diện nguồn mà không ghi nhận lượt truy cập.',
    'Promotion link preview': 'Xem trước liên kết quảng bá',
    'Recognition basis: saved promotion link identifier.':
      'Căn cứ nhận diện: mã liên kết quảng bá đã lưu.',
    'Unable to preview this promotion link.':
      'Không thể xem trước liên kết quảng bá này.',
  },
}

const clientPresetsCopy = {
  zh: {
    'In AstrBot, open Providers → Chat Completion and add an OpenAI Compatible provider. Fill in the fields below, save and fetch models, enable your model, then select it under Config → AI → Model.':
      '在 AstrBot 中打开「服务提供商 → 对话补全」，添加 OpenAI 兼容提供商。填写下方内容，保存并获取模型，启用模型，再到「配置 → AI → 模型」中选用。',
    'Install Python and the SDK. Copy the example into a .py file and run it; enter your LMM API key only in the local password prompt. Choose a model supporting this SDK protocol.':
      '安装 Python 和 SDK，将示例复制到 .py 文件后运行。仅在本地密码提示中输入 LMM API Key，并选择支持该 SDK 接口的模型。',
    'Install SDK': '安装 SDK',
    'Python request example': 'Python 请求示例',
    'Running this example sends one request and may use your balance. Check its result in Usage Logs.':
      '运行示例会发送一次请求，可能消耗余额。请在调用记录中查看结果。',
    'Install Pi': '安装 Pi',
    'Sign in with OAuth': '通过 OAuth 登录',
    'Pi uses browser authorization. You do not need to create or paste a manual API key.':
      'Pi 使用浏览器授权，无需手动创建或粘贴 API Key。',
    'You can install clients during review. API requests become available after access is approved.':
      '审核期间可以先安装客户端。权限通过后即可发送 API 请求。',
  },
  'zh-TW': {
    'In AstrBot, open Providers → Chat Completion and add an OpenAI Compatible provider. Fill in the fields below, save and fetch models, enable your model, then select it under Config → AI → Model.':
      '在 AstrBot 中開啟「服務提供商 → 對話補全」，新增 OpenAI 相容提供商。填寫下方內容，儲存並取得模型，啟用模型，再到「設定 → AI → 模型」中選用。',
    'Install Python and the SDK. Copy the example into a .py file and run it; enter your LMM API key only in the local password prompt. Choose a model supporting this SDK protocol.':
      '安裝 Python 和 SDK，將範例複製到 .py 檔案後執行。僅在本機密碼提示中輸入 LMM API Key，並選擇支援該 SDK 介面的模型。',
    'Install SDK': '安裝 SDK',
    'Python request example': 'Python 請求範例',
    'Running this example sends one request and may use your balance. Check its result in Usage Logs.':
      '執行範例會傳送一次請求，可能消耗餘額。請在呼叫記錄中查看結果。',
    'Install Pi': '安裝 Pi',
    'Sign in with OAuth': '透過 OAuth 登入',
    'Pi uses browser authorization. You do not need to create or paste a manual API key.':
      'Pi 使用瀏覽器授權，無需手動建立或貼上 API Key。',
    'You can install clients during review. API requests become available after access is approved.':
      '審核期間可以先安裝用戶端。權限通過後即可傳送 API 請求。',
  },
  fr: {
    'In AstrBot, open Providers → Chat Completion and add an OpenAI Compatible provider. Fill in the fields below, save and fetch models, enable your model, then select it under Config → AI → Model.':
      'Dans AstrBot, ouvrez Providers → Chat Completion et ajoutez un fournisseur OpenAI Compatible. Remplissez les champs ci-dessous, enregistrez et récupérez les modèles, activez le vôtre, puis sélectionnez-le dans Config → AI → Model.',
    'Install Python and the SDK. Copy the example into a .py file and run it; enter your LMM API key only in the local password prompt. Choose a model supporting this SDK protocol.':
      'Installez Python et le SDK. Copiez l’exemple dans un fichier .py et exécutez-le ; saisissez votre clé API LMM uniquement dans l’invite locale de mot de passe. Choisissez un modèle compatible avec le protocole du SDK.',
    'Install SDK': 'Installer le SDK',
    'Python request example': 'Exemple de requête Python',
    'Running this example sends one request and may use your balance. Check its result in Usage Logs.':
      'Cet exemple envoie une requête et peut consommer votre solde. Consultez son résultat dans les journaux d’utilisation.',
    'Install Pi': 'Installer Pi',
    'Sign in with OAuth': 'Connexion avec OAuth',
    'Pi uses browser authorization. You do not need to create or paste a manual API key.':
      'Pi utilise l’autorisation du navigateur. Aucune création ni saisie manuelle de clé API n’est nécessaire.',
    'You can install clients during review. API requests become available after access is approved.':
      'Vous pouvez installer les clients pendant l’examen. Les requêtes API sont disponibles une fois l’accès approuvé.',
  },
  ja: {
    'In AstrBot, open Providers → Chat Completion and add an OpenAI Compatible provider. Fill in the fields below, save and fetch models, enable your model, then select it under Config → AI → Model.':
      'AstrBot で Providers → Chat Completion を開き、OpenAI Compatible を追加します。以下の項目を入力して保存し、モデルを取得・有効化した後、Config → AI → Model で選択してください。',
    'Install Python and the SDK. Copy the example into a .py file and run it; enter your LMM API key only in the local password prompt. Choose a model supporting this SDK protocol.':
      'Python と SDK をインストールし、例を .py ファイルにコピーして実行します。LMM API キーはローカルのパスワード入力画面だけに入力し、この SDK のプロトコルに対応するモデルを選んでください。',
    'Install SDK': 'SDK をインストール',
    'Python request example': 'Python リクエスト例',
    'Running this example sends one request and may use your balance. Check its result in Usage Logs.':
      'この例を実行するとリクエストが1回送信され、残高を消費する場合があります。呼び出し履歴で結果を確認してください。',
    'Install Pi': 'Pi をインストール',
    'Sign in with OAuth': 'OAuth でログイン',
    'Pi uses browser authorization. You do not need to create or paste a manual API key.':
      'Pi はブラウザーで認可します。API キーを手動で作成・貼り付ける必要はありません。',
    'You can install clients during review. API requests become available after access is approved.':
      '審査中にクライアントをインストールできます。承認後に API リクエストを送信できます。',
  },
  ru: {
    'In AstrBot, open Providers → Chat Completion and add an OpenAI Compatible provider. Fill in the fields below, save and fetch models, enable your model, then select it under Config → AI → Model.':
      'В AstrBot откройте Providers → Chat Completion и добавьте провайдера OpenAI Compatible. Заполните поля ниже, сохраните настройки и загрузите модели, включите нужную модель и выберите её в Config → AI → Model.',
    'Install Python and the SDK. Copy the example into a .py file and run it; enter your LMM API key only in the local password prompt. Choose a model supporting this SDK protocol.':
      'Установите Python и SDK. Скопируйте пример в файл .py и запустите его; вводите API-ключ LMM только в локальном запросе пароля. Выберите модель, поддерживающую протокол этого SDK.',
    'Install SDK': 'Установить SDK',
    'Python request example': 'Пример запроса Python',
    'Running this example sends one request and may use your balance. Check its result in Usage Logs.':
      'Запуск примера отправляет один запрос и может списать средства с баланса. Результат смотрите в журнале вызовов.',
    'Install Pi': 'Установить Pi',
    'Sign in with OAuth': 'Войти через OAuth',
    'Pi uses browser authorization. You do not need to create or paste a manual API key.':
      'Pi использует авторизацию в браузере. Создавать или вставлять API-ключ вручную не требуется.',
    'You can install clients during review. API requests become available after access is approved.':
      'Во время рассмотрения можно установить клиенты. API-запросы станут доступны после одобрения доступа.',
  },
  vi: {
    'In AstrBot, open Providers → Chat Completion and add an OpenAI Compatible provider. Fill in the fields below, save and fetch models, enable your model, then select it under Config → AI → Model.':
      'Trong AstrBot, mở Providers → Chat Completion và thêm nhà cung cấp OpenAI Compatible. Điền các trường bên dưới, lưu và lấy danh sách mô hình, bật mô hình rồi chọn tại Config → AI → Model.',
    'Install Python and the SDK. Copy the example into a .py file and run it; enter your LMM API key only in the local password prompt. Choose a model supporting this SDK protocol.':
      'Cài Python và SDK. Sao chép ví dụ vào tệp .py rồi chạy; chỉ nhập API Key LMM tại lời nhắc mật khẩu cục bộ. Chọn mô hình hỗ trợ giao thức của SDK này.',
    'Install SDK': 'Cài SDK',
    'Python request example': 'Ví dụ yêu cầu Python',
    'Running this example sends one request and may use your balance. Check its result in Usage Logs.':
      'Chạy ví dụ sẽ gửi một yêu cầu và có thể sử dụng số dư. Xem kết quả trong nhật ký sử dụng.',
    'Install Pi': 'Cài Pi',
    'Sign in with OAuth': 'Đăng nhập bằng OAuth',
    'Pi uses browser authorization. You do not need to create or paste a manual API key.':
      'Pi dùng cấp quyền qua trình duyệt. Bạn không cần tự tạo hoặc dán API Key.',
    'You can install clients during review. API requests become available after access is approved.':
      'Bạn có thể cài ứng dụng trong lúc chờ duyệt. Yêu cầu API khả dụng sau khi được cấp quyền.',
  },
  en: {
    'In AstrBot, open Providers → Chat Completion and add an OpenAI Compatible provider. Fill in the fields below, save and fetch models, enable your model, then select it under Config → AI → Model.':
      'In AstrBot, open Providers → Chat Completion and add an OpenAI Compatible provider. Fill in the fields below, save and fetch models, enable your model, then select it under Config → AI → Model.',
    'Install Python and the SDK. Copy the example into a .py file and run it; enter your LMM API key only in the local password prompt. Choose a model supporting this SDK protocol.':
      'Install Python and the SDK. Copy the example into a .py file and run it; enter your LMM API key only in the local password prompt. Choose a model supporting this SDK protocol.',
    'Install SDK': 'Install SDK',
    'Python request example': 'Python request example',
    'Running this example sends one request and may use your balance. Check its result in Usage Logs.':
      'Running this example sends one request and may use your balance. Check its result in Usage Logs.',
    'Install Pi': 'Install Pi',
    'Sign in with OAuth': 'Sign in with OAuth',
    'Pi uses browser authorization. You do not need to create or paste a manual API key.':
      'Pi uses browser authorization. You do not need to create or paste a manual API key.',
    'You can install clients during review. API requests become available after access is approved.':
      'You can install clients during review. API requests become available after access is approved.',
  },
}

const acquisitionCostCopy = {
  en: {
    'Compare spend with attributed registrations for this promotion link only.':
      'Compare spend with attributed registrations for this promotion link only.',
    'Cost per first paying account': 'Cost per first paying account',
    'Cost per registration': 'Cost per registration',
    'First paying accounts': 'First paying accounts',
    'Observation days after registration':
      'Observation days after registration',
    'Observing until {{date}}; payer cost is not final.':
      'Observing until {{date}}; payer cost is not final.',
    'Only retained, attributed accounts are counted. Successful cash payments count once per account; gifts and API usage are excluded. No currency conversion or profit calculation is performed. Overlapping cohorts must not be added together.':
      'Only retained, attributed accounts are counted. Successful cash payments count once per account; gifts and API usage are excluded. No currency conversion or profit calculation is performed. Overlapping cohorts must not be added together.',
    'Promotion cost': 'Promotion cost',
    'Promotion cost comparison': 'Promotion cost comparison',
    'Recorded spend': 'Recorded spend',
    'Registration end, exclusive (UTC)': 'Registration end, exclusive (UTC)',
    'Registration start (UTC)': 'Registration start (UTC)',
    'Save spend': 'Save spend',
    'Select a valid cohort and currency.':
      'Select a valid cohort and currency.',
    'Some payment records lack settlement evidence. Payer cost is unavailable.':
      'Some payment records lack settlement evidence. Payer cost is unavailable.',
    'Spend for this exact cohort': 'Spend for this exact cohort',
    'Unable to load this cost comparison. Choose a cohort within the retained year.':
      'Unable to load this cost comparison. Choose a cohort within the retained year.',
    'Unable to save promotion spend.': 'Unable to save promotion spend.',
  },
  zh: {
    'Compare spend with attributed registrations for this promotion link only.':
      '仅对照这条推广链接归属的注册用户与推广支出。',
    'Cost per first paying account': '每首次付费账号成本',
    'Cost per registration': '每注册账号成本',
    'First paying accounts': '首次付费账号',
    'Observation days after registration': '注册后观察天数',
    'Observing until {{date}}; payer cost is not final.':
      '观察至 {{date}}，付费获客成本尚未定案。',
    'Only retained, attributed accounts are counted. Successful cash payments count once per account; gifts and API usage are excluded. No currency conversion or profit calculation is performed. Overlapping cohorts must not be added together.':
      '只统计保留期内且能归属来源的账号。成功现金付款按账号去重，不计赠送余额和 API 消费。不换算币种，不计算利润。重叠批次不能相加。',
    'Promotion cost': '推广成本',
    'Promotion cost comparison': '推广成本对照',
    'Recorded spend': '已录入支出',
    'Registration end, exclusive (UTC)': '注册截止日期，不含当天（UTC）',
    'Registration start (UTC)': '注册起始日期（UTC）',
    'Save spend': '保存支出',
    'Select a valid cohort and currency.': '请选择有效的注册批次和币种。',
    'Some payment records lack settlement evidence. Payer cost is unavailable.':
      '部分付款记录缺少结算依据，无法计算首次付费账号成本。',
    'Spend for this exact cohort': '该批次对应的支出',
    'Unable to load this cost comparison. Choose a cohort within the retained year.':
      '无法加载成本对照，请选择保留期一年内的注册批次。',
    'Unable to save promotion spend.': '无法保存推广支出。',
  },
  'zh-TW': {
    'Compare spend with attributed registrations for this promotion link only.':
      '僅對照這條推廣連結歸屬的註冊使用者與推廣支出。',
    'Cost per first paying account': '每首次付費帳號成本',
    'Cost per registration': '每註冊帳號成本',
    'First paying accounts': '首次付費帳號',
    'Observation days after registration': '註冊後觀察天數',
    'Observing until {{date}}; payer cost is not final.':
      '觀察至 {{date}}，付費獲客成本尚未定案。',
    'Only retained, attributed accounts are counted. Successful cash payments count once per account; gifts and API usage are excluded. No currency conversion or profit calculation is performed. Overlapping cohorts must not be added together.':
      '僅統計保留期間內且能歸屬來源的帳號。成功現金付款按帳號去重，不計贈送餘額和 API 消費。不換算幣別，不計算利潤。重疊批次不可相加。',
    'Promotion cost': '推廣成本',
    'Promotion cost comparison': '推廣成本對照',
    'Recorded spend': '已輸入支出',
    'Registration end, exclusive (UTC)': '註冊截止日期，不含當天（UTC）',
    'Registration start (UTC)': '註冊起始日期（UTC）',
    'Save spend': '儲存支出',
    'Select a valid cohort and currency.': '請選擇有效的註冊批次與幣別。',
    'Some payment records lack settlement evidence. Payer cost is unavailable.':
      '部分付款紀錄缺少結算依據，無法計算首次付費帳號成本。',
    'Spend for this exact cohort': '此批次對應的支出',
    'Unable to load this cost comparison. Choose a cohort within the retained year.':
      '無法載入成本對照，請選擇保留期間一年內的註冊批次。',
    'Unable to save promotion spend.': '無法儲存推廣支出。',
  },
  fr: {
    'Compare spend with attributed registrations for this promotion link only.':
      'Comparez les dépenses aux inscriptions attribuées uniquement à ce lien promotionnel.',
    'Cost per first paying account': 'Coût par premier compte payant',
    'Cost per registration': 'Coût par inscription',
    'First paying accounts': 'Premiers comptes payants',
    'Observation days after registration':
      'Jours d’observation après inscription',
    'Observing until {{date}}; payer cost is not final.':
      'Observation jusqu’au {{date}} ; le coût par compte payant n’est pas définitif.',
    'Only retained, attributed accounts are counted. Successful cash payments count once per account; gifts and API usage are excluded. No currency conversion or profit calculation is performed. Overlapping cohorts must not be added together.':
      'Seuls les comptes conservés et attribués sont comptés. Les paiements monétaires réussis sont dédupliqués par compte ; crédits offerts et consommation API sont exclus. Aucun change ni calcul de bénéfice. N’additionnez pas les cohortes qui se chevauchent.',
    'Promotion cost': 'Coût promotionnel',
    'Promotion cost comparison': 'Comparaison des coûts promotionnels',
    'Recorded spend': 'Dépenses enregistrées',
    'Registration end, exclusive (UTC)': 'Fin des inscriptions, exclue (UTC)',
    'Registration start (UTC)': 'Début des inscriptions (UTC)',
    'Save spend': 'Enregistrer les dépenses',
    'Select a valid cohort and currency.':
      'Sélectionnez une cohorte et une devise valides.',
    'Some payment records lack settlement evidence. Payer cost is unavailable.':
      'Certains paiements manquent de preuves de règlement. Le coût par premier compte payant est indisponible.',
    'Spend for this exact cohort': 'Dépenses de cette cohorte précise',
    'Unable to load this cost comparison. Choose a cohort within the retained year.':
      'Comparaison indisponible. Choisissez une cohorte dans l’année de conservation.',
    'Unable to save promotion spend.':
      'Impossible d’enregistrer les dépenses promotionnelles.',
  },
  ja: {
    'Compare spend with attributed registrations for this promotion link only.':
      'このプロモーションリンクに帰属する登録と支出のみを比較します。',
    'Cost per first paying account': '初回支払いアカウントあたりの費用',
    'Cost per registration': '登録アカウントあたりの費用',
    'First paying accounts': '初回支払いアカウント',
    'Observation days after registration': '登録後の観測日数',
    'Observing until {{date}}; payer cost is not final.':
      '{{date}} まで観測中です。支払いアカウント獲得費用は未確定です。',
    'Only retained, attributed accounts are counted. Successful cash payments count once per account; gifts and API usage are excluded. No currency conversion or profit calculation is performed. Overlapping cohorts must not be added together.':
      '保存期間内で流入元に帰属できるアカウントのみ集計します。成功した実払いはアカウント単位で重複を除き、付与残高と API 利用額を含めません。通貨換算や利益計算は行いません。重複する登録期間の集計を合算しないでください。',
    'Promotion cost': 'プロモーション費用',
    'Promotion cost comparison': 'プロモーション費用の比較',
    'Recorded spend': '記録済み支出',
    'Registration end, exclusive (UTC)': '登録終了日・当日を含まない（UTC）',
    'Registration start (UTC)': '登録開始日（UTC）',
    'Save spend': '支出を保存',
    'Select a valid cohort and currency.':
      '有効な登録期間と通貨を選択してください。',
    'Some payment records lack settlement evidence. Payer cost is unavailable.':
      '一部の支払いに決済の根拠が不足しているため、初回支払いアカウントあたりの費用を算出できません。',
    'Spend for this exact cohort': 'この登録期間に対応する支出',
    'Unable to load this cost comparison. Choose a cohort within the retained year.':
      '費用を比較できません。保存期間の1年以内の登録期間を選択してください。',
    'Unable to save promotion spend.': 'プロモーション支出を保存できません。',
  },
  ru: {
    'Compare spend with attributed registrations for this promotion link only.':
      'Сравните расходы с регистрациями, отнесёнными только к этой рекламной ссылке.',
    'Cost per first paying account': 'Стоимость первого платящего аккаунта',
    'Cost per registration': 'Стоимость регистрации',
    'First paying accounts': 'Впервые заплатившие аккаунты',
    'Observation days after registration': 'Дней наблюдения после регистрации',
    'Observing until {{date}}; payer cost is not final.':
      'Наблюдение до {{date}}; стоимость привлечения плательщика ещё не окончательная.',
    'Only retained, attributed accounts are counted. Successful cash payments count once per account; gifts and API usage are excluded. No currency conversion or profit calculation is performed. Overlapping cohorts must not be added together.':
      'Учитываются только сохранённые аккаунты с установленным источником. Успешные денежные платежи считаются один раз на аккаунт; подарки и расход API исключены. Конвертация валют и расчёт прибыли не выполняются. Пересекающиеся когорты нельзя суммировать.',
    'Promotion cost': 'Расходы на продвижение',
    'Promotion cost comparison': 'Сравнение расходов на продвижение',
    'Recorded spend': 'Записанные расходы',
    'Registration end, exclusive (UTC)':
      'Конец регистрации, не включая дату (UTC)',
    'Registration start (UTC)': 'Начало регистрации (UTC)',
    'Save spend': 'Сохранить расходы',
    'Select a valid cohort and currency.':
      'Выберите допустимую когорту и валюту.',
    'Some payment records lack settlement evidence. Payer cost is unavailable.':
      'Для некоторых платежей нет подтверждения расчёта. Стоимость первого платящего аккаунта недоступна.',
    'Spend for this exact cohort': 'Расходы именно на эту когорту',
    'Unable to load this cost comparison. Choose a cohort within the retained year.':
      'Не удалось загрузить сравнение. Выберите когорту за сохраняемый год.',
    'Unable to save promotion spend.':
      'Не удалось сохранить расходы на продвижение.',
  },
  vi: {
    'Compare spend with attributed registrations for this promotion link only.':
      'Chỉ đối chiếu chi phí với lượt đăng ký được quy cho liên kết quảng bá này.',
    'Cost per first paying account': 'Chi phí mỗi tài khoản trả tiền lần đầu',
    'Cost per registration': 'Chi phí mỗi tài khoản đăng ký',
    'First paying accounts': 'Tài khoản trả tiền lần đầu',
    'Observation days after registration': 'Số ngày theo dõi sau đăng ký',
    'Observing until {{date}}; payer cost is not final.':
      'Theo dõi đến {{date}}; chi phí thu hút người trả tiền chưa phải kết quả cuối cùng.',
    'Only retained, attributed accounts are counted. Successful cash payments count once per account; gifts and API usage are excluded. No currency conversion or profit calculation is performed. Overlapping cohorts must not be added together.':
      'Chỉ tính tài khoản còn trong thời hạn lưu giữ và có nguồn được quy thuộc. Thanh toán tiền thực thành công được tính một lần mỗi tài khoản; loại trừ số dư tặng và tiêu thụ API. Không quy đổi tiền tệ hay tính lợi nhuận. Không cộng các nhóm thời gian chồng lấp.',
    'Promotion cost': 'Chi phí quảng bá',
    'Promotion cost comparison': 'Đối chiếu chi phí quảng bá',
    'Recorded spend': 'Chi phí đã ghi nhận',
    'Registration end, exclusive (UTC)':
      'Ngày kết thúc đăng ký, không bao gồm ngày này (UTC)',
    'Registration start (UTC)': 'Ngày bắt đầu đăng ký (UTC)',
    'Save spend': 'Lưu chi phí',
    'Select a valid cohort and currency.':
      'Chọn nhóm đăng ký và loại tiền hợp lệ.',
    'Some payment records lack settlement evidence. Payer cost is unavailable.':
      'Một số khoản thanh toán thiếu bằng chứng quyết toán. Chưa thể tính chi phí mỗi tài khoản trả tiền lần đầu.',
    'Spend for this exact cohort': 'Chi phí cho đúng nhóm đăng ký này',
    'Unable to load this cost comparison. Choose a cohort within the retained year.':
      'Không thể tải đối chiếu chi phí. Hãy chọn nhóm đăng ký trong thời hạn lưu giữ một năm.',
    'Unable to save promotion spend.': 'Không thể lưu chi phí quảng bá.',
  },
}

const keyFollowthroughCopy = {
  zh: {
    'Configure with CC Switch': '使用 CC Switch 配置',
    'Revoking stops clients using this key. Confirm to continue.':
      '撤销后，使用此 Key 的客户端将停止访问。请确认后继续。',
    'Other client setup guides': '其他客户端配置指南',
    'This is the only time the full API key is available. Save it before closing; it cannot be retrieved later.':
      '这是唯一一次查看完整 API Key 的机会。关闭前请妥善保存，以后无法再次获取。',
    'Confirm revocation': '确认撤销',
    'Revoke this key': '撤销此 Key',
    'Check key connection': '检查 Key 连接',
    'I saved the key, close': '已保存 Key，关闭',
    'The connection check only verifies this key and lists models. It does not complete your first successful model request.':
      '连接检查仅验证 Key 并获取模型列表，不算完成首次成功模型请求。',
    'A single new key is shown only once. Advanced batch creation keeps the existing retrievable-key behavior.':
      '单个新 Key 只显示一次。高级批量创建保留原有可再次获取 Key 的方式。',
    'Creation could not be confirmed. Check the key list before creating another key; an existing key may need to be revoked.':
      '无法确认是否创建成功。再次创建前请检查 Key 列表；如已有 Key，可能需要先撤销。',
    'Shown only at creation. Use your saved key or revoke and replace it.':
      '仅在创建时显示。请使用已保存的 Key，或撤销后重新创建。',
    'Keys shown only at creation are excluded from batch copy.':
      '批量复制不包含仅在创建时显示的 Key。',
    'Key authentication succeeded. No model request was sent or billed.':
      'Key 验证成功，未发送模型请求，也未产生调用费用。',
    'Key check failed. Check access, expiry and allowed IP addresses.':
      'Key 检查失败，请检查访问权限、有效期和 IP 白名单。',
    'Could not revoke the key. Try again from API Keys.':
      '未能撤销 Key，请在 API Key 页面重试。',
    'Latest API request': '最近一次 API 请求',
    'Unable to load the latest request.': '无法加载最近一次请求。',
    'Your first API request will appear here. No request record is available yet.':
      '首次 API 请求的记录会显示在这里，目前暂无请求记录。',
    'Usage recorded; check details for the request outcome.':
      '已记录用量，请查看详情确认请求结果。',
  },
  'zh-TW': {
    'Configure with CC Switch': '使用 CC Switch 設定',
    'Revoking stops clients using this key. Confirm to continue.':
      '撤銷後，使用此 Key 的用戶端將停止存取。請確認後繼續。',
    'Other client setup guides': '其他用戶端設定指南',
    'This is the only time the full API key is available. Save it before closing; it cannot be retrieved later.':
      '這是唯一一次查看完整 API Key 的機會。關閉前請妥善保存，以後無法再次取得。',
    'Confirm revocation': '確認撤銷',
    'Revoke this key': '撤銷此 Key',
    'Check key connection': '檢查 Key 連線',
    'I saved the key, close': '已保存 Key，關閉',
    'The connection check only verifies this key and lists models. It does not complete your first successful model request.':
      '連線檢查僅驗證 Key 並取得模型清單，不算完成首次成功模型請求。',
    'A single new key is shown only once. Advanced batch creation keeps the existing retrievable-key behavior.':
      '單個新 Key 只顯示一次。進階批次建立保留原有可再次取得 Key 的方式。',
    'Creation could not be confirmed. Check the key list before creating another key; an existing key may need to be revoked.':
      '無法確認是否建立成功。再次建立前請檢查 Key 清單；如已有 Key，可能需要先撤銷。',
    'Shown only at creation. Use your saved key or revoke and replace it.':
      '僅在建立時顯示。請使用已保存的 Key，或撤銷後重新建立。',
    'Keys shown only at creation are excluded from batch copy.':
      '批次複製不包含僅在建立時顯示的 Key。',
    'Key authentication succeeded. No model request was sent or billed.':
      'Key 驗證成功，未傳送模型請求，也未產生呼叫費用。',
    'Key check failed. Check access, expiry and allowed IP addresses.':
      'Key 檢查失敗，請檢查存取權限、有效期和 IP 白名單。',
    'Could not revoke the key. Try again from API Keys.':
      '未能撤銷 Key，請在 API Key 頁面重試。',
    'Latest API request': '最近一次 API 請求',
    'Unable to load the latest request.': '無法載入最近一次請求。',
    'Your first API request will appear here. No request record is available yet.':
      '首次 API 請求的紀錄會顯示在這裡，目前暫無請求紀錄。',
    'Usage recorded; check details for the request outcome.':
      '已記錄用量，請查看詳情確認請求結果。',
  },
  fr: {
    'Configure with CC Switch': 'Configurer avec CC Switch',
    'Revoking stops clients using this key. Confirm to continue.':
      'La révocation bloque les clients utilisant cette clé. Confirmez pour continuer.',
    'Other client setup guides': 'Guides des autres clients',
    'This is the only time the full API key is available. Save it before closing; it cannot be retrieved later.':
      'La clé API complète est disponible uniquement maintenant. Enregistrez-la avant de fermer ; elle ne pourra plus être récupérée.',
    'Confirm revocation': 'Confirmer la révocation',
    'Revoke this key': 'Révoquer cette clé',
    'Check key connection': 'Vérifier la connexion',
    'I saved the key, close': 'Clé enregistrée, fermer',
    'The connection check only verifies this key and lists models. It does not complete your first successful model request.':
      'Cette vérification authentifie la clé et liste les modèles. Elle ne constitue pas votre première requête de modèle réussie.',
    'A single new key is shown only once. Advanced batch creation keeps the existing retrievable-key behavior.':
      'Une nouvelle clé individuelle est affichée une seule fois. La création avancée par lot conserve la récupération ultérieure des clés.',
    'Creation could not be confirmed. Check the key list before creating another key; an existing key may need to be revoked.':
      'Création non confirmée. Vérifiez la liste avant de créer une autre clé ; une clé existante pourrait devoir être révoquée.',
    'Shown only at creation. Use your saved key or revoke and replace it.':
      'Affichée uniquement à la création. Utilisez votre copie ou révoquez et remplacez la clé.',
    'Keys shown only at creation are excluded from batch copy.':
      'Les clés affichées uniquement à la création sont exclues de la copie par lot.',
    'Key authentication succeeded. No model request was sent or billed.':
      'Clé authentifiée. Aucune requête de modèle envoyée ni facturée.',
    'Key check failed. Check access, expiry and allowed IP addresses.':
      'Échec de vérification. Vérifiez les droits, l’expiration et les adresses IP autorisées.',
    'Could not revoke the key. Try again from API Keys.':
      'Impossible de révoquer la clé. Réessayez depuis la page des clés API.',
    'Latest API request': 'Dernière requête API',
    'Unable to load the latest request.':
      'Impossible de charger la dernière requête.',
    'Your first API request will appear here. No request record is available yet.':
      'Votre première requête API apparaîtra ici. Aucun enregistrement n’est disponible pour le moment.',
    'Usage recorded; check details for the request outcome.':
      'Utilisation enregistrée ; consultez les détails pour connaître le résultat.',
  },
  ja: {
    'Configure with CC Switch': 'CC Switch で設定',
    'Revoking stops clients using this key. Confirm to continue.':
      '取り消すと、このキーを使うクライアントはアクセスできなくなります。確認して続行してください。',
    'Other client setup guides': 'その他のクライアント設定ガイド',
    'This is the only time the full API key is available. Save it before closing; it cannot be retrieved later.':
      '完全な API キーを確認できるのは今回だけです。閉じる前に保存してください。後から再取得はできません。',
    'Confirm revocation': '取り消しを確認',
    'Revoke this key': 'このキーを取り消す',
    'Check key connection': 'キーの接続を確認',
    'I saved the key, close': 'キーを保存したので閉じる',
    'The connection check only verifies this key and lists models. It does not complete your first successful model request.':
      '接続確認ではキーの認証とモデル一覧の取得のみを行います。初回モデルリクエストの成功には数えません。',
    'A single new key is shown only once. Advanced batch creation keeps the existing retrievable-key behavior.':
      '新しいキーを1つ作成する場合、表示は1回だけです。高度な一括作成では、従来どおりキーを再取得できます。',
    'Creation could not be confirmed. Check the key list before creating another key; an existing key may need to be revoked.':
      '作成の完了を確認できません。再作成する前にキー一覧を確認してください。すでに作成されたキーの取り消しが必要な場合があります。',
    'Shown only at creation. Use your saved key or revoke and replace it.':
      '作成時のみ表示されます。保存したキーを使うか、取り消して再作成してください。',
    'Keys shown only at creation are excluded from batch copy.':
      '作成時のみ表示されるキーは一括コピーに含まれません。',
    'Key authentication succeeded. No model request was sent or billed.':
      'キーの認証に成功しました。モデルリクエストは送信されず、料金も発生していません。',
    'Key check failed. Check access, expiry and allowed IP addresses.':
      'キーの確認に失敗しました。権限、有効期限、許可された IP アドレスを確認してください。',
    'Could not revoke the key. Try again from API Keys.':
      'キーを取り消せませんでした。API キーページから再試行してください。',
    'Latest API request': '最新の API リクエスト',
    'Unable to load the latest request.': '最新のリクエストを読み込めません。',
    'Your first API request will appear here. No request record is available yet.':
      '最初の API リクエストの記録がここに表示されます。現在、記録はありません。',
    'Usage recorded; check details for the request outcome.':
      '使用量が記録されました。リクエストの結果は詳細で確認してください。',
  },
  ru: {
    'Configure with CC Switch': 'Настроить через CC Switch',
    'Revoking stops clients using this key. Confirm to continue.':
      'После отзыва клиенты с этим ключом потеряют доступ. Подтвердите действие.',
    'Other client setup guides': 'Настройка других клиентов',
    'This is the only time the full API key is available. Save it before closing; it cannot be retrieved later.':
      'Полный API-ключ доступен только сейчас. Сохраните его перед закрытием; получить его повторно будет невозможно.',
    'Confirm revocation': 'Подтвердить отзыв',
    'Revoke this key': 'Отозвать ключ',
    'Check key connection': 'Проверить подключение',
    'I saved the key, close': 'Ключ сохранён, закрыть',
    'The connection check only verifies this key and lists models. It does not complete your first successful model request.':
      'Проверка только проверяет ключ и получает список моделей. Она не считается первым успешным запросом к модели.',
    'A single new key is shown only once. Advanced batch creation keeps the existing retrievable-key behavior.':
      'Новый одиночный ключ показывается один раз. Расширенное пакетное создание сохраняет возможность повторного получения ключей.',
    'Creation could not be confirmed. Check the key list before creating another key; an existing key may need to be revoked.':
      'Создание не подтверждено. Перед повторной попыткой проверьте список ключей: возможно, созданный ключ нужно отозвать.',
    'Shown only at creation. Use your saved key or revoke and replace it.':
      'Показывается только при создании. Используйте сохранённую копию или отзовите и замените ключ.',
    'Keys shown only at creation are excluded from batch copy.':
      'Ключи, показываемые только при создании, исключены из пакетного копирования.',
    'Key authentication succeeded. No model request was sent or billed.':
      'Ключ прошёл проверку. Запрос к модели не отправлялся и не оплачивался.',
    'Key check failed. Check access, expiry and allowed IP addresses.':
      'Проверка ключа не удалась. Проверьте доступ, срок действия и разрешённые IP-адреса.',
    'Could not revoke the key. Try again from API Keys.':
      'Не удалось отозвать ключ. Повторите попытку на странице API-ключей.',
    'Latest API request': 'Последний API-запрос',
    'Unable to load the latest request.':
      'Не удалось загрузить последний запрос.',
    'Your first API request will appear here. No request record is available yet.':
      'Здесь появится первый API-запрос. Пока записей нет.',
    'Usage recorded; check details for the request outcome.':
      'Использование записано; результат запроса смотрите в подробностях.',
  },
  vi: {
    'Configure with CC Switch': 'Cấu hình bằng CC Switch',
    'Revoking stops clients using this key. Confirm to continue.':
      'Thu hồi sẽ ngừng quyền truy cập của ứng dụng dùng khóa này. Hãy xác nhận để tiếp tục.',
    'Other client setup guides': 'Hướng dẫn cấu hình ứng dụng khác',
    'This is the only time the full API key is available. Save it before closing; it cannot be retrieved later.':
      'Đây là lần duy nhất có thể xem API Key đầy đủ. Hãy lưu trước khi đóng; bạn sẽ không thể lấy lại sau đó.',
    'Confirm revocation': 'Xác nhận thu hồi',
    'Revoke this key': 'Thu hồi khóa này',
    'Check key connection': 'Kiểm tra kết nối khóa',
    'I saved the key, close': 'Đã lưu khóa, đóng',
    'The connection check only verifies this key and lists models. It does not complete your first successful model request.':
      'Kiểm tra chỉ xác thực khóa và lấy danh sách mô hình. Đây không được tính là yêu cầu mô hình thành công đầu tiên.',
    'A single new key is shown only once. Advanced batch creation keeps the existing retrievable-key behavior.':
      'Khóa mới tạo riêng lẻ chỉ hiển thị một lần. Tạo hàng loạt nâng cao vẫn cho phép lấy lại khóa như trước.',
    'Creation could not be confirmed. Check the key list before creating another key; an existing key may need to be revoked.':
      'Chưa xác nhận được việc tạo khóa. Kiểm tra danh sách trước khi tạo khóa khác; có thể cần thu hồi khóa đã tồn tại.',
    'Shown only at creation. Use your saved key or revoke and replace it.':
      'Chỉ hiển thị khi tạo. Dùng bản đã lưu hoặc thu hồi rồi tạo khóa thay thế.',
    'Keys shown only at creation are excluded from batch copy.':
      'Sao chép hàng loạt không bao gồm khóa chỉ hiển thị khi tạo.',
    'Key authentication succeeded. No model request was sent or billed.':
      'Xác thực khóa thành công. Không gửi yêu cầu mô hình và không phát sinh phí gọi.',
    'Key check failed. Check access, expiry and allowed IP addresses.':
      'Kiểm tra khóa thất bại. Hãy kiểm tra quyền truy cập, hạn dùng và địa chỉ IP được phép.',
    'Could not revoke the key. Try again from API Keys.':
      'Không thể thu hồi khóa. Hãy thử lại tại trang API Key.',
    'Latest API request': 'Yêu cầu API gần nhất',
    'Unable to load the latest request.': 'Không tải được yêu cầu gần nhất.',
    'Your first API request will appear here. No request record is available yet.':
      'Yêu cầu API đầu tiên sẽ xuất hiện ở đây. Hiện chưa có bản ghi.',
    'Usage recorded; check details for the request outcome.':
      'Đã ghi nhận mức sử dụng; xem chi tiết để biết kết quả yêu cầu.',
  },
  en: {
    'Configure with CC Switch': 'Configure with CC Switch',
    'Revoking stops clients using this key. Confirm to continue.':
      'Revoking stops clients using this key. Confirm to continue.',
    'Other client setup guides': 'Other client setup guides',
    'This is the only time the full API key is available. Save it before closing; it cannot be retrieved later.':
      'This is the only time the full API key is available. Save it before closing; it cannot be retrieved later.',
    'Confirm revocation': 'Confirm revocation',
    'Revoke this key': 'Revoke this key',
    'Check key connection': 'Check key connection',
    'I saved the key, close': 'I saved the key, close',
    'The connection check only verifies this key and lists models. It does not complete your first successful model request.':
      'The connection check only verifies this key and lists models. It does not complete your first successful model request.',
    'A single new key is shown only once. Advanced batch creation keeps the existing retrievable-key behavior.':
      'A single new key is shown only once. Advanced batch creation keeps the existing retrievable-key behavior.',
    'Creation could not be confirmed. Check the key list before creating another key; an existing key may need to be revoked.':
      'Creation could not be confirmed. Check the key list before creating another key; an existing key may need to be revoked.',
    'Shown only at creation. Use your saved key or revoke and replace it.':
      'Shown only at creation. Use your saved key or revoke and replace it.',
    'Keys shown only at creation are excluded from batch copy.':
      'Keys shown only at creation are excluded from batch copy.',
    'Key authentication succeeded. No model request was sent or billed.':
      'Key authentication succeeded. No model request was sent or billed.',
    'Key check failed. Check access, expiry and allowed IP addresses.':
      'Key check failed. Check access, expiry and allowed IP addresses.',
    'Could not revoke the key. Try again from API Keys.':
      'Could not revoke the key. Try again from API Keys.',
    'Latest API request': 'Latest API request',
    'Unable to load the latest request.': 'Unable to load the latest request.',
    'Your first API request will appear here. No request record is available yet.':
      'Your first API request will appear here. No request record is available yet.',
    'Usage recorded; check details for the request outcome.':
      'Usage recorded; check details for the request outcome.',
  },
}

const retiredPricingKeys = new Set([
  'JSON map of group → cost multiplier used as the base for that billing group. Dynamic pricing adds the profit multiplier.',
  'Final charge = model base cost × group cost multiplier × dynamic profit multiplier.',
  'The group value is a cost basis, not a personal discount. Dynamic pricing supplies the profit multiplier separately.',
  'Set the base cost multiplier for each routing group. Dynamic pricing adds the live profit multiplier on top; top-up ratio remains an independent balance multiplier.',
  'Active channel',
  'Automatically refreshes every 3 seconds.',
  'Caps the load-driven profit premium. Cost protection can still raise the effective multiplier when needed.',
  'Configure costs for every active channel before enabling: {{channels}}',
  'Configured-cost coverage ready',
  'Conservative channel costs',
  'Cost EMA',
  'Cost floor',
  'Cost for {{channel}}',
  'Cost protection margin',
  'Cost × profit pricing preview',
  'Current dynamic profit multiplier',
  'Dynamic Profit Pricing',
  'Dynamic pricing channel costs',
  'Dynamic pricing model overrides',
  'Dynamic profit ceiling',
  'Effective billing multiplier',
  'Enable dynamic profit pricing',
  'Engine tick: {{seconds}}s',
  'Enter a conservative upper-bound USD cost per 1M total tokens. Upstream responses provide usage tokens, but generally not the final dollar cost; unknown-cost channels are blocked while this feature is enabled.',
  'Enter a positive conservative cost for channel {{channel}}.',
  'Fill every active channel cost first, then enable.',
  'Final billing = group cost × dynamic profit',
  'Formula',
  'Group Pricing stores the base cost multiplier. This page computes the live profit multiplier on top of that cost.',
  'Group Pricing supplies the cost multiplier. Dynamic pricing supplies the profit multiplier. Final billing multiplies both.',
  'Known upstream cost is multiplied by this margin before the cost floor is compared with profit pricing.',
  'Live profit multiplier preview',
  'Live safety status is currently unavailable.',
  'Load EMA',
  'Minimum multiplier cannot exceed the dynamic ceiling.',
  'Minimum profit multiplier',
  'No active channels found',
  'No model samples yet. The configured minimum is used until the first engine tick.',
  'No pricing groups configured',
  'Not ready to enable safely',
  'Per-model live factors',
  'Profit multiplier',
  'Reference model cost (USD / 1M tokens)',
  'The final charge is the group cost multiplier multiplied by this dynamic profit multiplier.',
  'The profit multiplier never falls below this value while dynamic pricing is enabled.',
  'USD / 1M tokens',
  'Unknown cost',
  'Use the model cost baseline used to compare upstream cost with the configured group cost multiplier.',
  '{{configured}} of {{active}} active channels have costs.',
  'Base prices exclude dynamic profit multipliers and usage discounts. USD estimates also exclude payment discounts and fees; checkout confirms the payable amount.',
])
const fixedGroupCopy = {
  en: {
    'JSON map of group → fixed multiplier applied to model prices for that billing group.':
      'JSON map of group → fixed multiplier applied to model prices for that billing group.',
    'Before usage discounts, the group price equals the model base price multiplied by the group ratio.':
      'Before usage discounts, the group price equals the model base price multiplied by the group ratio.',
    'The group ratio changes model prices for that group. Top-up ratios change credited balance separately.':
      'The group ratio changes model prices for that group. Top-up ratios change credited balance separately.',
    'Set a fixed price multiplier for each routing group. The top-up ratio remains an independent balance multiplier.':
      'Set a fixed price multiplier for each routing group. The top-up ratio remains an independent balance multiplier.',
    'Legacy pricing adjustment': 'Legacy pricing adjustment',
  },
  zh: {
    'JSON map of group → fixed multiplier applied to model prices for that billing group.':
      'JSON 映射：分组 → 该计费分组模型价格采用的固定倍率。',
    'Before usage discounts, the group price equals the model base price multiplied by the group ratio.':
      '应用用量优惠前，分组价格等于模型基础价格乘以分组倍率。',
    'The group ratio changes model prices for that group. Top-up ratios change credited balance separately.':
      '分组倍率调整该分组的模型价格；充值倍率单独调整到账余额。',
    'Set a fixed price multiplier for each routing group. The top-up ratio remains an independent balance multiplier.':
      '为每个路由分组设置固定价格倍率。充值倍率仍独立决定到账余额。',
    'Legacy pricing adjustment': '旧账单价格调整',
  },
  'zh-TW': {
    'JSON map of group → fixed multiplier applied to model prices for that billing group.':
      'JSON 對應：群組 → 該計費群組模型價格採用的固定倍率。',
    'Before usage discounts, the group price equals the model base price multiplied by the group ratio.':
      '套用用量優惠前，群組價格等於模型基礎價格乘以群組倍率。',
    'The group ratio changes model prices for that group. Top-up ratios change credited balance separately.':
      '群組倍率調整該群組的模型價格；儲值倍率另行調整入帳餘額。',
    'Set a fixed price multiplier for each routing group. The top-up ratio remains an independent balance multiplier.':
      '為每個路由群組設定固定價格倍率。儲值倍率仍獨立決定入帳餘額。',
    'Legacy pricing adjustment': '舊帳單價格調整',
  },
  fr: {
    'JSON map of group → fixed multiplier applied to model prices for that billing group.':
      'Table JSON groupe → multiplicateur fixe appliqué aux prix des modèles de ce groupe.',
    'Before usage discounts, the group price equals the model base price multiplied by the group ratio.':
      'Avant les remises d’utilisation, le prix du groupe est le prix de base du modèle multiplié par le ratio du groupe.',
    'The group ratio changes model prices for that group. Top-up ratios change credited balance separately.':
      'Le ratio du groupe ajuste les prix des modèles. Le ratio de recharge ajuste séparément le solde crédité.',
    'Set a fixed price multiplier for each routing group. The top-up ratio remains an independent balance multiplier.':
      'Définissez un multiplicateur fixe par groupe de routage. Le ratio de recharge reste un multiplicateur indépendant du solde.',
    'Legacy pricing adjustment': 'Ajustement tarifaire historique',
  },
  ja: {
    'JSON map of group → fixed multiplier applied to model prices for that billing group.':
      'JSON 対応表：グループ → その課金グループのモデル価格に適用する固定倍率。',
    'Before usage discounts, the group price equals the model base price multiplied by the group ratio.':
      '利用割引の適用前は、グループ価格はモデル基本価格にグループ倍率を掛けた値です。',
    'The group ratio changes model prices for that group. Top-up ratios change credited balance separately.':
      'グループ倍率はモデル価格を調整します。チャージ倍率は入金残高を別途調整します。',
    'Set a fixed price multiplier for each routing group. The top-up ratio remains an independent balance multiplier.':
      '各ルーティンググループに固定価格倍率を設定します。チャージ倍率は残高に独立して適用されます。',
    'Legacy pricing adjustment': '過去の請求の価格調整',
  },
  ru: {
    'JSON map of group → fixed multiplier applied to model prices for that billing group.':
      'JSON: группа → фиксированный множитель цен моделей в этой расчётной группе.',
    'Before usage discounts, the group price equals the model base price multiplied by the group ratio.':
      'До скидок за использование цена группы равна базовой цене модели, умноженной на коэффициент группы.',
    'The group ratio changes model prices for that group. Top-up ratios change credited balance separately.':
      'Коэффициент группы меняет цены моделей. Коэффициент пополнения отдельно меняет зачисляемый баланс.',
    'Set a fixed price multiplier for each routing group. The top-up ratio remains an independent balance multiplier.':
      'Задайте фиксированный множитель цены для каждой группы маршрутизации. Коэффициент пополнения применяется к балансу отдельно.',
    'Legacy pricing adjustment': 'Корректировка старого тарифа',
  },
  vi: {
    'JSON map of group → fixed multiplier applied to model prices for that billing group.':
      'Ánh xạ JSON: nhóm → hệ số cố định áp dụng cho giá mô hình của nhóm thanh toán đó.',
    'Before usage discounts, the group price equals the model base price multiplied by the group ratio.':
      'Trước ưu đãi sử dụng, giá của nhóm bằng giá cơ sở của mô hình nhân với hệ số nhóm.',
    'The group ratio changes model prices for that group. Top-up ratios change credited balance separately.':
      'Hệ số nhóm điều chỉnh giá mô hình trong nhóm. Hệ số nạp tiền điều chỉnh số dư được cộng riêng biệt.',
    'Set a fixed price multiplier for each routing group. The top-up ratio remains an independent balance multiplier.':
      'Đặt hệ số giá cố định cho từng nhóm định tuyến. Hệ số nạp tiền vẫn là hệ số số dư độc lập.',
    'Legacy pricing adjustment': 'Điều chỉnh giá của hóa đơn cũ',
  },
}

const operationsFinishCopy = {
  en: {
    'Account export failed. Check export permission or narrow the registration period.':
      'Account export failed. Check export permission or narrow the registration period.',
    'Corrected source': 'Corrected source',
    'Correction reason': 'Correction reason',
    'Correction was not saved. Reloaded the latest version; check the source, reason and consent before retrying.':
      'Correction was not saved. Reloaded the latest version; check the source, reason and consent before retrying.',
    'Explain the evidence without URLs, credentials or personal contact details.':
      'Explain the evidence without URLs, credentials or personal contact details.',
    'Export filtered account details': 'Export filtered account details',
    'Exports account IDs and conversion status for the current source and registration period, without email addresses. Maximum 10,000 accounts.':
      'Exports account IDs and conversion status for the current source and registration period, without email addresses. Maximum 10,000 accounts.',
    'Manual corrections are shown separately. Observed attribution, channel totals, invitations and payments remain unchanged.':
      'Manual corrections are shown separately. Observed attribution, channel totals, invitations and payments remain unchanged.',
    'Manual source correction': 'Manual source correction',
    'No manual source correction recorded.':
      'No manual source correction recorded.',
    'Save correction with audit record': 'Save correction with audit record',
    'Showing the latest 100 retained corrections.':
      'Showing the latest 100 retained corrections.',
    'API access approved': 'API access approved',
    'API connection': 'API connection',
    'Access application submitted': 'Access application submitted',
    'Apply date range': 'Apply date range',
    'Apply filters': 'Apply filters',
    'Attributed from earlier source records':
      'Attributed from earlier source records',
    'Attribution evidence': 'Attribution evidence',
    'Average hours since registration': 'Average hours since registration',
    Both: 'Both',
    'Choose a valid date range ending no later than today.':
      'Choose a valid date range ending no later than today.',
    'Client configuration confirmed': 'Client configuration confirmed',
    'Cohort conversion': 'Cohort conversion',
    'Counts cover consenting browser identifiers, not people. One browser can appear in several channels, so channel counts must not be added together.':
      'Counts cover consenting browser identifiers, not people. One browser can appear in several channels, so channel counts must not be added together.',
    'Dates use UTC; the end date is included. Up to 366 days.':
      'Dates use UTC; the end date is included. Up to 366 days.',
    'Each account is observed for the same number of days after registration. OAuth and manual keys are alternative paths; payment is independent of first use.':
      'Each account is observed for the same number of days after registration. OAuth and manual keys are alternative paths; payment is independent of first use.',
    'End date': 'End date',
    'First successful payment': 'First successful payment',
    'Identifiable browser visitors': 'Identifiable browser visitors',
    'Manual API key created': 'Manual API key created',
    'Mature cohort': 'Mature cohort',
    'Mature cohort conversion': 'Mature cohort conversion',
    'No completion observed': 'No completion observed',
    'No payment attribution recorded yet.':
      'No payment attribution recorded yet.',
    'No visitor observations in the retained part of this period.':
      'No visitor observations in the retained part of this period.',
    'OAuth authorized': 'OAuth authorized',
    'OAuth authorized or key created': 'OAuth authorized or key created',
    'Observation days': 'Observation days',
    'Observed accounts': 'Observed accounts',
    'Observed browser identifiers': 'Observed browser identifiers',
    Observing: 'Observing',
    'Payment attribution uses saved evidence and its recorded lookback window. It does not change registration attribution or prove causation.':
      'Payment attribution uses saved evidence and its recorded lookback window. It does not change registration attribution or prove causation.',
    Payments: 'Payments',
    'Rates use only accounts with a complete observation window and known evidence. Timings are from registration, with the timing sample count in parentheses.':
      'Rates use only accounts with a complete observation window and known evidence. Timings are from registration, with the timing sample count in parentheses.',
    'Records available from': 'Records available from',
    'Repeat payment': 'Repeat payment',
    'Selected registration period': 'Selected registration period',
    'Some stages lack historical evidence. Unknown accounts are not failed conversions.':
      'Some stages lack historical evidence. Unknown accounts are not failed conversions.',
    'Source before first payment': 'Source before first payment',
    Stage: 'Stage',
    'Start date': 'Start date',
    'These filters apply only to this conversion section. Content filters exclude older registrations without a recorded content label.':
      'These filters apply only to this conversion section. Content filters exclude older registrations without a recorded content label.',
    'Today (UTC)': 'Today (UTC)',
    'Visitor records do not cover this entire period. Counts below include only retained observations.':
      'Visitor records do not cover this entire period. Counts below include only retained observations.',
    'Payment attribution is still processing or unavailable; missing snapshots are not zero conversions.':
      'Payment attribution is still processing or unavailable; missing snapshots are not zero conversions.',
    'Administrator status notice': 'Administrator status notice',
    'Current routing configuration': 'Current routing configuration',
    'Routing availability is a configuration snapshot, not an upstream health probe. Administrator notices do not change request routing.':
      'Routing availability is a configuration snapshot, not an upstream health probe. Administrator notices do not change request routing.',
    'Status notice expires': 'Status notice expires',
    'Temporarily unavailable': 'Temporarily unavailable',
    Congested: 'Congested',
    'This publishes a status notice only. It does not disable channels or change routing. Notices must expire within 30 days.':
      'This publishes a status notice only. It does not disable channels or change routing. Notices must expire within 30 days.',
    'Under maintenance': 'Under maintenance',
    'Public operational status': 'Public operational status',
    'Operational status': 'Operational status',
    'Use routing configuration': 'Use routing configuration',
    'Public status explanation': 'Public status explanation',
    'Routable now': 'Routable now',
    'No account access': 'No account access',
    'Not in the model catalog': 'Not in the model catalog',
    'Runtime status unknown': 'Runtime status unknown',
  },
  zh: {
    'Account export failed. Check export permission or narrow the registration period.':
      '账号明细导出失败，请检查导出权限或缩小注册时间范围。',
    'Corrected source': '修正后的来源',
    'Correction reason': '修正理由',
    'Correction was not saved. Reloaded the latest version; check the source, reason and consent before retrying.':
      '修正未保存。已重新加载最新版本，请检查来源、理由和用户同意状态后重试。',
    'Explain the evidence without URLs, credentials or personal contact details.':
      '请说明判断依据，不要填写网址、凭据或个人联系方式。',
    'Export filtered account details': '导出当前筛选的账号明细',
    'Exports account IDs and conversion status for the current source and registration period, without email addresses. Maximum 10,000 accounts.':
      '导出当前来源和注册时间范围内的账号 ID 及转化状态，不含邮箱地址，最多 10,000 个账号。',
    'Manual corrections are shown separately. Observed attribution, channel totals, invitations and payments remain unchanged.':
      '人工修正单独展示，不改动自动来源归属、渠道汇总、邀请关系和付款记录。',
    'Manual source correction': '人工修正来源',
    'No manual source correction recorded.': '尚无人工来源修正记录。',
    'Save correction with audit record': '保存修正并记录审计',
    'Showing the latest 100 retained corrections.':
      '仅显示最近 100 条保留期内的修正记录。',
    'API access approved': 'API 访问已批准',
    'API connection': 'API 接入',
    'Access application submitted': '已提交访问申请',
    'Apply date range': '应用日期范围',
    'Apply filters': '应用筛选',
    'Attributed from earlier source records': '由此前来源记录归属',
    'Attribution evidence': '归属依据',
    'Average hours since registration': '注册后平均小时数',
    Both: '两种方式',
    'Choose a valid date range ending no later than today.':
      '请选择有效日期范围，结束日期不能晚于今天。',
    'Client configuration confirmed': '已确认客户端配置',
    'Cohort conversion': '同批用户转化',
    'Counts cover consenting browser identifiers, not people. One browser can appear in several channels, so channel counts must not be added together.':
      '仅统计同意采集的浏览器标识，不是自然人数。同一浏览器可能来自多个渠道，不能将各渠道数量直接相加。',
    'Dates use UTC; the end date is included. Up to 366 days.':
      '日期按 UTC 计算，包含结束日期，最长 366 天。',
    'Each account is observed for the same number of days after registration. OAuth and manual keys are alternative paths; payment is independent of first use.':
      '每个账号从注册起观察相同天数。OAuth 和手动 Key 是不同接入路径，付费不要求先完成首次调用。',
    'End date': '结束日期',
    'First successful payment': '首次成功付款',
    'Identifiable browser visitors': '可识别浏览器访客',
    'Manual API key created': '已创建手动 API Key',
    'Mature cohort': '已满观察期的账号',
    'Mature cohort conversion': '已满观察期账号的转化',
    'No completion observed': '尚未观察到完成',
    'No payment attribution recorded yet.': '暂无付款来源归属记录。',
    'No visitor observations in the retained part of this period.':
      '此期间保留的数据中没有访客记录。',
    'OAuth authorized': '已完成 OAuth 授权',
    'OAuth authorized or key created': '已授权 OAuth 或创建 Key',
    'Observation days': '观察天数',
    'Observed accounts': '已观察到的账号',
    'Observed browser identifiers': '已观察到的浏览器标识',
    Observing: '观察中',
    'Payment attribution uses saved evidence and its recorded lookback window. It does not change registration attribution or prove causation.':
      '付款归属使用已保存的证据及当时的回看范围，不改变注册归属，也不证明渠道导致付款。',
    Payments: '付款',
    'Rates use only accounts with a complete observation window and known evidence. Timings are from registration, with the timing sample count in parentheses.':
      '转化率仅使用观察期已完整结束且有明确证据的账号。耗时从注册起算，括号内为耗时样本数。',
    'Records available from': '可用记录起始时间',
    'Repeat payment': '再次付款',
    'Selected registration period': '选定的注册时间范围',
    'Some stages lack historical evidence. Unknown accounts are not failed conversions.':
      '部分环节缺少历史证据。状态未知的账号不能算作转化失败。',
    'Source before first payment': '首次付款前来源',
    Stage: '环节',
    'Start date': '开始日期',
    'These filters apply only to this conversion section. Content filters exclude older registrations without a recorded content label.':
      '这些筛选只影响本转化区域。内容筛选不包含尚未记录内容标记的历史注册账号。',
    'Today (UTC)': '今天（UTC）',
    'Visitor records do not cover this entire period. Counts below include only retained observations.':
      '访客记录未覆盖整个时间范围，以下数量仅统计仍保留的记录。',
    'Payment attribution is still processing or unavailable; missing snapshots are not zero conversions.':
      '付款归属仍在处理或暂不可用；缺少快照不代表没有转化。',
    'Administrator status notice': '管理员状态声明',
    'Current routing configuration': '当前路由配置',
    'Routing availability is a configuration snapshot, not an upstream health probe. Administrator notices do not change request routing.':
      '可用情况来自当前路由配置，并非上游健康探测。管理员状态声明不会改变请求路由。',
    'Status notice expires': '状态声明到期时间',
    'Temporarily unavailable': '暂时不可用',
    Congested: '拥堵',
    'This publishes a status notice only. It does not disable channels or change routing. Notices must expire within 30 days.':
      '这里只发布状态声明，不会禁用渠道或改变路由。声明必须在 30 天内到期。',
    'Under maintenance': '维护中',
    'Public operational status': '公开运行状态',
    'Operational status': '运行状态',
    'Use routing configuration': '根据路由配置判断',
    'Public status explanation': '公开状态说明',
    'Routable now': '可用（路由已启用）',
    'No account access': '当前账号无权限',
    'Not in the model catalog': '未列入模型目录',
    'Runtime status unknown': '运行状态未知',
  },
  'zh-TW': {
    'Account export failed. Check export permission or narrow the registration period.':
      '帳號明細匯出失敗，請檢查匯出權限或縮小註冊時間範圍。',
    'Corrected source': '修正後的來源',
    'Correction reason': '修正理由',
    'Correction was not saved. Reloaded the latest version; check the source, reason and consent before retrying.':
      '修正未儲存。已重新載入最新版本，請檢查來源、理由和使用者同意狀態後重試。',
    'Explain the evidence without URLs, credentials or personal contact details.':
      '請說明判斷依據，不要填寫網址、憑據或個人聯絡方式。',
    'Export filtered account details': '匯出目前篩選的帳號明細',
    'Exports account IDs and conversion status for the current source and registration period, without email addresses. Maximum 10,000 accounts.':
      '匯出目前來源和註冊時間範圍內的帳號 ID 及轉換狀態，不含電子郵件地址，最多 10,000 個帳號。',
    'Manual corrections are shown separately. Observed attribution, channel totals, invitations and payments remain unchanged.':
      '人工修正會分開顯示，不變更自動來源歸屬、管道彙總、邀請關係和付款紀錄。',
    'Manual source correction': '人工修正來源',
    'No manual source correction recorded.': '尚無人工來源修正紀錄。',
    'Save correction with audit record': '儲存修正並記錄稽核',
    'Showing the latest 100 retained corrections.':
      '僅顯示最近 100 筆保留期間內的修正紀錄。',
    'API access approved': 'API 存取已核准',
    'API connection': 'API 接入',
    'Access application submitted': '已提交存取申請',
    'Apply date range': '套用日期範圍',
    'Apply filters': '套用篩選',
    'Attributed from earlier source records': '根據較早的來源紀錄歸屬',
    'Attribution evidence': '歸屬依據',
    'Average hours since registration': '註冊後平均經過時數',
    Both: '兩者皆有',
    'Choose a valid date range ending no later than today.':
      '請選擇有效的日期範圍，結束日期不得晚於今天。',
    'Client configuration confirmed': '已確認用戶端設定',
    'Cohort conversion': '同期註冊帳號轉換',
    'Counts cover consenting browser identifiers, not people. One browser can appear in several channels, so channel counts must not be added together.':
      '此處統計已取得同意的瀏覽器識別碼，並非人數。同一瀏覽器可能出現在多個管道，因此各管道數量不可相加。',
    'Dates use UTC; the end date is included. Up to 366 days.':
      '日期以 UTC 為準，包含結束當日，最多 366 天。',
    'Each account is observed for the same number of days after registration. OAuth and manual keys are alternative paths; payment is independent of first use.':
      '每個帳號均從註冊日起觀察相同天數。OAuth 與手動 API Key 是不同的接入途徑；付款與首次使用沒有固定先後順序。',
    'End date': '結束日期',
    'First successful payment': '首次付款成功',
    'Identifiable browser visitors': '可識別的瀏覽器訪客',
    'Manual API key created': '已手動建立 API Key',
    'Mature cohort': '已滿觀察期的註冊群組',
    'Mature cohort conversion': '已滿觀察期群組轉換率',
    'No completion observed': '尚未觀察到完成',
    'No payment attribution recorded yet.': '尚無付款來源歸屬紀錄。',
    'No visitor observations in the retained part of this period.':
      '此期間仍在保留範圍內的部分沒有訪客觀察紀錄。',
    'OAuth authorized': '已完成 OAuth 授權',
    'OAuth authorized or key created': '已完成 OAuth 授權或建立 Key',
    'Observation days': '觀察天數',
    'Observed accounts': '已觀察到的帳號',
    'Observed browser identifiers': '已觀察到的瀏覽器識別碼',
    Observing: '觀察中',
    'Payment attribution uses saved evidence and its recorded lookback window. It does not change registration attribution or prove causation.':
      '付款來源歸屬採用已儲存的依據及當時記錄的回溯期間，不改變註冊來源歸屬，也不代表已證實因果關係。',
    Payments: '付款',
    'Rates use only accounts with a complete observation window and known evidence. Timings are from registration, with the timing sample count in parentheses.':
      '轉換率僅使用已滿觀察期且具備明確依據的帳號計算。耗時從註冊時計起，括號內為耗時統計的樣本數。',
    'Records available from': '可用紀錄起始時間',
    'Repeat payment': '再次付款',
    'Selected registration period': '已選註冊期間',
    'Some stages lack historical evidence. Unknown accounts are not failed conversions.':
      '部分階段缺少歷史依據，不能將狀態未知的帳號算作轉換失敗。',
    'Source before first payment': '首次付款前來源',
    Stage: '階段',
    'Start date': '開始日期',
    'These filters apply only to this conversion section. Content filters exclude older registrations without a recorded content label.':
      '這些篩選僅套用於此轉換區域。依內容篩選時，將排除未記錄內容標記的舊註冊帳號。',
    'Today (UTC)': '今天（UTC）',
    'Visitor records do not cover this entire period. Counts below include only retained observations.':
      '訪客紀錄未涵蓋整個期間，下方數量僅包含仍保留的觀察紀錄。',
    'Payment attribution is still processing or unavailable; missing snapshots are not zero conversions.':
      '付款來源歸屬仍在處理中或暫不可用；缺少快照不代表轉換數為零。',
    'Administrator status notice': '管理員狀態聲明',
    'Current routing configuration': '目前路由設定',
    'Routing availability is a configuration snapshot, not an upstream health probe. Administrator notices do not change request routing.':
      '可用情況來自目前路由設定，並非上游健康探測。管理員狀態聲明不會改變請求路由。',
    'Status notice expires': '狀態聲明到期時間',
    'Temporarily unavailable': '暫時無法使用',
    Congested: '壅塞',
    'This publishes a status notice only. It does not disable channels or change routing. Notices must expire within 30 days.':
      '這裡僅發布狀態聲明，不會停用管道或改變路由。聲明必須在 30 天內到期。',
    'Under maintenance': '維護中',
    'Public operational status': '公開運行狀態',
    'Operational status': '運行狀態',
    'Use routing configuration': '根據路由設定判斷',
    'Public status explanation': '公開狀態說明',
    'Routable now': '可用（路由已啟用）',
    'No account access': '目前帳號無權限',
    'Not in the model catalog': '未列入模型目錄',
    'Runtime status unknown': '運行狀態未知',
  },
  fr: {
    'Account export failed. Check export permission or narrow the registration period.':
      'Échec de l’export des comptes. Vérifiez les droits d’export ou réduisez la période d’inscription.',
    'Corrected source': 'Source corrigée',
    'Correction reason': 'Motif de correction',
    'Correction was not saved. Reloaded the latest version; check the source, reason and consent before retrying.':
      'Correction non enregistrée. La dernière version a été rechargée ; vérifiez la source, le motif et le consentement avant de réessayer.',
    'Explain the evidence without URLs, credentials or personal contact details.':
      'Expliquez les éléments justificatifs sans URL, identifiants secrets ni coordonnées personnelles.',
    'Export filtered account details': 'Exporter les comptes filtrés',
    'Exports account IDs and conversion status for the current source and registration period, without email addresses. Maximum 10,000 accounts.':
      'Exporte les identifiants des comptes et leur état de conversion pour la source et la période d’inscription actuelles, sans adresses e-mail. Maximum : 10 000 comptes.',
    'Manual corrections are shown separately. Observed attribution, channel totals, invitations and payments remain unchanged.':
      'Les corrections manuelles sont affichées séparément. L’attribution observée, les totaux par canal, les invitations et les paiements restent inchangés.',
    'Manual source correction': 'Correction manuelle de la source',
    'No manual source correction recorded.':
      'Aucune correction manuelle de source enregistrée.',
    'Save correction with audit record':
      'Enregistrer la correction et sa trace d’audit',
    'Showing the latest 100 retained corrections.':
      'Affichage des 100 dernières corrections encore conservées.',
    'API access approved': 'Accès API approuvé',
    'API connection': 'Connexion à l’API',
    'Access application submitted': 'Demande d’accès envoyée',
    'Apply date range': 'Appliquer la période',
    'Apply filters': 'Appliquer les filtres',
    'Attributed from earlier source records':
      'Attribué à partir d’observations antérieures',
    'Attribution evidence': 'Preuves d’attribution',
    'Average hours since registration': 'Heures moyennes depuis l’inscription',
    Both: 'Les deux',
    'Choose a valid date range ending no later than today.':
      'Choisissez une période valide se terminant au plus tard aujourd’hui.',
    'Client configuration confirmed': 'Configuration du client confirmée',
    'Cohort conversion': 'Conversion par cohorte',
    'Counts cover consenting browser identifiers, not people. One browser can appear in several channels, so channel counts must not be added together.':
      'Ces nombres représentent des identifiants de navigateurs ayant consenti, pas des personnes. Un navigateur peut figurer dans plusieurs canaux ; ne les additionnez pas.',
    'Dates use UTC; the end date is included. Up to 366 days.':
      'Dates en UTC, date de fin incluse. Maximum 366 jours.',
    'Each account is observed for the same number of days after registration. OAuth and manual keys are alternative paths; payment is independent of first use.':
      'Chaque compte est observé pendant la même durée après son inscription. OAuth et les clés manuelles sont deux parcours distincts ; le paiement est indépendant du premier appel.',
    'End date': 'Date de fin',
    'First successful payment': 'Premier paiement réussi',
    'Identifiable browser visitors': 'Visiteurs identifiables par navigateur',
    'Manual API key created': 'Clé API manuelle créée',
    'Mature cohort': 'Comptes ayant terminé la période d’observation',
    'Mature cohort conversion': 'Conversion des comptes arrivés à échéance',
    'No completion observed': 'Aucune réalisation observée',
    'No payment attribution recorded yet.':
      'Aucune attribution de paiement enregistrée.',
    'No visitor observations in the retained part of this period.':
      'Aucun visiteur observé dans les données conservées pour cette période.',
    'OAuth authorized': 'Autorisation OAuth obtenue',
    'OAuth authorized or key created': 'OAuth autorisé ou clé créée',
    'Observation days': 'Jours d’observation',
    'Observed accounts': 'Comptes observés',
    'Observed browser identifiers': 'Identifiants de navigateurs observés',
    Observing: 'En observation',
    'Payment attribution uses saved evidence and its recorded lookback window. It does not change registration attribution or prove causation.':
      'L’attribution des paiements utilise les preuves sauvegardées et la fenêtre de recherche enregistrée. Elle ne change pas l’attribution des inscriptions et ne prouve aucun lien causal.',
    Payments: 'Paiements',
    'Rates use only accounts with a complete observation window and known evidence. Timings are from registration, with the timing sample count in parentheses.':
      'Les taux concernent uniquement les comptes ayant terminé leur période d’observation et disposant de preuves. Les durées partent de l’inscription ; le nombre d’échantillons figure entre parenthèses.',
    'Records available from': 'Début des données disponibles',
    'Repeat payment': 'Paiement renouvelé',
    'Selected registration period': 'Période d’inscription sélectionnée',
    'Some stages lack historical evidence. Unknown accounts are not failed conversions.':
      'Certaines étapes manquent de preuves historiques. Les comptes au statut inconnu ne sont pas des échecs de conversion.',
    'Source before first payment': 'Source avant le premier paiement',
    Stage: 'Étape',
    'Start date': 'Date de début',
    'These filters apply only to this conversion section. Content filters exclude older registrations without a recorded content label.':
      'Ces filtres ne concernent que cette section de conversion. Le filtre de contenu exclut les anciennes inscriptions sans marqueur de contenu enregistré.',
    'Today (UTC)': 'Aujourd’hui (UTC)',
    'Visitor records do not cover this entire period. Counts below include only retained observations.':
      'Les données de visiteurs ne couvrent pas toute la période. Les nombres ci-dessous concernent uniquement les observations conservées.',
    'Payment attribution is still processing or unavailable; missing snapshots are not zero conversions.':
      'L’attribution des paiements est en cours ou indisponible ; l’absence de données sauvegardées ne signifie pas zéro conversion.',
    'Administrator status notice': 'Avis de statut administrateur',
    'Current routing configuration': 'Configuration actuelle du routage',
    'Routing availability is a configuration snapshot, not an upstream health probe. Administrator notices do not change request routing.':
      'La disponibilité reflète la configuration du routage, pas un test de santé en amont. Les avis administrateur ne modifient pas le routage des requêtes.',
    'Status notice expires': 'Expiration de l’avis',
    'Temporarily unavailable': 'Temporairement indisponible',
    Congested: 'Saturé',
    'This publishes a status notice only. It does not disable channels or change routing. Notices must expire within 30 days.':
      'Publie uniquement un avis de statut, sans désactiver les canaux ni modifier le routage. L’avis doit expirer sous 30 jours.',
    'Under maintenance': 'En maintenance',
    'Public operational status': 'Statut opérationnel public',
    'Operational status': 'Statut opérationnel',
    'Use routing configuration': 'Selon la configuration du routage',
    'Public status explanation': 'Explication publique du statut',
    'Routable now': 'Routage disponible',
    'No account access': 'Accès non autorisé pour ce compte',
    'Not in the model catalog': 'Absent du catalogue de modèles',
    'Runtime status unknown': 'Statut opérationnel inconnu',
  },
  ja: {
    'Account export failed. Check export permission or narrow the registration period.':
      'アカウント明細をエクスポートできませんでした。エクスポート権限を確認するか、登録期間を絞り込んでください。',
    'Corrected source': '修正後の流入元',
    'Correction reason': '修正理由',
    'Correction was not saved. Reloaded the latest version; check the source, reason and consent before retrying.':
      '修正は保存されませんでした。最新の内容を再読み込みしました。流入元、理由、ユーザーの同意状態を確認して再試行してください。',
    'Explain the evidence without URLs, credentials or personal contact details.':
      'URL、認証情報、個人の連絡先を含めずに、判断の根拠を記載してください。',
    'Export filtered account details':
      '絞り込み済みアカウント明細をエクスポート',
    'Exports account IDs and conversion status for the current source and registration period, without email addresses. Maximum 10,000 accounts.':
      '現在の流入元と登録期間に該当するアカウント ID と転換状況をエクスポートします。メールアドレスは含みません。上限は 10,000 アカウントです。',
    'Manual corrections are shown separately. Observed attribution, channel totals, invitations and payments remain unchanged.':
      '手動修正は別に表示されます。観測に基づく帰属、チャネル別集計、招待関係、支払い記録は変更されません。',
    'Manual source correction': '流入元の手動修正',
    'No manual source correction recorded.':
      '流入元の手動修正は記録されていません。',
    'Save correction with audit record': '修正と監査記録を保存',
    'Showing the latest 100 retained corrections.':
      '保存期間内の最新 100 件の修正を表示しています。',
    'API access approved': 'API アクセス承認済み',
    'API connection': 'API 接続',
    'Access application submitted': 'アクセス申請済み',
    'Apply date range': '期間を適用',
    'Apply filters': '絞り込みを適用',
    'Attributed from earlier source records': '過去の流入元記録から帰属',
    'Attribution evidence': '帰属の根拠',
    'Average hours since registration': '登録からの平均時間',
    Both: '両方',
    'Choose a valid date range ending no later than today.':
      '終了日が今日以前の有効な期間を選択してください。',
    'Client configuration confirmed': 'クライアント設定確認済み',
    'Cohort conversion': '登録時期別の転換状況',
    'Counts cover consenting browser identifiers, not people. One browser can appear in several channels, so channel counts must not be added together.':
      '同意を得たブラウザー識別子を集計しており、人数ではありません。同じブラウザーが複数のチャネルに現れるため、チャネル別の件数は合算できません。',
    'Dates use UTC; the end date is included. Up to 366 days.':
      '日付は UTC 基準で、終了日を含みます。最大 366 日間です。',
    'Each account is observed for the same number of days after registration. OAuth and manual keys are alternative paths; payment is independent of first use.':
      '各アカウントを登録後の同じ日数で観測します。OAuth と手動作成のキーは代替経路であり、支払いと初回利用の順序は問いません。',
    'End date': '終了日',
    'First successful payment': '初回支払い成功',
    'Identifiable browser visitors': '識別可能なブラウザー訪問者',
    'Manual API key created': '手動 API キー作成済み',
    'Mature cohort': '観測期間を満了した登録群',
    'Mature cohort conversion': '観測期間満了群の転換率',
    'No completion observed': '完了は未観測',
    'No payment attribution recorded yet.':
      '支払いの流入元帰属はまだ記録されていません。',
    'No visitor observations in the retained part of this period.':
      'この期間のうち保存対象の範囲に訪問記録はありません。',
    'OAuth authorized': 'OAuth 認可済み',
    'OAuth authorized or key created': 'OAuth 認可またはキー作成済み',
    'Observation days': '観測日数',
    'Observed accounts': '観測済みアカウント',
    'Observed browser identifiers': '観測したブラウザー識別子',
    Observing: '観測中',
    'Payment attribution uses saved evidence and its recorded lookback window. It does not change registration attribution or prove causation.':
      '支払いの帰属には、保存済みの根拠と記録された遡及期間を使います。登録時の帰属は変更せず、因果関係を証明するものではありません。',
    Payments: '支払い',
    'Rates use only accounts with a complete observation window and known evidence. Timings are from registration, with the timing sample count in parentheses.':
      '転換率は、観測期間を満了し根拠が確認できるアカウントのみで計算します。所要時間は登録時点から計測し、括弧内に時間集計の対象件数を表示します。',
    'Records available from': '記録の利用可能開始日時',
    'Repeat payment': '再度の支払い',
    'Selected registration period': '選択中の登録期間',
    'Some stages lack historical evidence. Unknown accounts are not failed conversions.':
      '一部の段階には過去の根拠がありません。状況不明のアカウントを転換失敗とは扱いません。',
    'Source before first payment': '初回支払い前の流入元',
    Stage: '段階',
    'Start date': '開始日',
    'These filters apply only to this conversion section. Content filters exclude older registrations without a recorded content label.':
      'この絞り込みは転換状況のセクションにのみ適用されます。コンテンツで絞り込むと、コンテンツラベルが記録されていない過去の登録は除外されます。',
    'Today (UTC)': '今日（UTC）',
    'Visitor records do not cover this entire period. Counts below include only retained observations.':
      '訪問記録はこの期間全体を網羅していません。以下の件数には保存済みの観測記録のみを含みます。',
    'Payment attribution is still processing or unavailable; missing snapshots are not zero conversions.':
      '支払いの流入元帰属は処理中、または利用できません。スナップショットの欠落は転換件数がゼロであることを意味しません。',
    'Administrator status notice': '管理者による状態通知',
    'Current routing configuration': '現在のルーティング設定',
    'Routing availability is a configuration snapshot, not an upstream health probe. Administrator notices do not change request routing.':
      '利用状況は現在のルーティング設定に基づき、上流の稼働確認ではありません。管理者の状態通知はリクエストの経路を変更しません。',
    'Status notice expires': '状態通知の有効期限',
    'Temporarily unavailable': '一時的に利用不可',
    Congested: '混雑中',
    'This publishes a status notice only. It does not disable channels or change routing. Notices must expire within 30 days.':
      '状態通知のみを公開します。チャネルの無効化や経路の変更は行いません。通知は30日以内に期限が切れる必要があります。',
    'Under maintenance': 'メンテナンス中',
    'Public operational status': '公開する稼働状態',
    'Operational status': '稼働状態',
    'Use routing configuration': 'ルーティング設定に基づく',
    'Public status explanation': '公開する状態の説明',
    'Routable now': '利用可能な経路あり',
    'No account access': 'このアカウントには権限がありません',
    'Not in the model catalog': 'モデルカタログに未掲載',
    'Runtime status unknown': '稼働状態不明',
  },
  ru: {
    'Account export failed. Check export permission or narrow the registration period.':
      'Не удалось экспортировать данные аккаунтов. Проверьте разрешение на экспорт или сократите период регистрации.',
    'Corrected source': 'Исправленный источник',
    'Correction reason': 'Причина исправления',
    'Correction was not saved. Reloaded the latest version; check the source, reason and consent before retrying.':
      'Исправление не сохранено. Загружена последняя версия; проверьте источник, причину и согласие пользователя перед повторной попыткой.',
    'Explain the evidence without URLs, credentials or personal contact details.':
      'Опишите основания без URL, секретных учётных данных и личных контактов.',
    'Export filtered account details':
      'Экспортировать отфильтрованные аккаунты',
    'Exports account IDs and conversion status for the current source and registration period, without email addresses. Maximum 10,000 accounts.':
      'Экспорт идентификаторов аккаунтов и статусов конверсии по текущему источнику и периоду регистрации, без адресов электронной почты. Максимум 10 000 аккаунтов.',
    'Manual corrections are shown separately. Observed attribution, channel totals, invitations and payments remain unchanged.':
      'Ручные исправления показаны отдельно. Наблюдаемая атрибуция, итоги по каналам, приглашения и платежи не изменяются.',
    'Manual source correction': 'Ручное исправление источника',
    'No manual source correction recorded.':
      'Ручные исправления источника ещё не записаны.',
    'Save correction with audit record':
      'Сохранить исправление с записью аудита',
    'Showing the latest 100 retained corrections.':
      'Показаны последние 100 исправлений в пределах срока хранения.',
    'API access approved': 'Доступ к API одобрен',
    'API connection': 'Подключение к API',
    'Access application submitted': 'Заявка на доступ подана',
    'Apply date range': 'Применить период',
    'Apply filters': 'Применить фильтры',
    'Attributed from earlier source records':
      'Отнесено по предыдущим записям источника',
    'Attribution evidence': 'Основание атрибуции',
    'Average hours since registration': 'Среднее число часов с регистрации',
    Both: 'Оба способа',
    'Choose a valid date range ending no later than today.':
      'Выберите допустимый период с датой окончания не позднее сегодняшней.',
    'Client configuration confirmed': 'Настройка клиента подтверждена',
    'Cohort conversion': 'Конверсия когорты',
    'Counts cover consenting browser identifiers, not people. One browser can appear in several channels, so channel counts must not be added together.':
      'Учитываются идентификаторы браузеров с полученным согласием, а не люди. Один браузер может встречаться в нескольких каналах, поэтому показатели каналов нельзя суммировать.',
    'Dates use UTC; the end date is included. Up to 366 days.':
      'Даты указаны по UTC; конечная дата включается. Максимум 366 дней.',
    'Each account is observed for the same number of days after registration. OAuth and manual keys are alternative paths; payment is independent of first use.':
      'Каждый аккаунт наблюдается одинаковое число дней после регистрации. OAuth и ручное создание ключа — альтернативные пути; оплата не зависит от порядка первого использования.',
    'End date': 'Дата окончания',
    'First successful payment': 'Первый успешный платёж',
    'Identifiable browser visitors': 'Идентифицируемые посетители браузеров',
    'Manual API key created': 'API-ключ создан вручную',
    'Mature cohort': 'Когорта с завершённым периодом наблюдения',
    'Mature cohort conversion': 'Конверсия когорты с завершённым наблюдением',
    'No completion observed': 'Завершение не наблюдалось',
    'No payment attribution recorded yet.':
      'Атрибуция платежей ещё не записана.',
    'No visitor observations in the retained part of this period.':
      'В сохраняемой части этого периода нет наблюдений посетителей.',
    'OAuth authorized': 'Авторизация OAuth выполнена',
    'OAuth authorized or key created': 'OAuth авторизован или ключ создан',
    'Observation days': 'Дней наблюдения',
    'Observed accounts': 'Наблюдаемые аккаунты',
    'Observed browser identifiers': 'Наблюдаемые идентификаторы браузеров',
    Observing: 'Наблюдение продолжается',
    'Payment attribution uses saved evidence and its recorded lookback window. It does not change registration attribution or prove causation.':
      'Атрибуция платежей использует сохранённые основания и записанное окно ретроспективного поиска. Она не меняет атрибуцию регистрации и не доказывает причинную связь.',
    Payments: 'Платежи',
    'Rates use only accounts with a complete observation window and known evidence. Timings are from registration, with the timing sample count in parentheses.':
      'Доли рассчитываются только по аккаунтам с полным периодом наблюдения и известными основаниями. Время отсчитывается от регистрации; в скобках указано число аккаунтов в выборке для расчёта времени.',
    'Records available from': 'Записи доступны с',
    'Repeat payment': 'Повторный платёж',
    'Selected registration period': 'Выбранный период регистрации',
    'Some stages lack historical evidence. Unknown accounts are not failed conversions.':
      'Для некоторых этапов нет исторических свидетельств. Аккаунты с неизвестным статусом не считаются неуспешными конверсиями.',
    'Source before first payment': 'Источник перед первым платежом',
    Stage: 'Этап',
    'Start date': 'Дата начала',
    'These filters apply only to this conversion section. Content filters exclude older registrations without a recorded content label.':
      'Эти фильтры применяются только к разделу конверсии. Фильтрация по материалу исключает старые регистрации без записанной метки материала.',
    'Today (UTC)': 'Сегодня (UTC)',
    'Visitor records do not cover this entire period. Counts below include only retained observations.':
      'Записи посетителей не охватывают весь этот период. Показатели ниже учитывают только сохранённые наблюдения.',
    'Payment attribution is still processing or unavailable; missing snapshots are not zero conversions.':
      'Атрибуция платежей ещё обрабатывается или недоступна; отсутствие снимков не означает нулевую конверсию.',
    'Administrator status notice': 'Уведомление администратора',
    'Current routing configuration': 'Текущая конфигурация маршрутизации',
    'Routing availability is a configuration snapshot, not an upstream health probe. Administrator notices do not change request routing.':
      'Доступность отражает конфигурацию маршрутизации, а не проверку провайдера. Уведомления администратора не меняют маршруты запросов.',
    'Status notice expires': 'Срок действия уведомления',
    'Temporarily unavailable': 'Временно недоступно',
    Congested: 'Перегрузка',
    'This publishes a status notice only. It does not disable channels or change routing. Notices must expire within 30 days.':
      'Публикуется только уведомление: каналы не отключаются, маршрутизация не меняется. Срок действия — не более 30 дней.',
    'Under maintenance': 'На обслуживании',
    'Public operational status': 'Публичный рабочий статус',
    'Operational status': 'Рабочий статус',
    'Use routing configuration': 'По конфигурации маршрутизации',
    'Public status explanation': 'Публичное пояснение статуса',
    'Routable now': 'Маршрут доступен',
    'No account access': 'Нет доступа у аккаунта',
    'Not in the model catalog': 'Нет в каталоге моделей',
    'Runtime status unknown': 'Рабочий статус неизвестен',
  },
  vi: {
    'Account export failed. Check export permission or narrow the registration period.':
      'Không thể xuất chi tiết tài khoản. Hãy kiểm tra quyền xuất hoặc thu hẹp khoảng thời gian đăng ký.',
    'Corrected source': 'Nguồn đã sửa',
    'Correction reason': 'Lý do sửa',
    'Correction was not saved. Reloaded the latest version; check the source, reason and consent before retrying.':
      'Chưa lưu bản sửa. Đã tải lại phiên bản mới nhất; hãy kiểm tra nguồn, lý do và trạng thái đồng ý trước khi thử lại.',
    'Explain the evidence without URLs, credentials or personal contact details.':
      'Giải thích căn cứ xác minh, không nhập URL, thông tin xác thực hay thông tin liên hệ cá nhân.',
    'Export filtered account details': 'Xuất chi tiết tài khoản theo bộ lọc',
    'Exports account IDs and conversion status for the current source and registration period, without email addresses. Maximum 10,000 accounts.':
      'Xuất ID tài khoản và trạng thái chuyển đổi theo nguồn và khoảng thời gian đăng ký hiện tại, không gồm địa chỉ email. Tối đa 10.000 tài khoản.',
    'Manual corrections are shown separately. Observed attribution, channel totals, invitations and payments remain unchanged.':
      'Các bản sửa thủ công được hiển thị riêng. Nguồn quy thuộc đã quan sát, tổng số theo kênh, quan hệ mời và các khoản thanh toán không thay đổi.',
    'Manual source correction': 'Sửa nguồn thủ công',
    'No manual source correction recorded.': 'Chưa có bản sửa nguồn thủ công.',
    'Save correction with audit record': 'Lưu bản sửa kèm nhật ký kiểm tra',
    'Showing the latest 100 retained corrections.':
      'Hiển thị 100 bản sửa gần nhất còn trong thời hạn lưu giữ.',
    'API access approved': 'Đã phê duyệt quyền truy cập API',
    'API connection': 'Kết nối API',
    'Access application submitted': 'Đã gửi đơn xin quyền truy cập',
    'Apply date range': 'Áp dụng khoảng ngày',
    'Apply filters': 'Áp dụng bộ lọc',
    'Attributed from earlier source records':
      'Quy thuộc từ các bản ghi nguồn trước đó',
    'Attribution evidence': 'Bằng chứng quy thuộc',
    'Average hours since registration': 'Số giờ trung bình kể từ khi đăng ký',
    Both: 'Cả hai',
    'Choose a valid date range ending no later than today.':
      'Chọn khoảng ngày hợp lệ có ngày kết thúc không muộn hơn hôm nay.',
    'Client configuration confirmed': 'Đã xác nhận cấu hình ứng dụng',
    'Cohort conversion': 'Chuyển đổi theo nhóm đăng ký',
    'Counts cover consenting browser identifiers, not people. One browser can appear in several channels, so channel counts must not be added together.':
      'Số liệu tính theo mã nhận diện trình duyệt đã đồng ý, không phải số người. Một trình duyệt có thể xuất hiện ở nhiều kênh, vì vậy không được cộng số lượng giữa các kênh.',
    'Dates use UTC; the end date is included. Up to 366 days.':
      'Ngày tính theo UTC và bao gồm ngày kết thúc. Tối đa 366 ngày.',
    'Each account is observed for the same number of days after registration. OAuth and manual keys are alternative paths; payment is independent of first use.':
      'Mỗi tài khoản được theo dõi cùng số ngày sau khi đăng ký. OAuth và khóa tạo thủ công là hai cách kết nối thay thế nhau; thanh toán không phải theo thứ tự cố định với lần sử dụng đầu tiên.',
    'End date': 'Ngày kết thúc',
    'First successful payment': 'Thanh toán thành công lần đầu',
    'Identifiable browser visitors':
      'Khách truy cập trình duyệt có thể nhận diện',
    'Manual API key created': 'Đã tạo khóa API thủ công',
    'Mature cohort': 'Nhóm đã đủ thời gian theo dõi',
    'Mature cohort conversion':
      'Tỷ lệ chuyển đổi của nhóm đã đủ thời gian theo dõi',
    'No completion observed': 'Chưa quan sát thấy hoàn tất',
    'No payment attribution recorded yet.':
      'Chưa có bản ghi quy thuộc nguồn thanh toán.',
    'No visitor observations in the retained part of this period.':
      'Không có quan sát khách truy cập trong phần còn được lưu giữ của khoảng thời gian này.',
    'OAuth authorized': 'Đã cấp quyền OAuth',
    'OAuth authorized or key created': 'Đã cấp quyền OAuth hoặc tạo khóa',
    'Observation days': 'Số ngày theo dõi',
    'Observed accounts': 'Tài khoản đã quan sát',
    'Observed browser identifiers': 'Mã nhận diện trình duyệt đã quan sát',
    Observing: 'Đang theo dõi',
    'Payment attribution uses saved evidence and its recorded lookback window. It does not change registration attribution or prove causation.':
      'Việc quy thuộc nguồn thanh toán dùng bằng chứng đã lưu và khoảng truy hồi đã ghi nhận. Điều này không thay đổi nguồn quy thuộc khi đăng ký hay chứng minh quan hệ nhân quả.',
    Payments: 'Thanh toán',
    'Rates use only accounts with a complete observation window and known evidence. Timings are from registration, with the timing sample count in parentheses.':
      'Tỷ lệ chỉ tính trên các tài khoản đã đủ thời gian theo dõi và có bằng chứng rõ ràng. Thời gian được tính từ lúc đăng ký; số mẫu dùng để tính thời gian nằm trong ngoặc.',
    'Records available from': 'Bản ghi có sẵn từ',
    'Repeat payment': 'Thanh toán lại',
    'Selected registration period': 'Khoảng thời gian đăng ký đã chọn',
    'Some stages lack historical evidence. Unknown accounts are not failed conversions.':
      'Một số giai đoạn thiếu bằng chứng lịch sử. Tài khoản có trạng thái chưa rõ không được tính là chuyển đổi thất bại.',
    'Source before first payment': 'Nguồn trước lần thanh toán đầu tiên',
    Stage: 'Giai đoạn',
    'Start date': 'Ngày bắt đầu',
    'These filters apply only to this conversion section. Content filters exclude older registrations without a recorded content label.':
      'Các bộ lọc này chỉ áp dụng cho phần chuyển đổi. Lọc theo nội dung sẽ loại trừ các đăng ký cũ không có nhãn nội dung được ghi nhận.',
    'Today (UTC)': 'Hôm nay (UTC)',
    'Visitor records do not cover this entire period. Counts below include only retained observations.':
      'Bản ghi khách truy cập không bao phủ toàn bộ khoảng thời gian này. Số liệu bên dưới chỉ gồm các quan sát còn được lưu giữ.',
    'Payment attribution is still processing or unavailable; missing snapshots are not zero conversions.':
      'Việc quy thuộc nguồn thanh toán vẫn đang xử lý hoặc chưa khả dụng; thiếu bản chụp dữ liệu không có nghĩa là số lượt chuyển đổi bằng không.',
    'Administrator status notice': 'Thông báo trạng thái của quản trị viên',
    'Current routing configuration': 'Cấu hình định tuyến hiện tại',
    'Routing availability is a configuration snapshot, not an upstream health probe. Administrator notices do not change request routing.':
      'Khả dụng phản ánh cấu hình định tuyến, không phải kiểm tra sức khỏe nhà cung cấp. Thông báo của quản trị viên không thay đổi tuyến yêu cầu.',
    'Status notice expires': 'Thời hạn thông báo trạng thái',
    'Temporarily unavailable': 'Tạm thời không khả dụng',
    Congested: 'Đang quá tải',
    'This publishes a status notice only. It does not disable channels or change routing. Notices must expire within 30 days.':
      'Chỉ công bố thông báo trạng thái, không tắt kênh hoặc đổi tuyến. Thông báo phải hết hạn trong vòng 30 ngày.',
    'Under maintenance': 'Đang bảo trì',
    'Public operational status': 'Trạng thái vận hành công khai',
    'Operational status': 'Trạng thái vận hành',
    'Use routing configuration': 'Theo cấu hình định tuyến',
    'Public status explanation': 'Giải thích trạng thái công khai',
    'Routable now': 'Có tuyến khả dụng',
    'No account access': 'Tài khoản không có quyền truy cập',
    'Not in the model catalog': 'Không có trong danh mục mô hình',
    'Runtime status unknown': 'Chưa rõ trạng thái vận hành',
  },
}

async function main() {
  if (process.argv.includes('--merge-locale-conflicts')) {
    const { execFileSync } = await import('node:child_process')
    for (const locale of ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi']) {
      const file = `apps/web/src/i18n/locales/${locale}.json`
      const [base, ours, theirs] = [1, 2, 3].map((stage) =>
        JSON.parse(
          execFileSync('git', ['show', `:${stage}:${file}`], {
            encoding: 'utf8',
            maxBuffer: 16 * 1024 * 1024,
          })
        )
      )
      const merged = {}
      for (const key of new Set([
        ...Object.keys(ours.translation),
        ...Object.keys(theirs.translation),
      ])) {
        const a = base.translation[key],
          b = ours.translation[key],
          c = theirs.translation[key]
        const decisions = {
          'zh:Account': '账户',
          'zh-TW:Account': '帳戶',
          'ru:Public': 'Публичный',
        }
        const conflict = b !== c && b !== a && c !== a
        if (conflict && !Object.hasOwn(decisions, `${locale}:${key}`)) {
          throw new Error(
            `Resolve translation meaning before merging: ${locale}: ${key}`
          )
        }
        const value = conflict ? decisions[`${locale}:${key}`] : b !== a ? b : c
        if (value !== undefined) merged[key] = value
      }
      ours.translation = Object.fromEntries(
        Object.entries(merged).sort(([a], [b]) => a.localeCompare(b))
      )
      await fs.writeFile(
        path.join(LOCALES_DIR, `${locale}.json`),
        stableStringify(ours),
        'utf8'
      )
    }
    return
  }

  // Allow scoped additions without overwriting unrelated in-progress translations.
  const paymentOnly = process.argv.includes('--only-payment-pricing')
  const homeOnly = process.argv.includes('--only-home-editorial')
  const homeTokenOnly = process.argv.includes('--only-home-token')
  const waitOnly = process.argv.includes('--only-wait-companion')
  const assistantToolOnly = process.argv.includes('--only-assistant-tool')
  const experienceOnly = process.argv.includes('--only-experience')
  const acquisitionOnly = process.argv.includes('--only-acquisition')
  const toolMarketOnly = process.argv.includes('--only-tool-market')
  const activityOnly = process.argv.includes('--only-acquisition-activity')
  const estimateOnly = process.argv.includes('--only-request-estimate')
  const parallelOnly = process.argv.includes('--only-parallel-experience')
  const statusOnly = process.argv.includes('--only-model-status')
  const logRecoveryOnly = process.argv.includes('--only-log-recovery')
  const feedbackOnly = process.argv.includes('--only-source-feedback')
  const clientsOnly = process.argv.includes('--only-client-presets')
  const costOnly = process.argv.includes('--only-acquisition-cost')
  const competitionOnly = process.argv.includes('--only-signal-competition')
  const keyFollowthroughOnly = process.argv.includes('--only-key-followthrough')
  const fixedGroupOnly = process.argv.includes('--only-fixed-group')
  const operationsFinishOnly = process.argv.includes('--only-operations-finish')
  const passkeyOnly = process.argv.includes('--only-passkey')
  const forgeRefreshOnly = process.argv.includes('--only-forge-refresh')
  const scoped =
    forgeRefreshOnly ||
    passkeyOnly ||
    operationsFinishOnly ||
    fixedGroupOnly ||
    keyFollowthroughOnly ||
    competitionOnly ||
    costOnly ||
    clientsOnly ||
    feedbackOnly ||
    logRecoveryOnly ||
    statusOnly ||
    parallelOnly ||
    estimateOnly ||
    activityOnly ||
    toolMarketOnly ||
    acquisitionOnly ||
    experienceOnly ||
    paymentOnly ||
    homeOnly ||
    homeTokenOnly ||
    waitOnly ||
    assistantToolOnly
  const entries = homeTokenOnly
    ? homeTokenCopy
    : passkeyOnly
      ? passkeyCopy
      : operationsFinishOnly
        ? operationsFinishCopy
        : fixedGroupOnly
          ? fixedGroupCopy
          : keyFollowthroughOnly
            ? keyFollowthroughCopy
            : competitionOnly
              ? Object.fromEntries(
                  Object.entries(signalCompetitionKeys).map(
                    ([locale, values]) => [
                      locale,
                      Object.fromEntries(
                        Object.entries(values).filter(
                          ([key]) => !['Account', 'Public'].includes(key)
                        )
                      ),
                    ]
                  )
                )
              : costOnly
                ? acquisitionCostCopy
                : clientsOnly
                  ? clientPresetsCopy
                  : feedbackOnly
                    ? sourceFeedbackCopy
                    : logRecoveryOnly
                      ? logRecoveryCopy
                      : statusOnly
                        ? modelStatusCopy
                        : parallelOnly
                          ? parallelExperienceCopy
                          : estimateOnly
                            ? requestEstimateCopy
                            : activityOnly
                              ? acquisitionActivityCopy
                              : toolMarketOnly
                                ? toolMarketCopy
                                : acquisitionOnly
                                  ? acquisitionCopy
                                  : experienceOnly
                                    ? experienceCopy
                                    : paymentOnly
                                      ? paymentPricingCopy
                                      : homeOnly
                                        ? homeEditorialCopy
                                        : waitOnly
                                          ? waitCompanionCopy
                                          : assistantToolOnly
                                            ? assistantToolCopy
                                            : newKeys
  const selectedEntries = forgeRefreshOnly
    ? forgeRefreshCopy
    : passkeyOnly
      ? passkeyCopy
      : entries
  let totalAdded = 0
  for (const [locale, baseTranslations] of Object.entries(selectedEntries)) {
    const translations = scoped
      ? baseTranslations
      : {
          ...baseTranslations,
          ...apiKeySourceCopy[locale],
          ...paymentPricingCopy[locale],
          ...homeEditorialCopy[locale],
          ...homeTokenCopy[locale],
          ...drawingWalletCopy[locale],
          ...piOAuthCopy[locale],
          ...assistantSettingsCopy[locale],
          ...drawingMcpExtraCopy[locale],
          ...piGuideCopy[locale],
          ...dshGuideCopy[locale],
          ...remoteControlCopy[locale],
          ...waitCompanionCopy[locale],
          ...scriptsCopy[locale],
          ...forgeRefreshCopy[locale],
        }
    const filePath = path.join(LOCALES_DIR, `${locale}.json`)
    const json = JSON.parse(await fs.readFile(filePath, 'utf8'))
    let count = 0
    for (const key of [
      ...retiredPricingKeys,
      ...(scoped ? [] : deprecatedCurrencyKeys),
    ]) {
      if (Object.hasOwn(json.translation, key)) {
        delete json.translation[key]
        count++
      }
    }
    if (
      !scoped &&
      'Routing rules cannot exceed 16384 characters.' in json.translation
    ) {
      delete json.translation['Routing rules cannot exceed 16384 characters.']
      count++
    }
    for (const [key, value] of Object.entries(translations)) {
      if (retiredPricingKeys.has(key)) continue
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

const webMcpKeys = {
  en: {
    'View source on GitHub': 'View source on GitHub',
    'Star count unavailable': 'Star count unavailable',
    'GitHub stars': 'GitHub stars',
    'Let your browser agent work with LMM.':
      'Let your browser agent work with LMM.',
    'Discover site information, model prices, public scripts, and account status through structured browser tools.':
      'Discover site information, model prices, public scripts, and account status through structured browser tools.',
    'Available tools': 'Available tools',
    'Browser support': 'Browser support',
    'WebMCP is available in this browser.':
      'WebMCP is available in this browser.',
    'WebMCP is not available in this browser. The normal interface still works.':
      'WebMCP is not available in this browser. The normal interface still works.',
    'Check browser support': 'Check browser support',
    'Clear boundaries': 'Clear boundaries',
    'API keys, passwords, payments, and script execution are not exposed through these tools.':
      'API keys, passwords, payments, and script execution are not exposed through these tools.',
    'WebMCP documentation': 'WebMCP documentation',
    'Site information and public page links':
      'Site information and public page links',
    'Navigate to supported LMM pages': 'Navigate to supported LMM pages',
    'Public model prices and billing units':
      'Public model prices and billing units',
    'Current sign-in and access status': 'Current sign-in and access status',
    'Public script names and download links':
      'Public script names and download links',
    'Project repositories and GitHub stars':
      'Project repositories and GitHub stars',
  },
  zh: {
    'View source on GitHub': '查看 GitHub 源码',
    'Star count unavailable': '暂时无法获取 Star 数量',
    'GitHub stars': 'GitHub Star 数量',
    'Let your browser agent work with LMM.': '让浏览器智能体使用 LMM。',
    'Discover site information, model prices, public scripts, and account status through structured browser tools.':
      '通过结构化浏览器工具查询站点信息、模型价格、公开脚本和账户状态。',
    'Available tools': '可用工具',
    'Browser support': '浏览器支持',
    'WebMCP is available in this browser.': '此浏览器支持 WebMCP。',
    'WebMCP is not available in this browser. The normal interface still works.':
      '此浏览器暂不支持 WebMCP，常规界面仍可正常使用。',
    'Check browser support': '检测浏览器支持',
    'Clear boundaries': '工具权限范围',
    'API keys, passwords, payments, and script execution are not exposed through these tools.':
      '这些工具不会提供 API 密钥或密码，也不会执行付款或运行脚本。',
    'WebMCP documentation': 'WebMCP 文档',
    'Site information and public page links': '站点信息与公开页面链接',
    'Navigate to supported LMM pages': '打开支持的 LMM 页面',
    'Public model prices and billing units': '公开模型价格与计费单位',
    'Current sign-in and access status': '当前登录与访问权限状态',
    'Public script names and download links': '公开脚本名称与下载链接',
    'Project repositories and GitHub stars': '项目仓库与 GitHub Star 数量',
  },
  'zh-TW': {
    'View source on GitHub': '檢視 GitHub 原始碼',
    'Star count unavailable': '暫時無法取得 Star 數量',
    'GitHub stars': 'GitHub Star 數量',
    'Let your browser agent work with LMM.': '讓瀏覽器智慧代理使用 LMM。',
    'Discover site information, model prices, public scripts, and account status through structured browser tools.':
      '透過結構化瀏覽器工具查詢網站資訊、模型價格、公開腳本及帳戶狀態。',
    'Available tools': '可用工具',
    'Browser support': '瀏覽器支援',
    'WebMCP is available in this browser.': '此瀏覽器支援 WebMCP。',
    'WebMCP is not available in this browser. The normal interface still works.':
      '此瀏覽器尚未支援 WebMCP，一般介面仍可正常使用。',
    'Check browser support': '檢查瀏覽器支援',
    'Clear boundaries': '工具權限範圍',
    'API keys, passwords, payments, and script execution are not exposed through these tools.':
      '這些工具不會提供 API 金鑰或密碼，也不會執行付款或執行腳本。',
    'WebMCP documentation': 'WebMCP 文件',
    'Site information and public page links': '網站資訊與公開頁面連結',
    'Navigate to supported LMM pages': '開啟支援的 LMM 頁面',
    'Public model prices and billing units': '公開模型價格與計費單位',
    'Current sign-in and access status': '目前登入與存取權限狀態',
    'Public script names and download links': '公開腳本名稱與下載連結',
    'Project repositories and GitHub stars': '專案儲存庫與 GitHub Star 數量',
  },
  fr: {
    'View source on GitHub': 'Voir le code sur GitHub',
    'Star count unavailable': 'Nombre d’étoiles indisponible',
    'GitHub stars': 'Étoiles GitHub',
    'Let your browser agent work with LMM.':
      'Votre agent de navigateur peut utiliser LMM.',
    'Discover site information, model prices, public scripts, and account status through structured browser tools.':
      'Consultez les informations du site, les tarifs des modèles, les scripts publics et l’état du compte via des outils structurés du navigateur.',
    'Available tools': 'Outils disponibles',
    'Browser support': 'Compatibilité du navigateur',
    'WebMCP is available in this browser.':
      'WebMCP est disponible dans ce navigateur.',
    'WebMCP is not available in this browser. The normal interface still works.':
      'WebMCP n’est pas disponible dans ce navigateur. L’interface habituelle reste accessible.',
    'Check browser support': 'Vérifier la compatibilité',
    'Clear boundaries': 'Périmètre des outils',
    'API keys, passwords, payments, and script execution are not exposed through these tools.':
      'Ces outils ne divulguent ni clés API ni mots de passe, et ne permettent ni paiement ni exécution de scripts.',
    'WebMCP documentation': 'Documentation WebMCP',
    'Site information and public page links':
      'Informations du site et liens publics',
    'Navigate to supported LMM pages': 'Ouvrir les pages LMM prises en charge',
    'Public model prices and billing units':
      'Tarifs publics des modèles et unités de facturation',
    'Current sign-in and access status': 'État de connexion et droits d’accès',
    'Public script names and download links':
      'Noms des scripts publics et liens de téléchargement',
    'Project repositories and GitHub stars':
      'Dépôts du projet et étoiles GitHub',
  },
  ja: {
    'View source on GitHub': 'GitHub でソースを見る',
    'Star count unavailable': 'スター数を取得できません',
    'GitHub stars': 'GitHub スター数',
    'Let your browser agent work with LMM.':
      'ブラウザーのエージェントから LMM を使えます。',
    'Discover site information, model prices, public scripts, and account status through structured browser tools.':
      '構造化されたブラウザーツールで、サイト情報、モデル料金、公開スクリプト、アカウント状態を確認できます。',
    'Available tools': '利用可能なツール',
    'Browser support': 'ブラウザーの対応状況',
    'WebMCP is available in this browser.':
      'このブラウザーは WebMCP に対応しています。',
    'WebMCP is not available in this browser. The normal interface still works.':
      'このブラウザーは WebMCP に対応していません。通常の画面はそのまま使えます。',
    'Check browser support': '対応状況を確認',
    'Clear boundaries': 'ツールの権限範囲',
    'API keys, passwords, payments, and script execution are not exposed through these tools.':
      'これらのツールは API キーやパスワードを公開せず、支払いやスクリプトの実行も行いません。',
    'WebMCP documentation': 'WebMCP ドキュメント',
    'Site information and public page links':
      'サイト情報と公開ページへのリンク',
    'Navigate to supported LMM pages': '対応する LMM ページを開く',
    'Public model prices and billing units': '公開モデル料金と課金単位',
    'Current sign-in and access status': '現在のログイン状態とアクセス権限',
    'Public script names and download links':
      '公開スクリプト名とダウンロードリンク',
    'Project repositories and GitHub stars':
      'プロジェクトのリポジトリと GitHub スター数',
  },
  ru: {
    'View source on GitHub': 'Исходный код на GitHub',
    'Star count unavailable': 'Число звёзд недоступно',
    'GitHub stars': 'Звёзды GitHub',
    'Let your browser agent work with LMM.':
      'Работайте с LMM через агента браузера.',
    'Discover site information, model prices, public scripts, and account status through structured browser tools.':
      'Получайте сведения о сайте, цены моделей, публичные скрипты и состояние аккаунта через структурированные инструменты браузера.',
    'Available tools': 'Доступные инструменты',
    'Browser support': 'Поддержка браузером',
    'WebMCP is available in this browser.': 'WebMCP доступен в этом браузере.',
    'WebMCP is not available in this browser. The normal interface still works.':
      'WebMCP недоступен в этом браузере. Обычный интерфейс продолжает работать.',
    'Check browser support': 'Проверить поддержку',
    'Clear boundaries': 'Границы доступа',
    'API keys, passwords, payments, and script execution are not exposed through these tools.':
      'Эти инструменты не раскрывают API-ключи и пароли, не выполняют платежи и не запускают скрипты.',
    'WebMCP documentation': 'Документация WebMCP',
    'Site information and public page links':
      'Сведения о сайте и ссылки на публичные страницы',
    'Navigate to supported LMM pages': 'Переход на поддерживаемые страницы LMM',
    'Public model prices and billing units':
      'Публичные цены моделей и единицы тарификации',
    'Current sign-in and access status': 'Текущее состояние входа и доступа',
    'Public script names and download links':
      'Названия публичных скриптов и ссылки для скачивания',
    'Project repositories and GitHub stars':
      'Репозитории проекта и звёзды GitHub',
  },
  vi: {
    'View source on GitHub': 'Xem mã nguồn trên GitHub',
    'Star count unavailable': 'Không tải được số sao',
    'GitHub stars': 'Số sao GitHub',
    'Let your browser agent work with LMM.':
      'Cho phép trợ lý trình duyệt sử dụng LMM.',
    'Discover site information, model prices, public scripts, and account status through structured browser tools.':
      'Tra cứu thông tin trang web, giá mô hình, tập lệnh công khai và trạng thái tài khoản qua các công cụ trình duyệt có cấu trúc.',
    'Available tools': 'Công cụ khả dụng',
    'Browser support': 'Hỗ trợ trình duyệt',
    'WebMCP is available in this browser.': 'Trình duyệt này hỗ trợ WebMCP.',
    'WebMCP is not available in this browser. The normal interface still works.':
      'Trình duyệt này chưa hỗ trợ WebMCP. Giao diện thông thường vẫn hoạt động.',
    'Check browser support': 'Kiểm tra hỗ trợ trình duyệt',
    'Clear boundaries': 'Phạm vi quyền hạn',
    'API keys, passwords, payments, and script execution are not exposed through these tools.':
      'Các công cụ này không cung cấp khóa API hay mật khẩu, không thanh toán và không chạy tập lệnh.',
    'WebMCP documentation': 'Tài liệu WebMCP',
    'Site information and public page links':
      'Thông tin trang web và liên kết trang công khai',
    'Navigate to supported LMM pages': 'Mở các trang LMM được hỗ trợ',
    'Public model prices and billing units':
      'Giá mô hình công khai và đơn vị tính phí',
    'Current sign-in and access status':
      'Trạng thái đăng nhập và quyền truy cập hiện tại',
    'Public script names and download links':
      'Tên tập lệnh công khai và liên kết tải xuống',
    'Project repositories and GitHub stars':
      'Kho mã nguồn dự án và số sao GitHub',
  },
}
for (const [locale, values] of Object.entries(webMcpKeys)) {
  Object.assign(newKeys[locale], values)
}

const signalGameKeys = {
  en: {
    'Signal path': 'Signal path',
    'Rotate the tiles to connect input to output.':
      'Rotate the tiles to connect input to output.',
    Moves: 'Moves',
    'Circuits solved': 'Circuits solved',
    North: 'North',
    East: 'East',
    South: 'South',
    West: 'West',
    'Rotate tile at row {{row}}, column {{column}}':
      'Rotate tile at row {{row}}, column {{column}}',
    'Connected to input': 'Connected to input',
    'Connected in {{count}} moves!': 'Connected in {{count}} moves!',
    'Signal reached {{count}} tiles': 'Signal reached {{count}} tiles',
    'New circuit': 'New circuit',
    'Play again': 'Play again',
    'Restart circuit': 'Restart circuit',
    'Use arrow keys to move and Enter to rotate.':
      'Use arrow keys to move and Enter to rotate.',
    'Just for fun. You can sign in or register at any time.':
      'Just for fun. You can sign in or register at any time.',
    'Play a round': 'Play a round',
  },
  zh: {
    'Signal path': '接通信号',
    'Rotate the tiles to connect input to output.':
      '转动线路格，让入口的信号抵达终点。',
    Moves: '步数',
    'Circuits solved': '已接通',
    North: '上方',
    East: '右侧',
    South: '下方',
    West: '左侧',
    'Rotate tile at row {{row}}, column {{column}}':
      '转动第 {{row}} 行、第 {{column}} 列的格子',
    'Connected to input': '已连接入口',
    'Connected in {{count}} moves!': '接通了！共用 {{count}} 步',
    'Signal reached {{count}} tiles': '信号已到达 {{count}} 个格子',
    'New circuit': '换一张图',
    'Play again': '再来一局',
    'Restart circuit': '重新开始这局',
    'Use arrow keys to move and Enter to rotate.':
      '也可用方向键选格，按 Enter 转动。',
    'Just for fun. You can sign in or register at any time.':
      '纯粹玩一下，随时都能登录或注册。',
    'Play a round': '玩一局',
  },
  'zh-TW': {
    'Signal path': '接通信號',
    'Rotate the tiles to connect input to output.':
      '轉動線路格，讓入口的訊號抵達終點。',
    Moves: '步數',
    'Circuits solved': '已接通',
    North: '上方',
    East: '右側',
    South: '下方',
    West: '左側',
    'Rotate tile at row {{row}}, column {{column}}':
      '轉動第 {{row}} 列、第 {{column}} 欄的格子',
    'Connected to input': '已連接入口',
    'Connected in {{count}} moves!': '接通了！共用 {{count}} 步',
    'Signal reached {{count}} tiles': '訊號已抵達 {{count}} 個格子',
    'New circuit': '換一張圖',
    'Play again': '再來一局',
    'Restart circuit': '重新開始這局',
    'Use arrow keys to move and Enter to rotate.':
      '也可用方向鍵選格，按 Enter 轉動。',
    'Just for fun. You can sign in or register at any time.':
      '輕鬆玩一下，隨時都能登入或註冊。',
    'Play a round': '玩一局',
  },
  fr: {
    'Signal path': 'Circuit du signal',
    'Rotate the tiles to connect input to output.':
      'Tournez les cases pour relier l’entrée à la sortie.',
    Moves: 'Coups',
    'Circuits solved': 'Circuits réussis',
    North: 'Haut',
    East: 'Droite',
    South: 'Bas',
    West: 'Gauche',
    'Rotate tile at row {{row}}, column {{column}}':
      'Tourner la case, ligne {{row}}, colonne {{column}}',
    'Connected to input': 'Relié à l’entrée',
    'Connected in {{count}} moves!': 'Connecté en {{count}} coups !',
    'Signal reached {{count}} tiles': 'Le signal atteint {{count}} cases',
    'New circuit': 'Nouveau circuit',
    'Play again': 'Rejouer',
    'Restart circuit': 'Recommencer ce circuit',
    'Use arrow keys to move and Enter to rotate.':
      'Utilisez les flèches pour vous déplacer et Entrée pour tourner.',
    'Just for fun. You can sign in or register at any time.':
      'Juste pour le plaisir. Connexion et inscription restent accessibles.',
    'Play a round': 'Faire une partie',
  },
  ja: {
    'Signal path': 'シグナル回路',
    'Rotate the tiles to connect input to output.':
      'タイルを回して、入力から出力までつなげましょう。',
    Moves: '手数',
    'Circuits solved': 'クリア数',
    North: '上',
    East: '右',
    South: '下',
    West: '左',
    'Rotate tile at row {{row}}, column {{column}}':
      '{{row}} 行 {{column}} 列のタイルを回転',
    'Connected to input': '入力に接続済み',
    'Connected in {{count}} moves!': '{{count}} 手で接続できました！',
    'Signal reached {{count}} tiles': '信号が {{count}} マスまで到達',
    'New circuit': '新しい回路',
    'Play again': 'もう一度遊ぶ',
    'Restart circuit': 'この回路をやり直す',
    'Use arrow keys to move and Enter to rotate.':
      '矢印キーで移動、Enter キーで回転できます。',
    'Just for fun. You can sign in or register at any time.':
      '気軽に遊べるミニゲームです。ログインや登録はいつでもできます。',
    'Play a round': 'ひと遊びする',
  },
  ru: {
    'Signal path': 'Путь сигнала',
    'Rotate the tiles to connect input to output.':
      'Поворачивайте плитки, чтобы соединить вход с выходом.',
    Moves: 'Ходы',
    'Circuits solved': 'Собрано цепей',
    North: 'Вверх',
    East: 'Вправо',
    South: 'Вниз',
    West: 'Влево',
    'Rotate tile at row {{row}}, column {{column}}':
      'Повернуть плитку: строка {{row}}, столбец {{column}}',
    'Connected to input': 'Соединено со входом',
    'Connected in {{count}} moves!': 'Соединено за {{count}} ходов!',
    'Signal reached {{count}} tiles': 'Сигнал достиг {{count}} плиток',
    'New circuit': 'Новая цепь',
    'Play again': 'Играть ещё',
    'Restart circuit': 'Начать эту цепь заново',
    'Use arrow keys to move and Enter to rotate.':
      'Стрелки — выбор плитки, Enter — поворот.',
    'Just for fun. You can sign in or register at any time.':
      'Просто для развлечения. Вход и регистрация доступны в любой момент.',
    'Play a round': 'Сыграть раунд',
  },
  vi: {
    'Signal path': 'Đường tín hiệu',
    'Rotate the tiles to connect input to output.':
      'Xoay các ô để nối đầu vào với đầu ra.',
    Moves: 'Số lượt',
    'Circuits solved': 'Mạch đã nối',
    North: 'Trên',
    East: 'Phải',
    South: 'Dưới',
    West: 'Trái',
    'Rotate tile at row {{row}}, column {{column}}':
      'Xoay ô ở hàng {{row}}, cột {{column}}',
    'Connected to input': 'Đã nối với đầu vào',
    'Connected in {{count}} moves!': 'Đã nối sau {{count}} lượt!',
    'Signal reached {{count}} tiles': 'Tín hiệu đã đến {{count}} ô',
    'New circuit': 'Mạch mới',
    'Play again': 'Chơi lại',
    'Restart circuit': 'Bắt đầu lại mạch này',
    'Use arrow keys to move and Enter to rotate.':
      'Dùng phím mũi tên để di chuyển, Enter để xoay.',
    'Just for fun. You can sign in or register at any time.':
      'Chỉ để giải trí. Bạn vẫn có thể đăng nhập hoặc đăng ký bất cứ lúc nào.',
    'Play a round': 'Chơi một ván',
  },
}
for (const [locale, values] of Object.entries(signalGameKeys)) {
  Object.assign(newKeys[locale], values)
}

const signalCompetitionKeys = {
  en: {
    Account: 'Account',
    'Account records': 'Account records',
    'Advanced mode': 'Advanced mode',
    'Board size': 'Board size',
    'Browser and WebMCP indicate the entry used, not verified human or model identity.':
      'Browser and WebMCP indicate the entry used, not verified human or model identity.',
    'Browser player': 'Browser player',
    'Challenge verification failed. Keep the local record and retry.':
      'Challenge verification failed. Keep the local record and retry.',
    'Challenges: 3-second countdown, no hints, ranked by moves then time.':
      'Challenges: 3-second countdown, no hints, ranked by moves then time.',
    'Copy failed. Select the prompt below to copy it.':
      'Copy failed. Select the prompt below to copy it.',
    'Copy prompt for AI': 'Copy prompt for AI',
    'Could not load account records': 'Could not load account records',
    'Email (optional, private)': 'Email (optional, private)',
    'Finish a circuit to record your result':
      'Finish a circuit to record your result',
    'Game service unavailable. Practice is still available.':
      'Game service unavailable. Practice is still available.',
    'Guest play is allowed. Only lmm_signal_submit requires sign-in. It never signs in, creates accounts or spends credits for you.':
      'Guest play is allowed. Only lmm_signal_submit requires sign-in. It never signs in, creates accounts or spends credits for you.',
    'Guest results stay in this browser. Sign in later to upload them; clearing browser data removes local records.':
      'Guest results stay in this browser. Sign in later to upload them; clearing browser data removes local records.',
    Leaderboard: 'Leaderboard',
    'Leaderboard unavailable. You can still practice.':
      'Leaderboard unavailable. You can still practice.',
    'Leaderboard, records and AI guide': 'Leaderboard, records and AI guide',
    'Let an AI play through WebMCP': 'Let an AI play through WebMCP',
    'Local records': 'Local records',
    'Local storage failed. Keep this page open until your record is saved.':
      'Local storage failed. Keep this page open until your record is saved.',
    'No submitted scores yet': 'No submitted scores yet',
    Practice: 'Practice',
    Private: 'Private',
    Public: 'Public',
    'Public note (optional)': 'Public note (optional)',
    'Read the board with lmm_signal_state; rotate at most 32 tiles per lmm_signal_rotate call. Challenge hints are disabled, including through tools.':
      'Read the board with lmm_signal_state; rotate at most 32 tiles per lmm_signal_rotate call. Challenge hints are disabled, including through tools.',
    'Retry score verification': 'Retry score verification',
    'Rotate selected tile': 'Rotate selected tile',
    'Same daily board, ranked by moves, then time. Only submitted challenges are public.':
      'Same daily board, ranked by moves, then time. Only submitted challenges are public.',
    'Save to my records': 'Save to my records',
    'Score saved': 'Score saved',
    'Score submission failed. Your local record is still available.':
      'Score submission failed. Your local record is still available.',
    'Select a record': 'Select a record',
    'Sign in to submit your score': 'Sign in to submit your score',
    'Start challenge': 'Start challenge',
    'Submit to leaderboard': 'Submit to leaderboard',
    'The agent must declare its model ID, harness and agent name before a round. Uploaded scores belong to the account signed in at submission.':
      'The agent must declare its model ID, harness and agent name before a round. Uploaded scores belong to the account signed in at submission.',
    'Use a browser agent that supports document.modelContext. Open this game page in that browser.':
      'Use a browser agent that supports document.modelContext. Open this game page in that browser.',
    'Zoom in': 'Zoom in',
    'Zoom out': 'Zoom out',
    'Read the game board and current round':
      'Read the game board and current round',
    'Start a game as an identified AI participant':
      'Start a game as an identified AI participant',
    'Rotate game tiles in bounded batches':
      'Rotate game tiles in bounded batches',
    'Use a hint in practice mode only': 'Use a hint in practice mode only',
    'Read game records and the leaderboard':
      'Read game records and the leaderboard',
    'Submit a completed result using the signed-in account':
      'Submit a completed result using the signed-in account',
    'Open {{url}} and play one {{size}} × {{size}} Signal path challenge through WebMCP. Before starting, provide your actual model ID, harness name and an agent name in lmm_signal_start. Ask me if an identity field is unknown; do not invent it. Read lmm_signal_state, wait for the three-second countdown, and use lmm_signal_rotate with the returned round_id. Hints are forbidden. Stop after winning or {{limit}} rotations; do not automatically start another round. Report moves and time. Submit with lmm_signal_submit only when I ask you to upload; it uses my signed-in account. If signed out, ask me to sign in manually and keep the local record. Do not register an account or perform payments.':
      'Open {{url}} and play one {{size}} × {{size}} Signal path challenge through WebMCP. Before starting, provide your actual model ID, harness name and an agent name in lmm_signal_start. Ask me if an identity field is unknown; do not invent it. Read lmm_signal_state, wait for the three-second countdown, and use lmm_signal_rotate with the returned round_id. Hints are forbidden. Stop after winning or {{limit}} rotations; do not automatically start another round. Report moves and time. Submit with lmm_signal_submit only when I ask you to upload; it uses my signed-in account. If signed out, ask me to sign in manually and keep the local record. Do not register an account or perform payments.',
  },
  zh: {
    Account: '账号',
    'Account records': '账号记录',
    'Advanced mode': '进阶模式',
    'Board size': '棋盘尺寸',
    'Browser and WebMCP indicate the entry used, not verified human or model identity.':
      '网页／WebMCP 表示参与入口，不代表已验证真人或模型身份。',
    'Browser player': '网页玩家',
    'Challenge verification failed. Keep the local record and retry.':
      '挑战验证失败，请保留本机记录并重试。',
    'Challenges: 3-second countdown, no hints, ranked by moves then time.':
      '挑战开始前倒计时 3 秒，不提供提示；排名先比步数，再比用时。',
    'Copy failed. Select the prompt below to copy it.':
      '复制失败，请选中下方提示词手动复制。',
    'Copy prompt for AI': '复制给 AI',
    'Could not load account records': '无法加载账号记录',
    'Email (optional, private)': '邮箱（可选，不公开）',
    'Finish a circuit to record your result': '接通一局后会留下记录',
    'Game service unavailable. Practice is still available.':
      '游戏服务暂不可用，仍可玩练习模式。',
    'Guest play is allowed. Only lmm_signal_submit requires sign-in. It never signs in, creates accounts or spends credits for you.':
      '游客可以玩；只有 lmm_signal_submit 要求登录。工具不会代你登录、注册账号或消费额度。',
    'Guest results stay in this browser. Sign in later to upload them; clearing browser data removes local records.':
      '游客成绩保存在此浏览器，登录后可以补交；清除浏览器数据会删除本机记录。',
    Leaderboard: '排行榜',
    'Leaderboard unavailable. You can still practice.':
      '暂时无法获取排行榜，仍可继续练习。',
    'Leaderboard, records and AI guide': '排行榜、记录与 AI 教程',
    'Let an AI play through WebMCP': '通过 WebMCP 让 AI 来玩',
    'Local records': '本机记录',
    'Local storage failed. Keep this page open until your record is saved.':
      '本机保存失败，记录保存前请保留此页面。',
    'No submitted scores yet': '还没有人提交成绩',
    Practice: '练习',
    Private: '私有',
    Public: '公开',
    'Public note (optional)': '公开备注（可选）',
    'Read the board with lmm_signal_state; rotate at most 32 tiles per lmm_signal_rotate call. Challenge hints are disabled, including through tools.':
      '用 lmm_signal_state 读取棋盘；每次 lmm_signal_rotate 最多旋转 32 格。挑战模式禁止提示，工具也不能绕过。',
    'Retry score verification': '重试成绩验证',
    'Rotate selected tile': '转动选中的格子',
    'Same daily board, ranked by moves, then time. Only submitted challenges are public.':
      '每日同图，先比步数，再比用时；只有主动提交的挑战成绩会公开。',
    'Save to my records': '保存到我的记录',
    'Score saved': '成绩已保存',
    'Score submission failed. Your local record is still available.':
      '成绩提交失败，本机记录仍然保留。',
    'Select a record': '选择记录',
    'Sign in to submit your score': '登录后提交成绩',
    'Start challenge': '开始挑战',
    'Submit to leaderboard': '提交到排行榜',
    'The agent must declare its model ID, harness and agent name before a round. Uploaded scores belong to the account signed in at submission.':
      'Agent 开局前必须填写模型 ID、harness 和 Agent 名称。上传的成绩归属提交时登录的账号。',
    'Use a browser agent that supports document.modelContext. Open this game page in that browser.':
      '使用支持 document.modelContext 的浏览器代理，并在该浏览器中打开本游戏页面。',
    'Zoom in': '放大',
    'Zoom out': '缩小',
    'Read the game board and current round': '读取棋盘与当前对局',
    'Start a game as an identified AI participant':
      '以已填写身份的 AI 玩家开局',
    'Rotate game tiles in bounded batches': '分批旋转格子',
    'Use a hint in practice mode only': '仅在练习模式使用提示',
    'Read game records and the leaderboard': '读取对局记录与排行榜',
    'Submit a completed result using the signed-in account':
      '用当前登录账号提交完成的成绩',
    'Open {{url}} and play one {{size}} × {{size}} Signal path challenge through WebMCP. Before starting, provide your actual model ID, harness name and an agent name in lmm_signal_start. Ask me if an identity field is unknown; do not invent it. Read lmm_signal_state, wait for the three-second countdown, and use lmm_signal_rotate with the returned round_id. Hints are forbidden. Stop after winning or {{limit}} rotations; do not automatically start another round. Report moves and time. Submit with lmm_signal_submit only when I ask you to upload; it uses my signed-in account. If signed out, ask me to sign in manually and keep the local record. Do not register an account or perform payments.':
      '打开 {{url}}，通过 WebMCP 玩一局 {{size}} × {{size}} 的接通信号挑战。开局前，在 lmm_signal_start 填写真实模型 ID、harness 名称和 Agent 名称；不知道的身份字段先问我，不要编造。用 lmm_signal_state 读取棋盘，等待 3 秒倒计时，然后携带返回的 round_id 调用 lmm_signal_rotate。禁止提示。通关或旋转达到 {{limit}} 次后立即停止，不自动开新局，报告步数和用时。只有我要求上传时才用 lmm_signal_submit 提交，归属我当前登录的账号。未登录时请让我手动登录，并保留本机记录。不要注册账号或进行付款。',
  },
  'zh-TW': {
    Account: '帳號',
    'Account records': '帳號紀錄',
    'Advanced mode': '進階模式',
    'Board size': '棋盤尺寸',
    'Browser and WebMCP indicate the entry used, not verified human or model identity.':
      '網頁／WebMCP 表示參與入口，不代表已驗證真人或模型身分。',
    'Browser player': '網頁玩家',
    'Challenge verification failed. Keep the local record and retry.':
      '挑戰驗證失敗，請保留本機紀錄並重試。',
    'Challenges: 3-second countdown, no hints, ranked by moves then time.':
      '挑戰開始前倒數 3 秒，不提供提示；排名先比步數，再比用時。',
    'Copy failed. Select the prompt below to copy it.':
      '複製失敗，請選取下方提示詞手動複製。',
    'Copy prompt for AI': '複製給 AI',
    'Could not load account records': '無法載入帳號紀錄',
    'Email (optional, private)': '電子郵件（選填，不公開）',
    'Finish a circuit to record your result': '接通一局後會留下紀錄',
    'Game service unavailable. Practice is still available.':
      '遊戲服務暫時無法使用，仍可玩練習模式。',
    'Guest play is allowed. Only lmm_signal_submit requires sign-in. It never signs in, creates accounts or spends credits for you.':
      '訪客可以玩；只有 lmm_signal_submit 要求登入。工具不會代你登入、註冊帳號或消費額度。',
    'Guest results stay in this browser. Sign in later to upload them; clearing browser data removes local records.':
      '訪客成績儲存在此瀏覽器，登入後可以補交；清除瀏覽器資料會刪除本機紀錄。',
    Leaderboard: '排行榜',
    'Leaderboard unavailable. You can still practice.':
      '暫時無法取得排行榜，仍可繼續練習。',
    'Leaderboard, records and AI guide': '排行榜、紀錄與 AI 教學',
    'Let an AI play through WebMCP': '透過 WebMCP 讓 AI 來玩',
    'Local records': '本機紀錄',
    'Local storage failed. Keep this page open until your record is saved.':
      '本機儲存失敗，紀錄儲存前請保留此頁面。',
    'No submitted scores yet': '還沒有人提交成績',
    Practice: '練習',
    Private: '私人',
    Public: '公開',
    'Public note (optional)': '公開備註（選填）',
    'Read the board with lmm_signal_state; rotate at most 32 tiles per lmm_signal_rotate call. Challenge hints are disabled, including through tools.':
      '用 lmm_signal_state 讀取棋盤；每次 lmm_signal_rotate 最多旋轉 32 格。挑戰模式禁止提示，工具也不能繞過。',
    'Retry score verification': '重試成績驗證',
    'Rotate selected tile': '轉動選取的格子',
    'Same daily board, ranked by moves, then time. Only submitted challenges are public.':
      '每日同圖，先比步數，再比用時；只有主動提交的挑戰成績會公開。',
    'Save to my records': '儲存至我的紀錄',
    'Score saved': '成績已儲存',
    'Score submission failed. Your local record is still available.':
      '成績提交失敗，本機紀錄仍然保留。',
    'Select a record': '選擇紀錄',
    'Sign in to submit your score': '登入後提交成績',
    'Start challenge': '開始挑戰',
    'Submit to leaderboard': '提交至排行榜',
    'The agent must declare its model ID, harness and agent name before a round. Uploaded scores belong to the account signed in at submission.':
      'Agent 開局前必須填寫模型 ID、harness 和 Agent 名稱。上傳的成績歸屬提交時登入的帳號。',
    'Use a browser agent that supports document.modelContext. Open this game page in that browser.':
      '使用支援 document.modelContext 的瀏覽器代理，並在該瀏覽器開啟本遊戲頁面。',
    'Zoom in': '放大',
    'Zoom out': '縮小',
    'Read the game board and current round': '讀取棋盤與目前對局',
    'Start a game as an identified AI participant':
      '以已填寫身分的 AI 玩家開局',
    'Rotate game tiles in bounded batches': '分批旋轉格子',
    'Use a hint in practice mode only': '僅在練習模式使用提示',
    'Read game records and the leaderboard': '讀取對局紀錄與排行榜',
    'Submit a completed result using the signed-in account':
      '用目前登入帳號提交完成的成績',
    'Open {{url}} and play one {{size}} × {{size}} Signal path challenge through WebMCP. Before starting, provide your actual model ID, harness name and an agent name in lmm_signal_start. Ask me if an identity field is unknown; do not invent it. Read lmm_signal_state, wait for the three-second countdown, and use lmm_signal_rotate with the returned round_id. Hints are forbidden. Stop after winning or {{limit}} rotations; do not automatically start another round. Report moves and time. Submit with lmm_signal_submit only when I ask you to upload; it uses my signed-in account. If signed out, ask me to sign in manually and keep the local record. Do not register an account or perform payments.':
      '開啟 {{url}}，透過 WebMCP 玩一局 {{size}} × {{size}} 的接通信號挑戰。開局前，在 lmm_signal_start 填寫真實模型 ID、harness 名稱和 Agent 名稱；不知道的身分欄位先問我，不要編造。用 lmm_signal_state 讀取棋盤，等待 3 秒倒數，再帶上回傳的 round_id 呼叫 lmm_signal_rotate。禁止提示。通關或旋轉達到 {{limit}} 次後立即停止，不自動開新局，回報步數與用時。只有我要求上傳時才用 lmm_signal_submit 提交，歸屬我目前登入的帳號。未登入時請讓我手動登入，並保留本機紀錄。不要註冊帳號或付款。',
  },
  fr: {
    Account: 'Compte',
    'Account records': 'Historique du compte',
    'Advanced mode': 'Mode avancé',
    'Board size': 'Taille de grille',
    'Browser and WebMCP indicate the entry used, not verified human or model identity.':
      'Navigateur et WebMCP indiquent le mode d’accès, pas une identité humaine ou un modèle vérifié.',
    'Browser player': 'Joueur navigateur',
    'Challenge verification failed. Keep the local record and retry.':
      'Échec de vérification. Conservez la partie locale et réessayez.',
    'Challenges: 3-second countdown, no hints, ranked by moves then time.':
      'Défi : décompte de 3 secondes, sans indice. Classement par coups, puis par temps.',
    'Copy failed. Select the prompt below to copy it.':
      'Copie impossible. Sélectionnez le texte ci-dessous pour le copier.',
    'Copy prompt for AI': 'Copier pour l’IA',
    'Could not load account records': 'Historique du compte indisponible',
    'Email (optional, private)': 'E-mail (facultatif, privé)',
    'Finish a circuit to record your result':
      'Terminez un circuit pour enregistrer votre résultat',
    'Game service unavailable. Practice is still available.':
      'Service de jeu indisponible. L’entraînement reste accessible.',
    'Guest play is allowed. Only lmm_signal_submit requires sign-in. It never signs in, creates accounts or spends credits for you.':
      'Le jeu est ouvert aux invités. Seul lmm_signal_submit exige une connexion. Aucun outil ne se connecte, ne crée de compte ni ne dépense de crédits pour vous.',
    'Guest results stay in this browser. Sign in later to upload them; clearing browser data removes local records.':
      'Les résultats invités restent dans ce navigateur et peuvent être envoyés après connexion. Effacer les données du navigateur les supprime.',
    Leaderboard: 'Classement',
    'Leaderboard unavailable. You can still practice.':
      'Classement indisponible. Vous pouvez continuer à vous entraîner.',
    'Leaderboard, records and AI guide': 'Classement, historique et guide IA',
    'Let an AI play through WebMCP': 'Faire jouer une IA via WebMCP',
    'Local records': 'Historique local',
    'Local storage failed. Keep this page open until your record is saved.':
      'Échec de sauvegarde locale. Gardez cette page ouverte jusqu’à l’enregistrement.',
    'No submitted scores yet': 'Aucun score envoyé',
    Practice: 'Entraînement',
    Private: 'Privé',
    Public: 'Public',
    'Public note (optional)': 'Note publique (facultative)',
    'Read the board with lmm_signal_state; rotate at most 32 tiles per lmm_signal_rotate call. Challenge hints are disabled, including through tools.':
      'Lisez la grille avec lmm_signal_state ; chaque appel lmm_signal_rotate tourne au plus 32 cases. Aucun indice en défi, même via les outils.',
    'Retry score verification': 'Revérifier le score',
    'Rotate selected tile': 'Tourner la case sélectionnée',
    'Same daily board, ranked by moves, then time. Only submitted challenges are public.':
      'Même grille du jour, classement par coups puis par temps. Seuls les défis envoyés sont publics.',
    'Save to my records': 'Enregistrer dans mon historique',
    'Score saved': 'Score enregistré',
    'Score submission failed. Your local record is still available.':
      'Échec de l’envoi. Votre résultat local est conservé.',
    'Select a record': 'Choisir une partie',
    'Sign in to submit your score': 'Se connecter pour envoyer un score',
    'Start challenge': 'Lancer le défi',
    'Submit to leaderboard': 'Publier au classement',
    'The agent must declare its model ID, harness and agent name before a round. Uploaded scores belong to the account signed in at submission.':
      'L’agent déclare son identifiant de modèle, son harness et son nom avant de jouer. Le score appartient au compte connecté lors de l’envoi.',
    'Use a browser agent that supports document.modelContext. Open this game page in that browser.':
      'Utilisez un agent navigateur compatible avec document.modelContext et ouvrez cette page de jeu dans ce navigateur.',
    'Zoom in': 'Agrandir',
    'Zoom out': 'Réduire',
    'Read the game board and current round': 'Lire la grille et la partie',
    'Start a game as an identified AI participant':
      'Démarrer avec une identité IA déclarée',
    'Rotate game tiles in bounded batches': 'Tourner un lot limité de cases',
    'Use a hint in practice mode only': 'Utiliser un indice en entraînement',
    'Read game records and the leaderboard':
      'Lire l’historique et le classement',
    'Submit a completed result using the signed-in account':
      'Envoyer un résultat avec le compte connecté',
    'Open {{url}} and play one {{size}} × {{size}} Signal path challenge through WebMCP. Before starting, provide your actual model ID, harness name and an agent name in lmm_signal_start. Ask me if an identity field is unknown; do not invent it. Read lmm_signal_state, wait for the three-second countdown, and use lmm_signal_rotate with the returned round_id. Hints are forbidden. Stop after winning or {{limit}} rotations; do not automatically start another round. Report moves and time. Submit with lmm_signal_submit only when I ask you to upload; it uses my signed-in account. If signed out, ask me to sign in manually and keep the local record. Do not register an account or perform payments.':
      'Ouvre {{url}} et joue un seul défi Signal path de {{size}} × {{size}} via WebMCP. Dans lmm_signal_start, indique ton vrai ID de modèle, ton harness et un nom d’agent. Demande-moi toute identité inconnue, sans l’inventer. Lis lmm_signal_state, attends le décompte de trois secondes, puis utilise lmm_signal_rotate avec le round_id reçu. Aucun indice. Arrête-toi dès la victoire ou après {{limit}} rotations, sans relancer de partie. Rapporte les coups et le temps. Utilise lmm_signal_submit uniquement si je demande l’envoi, avec mon compte connecté. Sinon, demande-moi de me connecter manuellement et conserve la partie locale. Ne crée aucun compte et n’effectue aucun paiement.',
  },
  ja: {
    Account: 'アカウント',
    'Account records': 'アカウントの記録',
    'Advanced mode': '上級モード',
    'Board size': '盤面サイズ',
    'Browser and WebMCP indicate the entry used, not verified human or model identity.':
      'ブラウザー／WebMCP は参加経路を示します。人間やモデルの本人確認を保証するものではありません。',
    'Browser player': 'ブラウザー参加者',
    'Challenge verification failed. Keep the local record and retry.':
      'チャレンジの検証に失敗しました。ローカル記録を残して再試行してください。',
    'Challenges: 3-second countdown, no hints, ranked by moves then time.':
      'チャレンジは3秒カウントダウン後に開始。ヒントなし、手数とタイムの順で順位が決まります。',
    'Copy failed. Select the prompt below to copy it.':
      'コピーできませんでした。下の文章を選択してコピーしてください。',
    'Copy prompt for AI': 'AI 用プロンプトをコピー',
    'Could not load account records': 'アカウントの記録を読み込めません',
    'Email (optional, private)': 'メールアドレス（任意・非公開）',
    'Finish a circuit to record your result':
      '回路を完成させると結果が記録されます',
    'Game service unavailable. Practice is still available.':
      'ゲームサービスを利用できません。練習モードは引き続き遊べます。',
    'Guest play is allowed. Only lmm_signal_submit requires sign-in. It never signs in, creates accounts or spends credits for you.':
      'ゲストでも遊べます。ログインが必要なのは lmm_signal_submit だけです。ツールがログイン、登録、クレジット消費を代行することはありません。',
    'Guest results stay in this browser. Sign in later to upload them; clearing browser data removes local records.':
      'ゲストの結果はこのブラウザーに保存され、ログイン後に送信できます。ブラウザーのデータを消すと記録も消えます。',
    Leaderboard: 'ランキング',
    'Leaderboard unavailable. You can still practice.':
      'ランキングを取得できません。練習は続けられます。',
    'Leaderboard, records and AI guide': 'ランキング・記録・AI ガイド',
    'Let an AI play through WebMCP': 'WebMCP で AI に遊んでもらう',
    'Local records': 'ローカル記録',
    'Local storage failed. Keep this page open until your record is saved.':
      'ローカル保存に失敗しました。保存されるまでこのページを開いたままにしてください。',
    'No submitted scores yet': 'まだスコアが送信されていません',
    Practice: '練習',
    Private: '非公開',
    Public: '公開',
    'Public note (optional)': '公開メモ（任意）',
    'Read the board with lmm_signal_state; rotate at most 32 tiles per lmm_signal_rotate call. Challenge hints are disabled, including through tools.':
      'lmm_signal_state で盤面を読み、lmm_signal_rotate 1回につき最大32マスを回転できます。チャレンジではツール経由でもヒントは使えません。',
    'Retry score verification': 'スコアを再検証',
    'Rotate selected tile': '選択したタイルを回転',
    'Same daily board, ranked by moves, then time. Only submitted challenges are public.':
      '毎日同じ盤面で、手数、タイムの順に順位を決めます。送信したチャレンジ結果だけが公開されます。',
    'Save to my records': '自分の記録に保存',
    'Score saved': 'スコアを保存しました',
    'Score submission failed. Your local record is still available.':
      'スコアを送信できませんでした。ローカル記録は残っています。',
    'Select a record': '記録を選択',
    'Sign in to submit your score': 'ログインしてスコアを送信',
    'Start challenge': 'チャレンジ開始',
    'Submit to leaderboard': 'ランキングに送信',
    'The agent must declare its model ID, harness and agent name before a round. Uploaded scores belong to the account signed in at submission.':
      '開始前にモデル ID、harness、Agent 名を登録します。送信した結果は送信時にログインしているアカウントに紐づきます。',
    'Use a browser agent that supports document.modelContext. Open this game page in that browser.':
      'document.modelContext 対応のブラウザーエージェントを使い、そのブラウザーでこのゲームページを開きます。',
    'Zoom in': '拡大',
    'Zoom out': '縮小',
    'Read the game board and current round': '盤面と現在のラウンドを読む',
    'Start a game as an identified AI participant':
      'AI の参加情報を指定して開始',
    'Rotate game tiles in bounded batches': '上限付きの一括回転',
    'Use a hint in practice mode only': '練習モードでのみヒントを使用',
    'Read game records and the leaderboard': '記録とランキングを読む',
    'Submit a completed result using the signed-in account':
      'ログイン中のアカウントで結果を送信',
    'Open {{url}} and play one {{size}} × {{size}} Signal path challenge through WebMCP. Before starting, provide your actual model ID, harness name and an agent name in lmm_signal_start. Ask me if an identity field is unknown; do not invent it. Read lmm_signal_state, wait for the three-second countdown, and use lmm_signal_rotate with the returned round_id. Hints are forbidden. Stop after winning or {{limit}} rotations; do not automatically start another round. Report moves and time. Submit with lmm_signal_submit only when I ask you to upload; it uses my signed-in account. If signed out, ask me to sign in manually and keep the local record. Do not register an account or perform payments.':
      '{{url}} を開き、WebMCP で {{size}} × {{size}} の Signal path チャレンジを1回遊んでください。lmm_signal_start に実際のモデル ID、harness 名、Agent 名を指定します。不明な情報は捏造せず私に確認してください。lmm_signal_state で盤面を読み、3秒のカウントダウンを待ち、返された round_id を使って lmm_signal_rotate を呼びます。ヒントは禁止です。成功するか {{limit}} 回回転したら停止し、自動で次のラウンドを始めず、手数とタイムを報告してください。私が送信を頼んだ場合のみ lmm_signal_submit でログイン中の私のアカウントに結果を送信してください。未ログインなら手動ログインを私に依頼し、ローカル記録を保持してください。アカウント登録や支払いはしないでください。',
  },
  ru: {
    Account: 'Аккаунт',
    'Account records': 'Записи аккаунта',
    'Advanced mode': 'Продвинутый режим',
    'Board size': 'Размер поля',
    'Browser and WebMCP indicate the entry used, not verified human or model identity.':
      'Браузер и WebMCP обозначают способ участия, а не подтверждённую личность человека или модели.',
    'Browser player': 'Игрок в браузере',
    'Challenge verification failed. Keep the local record and retry.':
      'Проверка испытания не удалась. Сохраните локальную запись и повторите попытку.',
    'Challenges: 3-second countdown, no hints, ranked by moves then time.':
      'Испытание: отсчёт 3 секунды, без подсказок. Рейтинг по ходам, затем по времени.',
    'Copy failed. Select the prompt below to copy it.':
      'Не удалось скопировать. Выделите текст ниже и скопируйте вручную.',
    'Copy prompt for AI': 'Скопировать запрос для ИИ',
    'Could not load account records': 'Не удалось загрузить записи аккаунта',
    'Email (optional, private)': 'Почта (необязательно, скрыта)',
    'Finish a circuit to record your result':
      'Завершите цепь, чтобы появилась запись',
    'Game service unavailable. Practice is still available.':
      'Игровой сервис недоступен. Тренировка по-прежнему работает.',
    'Guest play is allowed. Only lmm_signal_submit requires sign-in. It never signs in, creates accounts or spends credits for you.':
      'Гости могут играть. Вход нужен только для lmm_signal_submit. Инструменты не входят в аккаунт, не регистрируют его и не тратят кредиты за вас.',
    'Guest results stay in this browser. Sign in later to upload them; clearing browser data removes local records.':
      'Результаты гостя хранятся в этом браузере и доступны для отправки после входа. Очистка данных браузера удалит их.',
    Leaderboard: 'Рейтинг',
    'Leaderboard unavailable. You can still practice.':
      'Рейтинг недоступен. Можно продолжить тренировку.',
    'Leaderboard, records and AI guide': 'Рейтинг, записи и руководство для ИИ',
    'Let an AI play through WebMCP': 'Пусть ИИ сыграет через WebMCP',
    'Local records': 'Локальные записи',
    'Local storage failed. Keep this page open until your record is saved.':
      'Локальное сохранение не удалось. Не закрывайте страницу, пока запись не сохранена.',
    'No submitted scores yet': 'Пока нет отправленных результатов',
    Practice: 'Тренировка',
    Private: 'Личное',
    Public: 'Публичное',
    'Public note (optional)': 'Публичная заметка (необязательно)',
    'Read the board with lmm_signal_state; rotate at most 32 tiles per lmm_signal_rotate call. Challenge hints are disabled, including through tools.':
      'Читайте поле через lmm_signal_state; один вызов lmm_signal_rotate поворачивает до 32 плиток. В испытании подсказки запрещены и через инструменты.',
    'Retry score verification': 'Повторить проверку результата',
    'Rotate selected tile': 'Повернуть выбранную плитку',
    'Same daily board, ranked by moves, then time. Only submitted challenges are public.':
      'Одинаковое поле дня, рейтинг по ходам, затем по времени. Публичны только отправленные результаты испытаний.',
    'Save to my records': 'Сохранить в мои записи',
    'Score saved': 'Результат сохранён',
    'Score submission failed. Your local record is still available.':
      'Не удалось отправить результат. Локальная запись сохранена.',
    'Select a record': 'Выбрать запись',
    'Sign in to submit your score': 'Войти для отправки результата',
    'Start challenge': 'Начать испытание',
    'Submit to leaderboard': 'Отправить в рейтинг',
    'The agent must declare its model ID, harness and agent name before a round. Uploaded scores belong to the account signed in at submission.':
      'Перед игрой агент указывает ID модели, harness и имя агента. Результат принадлежит аккаунту, в который выполнен вход при отправке.',
    'Use a browser agent that supports document.modelContext. Open this game page in that browser.':
      'Используйте браузерного агента с поддержкой document.modelContext и откройте в нём эту страницу игры.',
    'Zoom in': 'Увеличить',
    'Zoom out': 'Уменьшить',
    'Read the game board and current round': 'Прочитать поле и текущий раунд',
    'Start a game as an identified AI participant':
      'Начать игру с указанной личностью ИИ',
    'Rotate game tiles in bounded batches':
      'Повернуть ограниченную группу плиток',
    'Use a hint in practice mode only': 'Подсказка только в тренировке',
    'Read game records and the leaderboard': 'Прочитать записи и рейтинг',
    'Submit a completed result using the signed-in account':
      'Отправить результат от текущего аккаунта',
    'Open {{url}} and play one {{size}} × {{size}} Signal path challenge through WebMCP. Before starting, provide your actual model ID, harness name and an agent name in lmm_signal_start. Ask me if an identity field is unknown; do not invent it. Read lmm_signal_state, wait for the three-second countdown, and use lmm_signal_rotate with the returned round_id. Hints are forbidden. Stop after winning or {{limit}} rotations; do not automatically start another round. Report moves and time. Submit with lmm_signal_submit only when I ask you to upload; it uses my signed-in account. If signed out, ask me to sign in manually and keep the local record. Do not register an account or perform payments.':
      'Открой {{url}} и сыграй один раунд Signal path {{size}} × {{size}} через WebMCP. Перед началом укажи в lmm_signal_start настоящий ID модели, harness и имя агента. Неизвестные данные спроси у меня, не выдумывай. Прочитай lmm_signal_state, дождись трёхсекундного отсчёта и вызывай lmm_signal_rotate с полученным round_id. Подсказки запрещены. Остановись после победы или {{limit}} поворотов, не начинай новую игру автоматически. Сообщи ходы и время. Отправляй результат через lmm_signal_submit только по моей просьбе, от аккаунта, в который я вошёл. Если вход не выполнен, попроси меня войти вручную и сохрани локальную запись. Не регистрируй аккаунт и не выполняй платежи.',
  },
  vi: {
    Account: 'Tài khoản',
    'Account records': 'Lịch sử tài khoản',
    'Advanced mode': 'Chế độ nâng cao',
    'Board size': 'Kích thước bàn chơi',
    'Browser and WebMCP indicate the entry used, not verified human or model identity.':
      'Trình duyệt và WebMCP chỉ cho biết cách tham gia, không xác minh danh tính con người hay mô hình.',
    'Browser player': 'Người chơi qua trình duyệt',
    'Challenge verification failed. Keep the local record and retry.':
      'Xác minh thử thách thất bại. Giữ bản ghi trên thiết bị và thử lại.',
    'Challenges: 3-second countdown, no hints, ranked by moves then time.':
      'Thử thách đếm ngược 3 giây, không có gợi ý; xếp hạng theo số lượt rồi đến thời gian.',
    'Copy failed. Select the prompt below to copy it.':
      'Sao chép thất bại. Hãy chọn nội dung bên dưới để sao chép.',
    'Copy prompt for AI': 'Sao chép yêu cầu cho AI',
    'Could not load account records': 'Không tải được lịch sử tài khoản',
    'Email (optional, private)': 'Email (không bắt buộc, riêng tư)',
    'Finish a circuit to record your result':
      'Hoàn thành một mạch để ghi lại kết quả',
    'Game service unavailable. Practice is still available.':
      'Dịch vụ trò chơi chưa khả dụng. Bạn vẫn có thể luyện tập.',
    'Guest play is allowed. Only lmm_signal_submit requires sign-in. It never signs in, creates accounts or spends credits for you.':
      'Khách vẫn chơi được. Chỉ lmm_signal_submit yêu cầu đăng nhập. Công cụ không tự đăng nhập, tạo tài khoản hay tiêu tín dụng.',
    'Guest results stay in this browser. Sign in later to upload them; clearing browser data removes local records.':
      'Kết quả của khách được lưu trong trình duyệt này và có thể gửi sau khi đăng nhập. Xóa dữ liệu trình duyệt sẽ xóa các bản ghi này.',
    Leaderboard: 'Bảng xếp hạng',
    'Leaderboard unavailable. You can still practice.':
      'Chưa tải được bảng xếp hạng. Bạn vẫn có thể luyện tập.',
    'Leaderboard, records and AI guide': 'Xếp hạng, lịch sử và hướng dẫn AI',
    'Let an AI play through WebMCP': 'Cho AI chơi qua WebMCP',
    'Local records': 'Lịch sử trên thiết bị',
    'Local storage failed. Keep this page open until your record is saved.':
      'Lưu trên thiết bị thất bại. Giữ trang mở cho đến khi bản ghi được lưu.',
    'No submitted scores yet': 'Chưa có điểm nào được gửi',
    Practice: 'Luyện tập',
    Private: 'Riêng tư',
    Public: 'Công khai',
    'Public note (optional)': 'Ghi chú công khai (không bắt buộc)',
    'Read the board with lmm_signal_state; rotate at most 32 tiles per lmm_signal_rotate call. Challenge hints are disabled, including through tools.':
      'Đọc bàn chơi bằng lmm_signal_state; mỗi lmm_signal_rotate xoay tối đa 32 ô. Thử thách không cho phép gợi ý, kể cả qua công cụ.',
    'Retry score verification': 'Thử xác minh điểm lại',
    'Rotate selected tile': 'Xoay ô đã chọn',
    'Same daily board, ranked by moves, then time. Only submitted challenges are public.':
      'Cùng bàn chơi mỗi ngày, xếp hạng theo số lượt rồi thời gian. Chỉ kết quả thử thách chủ động gửi mới được công khai.',
    'Save to my records': 'Lưu vào lịch sử của tôi',
    'Score saved': 'Đã lưu điểm',
    'Score submission failed. Your local record is still available.':
      'Gửi điểm thất bại. Bản ghi trên thiết bị vẫn còn.',
    'Select a record': 'Chọn bản ghi',
    'Sign in to submit your score': 'Đăng nhập để gửi điểm',
    'Start challenge': 'Bắt đầu thử thách',
    'Submit to leaderboard': 'Gửi lên bảng xếp hạng',
    'The agent must declare its model ID, harness and agent name before a round. Uploaded scores belong to the account signed in at submission.':
      'Agent phải khai báo ID mô hình, harness và tên trước khi chơi. Điểm gửi lên thuộc tài khoản đang đăng nhập lúc gửi.',
    'Use a browser agent that supports document.modelContext. Open this game page in that browser.':
      'Dùng tác nhân trình duyệt hỗ trợ document.modelContext và mở trang trò chơi này trong trình duyệt đó.',
    'Zoom in': 'Phóng to',
    'Zoom out': 'Thu nhỏ',
    'Read the game board and current round': 'Đọc bàn chơi và ván hiện tại',
    'Start a game as an identified AI participant':
      'Bắt đầu với thông tin người chơi AI',
    'Rotate game tiles in bounded batches': 'Xoay các ô theo nhóm có giới hạn',
    'Use a hint in practice mode only': 'Chỉ dùng gợi ý trong luyện tập',
    'Read game records and the leaderboard': 'Đọc lịch sử và bảng xếp hạng',
    'Submit a completed result using the signed-in account':
      'Gửi kết quả bằng tài khoản đang đăng nhập',
    'Open {{url}} and play one {{size}} × {{size}} Signal path challenge through WebMCP. Before starting, provide your actual model ID, harness name and an agent name in lmm_signal_start. Ask me if an identity field is unknown; do not invent it. Read lmm_signal_state, wait for the three-second countdown, and use lmm_signal_rotate with the returned round_id. Hints are forbidden. Stop after winning or {{limit}} rotations; do not automatically start another round. Report moves and time. Submit with lmm_signal_submit only when I ask you to upload; it uses my signed-in account. If signed out, ask me to sign in manually and keep the local record. Do not register an account or perform payments.':
      'Mở {{url}} và chơi một thử thách Signal path {{size}} × {{size}} qua WebMCP. Trước khi bắt đầu, điền ID mô hình thực, tên harness và tên Agent vào lmm_signal_start. Hỏi tôi nếu không biết thông tin nhận dạng, không tự bịa. Đọc lmm_signal_state, chờ đếm ngược ba giây rồi dùng lmm_signal_rotate với round_id đã nhận. Không dùng gợi ý. Dừng khi thắng hoặc đạt {{limit}} lượt xoay, không tự mở ván mới; báo số lượt và thời gian. Chỉ dùng lmm_signal_submit khi tôi yêu cầu gửi kết quả, bằng tài khoản tôi đang đăng nhập. Nếu chưa đăng nhập, yêu cầu tôi đăng nhập thủ công và giữ bản ghi trên thiết bị. Không tạo tài khoản hay thanh toán.',
  },
}
for (const [locale, values] of Object.entries(signalCompetitionKeys)) {
  Object.assign(newKeys[locale], values)
}

const integrationGameLocaleKeys = {
  en: {
    'The SMS provider has insufficient balance. Contact site support; this is not your wallet balance.':
      'The SMS provider has insufficient balance. Contact site support; this is not your wallet balance.',
  },
  fr: {
    'The SMS provider has insufficient balance. Contact site support; this is not your wallet balance.':
      'Le fournisseur SMS manque de fonds. Contactez l’assistance du site ; il ne s’agit pas du solde de votre portefeuille.',
  },
  ja: {
    'The SMS provider has insufficient balance. Contact site support; this is not your wallet balance.':
      'SMS 提供元の残高が不足しています。サイトのサポートにお問い合わせください。お客様のウォレット残高とは別の問題です。',
  },
  ru: {
    'The SMS provider has insufficient balance. Contact site support; this is not your wallet balance.':
      'У поставщика SMS недостаточно средств. Обратитесь в поддержку сайта: это не связано с балансом вашего кошелька.',
  },
  vi: {
    'The SMS provider has insufficient balance. Contact site support; this is not your wallet balance.':
      'Nhà cung cấp SMS không đủ số dư. Hãy liên hệ hỗ trợ trang web; đây không phải số dư ví của bạn.',
  },
  'zh-TW': {
    'The SMS provider has insufficient balance. Contact site support; this is not your wallet balance.':
      '接碼供應商餘額不足，請聯絡網站支援；這不是你的帳戶餘額不足。',
  },
  zh: {
    'The SMS provider has insufficient balance. Contact site support; this is not your wallet balance.':
      '接码供应商余额不足，请联系站点支持；这不是你的账户余额不足。',
  },
}
for (const [locale, values] of Object.entries(integrationGameLocaleKeys)) {
  Object.assign(newKeys[locale], values)
}

for (const [locale, values] of Object.entries(profileShareCopy)) {
  Object.assign(newKeys[locale], values)
}

for (const [locale, values] of Object.entries(acquisitionLinkCopy)) {
  Object.assign(newKeys[locale], values)
}

for (const [locale, values] of Object.entries(aiDirectoryCopy)) {
  Object.assign(newKeys[locale], values)
}

main().catch((error) => {
  console.error(error)
  process.exitCode = 1
})
