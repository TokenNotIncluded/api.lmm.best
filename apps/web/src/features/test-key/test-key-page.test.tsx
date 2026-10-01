/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { after, afterEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'

const dom = new Window({ url: 'https://api.lmm.best/test-key' })
for (const key of [
  'window',
  'document',
  'navigator',
  'history',
  'location',
  'HTMLElement',
  'HTMLInputElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'CustomEvent',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'scrollTo',
  'localStorage',
  'sessionStorage',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const {
  Outlet,
  RouterProvider,
  createRootRoute,
  createRoute,
  createRouter,
  createMemoryHistory,
} = await import('@tanstack/react-router')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { TestKeyPage } = await import('./test-key-page')
const { BookmarkletInstall } = await import('./bookmarklet-install')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const originalGet = api.get
const originalPost = api.post
const originalOpen = window.open
const flush = () => new Promise((resolve) => setTimeout(resolve, 25))
const user = {
  id: 77,
  username: 'tester',
  role: 1,
  developer_access_granted: true,
}
let copies: string[] = []
afterEach(() => {
  api.get = originalGet
  api.post = originalPost
  window.open = originalOpen
  useAuthStore.getState().auth.reset('complete')
  window.localStorage.clear()
  window.sessionStorage.clear()
  document.body.replaceChildren()
  copies = []
})
after(() => dom.close())
async function mount(
  options: {
    anonymous?: boolean
    checking?: boolean
    inactive?: boolean
    warning?: number
    component?: typeof TestKeyPage
  } = {}
) {
  useAuthStore
    .getState()
    .auth.setUser(
      options.anonymous
        ? null
        : { ...user, developer_access_granted: !options.inactive }
    )
  if (options.checking) {
    useAuthStore.getState().auth.setBootstrapState('checking')
  }
  const posts: unknown[] = []
  api.get = (async (url) => {
    if (url === '/api/user/self/groups') {
      return {
        data: {
          success: true,
          data: {
            auto: {
              desc: 'Auto',
              ratio: 1,
              ...(options.warning
                ? {
                    warning: {
                      enabled: true,
                      message: 'This group has special billing.',
                      mode: 'inline',
                      confirmations: options.warning,
                    },
                  }
                : {}),
            },
          },
        },
      }
    }
    return {
      data: {
        success: true,
        data: {
          quota_per_unit: 500000,
          server_address: 'https://api.lmm.best',
        },
      },
    }
  }) as typeof api.get
  api.post = (async (_url, body) => {
    posts.push(body)
    return {
      data: { success: true, data: { id: 123, key: 'test-fixture-secret' } },
    }
  }) as typeof api.post
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: {
      writeText: async (text: string) => {
        copies.push(text)
      },
    },
  })
  const rootRoute = createRootRoute({ component: Outlet })
  const route = createRoute({
    getParentRoute: () => rootRoute,
    path: '/test-key',
    component: options.component ?? TestKeyPage,
  })
  const children = ['/sign-in', '/getting-started', '/keys'].map((path) =>
    createRoute({
      getParentRoute: () => rootRoute,
      path,
      component: () => null,
    })
  )
  const router = createRouter({
    routeTree: rootRoute.addChildren([route, ...children]),
    history: createMemoryHistory({ initialEntries: ['/test-key'] }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
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
    await flush()
  })
  await act(flush)
  const unmount = async () => {
    await act(async () => root.unmount())
    client.clear()
    container.remove()
  }
  const chooseGroup = async () => {
    const select = container.querySelector('select')
    assert.ok(select)
    await act(async () => {
      select.value = 'auto'
      select.dispatchEvent(new Event('change', { bubbles: true }))
      await flush()
    })
  }
  const submit = async () => {
    const form = container.querySelector('form')
    assert.ok(form)
    await act(async () => {
      form.dispatchEvent(
        new Event('submit', { bubbles: true, cancelable: true })
      )
      await flush()
    })
  }
  return { container, posts, client, unmount, chooseGroup, submit }
}

describe('test key popup', () => {
  test('shows pending authentication before offering sign-in and never creates a key automatically', async () => {
    const page = await mount({ anonymous: true, checking: true })
    assert.ok(page.container.querySelector('[role="status"]'))
    assert.equal(page.container.querySelector('a[href*="sign-in"]'), null)
    assert.equal(page.container.querySelector('form'), null)
    assert.equal(page.posts.length, 0)
    await act(async () => {
      useAuthStore.getState().auth.reset('complete')
      await flush()
    })
    assert.ok(page.container.querySelector('a[href*="sign-in"]'))
    assert.equal(page.posts.length, 0)
    await page.unmount()
  })
  test('does not create on navigation, requires login and preserves the destination', async () => {
    const page = await mount({ anonymous: true })
    assert.equal(page.posts.length, 0)
    const href = page.container.querySelector('a')?.getAttribute('href') ?? ''
    assert.match(href, /sign-in/)
    assert.match(decodeURIComponent(href), /\/test-key/)
    assert.equal(page.container.querySelector('form'), null)
    await page.unmount()
  })
  test('keeps unactivated accounts on the setup path', async () => {
    const page = await mount({ inactive: true })
    assert.ok(page.container.querySelector('a[href="/getting-started"]'))
    assert.equal(page.container.querySelector('form'), null)
    assert.equal(page.posts.length, 0)
    await page.unmount()
  })
  test('creates once after an explicit action, copies, and never caches the secret', async () => {
    const page = await mount()
    assert.equal(page.posts.length, 0)
    await page.chooseGroup()
    const form = page.container.querySelector('form')
    assert.ok(form)
    await act(async () => {
      form.dispatchEvent(
        new Event('submit', { bubbles: true, cancelable: true })
      )
      form.dispatchEvent(
        new Event('submit', { bubbles: true, cancelable: true })
      )
      await flush()
    })
    assert.equal(page.posts.length, 1)
    const payload = page.posts[0] as Record<string, unknown>
    assert.equal(payload.one_time_reveal, true)
    assert.equal(payload.expired_time, -1)
    assert.equal(payload.model_limits_enabled, false)
    assert.equal(payload.allow_ips, '')
    assert.deepEqual(copies, ['sk-test-fixture-secret'])
    assert.match(page.container.textContent ?? '', /Created and copied/)
    assert.equal(page.container.querySelector('form'), null)
    assert.doesNotMatch(
      JSON.stringify(
        page.client
          .getQueryCache()
          .getAll()
          .map((q) => q.state.data)
      ),
      /test-fixture-secret/
    )
    for (const storage of [localStorage, sessionStorage]) {
      for (let i = 0; i < storage.length; i++) {
        assert.doesNotMatch(
          storage.getItem(storage.key(i) ?? '') ?? '',
          /test-fixture-secret/
        )
      }
    }
    await act(async () => {
      window.dispatchEvent(new Event('pagehide'))
      window.dispatchEvent(new Event('pageshow'))
      await flush()
    })
    assert.equal(page.container.querySelector('#test-key-secret'), null)
    await page.unmount()
  })
  test('requires the existing group warning acknowledgments', async () => {
    const page = await mount({ warning: 2 })
    await page.chooseGroup()
    await page.submit()
    assert.equal(page.posts.length, 0)
    const confirmation = [...page.container.querySelectorAll('button')].find(
      (button) => button.textContent?.includes('I understand')
    )
    assert.ok(confirmation)
    await act(async () => confirmation.click())
    await page.submit()
    assert.equal(page.posts.length, 0)
    await act(async () => confirmation.click())
    await page.submit()
    assert.equal(page.posts.length, 1)
    assert.equal(
      (page.posts[0] as Record<string, unknown>).group_warning_confirmations,
      2
    )
    await page.unmount()
  })
  test('shows manual copy when clipboard access is denied', async () => {
    const page = await mount()
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: undefined,
    })
    Object.defineProperty(document, 'execCommand', {
      configurable: true,
      value: () => false,
    })
    await page.chooseGroup()
    await page.submit()
    assert.match(
      page.container.textContent ?? '',
      /Key created\. Copy it below/
    )
    assert.doesNotMatch(page.container.textContent ?? '', /Created and copied/)
    assert.equal(
      (page.container.querySelector('#test-key-secret') as HTMLInputElement)
        .value,
      'sk-test-fixture-secret'
    )
    await page.unmount()
  })
  test('refreshes changed group warnings without retrying creation automatically', async () => {
    const page = await mount()
    await page.chooseGroup()
    let postCount = 0
    const originalFixtureGet = api.get
    api.get = (async (url) => {
      if (url === '/api/user/self/groups') {
        return {
          data: {
            success: true,
            data: {
              auto: {
                desc: 'Auto',
                ratio: 1,
                warning: {
                  enabled: true,
                  message: 'Updated billing warning.',
                  mode: 'inline',
                  confirmations: 2,
                },
              },
            },
          },
        }
      }
      return originalFixtureGet(url)
    }) as typeof api.get
    api.post = (async () => {
      postCount++
      if (postCount === 1) {
        throw {
          isAxiosError: true,
          response: {
            status: 422,
            data: {
              code: 'GROUP_WARNING_CONFIRMATION_REQUIRED',
              message: 'Updated billing warning.',
            },
          },
        }
      }
      return {
        data: { success: true, data: { id: 123, key: 'test-fixture-secret' } },
      }
    }) as typeof api.post
    await page.submit()
    assert.equal(postCount, 1)
    assert.ok(page.container.querySelector('form'))
    assert.match(page.container.textContent ?? '', /Updated billing warning/)
    assert.equal(page.container.querySelector('#test-key-secret'), null)
    await page.submit()
    assert.equal(postCount, 1)
    for (let i = 0; i < 2; i++) {
      const confirm = [...page.container.querySelectorAll('button')].find(
        (button) => (button.textContent ?? '').includes('I understand')
      )
      assert.ok(confirm)
      await act(async () => {
        confirm.click()
        await flush()
      })
      assert.equal(postCount, 1)
    }
    await page.submit()
    assert.equal(postCount, 2)
    assert.ok(page.container.querySelector('#test-key-secret'))
    await page.unmount()
  })
  test('does not automatically retry an ambiguous creation response', async () => {
    const page = await mount()
    api.post = (async () => {
      throw new Error('Connection lost')
    }) as typeof api.post
    await page.chooseGroup()
    await page.submit()
    assert.match(
      page.container.textContent ?? '',
      /Creation could not be confirmed/
    )
    assert.equal(page.container.querySelector('form'), null)
    assert.ok(page.container.querySelector('a[href="/keys"]'))
    await page.unmount()
  })
  test('discards a late response after leaving the page, even after BFCache restore', async () => {
    const page = await mount()
    let resolvePost: (value: unknown) => void = () => {}
    api.post = (() =>
      new Promise<unknown>((resolve) => {
        resolvePost = resolve
      })) as typeof api.post
    await page.chooseGroup()
    await page.submit()
    await act(async () => {
      window.dispatchEvent(new Event('pagehide'))
      window.dispatchEvent(new Event('pageshow'))
      resolvePost({ data: { success: true, data: { key: 'late-secret' } } })
      await flush()
    })
    assert.equal(page.container.querySelector('#test-key-secret'), null)
    assert.deepEqual(copies, [])
    await page.unmount()
  })
  test('clears a revealed key when the account changes', async () => {
    const page = await mount()
    await page.chooseGroup()
    await page.submit()
    await act(async () => {
      useAuthStore.getState().auth.setUser({ ...user, id: 88 })
      await flush()
    })
    assert.equal(page.container.querySelector('#test-key-secret'), null)
    await page.unmount()
  })
  test('installs a real draggable script URL and opens a popup without a false blocked fallback', async () => {
    const page = await mount({ component: () => <BookmarkletInstall /> })
    const link = page.container.querySelector<HTMLAnchorElement>('a[draggable]')
    assert.ok(link)
    assert.match(
      link.getAttribute('href') ?? '',
      /^javascript:void\(window.open/
    )
    assert.match(page.container.textContent ?? '', /Ctrl \+ Shift \+ B/)
    assert.match(page.container.textContent ?? '', /⌘ \+ Shift \+ B/)
    const calls: unknown[][] = []
    window.open = ((...args: unknown[]) => {
      calls.push(args)
      return null
    }) as typeof window.open
    await act(async () => link.click())
    assert.equal(calls.length, 1)
    assert.equal(calls[0][0], 'https://api.lmm.best/test-key')
    assert.equal(location.href, 'https://api.lmm.best/test-key')
    await page.unmount()
  })
})
