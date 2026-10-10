/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import type { i18n } from 'i18next'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

export const providerNamespace = 'tool-market-providers'
const languages = ['en', 'zhCN', 'zhTW', 'fr', 'ja', 'ru', 'vi'] as const
const rows = [
  [
    'Built-in gateway tool',
    '内置入口工具',
    '內建入口工具',
    'Outil intégré de la passerelle',
    '内蔵ゲートウェイツール',
    'Встроенный инструмент шлюза',
    'Công cụ cổng tích hợp',
  ],
  [
    'Available after connecting. Discover tools, inspect their details, then load and invoke them through one entry.',
    '连接后即可使用。通过一个入口发现工具、查看详情、加载并执行。',
    '連線後即可使用。透過一個入口探索工具、查看詳情、載入並執行。',
    'Disponible après connexion. Découvrez les outils, consultez leurs détails, puis chargez-les et exécutez-les depuis une seule entrée.',
    '接続後に利用できます。1 つの入口でツールの検索、詳細確認、読み込み、実行ができます。',
    'Доступен после подключения. Находите инструменты, смотрите описание, загружайте и вызывайте через единый вход.',
    'Dùng sau khi kết nối. Tìm công cụ, xem chi tiết, tải và gọi qua một điểm truy cập.',
  ],
  [
    'Connect MCP client',
    '接入 MCP 客户端',
    '接入 MCP 用戶端',
    'Connecter un client MCP',
    'MCP クライアントを接続',
    'Подключить клиент MCP',
    'Kết nối ứng dụng MCP',
  ],
  [
    'Discover → Inspect → Load → Authorize → Invoke',
    '发现 → 详情 → 加载 → 授权 → 执行',
    '探索 → 詳情 → 載入 → 授權 → 執行',
    'Découvrir → Examiner → Charger → Autoriser → Exécuter',
    '検索 → 詳細 → 読み込み → 許可 → 実行',
    'Поиск → Описание → Загрузка → Разрешение → Вызов',
    'Tìm → Xem → Tải → Cấp quyền → Gọi',
  ],
  [
    'Management is free. Invocation uses the selected tool price. Loading does not grant payment permission.',
    '管理操作免费。执行按所选工具计费。加载不等于授权付费。',
    '管理操作免費。執行依所選工具計費。載入不等於授權付費。',
    'La gestion est gratuite. L’exécution suit le tarif de l’outil. Le chargement n’autorise pas le paiement.',
    '管理操作は無料です。実行には選択したツールの料金がかかります。読み込みは支払いの許可ではありません。',
    'Управление бесплатно. Вызов оплачивается по цене инструмента. Загрузка не разрешает оплату.',
    'Quản lý miễn phí. Gọi theo giá công cụ đã chọn. Tải không cấp quyền thanh toán.',
  ],
  [
    'View actions and parameters',
    '查看操作与参数',
    '查看操作與參數',
    'Voir les actions et paramètres',
    '操作とパラメーターを表示',
    'Действия и параметры',
    'Xem thao tác và tham số',
  ],
  [
    'Add this server URL in an MCP client, choose OAuth, and approve access in your browser. No pasted token is needed.',
    '在 MCP 客户端添加此服务器地址，选择 OAuth，再到浏览器确认授权。不需要粘贴令牌。',
    '在 MCP 用戶端新增此伺服器位址，選擇 OAuth，再到瀏覽器確認授權。不需要貼上權杖。',
    'Ajoutez cette URL dans un client MCP, choisissez OAuth et autorisez l’accès dans le navigateur. Aucun jeton à coller.',
    'MCP クライアントにこの URL を追加し、OAuth を選択してブラウザーで許可します。トークンの貼り付けは不要です。',
    'Добавьте URL сервера в клиент MCP, выберите OAuth и подтвердите доступ в браузере. Вставлять токен не нужно.',
    'Thêm URL máy chủ vào ứng dụng MCP, chọn OAuth và xác nhận trong trình duyệt. Không cần dán token.',
  ],
  [
    'MCP tool list',
    'MCP 工具列表',
    'MCP 工具清單',
    'Liste des outils MCP',
    'MCP ツール一覧',
    'Список инструментов MCP',
    'Danh sách công cụ MCP',
  ],
  [
    'Compact: metamcp only',
    '精简：仅显示 metamcp',
    '精簡：僅顯示 metamcp',
    'Compact : metamcp seul',
    '簡潔：metamcp のみ',
    'Кратко: только metamcp',
    'Gọn: chỉ metamcp',
  ],
  [
    'Full: individual tools too',
    '完整：同时显示独立工具',
    '完整：同時顯示獨立工具',
    'Complet : outils individuels aussi',
    '完全：個別ツールも表示',
    'Полно: также отдельные инструменты',
    'Đầy đủ: thêm từng công cụ',
  ],
  [
    'Copy server URL',
    '复制服务器地址',
    '複製伺服器位址',
    'Copier l’URL du serveur',
    'サーバー URL をコピー',
    'Копировать URL сервера',
    'Sao chép URL máy chủ',
  ],

  [
    'Provider preset',
    '平台预设',
    '平台預設',
    'Plateforme prédéfinie',
    'プラットフォームのプリセット',
    'Шаблон платформы',
    'Nền tảng có sẵn',
  ],
  [
    'Custom MCP service',
    '自定义 MCP 服务',
    '自訂 MCP 服務',
    'Service MCP personnalisé',
    'カスタム MCP サービス',
    'Свой сервис MCP',
    'Dịch vụ MCP tùy chỉnh',
  ],
  [
    'Price multiplier',
    '价格倍率',
    '價格倍率',
    'Multiplicateur de prix',
    '価格倍率',
    'Множитель цены',
    'Hệ số giá',
  ],
  [
    'Upstream quote × multiplier',
    '上游报价 × 倍率',
    '上游報價 × 倍率',
    'Prix fournisseur × multiplicateur',
    '上流の見積価格 × 倍率',
    'Цена поставщика × множитель',
    'Giá nhà cung cấp × hệ số',
  ],
  [
    'Choose a provider, enter its API key, then review the tools and spending ceiling. Your customers sign in to LMM separately; they do not receive this key.',
    '选择平台，填写 API Key，再检查工具和单次收费上限。买家登录 LMM，不会获得这个密钥。',
    '選擇平台，填寫 API Key，再檢查工具和單次收費上限。買家登入 LMM，不會取得這個金鑰。',
    'Choisissez une plateforme, entrez sa clé API et vérifiez les outils et le plafond. Les clients se connectent à LMM sans recevoir cette clé.',
    'プラットフォームと API キーを設定し、ツールと料金上限を確認します。購入者は LMM にログインします。このキーは渡されません。',
    'Выберите платформу, введите ключ API и проверьте инструменты и лимит. Покупатель входит в LMM и не получает этот ключ.',
    'Chọn nền tảng, nhập khóa API rồi kiểm tra công cụ và giới hạn phí. Khách hàng đăng nhập LMM và không nhận khóa này.',
  ],
  [
    'Sale price = upstream USD quote × multiplier. Calls are blocked when the quote is missing, exceeds a spending limit, or the platform fee would make the sale fall below cost.',
    '售价 = 上游美元报价 × 倍率。缺少报价、超出上限，或扣除平台费后不足以覆盖报价时，会阻止调用。',
    '售價 = 上游美元報價 × 倍率。缺少報價、超出上限，或扣除平台費後不足以支付成本時，會阻止呼叫。',
    'Prix de vente = devis USD × multiplicateur. Les appels sont bloqués si le devis manque, dépasse le plafond ou ne couvre pas le coût après les frais.',
    '販売価格 = 上流のドル見積価格 × 倍率。見積なし、上限超過、手数料控除後の原価割れでは呼び出しを停止します。',
    'Цена продажи = котировка USD × множитель. Вызов блокируется без котировки, при превышении лимита или убытке после комиссии.',
    'Giá bán = báo giá USD × hệ số. Chặn lệnh gọi khi thiếu báo giá, vượt hạn mức hoặc không đủ bù chi phí sau phí nền tảng.',
  ],
  [
    'The server checks the current USD quote before each call. This amount is a spending ceiling, not a fixed charge. Variable-price tools are blocked until a spending bound can be verified.',
    '每次调用前会查询美元报价。这里是收费上限，不是固定售价。无法确认最高费用的变动计费工具暂不可调用。',
    '每次呼叫前會查詢美元報價。這裡是收費上限，不是固定售價。無法確認最高費用的浮動計費工具暫不可呼叫。',
    'Le devis USD est vérifié avant chaque appel. Ce montant est un plafond, pas un prix fixe. Les tarifs variables sans limite vérifiable sont bloqués.',
    '呼び出し前にドル見積価格を確認します。この金額は固定料金ではなく上限です。最大費用を確認できない変動料金のツールは利用できません。',
    'Котировка USD проверяется до вызова. Эта сумма — лимит, не фиксированная цена. Переменные тарифы без проверяемого предела блокируются.',
    'Kiểm tra báo giá USD trước mỗi lần gọi. Đây là mức trần, không phải phí cố định. Chặn công cụ có phí biến đổi mà không xác minh được mức tối đa.',
  ],
  [
    'Manual connection token',
    '手动配置连接令牌',
    '手動設定連線權杖',
    'Jeton de connexion manuel',
    '手動接続トークン',
    'Ручная настройка токена',
    'Token kết nối thủ công',
  ],
  [
    'Browser login (recommended)',
    '浏览器登录（推荐）',
    '瀏覽器登入（建議）',
    'Connexion par navigateur (recommandée)',
    'ブラウザーでログイン（推奨）',
    'Вход через браузер (рекомендуется)',
    'Đăng nhập bằng trình duyệt (khuyên dùng)',
  ],
  [
    'Run the command, sign in to LMM, and approve access. Your provider key is not sent to the client.',
    '运行命令，登录 LMM 并确认授权。供应商密钥不会发送给客户端。',
    '執行命令，登入 LMM 並確認授權。供應商金鑰不會傳送給用戶端。',
    'Exécutez la commande, connectez-vous à LMM et autorisez l’accès. La clé fournisseur reste sur le serveur.',
    'コマンドを実行し、LMM にログインしてアクセスを許可します。プロバイダーのキーはクライアントに送信されません。',
    'Выполните команду, войдите в LMM и подтвердите доступ. Ключ поставщика не передаётся клиенту.',
    'Chạy lệnh, đăng nhập LMM và cấp quyền. Khóa nhà cung cấp không được gửi cho ứng dụng.',
  ],
  [
    'After login, select this OAuth client and set tool access and spending limits. Login alone does not authorize spending.',
    '登录后选择此 OAuth 客户端，设置可用工具和消费上限。仅登录不会授予消费权限。',
    '登入後選擇此 OAuth 用戶端，設定可用工具和消費上限。僅登入不會授予消費權限。',
    'Après connexion, choisissez ce client OAuth et fixez ses outils et plafonds. La connexion seule ne permet aucune dépense.',
    'ログイン後、この OAuth クライアントのツールと利用額の上限を設定します。ログインだけでは支出は許可されません。',
    'После входа выберите OAuth-клиент и задайте инструменты и лимиты. Сам вход не разрешает расходы.',
    'Sau khi đăng nhập, chọn ứng dụng OAuth này và đặt quyền công cụ cùng hạn mức. Đăng nhập không tự cấp quyền chi tiêu.',
  ],
  [
    'Live upstream price × multiplier; capped per call',
    '按上游实时报价乘以倍率计费，每次调用设有上限',
    '依上游即時報價乘以倍率計費，每次呼叫設有上限',
    'Prix fournisseur actuel × multiplicateur ; plafond par appel',
    '上流の現在価格 × 倍率。呼び出しごとに上限あり',
    'Текущая цена × множитель; лимит на каждый вызов',
    'Giá hiện tại × hệ số; có mức trần mỗi lần gọi',
  ],
  [
    'Upstream price × {{multiplier}}; maximum {{amount}} per call',
    '上游价格 × {{multiplier}}；每次最多 {{amount}}',
    '上游價格 × {{multiplier}}；每次最多 {{amount}}',
    'Prix fournisseur × {{multiplier}} ; maximum {{amount}} par appel',
    '上流価格 × {{multiplier}}。1 回の上限 {{amount}}',
    'Цена поставщика × {{multiplier}}; максимум {{amount}} за вызов',
    'Giá nhà cung cấp × {{multiplier}}; tối đa {{amount}} mỗi lần gọi',
  ],
] as const

export const providerTranslations = Object.fromEntries(
  languages.map((language, index) => [
    language,
    Object.fromEntries(rows.map((values) => [values[0], values[index]])),
  ])
) as Record<(typeof languages)[number], Record<string, string>>

const registered = new WeakSet<i18n>()
export function registerProviderTranslations(instance: i18n): void {
  if (registered.has(instance)) return
  for (const language of languages) {
    instance.addResourceBundle(
      language,
      providerNamespace,
      providerTranslations[language],
      true,
      false
    )
  }
  registered.add(instance)
}

// Keep feature copy out of the global locale files. Using a separate namespace
// also avoids marking an asynchronously loaded global locale as already loaded.
export function useMarketTranslation() {
  const translation = useTranslation()
  const { i18n } = translation
  registerProviderTranslations(i18n)
  const language = i18n.resolvedLanguage || i18n.language
  const t = useMemo(
    () => i18n.getFixedT(language, [providerNamespace, 'translation']),
    [i18n, language]
  )
  return { ...translation, t }
}
