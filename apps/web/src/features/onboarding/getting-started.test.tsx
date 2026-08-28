/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, afterEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'

import type { ApiRequestConfig } from '@/lib/api'
import type { AuthUser } from '@/stores/auth-store'

const domWindow = new Window({ url: 'https://console.example.test/' })
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
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'scrollTo',
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
const { consumeQueuedAssistantRequest, subscribeToAssistantOpen } =
  await import('@/features/assistant/assistant-events')
const { useAuthStore } = await import('@/stores/auth-store')
const { GettingStarted } = await import('./getting-started')

const originalGet = api.get
const originalPost = api.post
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
  username: 'new-user',
  role: 1,
  developer_access_granted: false,
}

async function flushEffects() {
  await new Promise((resolve) => setTimeout(resolve, 20))
}

function makeRouter() {
  const rootRoute = createRootRoute({ component: Outlet })
  const gettingStartedRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/getting-started',
    component: GettingStarted,
  })
  const emptyRoutes = [
    '/challenges',
    '/support',
    '/keys',
    '/dashboard',
    '/pricing',
  ].map((path) =>
    createRoute({
      getParentRoute: () => rootRoute,
      path,
      component: () => null,
    })
  )
  return createRouter({
    routeTree: rootRoute.addChildren([gettingStartedRoute, ...emptyRoutes]),
    history: createMemoryHistory({ initialEntries: ['/getting-started'] }),
  })
}

async function renderPage(
  bountyCapability = false,
  bountyResponse: { data: Record<string, unknown> } | Error = {
    data: {
      success: true,
      data: { items: [], total: 0, page: 1, page_size: 50 },
    },
  },
  accessRequest: Record<string, unknown> | null = null,
  userOverride: Partial<AuthUser> = {}
) {
  const currentUser = { ...user, ...userOverride }
  const gets: string[] = []
  const getConfigs: Array<ApiRequestConfig | undefined> = []
  api.get = (async (url, config) => {
    gets.push(url)
    getConfigs.push(config)
    if (url === '/api/user/self') {
      return { data: { success: true, data: currentUser } }
    }
    if (url === '/api/user/developer-access/request') {
      return { data: { success: true, data: accessRequest } }
    }
    if (url === '/api/status') {
      return {
        data: {
          success: true,
          data: {
            backend_capabilities: {
              bounty_notifications: false,
              bounty_challenge_cancel: false,
              bounty_public_read: bountyCapability,
              self_oauth_unbind: false,
              responses_websocket: false,
            },
          },
        },
      }
    }
    if (url.startsWith('/api/open-source-bounties?')) {
      if (bountyResponse instanceof Error) throw bountyResponse
      return bountyResponse
    }
    return { data: { success: true, data: [] } }
  }) as typeof api.get
  useAuthStore.getState().auth.setUser(currentUser)

  const queryClient = new QueryClient({
    // Keep the test honest: ChallengeList must disable retries itself for a
    // best-effort route probe, rather than relying on a test-only default.
    defaultOptions: { queries: { retry: 3 } },
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
  return { container, root, queryClient, gets, getConfigs }
}

async function unmountPage(page: Awaited<ReturnType<typeof renderPage>>) {
  await act(async () => page.root.unmount())
  page.queryClient.clear()
  page.container.remove()
}

afterEach(() => {
  consumeQueuedAssistantRequest()
  api.get = originalGet
  api.post = originalPost
  useAuthStore.getState().auth.reset('complete')
  window.localStorage.clear()
  window.sessionStorage.clear()
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('getting started access boundaries', () => {
  test('keeps the model square discoverable from the L0 onboarding page', async () => {
    const page = await renderPage()
    const modelSquare = page.container.querySelector('a[href="/pricing"]')

    assert.ok(modelSquare)
    assert.equal(modelSquare.textContent?.includes('Model Square'), true)
    await unmountPage(page)
  })

  test('opens the AI onboarding conversation once when an L0 user enters', async () => {
    const opened: Array<string | undefined> = []
    const unsubscribe = subscribeToAssistantOpen((request) =>
      opened.push(request.preset)
    )

    const first = await renderPage(false, undefined, null, { id: 7001 })
    assert.deepEqual(opened, ['onboarding'])
    await unmountPage(first)

    const second = await renderPage(false, undefined, null, { id: 7001 })
    assert.deepEqual(opened, ['onboarding'])
    await unmountPage(second)
    unsubscribe()
  })

  test('keeps the setup tutorial out of L0 and derives L1 progress from account state', async () => {
    const l0Page = await renderPage()
    assert.equal(
      l0Page.container.textContent?.includes('Three steps to get started'),
      false
    )
    assert.equal(
      l0Page.container.textContent?.includes('How can I help?'),
      true
    )
    await unmountPage(l0Page)

    const l1Page = await renderPage(false, undefined, null, {
      developer_access_granted: true,
      onboarding: {
        activation_complete: true,
        credential_complete: false,
        first_request_complete: false,
        stage: 'credential',
      },
    })
    assert.equal(l1Page.container.textContent?.includes('1/3'), true)
    assert.equal(l1Page.container.textContent?.includes('Create API key'), true)
    assert.equal(l1Page.container.textContent?.includes('Continue setup'), true)
    await unmountPage(l1Page)
  })

  test('uses one assistant entry without a second composer or hard-coded presets', async () => {
    const opened: Array<string | undefined> = []
    const messages: Array<string | undefined> = []
    const unsubscribe = subscribeToAssistantOpen((request) => {
      opened.push(request.preset)
      messages.push(request.message)
    })
    const page = await renderPage()
    await act(flushEffects)

    const start = [...page.container.querySelectorAll('button')].find(
      (button) => button.textContent?.includes('Start with AI assistant')
    )
    assert.ok(start)
    await act(async () => {
      start.click()
      await flushEffects()
    })

    assert.deepEqual(opened, ['onboarding'])
    assert.deepEqual(messages, [undefined])
    assert.equal(
      page.container.textContent?.includes(
        'What can I do while access is under review?'
      ),
      false
    )
    assert.equal(
      page.container.textContent?.includes('Which option is the best value?'),
      false
    )
    assert.equal(
      page.container.textContent?.includes('How is request cost calculated?'),
      false
    )
    assert.equal(page.container.querySelector('input'), null)
    await unmountPage(page)
    unsubscribe()
  })

  test('opens onboarding guidance once while administrator review is pending', async () => {
    const opened: Array<string | undefined> = []
    const unsubscribe = subscribeToAssistantOpen((request) =>
      opened.push(request.preset)
    )
    const pendingRequest = {
      id: 9901,
      status: 'pending',
      reason: '',
      admin_note: '',
      created_at: 1,
      reviewed_at: 0,
    }

    const first = await renderPage(
      false,
      { data: { success: true, data: [] } },
      pendingRequest
    )
    assert.deepEqual(opened, ['onboarding'])
    await unmountPage(first)

    const second = await renderPage(
      false,
      { data: { success: true, data: [] } },
      pendingRequest
    )
    assert.deepEqual(opened, ['onboarding'])
    await unmountPage(second)
    unsubscribe()
  })

  test('keeps a pending recommendation to one compact status line', async () => {
    const page = await renderPage(
      false,
      { data: { success: true, data: [] } },
      {
        id: 9902,
        status: 'pending',
        reason: 'I am building a small Claude Code integration.',
        source: 'assistant_recommendation',
        ai_recommendation:
          'Recommend L1 for a documented development use case.',
        admin_note: '',
        created_at: 1,
        reviewed_at: 0,
      },
      { id: 7002 }
    )

    assert.equal(
      page.container.textContent?.includes('AI recommendation submitted'),
      true
    )
    assert.equal(page.container.textContent?.includes('Pending review'), true)
    assert.equal(
      page.container.textContent?.includes(
        'I am building a small Claude Code integration.'
      ),
      false
    )
    assert.equal(
      page.container.textContent?.includes(
        'Recommend L1 for a documented development use case.'
      ),
      false
    )
    assert.equal(page.container.querySelector('[role="progressbar"]'), null)
    await unmountPage(page)
  })

  test('routes a direct L1 application through the single assistant surface', async () => {
    const page = await renderPage(
      false,
      { data: { success: true, data: [] } },
      null,
      { id: 9904 }
    )

    assert.equal(
      page.container.querySelector('[data-testid="l0-direct-access-request"]'),
      null
    )
    assert.equal(page.container.querySelector('textarea'), null)
    assert.ok(
      [...page.container.querySelectorAll('button')].find((button) =>
        button.textContent?.includes('Start with AI assistant')
      )
    )
    await unmountPage(page)
  })

  test('shows administrator feedback and lets a rejected user revise with AI', async () => {
    const opened: Array<string | undefined> = []
    const unsubscribe = subscribeToAssistantOpen((request) =>
      opened.push(request.preset)
    )
    const page = await renderPage(
      false,
      { data: { success: true, data: [] } },
      {
        id: 9903,
        status: 'rejected',
        reason: 'Need access.',
        source: 'assistant_recommendation',
        ai_recommendation: 'The use case needs more detail.',
        admin_note: 'Please explain which client and models you plan to use.',
        created_at: 1,
        reviewed_at: 2,
      },
      { id: 7003 }
    )

    assert.equal(
      page.container.textContent?.includes('Access request rejected'),
      true
    )
    assert.equal(
      page.container.textContent?.includes(
        'Please explain which client and models you plan to use.'
      ),
      true
    )

    const revise = [...page.container.querySelectorAll('button')].find(
      (button) => button.textContent?.includes('Revise')
    )
    assert.ok(revise)
    await act(async () => {
      revise.click()
      await flushEffects()
    })
    assert.equal(opened.at(-1), 'onboarding')

    await unmountPage(page)
    unsubscribe()
  })

  test('shows only the read-only access conversation to L0', async () => {
    const page = await renderPage(true)
    await act(flushEffects)

    assert.equal(page.container.querySelector('a[href="/wallet"]'), null)
    assert.equal(page.gets.includes('/api/user/topup/info'), false)
    assert.equal(page.container.textContent?.includes('How can I help?'), true)
    assert.equal(page.container.querySelector('a[href="/challenges"]'), null)
    assert.equal(page.container.textContent?.includes('Create API key'), false)
    assert.equal(
      page.container.textContent?.includes('Open setup guide'),
      false
    )
    assert.equal(
      page.gets.some((url) => url.startsWith('/api/open-source-bounties?')),
      false
    )
    await unmountPage(page)
  })

  test('keeps unavailable optional probes inline and does not retry them', async () => {
    const page = await renderPage(true, new Error('Not Found'), null, {
      developer_access_granted: true,
      onboarding: {
        activation_complete: true,
        credential_complete: false,
        first_request_complete: false,
        stage: 'credential',
      },
    })
    await act(flushEffects)

    const bountyCalls = page.gets.filter((url) =>
      url.startsWith('/api/open-source-bounties?')
    )
    assert.equal(bountyCalls.length, 1)
    assert.equal(
      page.container.textContent?.includes(
        'Challenges are temporarily unavailable.'
      ),
      true
    )

    const bountyConfig = page.getConfigs.find((_, index) =>
      page.gets[index].startsWith('/api/open-source-bounties?')
    )
    assert.equal(bountyConfig?.skipBusinessError, true)
    assert.equal(bountyConfig?.skipErrorHandler, true)
    await unmountPage(page)
  })
})
