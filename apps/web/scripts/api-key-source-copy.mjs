/*
Copyright (C) 2026 LIghtJUNction
*/
const baseApiKeySourceCopy = {
  en: {
    'API key creation mode': 'API key creation mode',
    'Manual creation': 'Manual creation',
    'Automatic creation': 'Automatic creation',
    'Creation source': 'Creation source',
    Manual: 'Manual',
    System: 'System',
    Legacy: 'Legacy',
    'No automatically created API keys': 'No automatically created API keys',
    'Keys created by Drawing MCP, Assistant, and other connected tools appear here.':
      'Keys created by Drawing MCP, Assistant, and other connected tools appear here.',
    'Unable to prepare drawing API key': 'Unable to prepare drawing API key',
    'Drawing MCP API key created': 'Drawing MCP API key created',
    'Existing drawing API key selected': 'Existing drawing API key selected',
    'Prepare an image-2 API key here, then choose it in Drawing MCP settings.':
      'Prepare an image-2 API key here, then choose it in Drawing MCP settings.',
    'Preparing...': 'Preparing...',
    'Prepare API key': 'Prepare API key',
    'Open Drawing MCP settings': 'Open Drawing MCP settings',
  },
  zh: {
    'API key creation mode': 'API 密钥创建方式',
    'Manual creation': '手动创建',
    'Automatic creation': '自动创建',
    'Creation source': '创建来源',
    Manual: '手动',
    System: '系统',
    Legacy: '历史密钥',
    'No automatically created API keys': '暂无自动创建的 API 密钥',
    'Keys created by Drawing MCP, Assistant, and other connected tools appear here.':
      '由绘图 MCP、助手及其他已连接工具创建的密钥会显示在这里。',
    'Unable to prepare drawing API key': '无法准备绘图 API 密钥',
    'Drawing MCP API key created': '已创建绘图 MCP API 密钥',
    'Existing drawing API key selected': '已选择现有绘图 API 密钥',
    'Prepare an image-2 API key here, then choose it in Drawing MCP settings.':
      '在这里准备一个 image-2 API 密钥，然后在绘图 MCP 设置中选择它。',
    'Preparing...': '正在准备...',
    'Prepare API key': '准备 API 密钥',
    'Open Drawing MCP settings': '打开绘图 MCP 设置',
  },
  'zh-TW': {
    'API key creation mode': 'API 金鑰建立方式',
    'Manual creation': '手動建立',
    'Automatic creation': '自動建立',
    'Creation source': '建立來源',
    Manual: '手動',
    System: '系統',
    Legacy: '舊版金鑰',
    'No automatically created API keys': '暫無自動建立的 API 金鑰',
    'Keys created by Drawing MCP, Assistant, and other connected tools appear here.':
      '由繪圖 MCP、助理及其他已連線工具建立的金鑰會顯示在這裡。',
    'Unable to prepare drawing API key': '無法準備繪圖 API 金鑰',
    'Drawing MCP API key created': '已建立繪圖 MCP API 金鑰',
    'Existing drawing API key selected': '已選擇現有繪圖 API 金鑰',
    'Prepare an image-2 API key here, then choose it in Drawing MCP settings.':
      '在這裡準備一個 image-2 API 金鑰，然後在繪圖 MCP 設定中選擇它。',
    'Preparing...': '正在準備...',
    'Prepare API key': '準備 API 金鑰',
    'Open Drawing MCP settings': '開啟繪圖 MCP 設定',
  },
  fr: {
    'API key creation mode': 'Mode de création des clés API',
    'Manual creation': 'Création manuelle',
    'Automatic creation': 'Création automatique',
    'Creation source': 'Source de création',
    Manual: 'Manuelle',
    System: 'Système',
    Legacy: 'Ancienne clé',
    'No automatically created API keys': 'Aucune clé API créée automatiquement',
    'Keys created by Drawing MCP, Assistant, and other connected tools appear here.':
      'Les clés créées par Drawing MCP, l’assistant et d’autres outils connectés apparaissent ici.',
    'Unable to prepare drawing API key':
      'Impossible de préparer la clé API de dessin',
    'Drawing MCP API key created': 'Clé API Drawing MCP créée',
    'Existing drawing API key selected':
      'Clé API de dessin existante sélectionnée',
    'Prepare an image-2 API key here, then choose it in Drawing MCP settings.':
      'Préparez ici une clé API image-2, puis sélectionnez-la dans les paramètres Drawing MCP.',
    'Preparing...': 'Préparation...',
    'Prepare API key': 'Préparer la clé API',
    'Open Drawing MCP settings': 'Ouvrir les paramètres Drawing MCP',
  },
  ja: {
    'API key creation mode': 'API キーの作成方法',
    'Manual creation': '手動作成',
    'Automatic creation': '自動作成',
    'Creation source': '作成元',
    Manual: '手動',
    System: 'システム',
    Legacy: '従来のキー',
    'No automatically created API keys': '自動作成された API キーはありません',
    'Keys created by Drawing MCP, Assistant, and other connected tools appear here.':
      'Drawing MCP、アシスタント、その他の接続済みツールが作成したキーがここに表示されます。',
    'Unable to prepare drawing API key': '描画 API キーを準備できません',
    'Drawing MCP API key created': 'Drawing MCP API キーを作成しました',
    'Existing drawing API key selected': '既存の描画 API キーを選択しました',
    'Prepare an image-2 API key here, then choose it in Drawing MCP settings.':
      'ここで image-2 API キーを準備し、Drawing MCP 設定で選択します。',
    'Preparing...': '準備中...',
    'Prepare API key': 'API キーを準備',
    'Open Drawing MCP settings': 'Drawing MCP 設定を開く',
  },
  ru: {
    'API key creation mode': 'Способ создания API-ключа',
    'Manual creation': 'Созданные вручную',
    'Automatic creation': 'Созданные автоматически',
    'Creation source': 'Источник создания',
    Manual: 'Вручную',
    System: 'Система',
    Legacy: 'Старый ключ',
    'No automatically created API keys':
      'Нет автоматически созданных API-ключей',
    'Keys created by Drawing MCP, Assistant, and other connected tools appear here.':
      'Здесь отображаются ключи, созданные Drawing MCP, помощником и другими подключёнными инструментами.',
    'Unable to prepare drawing API key':
      'Не удалось подготовить API-ключ для рисования',
    'Drawing MCP API key created': 'API-ключ Drawing MCP создан',
    'Existing drawing API key selected':
      'Выбран существующий API-ключ для рисования',
    'Prepare an image-2 API key here, then choose it in Drawing MCP settings.':
      'Подготовьте здесь API-ключ image-2, затем выберите его в настройках Drawing MCP.',
    'Preparing...': 'Подготовка...',
    'Prepare API key': 'Подготовить API-ключ',
    'Open Drawing MCP settings': 'Открыть настройки Drawing MCP',
  },
  vi: {
    'API key creation mode': 'Cách tạo khóa API',
    'Manual creation': 'Tạo thủ công',
    'Automatic creation': 'Tạo tự động',
    'Creation source': 'Nguồn tạo',
    Manual: 'Thủ công',
    System: 'Hệ thống',
    Legacy: 'Khóa cũ',
    'No automatically created API keys':
      'Chưa có khóa API nào được tạo tự động',
    'Keys created by Drawing MCP, Assistant, and other connected tools appear here.':
      'Khóa do Drawing MCP, Trợ lý và các công cụ đã kết nối khác tạo sẽ xuất hiện tại đây.',
    'Unable to prepare drawing API key': 'Không thể chuẩn bị khóa API vẽ',
    'Drawing MCP API key created': 'Đã tạo khóa API Drawing MCP',
    'Existing drawing API key selected': 'Đã chọn khóa API vẽ hiện có',
    'Prepare an image-2 API key here, then choose it in Drawing MCP settings.':
      'Chuẩn bị khóa API image-2 tại đây, sau đó chọn khóa trong phần cài đặt Drawing MCP.',
    'Preparing...': 'Đang chuẩn bị...',
    'Prepare API key': 'Chuẩn bị khóa API',
    'Open Drawing MCP settings': 'Mở cài đặt Drawing MCP',
  },
}

const quickCreateCopy = {
  en: {
    'Unable to prepare drawing API key': 'Unable to prepare drawing API key',
    'Drawing MCP API key created': 'Drawing MCP API key created',
    'Existing drawing API key selected': 'Existing drawing API key selected',
    'Prepare an image-2 API key here, then choose it in Drawing MCP settings.':
      'Prepare an image-2 API key here, then choose it in Drawing MCP settings.',
    'Preparing...': 'Preparing...',
    'Prepare API key': 'Prepare API key',
    'Open Drawing MCP settings': 'Open Drawing MCP settings',
  },
  zh: {
    'Unable to prepare drawing API key': '无法准备绘图 API 密钥',
    'Drawing MCP API key created': '已创建绘图 MCP API 密钥',
    'Existing drawing API key selected': '已选择现有绘图 API 密钥',
    'Prepare an image-2 API key here, then choose it in Drawing MCP settings.':
      '在这里准备一个 image-2 API 密钥，然后在绘图 MCP 设置中选择它。',
    'Preparing...': '正在准备...',
    'Prepare API key': '准备 API 密钥',
    'Open Drawing MCP settings': '打开绘图 MCP 设置',
  },
  'zh-TW': {
    'Unable to prepare drawing API key': '無法準備繪圖 API 金鑰',
    'Drawing MCP API key created': '已建立繪圖 MCP API 金鑰',
    'Existing drawing API key selected': '已選擇現有繪圖 API 金鑰',
    'Prepare an image-2 API key here, then choose it in Drawing MCP settings.':
      '在這裡準備一個 image-2 API 金鑰，然後在繪圖 MCP 設定中選擇它。',
    'Preparing...': '正在準備...',
    'Prepare API key': '準備 API 金鑰',
    'Open Drawing MCP settings': '開啟繪圖 MCP 設定',
  },
  fr: {
    'Unable to prepare drawing API key':
      'Impossible de préparer la clé API de dessin',
    'Drawing MCP API key created': 'Clé API Drawing MCP créée',
    'Existing drawing API key selected':
      'Clé API de dessin existante sélectionnée',
    'Prepare an image-2 API key here, then choose it in Drawing MCP settings.':
      'Préparez ici une clé API image-2, puis sélectionnez-la dans les paramètres Drawing MCP.',
    'Preparing...': 'Préparation...',
    'Prepare API key': 'Préparer la clé API',
    'Open Drawing MCP settings': 'Ouvrir les paramètres Drawing MCP',
  },
  ja: {
    'Unable to prepare drawing API key': '描画 API キーを準備できません',
    'Drawing MCP API key created': 'Drawing MCP API キーを作成しました',
    'Existing drawing API key selected': '既存の描画 API キーを選択しました',
    'Prepare an image-2 API key here, then choose it in Drawing MCP settings.':
      'ここで image-2 API キーを準備し、Drawing MCP 設定で選択します。',
    'Preparing...': '準備中...',
    'Prepare API key': 'API キーを準備',
    'Open Drawing MCP settings': 'Drawing MCP 設定を開く',
  },
  ru: {
    'Unable to prepare drawing API key':
      'Не удалось подготовить API-ключ для рисования',
    'Drawing MCP API key created': 'API-ключ Drawing MCP создан',
    'Existing drawing API key selected':
      'Выбран существующий API-ключ для рисования',
    'Prepare an image-2 API key here, then choose it in Drawing MCP settings.':
      'Подготовьте здесь API-ключ image-2, затем выберите его в настройках Drawing MCP.',
    'Preparing...': 'Подготовка...',
    'Prepare API key': 'Подготовить API-ключ',
    'Open Drawing MCP settings': 'Открыть настройки Drawing MCP',
  },
  vi: {
    'Unable to prepare drawing API key': 'Không thể chuẩn bị khóa API vẽ',
    'Drawing MCP API key created': 'Đã tạo khóa API Drawing MCP',
    'Existing drawing API key selected': 'Đã chọn khóa API vẽ hiện có',
    'Prepare an image-2 API key here, then choose it in Drawing MCP settings.':
      'Chuẩn bị khóa API image-2 tại đây, sau đó chọn khóa trong phần cài đặt Drawing MCP.',
    'Preparing...': 'Đang chuẩn bị...',
    'Prepare API key': 'Chuẩn bị khóa API',
    'Open Drawing MCP settings': 'Mở cài đặt Drawing MCP',
  },
}

const automaticKeyOriginsCopy = {
  en: {
    'Created with assistant': 'Created with assistant',
    'Red packet cover': 'Red packet cover',
    'The assistant runtime key belongs to the super administrator. Keys the assistant creates for your client appear in your account after you confirm them.':
      'The assistant runtime key belongs to the super administrator. Keys the assistant creates for your client appear in your account after you confirm them.',
    'Ask assistant to create a key': 'Ask assistant to create a key',
    'Generating a red packet cover creates an API key for the selected group on first use. The key then appears below.':
      'Generating a red packet cover creates an API key for the selected group on first use. The key then appears below.',
    'Open Red Packets': 'Open Red Packets',
    'Keys created for site features appear here. Keys created with the assistant appear after your confirmation.':
      'Keys created for site features appear here. Keys created with the assistant appear after your confirmation.',
  },
  zh: {
    'Created with assistant': '助手创建',
    'Red packet cover': '红包封面',
    'The assistant runtime key belongs to the super administrator. Keys the assistant creates for your client appear in your account after you confirm them.':
      '助手运行密钥属于超级管理员。助手为你的客户端创建的密钥，经你确认后会显示在你的账户里。',
    'Ask assistant to create a key': '让助手创建密钥',
    'Generating a red packet cover creates an API key for the selected group on first use. The key then appears below.':
      '首次生成红包封面时，会为所选分组创建 API 密钥，随后显示在下方。',
    'Open Red Packets': '打开红包',
    'Keys created for site features appear here. Keys created with the assistant appear after your confirmation.':
      '网站功能创建的密钥会显示在这里。经你确认，由助手创建的密钥也会显示在这里。',
  },
  'zh-TW': {
    'Created with assistant': '助理建立',
    'Red packet cover': '紅包封面',
    'The assistant runtime key belongs to the super administrator. Keys the assistant creates for your client appear in your account after you confirm them.':
      '助理執行金鑰屬於超級管理員。助理為你的客戶端建立的金鑰，經你確認後會顯示在你的帳戶中。',
    'Ask assistant to create a key': '請助理建立金鑰',
    'Generating a red packet cover creates an API key for the selected group on first use. The key then appears below.':
      '首次產生紅包封面時，會為所選分組建立 API 金鑰，隨後顯示在下方。',
    'Open Red Packets': '開啟紅包',
    'Keys created for site features appear here. Keys created with the assistant appear after your confirmation.':
      '網站功能建立的金鑰會顯示在這裡。經你確認，由助理建立的金鑰也會顯示在這裡。',
  },
  fr: {
    'Created with assistant': 'Créée avec l’assistant',
    'Red packet cover': 'Couverture de paquet cadeau',
    'The assistant runtime key belongs to the super administrator. Keys the assistant creates for your client appear in your account after you confirm them.':
      'La clé interne de l’assistant appartient au super administrateur. Les clés créées pour votre client apparaissent dans votre compte après confirmation.',
    'Ask assistant to create a key': 'Demander une clé à l’assistant',
    'Generating a red packet cover creates an API key for the selected group on first use. The key then appears below.':
      'La première génération d’une couverture crée une clé API pour le groupe choisi. La clé apparaît ensuite ci-dessous.',
    'Open Red Packets': 'Ouvrir les paquets cadeaux',
    'Keys created for site features appear here. Keys created with the assistant appear after your confirmation.':
      'Les clés créées pour les fonctionnalités du site apparaissent ici. Celles créées avec l’assistant apparaissent après votre confirmation.',
  },
  ja: {
    'Created with assistant': 'アシスタントで作成',
    'Red packet cover': 'お年玉のカバー画像',
    'The assistant runtime key belongs to the super administrator. Keys the assistant creates for your client appear in your account after you confirm them.':
      'アシスタントの実行用キーはスーパー管理者に属します。クライアント用に作成されたキーは、確認後にあなたのアカウントへ表示されます。',
    'Ask assistant to create a key': 'アシスタントにキー作成を依頼',
    'Generating a red packet cover creates an API key for the selected group on first use. The key then appears below.':
      '初めてカバー画像を生成すると、選択したグループの API キーが作成され、下に表示されます。',
    'Open Red Packets': 'お年玉を開く',
    'Keys created for site features appear here. Keys created with the assistant appear after your confirmation.':
      'サイトの機能で作成されたキーはここに表示されます。アシスタントで作成したキーは確認後に表示されます。',
  },
  ru: {
    'Created with assistant': 'Создан помощником',
    'Red packet cover': 'Обложка подарочного пакета',
    'The assistant runtime key belongs to the super administrator. Keys the assistant creates for your client appear in your account after you confirm them.':
      'Внутренний ключ помощника принадлежит супер-администратору. Ключи для вашего клиента появятся в вашем аккаунте после подтверждения.',
    'Ask assistant to create a key': 'Попросить помощника создать ключ',
    'Generating a red packet cover creates an API key for the selected group on first use. The key then appears below.':
      'При первой генерации обложки создаётся API-ключ для выбранной группы. Затем он появится ниже.',
    'Open Red Packets': 'Открыть подарочные пакеты',
    'Keys created for site features appear here. Keys created with the assistant appear after your confirmation.':
      'Здесь отображаются ключи, созданные для функций сайта. Ключи от помощника появятся после вашего подтверждения.',
  },
  vi: {
    'Created with assistant': 'Tạo bằng trợ lý',
    'Red packet cover': 'Ảnh bìa lì xì',
    'The assistant runtime key belongs to the super administrator. Keys the assistant creates for your client appear in your account after you confirm them.':
      'Khóa chạy nội bộ của trợ lý thuộc tài khoản siêu quản trị. Khóa trợ lý tạo cho ứng dụng của bạn sẽ hiện trong tài khoản sau khi bạn xác nhận.',
    'Ask assistant to create a key': 'Nhờ trợ lý tạo khóa',
    'Generating a red packet cover creates an API key for the selected group on first use. The key then appears below.':
      'Lần đầu tạo ảnh bìa lì xì sẽ tạo khóa API cho nhóm đã chọn. Sau đó khóa sẽ hiện bên dưới.',
    'Open Red Packets': 'Mở Lì xì',
    'Keys created for site features appear here. Keys created with the assistant appear after your confirmation.':
      'Khóa được tạo cho các tính năng của trang sẽ hiện ở đây. Khóa do trợ lý tạo sẽ hiện sau khi bạn xác nhận.',
  },
}

const assistantRuntimeCopy = {
  en: {
    'AI assistant runtime': 'AI assistant runtime',
    'The assistant uses an internal key owned by this super administrator. It is created automatically and appears below, but cannot be used for ordinary API calls.':
      'The assistant uses an internal key owned by this super administrator. It is created automatically and appears below, but cannot be used for ordinary API calls.',
    'Internal assistant key; it cannot be copied or used for API calls.':
      'Internal assistant key; it cannot be copied or used for API calls.',
    'Billed to super administrator wallet':
      'Billed to super administrator wallet',
    'Tracked in assistant funding': 'Tracked in assistant funding',
    'AI assistant is disabled': 'AI assistant is disabled',
    'Unable to prepare assistant runtime key':
      'Unable to prepare assistant runtime key',
  },
  zh: {
    'AI assistant runtime': 'AI 助手运行密钥',
    'The assistant uses an internal key owned by this super administrator. It is created automatically and appears below, but cannot be used for ordinary API calls.':
      '助手使用这位超级管理员账户名下的内部密钥。它会自动创建并显示在下方，但不能用于普通 API 调用。',
    'Internal assistant key; it cannot be copied or used for API calls.':
      '助手内部密钥；无法复制，也不能用于 API 调用。',
    'Billed to super administrator wallet': '从超级管理员钱包扣费',
    'Tracked in assistant funding': '在助手经费中统计',
    'AI assistant is disabled': 'AI 助手已停用',
    'Unable to prepare assistant runtime key': '无法准备 AI 助手运行密钥',
  },
  'zh-TW': {
    'AI assistant runtime': 'AI 助理執行金鑰',
    'The assistant uses an internal key owned by this super administrator. It is created automatically and appears below, but cannot be used for ordinary API calls.':
      '助理使用這位超級管理員帳戶的內部金鑰。金鑰會自動建立並顯示於下方，但無法用於一般 API 呼叫。',
    'Internal assistant key; it cannot be copied or used for API calls.':
      '助理內部金鑰；無法複製，也不能用於 API 呼叫。',
    'Billed to super administrator wallet': '由超級管理員錢包付費',
    'Tracked in assistant funding': '計入助理經費',
    'AI assistant is disabled': 'AI 助理已停用',
    'Unable to prepare assistant runtime key': '無法準備 AI 助理執行金鑰',
  },
  fr: {
    'AI assistant runtime': 'Clé interne de l’assistant IA',
    'The assistant uses an internal key owned by this super administrator. It is created automatically and appears below, but cannot be used for ordinary API calls.':
      'L’assistant utilise une clé interne de ce super administrateur. Elle est créée automatiquement et apparaît ci-dessous, sans pouvoir servir aux appels API ordinaires.',
    'Internal assistant key; it cannot be copied or used for API calls.':
      'Clé interne de l’assistant ; elle ne peut être ni copiée ni utilisée pour des appels API.',
    'Billed to super administrator wallet':
      'Facturé au portefeuille du super administrateur',
    'Tracked in assistant funding': 'Suivi dans les dépenses de l’assistant',
    'AI assistant is disabled': 'L’assistant IA est désactivé',
    'Unable to prepare assistant runtime key':
      'Impossible de préparer la clé interne de l’assistant',
  },
  ja: {
    'AI assistant runtime': 'AI アシスタント実行用キー',
    'The assistant uses an internal key owned by this super administrator. It is created automatically and appears below, but cannot be used for ordinary API calls.':
      'アシスタントはこのスーパー管理者の内部キーを使用します。キーは自動作成されて下に表示されますが、通常の API 呼び出しには使えません。',
    'Internal assistant key; it cannot be copied or used for API calls.':
      'アシスタントの内部キーです。コピーや API 呼び出しには使用できません。',
    'Billed to super administrator wallet': 'スーパー管理者の残高から請求',
    'Tracked in assistant funding': 'アシスタント費用に計上',
    'AI assistant is disabled': 'AI アシスタントは無効です',
    'Unable to prepare assistant runtime key':
      'アシスタント実行用キーを準備できません',
  },
  ru: {
    'AI assistant runtime': 'Внутренний ключ ИИ-помощника',
    'The assistant uses an internal key owned by this super administrator. It is created automatically and appears below, but cannot be used for ordinary API calls.':
      'Помощник использует внутренний ключ этого супер-администратора. Он создаётся автоматически и появляется ниже, но не подходит для обычных API-запросов.',
    'Internal assistant key; it cannot be copied or used for API calls.':
      'Внутренний ключ помощника: его нельзя копировать или использовать для API-запросов.',
    'Billed to super administrator wallet':
      'Оплачивается из кошелька супер-администратора',
    'Tracked in assistant funding': 'Учтено в расходах помощника',
    'AI assistant is disabled': 'ИИ-помощник отключён',
    'Unable to prepare assistant runtime key':
      'Не удалось подготовить внутренний ключ помощника',
  },
  vi: {
    'AI assistant runtime': 'Khóa nội bộ của trợ lý AI',
    'The assistant uses an internal key owned by this super administrator. It is created automatically and appears below, but cannot be used for ordinary API calls.':
      'Trợ lý dùng khóa nội bộ thuộc tài khoản siêu quản trị này. Khóa được tạo tự động và hiện bên dưới, nhưng không thể dùng cho lệnh gọi API thông thường.',
    'Internal assistant key; it cannot be copied or used for API calls.':
      'Khóa nội bộ của trợ lý; không thể sao chép hoặc dùng để gọi API.',
    'Billed to super administrator wallet': 'Tính phí vào ví siêu quản trị',
    'Tracked in assistant funding': 'Theo dõi trong chi phí trợ lý',
    'AI assistant is disabled': 'Trợ lý AI đã tắt',
    'Unable to prepare assistant runtime key':
      'Không thể chuẩn bị khóa nội bộ của trợ lý',
  },
}

export const apiKeySourceCopy = Object.fromEntries(
  Object.entries(baseApiKeySourceCopy).map(([locale, translations]) => [
    locale,
    {
      ...translations,
      ...quickCreateCopy[locale],
      ...automaticKeyOriginsCopy[locale],
      ...assistantRuntimeCopy[locale],
    },
  ])
)
