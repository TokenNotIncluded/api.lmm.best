/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

import type { StoreConfig, StoreLinkPreset, StoreProductInput } from './types'

const dom = new Window({
  url: 'https://shop.example.test/system-settings/integrations',
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
// This suite exercises editing and HTTP payloads; no Markdown is rendered here.
mock.module('@/components/ui/markdown', () => ({ Markdown: () => null }))
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { useAuthStore } = await import('@/stores/auth-store')
const { api } = await import('@/lib/api')
const { StoreProductEditor } = await import('./seller-page')
const { MerchantStoreSettingsSection } =
  await import('@/features/system-settings/integrations/merchant-store-settings-section')
const i18n = createInstance()
await i18n.use(initReactI18next).init({ lng: 'en', resources: {} })
const originalGet = api.get
const originalPost = api.post
const originalPut = api.put
const originalUser = useAuthStore.getState().auth.user
let root: ReturnType<typeof createRoot> | undefined
let client: InstanceType<typeof QueryClient> | undefined
const presets: StoreLinkPreset[] = [
  {
    id: 'custom-guide',
    title: 'Seller setup guide',
    url: 'https://guides.example.test/setup',
    description: 'Custom instructions',
  },
]
const config: StoreConfig = {
  fee_bps: 100,
  promotion_quota: 500000,
  minimum_unit_price_quota: 750000,
  product_link_presets: presets,
  disclaimer_version: 'merchant-store-v1',
  disclaimer_text: 'Terms',
  platform_payment_methods: [],
}
const result = (data: unknown) => ({ data: { success: true, data } })
async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 35))
}
function view(node: React.ReactNode) {
  assert.ok(client)
  return (
    <QueryClientProvider client={client}>
      <I18nextProvider i18n={i18n}>{node}</I18nextProvider>
    </QueryClientProvider>
  )
}
async function mount(node: React.ReactNode) {
  const host = document.createElement('div')
  document.body.append(host)
  const mounted = createRoot(host)
  root = mounted
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  await act(async () => {
    mounted.render(view(node))
    await flush()
  })
  await act(flush)
}
function button(text: string) {
  const node = [...document.querySelectorAll<HTMLButtonElement>('button')].find(
    (item) => item.textContent?.trim() === text
  )
  assert.ok(node, `button ${text}`)
  return node
}
function element<T extends HTMLElement>(selector: string) {
  const node = document.querySelector<T>(selector)
  assert.ok(node, selector)
  return node
}
function field(label: string) {
  const node = [...document.querySelectorAll<HTMLLabelElement>('label')].find(
    (item) => item.textContent?.trim() === label
  )
  assert.ok(node, `label ${label}`)
  const input = document.getElementById(node.htmlFor) as
    | HTMLInputElement
    | HTMLTextAreaElement
    | null
  assert.ok(input, `input ${label}`)
  return input
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
    const setter = Object.getOwnPropertyDescriptor(
      node.tagName === 'TEXTAREA'
        ? dom.HTMLTextAreaElement.prototype
        : dom.HTMLInputElement.prototype,
      'value'
    )?.set
    assert.ok(setter)
    setter.call(node, value)
    node.dispatchEvent(new Event('input', { bubbles: true }))
    await flush()
  })
}
async function select(node: HTMLSelectElement, value: string) {
  await act(async () => {
    node.value = value
    node.dispatchEvent(new Event('change', { bubbles: true }))
    await flush()
  })
}
afterEach(async () => {
  const mounted = root
  if (mounted) await act(async () => mounted.unmount())
  root = undefined
  client?.clear()
  api.get = originalGet
  api.post = originalPost
  api.put = originalPut
  useAuthStore.getState().auth.setUser(originalUser)
  document.body.replaceChildren()
})
after(() => dom.happyDOM.abort())

test('new products start with a blank price and preset copies stay editable and independent', async () => {
  let saved: StoreProductInput | undefined
  api.post = (async (url: string, body: StoreProductInput) => {
    assert.equal(url, '/api/store/products')
    saved = body
    return result({ id: 'new-product' })
  }) as typeof api.post
  const editor = (links: StoreLinkPreset[]) => (
    <StoreProductEditor
      minimumPriceQuota={750000}
      linkPresets={links}
      allowedMethods={[]}
      onClose={() => {}}
      onSaved={async () => {}}
    />
  )
  await mount(editor(presets))
  const price = element<HTMLInputElement>('#store-price')
  assert.equal(price.value, '')
  await input(element<HTMLInputElement>('#store-title'), 'My product')
  await click(button('Save draft'))
  assert.equal(Boolean(saved), false)
  const chooser = element<HTMLSelectElement>(
    'select[data-slot="native-select"]'
  )
  assert.equal(button('Add preset link').disabled, true)
  await select(chooser, 'custom-guide')
  await click(button('Add preset link'))
  const copiedTitle = element<HTMLInputElement>(
    'input[aria-label="Link title"]'
  )
  const copiedURL = element<HTMLInputElement>('input[aria-label="Link URL"]')
  const copiedDescription = element<HTMLInputElement>(
    'input[aria-label="Link description"]'
  )
  assert.equal(copiedTitle.value, presets[0].title)
  await input(copiedTitle, 'Seller-edited title')
  await input(copiedURL, 'http://seller.example.test/custom')
  await input(copiedDescription, 'Seller-edited instructions')
  const revised = [
    {
      ...presets[0],
      title: 'Admin replacement',
      url: 'https://other.example.test',
    },
  ]
  const mounted = root
  assert.ok(mounted)
  await act(async () => {
    mounted.render(view(editor(revised)))
    await flush()
  })
  assert.equal(copiedTitle.value, 'Seller-edited title')
  assert.equal(chooser.options[1].textContent, 'Admin replacement')
  await click(button('Add link'))
  const titles = document.querySelectorAll<HTMLInputElement>(
    'input[aria-label="Link title"]'
  )
  const urls = document.querySelectorAll<HTMLInputElement>(
    'input[aria-label="Link URL"]'
  )
  await input(titles[1], 'My own link')
  await input(urls[1], 'https://seller.example.test/help')
  await select(
    element<HTMLSelectElement>('select[aria-label="Price currency"]'),
    'CREDIT'
  )
  await input(price, '750000')
  await click(button('Save draft'))
  assert.ok(saved)
  assert.equal(saved.price_quota, 750000)
  assert.deepEqual(saved.links, [
    {
      title: 'Seller-edited title',
      url: 'http://seller.example.test/custom',
      description: 'Seller-edited instructions',
    },
    {
      title: 'My own link',
      url: 'https://seller.example.test/help',
      description: '',
    },
  ])
  assert.equal(presets[0].title, 'Seller setup guide')
})

for (const role of [1, 10]) {
  test(`global preset configuration is hidden from role ${role}`, async () => {
    let reads = 0
    api.get = (async () => {
      reads++
      return result(config)
    }) as typeof api.get
    useAuthStore.getState().auth.setUser({ id: 2, role, username: 'seller' })
    await mount(<MerchantStoreSettingsSection />)
    assert.equal(reads, 0)
    assert.equal(document.body.textContent, '')
  })
}

test('root config creates custom presets and refreshes the shared config query after save', async () => {
  useAuthStore.getState().auth.setUser({ id: 2, role: 100, username: 'root' })
  let reads = 0
  let current: StoreLinkPreset[] = []
  let saved: StoreLinkPreset[] | undefined
  api.get = (async (url: string) => {
    assert.equal(url, '/api/store/config')
    reads++
    return result({ ...config, product_link_presets: current })
  }) as typeof api.get
  api.put = (async (url: string, body: { presets: StoreLinkPreset[] }) => {
    assert.equal(url, '/api/store/product-link-presets')
    saved = body.presets
    current = body.presets
    return result(null)
  }) as typeof api.put
  await mount(<MerchantStoreSettingsSection />)
  assert.equal(document.querySelectorAll('input[type="url"]').length, 0)
  await click(button('Add link preset'))
  assert.equal(field('Link title').value, '')
  assert.equal(field('Link URL').value, '')
  await input(field('Link title'), 'My public support page')
  await input(field('Link URL'), 'https://support.example.test/custom')
  await input(field('Link description'), 'Any custom description')
  await click(button('Save link presets'))
  assert.ok(saved)
  assert.equal(saved.length, 1)
  assert.match(saved[0].id, /^[A-Za-z0-9_-]{1,64}$/)
  assert.deepEqual(
    { ...saved[0], id: '' },
    {
      id: '',
      title: 'My public support page',
      url: 'https://support.example.test/custom',
      description: 'Any custom description',
    }
  )
  assert.ok(reads >= 2, 'saving must invalidate the shared config query')
  assert.equal(field('Link title').value, 'My public support page')
  await click(button('Remove'))
  await click(button('Save link presets'))
  assert.deepEqual(saved, [])
})

test('an older config response does not expose unsupported preset writes', async () => {
  useAuthStore.getState().auth.setUser({ id: 2, role: 100, username: 'root' })
  const { product_link_presets: _, ...legacyConfig } = config
  api.get = (async () => result(legacyConfig)) as typeof api.get
  await mount(<MerchantStoreSettingsSection />)
  assert.match(document.body.textContent || '', /Store administration/)
  assert.equal(
    document.body.textContent?.includes('Product link presets'),
    false
  )
})
