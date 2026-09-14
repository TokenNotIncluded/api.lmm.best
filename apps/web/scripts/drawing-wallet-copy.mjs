/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
const keys = [
  'Increase platform credit',
  'Decrease platform credit',
  'Quota must be a non-negative decimal number.',
  'Create a new API key for MCP',
  'Select a valid drawing group and enter a key name.',
]
const values = {
  en: keys,
  zh: [
    '增加平台额度',
    '减少平台额度',
    '额度必须是非负数字。',
    '为 MCP 创建新的 API 密钥',
    '请选择有效绘图分组并输入密钥名称。',
  ],
  'zh-TW': [
    '增加平台額度',
    '減少平台額度',
    '額度必須是非負數字。',
    '為 MCP 建立新的 API 金鑰',
    '請選擇有效繪圖分組並輸入金鑰名稱。',
  ],
  fr: [
    'Augmenter le crédit de la plateforme',
    'Réduire le crédit de la plateforme',
    'Le quota doit être un nombre décimal positif ou nul.',
    'Créer une nouvelle clé API pour MCP',
    'Sélectionnez un groupe de dessin valide et saisissez un nom de clé.',
  ],
  ja: [
    'プラットフォームクレジットを増やす',
    'プラットフォームクレジットを減らす',
    'クォータは0以上の数値である必要があります。',
    'MCP 用の新しい API キーを作成',
    '有効な描画グループを選び、キー名を入力してください。',
  ],
  ru: [
    'Увеличить кредит платформы',
    'Уменьшить кредит платформы',
    'Квота должна быть неотрицательным числом.',
    'Создать новый API-ключ для MCP',
    'Выберите доступную группу рисования и введите имя ключа.',
  ],
  vi: [
    'Tăng tín dụng nền tảng',
    'Giảm tín dụng nền tảng',
    'Hạn mức phải là số thập phân không âm.',
    'Tạo API key mới cho MCP',
    'Hãy chọn nhóm vẽ hợp lệ và nhập tên key.',
  ],
}
export const drawingWalletCopy = Object.fromEntries(
  Object.entries(values).map(([locale, translations]) => [
    locale,
    Object.fromEntries(keys.map((key, index) => [key, translations[index]])),
  ])
)
