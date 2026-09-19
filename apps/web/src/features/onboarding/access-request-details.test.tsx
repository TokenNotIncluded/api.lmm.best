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
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => {
  api.get = originalGet
  api.post = originalPost
  document.body.replaceChildren()
  useAuthStore.getState().auth.setUser(null)
})
after(() => domWindow.close())

describe('access application review', () => {
  test('shows rejection details and requires an explicit submission to retry with the AI recommendation retained', async () => {
    useAuthStore.getState().auth.setUser({
      id: 7,
      username: 'applicant',
      role: 1,
      developer_access_granted: false,
    })
    const application = {
      id: 9,
      status: 'rejected',
      reason: 'Build a personal research assistant',
      ai_recommendation:
        'The described application has a clear legitimate use.',
      admin_note: 'Please describe the expected usage.',
      created_at: 1700000000,
      reviewed_at: 1700000300,
    }
    let submissions = 0
    api.get = (async () => ({
      data: { success: true, data: application },
    })) as typeof api.get
    api.post = (async (_url: string, input: unknown) => {
      submissions++
      assert.deepEqual(input, {
        reason: application.reason,
        confirmed: true,
        ai_recommendation: application.ai_recommendation,
      })
      return {
        data: {
          success: true,
          data: {
            ...application,
            status: 'pending',
            admin_note: '',
            reviewed_at: 0,
          },
        },
      }
    }) as typeof api.post
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)
    const flush = () => new Promise((resolve) => setTimeout(resolve, 25))
    await act(async () => {
      root.render(
        <QueryClientProvider client={queryClient}>
          <I18nextProvider i18n={i18n}>
            <AccessRequestDetails />
          </I18nextProvider>
        </QueryClientProvider>
      )
      await flush()
    })
    await act(flush)
    assert.match(
      container.textContent ?? '',
      /Please describe the expected usage/
    )
    assert.match(container.textContent ?? '', /Review completed/)
    const button = (label: string) => {
      const found = [...container.querySelectorAll('button')].find(
        (node) => node.textContent === label
      )
      assert.ok(found)
      return found
    }
    await act(async () => {
      button('Revise access request').click()
      await flush()
    })
    assert.equal(submissions, 0)
    assert.equal(container.querySelector('textarea')?.value, application.reason)
    await act(async () => {
      button('Confirm and submit application').click()
      await flush()
    })
    await act(flush)
    assert.equal(submissions, 1)
    assert.match(container.textContent ?? '', /Pending review/)
    assert.doesNotMatch(
      container.textContent ?? '',
      /Please describe the expected usage/
    )
    await act(async () => root.unmount())
    queryClient.clear()
    container.remove()
  })
})
