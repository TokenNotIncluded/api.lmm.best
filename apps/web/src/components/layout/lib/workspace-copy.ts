/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
// Feature-local copy follows the seven configured locales. Navigation titles
// still come from the existing permission-filtered, translated registry.
const english = {
  workspace: 'Your workspace',
  description: 'Models, keys and usage. Start with what you need.',
  shortcuts: 'Quick access',
  navigation: 'Navigation',
  searchPlaceholder: 'Filter navigation…',
  noResults: 'No matching destinations.',
  clear: 'Clear filter',
  allTools: 'Search all tools',
  keys: 'Create and manage access',
  pricing: 'Compare models and rates',
  wallet: 'Credit and reward history',
  usage: 'Inspect requests and usage',
  empty: 'Only tools available to your account are shown.',
}

export type WorkspaceCopy = typeof english

const translations: Record<string, WorkspaceCopy> = {
  en: english,
  zhCN: {
    workspace: '工作台',
    description: '模型、密钥与用量，从你需要的操作开始。',
    shortcuts: '常用入口',
    navigation: '导航',
    searchPlaceholder: '筛选导航…',
    noResults: '没有匹配的入口。',
    clear: '清除筛选',
    allTools: '搜索全部功能',
    keys: '创建与管理访问密钥',
    pricing: '比较模型与价格',
    wallet: '查看额度与奖励流水',
    usage: '查看请求与使用记录',
    empty: '这里只显示当前账户可用的功能。',
  },
  zhTW: {
    workspace: '工作台',
    description: '模型、金鑰與用量，從你需要的操作開始。',
    shortcuts: '常用入口',
    navigation: '導覽',
    searchPlaceholder: '篩選導覽…',
    noResults: '沒有符合的入口。',
    clear: '清除篩選',
    allTools: '搜尋全部功能',
    keys: '建立與管理存取金鑰',
    pricing: '比較模型與價格',
    wallet: '查看額度與獎勵明細',
    usage: '查看請求與使用紀錄',
    empty: '這裡只顯示目前帳戶可用的功能。',
  },
  fr: {
    workspace: 'Votre espace',
    description:
      'Modèles, clés et utilisation. Commencez par ce dont vous avez besoin.',
    shortcuts: 'Accès rapide',
    navigation: 'Navigation',
    searchPlaceholder: 'Filtrer la navigation…',
    noResults: 'Aucune destination correspondante.',
    clear: 'Effacer le filtre',
    allTools: 'Rechercher un outil',
    keys: 'Créer et gérer les accès',
    pricing: 'Comparer modèles et tarifs',
    wallet: 'Crédit et historique des récompenses',
    usage: 'Consulter les requêtes et leur utilisation',
    empty: 'Seuls les outils disponibles pour votre compte sont affichés.',
  },
  ru: {
    workspace: 'Рабочее пространство',
    description: 'Модели, ключи и использование. Начните с нужного действия.',
    shortcuts: 'Быстрый доступ',
    navigation: 'Навигация',
    searchPlaceholder: 'Фильтр навигации…',
    noResults: 'Подходящих разделов нет.',
    clear: 'Сбросить фильтр',
    allTools: 'Найти инструмент',
    keys: 'Создание и управление ключами',
    pricing: 'Сравнение моделей и тарифов',
    wallet: 'Баланс и история наград',
    usage: 'Запросы и история использования',
    empty: 'Показаны только инструменты, доступные вашему аккаунту.',
  },
  ja: {
    workspace: 'ワークスペース',
    description: 'モデル、キー、使用状況。必要な操作から始めましょう。',
    shortcuts: 'クイックアクセス',
    navigation: 'ナビゲーション',
    searchPlaceholder: 'ナビゲーションを絞り込む…',
    noResults: '一致する項目はありません。',
    clear: '絞り込みを解除',
    allTools: 'すべての機能を検索',
    keys: 'アクセスキーの作成と管理',
    pricing: 'モデルと料金を比較',
    wallet: '残高と報酬履歴を確認',
    usage: 'リクエストと使用履歴を確認',
    empty: 'このアカウントで利用できる機能のみ表示しています。',
  },
  vi: {
    workspace: 'Không gian làm việc',
    description: 'Mô hình, khóa và mức sử dụng. Bắt đầu với thao tác bạn cần.',
    shortcuts: 'Truy cập nhanh',
    navigation: 'Điều hướng',
    searchPlaceholder: 'Lọc điều hướng…',
    noResults: 'Không tìm thấy mục phù hợp.',
    clear: 'Xóa bộ lọc',
    allTools: 'Tìm tất cả công cụ',
    keys: 'Tạo và quản lý khóa truy cập',
    pricing: 'So sánh mô hình và giá',
    wallet: 'Số dư và lịch sử phần thưởng',
    usage: 'Xem yêu cầu và mức sử dụng',
    empty: 'Chỉ hiển thị công cụ mà tài khoản của bạn có thể sử dụng.',
  },
}

export function getWorkspaceCopy(language: string): WorkspaceCopy {
  const normalized = language.toLowerCase().replace(/_/g, '-')
  if (/^zh(?:tw|-tw|-hk|-mo|-hant)/.test(normalized)) return translations.zhTW
  if (normalized.startsWith('zh')) return translations.zhCN
  const locale = normalized.split('-')[0]
  return Object.prototype.hasOwnProperty.call(translations, locale)
    ? translations[locale]
    : english
}
