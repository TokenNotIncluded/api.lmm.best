/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window({
  url: 'https://console.example.test/admin/users',
})
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
})

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { useSystemConfigStore, DEFAULT_CURRENCY_CONFIG } =
  await import('@/stores/system-config-store')
const { useWalletCurrencyPreferenceStore } =
  await import('@/stores/wallet-currency-preference-store')
const { ModerationGroupPolicyEditor } =
  await import('./moderation-group-policy-editor')
const globals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
globals.IS_REACT_ACT_ENVIRONMENT = true
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
after(() => domWindow.close())
afterEach(() => document.body.replaceChildren())
async function change(input: HTMLInputElement, text: string) {
  const setter = Object.getOwnPropertyDescriptor(
    HTMLInputElement.prototype,
    'value'
  )!.set!
  await act(async () => {
    setter.call(input, text)
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await new Promise((resolve) => setTimeout(resolve, 10))
  })
}
async function renderEditor(value: string, scale = 5) {
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...DEFAULT_CURRENCY_CONFIG,
      currencyUnit: 'credit',
      creditsPerUsd: scale * 500000,
      cnyPerUsd: 7.2,
      legacyPricingUnitsPerUsd: scale,
    },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  useWalletCurrencyPreferenceStore.getState().setPreference('USD')
  const changes: string[] = []
  const valid: boolean[] = []
  await act(async () =>
    root.render(
      <I18nextProvider i18n={i18n}>
        <ModerationGroupPolicyEditor
          value={value}
          groups={[]}
          onChange={(next) => changes.push(next)}
          onValidityChange={(next) => valid.push(next)}
        />
      </I18nextProvider>
    )
  )
  return { root, changes, valid }
}
test('editing one legacy category stores true USD and keeps every untouched group unchanged', async () => {
  const old = {
    alpha: {
      mode: 'strict',
      category_fines_usd: { harassment: 0.5, hate: 0.25 },
    },
    untouched: { mode: 'off', category_fines_usd: { hate: 0.7 } },
  }
  const rendered = await renderEditor(JSON.stringify(old))
  try {
    const input = document.querySelector<HTMLInputElement>(
      '#moderation-fine-0-0'
    )!
    assert.equal(input.value, '0.1')
    await change(input, '0.2')
    assert.deepEqual(JSON.parse(rendered.changes[0]), {
      alpha: {
        mode: 'strict',
        amount_currency: 'USD',
        category_fines_usd: { hate: 0.05, harassment: 0.2 },
      },
      untouched: old.untouched,
    })
    assert.equal(rendered.valid.at(-1), true)
  } finally {
    await act(async () => rendered.root.unmount())
  }
})
test('a nonrepresentable legacy fine or invalid new USD amount cannot emit a changed policy', async () => {
  for (const [scale, text] of [
    [3, '0.2'],
    [5, '-1'],
    [5, '1000.000001'],
    [5, '0.0000001'],
    [5, ''],
  ] as const) {
    const rendered = await renderEditor(
      JSON.stringify({
        alpha: {
          mode: 'strict',
          category_fines_usd: { harassment: 0.5, hate: 0.5 },
        },
      }),
      scale
    )
    try {
      await change(
        document.querySelector<HTMLInputElement>('#moderation-fine-0-0')!,
        text
      )
      assert.deepEqual(rendered.changes, [], `${scale}:${text}`)
      assert.equal(rendered.valid.at(-1), false)
      assert.ok(document.querySelector('[role="alert"]'))
    } finally {
      await act(async () => rendered.root.unmount())
      document.body.replaceChildren()
    }
  }
})
