/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import assert from 'node:assert/strict'
import { after, afterEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window({ url: 'https://console.example.test/pricing' })
domWindow.document.write(
  '<!doctype html><html><head></head><body></body></html>'
)
Object.defineProperty(domWindow.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'history',
  'location',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'SVGElement',
  'customElements',
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
  'scrollTo',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
Object.defineProperty(globalThis, 'matchMedia', {
  configurable: true,
  value: (media: string) => ({
    matches: false,
    media,
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent() {
      return false
    },
  }),
})

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { CreatedApiKey } = await import('./created-api-key')

const originalDelete = api.delete
const originalFetch = globalThis.fetch
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => {
  api.delete = originalDelete
  globalThis.fetch = originalFetch
  document.body.replaceChildren()
})
after(() => domWindow.close())

describe('one-time creation result', () => {
  test('masks the key, checks only after a click, and confirms revocation', async () => {
    let checks = 0,
      deletes = 0,
      closed = 0
    globalThis.fetch = (async (_url: unknown, options: RequestInit) => {
      checks++
      assert.equal(_url, '/v1/models')
      assert.equal(options.credentials, 'omit')
      assert.equal(
        (options.headers as Record<string, string>).Authorization,
        'Bearer sk-test-secret'
      )
      return new Response(JSON.stringify({ data: [] }), { status: 200 })
    }) as typeof fetch
    api.delete = (async (url: string) => {
      assert.equal(url, '/api/token/9/')
      deletes++
      return { data: { success: true } }
    }) as typeof api.delete
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const flush = () => new Promise((resolve) => setTimeout(resolve, 25))
    await act(async () => {
      root.render(
        <QueryClientProvider client={client}>
          <I18nextProvider i18n={i18n}>
            <CreatedApiKey
              secret={{ id: 9, name: 'First device', key: 'test-secret' }}
              onClose={() => {
                closed++
              }}
              onRevoked={() => {}}
            />
          </I18nextProvider>
        </QueryClientProvider>
      )
      await flush()
    })
    const button = (label: string) => {
      const node = [...document.querySelectorAll('button')].find(
        (element) => element.textContent === label
      )
      assert.ok(node)
      return node
    }
    assert.equal(
      document.querySelector<HTMLInputElement>('input[aria-label="API Key"]')
        ?.type,
      'password'
    )
    assert.equal(checks, 0)
    assert.equal(client.getQueryCache().getAll().length, 0)
    await act(async () => {
      button('Check key connection').click()
      await flush()
    })
    assert.equal(checks, 1)
    assert.match(
      document.body.textContent ?? '',
      /No model request was sent or billed/
    )
    await act(async () => {
      button('Revoke this key').click()
      await flush()
    })
    assert.equal(deletes, 0)
    await act(async () => {
      button('Confirm revocation').click()
      await flush()
    })
    assert.equal(deletes, 1)
    assert.equal(closed, 1)
    await act(async () => root.unmount())
    client.clear()
    container.remove()
  })
})
