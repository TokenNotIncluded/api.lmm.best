/*
Copyright (C) 2026 LIghtJUNction
*/

import assert from 'node:assert/strict'
import { after, describe, test } from 'node:test'

import { Window } from 'happy-dom'

import type {
  ModelRatioData,
  ModelPricingEditorPanelHandle,
} from './model-pricing-sheet'

const domWindow = new Window({ url: 'https://console.example.test/settings' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'HTMLLabelElement',
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
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
Object.defineProperties(globalThis, {
  requestAnimationFrame: {
    configurable: true,
    value: (callback: FrameRequestCallback) => setTimeout(() => callback(0), 0),
  },
  cancelAnimationFrame: {
    configurable: true,
    value: (handle: number) => clearTimeout(handle),
  },
  getComputedStyle: {
    configurable: true,
    value: domWindow.getComputedStyle.bind(domWindow),
  },
  IS_REACT_ACT_ENVIRONMENT: { configurable: true, value: true },
})

const { act, createRef } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { toast } = await import('sonner')
const { ModelPricingEditorPanel } = await import('./model-pricing-sheet')

const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })

async function renderPanel(editData: ModelRatioData, isLocked?: boolean) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const ref = createRef<ModelPricingEditorPanelHandle>()
  let toggleCount = 0
  const render = async (locked?: boolean) => {
    await act(async () => {
      root.render(
        <I18nextProvider i18n={i18n}>
          <ModelPricingEditorPanel
            ref={ref}
            editData={editData}
            isLocked={locked}
            onToggleLock={() => {
              toggleCount += 1
            }}
          />
        </I18nextProvider>
      )
    })
  }
  await render(isLocked)
  return {
    container,
    ref,
    render,
    toggleCount: () => toggleCount,
    cleanup: async () => {
      await act(async () => root.unmount())
      container.remove()
    },
  }
}

after(() => domWindow.close())

describe('model pricing editor lock protection', () => {
  for (const data of [
    {
      name: 'token-model',
      ratio: '1.5',
      completionRatio: '2',
      cacheRatio: '0',
      billingMode: 'per-token',
    },
    { name: 'request-model', price: '0.01', billingMode: 'per-request' },
  ] satisfies ModelRatioData[]) {
    test(`keeps ${data.billingMode} fields read-only while allowing explicit unlock`, async () => {
      const panel = await renderPanel(data, true)
      try {
        const fieldset = panel.container.querySelector('fieldset')
        assert.ok(fieldset)
        assert.equal(fieldset.disabled, true)
        assert.ok(
          fieldset.querySelector('input'),
          'The saved pricing must remain visible'
        )
        const lockButton = panel.container.querySelector<HTMLButtonElement>(
          'button[aria-label="Unlock model prices"]'
        )
        assert.ok(lockButton)
        assert.equal(lockButton.disabled, false)
        assert.equal(fieldset.contains(lockButton), false)
        await act(async () => lockButton.click())
        assert.equal(panel.toggleCount(), 1)
        assert.deepEqual(await panel.ref.current?.commitDraft(), data)
      } finally {
        await panel.cleanup()
      }
    })
  }

  test('renders locked billing and request expressions without an editable code control', async () => {
    const data: ModelRatioData = {
      name: 'expression-model',
      billingMode: 'tiered_expr',
      billingExpr: 'tier("base", p * 2 + c * 4)',
      requestRuleExpr: 'requests * 0.01',
    }
    const panel = await renderPanel(data, true)
    try {
      assert.equal(panel.container.querySelector('fieldset')?.disabled, true)
      assert.equal(
        panel.container.querySelector('pre')?.textContent,
        `${data.billingExpr}\n${data.requestRuleExpr}`
      )
      assert.equal(
        panel.container.querySelector('textarea, [contenteditable="true"]'),
        null
      )
      assert.deepEqual(await panel.ref.current?.commitDraft(), data)
    } finally {
      await panel.cleanup()
    }
  })

  test('allows edits by default, then discards a pending price edit when the model becomes locked', async () => {
    const data: ModelRatioData = { name: 'request-model', price: '0.01' }
    const panel = await renderPanel(data)
    const originalWarning = toast.warning
    const warnings: unknown[] = []
    toast.warning = ((message: unknown) => {
      warnings.push(message)
      return 0
    }) as typeof toast.warning
    try {
      assert.equal(panel.container.querySelector('fieldset')?.disabled, false)
      const input = panel.container.querySelector<HTMLInputElement>(
        'input[name="price"]'
      )
      assert.ok(input)
      const setValue = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        'value'
      )?.set
      assert.ok(setValue)
      await act(async () => {
        setValue.call(input, '0.5')
        input.dispatchEvent(new Event('input', { bubbles: true }))
        input.dispatchEvent(new Event('change', { bubbles: true }))
      })
      let draft: ModelRatioData | null = null
      const handle = panel.ref.current
      assert.ok(handle)
      await act(async () => {
        draft = await handle.commitDraft()
      })
      assert.equal((draft as ModelRatioData | null)?.price, '0.5')

      await panel.render(true)
      assert.deepEqual(await panel.ref.current?.commitDraft(), data)
      await act(async () => {
        input.dispatchEvent(
          new PointerEvent('pointerdown', { bubbles: true, cancelable: true })
        )
      })
      assert.deepEqual(warnings, [
        'Locked model prices were preserved; changes were ignored.',
      ])
    } finally {
      toast.warning = originalWarning
      await panel.cleanup()
    }
  })
})
