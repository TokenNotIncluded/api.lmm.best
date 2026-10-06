/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

import type { StorePromotionCode, StorePromotionQuote } from './promotion-types'
import type { StoreProduct, StoreVariant } from './types'

const dom = new Window({
  url: 'https://shop.example.test/store/products/product-fixture',
})
dom.document.write('<!doctype html><html><head></head><body></body></html>')
Object.defineProperty(dom.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
const originalGlobals = new Map<string, PropertyDescriptor | undefined>()
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
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
] as const) {
  originalGlobals.set(key, Object.getOwnPropertyDescriptor(globalThis, key))
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value:
      key === 'getComputedStyle' ? dom.getComputedStyle.bind(dom) : dom[key],
  })
}
originalGlobals.set(
  'IS_REACT_ACT_ENVIRONMENT',
  Object.getOwnPropertyDescriptor(globalThis, 'IS_REACT_ACT_ENVIRONMENT')
)
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  value: true,
})
mock.module('@/components/ui/markdown', () => ({
  Markdown: ({ children }: { children: string }) => <div>{children}</div>,
}))
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { useSystemConfigStore } = await import('@/stores/system-config-store')
const { useWalletCurrencyPreferenceStore } =
  await import('@/stores/wallet-currency-preference-store')
const { StoreCheckout } = await import('./product-page')
const { StorePromotionCodes } = await import('./promotion-codes')
const { promotionDiscountBps } = await import('./promotion-utils')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const originalMethods = {
  get: api.get,
  post: api.post,
  put: api.put,
  delete: api.delete,
}
const originalClipboard = dom.navigator.clipboard.writeText
const originalUser = useAuthStore.getState().auth.user
const originalConfig = useSystemConfigStore.getState().config
const originalPreference =
  useWalletCurrencyPreferenceStore.getState().preference
let root: ReturnType<typeof createRoot> | undefined
let client: InstanceType<typeof QueryClient> | undefined
const envelope = (data: unknown) => ({ data: { success: true, data } })
const variant: StoreVariant = {
  id: 'variant-a',
  product_id: 'product-fixture',
  name: 'Variant A',
  price_quota: 1000000,
  template: 'card-key',
  enabled: true,
  is_default: true,
  inventory_total: 5,
  inventory_available: 5,
  reserved_stock: 0,
  sale_available: 3,
  trading_paused: false,
  created_at: 1,
  updated_at: 1,
}
const product: StoreProduct = {
  id: 'product-fixture',
  seller_id: 9,
  title: 'Fixture keys',
  description: '',
  image_urls: [],
  contact: '',
  links: [],
  price_quota: 1000000,
  template: 'card-key',
  delivery_strategy: 'sequential',
  payment_methods: ['balance'],
  pickup_login_required: false,
  pickup_code_required: false,
  email_pickup_link: false,
  status: 'published',
  official: true,
  available_stock: 5,
  sale_available: 2,
  promotion_expires_at: 0,
  created_at: 1,
  updated_at: 1,
  review_note: '',
  variants: [variant],
  default_variant_id: variant.id,
}
function code(
  id = 'promo-a',
  patch: Partial<StorePromotionCode> = {}
): StorePromotionCode {
  return {
    id,
    product_id: product.id,
    seller_id: 9,
    code: id.toUpperCase(),
    discount_bps: 2500,
    variant_ids: [],
    expires_at: null,
    max_uses: 3,
    status: 'active',
    uses_count: 1,
    reserved_count: 0,
    created_at: 1,
    updated_at: 1,
    share_path: `/store/products/${product.id}?promotion=${id.toUpperCase()}`,
    ...patch,
  }
}
function quote(
  quantity = 1,
  patch: Partial<StorePromotionQuote> = {}
): StorePromotionQuote {
  return {
    product_id: product.id,
    variant_id: variant.id,
    quantity,
    promotion_code: 'FREE',
    discount_bps: 10000,
    original_price_quota: 1000000 * quantity,
    discount_quota: 1000000 * quantity,
    price_quota: 0,
    free: true,
    checkout_allowed: true,
    max_quantity: 2,
    payment_methods: ['free'],
    ...patch,
  }
}
function owner(id = 12) {
  useSystemConfigStore.setState({
    config: {
      ...originalConfig,
      currency: {
        ...originalConfig.currency,
        currencyUnit: 'credit',
        creditsPerUsd: 500000,
        creditsPerUsdExact: '500000',
        cnyPerUsd: 7,
        cnyPerUsdExact: '7',
      },
    },
  })
  useAuthStore.getState().auth.setUser({
    id,
    role: 1,
    username: `user-${id}`,
    quota: 5000000,
    setting: JSON.stringify({ wallet_display_currency: 'CREDIT' }),
  })
}
const flush = () => new Promise((resolve) => setTimeout(resolve, 30))
async function mount(node: React.ReactNode) {
  document.body.replaceChildren()
  const host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  await act(async () => {
    root!.render(
      <QueryClientProvider client={client!}>
        <I18nextProvider i18n={i18n}>{node}</I18nextProvider>
      </QueryClientProvider>
    )
    await flush()
  })
  await act(flush)
}
function button(text: string, container: ParentNode = document) {
  const found = [
    ...container.querySelectorAll<HTMLButtonElement>('button'),
  ].find((node) => node.textContent?.trim() === text)
  assert.ok(found, `button ${text}`)
  return found
}
async function click(node: HTMLElement) {
  await act(async () => {
    node.click()
    await flush()
  })
}
async function waitFor(predicate: () => boolean, message: string) {
  for (let attempt = 0; attempt < 50; attempt++) {
    if (predicate()) {
      return
    }
    await act(flush)
  }
  assert.ok(predicate(), message)
}
async function input(node: HTMLInputElement, value: string) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(
      dom.HTMLInputElement.prototype,
      'value'
    )!.set!.call(node, value)
    node.dispatchEvent(new Event('input', { bubbles: true }))
    await flush()
  })
}
function field(label: string) {
  const found = [...document.querySelectorAll<HTMLLabelElement>('label')].find(
    (node) => node.textContent === label
  )
  assert.ok(found?.htmlFor, `field ${label}`)
  const element = document.getElementById(found.htmlFor)
  assert.ok(element)
  return element as HTMLInputElement
}
function publicReads() {
  api.get = (async (url: string) => {
    if (url.endsWith('/resolve')) {
      return envelope({
        product_id: product.id,
        code: 'FREE',
        discount_bps: 10000,
        variant_ids: [variant.id],
        expires_at: null,
        status: 'active',
      })
    }
    if (url.endsWith('/disclaimer')) {
      return envelope({ version: 'v1', text: 'Terms', accepted: true })
    }
    throw new Error(`Unexpected read: ${url}`)
  }) as typeof api.get
}
afterEach(async () => {
  if (root) await act(async () => root!.unmount())
  root = undefined
  client?.clear()
  Object.assign(api, originalMethods)
  dom.navigator.clipboard.writeText = originalClipboard
  useAuthStore.getState().auth.setUser(originalUser)
  useSystemConfigStore.setState({ config: originalConfig })
  useWalletCurrencyPreferenceStore.getState().setPreference(originalPreference)
  document.body.replaceChildren()
})
after(() => {
  dom.close()
  for (const [key, descriptor] of originalGlobals) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor)
    else Reflect.deleteProperty(globalThis, key)
  }
})

test('custom percentages are exact discounts and accept 100% without accepting larger values', () => {
  assert.equal(promotionDiscountBps('25.75'), 2575)
  assert.equal(promotionDiscountBps('100'), 10000)
  assert.equal(promotionDiscountBps('0'), 0)
  for (const value of ['100.01', '-1', '1.234', '1e2', '', 'Infinity']) {
    assert.equal(promotionDiscountBps(value), undefined)
  }
})

test('seller creates a scoped promotion with exact percentage, expiry and uses, then copies its public link', async () => {
  owner(9)
  const created = code('created')
  let items: StorePromotionCode[] = []
  let body: unknown
  let copied = ''
  api.get = (async () =>
    envelope({
      items,
      offset: 0,
      limit: 20,
      has_more: false,
    })) as typeof api.get
  api.post = (async (_url: string, request: unknown) => {
    body = request
    items = [created]
    return envelope(created)
  }) as typeof api.post
  dom.navigator.clipboard.writeText = async (value: string) => {
    copied = value
  }
  await mount(<StorePromotionCodes product={product} onClose={() => {}} />)
  await input(field('Discount (%)'), '25.75')
  await input(field('Maximum uses'), '3')
  const future = new Date(Date.now() + 86400000)
  const local = new Date(future.getTime() - future.getTimezoneOffset() * 60000)
    .toISOString()
    .slice(0, 16)
  await input(field('Expires at'), local)
  await act(async () => {
    const scope = document.querySelector<HTMLSelectElement>('select')!
    scope.value = 'selected'
    scope.dispatchEvent(new Event('change', { bubbles: true }))
  })
  const choice = [...document.querySelectorAll<HTMLLabelElement>('label')].find(
    (node) => node.textContent === variant.name
  )!
  await click(choice)
  await click(button('Create promotion code'))
  assert.deepEqual(body, {
    discount_bps: 2575,
    variant_ids: [variant.id],
    expires_at: Math.floor(new Date(local).getTime() / 1000),
    max_uses: 3,
    status: 'active',
  })
  await click(button('Copy share link'))
  assert.equal(
    copied,
    'https://shop.example.test/store/products/product-fixture?promotion=CREATED'
  )
  assert.equal(button('Copied').disabled, false)
})

test('page selection can be inverted and cleared; batch and cleanup remain scoped to this product', async () => {
  owner(9)
  let items = [code(), code('promo-b')]
  const mutations: { url: string; body: unknown }[] = []
  api.get = (async () =>
    envelope({
      items,
      offset: 0,
      limit: 20,
      has_more: false,
    })) as typeof api.get
  api.post = (async (url: string, body: unknown) => {
    mutations.push({ url, body })
    if (url.endsWith('/cleanup')) {
      items = []
      return envelope({ deleted: 2 })
    }
    items = items.map((item) => ({ ...item, status: 'paused' as const }))
    return envelope({ affected: 2 })
  }) as typeof api.post
  await mount(<StorePromotionCodes product={product} onClose={() => {}} />)
  await click(button('Select all on this page'))
  assert.ok(button('Pause selected · 2'))
  await click(button('Invert selection'))
  assert.equal(document.querySelector('[aria-label="Bulk actions"]'), null)
  await click(button('Select all on this page'))
  await click(button('Clear selection'))
  assert.equal(document.querySelector('[aria-label="Bulk actions"]'), null)
  await click(button('Select all on this page'))
  await click(button('Pause selected · 2'))
  assert.deepEqual(mutations[0], {
    url: '/api/store/products/product-fixture/promotions/batch',
    body: { ids: ['promo-a', 'promo-b'], action: 'pause' },
  })
  await click(button('Clean up expired and exhausted codes'))
  assert.deepEqual(mutations[1], {
    url: '/api/store/products/product-fixture/promotions/cleanup',
    body: { limit: 100 },
  })
  assert.match(
    document.body.textContent || '',
    /Cleaned up 2 promotion codes\./
  )
  assert.match(document.body.textContent || '', /No promotion codes yet/)
})

test('an unverified link cannot display or place a free order, and removal deliberately returns to the ordinary price', async () => {
  owner()
  let posts = 0
  api.get = (async (url: string) =>
    url.endsWith('/resolve')
      ? { data: { success: false, message: 'Store promotion unavailable' } }
      : envelope({
          version: 'v1',
          text: 'Terms',
          accepted: true,
        })) as typeof api.get
  api.post = (async () => {
    posts++
    return envelope(null)
  }) as typeof api.post
  await mount(<StoreCheckout product={product} promotionCode='FREE' />)
  assert.equal(button('Place order').disabled, true)
  assert.equal(
    document.body.textContent?.includes('Promotion code applied'),
    false
  )
  assert.equal(document.body.textContent?.includes('Free claim'), false)
  await click(button('Remove promotion'))
  assert.equal(button('Place order').disabled, false)
  assert.equal(posts, 0)
})

test('verified full discount bypasses absent gateways, sends free and returns paid without a pay request', async () => {
  owner()
  publicReads()
  const posts: { url: string; body: unknown }[] = []
  api.post = (async (url: string, body: unknown) => {
    posts.push({ url, body })
    if (url.endsWith('/quote')) return envelope(quote())
    if (url === '/api/store/orders') {
      return envelope({
        created: true,
        order: {
          id: 'free-order',
          trade_no: 'free-trade',
          status: 'paid',
          price_quota: 0,
          unit_price_quota: 0,
          payment_method: 'free',
          variant_name: variant.name,
        },
      })
    }
    throw new Error(`Free order must not call ${url}`)
  }) as typeof api.post
  await mount(
    <StoreCheckout
      product={{
        ...product,
        payment_methods: [],
        trading_paused: true,
        sale_available: 0,
        variants: [{ ...variant, trading_paused: true, sale_available: 0 }],
      }}
      promotionCode='FREE'
    />
  )
  assert.equal(document.querySelector('input[name="store-payment"]'), null)
  assert.equal(button('Free claim').disabled, false)
  assert.match(document.body.textContent || '', /Original price/)
  assert.match(document.body.textContent || '', /Final price/)
  await click(button('Free claim'))
  const checkout = posts.find((request) => request.url === '/api/store/orders')!
  assert.ok(checkout)
  assert.equal(
    (checkout.body as { payment_method: string }).payment_method,
    'free'
  )
  assert.equal(
    (checkout.body as { promotion_code: string }).promotion_code,
    'FREE'
  )
  assert.match(document.body.textContent || '', /Order paid/)
  assert.equal(
    document.querySelector('a[href="/store/orders"]')?.textContent,
    'View order and collect items'
  )
  assert.equal(
    posts.some((request) => request.url.includes('/pay')),
    false
  )
})

test('quantity change discards the old quote and free orders keep server sales/buyer capacity and pickup requirements', async () => {
  owner()
  publicReads()
  let release: ((value: ReturnType<typeof envelope>) => void) | undefined
  api.post = (async (_url: string, body: { quantity: number }) =>
    body.quantity === 1
      ? envelope(quote())
      : new Promise<ReturnType<typeof envelope>>((resolve) => {
          release = resolve
        })) as typeof api.post
  await mount(
    <StoreCheckout
      product={{ ...product, pickup_code_required: true }}
      promotionCode='FREE'
    />
  )
  assert.equal(button('Free claim').disabled, true)
  await input(
    document.querySelector<HTMLInputElement>('#store-pickup-code')!,
    'secret-code'
  )
  assert.equal(button('Free claim').disabled, false)
  await input(document.querySelector<HTMLInputElement>('#store-quantity')!, '2')
  assert.equal(button('Place order').disabled, true)
  assert.equal(
    document.body.textContent?.includes('Promotion code applied'),
    false
  )
  assert.equal(
    document.querySelector<HTMLInputElement>('#store-quantity')!.value,
    '2',
    'a pending quote has no new maximum and cannot silently reduce the buyer quantity'
  )
  await act(async () => {
    release!(envelope(quote(2)))
    await flush()
  })
  assert.equal(button('Free claim').disabled, false)
  assert.equal(
    document.querySelector<HTMLInputElement>('#store-quantity')!.value,
    '2'
  )
  assert.equal(
    document.querySelector('#store-quantity')?.getAttribute('aria-valuemax'),
    '2'
  )
  await input(document.querySelector<HTMLInputElement>('#store-quantity')!, '3')
  await act(async () => {
    release!(envelope(quote(3)))
    await flush()
  })
  assert.equal(button('Place order').disabled, true)
})

test('a full-discount link can select a scoped variant despite paid availability being zero, then waits for its own quote', async () => {
  owner()
  const second = { ...variant, id: 'variant-b', name: 'Variant B' }
  api.get = (async (url: string) =>
    url.endsWith('/resolve')
      ? envelope({
          product_id: product.id,
          code: 'FREE',
          discount_bps: 10000,
          variant_ids: [variant.id, second.id],
          expires_at: null,
          status: 'active',
        })
      : envelope({
          version: 'v1',
          text: 'Terms',
          accepted: true,
        })) as typeof api.get
  let release: ((value: ReturnType<typeof envelope>) => void) | undefined
  let quotedVariant = ''
  api.post = (async (_url: string, body: { variant_id: string }) => {
    quotedVariant = body.variant_id
    return new Promise<ReturnType<typeof envelope>>((resolve) => {
      release = resolve
    })
  }) as typeof api.post
  await mount(
    <StoreCheckout
      product={{
        ...product,
        payment_methods: [],
        trading_paused: true,
        sale_available: 0,
        variants: [variant, second].map((item) => ({
          ...item,
          trading_paused: true,
          sale_available: 0,
        })),
      }}
      promotionCode='FREE'
    />
  )
  const selection = document.querySelector<HTMLInputElement>(
    'input[name="store-variant"][value="variant-b"]'
  )!
  assert.equal(selection.disabled, false)
  assert.equal(button('Place order').disabled, true)
  await click(selection)
  assert.equal(quotedVariant, second.id)
  assert.equal(button('Place order').disabled, true)
  await act(async () => {
    release!(envelope(quote(1, { variant_id: second.id })))
    await flush()
  })
  assert.equal(button('Free claim').disabled, false)
})

test('a zero quote without explicit checkout eligibility cannot bypass the trading gate', async () => {
  owner()
  publicReads()
  api.post = (async () =>
    envelope(quote(1, { checkout_allowed: false }))) as typeof api.post
  await mount(
    <StoreCheckout
      product={{ ...product, trading_paused: true }}
      promotionCode='FREE'
    />
  )
  assert.equal(button('Free claim').disabled, true)
  assert.match(
    document.body.textContent || '',
    /This product or payment method is currently unavailable/
  )
})

test('partial discount uses server amounts and available payment methods, without claiming a free order', async () => {
  owner()
  useWalletCurrencyPreferenceStore.getState().setPreference('CREDIT')
  publicReads()
  const posts: { url: string; body: unknown }[] = []
  api.post = (async (url: string, body: unknown) => {
    posts.push({ url, body })
    if (url.endsWith('/quote')) {
      return envelope(
        quote(1, {
          discount_bps: 5000,
          original_price_quota: 20000,
          discount_quota: 10000,
          price_quota: 10000,
          free: false,
          payment_methods: ['balance'],
        })
      )
    }
    return envelope({
      created: true,
      order: {
        id: 'paid-promo-order',
        trade_no: 'paid-trade',
        status: 'pending',
        price_quota: 10000,
        unit_price_quota: 10000,
        payment_method: 'balance',
      },
    })
  }) as typeof api.post
  await mount(
    <StoreCheckout
      product={{ ...product, payment_methods: ['external:epay', 'balance'] }}
      promotionCode='FREE'
    />
  )
  assert.equal(
    document.querySelectorAll('input[name="store-payment"]').length,
    1
  )
  assert.equal(
    document.querySelector<HTMLInputElement>('input[name="store-payment"]')
      ?.value,
    'balance'
  )
  const detail = document.querySelector('section[aria-label="Promotion code"]')!
  assert.match(detail.textContent || '', /20,000 Credits/)
  assert.match(detail.textContent || '', /10,000 Credits/)
  assert.equal(button('Place order').disabled, false)
  await click(button('Place order'))
  const order = posts.find((request) => request.url === '/api/store/orders')!
  assert.equal(
    (order.body as { payment_method: string }).payment_method,
    'balance'
  )
  assert.equal(
    (order.body as { promotion_code: string }).promotion_code,
    'FREE'
  )
  assert.ok(button('Pay with balance'))
})

for (const eligible of [true, false]) {
  test(`a partial discounted fee uses server eligibility ${eligible} when the original paid aggregates are zero`, async () => {
    owner()
    publicReads()
    let orders = 0
    api.post = (async (url: string) => {
      if (url.endsWith('/quote')) {
        return envelope(
          quote(1, {
            discount_bps: 5000,
            original_price_quota: 20000,
            discount_quota: 10000,
            price_quota: 10000,
            free: false,
            checkout_allowed: eligible,
            payment_methods: ['balance'],
          })
        )
      }
      orders++
      return envelope({
        created: true,
        order: {
          id: 'discounted-fee-order',
          trade_no: 'discounted-fee-trade',
          status: 'pending',
          price_quota: 10000,
          unit_price_quota: 10000,
          payment_method: 'balance',
        },
      })
    }) as typeof api.post
    await mount(
      <StoreCheckout
        product={{
          ...product,
          trading_paused: true,
          sale_available: 0,
          variants: [{ ...variant, trading_paused: true, sale_available: 0 }],
        }}
        promotionCode='FREE'
      />
    )
    assert.equal(
      document.querySelector('#store-quantity')?.getAttribute('aria-valuemax'),
      '2'
    )
    assert.equal(button('Place order').disabled, !eligible)
    await click(button('Place order'))
    assert.equal(orders, eligible ? 1 : 0)
  })
}

test('a verified paid offer still caps gateway quantity at 100 and lets an available balance method use the server limit', async () => {
  owner()
  publicReads()
  api.post = (async (_url: string, body: { quantity: number }) =>
    envelope(
      quote(body.quantity, {
        discount_bps: 5000,
        original_price_quota: body.quantity * 1000000,
        discount_quota: body.quantity * 500000,
        price_quota: body.quantity * 500000,
        free: false,
        max_quantity: 500,
        payment_methods: ['external:epay', 'balance'],
      })
    )) as typeof api.post
  await mount(
    <StoreCheckout
      product={{ ...product, payment_methods: ['external:epay', 'balance'] }}
      promotionCode='FREE'
    />
  )
  assert.equal(
    document.querySelector('#store-quantity')?.getAttribute('aria-valuemax'),
    '100'
  )
  await input(
    document.querySelector<HTMLInputElement>('#store-quantity')!,
    '101'
  )
  assert.equal(button('Place order').disabled, true)
  await click(
    document.querySelector<HTMLInputElement>(
      'input[name="store-payment"][value="balance"]'
    )!
  )
  assert.equal(
    document.querySelector('#store-quantity')?.getAttribute('aria-valuemax'),
    '500'
  )
  assert.equal(button('Place order').disabled, false)
})

test('pagination clears selection and deleting a selected code waits for a concrete confirmation', async () => {
  owner(9)
  const reads: number[] = []
  const posts: unknown[] = []
  const rows = [code('first'), code('second')]
  api.get = (async (_url: string, options: { params: { offset: number } }) => {
    const offset = options.params.offset
    reads.push(offset)
    return envelope({
      items: [rows[offset === 0 ? 0 : 1]],
      offset,
      limit: 20,
      has_more: offset === 0,
    })
  }) as typeof api.get
  api.post = (async (_url: string, body: unknown) => {
    posts.push(body)
    return envelope({ affected: 1 })
  }) as typeof api.post
  await mount(<StorePromotionCodes product={product} onClose={() => {}} />)
  await click(button('Select all on this page'))
  await click(button('Next page'))
  await waitFor(
    () =>
      !!document.querySelector('[aria-label="Select promotion code: SECOND"]'),
    'second page promotion is loaded'
  )
  assert.equal(document.querySelector('[aria-label="Bulk actions"]'), null)
  assert.ok(reads.includes(20))
  await click(button('Select all on this page'))
  await click(button('Delete selected · 1'))
  assert.equal(posts.length, 0)
  const confirmation = document.querySelector('[role="alertdialog"]')!
  assert.match(confirmation.textContent || '', /Delete promotion codes\?/)
  await click(button('Delete', confirmation))
  assert.deepEqual(posts[0], { ids: ['second'], action: 'delete' })
  assert.equal(document.querySelector('[aria-label="Bulk actions"]'), null)
})

test('guests see a verified offer and retain the code through sign-in; buyer changes require a new eligible quote', async () => {
  owner()
  useAuthStore.getState().auth.setUser(null)
  publicReads()
  const actors: (number | undefined)[] = []
  api.post = (async () => {
    const actor = useAuthStore.getState().auth.user?.id
    actors.push(actor)
    return envelope(quote(1, { checkout_allowed: actor === 12 }))
  }) as typeof api.post
  await mount(<StoreCheckout product={product} promotionCode='FREE' />)
  assert.match(
    document.querySelector('section[aria-label="Promotion code"]')
      ?.textContent || '',
    /Promotion code applied/
  )
  const signIn = document.querySelector<HTMLAnchorElement>(
    'a[href^="/sign-in?redirect="]'
  )!
  assert.equal(
    new URL(signIn.href).searchParams.get('redirect'),
    '/store/products/product-fixture?promotion=FREE'
  )
  await act(async () => {
    owner()
    await flush()
  })
  await act(flush)
  assert.equal(button('Free claim').disabled, false)
  assert.deepEqual(actors, [undefined, 12])
})

test("a response quoted for another variant cannot become this variant's free offer", async () => {
  owner()
  publicReads()
  api.post = (async () =>
    envelope(quote(1, { variant_id: 'another-variant' }))) as typeof api.post
  await mount(<StoreCheckout product={product} promotionCode='FREE' />)
  assert.equal(button('Place order').disabled, true)
  assert.equal(
    document.body.textContent?.includes('Promotion code applied'),
    false
  )
  assert.match(document.body.textContent || '', /Store promotion unavailable/)
})

test('a free first purchase still accepts the existing independent seller disclaimer before creating the order', async () => {
  owner()
  api.get = (async (url: string) =>
    url.endsWith('/resolve')
      ? envelope({
          product_id: product.id,
          code: 'FREE',
          discount_bps: 10000,
          variant_ids: [variant.id],
          expires_at: null,
          status: 'active',
        })
      : envelope({
          version: 'v1',
          text: 'Independent seller terms',
          accepted: false,
        })) as typeof api.get
  const calls: { url: string; body: Record<string, unknown> }[] = []
  api.post = (async (url: string, body: Record<string, unknown>) => {
    if (url.endsWith('/quote')) return envelope(quote())
    calls.push({ url, body })
    return envelope(
      url.endsWith('/accept')
        ? null
        : {
            created: true,
            order: {
              id: 'free-disclaimer-order',
              trade_no: 'free-disclaimer-trade',
              status: 'paid',
              payment_method: 'free',
              price_quota: 0,
              unit_price_quota: 0,
            },
          }
    )
  }) as typeof api.post
  await mount(
    <StoreCheckout
      product={{ ...product, official: false }}
      promotionCode='FREE'
    />
  )
  await click(button('Free claim'))
  assert.equal(calls.length, 0)
  assert.equal(button('Agree and place order').disabled, true)
  await click(
    document.querySelector<HTMLInputElement>('input[type="checkbox"]')!
  )
  await click(button('Agree and place order'))
  assert.deepEqual(
    calls.map((call) => call.url),
    ['/api/store/disclaimer/accept', '/api/store/orders']
  )
  assert.equal(calls[1].body.payment_method, 'free')
  assert.equal(calls[1].body.disclaimer_version, 'v1')
})

test('test-mode promotion links use the private preview route', async () => {
  owner(9)
  api.get = (async () =>
    envelope({
      items: [code()],
      offset: 0,
      limit: 20,
      has_more: false,
    })) as typeof api.get
  let copied = ''
  dom.navigator.clipboard.writeText = async (value: string) => {
    copied = value
  }
  await mount(
    <StorePromotionCodes
      product={{ ...product, test_mode: true }}
      onClose={() => {}}
    />
  )
  await click(button('Copy share link'))
  assert.equal(
    copied,
    'https://shop.example.test/store/preview/product-fixture?promotion=PROMO-A'
  )
})

for (const [name, changed, preview] of [
  ['a paused product', { status: 'paused' as const }, false],
  ["another seller's test product", { test_mode: true }, true],
] as const) {
  test(`a free quote does not bypass ${name}`, async () => {
    owner()
    publicReads()
    let orders = 0
    api.post = (async (url: string) => {
      if (url.endsWith('/quote')) return envelope(quote())
      orders++
      return envelope(null)
    }) as typeof api.post
    await mount(
      <StoreCheckout
        product={{ ...product, ...changed }}
        ownerPreview={preview}
        promotionCode='FREE'
      />
    )
    assert.equal(button('Free claim').disabled, true)
    await click(button('Free claim'))
    assert.equal(orders, 0)
  })
}

test('a typed minimum error confirmed cancelled by the server requotes and allows a new balance order', async () => {
  owner()
  publicReads()
  const discounted = () =>
    quote(1, {
      discount_bps: 5000,
      original_price_quota: 20000,
      discount_quota: 10000,
      price_quota: 10000,
      free: false,
      payment_methods: ['external:epay', 'balance'],
    })
  let quotes = 0
  let release: ((value: ReturnType<typeof envelope>) => void) | undefined
  const orders: Record<string, unknown>[] = []
  const posts: string[] = []
  api.post = (async (url: string, body: Record<string, unknown>) => {
    posts.push(url)
    if (url.endsWith('/quote')) {
      quotes++
      return quotes === 1
        ? envelope(discounted())
        : new Promise<ReturnType<typeof envelope>>((resolve) => {
            release = resolve
          })
    }
    if (url.endsWith('/pay')) {
      return {
        data: {
          success: false,
          code: 'STORE_PAYMENT_MINIMUM',
          message:
            'Payment amount is below the gateway minimum; choose balance',
          order_id: 'native-order',
          order_status: 'cancelled',
          order_cancelled: true,
        },
      }
    }
    assert.equal(url, '/api/store/orders')
    orders.push(body)
    return envelope({
      created: true,
      order: {
        id: 'native-order',
        trade_no: 'native-trade',
        status: 'pending',
        price_quota: 10000,
        unit_price_quota: 10000,
        payment_method: body.payment_method,
      },
    })
  }) as typeof api.post
  await mount(
    <StoreCheckout
      product={{ ...product, payment_methods: ['external:epay', 'balance'] }}
      promotionCode='FREE'
    />
  )
  await click(button('Place order'))
  await click(button('Prepare payment'))
  assert.equal(quotes, 2)
  assert.equal(button('Place order').disabled, true)
  await act(async () => {
    release!(envelope(discounted()))
    await flush()
  })
  const balance = document.querySelector<HTMLInputElement>(
    'input[name="store-payment"][value="balance"]'
  )!
  assert.equal(balance.checked, true)
  assert.equal(button('Place order').disabled, false)
  await click(button('Place order'))
  assert.equal(orders.length, 2)
  assert.equal(orders[1].payment_method, 'balance')
  assert.equal(orders[1].promotion_code, 'FREE')
  assert.notEqual(orders[1].request_key, orders[0].request_key)
  assert.equal(
    posts.some((url) => url.endsWith('/cancel')),
    false
  )
})

for (const [name, failure] of [
  [
    'an issued order',
    {
      code: 'STORE_PAYMENT_MINIMUM',
      order_id: 'native-order',
      order_status: 'pending',
      order_cancelled: false,
    },
  ],
  [
    'a different order',
    {
      code: 'STORE_PAYMENT_MINIMUM',
      order_id: 'other-order',
      order_status: 'cancelled',
      order_cancelled: true,
    },
  ],
  ['an untyped error with the same message', {}],
] as const) {
  test(`payment recovery preserves ${name} instead of clearing its obligation`, async () => {
    owner()
    publicReads()
    let quotes = 0
    const posts: string[] = []
    api.post = (async (url: string) => {
      posts.push(url)
      if (url.endsWith('/quote')) {
        quotes++
        return envelope(
          quote(1, {
            discount_bps: 5000,
            original_price_quota: 20000,
            discount_quota: 10000,
            price_quota: 10000,
            free: false,
            payment_methods: ['external:epay', 'balance'],
          })
        )
      }
      if (url.endsWith('/pay')) {
        return {
          data: {
            success: false,
            message:
              'Payment amount is below the gateway minimum; choose balance',
            ...failure,
          },
        }
      }
      return envelope({
        created: true,
        order: {
          id: 'native-order',
          trade_no: 'native-trade',
          status: 'pending',
          price_quota: 10000,
          unit_price_quota: 10000,
          payment_method: 'external:epay',
        },
      })
    }) as typeof api.post
    await mount(
      <StoreCheckout
        product={{ ...product, payment_methods: ['external:epay', 'balance'] }}
        promotionCode='FREE'
      />
    )
    await click(button('Place order'))
    await click(button('Prepare payment'))
    assert.equal(quotes, 1)
    assert.ok(button('Prepare payment'))
    assert.equal(document.querySelector('#store-quantity'), null)
    assert.equal(
      posts.some((url) => url.endsWith('/cancel')),
      false
    )
    assert.equal(posts.filter((url) => url === '/api/store/orders').length, 1)
  })
}
