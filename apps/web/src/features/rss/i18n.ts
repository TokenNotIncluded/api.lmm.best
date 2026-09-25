/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import i18next from 'i18next'

const en = {
  'Manage feeds': 'Manage feeds',
  'Search articles': 'Search articles',
  'All feeds': 'All feeds',
  '{{feeds}} feeds · {{articles}} articles':
    '{{feeds}} feeds · {{articles}} articles',
  'Loading articles...': 'Loading articles...',
  'Unable to load RSS feeds': 'Unable to load RSS feeds',
  'Try loading the feeds again.': 'Try loading the feeds again.',
  'No RSS feeds are configured yet.': 'No RSS feeds are configured yet.',
  'No articles match your filters.': 'No articles match your filters.',
  'Clear filters': 'Clear filters',
  'Some feeds could not be refreshed.': 'Some feeds could not be refreshed.',
  'Configure the RSS and Atom sources shown in the reader. Changes are shared with all users.':
    'Configure the RSS and Atom sources shown in the reader. Changes are shared with all users.',
  'Add feed': 'Add feed',
  'Save feeds': 'Save feeds',
  'No feeds are configured. Add one to start the reader.':
    'No feeds are configured. Add one to start the reader.',
  'New feed': 'New feed',
  'Feed name': 'Feed name',
  'Feed URL': 'Feed URL',
  'Fetch this feed': 'Fetch this feed',
  'Disabled feeds stay saved but are not fetched.':
    'Disabled feeds stay saved but are not fetched.',
  'Enter a feed name (up to 80 characters).':
    'Enter a feed name (up to 80 characters).',
  'Enter a valid HTTP or HTTPS feed URL.':
    'Enter a valid HTTP or HTTPS feed URL.',
  'This feed URL is already configured.':
    'This feed URL is already configured.',
  'Check the highlighted feed.': 'Check the highlighted feed.',
  'Move {{name}} up': 'Move {{name}} up',
  'Move {{name}} down': 'Move {{name}} down',
  'Remove {{name}}': 'Remove {{name}}',
} as const

export const rssTranslations = {
  en,
  zhCN: {
    'Manage feeds': '管理订阅源',
    'Search articles': '搜索文章',
    'All feeds': '全部订阅',
    '{{feeds}} feeds · {{articles}} articles': '{{feeds}} 个订阅 · {{articles}} 篇文章',
    'Loading articles...': '正在加载文章...',
    'Unable to load RSS feeds': '无法加载 RSS 订阅',
    'Try loading the feeds again.': '请重试加载订阅。',
    'No RSS feeds are configured yet.': '还没有配置 RSS 订阅。',
    'No articles match your filters.': '没有符合筛选条件的文章。',
    'Clear filters': '清除筛选',
    'Some feeds could not be refreshed.': '部分订阅源暂时无法刷新。',
    'Configure the RSS and Atom sources shown in the reader. Changes are shared with all users.':
      '配置阅读页中显示的 RSS 与 Atom 订阅源，保存后对所有用户生效。',
    'Add feed': '添加订阅',
    'Save feeds': '保存订阅',
    'No feeds are configured. Add one to start the reader.':
      '尚未配置订阅源，添加一个即可开始使用。',
    'New feed': '新订阅',
    'Feed name': '订阅名称',
    'Feed URL': '订阅地址',
    'Fetch this feed': '抓取此订阅',
    'Disabled feeds stay saved but are not fetched.':
      '关闭后仍保留配置，但不会抓取内容。',
    'Enter a feed name (up to 80 characters).': '请输入订阅名称（最多 80 个字符）。',
    'Enter a valid HTTP or HTTPS feed URL.': '请输入有效的 HTTP 或 HTTPS 订阅地址。',
    'This feed URL is already configured.': '这个订阅地址已经配置过了。',
    'Check the highlighted feed.': '请检查标出的订阅项。',
    'Move {{name}} up': '上移 {{name}}',
    'Move {{name}} down': '下移 {{name}}',
    'Remove {{name}}': '删除 {{name}}',
  },
  zhTW: {
    'Manage feeds': '管理訂閱來源',
    'Search articles': '搜尋文章',
    'All feeds': '全部訂閱',
    '{{feeds}} feeds · {{articles}} articles': '{{feeds}} 個訂閱 · {{articles}} 篇文章',
    'Loading articles...': '正在載入文章...',
    'Unable to load RSS feeds': '無法載入 RSS 訂閱',
    'Try loading the feeds again.': '請重試載入訂閱。',
    'No RSS feeds are configured yet.': '尚未設定 RSS 訂閱。',
    'No articles match your filters.': '沒有符合篩選條件的文章。',
    'Clear filters': '清除篩選',
    'Some feeds could not be refreshed.': '部分訂閱來源暫時無法重新整理。',
    'Configure the RSS and Atom sources shown in the reader. Changes are shared with all users.':
      '設定閱讀頁顯示的 RSS 與 Atom 訂閱來源，儲存後對所有使用者生效。',
    'Add feed': '新增訂閱',
    'Save feeds': '儲存訂閱',
    'No feeds are configured. Add one to start the reader.':
      '尚未設定訂閱來源，新增一個即可開始使用。',
    'New feed': '新訂閱',
    'Feed name': '訂閱名稱',
    'Feed URL': '訂閱網址',
    'Fetch this feed': '抓取此訂閱',
    'Disabled feeds stay saved but are not fetched.':
      '關閉後仍保留設定，但不會抓取內容。',
    'Enter a feed name (up to 80 characters).': '請輸入訂閱名稱（最多 80 個字元）。',
    'Enter a valid HTTP or HTTPS feed URL.': '請輸入有效的 HTTP 或 HTTPS 訂閱網址。',
    'This feed URL is already configured.': '這個訂閱網址已經設定過了。',
    'Check the highlighted feed.': '請檢查標示的訂閱項目。',
    'Move {{name}} up': '上移 {{name}}',
    'Move {{name}} down': '下移 {{name}}',
    'Remove {{name}}': '刪除 {{name}}',
  },
  fr: {
    'Manage feeds': 'Gérer les flux',
    'Search articles': 'Rechercher des articles',
    'All feeds': 'Tous les flux',
    '{{feeds}} feeds · {{articles}} articles': '{{feeds}} flux · {{articles}} articles',
    'Loading articles...': 'Chargement des articles...',
    'Unable to load RSS feeds': 'Impossible de charger les flux RSS',
    'Try loading the feeds again.': 'Réessayez de charger les flux.',
    'No RSS feeds are configured yet.': 'Aucun flux RSS n’est encore configuré.',
    'No articles match your filters.': 'Aucun article ne correspond aux filtres.',
    'Clear filters': 'Effacer les filtres',
    'Some feeds could not be refreshed.': 'Certains flux n’ont pas pu être actualisés.',
    'Configure the RSS and Atom sources shown in the reader. Changes are shared with all users.':
      'Configurez les sources RSS et Atom du lecteur. Les modifications s’appliquent à tous les utilisateurs.',
    'Add feed': 'Ajouter un flux',
    'Save feeds': 'Enregistrer les flux',
    'No feeds are configured. Add one to start the reader.':
      'Aucun flux n’est configuré. Ajoutez-en un pour démarrer.',
    'New feed': 'Nouveau flux',
    'Feed name': 'Nom du flux',
    'Feed URL': 'URL du flux',
    'Fetch this feed': 'Récupérer ce flux',
    'Disabled feeds stay saved but are not fetched.':
      'Les flux désactivés restent enregistrés mais ne sont pas récupérés.',
    'Enter a feed name (up to 80 characters).':
      'Saisissez un nom de flux (80 caractères maximum).',
    'Enter a valid HTTP or HTTPS feed URL.':
      'Saisissez une URL de flux HTTP ou HTTPS valide.',
    'This feed URL is already configured.': 'Cette URL de flux est déjà configurée.',
    'Check the highlighted feed.': 'Vérifiez le flux signalé.',
    'Move {{name}} up': 'Monter {{name}}',
    'Move {{name}} down': 'Descendre {{name}}',
    'Remove {{name}}': 'Supprimer {{name}}',
  },
  ja: {
    'Manage feeds': 'フィードを管理',
    'Search articles': '記事を検索',
    'All feeds': 'すべてのフィード',
    '{{feeds}} feeds · {{articles}} articles': '{{feeds}} フィード · {{articles}} 記事',
    'Loading articles...': '記事を読み込み中...',
    'Unable to load RSS feeds': 'RSS フィードを読み込めません',
    'Try loading the feeds again.': 'フィードをもう一度読み込んでください。',
    'No RSS feeds are configured yet.': 'RSS フィードはまだ設定されていません。',
    'No articles match your filters.': '条件に一致する記事がありません。',
    'Clear filters': 'フィルターを解除',
    'Some feeds could not be refreshed.': '一部のフィードを更新できませんでした。',
    'Configure the RSS and Atom sources shown in the reader. Changes are shared with all users.':
      'リーダーに表示する RSS / Atom ソースを設定します。変更はすべてのユーザーに反映されます。',
    'Add feed': 'フィードを追加',
    'Save feeds': 'フィードを保存',
    'No feeds are configured. Add one to start the reader.':
      'フィードが設定されていません。追加すると利用を開始できます。',
    'New feed': '新しいフィード',
    'Feed name': 'フィード名',
    'Feed URL': 'フィード URL',
    'Fetch this feed': 'このフィードを取得',
    'Disabled feeds stay saved but are not fetched.':
      '無効にしたフィードは保存されますが、取得されません。',
    'Enter a feed name (up to 80 characters).': 'フィード名を入力してください（80文字以内）。',
    'Enter a valid HTTP or HTTPS feed URL.': '有効な HTTP または HTTPS のフィード URL を入力してください。',
    'This feed URL is already configured.': 'このフィード URL はすでに設定されています。',
    'Check the highlighted feed.': '強調表示されたフィードを確認してください。',
    'Move {{name}} up': '{{name}} を上へ移動',
    'Move {{name}} down': '{{name}} を下へ移動',
    'Remove {{name}}': '{{name}} を削除',
  },
  ru: {
    'Manage feeds': 'Управление лентами',
    'Search articles': 'Поиск статей',
    'All feeds': 'Все ленты',
    '{{feeds}} feeds · {{articles}} articles': '{{feeds}} лент · {{articles}} статей',
    'Loading articles...': 'Загрузка статей...',
    'Unable to load RSS feeds': 'Не удалось загрузить RSS-ленты',
    'Try loading the feeds again.': 'Попробуйте загрузить ленты снова.',
    'No RSS feeds are configured yet.': 'RSS-ленты пока не настроены.',
    'No articles match your filters.': 'Нет статей, соответствующих фильтрам.',
    'Clear filters': 'Сбросить фильтры',
    'Some feeds could not be refreshed.': 'Некоторые ленты не удалось обновить.',
    'Configure the RSS and Atom sources shown in the reader. Changes are shared with all users.':
      'Настройте источники RSS и Atom для страницы чтения. Изменения применяются ко всем пользователям.',
    'Add feed': 'Добавить ленту',
    'Save feeds': 'Сохранить ленты',
    'No feeds are configured. Add one to start the reader.':
      'Ленты не настроены. Добавьте первую ленту.',
    'New feed': 'Новая лента',
    'Feed name': 'Название ленты',
    'Feed URL': 'URL ленты',
    'Fetch this feed': 'Загружать эту ленту',
    'Disabled feeds stay saved but are not fetched.':
      'Отключённые ленты сохраняются, но не загружаются.',
    'Enter a feed name (up to 80 characters).':
      'Введите название ленты (до 80 символов).',
    'Enter a valid HTTP or HTTPS feed URL.':
      'Введите корректный HTTP- или HTTPS-адрес ленты.',
    'This feed URL is already configured.': 'Этот адрес ленты уже настроен.',
    'Check the highlighted feed.': 'Проверьте выделенную ленту.',
    'Move {{name}} up': 'Переместить {{name}} вверх',
    'Move {{name}} down': 'Переместить {{name}} вниз',
    'Remove {{name}}': 'Удалить {{name}}',
  },
  vi: {
    'Manage feeds': 'Quản lý nguồn tin',
    'Search articles': 'Tìm bài viết',
    'All feeds': 'Tất cả nguồn tin',
    '{{feeds}} feeds · {{articles}} articles': '{{feeds}} nguồn · {{articles}} bài viết',
    'Loading articles...': 'Đang tải bài viết...',
    'Unable to load RSS feeds': 'Không thể tải nguồn RSS',
    'Try loading the feeds again.': 'Hãy thử tải lại các nguồn tin.',
    'No RSS feeds are configured yet.': 'Chưa có nguồn RSS nào được cấu hình.',
    'No articles match your filters.': 'Không có bài viết phù hợp bộ lọc.',
    'Clear filters': 'Xóa bộ lọc',
    'Some feeds could not be refreshed.': 'Một số nguồn tin chưa thể làm mới.',
    'Configure the RSS and Atom sources shown in the reader. Changes are shared with all users.':
      'Cấu hình nguồn RSS và Atom hiển thị trong trình đọc. Thay đổi áp dụng cho tất cả người dùng.',
    'Add feed': 'Thêm nguồn',
    'Save feeds': 'Lưu nguồn',
    'No feeds are configured. Add one to start the reader.':
      'Chưa có nguồn tin. Thêm một nguồn để bắt đầu.',
    'New feed': 'Nguồn mới',
    'Feed name': 'Tên nguồn',
    'Feed URL': 'URL nguồn',
    'Fetch this feed': 'Tải nguồn này',
    'Disabled feeds stay saved but are not fetched.':
      'Nguồn bị tắt vẫn được lưu nhưng sẽ không được tải.',
    'Enter a feed name (up to 80 characters).': 'Nhập tên nguồn (tối đa 80 ký tự).',
    'Enter a valid HTTP or HTTPS feed URL.': 'Nhập URL nguồn HTTP hoặc HTTPS hợp lệ.',
    'This feed URL is already configured.': 'URL nguồn này đã được cấu hình.',
    'Check the highlighted feed.': 'Kiểm tra nguồn được đánh dấu.',
    'Move {{name}} up': 'Di chuyển {{name}} lên',
    'Move {{name}} down': 'Di chuyển {{name}} xuống',
    'Remove {{name}}': 'Xóa {{name}}',
  },
} as const

let registered = false

export function registerRSSTranslations() {
  if (registered) return
  for (const [language, translations] of Object.entries(rssTranslations)) {
    i18next.addResourceBundle(language, 'translation', translations, true, true)
  }
  registered = true
}
