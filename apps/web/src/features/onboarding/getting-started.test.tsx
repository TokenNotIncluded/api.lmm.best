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
const { useSystemConfigStore } = await import('@/stores/system-config-store')
const { GettingStarted } = await import('./getting-started')
const originalGet = api.get
const originalPost = api.post
const originalFetch = globalThis.fetch
const originalConfig = useSystemConfigStore.getState().config
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
    '/guide',
    '/test-key',
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
      return { data: { success: true, data: { ...currentUser } } }
    }
    if (url === '/api/assistant/registration-check') {
      return {
        data: {
          success: true,
          data: accessRequest ?? { state: 'context_needed' },
        },
      }
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
async function askInline(page: Awaited<ReturnType<typeof renderPage>>) {
  const input = page.container.querySelector<HTMLInputElement>('#l0-question')
  assert.ok(input)
  const setter = Object.getOwnPropertyDescriptor(
    window.HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(setter)
  await act(async () => {
    setter.call(input, 'Help me connect my coding client')
    input.dispatchEvent(new window.Event('input', { bubbles: true }))
    await flushEffects()
  })
  await act(async () => {
    input
      .closest('form')
      ?.dispatchEvent(
        new window.Event('submit', { bubbles: true, cancelable: true })
      )
    await flushEffects()
  })
  await act(flushEffects)
}
afterEach(() => {
  consumeQueuedAssistantRequest()
  api.get = originalGet
  api.post = originalPost
  globalThis.fetch = originalFetch
  useAuthStore.getState().auth.reset('complete')
  useSystemConfigStore.setState({ config: originalConfig })
  window.localStorage.clear()
  window.sessionStorage.clear()
  document.body.replaceChildren()
})
after(() => domWindow.close())

describe('getting started access boundaries', () => {
  for (const status of ['pending', 'rejected', 'approved']) {
    test(`retired ${status} letters never become an upgrade gate or grant`, async () => {
      const page = await renderPage()
      page.queryClient.setQueryData(
        ['assistant-developer-access-request', user.id],
        {
          status,
          reason: 'retired reason',
          ai_recommendation: 'retired letter',
        }
      )
      try {
        await act(async () => {
          button(page, 'Unlock').click()
          await flushEffects()
        })
        assert.doesNotMatch(
          page.container.textContent ?? '',
          /retired reason|retired letter|Pending review|Access request rejected/
        )
        assert.ok(
          !page.gets.some((url) => url.includes('developer-access/request'))
        )
        assert.equal(
          useAuthStore.getState().auth.user?.developer_access_granted,
          false
        )
        assert.equal(page.router.state.location.pathname, '/getting-started')
      } finally {
        await unmountPage(page)
      }
    })
  }
  test('a focus refresh leaves L0 after server-confirmed activation', async () => {
    const page = await renderPage()
    try {
      page.currentUser.developer_access_granted = true
      await act(async () => {
        window.dispatchEvent(new Event('focus'))
        await flushEffects()
      })
      const deadline = Date.now() + 2000
      while (
        Date.now() < deadline &&
        page.router.state.location.pathname !== '/dashboard'
      ) {
        await act(flushEffects)
      }
      assert.equal(page.router.state.location.pathname, '/dashboard')
    } finally {
      await unmountPage(page)
    }
  })
  for (const history of [
    null,
    {
      id: 9910,
      status: 'pending',
      reason: 'Old request',
      created_at: 1,
      reviewed_at: 0,
    },
  ]) {
    test(`inline grant refreshes access and leaves L0 with ${history ? 'historical pending' : 'no'} application`, async () => {
      const page = await renderPage()
      if (history) {
        page.queryClient.setQueryData(
          ['assistant-developer-access-request', user.id],
          history
        )
      }
      globalThis.fetch = (async () => {
        page.currentUser.developer_access_granted = true
        return new Response(
          JSON.stringify({
            choices: [{ message: { content: 'Access verified.' } }],
            lmm_assistant_tools: [
              { name: 'grant_l1_access', status: 'output-available' },
            ],
          }),
          { headers: { 'content-type': 'application/json' } }
        )
      }) as typeof fetch
      try {
        await askInline(page)
        const deadline = Date.now() + 2_000
        while (
          Date.now() < deadline &&
          page.router.state.location.pathname !== '/dashboard'
        ) {
          await act(flushEffects)
        }
        assert.equal(page.router.state.location.pathname, '/dashboard')
        assert.equal(
          useAuthStore.getState().auth.user?.developer_access_granted,
          true
        )
        assert.equal(
          page.gets.filter((url) => url === '/api/user/self').length,
          2
        )
      } finally {
        await unmountPage(page)
      }
    })
  }
  for (const status of ['output-error', 'approval-requested', undefined]) {
    test(`a ${status ?? 'missing'} grant trace cannot change access from reply text`, async () => {
      const page = await renderPage()
      globalThis.fetch = (async () =>
        new Response(
          JSON.stringify({
            choices: [{ message: { content: 'L1 access is active.' } }],
            ...(status
              ? { lmm_assistant_tools: [{ name: 'grant_l1_access', status }] }
              : {}),
          }),
          { headers: { 'content-type': 'application/json' } }
        )) as typeof fetch
      try {
        await askInline(page)
        assert.equal(page.router.state.location.pathname, '/getting-started')
        assert.equal(
          useAuthStore.getState().auth.user?.developer_access_granted,
          false
        )
        assert.equal(
          page.gets.filter((url) => url === '/api/user/self').length,
          1
        )
      } finally {
        await unmountPage(page)
      }
    })
  }
  for (const readFailure of ['unavailable', 'pending']) {
    test(`completed inline answer remains usable when grant refresh is ${readFailure}`, async () => {
      const page = await renderPage()
      const previousGet = api.get
      let finishRead: (() => void) | undefined
      api.get = (async (url, config) => {
        if (url !== '/api/user/self') return previousGet(url, config)
        if (readFailure === 'unavailable') throw new Error('HTTP 503')
        await new Promise<void>((resolve) => {
          finishRead = resolve
        })
        return { data: { success: true, data: { ...page.currentUser } } }
      }) as typeof api.get
      globalThis.fetch = (async () =>
        new Response(
          JSON.stringify({
            choices: [
              { message: { content: 'Access verification finished.' } },
            ],
            lmm_assistant_tools: [
              { name: 'grant_l1_access', status: 'output-available' },
            ],
          }),
          { headers: { 'content-type': 'application/json' } }
        )) as typeof fetch
      try {
        await askInline(page)
        assert.equal(page.router.state.location.pathname, '/getting-started')
        assert.equal(
          useAuthStore.getState().auth.user?.developer_access_granted,
          false
        )
        assert.match(
          page.container.querySelector('.l0-answer')?.textContent ?? '',
          /Access verification finished/
        )
        assert.equal(
          page.container
            .querySelector('.l0-composer')
            ?.getAttribute('data-phase'),
          'done'
        )
      } finally {
        await act(async () => {
          finishRead?.()
          await flushEffects()
        })
        await unmountPage(page)
      }
    })
  }
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
    for (const text of [
      'API access enabled',
      'Start with a separate key',
      'Copy the API address',
    ]) {
      assert.ok(l1.container.textContent?.includes(text))
    }
    await unmountPage(l1)
  })
  test('offers conversation and visible support without querying or submitting retired letters', async () => {
    const page = await renderPage()
    page.queryClient.setQueryData(
      ['assistant-developer-access-request', user.id],
      {
        status: 'rejected',
        ai_recommendation: 'retired recommendation',
        admin_note: 'old rejection',
      }
    )
    await act(flushEffects)
    assert.ok(
      page.container.querySelector(
        '[data-testid="l0-contact-support"][href="/support"]'
      )
    )
    assert.equal(
      page.container.querySelector('textarea#access-request-reason'),
      null
    )
    assert.ok(
      !page.gets.some((url) => url.includes('developer-access/request'))
    )
    assert.doesNotMatch(
      page.container.textContent ?? '',
      /old rejection|retired recommendation|Apply for access|Pending review/
    )
    await act(async () => {
      button(page, 'Explore').click()
      button(page, 'Chat to enable L1').click()
      await flushEffects()
    })
    assert.equal(
      page.container.querySelector<HTMLElement>('#l0-panel-chat')?.hidden,
      false
    )
    assert.equal(
      page.container.querySelector<HTMLElement>('#l0-panel-access')?.hidden,
      true
    )
    assert.equal(consumeQueuedAssistantRequest(), undefined)
    await unmountPage(page)
  })
  test('shows a current hold and a support escape path, never a letter editor', async () => {
    const page = await renderPage(false, undefined, { state: 'held' })
    await act(async () => button(page, 'Unlock').click())
    assert.match(
      page.container.textContent ?? '',
      /Registration needs another check/
    )
    assert.ok(
      page.container.querySelector('#l0-panel-access a[href="/support"]')
    )
    assert.equal(
      page.container.querySelector('textarea#access-request-reason'),
      null
    )
    await unmountPage(page)
  })
  test('an actual active state refreshes the authenticated user and leaves L0', async () => {
    const registration = { state: 'context_needed' }
    const page = await renderPage(false, undefined, registration, { id: 7005 })
    registration.state = 'active'
    page.currentUser.developer_access_granted = true
    await act(async () => {
      await page.queryClient.invalidateQueries({
        queryKey: ['assistant-registration-state'],
      })
      await flushEffects()
    })
    const deadline = Date.now() + 2000
    while (
      Date.now() < deadline &&
      page.router.state.location.pathname !== '/dashboard'
    ) {
      await act(flushEffects)
    }
    assert.equal(
      useAuthStore.getState().auth.user?.developer_access_granted,
      true
    )
    assert.equal(
      page.container.querySelector('[data-testid="l0-conversation"]'),
      null
    )
    await unmountPage(page)
  })
  test('lets L0 browse and fund the account before approval', async () => {
    const page = await renderPage(true)
    assert.equal(page.container.querySelector('a[href="/wallet"]'), null)
    assert.equal(consumeQueuedAssistantRequest(), undefined)
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
    useSystemConfigStore.setState({
      config: {
        ...originalConfig,
        currency: {
          ...originalConfig.currency,
          currencyUnit: 'credit',
          creditsPerUsd: 500000,
          creditsPerUsdExact: '500000',
          cnyPerUsd: 7,
          cnyPerUsdExact: '7',
        },
      },
    })
    const onboarding = {
      activation_complete: false,
      credential_complete: false,
      first_request_complete: false,
      stage: 'activate' as const,
      paid_activation_enabled: true,
      paid_activation_min_amount: 9999,
      paid_activation_min_credits: '2500001',
    }
    const creditProgress: Partial<AuthUser> = {
      setting: { wallet_display_currency: 'USD' },
      trust_level_info: {
        level: 0,
        automatic_level: 0,
        override_level: null,
        paid_amount: 99999,
        paid_credits: '1000000',
        discount_ratio: 1,
        discount_percent: 0,
        inactivity_decay_steps: 0,
        decay_period_days: 0,
        overridden: false,
      },
    }
    for (const language of ['en', 'zhCN', 'zhTW', 'fr', 'ja', 'ru', 'vi']) {
      await i18n.changeLanguage(language)
      const page = await renderPage(false, undefined, null, {
        ...creditProgress,
        onboarding,
      })
      assert.ok(
        page.container.querySelector('[data-testid="l0-paid-progress"]')
      )
      assert.doesNotMatch(page.container.textContent ?? '', /NaN|undefined/)
      if (language === 'en') {
        assert.match(
          page.container.querySelector('.l0-rail-meta')?.textContent ?? '',
          /Top up 3\.01 USD to enable L1 immediately/
        )
      }
      await unmountPage(page)
    }
    await i18n.changeLanguage('en')
    for (const override of [
      { ...onboarding, paid_activation_enabled: false },
      { ...onboarding, paid_activation_min_credits: undefined },
    ]) {
      const page = await renderPage(false, undefined, null, {
        ...creditProgress,
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
  test('does not fetch unrelated bounty probes from the activated setup page', async () => {
    const page = await renderPage(true, new Error('Not Found'), null, {
      developer_access_granted: true,
      onboarding: {
        activation_complete: true,
        credential_complete: false,
        first_request_complete: false,
        stage: 'credential',
      },
    })
    assert.equal(
      page.gets.filter((url) => url.startsWith('/api/open-source-bounties?'))
        .length,
      0
    )
    assert.doesNotMatch(
      page.container.textContent ?? '',
      /Challenges are temporarily unavailable/
    )
    assert.ok(page.container.querySelector('a[href="/keys"]'))
    await unmountPage(page)
  })
})

test('activated onboarding offers direct setup actions without requiring a conversation', async () => {
  const page = await renderPage(false, undefined, null, {
    developer_access_granted: true,
  })
  assert.match(page.container.textContent ?? '', /Connect your first client/)
  assert.ok(page.container.querySelector('a[href="/keys"]'))
  assert.ok(page.container.querySelector('a[href="/guide"]'))
  assert.ok(page.container.querySelector('a[href="/pricing"]'))
  assert.ok(page.container.querySelector('a[draggable="true"]'))
  assert.doesNotMatch(
    page.container.textContent ?? '',
    /One conversation to get started|What the assistant can do/
  )
  await unmountPage(page)
})
