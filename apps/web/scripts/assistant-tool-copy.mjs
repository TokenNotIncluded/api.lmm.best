/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
const keys = [
  'Prepare API key creation',
  'API key creation prepared',
  'Prepared; confirmation required',
  'Confirm and submit for review',
  'Discuss your use case with the AI assistant. After three completed turns, it can grant L1 directly; earlier requests continue through automatic review with human fallback.',
  'Pending L0 requests appear here only when automatic review has not approved them. The assistant may also grant L1 directly after three completed turns.',
]

const values = {
  en: keys,
  zh: [
    '准备创建 API 密钥',
    '已准备创建 API 密钥',
    '待确认',
    '确认并提交审核',
    '向 AI 助手说明你的用途。完成三轮对话后，它可以直接授予 L1；更早提交的申请会先自动审核，必要时转人工处理。',
    '这里只显示尚未被自动审核批准的 L0 申请。完成三轮对话后，助手也可以直接授予 L1。',
  ],
  'zh-TW': [
    '準備建立 API 金鑰',
    '已準備建立 API 金鑰',
    '待確認',
    '確認並提交審核',
    '向 AI 助手說明你的用途。完成三輪對話後，它可以直接授予 L1；較早提交的申請會先自動審核，必要時轉由人工處理。',
    '這裡只顯示尚未獲自動審核批准的 L0 申請。完成三輪對話後，助手也可以直接授予 L1。',
  ],
  fr: [
    'Préparer la création de la clé API',
    'Création de la clé API préparée',
    'Préparé ; confirmation requise',
    'Confirmer et soumettre à examen',
    "Décrivez votre usage à l’assistant IA. Après trois échanges complets, il peut accorder directement le niveau L1 ; les demandes antérieures passent par l’examen automatique avec recours humain.",
    "Seules les demandes L0 non approuvées automatiquement figurent ici. L’assistant peut aussi accorder directement le niveau L1 après trois échanges complets.",
  ],
  ja: [
    'API キーの作成を準備',
    'API キーの作成を準備しました',
    '準備完了。確認が必要です',
    '確認して審査に送信',
    '用途を AI アシスタントに伝えてください。3 回の対話が完了すると、アシスタントが L1 を直接付与できます。それ以前の申請は自動審査され、必要な場合のみ人が対応します。',
    'ここには自動審査で承認されなかった L0 申請だけが表示されます。3 回の対話が完了すると、アシスタントが L1 を直接付与することもできます。',
  ],
  ru: [
    'Подготовить создание API-ключа',
    'Создание API-ключа подготовлено',
    'Подготовлено; требуется подтверждение',
    'Подтвердить и отправить на проверку',
    'Опишите сценарий использования ИИ-ассистенту. После трёх завершённых диалоговых циклов он может напрямую выдать уровень L1; более ранние заявки проходят автоматическую проверку с передачей человеку при необходимости.',
    'Здесь отображаются только заявки L0, не одобренные автоматической проверкой. После трёх завершённых диалоговых циклов ассистент также может напрямую выдать уровень L1.',
  ],
  vi: [
    'Chuẩn bị tạo khóa API',
    'Đã chuẩn bị tạo khóa API',
    'Đã chuẩn bị; cần xác nhận',
    'Xác nhận và gửi xét duyệt',
    'Hãy mô tả mục đích sử dụng cho trợ lý AI. Sau ba lượt trao đổi hoàn chỉnh, trợ lý có thể cấp thẳng L1; các yêu cầu gửi sớm hơn sẽ được xét duyệt tự động và chuyển cho người thật khi cần.',
    'Chỉ các yêu cầu L0 chưa được xét duyệt tự động chấp thuận mới xuất hiện ở đây. Trợ lý cũng có thể cấp thẳng L1 sau ba lượt trao đổi hoàn chỉnh.',
  ],
}

export const assistantToolCopy = Object.fromEntries(
  Object.entries(values).map(([locale, translations]) => [
    locale,
    Object.fromEntries(keys.map((key, index) => [key, translations[index]])),
  ])
)
