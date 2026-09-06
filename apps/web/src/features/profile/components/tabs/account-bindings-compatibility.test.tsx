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
import { after, afterEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'

import type { UserProfile } from '../../types'

const domWindow = new Window()
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
const { AccountBindingsTab } = await import('./account-bindings-tab')
const { api } = await import('@/lib/api')

const originalGet = api.get
const originalDelete = api.delete
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((nextResolve) => {
    resolve = nextResolve
  })
  return { promise, resolve }
}

async function flushQueries() {
  await new Promise((resolve) => setTimeout(resolve, 10))
}

async function waitForCondition(
  condition: () => boolean,
  failureMessage: string
): Promise<void> {
  const deadline = Date.now() + 1500
  while (!condition()) {
    if (Date.now() >= deadline) {
      throw new Error(`${failureMessage}: ${document.body.textContent}`)
    }
    await new Promise((resolve) => setTimeout(resolve, 5))
  }
}

function cachedStatus() {
  return {
    github_oauth: true,
    github_client_id: 'client-id',
    backend_capabilities: {
      bounty_notifications: true,
      bounty_challenge_cancel: true,
      bounty_public_read: true,
      self_oauth_unbind: true,
      responses_websocket: true,
    },
  }
}

function customOAuthStatus() {
  return {
    ...cachedStatus(),
    custom_oauth_providers: [
      {
        id: 42,
        name: 'Acme SSO',
        slug: 'acme-sso',
        icon: 'link',
        client_id: 'client-id',
        authorization_endpoint: 'https://sso.example.test/authorize',
        scopes: 'openid',
      },
    ],
  }
}

function misconfiguredOAuthStatus() {
  return {
    github_oauth: true,
    discord_oauth: true,
    oidc_enabled: true,
    telegram_oauth: true,
    linuxdo_oauth: true,
    custom_oauth_providers: [
      {
        id: 43,
        name: 'Incomplete SSO',
        slug: 'incomplete-sso',
        icon: 'link',
        client_id: '',
        authorization_endpoint: '',
        scopes: 'openid',
      },
    ],
  }
}

const profile: UserProfile = {
  id: 7,
  username: 'compat-user',
  display_name: 'Compat User',
  role: 1,
  email: 'compat@example.com',
  group: 'default',
  quota: 100,
  used_quota: 0,
  request_count: 0,
  status: 1,
  aff_count: 0,
  aff_quota: 0,
  aff_history_quota: 0,
  created_time: 0,
  github_id: 'github-user-id',
}

async function renderBindings(queryClient: InstanceType<typeof QueryClient>) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <AccountBindingsTab profile={profile} onUpdate={() => undefined} />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flushQueries()
  })
  return { container, root }
}

function buttonsWithText(text: string) {
  return [...document.querySelectorAll('button')].filter(
    (button) => button.textContent?.trim() === text
  ) as HTMLButtonElement[]
}

afterEach(() => {
  api.get = originalGet
  api.delete = originalDelete
  window.localStorage.clear()
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('legacy Go account binding compatibility', () => {
  test('hides binding actions whose required OAuth configuration is missing', async () => {
    const status = misconfiguredOAuthStatus()
    api.get = (async (url) => {
      if (url === '/api/status') {
        return { data: { success: true, data: status } }
      }
      return { data: { success: true, data: [] } }
    }) as typeof api.get

    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    queryClient.setQueryData(['status', 'anonymous'], status)
    const { root } = await renderBindings(queryClient)

    for (const provider of [
      'GitHub',
      'Discord',
      'OIDC',
      'Telegram',
      'LinuxDO',
      'Incomplete SSO',
    ]) {
      assert.equal(
        document.body.textContent?.includes(provider),
        false,
        `unexpected binding provider: ${provider}`
      )
    }
    assert.equal(buttonsWithText('Bind').length, 0)

    await act(async () => root.unmount())
    queryClient.clear()
  })

  test('does not render or call built-in unbind from stale capabilities after legacy status', async () => {
    const statusResponse = deferred<{
      data: { success: boolean; data: { github_oauth: boolean } }
    }>()
    const deletes: string[] = []
    api.get = (async (url) => {
      if (url === '/api/status') return statusResponse.promise
      return { data: { success: true, data: [] } }
    }) as typeof api.get
    api.delete = (async (url) => {
      deletes.push(url)
      return { data: { success: true, data: null } }
    }) as typeof api.delete
    window.localStorage.setItem('status', JSON.stringify(cachedStatus()))

    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    queryClient.setQueryData(['status', 'anonymous'], cachedStatus())
    const { root } = await renderBindings(queryClient)

    assert.equal(buttonsWithText('Unbind').length, 0)
    statusResponse.resolve({
      data: { success: true, data: { github_oauth: true } },
    })
    await act(async () =>
      waitForCondition(
        () =>
          queryClient.isFetching({
            queryKey: ['status', 'anonymous'],
            exact: true,
          }) === 0,
        'legacy status request did not settle'
      )
    )

    assert.equal(buttonsWithText('Unbind').length, 0)
    assert.deepEqual(deletes, [])

    await act(async () => root.unmount())
    queryClient.clear()
  })

  test('renders and calls built-in unbind when live status advertises it', async () => {
    const statusResponse = deferred<{
      data: { success: boolean; data: ReturnType<typeof cachedStatus> }
    }>()
    const deletes: string[] = []
    api.get = (async (url) => {
      if (url === '/api/status') return statusResponse.promise
      return { data: { success: true, data: [] } }
    }) as typeof api.get
    api.delete = (async (url) => {
      deletes.push(url)
      return { data: { success: true, data: null } }
    }) as typeof api.delete

    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    queryClient.setQueryData(['status', 'anonymous'], cachedStatus())
    const { root } = await renderBindings(queryClient)
    assert.equal(buttonsWithText('Unbind').length, 0)

    statusResponse.resolve({
      data: { success: true, data: cachedStatus() },
    })
    await act(async () =>
      waitForCondition(
        () => buttonsWithText('Unbind').length > 0,
        'live self-unbind capability did not render'
      )
    )

    const unbindButton = buttonsWithText('Unbind')[0]
    assert.ok(unbindButton)
    await act(async () => unbindButton.click())
    await act(async () =>
      waitForCondition(
        () => buttonsWithText('Confirm Unbind').length > 0,
        'unbind confirmation did not render'
      )
    )
    const confirmButton = buttonsWithText('Confirm Unbind')[0]
    assert.ok(confirmButton)
    await act(async () => {
      confirmButton.click()
      await waitForCondition(
        () => deletes.length === 1,
        'self-unbind request was not sent'
      )
    })
    assert.deepEqual(deletes, ['/api/user/bindings/github'])

    await act(async () => root.unmount())
    queryClient.clear()
  })

  test('recognizes an already-bound custom provider when the API returns a numeric id', async () => {
    const status = customOAuthStatus()
    api.get = (async (url) => {
      if (url === '/api/status') {
        return { data: { success: true, data: status } }
      }
      if (url === '/api/user/oauth/bindings') {
        return {
          data: {
            success: true,
            data: [
              {
                provider_id: 42,
                provider_name: 'Acme SSO',
                provider_slug: 'acme-sso',
                provider_icon: 'link',
                provider_user_id: 'acme-user-1',
              },
            ],
          },
        }
      }
      return { data: { success: true, data: [] } }
    }) as typeof api.get

    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    queryClient.setQueryData(['status', 'anonymous'], status)
    const { root } = await renderBindings(queryClient)

    await act(async () =>
      waitForCondition(
        () => document.body.textContent?.includes('Acme SSO') === true,
        'custom provider did not render'
      )
    )
    await act(async () =>
      waitForCondition(
        () => buttonsWithText('Unbind').length === 2,
        'numeric custom provider binding was not recognized'
      )
    )
    assert.equal(buttonsWithText('Bind').length, 0)

    await act(async () => root.unmount())
    queryClient.clear()
  })
})
