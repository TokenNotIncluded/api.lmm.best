/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, test } from 'node:test'

import { Window } from 'happy-dom'

import { usageLogSchema, type UsageLog } from '../../data/schema'

const domWindow = new Window({ url: 'http://localhost/' })
domWindow.document.write('<!doctype html><html><body></body></html>')
for (const key of [
  'window',
  'document',
  'navigator',
  'localStorage',
  'HTMLElement',
  'HTMLButtonElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  value: true,
})

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { DetailsDialog } = await import('../dialogs/details-dialog')
const { DEFAULT_CURRENCY_CONFIG, useSystemConfigStore } =
  await import('@/stores/system-config-store')
const originalConfig = useSystemConfigStore.getState().config
const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  fallbackLng: 'en',
  interpolation: { escapeValue: false },
  resources: { en: { translation: {} } },
})

let rendered:
  | { root: ReturnType<typeof createRoot>; container: HTMLDivElement }
  | undefined

beforeEach(() => {
  useSystemConfigStore.setState({
    config: {
      ...originalConfig,
      currency: {
        ...DEFAULT_CURRENCY_CONFIG,
        currencyUnit: 'credit',
        creditsPerUsd: 500_000,
        creditsPerUsdExact: '500000',
        cnyPerUsd: 7,
        cnyPerUsdExact: '7',
      },
    },
  })
})
afterEach(async () => {
  if (rendered) {
    await act(async () => rendered?.root.unmount())
    rendered.container.remove()
    rendered = undefined
  }
  useSystemConfigStore.setState({ config: originalConfig })
  document.body.replaceChildren()
})
after(() => domWindow.close())

function usageLog(other: unknown, overrides: Partial<UsageLog> = {}): UsageLog {
  return usageLogSchema.parse({
    id: 1,
    user_id: 1,
    created_at: 1,
    type: 2,
    content: '',
    model_name: 'history-test',
    quota: 3_500_000,
    other: JSON.stringify(other),
    ...overrides,
  })
}

async function mount(log: UsageLog, isAdmin = false) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  rendered = { root, container }
  await act(async () =>
    root.render(
      <I18nextProvider i18n={i18n}>
        <DetailsDialog
          log={log}
          isAdmin={isAdmin}
          open
          onOpenChange={() => {}}
        />
      </I18nextProvider>
    )
  )
  const dialog = document.querySelector('[role="dialog"]')
  assert.ok(dialog, 'Expected an open details dialog in the portal')
  return dialog
}

function detailValue(dialog: Element, label: string) {
  const labels = [...dialog.querySelectorAll('span')].filter(
    (element) => element.textContent === label && element.nextElementSibling
  )
  assert.equal(labels.length, 1, `Expected one details row for ${label}`)
  return labels[0].nextElementSibling?.textContent
}

function summaryValue(dialog: Element, label: string) {
  const labels = [...dialog.querySelectorAll('dt')].filter(
    (element) => element.textContent === label
  )
  assert.equal(labels.length, 1, `Expected one summary row for ${label}`)
  return labels[0].nextElementSibling?.textContent
}

const subscription = {
  billing_source: 'subscription',
  subscription_pre_consumed: 1_400_000,
  subscription_post_delta: 350_000,
  subscription_consumed: 1_750_000,
  subscription_remain: 5_250_000,
  subscription_total: 7_000_000,
}

test('details without a captured anchor keep charges and subscription snapshots raw', async () => {
  const dialog = await mount(usageLog(subscription))
  assert.equal(detailValue(dialog, 'Total Cost'), '3,500,000 Raw credits')
  assert.equal(detailValue(dialog, 'Pre-consumed'), '1,400,000 Raw credits')
  assert.equal(detailValue(dialog, 'Post Delta'), '350,000 Raw credits')
  assert.equal(detailValue(dialog, 'Final Consumed'), '1,750,000 Raw credits')
  assert.equal(
    summaryValue(dialog, 'Plan quota used in this entry'),
    '1,750,000 Raw credits'
  )
  assert.equal(
    detailValue(dialog, 'Remaining'),
    '5,250,000 Raw credits / 7,000,000 Raw credits'
  )
  assert.doesNotMatch(dialog.textContent ?? '', /USD|CNY/)
})

test('captured charge units do not reprice subscription remaining and total snapshots', async () => {
  const dialog = await mount(
    usageLog({ ...subscription, pricing_credits_per_usd: 3_500_000 })
  )
  assert.equal(detailValue(dialog, 'Total Cost'), '1 USD')
  assert.equal(detailValue(dialog, 'Pre-consumed'), '0.4 USD')
  assert.equal(detailValue(dialog, 'Post Delta'), '0.1 USD')
  assert.equal(detailValue(dialog, 'Final Consumed'), '0.5 USD')
  assert.equal(summaryValue(dialog, 'Plan quota used in this entry'), '0.5 USD')
  assert.equal(
    detailValue(dialog, 'Remaining'),
    '5,250,000 Raw credits / 7,000,000 Raw credits'
  )
  await act(async () =>
    useSystemConfigStore.setState({
      config: {
        ...useSystemConfigStore.getState().config,
        currency: {
          ...useSystemConfigStore.getState().config.currency,
          cnyPerUsd: 8,
          cnyPerUsdExact: '8',
        },
      },
    })
  )
  assert.equal(detailValue(dialog, 'Total Cost'), '1 USD')
  assert.equal(
    detailValue(dialog, 'Remaining'),
    '5,250,000 Raw credits / 7,000,000 Raw credits'
  )
})

test('a complete typed charge does not price unanchored subscription subamounts', async () => {
  const dialog = await mount(
    usageLog(subscription, {
      quota_money_available: true,
      quota_money_basis: 'captured_credits_per_usd',
      quota_usd: '1',
    })
  )
  assert.equal(detailValue(dialog, 'Total Cost'), '1 USD')
  assert.equal(detailValue(dialog, 'Pre-consumed'), '1,400,000 Raw credits')
  assert.equal(detailValue(dialog, 'Post Delta'), '350,000 Raw credits')
  assert.equal(detailValue(dialog, 'Final Consumed'), '1,750,000 Raw credits')
  assert.equal(
    summaryValue(dialog, 'Plan quota used in this entry'),
    '1,750,000 Raw credits'
  )
  assert.equal(
    detailValue(dialog, 'Remaining'),
    '5,250,000 Raw credits / 7,000,000 Raw credits'
  )
})

test('explicit unavailable projections override a captured metadata anchor', async () => {
  const dialog = await mount(
    usageLog(
      { pricing_credits_per_usd: 3_500_000 },
      {
        quota_money_available: false,
        quota_money_basis: 'missing_credit_basis',
      }
    )
  )
  assert.equal(detailValue(dialog, 'Total Cost'), '3,500,000 Raw credits')
  assert.equal(
    summaryValue(dialog, 'Charge recorded in this entry'),
    '3,500,000 Raw credits'
  )
  assert.doesNotMatch(dialog.textContent ?? '', /USD|CNY/)
})

test('a NULL stored charge is unknown without hiding independent subscription snapshots', async () => {
  const dialog = await mount(
    usageLog(
      {
        billing_source: 'subscription',
        subscription_remain: 0,
        subscription_total: 500,
      },
      {
        quota: 0,
        quota_money_available: false,
        quota_money_basis: 'invalid_stored_quota',
      }
    )
  )
  assert.equal(detailValue(dialog, 'Total Cost'), '—')
  assert.equal(summaryValue(dialog, 'Plan quota used in this entry'), '—')
  assert.equal(
    detailValue(dialog, 'Remaining'),
    '0 Raw credits / 500 Raw credits'
  )
})

test('a genuine zero charge with missing money basis remains zero raw credits', async () => {
  const dialog = await mount(
    usageLog(
      {
        billing_source: 'subscription',
        subscription_remain: 0,
        subscription_total: 500,
      },
      {
        quota: 0,
        quota_money_available: false,
        quota_money_basis: 'missing_credit_basis',
      }
    )
  )
  assert.equal(detailValue(dialog, 'Total Cost'), '0 Raw credits')
  assert.equal(
    summaryValue(dialog, 'Plan quota used in this entry'),
    '0 Raw credits'
  )
  assert.equal(
    detailValue(dialog, 'Remaining'),
    '0 Raw credits / 500 Raw credits'
  )
})

test('a NULL stored charge does not hide independently stored audit quota', async () => {
  const dialog = await mount(
    usageLog(
      { op: { action: 'user.quota_add', params: { quota: '500 Credits' } } },
      {
        type: 3,
        quota: 0,
        quota_money_available: false,
        quota_money_basis: 'invalid_stored_quota',
      }
    ),
    true
  )
  assert.equal(
    detailValue(dialog, 'Operation'),
    'Increased user quota by 500 Raw credits'
  )
})

test('management audit quota changes remain raw even with a charge anchor and typed projection', async () => {
  const dialog = await mount(
    usageLog(
      {
        pricing_credits_per_usd: 3_500_000,
        op: { action: 'user.quota_add', params: { quota: '3500000 Credits' } },
      },
      {
        type: 3,
        quota_money_available: true,
        quota_money_basis: 'captured_credits_per_usd',
        quota_usd: '1',
      }
    ),
    true
  )
  assert.equal(
    detailValue(dialog, 'Operation'),
    'Increased user quota by 3,500,000 Raw credits'
  )
  assert.doesNotMatch(detailValue(dialog, 'Operation') ?? '', /USD|CNY/)
})
