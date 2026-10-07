/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

import type { StoreOrder, StoreProduct, StoreVariant } from './types'

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
Object.defineProperty(dom.navigator, 'locks', {
  configurable: true,
  value: {
    request: async (_name: string, task: () => Promise<unknown>) => task(),
  },
})
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  value: true,
})
// Happy DOM does not provide DOMPurify's browser realm. This suite checks the
// page-to-renderer boundary; store-pickup-review.mjs verifies the real renderer
// and sanitizer in Chromium.
mock.module('@/components/ui/markdown', () => ({
  Markdown: ({ children }: { children: string }) => (
    <div data-store-markdown>{children}</div>
  ),
}))
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
const { StoreClaimItems } = await import('./claim-items')
const { StoreGatewayEditor } = await import('./settings-page')
const { StoreSettingsPage } = await import('./settings-page')
const { StorePaymentCategoriesForm } = await import('./payment-categories')
const { MerchantStoreSettingsSection } =
  await import('@/features/system-settings/integrations/merchant-store-settings-section')
const { StoreOrderRow, StoreOrdersPage } = await import('./orders-page')
const { createStoreCheckoutIntentJournal } = await import('./checkout-intent')
const { StoreAmount } = await import('./shared')
const { StoreDeliveryEmail } = await import('./delivery-email')
const { StoreProductEditor } = await import('./seller-page')
const { StoreInventoryImport } = await import('./seller-page')
const { StoreSellerPage } = await import('./seller-page')
const { StoreSalesLimit } = await import('./sales-limit')
const { StoreVariantsManager } = await import('./variants-manager')
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
const originalDelete = api.delete
const originalWriteText = dom.navigator.clipboard.writeText
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
function spec(id: string, changes: Partial<StoreVariant> = {}): StoreVariant {
  return {
    id,
    product_id: product.id,
    name: id,
    price_quota: 1500000,
    template: 'card-key',
    enabled: true,
    is_default: false,
    inventory_total: 5,
    inventory_available: 5,
    reserved_stock: 0,
    sale_available: 2,
    trading_paused: false,
    created_at: 1,
    updated_at: 1,
    ...changes,
  }
}
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
  const mockPost = api.post
  api.post = (async (url: string, body: unknown, options?: unknown) => {
    const buyerId = useAuthStore.getState().auth.user?.id
    const response = await mockPost(url, body, options as never)
    if (
      url === '/api/store/orders' &&
      response.data?.success === true &&
      response.data?.data?.order
    ) {
      const request = body as Record<string, unknown>
      response.data.data.order = {
        product_id: request.product_id,
        variant_id: request.variant_id,
        quantity: request.quantity,
        payment_method: request.payment_method,
        buyer_id: buyerId,
        ...response.data.data.order,
      }
    }
    return response
  }) as typeof api.post

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
async function input(
  node: HTMLInputElement | HTMLTextAreaElement,
  value: string
) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(
      node.tagName === 'TEXTAREA'
        ? dom.HTMLTextAreaElement.prototype
        : dom.HTMLInputElement.prototype,
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
  api.delete = originalDelete
  dom.navigator.clipboard.writeText = originalWriteText
  owner(null)
  useSystemConfigStore.setState({ config: originalConfig })
  localStorage.clear()
  dom.happyDOM.setURL(
    'https://shop.example.test/store/products/product-fixture'
  )
  document.body.replaceChildren()
})
after(() => dom.happyDOM.abort())

test('fixed-content product edits load private content from the seller endpoint and write only intentional changes', async () => {
  owner(2)
  const body = '# Private guide\n\nShared instructions.'
  const fixed = {
    ...product,
    template: 'fixed-content' as const,
    unlimited_supply: true,
    default_variant_id: 'fixed-default',
  }
  const reads: string[] = []
  const writes: Record<string, unknown>[] = []
  api.get = (async (url: string) => {
    reads.push(url)
    return result({ content: body })
  }) as typeof api.get
  api.put = (async (_url: string, value: Record<string, unknown>) => {
    writes.push(value)
    return result(fixed)
  }) as typeof api.put
  await mount(
    <StoreProductEditor
      product={fixed}
      minimumPriceQuota={0}
      allowedMethods={fixed.payment_methods}
      onClose={() => {}}
      onSaved={async () => {}}
    />
  )
  const currency = document.querySelector<HTMLSelectElement>(
    'select[aria-label="Price currency"]'
  )!
  await act(async () => {
    currency.value = 'CREDIT'
    currency.dispatchEvent(new Event('change', { bubbles: true }))
    await flush()
  })
  const field = document.querySelector<HTMLTextAreaElement>(
    '#store-fixed-content'
  )!
  assert.ok(field)
  assert.equal(field.value, body)
  assert.deepEqual(reads, [
    `/api/store/products/${product.id}/variants/fixed-default/fixed-content`,
  ])
  await click(button('Save draft'))
  assert.equal(writes[0].fixed_content, undefined)
  await input(field, '\n  ')
  assert.equal(button('Save draft').disabled, true)
  await input(field, '界'.repeat(44000))
  assert.equal(button('Save draft').disabled, true)
  await input(field, body + '\n\nUpdated.')
  await click(button('Save draft'))
  assert.equal(writes[1].fixed_content, body + '\n\nUpdated.')
  assert.equal(writes[1].unlimited_supply, undefined)
})

test('new fixed-content variants require one private body and send it with the exact selected template', async () => {
  owner(2)
  const writes: Record<string, unknown>[] = []
  api.post = (async (url: string, value: Record<string, unknown>) => {
    assert.equal(url, `/api/store/products/${product.id}/variants`)
    writes.push(value)
    return result(null)
  }) as typeof api.post
  await mount(
    <StoreVariantsManager
      product={{ ...product, variants: [] }}
      fixedContentSupported
      minimumPriceQuota={0}
      onChanged={async () => {}}
    />
  )
  await click(button('Add variant'))
  const currency = document.querySelector<HTMLSelectElement>(
    'select[aria-label="Price currency"]'
  )!
  await act(async () => {
    currency.value = 'CREDIT'
    currency.dispatchEvent(new Event('change', { bubbles: true }))
    await flush()
  })
  const template = document.querySelector<HTMLSelectElement>(
    `#variant-template-${product.id}`
  )!
  await act(async () => {
    template.value = 'fixed-content'
    template.dispatchEvent(new Event('change', { bubbles: true }))
    await flush()
  })
  await input(
    document.querySelector<HTMLInputElement>(`#variant-name-${product.id}`)!,
    'Shared guide'
  )
  assert.equal(button('Save variant').disabled, true)
  const textareas = document.querySelectorAll<HTMLTextAreaElement>('textarea')
  assert.equal(textareas.length, 1)
  await input(textareas[0], '# Shared content\n\nKeep line breaks.')
  await click(button('Save variant'))
  assert.equal(writes.length, 1)
  assert.equal(writes[0].template, 'fixed-content')
  assert.equal(writes[0].fixed_content, '# Shared content\n\nKeep line breaks.')
})

test('fixed-content inventory shows guidance without reading or importing stock', async () => {
  owner(2)
  let requests = 0
  api.get = (async () => {
    requests++
    return result({ items: [] })
  }) as typeof api.get
  api.post = (async () => {
    requests++
    return result(null)
  }) as typeof api.post
  await mount(
    <StoreInventoryImport
      product={{ ...product, template: 'fixed-content' }}
      onClose={() => {}}
      onSaved={async () => {}}
    />
  )
  assert.match(
    document.body.textContent || '',
    /does not accept inventory imports/
  )
  assert.equal(document.querySelector('textarea'), null)
  assert.equal(document.querySelector('input[type="file"]'), null)
  assert.equal(requests, 0)
})

test('unsupported servers omit fixed-content authoring from new product choices', async () => {
  owner(2)
  await mount(
    <StoreProductEditor
      product={product}
      minimumPriceQuota={0}
      allowedMethods={product.payment_methods}
      onClose={() => {}}
      onSaved={async () => {}}
    />
  )
  assert.equal(
    document.querySelector('#store-template option[value="fixed-content"]'),
    null
  )
})

test('unsupported fixed-content capability cannot be inherited into a new variant from the product template', async () => {
  owner(2)
  await mount(
    <StoreVariantsManager
      product={{ ...product, template: 'fixed-content', variants: [] }}
      minimumPriceQuota={0}
      onChanged={async () => {}}
    />
  )
  await click(button('Add variant'))
  const template = document.querySelector<HTMLSelectElement>(
    `#variant-template-${product.id}`
  )!
  assert.equal(template.value, 'card-key')
  assert.equal(template.querySelector('option[value="fixed-content"]'), null)
  assert.equal(document.querySelector('textarea'), null)
})

test('editing a fixed-content non-default variant reads and updates only that variants protected content', async () => {
  owner(2)
  const variant = spec('private-guide', {
    template: 'fixed-content',
    unlimited_supply: true,
    is_default: false,
  })
  const reads: string[] = []
  const writes: Array<{ url: string; body: Record<string, unknown> }> = []
  api.get = (async (url: string) => {
    reads.push(url)
    return result({ content: 'Original guide' })
  }) as typeof api.get
  api.put = (async (url: string, body: Record<string, unknown>) => {
    writes.push({ url, body })
    return result(variant)
  }) as typeof api.put
  await mount(
    <StoreVariantsManager
      product={{ ...product, variants: [variant] }}
      minimumPriceQuota={0}
      fixedContentSupported
      onChanged={async () => {}}
    />
  )
  await click(button('Edit'))
  const currency = document.querySelector<HTMLSelectElement>(
    'select[aria-label="Price currency"]'
  )!
  await act(async () => {
    currency.value = 'CREDIT'
    currency.dispatchEvent(new Event('change', { bubbles: true }))
    await flush()
  })
  const textarea = document.querySelector<HTMLTextAreaElement>(
    `#variant-fixed-content-${product.id}`
  )!
  assert.equal(textarea.value, 'Original guide')
  assert.deepEqual(reads, [
    `/api/store/products/${product.id}/variants/private-guide/fixed-content`,
  ])
  await input(textarea, 'Updated guide')
  await click(button('Save variant'))
  assert.equal(
    writes[0].url,
    `/api/store/products/${product.id}/variants/private-guide`
  )
  assert.equal(writes[0].body.fixed_content, 'Updated guide')
})

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
  assert.equal(document.querySelectorAll('article, [role="article"]').length, 1)
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
test('private draft pause survives resume while public drafts remain unpausable', async () => {
  owner(9)
  let privateProduct: StoreProduct = {
    ...product,
    title: 'Private draft fixture',
    visibility: 'private',
    test_mode: true,
    status: 'draft',
  }
  const privatePending: StoreProduct = {
    ...privateProduct,
    id: 'private-pending-fixture',
    title: 'Private pending fixture',
    status: 'pending',
  }
  const publicDraft: StoreProduct = {
    ...product,
    id: 'public-draft-fixture',
    title: 'Public draft fixture',
    visibility: 'public',
    test_mode: false,
    status: 'draft',
  }
  const publicPublished: StoreProduct = {
    ...publicDraft,
    id: 'public-published-fixture',
    title: 'Public published fixture',
    status: 'published',
  }
  const writes: boolean[] = []
  api.get = (async (url: string) => {
    if (url === '/api/store/config') {
      return result({ minimum_unit_price_quota: 0 })
    }
    if (url === '/api/store/payments/settings') return result({ items: [] })
    assert.equal(url, '/api/store/my/products')
    return result({
      items: [privateProduct, privatePending, publicDraft, publicPublished],
      has_more: false,
    })
  }) as typeof api.get
  api.put = (async (url: string, body: { paused: boolean }) => {
    assert.equal(url, `/api/store/products/${product.id}/paused`)
    assert.deepEqual(Object.keys(body), ['paused'])
    writes.push(body.paused)
    // Private resume deliberately returns to the backend's buyable draft state.
    privateProduct = {
      ...privateProduct,
      status: body.paused ? 'paused' : 'draft',
    }
    return result(null)
  }) as typeof api.put
  await mount(<StoreSellerPage />)
  function row(title: string) {
    const node = [...document.querySelectorAll('article')].find(
      (item) => item.querySelector('h2')?.textContent === title
    )
    assert.ok(node, `product row ${title}`)
    return node
  }
  function rowButton(title: string, label: string) {
    const node = [...row(title).querySelectorAll('button')].find(
      (item) => item.textContent?.trim() === label
    )
    assert.ok(node, `${title}: ${label}`)
    return node
  }
  function assertPublicDraftUnpausable() {
    assert.doesNotMatch(
      row(publicDraft.title).textContent || '',
      /Pause trading|Resume trading/
    )
  }
  assertPublicDraftUnpausable()
  assert.ok(rowButton(publicPublished.title, 'Take off shelf'))
  assert.ok(rowButton(privatePending.title, 'Pause trading'))
  await click(rowButton(privateProduct.title, 'Pause trading'))
  assertPublicDraftUnpausable()
  assert.doesNotMatch(
    row(privateProduct.title).textContent || '',
    /Take off shelf|Relist product/
  )
  assert.ok(rowButton(privateProduct.title, 'Resume trading'))
  await click(rowButton(privateProduct.title, 'Resume trading'))
  assertPublicDraftUnpausable()
  await click(rowButton(privateProduct.title, 'Pause trading'))
  assertPublicDraftUnpausable()
  assert.deepEqual(writes, [true, false, true])
})
test('taking a product off shelf and relisting use distinct listing requests without deleting stock', async () => {
  owner(2)
  let listed = true
  const writes: unknown[] = []
  api.get = (async (url: string) => {
    if (url === '/api/store/config') {
      return result({ minimum_unit_price_quota: 0 })
    }
    if (url === '/api/store/payments/settings') {
      return result({
        items: [],
        categories: { platform_enabled: false, external_enabled: false },
      })
    }
    assert.equal(url, '/api/store/my/products')
    return result({
      items: [
        {
          ...product,
          status: listed ? 'published' : 'off_shelf',
          available_stock: 100,
          sale_limit: 10,
          paid_quantity: 2,
          reserved_quantity: 3,
          sale_available: 5,
        },
      ],
      has_more: false,
    })
  }) as typeof api.get
  api.put = (async (url: string, body: { listed: boolean }) => {
    assert.equal(url, `/api/store/products/${product.id}/listing`)
    writes.push(body)
    listed = body.listed
    return result(null)
  }) as typeof api.put
  api.delete = (async () =>
    assert.fail('Listing must not delete inventory')) as typeof api.delete
  await mount(<StoreSellerPage />)
  await click(button('Take off shelf'))
  assert.match(document.body.textContent || '', /Off shelf/)
  assert.match(document.body.textContent || '', /Undelivered inventory: 100/)
  await click(button('Relist product'))
  assert.deepEqual(writes, [{ listed: false }, { listed: true }])
  assert.ok(button('Take off shelf'))
})
test('remaining sales quota preserves 1000 physical items while setting 10, 0, and unlimited distinctly', async () => {
  const calls: unknown[] = []
  let refreshed = 0
  api.put = (async (url: string, body: unknown) => {
    assert.equal(url, `/api/store/products/${product.id}/sales-availability`)
    calls.push(body)
    return result(null)
  }) as typeof api.put
  await mount(
    <StoreSalesLimit
      product={{
        ...product,
        available_stock: 997,
        inventory_total: 1000,
        sale_limit: null,
        paid_quantity: 20,
        reserved_quantity: 3,
        sale_available: 997,
      }}
      onSaved={async () => {
        refreshed++
      }}
    />
  )
  assert.match(document.body.textContent || '', /Undelivered inventory: 1000/)
  const unlimited = document.querySelector<HTMLElement>('[role="switch"]')
  assert.ok(unlimited)
  await click(unlimited)
  const limit = document.querySelector<HTMLInputElement>(
    `#store-sale-limit-${product.id}`
  )
  assert.ok(limit)
  await input(limit, '10')
  await click(button('Save sales quota'))
  await input(limit, '0')
  await click(button('Save sales quota'))
  await click(unlimited)
  await click(button('Save sales quota'))
  assert.deepEqual(calls, [
    { available_count: 10 },
    { available_count: 0 },
    { available_count: null },
  ])
  assert.equal(refreshed, 3)
  assert.match(document.body.textContent || '', /Undelivered inventory: 1000/)
})
test('a failed sales limit save keeps the merchant draft and rejects fractional caps', async () => {
  let calls = 0
  let refreshed = 0
  api.put = (async () => {
    calls++
    throw new Error('offline')
  }) as typeof api.put
  await mount(
    <StoreSalesLimit
      product={{
        ...product,
        sale_limit: 10,
        paid_quantity: 1,
        reserved_quantity: 1,
        sale_available: 5,
      }}
      onSaved={async () => {
        refreshed++
      }}
    />
  )
  const limit = document.querySelector<HTMLInputElement>(
    `#store-sale-limit-${product.id}`
  )
  assert.ok(limit)
  assert.equal(
    limit.value,
    '9',
    'the merchant edits remaining quota without adding lifetime paid quantity'
  )
  await input(limit, '1.5')
  await click(button('Save sales quota'))
  assert.equal(calls, 0)
  await input(limit, '20')
  await click(button('Save sales quota'))
  assert.equal(calls, 1)
  assert.equal(refreshed, 0)
  assert.equal(limit.value, '20')
  assert.match(document.body.textContent || '', /Store request failed/)
})
test('remaining quota follows settlements only while pristine and preserves unsaved edits across refreshes', async () => {
  let replaceProduct!: React.Dispatch<React.SetStateAction<StoreProduct>>
  const saved: unknown[] = []
  api.put = (async (_url: string, body: unknown) => {
    saved.push(body)
    return result(null)
  }) as typeof api.put
  function Harness() {
    const [current, setCurrent] = useState<StoreProduct>({
      ...product,
      sale_limit: 30,
      paid_quantity: 20,
      reserved_quantity: 3,
      sale_available: 7,
    })
    replaceProduct = setCurrent
    return <StoreSalesLimit product={current} onSaved={async () => {}} />
  }
  await mount(<Harness />)
  const limit = document.querySelector<HTMLInputElement>(
    `#store-sale-limit-${product.id}`
  )!
  assert.equal(limit.value, '10')
  await act(async () => {
    replaceProduct((current) => ({
      ...current,
      paid_quantity: 23,
      reserved_quantity: 0,
    }))
    await flush()
  })
  assert.equal(
    limit.value,
    '7',
    'a payment cannot silently replenish an untouched quota form'
  )
  await input(limit, '12')
  await act(async () => {
    replaceProduct((current) => ({ ...current, paid_quantity: 24 }))
    await flush()
  })
  assert.equal(
    limit.value,
    '12',
    'background data does not discard a merchant draft'
  )
  await click(button('Save sales quota'))
  assert.deepEqual(saved, [{ available_count: 12 }])
  assert.equal(
    limit.value,
    '12',
    'a delayed refresh cannot overwrite the successful input with stale props'
  )
  await act(async () => {
    replaceProduct((current) => ({
      ...current,
      sale_limit: 37,
      paid_quantity: 26,
    }))
    await flush()
  })
  assert.equal(
    limit.value,
    '11',
    'after save, subsequent server settlements update the pristine form'
  )
})
test('missing historical paid quantity does not turn a cumulative ceiling into guessed remaining quota', async () => {
  const saved: unknown[] = []
  api.put = (async (_url: string, body: unknown) => {
    saved.push(body)
    return result(null)
  }) as typeof api.put
  await mount(
    <StoreSalesLimit
      product={{ ...product, sale_limit: 30, paid_quantity: undefined }}
      onSaved={async () => {}}
    />
  )
  const limit = document.querySelector<HTMLInputElement>(
    `#store-sale-limit-${product.id}`
  )!
  assert.equal(limit.value, '')
  await click(button('Save sales quota'))
  assert.deepEqual(saved, [])
  await input(limit, '10')
  await click(button('Save sales quota'))
  assert.deepEqual(saved, [{ available_count: 10 }])
})
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
test('pickup shows the merchant specification for each key and safely renders authorized product details', async () => {
  owner(2)
  const reads: string[] = []
  const writes: string[] = []
  const items = ['first key', 'second key\nextra line', 'third key']
  const variantName = '商家自定义 <特别套餐> / 任意规格'
  api.get = (async (url: string) => {
    reads.push(url)
    return result({
      product_title: 'Fixture keys',
      variant_name: variantName,
      status: 'paid',
      pickup_login_required: false,
      pickup_code_required: false,
    })
  }) as typeof api.get
  api.post = (async () =>
    result({
      order_id: 'order-fixture',
      product_title: 'Fixture keys',
      variant_name: variantName,
      product_description:
        '## Merchant instructions\n\n| Feature | Detail |\n| --- | --- |\n| Custom | Merchant description |\n\n- First step\n\n[Documentation](https://merchant.example.test/docs)\n\n<script>maliciousDescription()</script>\n<img src="https://merchant.example.test/image" onerror="maliciousDescription()">\n[Unsafe](javascript:alert(1))',
      product_links: [
        {
          title: 'Merchant instructions',
          url: 'https://merchant.example.test/help',
          description: '<b>Plain link explanation</b>',
        },
        { title: 'Unsafe script', url: 'javascript:alert(1)', description: '' },
        {
          title: 'Credential URL',
          url: 'https://user:password@merchant.example.test',
          description: '',
        },
      ],
      items,
    })) as typeof api.post
  dom.navigator.clipboard.writeText = async (value) => {
    writes.push(value)
  }
  await mount(<StoreClaimPage token='selection-order' />)
  assert.equal(document.querySelectorAll('textarea').length, 0)
  assert.equal(
    document.body.textContent?.includes('merchant description'),
    false
  )
  await click(button('Collect items'))
  const checkboxes = [
    ...document.querySelectorAll<HTMLElement>('[role="checkbox"]'),
  ]
  assert.equal(checkboxes.length, 3)
  assert.equal(document.querySelectorAll('script').length, 0)
  assert.match(
    document.querySelector('[data-store-markdown]')?.textContent || '',
    /## Merchant instructions/
  )
  assert.match(
    document.body.textContent || '',
    /<b>Plain link explanation<\/b>/
  )
  const merchantLink = document.querySelector<HTMLAnchorElement>(
    'a[href="https://merchant.example.test/help"]'
  )
  assert.ok(merchantLink)
  assert.equal(merchantLink.rel, 'noopener noreferrer')
  assert.equal(document.querySelector('a[href^="javascript:"]'), null)
  assert.equal(document.body.textContent?.includes('Credential URL'), false)
  assert.equal(
    document.body.textContent?.split(`Specification: ${variantName}`).length,
    5,
    'the exact merchant name appears in the order heading and all three rows'
  )
  assert.equal(button('Copy selected items').disabled, true)
  await click(checkboxes[2])
  await click(checkboxes[0])
  await click(button('Copy selected items'))
  assert.equal(
    writes.at(-1),
    'first key\nthird key',
    'selection keeps original item order'
  )
  await click(button('Invert selection'))
  await click(button('Copy selected items'))
  assert.equal(writes.at(-1), 'second key\nextra line')
  await click(button('Select all'))
  assert.equal(
    checkboxes.every((node) => node.getAttribute('aria-checked') === 'true'),
    true
  )
  await click(button('Copy selected items'))
  assert.equal(writes.at(-1), items.join('\n'))
  await click(button('Invert selection'))
  assert.equal(button('Copy selected items').disabled, true)
  await click(button('Copy all items'))
  assert.equal(writes.at(-1), items.join('\n'))
  const individualCopies = [
    ...document.querySelectorAll<HTMLButtonElement>('button'),
  ].filter((node) => node.textContent?.trim() === 'Copy')
  await click(individualCopies[1])
  assert.equal(writes.at(-1), items[1])
  assert.deepEqual(
    reads,
    ['/api/user/auth/store-claim/selection-order'],
    'pickup never looks up a public product endpoint'
  )
})
test('a partial refund preserves original item numbers and clears selection for the changed delivery set', async () => {
  let update: React.Dispatch<
    React.SetStateAction<{
      items: string[]
      positions: number[]
      ids: string[]
    }>
  >
  const writes: string[] = []
  dom.navigator.clipboard.writeText = async (value) => {
    writes.push(value)
  }
  function RemainingDelivery() {
    const [value, setValue] = useState({
      items: ['first purchased card', 'second purchased card'],
      positions: [1, 2],
      ids: ['first-stock-id', 'second-stock-id'],
    })
    update = setValue
    return (
      <StoreClaimItems
        items={value.items}
        itemPositions={value.positions}
        itemStockIds={value.ids}
        variantName='商家任意规格'
      />
    )
  }
  await mount(<RemainingDelivery />)
  await click(document.querySelector<HTMLElement>('[role="checkbox"]')!)
  assert.equal(button('Copy selected items').disabled, false)
  await act(async () => {
    update!({
      items: ['second purchased card'],
      positions: [2],
      ids: ['second-stock-id'],
    })
    await flush()
  })
  assert.equal(
    button('Copy selected items').disabled,
    true,
    'old selection cannot silently select another card'
  )
  assert.equal(document.body.textContent?.includes('Select item 2'), true)
  assert.equal(document.body.textContent?.includes('Select item 1'), false)
  assert.ok(document.querySelector('#pickup-item-1'))
  assert.equal(document.querySelector('#pickup-item-0'), null)
  await click(document.querySelector<HTMLElement>('[role="checkbox"]')!)
  await click(button('Copy selected items'))
  assert.equal(writes.at(-1), 'second purchased card')
})

test('pickup selection and private details reset when the account or pickup token changes', async () => {
  owner(2)
  api.get = (async () =>
    result({
      product_title: 'Fixture keys',
      status: 'paid',
      pickup_login_required: false,
      pickup_code_required: false,
    })) as typeof api.get
  api.post = (async (url: string) =>
    result({
      order_id: url.endsWith('second') ? 'second-order' : 'first-order',
      product_title: 'Fixture keys',
      product_description: 'Private merchant details',
      items: ['private selection key'],
    })) as typeof api.post
  let changeToken: (value: string) => void = () => {}
  function ChangingToken() {
    const [token, setToken] = useState('first')
    changeToken = setToken
    return <StoreClaimPage token={token} />
  }
  await mount(<ChangingToken />)
  await click(button('Collect items'))
  assert.equal(
    document.body.textContent?.includes('Specification'),
    false,
    'legacy claims never invent a specification'
  )
  await click(button('Select all'))
  assert.equal(button('Copy selected items').disabled, false)
  await act(async () => {
    owner(3)
    await flush()
  })
  assert.equal(document.querySelector('textarea'), null)
  assert.equal(
    document.body.textContent?.includes('Private merchant details'),
    false
  )
  await click(button('Collect items'))
  assert.equal(button('Copy selected items').disabled, true)
  await click(button('Select all'))
  await act(async () => {
    changeToken('second')
    await flush()
  })
  await act(flush)
  assert.equal(document.querySelector('textarea'), null)
  await click(button('Collect items'))
  assert.equal(button('Copy selected items').disabled, true)
})
test('public product descriptions pass the unchanged merchant source to the shared Markdown renderer', async () => {
  api.get = (async (url: string) => {
    if (url === '/api/store/products/product-fixture') {
      return result({
        ...product,
        description:
          '## Merchant-defined options\n\n| Specification | Description |\n| --- | --- |\n| 自定义规格 | 商家内容 |\n\n1. Read the guide\n2. Use your key\n\n[Guide](https://merchant.example.test/guide)\n\n<script>bad()</script>\n<a href="javascript:bad()" onclick="bad()">Unsafe</a>',
      })
    }
    if (url === '/api/store/disclaimer') {
      return result({ version: 'v1', text: 'Terms', accepted: false })
    }
    assert.fail(`Unexpected product request: ${url}`)
  }) as typeof api.get
  await mount(<StoreProductPage id='product-fixture' />)
  const source =
    document.querySelector('[data-store-markdown]')?.textContent || ''
  assert.match(source, /## Merchant-defined options/)
  assert.match(source, /\| Specification \| Description \|/)
  assert.match(source, /\| 自定义规格 \| 商家内容 \|/)
  assert.match(source, /\[Guide\]\(https:\/\/merchant\.example\.test\/guide\)/)
})
test('pickup clipboard failure keeps the selected text and offers manual copy', async () => {
  api.get = (async () =>
    result({ product_title: 'Fixture keys', status: 'paid' })) as typeof api.get
  api.post = (async () =>
    result({
      order_id: 'failure-order',
      items: ['still-copyable-key'],
    })) as typeof api.post
  dom.navigator.clipboard.writeText = async () => {
    throw new Error('Clipboard denied')
  }
  await mount(<StoreClaimPage token='copy-failure' />)
  await click(button('Collect items'))
  await click(button('Select all'))
  await click(button('Copy selected items'))
  assert.match(
    document.body.textContent || '',
    /Copy failed\. Select and copy the text manually\./
  )
  assert.equal(
    (document.querySelector('textarea') as HTMLTextAreaElement).value,
    'still-copyable-key'
  )
  assert.equal(
    document.querySelector('[role="checkbox"]')?.getAttribute('aria-checked'),
    'true'
  )
})
test('seller unlisting requires confirmation, retains failed rows, and invalidates sale queries after success', async () => {
  owner(9)
  let currentProduct = product
  let attempts = 0
  let reads = 0
  api.get = (async (url: string) => {
    if (url === '/api/store/my/products') {
      reads++
      return result({ items: [currentProduct], has_more: false })
    }
    if (url === '/api/store/config') {
      return result({
        fee_bps: 0,
        promotion_quota: 0,
        platform_payment_methods: [],
      })
    }
    if (url === '/api/store/payments/settings') {
      return result({ items: [], fee_bps: 0 })
    }
    assert.fail(`Unexpected seller request: ${url}`)
  }) as typeof api.get
  api.post = (async (url: string) => {
    assert.equal(url, '/api/store/products/product-fixture/unlist')
    attempts++
    if (attempts === 1) {
      return { data: { success: false, message: 'Unlisting failed' } }
    }
    currentProduct = { ...product, status: 'unlisted' }
    return result(null)
  }) as typeof api.post
  await mount(<StoreSellerPage />)
  const sellerClient = client
  assert.ok(sellerClient)
  sellerClient.setQueryData(['store', 'products', '', 1], { items: [product] })
  sellerClient.setQueryData(['store', 'product', product.id], product)
  const retiredDetails = [
    ['store', 'product', 'account:9', product.id],
    ['store', 'product', 'anonymous', product.id],
    ['store', 'product-preview', 'account:9', product.id],
  ]
  for (const key of retiredDetails) sellerClient.setQueryData(key, product)
  const otherDetail = ['store', 'product', 'account:9', 'other-product']
  sellerClient.setQueryData(otherDetail, { ...product, id: 'other-product' })
  sellerClient.setQueryData(['store', 'reviews'], { items: [product] })
  sellerClient.setQueryData(['store', 'orders', 9], {
    items: ['retained-order'],
  })
  await click(button('Unlist product'))
  assert.equal(attempts, 0)
  assert.ok(document.querySelector('[data-slot="alert-dialog-content"]'))
  await click(button('Cancel'))
  assert.equal(attempts, 0)
  await click(button('Unlist product'))
  await click(
    document.querySelector('[data-slot="alert-dialog-action"]') as HTMLElement
  )
  assert.equal(attempts, 1)
  assert.equal(document.querySelectorAll('article').length, 1)
  assert.match(
    document.querySelector('[data-slot="alert-dialog-content"]')?.textContent ||
      '',
    /Unlisting failed/
  )
  assert.equal(reads, 1, 'failed mutations do not invalidate or hide the row')
  await click(
    document.querySelector('[data-slot="alert-dialog-action"]') as HTMLElement
  )
  assert.equal(document.querySelectorAll('article').length, 1)
  assert.match(document.body.textContent || '', /unlisted/)
  assert.equal(
    document.querySelector('[data-slot="alert-dialog-content"]'),
    null
  )
  assert.equal(
    [...document.querySelectorAll('button')].some(
      (node) => node.textContent?.trim() === 'Unlist product'
    ),
    false
  )
  assert.equal(
    [...document.querySelectorAll('button')].some(
      (node) => node.textContent?.trim() === 'Resume trading'
    ),
    false
  )
  for (const key of [
    ['store', 'products', '', 1],
    ['store', 'reviews'],
  ]) {
    assert.equal(sellerClient.getQueryState(key)?.isInvalidated, true)
  }
  assert.deepEqual(sellerClient.getQueryData(['store', 'products', '', 1]), {
    items: [],
  })
  assert.equal(
    sellerClient.getQueryData(['store', 'product', product.id]),
    undefined,
    'A retired detail cannot reappear from an old successful cache entry'
  )
  for (const key of retiredDetails) {
    assert.equal(sellerClient.getQueryData(key), undefined)
  }
  assert.equal(
    sellerClient.getQueryData<StoreProduct>(otherDetail)?.id,
    'other-product',
    'Retiring one product preserves other viewer-scoped details'
  )
  assert.equal(
    sellerClient.getQueryState(['store', 'orders', 9])?.isInvalidated,
    false
  )
  assert.equal(reads, 2)
})
test('seller deletion waits for server success, supports retry, then removes the product', async () => {
  owner(9)
  let present = true
  let attempts = 0
  let resolveDelete: () => void = () => {}
  api.get = (async (url: string) => {
    if (url === '/api/store/my/products') {
      return result({ items: present ? [product] : [], has_more: false })
    }
    if (url === '/api/store/config') {
      return result({
        fee_bps: 0,
        promotion_quota: 0,
        platform_payment_methods: [],
      })
    }
    if (url === '/api/store/payments/settings') {
      return result({ items: [], fee_bps: 0 })
    }
    assert.fail(`Unexpected seller request: ${url}`)
  }) as typeof api.get
  api.delete = (async (url: string) => {
    assert.equal(url, '/api/store/products/product-fixture')
    attempts++
    if (attempts === 1) {
      return { data: { success: false, message: 'Deletion failed' } }
    }
    await new Promise<void>((resolve) => {
      resolveDelete = resolve
    })
    present = false
    return result(null)
  }) as typeof api.delete
  await mount(<StoreSellerPage />)
  await click(button('Delete product'))
  assert.equal(attempts, 0)
  await click(button('Cancel'))
  assert.equal(document.querySelectorAll('article').length, 1)
  await click(button('Delete product'))
  await click(
    document.querySelector('[data-slot="alert-dialog-action"]') as HTMLElement
  )
  assert.equal(document.querySelectorAll('article').length, 1)
  assert.match(
    document.querySelector('[data-slot="alert-dialog-content"]')?.textContent ||
      '',
    /Deletion failed/
  )
  await click(
    document.querySelector('[data-slot="alert-dialog-action"]') as HTMLElement
  )
  assert.equal(attempts, 2)
  assert.equal(
    document.querySelectorAll('article').length,
    1,
    'pending request must not hide the product'
  )
  assert.equal(
    (
      document.querySelector(
        '[data-slot="alert-dialog-action"]'
      ) as HTMLButtonElement
    ).disabled,
    true
  )
  await act(async () => {
    resolveDelete()
    await flush()
  })
  assert.equal(document.querySelectorAll('article').length, 0)
  assert.match(document.body.textContent || '', /No products yet/)
  assert.equal(
    document.querySelector('[data-slot="alert-dialog-content"]'),
    null
  )
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
test('multi-variant checkout binds retries to the original ID and refuses a new purchase while the result is unknown', async () => {
  owner(2)
  const writes: Record<string, unknown>[] = []
  api.get = (async () =>
    result({ version: 'merchant-store-v1', accepted: true })) as typeof api.get
  api.post = (async (url: string, body: Record<string, unknown>) => {
    assert.equal(url, '/api/store/orders')
    writes.push(body)
    throw {
      response: {
        data: {
          code: 'STORE_UPGRADE_IN_PROGRESS',
          message: 'Server upgrade gate',
        },
      },
    }
  }) as typeof api.post
  await mount(
    <StoreCheckout
      product={{
        ...product,
        official: true,
        available_stock: 10,
        default_variant_id: 'basic',
        variants: [spec('basic'), spec('plus', { price_quota: 5000000 })],
      }}
    />
  )
  assert.equal(button('Place order').disabled, true)
  assert.match(
    document.body.textContent || '',
    /Choose a variant before ordering/
  )
  const choose = (id: string) =>
    document.querySelector<HTMLInputElement>(
      `input[type="radio"][value="${id}"]`
    )!
  await click(choose('basic'))
  const quantity = document.querySelector<HTMLInputElement>('#store-quantity')!
  await input(quantity, '3')
  assert.equal(button('Place order').disabled, true)
  assert.equal(quantity.getAttribute('aria-valuemax'), '2')
  await input(quantity, '2')
  await click(button('Place order'))
  assert.match(
    document.body.textContent || '',
    /Shop upgrade is in progress. Existing orders are still accessible./
  )
  await click(button('Retry this order request'))
  await click(choose('plus'))
  assert.equal(button('Place order').disabled, true)
  await click(button('Place order'))
  assert.equal(writes.length, 2)
  assert.equal(writes[0].variant_id, 'basic')
  assert.equal(writes[0].quantity, 2)
  assert.equal(writes[0].request_key, writes[1].request_key)
  assert.ok(
    writes.every(
      (body) =>
        !('price_quota' in body) &&
        !('unit_price_quota' in body) &&
        !('buyer_id' in body)
    )
  )
})
test('a variant-required response stays in the checkout form and displays the known localized hint', async () => {
  owner(2)
  api.get = (async () =>
    result({ version: 'merchant-store-v1', accepted: true })) as typeof api.get
  api.post = (async () => ({
    data: {
      success: false,
      code: 'STORE_VARIANT_REQUIRED',
      message: 'Select a product variant before ordering.',
    },
  })) as typeof api.post
  await mount(<StoreCheckout product={{ ...product, official: true }} />)
  await click(button('Place order'))
  assert.match(
    document.body.textContent || '',
    /Choose a variant before ordering/
  )
  assert.ok(document.querySelector('#store-quantity'))
})
test('variant inventory tabs require confirmation before discarding drafts and import only to the selected target', async () => {
  owner(2)
  const reads: string[] = []
  const writes: Array<{ url: string; body: unknown }> = []
  api.get = (async (url: string) => {
    reads.push(url)
    return result({ items: [], has_more: false })
  }) as typeof api.get
  api.post = (async (url: string, body: unknown) => {
    writes.push({ url, body })
    return result({ added: 1 })
  }) as typeof api.post
  const originalConfirm = window.confirm
  let allow = false
  window.confirm = () => allow
  try {
    await mount(
      <StoreInventoryImport
        product={{
          ...product,
          default_variant_id: 'basic',
          variants: [
            spec('basic', { is_default: true }),
            spec('plus', { enabled: false }),
          ],
        }}
        onClose={() => {}}
        onSaved={async () => {}}
      />
    )
    const text = () =>
      document.querySelector<HTMLTextAreaElement>(
        '[aria-label="Inventory text"]'
      )!
    await input(text(), ' unsaved-basic ')
    const plusTab = [
      ...document.querySelectorAll<HTMLElement>('[role="tab"]'),
    ].find((tab) => tab.textContent?.startsWith('plus'))!
    await click(plusTab)
    assert.equal(text().value, ' unsaved-basic ')
    assert.equal(reads.length, 1)
    allow = true
    await click(plusTab)
    assert.equal(text().value, '')
    assert.equal(
      reads[1],
      `/api/store/products/${product.id}/variants/plus/inventory`
    )
    await input(text(), ' plus-key ')
    await click(button('Import inventory'))
    assert.deepEqual(writes, [
      {
        url: `/api/store/products/${product.id}/variants/plus/inventory`,
        body: { items: [' plus-key '] },
      },
    ])
  } finally {
    window.confirm = originalConfirm
  }
})
test('collection displays the frozen variant name and never queries a current product specification', async () => {
  owner(null)
  api.get = (async (url: string) => {
    assert.equal(url, `/api/user/auth/store-claim/${'y'.repeat(43)}`)
    return result({
      product_title: 'Fixture',
      variant_name: 'Original Plus',
      status: 'paid',
      pickup_login_required: false,
      pickup_code_required: false,
      pickup_login_satisfied: false,
    })
  }) as typeof api.get
  api.post = (async () =>
    result({
      order_id: 'fixture',
      product_title: 'Fixture',
      variant_name: 'Original Plus',
      items: ['fixture-only-secret'],
    })) as typeof api.post
  await mount(<StoreClaimPage token={'y'.repeat(43)} />)
  assert.match(document.body.textContent || '', /Original Plus/)
  await click(button('Collect items'))
  assert.match(document.body.textContent || '', /Original Plus/)
  assert.equal(
    document.querySelector<HTMLTextAreaElement>('#pickup-item-0')?.value,
    'fixture-only-secret'
  )
})
test('multi-line custom inventory remains one item and failed import preserves the composed queue', async () => {
  owner(2)
  let fail = true
  const calls: unknown[] = []
  api.get = (async () =>
    result({ items: [], has_more: false })) as typeof api.get
  api.post = (async (url: string, body: unknown) => {
    assert.equal(url, `/api/store/products/${product.id}/inventory`)
    calls.push(body)
    if (fail) throw new Error('offline')
    return result({ added: 1 })
  }) as typeof api.post
  await mount(
    <StoreInventoryImport
      product={{ ...product, template: 'custom-text' }}
      onClose={() => {}}
      onSaved={async () => {}}
    />
  )
  assert.equal(document.querySelector('input[type=file]'), null)
  const text = document.querySelector<HTMLTextAreaElement>(
    '#store-complete-item'
  )
  assert.ok(text)
  await input(text, 'line one\nline two')
  await click(button('Add one item'))
  assert.match(document.body.textContent || '', /1 items ready to import/)
  await click(button('Import inventory'))
  assert.match(document.body.textContent || '', /1 items ready to import/)
  fail = false
  await click(button('Import inventory'))
  assert.deepEqual(calls, [
    { items: ['line one\nline two'] },
    { items: ['line one\nline two'] },
  ])
})
for (const source of ['textarea', 'file']) {
  test(`inventory wire-size guard rejects JSON expansion and preserves valid Unicode stock (${source})`, async () => {
    owner(2)
    const writes: unknown[] = []
    let saves = 0
    api.get = (async () =>
      result({ items: [], has_more: false })) as typeof api.get
    api.post = (async (url: string, body: unknown) => {
      assert.equal(url, `/api/store/products/${product.id}/inventory`)
      writes.push(body)
      return result({ added: 4 })
    }) as typeof api.post
    await mount(
      <StoreInventoryImport
        product={product}
        onClose={() => {}}
        onSaved={async () => {
          saves++
        }}
      />
    )
    const oversized = Array(10000).fill('A'.repeat(180) + '"'.repeat(20))
    const text = oversized.join('\n')
    assert.ok(new TextEncoder().encode(text).length < 2 * 1024 * 1024)
    assert.ok(
      new TextEncoder().encode(JSON.stringify({ items: oversized })).length >
        2 * 1024 * 1024
    )
    const textarea = document.querySelector<HTMLTextAreaElement>(
      '[aria-label="Inventory text"]'
    )
    assert.ok(textarea)
    if (source === 'file') {
      const file = document.querySelector<HTMLInputElement>('input[type=file]')
      assert.ok(file)
      Object.defineProperty(file, 'files', {
        configurable: true,
        value: [new dom.File([text], 'keys.txt', { type: 'text/plain' })],
      })
      await act(async () => {
        file.dispatchEvent(new Event('change', { bubbles: true }))
        await flush()
      })
    } else {
      await input(textarea, text)
    }
    assert.equal(textarea.value, text)
    assert.equal(button('Import inventory').disabled, true)
    assert.ok(
      document.body.textContent?.includes(
        "Check each item's size or split this inventory import into smaller batches."
      )
    )
    await click(button('Import inventory'))
    assert.equal(writes.length, 0)
    const valid = [' 密钥 "one" ', ' 密钥 "one" ', '\\two\\', '日本語🔑']
    await input(textarea, valid.join('\n'))
    assert.equal(button('Import inventory').disabled, false)
    await click(button('Import inventory'))
    assert.deepEqual(writes, [{ items: valid }])
    assert.equal(saves, 1)
  })
}
for (const template of [
  'card-key',
  'custom-text',
  'account-details',
] as const) {
  test(`inventory close guard preserves drafts until discard and skips confirmation after import (${template})`, async () => {
    owner(2)
    let closes = 0
    let saves = 0
    let confirmations = 0
    let discard = false
    const originalConfirm = window.confirm
    window.confirm = (message) => {
      assert.equal(
        message,
        'Closing will discard the unsaved inventory draft. Continue?'
      )
      confirmations++
      return discard
    }
    let completeImport: (() => void) | undefined
    const importCompletion = new Promise<void>((resolve) => {
      completeImport = resolve
    })
    const writes: Array<{ items: string[] }> = []
    api.get = (async () =>
      result({ items: [], has_more: false })) as typeof api.get
    api.post = (async (_url: string, body: { items: string[] }) => {
      writes.push(body)
      await importCompletion
      return result({ added: body.items.length })
    }) as typeof api.post
    function Harness() {
      const [opened, setOpened] = useState(true)
      return opened ? (
        <StoreInventoryImport
          product={{ ...product, template }}
          onClose={() => {
            closes++
            setOpened(false)
          }}
          onSaved={async () => {
            saves++
            setOpened(false)
          }}
        />
      ) : (
        <button type='button' onClick={() => setOpened(true)}>
          Reopen inventory
        </button>
      )
    }
    const field = (id: string) => {
      const node = document.querySelector<
        HTMLInputElement | HTMLTextAreaElement
      >(id)
      assert.ok(node, `inventory field ${id}`)
      return node
    }
    async function fillDraft(includeUnqueued = false) {
      if (template === 'card-key') {
        await input(field('[aria-label="Inventory text"]'), ' key-A \n key-A ')
      } else if (template === 'custom-text') {
        await input(field('#store-complete-item'), 'queued key\nsecond line')
        await click(button('Add one item'))
        if (includeUnqueued) {
          await input(field('#store-complete-item'), 'unfinished next key')
        }
      } else {
        await input(field('#store-delivery-username'), 'merchant-user')
        await input(field('#store-delivery-password'), ' private password ')
        if (!includeUnqueued) await click(button('Add one item'))
      }
    }
    try {
      await mount(<Harness />)
      await fillDraft(true)
      await click(button('Close'))
      assert.equal(closes, 0)
      assert.equal(confirmations, 1)
      if (template === 'card-key') {
        assert.equal(
          field('[aria-label="Inventory text"]').value,
          ' key-A \n key-A '
        )
      } else if (template === 'custom-text') {
        assert.match(document.body.textContent || '', /1 items ready to import/)
        assert.equal(field('#store-complete-item').value, 'unfinished next key')
      } else {
        assert.equal(field('#store-delivery-username').value, 'merchant-user')
        assert.equal(
          field('#store-delivery-password').value,
          ' private password '
        )
      }
      assert.equal(writes.length, 0)
      discard = true
      await click(button('Close'))
      assert.equal(closes, 1)
      assert.equal(confirmations, 2)
      assert.equal(
        document.querySelector('[aria-label="Inventory text"]'),
        null
      )
      await click(button('Reopen inventory'))
      assert.match(document.body.textContent || '', /0 items ready to import/)
      assert.equal(
        field(
          template === 'card-key'
            ? '[aria-label="Inventory text"]'
            : template === 'custom-text'
              ? '#store-complete-item'
              : '#store-delivery-username'
        ).value,
        ''
      )
      await fillDraft()
      await click(button('Import inventory'))
      await click(button('Close'))
      assert.equal(closes, 1, 'an in-flight import cannot be closed')
      assert.equal(confirmations, 2)
      await act(async () => {
        completeImport?.()
        await flush()
      })
      assert.equal(saves, 1)
      assert.equal(
        confirmations,
        2,
        'successful import closes without discard confirmation'
      )
      assert.ok(button('Reopen inventory'))
      assert.equal(writes.length, 1)
      if (template === 'card-key') {
        assert.deepEqual(writes[0], { items: [' key-A ', ' key-A '] })
      } else if (template === 'custom-text') {
        assert.deepEqual(writes[0], { items: ['queued key\nsecond line'] })
      } else {
        assert.deepEqual(JSON.parse(writes[0].items[0]), {
          lmm_store_delivery: 1,
          template: 'account-details',
          fields: { username: 'merchant-user', password: ' private password ' },
        })
      }
    } finally {
      completeImport?.()
      window.confirm = originalConfirm
    }
  })
}
test('account inventory combines its fields into one item without showing an internal marker', async () => {
  owner(2)
  api.get = (async () =>
    result({ items: [], has_more: false })) as typeof api.get
  let saved: { items: string[] } | undefined
  api.post = (async (_: string, body: { items: string[] }) => {
    saved = body
    return result({ added: 1 })
  }) as typeof api.post
  await mount(
    <StoreInventoryImport
      product={{ ...product, template: 'account-details' }}
      onClose={() => {}}
      onSaved={async () => {}}
    />
  )
  const username = document.querySelector<HTMLInputElement>(
    '#store-delivery-username'
  )
  const password = document.querySelector<HTMLInputElement>(
    '#store-delivery-password'
  )
  assert.ok(username && password)
  await input(username, 'fixture-user')
  await input(password, 'fixture-password')
  assert.equal(password.type, 'password')
  await click(button('Add one item'))
  assert.doesNotMatch(document.body.textContent || '', /lmm_store_delivery/)
  await click(button('Import inventory'))
  assert.ok(saved)
  assert.equal(saved.items.length, 1)
  assert.deepEqual(JSON.parse(saved.items[0]), {
    lmm_store_delivery: 1,
    template: 'account-details',
    fields: { username: 'fixture-user', password: 'fixture-password' },
  })
})
test('structured collection respects the order template and reveals passwords only on request', async () => {
  owner(null)
  const variantName = '商家自定义：标准版 / 93天 · 特别包'
  const copied: string[] = []
  dom.navigator.clipboard.writeText = async (value) => {
    copied.push(value)
  }
  api.get = (async () =>
    result({
      product_title: 'Fixture account',
      variant_name: variantName,
      status: 'paid',
      delivery_template: 'account-details',
      pickup_login_required: false,
      pickup_code_required: false,
      pickup_login_satisfied: false,
    })) as typeof api.get
  const raw = JSON.stringify({
    lmm_store_delivery: 1,
    template: 'account-details',
    fields: {
      username: 'fixture-user',
      password: 'fixture-password',
      url: 'https://example.test/login',
    },
  })
  api.post = (async () =>
    result({
      product_title: 'Fixture account',
      order_id: 'fixture-order',
      variant_name: variantName,
      delivery_template: 'account-details',
      items: [raw],
    })) as typeof api.post
  await mount(<StoreClaimPage token={'x'.repeat(43)} />)
  await click(button('Collect items'))
  const password = document.querySelector<HTMLInputElement>(
    '#pickup-item-0-password'
  )
  assert.ok(password)
  assert.equal(password.type, 'password')
  assert.doesNotMatch(document.body.textContent || '', /lmm_store_delivery/)
  assert.equal(
    document.body.textContent?.split(`Specification: ${variantName}`).length,
    3,
    'the exact frozen merchant specification appears in the heading and structured row'
  )
  await click(document.querySelector<HTMLElement>('[role="checkbox"]')!)
  await click(button('Copy selected items'))
  assert.equal(
    copied.at(-1),
    'Username: fixture-user\nPassword: fixture-password\nLogin URL: https://example.test/login',
    'selection copies the delivered fields rather than the internal JSON envelope'
  )
  await click(button('Show delivered password'))
  assert.equal(password.type, 'text')
  assert.equal(password.value, 'fixture-password')
  await click(button('Hide delivered password'))
  assert.equal(password.type, 'password')
  const link = document.querySelector<HTMLAnchorElement>(
    'a[href="https://example.test/login"]'
  )
  assert.ok(link)
  assert.equal(link.rel, 'noopener noreferrer')
})
for (const frozenTemplate of ['', 'license-key', 'unknown-future-kind']) {
  test(`legacy or mismatched collection (${frozenTemplate || 'legacy'}) keeps the exact raw inventory`, async () => {
    owner(null)
    const raw = JSON.stringify({
      lmm_store_delivery: 1,
      template: 'download-link',
      fields: { url: 'https://example.test/file' },
    })
    api.get = (async () =>
      result({
        product_title: 'Fixture',
        status: 'paid',
        delivery_template: frozenTemplate,
        pickup_login_required: false,
        pickup_code_required: false,
        pickup_login_satisfied: false,
      })) as typeof api.get
    api.post = (async () =>
      result({
        product_title: 'Fixture',
        order_id: 'fixture',
        delivery_template: frozenTemplate,
        items: [raw],
      })) as typeof api.post
    await mount(<StoreClaimPage token={'y'.repeat(43)} />)
    await click(button('Collect items'))
    const item = document.querySelector<HTMLTextAreaElement>('#pickup-item-0')
    assert.ok(item)
    assert.equal(item.value, raw)
    assert.equal(
      document.querySelector('a[href="https://example.test/file"]'),
      null
    )
  })
}
for (const raw of [
  '{"lmm_store_delivery":1e0,"template":"download-link","fields":{"url":"https://example.test/download"}}',
  '{"lmm_store_delivery":1.0,"template":"download-link","fields":{"url":"javascript:alert(1)"}}',
  '{"lmm_store_delivery":1,"template":"download-link","fields":{"url":"https://example.test/download","unknown":"field"}}',
]) {
  test(`collection only exposes a download link for the complete safe delivery schema (${raw.length})`, async () => {
    owner(null)
    api.get = (async () =>
      result({
        product_title: 'Fixture download',
        status: 'paid',
        pickup_login_required: false,
        pickup_code_required: false,
        pickup_login_satisfied: false,
        delivery_template: 'download-link',
      })) as typeof api.get
    api.post = (async () =>
      result({
        order_id: 'fixture',
        product_title: 'Fixture download',
        delivery_template: 'download-link',
        items: [raw],
      })) as typeof api.post
    await mount(<StoreClaimPage token={'z'.repeat(43)} />)
    await click(button('Collect items'))
    const link = document.querySelector<HTMLAnchorElement>(
      'a[href="https://example.test/download"]'
    )
    if (raw.includes('1e0')) {
      assert.ok(link)
      assert.doesNotMatch(document.body.textContent || '', /lmm_store_delivery/)
    } else {
      assert.equal(link, null)
      const contents =
        document.querySelector<HTMLTextAreaElement>('#pickup-item-0')
      assert.ok(contents)
      assert.equal(contents.value, raw)
      assert.equal(document.querySelector('a[href^="javascript:"]'), null)
    }
  })
}
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
  const refundLogin = document.querySelector('a[href^="/sign-in?redirect="]')
  assert.equal(
    refundLogin?.getAttribute('href'),
    `/sign-in?redirect=${encodeURIComponent(window.location.pathname)}`
  )
  assert.equal(
    document.querySelectorAll('a[href^="/sign-in?redirect="]').length,
    1
  )
  assert.equal(
    document.body.textContent?.includes(
      'Sign in with the purchasing account to view or request refunds.'
    ),
    true
  )
  assert.equal(button('Collect items').hasAttribute('disabled'), false)
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

function pendingPaymentOrder(): StoreOrder {
  return {
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
}

for (const preparation of [
  'pending',
  'lost-response',
  'refresh-fails',
  'detail-refresh-fails',
  'cancel-conflict',
]) {
  test(`order preparation refreshes cancellation authority without a new payment (${preparation})`, async () => {
    owner(2)
    const pending = { ...pendingPaymentOrder(), payment_issued: false }
    let issued = false
    let refreshFails = false
    let paymentAttempts = 0
    let cancellationAttempts = 0
    dom.happyDOM.setURL(
      preparation === 'detail-refresh-fails'
        ? `https://shop.example.test/store/orders?order=${pending.id}`
        : 'https://shop.example.test/store/orders'
    )
    api.get = (async (url: string) => {
      if (url === '/api/store/my/orders') {
        if (refreshFails && preparation !== 'detail-refresh-fails') {
          throw new Error('Order refresh unavailable')
        }
        return result({
          items: [{ ...pending, payment_issued: issued }],
          has_more: false,
        })
      }
      if (url === '/api/user/self') return result({ id: 2, quota: 5000000 })
      if (url === `/api/store/orders/${pending.id}`) {
        if (refreshFails) throw new Error('Order refresh unavailable')
        return result({ ...pending, payment_issued: issued })
      }
      assert.fail(`Unexpected GET ${url}`)
    }) as typeof api.get
    api.post = (async (url: string) => {
      if (url.endsWith('/pay')) {
        paymentAttempts++
        issued = true
        if (preparation === 'lost-response') {
          throw new Error('Payment preparation response lost')
        }
        refreshFails = preparation.endsWith('refresh-fails')
        return result({
          order_id: pending.id,
          method: 'GET',
          payment_url: 'https://gateway.example.test/pay',
          currency: 'CNY',
          amount: '7.00',
          amount_minor: 700,
          status: 'pending',
        })
      }
      if (url.endsWith('/reconcile')) return result(null)
      if (url.endsWith('/cancel')) {
        cancellationAttempts++
        issued = true
        throw {
          response: {
            data: {
              success: false,
              code: 'STORE_CONFLICT',
              message: 'The order state changed. Refresh and try again.',
            },
          },
        }
      }
      assert.fail(`Unexpected POST ${url}`)
    }) as typeof api.post
    await createStoreCheckoutIntentJournal({
      isActorCurrent: (actor) =>
        actor.kind === 'account' && actor.accountId === 2,
    }).prepare(
      { kind: 'account', accountId: 2 },
      { productId: product.id, quantity: 1 },
      {
        product_id: product.id,
        quantity: 1,
        payment_method: pending.payment_method,
      }
    )
    const journal = localStorage.getItem('lmm:store:checkout-intents')
    assert.ok(journal)
    await mount(<StoreOrdersPage />)
    assert.ok(button('Cancel order'))
    await click(
      button(
        preparation === 'cancel-conflict' ? 'Cancel order' : 'Prepare payment'
      )
    )
    assert.equal(
      [...document.querySelectorAll('button')].some(
        (node) => node.textContent?.trim() === 'Cancel order'
      ),
      false
    )
    assert.equal(paymentAttempts, preparation === 'cancel-conflict' ? 0 : 1)
    assert.equal(
      cancellationAttempts,
      preparation === 'cancel-conflict' ? 1 : 0
    )
    assert.equal(localStorage.getItem('lmm:store:checkout-intents'), journal)
    if (preparation === 'cancel-conflict') {
      assert.ok(document.body.textContent?.includes('order state changed'))
    } else if (preparation === 'lost-response') {
      assert.ok(document.body.textContent?.includes('Store request failed'))
    } else {
      assert.ok(button('Continue to payment'))
    }
    if (preparation.endsWith('refresh-fails')) {
      assert.ok(document.body.textContent?.includes('Store request failed'))
      refreshFails = false
      await click(button('Check payment status'))
      assert.equal(paymentAttempts, 1)
      assert.equal(cancellationAttempts, 0)
      assert.equal(localStorage.getItem('lmm:store:checkout-intents'), journal)
    }
    dom.happyDOM.setURL(
      'https://shop.example.test/store/products/product-fixture'
    )
  })
}

test('an updated paid order clears a previously prepared payment session', async () => {
  owner(2)
  const pending = pendingPaymentOrder()
  api.get = (async (url: string) => {
    assert.equal(url, '/api/user/self')
    return result({ id: 2, quota: 5000000 })
  }) as typeof api.get
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

for (const matching of [true, false]) {
  test(`order history refreshes only a matching confirmed minimum cancellation (${matching})`, async () => {
    owner(2)
    const pending = pendingPaymentOrder()
    let status: StoreOrder['status'] = 'pending'
    let reads = 0
    api.get = (async (url: string) => {
      if (url === '/api/store/my/orders') {
        reads++
        return result({ items: [{ ...pending, status }], has_more: false })
      }
      return result(null)
    }) as typeof api.get
    api.post = (async () => {
      if (matching) status = 'cancelled'
      throw {
        response: {
          data: {
            success: false,
            code: 'STORE_PAYMENT_MINIMUM',
            message: 'Payment amount is below the gateway minimum.',
            order_id: matching ? pending.id : 'another-order',
            order_status: 'cancelled',
            order_cancelled: true,
          },
        },
      }
    }) as typeof api.post
    await mount(<StoreOrdersPage />)
    const initialReads = reads
    await click(button('Prepare payment'))
    assert.equal(reads > initialReads, matching)
    const hasPaymentButton = [...document.querySelectorAll('button')].some(
      (node) => node.textContent?.trim() === 'Prepare payment'
    )
    assert.equal(hasPaymentButton, !matching)
    assert.ok(document.body.textContent?.includes('Payment amount is below'))
  })
}

test('order history reads the server balance after a confirmed balance payment', async () => {
  owner(2)
  const pending = {
    ...pendingPaymentOrder(),
    payment_method: 'balance',
    payment_issued: false,
  }
  let paid = false
  let accountReads = 0
  api.get = (async (url: string) => {
    if (url === '/api/user/self') {
      accountReads++
      return result({ id: 2, role: 1, username: 'buyer-2', quota: 1234567 })
    }
    if (url === '/api/store/my/orders') {
      return result({
        items: [{ ...pending, status: paid ? 'paid' : 'pending' }],
        has_more: false,
      })
    }
    return result([])
  }) as typeof api.get
  api.post = (async () => {
    paid = true
    return result({ order_id: pending.id, status: 'paid' })
  }) as typeof api.post
  await mount(<StoreOrdersPage />)
  assert.equal(accountReads, 0)
  await click(button('Pay with balance'))
  assert.equal(accountReads, 1)
  assert.equal(useAuthStore.getState().auth.user?.quota, 1234567)
  assert.ok(button('Get pickup link'))
})

for (const { currency, quota, expected } of [
  { currency: 'CNY', quota: 68493, expected: '1 CNY' },
  { currency: 'USD', quota: 1, expected: '0.000002 USD' },
  { currency: 'CREDIT', quota: 68493, expected: '68,493 Credits' },
]) {
  test(`store price display remains usable for ${currency}`, async () => {
    owner(2)
    const current = useAuthStore.getState().auth.user
    assert.ok(current)
    useAuthStore.getState().auth.setUser({
      ...current,
      setting: JSON.stringify({ wallet_display_currency: currency }),
    })
    useSystemConfigStore.getState().setConfig({
      currency: {
        ...originalConfig.currency,
        currencyUnit: 'credit',
        quotaPerUnit: 500000,
        creditsPerUsd: 500000,
        creditsPerUsdExact: '500000',
        cnyPerUsd: 7.3,
        cnyPerUsdExact: '7.3',
      },
    })
    await mount(<StoreAmount quota={quota} />)
    assert.equal(document.body.textContent, expected)
  })
}

test('ordinary owners can place a real order for their own published product', async () => {
  owner(product.seller_id)
  api.get = (async () => result({ accepted: true })) as typeof api.get
  const calls: unknown[] = []
  api.post = (async (url: string, body: unknown) => {
    assert.equal(url, '/api/store/orders')
    calls.push(body)
    return result({
      order: { id: 'self-purchase', status: 'pending', trade_no: 'MS-self' },
      created: true,
    })
  }) as typeof api.post
  await mount(<StoreCheckout product={{ ...product, official: true }} />)
  assert.equal(button('Place order').disabled, false)
  await click(button('Place order'))
  assert.equal(calls.length, 1)
  assert.equal((calls[0] as Record<string, unknown>).product_id, product.id)
  assert.match(document.body.textContent || '', /MS-self/)
})
for (const preview of [false, true]) {
  test(`private draft checkout is available to its owner with or without preview (${preview})`, async () => {
    owner(product.seller_id)
    api.get = (async () => result({ accepted: true })) as typeof api.get
    await mount(
      <StoreCheckout
        product={{
          ...product,
          test_mode: true,
          status: 'draft',
          official: true,
        }}
        ownerPreview={preview}
      />
    )
    assert.equal(button('Place order').disabled, false)
  })
}
test('non-owners cannot use a test-mode checkout even with a preview flag', async () => {
  owner(2)
  api.get = (async () => result({ accepted: true })) as typeof api.get
  await mount(
    <StoreCheckout
      product={{ ...product, test_mode: true, official: true }}
      ownerPreview
    />
  )
  assert.equal(button('Place order').disabled, true)
})
test('owner preview fetches only the private endpoint and never falls back to public data', async () => {
  owner(product.seller_id)
  const reads: string[] = []
  api.get = (async (url: string) => {
    reads.push(url)
    if (url.endsWith('/preview')) {
      return result({
        ...product,
        test_mode: true,
        status: 'draft',
        official: true,
      })
    }
    assert.equal(url, '/api/store/disclaimer')
    return result({ accepted: true })
  }) as typeof api.get
  await mount(<StoreProductPage id={product.id} ownerPreview />)
  assert.ok(reads.includes(`/api/store/my/products/${product.id}/preview`))
  assert.ok(!reads.includes(`/api/store/products/${product.id}`))
  assert.match(
    document.body.textContent || '',
    /only visible to the product owner/
  )
  assert.equal(button('Place order').disabled, false)
})
for (const supported of [false, true]) {
  test(`test-mode control requires an actual backend capability and explicitly saves its flag (${supported})`, async () => {
    owner(product.seller_id)
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
    let saved: Record<string, unknown> | undefined
    api.put = (async (_url: string, body: Record<string, unknown>) => {
      saved = body
      return result(product)
    }) as typeof api.put
    await mount(
      <StoreProductEditor
        product={product}
        allowedMethods={['balance']}
        minimumPriceQuota={0}
        testModeSupported={supported}
        onClose={() => {}}
        onSaved={async () => {}}
      />
    )
    assert.equal(
      document.body.textContent?.includes('Product test mode'),
      supported
    )
    if (supported) {
      const switches = [
        ...document.querySelectorAll<HTMLElement>('[role="switch"]'),
      ]
      const toggle = switches
        .at(-1)
        ?.closest('label')
        ?.querySelector<HTMLInputElement>('input[type=checkbox]')
      assert.ok(toggle)
      await click(toggle)
    }
    await click(button('Save draft'))
    assert.ok(saved)
    assert.equal('test_mode' in saved, supported)
    if (supported) assert.equal(saved.test_mode, true)
  })
}

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
