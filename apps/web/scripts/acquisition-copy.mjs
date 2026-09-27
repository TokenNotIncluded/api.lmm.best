/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
// English source, then zh, zh-TW, fr, ja, ru, vi.
const rows = [
  [
    'Promotion link created.',
    '推广链接已创建。',
    '推廣連結已建立。',
    'Lien de promotion créé.',
    'プロモーションリンクを作成しました。',
    'Рекламная ссылка создана.',
    'Đã tạo liên kết quảng bá.',
  ],
  [
    'Create, archive, and delete promotion links.',
    '创建、归档和删除推广链接。',
    '建立、封存和刪除推廣連結。',
    'Créer, archiver et supprimer des liens de promotion.',
    'プロモーションリンクの作成、アーカイブ、削除。',
    'Создавать, архивировать и удалять рекламные ссылки.',
    'Tạo, lưu trữ và xóa liên kết quảng bá.',
  ],
  [
    'Promotion link deleted.',
    '推广链接已删除。',
    '推廣連結已刪除。',
    'Lien de promotion supprimé.',
    'プロモーションリンクを削除しました。',
    'Рекламная ссылка удалена.',
    'Đã xóa liên kết quảng bá.',
  ],
  [
    'Unable to delete promotion link.',
    '无法删除推广链接。',
    '無法刪除推廣連結。',
    'Impossible de supprimer le lien de promotion.',
    'プロモーションリンクを削除できません。',
    'Не удалось удалить рекламную ссылку.',
    'Không thể xóa liên kết quảng bá.',
  ],
  [
    'Unable to load promotion links.',
    '无法加载推广链接。',
    '無法載入推廣連結。',
    'Impossible de charger les liens de promotion.',
    'プロモーションリンクを読み込めません。',
    'Не удалось загрузить рекламные ссылки.',
    'Không thể tải liên kết quảng bá.',
  ],
  [
    'Search promotion links',
    '搜索推广链接',
    '搜尋推廣連結',
    'Rechercher des liens de promotion',
    'プロモーションリンクを検索',
    'Поиск рекламных ссылок',
    'Tìm liên kết quảng bá',
  ],
  [
    'Name, source, or campaign',
    '名称、来源或活动',
    '名稱、來源或活動',
    'Nom, source ou campagne',
    '名前、流入元、キャンペーン',
    'Название, источник или кампания',
    'Tên, nguồn hoặc chiến dịch',
  ],
  [
    'Registration and payments',
    '注册与支付',
    '註冊與付款',
    'Inscriptions et paiements',
    '登録と支払い',
    'Регистрации и платежи',
    'Đăng ký và thanh toán',
  ],
  [
    'Filter links',
    '筛选链接',
    '篩選連結',
    'Filtrer les liens',
    'リンクを絞り込む',
    'Фильтр ссылок',
    'Lọc liên kết',
  ],
  [
    'No matching promotion links.',
    '没有符合条件的推广链接。',
    '沒有符合條件的推廣連結。',
    'Aucun lien de promotion correspondant.',
    '条件に一致するプロモーションリンクはありません。',
    'Нет подходящих рекламных ссылок.',
    'Không có liên kết quảng bá phù hợp.',
  ],
  [
    'Promotion link URL',
    '推广链接地址',
    '推廣連結網址',
    'URL du lien de promotion',
    'プロモーションリンクの URL',
    'URL рекламной ссылки',
    'URL liên kết quảng bá',
  ],
  [
    'Delete promotion link',
    '删除推广链接',
    '刪除推廣連結',
    'Supprimer le lien de promotion',
    'プロモーションリンクを削除',
    'Удалить рекламную ссылку',
    'Xóa liên kết quảng bá',
  ],
  [
    'Deleted links stay in historical reports, but their tracking IDs no longer record new visits.',
    '已删除的链接仍保留在历史报表中，但其追踪 ID 不再记录新访问。',
    '已刪除的連結仍保留在歷史報表中，但其追蹤 ID 不再記錄新造訪。',
    'Les liens supprimés restent dans les rapports historiques, mais leurs identifiants de suivi n’enregistrent plus de nouvelles visites.',
    '削除したリンクは過去のレポートに残りますが、追跡 ID による新しい訪問の記録は停止します。',
    'Удалённые ссылки остаются в исторических отчётах, но их идентификаторы больше не регистрируют новые посещения.',
    'Liên kết đã xóa vẫn nằm trong báo cáo lịch sử, nhưng mã theo dõi không còn ghi nhận lượt truy cập mới.',
  ],
  [
    'Delete "{{name}}"? Its tracking ID will stop working. Historical attribution and spend stay in reports. This cannot be undone.',
    '确定删除“{{name}}”吗？该链接的追踪 ID 将失效，历史归因和推广支出仍保留在报表中。此操作无法撤销。',
    '確定刪除「{{name}}」嗎？該連結的追蹤 ID 將失效，歷史歸因和推廣支出仍保留在報表中。此操作無法復原。',
    'Supprimer « {{name}} » ? Son identifiant de suivi cessera de fonctionner. L’attribution et les dépenses passées resteront dans les rapports. Cette action est irréversible.',
    '「{{name}}」を削除しますか？追跡 ID は使用できなくなります。過去の流入元データと支出はレポートに残ります。この操作は元に戻せません。',
    'Удалить «{{name}}»? Идентификатор отслеживания перестанет работать. Исторические данные об атрибуции и расходах сохранятся в отчётах. Это действие нельзя отменить.',
    'Xóa "{{name}}"? Mã theo dõi sẽ ngừng hoạt động. Dữ liệu phân bổ nguồn và chi phí trước đây vẫn có trong báo cáo. Không thể hoàn tác thao tác này.',
  ],
]

const locales = ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi']
export const acquisitionCopy = Object.fromEntries(
  locales.map((locale) => [locale, {}])
)
for (const [key, ...values] of rows) {
  acquisitionCopy.en[key] = key
  for (let index = 1; index < locales.length; index += 1) {
    acquisitionCopy[locales[index]][key] = values[index - 1]
  }
}
