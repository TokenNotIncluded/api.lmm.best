/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

import type { StoreProduct } from './types'

const dom = new Window({ url: 'https://shop.example.test/store' })
// DOMPurify deliberately calls Node's native getter. Happy DOM's base getter
// returns an empty name for Elements, unlike a browser; forward to its actual
// Element getter so the real sanitizer can inspect tags. No sanitizer is mocked.
const nodeName = Object.getOwnPropertyDescriptor(
  dom.Node.prototype,
  'nodeName'
)?.get
const elementName = Object.getOwnPropertyDescriptor(
  dom.Element.prototype,
  'nodeName'
)?.get
const textName = Object.getOwnPropertyDescriptor(
  Object.getPrototypeOf(dom.Text.prototype),
  'nodeName'
)?.get
const commentName = Object.getOwnPropertyDescriptor(
  Object.getPrototypeOf(dom.Comment.prototype),
  'nodeName'
)?.get
assert.ok(nodeName && elementName && textName && commentName)
Object.defineProperty(dom.Node.prototype, 'nodeName', {
  configurable: true,
  get() {
    if (this instanceof dom.Element) return elementName.call(this)
    if (this instanceof dom.Text) return textName.call(this)
    if (this instanceof dom.Comment) return commentName.call(this)
    return nodeName.call(this)
  },
})
dom.document.write('<!doctype html><html><body></body></html>')
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
const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const {
  normalizeStoreImageSource,
  safeStoreMediaUrl,
  STORE_SVG_DATA_PREFIX,
  STORE_SVG_MAX_BYTES,
  storeImageEditorText,
} = await import('./product-media')
const { StoreProductMediaEditor } = await import('./product-media-editor')
const {
  storeProductMediaDraft,
  storeProductHeaderImage,
  storeProductGalleryImages,
} = await import('./product-media-fields')
const { StorePage } = await import('./store-page')
const { StoreProductPage } = await import('./product-page')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const originalGet = api.get
const svg =
  '<svg viewBox="0 0 48 48"><defs><linearGradient id="color"><stop offset="0" stop-color="#24c"/></linearGradient></defs><rect width="48" height="48" fill="url(#color)"/><text x="4" y="20">店铺</text></svg>'
const product: StoreProduct = {
  id: 'public-svg-product',
  seller_id: 9,
  title: 'Public image product',
  description: '',
  image_urls: [],
  contact: '',
  links: [],
  price_quota: 500000,
  template: 'card-key',
  delivery_strategy: 'sequential',
  payment_methods: ['balance'],
  pickup_login_required: true,
  pickup_code_required: false,
  email_pickup_link: false,
  status: 'published',
  official: false,
  available_stock: 2,
  promotion_expires_at: 0,
  created_at: 1,
  updated_at: 1,
  review_note: '',
}
let root: ReturnType<typeof createRoot> | undefined
let client: InstanceType<typeof QueryClient> | undefined
async function mount(node: React.ReactNode) {
  document.body.replaceChildren()
  const host = document.createElement('main')
  document.body.append(host)
  root = createRoot(host)
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client = queryClient
  await act(async () => {
    root?.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>{node}</I18nextProvider>
      </QueryClientProvider>
    )
  })
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 40))
  })
}
afterEach(async () => {
  await act(async () => root?.unmount())
  root = undefined
  client?.clear()
  api.get = originalGet
  useAuthStore.getState().auth.setUser(null)
})
after(async () => {
  await dom.happyDOM.close()
})

test('static SVG is purified into a standalone UTF-8 image with local gradients', () => {
  const image = normalizeStoreImageSource(svg)
  assert.ok(image?.startsWith(STORE_SVG_DATA_PREFIX))
  const decoded = storeImageEditorText(image)
  assert.match(decoded, /<svg[^>]+xmlns="http:\/\/www.w3.org\/2000\/svg"/)
  assert.match(decoded, /店铺/)
  assert.match(decoded, /url\(#color\)/)
  assert.equal(
    safeStoreMediaUrl(svg),
    undefined,
    'raw SVG must never become a render src'
  )
  assert.equal(safeStoreMediaUrl(image), image)
})

test('active, external and oversized media fail closed, including existing unsafe data URIs', () => {
  for (const source of [
    '<svg onload="evil()"/>',
    '<svg><script>evil()</script></svg>',
    '<svg><foreignObject><div>HTML</div></foreignObject></svg>',
    '<svg><image href="https://evil.example/x"/></svg>',
    '<svg><use href="#x"/></svg>',
    '<svg><animate attributeName="href"/></svg>',
    '<svg style="fill:red"/>',
    '<svg><rect fill="url(https://evil.example/x)"/></svg>',
    '<svg><rect fill="u\\72l(https://evil.example/x)"/></svg>',
    '<svg><rect fill="u/**/rl(//evil.example/x)"/></svg>',
    '<svg width="4097"/>',
    '<svg viewBox="0 0 9000 9000"/>',
    '<!DOCTYPE svg [<!ENTITY x SYSTEM "file:///etc/passwd">]><svg/>',
    '<?xml-stylesheet href="https://evil.example/x"?><svg/>',
    '<svg xmlns="https://evil.example/"/>',
    '<svg xml:base="https://evil.example/"/>',
    `<svg>${'<g>'.repeat(32)}${'</g>'.repeat(32)}</svg>`,
    `<svg><desc>${'x'.repeat(STORE_SVG_MAX_BYTES)}</desc></svg>`,
  ]) {
    assert.equal(
      normalizeStoreImageSource(source),
      undefined,
      source.slice(0, 80)
    )
    assert.equal(
      safeStoreMediaUrl(
        `${STORE_SVG_DATA_PREFIX}${Buffer.from(source).toString('base64')}`
      ),
      undefined
    )
  }
  for (const source of [
    'data:image/svg+xml,<svg/>',
    'javascript:evil()',
    'data:text/html;base64,PHN2Zy8+',
    STORE_SVG_DATA_PREFIX + 'PHN2\nZy8+',
  ]) {
    assert.equal(safeStoreMediaUrl(source), undefined)
  }
})

test('editing logo and header keeps independent roles and the complete legacy gallery', () => {
  const old = [
    'https://cdn.example.test/old.png',
    'http://legacy.example.test/two.jpg',
    'https://cdn.example.test/three.webp',
  ]
  assert.deepEqual(
    storeProductMediaDraft(old[0], old[1], old.slice(2).join('\n')),
    old
  )
  const image = normalizeStoreImageSource(svg)
  assert.ok(image)
  assert.deepEqual(
    storeProductMediaDraft(svg, old[1], old.slice(2).join('\n')),
    [image, ...old.slice(1)]
  )
  assert.equal(
    storeProductMediaDraft(
      '<svg onload="evil()"/>',
      old[1],
      old.slice(2).join('\n')
    ),
    undefined
  )
  assert.equal(
    storeProductMediaDraft(old[0], old[1], Array(31).fill(old[2]).join('\n')),
    undefined
  )
  assert.deepEqual(storeProductMediaDraft('', svg, old[2]), ['', image, old[2]])
  assert.deepEqual(storeProductMediaDraft(old[0], '', old[2]), [
    old[0],
    '',
    old[2],
  ])
  assert.equal(storeProductHeaderImage([old[0], image]), image)
  assert.equal(storeProductHeaderImage([image, '']), image)
  assert.deepEqual(storeProductGalleryImages([old[0], image, old[2]]), [
    old[0],
    old[2],
  ])
})

test('pasted SVG has an img preview and preserves the separate gallery text', async () => {
  function Editor() {
    const [logo, setLogo] = useState(svg)
    const [additional, setAdditional] = useState(
      'https://cdn.example.test/gallery.png'
    )
    return (
      <StoreProductMediaEditor
        logo={logo}
        header={svg}
        additional={additional}
        svgSupported
        onLogoChange={setLogo}
        onHeaderChange={() => {}}
        onAdditionalChange={setAdditional}
      />
    )
  }
  await mount(<Editor />)
  const image = document
    .querySelector('#store-logo-image')
    ?.parentElement?.querySelector('img')
  assert.ok(image?.src.startsWith(STORE_SVG_DATA_PREFIX))
  assert.equal(
    document.querySelectorAll('svg').length,
    0,
    'pasted markup is never injected'
  )
  assert.equal(
    document.querySelector<HTMLTextAreaElement>('#store-images')?.value,
    'https://cdn.example.test/gallery.png'
  )
  assert.match(
    document.querySelector('#store-logo-image')?.getAttribute('placeholder') ||
      '',
    /SVG/
  )
})

function publicRequests() {
  const image = normalizeStoreImageSource(svg)
  assert.ok(image)
  const visible = {
    ...product,
    image_urls: [image, 'https://cdn.example.test/gallery.png'],
  }
  const result = (data: unknown) => ({ data: { success: true, data } })
  api.get = (async (url: string) => {
    if (url === `/api/store/products/${product.id}`) return result(visible)
    if (url === '/api/store/products') {
      return result({ items: [visible], offset: 0, limit: 24, has_more: false })
    }
    if (url === '/api/store/config') {
      return result({
        fee_bps: 0,
        credits_per_usd: 500000,
        promotion_quota: 500000,
        disclaimer_version: 'merchant-store-v1',
        disclaimer_text: '',
        platform_payment_methods: [],
      })
    }
    return result({
      configured: false,
      items: [],
      count: null,
      liked: false,
      supported: false,
    })
  }) as typeof api.get
  useAuthStore.getState().auth.setUser(null)
  return visible
}

test('guest catalogue and seller storefront cards render the SVG main image', async () => {
  publicRequests()
  await mount(<StorePage sellerId={product.seller_id} />)
  const image = document.querySelector<HTMLImageElement>(
    'img[alt="Public image product"]'
  )
  assert.ok(image?.src.startsWith(STORE_SVG_DATA_PREFIX))
  assert.ok(document.querySelector(`a[href="/store/products/${product.id}"]`))
})

test('guest product detail renders the SVG and original gallery as images', async () => {
  publicRequests()
  await mount(<StoreProductPage id={product.id} />)
  const images = [
    ...document.querySelectorAll<HTMLImageElement>(
      'img[alt="Public image product"]'
    ),
  ]
  assert.equal(images.length, 2)
  assert.equal(images[0].src, 'https://cdn.example.test/gallery.png')
  assert.equal(images[0].getAttribute('data-store-media-role'), 'header')
  assert.ok(images[1].src.startsWith(STORE_SVG_DATA_PREFIX))
})
