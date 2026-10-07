/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

import type { StoreClaimMetadata } from './types'

const token = 'fixed-content-claim-fixture'
const endpoint = `/api/user/auth/store-claim/${token}`
const content =
  '# Private delivery\n\n**Keep this private.**\n\nOne shared guide.'
const dom = new Window({
  url: `https://shop.example.test/store/claim/${token}`,
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
// Match the existing store interaction tests: verify the protected page-to-
// Markdown boundary without using Happy DOM as DOMPurify's browser realm.
mock.module('@/components/ui/markdown', () => ({
  Markdown: ({ children }: { children: string }) => (
    <div data-store-markdown>{children}</div>
  ),
}))
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { useAuthStore } = await import('@/stores/auth-store')
const { api } = await import('@/lib/api')
const { StoreClaimPage } = await import('./claim-page')
const i18n = createInstance()
await i18n.use(initReactI18next).init({ lng: 'en', resources: {} })
const originalGet = api.get
const originalPost = api.post
const originalWriteText = dom.navigator.clipboard.writeText
let root: ReturnType<typeof createRoot> | undefined
let client: InstanceType<typeof QueryClient> | undefined
const envelope = (data: unknown) => ({ data: { success: true, data } })
const metadata: StoreClaimMetadata = {
  order_id: 'fixed-content-order',
  product_title: 'Private shared guide',
  variant_name: 'Purchased guide',
  delivery_template: 'fixed-content',
  quantity: 5,
  status: 'paid',
  pickup_login_required: false,
  pickup_code_required: false,
  pickup_login_satisfied: false,
}
async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 35))
}
async function mount(changes: Partial<StoreClaimMetadata> = {}) {
  useAuthStore.getState().auth.setUser(null)
  api.get = (async (url: string) => {
    assert.equal(url, endpoint, 'only the public collection metadata is read')
    return envelope({ ...metadata, ...changes })
  }) as typeof api.get
  document.body.replaceChildren()
  const host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  await act(async () => {
    root!.render(
      <QueryClientProvider client={client!}>
        <I18nextProvider i18n={i18n}>
          <StoreClaimPage token={token} />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flush()
  })
  await act(flush)
}
function buttons(label: string) {
  return [...document.querySelectorAll<HTMLButtonElement>('button')].filter(
    (button) => button.textContent?.trim() === label
  )
}
async function click(button: HTMLButtonElement) {
  await act(async () => {
    button.click()
    await flush()
  })
}
function assertPrivateContentHidden() {
  assert.equal(document.querySelector('[data-store-markdown]'), null)
  assert.doesNotMatch(document.body.textContent || '', /Private delivery/)
  assert.equal(buttons('Copy all').length, 0)
}
afterEach(async () => {
  await act(async () => root?.unmount())
  root = undefined
  client?.clear()
  client = undefined
  api.get = originalGet
  api.post = originalPost
  dom.navigator.clipboard.writeText = originalWriteText
  useAuthStore.getState().auth.setUser(null)
})
after(() => dom.close())

test('unpaid fixed content never invokes collection or reveals the private body', async () => {
  let collections = 0
  api.post = (async () => {
    collections++
    return envelope({ fixed_content: content, items: [] })
  }) as typeof api.post
  await mount({ status: 'pending' })
  assert.match(document.body.textContent || '', /This order is not paid yet/)
  assertPrivateContentHidden()
  assert.equal(buttons('Collect items').length, 0)
  assert.equal(collections, 0)
})

test('paid protected collection displays and copies the body once with its remaining quantity', async () => {
  const copied: string[] = []
  dom.navigator.clipboard.writeText = async (value) => {
    copied.push(value)
  }
  const requests: Array<{ url: string; body: unknown }> = []
  api.post = (async (url: string, body: unknown) => {
    requests.push({ url, body })
    return envelope({
      order_id: metadata.order_id,
      product_title: metadata.product_title,
      variant_name: metadata.variant_name,
      quantity: 3,
      delivery_template: 'fixed-content',
      fixed_content: content,
      items: [],
    })
  }) as typeof api.post
  await mount()
  assertPrivateContentHidden()
  assert.match(document.body.textContent || '', /Quantity: 5/)
  assert.equal(requests.length, 0)
  const collect = buttons('Collect items')[0]
  assert.ok(collect)
  await click(collect)
  assert.deepEqual(requests, [{ url: endpoint, body: { pickup_code: '' } }])
  assert.match(
    document.body.textContent || '',
    /Specification: Purchased guide/
  )
  assert.match(document.body.textContent || '', /Quantity: 3/)
  assert.doesNotMatch(document.body.textContent || '', /Quantity: 5/)
  const markdown = document.querySelectorAll('[data-store-markdown]')
  assert.equal(markdown.length, 1)
  assert.equal(markdown[0].textContent, content)
  assert.equal(document.querySelector('[role="checkbox"]'), null)
  const copy = buttons('Copy all')
  assert.equal(copy.length, 1)
  await click(copy[0])
  assert.deepEqual(copied, [content])
})

test('account protection blocks fixed content collection before purchaser sign-in', async () => {
  let collections = 0
  api.post = (async () => {
    collections++
    return envelope({ fixed_content: content, items: [] })
  }) as typeof api.post
  await mount({ pickup_login_required: true, pickup_login_satisfied: false })
  assertPrivateContentHidden()
  assert.ok(document.querySelector('a[href^="/sign-in?redirect="]'))
  assert.equal(buttons('Collect items').length, 0)
  assert.equal(collections, 0)
})

test('a required or rejected pickup code never reveals fixed content', async () => {
  let collections = 0
  api.post = (async (url: string, body: unknown) => {
    assert.equal(url, endpoint)
    assert.deepEqual(body, { pickup_code: 'incorrect-code' })
    collections++
    return { data: { success: false, message: 'Pickup code does not match.' } }
  }) as typeof api.post
  await mount({ pickup_code_required: true })
  const collect = buttons('Collect items')[0]
  assert.ok(collect)
  assert.equal(collect.disabled, true)
  await click(collect)
  assert.equal(collections, 0)
  assertPrivateContentHidden()
  const code = document.querySelector<HTMLInputElement>('#claim-code')
  assert.ok(code)
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(
      dom.HTMLInputElement.prototype,
      'value'
    )?.set
    assert.ok(setter)
    setter.call(code, 'incorrect-code')
    code.dispatchEvent(new Event('input', { bubbles: true }))
    await flush()
  })
  await click(collect)
  assert.equal(collections, 1)
  assertPrivateContentHidden()
  assert.match(document.body.textContent || '', /Pickup code does not match/)
})
