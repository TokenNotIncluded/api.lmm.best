/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
const keys = [
  'Keep your tools.',
  'Choose your AI.',
  'One address for compatible apps. Compare rates before you start.',
  'Make room for your next idea.',
]
const values = {
  en: keys,
  zh: [
    '工具照旧。',
    '模型随你。',
    '一个地址，连接常用工具。先看价格，再开始。',
    '把时间，留给下一个想法。',
  ],
  'zh-TW': [
    '工具照舊。',
    '模型隨你。',
    '一個地址，連接常用工具。先看價格，再開始。',
    '把時間，留給下一個想法。',
  ],
  fr: [
    'Gardez vos outils.',
    'Choisissez votre IA.',
    'Une adresse pour vos applications compatibles. Consultez les tarifs avant de commencer.',
    'Faites place à votre prochaine idée.',
  ],
  ja: [
    'いつものツールで。',
    'AIは自由に選ぶ。',
    '対応アプリをひとつのアドレスで接続。料金を確認してから始めましょう。',
    '次のアイデアに、時間を。',
  ],
  ru: [
    'Привычные инструменты.',
    'ИИ на ваш выбор.',
    'Один адрес для совместимых приложений. Сравните тарифы перед началом.',
    'Оставьте время для новой идеи.',
  ],
  vi: [
    'Giữ công cụ quen thuộc.',
    'Chọn AI bạn muốn.',
    'Một địa chỉ cho các ứng dụng tương thích. So sánh giá trước khi bắt đầu.',
    'Dành thời gian cho ý tưởng tiếp theo.',
  ],
}
export const homeEditorialCopy = Object.fromEntries(
  Object.entries(values).map(([locale, translations]) => [
    locale,
    Object.fromEntries(keys.map((key, index) => [key, translations[index]])),
  ])
)
