/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { Window } from 'happy-dom'

const dom = new Window({ url: 'https://console.example.test/' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'HTMLButtonElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'PointerEvent',
  'FocusEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
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
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { RedPackets } = await import('./index')

test('create button opens the actual dialog outside the layout slots', async () => {
  const i18n = createInstance()
  await i18n
    .use(initReactI18next)
    .init({ lng: 'en', resources: { en: { translation: {} } } })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const originalGet = api.get
  api.get = (async (url: string) => ({
    data: {
      success: true,
      data: url === '/api/red-packet/admin' ? [] : { items: [] },
    },
  })) as typeof api.get
  try {
    await act(async () => {
      root.render(
        <I18nextProvider i18n={i18n}>
          <QueryClientProvider client={client}>
            <RedPackets />
          </QueryClientProvider>
        </I18nextProvider>
      )
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    const button = [...container.querySelectorAll('button')].find((element) =>
      element.textContent?.includes('Create red packet')
    )
    assert.ok(button)
    assert.equal(document.querySelector('[role="dialog"]'), null)
    await act(async () => {
      button.click()
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
    const dialog = document.querySelector('[role="dialog"]')
    assert.ok(
      dialog,
      'clicking Create must render a dialog, not only update hidden state'
    )
    assert.match(dialog.textContent ?? '', /Choose existing codes/)
    assert.ok(dialog.querySelector('input'))
  } finally {
    await act(async () => root.unmount())
    api.get = originalGet
    client.clear()
    container.remove()
    await dom.happyDOM.abort()
  }
})
