/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */

const locales = ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi']
const rows = [
  [
    'Base price (1×)',
    '基础价（1×）',
    '基礎價（1×）',
    'Prix de base (1×)',
    '基本価格（1×）',
    'Базовая цена (1×)',
    'Giá cơ sở (1×)',
  ],
  [
    'Available groups',
    '可用分组',
    '可用群組',
    'Groupes disponibles',
    '利用可能なグループ',
    'Доступные группы',
    'Nhóm khả dụng',
  ],
  [
    'Groups filter model availability only. Prices always use the base price (1×).',
    '分组仅筛选可用模型，价格始终显示基础价（1×）。',
    '群組僅篩選可用模型，價格一律顯示基礎價（1×）。',
    'Les groupes filtrent uniquement les modèles disponibles. Les prix affichés restent les prix de base (1×).',
    'グループは利用可能なモデルの絞り込みにのみ使われます。価格は常に基本価格（1×）です。',
    'Группы фильтруют только доступность моделей. Всегда отображаются базовые цены (1×).',
    'Nhóm chỉ lọc mô hình khả dụng. Giá luôn hiển thị theo giá cơ sở (1×).',
  ],
  [
    'One request at the base price (1×), using input tokens. Actual charges may differ.',
    '按基础价（1×）和输入 token 估算一次请求，实际收费可能不同。',
    '依基礎價（1×）與輸入 token 估算一次請求，實際收費可能不同。',
    'Une requête au prix de base (1×), selon les tokens d’entrée. Le montant facturé peut varier.',
    '入力トークンから基本価格（1×）で1回のリクエストを見積もります。実際の料金は異なる場合があります。',
    'Один запрос по базовой цене (1×), с учётом входных токенов. Фактическая плата может отличаться.',
    'Ước tính một yêu cầu theo giá cơ sở (1×) và token đầu vào. Phí thực tế có thể khác.',
  ],
  [
    'One request at the base price (1×). Cached tokens are part of input. Excludes cache writes, media and tool fees; actual charges may differ.',
    '按基础价（1×）估算一次请求，缓存 token 包含在输入中。不含缓存写入、媒体和工具费用，实际收费可能不同。',
    '依基礎價（1×）估算一次請求，快取 token 包含在輸入中。不含快取寫入、媒體與工具費用，實際收費可能不同。',
    'Une requête au prix de base (1×). Les tokens en cache font partie de l’entrée. Écritures du cache, médias et outils exclus ; le montant facturé peut varier.',
    '基本価格（1×）で1回のリクエストを見積もります。キャッシュ済みトークンは入力に含まれます。キャッシュ書き込み、メディア、ツール料金は含まず、実際の料金は異なる場合があります。',
    'Один запрос по базовой цене (1×). Кэшированные токены входят во входные. Запись кэша, медиа и инструменты не учтены; фактическая плата может отличаться.',
    'Ước tính một yêu cầu theo giá cơ sở (1×). Token bộ nhớ đệm thuộc đầu vào. Không gồm ghi bộ nhớ đệm, nội dung đa phương tiện và công cụ; phí thực tế có thể khác.',
  ],
  [
    'Check token counts. Dynamic or special billing requires the pricing rules above.',
    '请检查 token 数量。动态或特殊计费请参考上方规则。',
    '請檢查 token 數量。動態或特殊計費請參考上方規則。',
    'Vérifiez les nombres de tokens. Pour une tarification dynamique ou spéciale, consultez les règles ci-dessus.',
    'トークン数を確認してください。動的または特殊な課金は上記の規則を参照してください。',
    'Проверьте количество токенов. Для динамической или особой тарификации см. правила выше.',
    'Kiểm tra số token. Với tính phí động hoặc đặc biệt, xem quy tắc phía trên.',
  ],
]

export const pricingBaseCopy = Object.fromEntries(
  locales.map((locale, index) => [
    locale,
    Object.fromEntries(rows.map((row) => [row[0], row[index]])),
  ])
)
