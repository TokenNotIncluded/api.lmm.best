/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
const en = {
  'API key created and selected for MCP billing.':
    'API key created and selected for MCP billing.',
  'API key for MCP billing': 'API key for MCP billing',
  'Create a new key and bind MCP billing to it. The key secret stays in API key management.':
    'Create a new key and bind MCP billing to it. The key secret stays in API key management.',
  'Create an API key for drawing MCP': 'Create an API key for drawing MCP',
  'Create and select key': 'Create and select key',
  'Drawing MCP token revoked.': 'Drawing MCP token revoked.',
  'Leave empty for unlimited': 'Leave empty for unlimited',
  'MCP uses this key; its total usage includes other clients.':
    'MCP uses this key; its total usage includes other clients.',
  'Revoke MCP token': 'Revoke MCP token',
  'Revoke this drawing MCP token? Existing MCP configurations will stop working.':
    'Revoke this drawing MCP token? Existing MCP configurations will stop working.',
  'Rotate MCP token': 'Rotate MCP token',
  'Rotate the drawing MCP token? Existing MCP configurations will stop working.':
    'Rotate the drawing MCP token? Existing MCP configurations will stop working.',
  'Select an API key before generating MCP config':
    'Select an API key before generating MCP config',
  'Unable to create and select the API key.':
    'Unable to create and select the API key.',
  'Unable to revoke the drawing MCP token.':
    'Unable to revoke the drawing MCP token.',
  'Unable to rotate the drawing MCP token.':
    'Unable to rotate the drawing MCP token.',
  'Prepare an API key for MCP': 'Prepare an API key for MCP',
  'Default drawing model': 'Default drawing model',
  'Automatic (first available model)': 'Automatic (first available model)',
  'Calls may switch to another available model allowed by this API key. Generate or rotate the MCP token to apply changes.':
    'Calls may switch to another available model allowed by this API key. Generate or rotate the MCP token to apply changes.',
  Unavailable: 'Unavailable',
  remaining: 'remaining',
}
const zh = {
  'API key created and selected for MCP billing.':
    'API 密钥已创建并选为 MCP 计费密钥。',
  'API key for MCP billing': 'MCP 计费 API 密钥',
  'Create a new key and bind MCP billing to it. The key secret stays in API key management.':
    '创建新密钥并将 MCP 计费绑定到它。密钥内容只会保留在 API 密钥管理中。',
  'Create an API key for drawing MCP': '为绘图 MCP 创建 API 密钥',
  'Create and select key': '创建并选择密钥',
  'Drawing MCP token revoked.': '绘图 MCP 令牌已撤销。',
  'Leave empty for unlimited': '留空表示不限制',
  'MCP uses this key; its total usage includes other clients.':
    'MCP 使用此密钥；总用量也包括其他客户端。',
  'Revoke MCP token': '撤销 MCP 令牌',
  'Revoke this drawing MCP token? Existing MCP configurations will stop working.':
    '撤销此绘图 MCP 令牌？现有 MCP 配置将停止工作。',
  'Rotate MCP token': '轮换 MCP 令牌',
  'Rotate the drawing MCP token? Existing MCP configurations will stop working.':
    '轮换绘图 MCP 令牌？现有 MCP 配置将停止工作。',
  'Select an API key before generating MCP config':
    '生成 MCP 配置前请选择 API 密钥',
  'Unable to create and select the API key.': '无法创建并选择 API 密钥。',
  'Unable to revoke the drawing MCP token.': '无法撤销绘图 MCP 令牌。',
  'Unable to rotate the drawing MCP token.': '无法轮换绘图 MCP 令牌。',
  'Prepare an API key for MCP': '为 MCP 准备 API 密钥',
  'Default drawing model': '默认绘图模型',
  'Automatic (first available model)': '自动（首个可用模型）',
  'Calls may switch to another available model allowed by this API key. Generate or rotate the MCP token to apply changes.':
    '调用时可以切换为此 API 密钥允许的其他可用模型。生成或轮换 MCP 令牌后更改才会生效。',
  Unavailable: '不可用',
  remaining: '剩余',
}
const zhTW = {
  'API key created and selected for MCP billing.':
    'API 金鑰已建立並選為 MCP 計費金鑰。',
  'API key for MCP billing': 'MCP 計費 API 金鑰',
  'Create a new key and bind MCP billing to it. The key secret stays in API key management.':
    '建立新金鑰並將 MCP 計費綁定至此。金鑰內容只會保留在 API 金鑰管理中。',
  'Create an API key for drawing MCP': '為繪圖 MCP 建立 API 金鑰',
  'Create and select key': '建立並選取金鑰',
  'Drawing MCP token revoked.': '繪圖 MCP 權杖已撤銷。',
  'Leave empty for unlimited': '留空表示不限制',
  'MCP uses this key; its total usage includes other clients.':
    'MCP 使用此金鑰；總用量也包括其他用戶端。',
  'Revoke MCP token': '撤銷 MCP 權杖',
  'Revoke this drawing MCP token? Existing MCP configurations will stop working.':
    '撤銷此繪圖 MCP 權杖？現有 MCP 設定將停止運作。',
  'Rotate MCP token': '輪換 MCP 權杖',
  'Rotate the drawing MCP token? Existing MCP configurations will stop working.':
    '輪換繪圖 MCP 權杖？現有 MCP 設定將停止運作。',
  'Select an API key before generating MCP config':
    '產生 MCP 設定前請選取 API 金鑰',
  'Unable to create and select the API key.': '無法建立並選取 API 金鑰。',
  'Unable to revoke the drawing MCP token.': '無法撤銷繪圖 MCP 權杖。',
  'Unable to rotate the drawing MCP token.': '無法輪換繪圖 MCP 權杖。',
  'Prepare an API key for MCP': '為 MCP 準備 API 金鑰',
  'Default drawing model': '預設繪圖模型',
  'Automatic (first available model)': '自動（第一個可用模型）',
  'Calls may switch to another available model allowed by this API key. Generate or rotate the MCP token to apply changes.':
    '呼叫時可以切換為此 API 金鑰允許的其他可用模型。產生或輪換 MCP 權杖後變更才會生效。',
  Unavailable: '不可用',
  remaining: '剩餘',
}
const fr = {
  'API key created and selected for MCP billing.':
    'Clé API créée et sélectionnée pour la facturation MCP.',
  'API key for MCP billing': 'Clé API pour la facturation MCP',
  'Create a new key and bind MCP billing to it. The key secret stays in API key management.':
    'Créez une clé et associez-y la facturation MCP. Le secret reste dans la gestion des clés API.',
  'Create an API key for drawing MCP': 'Créer une clé API pour MCP dessin',
  'Create and select key': 'Créer et sélectionner la clé',
  'Drawing MCP token revoked.': 'Jeton MCP dessin révoqué.',
  'Leave empty for unlimited': 'Laisser vide pour un accès illimité',
  'MCP uses this key; its total usage includes other clients.':
    'MCP utilise cette clé ; son total inclut les autres clients.',
  'Revoke MCP token': 'Révoquer le jeton MCP',
  'Revoke this drawing MCP token? Existing MCP configurations will stop working.':
    'Révoquer ce jeton MCP dessin ? Les configurations existantes cesseront de fonctionner.',
  'Rotate MCP token': 'Faire tourner le jeton MCP',
  'Rotate the drawing MCP token? Existing MCP configurations will stop working.':
    'Faire tourner le jeton MCP dessin ? Les configurations existantes cesseront de fonctionner.',
  'Select an API key before generating MCP config':
    'Sélectionnez une clé API avant de générer la configuration MCP',
  'Unable to create and select the API key.':
    'Impossible de créer et sélectionner la clé API.',
  'Unable to revoke the drawing MCP token.':
    'Impossible de révoquer le jeton MCP dessin.',
  'Unable to rotate the drawing MCP token.':
    'Impossible de faire tourner le jeton MCP dessin.',
  'Prepare an API key for MCP': 'Préparer une clé API pour MCP',
  'Default drawing model': 'Modèle de dessin par défaut',
  'Automatic (first available model)':
    'Automatique (premier modèle disponible)',
  'Calls may switch to another available model allowed by this API key. Generate or rotate the MCP token to apply changes.':
    'Les appels peuvent utiliser un autre modèle disponible autorisé par cette clé API. Générez ou renouvelez le jeton MCP pour appliquer les modifications.',
  Unavailable: 'Indisponible',
  remaining: 'restant',
}
const ja = {
  'API key created and selected for MCP billing.':
    'API キーを作成し、MCP 課金用に選択しました。',
  'API key for MCP billing': 'MCP 課金用 API キー',
  'Create a new key and bind MCP billing to it. The key secret stays in API key management.':
    '新しいキーを作成して MCP 課金に紐付けます。キーの秘密は API キー管理に保管されます。',
  'Create an API key for drawing MCP': '描画 MCP の API キーを作成',
  'Create and select key': 'キーを作成して選択',
  'Drawing MCP token revoked.': '描画 MCP トークンを取り消しました。',
  'Leave empty for unlimited': '空欄で無制限',
  'MCP uses this key; its total usage includes other clients.':
    'MCP はこのキーを使い、合計使用量には他のクライアントも含まれます。',
  'Revoke MCP token': 'MCP トークンを取り消す',
  'Revoke this drawing MCP token? Existing MCP configurations will stop working.':
    'この描画 MCP トークンを取り消しますか？既存の設定は動作しなくなります。',
  'Rotate MCP token': 'MCP トークンを更新',
  'Rotate the drawing MCP token? Existing MCP configurations will stop working.':
    '描画 MCP トークンを更新しますか？既存の設定は動作しなくなります。',
  'Select an API key before generating MCP config':
    'MCP 設定を生成する前に API キーを選択してください',
  'Unable to create and select the API key.':
    'API キーを作成して選択できません。',
  'Unable to revoke the drawing MCP token.':
    '描画 MCP トークンを取り消せません。',
  'Unable to rotate the drawing MCP token.':
    '描画 MCP トークンを更新できません。',
  'Prepare an API key for MCP': 'MCP 用 API キーを準備',
  'Default drawing model': 'デフォルトの描画モデル',
  'Automatic (first available model)': '自動（最初の利用可能なモデル）',
  'Calls may switch to another available model allowed by this API key. Generate or rotate the MCP token to apply changes.':
    '呼び出し時に、この API キーで許可された別の利用可能なモデルへ切り替えられます。変更を適用するには MCP トークンを生成または更新してください。',
  Unavailable: '利用不可',
  remaining: '残り',
}
const ru = {
  'API key created and selected for MCP billing.':
    'API-ключ создан и выбран для оплаты MCP.',
  'API key for MCP billing': 'API-ключ для оплаты MCP',
  'Create a new key and bind MCP billing to it. The key secret stays in API key management.':
    'Создайте ключ и привяжите к нему оплату MCP. Секрет хранится только в управлении API-ключами.',
  'Create an API key for drawing MCP': 'Создать API-ключ для графического MCP',
  'Create and select key': 'Создать и выбрать ключ',
  'Drawing MCP token revoked.': 'Токен графического MCP отозван.',
  'Leave empty for unlimited': 'Оставьте пустым для снятия лимита',
  'MCP uses this key; its total usage includes other clients.':
    'MCP использует этот ключ; общий расход включает другие клиенты.',
  'Revoke MCP token': 'Отозвать токен MCP',
  'Revoke this drawing MCP token? Existing MCP configurations will stop working.':
    'Отозвать токен графического MCP? Текущие конфигурации перестанут работать.',
  'Rotate MCP token': 'Обновить токен MCP',
  'Rotate the drawing MCP token? Existing MCP configurations will stop working.':
    'Обновить токен графического MCP? Текущие конфигурации перестанут работать.',
  'Select an API key before generating MCP config':
    'Выберите API-ключ перед созданием конфигурации MCP',
  'Unable to create and select the API key.':
    'Не удалось создать и выбрать API-ключ.',
  'Unable to revoke the drawing MCP token.':
    'Не удалось отозвать токен графического MCP.',
  'Unable to rotate the drawing MCP token.':
    'Не удалось обновить токен графического MCP.',
  'Prepare an API key for MCP': 'Подготовить API-ключ для MCP',
  'Default drawing model': 'Модель рисования по умолчанию',
  'Automatic (first available model)':
    'Автоматически (первая доступная модель)',
  'Calls may switch to another available model allowed by this API key. Generate or rotate the MCP token to apply changes.':
    'При вызове можно выбрать другую доступную модель, разрешённую этим API-ключом. Чтобы применить изменения, создайте или обновите токен MCP.',
  Unavailable: 'Недоступно',
  remaining: 'осталось',
}
const vi = {
  'API key created and selected for MCP billing.':
    'Đã tạo và chọn API key để thanh toán MCP.',
  'API key for MCP billing': 'API key thanh toán MCP',
  'Create a new key and bind MCP billing to it. The key secret stays in API key management.':
    'Tạo key mới và gắn thanh toán MCP vào đó. Bí mật key chỉ nằm trong quản lý API key.',
  'Create an API key for drawing MCP': 'Tạo API key cho MCP vẽ',
  'Create and select key': 'Tạo và chọn key',
  'Drawing MCP token revoked.': 'Đã thu hồi token MCP vẽ.',
  'Leave empty for unlimited': 'Để trống để không giới hạn',
  'MCP uses this key; its total usage includes other clients.':
    'MCP dùng key này; tổng mức dùng gồm cả client khác.',
  'Revoke MCP token': 'Thu hồi token MCP',
  'Revoke this drawing MCP token? Existing MCP configurations will stop working.':
    'Thu hồi token MCP vẽ này? Các cấu hình MCP hiện có sẽ ngừng hoạt động.',
  'Rotate MCP token': 'Xoay vòng token MCP',
  'Rotate the drawing MCP token? Existing MCP configurations will stop working.':
    'Xoay vòng token MCP vẽ? Các cấu hình MCP hiện có sẽ ngừng hoạt động.',
  'Select an API key before generating MCP config':
    'Chọn API key trước khi tạo cấu hình MCP',
  'Unable to create and select the API key.': 'Không thể tạo và chọn API key.',
  'Unable to revoke the drawing MCP token.': 'Không thể thu hồi token MCP vẽ.',
  'Unable to rotate the drawing MCP token.':
    'Không thể xoay vòng token MCP vẽ.',
  'Prepare an API key for MCP': 'Chuẩn bị khóa API cho MCP',
  'Default drawing model': 'Mô hình vẽ mặc định',
  'Automatic (first available model)': 'Tự động (mô hình khả dụng đầu tiên)',
  'Calls may switch to another available model allowed by this API key. Generate or rotate the MCP token to apply changes.':
    'Lệnh gọi có thể chuyển sang mô hình khả dụng khác mà khóa API này cho phép. Hãy tạo hoặc xoay vòng token MCP để áp dụng thay đổi.',
  Unavailable: 'Không khả dụng',
  remaining: 'còn lại',
}
export const drawingMcpExtraCopy = { en, zh, 'zh-TW': zhTW, fr, ja, ru, vi }
