/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

import type { ModelPricingConfig } from '@/features/system-settings/models/model-pricing-api'

import type { Model } from '../../types'

const domWindow = new Window({ url: 'https://console.example.test/' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'HTMLButtonElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'PointerEvent',
  'FocusEvent',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'localStorage',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { ModelMutateDrawer } = await import('./model-mutate-drawer')
const { api } = await import('@/lib/api')
const { USD_PRICING_KEYS, MODEL_PRICING_QUERY_KEY } =
  await import('@/features/system-settings/models/model-pricing-api')
;(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
after(() => domWindow.close())

test('model catalog drawer reads real USD and saves zero only through the versioned USD endpoint', async () => {
  const originals = { get: api.get, put: api.put, post: api.post }
  const model = {
    id: 1,
    model_name: 'request',
    status: 1,
    sync_official: 1,
  } as Model
  let config: ModelPricingConfig = {
    schema_version: 2,
    currency: 'USD',
    storage_basis: 'legacy_pricing_unit',
    revision: 'a'.repeat(64),
    credits_per_usd: 3_600_000,
    legacy_pricing_units_per_usd: 7.2,
    model_ratio_usd_per_million: 1_000_000 / 3_600_000,
    tool_price_defaults: {},
    values: {
      ...Object.fromEntries(USD_PRICING_KEYS.map((key) => [key, '{}'])),
      ModelPrice: '{"request":5}',
    } as ModelPricingConfig['values'],
  }
  const prices: Array<{
    schema_version: number
    currency: string
    expected_revision: string
    values: Record<string, string>
  }> = []
  api.get = (async (url: string) => {
    if (url === '/api/option/pricing') {
      return { data: { success: true, data: config } }
    }
    if (url === '/api/models/1') return { data: { success: true, data: model } }
    if (url === '/api/vendors/') {
      return { data: { success: true, data: { items: [] } } }
    }
    throw new Error(`Unexpected read ${url}`)
  }) as typeof api.get
  api.put = (async (url: string) => {
    assert.equal(url, '/api/models/')
    return { data: { success: true } }
  }) as typeof api.put
  api.post = (async (url: string, request: (typeof prices)[number]) => {
    assert.equal(url, '/api/option/pricing/bulk')
    prices.push(request)
    config = { ...config, values: { ...config.values, ...request.values } }
    return { data: { success: true, data: config } }
  }) as typeof api.post
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  client.setQueryData(MODEL_PRICING_QUERY_KEY, config)
  client.setQueryData(['models', 'detail', 1], { success: true, data: model })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  try {
    await act(async () =>
      root.render(
        <QueryClientProvider client={client}>
          <I18nextProvider i18n={i18n}>
            <ModelMutateDrawer
              open
              onOpenChange={() => {}}
              currentRow={model}
            />
          </I18nextProvider>
        </QueryClientProvider>
      )
    )
    const input = document.querySelector<HTMLInputElement>(
      'input[name="price"]'
    )
    assert.ok(input)
    assert.equal(input.value, '5')
    const setter = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      'value'
    )?.set
    assert.ok(setter)
    await act(async () => {
      setter.call(input, '0')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    const form = document.querySelector<HTMLFormElement>('#model-form')
    assert.ok(form)
    await act(async () => {
      form.dispatchEvent(
        new Event('submit', { bubbles: true, cancelable: true })
      )
    })
    assert.equal(prices.length, 1)
    assert.equal(prices[0].schema_version, 2)
    assert.equal(prices[0].currency, 'USD')
    assert.equal(prices[0].expected_revision, 'a'.repeat(64))
    assert.deepEqual(JSON.parse(prices[0].values.ModelPrice), { request: 0 })
  } finally {
    await act(async () => root.unmount())
    client.clear()
    container.remove()
    api.get = originals.get
    api.put = originals.put
    api.post = originals.post
  }
})
