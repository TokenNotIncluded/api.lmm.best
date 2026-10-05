/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
// @ts-expect-error Bun's test module is only available in the test runtime.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

import type { User } from '../types'

const domWindow = new Window({ url: 'https://console.example.test/users' })
const originalGlobals = new Map<string, PropertyDescriptor | undefined>()
for (const key of [
  'window',
  'document',
  'navigator',
  'localStorage',
  'HTMLElement',
  'HTMLInputElement',
  'HTMLButtonElement',
  'HTMLDetailsElement',
  'HTMLLabelElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'PointerEvent',
  'FocusEvent',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const) {
  originalGlobals.set(key, Object.getOwnPropertyDescriptor(globalThis, key))
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value:
      key === 'getComputedStyle'
        ? domWindow.getComputedStyle.bind(domWindow)
        : domWindow[key],
  })
}
originalGlobals.set(
  'IS_REACT_ACT_ENVIRONMENT',
  Object.getOwnPropertyDescriptor(globalThis, 'IS_REACT_ACT_ENVIRONMENT')
)
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { getCoreRowModel, useReactTable } = await import('@tanstack/react-table')
type RowSelectionState = import('@tanstack/react-table').RowSelectionState
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { useWalletCurrencyPreferenceStore } =
  await import('@/stores/wallet-currency-preference-store')
const { DEFAULT_CURRENCY_CONFIG, useSystemConfigStore } =
  await import('@/stores/system-config-store')

// These controls have their own request/permission tests. Here they provide
// focusable children, including an unsaved field, to exercise the row disclosure.
mock.module('./data-table-row-actions', () => ({
  DataTableRowActions: () => <button type='button'>Edit user</button>,
}))
mock.module('./user-risk-cell', () => ({
  UserRiskCell: () => <button type='button'>Risk details</button>,
}))
mock.module('./user-trust-level-cell', () => ({
  UserTrustLevelCell: () => (
    <label>
      Trust note
      <input defaultValue='Unsaved note' />
    </label>
  ),
}))
mock.module('./user-assistant-history-dialog', () => ({
  UserAssistantHistoryDialog: () => (
    <button type='button'>Conversation history</button>
  ),
}))
mock.module('./user-assistant-review-dialog', () => ({
  UserAssistantReviewDialog: () => (
    <button type='button'>Assistant review</button>
  ),
}))
const { UsersMobileList } = await import('./users-mobile-list')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

const originalAdapter = api.defaults.adapter
after(() => {
  api.defaults.adapter = originalAdapter
  domWindow.close()
  for (const [key, descriptor] of originalGlobals) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor)
    else Reflect.deleteProperty(globalThis, key)
  }
})

function makeUser(patch: Partial<User> = {}): User {
  return {
    id: 42,
    username: 'a-long-username-that-needs-to-wrap-on-a-phone',
    display_name: 'A complete display name',
    email: 'a-long-account-address+mobile-list@example.test',
    quota: 6_175_000,
    used_quota: 500_000,
    request_count: 12,
    group: 'mobile-fixture',
    status: 1,
    role: 10,
    assistant_conversation_count: 3,
    topup_summary: {
      quota: 49_380_000,
      money_micros: 98_760_000,
      currency: 'CNY',
      orders: 2,
      methods: [
        {
          method: 'Card',
          provider: 'Fixture provider',
          settlement_currency: 'CNY',
          quota: 49_380_000,
          money_micros: 98_760_000,
          orders: 2,
        },
      ],
    },
    ...patch,
  }
}

async function renderList(
  users: User[],
  preference: 'USD' | 'CNY' | 'CREDIT' = 'USD'
) {
  const requests: { method: string | undefined; url: string | undefined }[] = []
  api.defaults.adapter = async (config) => {
    requests.push({ method: config.method, url: config.url })
    throw new Error(
      'Displaying, expanding, and selecting users must not call an API'
    )
  }
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...DEFAULT_CURRENCY_CONFIG,
      currencyUnit: 'credit',
      creditsPerUsd: 3000000,
      creditsPerUsdExact: '3000000',
      cnyPerUsd: 7.2,
      cnyPerUsdExact: '7.2',
      legacyPricingUnitsPerUsd: 6,
      quotaPerUnit: 500000,
    },
  })
  useWalletCurrencyPreferenceStore.getState().setPreference(preference)
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  function Harness() {
    const [rowSelection, setRowSelection] = useState<RowSelectionState>({})
    const table = useReactTable<User>({
      data: users,
      columns: [],
      getRowId: (user) => String(user.id),
      getCoreRowModel: getCoreRowModel(),
      enableRowSelection: true,
      onRowSelectionChange: setRowSelection,
      state: { rowSelection },
    })
    return (
      <I18nextProvider i18n={i18n}>
        <UsersMobileList
          table={table}
          emptyTitle='No users'
          emptyDescription='No users found'
        />
        <output aria-label='Selected user IDs'>
          {table
            .getSelectedRowModel()
            .rows.map((row) => row.original.id)
            .join(',')}
        </output>
      </I18nextProvider>
    )
  }
  await act(async () => root.render(<Harness />))
  return {
    host,
    requests,
    click: async (element: HTMLElement) => {
      await act(async () => element.click())
    },
    dispose: async () => {
      await act(async () => root.unmount())
      host.remove()
      api.defaults.adapter = originalAdapter
    },
  }
}

function disclosure(article: HTMLElement) {
  const button = article.querySelector<HTMLButtonElement>(
    'button[aria-controls]'
  )
  assert.ok(button, 'Each user needs a details button')
  const id = button.getAttribute('aria-controls')
  assert.ok(id)
  const region = document.getElementById(id)
  assert.ok(
    region && article.contains(region),
    'The button must control its own details'
  )
  return { button, region }
}

function summaryText(article: HTMLElement, region: HTMLElement) {
  return [...article.children]
    .filter((child) => child !== region)
    .map((child) => child.textContent)
    .join(' ')
}

function firstArticle(host: HTMLElement) {
  const article = host.querySelector('article')
  assert.ok(article, 'The list must contain a user row')
  return article
}

test('the collapsed row keeps complete identity, status, balance, and fiat top-up visible', async () => {
  const user = makeUser()
  const view = await renderList([user])
  try {
    const article = firstArticle(view.host)
    const { button, region } = disclosure(article)
    const summary = summaryText(article, region)
    for (const value of [
      user.username,
      user.display_name,
      'a-long-account-address+mobile-list@example.test',
      '#42',
      'Enabled',
      'mobile-fixture',
      'Admin',
      '2.06 USD',
      '98.76 CNY',
      'Edit user',
    ]) {
      assert.ok(summary.includes(value), `${value} must remain in the summary`)
    }
    assert.equal(
      article.querySelector('[title]')?.getAttribute('title'),
      user.email
    )
    assert.equal(button.getAttribute('aria-expanded'), 'false')
    assert.equal(region.hidden, true)
    assert.deepEqual(view.requests, [])
  } finally {
    await view.dispose()
  }
})

test('details open and close accessibly without unmounting controls or writing user data', async () => {
  const view = await renderList([makeUser()])
  try {
    const { button, region } = disclosure(firstArticle(view.host))
    const draft = region.querySelector<HTMLInputElement>('input')
    assert.ok(draft, 'Detail controls remain mounted while collapsed')
    assert.equal(draft.closest('[hidden]'), region)
    await view.click(button)
    assert.equal(button.getAttribute('aria-expanded'), 'true')
    assert.equal(region.hidden, false)
    for (const name of [
      'Risk details',
      'Conversation history',
      'Assistant review',
    ]) {
      const control = [...region.querySelectorAll('button')].find(
        (candidate) => candidate.textContent === name
      )
      assert.ok(control, `${name} must remain reachable in details`)
      assert.equal(control.closest('[hidden]'), null)
    }
    draft.value = 'Keep this unsaved note'
    await view.click(button)
    assert.equal(button.getAttribute('aria-expanded'), 'false')
    assert.equal(region.hidden, true)
    await view.click(button)
    assert.equal(region.hidden, false)
    assert.equal(region.querySelector('input'), draft)
    assert.equal(draft.value, 'Keep this unsaved note')
    assert.deepEqual(view.requests, [])
  } finally {
    await view.dispose()
  }
})

test('the labeled checkbox changes only the selected row and keeps detail IDs independent', async () => {
  const view = await renderList([
    makeUser(),
    makeUser({ id: 43, username: 'second-user' }),
  ])
  try {
    const articles = [...view.host.querySelectorAll('article')]
    const first = disclosure(articles[0])
    const second = disclosure(articles[1])
    assert.notEqual(first.region.id, second.region.id)
    const checkbox = articles[0].querySelector<HTMLElement>('[role="checkbox"]')
    assert.ok(checkbox)
    assert.equal(
      checkbox.getAttribute('aria-label'),
      `Select row: ${makeUser().username}`
    )
    const label = articles[0].querySelector<HTMLLabelElement>('label[for]')
    assert.ok(label)
    const nativeCheckbox = articles[0].querySelector<HTMLInputElement>(
      'input[type="checkbox"]'
    )
    assert.ok(nativeCheckbox)
    assert.equal(label.htmlFor, nativeCheckbox.id)
    await view.click(label)
    assert.equal(checkbox.getAttribute('aria-checked'), 'true')
    assert.equal(view.host.querySelector('output')?.textContent, '42')
    assert.equal(
      articles[1]
        .querySelector('[role="checkbox"]')
        ?.getAttribute('aria-checked'),
      'false'
    )
    await view.click(first.button)
    assert.equal(first.region.hidden, false)
    assert.equal(second.region.hidden, true)
    await view.click(label)
    assert.equal(checkbox.getAttribute('aria-checked'), 'false')
    assert.equal(view.host.querySelector('output')?.textContent, '')
    assert.deepEqual(view.requests, [])
  } finally {
    await view.dispose()
  }
})

test('multiple settlement currencies retain method amounts without presenting their aggregate as fiat', async () => {
  const view = await renderList([
    makeUser({
      topup_summary: {
        quota: 9_000_000,
        money_micros: 777_000_000,
        currency: 'MULTIPLE',
        orders: 6,
        methods: [
          {
            method: 'Card',
            provider: 'USD provider',
            settlement_currency: ' usd ',
            quota: 1_000_000,
            money_micros: 12_340_000,
            orders: 2,
          },
          {
            method: 'Bank',
            provider: 'CNY provider',
            settlement_currency: 'cny',
            quota: 2_000_000,
            money_micros: 56_780_000,
            orders: 3,
          },
          {
            method: 'Legacy',
            provider: 'Unknown provider',
            settlement_currency: 'UNKNOWN',
            quota: 6_000_000,
            money_micros: 9_876_543,
            orders: 1,
          },
        ],
      },
    }),
  ])
  try {
    const article = firstArticle(view.host)
    const { button, region } = disclosure(article)
    const summary = summaryText(article, region)
    assert.ok(summary.includes('Multiple fiat currencies'))
    assert.equal(summary.includes('777'), false)
    await view.click(button)
    const payment = region.querySelector('details')
    assert.ok(payment)
    const paymentButton = payment.querySelector('summary')
    assert.ok(paymentButton)
    assert.equal(paymentButton.textContent, 'Payment method · 3')
    assert.equal(paymentButton.closest('[hidden]'), null)
    await view.click(paymentButton)
    assert.equal(payment.open, true)
    for (const [label, fiat, quota, orders] of [
      ['Card · USD provider', '12.34 USD', '0.333333 USD', '2'],
      ['Bank · CNY provider', '56.78 CNY', '0.666667 USD', '3'],
      [
        'Legacy · Unknown provider',
        '9.876543 (Currency unavailable)',
        '2 USD',
        '1',
      ],
    ]) {
      const methodLabel: HTMLSpanElement | undefined = [
        ...payment.querySelectorAll('span'),
      ].find((span) => span.textContent === label)
      assert.ok(methodLabel, `${label} must remain in payment details`)
      const method: HTMLElement | null = methodLabel.parentElement
      assert.ok(method)
      assert.ok(
        method.textContent?.includes(fiat),
        `${label} must retain its own settlement currency`
      )
      assert.ok(
        method.textContent?.includes(quota),
        `${label} must retain its own credited quota`
      )
      assert.ok(
        method.textContent?.trim().endsWith(`· ${orders}`),
        `${label} must retain its own order count`
      )
    }
    await view.click(button)
    assert.equal(region.hidden, true)
    await view.click(button)
    assert.equal(region.querySelector('details'), payment)
    assert.equal(
      payment.open,
      true,
      'Closing the row must preserve the payment-method disclosure'
    )
    assert.deepEqual(view.requests, [])
  } finally {
    await view.dispose()
  }
})

test('unknown currency stays explicit and a missing email keeps a usable identity fallback', async () => {
  const view = await renderList(
    [
      makeUser({
        email: '   ',
        topup_summary: {
          quota: 500_000,
          money_micros: 9_876_543,
          currency: 'UNKNOWN',
          orders: 1,
          methods: [
            {
              method: 'Legacy',
              settlement_currency: 'UNKNOWN',
              quota: 500_000,
              money_micros: 9_876_543,
              orders: 1,
            },
          ],
        },
      }),
    ],
    'CREDIT'
  )
  try {
    const article = firstArticle(view.host)
    const { button, region } = disclosure(article)
    const summary = summaryText(article, region)
    assert.ok(summary.includes('No email provided'))
    assert.ok(summary.includes('Currency unavailable'))
    assert.equal(summary.includes('USD'), false)
    assert.equal(summary.includes('CNY'), false)
    await view.click(button)
    const payment = region.querySelector('details')
    assert.ok(payment)
    const paymentButton = payment.querySelector('summary')
    assert.ok(paymentButton)
    await view.click(paymentButton)
    assert.equal(payment.open, true)
    assert.ok(payment.textContent?.includes('9.876543 (Currency unavailable)'))
    assert.deepEqual(view.requests, [])
  } finally {
    await view.dispose()
  }
})

test('native Credit display retains raw balances while settlement amounts keep their own fiat currency', async () => {
  const view = await renderList([makeUser()], 'CREDIT')
  try {
    const article = firstArticle(view.host)
    const { button, region } = disclosure(article)
    const summary = summaryText(article, region)
    assert.ok(summary.includes('6,175,000 Credits'))
    assert.ok(summary.includes('98.76 CNY'))
    assert.equal(summary.includes('USD'), false)
    await view.click(button)
    const payment = region.querySelector('details')
    assert.ok(payment)
    const paymentButton = payment.querySelector('summary')
    assert.ok(paymentButton)
    await view.click(paymentButton)
    assert.ok(payment.textContent?.includes('98.76 CNY'))
    assert.ok(payment.textContent?.includes('49,380,000 Credits'))
    assert.deepEqual(view.requests, [])
  } finally {
    await view.dispose()
  }
})
