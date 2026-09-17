/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

import type { SystemOptionsResponse } from '../types'
import { MODEL_PRICE_SNAPSHOT_KEYS } from './model-price-lock-snapshot'

const domWindow = new Window({ url: 'https://console.example.test/' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'Node',
  'Element',
  'Event',
  'MutationObserver',
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
const { api } = await import('@/lib/api')
const { toast } = await import('sonner')
const { useModelPriceLocks } = await import('./use-model-price-locks')
;(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
after(() => domWindow.close())

function pricing(locked: boolean) {
  return {
    ...Object.fromEntries(MODEL_PRICE_SNAPSHOT_KEYS.map((key) => [key, '{}'])),
    ModelPriceLock: JSON.stringify({ source: locked }),
    ModelPrice: '{"source":7}',
  }
}

function options(): SystemOptionsResponse {
  return {
    success: true,
    message: '',
    capabilities: { model_price_locks: true },
    data: Object.entries(pricing(false)).map(([key, value]) => ({ key, value })),
  }
}

async function harness(response: unknown, failReadAfterWrite = true) {
  const originals = { get: api.get, put: api.put, error: toast.error }
  const errors: string[] = []
  let reads = 0
  let writes = 0
  let latest: ReturnType<typeof useModelPriceLocks> | undefined
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const initial = options()
  client.setQueryData(['system-options'], initial)
  api.get = (async () => {
    reads++
    if (writes && failReadAfterWrite) throw new Error('read unavailable')
    return { data: initial }
  }) as typeof api.get
  api.put = (async () => {
    writes++
    client.setQueryData<SystemOptionsResponse>(['system-options'], {
      ...initial,
      data: [...initial.data, { key: 'Notice', value: 'new unrelated edit' }],
    })
    return { data: response }
  }) as typeof api.put
  toast.error = ((message: string) => {
    errors.push(message)
    return 1
  }) as typeof toast.error
  function Harness() {
    latest = useModelPriceLocks()
    return null
  }
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <QueryClientProvider client={client}>
        <I18nextProvider i18n={i18n}>
          <Harness />
        </I18nextProvider>
      </QueryClientProvider>
    )
  })
  return {
    client,
    errors,
    counts: () => ({ reads, writes }),
    async toggle() {
      let result: Awaited<
        ReturnType<ReturnType<typeof useModelPriceLocks>['toggle']>
      >
      await act(async () => {
        assert.ok(latest)
        result = await latest.toggle('source')
      })
      return result
    },
    async cleanup() {
      await act(async () => root.unmount())
      client.clear()
      container.remove()
      api.get = originals.get
      api.put = originals.put
      toast.error = originals.error
    },
  }
}

test('actual hook accepts committed pricing without a fallible read after PUT', async () => {
  const ctx = await harness({
    success: true,
    message: '',
    pricing: pricing(true),
  })
  try {
    const result = await ctx.toggle()
    assert.ok(result?.locked)
    assert.equal(ctx.counts().writes, 1)
    assert.equal(ctx.counts().reads, 1)
    assert.deepEqual(ctx.errors, [])
    assert.equal(
      result.options.find(({ key }) => key === 'ModelPriceLock')?.value,
      '{"source":true}'
    )
    assert.equal(
      result.options.find(({ key }) => key === 'Notice')?.value,
      'new unrelated edit'
    )
  } finally {
    await ctx.cleanup()
  }
})

test('actual hook invalidates unknown outcomes and never returns a successful draft', async () => {
  for (const response of [
    { success: true, message: '' },
    { success: true, message: '', pricing: null },
    { success: true, message: '', pricing: pricing(false) },
  ]) {
    const ctx = await harness(response)
    try {
      assert.equal(await ctx.toggle(), undefined)
      assert.equal(ctx.counts().writes, 1)
      assert.ok(ctx.errors.length > 0)
      assert.equal(
        ctx.client.getQueryState(['system-options'])?.isInvalidated,
        true
      )
    } finally {
      await ctx.cleanup()
    }
  }
})
