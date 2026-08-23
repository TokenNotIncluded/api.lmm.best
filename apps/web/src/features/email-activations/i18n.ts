/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import i18next, { type i18n as I18nInstance } from 'i18next'

export const heroSmsTranslations = {
  en: {
    'Activation details': 'Activation details',
    'Activation refreshed': 'Activation refreshed',
    'Add quota in Wallet, then retry the purchase or reorder action.':
      'Add quota in Wallet, then retry the purchase or reorder action.',
    'All statuses': 'All statuses',
    'Allow authenticated users to purchase HeroSMS temporary email activations from the console.':
      'Allow authenticated users to purchase HeroSMS temporary email activations from the console.',
    'Awaiting code': 'Awaiting code',
    'Buy activation': 'Buy activation',
    'Cancel activation': 'Cancel activation',
    'Cancel pending': 'Cancel pending',
    'Cancellation requested': 'Cancellation requested',
    'Choose a domain': 'Choose a domain',
    'Choose a site, domain, and quantity, then confirm the latest stock and quota charge before purchasing.':
      'Choose a site, domain, and quantity, then confirm the latest stock and quota charge before purchasing.',
    'Choose another domain or refresh to check for replenished inventory.':
      'Choose another domain or refresh to check for replenished inventory.',
    'Clear saved HeroSMS API key': 'Clear saved HeroSMS API key',
    'Clear saved key': 'Clear saved key',
    'Clearing...': 'Clearing...',
    'Code received': 'Code received',
    Configured: 'Configured',
    'Confirm cancel': 'Confirm cancel',
    'Confirm reorder': 'Confirm reorder',
    'Connection test succeeded': 'Connection test succeeded',
    'Current activation': 'Current activation',
    'Email activation purchased': 'Email activation purchased',
    'Enable HeroSMS email activations': 'Enable HeroSMS email activations',
    'Enter target site first': 'Enter target site first',
    'Final quota price': 'Final quota price',
    'Fixed provider settlement currency': 'Fixed provider settlement currency',
    'For security, the browser never reads back the saved secret. Enter a new key only when rotating it.':
      'For security, the browser never reads back the saved secret. Enter a new key only when rotating it.',
    'HeroSMS API key cleared': 'HeroSMS API key cleared',
    'HeroSMS connection test passed': 'HeroSMS connection test passed',
    'HeroSMS Email': 'HeroSMS Email',
    'HeroSMS is temporarily unavailable. Keep this page open and try again shortly.':
      'HeroSMS is temporarily unavailable. Keep this page open and try again shortly.',
    'HeroSMS only returns purchasable email domains after you provide a non-empty target site.':
      'HeroSMS only returns purchasable email domains after you provide a non-empty target site.',
    'HeroSMS purchasing is disabled': 'HeroSMS purchasing is disabled',
    'HeroSMS settings saved': 'HeroSMS settings saved',
    'Insufficient quota': 'Insufficient quota',
    Inventory: 'Inventory',
    'ISO numeric currency code': 'ISO numeric currency code',
    'Keep the latest active email and verification code visible while you complete sign-up or login.':
      'Keep the latest active email and verification code visible while you complete sign-up or login.',
    'Latest provider update': 'Latest provider update',
    'Leave blank to keep the current saved key':
      'Leave blank to keep the current saved key',
    'Loading activation details...': 'Loading activation details...',
    'Loading email activations...': 'Loading email activations...',
    'Loading HeroSMS settings...': 'Loading HeroSMS settings...',
    'Loading products...': 'Loading products...',
    'No active email activation': 'No active email activation',
    'No email activations found': 'No email activations found',
    'No email activations match the current filter.':
      'No email activations match the current filter.',
    'No HeroSMS email products are available for the target site right now.':
      'No HeroSMS email products are available for the target site right now.',
    'Open details': 'Open details',
    'Order #{{id}}': 'Order #{{id}}',
    'Order status': 'Order status',
    'Out of stock': 'Out of stock',
    'Pending email assignment': 'Pending email assignment',
    'Pending purchase': 'Pending purchase',
    'Please wait a moment before sending another request.':
      'Please wait a moment before sending another request.',
    'Price changed': 'Price changed',
    'Provider message': 'Provider message',
    'Provider price': 'Provider price',
    'Purchase activation': 'Purchase activation',
    'Purchase an activation to start receiving temporary email logins here.':
      'Purchase an activation to start receiving temporary email logins here.',
    'Purchase reconciling': 'Purchase reconciling',
    'Purchasing unavailable': 'Purchasing unavailable',
    Quote: 'Quote',
    Reconciling: 'Reconciling',
    Refunded: 'Refunded',
    'Reorder paid activation': 'Reorder paid activation',
    'Reorder submitted': 'Reorder submitted',
    'Replacement API key': 'Replacement API key',
    'Retry to fetch the latest products and activation history.':
      'Retry to fetch the latest products and activation history.',
    'Retry to fetch the latest provider configuration before editing this section.':
      'Retry to fetch the latest provider configuration before editing this section.',
    'Review current and past HeroSMS email activations, filter by status, and reopen order details.':
      'Review current and past HeroSMS email activations, filter by status, and reopen order details.',
    'Review the latest provider status, timestamps, and order identifiers for this activation.':
      'Review the latest provider status, timestamps, and order identifiers for this activation.',
    'Saving is still available, but refresh again if you suspect the server state changed elsewhere.':
      'Saving is still available, but refresh again if you suspect the server state changed elsewhere.',
    'Temporary upstream issue': 'Temporary upstream issue',
    'The latest provider price no longer matches the quote shown here. Refresh products and confirm the new price before retrying.':
      'The latest provider price no longer matches the quote shown here. Refresh products and confirm the new price before retrying.',
    'The provider is still reconciling your last purchase. Refresh this page in a moment before trying again.':
      'The provider is still reconciling your last purchase. Refresh this page in a moment before trying again.',
    'Turn this on only after the API key, multiplier, and test connection all succeed.':
      'Turn this on only after the API key, multiplier, and test connection all succeed.',
    'Unable to load HeroSMS email activations':
      'Unable to load HeroSMS email activations',
    'Unable to load HeroSMS settings': 'Unable to load HeroSMS settings',
    'Unknown status': 'Unknown status',
    'Using last loaded HeroSMS settings': 'Using last loaded HeroSMS settings',
    'Waiting for code': 'Waiting for code',
    'Your next purchased activation will appear here until it completes, expires, or is cancelled.':
      'Your next purchased activation will appear here until it completes, expires, or is cancelled.',
    '{{count}} available': '{{count}} available',
    'The provider is reconciling this purchase. Do not submit another order; this activation will update automatically.':
      'The provider is reconciling this purchase. Do not submit another order; this activation will update automatically.',
    'Purchase submitted for reconciliation':
      'Purchase submitted for reconciliation',
    'Reorder unavailable': 'Reorder unavailable',
    'This activation does not contain a reusable site and domain.':
      'This activation does not contain a reusable site and domain.',
    'No matching HeroSMS inventory is available for this activation.':
      'No matching HeroSMS inventory is available for this activation.',
    'Confirm paid purchase': 'Confirm paid purchase',
    'Purchase {{quantity}} × {{domain}} for {{quota}} quota ({{price}} customer price)?':
      'Purchase {{quantity}} × {{domain}} for {{quota}} quota ({{price}} customer price)?',
    'Confirm purchase': 'Confirm purchase',
    'Cancel this activation to stop waiting for a code. Voluntary cancellation does not guarantee or issue a local quota refund.':
      'Cancel this activation to stop waiting for a code. Voluntary cancellation does not guarantee or issue a local quota refund.',
    'Reorder {{domain}} for {{quota}} quota ({{price}} customer price)? This creates a new paid activation.':
      'Reorder {{domain}} for {{quota}} quota ({{price}} customer price)? This creates a new paid activation.',
    '$1 provider cost → {{price}} customer price':
      '$1 provider cost → {{price}} customer price',
    'API key must contain at least 16 characters':
      'API key must contain at least 16 characters',
    'Use at most 6 decimal places': 'Use at most 6 decimal places',
    'Unable to save HeroSMS settings': 'Unable to save HeroSMS settings',
    'Enter an API key before enabling HeroSMS':
      'Enter an API key before enabling HeroSMS',
    'Unable to clear HeroSMS API key': 'Unable to clear HeroSMS API key',
    'Disable HeroSMS before clearing the saved key':
      'Disable HeroSMS before clearing the saved key',
    'The server can reach HeroSMS with the provided or saved credential.':
      'The server can reach HeroSMS with the provided or saved credential.',
    'Disable HeroSMS first. This permanently removes the server-side secret; purchasing and connection tests will fail until a new key is saved.':
      'Disable HeroSMS first. This permanently removes the server-side secret; purchasing and connection tests will fail until a new key is saved.',
    'Clear key': 'Clear key',
    'Connection test failed': 'Connection test failed',
    'Currency code': 'Currency code',
    History: 'History',
    'Price multiplier': 'Price multiplier',
    'Quota charge': 'Quota charge',
    Reorder: 'Reorder',
    Site: 'Site',
    'Test connection': 'Test connection',
    'Wait for active orders to finish before clearing the saved key':
      'Wait for active orders to finish before clearing the saved key',
    'Active orders are still being reconciled':
      'Active orders are still being reconciled',
    'Keep the HeroSMS API key until active orders finish or are refunded.':
      'Keep the HeroSMS API key until active orders finish or are refunded.',
    'Cancellation reason': 'Cancellation reason',
    'User requested cancellation': 'User requested cancellation',
    'Provider price changed': 'Provider price changed',
    'Provider currency mismatch': 'Provider currency mismatch',
    'Invalid provider response': 'Invalid provider response',
    'Purchase failed': 'Purchase failed',
    'The provider purchase failed and the reserved quota was refunded.':
      'The provider purchase failed and the reserved quota was refunded.',
  },
  zhCN: {
    'Activation details': '接码详情',
    'Activation refreshed': '已刷新接码状态',
    'Add quota in Wallet, then retry the purchase or reorder action.':
      '先在钱包充值额度，再重试购买或重新下单。',
    'All statuses': '全部状态',
    'Allow authenticated users to purchase HeroSMS temporary email activations from the console.':
      '允许已认证用户在控制台购买 HeroSMS 临时邮箱接码。',
    'Awaiting code': '等待验证码',
    'Buy activation': '购买接码',
    'Cancel activation': '取消接码',
    'Cancel pending': '取消处理中',
    'Cancellation requested': '已提交取消请求',
    'Choose a domain': '选择域名',
    'Choose a site, domain, and quantity, then confirm the latest stock and quota charge before purchasing.':
      '选择站点、域名与数量，并在购买前确认最新库存和额度价格。',
    'Choose another domain or refresh to check for replenished inventory.':
      '请选择其他域名或刷新以检查是否补货。',
    'Clear saved HeroSMS API key': '清除已保存的 HeroSMS API 密钥',
    'Clear saved key': '清除已保存的密钥',
    'Clearing...': '清除中...',
    'Code received': '已收到验证码',
    Configured: '已配置',
    'Confirm cancel': '确认取消',
    'Confirm reorder': '确认重新下单',
    'Connection test succeeded': '连接测试成功',
    'Current activation': '当前接码',
    'Email activation purchased': '邮箱接码已购买',
    'Enable HeroSMS email activations': '启用 HeroSMS 邮箱接码',
    'Enter target site first': '先输入目标站点',
    'Final quota price': '最终额度价格',
    'Fixed provider settlement currency': '固定服务商结算货币',
    'For security, the browser never reads back the saved secret. Enter a new key only when rotating it.':
      '出于安全考虑，浏览器不会读回已保存的密钥。只有在轮换时才输入新密钥。',
    'HeroSMS API key cleared': '已清除 HeroSMS API 密钥',
    'HeroSMS connection test passed': 'HeroSMS 连接测试通过',
    'HeroSMS Email': 'HeroSMS 邮箱接码',
    'HeroSMS is temporarily unavailable. Keep this page open and try again shortly.':
      'HeroSMS 暂时不可用，请保持页面打开并稍后重试。',
    'HeroSMS only returns purchasable email domains after you provide a non-empty target site.':
      '只有在提供非空目标站点后，HeroSMS 才会返回可购买的邮箱域名。',
    'HeroSMS purchasing is disabled': 'HeroSMS 购买已禁用',
    'HeroSMS settings saved': '已保存 HeroSMS 设置',
    'Insufficient quota': '额度不足',
    Inventory: '库存',
    'ISO numeric currency code': 'ISO 数字货币代码',
    'Keep the latest active email and verification code visible while you complete sign-up or login.':
      '在完成注册或登录时，让最新的激活邮箱和验证码保持可见。',
    'Latest provider update': '最新服务商更新',
    'Leave blank to keep the current saved key': '留空则保留当前已保存的密钥',
    'Loading activation details...': '正在加载接码详情...',
    'Loading email activations...': '正在加载邮箱接码...',
    'Loading HeroSMS settings...': '正在加载 HeroSMS 设置...',
    'Loading products...': '正在加载产品...',
    'No active email activation': '没有进行中的邮箱接码',
    'No email activations found': '未找到邮箱接码记录',
    'No email activations match the current filter.':
      '当前筛选条件下没有邮箱接码记录。',
    'No HeroSMS email products are available for the target site right now.':
      '当前目标站点暂时没有可购买的 HeroSMS 邮箱产品。',
    'Open details': '打开详情',
    'Order #{{id}}': '订单 #{{id}}',
    'Order status': '订单状态',
    'Out of stock': '缺货',
    'Pending email assignment': '待分配邮箱',
    'Pending purchase': '待购买',
    'Please wait a moment before sending another request.':
      '请稍等片刻后再发送下一次请求。',
    'Price changed': '价格已变更',
    'Provider message': '服务商消息',
    'Provider price': '服务商价格',
    'Purchase activation': '购买接码',
    'Purchase an activation to start receiving temporary email logins here.':
      '购买一个接码后，临时邮箱登录记录会显示在这里。',
    'Purchase reconciling': '正在核对购买结果',
    'Purchasing unavailable': '暂不可购买',
    Quote: '报价',
    Reconciling: '对账中',
    Refunded: '已退款',
    'Reorder paid activation': '重新下单已支付接码',
    'Reorder submitted': '已提交重新下单',
    'Replacement API key': '替换 API 密钥',
    'Retry to fetch the latest products and activation history.':
      '重试获取最新产品与接码历史。',
    'Retry to fetch the latest provider configuration before editing this section.':
      '编辑此部分前，请重试获取最新服务商配置。',
    'Review current and past HeroSMS email activations, filter by status, and reopen order details.':
      '查看当前与历史 HeroSMS 邮箱接码，按状态筛选，并重新打开订单详情。',
    'Review the latest provider status, timestamps, and order identifiers for this activation.':
      '查看此接码的最新服务商状态、时间戳与订单标识。',
    'Saving is still available, but refresh again if you suspect the server state changed elsewhere.':
      '仍可保存，但如果你怀疑服务器状态已在其他地方变更，请再次刷新。',
    'Temporary upstream issue': '上游临时异常',
    'The latest provider price no longer matches the quote shown here. Refresh products and confirm the new price before retrying.':
      '最新服务商价格已与当前报价不一致，请刷新产品并确认新价格后重试。',
    'The provider is still reconciling your last purchase. Refresh this page in a moment before trying again.':
      '服务商仍在核对你上一次购买，请稍后刷新页面后再试。',
    'Turn this on only after the API key, multiplier, and test connection all succeed.':
      '仅在 API 密钥、倍率和连接测试都成功后再启用。',
    'Unable to load HeroSMS email activations': '无法加载 HeroSMS 邮箱接码',
    'Unable to load HeroSMS settings': '无法加载 HeroSMS 设置',
    'Unknown status': '未知状态',
    'Using last loaded HeroSMS settings': '正在使用上次加载的 HeroSMS 设置',
    'Waiting for code': '等待验证码',
    'Your next purchased activation will appear here until it completes, expires, or is cancelled.':
      '你下一次购买的接码会显示在这里，直到完成、过期或被取消。',
    '{{count}} available': '可用 {{count}} 个',
    'The provider is reconciling this purchase. Do not submit another order; this activation will update automatically.':
      '服务商正在核对本次购买结果。请勿重复下单；此接码记录会自动更新。',
    'Purchase submitted for reconciliation': '购买已进入对账',
    'Reorder unavailable': '无法重购',
    'This activation does not contain a reusable site and domain.':
      '此接码记录缺少可用于重购的站点和域名。',
    'No matching HeroSMS inventory is available for this activation.':
      'HeroSMS 当前没有与此接码记录匹配的库存。',
    'Confirm paid purchase': '确认付费购买',
    'Purchase {{quantity}} × {{domain}} for {{quota}} quota ({{price}} customer price)?':
      '确认购买 {{quantity}} 个 {{domain}}，扣除 {{quota}} 额度（用户价格 {{price}}）？',
    'Confirm purchase': '确认购买',
    'Cancel this activation to stop waiting for a code. Voluntary cancellation does not guarantee or issue a local quota refund.':
      '取消后将停止等待验证码。主动取消不保证退款，也不会自动退还本站额度。',
    'Reorder {{domain}} for {{quota}} quota ({{price}} customer price)? This creates a new paid activation.':
      '确认以 {{quota}} 额度（用户价格 {{price}}）重购 {{domain}}？这会创建一笔新的付费接码订单。',
    '$1 provider cost → {{price}} customer price':
      '服务商成本 $1 → 用户价格 {{price}}',
    'API key must contain at least 16 characters': 'API Key 至少需要 16 个字符',
    'Use at most 6 decimal places': '最多保留 6 位小数',
    'Unable to save HeroSMS settings': '无法保存 HeroSMS 设置',
    'Enter an API key before enabling HeroSMS':
      '启用 HeroSMS 前请先填写 API Key',
    'Unable to clear HeroSMS API key': '无法清除 HeroSMS API Key',
    'Disable HeroSMS before clearing the saved key':
      '请先停用 HeroSMS，再清除已保存的密钥',
    'The server can reach HeroSMS with the provided or saved credential.':
      '服务器可以使用本次填写或已保存的凭据连接 HeroSMS。',
    'Disable HeroSMS first. This permanently removes the server-side secret; purchasing and connection tests will fail until a new key is saved.':
      '请先停用 HeroSMS。此操作会永久删除服务端密钥；保存新密钥之前，购买和连接测试均不可用。',
    'Clear key': '清除密钥',
    'Connection test failed': '连接测试失败',
    'Currency code': '货币代码',
    History: '历史记录',
    'Price multiplier': '价格倍率',
    'Quota charge': '扣除额度',
    Reorder: '重新购买',
    Site: '目标站点',
    'Test connection': '测试连接',
    'Wait for active orders to finish before clearing the saved key':
      '请等待进行中的订单完成后再清除已保存的密钥',
    'Active orders are still being reconciled': '仍有订单正在对账',
    'Keep the HeroSMS API key until active orders finish or are refunded.':
      '请保留 HeroSMS API Key，直到进行中的订单完成或退款。',
    'Cancellation reason': '取消原因',
    'User requested cancellation': '用户主动取消',
    'Provider price changed': '服务商价格已变化',
    'Provider currency mismatch': '服务商币种不匹配',
    'Invalid provider response': '服务商返回内容无效',
    'Purchase failed': '购买失败',
    'The provider purchase failed and the reserved quota was refunded.':
      '服务商购买失败，已退还预留额度。',
  },
  zhTW: {
    'Activation details': '接碼詳情',
    'Activation refreshed': '已重新整理接碼狀態',
    'Add quota in Wallet, then retry the purchase or reorder action.':
      '先在錢包儲值額度，再重試購買或重新下單。',
    'All statuses': '全部狀態',
    'Allow authenticated users to purchase HeroSMS temporary email activations from the console.':
      '允許已驗證使用者在主控台購買 HeroSMS 臨時郵箱接碼。',
    'Awaiting code': '等待驗證碼',
    'Buy activation': '購買接碼',
    'Cancel activation': '取消接碼',
    'Cancel pending': '取消處理中',
    'Cancellation requested': '已提交取消請求',
    'Choose a domain': '選擇網域',
    'Choose a site, domain, and quantity, then confirm the latest stock and quota charge before purchasing.':
      '選擇站點、網域與數量，並在購買前確認最新庫存與額度價格。',
    'Choose another domain or refresh to check for replenished inventory.':
      '請選擇其他網域，或重新整理以檢查是否已補貨。',
    'Clear saved HeroSMS API key': '清除已儲存的 HeroSMS API 金鑰',
    'Clear saved key': '清除已儲存的金鑰',
    'Clearing...': '清除中...',
    'Code received': '已收到驗證碼',
    Configured: '已設定',
    'Confirm cancel': '確認取消',
    'Confirm reorder': '確認重新下單',
    'Connection test succeeded': '連線測試成功',
    'Current activation': '目前接碼',
    'Email activation purchased': '郵箱接碼已購買',
    'Enable HeroSMS email activations': '啟用 HeroSMS 郵箱接碼',
    'Enter target site first': '請先輸入目標站點',
    'Final quota price': '最終額度價格',
    'Fixed provider settlement currency': '固定服務商結算貨幣',
    'For security, the browser never reads back the saved secret. Enter a new key only when rotating it.':
      '基於安全考量，瀏覽器不會讀回已儲存的密鑰。只有在輪換時才輸入新金鑰。',
    'HeroSMS API key cleared': '已清除 HeroSMS API 金鑰',
    'HeroSMS connection test passed': 'HeroSMS 連線測試通過',
    'HeroSMS Email': 'HeroSMS 郵箱接碼',
    'HeroSMS is temporarily unavailable. Keep this page open and try again shortly.':
      'HeroSMS 暫時不可用，請保持頁面開啟並稍後重試。',
    'HeroSMS only returns purchasable email domains after you provide a non-empty target site.':
      '只有在提供非空目標站點後，HeroSMS 才會回傳可購買的郵箱網域。',
    'HeroSMS purchasing is disabled': 'HeroSMS 購買已停用',
    'HeroSMS settings saved': '已儲存 HeroSMS 設定',
    'Insufficient quota': '額度不足',
    Inventory: '庫存',
    'ISO numeric currency code': 'ISO 數字貨幣代碼',
    'Keep the latest active email and verification code visible while you complete sign-up or login.':
      '在完成註冊或登入時，讓最新的啟用郵箱與驗證碼保持可見。',
    'Latest provider update': '最新服務商更新',
    'Leave blank to keep the current saved key': '留空以保留目前已儲存的金鑰',
    'Loading activation details...': '正在載入接碼詳情...',
    'Loading email activations...': '正在載入郵箱接碼...',
    'Loading HeroSMS settings...': '正在載入 HeroSMS 設定...',
    'Loading products...': '正在載入產品...',
    'No active email activation': '沒有進行中的郵箱接碼',
    'No email activations found': '找不到郵箱接碼紀錄',
    'No email activations match the current filter.':
      '目前篩選條件下沒有郵箱接碼紀錄。',
    'No HeroSMS email products are available for the target site right now.':
      '目前目標站點暫時沒有可購買的 HeroSMS 郵箱產品。',
    'Open details': '開啟詳情',
    'Order #{{id}}': '訂單 #{{id}}',
    'Order status': '訂單狀態',
    'Out of stock': '缺貨',
    'Pending email assignment': '待分配郵箱',
    'Pending purchase': '待購買',
    'Please wait a moment before sending another request.':
      '請稍候片刻後再送出下一次請求。',
    'Price changed': '價格已變更',
    'Provider message': '服務商訊息',
    'Provider price': '服務商價格',
    'Purchase activation': '購買接碼',
    'Purchase an activation to start receiving temporary email logins here.':
      '購買一個接碼後，臨時郵箱登入紀錄會顯示在這裡。',
    'Purchase reconciling': '正在核對購買結果',
    'Purchasing unavailable': '暫時無法購買',
    Quote: '報價',
    Reconciling: '對帳中',
    Refunded: '已退款',
    'Reorder paid activation': '重新下單已支付接碼',
    'Reorder submitted': '已提交重新下單',
    'Replacement API key': '替換 API 金鑰',
    'Retry to fetch the latest products and activation history.':
      '請重試取得最新產品與接碼歷史。',
    'Retry to fetch the latest provider configuration before editing this section.':
      '編輯此區塊前，請重試取得最新服務商設定。',
    'Review current and past HeroSMS email activations, filter by status, and reopen order details.':
      '查看目前與歷史 HeroSMS 郵箱接碼，依狀態篩選，並重新開啟訂單詳情。',
    'Review the latest provider status, timestamps, and order identifiers for this activation.':
      '查看此接碼的最新服務商狀態、時間戳與訂單識別。',
    'Saving is still available, but refresh again if you suspect the server state changed elsewhere.':
      '仍可儲存，但如果你懷疑伺服器狀態已在其他地方變更，請再次重新整理。',
    'Temporary upstream issue': '上游暫時異常',
    'The latest provider price no longer matches the quote shown here. Refresh products and confirm the new price before retrying.':
      '最新服務商價格已與目前報價不一致，請重新整理產品並確認新價格後再試。',
    'The provider is still reconciling your last purchase. Refresh this page in a moment before trying again.':
      '服務商仍在核對你上一次購買，請稍後重新整理頁面後再試。',
    'Turn this on only after the API key, multiplier, and test connection all succeed.':
      '只有在 API 金鑰、倍率與連線測試都成功後才啟用。',
    'Unable to load HeroSMS email activations': '無法載入 HeroSMS 郵箱接碼',
    'Unable to load HeroSMS settings': '無法載入 HeroSMS 設定',
    'Unknown status': '未知狀態',
    'Using last loaded HeroSMS settings': '正在使用上次載入的 HeroSMS 設定',
    'Waiting for code': '等待驗證碼',
    'Your next purchased activation will appear here until it completes, expires, or is cancelled.':
      '你下一次購買的接碼會顯示在這裡，直到完成、過期或被取消。',
    '{{count}} available': '可用 {{count}} 個',
    'The provider is reconciling this purchase. Do not submit another order; this activation will update automatically.':
      '服務商正在核對本次購買結果。請勿重複下單；此接碼記錄會自動更新。',
    'Purchase submitted for reconciliation': '購買已進入對帳',
    'Reorder unavailable': '無法重新購買',
    'This activation does not contain a reusable site and domain.':
      '此接碼記錄缺少可用於重新購買的站點與網域。',
    'No matching HeroSMS inventory is available for this activation.':
      'HeroSMS 目前沒有與此接碼記錄相符的庫存。',
    'Confirm paid purchase': '確認付費購買',
    'Purchase {{quantity}} × {{domain}} for {{quota}} quota ({{price}} customer price)?':
      '確認購買 {{quantity}} 個 {{domain}}，扣除 {{quota}} 額度（使用者價格 {{price}}）？',
    'Confirm purchase': '確認購買',
    'Cancel this activation to stop waiting for a code. Voluntary cancellation does not guarantee or issue a local quota refund.':
      '取消後將停止等待驗證碼。主動取消不保證退款，也不會自動退還本站額度。',
    'Reorder {{domain}} for {{quota}} quota ({{price}} customer price)? This creates a new paid activation.':
      '確認以 {{quota}} 額度（使用者價格 {{price}}）重新購買 {{domain}}？這會建立一筆新的付費接碼訂單。',
    '$1 provider cost → {{price}} customer price':
      '服務商成本 $1 → 使用者價格 {{price}}',
    'API key must contain at least 16 characters': 'API Key 至少需要 16 個字元',
    'Use at most 6 decimal places': '最多保留 6 位小數',
    'Unable to save HeroSMS settings': '無法儲存 HeroSMS 設定',
    'Enter an API key before enabling HeroSMS':
      '啟用 HeroSMS 前請先填寫 API Key',
    'Unable to clear HeroSMS API key': '無法清除 HeroSMS API Key',
    'Disable HeroSMS before clearing the saved key':
      '請先停用 HeroSMS，再清除已儲存的金鑰',
    'The server can reach HeroSMS with the provided or saved credential.':
      '伺服器可以使用本次填寫或已儲存的憑證連線至 HeroSMS。',
    'Disable HeroSMS first. This permanently removes the server-side secret; purchasing and connection tests will fail until a new key is saved.':
      '請先停用 HeroSMS。此操作會永久刪除伺服器端金鑰；儲存新金鑰之前，購買與連線測試均不可用。',
    'Clear key': '清除金鑰',
    'Connection test failed': '連線測試失敗',
    'Currency code': '貨幣代碼',
    History: '歷史記錄',
    'Price multiplier': '價格倍率',
    'Quota charge': '扣除額度',
    Reorder: '重新購買',
    Site: '目標站點',
    'Test connection': '測試連線',
    'Wait for active orders to finish before clearing the saved key':
      '請等待進行中的訂單完成後再清除已儲存的金鑰',
    'Active orders are still being reconciled': '仍有訂單正在對帳',
    'Keep the HeroSMS API key until active orders finish or are refunded.':
      '請保留 HeroSMS API Key，直到進行中的訂單完成或退款。',
    'Cancellation reason': '取消原因',
    'User requested cancellation': '使用者主動取消',
    'Provider price changed': '服務商價格已變更',
    'Provider currency mismatch': '服務商幣別不符',
    'Invalid provider response': '服務商回傳內容無效',
    'Purchase failed': '購買失敗',
    'The provider purchase failed and the reserved quota was refunded.':
      '服務商購買失敗，已退還預留額度。',
  },
  fr: {
    'Activation details': 'Détails de l’activation',
    'Activation refreshed': 'Activation actualisée',
    'Add quota in Wallet, then retry the purchase or reorder action.':
      'Ajoutez du quota dans le Portefeuille, puis réessayez l’achat ou la nouvelle commande.',
    'All statuses': 'Tous les statuts',
    'Allow authenticated users to purchase HeroSMS temporary email activations from the console.':
      'Autoriser les utilisateurs authentifiés à acheter des activations e-mail temporaires HeroSMS depuis la console.',
    'Awaiting code': 'En attente du code',
    'Buy activation': 'Acheter l’activation',
    'Cancel activation': 'Annuler l’activation',
    'Cancel pending': 'Annulation en attente',
    'Cancellation requested': 'Annulation demandée',
    'Choose a domain': 'Choisir un domaine',
    'Choose a site, domain, and quantity, then confirm the latest stock and quota charge before purchasing.':
      'Choisissez un site, un domaine et une quantité, puis confirmez le stock et le coût en quota avant l’achat.',
    'Choose another domain or refresh to check for replenished inventory.':
      'Choisissez un autre domaine ou actualisez pour vérifier le réassort.',
    'Clear saved HeroSMS API key': 'Effacer la clé API HeroSMS enregistrée',
    'Clear saved key': 'Effacer la clé enregistrée',
    'Clearing...': 'Effacement...',
    'Code received': 'Code reçu',
    Configured: 'Configuré',
    'Confirm cancel': 'Confirmer l’annulation',
    'Confirm reorder': 'Confirmer la nouvelle commande',
    'Connection test succeeded': 'Test de connexion réussi',
    'Current activation': 'Activation en cours',
    'Email activation purchased': 'Activation e-mail achetée',
    'Enable HeroSMS email activations':
      'Activer les activations e-mail HeroSMS',
    'Enter target site first': 'Saisissez d’abord le site cible',
    'Final quota price': 'Prix final en quota',
    'Fixed provider settlement currency':
      'Devise fixe de règlement du fournisseur',
    'For security, the browser never reads back the saved secret. Enter a new key only when rotating it.':
      'Pour des raisons de sécurité, le navigateur ne relit jamais le secret enregistré. Saisissez une nouvelle clé uniquement lors de sa rotation.',
    'HeroSMS API key cleared': 'Clé API HeroSMS effacée',
    'HeroSMS connection test passed': 'Test de connexion HeroSMS réussi',
    'HeroSMS Email': 'E-mail HeroSMS',
    'HeroSMS is temporarily unavailable. Keep this page open and try again shortly.':
      'HeroSMS est temporairement indisponible. Laissez cette page ouverte et réessayez sous peu.',
    'HeroSMS only returns purchasable email domains after you provide a non-empty target site.':
      'HeroSMS ne renvoie des domaines e-mail achetables qu’après avoir fourni un site cible non vide.',
    'HeroSMS purchasing is disabled': 'L’achat HeroSMS est désactivé',
    'HeroSMS settings saved': 'Paramètres HeroSMS enregistrés',
    'Insufficient quota': 'Quota insuffisant',
    Inventory: 'Stock',
    'ISO numeric currency code': 'Code numérique ISO de la devise',
    'Keep the latest active email and verification code visible while you complete sign-up or login.':
      'Gardez visibles le dernier e-mail actif et le code de vérification pendant l’inscription ou la connexion.',
    'Latest provider update': 'Dernière mise à jour du fournisseur',
    'Leave blank to keep the current saved key':
      'Laissez vide pour conserver la clé enregistrée actuelle',
    'Loading activation details...':
      'Chargement des détails de l’activation...',
    'Loading email activations...': 'Chargement des activations e-mail...',
    'Loading HeroSMS settings...': 'Chargement des paramètres HeroSMS...',
    'Loading products...': 'Chargement des produits...',
    'No active email activation': 'Aucune activation e-mail active',
    'No email activations found': 'Aucune activation e-mail trouvée',
    'No email activations match the current filter.':
      'Aucune activation e-mail ne correspond au filtre actuel.',
    'No HeroSMS email products are available for the target site right now.':
      'Aucun produit e-mail HeroSMS n’est disponible pour le site cible pour le moment.',
    'Open details': 'Ouvrir les détails',
    'Order #{{id}}': 'Commande n°{{id}}',
    'Order status': 'Statut de la commande',
    'Out of stock': 'Rupture de stock',
    'Pending email assignment': 'Affectation d’e-mail en attente',
    'Pending purchase': 'Achat en attente',
    'Please wait a moment before sending another request.':
      'Veuillez patienter un instant avant d’envoyer une autre demande.',
    'Price changed': 'Prix modifié',
    'Provider message': 'Message du fournisseur',
    'Provider price': 'Prix du fournisseur',
    'Purchase activation': 'Acheter une activation',
    'Purchase an activation to start receiving temporary email logins here.':
      'Achetez une activation pour commencer à recevoir ici les connexions e-mail temporaires.',
    'Purchase reconciling': 'Achat en cours de rapprochement',
    'Purchasing unavailable': 'Achat indisponible',
    Quote: 'Devis',
    Reconciling: 'En rapprochement',
    Refunded: 'Remboursé',
    'Reorder paid activation': 'Recommander l’activation payée',
    'Reorder submitted': 'Nouvelle commande envoyée',
    'Replacement API key': 'Clé API de remplacement',
    'Retry to fetch the latest products and activation history.':
      'Réessayez pour récupérer les derniers produits et l’historique des activations.',
    'Retry to fetch the latest provider configuration before editing this section.':
      'Réessayez de récupérer la dernière configuration fournisseur avant de modifier cette section.',
    'Review current and past HeroSMS email activations, filter by status, and reopen order details.':
      'Consultez les activations e-mail HeroSMS actuelles et passées, filtrez par statut et rouvrez les détails de commande.',
    'Review the latest provider status, timestamps, and order identifiers for this activation.':
      'Consultez le dernier statut fournisseur, les horodatages et les identifiants de commande pour cette activation.',
    'Saving is still available, but refresh again if you suspect the server state changed elsewhere.':
      'L’enregistrement reste possible, mais actualisez de nouveau si vous pensez que l’état du serveur a changé ailleurs.',
    'Temporary upstream issue': 'Problème temporaire en amont',
    'The latest provider price no longer matches the quote shown here. Refresh products and confirm the new price before retrying.':
      'Le dernier prix du fournisseur ne correspond plus au devis affiché ici. Actualisez les produits et confirmez le nouveau prix avant de réessayer.',
    'The provider is still reconciling your last purchase. Refresh this page in a moment before trying again.':
      'Le fournisseur rapproche encore votre dernier achat. Actualisez cette page dans un instant avant de réessayer.',
    'Turn this on only after the API key, multiplier, and test connection all succeed.':
      'Activez ceci seulement après la réussite de la clé API, du multiplicateur et du test de connexion.',
    'Unable to load HeroSMS email activations':
      'Impossible de charger les activations e-mail HeroSMS',
    'Unable to load HeroSMS settings':
      'Impossible de charger les paramètres HeroSMS',
    'Unknown status': 'Statut inconnu',
    'Using last loaded HeroSMS settings':
      'Utilisation des derniers paramètres HeroSMS chargés',
    'Waiting for code': 'En attente du code',
    'Your next purchased activation will appear here until it completes, expires, or is cancelled.':
      'Votre prochaine activation achetée apparaîtra ici jusqu’à sa fin, son expiration ou son annulation.',
    '{{count}} available': '{{count}} disponibles',
    'The provider is reconciling this purchase. Do not submit another order; this activation will update automatically.':
      'Le fournisseur rapproche cet achat. Ne passez pas une autre commande ; cette activation sera mise à jour automatiquement.',
    'Purchase submitted for reconciliation': 'Achat envoyé au rapprochement',
    'Reorder unavailable': 'Nouvelle commande indisponible',
    'This activation does not contain a reusable site and domain.':
      'Cette activation ne contient pas de site ni de domaine réutilisable.',
    'No matching HeroSMS inventory is available for this activation.':
      'Aucun stock HeroSMS correspondant à cette activation n’est disponible.',
    'Confirm paid purchase': 'Confirmer l’achat payant',
    'Purchase {{quantity}} × {{domain}} for {{quota}} quota ({{price}} customer price)?':
      'Acheter {{quantity}} × {{domain}} pour {{quota}} de quota (prix client : {{price}}) ?',
    'Confirm purchase': 'Confirmer l’achat',
    'Cancel this activation to stop waiting for a code. Voluntary cancellation does not guarantee or issue a local quota refund.':
      'Annulez cette activation pour ne plus attendre de code. Une annulation volontaire ne garantit ni ne déclenche un remboursement du quota local.',
    'Reorder {{domain}} for {{quota}} quota ({{price}} customer price)? This creates a new paid activation.':
      'Commander à nouveau {{domain}} pour {{quota}} de quota (prix client : {{price}}) ? Cela crée une nouvelle activation payante.',
    '$1 provider cost → {{price}} customer price':
      'Coût fournisseur de 1 $ → prix client {{price}}',
    'API key must contain at least 16 characters':
      'La clé API doit contenir au moins 16 caractères',
    'Use at most 6 decimal places': 'Utilisez au maximum 6 décimales',
    'Unable to save HeroSMS settings':
      'Impossible d’enregistrer les paramètres HeroSMS',
    'Enter an API key before enabling HeroSMS':
      'Saisissez une clé API avant d’activer HeroSMS',
    'Unable to clear HeroSMS API key':
      'Impossible d’effacer la clé API HeroSMS',
    'Disable HeroSMS before clearing the saved key':
      'Désactivez HeroSMS avant d’effacer la clé enregistrée',
    'The server can reach HeroSMS with the provided or saved credential.':
      'Le serveur peut joindre HeroSMS avec l’identifiant fourni ou enregistré.',
    'Disable HeroSMS first. This permanently removes the server-side secret; purchasing and connection tests will fail until a new key is saved.':
      'Désactivez d’abord HeroSMS. Cette action supprime définitivement le secret côté serveur ; les achats et les tests de connexion échoueront jusqu’à l’enregistrement d’une nouvelle clé.',
    'Clear key': 'Effacer la clé',
    'Connection test failed': 'Échec du test de connexion',
    'Currency code': 'Code devise',
    History: 'Historique',
    'Price multiplier': 'Multiplicateur de prix',
    'Quota charge': 'Quota débité',
    Reorder: 'Commander à nouveau',
    Site: 'Site cible',
    'Test connection': 'Tester la connexion',
    'Wait for active orders to finish before clearing the saved key':
      'Attendez la fin des commandes actives avant d’effacer la clé enregistrée',
    'Active orders are still being reconciled':
      'Des commandes actives sont encore en cours de rapprochement',
    'Keep the HeroSMS API key until active orders finish or are refunded.':
      'Conservez la clé API HeroSMS jusqu’à la fin ou au remboursement des commandes actives.',
    'Cancellation reason': 'Motif d’annulation',
    'User requested cancellation': 'Annulation demandée par l’utilisateur',
    'Provider price changed': 'Le prix du fournisseur a changé',
    'Provider currency mismatch': 'La devise du fournisseur ne correspond pas',
    'Invalid provider response': 'Réponse du fournisseur non valide',
    'Purchase failed': 'Échec de l’achat',
    'The provider purchase failed and the reserved quota was refunded.':
      'L’achat auprès du fournisseur a échoué et le quota réservé a été remboursé.',
  },
  ja: {
    'Activation details': '認証受信の詳細',
    'Activation refreshed': '認証受信を更新しました',
    'Add quota in Wallet, then retry the purchase or reorder action.':
      'Wallet でクォータを追加してから、購入または再注文をやり直してください。',
    'All statuses': 'すべての状態',
    'Allow authenticated users to purchase HeroSMS temporary email activations from the console.':
      '認証済みユーザーがコンソールから HeroSMS の一時メール認証受信を購入できるようにします。',
    'Awaiting code': 'コード待ち',
    'Buy activation': '認証受信を購入',
    'Cancel activation': '認証受信をキャンセル',
    'Cancel pending': 'キャンセル処理中',
    'Cancellation requested': 'キャンセルを申請しました',
    'Choose a domain': 'ドメインを選択',
    'Choose a site, domain, and quantity, then confirm the latest stock and quota charge before purchasing.':
      'サイト、ドメイン、数量を選択し、購入前に最新の在庫とクォータ料金を確認してください。',
    'Choose another domain or refresh to check for replenished inventory.':
      '別のドメインを選ぶか、更新して補充された在庫を確認してください。',
    'Clear saved HeroSMS API key': '保存済みの HeroSMS API キーを消去',
    'Clear saved key': '保存済みキーを消去',
    'Clearing...': '消去中...',
    'Code received': 'コード受信済み',
    Configured: '設定済み',
    'Confirm cancel': 'キャンセルを確認',
    'Confirm reorder': '再注文を確認',
    'Connection test succeeded': '接続テストに成功しました',
    'Current activation': '現在の認証受信',
    'Email activation purchased': 'メール認証受信を購入しました',
    'Enable HeroSMS email activations': 'HeroSMS メール認証受信を有効化',
    'Enter target site first': '先に対象サイトを入力してください',
    'Final quota price': '最終クォータ価格',
    'Fixed provider settlement currency': '固定のプロバイダー決済通貨',
    'For security, the browser never reads back the saved secret. Enter a new key only when rotating it.':
      'セキュリティのため、ブラウザーは保存済みシークレットを読み戻しません。新しいキーはローテーション時のみ入力してください。',
    'HeroSMS API key cleared': 'HeroSMS API キーを消去しました',
    'HeroSMS connection test passed': 'HeroSMS 接続テストに成功しました',
    'HeroSMS Email': 'HeroSMS メール認証受信',
    'HeroSMS is temporarily unavailable. Keep this page open and try again shortly.':
      'HeroSMS は一時的に利用できません。このページを開いたまま、しばらくしてから再試行してください。',
    'HeroSMS only returns purchasable email domains after you provide a non-empty target site.':
      'HeroSMS は空でない対象サイトを指定した後にのみ購入可能なメールドメインを返します。',
    'HeroSMS purchasing is disabled': 'HeroSMS の購入は無効です',
    'HeroSMS settings saved': 'HeroSMS 設定を保存しました',
    'Insufficient quota': 'クォータ不足',
    Inventory: '在庫',
    'ISO numeric currency code': 'ISO 数値通貨コード',
    'Keep the latest active email and verification code visible while you complete sign-up or login.':
      '登録またはログインを完了する間、最新の有効なメールと認証コードを見える状態に保ちます。',
    'Latest provider update': '最新のプロバイダー更新',
    'Leave blank to keep the current saved key':
      '空欄のままにすると現在の保存済みキーを保持します',
    'Loading activation details...': '認証受信の詳細を読み込み中...',
    'Loading email activations...': 'メール認証受信を読み込み中...',
    'Loading HeroSMS settings...': 'HeroSMS 設定を読み込み中...',
    'Loading products...': '商品を読み込み中...',
    'No active email activation': '有効なメール認証受信はありません',
    'No email activations found': 'メール認証受信が見つかりません',
    'No email activations match the current filter.':
      '現在のフィルターに一致するメール認証受信はありません。',
    'No HeroSMS email products are available for the target site right now.':
      '現在、対象サイトで利用可能な HeroSMS メール商品はありません。',
    'Open details': '詳細を開く',
    'Order #{{id}}': '注文 #{{id}}',
    'Order status': '注文状態',
    'Out of stock': '在庫切れ',
    'Pending email assignment': 'メール割り当て待ち',
    'Pending purchase': '購入待ち',
    'Please wait a moment before sending another request.':
      '次のリクエストを送る前に少しお待ちください。',
    'Price changed': '価格が変更されました',
    'Provider message': 'プロバイダーメッセージ',
    'Provider price': 'プロバイダー価格',
    'Purchase activation': '認証受信を購入',
    'Purchase an activation to start receiving temporary email logins here.':
      '認証受信を購入すると、一時メールのログイン情報がここに表示されます。',
    'Purchase reconciling': '購入結果を照合中',
    'Purchasing unavailable': '購入できません',
    Quote: '見積もり',
    Reconciling: '照合中',
    Refunded: '返金済み',
    'Reorder paid activation': '支払い済み認証受信を再注文',
    'Reorder submitted': '再注文を送信しました',
    'Replacement API key': '置き換え API キー',
    'Retry to fetch the latest products and activation history.':
      '最新の商品と認証履歴の取得を再試行してください。',
    'Retry to fetch the latest provider configuration before editing this section.':
      'このセクションを編集する前に、最新のプロバイダー設定の取得を再試行してください。',
    'Review current and past HeroSMS email activations, filter by status, and reopen order details.':
      '現在と過去の HeroSMS メール認証受信を確認し、状態で絞り込み、注文詳細を再表示できます。',
    'Review the latest provider status, timestamps, and order identifiers for this activation.':
      'この認証受信の最新プロバイダー状態、タイムスタンプ、注文識別子を確認します。',
    'Saving is still available, but refresh again if you suspect the server state changed elsewhere.':
      '保存は可能ですが、サーバー状態が他で変更された疑いがある場合はもう一度更新してください。',
    'Temporary upstream issue': '上流側の一時的な問題',
    'The latest provider price no longer matches the quote shown here. Refresh products and confirm the new price before retrying.':
      '最新のプロバイダー価格がここに表示されている見積もりと一致しません。商品を更新して新しい価格を確認してから再試行してください。',
    'The provider is still reconciling your last purchase. Refresh this page in a moment before trying again.':
      'プロバイダーが前回の購入をまだ照合しています。少し待ってからこのページを更新して再試行してください。',
    'Turn this on only after the API key, multiplier, and test connection all succeed.':
      'API キー、倍率、接続テストがすべて成功した後にのみ有効にしてください。',
    'Unable to load HeroSMS email activations':
      'HeroSMS メール認証受信を読み込めません',
    'Unable to load HeroSMS settings': 'HeroSMS 設定を読み込めません',
    'Unknown status': '不明な状態',
    'Using last loaded HeroSMS settings':
      '最後に読み込んだ HeroSMS 設定を使用中',
    'Waiting for code': 'コード待ち',
    'Your next purchased activation will appear here until it completes, expires, or is cancelled.':
      '次に購入した認証受信は、完了・期限切れ・キャンセルになるまでここに表示されます。',
    '{{count}} available': '利用可能: {{count}} 件',
    'The provider is reconciling this purchase. Do not submit another order; this activation will update automatically.':
      'プロバイダーが購入結果を照合しています。重複注文は行わないでください。このアクティベーションは自動更新されます。',
    'Purchase submitted for reconciliation': '購入を照合処理に送信しました',
    'Reorder unavailable': '再注文できません',
    'This activation does not contain a reusable site and domain.':
      'このアクティベーションには再注文に使えるサイトとドメインがありません。',
    'No matching HeroSMS inventory is available for this activation.':
      'このアクティベーションに一致する HeroSMS の在庫がありません。',
    'Confirm paid purchase': '有料購入を確認',
    'Purchase {{quantity}} × {{domain}} for {{quota}} quota ({{price}} customer price)?':
      '{{domain}} を {{quantity}} 件、{{quota}} クォータ（顧客価格 {{price}}）で購入しますか？',
    'Confirm purchase': '購入を確定',
    'Cancel this activation to stop waiting for a code. Voluntary cancellation does not guarantee or issue a local quota refund.':
      'このアクティベーションをキャンセルするとコード待機を停止します。任意キャンセルではローカルクォータの返金は保証・実行されません。',
    'Reorder {{domain}} for {{quota}} quota ({{price}} customer price)? This creates a new paid activation.':
      '{{domain}} を {{quota}} クォータ（顧客価格 {{price}}）で再注文しますか？新しい有料アクティベーションが作成されます。',
    '$1 provider cost → {{price}} customer price':
      'プロバイダー原価 $1 → 顧客価格 {{price}}',
    'API key must contain at least 16 characters':
      'API キーは16文字以上で入力してください',
    'Use at most 6 decimal places': '小数点以下は最大6桁にしてください',
    'Unable to save HeroSMS settings': 'HeroSMS 設定を保存できません',
    'Enter an API key before enabling HeroSMS':
      'HeroSMS を有効にする前に API キーを入力してください',
    'Unable to clear HeroSMS API key': 'HeroSMS API キーを消去できません',
    'Disable HeroSMS before clearing the saved key':
      '保存済みキーを消去する前に HeroSMS を無効にしてください',
    'The server can reach HeroSMS with the provided or saved credential.':
      '入力済みまたは保存済みの認証情報で、サーバーから HeroSMS に接続できます。',
    'Disable HeroSMS first. This permanently removes the server-side secret; purchasing and connection tests will fail until a new key is saved.':
      '先に HeroSMS を無効にしてください。この操作はサーバー側のシークレットを完全に削除します。新しいキーを保存するまで購入と接続テストは失敗します。',
    'Clear key': 'キーを消去',
    'Connection test failed': '接続テストに失敗しました',
    'Currency code': '通貨コード',
    History: '履歴',
    'Price multiplier': '価格倍率',
    'Quota charge': '消費クォータ',
    Reorder: '再注文',
    Site: '対象サイト',
    'Test connection': '接続をテスト',
    'Wait for active orders to finish before clearing the saved key':
      '処理中の注文が完了してから保存済みキーを消去してください',
    'Active orders are still being reconciled': '処理中の注文を照合しています',
    'Keep the HeroSMS API key until active orders finish or are refunded.':
      '処理中の注文が完了または返金されるまで HeroSMS API キーを保持してください。',
    'Cancellation reason': 'キャンセル理由',
    'User requested cancellation': 'ユーザーによるキャンセル',
    'Provider price changed': 'プロバイダー価格が変更されました',
    'Provider currency mismatch': 'プロバイダーの通貨が一致しません',
    'Invalid provider response': 'プロバイダーの応答が不正です',
    'Purchase failed': '購入に失敗しました',
    'The provider purchase failed and the reserved quota was refunded.':
      'プロバイダーでの購入に失敗し、予約済みクォータは返金されました。',
  },
  ru: {
    'Activation details': 'Детали активации',
    'Activation refreshed': 'Активация обновлена',
    'Add quota in Wallet, then retry the purchase or reorder action.':
      'Пополните квоту в Wallet, затем повторите покупку или повторный заказ.',
    'All statuses': 'Все статусы',
    'Allow authenticated users to purchase HeroSMS temporary email activations from the console.':
      'Разрешить аутентифицированным пользователям покупать временные почтовые активации HeroSMS из консоли.',
    'Awaiting code': 'Ожидание кода',
    'Buy activation': 'Купить активацию',
    'Cancel activation': 'Отменить активацию',
    'Cancel pending': 'Отмена обрабатывается',
    'Cancellation requested': 'Запрошена отмена',
    'Choose a domain': 'Выберите домен',
    'Choose a site, domain, and quantity, then confirm the latest stock and quota charge before purchasing.':
      'Выберите сайт, домен и количество, затем подтвердите актуальный остаток и стоимость в квоте перед покупкой.',
    'Choose another domain or refresh to check for replenished inventory.':
      'Выберите другой домен или обновите страницу, чтобы проверить пополнение.',
    'Clear saved HeroSMS API key': 'Очистить сохранённый API-ключ HeroSMS',
    'Clear saved key': 'Очистить сохранённый ключ',
    'Clearing...': 'Очистка...',
    'Code received': 'Код получен',
    Configured: 'Настроено',
    'Confirm cancel': 'Подтвердить отмену',
    'Confirm reorder': 'Подтвердить повторный заказ',
    'Connection test succeeded': 'Проверка подключения успешна',
    'Current activation': 'Текущая активация',
    'Email activation purchased': 'Почтовая активация куплена',
    'Enable HeroSMS email activations': 'Включить почтовые активации HeroSMS',
    'Enter target site first': 'Сначала введите целевой сайт',
    'Final quota price': 'Итоговая цена в квоте',
    'Fixed provider settlement currency':
      'Фиксированная валюта расчёта провайдера',
    'For security, the browser never reads back the saved secret. Enter a new key only when rotating it.':
      'Из соображений безопасности браузер никогда не читает сохранённый секрет обратно. Вводите новый ключ только при ротации.',
    'HeroSMS API key cleared': 'API-ключ HeroSMS очищен',
    'HeroSMS connection test passed':
      'Проверка подключения HeroSMS прошла успешно',
    'HeroSMS Email': 'Почта HeroSMS',
    'HeroSMS is temporarily unavailable. Keep this page open and try again shortly.':
      'HeroSMS временно недоступен. Оставьте эту страницу открытой и попробуйте снова чуть позже.',
    'HeroSMS only returns purchasable email domains after you provide a non-empty target site.':
      'HeroSMS возвращает покупаемые почтовые домены только после указания непустого целевого сайта.',
    'HeroSMS purchasing is disabled': 'Покупка HeroSMS отключена',
    'HeroSMS settings saved': 'Настройки HeroSMS сохранены',
    'Insufficient quota': 'Недостаточно квоты',
    Inventory: 'Остаток',
    'ISO numeric currency code': 'Числовой код валюты ISO',
    'Keep the latest active email and verification code visible while you complete sign-up or login.':
      'Держите последний активный адрес и код подтверждения на виду, пока завершаете регистрацию или вход.',
    'Latest provider update': 'Последнее обновление провайдера',
    'Leave blank to keep the current saved key':
      'Оставьте пустым, чтобы сохранить текущий ключ',
    'Loading activation details...': 'Загрузка деталей активации...',
    'Loading email activations...': 'Загрузка почтовых активаций...',
    'Loading HeroSMS settings...': 'Загрузка настроек HeroSMS...',
    'Loading products...': 'Загрузка продуктов...',
    'No active email activation': 'Нет активной почтовой активации',
    'No email activations found': 'Почтовые активации не найдены',
    'No email activations match the current filter.':
      'Нет почтовых активаций, подходящих под текущий фильтр.',
    'No HeroSMS email products are available for the target site right now.':
      'Сейчас для целевого сайта нет доступных почтовых продуктов HeroSMS.',
    'Open details': 'Открыть детали',
    'Order #{{id}}': 'Заказ №{{id}}',
    'Order status': 'Статус заказа',
    'Out of stock': 'Нет в наличии',
    'Pending email assignment': 'Назначение почты ожидается',
    'Pending purchase': 'Покупка ожидается',
    'Please wait a moment before sending another request.':
      'Подождите немного перед отправкой следующего запроса.',
    'Price changed': 'Цена изменилась',
    'Provider message': 'Сообщение провайдера',
    'Provider price': 'Цена провайдера',
    'Purchase activation': 'Купить активацию',
    'Purchase an activation to start receiving temporary email logins here.':
      'Купите активацию, чтобы здесь начали появляться временные почтовые входы.',
    'Purchase reconciling': 'Покупка сверяется',
    'Purchasing unavailable': 'Покупка недоступна',
    Quote: 'Цена',
    Reconciling: 'Сверяется',
    Refunded: 'Возвращено',
    'Reorder paid activation': 'Повторно заказать оплаченную активацию',
    'Reorder submitted': 'Повторный заказ отправлен',
    'Replacement API key': 'Новый API-ключ',
    'Retry to fetch the latest products and activation history.':
      'Повторите попытку получить последние продукты и историю активаций.',
    'Retry to fetch the latest provider configuration before editing this section.':
      'Повторите попытку получить последнюю конфигурацию провайдера перед редактированием этого раздела.',
    'Review current and past HeroSMS email activations, filter by status, and reopen order details.':
      'Просматривайте текущие и прошлые почтовые активации HeroSMS, фильтруйте по статусу и повторно открывайте детали заказа.',
    'Review the latest provider status, timestamps, and order identifiers for this activation.':
      'Просматривайте последний статус провайдера, метки времени и идентификаторы заказа для этой активации.',
    'Saving is still available, but refresh again if you suspect the server state changed elsewhere.':
      'Сохранение всё ещё доступно, но обновите ещё раз, если подозреваете, что состояние сервера изменилось где-то ещё.',
    'Temporary upstream issue': 'Временная проблема у провайдера',
    'The latest provider price no longer matches the quote shown here. Refresh products and confirm the new price before retrying.':
      'Последняя цена провайдера больше не совпадает с показанной здесь. Обновите продукты и подтвердите новую цену перед повторной попыткой.',
    'The provider is still reconciling your last purchase. Refresh this page in a moment before trying again.':
      'Провайдер всё ещё сверяет вашу последнюю покупку. Обновите эту страницу через мгновение и попробуйте снова.',
    'Turn this on only after the API key, multiplier, and test connection all succeed.':
      'Включайте это только после успешной проверки API-ключа, множителя и подключения.',
    'Unable to load HeroSMS email activations':
      'Не удалось загрузить почтовые активации HeroSMS',
    'Unable to load HeroSMS settings': 'Не удалось загрузить настройки HeroSMS',
    'Unknown status': 'Неизвестный статус',
    'Using last loaded HeroSMS settings':
      'Используются последние загруженные настройки HeroSMS',
    'Waiting for code': 'Ожидание кода',
    'Your next purchased activation will appear here until it completes, expires, or is cancelled.':
      'Следующая купленная активация появится здесь, пока не завершится, не истечёт или не будет отменена.',
    '{{count}} available': 'Доступно: {{count}}',
    'The provider is reconciling this purchase. Do not submit another order; this activation will update automatically.':
      'Провайдер сверяет эту покупку. Не создавайте повторный заказ — активация обновится автоматически.',
    'Purchase submitted for reconciliation': 'Покупка отправлена на сверку',
    'Reorder unavailable': 'Повторный заказ недоступен',
    'This activation does not contain a reusable site and domain.':
      'В этой активации нет сайта и домена для повторного заказа.',
    'No matching HeroSMS inventory is available for this activation.':
      'Для этой активации нет подходящего запаса HeroSMS.',
    'Confirm paid purchase': 'Подтвердить платную покупку',
    'Purchase {{quantity}} × {{domain}} for {{quota}} quota ({{price}} customer price)?':
      'Купить {{quantity}} × {{domain}} за {{quota}} квоты (цена для клиента: {{price}})?',
    'Confirm purchase': 'Подтвердить покупку',
    'Cancel this activation to stop waiting for a code. Voluntary cancellation does not guarantee or issue a local quota refund.':
      'Отмените активацию, чтобы прекратить ожидание кода. Добровольная отмена не гарантирует и не выполняет возврат локальной квоты.',
    'Reorder {{domain}} for {{quota}} quota ({{price}} customer price)? This creates a new paid activation.':
      'Заказать {{domain}} повторно за {{quota}} квоты (цена для клиента: {{price}})? Будет создана новая платная активация.',
    '$1 provider cost → {{price}} customer price':
      'Стоимость провайдера $1 → цена для клиента {{price}}',
    'API key must contain at least 16 characters':
      'Ключ API должен содержать не менее 16 символов',
    'Use at most 6 decimal places':
      'Используйте не более 6 знаков после запятой',
    'Unable to save HeroSMS settings': 'Не удалось сохранить настройки HeroSMS',
    'Enter an API key before enabling HeroSMS':
      'Введите ключ API перед включением HeroSMS',
    'Unable to clear HeroSMS API key': 'Не удалось удалить ключ API HeroSMS',
    'Disable HeroSMS before clearing the saved key':
      'Отключите HeroSMS перед удалением сохранённого ключа',
    'The server can reach HeroSMS with the provided or saved credential.':
      'Сервер может подключиться к HeroSMS с указанными или сохранёнными учётными данными.',
    'Disable HeroSMS first. This permanently removes the server-side secret; purchasing and connection tests will fail until a new key is saved.':
      'Сначала отключите HeroSMS. Это навсегда удалит серверный секрет; покупки и проверки подключения не будут работать, пока не будет сохранён новый ключ.',
    'Clear key': 'Удалить ключ',
    'Connection test failed': 'Проверка подключения не удалась',
    'Currency code': 'Код валюты',
    History: 'История',
    'Price multiplier': 'Множитель цены',
    'Quota charge': 'Списание квоты',
    Reorder: 'Заказать повторно',
    Site: 'Целевой сайт',
    'Test connection': 'Проверить подключение',
    'Wait for active orders to finish before clearing the saved key':
      'Дождитесь завершения активных заказов перед удалением сохранённого ключа',
    'Active orders are still being reconciled':
      'Активные заказы всё ещё сверяются',
    'Keep the HeroSMS API key until active orders finish or are refunded.':
      'Сохраняйте ключ API HeroSMS, пока активные заказы не завершатся или не будут возвращены.',
    'Cancellation reason': 'Причина отмены',
    'User requested cancellation': 'Отмена по запросу пользователя',
    'Provider price changed': 'Цена провайдера изменилась',
    'Provider currency mismatch': 'Валюта провайдера не совпадает',
    'Invalid provider response': 'Некорректный ответ провайдера',
    'Purchase failed': 'Покупка не удалась',
    'The provider purchase failed and the reserved quota was refunded.':
      'Покупка у провайдера не удалась, зарезервированная квота возвращена.',
  },
  vi: {
    'Activation details': 'Chi tiết kích hoạt',
    'Activation refreshed': 'Đã làm mới kích hoạt',
    'Add quota in Wallet, then retry the purchase or reorder action.':
      'Nạp thêm quota trong Wallet rồi thử lại thao tác mua hoặc đặt lại.',
    'All statuses': 'Tất cả trạng thái',
    'Allow authenticated users to purchase HeroSMS temporary email activations from the console.':
      'Cho phép người dùng đã xác thực mua kích hoạt email tạm thời HeroSMS từ console.',
    'Awaiting code': 'Đang chờ mã',
    'Buy activation': 'Mua kích hoạt',
    'Cancel activation': 'Hủy kích hoạt',
    'Cancel pending': 'Đang chờ hủy',
    'Cancellation requested': 'Đã yêu cầu hủy',
    'Choose a domain': 'Chọn domain',
    'Choose a site, domain, and quantity, then confirm the latest stock and quota charge before purchasing.':
      'Chọn site, domain và số lượng, rồi xác nhận tồn kho và mức trừ quota mới nhất trước khi mua.',
    'Choose another domain or refresh to check for replenished inventory.':
      'Hãy chọn domain khác hoặc làm mới để kiểm tra hàng mới.',
    'Clear saved HeroSMS API key': 'Xóa API key HeroSMS đã lưu',
    'Clear saved key': 'Xóa khóa đã lưu',
    'Clearing...': 'Đang xóa...',
    'Code received': 'Đã nhận mã',
    Configured: 'Đã cấu hình',
    'Confirm cancel': 'Xác nhận hủy',
    'Confirm reorder': 'Xác nhận đặt lại',
    'Connection test succeeded': 'Kiểm tra kết nối thành công',
    'Current activation': 'Kích hoạt hiện tại',
    'Email activation purchased': 'Đã mua kích hoạt email',
    'Enable HeroSMS email activations': 'Bật kích hoạt email HeroSMS',
    'Enter target site first': 'Hãy nhập site mục tiêu trước',
    'Final quota price': 'Giá quota cuối cùng',
    'Fixed provider settlement currency':
      'Đơn vị tiền tệ thanh toán cố định của nhà cung cấp',
    'For security, the browser never reads back the saved secret. Enter a new key only when rotating it.':
      'Vì lý do bảo mật, trình duyệt không bao giờ đọc lại bí mật đã lưu. Chỉ nhập khóa mới khi bạn xoay vòng khóa.',
    'HeroSMS API key cleared': 'Đã xóa API key HeroSMS',
    'HeroSMS connection test passed': 'Kiểm tra kết nối HeroSMS thành công',
    'HeroSMS Email': 'Email HeroSMS',
    'HeroSMS is temporarily unavailable. Keep this page open and try again shortly.':
      'HeroSMS tạm thời không khả dụng. Hãy giữ trang này mở và thử lại sau ít phút.',
    'HeroSMS only returns purchasable email domains after you provide a non-empty target site.':
      'HeroSMS chỉ trả về các domain email có thể mua sau khi bạn cung cấp một site mục tiêu không rỗng.',
    'HeroSMS purchasing is disabled': 'Mua HeroSMS đang bị tắt',
    'HeroSMS settings saved': 'Đã lưu cài đặt HeroSMS',
    'Insufficient quota': 'Không đủ quota',
    Inventory: 'Tồn kho',
    'ISO numeric currency code': 'Mã tiền tệ số ISO',
    'Keep the latest active email and verification code visible while you complete sign-up or login.':
      'Giữ email đang hoạt động mới nhất và mã xác minh luôn hiển thị trong lúc bạn hoàn tất đăng ký hoặc đăng nhập.',
    'Latest provider update': 'Cập nhật mới nhất từ nhà cung cấp',
    'Leave blank to keep the current saved key':
      'Để trống để giữ nguyên khóa đang lưu',
    'Loading activation details...': 'Đang tải chi tiết kích hoạt...',
    'Loading email activations...': 'Đang tải kích hoạt email...',
    'Loading HeroSMS settings...': 'Đang tải cài đặt HeroSMS...',
    'Loading products...': 'Đang tải sản phẩm...',
    'No active email activation': 'Không có kích hoạt email nào đang hoạt động',
    'No email activations found': 'Không tìm thấy kích hoạt email',
    'No email activations match the current filter.':
      'Không có kích hoạt email nào khớp với bộ lọc hiện tại.',
    'No HeroSMS email products are available for the target site right now.':
      'Hiện không có sản phẩm email HeroSMS nào cho site mục tiêu này.',
    'Open details': 'Mở chi tiết',
    'Order #{{id}}': 'Đơn hàng #{{id}}',
    'Order status': 'Trạng thái đơn hàng',
    'Out of stock': 'Hết hàng',
    'Pending email assignment': 'Đang chờ cấp email',
    'Pending purchase': 'Đang chờ mua',
    'Please wait a moment before sending another request.':
      'Vui lòng đợi một lát trước khi gửi yêu cầu khác.',
    'Price changed': 'Giá đã thay đổi',
    'Provider message': 'Tin nhắn từ nhà cung cấp',
    'Provider price': 'Giá nhà cung cấp',
    'Purchase activation': 'Mua kích hoạt',
    'Purchase an activation to start receiving temporary email logins here.':
      'Mua một kích hoạt để bắt đầu nhận các lần đăng nhập email tạm thời tại đây.',
    'Purchase reconciling': 'Đang đối soát giao dịch',
    'Purchasing unavailable': 'Không thể mua',
    Quote: 'Báo giá',
    Reconciling: 'Đang đối soát',
    Refunded: 'Đã hoàn tiền',
    'Reorder paid activation': 'Đặt lại kích hoạt đã thanh toán',
    'Reorder submitted': 'Đã gửi đặt lại',
    'Replacement API key': 'API key thay thế',
    'Retry to fetch the latest products and activation history.':
      'Hãy thử lại để lấy sản phẩm và lịch sử kích hoạt mới nhất.',
    'Retry to fetch the latest provider configuration before editing this section.':
      'Hãy thử lấy lại cấu hình nhà cung cấp mới nhất trước khi chỉnh sửa mục này.',
    'Review current and past HeroSMS email activations, filter by status, and reopen order details.':
      'Xem lại các kích hoạt email HeroSMS hiện tại và trước đây, lọc theo trạng thái và mở lại chi tiết đơn hàng.',
    'Review the latest provider status, timestamps, and order identifiers for this activation.':
      'Xem trạng thái mới nhất từ nhà cung cấp, mốc thời gian và mã đơn hàng cho kích hoạt này.',
    'Saving is still available, but refresh again if you suspect the server state changed elsewhere.':
      'Bạn vẫn có thể lưu, nhưng hãy làm mới lại nếu nghi ngờ trạng thái máy chủ đã thay đổi ở nơi khác.',
    'Temporary upstream issue': 'Sự cố tạm thời từ upstream',
    'The latest provider price no longer matches the quote shown here. Refresh products and confirm the new price before retrying.':
      'Giá mới nhất từ nhà cung cấp không còn khớp với báo giá đang hiển thị. Hãy làm mới sản phẩm và xác nhận giá mới trước khi thử lại.',
    'The provider is still reconciling your last purchase. Refresh this page in a moment before trying again.':
      'Nhà cung cấp vẫn đang đối soát lần mua gần nhất của bạn. Hãy tải lại trang này sau một lát rồi thử lại.',
    'Turn this on only after the API key, multiplier, and test connection all succeed.':
      'Chỉ bật tính năng này sau khi API key, hệ số và kiểm tra kết nối đều thành công.',
    'Unable to load HeroSMS email activations':
      'Không thể tải kích hoạt email HeroSMS',
    'Unable to load HeroSMS settings': 'Không thể tải cài đặt HeroSMS',
    'Unknown status': 'Trạng thái không xác định',
    'Using last loaded HeroSMS settings':
      'Đang dùng cài đặt HeroSMS đã tải lần trước',
    'Waiting for code': 'Đang chờ mã',
    'Your next purchased activation will appear here until it completes, expires, or is cancelled.':
      'Kích hoạt bạn mua tiếp theo sẽ xuất hiện ở đây cho đến khi hoàn tất, hết hạn hoặc bị hủy.',
    '{{count}} available': 'Còn {{count}}',
    'The provider is reconciling this purchase. Do not submit another order; this activation will update automatically.':
      'Nhà cung cấp đang đối soát giao dịch này. Không tạo đơn khác; lượt kích hoạt sẽ tự động cập nhật.',
    'Purchase submitted for reconciliation':
      'Giao dịch đã được đưa vào đối soát',
    'Reorder unavailable': 'Không thể mua lại',
    'This activation does not contain a reusable site and domain.':
      'Lượt kích hoạt này không có trang và tên miền có thể dùng để mua lại.',
    'No matching HeroSMS inventory is available for this activation.':
      'Hiện không có tồn kho HeroSMS phù hợp với lượt kích hoạt này.',
    'Confirm paid purchase': 'Xác nhận giao dịch trả phí',
    'Purchase {{quantity}} × {{domain}} for {{quota}} quota ({{price}} customer price)?':
      'Mua {{quantity}} × {{domain}} với {{quota}} quota (giá khách hàng {{price}})?',
    'Confirm purchase': 'Xác nhận mua',
    'Cancel this activation to stop waiting for a code. Voluntary cancellation does not guarantee or issue a local quota refund.':
      'Hủy lượt kích hoạt để ngừng chờ mã. Việc tự nguyện hủy không đảm bảo và không tự động hoàn quota nội bộ.',
    'Reorder {{domain}} for {{quota}} quota ({{price}} customer price)? This creates a new paid activation.':
      'Mua lại {{domain}} với {{quota}} quota (giá khách hàng {{price}})? Thao tác này tạo một lượt kích hoạt trả phí mới.',
    '$1 provider cost → {{price}} customer price':
      'Chi phí nhà cung cấp $1 → giá khách hàng {{price}}',
    'API key must contain at least 16 characters':
      'Khóa API phải có ít nhất 16 ký tự',
    'Use at most 6 decimal places': 'Chỉ dùng tối đa 6 chữ số thập phân',
    'Unable to save HeroSMS settings': 'Không thể lưu cài đặt HeroSMS',
    'Enter an API key before enabling HeroSMS':
      'Nhập khóa API trước khi bật HeroSMS',
    'Unable to clear HeroSMS API key': 'Không thể xóa khóa API HeroSMS',
    'Disable HeroSMS before clearing the saved key':
      'Tắt HeroSMS trước khi xóa khóa đã lưu',
    'The server can reach HeroSMS with the provided or saved credential.':
      'Máy chủ có thể kết nối HeroSMS bằng thông tin xác thực vừa nhập hoặc đã lưu.',
    'Disable HeroSMS first. This permanently removes the server-side secret; purchasing and connection tests will fail until a new key is saved.':
      'Hãy tắt HeroSMS trước. Thao tác này xóa vĩnh viễn bí mật phía máy chủ; việc mua và kiểm tra kết nối sẽ không hoạt động cho đến khi lưu khóa mới.',
    'Clear key': 'Xóa khóa',
    'Connection test failed': 'Kiểm tra kết nối thất bại',
    'Currency code': 'Mã tiền tệ',
    History: 'Lịch sử',
    'Price multiplier': 'Hệ số giá',
    'Quota charge': 'Quota đã trừ',
    Reorder: 'Mua lại',
    Site: 'Trang đích',
    'Test connection': 'Kiểm tra kết nối',
    'Wait for active orders to finish before clearing the saved key':
      'Chờ các đơn đang hoạt động hoàn tất trước khi xóa khóa đã lưu',
    'Active orders are still being reconciled':
      'Các đơn đang hoạt động vẫn đang được đối soát',
    'Keep the HeroSMS API key until active orders finish or are refunded.':
      'Giữ khóa API HeroSMS cho đến khi các đơn đang hoạt động hoàn tất hoặc được hoàn tiền.',
    'Cancellation reason': 'Lý do hủy',
    'User requested cancellation': 'Người dùng yêu cầu hủy',
    'Provider price changed': 'Giá nhà cung cấp đã thay đổi',
    'Provider currency mismatch': 'Đơn vị tiền của nhà cung cấp không khớp',
    'Invalid provider response': 'Phản hồi nhà cung cấp không hợp lệ',
    'Purchase failed': 'Mua thất bại',
    'The provider purchase failed and the reserved quota was refunded.':
      'Giao dịch với nhà cung cấp thất bại và quota đã giữ được hoàn lại.',
  },
} as const

let registered = false

export function registerHeroSmsTranslations(instance: I18nInstance = i18next) {
  if (registered) return
  for (const [language, translations] of Object.entries(heroSmsTranslations)) {
    instance.addResourceBundle(
      language,
      'translation',
      translations,
      true,
      true
    )
  }
  registered = true
}
