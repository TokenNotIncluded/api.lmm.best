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
const matchMediaStub = () => ({
  matches: true,
  media: '',
  addListener() {},
  removeListener() {},
  addEventListener() {},
  removeEventListener() {},
  dispatchEvent() {
    return false
  },
})
Object.defineProperty(domWindow, 'matchMedia', {
  configurable: true,
  value: matchMediaStub,
})
Object.defineProperty(globalThis, 'matchMedia', {
  configurable: true,
  value: matchMediaStub,
})
Object.defineProperty(globalThis, 'customElements', {
  configurable: true,
  value: {
    get() {
      return undefined
    },
    define() {},
  },
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
const { consumeQueuedAssistantRequest, subscribeToAssistantOpen } =
  await import('@/features/assistant/assistant-events')
const { useAuthStore } = await import('@/stores/auth-store')
const { GettingStarted } = await import('./getting-started')
const originalGet = api.get
const originalPost = api.post
const originalFetch = globalThis.fetch
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const user: AuthUser = {
  id: 7,
  username: 'new-user',
  role: 1,
  developer_access_granted: false,
}
const flushEffects = () => new Promise((resolve) => setTimeout(resolve, 20))
function makeRouter() {
  const rootRoute = createRootRoute({ component: Outlet })
  const route = createRoute({
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
    '/wallet',
    '/tool-market',
  ].map((path) =>
    createRoute({
      getParentRoute: () => rootRoute,
      path,
      component: () => null,
    })
  )
  return createRouter({
    routeTree: rootRoute.addChildren([route, ...emptyRoutes]),
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
  // Optional probes must disable their own retry rather than rely on test defaults.
  const queryClient = new QueryClient({
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
  await act(flushEffects)
  return { container, root, queryClient, router, currentUser, gets, getConfigs }
}
async function unmountPage(page: Awaited<ReturnType<typeof renderPage>>) {
  await act(async () => page.root.unmount())
  page.queryClient.clear()
  page.container.remove()
}
const button = (page: Awaited<ReturnType<typeof renderPage>>, text: string) => {
  const found = [...page.container.querySelectorAll('button')].find((node) =>
    node.textContent?.includes(text)
  )
  assert.ok(found, text)
  return found
}
afterEach(() => {
  consumeQueuedAssistantRequest()
  api.get = originalGet
  api.post = originalPost
  globalThis.fetch = originalFetch
  useAuthStore.getState().auth.reset('complete')
  window.localStorage.clear()
  window.sessionStorage.clear()
  document.body.replaceChildren()
})
after(() => domWindow.close())

describe('getting started access boundaries', () => {
  test('keeps the model square discoverable from the L0 onboarding page', async () => {
    const page = await renderPage()
    assert.match(
      page.container.querySelector('a[href="/pricing"]')?.textContent ?? '',
      /Models and pricing/
    )
    await unmountPage(page)
  })
  test('lets an L0 user explore before opening the assistant', async () => {
    const opened: Array<string | undefined> = []
    const unsubscribe = subscribeToAssistantOpen((request) =>
      opened.push(request.preset)
    )
    for (let index = 0; index < 2; index++) {
      const page = await renderPage(false, undefined, null, { id: 7001 })
      assert.deepEqual(opened, [])
      await unmountPage(page)
    }
    unsubscribe()
  })
  test('keeps the setup tutorial out of L0 and derives L1 progress from account state', async () => {
    const l0 = await renderPage()
    assert.doesNotMatch(
      l0.container.textContent ?? '',
      /Three steps to get started/
    )
    assert.match(l0.container.textContent ?? '', /What will you make\?/)
    await unmountPage(l0)
    const l1 = await renderPage(false, undefined, null, {
      developer_access_granted: true,
      onboarding: {
        activation_complete: true,
        credential_complete: false,
        first_request_complete: false,
        stage: 'credential',
      },
    })
    for (const text of ['1/3', 'Create API key', 'Continue setup']) {
      assert.ok(l1.container.textContent?.includes(text))
    }
    await unmountPage(l1)
  })
  test('opens the same assistant for an access application', async () => {
    const opened: Array<string | undefined> = []
    const messages: Array<string | undefined> = []
    const unsubscribe = subscribeToAssistantOpen((request) => {
      opened.push(request.preset)
      messages.push(request.message)
    })
    const page = await renderPage()
    await act(async () => {
      button(page, 'Start with AI assistant').click()
      await flushEffects()
    })
    assert.deepEqual(opened, ['onboarding'])
    assert.deepEqual(messages, [undefined])
    for (const text of [
      'What can I do while access is under review?',
      'Which option is the best value?',
      'How is request cost calculated?',
    ]) {
      assert.ok(!page.container.textContent?.includes(text))
    }
    assert.ok(page.container.querySelector('input#l0-question'))
    await unmountPage(page)
    unsubscribe()
  })
  test('keeps pending review visible without forcing the assistant open', async () => {
    const opened: Array<string | undefined> = []
    const unsubscribe = subscribeToAssistantOpen((request) =>
      opened.push(request.preset)
    )
    for (let index = 0; index < 2; index++) {
      const page = await renderPage(false, undefined, {
        id: 9901,
        status: 'pending',
        reason: '',
        admin_note: '',
        created_at: 1,
        reviewed_at: 0,
      })
      assert.deepEqual(opened, [])
      assert.match(
        page.container.querySelector(
          '[data-testid="l0-account-details"] summary'
        )?.textContent ?? '',
        /Pending review/
      )
      await unmountPage(page)
    }
    unsubscribe()
  })
  test('shows pending application details and recommendation', async () => {
    const page = await renderPage(
      false,
      undefined,
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
    for (const text of [
      'AI recommendation submitted',
      'Pending review',
      'I am building a small Claude Code integration.',
      'Recommend L1 for a documented development use case.',
    ]) {
      assert.ok(page.container.textContent?.includes(text))
    }
    assert.equal(page.container.querySelector('[role="progressbar"]'), null)
    await unmountPage(page)
  })
  test('polls a pending request, refreshes auth after approval, and leaves L0 onboarding', async () => {
    const request = {
      id: 9905,
      status: 'pending',
      reason: 'I am building a private coding client.',
      source: 'assistant_recommendation',
      ai_recommendation: 'Recommend L1 for a concrete coding workflow.',
      admin_note: '',
      created_at: 1,
      reviewed_at: 0,
    }
    const page = await renderPage(false, undefined, request, { id: 7005 })
    request.status = 'approved'
    request.admin_note = 'Approved automatically.'
    request.reviewed_at = 2
    page.currentUser.developer_access_granted = true
    await act(async () => {
      window.dispatchEvent(new Event('focus'))
      await flushEffects()
    })
    const deadline = Date.now() + 2_000
    while (
      Date.now() < deadline &&
      (useAuthStore.getState().auth.user?.developer_access_granted !== true ||
        page.router.state.location.pathname !== '/dashboard')
    ) {
      await act(flushEffects)
    }
    assert.equal(
      useAuthStore.getState().auth.user?.developer_access_granted,
      true
    )
    assert.equal(page.router.state.location.pathname, '/dashboard')
    assert.ok(page.gets.includes('/api/user/self'))
    await unmountPage(page)
  })
  test('routes a direct L1 application through the single assistant surface', async () => {
    const page = await renderPage(false, undefined, null, { id: 9904 })
    assert.equal(
      page.container.querySelector('[data-testid="l0-direct-access-request"]'),
      null
    )
    assert.equal(page.container.querySelector('textarea'), null)
    button(page, 'Start with AI assistant')
    await unmountPage(page)
  })
  test('shows administrator feedback and lets a rejected user revise with AI', async () => {
    const opened: Array<string | undefined> = []
    const unsubscribe = subscribeToAssistantOpen((request) =>
      opened.push(request.preset)
    )
    const page = await renderPage(
      false,
      undefined,
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
    assert.match(page.container.textContent ?? '', /Access request rejected/)
    assert.match(
      page.container.textContent ?? '',
      /Please explain which client and models you plan to use\./
    )
    await act(async () => {
      button(page, 'Revise').click()
      await flushEffects()
    })
    assert.equal(opened.at(-1), 'onboarding')
    await unmountPage(page)
    unsubscribe()
  })
  test('lets L0 browse and fund the account before approval', async () => {
    const page = await renderPage(true)
    assert.equal(page.container.querySelector('a[href="/wallet"]'), null)
    await act(async () => button(page, 'Plans & top-ups').click())
    assert.equal(consumeQueuedAssistantRequest()?.preset, 'plan')
    await act(async () => button(page, 'Explore').click())
    for (const [label, path] of [
      ['Tools', '/tool-market'],
      ['Models', '/pricing'],
      ['Open source', '/challenges'],
    ]) {
      await act(async () => {
        const choice = [
          ...page.container.querySelectorAll<HTMLButtonElement>(
            '.l0-discover-switch button'
          ),
        ].find((node) => node.textContent === label)
        assert.ok(choice)
        choice.click()
      })
      assert.ok(page.container.querySelector(`a[href="${path}"]`))
    }
    assert.match(page.container.textContent ?? '', /What will you make\?/)
    assert.equal(
      page.container
        .querySelector('[data-testid="l0-activation"]')
        ?.getAttribute('data-access-mode'),
      'unknown'
    )
    assert.doesNotMatch(
      page.container.textContent ?? '',
      /Create API key|Open setup guide/
    )
    await unmountPage(page)
  })
  test('shows the configured payment threshold and hides it when paid activation is disabled', async () => {
    const onboarding = {
      activation_complete: false,
      credential_complete: false,
      first_request_complete: false,
      stage: 'activate' as const,
      paid_activation_enabled: true,
      paid_activation_min_amount: 5,
    }
    for (const language of ['en', 'zhCN', 'zhTW', 'fr', 'ja', 'ru', 'vi']) {
      await i18n.changeLanguage(language)
      const page = await renderPage(false, undefined, null, { onboarding })
      assert.ok(
        page.container.querySelector('[data-testid="l0-paid-progress"]')
      )
      assert.doesNotMatch(page.container.textContent ?? '', /NaN|undefined/)
      await unmountPage(page)
    }
    await i18n.changeLanguage('en')
    for (const override of [
      { ...onboarding, paid_activation_enabled: false },
      { ...onboarding, paid_activation_min_amount: undefined },
    ]) {
      const page = await renderPage(false, undefined, null, {
        onboarding: override,
      })
      assert.equal(
        page.container.querySelector('[data-testid="l0-paid-progress"]'),
        null
      )
      await unmountPage(page)
    }
  })
  test('streams a custom question inline, rejects invalid input and never opens a second drawer', async () => {
    const sent: Array<{ message: string; client_turn_id: string }> = []
    globalThis.fetch = (async (url, init) => {
      assert.equal(url, '/api/assistant/chat')
      sent.push(JSON.parse(String(init?.body)))
      return new Response(
        JSON.stringify({
          choices: [{ message: { content: 'Inline answer' } }],
        }),
        { headers: { 'content-type': 'application/json' } }
      )
    }) as typeof fetch
    const page = await renderPage()
    const input = page.container.querySelector<HTMLInputElement>('#l0-question')
    assert.ok(input)
    const form = input.closest('form')
    assert.ok(form)
    const submit = form.querySelector<HTMLButtonElement>(
      'button[type="submit"]'
    )
    assert.ok(submit)
    assert.equal(submit.disabled, true)
    const setter = Object.getOwnPropertyDescriptor(
      window.HTMLInputElement.prototype,
      'value'
    )?.set
    assert.ok(setter)
    for (const [value, disabled] of [
      ['。', true],
      ['Help me build a tool', false],
    ] as const) {
      await act(async () => {
        setter.call(input, value)
        input.dispatchEvent(new window.Event('input', { bubbles: true }))
        await flushEffects()
      })
      assert.equal(submit.disabled, disabled)
    }
    assert.equal(sent.length, 0)
    await act(async () => {
      form.dispatchEvent(
        new window.Event('submit', { bubbles: true, cancelable: true })
      )
      await flushEffects()
    })
    const deadline = Date.now() + 2_000
    while (
      Date.now() < deadline &&
      !page.container
        .querySelector('.l0-answer')
        ?.textContent?.includes('Inline answer')
    ) {
      await act(flushEffects)
    }
    assert.equal(sent.length, 1)
    assert.equal(sent[0].message, 'Help me build a tool')
    assert.ok(sent[0].client_turn_id)
    assert.match(
      page.container.querySelector('.l0-answer')?.textContent ?? '',
      /Inline answer/
    )
    assert.equal(consumeQueuedAssistantRequest(), undefined)
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
    const unavailableMessage = 'Challenges are temporarily unavailable.'
    const deadline = Date.now() + 1_000
    while (
      Date.now() < deadline &&
      !page.container.textContent?.includes(unavailableMessage)
    ) {
      await act(flushEffects)
    }
    assert.equal(
      page.gets.filter((url) => url.startsWith('/api/open-source-bounties?'))
        .length,
      1
    )
    assert.ok(page.container.textContent?.includes(unavailableMessage))
    const config = page.getConfigs.find((_, index) =>
      page.gets[index].startsWith('/api/open-source-bounties?')
    )
    assert.equal(config?.skipBusinessError, true)
    assert.equal(config?.skipErrorHandler, true)
    await unmountPage(page)
  })
})
