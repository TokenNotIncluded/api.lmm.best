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

export const apiKeySourceCopy = Object.fromEntries(
  Object.entries(baseApiKeySourceCopy).map(([locale, translations]) => [
    locale,
    { ...translations, ...quickCreateCopy[locale] },
  ])
)
