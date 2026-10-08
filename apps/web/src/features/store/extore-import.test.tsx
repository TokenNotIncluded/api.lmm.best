/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

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

const { act, StrictMode } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { StoreExtoreImport } = await import('./extore-import')
const { StoreProductEditor } = await import('./seller-page')
const { storeApi } = await import('./api')
const { parseExtoreCatalog, extoreProductDraft } =
  await import('./extore-import-protocol')
const { useAuthStore } = await import('@/stores/auth-store')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
let root: ReturnType<typeof createRoot> | undefined
let client: InstanceType<typeof QueryClient> | undefined
const originalCatalog = storeApi.extoreCatalog
const flush = () => new Promise((resolve) => setTimeout(resolve, 30))
async function mount(node: React.ReactNode) {
  const host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const currentRoot = root,
    currentClient = client
  await act(async () => {
    currentRoot.render(
      <QueryClientProvider client={currentClient}>
        <I18nextProvider i18n={i18n}>{node}</I18nextProvider>
      </QueryClientProvider>
    )
    await flush()
  })
  await act(flush)
}
afterEach(async () => {
  await act(async () => root?.unmount())
  root = undefined
  client?.clear()
  client = undefined
  storeApi.extoreCatalog = originalCatalog
  useAuthStore.getState().auth.setUser(null)
  localStorage.clear()
  document.body.replaceChildren()
})
after(() => dom.happyDOM.abort())
const catalog = {
  schema: 'extore.commerce-catalog.v1',
  issuer: 'https://extore.lmm.best',
  shop: { id: 'shop', name: 'Test shop' },
  grant_id: 'test-grant',
  products: [
    {
      schema: 'extore.product-listing.v1',
      id: 'notes',
      shop_id: 'shop',
      revision: 'a'.repeat(64),
      redemption_url: 'https://extore.lmm.best/',
      semantics: {
        price: 'reference',
        inventory: 'not_exported',
        payment: 'external_sales_platform',
        redemption: 'extore',
      },
      product: {
        name: 'Research notes',
        description: 'Prepare research notes.',
        public: true,
      },
      variants: [
        {
          id: 'basic',
          name: 'Basic',
          price: '25.00',
          currency: 'CNY',
          enabled: true,
          attributes: {},
        },
        {
          id: 'retired',
          name: 'Retired',
          price: null,
          currency: 'CNY',
          enabled: false,
          attributes: {},
        },
      ],
    },
  ],
}
const redirectUri = 'https://shop.example.test/store/manage'
async function select(id: string, value: string) {
  await act(async () => {
    const field = document.querySelector<HTMLSelectElement>(id)
    assert.ok(field)
    field.value = value
    field.dispatchEvent(new Event('change', { bubbles: true }))
    await flush()
  })
}
test('Extore settings default to the public origin and stay separated by account', async () => {
  localStorage.setItem(
    'store:extore:7',
    JSON.stringify({
      baseUrl: 'https://private-shop.example',
      clientId: 'other-account',
    })
  )
  await mount(
    <StoreExtoreImport
      userId={8}
      callbackUrl={null}
      redirectUri={redirectUri}
      accessSupported
      onClose={() => {}}
      onImport={() => {}}
    />
  )
  assert.equal(
    document.querySelector<HTMLInputElement>('#extore-base-url')?.value,
    'https://extore.lmm.best'
  )
  assert.equal(
    document.querySelector<HTMLInputElement>('#extore-client-id')?.value,
    ''
  )
  assert.equal(document.querySelectorAll('details[open]').length, 0)
})
test('StrictMode exchanges an Extore code once and disables retired variants before draft import', async () => {
  let requests = 0
  storeApi.extoreCatalog = async () => {
    requests++
    return catalog
  }
  let draft: ReturnType<typeof extoreProductDraft> | undefined
  await mount(
    <StrictMode>
      <StoreExtoreImport
        userId={8}
        callbackUrl={
          redirectUri +
          '?state=test&code=test&iss=https%3A%2F%2Fextore.lmm.best'
        }
        redirectUri={redirectUri}
        accessSupported
        onClose={() => {}}
        onImport={(value) => {
          draft = value
        }}
      />
    </StrictMode>
  )
  assert.equal(requests, 1)
  await select('#extore-product', 'notes')
  assert.equal(
    document.querySelector<HTMLOptionElement>(
      '#extore-variant option[value=retired]'
    )?.disabled,
    true
  )
  await select('#extore-variant', 'basic')
  assert.match(document.body.textContent || '', /25.00 CNY/)
  const fill = [...document.querySelectorAll<HTMLButtonElement>('button')].find(
    (node) => node.textContent?.trim() === 'Fill product draft'
  )
  assert.ok(fill)
  await act(async () => {
    fill.click()
    await flush()
  })
  assert.ok(draft)
  assert.equal(draft.fields.title, 'Research notes · Basic')
  assert.equal('price_quota' in draft.fields, false)
  assert.equal('status' in draft.fields, false)
})
test('the imported editor keeps sale price blank and optional settings folded', async () => {
  const parsed = parseExtoreCatalog(catalog)
  const product = parsed.products[0]
  assert.ok(product)
  const variant = product.variants[0]
  assert.ok(variant)
  const draft = extoreProductDraft(parsed, product, variant, 'en')
  useAuthStore
    .getState()
    .auth.setUser({ id: 8, role: 1, status: 1, username: 'seller' })
  await mount(
    <StoreProductEditor
      importedDraft={draft}
      minimumPriceQuota={500000}
      allowedMethods={['balance']}
      accessSupported
      onClose={() => {}}
      onSaved={async () => {}}
    />
  )
  assert.equal(
    document.querySelector<HTMLInputElement>('#store-title')?.value,
    'Research notes · Basic'
  )
  assert.equal(
    document.querySelector<HTMLInputElement>('#store-price')?.value,
    ''
  )
  assert.equal(document.querySelectorAll('details[open]').length, 0)
})
