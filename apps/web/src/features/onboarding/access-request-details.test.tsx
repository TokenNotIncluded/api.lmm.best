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
const { AccessRequestDetails } = await import('./access-request-details')
const { useAuthStore } = await import('@/stores/auth-store')

const originalGet = api.get
const originalPost = api.post
const rendered: Array<{
  root: ReturnType<typeof createRoot>
  client: InstanceType<typeof QueryClient>
}> = []
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(async () => {
  for (const { root, client } of rendered.splice(0)) {
    await act(async () => root.unmount())
    client.clear()
  }
  api.get = originalGet
  api.post = originalPost
  document.body.replaceChildren()
  useAuthStore.getState().auth.setUser(null)
})
after(() => domWindow.close())

const flush = () => new Promise((resolve) => setTimeout(resolve, 25))

function login() {
  useAuthStore.getState().auth.setUser({
    id: 7,
    username: 'applicant',
    role: 1,
    developer_access_granted: false,
  })
}

async function renderHistory(inline = false) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  rendered.push({ root, client })
  await act(async () => {
    root.render(
      <QueryClientProvider client={client}>
        <I18nextProvider i18n={i18n}>
          <AccessRequestDetails inline={inline} />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flush()
  })
  await act(flush)
  return container
}

describe('retired access application history', () => {
  for (const [status, label] of [
    ['pending', 'Pending review'],
    ['approved', 'Access request approved'],
    ['rejected', 'Access request rejected'],
  ] as const) {
    test(`keeps ${status} history readable without any application write action`, async () => {
      login()
      const application = {
        id: 9,
        status,
        reason: 'Build a personal research assistant',
        ai_recommendation:
          'The described application has a clear legitimate use.',
        admin_note: status === 'pending' ? '' : 'Historic review note.',
        created_at: 1700000000,
        reviewed_at: status === 'pending' ? 0 : 1700000300,
      }
      let reads = 0
      let writes = 0
      api.get = (async (url: string) => {
        assert.equal(url, '/api/user/developer-access/request')
        reads++
        return { data: { success: true, data: application } }
      }) as typeof api.get
      api.post = (async () => {
        writes++
        throw new Error('The retired application endpoint must not be called')
      }) as typeof api.post
      for (const inline of [false, true]) {
        const container = await renderHistory(inline)
        assert.match(container.textContent ?? '', new RegExp(label))
        assert.match(container.textContent ?? '', /Original request submitted/)
        assert.match(container.textContent ?? '', /2023/)
        assert.match(
          container.textContent ?? '',
          /Build a personal research assistant/
        )
        assert.match(
          container.textContent ?? '',
          /The described application has a clear legitimate use/
        )
        if (status === 'pending') {
          assert.doesNotMatch(container.textContent ?? '', /Review completed/)
        } else {
          assert.match(container.textContent ?? '', /Review completed/)
          assert.match(container.textContent ?? '', /Historic review note/)
        }
        assert.equal(
          container.querySelector('form, textarea, input, button'),
          null
        )
        assert.equal(
          container.querySelector('[data-testid="l0-direct-access-request"]'),
          null
        )
      }
      assert.equal(reads, 2)
      assert.equal(writes, 0)
    })
  }

  test('shows no-history state without offering the retired form', async () => {
    login()
    api.get = (async () => ({
      data: { success: true, data: null },
    })) as typeof api.get
    for (const inline of [false, true]) {
      const container = await renderHistory(inline)
      assert.match(container.textContent ?? '', /Not requested/)
      assert.equal(
        container.querySelector('form, textarea, input, button'),
        null
      )
      assert.doesNotMatch(
        container.textContent ?? '',
        /Request API access|Confirm and submit|Revise access request/
      )
    }
  })

  test('allows retrying a failed history GET without posting an application', async () => {
    login()
    let reads = 0
    let writes = 0
    api.get = (async (url: string) => {
      assert.equal(url, '/api/user/developer-access/request')
      if (++reads === 1) throw new Error('Temporary read failure')
      return { data: { success: true, data: null } }
    }) as typeof api.get
    api.post = (async () => {
      writes++
      throw new Error('The retired application endpoint must not be called')
    }) as typeof api.post
    const container = await renderHistory(true)
    assert.match(
      container.querySelector('[role="alert"]')?.textContent ?? '',
      /Unable to load access status/
    )
    const retry = container.querySelector('button')
    assert.ok(retry)
    assert.equal(retry.textContent, 'Retry')
    await act(async () => {
      retry.click()
      await flush()
    })
    await act(flush)
    assert.equal(reads, 2)
    assert.equal(writes, 0)
    assert.match(container.textContent ?? '', /Not requested/)
    assert.equal(container.querySelector('form, textarea, input, button'), null)
  })
})
