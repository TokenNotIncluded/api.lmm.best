/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

const dom = new Window({
  url: 'https://shop.test/store/claim/secret?guest=private',
})
dom.document.write('<!doctype html><html><body></body></html>')
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
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'matchMedia',
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
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { StoreProductXShareActions } = await import('./product-x-share-actions')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const product = {
  id: 'public-product',
  title: 'Useful product',
  description: '<p>Public description</p>',
  status: 'published',
  visibility: 'public',
  image_urls: ['https://images.example.test/cover.png'],
}
let root: ReturnType<typeof createRoot> | undefined
const originalFetch = globalThis.fetch
afterEach(async () => {
  await act(async () => root?.unmount())
  root = undefined
  document.body.replaceChildren()
  globalThis.fetch = originalFetch
})
after(() => dom.happyDOM.abort())
async function mount(value = product) {
  const host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  await act(async () =>
    root?.render(
      <I18nextProvider i18n={i18n}>
        <StoreProductXShareActions product={value} />
      </I18nextProvider>
    )
  )
  return host
}

test('visible X action opens a user-confirmed composer without fetching images or exposing pickup credentials', async () => {
  let calls = 0
  globalThis.fetch = async () => {
    calls++
    throw new Error('Unexpected fetch')
  }
  const host = await mount()
  const anchor = host.querySelector<HTMLAnchorElement>('a')
  assert.ok(anchor)
  assert.equal(anchor.textContent?.trim(), 'Share on X')
  assert.equal(anchor.target, '_blank')
  assert.equal(anchor.rel, 'noopener noreferrer')
  const intent = new URL(anchor.href)
  assert.equal(
    intent.searchParams.get('text'),
    'Useful product\nPublic description'
  )
  assert.equal(
    intent.searchParams.get('url'),
    'https://shop.test/store/products/public-product'
  )
  assert.ok(!anchor.href.includes('private') && !anchor.href.includes('secret'))
  assert.equal(calls, 0)
})

test('private and test products render neither public title, share links nor image actions', async () => {
  for (const value of [
    { ...product, visibility: 'private' },
    { ...product, status: 'draft' },
    { ...product, test_mode: true },
  ]) {
    const host = await mount(value)
    assert.equal(host.textContent, '')
    assert.equal(host.querySelector('a,button'), null)
    await act(async () => root?.unmount())
    root = undefined
  }
})

test('real CORS download failure keeps X usable and offers truthful manual image saving', async () => {
  globalThis.fetch = async () => {
    throw new TypeError('Failed to fetch')
  }
  const host = await mount()
  const button = [...host.querySelectorAll<HTMLButtonElement>('button')].find(
    (node) => node.textContent?.includes('Download product image')
  )
  assert.ok(button)
  await act(async () => {
    button.click()
    await new Promise((resolve) => setTimeout(resolve, 20))
  })
  assert.match(
    host.querySelector('[role=status]')?.textContent ?? '',
    /Image download unavailable/
  )
  const imageLink = [...host.querySelectorAll<HTMLAnchorElement>('a')].find(
    (node) => node.textContent === 'Open image'
  )
  assert.ok(imageLink)
  assert.equal(imageLink.href, product.image_urls[0])
  assert.ok(host.querySelector('a[href^="https://x.com/intent/"]'))
  assert.equal(button.disabled, false)
})
