/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import i18next from 'i18next'

export const discountCodeTranslations = {
  en: {
    'Clear exhausted codes': 'Clear exhausted codes',
    'Unable to clear exhausted discount codes':
      'Unable to clear exhausted discount codes',
    'Deleted {{count}} exhausted discount codes':
      'Deleted {{count}} exhausted discount codes',
    'Delete exhausted discount codes?': 'Delete exhausted discount codes?',
    'This permanently removes every finite-use discount code whose usage limit has been reached. Partially used and unlimited codes are kept.':
      'This permanently removes every finite-use discount code whose usage limit has been reached. Partially used and unlimited codes are kept.',
    'Delete exhausted codes': 'Delete exhausted codes',
    'No exhausted discount codes to delete':
      'No exhausted discount codes to delete',
  },
  zh: {
    'Clear exhausted codes': '清理已用完优惠码',
    'Unable to clear exhausted discount codes': '无法清理已用完优惠码',
    'Deleted {{count}} exhausted discount codes':
      '已删除 {{count}} 个已用完优惠码',
    'Delete exhausted discount codes?': '删除已用完优惠码？',
    'This permanently removes every finite-use discount code whose usage limit has been reached. Partially used and unlimited codes are kept.':
      '此操作将永久删除所有已达到使用上限的有限次数优惠码。部分使用和不限次数的优惠码会保留。',
    'Delete exhausted codes': '删除已用完优惠码',
    'No exhausted discount codes to delete': '没有需要删除的已用完优惠码',
  },
  'zh-TW': {
    'Clear exhausted codes': '清理已用完優惠碼',
    'Unable to clear exhausted discount codes': '無法清理已用完優惠碼',
    'Deleted {{count}} exhausted discount codes':
      '已刪除 {{count}} 個已用完優惠碼',
    'Delete exhausted discount codes?': '刪除已用完優惠碼？',
    'This permanently removes every finite-use discount code whose usage limit has been reached. Partially used and unlimited codes are kept.':
      '此操作將永久刪除所有已達使用上限的有限次數優惠碼。部分使用與不限次數的優惠碼會保留。',
    'Delete exhausted codes': '刪除已用完優惠碼',
    'No exhausted discount codes to delete': '沒有需要刪除的已用完優惠碼',
  },
  fr: {
    'Clear exhausted codes': 'Nettoyer les codes épuisés',
    'Unable to clear exhausted discount codes':
      'Impossible de nettoyer les codes de réduction épuisés',
    'Deleted {{count}} exhausted discount codes':
      '{{count}} codes de réduction épuisés supprimés',
    'Delete exhausted discount codes?':
      'Supprimer les codes de réduction épuisés ?',
    'This permanently removes every finite-use discount code whose usage limit has been reached. Partially used and unlimited codes are kept.':
      'Cette action supprime définitivement tous les codes à usage limité ayant atteint leur limite. Les codes partiellement utilisés et illimités sont conservés.',
    'Delete exhausted codes': 'Supprimer les codes épuisés',
    'No exhausted discount codes to delete':
      'Aucun code de réduction épuisé à supprimer',
  },
  ja: {
    'Clear exhausted codes': '使用済みコードを整理',
    'Unable to clear exhausted discount codes':
      '使用済み割引コードを整理できません',
    'Deleted {{count}} exhausted discount codes':
      '使用済み割引コードを {{count}} 件削除しました',
    'Delete exhausted discount codes?': '使用済み割引コードを削除しますか？',
    'This permanently removes every finite-use discount code whose usage limit has been reached. Partially used and unlimited codes are kept.':
      '使用上限に達した有限回数の割引コードをすべて完全に削除します。一部使用済みおよび無制限のコードは保持されます。',
    'Delete exhausted codes': '使用済みコードを削除',
    'No exhausted discount codes to delete':
      '削除できる使用済み割引コードはありません',
  },
  ru: {
    'Clear exhausted codes': 'Очистить исчерпанные коды',
    'Unable to clear exhausted discount codes':
      'Не удалось очистить исчерпанные промокоды',
    'Deleted {{count}} exhausted discount codes':
      'Удалено исчерпанных промокодов: {{count}}',
    'Delete exhausted discount codes?': 'Удалить исчерпанные промокоды?',
    'This permanently removes every finite-use discount code whose usage limit has been reached. Partially used and unlimited codes are kept.':
      'Все промокоды с ограниченным числом использований, достигшие лимита, будут удалены безвозвратно. Частично использованные и безлимитные коды сохранятся.',
    'Delete exhausted codes': 'Удалить исчерпанные коды',
    'No exhausted discount codes to delete':
      'Нет исчерпанных промокодов для удаления',
  },
  vi: {
    'Clear exhausted codes': 'Dọn mã đã dùng hết',
    'Unable to clear exhausted discount codes':
      'Không thể dọn các mã giảm giá đã dùng hết',
    'Deleted {{count}} exhausted discount codes':
      'Đã xóa {{count}} mã giảm giá đã dùng hết',
    'Delete exhausted discount codes?': 'Xóa các mã giảm giá đã dùng hết?',
    'This permanently removes every finite-use discount code whose usage limit has been reached. Partially used and unlimited codes are kept.':
      'Thao tác này xóa vĩnh viễn mọi mã có số lượt dùng hữu hạn đã đạt giới hạn. Mã mới dùng một phần và mã không giới hạn vẫn được giữ lại.',
    'Delete exhausted codes': 'Xóa mã đã dùng hết',
    'No exhausted discount codes to delete':
      'Không có mã giảm giá đã dùng hết để xóa',
  },
} as const

let registered = false

export function registerDiscountCodeTranslations() {
  if (registered) return
  for (const [language, translations] of Object.entries(
    discountCodeTranslations
  )) {
    i18next.addResourceBundle(language, 'translation', translations, true, true)
  }
  registered = true
}
