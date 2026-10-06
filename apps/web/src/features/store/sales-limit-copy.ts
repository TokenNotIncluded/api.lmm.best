/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
export const STORE_SALES_LIMIT_COPY = {
  title: 'Available sales quota',
  unlimited: 'Unlimited sales',
  limit: 'Remaining sales quota',
  help: 'This quota includes unpaid reservations. Paid orders reduce it; adding inventory does not increase it. Set 0 to stop new orders.',
  save: 'Save sales quota',
  invalid: 'Enter a whole sales limit of 0 or more.',
  inventory: 'Undelivered inventory: {{count}}',
  paid: 'Paid: {{count}}',
  reserved: 'Reserved: {{count}}',
  available: 'Available to sell: {{count}}',
  offShelf: 'Take off shelf',
  relist: 'Relist product',
  offShelfStatus: 'Off shelf',
} as const
