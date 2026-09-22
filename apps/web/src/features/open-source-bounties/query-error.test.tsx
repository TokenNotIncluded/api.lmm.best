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

const domWindow = new Window()
domWindow.document.write(
  '<!doctype html><html><head></head><body></body></html>'
)
Object.defineProperty(domWindow.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
const originalGlobals = new Map<string, PropertyDescriptor | undefined>()
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
] as const) {
  originalGlobals.set(key, Object.getOwnPropertyDescriptor(globalThis, key))
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { OpenSourceBounties } = await import('./index')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
originalGlobals.set(
  'IS_REACT_ACT_ENVIRONMENT',
  Object.getOwnPropertyDescriptor(globalThis, 'IS_REACT_ACT_ENVIRONMENT')
)
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
after(() => {
  domWindow.close()
  for (const [key, descriptor] of originalGlobals) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor)
    else Reflect.deleteProperty(globalThis, key)
  }
})

test('bounty board distinguishes failed requests, retry, cached data and successful empty results', async () => {
  const originalAdapter = api.defaults.adapter
  const originalAuth = useAuthStore.getState().auth
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  let response: 'error' | 'empty' | 'items' | 'business' | 'network' = 'error'
  let release: (() => void) | undefined
  let hold = false
  const requests: string[] = []
  const project = {
    id: 42,
    title: 'Cached fixture project',
    description: 'Local test',
    repository_url: 'https://github.com/example/project',
    rules: 'Local rules',
    owner_username: 'fixture',
    owner_user_id: 8,
    reward_slots: 3,
    reward_quota: 100,
    net_reward_quota: 90,
    escrow_quota: 0,
    status: 'published',
    active_challenge_count: 0,
    approved_challenge_count: 0,
    owner_rating_count: 0,
    updated_at: 1790050000,
  }
  api.defaults.adapter = async (config) => {
    const url = config.url ?? ''
    requests.push(url)
    let data: unknown = []
    if (url.startsWith('/api/open-source-bounties?')) {
      if (hold) {
        await new Promise<void>((resolve) => {
          release = resolve
        })
      }
      if (response === 'error' || response === 'network') {
        throw new Error(
          response === 'error' ? 'HTTP 500 fixture' : 'Network Error'
        )
      }
      if (response === 'business') {
        return {
          config,
          status: 200,
          statusText: 'OK',
          headers: {},
          data: { success: false, message: 'Fixture rejected' },
        }
      }
      data = {
        items: response === 'items' ? [project] : [],
        total: response === 'items' ? 1 : 0,
        page: 1,
        page_size: 50,
      }
    } else if (url === '/api/status') {
      data = { backend_capabilities: {} }
    } else if (url === '/api/open-source-bounties/config') {
      data = { rate_basis_points: 100, rate_percent: 1 }
    }
    return {
      config,
      status: 200,
      statusText: 'OK',
      headers: {},
      data: { success: true, data },
    }
  }
  useAuthStore
    .getState()
    .auth.setUser({ id: 7, username: 'fixture-viewer', role: 1 })
  const flush = async () => {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 30))
    })
  }
  const text = () => container.textContent ?? ''
  const retryButton = () =>
    [...container.querySelectorAll('button')].find(
      (button) => button.textContent?.trim() === 'Retry'
    )
  const refetch = async () => {
    await act(async () => {
      await client.refetchQueries({
        queryKey: ['open-source-bounties'],
        exact: true,
      })
    })
    await flush()
  }
  try {
    await act(async () => {
      root.render(
        <QueryClientProvider client={client}>
          <I18nextProvider i18n={i18n}>
            <OpenSourceBounties />
          </I18nextProvider>
        </QueryClientProvider>
      )
    })
    await flush()
    assert.ok(
      text().includes('Challenges are temporarily unavailable.'),
      'failed list must display an inline error'
    )
    assert.ok(
      !text().includes('No public bounty projects yet'),
      'failure is not a successful empty list'
    )
    assert.ok(retryButton())
    const otherRequests = requests.filter(
      (url) => !url.startsWith('/api/open-source-bounties?')
    ).length
    response = 'items'
    hold = true
    await act(async () => {
      retryButton()?.click()
    })
    await flush()
    assert.ok(
      !retryButton() || retryButton()?.disabled,
      'retry cannot be repeated while fetching'
    )
    assert.ok(release)
    await act(async () => {
      release?.()
    })
    hold = false
    await flush()
    assert.ok(text().includes(project.title), 'retry restores the project')
    assert.ok(!retryButton(), 'successful retry removes error')
    assert.equal(
      requests.filter((url) => !url.startsWith('/api/open-source-bounties?'))
        .length,
      otherRequests,
      'retry only refetches this list'
    )
    response = 'error'
    await refetch()
    assert.ok(
      text().includes(project.title),
      'failed refresh preserves cached cards'
    )
    assert.ok(
      text().includes('Challenges are temporarily unavailable.'),
      'cached cards do not hide refresh failure'
    )
    response = 'empty'
    await refetch()
    assert.ok(
      text().includes('No public bounty projects yet'),
      'successful empty result retains normal empty state'
    )
    assert.ok(!retryButton())
    for (const failure of ['error', 'business', 'network'] as const) {
      response = failure
      await refetch()
      assert.ok(
        text().includes('Challenges are temporarily unavailable.'),
        failure
      )
      assert.ok(
        !text().includes('No public bounty projects yet'),
        'cached empty result does not hide failure'
      )
    }
    await act(async () => {
      retryButton()?.click()
    })
    await flush()
    assert.ok(retryButton(), 'failed retry remains recoverable')
  } finally {
    await act(async () => root.unmount())
    client.clear()
    api.defaults.adapter = originalAdapter
    useAuthStore.setState({ auth: originalAuth })
    container.remove()
  }
})
