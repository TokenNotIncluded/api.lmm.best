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

import type { AuthUser } from '@/stores/auth-store'

const domWindow = new Window({
  url: 'https://console.example.test/challenges/41',
})
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
  'HTMLAnchorElement',
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
const { useAuthStore } = await import('@/stores/auth-store')
const { ChallengeDetailPage } = await import('./challenge-detail-page')

const originalGet = api.get
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

const user: AuthUser = {
  id: 7,
  username: 'l0-user',
  role: 1,
  developer_access_granted: false,
}

const detail = {
  project: {
    id: 41,
    owner_user_id: 2,
    owner_username: 'owner',
    repository_url: 'https://github.com/example/project',
    title: 'Fix the public challenge flow',
    description: 'A small, public contribution task.',
    rules: 'Keep the change focused.',
    reward_quota: 100,
    net_reward_quota: 100,
    reward_slots: 1,
    escrow_quota: 100,
    platform_fee_rate_bps: 0,
    platform_fee_quota: 0,
    status: 'published' as const,
    created_at: 1,
    updated_at: 1,
    published_at: 1,
    closed_at: 0,
    archived_at: 0,
    active_challenge_count: 0,
    approved_challenge_count: 0,
    owner_rating_average: 0,
    owner_rating_count: 0,
    owner_thank_heart_count: 0,
  },
  challenges: [],
  ledger: [],
}

async function flushEffects() {
  await new Promise((resolve) => setTimeout(resolve, 100))
}

function makeRouter() {
  const rootRoute = createRootRoute({ component: Outlet })
  const detailRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/challenges/$challengeId',
    component: () => <ChallengeDetailPage challengeId={41} />,
  })
  const emptyRoutes = ['/', '/challenges', '/wallet', '/security'].map(
    (path) =>
      createRoute({
        getParentRoute: () => rootRoute,
        path,
        component: () => null,
      })
  )
  return createRouter({
    routeTree: rootRoute.addChildren([detailRoute, ...emptyRoutes]),
    history: createMemoryHistory({ initialEntries: ['/challenges/41'] }),
  })
}

async function renderPage() {
  api.get = (async (url: string) => {
    if (url === '/api/notice') {
      return { data: { success: true, data: '' } }
    }
    if (url === '/api/status') {
      return {
        data: {
          success: true,
          data: { backend_capabilities: { bounty_public_read: true } },
        },
      }
    }
    if (url === '/api/open-source-bounties/projects/41') {
      return { data: { success: true, data: detail } }
    }
    return { data: { success: true, data: [] } }
  }) as typeof api.get
  useAuthStore.getState().auth.setUser(user)

  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const router = makeRouter()
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
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
  return { container, queryClient, root }
}

async function unmount(rendered: Awaited<ReturnType<typeof renderPage>>) {
  await act(async () => rendered.root.unmount())
  rendered.queryClient.clear()
  rendered.container.remove()
}

afterEach(() => {
  api.get = originalGet
  useAuthStore.getState().auth.reset('complete')
  window.localStorage.clear()
  window.sessionStorage.clear()
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('L0 challenge contribution entry', () => {
  test('offers the onboarding assistant path instead of a read-only dead end', async () => {
    const rendered = await renderPage()

    assert.match(
      rendered.container.textContent ?? '',
      /ask the AI assistant to request L1 access/
    )
    const onboardingLink = [...rendered.container.querySelectorAll('a')].find(
      (link) =>
        link.getAttribute('href') === '/wallet' &&
        link.textContent?.includes('Start with AI assistant')
    )
    assert.ok(onboardingLink)
    assert.match(onboardingLink.textContent ?? '', /Start with AI assistant/)

    await unmount(rendered)
  })
})
