/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const dom = new Window({ url: 'http://localhost/' })
dom.document.write('<!doctype html><html><body></body></html>')
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Element',
  'Node',
  'Event',
  'MutationObserver',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: key === 'window' ? dom : dom[key],
  })
}
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { initReactI18next } = await import('react-i18next')
await createInstance()
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const { IntegrationPrompt } = await import('./page')
const { PRICING_PROMPT, OAUTH_PROMPT } = await import('./integration-prompts')
after(() => dom.close())

test('one click copies the complete pricing integration instructions', async () => {
  let copied = ''
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: {
      writeText: async (value: string) => {
        copied = value
      },
    },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  try {
    await act(async () =>
      root.render(
        <IntegrationPrompt
          prompt={PRICING_PROMPT}
          label='Copy pricing prompt'
        />
      )
    )
    const copyButton = container.querySelector('button')
    assert.ok(copyButton)
    await act(async () => copyButton.click())
    assert.equal(copied, PRICING_PROMPT)
    assert.equal(container.querySelector('button')?.textContent, 'Copied')
    assert.equal(container.querySelector('[role="alert"]'), null)
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})

test('blocked clipboard opens and selects the OAuth prompt instead of claiming success', async () => {
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: undefined,
  })
  Object.defineProperty(document, 'execCommand', {
    configurable: true,
    value: () => false,
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  try {
    await act(async () =>
      root.render(
        <IntegrationPrompt prompt={OAUTH_PROMPT} label='Copy OAuth prompt' />
      )
    )
    const copyButton = container.querySelector('button')
    assert.ok(copyButton)
    await act(async () => copyButton.click())
    const field = container.querySelector('textarea')
    assert.ok(field)
    assert.equal(container.querySelector('details')?.open, true)
    assert.equal(field.value, OAUTH_PROMPT)
    assert.equal(field.selectionStart, 0)
    assert.equal(field.selectionEnd, OAUTH_PROMPT.length)
    assert.equal(
      container.querySelector('button')?.textContent,
      'Copy OAuth prompt'
    )
    assert.ok(container.querySelector('[role="alert"]'))
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})
