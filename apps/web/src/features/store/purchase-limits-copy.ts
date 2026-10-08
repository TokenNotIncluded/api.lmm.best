/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
export const STORE_PURCHASE_LIMIT_COPY = {
  title: 'Purchase limits',
  perOrder: 'Maximum quantity per order',
  perBuyer: 'Maximum quantity per buyer',
  unlimited: 'Unlimited',
  help: 'Leave blank for no limit. Limits apply across all variants of this product.',
  buyerHelp:
    'Paid purchases and pending stock reservations count toward the buyer limit. Completed quantity refunds release that quantity; amount-only refunds do not.',
  invalid: 'Enter a whole number of at least 1, or leave blank for no limit.',
  error: 'This quantity exceeds the product purchase limit.',
  orderSummary: 'Maximum per order: {{count}}',
  buyerSummary: 'Your remaining purchase allowance: {{count}}',
} as const
