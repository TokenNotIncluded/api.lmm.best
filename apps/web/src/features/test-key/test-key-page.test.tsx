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
const { useSystemConfigStore } = await import('@/stores/system-config-store')
const { useWalletCurrencyPreferenceStore } =
  await import('@/stores/wallet-currency-preference-store')
const { TestKeyPage } = await import('./test-key-page')
const { BookmarkletInstall } = await import('./bookmarklet-install')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const originalGet = api.get
const originalPost = api.post
const originalOpen = window.open
const originalConfig = useSystemConfigStore.getState().config
const originalPreference =
  useWalletCurrencyPreferenceStore.getState().preference
const cleanups = new Set<() => Promise<void>>()
const flush = () => new Promise((resolve) => setTimeout(resolve, 25))
const user = {
  id: 77,
  username: 'tester',
  role: 1,
  developer_access_granted: true,
}
let copies: string[] = []
afterEach(async () => {
  for (const cleanup of cleanups) await cleanup()
  api.get = originalGet
  api.post = originalPost
  window.open = originalOpen
  useAuthStore.getState().auth.reset('complete')
  useSystemConfigStore.setState({ config: originalConfig })
  useWalletCurrencyPreferenceStore.getState().setPreference(originalPreference)
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
    displayCurrency?: '' | 'CREDIT' | 'CNY' | 'USD'
    language?: string
    status?: Record<string, unknown>
  } = {}
) {
  await i18n.changeLanguage(options.language ?? 'en')
  useWalletCurrencyPreferenceStore.getState().setPreference('')
  useAuthStore.getState().auth.setUser(
    options.anonymous
      ? null
      : {
          ...user,
          developer_access_granted: !options.inactive,
          setting: { wallet_display_currency: options.displayCurrency ?? '' },
        }
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
          currency_unit: 'credit',
          credits_per_usd: '500000',
          cny_per_usd: '6.8',
          legacy_pricing_units_per_usd: '1',
          quota_per_unit: 500000,
          quota_display_type: 'CNY',
          usd_exchange_rate: 99,
          server_address: 'https://api.lmm.best',
          ...options.status,
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
  const children = ['/sign-in', '/wallet', '/keys'].map((path) =>
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
    cleanups.delete(unmount)
    await act(async () => root.unmount())
    client.clear()
    container.remove()
  }
  cleanups.add(unmount)
  const editBudget = async (value: string) => {
    const input = container.querySelector<HTMLInputElement>('#test-key-budget')
    assert.ok(input)
    const setValue = Object.getOwnPropertyDescriptor(
      dom.HTMLInputElement.prototype,
      'value'
    )?.set
    assert.ok(setValue)
    await act(async () => {
      setValue.call(input, value)
      input.dispatchEvent(new Event('input', { bubbles: true }))
      await flush()
    })
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
  return { container, posts, client, unmount, chooseGroup, submit, editBudget }
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
    assert.ok(page.container.querySelector('a[href="/wallet"]'))
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
    assert.equal(payload.remain_quota, 500000)
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
  test('creates literal integer quota for USD, CNY and Credit budget input', async () => {
    for (const example of [
      {
        displayCurrency: 'USD' as const,
        amount: '1.25',
        expectedQuota: 625000,
        label: 'USD',
      },
      {
        displayCurrency: 'CNY' as const,
        amount: '6.8',
        expectedQuota: 500000,
        label: 'CNY',
      },
      {
        displayCurrency: 'CREDIT' as const,
        amount: '1',
        expectedQuota: 1,
        label: 'Credits',
      },
    ]) {
      const page = await mount({ displayCurrency: example.displayCurrency })
      assert.equal(
        page.container.querySelector('label[for="test-key-budget"]')
          ?.textContent,
        `Spending limit (${example.label})`
      )
      await page.chooseGroup()
      await page.editBudget(example.amount)
      assert.equal(page.posts.length, 0)
      await page.submit()
      assert.equal(page.posts.length, 1)
      assert.equal(
        (page.posts[0] as Record<string, unknown>).remain_quota,
        example.expectedQuota
      )
      await page.unmount()
    }
  })
  test('uses the provider language for automatic budget units', async () => {
    for (const example of [
      { language: 'en', expectedQuota: 500000, label: 'Spending limit (USD)' },
      { language: 'zhCN', expectedQuota: 73529, label: '额度上限（CNY）' },
      { language: 'zhTW', expectedQuota: 73529, label: '額度上限（CNY）' },
    ]) {
      const page = await mount({ language: example.language })
      assert.equal(
        page.container.querySelector('label[for="test-key-budget"]')
          ?.textContent,
        example.label
      )
      assert.equal(
        page.container.querySelector<HTMLInputElement>('#test-key-budget')
          ?.value,
        example.language === 'en' ? '1' : '0.9999944'
      )
      await page.chooseGroup()
      await page.submit()
      assert.equal(page.posts.length, 1)
      assert.equal(
        (page.posts[0] as Record<string, unknown>).remain_quota,
        example.expectedQuota
      )
      await page.unmount()
    }
  })
  test('keeps one Credit through exact micro input, reactive currency, FX and language changes', async () => {
    const page = await mount({
      displayCurrency: 'USD',
      status: {
        credits_per_usd: '500000',
        legacy_pricing_units_per_usd: '1',
      },
    })
    await page.chooseGroup()
    await page.editBudget('0.000002')
    assert.equal(page.posts.length, 0)
    await act(async () => {
      useAuthStore
        .getState()
        .auth.setUser({ ...user, setting: { wallet_display_currency: 'CNY' } })
      await flush()
    })
    assert.equal(
      page.container.querySelector<HTMLInputElement>('#test-key-budget')?.value,
      '0.0000136'
    )
    assert.equal(
      page.container.querySelector('label[for="test-key-budget"]')?.textContent,
      'Spending limit (CNY)'
    )
    await act(async () => {
      useSystemConfigStore.getState().setConfig({
        currency: {
          ...useSystemConfigStore.getState().config.currency,
          cnyPerUsd: 13.6,
          cnyPerUsdExact: '13.6',
        },
      })
      await flush()
    })
    assert.equal(
      page.container.querySelector<HTMLInputElement>('#test-key-budget')?.value,
      '0.0000272'
    )
    await act(async () => {
      useAuthStore.getState().auth.setUser({
        ...user,
        setting: { wallet_display_currency: 'CREDIT' },
      })
      await i18n.changeLanguage('zhCN')
      await flush()
    })
    assert.equal(
      page.container.querySelector<HTMLInputElement>('#test-key-budget')?.value,
      '1'
    )
    assert.equal(
      page.container.querySelector<HTMLInputElement>('#test-key-budget')?.step,
      '1'
    )
    assert.equal(
      page.container.querySelector('label[for="test-key-budget"]')?.textContent,
      '额度上限（Credits）'
    )
    await page.editBudget('1.000000000000000000000000000001')
    await page.submit()
    assert.equal(page.posts.length, 0)
    await page.editBudget('1')
    await page.submit()
    assert.equal(page.posts.length, 1)
    assert.equal((page.posts[0] as Record<string, unknown>).remain_quota, 1)
    await page.unmount()
  })
  test('rejects invalid budget values and unknown fiat metadata without creating a key', async () => {
    for (const example of [
      { displayCurrency: 'USD' as const, status: { credits_per_usd: 0 } },
      {
        displayCurrency: 'USD' as const,
        status: {
          currency_unit: undefined,
          credits_per_usd: undefined,
          cny_per_usd: undefined,
        },
      },
      { displayCurrency: 'CNY' as const, status: { cny_per_usd: 0 } },
    ]) {
      const page = await mount(example)
      await page.chooseGroup()
      await page.editBudget('1')
      await page.submit()
      assert.equal(page.posts.length, 0)
      assert.match(
        page.container.textContent ?? '',
        /Enter a positive, finite quota/
      )
      assert.equal(page.container.querySelector('#test-key-secret'), null)
      await page.unmount()
    }
    const page = await mount({ displayCurrency: 'CREDIT' })
    await page.chooseGroup()
    for (const invalid of ['', '0', '-1', '1.5', '9007199254740992']) {
      await page.editBudget(invalid)
      await page.submit()
      assert.equal(page.posts.length, 0)
      assert.equal(page.container.querySelector('#test-key-secret'), null)
    }
    await page.editBudget('1')
    await page.submit()
    assert.equal(page.posts.length, 1)
    assert.equal((page.posts[0] as Record<string, unknown>).remain_quota, 1)
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
