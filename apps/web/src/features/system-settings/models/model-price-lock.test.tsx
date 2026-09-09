/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

import type { SystemOptionsResponse, UpdateOptionRequest } from '../types'

const domWindow = new Window({ url: 'https://console.example.test/' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
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
const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { toast } = await import('sonner')
const { ModelPriceLockButton } = await import('./model-price-lock')
const { parseModelPriceLocks, useModelPriceLocks } =
  await import('./use-model-price-locks')
const { ModelRatioVisualEditor } = await import('./model-ratio-visual-editor')
;(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
after(() => domWindow.close())
const emptyFields = {
  ModelPrice: '{}',
  ModelRatio: '{}',
  CacheRatio: '{}',
  CreateCacheRatio: '{}',
  CompletionRatio: '{}',
  ImageRatio: '{}',
  AudioRatio: '{}',
  AudioCompletionRatio: '{}',
  'billing_setting.billing_mode': '{}',
  'billing_setting.billing_expr': '{}',
}
function required<T>(value: T | null | undefined): T {
  assert.ok(value)
  return value
}
function options(locks = '{}', ratio = '{}'): SystemOptionsResponse {
  return {
    success: true,
    message: '',
    capabilities: { model_price_locks: true },
    data: Object.entries({
      ...emptyFields,
      ModelRatio: ratio,
      ModelPriceLock: locks,
    }).map(([key, value]) => ({ key, value })),
  }
}
function LockHarness() {
  const { locks, pending, toggle } = useModelPriceLocks()
  return (
    <ModelPriceLockButton
      name='source'
      locked={locks.source === true}
      pending={pending}
      onToggle={() => void toggle('source')}
    />
  )
}
async function setup(initial = options()) {
  const originals = { get: api.get, put: api.put, warning: toast.warning }
  const warnings: string[] = [],
    puts: UpdateOptionRequest[] = []
  let server = initial
  let beforePut: (() => Promise<void>) | undefined
  api.get = (async () => ({ data: server })) as typeof api.get
  api.put = (async (_url: string, request: UpdateOptionRequest) => {
    puts.push(request)
    if (beforePut) await beforePut()
    const locks = parseModelPriceLocks(
      server.data.find(({ key }) => key === 'ModelPriceLock')?.value
    )
    server = {
      ...server,
      data: [
        ...server.data.filter(({ key }) => key !== 'ModelPriceLock'),
        {
          key: 'ModelPriceLock',
          value: JSON.stringify({
            ...locks,
            [required(request.model)]: request.value,
          }),
        },
      ],
    }
    return { data: { success: true, message: '' } }
  }) as typeof api.put
  toast.warning = ((message: string) => {
    warnings.push(message)
    return 1
  }) as typeof toast.warning
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client.setQueryData(['system-options'], initial)
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  async function render(children = <LockHarness />) {
    await act(async () => {
      root.render(
        <QueryClientProvider client={client}>
          <I18nextProvider i18n={i18n}>{children}</I18nextProvider>
        </QueryClientProvider>
      )
    })
  }
  await render()
  return {
    container,
    client,
    puts,
    warnings,
    render,
    setServer(value: SystemOptionsResponse) {
      server = value
    },
    beforePut(callback: () => Promise<void>) {
      beforePut = callback
    },
    async cleanup() {
      await act(async () => root.unmount())
      client.clear()
      container.remove()
      api.get = originals.get
      api.put = originals.put
      toast.warning = originals.warning
    },
  }
}

test('default unlocked, accepting only true lock entries', () => {
  for (const value of [undefined, '', 'null', '[]', '{']) {
    assert.deepEqual(parseModelPriceLocks(value), {})
  }
  assert.deepEqual(parseModelPriceLocks('{"a":true,"b":false,"c":"true"}'), {
    a: true,
  })
})
test('atomic lock is retained across remounts', async () => {
  const ctx = await setup()
  try {
    assert.equal(
      ctx.container.querySelector('button')?.getAttribute('aria-pressed'),
      'false'
    )
    await act(async () =>
      required(ctx.container.querySelector('button')).click()
    )
    assert.deepEqual(ctx.puts, [
      { key: 'ModelPriceLock', model: 'source', value: true },
    ])
    await ctx.render(<div />)
    await ctx.render()
    assert.equal(
      ctx.container.querySelector('button')?.getAttribute('aria-pressed'),
      'true'
    )
  } finally {
    await ctx.cleanup()
  }
})
test('pending lock blocks repeated writes', async () => {
  const ctx = await setup()
  let resolve!: () => void
  ctx.beforePut(
    () =>
      new Promise<void>((done) => {
        resolve = done
      })
  )
  try {
    await act(async () =>
      required(ctx.container.querySelector('button')).click()
    )
    assert.equal(required(ctx.container.querySelector('button')).disabled, true)
    await act(async () =>
      required(ctx.container.querySelector('button')).click()
    )
    assert.equal(ctx.puts.length, 1)
    await act(async () => resolve())
    assert.equal(
      required(ctx.container.querySelector('button')).disabled,
      false
    )
  } finally {
    await ctx.cleanup()
  }
})
test('unlock writes false without touching another model', async () => {
  const ctx = await setup(options('{"source":true,"other":true}'))
  try {
    await act(async () =>
      required(ctx.container.querySelector('button')).click()
    )
    assert.deepEqual(ctx.puts, [
      { key: 'ModelPriceLock', model: 'source', value: false },
    ])
    const current = required(
      ctx.client.getQueryData<SystemOptionsResponse>(['system-options'])
    )
    assert.equal(
      parseModelPriceLocks(
        current.data.find(({ key }) => key === 'ModelPriceLock')?.value
      ).other,
      true
    )
  } finally {
    await ctx.cleanup()
  }
})
test('old servers and rolled-back capabilities warn without a write', async () => {
  for (const cachedCapability of [false, true]) {
    const initial = options()
    if (!cachedCapability) delete initial.capabilities
    const ctx = await setup(initial)
    try {
      const unsupported = options()
      delete unsupported.capabilities
      ctx.setServer(unsupported)
      await act(async () =>
        required(ctx.container.querySelector('button')).click()
      )
      assert.equal(ctx.puts.length, 0)
      assert.equal(ctx.warnings.length, 1)
      assert.match(ctx.warnings[0], /does not support price locks/)
    } finally {
      await ctx.cleanup()
    }
  }
})
test('stale unlocked display cannot unlock a remotely locked model', async () => {
  const ctx = await setup()
  try {
    ctx.setServer(options('{"source":true,"other":true}'))
    await act(async () =>
      required(ctx.container.querySelector('button')).click()
    )
    assert.equal(ctx.puts[0].value, true)
  } finally {
    await ctx.cleanup()
  }
})
function VisualHarness({
  onChange,
  draftRatio = '{"source":99,"target":3}',
}: {
  draftRatio?: string
  onChange: (field: string, value: string) => void
}) {
  const [fields, setFields] = useState({
    ...emptyFields,
    ModelRatio: draftRatio,
  })
  return (
    <ModelRatioVisualEditor
      savedModelPrice='{}'
      savedModelRatio='{"source":1,"target":3}'
      savedCacheRatio='{}'
      savedCreateCacheRatio='{}'
      savedCompletionRatio='{}'
      savedImageRatio='{}'
      savedAudioRatio='{}'
      savedAudioCompletionRatio='{}'
      savedBillingMode='{}'
      savedBillingExpr='{}'
      modelPrice={fields.ModelPrice}
      modelRatio={fields.ModelRatio}
      cacheRatio={fields.CacheRatio}
      createCacheRatio={fields.CreateCacheRatio}
      completionRatio={fields.CompletionRatio}
      imageRatio={fields.ImageRatio}
      audioRatio={fields.AudioRatio}
      audioCompletionRatio={fields.AudioCompletionRatio}
      billingMode={fields['billing_setting.billing_mode']}
      billingExpr={fields['billing_setting.billing_expr']}
      onSave={() => {}}
      isSaving={false}
      onChange={(field, value) => {
        setFields((previous) => ({ ...previous, [field]: value }))
        onChange(field, value)
      }}
    />
  )
}
test('locked editor shows saved prices and lock-only models remain reachable', async () => {
  const ctx = await setup(
    options('{"source":true,"orphan":true}', '{"source":1,"target":3}')
  )
  try {
    await ctx.render(<VisualHarness onChange={() => {}} />)
    assert.ok(
      ctx.container.querySelector('[aria-label="Unlock orphan pricing"]')
    )
    const source = required(
      [...ctx.container.querySelectorAll('tr')].find((row) =>
        row.textContent?.includes('source')
      )
    )
    await act(async () => source.click())
    assert.equal(ctx.container.querySelector('fieldset')?.disabled, true)
    assert.equal(
      ctx.container.querySelector<HTMLInputElement>(
        'input[inputmode="decimal"]'
      )?.value,
      '2'
    )
  } finally {
    await ctx.cleanup()
  }
})
test('bulk copy from locked source uses saved price and warns for locked targets', async () => {
  const ctx = await setup(options('{"source":true}', '{"source":1,"target":3}'))
  const changes: Record<string, string> = {}
  try {
    await ctx.render(
      <VisualHarness
        onChange={(field, value) => {
          changes[field] = value
        }}
      />
    )
    const rows = [...ctx.container.querySelectorAll('tr')]
    const source = required(
      rows.find((row) => row.textContent?.includes('source'))
    )
    const target = required(
      rows.find((row) => row.textContent?.includes('target'))
    )
    await act(async () => source.click())
    await act(async () =>
      (target.querySelector('[role="checkbox"]') as HTMLElement).click()
    )
    const copy = required(
      [...document.querySelectorAll('button')].find((button) =>
        button.textContent?.includes('Copy source pricing')
      )
    )
    assert.ok(copy)
    await act(async () => copy.click())
    assert.deepEqual(JSON.parse(changes.ModelRatio), { source: 1, target: 1 })
    assert.ok(
      ctx.warnings.some((warning) => warning.includes('Skipped locked models'))
    )
  } finally {
    await ctx.cleanup()
  }
})

test('locking refreshes concurrent prices and preserves other model drafts', async () => {
  const ctx = await setup(options('{}', '{"source":1,"target":3}'))
  ctx.beforePut(async () => {
    ctx.setServer(options('{}', '{"source":2,"target":3}'))
  })
  const changes: Record<string, string> = {}
  try {
    await ctx.render(
      <VisualHarness
        draftRatio='{"source":99,"target":77}'
        onChange={(field, value) => {
          changes[field] = value
        }}
      />
    )
    await act(async () =>
      required(
        ctx.container.querySelector<HTMLButtonElement>(
          '[aria-label="Lock source pricing"]'
        )
      ).click()
    )
    assert.deepEqual(JSON.parse(changes.ModelRatio), { source: 2, target: 77 })
    await act(async () =>
      required(
        ctx.container.querySelector<HTMLButtonElement>(
          '[aria-label="Unlock source pricing"]'
        )
      ).click()
    )
    const source = required(
      [...ctx.container.querySelectorAll('tr')].find((row) =>
        row.textContent?.includes('source')
      )
    )
    await act(async () => source.click())
    assert.equal(
      ctx.container.querySelector<HTMLInputElement>(
        'input[inputmode="decimal"]'
      )?.value,
      '4'
    )
  } finally {
    await ctx.cleanup()
  }
})
