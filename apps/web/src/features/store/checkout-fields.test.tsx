/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

import type { StoreProduct } from './types'

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
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { useAuthStore } = await import('@/stores/auth-store')
const { api } = await import('@/lib/api')
const { StoreCheckout } = await import('./product-page')
const { StoreProductEditor } = await import('./seller-page')
const { StoreInventoryComposer } = await import('./inventory-composer')
const { StoreInventoryImportPreview } =
  await import('./inventory-import-preview')
const i18n = createInstance()
await i18n.use(initReactI18next).init({ lng: 'en', resources: {} })
const originalGet = api.get
const originalPost = api.post
const originalPut = api.put
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
  official: true,
  available_stock: 5,
  promotion_expires_at: 0,
  created_at: 1,
  updated_at: 1,
  review_note: '',
}
const result = (data: unknown) => ({ data: { success: true, data } })
async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 35))
}
async function mount(node: React.ReactNode) {
  useAuthStore
    .getState()
    .auth.setUser({ id: 2, role: 1, username: 'buyer-2', quota: 5000000 })
  document.body.replaceChildren()
  const host = document.createElement('div')
  document.body.append(host)
  const mountedRoot = createRoot(host)
  root = mountedRoot
  const mountedClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
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
  const found = [
    ...document.querySelectorAll<HTMLButtonElement>('button'),
  ].find((node) => node.textContent?.trim() === text)
  assert.ok(found, `button ${text}`)
  return found
}
function field(id: string) {
  const found = document.querySelector<HTMLInputElement>(`#${id}`)
  assert.ok(found, `input ${id}`)
  return found
}
async function click(node: HTMLElement) {
  await act(async () => {
    node.click()
    await flush()
  })
}
async function input(node: HTMLInputElement, value: string) {
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
}
function mockCheckout() {
  const writes: Array<{ url: string; body: Record<string, unknown> }> = []
  api.get = (async () =>
    result({
      version: 'merchant-store-v1',
      text: 'Terms',
      accepted: true,
    })) as typeof api.get
  api.post = (async (url: string, body: Record<string, unknown>) => {
    writes.push({ url, body })
    return result({
      order: {
        id: 'order-fixture',
        status: 'pending',
        trade_no: 'MS-fixture',
      },
      created: true,
    })
  }) as typeof api.post
  return writes
}
afterEach(async () => {
  const mountedRoot = root
  if (mountedRoot) await act(async () => mountedRoot.unmount())
  root = undefined
  client?.clear()
  api.get = originalGet
  api.post = originalPost
  api.put = originalPut
  useAuthStore.getState().auth.setUser(null)
  document.body.replaceChildren()
})
after(() => dom.happyDOM.abort())

test('inventory composer reports a draft before a merchant clicks Add', async () => {
  const dirty: boolean[] = []
  await mount(
    <StoreInventoryComposer
      template='account-details'
      items={[]}
      onChange={() =>
        assert.fail('the unadded draft must not import inventory')
      }
      onDraftChange={(value) => dirty.push(value)}
      disabled={false}
    />
  )
  assert.equal(dirty.at(-1), false)
  await input(field('store-delivery-username'), 'unfinished-user')
  assert.equal(dirty.at(-1), true)
  await input(field('store-delivery-username'), '')
  assert.equal(dirty.at(-1), false)
})

test('inventory preview names the chosen custom variant without exposing cards or silently removing repeats', async () => {
  const items = ['private-card-fixture', 'private-card-fixture', ' different ']
  let selected: string[] | undefined
  await mount(
    <StoreInventoryImportPreview
      items={items}
      target='自定义规格 · 18 个月'
      onRemoveDuplicates={(value) => {
        selected = value
      }}
      disabled={false}
    />
  )
  assert.match(document.body.textContent ?? '', /自定义规格 · 18 个月/)
  assert.equal(
    document.body.textContent?.includes('private-card-fixture'),
    false
  )
  assert.equal(selected, undefined)
  assert.equal(items.length, 3)
  await click(button('Remove exact duplicates'))
  assert.deepEqual(selected, ['private-card-fixture', ' different '])
  assert.equal(items.length, 3)
})

for (const codeRequired of [false, true]) {
  for (const emailRequired of [false, true]) {
    test(`checkout always offers pickup fields; code required=${codeRequired}, email required=${emailRequired}`, async () => {
      const writes = mockCheckout()
      await mount(
        <StoreCheckout
          product={{
            ...product,
            pickup_code_required: codeRequired,
            email_pickup_link: emailRequired,
          }}
        />
      )
      const code = field('store-pickup-code')
      const email = field('store-pickup-email')
      assert.equal(code.disabled, false)
      assert.equal(email.disabled, false)
      assert.equal(code.required, codeRequired)
      assert.equal(email.required, emailRequired)
      assert.equal(email.type, 'email')
      assert.equal(
        button('Place order').disabled,
        codeRequired || emailRequired
      )
      await click(button('Place order'))
      if (codeRequired || emailRequired) assert.equal(writes.length, 0)
      else {
        assert.equal(writes.length, 1)
        assert.equal('pickup_code' in writes[0].body, false)
        assert.equal('pickup_email' in writes[0].body, false)
        return
      }
      await input(code, '12345678')
      assert.equal(button('Place order').disabled, emailRequired)
      await input(email, 'buyer@example.test')
      assert.equal(button('Place order').disabled, false)
      await input(code, '')
      assert.equal(button('Place order').disabled, codeRequired)
      await input(code, '12345678')
      await input(email, '')
      assert.equal(button('Place order').disabled, emailRequired)
      await input(email, 'buyer@example.test')
      await click(button('Place order'))
      assert.equal(writes.length, 1)
      assert.equal(writes[0].url, '/api/store/orders')
      assert.equal(writes[0].body.pickup_code, '12345678')
      assert.equal(writes[0].body.pickup_email, 'buyer@example.test')
    })
  }
}

test('optional pickup fields submit valid values and trim the email address', async () => {
  const writes = mockCheckout()
  await mount(<StoreCheckout product={product} />)
  await input(field('store-pickup-code'), 'optional-code')
  await input(field('store-pickup-email'), ' buyer+store@example.test ')
  assert.equal(button('Place order').disabled, false)
  await click(button('Place order'))
  assert.equal(writes.length, 1)
  assert.equal(writes[0].body.pickup_code, 'optional-code')
  assert.equal(writes[0].body.pickup_email, 'buyer+store@example.test')
})

test('optional pickup values must be valid before checkout and can be cleared', async () => {
  const writes = mockCheckout()
  await mount(<StoreCheckout product={product} />)
  const code = field('store-pickup-code')
  const email = field('store-pickup-email')
  for (const invalidCode of ['1234567', '密'.repeat(25)]) {
    await input(code, invalidCode)
    assert.equal(button('Place order').disabled, true)
    await click(button('Place order'))
    assert.equal(writes.length, 0)
  }
  await input(code, '密'.repeat(24))
  assert.equal(button('Place order').disabled, false)
  await input(code, '')
  assert.equal(button('Place order').disabled, false)
  for (const invalidEmail of ['buyer', 'buyer@', 'buyer@example.test extra']) {
    await input(email, invalidEmail)
    assert.equal(button('Place order').disabled, true)
    await click(button('Place order'))
    assert.equal(writes.length, 0)
  }
  await input(email, 'buyer@example.test')
  assert.equal(button('Place order').disabled, false)
  await input(email, '')
  assert.equal(button('Place order').disabled, false)
  assert.equal(writes.length, 0)
})

test('seller pickup controls are field previews with required switches and save their requirements', async () => {
  let saved: Record<string, unknown> | undefined
  api.put = (async (_url: string, body: Record<string, unknown>) => {
    saved = body
    return result(product)
  }) as typeof api.put
  await mount(
    <StoreProductEditor
      product={{
        ...product,
        pickup_code_required: true,
        email_pickup_link: true,
      }}
      allowedMethods={['balance']}
      minimumPriceQuota={500000}
      onClose={() => {}}
      onSaved={async () => {}}
    />
  )
  const codePreview = field('store-pickup-code-preview')
  const emailPreview = field('store-pickup-email-preview')
  for (const [preview, requirement] of [
    [codePreview, 'Require a pickup code'],
    [emailPreview, 'Require a pickup email'],
  ] as const) {
    assert.equal(preview.readOnly, true)
    assert.equal(preview.placeholder, 'Buyer enters this at checkout')
    assert.ok(document.querySelector(`label[for="${preview.id}"]`))
    const toggle = document.querySelector<HTMLElement>(
      `[role="switch"][aria-label="${requirement}"]`
    )
    assert.ok(toggle, requirement)
    assert.ok(preview.parentElement?.contains(toggle))
    assert.equal(toggle.closest('label')?.textContent?.trim(), 'Required')
    assert.ok(
      preview.compareDocumentPosition(toggle) & Node.DOCUMENT_POSITION_FOLLOWING
    )
    assert.equal(toggle.getAttribute('aria-checked'), 'true')
    const toggleInput = toggle
      .closest('label')
      ?.querySelector<HTMLInputElement>('input[type="checkbox"]')
    assert.ok(toggleInput, `checkbox ${requirement}`)
    await click(toggleInput)
    assert.equal(toggle.getAttribute('aria-checked'), 'false')
    assert.ok(document.getElementById(preview.id))
  }
  const protection = codePreview.closest('fieldset')
  assert.ok(protection)
  assert.equal(protection.querySelectorAll('[role="switch"]').length, 3)
  assert.match(
    protection.textContent || '',
    /Buyers can always fill in these fields/
  )
  assert.equal(
    protection.textContent?.includes('Email the pickup link to the buyer'),
    false
  )
  assert.equal(
    protection.textContent?.includes('Require buyer to set a pickup code'),
    false
  )
  const form = document.querySelector('form')
  assert.ok(form)
  await act(async () => {
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await flush()
  })
  assert.ok(saved)
  assert.equal(saved.pickup_code_required, false)
  assert.equal(saved.email_pickup_link, false)
})
