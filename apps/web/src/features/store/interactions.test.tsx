/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

import type { StoreOrder, StoreProduct } from './types'

const dom = new Window({
  url: 'https://shop.example.test/store/products/product-fixture',
})
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
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  value: true,
})
const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { useAuthStore } = await import('@/stores/auth-store')
const { useSystemConfigStore } = await import('@/stores/system-config-store')
const { useWalletCurrencyPreferenceStore } =
  await import('@/stores/wallet-currency-preference-store')
const { api } = await import('@/lib/api')
const { StoreCheckout, StoreProductPage } = await import('./product-page')
const { StoreClaimPage } = await import('./claim-page')
const { StoreGatewayEditor } = await import('./settings-page')
const { StoreSettingsPage } = await import('./settings-page')
const { StorePaymentCategoriesForm } = await import('./payment-categories')
const { MerchantStoreSettingsSection } =
  await import('@/features/system-settings/integrations/merchant-store-settings-section')
const { StoreOrderRow, StoreOrdersPage } = await import('./orders-page')
const { StoreDeliveryEmail } = await import('./delivery-email')
const { StoreProductEditor } = await import('./seller-page')
const { StorePage } = await import('./store-page')
const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        'Store purchase disclaimer v1':
          'Independent seller terms. Read and accept before placing this order.',
      },
    },
  },
})
const originalGet = api.get
const originalPost = api.post
const originalPut = api.put
const originalConfig = useSystemConfigStore.getState().config
let root: ReturnType<typeof createRoot> | undefined
let client: InstanceType<typeof QueryClient> | undefined
const product: StoreProduct = {
  id: 'product-fixture',
  seller_id: 9,
  title: 'Fixture keys',
  description: 'Text inventory fixture',
  image_urls: [],
  contact: '',
  links: [],
  price_quota: 500000,
  template: 'card-key',
  delivery_strategy: 'sequential',
  payment_methods: ['balance'],
  pickup_login_required: false,
  pickup_code_required: false,
  email_pickup_link: false,
  status: 'published',
  official: false,
  available_stock: 5,
  promotion_expires_at: 0,
  created_at: 1,
  updated_at: 1,
  review_note: '',
}
const result = (data: unknown) => ({ data: { success: true, data } })
function owner(id: number | null) {
  useAuthStore
    .getState()
    .auth.setUser(
      id ? { id, role: 1, username: `buyer-${id}`, quota: 5000000 } : null
    )
}
async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 35))
}
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
function button(text: string) {
  const found = [
    ...document.querySelectorAll<HTMLButtonElement>('button'),
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
afterEach(async () => {
  if (root) await act(async () => root!.unmount())
  root = undefined
  client?.clear()
  api.get = originalGet
  api.post = originalPost
  api.put = originalPut
  owner(null)
  useSystemConfigStore.setState({ config: originalConfig })
  document.body.replaceChildren()
})
after(() => dom.happyDOM.abort())

test('guests may inspect a product and disclaimer but cannot place an order', async () => {
  owner(null)
  let posts = 0
  api.get = (async () =>
    result({
      version: 'merchant-store-v1',
      text: 'Terms',
      accepted: false,
    })) as typeof api.get
  api.post = (async () => {
    posts++
    return result(null)
  }) as typeof api.post
  await mount(<StoreCheckout product={product} />)
  assert.ok(document.querySelector('a[href^="/sign-in?redirect="]'))
  assert.equal(document.querySelectorAll('button').length > 0, true)
  await click(button('Read purchase disclaimer'))
  assert.match(document.body.textContent || '', /Independent seller terms/)
  assert.equal(posts, 0)
})
test('an empty public shelf keeps browsing and seller access without games or orders', async () => {
  owner(null)
  let reads = 0
  api.get = (async (url: string) => {
    assert.equal(url, '/api/store/products')
    reads++
    return result({ items: [], has_more: false })
  }) as typeof api.get
  api.post = (async () =>
    assert.fail('An empty shelf must not submit an order')) as typeof api.post
  await mount(<StorePage />)
  assert.match(document.body.textContent || '', /Nothing on the shelves yet/)
  assert.match(
    document.body.textContent || '',
    /Published products will appear here/
  )
  assert.equal(document.querySelectorAll('article').length, 0)
  assert.equal(document.querySelectorAll('button[aria-pressed]').length, 0)
  assert.equal(document.body.textContent?.includes('constellation'), false)
  assert.equal(document.body.textContent?.includes('Next page'), false)
  assert.ok(document.querySelector('a[href="/store/manage"]'))
  assert.ok(document.querySelector('input[type="search"]'))
  assert.ok(button('Search'))
  assert.equal(reads, 1)
})
test('real products retain their product links and shelf', async () => {
  api.get = (async () =>
    result({ items: [product], has_more: false })) as typeof api.get
  await mount(<StorePage />)
  assert.equal(document.querySelectorAll('article').length, 1)
  assert.equal(document.querySelectorAll('button[aria-pressed]').length, 0)
  assert.ok(document.querySelector('a[href="/store/products/product-fixture"]'))
  assert.equal(
    document.body.textContent?.includes('Nothing on the shelves yet'),
    false
  )
})
test('seller category changes send only both master switches and retain a failed draft', async () => {
  const calls: unknown[] = []
  let fail = true
  api.put = (async (url: string, body: unknown) => {
    assert.equal(url, '/api/store/payments/categories')
    calls.push(body)
    if (fail) throw new Error('Category save failed')
    return result(body)
  }) as typeof api.put
  await mount(
    <StorePaymentCategoriesForm
      categories={{ platform_enabled: false, external_enabled: true }}
      onSaved={async () => {}}
    />
  )
  const platform = document.querySelector<HTMLElement>(
    '[role="switch"][aria-label="Platform payments"]'
  )
  assert.ok(platform)
  await click(platform)
  await click(button('Save payment categories'))
  assert.equal(platform.getAttribute('aria-checked'), 'true')
  assert.match(document.body.textContent || '', /Store request failed/)
  fail = false
  await click(button('Save payment categories'))
  assert.deepEqual(calls, [
    { platform_enabled: true, external_enabled: true },
    { platform_enabled: true, external_enabled: true },
  ])
})
for (const role of [1, 10, 100]) {
  test(`seller payment settings expose the administration link only to root (role ${role})`, async () => {
    useAuthStore.getState().auth.setUser({ id: 2, role, username: 'seller' })
    api.get = (async (url: string) => {
      if (url === '/api/store/config') {
        return result({
          fee_bps: 0,
          promotion_quota: 500000,
          platform_payment_catalog: [
            {
              payment_type: 'stripe',
              name: 'Stripe',
              supported: false,
              configured: true,
              unavailable_code: 'merchant_settlement_unsupported',
            },
          ],
        })
      }
      assert.equal(url, '/api/store/payments/settings')
      return result({
        items: [],
        categories: { platform_enabled: false, external_enabled: false },
        balance_quota: 0,
        fee_bps: 0,
        external_eligible: false,
      })
    }) as typeof api.get
    await mount(<StoreSettingsPage />)
    assert.equal(
      !!document.querySelector(
        'a[href="/system-settings/billing/payment#merchant-store"]'
      ),
      role === 100
    )
    assert.equal(document.querySelector('#store-fee'), null)
    assert.equal(document.querySelector('#store-linuxdo-rate'), null)
    assert.match(document.body.textContent || '', /Stripe/)
    assert.equal(
      document.querySelector('[role="switch"][aria-label="Enable Stripe"]'),
      null
    )
  })
}
test('root store administration saves only fees and native promotion credits, without a merchant-specific rate', async () => {
  useAuthStore.getState().auth.setUser({ id: 2, role: 100, username: 'root' })
  api.get = (async () =>
    result({
      fee_bps: 100,
      promotion_quota: 500000,
      linuxdo_units_per_usd: '2.5',
      disclaimer_version: 'v1',
      disclaimer_text: '',
      platform_payment_methods: [],
    })) as typeof api.get
  let saved: unknown
  api.put = (async (url: string, body: unknown) => {
    assert.equal(url, '/api/store/config')
    saved = body
    return result(null)
  }) as typeof api.put
  await mount(<MerchantStoreSettingsSection />)
  const fee = document.querySelector<HTMLInputElement>('#store-fee')
  assert.ok(fee)
  await input(fee, '2')
  await click(button('Save store settings'))
  assert.deepEqual(saved, { fee_bps: 200, promotion_quota: 500000 })
  assert.equal(document.querySelector('#store-linuxdo-rate'), null)
})
test('root can set the configured minimum to zero while preserving fee and promotion credits', async () => {
  useAuthStore.getState().auth.setUser({ id: 2, role: 100, username: 'root' })
  useWalletCurrencyPreferenceStore.getState().setPreference('USD')
  useSystemConfigStore.setState({
    config: {
      ...originalConfig,
      currency: {
        ...originalConfig.currency,
        creditsPerUsd: 500000,
        creditsPerUsdExact: '500000',
        cnyPerUsd: 7,
        cnyPerUsdExact: '7',
        quotaDisplayType: 'USD',
        currencyUnit: 'credit',
      },
    },
  })
  api.get = (async () =>
    result({
      fee_bps: 100,
      promotion_quota: 500000,
      minimum_unit_price_quota: 750000,
      platform_payment_methods: [],
    })) as typeof api.get
  let saved: unknown
  api.put = (async (url: string, body: unknown) => {
    assert.equal(url, '/api/store/config')
    saved = body
    return result(null)
  }) as typeof api.put
  await mount(<MerchantStoreSettingsSection />)
  const minimum = document.querySelector<HTMLInputElement>(
    '#store-minimum-price'
  )
  assert.ok(minimum)
  await input(minimum, '0')
  await click(button('Save store settings'))
  assert.deepEqual(saved, {
    fee_bps: 100,
    promotion_quota: 500000,
    minimum_unit_price_quota: 0,
  })
})
for (const role of [1, 10]) {
  test(`store fee administration does not load or render for non-root role ${role}`, async () => {
    useAuthStore.getState().auth.setUser({ id: 2, role, username: 'seller' })
    api.get = (async () =>
      assert.fail(
        'Non-root must not load the administration form'
      )) as typeof api.get
    await mount(<MerchantStoreSettingsSection />)
    assert.equal(document.querySelector('#store-fee'), null)
    assert.equal(document.querySelector('form'), null)
  })
}
test('a missing minimum from an older API does not silently allow saving product prices', async () => {
  await mount(
    <StoreProductEditor
      product={product}
      allowedMethods={product.payment_methods}
      onClose={() => {}}
      onSaved={async () => {}}
    />
  )
  assert.equal(button('Save draft').disabled, true)
})
for (const minimum of [0, 750000]) {
  test(`product prices honor the configured minimum ${minimum} without changing raw credits`, async () => {
    const saved: number[] = []
    api.put = (async (_: string, body: { price_quota: number }) => {
      saved.push(body.price_quota)
      return result(product)
    }) as typeof api.put
    await mount(
      <StoreProductEditor
        product={product}
        minimumPriceQuota={minimum}
        allowedMethods={product.payment_methods}
        onClose={() => {}}
        onSaved={async () => {}}
      />
    )
    const currency = document.querySelector<HTMLSelectElement>(
      'select[aria-label="Price currency"]'
    )
    assert.ok(currency)
    await act(async () => {
      currency.value = 'CREDIT'
      currency.dispatchEvent(new Event('change', { bubbles: true }))
      await flush()
    })
    const price = document.querySelector<HTMLInputElement>('#store-price')
    assert.ok(price)
    await input(price, '500000')
    await click(button('Save draft'))
    if (minimum > 500000) {
      assert.deepEqual(saved, [])
      assert.match(
        document.body.textContent || '',
        /The unit price must be at least/
      )
      await input(price, String(minimum))
      await click(button('Save draft'))
      assert.deepEqual(saved, [minimum])
    } else {
      assert.deepEqual(saved, [500000])
      await input(price, '0')
      await click(button('Save draft'))
      assert.deepEqual(
        saved,
        [500000],
        'a disabled minimum does not enable free products'
      )
    }
  })
}
test('a previously selected disabled payment remains visible until explicitly removed and cannot be saved as available', async () => {
  let updates = 0
  let saved: { payment_methods: string[] } | undefined
  api.put = (async (_: string, body: { payment_methods: string[] }) => {
    updates++
    saved = body
    return result(product)
  }) as typeof api.put
  await mount(
    <StoreProductEditor
      product={{ ...product, payment_methods: ['balance'] }}
      minimumPriceQuota={0}
      allowedMethods={[]}
      onClose={() => {}}
      onSaved={async () => {}}
    />
  )
  const currency = document.querySelector<HTMLSelectElement>(
    'select[aria-label="Price currency"]'
  )
  assert.ok(currency)
  await act(async () => {
    currency.value = 'CREDIT'
    currency.dispatchEvent(new Event('change', { bubbles: true }))
    await flush()
  })
  const fieldset = [...document.querySelectorAll('fieldset')].find(
    (field) => field.querySelector('legend')?.textContent === 'Payment methods'
  )
  assert.ok(fieldset)
  const selected = fieldset.querySelector<HTMLElement>('[role="switch"]')
  assert.ok(selected)
  assert.equal(selected.getAttribute('aria-checked'), 'true')
  await click(button('Save draft'))
  assert.equal(updates, 0)
  assert.match(
    document.body.textContent || '',
    /This selected payment method is unavailable/
  )
  await click(selected)
  assert.equal(fieldset.querySelector('[role="switch"]'), null)
  assert.equal(button('Save draft').disabled, false)
  assert.ok(document.querySelector('form')?.checkValidity())
  await click(button('Save draft'))
  assert.equal(
    updates,
    1,
    document.querySelector('[role="alert"]')?.textContent ?? ''
  )
  assert.deepEqual(saved?.payment_methods, [])
})
test('first independent purchase requires explicit acceptance before checkout; integer quota stays server-owned', async () => {
  owner(2)
  const calls: { url: string; body: Record<string, unknown> }[] = []
  api.get = (async () =>
    result({
      version: 'merchant-store-v1',
      text: 'Terms',
      accepted: false,
    })) as typeof api.get
  api.post = (async (url: string, body: Record<string, unknown>) => {
    calls.push({ url, body })
    return result(
      url.endsWith('/accept')
        ? null
        : {
            order: {
              id: 'order-fixture',
              status: 'pending',
              trade_no: 'MS-fixture',
            },
            created: true,
          }
    )
  }) as typeof api.post
  await mount(<StoreCheckout product={product} />)
  await click(button('Place order'))
  assert.equal(calls.length, 0)
  assert.equal(button('Agree and place order').disabled, true)
  const reading = document.querySelector<HTMLDivElement>(
    '[tabindex="0"].overflow-y-auto'
  )
  assert.ok(reading)
  await act(async () => {
    Object.defineProperty(reading, 'scrollHeight', {
      configurable: true,
      value: 500,
    })
    Object.defineProperty(reading, 'clientHeight', {
      configurable: true,
      value: 200,
    })
    reading.scrollTop = 300
    reading.dispatchEvent(new Event('scroll', { bubbles: true }))
    await flush()
  })
  const checkbox = document.querySelector<HTMLElement>('[role="checkbox"]')
  assert.ok(checkbox)
  const checkboxInput = document.querySelector<HTMLInputElement>(
    'input[type="checkbox"]'
  )
  assert.ok(checkboxInput)
  await click(checkboxInput)
  assert.equal(
    checkbox.getAttribute('aria-checked'),
    'true',
    checkbox.outerHTML
  )
  await click(button('Agree and place order'))
  assert.deepEqual(
    calls.map((call) => call.url),
    ['/api/store/disclaimer/accept', '/api/store/orders']
  )
  assert.deepEqual(calls[0].body, {
    version: 'merchant-store-v1',
    accepted: true,
  })
  assert.equal(calls[1].body.product_id, product.id)
  assert.equal(calls[1].body.payment_method, 'balance')
  assert.equal('price_quota' in calls[1].body, false)
  assert.equal('buyer_id' in calls[1].body, false)
  assert.ok(calls[1].body.request_key)
})
test('official products skip independent disclaimer acceptance and unavailable stock disables checkout', async () => {
  owner(2)
  const calls: string[] = []
  api.get = (async () =>
    result({
      version: 'merchant-store-v1',
      text: 'Terms',
      accepted: false,
    })) as typeof api.get
  api.post = (async (url: string) => {
    calls.push(url)
    return result({
      order: { id: 'order-fixture', status: 'pending' },
      created: true,
    })
  }) as typeof api.post
  await mount(
    <StoreCheckout
      product={{ ...product, official: true, available_stock: 0 }}
    />
  )
  assert.equal(button('Place order').disabled, true)
  assert.equal(calls.length, 0)
})
test('delivery content appears only after explicit collection and disappears on account switch', async () => {
  owner(2)
  let collections = 0
  api.get = (async () =>
    result({
      product_title: 'Fixture keys',
      status: 'paid',
      pickup_login_required: false,
      pickup_code_required: false,
    })) as typeof api.get
  api.post = (async () => {
    collections++
    return result({
      order_id: 'order-fixture',
      product_title: 'Fixture keys',
      items: ['private-key-fixture'],
    })
  }) as typeof api.post
  await mount(<StoreClaimPage token='opaque-fixture-token' />)
  assert.equal(collections, 0)
  assert.equal(
    document.body.textContent?.includes('private-key-fixture'),
    false
  )
  await click(button('Collect items'))
  assert.equal(
    (document.querySelector('textarea') as HTMLTextAreaElement).value,
    'private-key-fixture'
  )
  await act(async () => {
    owner(3)
    await flush()
  })
  assert.equal(document.querySelector('textarea'), null)
  assert.equal(collections, 1)
})
test('pickup protection validates both minimum length and the bcrypt UTF-8 byte limit', async () => {
  owner(2)
  api.get = (async () =>
    result({
      version: 'merchant-store-v1',
      text: 'Terms',
      accepted: true,
    })) as typeof api.get
  await mount(
    <StoreCheckout
      product={{ ...product, official: true, pickup_code_required: true }}
    />
  )
  const code = document.querySelector('#store-pickup-code') as HTMLInputElement
  await input(code, '1234567')
  assert.equal(button('Place order').disabled, true)
  await input(code, '12345678')
  assert.equal(button('Place order').disabled, false)
  await input(code, '密'.repeat(25))
  assert.equal(button('Place order').disabled, true)
})
test('login-protected claims never collect anonymously', async () => {
  owner(null)
  let collections = 0
  api.get = (async () =>
    result({
      product_title: 'Fixture keys',
      status: 'paid',
      pickup_login_required: true,
      pickup_code_required: true,
      pickup_login_satisfied: false,
    })) as typeof api.get
  api.post = (async () => {
    collections++
    return result(null)
  }) as typeof api.post
  await mount(<StoreClaimPage token='opaque-fixture-token' />)
  assert.ok(document.querySelector('a[href^="/sign-in?redirect="]'))
  assert.equal(document.querySelector('input'), null)
  assert.equal(collections, 0)
})
test('cold-open collection uses only order-scoped cookie authentication without bootstrapping a user', async () => {
  owner(null)
  const token = 'c'.repeat(43)
  const requests: Array<{
    method: string
    url: string
    config: Record<string, unknown>
  }> = []
  api.get = (async (url: string, config: Record<string, unknown>) => {
    requests.push({ method: 'GET', url, config })
    return result({
      product_title: 'Fixture keys',
      status: 'paid',
      pickup_login_required: true,
      pickup_code_required: false,
      pickup_login_satisfied: true,
    })
  }) as typeof api.get
  api.post = (async (
    url: string,
    _input: unknown,
    config: Record<string, unknown>
  ) => {
    requests.push({ method: 'POST', url, config })
    return result({
      order_id: 'order-fixture',
      product_title: 'Fixture keys',
      items: ['cookie-authorized-private-item'],
    })
  }) as typeof api.post
  await mount(<StoreClaimPage token={token} />)
  assert.equal(document.querySelector('a[href^="/sign-in?redirect="]'), null)
  assert.equal(requests.length, 1, 'GET must not collect private inventory')
  await click(button('Collect items'))
  assert.equal(
    (document.querySelector('textarea') as HTMLTextAreaElement).value,
    'cookie-authorized-private-item'
  )
  assert.deepEqual(
    requests.map(({ method, url }) => ({ method, url })),
    [
      { method: 'GET', url: `/api/user/auth/store-claim/${token}` },
      { method: 'POST', url: `/api/user/auth/store-claim/${token}` },
    ]
  )
  for (const request of requests) {
    assert.equal(request.config.skipAuthRefresh, true)
    assert.equal(request.config.disableDuplicate, true)
    assert.equal(request.config.withCredentials, true)
  }
  assert.equal(useAuthStore.getState().auth.user, null)
  assert.equal(useAuthStore.getState().auth.accessToken, null)
})
test('a different signed-in account cannot override server collection authorization', async () => {
  owner(3)
  let collections = 0
  api.get = (async () =>
    result({
      product_title: 'Fixture keys',
      status: 'paid',
      pickup_login_required: true,
      pickup_code_required: false,
      pickup_login_satisfied: false,
    })) as typeof api.get
  api.post = (async () => {
    collections++
    return result(null)
  }) as typeof api.post
  await mount(<StoreClaimPage token={'d'.repeat(43)} />)
  assert.ok(document.querySelector('a[href^="/sign-in?redirect="]'))
  assert.equal(document.querySelector('form'), null)
  assert.equal(collections, 0)
})
test('external gateway blank secrets remain write-only, metadata is not sent back', async () => {
  owner(2)
  let body: Record<string, unknown> | undefined
  api.put = (async (_url: string, input: Record<string, unknown>) => {
    body = input
    return result({})
  }) as typeof api.put
  await mount(
    <StoreGatewayEditor
      gateway={{
        provider: 'external:epay',
        enabled: true,
        configured: true,
        has_key: true,
        has_private_key: false,
        gateway_url: 'https://gateway.example.test',
        partner_id: '123',
        payment_type: 'alipay',
        currency: 'CNY',
        callback_urls: {
          notify:
            'https://shop.example.test/api/store/payments/epay/{order_id}/notify',
        },
      }}
      eligible
      onSaved={async () => {}}
    />
  )
  assert.equal(
    (document.querySelector('input[type=password]') as HTMLInputElement).value,
    ''
  )
  await click(button('Save payment method'))
  assert.equal(body?.key, '')
  assert.equal(body?.provider, 'external:epay')
  assert.equal('callback_urls' in (body || {}), false)
  assert.equal('has_key' in (body || {}), false)
})
test('seller local currency changes preserve integer credits and wallet preferences', async () => {
  owner(2)
  useWalletCurrencyPreferenceStore.getState().setPreference('USD')
  useSystemConfigStore.setState({
    config: {
      ...originalConfig,
      currency: {
        ...originalConfig.currency,
        creditsPerUsd: 500000,
        creditsPerUsdExact: '500000',
        cnyPerUsd: 7,
        cnyPerUsdExact: '7',
        quotaDisplayType: 'USD',
        currencyUnit: 'credit',
      },
    },
  })
  let body: Record<string, unknown> | undefined
  api.post = (async (_url: string, input: Record<string, unknown>) => {
    body = input
    return result(product)
  }) as typeof api.post
  await mount(
    <StoreProductEditor
      allowedMethods={['balance']}
      minimumPriceQuota={0}
      onClose={() => {}}
      onSaved={async () => {}}
    />
  )
  await input(
    document.querySelector('#store-title') as HTMLInputElement,
    'New keys'
  )
  await input(document.querySelector('#store-price') as HTMLInputElement, '10')
  const selector = document.querySelector(
    'select[aria-label="Price currency"]'
  ) as HTMLSelectElement
  await act(async () => {
    selector.value = 'CNY'
    selector.dispatchEvent(new Event('change', { bubbles: true }))
    await flush()
  })
  assert.equal(
    (document.querySelector('#store-price') as HTMLInputElement).value,
    '70'
  )
  await act(async () => {
    selector.value = 'CREDIT'
    selector.dispatchEvent(new Event('change', { bubbles: true }))
    await flush()
  })
  assert.equal(
    (document.querySelector('#store-price') as HTMLInputElement).value,
    '5000000'
  )
  assert.equal(useWalletCurrencyPreferenceStore.getState().preference, 'USD')
  await act(async () => {
    document
      .querySelector('form')!
      .dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await flush()
  })
  assert.equal(body?.price_quota, 5000000, document.body.textContent || '')
  assert.equal('official' in (body || {}), false)
})

test('malformed product identifiers fail visibly without a disabled-query spinner or request', async () => {
  owner(null)
  let requests = 0
  api.get = (async () => {
    requests++
    return result(product)
  }) as typeof api.get
  await mount(<StoreProductPage id='bad/identifier' />)
  assert.equal(requests, 0)
  assert.ok(document.body.textContent?.includes('Product not found'))
})

test('delivery email verification uses only the account address and confirms a six-digit code', async () => {
  owner(2)
  let verified = false
  const writes: Array<{ url: string; body: unknown }> = []
  api.get = (async () =>
    result({ verified, email: 'fixture@example.invalid' })) as typeof api.get
  api.post = (async (url: string, body: unknown) => {
    writes.push({ url, body })
    if (url.endsWith('/send')) return result({ sent: true })
    verified = true
    return result(null)
  }) as typeof api.post
  await mount(<StoreDeliveryEmail ownerId={2} />)
  assert.equal(writes.length, 0)
  await click(button('Send verification code'))
  assert.deepEqual(writes[0].body, {})
  await input(
    document.querySelector('#delivery-email-code') as HTMLInputElement,
    '12345'
  )
  assert.equal(button('Confirm email').disabled, true)
  await input(
    document.querySelector('#delivery-email-code') as HTMLInputElement,
    '123456'
  )
  await click(button('Confirm email'))
  assert.deepEqual(writes[1].body, { code: '123456' })
  assert.ok(document.body.textContent?.includes('Email verified'))
})

test('an updated paid order clears a previously prepared payment session', async () => {
  owner(2)
  const pending: StoreOrder = {
    id: 'order-fixture',
    trade_no: 'MS-fixture',
    buyer_id: 2,
    seller_id: 9,
    product_id: product.id,
    product_title: product.title,
    quantity: 1,
    unit_price_quota: 500000,
    price_quota: 500000,
    fee_quota: 5000,
    payment_method: 'platform:waffo_pancake',
    status: 'pending',
    amount_minor: 700,
    currency: 'CNY',
    frozen_usd_fx: '7',
    created_at: 1,
    paid_at: 0,
    expires_at: 9999999999,
    pickup_login_required: false,
    pickup_code_required: false,
    email_pickup_link: false,
    payment_issued: true,
  }
  let update: React.Dispatch<React.SetStateAction<StoreOrder>> | undefined
  function Harness() {
    const [order, setOrder] = useState(pending)
    update = setOrder
    return <StoreOrderRow order={order} buyer locale='en' />
  }
  api.post = (async () =>
    result({
      order_id: pending.id,
      method: 'GET',
      payment_url: 'https://gateway.example.test/pay',
      currency: 'CNY',
      amount: '7.00',
      amount_minor: 700,
      status: 'pending',
    })) as typeof api.post
  await mount(<Harness />)
  await click(button('Prepare payment'))
  assert.ok(button('Continue to payment'))
  await act(async () => {
    update?.({ ...pending, status: 'paid' })
    await flush()
  })
  assert.equal(
    Array.from(document.querySelectorAll('button')).some(
      (el) => el.textContent === 'Continue to payment'
    ),
    false
  )
  assert.ok(button('Get pickup link'))
})

for (const state of ['pending', 'sent', 'awaiting_verification']) {
  test(`account email verification is shown only for legacy awaiting verification orders (${state})`, async () => {
    owner(2)
    let accountEmailReads = 0
    api.get = (async (url: string) => {
      if (url.endsWith('/email/status')) {
        accountEmailReads++
        return result({ verified: false, email: 'account@example.invalid' })
      }
      return result({
        items: [
          {
            id: 'order-email-fixture',
            trade_no: 'MS-fixture',
            buyer_id: 2,
            seller_id: 9,
            product_title: 'Fixture keys',
            quantity: 1,
            price_quota: 500000,
            payment_method: 'balance',
            status: 'paid',
            created_at: 1,
            email_pickup_link: true,
            email_delivery_status: state,
          },
        ],
        offset: 0,
        limit: 20,
        has_more: false,
      })
    }) as typeof api.get
    await mount(<StoreOrdersPage />)
    const expected = state === 'awaiting_verification'
    assert.equal(accountEmailReads > 0, expected)
    assert.equal(
      document.body.textContent?.includes('Verify delivery email'),
      expected
    )
  })
}
