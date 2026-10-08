/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

const dom = new Window({ url: 'https://shop.example.test/store' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLImageElement',
  'Node',
  'Element',
  'Event',
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
const { StoreProductCardMedia } = await import('./product-card-media')
let root: ReturnType<typeof createRoot> | undefined
let host: HTMLElement

async function render(images: string[]) {
  if (!root) {
    host = document.createElement('div')
    document.body.append(host)
    root = createRoot(host)
  }
  await act(async () => {
    root!.render(
      <StoreProductCardMedia
        images={images}
        title='Product'
        list={false}
        href='/store/products/product'
      />
    )
  })
}
afterEach(async () => {
  await act(async () => root?.unmount())
  root = undefined
  document.body.innerHTML = ''
})
after(() => dom.happyDOM.close())

test('missing and rejected images leave no placeholder, image or empty link', async () => {
  for (const images of [[], ['', ''], ['javascript:alert(1)']]) {
    await render(images)
    assert.equal(host.innerHTML, '')
  }
})

test('failed header falls back to the logo and removes the entire region when both fail', async () => {
  const logo = 'https://images.example/logo.png'
  const header = 'https://images.example/header.png'
  await render([logo, header])
  assert.equal(host.querySelector('img')?.getAttribute('src'), header)
  await act(async () => {
    host.querySelector('img')!.dispatchEvent(new Event('error'))
  })
  assert.equal(host.querySelector('img')?.getAttribute('src'), logo)
  await act(async () => {
    host.querySelector('img')!.dispatchEvent(new Event('error'))
  })
  assert.equal(host.innerHTML, '')
  await render(['https://images.example/replacement.png'])
  assert.equal(
    host.querySelector('img')?.getAttribute('src'),
    'https://images.example/replacement.png'
  )
})
