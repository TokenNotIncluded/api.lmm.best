/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

import type {
  CommerceImportConnection,
  CommerceImportListing,
  CommerceImportRequest,
} from './commerce-import-types'

const dom = new Window({ url: 'https://shop.example.test/store/manage' })
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
  'HTMLSelectElement',
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
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { StoreCommerceImportManager } = await import('./commerce-import')
const { StoreCommerceImportPreview } = await import('./commerce-import-preview')
const { STORE_COMMERCE_IMPORT_COPY: copy } =
  await import('./commerce-import-copy')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })

const authScope = { userId: 19, sessionId: 'commerce-import-session' }
const config = {
  enabled: true,
  redirect_uri: 'https://shop.example.test/api/store/commerce-import/callback',
  trusted_origins: ['https://extore.example.test'],
}
const connection: CommerceImportConnection = {
  id: 'connection-fixture',
  issuer: config.trusted_origins[0],
  client_id: 'registered-client-fixture',
  shop_id: 'external-shop',
  shop_name: 'External fixture shop',
  scope: 'products.read cards.issue',
  status: 'active',
  maximum_cards_per_request: 100,
}
const listing: CommerceImportListing = {
  schema: 'extore.product-listing.v1',
  id: 'external-product',
  shop_id: 'external-shop',
  revision: 'a'.repeat(64),
  redemption_url: 'https://extore.example.test/',
  semantics: {
    price: 'reference',
    inventory: 'not_exported',
    payment: 'external_sales_platform',
    redemption: 'extore',
  },
  product: {
    name: 'External product fixture',
    description: 'Buyer redeems a card in the external shop.',
    mode: 'manual',
    public: true,
  },
  variants: [
    {
      id: 'usd-sku',
      name: 'USD reference SKU',
      price: '9.95',
      currency: 'USD',
      enabled: true,
    },
    {
      id: 'cny-sku',
      name: 'CNY reference SKU',
      price: '66',
      currency: 'CNY',
      enabled: true,
    },
  ],
}
const mapping = {
  external_product_id: listing.id,
  local_product_id: 'local-product',
  revision: listing.revision,
  variants: listing.variants.map((variant) => ({
    external_id: variant.id,
    local_variant_id: `local-${variant.id}`,
  })),
}
const original = { get: api.get, post: api.post }
let root: ReturnType<typeof createRoot> | undefined
let client: InstanceType<typeof QueryClient> | undefined
const envelope = (data: unknown) => ({ data: { success: true, data } })
type Request = {
  method: 'GET' | 'POST'
  url: string
  body?: unknown
  options?: unknown
}

function signIn() {
  const now = Math.floor(Date.now() / 1000)
  useAuthStore.getState().auth.setBundle({
    access_token: 'local-login-fixture',
    token_type: 'Bearer',
    access_expires_at: now + 3600,
    user: {
      id: authScope.userId,
      username: 'commerce-fixture',
      role: 1,
      status: 1,
    },
    session: {
      sid: authScope.sessionId,
      current: true,
      login_method: 'password',
      ip: '',
      user_agent: '',
      created_at: now,
      last_active_at: now,
      expires_at: now + 3600,
    },
  })
}
async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 35))
}
async function mount(node: React.ReactNode) {
  signIn()
  const host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const mounted = root
  const mountedClient = client
  await act(async () => {
    mounted.render(
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
    (candidate) => candidate.textContent?.trim() === text
  )
  assert.ok(node, `button ${text}`)
  return node
}
function labelled<T extends HTMLElement>(text: string) {
  const label = [...document.querySelectorAll<HTMLLabelElement>('label')].find(
    (candidate) => candidate.textContent?.trim() === text
  )
  assert.ok(label, `label ${text}`)
  const node = document.getElementById(label.htmlFor)
  assert.ok(node, `input for ${text}`)
  return node as T
}
async function choose(text: string, value: string) {
  const select = labelled<HTMLSelectElement>(text)
  await act(async () => {
    select.value = value
    select.dispatchEvent(new Event('change', { bubbles: true }))
    await flush()
  })
  await act(flush)
}
async function click(node: HTMLElement) {
  await act(async () => {
    node.click()
    await flush()
  })
  await act(flush)
}
async function input(text: string, value: string) {
  const node = labelled<HTMLInputElement>(text)
  const setter = Object.getOwnPropertyDescriptor(
    dom.HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(setter)
  await act(async () => {
    setter.call(node, value)
    node.dispatchEvent(new Event('input', { bubbles: true }))
    await flush()
  })
  await act(flush)
}
type RestockReply = (request: Request) => Promise<ReturnType<typeof envelope>>
function mockRequests(
  initial: CommerceImportRequest[],
  restockReply?: RestockReply,
  recoverReply?: RestockReply
) {
  let batches = initial
  const records: Request[] = []
  const base = `/api/store/commerce-import/connections/${connection.id}`
  api.get = (async (url: string, options: unknown) => {
    records.push({ method: 'GET', url, options })
    if (url.endsWith('/connections')) return envelope([connection])
    if (url === `${base}/catalog`) {
      return envelope({
        schema: 'extore.commerce-catalog.v1',
        issuer: connection.issuer,
        shop: { id: connection.shop_id, name: connection.shop_name },
        grant_id: 'grant-fixture',
        products: [listing],
        mappings: [mapping],
      })
    }
    if (url === `${base}/requests`) {
      return envelope(batches.map((batch) => ({ ...batch })))
    }
    assert.fail(`Unexpected GET ${url}`)
  }) as typeof api.get
  api.post = (async (url: string, body: unknown, options: unknown) => {
    const request: Request = { method: 'POST', url, body, options }
    records.push(request)
    if (url === `${base}/restock` && restockReply) return restockReply(request)
    const originalBatch = batches.find(
      (batch) =>
        url === `${base}/requests/${encodeURIComponent(batch.id)}/recover`
    )
    assert.ok(originalBatch)
    assert.equal(
      url,
      `${base}/requests/${encodeURIComponent(originalBatch.id)}/recover`
    )
    if (recoverReply) return recoverReply(request)
    const imported = { ...originalBatch, status: 'imported' as const }
    batches = [imported]
    return envelope(imported)
  }) as typeof api.post
  return records
}
async function showManager(
  requests: CommerceImportRequest[],
  restockReply?: RestockReply,
  recoverReply?: RestockReply
) {
  const records = mockRequests(requests, restockReply, recoverReply)
  await mount(
    <StoreCommerceImportManager
      authScope={authScope}
      config={config}
      navigate={() => assert.fail('Unexpected authorization navigation')}
    />
  )
  await choose(copy.selectConnection, connection.id)
  return records
}
async function showProduct() {
  const product = [
    ...document.querySelectorAll<HTMLButtonElement>('button'),
  ].find((candidate) =>
    candidate.textContent?.includes('External product fixture')
  )
  assert.ok(product, 'External product is available for review')
  await click(product)
}
async function confirmRestock() {
  await choose(copy.restockVariant, listing.variants[0].id)
  await input(copy.count, '2')
  await click(labelled(copy.restockConfirm))
  assert.equal(button(copy.generate).disabled, false)
}
function preview(scope = connection.scope, source = listing) {
  return (
    <StoreCommerceImportPreview
      listing={source}
      mapping={mapping}
      connection={{ ...connection, scope }}
      busy={false}
      uncertain={false}
      onSave={async () => assert.fail('Unconfirmed prices must not import')}
      onRestock={async () =>
        assert.fail('Read-only permissions must not restock')
      }
    />
  )
}

afterEach(async () => {
  const mounted = root
  if (mounted) await act(async () => mounted.unmount())
  root = undefined
  client?.clear()
  client = undefined
  Object.assign(api, original)
  useAuthStore.getState().auth.reset()
  document.body.replaceChildren()
})
after(() => dom.happyDOM.abort())

test('product-reading permission cannot generate new cards for an imported SKU', async () => {
  await mount(preview('products.read'))
  assert.equal(labelled<HTMLSelectElement>(copy.restockVariant).disabled, true)
  const generate = button(copy.generate)
  assert.equal(generate.disabled, true)
  await click(generate)
})

test('USD and CNY reference prices never fill actual selling prices or local visibility', async () => {
  await mount(preview())
  assert.match(document.body.textContent || '', /9\.95 USD/)
  assert.match(document.body.textContent || '', /66 CNY/)
  const priceInputs = [
    ...document.querySelectorAll<HTMLInputElement>('input'),
  ].filter((input) => /-price-\d+$/.test(input.id))
  assert.equal(priceInputs.length, 2)
  assert.deepEqual(
    priceInputs.map((input) => input.value),
    ['', '']
  )
  assert.equal(labelled<HTMLSelectElement>(copy.visibility).value, '')
  assert.equal(button(copy.saveDraft).disabled, true)
  await choose(copy.priceUnit, 'QUOTA')
  assert.deepEqual(
    priceInputs.map((input) => input.value),
    ['', '']
  )
  assert.equal(button(copy.saveDraft).disabled, true)
})

test('pending recovery uses the saved original request ID and never creates another restock', async () => {
  const pending: CommerceImportRequest = {
    id: 'saved/request id',
    product_id: listing.id,
    variant_id: listing.variants[0].id,
    count: 2,
    status: 'pending',
    recovery_expires: Math.floor(Date.now() / 1000) + 3600,
  }
  const records = await showManager([pending])
  await click(button(copy.recover))
  const writes = records.filter((request) => request.method === 'POST')
  assert.equal(writes.length, 1)
  assert.equal(
    writes[0].url,
    `/api/store/commerce-import/connections/${connection.id}/requests/saved%2Frequest%20id/recover`
  )
  assert.deepEqual(writes[0].body, {})
  assert.equal(
    records.some((request) => request.url.endsWith('/restock')),
    false
  )
  assert.match(
    document.body.textContent || '',
    new RegExp(copy.importedRequest)
  )
})

test('received cards remain waiting for local inventory import until recovery completes', async () => {
  const received: CommerceImportRequest = {
    id: 'received-fixture',
    product_id: listing.id,
    variant_id: listing.variants[0].id,
    count: 2,
    status: 'received',
    batch_id: 'encrypted-batch-fixture',
    recovery_expires: Math.floor(Date.now() / 1000) + 3600,
  }
  const records = await showManager([received])
  assert.ok((document.body.textContent || '').includes(copy.receivedRequest))
  assert.equal(
    (document.body.textContent || '').includes(copy.importedRequest),
    false
  )
  assert.equal(button(copy.recover).disabled, false)
  await click(button(copy.recover))
  assert.equal(records.filter((request) => request.method === 'POST').length, 1)
  assert.ok((document.body.textContent || '').includes(copy.importedRequest))
  assert.equal(
    records.some((request) => request.url.endsWith('/restock')),
    false
  )
})

test('external text-card stock cannot generate new cards even with issuance permission', async () => {
  const stockListing = {
    ...listing,
    product: { ...listing.product, mode: 'stock' },
  }
  await mount(preview(connection.scope, stockListing))
  assert.equal(labelled<HTMLSelectElement>(copy.restockVariant).disabled, true)
  assert.equal(labelled<HTMLInputElement>(copy.count).disabled, true)
  assert.equal(button(copy.generate).disabled, true)
  await click(button(copy.generate))
})

test('an externally disabled SKU cannot be re-enabled for new card issuance', async () => {
  const disabledListing = {
    ...listing,
    variants: listing.variants.map((variant) => ({
      ...variant,
      enabled: false,
    })),
  }
  await mount(preview(connection.scope, disabledListing))
  const select = labelled<HTMLSelectElement>(copy.restockVariant)
  const option = [...select.options].find(
    (item) => item.value === listing.variants[0].id
  )
  assert.ok(option)
  assert.equal(option.disabled, true)
  // A synthetic change cannot override the authoritative external enabled flag.
  await choose(copy.restockVariant, listing.variants[0].id)
  assert.equal(labelled<HTMLInputElement>(copy.count).disabled, true)
  assert.equal(button(copy.generate).disabled, true)
  await click(button(copy.generate))
})

test('a restock timeout blocks new issuance and recovers only its durable pending request', async () => {
  const batches: CommerceImportRequest[] = []
  const pending: CommerceImportRequest = {
    id: 'durable-timeout-request',
    product_id: listing.id,
    variant_id: listing.variants[0].id,
    count: 2,
    status: 'pending',
    recovery_expires: Math.floor(Date.now() / 1000) + 3600,
  }
  const records = await showManager(batches, async () => {
    batches.push(pending)
    throw Object.assign(
      new Error('Response was lost after the request was saved'),
      {
        code: 'ECONNABORTED',
      }
    )
  })
  await showProduct()
  await confirmRestock()
  await click(button(copy.generate))
  assert.ok((document.body.textContent || '').includes(copy.requestUnknown))
  assert.equal(labelled<HTMLSelectElement>(copy.restockVariant).disabled, true)
  assert.equal(button(copy.generate).disabled, true)
  await click(button(copy.generate))
  const newBatches = () =>
    records.filter((request) => request.url.endsWith('/restock'))
  assert.equal(newBatches().length, 1)
  assert.deepEqual(newBatches()[0].body, {
    product_id: listing.id,
    variant_id: listing.variants[0].id,
    count: 2,
    expected_revision: listing.revision,
    label: '',
  })
  await click(button(copy.recover))
  const writes = records.filter((request) => request.method === 'POST')
  assert.equal(writes.length, 2)
  assert.equal(
    writes[1].url,
    `/api/store/commerce-import/connections/${connection.id}/requests/${pending.id}/recover`
  )
  assert.deepEqual(writes[1].body, {})
  assert.equal(newBatches().length, 1)
  assert.ok((document.body.textContent || '').includes(copy.importedRequest))
  assert.equal(labelled<HTMLSelectElement>(copy.restockVariant).disabled, false)
})

for (const errorCode of [
  'quota_exceeded',
  'manual_unknown',
  'issuance_expired',
]) {
  test(`a historical ${errorCode} request keeps new issuance blocked`, async () => {
    const records = await showManager([
      {
        id: `unresolved-${errorCode}`,
        product_id: listing.id,
        variant_id: listing.variants[0].id,
        count: 2,
        status: 'manual_recovery',
        error_code: errorCode,
      },
    ])
    await showProduct()
    assert.equal(
      labelled<HTMLSelectElement>(copy.restockVariant).disabled,
      true
    )
    assert.equal(button(copy.generate).disabled, true)
    await click(button(copy.generate))
    assert.equal(
      records.some((request) => request.method === 'POST'),
      false
    )
    assert.equal(
      [...document.querySelectorAll('button')].some(
        (candidate) => candidate.textContent?.trim() === copy.recover
      ),
      false
    )
  })
}

test('a received batch past its recovery window requires manual verification', async () => {
  const records = await showManager([
    {
      id: 'expired-received-request',
      product_id: listing.id,
      variant_id: listing.variants[0].id,
      count: 2,
      status: 'received',
      batch_id: 'expired-encrypted-batch',
      recovery_expires: Math.floor(Date.now() / 1000) - 3600,
    },
  ])
  await showProduct()
  assert.equal(labelled<HTMLSelectElement>(copy.restockVariant).disabled, true)
  assert.equal(button(copy.generate).disabled, true)
  assert.equal(
    [...document.querySelectorAll('button')].some(
      (candidate) => candidate.textContent?.trim() === copy.recover
    ),
    false
  )
  assert.equal(
    records.some((request) => request.method === 'POST'),
    false
  )
})

test('a historical manual request confirmed to have no issuance permits a newly confirmed restock', async () => {
  const batches: CommerceImportRequest[] = [
    {
      id: 'classified-no-issuance',
      product_id: listing.id,
      variant_id: listing.variants[0].id,
      count: 2,
      status: 'manual_recovery',
      error_code: 'manual_unknown',
      issuance_uncertain: false,
    },
  ]
  const records = await showManager(batches, async () => {
    const imported: CommerceImportRequest = {
      id: 'newly-confirmed-request',
      product_id: listing.id,
      variant_id: listing.variants[0].id,
      count: 2,
      status: 'imported',
      issuance_uncertain: false,
    }
    batches.push(imported)
    return envelope(imported)
  })
  await showProduct()
  assert.equal(labelled<HTMLSelectElement>(copy.restockVariant).disabled, false)
  await confirmRestock()
  await click(button(copy.generate))
  const writes = records.filter((request) => request.method === 'POST')
  assert.equal(writes.length, 1)
  assert.ok(writes[0].url.endsWith('/restock'))
  assert.equal(
    records.some((request) => request.url.endsWith('/recover')),
    false
  )
})

test('a recovery rejection keeps issuance locked when its durable classification is uncertain', async () => {
  const requestId = '11111111-1111-4111-8111-111111111111'
  const batches: CommerceImportRequest[] = [
    {
      id: requestId,
      product_id: listing.id,
      variant_id: listing.variants[0].id,
      count: 2,
      status: 'pending',
      recovery_expires: Math.floor(Date.now() / 1000) + 3600,
    },
  ]
  const records = await showManager(batches, undefined, async () => {
    batches[0] = {
      ...batches[0],
      status: 'manual_recovery',
      error_code: 'quota_exceeded',
      issuance_uncertain: true,
    }
    throw Object.assign(new Error('Recovery was rejected'), {
      response: {
        status: 409,
        data: {
          success: false,
          error: 'quota_exceeded',
          request_id: requestId,
        },
      },
    })
  })
  await showProduct()
  await click(button(copy.recover))
  assert.equal(labelled<HTMLSelectElement>(copy.restockVariant).disabled, true)
  assert.equal(button(copy.generate).disabled, true)
  await click(button(copy.generate))
  const writes = records.filter((request) => request.method === 'POST')
  assert.equal(writes.length, 1)
  assert.ok(writes[0].url.endsWith(`/requests/${requestId}/recover`))
  assert.equal(
    records.some((request) => request.url.endsWith('/restock')),
    false
  )
})

for (const matching of [true, false]) {
  test(`a restock rejection ${matching ? 'clears' : 'keeps'} its temporary lock when the non-issuance row ${matching ? 'matches' : 'differs from'} its request ID`, async () => {
    const requestId = '11111111-1111-4111-8111-111111111111'
    const batches: CommerceImportRequest[] = []
    const records = await showManager(batches, async () => {
      batches.push({
        id: matching ? requestId : '22222222-2222-4222-8222-222222222222',
        product_id: listing.id,
        variant_id: listing.variants[0].id,
        count: 2,
        status: 'manual_recovery',
        error_code: 'quota_exceeded',
        issuance_uncertain: false,
      })
      throw Object.assign(new Error('The new request was rejected'), {
        response: {
          status: 409,
          data: {
            success: false,
            error: 'quota_exceeded',
            request_id: requestId,
          },
        },
      })
    })
    await showProduct()
    await confirmRestock()
    await click(button(copy.generate))
    assert.equal(
      labelled<HTMLSelectElement>(copy.restockVariant).disabled,
      !matching
    )
    assert.equal(
      records.filter((request) => request.url.endsWith('/restock')).length,
      1
    )
    // A successful ordinary refresh must not use another row to release this lock.
    await click(button(copy.refresh))
    assert.equal(
      labelled<HTMLSelectElement>(copy.restockVariant).disabled,
      !matching
    )
    assert.equal(
      records.filter((request) => request.method === 'POST').length,
      1
    )
  })
}

test('a failed classification refresh cannot clear a restock lock from stale non-issuance data', async () => {
  const requestId = '11111111-1111-4111-8111-111111111111'
  const batches: CommerceImportRequest[] = []
  const records = await showManager(batches, async () => {
    batches.push({
      id: requestId,
      product_id: listing.id,
      variant_id: listing.variants[0].id,
      count: 2,
      status: 'manual_recovery',
      issuance_uncertain: false,
    })
    throw Object.assign(new Error('The new request was rejected'), {
      response: {
        status: 409,
        data: {
          success: false,
          error: 'quota_exceeded',
          request_id: requestId,
        },
      },
    })
  })
  const configuredGet = api.get
  let classificationReads = 0
  api.get = (async (url, options) => {
    if (url.endsWith('/requests')) {
      classificationReads += 1
      // The first post-error read succeeds; the deciding refresh then fails.
      if (classificationReads === 2) {
        throw new Error('Classification refresh unavailable')
      }
    }
    return configuredGet(url, options)
  }) as typeof api.get
  await showProduct()
  await confirmRestock()
  await click(button(copy.generate))
  assert.equal(classificationReads, 2)
  assert.equal(labelled<HTMLSelectElement>(copy.restockVariant).disabled, true)
  await click(button(copy.refresh))
  assert.equal(classificationReads, 3)
  assert.equal(labelled<HTMLSelectElement>(copy.restockVariant).disabled, true)
  assert.equal(
    records.filter((request) => request.url.endsWith('/restock')).length,
    1
  )
})
