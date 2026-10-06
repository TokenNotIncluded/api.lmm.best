/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
export const STORE_SALES_LIMIT_COPY = {
  title: 'Sales limit',
  unlimited: 'Unlimited sales',
  limit: 'Total sales limit',
  help: 'The limit includes paid items and unpaid reservations. Set 0 to stop new orders.',
  save: 'Save sales limit',
  invalid: 'Enter a whole sales limit of 0 or more.',
  inventory: 'Inventory: {{count}}',
  paid: 'Paid: {{count}}',
  reserved: 'Reserved: {{count}}',
  available: 'Available to sell: {{count}}',
  offShelf: 'Take off shelf',
  relist: 'Relist product',
  offShelfStatus: 'Off shelf',
} as const
