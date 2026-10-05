/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
// @ts-expect-error Bun exposes module mocking only in the test runtime.
import { mock as moduleMock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type { Root } from 'react-dom/client'

import type { ChannelSelectorDialog } from './channel-selector-dialog'
import type { ConflictConfirmDialog } from './conflict-confirm-dialog'
import type { ModelPricingConfig } from './model-pricing-api'
import type { UpstreamRatioSyncTable } from './upstream-ratio-sync-table'

type TableProps = Parameters<typeof UpstreamRatioSyncTable>[0]
type ChannelProps = Parameters<typeof ChannelSelectorDialog>[0]
type ConflictProps = Parameters<typeof ConflictConfirmDialog>[0]
const dom = new Window({ url: 'https://console.example.test/' })
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
    value: dom[key],
  })
}
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  value: true,
})
let table: TableProps
let channels: ChannelProps
let conflicts: ConflictProps
moduleMock.module('./upstream-ratio-sync-table', () => ({
  UpstreamRatioSyncTable: (props: TableProps) => {
    table = props
    return null
  },
}))
moduleMock.module('./channel-selector-dialog', () => ({
  ChannelSelectorDialog: (props: ChannelProps) => {
    channels = props
    return null
  },
}))
moduleMock.module('./conflict-confirm-dialog', () => ({
  ConflictConfirmDialog: (props: ConflictProps) => {
    conflicts = props
    return null
  },
}))
const notices: Array<{ kind: string; message: string; id?: string | number }> =
  []
moduleMock.module('sonner', () => ({
  toast: Object.fromEntries(
    ['loading', 'success', 'warning', 'error'].map((kind) => [
      kind,
      (message: string, options?: { id?: string | number }) => {
        notices.push({ kind, message, id: options?.id })
        return 'sync-toast'
      },
    ])
  ),
}))
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider, notifyManager } =
  await import('@tanstack/react-query')
const { default: i18n } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { MODEL_PRICING_QUERY_KEY, USD_PRICING_KEYS } =
  await import('./model-pricing-api')
const { UpstreamRatioSync } = await import('./upstream-ratio-sync')
notifyManager.setScheduler(queueMicrotask)
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
function fixture(revision = 'a'): ModelPricingConfig {
  return {
    schema_version: 2,
    currency: 'USD',
    storage_basis: 'legacy_pricing_unit',
    revision: revision.repeat(64),
    credits_per_usd: 3_359_744,
    legacy_pricing_units_per_usd: 6.719488,
    model_ratio_usd_per_million: 1_000_000 / 3_359_744,
    values: {
      ...Object.fromEntries(USD_PRICING_KEYS.map((key) => [key, '{}'])),
      ModelPrice: '{"untouched":1.25,"locked":2}',
      ModelRatio: '{"target":6.719488}',
      'billing_setting.billing_expr': '{"untouched":"v1:p * 1.25"}',
    } as ModelPricingConfig['values'],
  }
}
function httpError(status: number) {
  return Object.assign(new Error(`Request failed with status code ${status}`), {
    response: {
      status,
      data: {
        message:
          status === 409
            ? 'pricing revision conflict'
            : 'pricing temporarily unavailable',
      },
    },
  })
}
let root: Root
let container: HTMLDivElement
let queryClient: InstanceType<typeof QueryClient>
let current: ModelPricingConfig
let fetchReply: Record<string, unknown>
let failWrite = 0
let failRead = false
let lockModels: string[] = []
let releaseWrite: (() => void) | undefined
let deferWrite = false
const posts: Array<{ url: string; body: unknown; options?: unknown }> = []
const originals = { get: api.get, post: api.post }
beforeEach(async () => {
  current = fixture()
  fetchReply = {
    pricing_config: fixture(),
    differences: {
      target: {
        model_ratio: {
          current: 6.719488,
          upstreams: { source: 3.359744 },
          confidence: {},
        },
        completion_ratio: {
          current: null,
          upstreams: { source: 0 },
          confidence: {},
        },
      },
      free: {
        model_price: {
          current: null,
          upstreams: { source: 0 },
          confidence: {},
        },
      },
      locked: {
        model_price: { current: 2, upstreams: { source: 99 }, confidence: {} },
      },
    },
    test_results: [{ name: 'source', status: 'success' }],
  }
  failWrite = 0
  failRead = false
  lockModels = []
  deferWrite = false
  releaseWrite = undefined
  posts.length = notices.length = 0
  api.get = (async (url: string) => {
    if (url === '/api/ratio_sync/channels') {
      return {
        data: {
          success: true,
          data: [
            {
              id: 1,
              name: 'source',
              base_url: 'https://reference.example.test',
              status: 1,
            },
          ],
        },
      }
    }
    if (failRead) {
      assert.equal(
        queryClient.getQueryData<ModelPricingConfig>(MODEL_PRICING_QUERY_KEY)
          ?.revision,
        'b'.repeat(64)
      )
      throw httpError(503)
    }
    return {
      data:
        url === '/api/option/pricing'
          ? { success: true, data: current }
          : { success: true, data: [] },
    }
  }) as typeof api.get
  api.post = (async (url: string, body: unknown, options?: unknown) => {
    posts.push({ url, body, options })
    if (url === '/api/ratio_sync/fetch') {
      return { data: { success: true, data: fetchReply } }
    }
    assert.equal(url, '/api/option/pricing/bulk')
    if (deferWrite) {
      await new Promise<void>((resolve) => {
        releaseWrite = resolve
      })
    }
    if (failWrite) throw httpError(failWrite)
    assert.equal(
      (body as { expected_revision: string }).expected_revision.length,
      64
    )
    if (
      (body as { expected_revision: string }).expected_revision !==
      current.revision
    ) {
      throw httpError(409)
    }
    const values = (body as { values: Record<string, string> }).values
    current = {
      ...current,
      revision: 'b'.repeat(64),
      values: { ...current.values, ...values },
    }
    if (lockModels.length) {
      current.values.ModelPrice = '{"untouched":1.25,"locked":2,"free":0}'
    }
    return { data: { success: true, data: current, locked_models: lockModels } }
  }) as typeof api.post
  queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: Infinity },
      mutations: { retry: false },
    },
  })
  queryClient.setQueryData(MODEL_PRICING_QUERY_KEY, fixture('c'))
  container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
  await act(async () =>
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <UpstreamRatioSync />
        </I18nextProvider>
      </QueryClientProvider>
    )
  )
})
afterEach(async () => {
  await act(async () => root.unmount())
  queryClient.clear()
  container.remove()
  api.get = originals.get
  api.post = originals.post
})
after(() => dom.close())
function button(label: string) {
  const result = [...container.querySelectorAll('button')].find((element) =>
    element.textContent?.includes(label)
  )
  assert.ok(result, label)
  return result
}
async function fetchQuotes() {
  await act(async () => button('Select Sync Channels').click())
  await act(async () => {
    channels.onConfirm([1])
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
  assert.equal(table.pricingConfig?.revision, 'a'.repeat(64))
}
async function selectBulk(includeLocked = false) {
  await act(async () =>
    table.onSelectValues([
      {
        model: 'target',
        ratioType: 'model_ratio',
        value: 3.359744,
        sourceName: 'source',
      },
      {
        model: 'target',
        ratioType: 'completion_ratio',
        value: 0,
        sourceName: 'source',
      },
      {
        model: 'free',
        ratioType: 'model_price',
        value: 0,
        sourceName: 'source',
      },
      ...(includeLocked
        ? [
            {
              model: 'locked',
              ratioType: 'model_price' as const,
              value: 99,
              sourceName: 'source',
            },
          ]
        : []),
    ])
  )
}
async function apply() {
  await act(async () => {
    button('Apply Sync').click()
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
}
function writes() {
  return posts.filter((post) => post.url === '/api/option/pricing/bulk')
}

test('bulk apply uses fetch CAS snapshot, preserves unselected canonical USD, and replaces the loading toast', async () => {
  await fetchQuotes()
  await selectBulk()
  await apply()
  assert.equal(writes().length, 1)
  const body = writes()[0].body as {
    schema_version: number
    currency: string
    expected_revision: string
    values: Record<string, string>
  }
  assert.equal(body.schema_version, 2)
  assert.equal(body.currency, 'USD')
  assert.equal(body.expected_revision, 'a'.repeat(64))
  assert.deepEqual(JSON.parse(body.values.ModelPrice), {
    untouched: 1.25,
    locked: 2,
    free: 0,
  })
  assert.deepEqual(JSON.parse(body.values.ModelRatio), { target: 3.359744 })
  assert.deepEqual(JSON.parse(body.values.CompletionRatio), { target: 0 })
  assert.equal(body.values['billing_setting.billing_expr'], undefined)
  assert.equal(body.values.ModelPriceLock, undefined)
  assert.equal(
    posts.some((post) => post.url === '/api/option/bulk'),
    false
  )
  assert.deepEqual(writes()[0].options, {
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  assert.deepEqual(table.resolutions, {})
  assert.equal(notices.at(-1)?.kind, 'success')
  assert.equal(notices.at(-1)?.id, 'sync-toast')
  assert.equal(notices.filter((notice) => notice.kind === 'loading').length, 1)
})

test('a post-save 503 retains accepted receipt and reports a warning without a duplicate write', async () => {
  await fetchQuotes()
  await selectBulk()
  failRead = true
  await apply()
  assert.equal(writes().length, 1)
  assert.equal(
    queryClient.getQueryData<ModelPricingConfig>(MODEL_PRICING_QUERY_KEY)
      ?.revision,
    'b'.repeat(64)
  )
  assert.deepEqual(table.resolutions, {})
  assert.equal(notices.at(-1)?.kind, 'warning')
  assert.equal(notices.at(-1)?.id, 'sync-toast')
  assert.match(notices.at(-1)?.message ?? '', /saved, but refreshing failed/)
})

for (const status of [409, 503]) {
  test(`write ${status} keeps selections and disables stale replay until a fresh fetch`, async () => {
    await fetchQuotes()
    await selectBulk()
    const before = table.resolutions
    if (status === 409) {
      current = fixture('d')
      current.values.ModelPrice = '{"untouched":9,"locked":2}'
    } else failWrite = status
    await apply()
    assert.deepEqual(table.resolutions, before)
    assert.equal(writes().length, 1)
    assert.equal(button('Apply Sync').disabled, true)
    assert.equal(
      queryClient.getQueryData<ModelPricingConfig>(MODEL_PRICING_QUERY_KEY)
        ?.revision,
      'c'.repeat(64)
    )
    assert.equal(notices.at(-1)?.kind, 'error')
    assert.equal(notices.at(-1)?.id, 'sync-toast')
    assert.match(container.textContent ?? '', /Fetch upstream prices again/)
  })
}

test('locked models keep unapplied choices and display authoritative server prices', async () => {
  await fetchQuotes()
  await selectBulk(true)
  lockModels = ['locked']
  await apply()
  assert.deepEqual(table.resolutions, { locked: { model_price: 99 } })
  assert.ok(table.pricingConfig)
  assert.equal(JSON.parse(table.pricingConfig.values.ModelPrice).locked, 2)
  assert.equal(table.differences.locked.model_price?.current, 2)
  assert.equal(notices.at(-1)?.kind, 'warning')
  assert.match(notices.at(-1)?.message ?? '', /locked/)
})

test('same-tick double apply submits exactly one POST and disables changes for the entire operation', async () => {
  await fetchQuotes()
  await selectBulk()
  deferWrite = true
  await act(async () => {
    const element = button('Apply Sync')
    element.click()
    element.click()
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
  assert.equal(writes().length, 1)
  assert.equal(table.isDisabled, true)
  assert.equal(button('Apply Sync').disabled, true)
  await act(async () => {
    assert.ok(releaseWrite)
    releaseWrite()
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
  assert.equal(writes().length, 1)
  assert.equal(table.isDisabled, false)
})

test('old fetch API without canonical snapshot cannot enable sync or fall back to generic bulk', async () => {
  delete fetchReply.pricing_config
  await act(async () => button('Select Sync Channels').click())
  await act(async () => {
    channels.onConfirm([1])
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
  assert.equal(table.pricingConfig, undefined)
  assert.equal(button('Apply Sync').disabled, true)
  assert.equal(writes().length, 0)
  assert.match(notices.at(-1)?.message ?? '', /Upgrade the server/)
})

test('tiered-to-token confirmation clears prior expression and applies the exact reviewed selection once', async () => {
  const config = fetchReply.pricing_config as ModelPricingConfig
  config.values['billing_setting.billing_mode'] = '{"target":"tiered_expr"}'
  config.values['billing_setting.billing_expr'] =
    '{"target":"v1:p * 2.5","untouched":"v1:p * 1.25"}'
  await fetchQuotes()
  await selectBulk()
  await apply()
  assert.equal(conflicts.open, true)
  assert.equal(writes().length, 0)
  assert.match(conflicts.conflicts[0].current, /v1:p \* 2.5/)
  assert.match(conflicts.conflicts[0].newVal, /USD 1 \/1M/)
  await act(async () => {
    conflicts.onConfirm()
    conflicts.onConfirm()
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
  assert.equal(writes().length, 1)
  const values = (writes()[0].body as { values: Record<string, string> }).values
  assert.deepEqual(JSON.parse(values['billing_setting.billing_mode']), {
    target: 'ratio',
    free: 'ratio',
  })
  assert.deepEqual(JSON.parse(values['billing_setting.billing_expr']), {
    untouched: 'v1:p * 1.25',
  })
  assert.equal(conflicts.open, false)
})

test('unsupported source models remain excluded and their skip reasons stay visible', async () => {
  fetchReply.test_results = [
    {
      name: 'models.dev',
      status: 'success',
      source_providers: { skipped: 'openai' },
      skipped_models: { skipped: 'unsupported modality' },
    },
  ]
  await fetchQuotes()
  assert.match(container.textContent ?? '', /Skipped pricing entries \(1\)/)
  assert.match(
    container.textContent ?? '',
    /models.dev · skipped: unsupported modality/
  )
  assert.equal(table.differences.skipped, undefined)
})

test('same-tick duplicate channel confirmation fetches once', async () => {
  await act(async () => button('Select Sync Channels').click())
  await act(async () => {
    channels.onConfirm([1])
    channels.onConfirm([1])
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
  assert.equal(
    posts.filter((post) => post.url === '/api/ratio_sync/fetch').length,
    1
  )
})

test('an explicit uncertain price selection warns instead of presenting it as a verified price', async () => {
  const diffs = fetchReply.differences as TableProps['differences']
  assert.ok(diffs.target.model_ratio)
  diffs.target.model_ratio.confidence.source = false
  await fetchQuotes()
  await act(async () =>
    table.onSelectValue('target', 'model_ratio', 3.359744, 'source')
  )
  assert.equal(notices.at(-1)?.kind, 'warning')
  assert.match(notices.at(-1)?.message ?? '', /unreliable/)
  assert.deepEqual(table.resolutions, { target: { model_ratio: 3.359744 } })
})

for (const initial of ['fixed', 'tiered'] as const) {
  test(`${initial} billing cannot be switched by selecting only an accessory ratio`, async () => {
    const config = fetchReply.pricing_config as ModelPricingConfig
    if (initial === 'fixed') {
      config.values.ModelPrice = '{"target":0,"untouched":1.25}'
    } else {
      config.values['billing_setting.billing_mode'] = '{"target":"tiered_expr"}'
    }
    await fetchQuotes()
    await act(async () =>
      table.onSelectValue('target', 'completion_ratio', 0, 'source')
    )
    await apply()
    assert.equal(writes().length, 0)
    assert.equal(conflicts.open, false)
    assert.equal(notices.at(-1)?.kind, 'error')
    assert.match(notices.at(-1)?.message ?? '', /complete upstream billing/)
    assert.deepEqual(table.resolutions, { target: { completion_ratio: 0 } })
  })
}

test('existing ratio billing can sync an accessory alone without resending the base price', async () => {
  await fetchQuotes()
  await act(async () =>
    table.onSelectValue('target', 'completion_ratio', 0, 'source')
  )
  await apply()
  assert.equal(writes().length, 1)
  const values = (writes()[0].body as { values: Record<string, string> }).values
  assert.deepEqual(JSON.parse(values.CompletionRatio), { target: 0 })
  assert.equal(values.ModelRatio, undefined)
  assert.equal(values.ModelPrice, undefined)
})
