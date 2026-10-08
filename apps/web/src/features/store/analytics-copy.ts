/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
export const STORE_ANALYTICS_COPY = {
  title: 'Product analytics',
  description: 'Compare product visibility, detail visits, orders and refunds.',
  mine: 'My products',
  all: 'All sellers',
  period: 'Analytics period',
  seven: 'Last 7 days',
  thirty: 'Last 30 days',
  ninety: 'Last 90 days',
  lifetime: 'All time',
  funnel: 'Product funnel',
  quantity: 'Item quantities',
  product: 'Product',
  impressions: 'Impressions',
  clicks: 'Detail visits',
  orders: 'Orders placed',
  paid: 'Paid orders',
  refunded: 'Refunded orders',
  quantityRefunded: 'Quantity-refunded orders',
  amountRefunded: 'Amount-refunded orders',
  refundHelp:
    'Quantity refunds reduce net paid items. Amount-only refunds do not reduce item quantities.',
  orderedQuantity: 'Ordered items',
  paidQuantity: 'Paid items',
  refundedQuantity: 'Refunded items',
  netQuantity: 'Net paid items',
  ctr: 'Click-through rate',
  conversion: 'Order conversion',
  disabled: 'Not enabled',
  trafficUnavailable: 'Impressions and detail visits have not been enabled.',
  trafficWindow:
    'Traffic is retained for up to {{days}} days and starts when analytics is enabled.',
  trafficSince: 'Earliest retained traffic: {{date}}.',
  allWindow:
    'All time includes full order history and only retained traffic. Rates are hidden because these periods differ.',
  shortWindow:
    'The selected period exceeds traffic retention. Rates are hidden because these periods differ.',
  orderWindow:
    'Periods use UTC calendar days. Orders are filtered by creation date. Paid and refunded counts show the current state of those orders; paid orders include later refunds.',
  rateHelp:
    'Click-through rate is detail visits / impressions. Order conversion is orders placed / detail visits. These compare event counts and can exceed 100%.',
  totals: 'Totals for all matching products',
  empty: 'No product analytics yet',
  emptyHelp: 'Your products will appear here with traffic and order counts.',
  seller: 'Seller ID',
  page: 'Page {{page}}',
  settings: 'Analytics retention settings',
  retention: 'Traffic retention in days',
  dedupe: 'Duplicate-event protection in days',
  retentionHelp:
    'Shortening retention permanently removes older traffic on the next cleanup.',
  saveSettings: 'Save analytics settings',
  saved: 'Analytics settings saved',
  invalidSettings:
    'Enter whole days: traffic retention 1–3650, duplicate-event protection 1–30 and no longer than traffic retention.',
  scope: 'Analytics scope',
} as const
