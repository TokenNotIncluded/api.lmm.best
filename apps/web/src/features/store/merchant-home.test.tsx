/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

import type { StoreMerchantHome } from './types'

const dom = new Window({ url: 'https://shop.example.test/store?seller_id=9' })
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
  'DOMParser',
  'XMLSerializer',
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
  Markdown: ({ children }: { children: string }) => (
    <div data-markdown>{children}</div>
  ),
}))
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const {
  StoreAnnouncement,
  StoreMerchantHomeHeader,
  StoreMerchantHomeSettings,
  StoreAnnouncementSettings,
} = await import('./merchant-home')
const { StoreMerchantIdentity } = await import('./merchant-identity')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const original = { get: api.get, put: api.put }
let root: ReturnType<typeof createRoot> | undefined
let client: InstanceType<typeof QueryClient> | undefined
const home: StoreMerchantHome = {
  seller: {
    id: 9,
    username: 'Actual merchant',
    display_name: 'Merchant fixture',
    avatar_url: `https://gravatar.com/avatar/${'a'.repeat(64)}?d=404&r=g&s=192`,
  },
  biography: '**Actual shop introduction**',
  announcement: '## Custom merchant news',
  header_image: 'https://example.test/header.svg',
  version: 3,
}
type Request = { method: string; url: string; body?: unknown }
function requests() {
  const items: Request[] = []
  api.get = (async (url: string) => {
    items.push({ method: 'GET', url })
    return {
      data: {
        success: true,
        data: url.endsWith('/announcement')
          ? { content: '## Global store news' }
          : home,
      },
    }
  }) as typeof api.get
  api.put = (async (url: string, body: unknown) => {
    items.push({ method: 'PUT', url, body })
    return { data: { success: true, data: home } }
  }) as typeof api.put
  return items
}
function owner(role = 1) {
  useAuthStore
    .getState()
    .auth.setUser({ id: 9, role, status: 1, username: 'Actual merchant' })
}
const flush = () => new Promise((resolve) => setTimeout(resolve, 35))
async function mount(node: React.ReactNode) {
  const host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  const mountedClient = client
  await act(async () => {
    root?.render(
      <QueryClientProvider client={mountedClient}>
        <I18nextProvider i18n={i18n}>{node}</I18nextProvider>
      </QueryClientProvider>
    )
    await flush()
  })
  await act(async () => {
    await flush()
  })
}
afterEach(async () => {
  await act(async () => root?.unmount())
  root = undefined
  client?.clear()
  client = undefined
  Object.assign(api, original)
  useAuthStore.getState().auth.setUser(null)
  document.body.replaceChildren()
})
after(() => dom.happyDOM.abort())

test('anonymous merchant home shows actual copy/header and separate public announcement', async () => {
  const sent = requests()
  await mount(
    <>
      <StoreAnnouncement supported />
      <StoreMerchantHomeHeader supported sellerId={9} />
    </>
  )
  assert.match(document.body.textContent ?? '', /Merchant fixture/)
  assert.match(document.body.textContent ?? '', /Actual shop introduction/)
  assert.match(document.body.textContent ?? '', /Global store news/)
  assert.match(document.body.textContent ?? '', /Custom merchant news/)
  assert.equal(
    document
      .querySelector('img[src="https://example.test/header.svg"]')
      ?.getAttribute('referrerpolicy'),
    'no-referrer'
  )
  assert.ok(sent.some((item) => item.url === '/api/store/merchants/9'))
  assert.equal(document.querySelectorAll('[data-markdown]').length, 3)
})

test('unsupported server does not make new endpoint requests or show broken profile controls', async () => {
  owner()
  const sent = requests()
  await mount(
    <>
      <StoreAnnouncement supported={false} />
      <StoreMerchantHomeHeader supported={false} sellerId={9} />
      <StoreMerchantHomeSettings supported={false} />
    </>
  )
  assert.equal(sent.length, 0)
  assert.equal(document.body.textContent, '')
})

test('shop editor submits only owned public fields with optimistic version, preserving avatar source explanation', async () => {
  owner()
  const sent = requests()
  await mount(<StoreMerchantHomeSettings supported />)
  assert.match(
    document.body.textContent ?? '',
    /same Gravatar as your platform account/
  )
  const input = document.querySelector<HTMLTextAreaElement>(
    '#merchant-biography'
  )
  assert.ok(input)
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(
      dom.HTMLTextAreaElement.prototype,
      'value'
    )?.set
    setter?.call(input, 'Updated shop introduction')
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await flush()
  })
  const form = document.querySelector('form')
  assert.ok(form)
  await act(async () => {
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await flush()
  })
  const save = sent.find((item) => item.method === 'PUT')
  assert.deepEqual(save?.body, {
    biography: 'Updated shop introduction',
    announcement: home.announcement,
    header_image: home.header_image,
    expected_version: 3,
  })
  assert.equal(save?.url, '/api/store/my/home')
})

test('merchant avatar navigates to its shop and refuses an arbitrary avatar URL', async () => {
  await mount(
    <StoreMerchantIdentity
      seller={{ ...home.seller, avatar_url: 'javascript:alert(1)' }}
    />
  )
  assert.equal(document.querySelector('img'), null)
  assert.equal(
    document.querySelector('a[aria-label]')?.getAttribute('href'),
    '/store?seller_id=9'
  )
})

test('store announcement editor is hidden to ordinary merchants and appears for current administrators', async () => {
  owner()
  const sent = requests()
  await mount(<StoreAnnouncementSettings supported />)
  assert.equal(sent.length, 0)
  assert.equal(document.querySelector('#store-announcement'), null)
  await act(async () => {
    owner(10)
    await flush()
  })
  await act(async () => {
    await flush()
  })
  assert.ok(document.querySelector('#store-announcement'))
  await act(async () => {
    owner(1)
    await flush()
  })
  assert.equal(document.querySelector('#store-announcement'), null)
})
