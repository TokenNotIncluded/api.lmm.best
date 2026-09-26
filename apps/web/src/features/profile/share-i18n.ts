/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import type { i18n } from 'i18next'

const translations = {
  en: {
    'Turn on model sharing': 'Turn on model sharing',
    'Turn off model sharing': 'Turn off model sharing',
    'Model names, tokens, requests and platform spend become public. Appearance options do not restrict access.':
      'Model names, tokens, requests and platform spend become public. Appearance options do not restrict access.',
    'Turn on model sharing to preview your real usage.':
      'Turn on model sharing to preview your real usage.',
    'Models to display': 'Models to display',
    'Remaining models are grouped together.':
      'Remaining models are grouped together.',
    'View full model statistics': 'View full model statistics',
  },
  zhCN: {
    'Turn on model sharing': '开启模型用量分享',
    'Turn off model sharing': '关闭模型用量分享',
    'Model names, tokens, requests and platform spend become public. Appearance options do not restrict access.':
      '模型名称、Token、请求数和平台用量将公开。外观设置不限制访问权限。',
    'Turn on model sharing to preview your real usage.':
      '开启模型用量分享，预览真实数据。',
    'Models to display': '显示模型数量',
    'Remaining models are grouped together.': '其余模型合并为「其他模型」。',
    'View full model statistics': '查看完整模型统计',
  },
  zhTW: {
    'Turn on model sharing': '開啟模型用量分享',
    'Turn off model sharing': '關閉模型用量分享',
    'Model names, tokens, requests and platform spend become public. Appearance options do not restrict access.':
      '模型名稱、Token、請求數和平台用量將公開。外觀設定不限制存取權限。',
    'Turn on model sharing to preview your real usage.':
      '開啟模型用量分享，預覽真實資料。',
    'Models to display': '顯示模型數量',
    'Remaining models are grouped together.': '其餘模型合併為「其他模型」。',
    'View full model statistics': '查看完整模型統計',
  },
  ja: {
    'Turn on model sharing': 'モデル利用状況を公開',
    'Turn off model sharing': 'モデル利用状況の公開を停止',
    'Model names, tokens, requests and platform spend become public. Appearance options do not restrict access.':
      'モデル名、トークン数、リクエスト数と利用額が公開されます。外観設定ではアクセスを制限できません。',
    'Turn on model sharing to preview your real usage.':
      'モデル利用状況を公開すると、実際のデータをプレビューできます。',
    'Models to display': '表示するモデル数',
    'Remaining models are grouped together.':
      '残りのモデルは「その他」にまとめます。',
    'View full model statistics': 'モデル別の全統計を表示',
  },
  fr: {
    'Turn on model sharing': 'Activer le partage par modèle',
    'Turn off model sharing': 'Désactiver le partage par modèle',
    'Model names, tokens, requests and platform spend become public. Appearance options do not restrict access.':
      'Les noms des modèles, tokens, requêtes et dépenses deviennent publics. Les options visuelles ne limitent pas l’accès.',
    'Turn on model sharing to preview your real usage.':
      'Activez le partage par modèle pour prévisualiser vos données réelles.',
    'Models to display': 'Nombre de modèles affichés',
    'Remaining models are grouped together.':
      'Les autres modèles sont regroupés.',
    'View full model statistics': 'Voir toutes les statistiques par modèle',
  },
  ru: {
    'Turn on model sharing': 'Включить публикацию по моделям',
    'Turn off model sharing': 'Отключить публикацию по моделям',
    'Model names, tokens, requests and platform spend become public. Appearance options do not restrict access.':
      'Названия моделей, токены, запросы и расходы станут публичными. Настройки оформления не ограничивают доступ.',
    'Turn on model sharing to preview your real usage.':
      'Включите публикацию по моделям для просмотра реальных данных.',
    'Models to display': 'Количество моделей',
    'Remaining models are grouped together.': 'Остальные модели объединяются.',
    'View full model statistics': 'Все статистические данные по моделям',
  },
  vi: {
    'Turn on model sharing': 'Bật chia sẻ theo mô hình',
    'Turn off model sharing': 'Tắt chia sẻ theo mô hình',
    'Model names, tokens, requests and platform spend become public. Appearance options do not restrict access.':
      'Tên mô hình, token, yêu cầu và chi phí sẽ được công khai. Tùy chọn hiển thị không giới hạn quyền truy cập.',
    'Turn on model sharing to preview your real usage.':
      'Bật chia sẻ theo mô hình để xem trước dữ liệu thực.',
    'Models to display': 'Số mô hình hiển thị',
    'Remaining models are grouped together.':
      'Các mô hình còn lại được gộp chung.',
    'View full model statistics': 'Xem toàn bộ thống kê theo mô hình',
  },
}

const registered = new WeakSet<i18n>()
export function registerProfileShareTranslations(instance: i18n) {
  if (registered.has(instance)) return
  for (const [language, resource] of Object.entries(translations)) {
    instance.addResourceBundle(language, 'translation', resource, true, true)
  }
  registered.add(instance)
}
