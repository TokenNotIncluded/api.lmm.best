/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

import type { StoreCategory, StoreProduct } from './types'

const dom = new Window({ url: 'https://store.example.test/store/manage' })
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
const {
  StoreCategorySelect,
  StoreProductCategoryEditor,
  StoreCategoriesManager,
} = await import('./categories')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const original = { get: api.get, put: api.put, post: api.post }
const active: StoreCategory = {
  id: '90a24a5d-9b5b-4e2f-ae7d-cefbe58c9fba',
  name: '管理员创建的任意分类',
  sort_order: 2,
  active: true,
  created_at: 1,
  updated_at: 1,
}
const result = (data: unknown) => ({ data: { success: true, data } })
let root: ReturnType<typeof createRoot> | undefined
let client: InstanceType<typeof QueryClient> | undefined
async function render(node: React.ReactNode) {
  document.body.innerHTML = '<main></main>'
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client = queryClient
  const main = document.querySelector('main')
  assert.ok(main)
  const renderRoot = createRoot(main)
  root = renderRoot
  await act(async () => {
    renderRoot.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>{node}</I18nextProvider>
      </QueryClientProvider>
    )
  })
  await settle()
}
async function settle() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 40))
  })
}
afterEach(async () => {
  await act(async () => root?.unmount())
  root = undefined
  client?.clear()
  Object.assign(api, original)
})
after(() => dom.happyDOM.abort())

test('dynamic category choices preserve an inactive current reference and allow clearing', async () => {
  const changes: string[] = []
  await render(
    <StoreCategorySelect
      id='fixture-category'
      value='retained-id'
      current={{
        id: 'retained-id',
        name: 'Retained custom name',
        active: false,
      }}
      items={[active]}
      onChange={(value) => changes.push(value)}
    />
  )
  const select = document.querySelector('select')
  assert.ok(select)
  assert.equal(select.value, 'retained-id')
  assert.equal(select.options[1].disabled, true)
  assert.equal(select.options[2].text, active.name)
  await act(async () => {
    select.value = ''
    select.dispatchEvent(new dom.Event('change', { bubbles: true }))
  })
  assert.deepEqual(changes, [''])
})

test('published category save sends only classification and exposes recoverable errors', async () => {
  api.get = (async () =>
    result({
      supported: true,
      items: [active],
      offset: 0,
      limit: 100,
      has_more: false,
    })) as typeof api.get
  const requests: unknown[] = []
  api.put = (async (url: string, body: unknown) => {
    requests.push({ url, body })
    throw {
      response: { data: { success: false, message: 'Category was disabled' } },
    }
  }) as typeof api.put
  const product = {
    id: 'published-product',
    category_id: '',
    status: 'published',
  } as StoreProduct
  await render(<StoreProductCategoryEditor product={product} />)
  const select = document.querySelector('select')
  assert.ok(select)
  await act(async () => {
    select.value = active.id
    select.dispatchEvent(new dom.Event('change', { bubbles: true }))
  })
  await act(async () => {
    const form = document.querySelector('form')
    assert.ok(form)
    form.dispatchEvent(
      new dom.Event('submit', { bubbles: true, cancelable: true })
    )
  })
  await settle()
  assert.deepEqual(requests, [
    {
      url: '/api/store/products/published-product/category',
      body: { category_id: active.id },
    },
  ])
  assert.match(document.body.textContent ?? '', /Category was disabled/)
  const submit = document.querySelector('button')
  assert.ok(submit)
  assert.equal(submit.disabled, false)
})

test('administrator can disable and reenable an existing category without product deletion', async () => {
  let category = { ...active }
  api.get = (async () =>
    result({
      supported: true,
      items: [category],
      offset: 0,
      limit: 100,
      has_more: false,
    })) as typeof api.get
  const requests: unknown[] = []
  api.put = (async (url: string, body: StoreCategory) => {
    requests.push({ url, body })
    category = { ...category, ...body, updated_at: category.updated_at + 1 }
    return result(category)
  }) as typeof api.put
  await render(<StoreCategoriesManager />)
  const button = Array.from(document.querySelectorAll('button')).find(
    (item) => item.textContent === 'Disable category'
  )
  assert.ok(button)
  await act(async () => button.click())
  await settle()
  assert.match(document.body.textContent ?? '', /Inactive category/)
  const enableButton = Array.from(document.querySelectorAll('button')).find(
    (item) => item.textContent === 'Enable category'
  )
  assert.ok(enableButton)
  await act(async () => enableButton.click())
  await settle()
  assert.deepEqual(requests, [
    {
      url: `/api/store/admin/categories/${active.id}`,
      body: { name: active.name, sort_order: 2, active: false },
    },
    {
      url: `/api/store/admin/categories/${active.id}`,
      body: { name: active.name, sort_order: 2, active: true },
    },
  ])
})
