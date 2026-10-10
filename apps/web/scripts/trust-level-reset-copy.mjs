/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
const keys = [
  'Restore automatic trust level',
  'Restore automatic trust level for {{username}}? This removes the administrator override and recalculates the level from account activation and eligible paid recharge history. Existing balance and account history are preserved.',
]
const values = {
  en: keys,
  zh: [
    '恢复自动等级',
    '恢复 {{username}} 的自动等级？这会移除管理员强制等级，根据账号激活状态和有效的已付充值记录重新判定。现有余额和账号历史将保留。',
  ],
  'zh-TW': [
    '恢復自動等級',
    '恢復 {{username}} 的自動等級？這會移除管理員強制等級，依帳號啟用狀態及有效的已付款儲值紀錄重新判定。現有餘額及帳號歷史將保留。',
  ],
  fr: [
    'Rétablir le niveau automatique',
    'Rétablir le niveau automatique de {{username}} ? La dérogation de l’administrateur sera supprimée et le niveau recalculé selon l’activation du compte et les recharges payées admissibles. Le solde et l’historique du compte seront conservés.',
  ],
  ja: [
    '自動レベル判定に戻す',
    '{{username}} の自動レベル判定に戻しますか？管理者による固定レベルを解除し、アカウントの有効化状況と対象の入金履歴に基づいて再判定します。現在の残高とアカウント履歴は保持されます。',
  ],
  ru: [
    'Вернуть автоматический уровень',
    'Вернуть автоматический уровень для {{username}}? Назначенный администратором уровень будет отменён и пересчитан по активации аккаунта и учитываемым оплаченным пополнениям. Баланс и история аккаунта сохранятся.',
  ],
  vi: [
    'Khôi phục cấp độ tự động',
    'Khôi phục cấp độ tự động cho {{username}}? Thao tác này gỡ cấp độ do quản trị viên áp đặt và tính lại theo trạng thái kích hoạt tài khoản cùng lịch sử nạp tiền hợp lệ đã thanh toán. Số dư và lịch sử tài khoản được giữ nguyên.',
  ],
}
export const trustLevelResetCopy = Object.fromEntries(
  Object.entries(values).map(([locale, translations]) => [
    locale,
    Object.fromEntries(keys.map((key, index) => [key, translations[index]])),
  ])
)
