/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

import type { ModelPricingConfig } from './model-pricing-api'
import type {
  ModelPricingEditorPanelHandle,
  ModelRatioData,
} from './model-pricing-sheet'

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
const { act, createRef } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { ModelPricingEditorPanel } = await import('./model-pricing-sheet')
const { ModelPricingUnitsContext } = await import('./model-pricing-units')
const { buildUsdPricingRequest, USD_PRICING_KEYS } =
  await import('./model-pricing-api')

;(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
after(() => domWindow.close())

function config(): ModelPricingConfig {
  return {
    schema_version: 2,
    currency: 'USD',
    storage_basis: 'legacy_pricing_unit',
    revision: 'a'.repeat(64),
    credits_per_usd: 3_600_000,
    legacy_pricing_units_per_usd: 7.2,
    model_ratio_usd_per_million: 1_000_000 / 3_600_000,
    values: Object.fromEntries(
      USD_PRICING_KEYS.map((key) => [key, '{}'])
    ) as ModelPricingConfig['values'],
  }
}

async function setup(
  data: ModelRatioData,
  locked = false,
  creditsPerUsd = 3_600_000
) {
  const ref = createRef<ModelPricingEditorPanelHandle>()
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () =>
    root.render(
      <I18nextProvider i18n={i18n}>
        <ModelPricingUnitsContext value={creditsPerUsd}>
          <ModelPricingEditorPanel ref={ref} editData={data} locked={locked} />
        </ModelPricingUnitsContext>
      </I18nextProvider>
    )
  )
  return {
    container,
    async change(selector: string, value: string) {
      const input = container.querySelector<HTMLInputElement>(selector)
      assert.ok(input)
      const setter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        'value'
      )?.set
      assert.ok(setter)
      await act(async () => {
        setter.call(input, value)
        input.dispatchEvent(new Event('input', { bubbles: true }))
      })
    },
    async commit() {
      let draft: ModelRatioData | null | undefined
      await act(async () => {
        draft = await ref.current?.commitDraft()
      })
      assert.ok(draft)
      return draft
    },
    async tryCommit() {
      let draft: ModelRatioData | null | undefined
      await act(async () => {
        draft = await ref.current?.commitDraft()
      })
      return draft
    },
    async cleanup() {
      await act(async () => root.unmount())
      container.remove()
    },
  }
}

test('fixed USD editor saves an explicit zero with the USD schema marker', async () => {
  const ctx = await setup({
    name: 'request',
    billingMode: 'per-request',
    price: '5',
  })
  try {
    assert.equal(
      ctx.container.querySelector<HTMLInputElement>('input[name="price"]')
        ?.value,
      '5'
    )
    assert.ok(ctx.container.textContent?.includes('USD'))
    await ctx.change('input[name="price"]', '0')
    const draft = await ctx.commit()
    assert.equal(draft.price, '0')
    const request = buildUsdPricingRequest(config(), {
      ModelPrice: JSON.stringify({ request: Number(draft.price) }),
    })
    assert.equal(request.currency, 'USD')
    assert.equal(request.schema_version, 2)
    assert.deepEqual(JSON.parse(request.values.ModelPrice), { request: 0 })
  } finally {
    await ctx.cleanup()
  }
})

test('unknown credit conversion renders no price input and cannot commit a draft', async () => {
  const ctx = await setup({ name: 'unknown', ratio: '7.2' }, false, Number.NaN)
  try {
    assert.equal(ctx.container.querySelector('input'), null)
    assert.ok(
      ctx.container.textContent?.includes('Failed to load USD model prices')
    )
    assert.equal(await ctx.tryCommit(), null)
  } finally {
    await ctx.cleanup()
  }
})

test('token USD editor saves raw fee ratios using K and keeps dependent USD prices', async () => {
  const ctx = await setup({
    name: 'tokens',
    billingMode: 'per-token',
    ratio: '7.2',
    completionRatio: '4',
  })
  try {
    assert.equal(
      ctx.container.querySelector<HTMLInputElement>('input[placeholder="3"]')
        ?.value,
      '2'
    )
    await ctx.change('input[placeholder="3"]', '3')
    const draft = await ctx.commit()
    assert.equal(draft.ratio, '10.8')
    assert.ok(
      Math.abs(Number(draft.ratio) * Number(draft.completionRatio) - 28.8) <
        1e-10
    )
  } finally {
    await ctx.cleanup()
  }
})

test('a locked editor returns the canonical saved snapshot without rewriting prices', async () => {
  const saved: ModelRatioData = {
    name: 'locked',
    billingMode: 'tiered_expr',
    billingExpr: '(tier("long", p * 7.2)) / 7.2',
    price: '0',
  }
  const ctx = await setup(saved, true)
  try {
    assert.equal(ctx.container.querySelector('fieldset')?.disabled, true)
    assert.equal(await ctx.commit(), saved)
  } finally {
    await ctx.cleanup()
  }
})
