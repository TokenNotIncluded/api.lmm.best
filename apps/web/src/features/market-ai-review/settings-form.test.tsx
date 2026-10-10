/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { Window } from 'happy-dom'

const window = new Window({ url: 'https://console.example.test/settings' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'HTMLSelectElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: window[key],
  })
}
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  value: true,
})
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { MarketAIReviewSettingsForm } = await import('./settings-form')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })

test('market modes remain independent and failed saves preserve the editable draft', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const updates: unknown[] = []
  await act(async () =>
    root.render(
      <I18nextProvider i18n={i18n}>
        <MarketAIReviewSettingsForm
          defaultValues={{ tool: 'off', product: 'off' }}
          onSave={async (value) => {
            updates.push(value)
            throw new Error('Synthetic save failure')
          }}
        />
      </I18nextProvider>
    )
  )
  const tool = container.querySelector<HTMLSelectElement>(
    '#market-ai-mode-tool'
  )
  const product = container.querySelector<HTMLSelectElement>(
    '#market-ai-mode-product'
  )
  assert.ok(tool)
  assert.ok(product)
  assert.equal(tool.value, 'off')
  assert.equal(product.value, 'off')
  await act(async () => {
    product.value = 'assist'
    product.dispatchEvent(new Event('change', { bubbles: true }))
  })
  assert.equal(tool.value, 'off')
  await act(async () => {
    const form = container.querySelector('form')
    assert.ok(form)
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
  })
  assert.deepEqual(updates, [{ product: 'assist' }])
  assert.equal(product.value, 'assist')
  assert.equal(product.disabled, false)
  assert.match(
    container.querySelector('[role=alert]')?.textContent ?? '',
    /Synthetic save failure/
  )
  await act(async () => root.unmount())
  container.remove()
  window.happyDOM.abort()
})
