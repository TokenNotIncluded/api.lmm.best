/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

import type {
  StoreRefund,
  StoreRefundInput,
  StoreRefundView,
} from './refund-types'
import type { StoreClaimMetadata } from './types'

const dom = new Window({ url: 'https://shop.example.test/store/orders' })
dom.document.write('<!doctype html><html><head></head><body></body></html>')
Object.defineProperty(dom.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'HTMLTextAreaElement',
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
  'matchMedia',
  'customElements',
  'CSSStyleSheet',
  'localStorage',
  'sessionStorage',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  value: true,
})
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { useAuthStore } = await import('@/stores/auth-store')
const { DEFAULT_CURRENCY_CONFIG, useSystemConfigStore } =
  await import('@/stores/system-config-store')
const { api } = await import('@/lib/api')
const { StoreRefundPanel, StorePickupRefunds, StoreRootRefunds } =
  await import('./refund-panel')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const originalGet = api.get
const originalPost = api.post
let root: ReturnType<typeof createRoot> | undefined
let client: InstanceType<typeof QueryClient> | undefined
const orderId = 'order-refund-fixture'
const view: StoreRefundView = {
  order_id: orderId,
  product_title: 'Original product',
  variant_name: 'Original blue variant',
  payment_method: 'balance',
  currency: 'USD',
  principal_quota: 1500000,
  refunded_quota: 500000,
  reserved_quota: 0,
  remaining_quota: 1000000,
  quantity: 3,
  refunded_quantity: 1,
  max_quantity: 2,
  eligible_items: [
    { stock_id: '00000000-0000-0000-0000-000000000002', position: 2 },
    { stock_id: '00000000-0000-0000-0000-000000000003', position: 3 },
  ],
  supports_quantity: true,
  supports_amount: true,
  native_basis_verified: false,
  refunds: [],
}
const refund: StoreRefund = {
  id: 'refund-fixture',
  order_id: orderId,
  request_key: 'original-request-key',
  mode: 'full',
  quantity: 2,
  stock_ids: view.eligible_items.map((item) => item.stock_id),
  amount_quota: 1000000,
  amount_minor: 0,
  currency: 'USD',
  reason: 'Wrong item',
  status: 'requested',
  requested_by: 2,
  requested_role: 'buyer',
  decision_by: 0,
  decision_reason: '',
  retained_fee_quota: 0,
  created_at: 1000,
  decided_at: 0,
  completed_at: 0,
}
type Request = {
  method: 'GET' | 'POST'
  url: string
  body?: unknown
  config?: unknown
}
const envelope = (data: unknown) => ({ data: { success: true, data } })
function requests(
  reply?: (
    request: Request
  ) => ReturnType<typeof envelope> | Promise<ReturnType<typeof envelope>>
) {
  const records: Request[] = []
  function response(request: Request) {
    records.push(request)
    return reply
      ? reply(request)
      : envelope(
          request.method === 'GET' || request.url.endsWith('/read')
            ? view
            : refund
        )
  }
  api.get = (async (url, config) =>
    response({ method: 'GET', url, config })) as typeof api.get
  api.post = (async (url, body, config) =>
    response({ method: 'POST', url, body, config })) as typeof api.post
  return records
}
async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 30))
}
async function mount(
  node: React.ReactNode,
  role: number | null = 1,
  currency: 'USD' | 'CNY' | 'CREDIT' = 'CNY'
) {
  useAuthStore.getState().auth.setUser(
    role === null
      ? null
      : {
          id: 2,
          role,
          username: 'refund-owner',
          quota: 5000000,
          setting: JSON.stringify({ wallet_display_currency: currency }),
        }
  )
  const host = document.createElement('div')
  document.body.append(host)
  const mountedRoot = createRoot(host)
  const mountedClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  root = mountedRoot
  client = mountedClient
  await act(async () => {
    mountedRoot.render(
      <QueryClientProvider client={mountedClient}>
        <I18nextProvider i18n={i18n}>{node}</I18nextProvider>
      </QueryClientProvider>
    )
    await flush()
  })
  await act(flush)
}
function button(text: string) {
  const node = [...document.querySelectorAll<HTMLButtonElement>('button')].find(
    (item) => item.textContent?.trim() === text
  )
  assert.ok(node, `button ${text}: ${document.body.textContent}`)
  return node
}
function hasButton(text: string) {
  return [...document.querySelectorAll('button')].some(
    (item) => item.textContent?.trim() === text
  )
}
function required<T>(value: T | null | undefined): T {
  assert.ok(value)
  return value
}
function field(label: string) {
  const node = [...document.querySelectorAll('label')].find(
    (item) => item.textContent?.trim() === label
  )
  assert.ok(node, `field ${label}`)
  const input = document.getElementById(node.htmlFor) as
    | HTMLInputElement
    | HTMLTextAreaElement
    | null
  assert.ok(input)
  return input
}
async function input(
  node: HTMLInputElement | HTMLTextAreaElement,
  value: string
) {
  const prototype =
    node.tagName === 'TEXTAREA'
      ? dom.HTMLTextAreaElement.prototype
      : dom.HTMLInputElement.prototype
  const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set
  assert.ok(setter)
  await act(async () => {
    setter.call(node, value)
    node.dispatchEvent(new Event('input', { bubbles: true }))
    await flush()
  })
}
async function click(node: HTMLElement) {
  await act(async () => {
    node.click()
    await flush()
  })
}
async function submit(
  label = 'Refund reason',
  reason = 'Wrong item',
  submitButton = 'Request refund'
) {
  await input(field(label), reason)
  await click(button(submitButton))
}
async function changeWalletCurrency(currency: 'USD' | 'CNY' | 'CREDIT') {
  await act(async () => {
    const user = required(useAuthStore.getState().auth.user)
    useAuthStore.getState().auth.setUser({
      ...user,
      setting: JSON.stringify({ wallet_display_currency: currency }),
    })
    await flush()
  })
}
beforeEach(() => {
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...DEFAULT_CURRENCY_CONFIG,
      currencyUnit: 'credit',
      creditsPerUsd: 500000,
      creditsPerUsdExact: '500000',
      cnyPerUsd: 7,
      cnyPerUsdExact: '7',
    },
  })
  localStorage.clear()
})
afterEach(async () => {
  const mountedRoot = root
  if (mountedRoot) await act(async () => mountedRoot.unmount())
  root = undefined
  client?.clear()
  api.get = originalGet
  api.post = originalPost
  document.body.replaceChildren()
  sessionStorage.clear()
  localStorage.clear()
})
after(() => dom.happyDOM.abort())

test('buyer full refund preserves original order identity and submits once despite rapid clicks', async () => {
  let release!: (value: ReturnType<typeof envelope>) => void
  const waiting = new Promise<ReturnType<typeof envelope>>((resolve) => {
    release = resolve
  })
  const records = requests((request) =>
    request.method === 'GET' ? envelope(view) : waiting
  )
  await mount(
    <StoreRefundPanel
      orderId={orderId}
      audience='buyer'
      showOrderIdentity
      initiallyOpen
    />
  )
  assert.match(document.body.textContent || '', /Original blue variant/)
  assert.match(document.body.textContent || '', /14 CNY/)
  assert.doesNotMatch(document.body.textContent || '', /Credits/)
  await input(field('Refund reason'), 'Wrong item')
  await act(async () => {
    button('Request refund').click()
    button('Request refund').click()
    await flush()
  })
  assert.equal(records.filter((record) => record.method === 'POST').length, 1)
  const body = records.find((record) => record.method === 'POST')
    ?.body as StoreRefundInput
  assert.equal(body.mode, 'full')
  assert.equal(body.reason, 'Wrong item')
  assert.match(body.request_key, /^[a-f0-9-]{36}$/)
  await act(async () => {
    release(envelope({ ...refund, request_key: body.request_key }))
    await flush()
  })
})

test('quantity selection submits only exact remaining card item IDs without showing card contents', async () => {
  const records = requests()
  await mount(
    <StoreRefundPanel orderId={orderId} audience='buyer' initiallyOpen />
  )
  await click(button('Refund by quantity'))
  const item = field('Item 3')
  await click(item)
  assert.equal(field('Quantity to refund').value, '1')
  await submit()
  const body = records.find((record) => record.method === 'POST')
    ?.body as StoreRefundInput
  assert.deepEqual(body.stock_ids, [view.eligible_items[1].stock_id])
  assert.equal(body.quantity, 1)
  assert.equal(body.mode, 'quantity')
  assert.ok(records.every((record) => record.url.includes('/refunds')))
  assert.doesNotMatch(
    document.body.textContent || '',
    /00000000-|password|secret/i
  )
})

test('fixed content refunds accept numeric quantities without rendering card selectors', async () => {
  const fixed = {
    ...view,
    delivery_template: 'fixed-content',
    quantity: 5,
    refunded_quantity: 1,
    max_quantity: 3,
    eligible_items: [],
  }
  const records = requests((request) =>
    envelope(
      request.method === 'GET'
        ? fixed
        : { ...refund, quantity: 2, stock_ids: [] }
    )
  )
  await mount(
    <StoreRefundPanel orderId={orderId} audience='buyer' initiallyOpen />
  )
  await click(button('Refund by quantity'))
  assert.doesNotMatch(
    document.body.textContent || '',
    /Choose specific items|original delivery order/
  )
  assert.equal(document.querySelector('[role="checkbox"]'), null)
  await input(field('Quantity to refund'), '4')
  await input(field('Refund reason'), 'Wrong quantity')
  assert.equal(button('Request refund').disabled, true)
  await input(field('Quantity to refund'), '2')
  await click(button('Request refund'))
  const body = records.find((record) => record.method === 'POST')
    ?.body as StoreRefundInput
  assert.equal(body.quantity, 2)
  assert.equal(body.mode, 'quantity')
  assert.equal(body.stock_ids, undefined)
})

test('amount refund is integer Credits and disables totals above the remaining original order value', async () => {
  const records = requests()
  await mount(
    <StoreRefundPanel orderId={orderId} audience='buyer' initiallyOpen />,
    1,
    'CREDIT'
  )
  await click(button('Refund by amount'))
  await input(field('Refund reason'), 'Partial adjustment')
  for (const amount of ['1000001', '0.5', '1e3']) {
    await input(field('Refund amount (Credits)'), amount)
    assert.equal(button('Request refund').disabled, true)
  }
  await input(field('Refund amount (Credits)'), '125000')
  await click(button('Request refund'))
  const body = records.find((record) => record.method === 'POST')
    ?.body as StoreRefundInput
  assert.equal(body.amount_quota, 125000)
  assert.equal(body.amount_minor, undefined)
})

test('unverified external payment hides partial options and does not present quote as actual payment', async () => {
  const external: StoreRefundView = {
    ...view,
    payment_method: 'external:epay',
    currency: 'JPY',
    amount_minor: 1200,
  }
  requests((request) => envelope(request.method === 'GET' ? external : refund))
  await mount(
    <StoreRefundPanel orderId={orderId} audience='buyer' initiallyOpen />
  )
  assert.equal(hasButton('Refund by quantity'), false)
  assert.equal(hasButton('Refund by amount'), false)
  assert.doesNotMatch(
    document.body.textContent || '',
    /Original payment|JPY|1,200/
  )
})

test('unknown network result persists the original request and never posts a new key on status check', async () => {
  let fail = true
  const records = requests((request) => {
    if (request.method === 'GET') return envelope(view)
    if (fail) return Promise.reject(new Error('connection lost after request'))
    return envelope(refund)
  })
  await mount(
    <StoreRefundPanel orderId={orderId} audience='buyer' initiallyOpen />
  )
  await submit()
  const first = records.find((record) => record.method === 'POST')
    ?.body as StoreRefundInput
  assert.equal(
    JSON.parse(
      required(sessionStorage.getItem(required(sessionStorage.key(0))))
    ).request_key,
    first.request_key
  )
  assert.match(document.body.textContent || '', /result is not known yet/)
  assert.equal(hasButton('Request refund'), false)
  await act(async () => required(root).unmount())
  client?.clear()
  root = undefined
  document.body.replaceChildren()
  await mount(
    <StoreRefundPanel orderId={orderId} audience='buyer' initiallyOpen />
  )
  assert.equal(hasButton('Request refund'), false)
  await click(button('Check refund status'))
  assert.equal(records.filter((record) => record.method === 'POST').length, 1)
  fail = false
  await click(button('Retry the same refund request'))
  const writes = records.filter((record) => record.method === 'POST')
  assert.equal(writes.length, 2)
  assert.deepEqual(writes[1].body, first)
  assert.equal(sessionStorage.length, 0)
})

test('status lookup recognizes a previously submitted request without replaying it', async () => {
  let accepted = false
  const records = requests((request) => {
    if (request.method === 'GET') {
      return envelope({
        ...view,
        remaining_quota: accepted ? 0 : view.remaining_quota,
        refunds: accepted
          ? [
              {
                ...refund,
                request_key: required(submitted).request_key,
                status: 'awaiting_provider',
              },
            ]
          : [],
      })
    }
    accepted = true
    submitted = request.body as StoreRefundInput
    return Promise.reject(new Error('timeout'))
  })
  let submitted: StoreRefundInput | undefined
  await mount(
    <StoreRefundPanel orderId={orderId} audience='buyer' initiallyOpen />
  )
  await submit()
  await click(button('Check refund status'))
  assert.match(document.body.textContent || '', /Awaiting payment provider/)
  assert.doesNotMatch(
    document.body.textContent || '',
    /Refund completed|result is not known yet/
  )
  assert.equal(hasButton('Retry the same refund request'), false)
  assert.equal(records.filter((record) => record.method === 'POST').length, 1)
  assert.equal(sessionStorage.length, 0)
})

test('seller can decide pending requests and proactively refund; provider and reconciliation states have no cancel or reject', async () => {
  const records = requests((request) =>
    envelope(request.method === 'GET' ? { ...view, refunds: [refund] } : refund)
  )
  await mount(
    <StoreRefundPanel orderId={orderId} audience='seller' initiallyOpen />
  )
  await input(field('Decision reason'), 'Agreed')
  await click(button('Approve refund'))
  assert.deepEqual(
    records.find((record) => record.url.endsWith('/decision'))?.body,
    { decision: 'approve', reason: 'Agreed' }
  )
  await click(button('Reject refund'))
  assert.deepEqual(
    required(
      [...records].reverse().find((record) => record.url.endsWith('/decision'))
    ).body,
    { decision: 'reject', reason: 'Agreed' }
  )
  await submit('Refund reason', 'Seller correction', 'Refund this order')
  assert.ok(records.some((record) => record.url.endsWith('/proactive')))
  assert.equal(hasButton('Cancel refund request'), false)
})

test('provider reconciliation stays pending with no approve/reject/cancel action', async () => {
  requests(() =>
    envelope({
      ...view,
      remaining_quota: 0,
      refunds: [
        { ...refund, status: 'reconciliation_required' },
        { ...refund, id: 'provider-refund', status: 'awaiting_provider' },
      ],
    })
  )
  await mount(
    <StoreRefundPanel orderId={orderId} audience='seller' initiallyOpen />
  )
  assert.match(
    document.body.textContent || '',
    /Payment refunded; platform settlement pending/
  )
  assert.match(document.body.textContent || '', /Awaiting payment provider/)
  for (const label of [
    'Approve refund',
    'Reject refund',
    'Cancel refund request',
    'Refund this order',
  ]) {
    assert.equal(hasButton(label), false)
  }
})

test('cold-cookie collection authority requires actual member sign-in before protected refund actions', async () => {
  const records = requests()
  const previousPath = window.location.pathname
  window.history.replaceState({}, '', '/store/claim/original-pickup-proof')
  const metadata: StoreClaimMetadata = {
    order_id: orderId,
    product_title: 'Original product',
    quantity: 3,
    status: 'paid',
    pickup_login_required: true,
    pickup_login_satisfied: true,
    pickup_code_required: false,
  }
  await mount(
    <StorePickupRefunds
      metadata={metadata}
      token='original-pickup-proof'
      onChanged={() => {}}
    />,
    null
  )
  assert.match(
    document.body.textContent || '',
    /Sign in with the purchasing account to view or request refunds\./
  )
  assert.equal(hasButton('View refunds or request a refund'), false)
  assert.equal(document.querySelector('form'), null)
  assert.equal(
    records.length,
    0,
    'the collection cookie does not authorize refund reads'
  )
  const signIn = document.querySelector<HTMLAnchorElement>(
    'a[href^="/sign-in?"]'
  )
  assert.ok(signIn)
  assert.equal(
    new URL(signIn.href).searchParams.get('redirect'),
    '/store/claim/original-pickup-proof'
  )
  await act(async () => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 2, role: 1, username: 'actual-purchaser' })
    await flush()
  })
  await click(button('View refunds or request a refund'))
  await act(flush)
  assert.equal(records[0].url, '/api/store/pickup/refunds/read')
  assert.deepEqual(records[0].body, {
    order_id: orderId,
    token: 'original-pickup-proof',
  })
  assert.ok(
    records.every(({ url }) => url.startsWith('/api/store/pickup/refunds/'))
  )
  assert.doesNotMatch(
    JSON.stringify({ ...localStorage, ...sessionStorage }),
    /original-pickup-proof/
  )
  window.history.replaceState({}, '', previousPath)
})
test('pickup refund reads and requests only with proof, without claim side effects or persisted proof secrets', async () => {
  const records = requests()
  const metadata: StoreClaimMetadata = {
    order_id: orderId,
    product_title: 'Original product',
    quantity: 3,
    status: 'refund_pending',
    pickup_login_required: false,
    pickup_code_required: true,
    pickup_login_satisfied: false,
  }
  await mount(
    <StorePickupRefunds
      metadata={metadata}
      token='private-pickup-token'
      onChanged={() => {}}
    />,
    null
  )
  await input(field('Pickup code'), 'private-pickup-code')
  await click(button('View refunds or request a refund'))
  await act(flush)
  assert.deepEqual(records[0].body, {
    order_id: orderId,
    token: 'private-pickup-token',
    code: 'private-pickup-code',
  })
  assert.equal(records[0].url, '/api/store/pickup/refunds/read')
  assert.match(document.body.textContent || '', /Refund reason/)
  await submit()
  const write = records.find((record) => record.url.endsWith('/request'))
  assert.ok(write)
  assert.equal((write.body as { input: StoreRefundInput }).input.mode, 'full')
  assert.ok(
    records.every((record) =>
      record.url.startsWith('/api/store/pickup/refunds/')
    )
  )
  assert.ok(
    records.every(
      (record) =>
        (record.config as { skipAuthRefresh: boolean }).skipAuthRefresh
    )
  )
  assert.doesNotMatch(
    document.body.textContent || '',
    /private-pickup-token|private-pickup-code/
  )
  assert.equal(localStorage.length, 0)
  assert.equal(sessionStorage.length, 0)
  await click(button('Change pickup code'))
  assert.equal(field('Pickup code').value, '')
})

test('ordinary admin has no Root refund lookup or read; Root can inspect frozen order identity directly', async () => {
  const records = requests()
  await mount(
    <>
      <StoreRootRefunds />
      <StoreRefundPanel orderId={orderId} audience='root' initiallyOpen />
    </>,
    10
  )
  assert.equal(document.body.textContent, '')
  assert.equal(records.length, 0)
  await act(async () => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 2, role: 100, username: 'root', quota: 5000000 })
    await flush()
  })
  await input(field('Order ID'), orderId)
  await click(button('Find order refunds'))
  assert.match(document.body.textContent || '', /Original blue variant/)
  assert.ok(records.every((record) => record.url.endsWith('/refunds')))
  assert.equal(hasButton('Refund this order'), true)
})

test('unknown native currency keeps raw historical units and disables amount without guessing ISO precision', async () => {
  const native: StoreRefundView = {
    ...view,
    payment_method: 'platform:waffo_pancake',
    currency: 'JPY',
    native_basis_verified: true,
    amount_minor: 3000,
    remaining_amount_minor: 2000,
    refunds: [{ ...refund, amount_minor: 125, currency: 'JPY' }],
  }
  const records = requests((request) =>
    envelope(
      request.method === 'GET'
        ? native
        : {
            ...refund,
            amount_minor: 125,
            currency: 'JPY',
            status: 'awaiting_provider',
          }
    )
  )
  await mount(
    <StoreRefundPanel orderId={orderId} audience='buyer' initiallyOpen />
  )
  assert.match(document.body.textContent || '', /3000 JPY minor units/)
  assert.match(document.body.textContent || '', /125 JPY minor units/)
  assert.match(
    document.body.textContent || '',
    /not supported for this currency/
  )
  assert.equal(hasButton('Refund by amount'), false)
  assert.equal(hasButton('Full refund'), true)
  assert.equal(hasButton('Refund by quantity'), true)
  assert.equal(records.filter((record) => record.method === 'POST').length, 0)
})

for (const currency of ['USD', 'CNY'] as const) {
  test(`${currency} native amount accepts 20.00 and preserves original payment/history currency`, async () => {
    const native: StoreRefundView = {
      ...view,
      payment_method: 'platform:waffo_pancake',
      currency,
      native_basis_verified: true,
      amount_minor: 5000,
      remaining_amount_minor: 3000,
      refunds: [
        { ...refund, amount_minor: 250, currency, status: 'completed' },
      ],
    }
    const records = requests((request) =>
      envelope(request.method === 'GET' ? native : refund)
    )
    await mount(
      <StoreRefundPanel orderId={orderId} audience='buyer' initiallyOpen />,
      1,
      currency === 'USD' ? 'CNY' : 'USD'
    )
    assert.match(
      document.body.textContent || '',
      new RegExp(`50.00 ${currency}`)
    )
    assert.match(
      document.body.textContent || '',
      new RegExp(`2.50 ${currency}`)
    )
    await click(button('Refund by amount'))
    const amount = field(`Refund amount (${currency})`)
    assert.equal(amount.inputMode, 'decimal')
    assert.match(
      document.body.textContent || '',
      new RegExp(`Maximum refund: 30.00 ${currency}`)
    )
    await input(amount, '20.00')
    await changeWalletCurrency(currency)
    assert.equal(field(`Refund amount (${currency})`).value, '20.00')
    await submit()
    const body = required(records.find((record) => record.method === 'POST'))
      .body as StoreRefundInput
    assert.equal(body.amount_minor, 2000)
    assert.equal(body.amount_quota, undefined)
  })

  test(`${currency} balance amount converts an ordinary decimal to integer ledger quota`, async () => {
    const records = requests((request) =>
      envelope(
        request.method === 'GET'
          ? {
              ...view,
              remaining_quota: 15000000,
              refunds: [
                { ...refund, amount_quota: 125001, status: 'completed' },
              ],
            }
          : refund
      )
    )
    await mount(
      <StoreRefundPanel orderId={orderId} audience='buyer' initiallyOpen />,
      1,
      currency
    )
    assert.match(
      required(document.querySelector('dl')).textContent || '',
      currency === 'USD' ? /3 USD/ : /21 CNY/
    )
    const historyAmount = required(
      document.querySelector('article .tabular-nums')
    )
    assert.equal(
      historyAmount.textContent,
      currency === 'USD' ? '0.250002 USD' : '1.75 CNY'
    )
    assert.doesNotMatch(historyAmount.textContent || '', /1\.750014/)
    await click(button('Refund by amount'))
    assert.match(
      document.body.textContent || '',
      currency === 'USD' ? /Maximum refund: 30 USD/ : /Maximum refund: 210 CNY/
    )
    await input(
      field(`Refund amount (${currency})`),
      currency === 'USD' ? '1.25' : '20.00'
    )
    await submit()
    const body = required(records.find((record) => record.method === 'POST'))
      .body as StoreRefundInput
    assert.equal(body.amount_quota, currency === 'USD' ? 625000 : 1428571)
    assert.equal(body.amount_minor, undefined)
  })
}

test('balance amount draft clears on currency or FX change instead of reinterpreting its old value', async () => {
  const records = requests((request) =>
    envelope(
      request.method === 'GET' ? { ...view, remaining_quota: 15000000 } : refund
    )
  )
  await mount(
    <StoreRefundPanel orderId={orderId} audience='buyer' initiallyOpen />
  )
  await click(button('Refund by amount'))
  await input(field('Refund amount (CNY)'), '20.00')
  await input(field('Refund reason'), 'Partial adjustment')
  assert.equal(button('Request refund').disabled, false)
  await changeWalletCurrency('USD')
  assert.equal(field('Refund amount (USD)').value, '')
  assert.equal(button('Request refund').disabled, true)
  await changeWalletCurrency('CNY')
  assert.equal(field('Refund amount (CNY)').value, '')
  await input(field('Refund amount (CNY)'), '20.00')
  await act(async () => {
    useSystemConfigStore.getState().setConfig({
      currency: {
        ...useSystemConfigStore.getState().config.currency,
        cnyPerUsd: 8,
        cnyPerUsdExact: '8',
      },
    })
    await flush()
  })
  assert.equal(field('Refund amount (CNY)').value, '')
  assert.equal(button('Request refund').disabled, true)
  assert.equal(records.filter((record) => record.method === 'POST').length, 0)
})

test('unknown amount result retries its original integer body and key after wallet denomination changes', async () => {
  let fail = true
  const records = requests((request) => {
    if (request.method === 'GET') return envelope(view)
    if (fail) return Promise.reject(new Error('lost response'))
    return envelope(refund)
  })
  await mount(
    <StoreRefundPanel orderId={orderId} audience='buyer' initiallyOpen />
  )
  await click(button('Refund by amount'))
  await input(field('Refund amount (CNY)'), '14.00')
  await submit()
  const first = required(records.find((record) => record.method === 'POST'))
    .body as StoreRefundInput
  assert.equal(first.amount_quota, 1000000)
  assert.equal(hasButton('Request refund'), false)
  await changeWalletCurrency('USD')
  await click(button('Check refund status'))
  assert.equal(records.filter((record) => record.method === 'POST').length, 1)
  fail = false
  await click(button('Retry the same refund request'))
  const writes = records.filter((record) => record.method === 'POST')
  assert.equal(writes.length, 2)
  assert.deepEqual(writes[1].body, first)
})

test('balance history keeps the smallest ledger amount visible in the wallet currency', async () => {
  requests(() =>
    envelope({
      ...view,
      refunds: [{ ...refund, amount_quota: 1, status: 'completed' }],
    })
  )
  await mount(
    <StoreRefundPanel orderId={orderId} audience='buyer' initiallyOpen />,
    1,
    'USD'
  )
  assert.match(document.body.textContent || '', /0.000002 USD/)
})

test('acknowledged refund with failed refresh blocks another request until authoritative status is read', async () => {
  let written = false
  let readable = false
  const records = requests((request) => {
    if (request.method === 'POST') {
      written = true
      return envelope(refund)
    }
    if (written && !readable) {
      return Promise.reject(new Error('status unavailable'))
    }
    return envelope(
      written ? { ...view, remaining_quota: 0, refunds: [refund] } : view
    )
  })
  await mount(
    <StoreRefundPanel orderId={orderId} audience='buyer' initiallyOpen />
  )
  await submit()
  assert.match(
    document.body.textContent || '',
    /Refund recorded. Refresh its status/
  )
  assert.equal(hasButton('Request refund'), false)
  assert.equal(hasButton('Retry the same refund request'), false)
  readable = true
  await click(button('Check refund status'))
  assert.doesNotMatch(
    document.body.textContent || '',
    /Refund recorded. Refresh its status/
  )
  assert.equal(records.filter((record) => record.method === 'POST').length, 1)
})

test('buyer can cancel only a requested refund using its existing refund identity', async () => {
  let cancelled = false
  const records = requests((request) => {
    if (request.method === 'POST') {
      cancelled = true
      return envelope({ ...refund, status: 'cancelled' })
    }
    return envelope({
      ...view,
      refunds: [{ ...refund, status: cancelled ? 'cancelled' : 'requested' }],
    })
  })
  await mount(
    <StoreRefundPanel orderId={orderId} audience='buyer' initiallyOpen />
  )
  await click(button('Cancel refund request'))
  assert.equal(
    records.find((record) => record.method === 'POST')?.url,
    `/api/store/orders/${orderId}/refunds/${refund.id}/cancel`
  )
  assert.match(document.body.textContent || '', /Refund request cancelled/)
  assert.equal(hasButton('Cancel refund request'), false)
})
