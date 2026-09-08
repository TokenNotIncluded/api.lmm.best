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

import type { AuthUser } from '@/stores/auth-store'

const domWindow = new Window({ url: 'https://console.example.test/guide' })
Object.defineProperty(domWindow.document, 'compatMode', { value: 'CSS1Compat' })
for (const key of [
  'window',
  'document',
  'navigator',
  'history',
  'location',
  'HTMLElement',
  'HTMLAnchorElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'SVGElement',
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
Object.defineProperty(domWindow.HTMLElement.prototype, 'scrollIntoView', {
  configurable: true,
  value() {},
})

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} = await import('@tanstack/react-router')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { consumeQueuedAssistantRequest } =
  await import('@/features/assistant/assistant-events')
const { useAuthStore } = await import('@/stores/auth-store')
const { Guide } = await import('./index')

const originalGet = api.get
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
let dispose: (() => Promise<void>) | undefined

async function flushEffects() {
  await new Promise((resolve) => setTimeout(resolve, 20))
}

async function renderGuide(user: AuthUser | null) {
  useAuthStore.getState().auth.setUser(user)
  api.get = (async (url) => {
    if (url === '/api/status') {
      return {
        data: { success: true, data: { assistant: { enabled: false } } },
      }
    }
    return { data: { success: true, data: url === '/api/notice' ? '' : [] } }
  }) as typeof api.get
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const rootRoute = createRootRoute({ component: Outlet })
  const guideRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/guide',
    component: Guide,
  })
  const destinations = [
    '/sign-in',
    '/getting-started',
    '/keys',
    '/support',
    '/pricing',
  ].map((path) =>
    createRoute({
      getParentRoute: () => rootRoute,
      path,
      component: () => null,
    })
  )
  const router = createRouter({
    routeTree: rootRoute.addChildren([guideRoute, ...destinations]),
    history: createMemoryHistory({ initialEntries: ['/guide'] }),
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  dispose = async () => {
    await act(async () => root.unmount())
    queryClient.clear()
    container.remove()
  }
  await act(async () => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <RouterProvider router={router} />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flushEffects()
  })
  await act(flushEffects)
  return { container, router }
}

function findButton(container: HTMLElement, label: string) {
  const button = [
    ...container.querySelectorAll<HTMLButtonElement>('button'),
  ].find((item) => item.textContent?.trim() === label)
  assert.ok(button, `Missing button: ${label}`)
  return button
}

async function click(button: HTMLButtonElement) {
  await act(async () => {
    button.click()
    await flushEffects()
  })
}

afterEach(async () => {
  await dispose?.()
  dispose = undefined
  consumeQueuedAssistantRequest()
  api.get = originalGet
  useAuthStore.getState().auth.reset('complete')
  window.localStorage.clear()
  window.sessionStorage.clear()
  document.body.replaceChildren()
})
after(() => domWindow.close())

describe('Guide when the AI assistant is disabled', () => {
  test('keeps public installation available and returns visitors to the guide after sign-in', async () => {
    const { container, router } = await renderGuide(null)
    assert.doesNotMatch(
      container.textContent ?? '',
      /Sign in for AI guidance|Walk me through this with the AI assistant/
    )
    assert.ok(
      container.querySelector('#client-setup a[target="_blank"]'),
      'Official downloads remain accessible'
    )

    await click(findButton(container, 'Continue to account setup'))

    assert.equal(router.state.location.pathname, '/sign-in')
    assert.deepEqual(router.state.location.search, { redirect: '/guide' })
    assert.equal(consumeQueuedAssistantRequest(), undefined)
  })

  test('takes approved users directly to API Keys', async () => {
    const { container, router } = await renderGuide({
      id: 31,
      username: 'approved',
      role: 1,
      developer_access_granted: true,
    })

    await click(findButton(container, 'Create a key and import'))

    assert.equal(router.state.location.pathname, '/keys')
    assert.equal(consumeQueuedAssistantRequest(), undefined)
  })

  test('keeps pending users on the guide and focuses reachable human support for access and errors', async () => {
    const { container, router } = await renderGuide({
      id: 32,
      username: 'pending',
      role: 1,
      developer_access_granted: false,
    })
    const support = container.querySelector<HTMLElement>('#guide-support')
    assert.ok(support)
    assert.ok(support.querySelector('a[href="mailto:support@lmm.best"]'))
    assert.equal(support.querySelector('a[href="/support"]'), null)

    await click(findButton(container, 'Continue to account setup'))
    assert.equal(router.state.location.pathname, '/guide')
    assert.equal(document.activeElement, support)

    const troubleshooting =
      container.querySelector<HTMLDetailsElement>('details')
    assert.ok(troubleshooting)
    await act(async () => {
      troubleshooting.querySelector('summary')?.click()
      await flushEffects()
    })
    assert.equal(troubleshooting.open, true)
    await click(findButton(troubleshooting, 'Contact support'))
    assert.equal(router.state.location.pathname, '/guide')
    assert.equal(document.activeElement, support)
    assert.equal(consumeQueuedAssistantRequest(), undefined)
  })
})
