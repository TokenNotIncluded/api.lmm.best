/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

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
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { TieredPricingEditor } = await import('./tiered-pricing-editor')
const {
  generateExprFromVisualConfig,
  normalizeVisualTier,
  tryParseVisualConfig,
} = await import('@/features/pricing/lib/tier-expr')

;(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
after(() => domWindow.close())

function required<T>(value: T | null | undefined): T {
  assert.ok(value)
  return value
}

function fixtureExpr() {
  return generateExprFromVisualConfig({
    tiers: [
      normalizeVisualTier({
        label: 'base',
        input_unit_cost: 2,
        output_unit_cost: 3,
        cache_read_unit_cost: 0.1,
        cache_text_read_unit_cost: 0.2,
        cache_image_read_unit_cost: 0.3,
        cache_audio_read_unit_cost: 0.4,
        audio_duration_unit_cost: 100,
      }),
    ],
  })
}

async function setup(initialExpr = fixtureExpr()) {
  let currentExpr = initialExpr
  const changes: string[] = []
  function Harness() {
    const [expr, setExpr] = useState(initialExpr)
    return (
      <TieredPricingEditor
        modelName='test-audio-model'
        billingExpr={expr}
        requestRuleExpr=''
        onBillingExprChange={(next) => {
          currentExpr = next
          changes.push(next)
          setExpr(next)
        }}
        onRequestRuleExprChange={() => {}}
      />
    )
  }
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <Harness />
      </I18nextProvider>
    )
  })
  function input(labelText: string): HTMLInputElement {
    const label = required(
      [...container.querySelectorAll<HTMLLabelElement>('label')].find(
        (element) => element.textContent === labelText
      )
    )
    assert.ok(label.htmlFor, `${labelText} must name its input`)
    const control = required(document.getElementById(label.htmlFor))
    assert.ok(control instanceof HTMLInputElement)
    return control
  }
  return {
    container,
    changes,
    input,
    expression: () => currentExpr,
    tier: () => required(tryParseVisualConfig(currentExpr)).tiers[0],
    async change(label: string, value: string) {
      const control = input(label)
      const setter = required(
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')
          ?.set
      )
      await act(async () => {
        setter.call(control, value)
        control.dispatchEvent(new Event('input', { bubbles: true }))
      })
    },
    async cleanup() {
      await act(async () => root.unmount())
      container.remove()
    },
  }
}

test('separate cache prices preserve the generic cache price and other values', async () => {
  const ctx = await setup()
  try {
    assert.deepEqual(ctx.changes, [])
    assert.equal(ctx.input('Cache read price').value, '0.1')
    assert.equal(ctx.input('Text cache read price').value, '0.2')
    assert.equal(ctx.input('Image cache read price').value, '0.3')
    assert.equal(ctx.input('Audio cache read price').value, '0.4')

    await ctx.change('Text cache read price', '0.25')
    assert.equal(ctx.tier().cache_text_read_unit_cost, 0.25)
    assert.equal(ctx.tier().cache_read_unit_cost, 0.1)
    assert.equal(ctx.tier().cache_image_read_unit_cost, 0.3)
    assert.equal(ctx.tier().cache_audio_read_unit_cost, 0.4)
    assert.equal(ctx.tier().audio_duration_unit_cost, 100)
    assert.equal(ctx.tier().input_unit_cost, 2)
    assert.equal(ctx.tier().output_unit_cost, 3)
  } finally {
    await ctx.cleanup()
  }
})

test('audio duration price displays USD per minute and round-trips its coefficient', async () => {
  const ctx = await setup()
  let updatedExpr = ''
  try {
    const duration = ctx.input('Audio duration price')
    assert.equal(duration.value, '0.006')
    const hint = required(duration.getAttribute('aria-describedby'))
    assert.equal(required(document.getElementById(hint)).textContent, '$/min')

    await ctx.change('Audio duration price', '0.012')
    assert.equal(ctx.tier().audio_duration_unit_cost, 200)
    assert.equal(ctx.tier().cache_read_unit_cost, 0.1)
    assert.equal(ctx.tier().cache_text_read_unit_cost, 0.2)
    updatedExpr = ctx.expression()
  } finally {
    await ctx.cleanup()
  }

  const restored = await setup(updatedExpr)
  try {
    assert.deepEqual(restored.changes, [])
    assert.equal(restored.input('Audio duration price').value, '0.012')
    assert.equal(restored.tier().audio_duration_unit_cost, 200)
  } finally {
    await restored.cleanup()
  }
})

test('estimator maps separate cache counts and decimal audio seconds independently', async () => {
  const ctx = await setup()
  try {
    assert.equal(ctx.input('Audio duration (seconds)').step, '0.01')
    await ctx.change('Cache Read', '4000')
    await ctx.change('Text Cache Read', '1000')
    await ctx.change('Image Cache Read', '2000')
    await ctx.change('Audio Cache Read', '3000')
    await ctx.change('Audio duration (seconds)', '60.5')

    assert.ok(
      ctx.container.textContent?.includes('Cache classification is unavailable')
    )
    await ctx.change('Cache Read', '6000')

    assert.equal(ctx.input('Cache Read').value, '6000')
    assert.equal(ctx.input('Text Cache Read').value, '1000')
    assert.equal(ctx.input('Image Cache Read').value, '2000')
    assert.equal(ctx.input('Audio Cache Read').value, '3000')
    assert.equal(ctx.input('Audio duration (seconds)').value, '60.5')
    assert.ok(
      ctx.container.textContent?.includes(
        `Estimated quota cost: ${(8650).toLocaleString()}`
      )
    )
    assert.deepEqual(ctx.changes, [])
  } finally {
    await ctx.cleanup()
  }
})
