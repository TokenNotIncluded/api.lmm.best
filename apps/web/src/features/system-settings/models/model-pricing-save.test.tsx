/*
Copyright (C) 2026 LIghtJUNction
*/
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock as moduleMock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type { Root } from 'react-dom/client'
import type { UseFormReturn } from 'react-hook-form'

import type { ModelPricingConfig } from './model-pricing-api'
import type { modelPricingFormSnapshot } from './model-pricing-save'

type FormValues = ReturnType<typeof modelPricingFormSnapshot>
type FormProps = {
  form: UseFormReturn<FormValues>
  savedValues: FormValues
  onSave: (values: FormValues) => Promise<void>
}

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
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  value: true,
})

let formProps: FormProps
const warnings: Array<{ message: string; options?: { id?: string | number } }> =
  []
const errors: string[] = []
const errorIds: Array<string | number | undefined> = []
const infos: string[] = []
const successes: string[] = []
moduleMock.module('./model-ratio-form', () => ({
  ModelRatioForm: (props: FormProps) => {
    formProps = props
    return null
  },
}))
moduleMock.module('sonner', () => ({
  toast: {
    warning: (message: string, options?: { id?: string | number }) =>
      warnings.push({ message, options }),
    error: (message: string, options?: { id?: string | number }) => {
      errors.push(message)
      errorIds.push(options?.id)
    },
    info: (message: string) => infos.push(message),
    success: (message: string) => successes.push(message),
  },
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
const { modelPricingFormSnapshot: snapshot } =
  await import('./model-pricing-save')
const { RatioSettingsCard } = await import('./ratio-settings-card')
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
    credits_per_usd: 3_600_000,
    legacy_pricing_units_per_usd: 7.2,
    model_ratio_usd_per_million: 1_000_000 / 3_600_000,
    values: {
      ...Object.fromEntries(USD_PRICING_KEYS.map((key) => [key, '{}'])),
      ModelPrice: '{"locked":1.25,"free":5}',
      ModelPriceLock: '{"locked":true}',
    } as ModelPricingConfig['values'],
  }
}
const groupDefaults = {
  GroupRatio: '{}',
  TopupGroupRatio: '{}',
  UserUsableGroups: '{}',
  GroupGroupRatio: '{}',
  AutoGroups: '[]',
  MaxTokenAutoGroups: 4,
  DefaultUseAutoGroup: false,
  GroupSpecialUsableGroup: '{}',
  GroupWarnings: '{}',
}
function serviceError() {
  return Object.assign(new Error('Request failed with status code 503'), {
    response: {
      status: 503,
      data: {
        error: {
          code: 'service_temporarily_unavailable',
          message: 'edge service unavailable',
        },
      },
    },
  })
}

let root: Root
let container: HTMLDivElement
let queryClient: InstanceType<typeof QueryClient>
let current: ModelPricingConfig
let failRead = false
let failWrite = false
const gets: Array<{ url: string; options?: unknown }> = []
const posts: Array<{ url: string; body: unknown; options?: unknown }> = []
const originals = { get: api.get, post: api.post }

beforeEach(async () => {
  current = fixture()
  failRead = false
  failWrite = false
  gets.length =
    posts.length =
    warnings.length =
    errors.length =
    infos.length =
    successes.length =
      0
  errorIds.length = 0
  api.get = (async (url: string, options?: unknown) => {
    gets.push({ url, options })
    if (failRead) {
      // The accepted receipt must be visible before any read can fail.
      assert.equal(
        queryClient.getQueryData<ModelPricingConfig>(MODEL_PRICING_QUERY_KEY)
          ?.revision,
        'b'.repeat(64)
      )
      assert.deepEqual(JSON.parse(formProps.form.getValues('ModelPrice')), {
        locked: 1.25,
        free: 0,
      })
      throw serviceError()
    }
    return {
      data:
        url === '/api/option/pricing'
          ? { success: true, data: current }
          : {
              success: true,
              message: '',
              data: [{ key: 'ExposeRatioEnabled', value: 'false' }],
            },
    }
  }) as typeof api.get
  api.post = (async (url: string, body: unknown, options?: unknown) => {
    posts.push({ url, body, options })
    if (failWrite) throw serviceError()
    current = fixture('b')
    // The lock was applied by the server: the submitted locked=99 must never
    // become the saved form value. Zero remains a deliberate accepted price.
    current.values.ModelPrice = '{"locked":1.25,"free":0}'
    return { data: { success: true, data: current, locked_models: ['locked'] } }
  }) as typeof api.post
  queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: Infinity },
      mutations: { retry: false },
    },
  })
  queryClient.setQueryData(MODEL_PRICING_QUERY_KEY, current)
  container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
  await act(async () =>
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <RatioSettingsCard
            modelDefaults={snapshot(current, false)}
            groupDefaults={groupDefaults}
            toolPricesDefault='{}'
            visibleTabs={['models']}
          />
        </I18nextProvider>
      </QueryClientProvider>
    )
  )
  gets.length = 0
})
afterEach(async () => {
  await act(async () => root.unmount())
  queryClient.clear()
  container.remove()
  api.get = originals.get
  api.post = originals.post
})
after(() => domWindow.close())

test('POST 200 then refresh 503 accepts the locked canonical receipt and cannot repeat the write', async () => {
  failRead = true
  await act(async () =>
    formProps.form.setValue('ModelPrice', '{"locked":99,"free":0}', {
      shouldDirty: true,
    })
  )
  await act(async () => formProps.onSave(formProps.form.getValues()))
  assert.equal(posts.length, 1)
  assert.equal(posts[0]?.url, '/api/option/pricing/bulk')
  assert.deepEqual(posts[0]?.options, {
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  assert.deepEqual(posts[0]?.body, {
    schema_version: 2,
    currency: 'USD',
    expected_revision: 'a'.repeat(64),
    values: { ModelPrice: '{"locked":99,"free":0}' },
  })
  assert.equal(
    queryClient.getQueryData<ModelPricingConfig>(MODEL_PRICING_QUERY_KEY)
      ?.revision,
    'b'.repeat(64)
  )
  assert.deepEqual(JSON.parse(formProps.form.getValues('ModelPrice')), {
    locked: 1.25,
    free: 0,
  })
  assert.deepEqual(JSON.parse(formProps.savedValues.ModelPrice), {
    locked: 1.25,
    free: 0,
  })
  assert.equal(formProps.form.formState.isDirty, false)
  assert.equal(errors.length, 0)
  assert.equal(successes.length, 0)
  assert.equal(warnings.length, 1)
  assert.match(
    warnings[0]?.message ?? '',
    /^Settings saved, but refreshing failed: .+/
  )
  assert.match(
    warnings[0]?.message ?? '',
    /The service is temporarily unavailable/
  )
  assert.doesNotMatch(
    warnings[0]?.message ?? '',
    /Update the server|USD pricing schema/
  )
  assert.equal(warnings[0]?.options?.id, 'service-temporarily-unavailable')
  assert.deepEqual(
    gets.map(({ url }) => url).sort(),
    ['/api/option/', '/api/option/pricing'].sort()
  )
  for (const read of gets) {
    assert.deepEqual(read.options, {
      skipBusinessError: true,
      skipErrorHandler: true,
    })
  }
  await act(async () => formProps.onSave(formProps.form.getValues()))
  assert.equal(posts.length, 1)
  assert.deepEqual(infos, ['No model price changes to save'])
})

test('POST 503 remains a save error without accepting drafts, reloading, or retrying the write', async () => {
  failWrite = true
  const acceptedAt = queryClient.getQueryState(
    MODEL_PRICING_QUERY_KEY
  )?.dataUpdatedAt
  await act(async () =>
    formProps.form.setValue('ModelPrice', '{"locked":99,"free":0}', {
      shouldDirty: true,
    })
  )
  await act(async () =>
    assert.rejects(formProps.onSave(formProps.form.getValues()), /503/)
  )
  assert.equal(posts.length, 1)
  assert.equal(gets.length, 0)
  assert.equal(errors.length, 1)
  assert.match(errors[0] ?? '', /The service is temporarily unavailable/)
  assert.deepEqual(errorIds, ['service-temporarily-unavailable'])
  assert.equal(warnings.length, 0)
  assert.equal(successes.length, 0)
  assert.equal(
    queryClient.getQueryData<ModelPricingConfig>(MODEL_PRICING_QUERY_KEY)
      ?.revision,
    'a'.repeat(64)
  )
  assert.equal(
    queryClient.getQueryState(MODEL_PRICING_QUERY_KEY)?.dataUpdatedAt,
    acceptedAt
  )
  assert.deepEqual(JSON.parse(formProps.savedValues.ModelPrice), {
    locked: 1.25,
    free: 5,
  })
  assert.deepEqual(JSON.parse(formProps.form.getValues('ModelPrice')), {
    locked: 99,
    free: 0,
  })
})
