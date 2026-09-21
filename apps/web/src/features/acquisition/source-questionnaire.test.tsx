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

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const dom = new Window({ url: 'https://console.example.test/' })
dom.document.write('<!doctype html><html><body></body></html>')
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Element',
  'Node',
  'Event',
  'MutationObserver',
  'localStorage',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: key === 'window' ? dom : dom[key],
  })
}
Object.assign(globalThis, {
  IS_REACT_ACT_ENVIRONMENT: true,
  __LMM_PERSONA_DEBUG__: false,
})
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { initReactI18next } = await import('react-i18next')
await createInstance()
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { SourceQuestionnaire } = await import('./source-questionnaire')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
after(() => dom.close())

test('source feedback is optional and a failed deletion remains retryable', async () => {
  const originalGet = api.get,
    originalDelete = api.delete
  const auth = useAuthStore.getState().auth
  auth.setUser({ id: 71 } as Parameters<typeof auth.setUser>[0])
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  let reads = 0,
    deletes = 0
  api.get = (async () => {
    reads++
    return {
      data: {
        success: true,
        data:
          deletes > 1
            ? null
            : { source: 'friend', detail: 'Tutorial', updated_at: 1 },
      },
    }
  }) as typeof api.get
  api.delete = (async () => {
    deletes++
    return { data: { success: deletes > 1 } }
  }) as typeof api.delete
  const button = (name: string) => {
    const value = [...container.querySelectorAll('button')].find(
      (x) => x.textContent === name
    )
    assert.ok(value)
    return value
  }
  const waitForButton = async (name: string) => {
    const deadline = Date.now() + 2000
    while (
      ![...container.querySelectorAll('button')].some(
        (value) => value.textContent === name
      ) &&
      Date.now() < deadline
    ) {
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 10))
      })
    }
    return button(name)
  }
  try {
    await act(async () =>
      root.render(
        <QueryClientProvider client={client}>
          <SourceQuestionnaire />
        </QueryClientProvider>
      )
    )
    assert.equal(reads, 0)
    await act(async () => {
      button('How did you first hear about LMM? (optional)').click()
    })
    await waitForButton('Skip')
    await act(async () => button('Skip').click())
    assert.equal(deletes, 0)
    await act(async () => {
      button('How did you first hear about LMM? (optional)').click()
    })
    await waitForButton('Delete')
    await act(async () => button('Delete').click())
    assert.ok(container.querySelector('[role="alert"]'))
    await act(async () => button('Delete').click())
    assert.equal(deletes, 2)
    assert.equal(container.querySelector('[role="alert"]'), null)
  } finally {
    await act(async () => root.unmount())
    client.clear()
    api.get = originalGet
    api.delete = originalDelete
    useAuthStore.setState({ auth })
    container.remove()
  }
})
