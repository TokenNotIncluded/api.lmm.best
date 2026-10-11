/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const dom = new Window({ url: 'http://localhost/setup' })
dom.document.write('<!doctype html><html><head></head><body></body></html>')
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'Element',
  'Node',
  'Event',
  'MouseEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'scrollTo',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: key === 'window' ? dom : dom[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} = await import('@tanstack/react-router')
const { api } = await import('@/lib/api')
const { getSetupStatus, submitSetup, isCliOnlySetupError } =
  await import('./api')
const { SetupWizard } = await import('./setup-wizard')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const originalGet = api.get
const originalPost = api.post
after(() => {
  api.get = originalGet
  api.post = originalPost
  dom.close()
})

const incomplete = {
  success: true,
  data: {
    status: false,
    root_init: true,
    database_type: 'postgres',
    initialization_method: 'cli',
  },
}
const delay = () => new Promise((resolve) => setTimeout(resolve, 15))
async function eventually(check: () => boolean) {
  for (let i = 0; i < 100; i++) {
    await act(async () => {
      await delay()
    })
    if (check()) return
  }
  assert.ok(check(), 'Expected UI state did not appear')
}
async function renderWizard() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const rootRoute = createRootRoute({ component: Outlet })
  const setupRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/setup',
    component: SetupWizard,
  })
  const homeRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/',
    component: () => <div>Home after setup</div>,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([setupRoute, homeRoute]),
    history: createMemoryHistory({ initialEntries: ['/setup'] }),
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <QueryClientProvider client={client}>
        <I18nextProvider i18n={i18n}>
          <RouterProvider router={router} />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await router.load()
  })
  return {
    container,
    router,
    async close() {
      await act(async () => root.unmount())
      client.clear()
      container.remove()
    },
  }
}
function button(container: HTMLElement, text: string) {
  const found = [...container.querySelectorAll('button')].find(
    (node) => node.textContent?.trim() === text
  )
  assert.ok(found, `Missing button: ${text}`)
  return found
}

test('CLI-only incomplete installation shows operator command without password or web submission', async () => {
  let gets = 0,
    posts = 0
  api.get = (async (url: string) => {
    assert.equal(url, '/api/setup')
    gets++
    return { data: incomplete }
  }) as typeof api.get
  api.post = (async () => {
    posts++
    throw new Error('Unexpected web initialization')
  }) as typeof api.post
  const view = await renderWizard()
  try {
    await eventually(
      () =>
        view.container.textContent?.includes('lmm-core complete-setup') === true
    )
    assert.match(
      view.container.textContent ?? '',
      /Operator initialization required/
    )
    assert.match(view.container.textContent ?? '', /offline database owner/)
    assert.equal(view.container.querySelector('input'), null)
    assert.equal(view.container.querySelector('form'), null)
    assert.equal(
      view.container.querySelector('.setup-editorial-step-list'),
      null
    )
    assert.doesNotMatch(
      view.container.textContent ?? '',
      /Create credentials for the root user/
    )
    await act(async () => button(view.container, 'Check setup status').click())
    await eventually(() => gets >= 2)
    assert.equal(posts, 0)
    assert.equal(view.router.state.location.pathname, '/setup')
  } finally {
    await view.close()
  }
})

test('operator status refresh redirects only after the backend reports actual completion', async () => {
  let complete = false
  api.get = (async () => ({
    data: { ...incomplete, data: { ...incomplete.data, status: complete } },
  })) as typeof api.get
  const view = await renderWizard()
  try {
    await eventually(
      () => view.container.textContent?.includes('Check setup status') === true
    )
    assert.equal(view.router.state.location.pathname, '/setup')
    complete = true
    await act(async () => button(view.container, 'Check setup status').click())
    await eventually(() => view.router.state.location.pathname === '/')
    assert.match(view.container.textContent ?? '', /Home after setup/)
  } finally {
    await view.close()
  }
})

test('unknown setup facts or unsupported policy remain a retryable error without initialization controls', async () => {
  for (const data of [
    { success: true, data: { status: false } },
    {
      ...incomplete,
      data: { ...incomplete.data, initialization_method: 'unrecognized' },
    },
    {
      ...incomplete,
      data: { ...incomplete.data, status: true, root_init: false },
    },
    { success: false, message: 'Installation state unavailable' },
  ]) {
    api.get = (async () => ({ data })) as typeof api.get
    const view = await renderWizard()
    try {
      await eventually(
        () =>
          view.container.textContent?.includes(
            'We could not load the setup status.'
          ) === true
      )
      assert.equal(view.container.querySelector('input'), null)
      assert.equal(view.container.querySelector('form'), null)
      assert.equal(
        view.container.querySelector('.setup-editorial-frame-footer'),
        null
      )
      assert.equal(view.router.state.location.pathname, '/setup')
      button(view.container, 'Retry')
    } finally {
      await view.close()
    }
  }
})

test('a setup transport failure stays an error and a genuine retry can reveal CLI guidance', async () => {
  let unavailable = true
  api.get = (async () => {
    if (unavailable) throw new Error('offline')
    return { data: incomplete }
  }) as typeof api.get
  const view = await renderWizard()
  try {
    await eventually(
      () =>
        view.container.textContent?.includes(
          'We could not load the setup status.'
        ) === true
    )
    unavailable = false
    await act(async () => button(view.container, 'Retry').click())
    await eventually(
      () =>
        view.container.textContent?.includes('lmm-core complete-setup') === true
    )
    assert.equal(view.container.querySelector('input'), null)
  } finally {
    await view.close()
  }
})

test('CLI-only refusal preserves HTTP rejection and is distinguished from unrelated forbidden errors', async () => {
  const refusal = {
    isAxiosError: true,
    response: { status: 403, data: { success: false, code: 'SETUP_CLI_ONLY' } },
  }
  api.post = (async (url: string, _payload: unknown, config: unknown) => {
    assert.equal(url, '/api/setup')
    assert.deepEqual(config, {
      skipBusinessError: true,
      skipErrorHandler: true,
    })
    throw refusal
  }) as typeof api.post
  await assert.rejects(
    () => submitSetup({}),
    (error) => error === refusal
  )
  assert.equal(isCliOnlySetupError(refusal), true)
  assert.equal(
    isCliOnlySetupError({
      isAxiosError: true,
      response: { status: 403, data: { code: 'ACCESS_DENIED' } },
    }),
    false
  )
  assert.equal(
    isCliOnlySetupError({
      isAxiosError: true,
      response: { status: 503, data: { code: 'SETUP_CLI_ONLY' } },
    }),
    false
  )
  assert.equal(isCliOnlySetupError(new Error('SETUP_CLI_ONLY')), false)
})

test('a legacy setup response is still supported without falsely inferring CLI completion', async () => {
  api.get = (async () => ({
    data: {
      success: true,
      data: { status: false, root_init: false, database_type: 'postgres' },
    },
  })) as typeof api.get
  assert.equal((await getSetupStatus()).data?.status, false)
  const view = await renderWizard()
  try {
    await eventually(
      () => view.container.querySelector('.setup-editorial-step-list') !== null
    )
    assert.equal(
      view.container.textContent?.includes('lmm-core complete-setup'),
      false
    )
    assert.equal(view.router.state.location.pathname, '/setup')
  } finally {
    await view.close()
  }
})

test('a stale web wizard receiving CLI_ONLY replaces credential controls with operator guidance', async () => {
  let posts = 0
  api.get = (async () => ({
    data: {
      success: true,
      data: { status: false, root_init: true, database_type: 'postgres' },
    },
  })) as typeof api.get
  api.post = (async () => {
    posts++
    throw {
      isAxiosError: true,
      response: {
        status: 403,
        data: { success: false, code: 'SETUP_CLI_ONLY' },
      },
    }
  }) as typeof api.post
  const view = await renderWizard()
  try {
    await eventually(
      () => view.container.querySelector('.setup-editorial-step-list') !== null
    )
    await act(async () => button(view.container, 'Next').click())
    const username = view.container.querySelector<HTMLInputElement>(
      'input[autocomplete="username"]'
    )
    const password = view.container.querySelector<HTMLInputElement>(
      'input[autocomplete="current-password"]'
    )
    assert.ok(username)
    assert.ok(password)
    const setValue = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      'value'
    )?.set
    assert.ok(setValue)
    await act(async () => {
      setValue.call(username, 'synthetic-operator')
      username.dispatchEvent(new Event('input', { bubbles: true }))
      setValue.call(password, 'synthetic-operator-password')
      password.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => button(view.container, 'Next').click())
    await act(async () => button(view.container, 'Next').click())
    await act(async () => button(view.container, 'Initialize system').click())
    await eventually(
      () =>
        view.container.textContent?.includes('lmm-core complete-setup') === true
    )
    assert.equal(posts, 1)
    assert.equal(view.container.querySelector('input'), null)
    assert.equal(view.container.querySelector('form'), null)
    assert.equal(view.router.state.location.pathname, '/setup')
  } finally {
    await view.close()
  }
})
