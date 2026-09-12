/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
const keys = [
  'Prepare API key creation',
  'API key creation prepared',
  'Prepared; confirmation required',
]

const values = {
  en: keys,
  zh: ['准备创建 API 密钥', '已准备创建 API 密钥', '待确认'],
  'zh-TW': ['準備建立 API 金鑰', '已準備建立 API 金鑰', '待確認'],
  fr: ['Préparer la création de la clé API', 'Création de la clé API préparée', 'Préparé ; confirmation requise'],
  ja: ['API キーの作成を準備', 'API キーの作成を準備しました', '準備完了。確認が必要です'],
  ru: ['Подготовить создание API-ключа', 'Создание API-ключа подготовлено', 'Подготовлено; требуется подтверждение'],
  vi: ['Chuẩn bị tạo khóa API', 'Đã chuẩn bị tạo khóa API', 'Đã chuẩn bị; cần xác nhận'],
}

export const assistantToolCopy = Object.fromEntries(
  Object.entries(values).map(([locale, translations]) => [
    locale,
    Object.fromEntries(keys.map((key, index) => [key, translations[index]])),
  ])
)
