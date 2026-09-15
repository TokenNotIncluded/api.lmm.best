/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { Window } from 'happy-dom'

const dom = new Window()
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
  'CustomEvent',
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
const { CCSwitchDialog } = await import('../dialogs/cc-switch-dialog')

test('owner grants and revokes balance access without importing or exposing a key', async () => {
  const i18n = createInstance()
  await i18n
    .use(initReactI18next)
    .init({ lng: 'en', resources: { en: { translation: {} } } })
  const originalAdapter = api.defaults.adapter
  let enabled = false
  const mutations: unknown[] = []
  api.defaults.adapter = async (config) => {
    let data: unknown
    if (config.url === '/api/token/42' && config.method === 'get') {
      data = { success: true, data: { account_balance_read: enabled } }
    } else if (
      config.url === '/api/token/42/account-balance-access' &&
      config.method === 'put'
    ) {
      const body = JSON.parse(config.data)
      mutations.push(body)
      enabled = body.enabled
      data = { success: true }
    } else if (config.method === 'get') {
      data = { success: true, data: [] }
    } else {
      throw new Error('Unexpected mutation')
    }
    return { data, status: 200, statusText: 'OK', headers: {}, config }
  }
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => {
      root.render(
        <QueryClientProvider client={client}>
          <I18nextProvider i18n={i18n}>
            <CCSwitchDialog
              open
              onOpenChange={() => {}}
              tokenId={42}
              tokenKey='sk-fixture-secret'
            />
          </I18nextProvider>
        </QueryClientProvider>
      )
    })
    const wait = async () => {
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 50))
      })
    }
    await wait()
    const toggle = document.querySelector<HTMLElement>('[data-slot="switch"]')
    assert.ok(toggle)
    assert.equal(toggle.hasAttribute('data-checked'), false)
    assert.equal(mutations.length, 0)
    for (const value of [true, false]) {
      await act(async () => toggle.click())
      await wait()
      assert.deepEqual(mutations.at(-1), { enabled: value })
      assert.equal(toggle.hasAttribute('data-checked'), value)
    }
    assert.ok(!document.body.textContent?.includes('sk-fixture-secret'))
  } finally {
    await act(async () => root.unmount())
    client.clear()
    api.defaults.adapter = originalAdapter
    host.remove()
    dom.close()
  }
})
