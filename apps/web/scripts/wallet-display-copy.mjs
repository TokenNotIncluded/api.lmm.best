/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
const rows = [
  [
    'Balance display currency',
    '余额显示单位',
    '餘額顯示單位',
    'Unité d’affichage du solde',
    '残高の表示単位',
    'Единица отображения баланса',
    'Đơn vị hiển thị số dư',
  ],
  [
    'Recharge display currency',
    '充值显示币种',
    '儲值顯示幣別',
    'Devise d’affichage de la recharge',
    'チャージの表示通貨',
    'Валюта отображения пополнения',
    'Đồng tiền hiển thị nạp tiền',
  ],
]
const locales = ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi']
export const walletDisplayCopy = Object.fromEntries(
  locales.map((locale, index) => [
    locale,
    Object.fromEntries(rows.map((row) => [row[0], row[index]])),
  ])
)
