/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

import type { StoreCatalogueProduct, StoreCartItem } from './catalogue-types'

const dom = new Window({ url: 'https://shop.example.test/store/cart' })
dom.document.write('<!doctype html><html><body></body></html>')
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
const { StoreCollectionActions } = await import('./collection-actions')
const { StoreProductSocialActions } = await import('./product-social-actions')
const { rememberStoreSocialIntent } = await import('./social-intent')
const { StoreCartPage } = await import('./cart-page')
const { StoreFavoritesPage } = await import('./favorites-page')
const { StorePage } = await import('./store-page')
const { SellerCatalogueEditor } = await import('./seller-catalogue-editor')
const {
  guestCartClear,
  guestCartUpsert,
  readGuestStoreCart,
  GUEST_STORE_CART_KEY,
  STORE_CATALOGUE_VIEW_KEY,
} = await import('./collection-storage')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const original = { get: api.get, put: api.put, delete: api.delete }
let root: ReturnType<typeof createRoot> | undefined
let client: InstanceType<typeof QueryClient> | undefined
const product: StoreCatalogueProduct = {
  id: 'product-fixture',
  seller_id: 9,
  title: 'Visible product fixture',
  description: '',
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
  available_stock: 99,
  sale_available: 5,
  promotion_expires_at: 0,
  created_at: 1,
  updated_at: 1,
  review_note: '',
  visibility: 'public',
  purchase_login_required: true,
  catalogue: {
    custom_tags: ['in_stock', '工具'],
    auto_delivery: true,
    ai_processing: false,
  },
  display_tags: ['in_stock', 'auto_delivery'],
  net_paid_quantity: 4,
  default_variant_id: 'sku-fixture',
  variants: [
    {
      id: 'sku-fixture',
      product_id: 'product-fixture',
      name: 'Actual SKU fixture',
      price_quota: 500000,
      template: 'card-key',
      enabled: true,
      is_default: true,
      inventory_total: 99,
      inventory_available: 99,
      reserved_stock: 0,
      sale_available: 5,
      trading_paused: false,
      created_at: 1,
      updated_at: 1,
    },
  ],
}
const result = (data: unknown) => ({ data: { success: true, data } })
const page = (items: unknown[]) =>
  result({ items, offset: 0, limit: 100, has_more: false })
type Request = {
  method: 'GET' | 'PUT' | 'DELETE'
  url: string
  body?: unknown
  config?: unknown
}
function mockRequests(
  reply?: (
    request: Request
  ) => ReturnType<typeof result> | Promise<ReturnType<typeof result>>,
  supported = true,
  likesSupported = false
) {
  const requests: Request[] = []
  const respond = (request: Request) => {
    requests.push(request)
    if (request.url === '/api/store/config') {
      return result({
        store_catalogue_supported: supported,
        store_collections_supported: supported,
        store_likes_supported: likesSupported,
      })
    }
    if (reply) return reply(request)
    if (request.url === '/api/store/products') return page([product])
    if (request.url === `/api/store/products/${product.id}`) {
      return result(product)
    }
    if (request.method === 'GET') return page([])
    return result(null)
  }
  api.get = (async (url: string, config: unknown) =>
    respond({ method: 'GET', url, config })) as typeof api.get
  api.put = (async (url: string, body: unknown, config: unknown) =>
    respond({ method: 'PUT', url, body, config })) as typeof api.put
  api.delete = (async (url: string, config: unknown) =>
    respond({ method: 'DELETE', url, config })) as typeof api.delete
  return requests
}
function owner(id: number | null) {
  useAuthStore
    .getState()
    .auth.setUser(
      id ? { id, role: 1, status: 1, username: `buyer-${id}` } : null
    )
}
const flush = () => new Promise((resolve) => setTimeout(resolve, 30))
function required<T>(value: T | null | undefined): T {
  assert.ok(value !== null && value !== undefined)
  return value
}
function lastProductRequest(requests: Request[]) {
  return required(
    [...requests]
      .reverse()
      .find((request) => request.url === '/api/store/products')
  )
}
async function mount(node: React.ReactNode) {
  const host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  await act(async () => {
    required(root).render(
      <QueryClientProvider client={required(client)}>
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
  await act(flush)
}
async function waitFor(predicate: () => boolean) {
  for (let i = 0; i < 15 && !predicate(); i++) await act(flush)
  assert.ok(predicate(), document.body.textContent ?? '')
}
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}
afterEach(async () => {
  await act(async () => root?.unmount())
  root = undefined
  client?.clear()
  client = undefined
  Object.assign(api, original)
  owner(null)
  guestCartClear()
  dom.localStorage.clear()
  document.body.replaceChildren()
})
after(() => dom.happyDOM.abort())

test('browse uses server sorting and independent filters, renders authoritative tags and persists only view preference', async () => {
  const requests = mockRequests()
  await mount(<StorePage />)
  assert.match(document.body.textContent ?? '', /Automatic delivery/)
  assert.match(document.body.textContent ?? '', /in_stock/)
  assert.doesNotMatch(document.body.textContent ?? '', /Stock: 99/)
  const select = required(
    document.querySelector<HTMLSelectElement>('#store-catalogue-sort')
  )
  await act(async () => {
    select.value = 'sales'
    select.dispatchEvent(new Event('change', { bubbles: true }))
    await flush()
  })
  assert.equal(
    (lastProductRequest(requests).config as { params: { sort: string } }).params
      .sort,
    'sales'
  )
  const guest = required(
    document.querySelector<HTMLSelectElement>('#store-catalogue-guestPurchase')
  )
  await act(async () => {
    guest.value = 'false'
    guest.dispatchEvent(new Event('change', { bubbles: true }))
    await flush()
  })
  assert.equal(
    (
      lastProductRequest(requests).config as {
        params: { guest_purchase: boolean }
      }
    ).params.guest_purchase,
    false
  )
  await click(button('List'))
  assert.equal(dom.localStorage.getItem(STORE_CATALOGUE_VIEW_KEY), 'list')
  assert.equal(dom.localStorage.getItem(GUEST_STORE_CART_KEY), null)
})

test('anonymous selected SKU and quantity persist as references only and refreshed checkout retains them through sign in', async () => {
  let current = product
  const requests = mockRequests((request) =>
    request.method === 'GET' &&
    request.url === `/api/store/products/${product.id}`
      ? result(current)
      : page([])
  )
  await mount(
    <StoreCollectionActions
      product={product}
      variantId='sku-fixture'
      quantity={3}
    />
  )
  await click(button('Add to cart'))
  assert.deepEqual(readGuestStoreCart(), [
    { product_id: product.id, variant_id: 'sku-fixture', quantity: 3 },
  ])
  assert.doesNotMatch(
    dom.localStorage.getItem(GUEST_STORE_CART_KEY) ?? '',
    /title|price|token|seller|visibility/
  )
  await act(async () => required(root).unmount())
  root = undefined
  required(client).clear()
  document.body.replaceChildren()
  current = {
    ...product,
    variants: [
      { ...required(product.variants?.[0]), price_quota: 750000 },
      {
        ...required(product.variants?.[0]),
        id: 'sku-second',
        name: 'Second SKU fixture',
        price_quota: 1000000,
      },
    ],
  }
  guestCartUpsert({
    product_id: product.id,
    variant_id: 'sku-second',
    quantity: 1,
  })
  const productReads = () =>
    requests.filter(
      (request) => request.url === `/api/store/products/${product.id}`
    ).length
  const beforeCart = productReads()
  await mount(<StoreCartPage />)
  await waitFor(() => !button('Review item for checkout').disabled)
  assert.match(document.body.textContent ?? '', /Second SKU fixture/)
  assert.equal(
    productReads() - beforeCart,
    1,
    'two SKUs share one fresh product query'
  )
  await click(button('Review item for checkout'))
  await waitFor(
    () =>
      document.body.textContent?.includes('Sign in and continue checkout') ===
      true
  )
  const link = [...document.querySelectorAll<HTMLAnchorElement>('a')].find(
    (node) => node.textContent?.trim() === 'Sign in and continue checkout'
  )
  assert.ok(link)
  const redirect = required(new URL(link.href).searchParams.get('redirect'))
  const target = new URL(redirect, 'https://shop.example.test')
  assert.equal(target.pathname, `/store/products/${product.id}`)
  assert.equal(target.searchParams.get('variant_id'), 'sku-fixture')
  assert.equal(target.searchParams.get('quantity'), '3')
  assert.equal(readGuestStoreCart()[0].quantity, 3)
  current = {
    ...current,
    variants: required(current.variants).map((variant, index) =>
      index === 0 ? { ...variant, price_quota: 1000000 } : variant
    ),
  }
  await click(button('Refresh cart'))
  await waitFor(
    () =>
      document.body.textContent?.includes('Sign in and continue checkout') ===
      false
  )
})

test('account switch during product refresh aborts the old add action before it can write to the new cart', async () => {
  owner(27)
  const pending = deferred<ReturnType<typeof result>>()
  const requests = mockRequests((request) =>
    request.url === `/api/store/products/${product.id}`
      ? pending.promise
      : page([])
  )
  await mount(
    <StoreCollectionActions
      product={product}
      variantId='sku-fixture'
      quantity={2}
    />
  )
  await click(button('Add to cart'))
  await act(async () => {
    owner(28)
    await flush()
  })
  await act(flush)
  await act(async () => {
    pending.resolve(result(product))
    await flush()
  })
  assert.equal(requests.filter((request) => request.method === 'PUT').length, 0)
  assert.doesNotMatch(document.body.textContent ?? '', /Added to cart/)
})

test('favorites save through the real endpoint and switching accounts removes the prior private title and cache', async () => {
  owner(27)
  let saved = false
  const privateProduct = {
    ...product,
    visibility: 'private' as const,
    title: 'Private title for account 27',
  }
  const requests = mockRequests((request) => {
    if (request.method === 'PUT') {
      saved = true
      return result({ product_id: product.id, created_at: 1 })
    }
    return page(
      saved && useAuthStore.getState().auth.user?.id === 27
        ? [
            {
              product_id: product.id,
              created_at: 1,
              valid: true,
              unavailable_reason: null,
              product: privateProduct,
            },
          ]
        : []
    )
  })
  await mount(
    <>
      <StoreCollectionActions product={privateProduct} />
      <StoreFavoritesPage />
    </>
  )
  await waitFor(() => !button('Save to favorites').disabled)
  await click(button('Save to favorites'))
  assert.deepEqual(requests.find((request) => request.method === 'PUT')?.body, {
    product_id: product.id,
  })
  assert.match(document.body.textContent ?? '', /Private title for account 27/)
  await act(async () => {
    owner(28)
    await flush()
  })
  await waitFor(
    () =>
      document.body.textContent?.includes(
        'You have no favorite products yet.'
      ) === true
  )
  assert.doesNotMatch(
    document.body.textContent ?? '',
    /Private title for account 27/
  )
  assert.equal(
    required(client).getQueryData(['store', 'favorites', 'account:27']),
    undefined
  )
})

test('paused favorites retain their title and removal without a broken detail link; sold-out published products retain their link', async () => {
  owner(27)
  const paused = {
    ...product,
    status: 'paused' as const,
    title: 'Paused retained product',
  }
  const soldOut = {
    ...product,
    id: 'sold-out-product',
    title: 'Sold-out published product',
    sale_available: 0,
    available_stock: 0,
  }
  let rows = [paused, soldOut].map((item) => ({
    product_id: item.id,
    created_at: 1,
    valid: true,
    unavailable_reason: null,
    product: item,
  }))
  const requests = mockRequests((request) => {
    if (request.method === 'DELETE') {
      assert.equal(request.url, `/api/store/favorites/${paused.id}`)
      rows = rows.filter((item) => item.product_id !== paused.id)
      return result(null)
    }
    assert.equal(request.url, '/api/store/favorites')
    return page(rows)
  })
  await mount(<StoreFavoritesPage />)
  await waitFor(
    () => document.body.textContent?.includes(paused.title) === true
  )
  const pausedCard = [...document.querySelectorAll('article')].find(
    (node) => node.querySelector('h2')?.textContent === paused.title
  )
  assert.ok(pausedCard)
  assert.equal(pausedCard.querySelector('h2 a'), null)
  assert.match(pausedCard.textContent || '', /paused/)
  assert.equal(
    document.querySelector<HTMLAnchorElement>(
      `a[href="/store/products/${soldOut.id}"]`
    )?.textContent,
    soldOut.title
  )
  await click(button('Remove from favorites'))
  assert.equal(
    requests.filter((request) => request.method === 'DELETE').length,
    1
  )
  await waitFor(() => !document.body.textContent?.includes(paused.title))
  assert.equal(
    document.querySelector<HTMLAnchorElement>(
      `a[href="/store/products/${soldOut.id}"]`
    )?.textContent,
    soldOut.title
  )
})

test('fresh cart projection preserves SKU rows, invalid stock and account clear uses the server', async () => {
  owner(27)
  let rows: StoreCartItem[] = [
    {
      id: 'cart-fixture',
      product_id: product.id,
      variant_id: 'sku-fixture',
      quantity: 3,
      created_at: 1,
      updated_at: 1,
      product: {
        ...product,
        variants: [{ ...required(product.variants?.[0]), sale_available: 1 }],
      },
      valid: false,
      unavailable_reason: 'insufficient_stock',
    },
  ]
  const requests = mockRequests((request) => {
    if (request.method === 'PUT' && request.url === '/api/store/cart') {
      const ref = request.body as { quantity: number }
      rows = rows.map((item) => ({
        ...item,
        quantity: ref.quantity,
        valid: ref.quantity <= 1,
        unavailable_reason: ref.quantity <= 1 ? null : 'insufficient_stock',
      }))
      return result(rows[0])
    }
    if (request.method === 'DELETE') {
      rows = []
      return result(null)
    }
    return page(request.url === '/api/store/cart' ? rows : [])
  })
  await mount(<StoreCartPage />)
  await waitFor(
    () => document.body.textContent?.includes('Actual SKU fixture') === true
  )
  assert.match(document.body.textContent ?? '', /Actual SKU fixture/)
  assert.equal(button('Review item for checkout').disabled, true)
  assert.match(document.body.textContent ?? '', /exceeds the current stock/)
  const decrease = () =>
    required(
      document.querySelector<HTMLButtonElement>(
        'button[aria-label="Decrease cart quantity"]'
      )
    )
  await click(decrease())
  await waitFor(
    () =>
      document.querySelector<HTMLInputElement>(
        'input[aria-label="Cart quantity"]'
      )?.value === '2'
  )
  assert.deepEqual(requests.find((request) => request.method === 'PUT')?.body, {
    product_id: product.id,
    variant_id: 'sku-fixture',
    quantity: 2,
  })
  await click(decrease())
  await waitFor(() => !button('Review item for checkout').disabled)
  assert.equal(
    document.querySelector<HTMLButtonElement>(
      'button[aria-label="Increase cart quantity"]'
    )?.disabled,
    true
  )
  await click(button('Clear cart'))
  assert.ok(
    requests.some(
      (request) =>
        request.method === 'DELETE' && request.url === '/api/store/cart'
    )
  )
  assert.match(document.body.textContent ?? '', /Your cart is empty/)
})

test('metadata editor writes the catalogue endpoint independently and does not modify access or price', async () => {
  owner(9)
  const requests = mockRequests()
  await mount(<SellerCatalogueEditor product={product} />)
  await click(button('Save catalogue tags'))
  assert.deepEqual(
    requests.find((request) => request.method === 'PUT'),
    {
      method: 'PUT',
      url: `/api/store/products/${product.id}/catalogue`,
      body: {
        custom_tags: ['in_stock', '工具'],
        auto_delivery: true,
        ai_processing: false,
      },
      config: { skipErrorHandler: true, skipBusinessError: true },
    }
  )
})

test('unsupported backend keeps anonymous references and offers no persistent writes', async () => {
  const requests = mockRequests(undefined, false)
  guestCartUpsert({
    product_id: product.id,
    variant_id: 'sku-fixture',
    quantity: 2,
  })
  await mount(<StoreCartPage />)
  assert.match(document.body.textContent ?? '', /not supported/)
  assert.equal(button('Clear cart').disabled, true)
  assert.equal(readGuestStoreCart()[0].quantity, 2)
  assert.equal(requests.filter((request) => request.method !== 'GET').length, 0)
})

test('guest social intent restores only the matching real favorite after login, once', async () => {
  owner(null)
  let saved = false
  const requests = mockRequests((request) => {
    if (request.method === 'PUT' && request.url === '/api/store/favorites') {
      saved = true
    }
    return request.method === 'GET'
      ? page(saved ? [{ product_id: product.id, valid: true, product }] : [])
      : result(null)
  })
  await mount(<StoreProductSocialActions product={product} />)
  await waitFor(() => !!document.querySelector('a[href^="/sign-in"]'))
  const link = required(
    [...document.querySelectorAll<HTMLAnchorElement>('a')].find((node) =>
      node.textContent?.includes('Sign in to save favorites')
    )
  )
  assert.equal(
    link.getAttribute('href'),
    '/sign-in?redirect=%2Fstore%2Fproducts%2Fproduct-fixture'
  )
  link.addEventListener('click', (event) => event.preventDefault())
  await click(link)
  assert.equal(requests.filter((request) => request.method === 'PUT').length, 0)
  await act(async () => {
    owner(27)
    await flush()
  })
  await waitFor(
    () => document.body.textContent?.includes('Remove from favorites') === true
  )
  assert.equal(requests.filter((request) => request.method === 'PUT').length, 1)
  assert.deepEqual(requests.find((request) => request.method === 'PUT')?.body, {
    product_id: product.id,
  })
  await act(async () => {
    required(client).invalidateQueries({
      queryKey: ['store', 'favorites', 'account:27'],
    })
    await flush()
  })
  assert.equal(requests.filter((request) => request.method === 'PUT').length, 1)
})

test('post-login social intent cannot save another product', async () => {
  owner(27)
  rememberStoreSocialIntent('another-product', 'favorite')
  const requests = mockRequests()
  await mount(<StoreProductSocialActions product={product} />)
  await waitFor(() => !button('Save to favorites').disabled)
  assert.equal(requests.filter((request) => request.method === 'PUT').length, 0)
  assert.ok(button('Share link'))
})

test('likes use absolute authenticated endpoints and render only confirmed server state, then clear on account switch', async () => {
  owner(27)
  let liked = false
  const requests = mockRequests(
    (request) => {
      if (request.url.endsWith('/likes')) {
        if (request.method === 'PUT') liked = true
        if (request.method === 'DELETE') liked = false
        return result({
          supported: true,
          count: liked ? 14 : 13,
          liked: useAuthStore.getState().auth.user?.id === 27 && liked,
        })
      }
      return page([])
    },
    true,
    true
  )
  await mount(<StoreProductSocialActions product={product} />)
  const likeButton = () =>
    document.querySelector<HTMLButtonElement>(
      'button[aria-label="Like this product"]'
    )
  await waitFor(() => !!likeButton())
  assert.match(required(likeButton()).textContent ?? '', /13/)
  await click(required(likeButton()))
  const unlike = required(
    document.querySelector<HTMLButtonElement>(
      'button[aria-label="Unlike this product"]'
    )
  )
  assert.equal(unlike.getAttribute('aria-pressed'), 'true')
  assert.match(unlike.textContent ?? '', /14/)
  const write = requests.find((request) => request.method === 'PUT')
  assert.equal(write?.url, `/api/store/products/${product.id}/likes`)
  assert.deepEqual(write?.body, {})
  await click(unlike)
  assert.equal(
    requests.filter((request) => request.method === 'DELETE').length,
    1
  )
  assert.equal(required(likeButton()).getAttribute('aria-pressed'), 'false')
  await act(async () => {
    owner(28)
    await flush()
  })
  assert.equal(
    required(client).getQueryData(['store', 'likes', 'account:27', product.id]),
    undefined
  )
})

test('unsupported likes do not display a false zero count or send a fake mutation', async () => {
  owner(27)
  const requests = mockRequests(undefined, false)
  await mount(<StoreProductSocialActions product={product} />)
  assert.ok(button('Share link'))
  assert.doesNotMatch(
    document.body.textContent ?? '',
    /Like this product|Unlike this product/
  )
  assert.equal(
    requests.filter(
      (request) => request.url.endsWith('/likes') || request.method === 'PUT'
    ).length,
    0
  )
})

test('guest login restores one explicit like using the real endpoint', async () => {
  owner(null)
  let liked = false
  const requests = mockRequests(
    (request) => {
      if (request.url.endsWith('/likes')) {
        if (request.method === 'PUT') liked = true
        return result({ supported: true, count: liked ? 1 : 0, liked })
      }
      return page([])
    },
    true,
    true
  )
  await mount(<StoreProductSocialActions product={product} />)
  await waitFor(() =>
    [...document.querySelectorAll('a')].some((node) =>
      node.textContent?.includes('Sign in to like this product')
    )
  )
  const link = required(
    [...document.querySelectorAll<HTMLAnchorElement>('a')].find((node) =>
      node.textContent?.includes('Sign in to like this product')
    )
  )
  link.addEventListener('click', (event) => event.preventDefault())
  await click(link)
  assert.equal(requests.filter((request) => request.method === 'PUT').length, 0)
  await act(async () => {
    owner(27)
    await flush()
  })
  await waitFor(
    () => !!document.querySelector('button[aria-label="Unlike this product"]')
  )
  assert.equal(requests.filter((request) => request.method === 'PUT').length, 1)
})

test('a failed favorite restore stays unsaved and can be retried explicitly without an automatic loop', async () => {
  owner(27)
  rememberStoreSocialIntent(product.id, 'favorite')
  let writes = 0
  mockRequests((request) => {
    if (request.method === 'PUT') {
      writes++
      throw new Error('network unavailable')
    }
    return page([])
  })
  await mount(<StoreProductSocialActions product={product} />)
  await waitFor(() => writes === 1 && !button('Save to favorites').disabled)
  assert.equal(
    button('Save to favorites').getAttribute('aria-pressed'),
    'false'
  )
  await act(flush)
  assert.equal(writes, 1)
  await click(button('Save to favorites'))
  assert.equal(writes, 2)
  assert.doesNotMatch(document.body.textContent ?? '', /Remove from favorites/)
})

test('an account switch during a pending like cannot repopulate old account cache or expose its result', async () => {
  owner(27)
  const pending = deferred<ReturnType<typeof result>>()
  const requests = mockRequests(
    (request) => {
      if (request.url.endsWith('/likes')) {
        if (request.method === 'PUT') return pending.promise
        return result({ supported: true, count: 0, liked: false })
      }
      return page([])
    },
    true,
    true
  )
  await mount(<StoreProductSocialActions product={product} />)
  const likeButton = () =>
    document.querySelector<HTMLButtonElement>(
      'button[aria-label="Like this product"]'
    )
  await waitFor(() => !!likeButton())
  await act(async () => {
    required(likeButton()).click()
    await flush()
  })
  await waitFor(() => requests.some((request) => request.method === 'PUT'))
  await act(async () => {
    owner(28)
    await flush()
    pending.resolve(result({ supported: true, count: 99, liked: true }))
    await flush()
  })
  assert.equal(
    required(client).getQueryData(['store', 'likes', 'account:27', product.id]),
    undefined
  )
  assert.equal(required(likeButton()).getAttribute('aria-pressed'), 'false')
  assert.doesNotMatch(required(likeButton()).textContent ?? '', /99/)
})
