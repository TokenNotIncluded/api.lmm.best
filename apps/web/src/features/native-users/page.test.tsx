/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
// @ts-expect-error Bun's test module is only available in the test runtime.
import { afterEach, expect, test } from 'bun:test'

import { Window } from 'happy-dom'

const dom = new Window({ url: 'https://console.example.invalid/users' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'HTMLButtonElement',
  'HTMLSelectElement',
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
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  value: true,
})
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { NativeAdminUsersEntry } = await import('./entry')
const { NativeUsersPage } = await import('./page')
const { nativeListFixture, nativeUserFixture, nativeStatusFixture } =
  await import('./test-fixtures')
const originalAdapter = api.defaults.adapter
const previousAuth = useAuthStore.getState().auth
const cleanups: (() => void)[] = []
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const pause = () => new Promise((resolve) => setTimeout(resolve, 20))
function login(role = 100, sid = 'synthetic-native-page-session') {
  useAuthStore.getState().auth.setBundle({
    access_token: 'synthetic-native-page-credential',
    token_type: 'Bearer',
    access_expires_at: 2000000000,
    user: { id: 901, role, username: 'synthetic-admin' },
    session: {
      sid,
      current: true,
      login_method: 'password',
      ip: '127.0.0.1',
      user_agent: 'synthetic',
      created_at: 1,
      last_active_at: 1,
      expires_at: 2000000000,
    },
  })
}
async function mount(search = '') {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  cleanups.push(() => {
    root.unmount()
    client.clear()
    container.remove()
  })
  await act(async () => {
    root.render(
      <QueryClientProvider client={client}>
        <I18nextProvider i18n={i18n}>
          <NativeAdminUsersEntry>
            {(scope, recheck) => (
              <NativeUsersPage
                scope={scope}
                recheck={recheck}
                search={search}
              />
            )}
          </NativeAdminUsersEntry>
        </I18nextProvider>
      </QueryClientProvider>
    )
    await pause()
  })
  await act(async () => {
    await pause()
  })
  return container
}
function button(container: HTMLElement, label: string) {
  const element = Array.from(
    container.querySelectorAll<HTMLButtonElement>('button')
  ).find((value) => value.textContent === label)
  if (!element) throw new Error(`Missing test button: ${label}`)
  return element
}
afterEach(async () => {
  await act(async () => {
    for (const cleanup of cleanups.splice(0)) cleanup()
  })
  api.defaults.adapter = originalAdapter
  useAuthStore.setState({ auth: previousAuth })
})

test('the actual page renders exact identity, memberships and signed units without dollar or legacy defaults', async () => {
  login(10)
  const seen: string[] = []
  api.defaults.adapter = async (config) => {
    seen.push(`${config.method} ${config.url}`)
    return {
      config,
      data:
        config.url === '/api/status'
          ? nativeStatusFixture
          : nativeListFixture(),
      headers: {},
      status: 200,
      statusText: 'OK',
    }
  }
  const container = await mount()
  const row = container.querySelector(
    '[data-native-user-id="9007199254740993"]'
  )
  expect(row).not.toBeNull()
  for (const value of [
    '9007199254740993',
    '9007199254740995',
    '9007199254740997',
    'synthetic-native@example.invalid',
    'alpha',
    'team-beta',
    '-7',
    'Debt magnitude',
    '0 ms since Unix epoch',
    '-1 ms since Unix epoch',
    'Empty value',
    'Service level',
  ]) {
    expect(row?.textContent).toContain(value)
  }
  expect(row?.textContent).not.toMatch(/\$|USD|L0|1970/)
  expect(container.textContent).toContain(
    'Risk, usage, top-up, referral history and legacy trust information are unavailable'
  )
  expect(container.querySelectorAll('details button[disabled]').length).toBe(21)
  await act(async () => {
    for (const action of container.querySelectorAll<HTMLButtonElement>(
      'details button'
    )) {
      action.click()
    }
  })
  expect(seen).toEqual(['get /api/status', 'get /core/v1/admin/users'])
})

test('false or absent capabilities and an ordinary role produce zero native or legacy user requests', async () => {
  const seen: string[] = []
  api.defaults.adapter = async (config) => {
    seen.push(config.url ?? '')
    return {
      config,
      data: {
        success: true,
        data: {
          admin_users_protocol: 'core.admin-users.v1',
          capabilities: { admin_users_read: false },
        },
      },
      headers: {},
      status: 200,
      statusText: 'OK',
    }
  }
  login()
  const container = await mount()
  expect(container.textContent).toContain('User management is unavailable')
  await act(async () => {
    login(1)
  })
  expect(container.querySelector('[data-native-user-id]')).toBeNull()
  expect(seen).toEqual(['/api/status'])
})

test('unsupported raw saved filters are visible and never fetch an unfiltered native list', async () => {
  login()
  const seen: string[] = []
  api.defaults.adapter = async (config) => {
    seen.push(config.url ?? '')
    return {
      config,
      data: nativeStatusFixture,
      headers: {},
      status: 200,
      statusText: 'OK',
    }
  }
  for (const search of [
    '?l0Only=true',
    '?sortBy=quota',
    '?sortBy=unknown',
    '?risk=high',
  ]) {
    const container = await mount(search)
    expect(container.textContent).toContain(
      'unsupported user filters or sorting'
    )
    expect(container.querySelector('a[href="/users"]')?.textContent).toBe(
      'Clear saved user filters'
    )
    expect(container.querySelector('[data-native-user-id]')).toBeNull()
  }
  expect(seen).toEqual(Array(4).fill('/api/status'))
})

test('known empty profiles and unavailable wallets remain distinct from invented values', async () => {
  login()
  const user = nativeUserFixture({
    profile_available: false,
    username: null,
    display_name: null,
    email: null,
    active: false,
    status: 2,
    account_active: false,
    groups: [],
    wallet: { status: 'unavailable', code: 'WALLET_SUBJECT_UNAVAILABLE' },
    registered_at_unix_ms: null,
  })
  api.defaults.adapter = async (config) => ({
    config,
    data:
      config.url === '/api/status'
        ? nativeStatusFixture
        : nativeListFixture([user]),
    headers: {},
    status: 200,
    statusText: 'OK',
  })
  const container = await mount()
  const row = container.querySelector('[data-native-user-id]')
  expect(row?.textContent).toContain('Profile unavailable')
  expect(row?.textContent).toContain('Disabled user')
  expect(row?.textContent).toContain('No explicit group memberships')
  expect(row?.textContent).not.toContain('Empty value')
  expect(row?.textContent).not.toContain('Economic position')
  expect(row?.textContent).not.toContain('synthetic-native@example.invalid')
})

test('pagination removes previous data immediately, ignores cancelled success and shows failures safely', async () => {
  login()
  let release: (() => void) | undefined
  let secondPageCalls = 0
  const seen: string[] = []
  api.defaults.adapter = async (config) => {
    seen.push(`${config.method} ${config.url}`)
    if (config.url === '/api/status') {
      return {
        config,
        data: nativeStatusFixture,
        headers: {},
        status: 200,
        statusText: 'OK',
      }
    }
    if (config.params.page === 2) {
      secondPageCalls++
      if (secondPageCalls === 1) {
        await new Promise<void>((resolve) => {
          release = resolve
        })
      } else {
        throw Object.assign(new Error('synthetic-private-upstream-query'), {
          response: { status: 503 },
        })
      }
    }
    return {
      config,
      data: nativeListFixture(undefined, config.params.page, 21),
      headers: {},
      status: 200,
      statusText: 'OK',
    }
  }
  const container = await mount()
  expect(container.textContent).toContain('synthetic-native@example.invalid')
  await act(async () => {
    button(container, 'Next user page').click()
    await pause()
  })
  expect(container.querySelector('[data-native-user-id]')).toBeNull()
  await act(async () => {
    button(container, 'Recheck server availability').click()
    await pause()
  })
  await act(async () => {
    release?.()
    await pause()
  })
  expect(container.textContent).toContain('Page 1')
  expect(container.textContent).not.toContain('Page 2')
  await act(async () => {
    button(container, 'Next user page').click()
    await pause()
  })
  expect(container.querySelector('[data-native-user-id]')).toBeNull()
  expect(container.textContent).toContain('No previous results are displayed')
  expect(container.textContent).not.toContain(
    'synthetic-private-upstream-query'
  )
  expect(
    seen.every(
      (value) => value.startsWith('get ') && !value.includes('/api/user/')
    )
  ).toBe(true)
})

test('switching SID while data is pending cannot restore old private results', async () => {
  login()
  let release: (() => void) | undefined
  let reads = 0
  api.defaults.adapter = async (config) => {
    if (config.url === '/api/status') {
      return {
        config,
        data: nativeStatusFixture,
        headers: {},
        status: 200,
        statusText: 'OK',
      }
    }
    if (++reads === 1) {
      await new Promise<void>((resolve) => {
        release = resolve
      })
    } else {
      throw Object.assign(new Error('unavailable'), {
        response: { status: 503 },
      })
    }
    return {
      config,
      data: nativeListFixture(),
      headers: {},
      status: 200,
      statusText: 'OK',
    }
  }
  const container = await mount()
  await act(async () => {
    login(100, 'synthetic-native-page-replacement')
    await pause()
  })
  await act(async () => {
    release?.()
    await pause()
  })
  expect(container.textContent).not.toContain(
    'synthetic-native@example.invalid'
  )
  expect(container.querySelector('[data-native-user-id]')).toBeNull()
  expect(container.textContent).toContain('No previous results are displayed')
})

test('groups page independently, selecting a choice keeps exact membership and every operation remains GET', async () => {
  login()
  const seen: {
    method: string | undefined
    url: string | undefined
    params: Record<string, unknown>
  }[] = []
  api.defaults.adapter = async (config) => {
    seen.push({ method: config.method, url: config.url, params: config.params })
    const data =
      config.url === '/api/status'
        ? nativeStatusFixture
        : config.url?.endsWith('/groups')
          ? {
              version: 1,
              total: 21,
              page: config.params.page,
              page_size: 20,
              items: config.params.page === 1 ? ['alpha'] : [' exact '],
              source: 'enabled_explicit_user_routing_grants',
            }
          : nativeListFixture()
    return { config, data, headers: {}, status: 200, statusText: 'OK' }
  }
  const container = await mount()
  await act(async () => {
    button(container, 'Browse group directory').click()
    await pause()
  })
  expect(container.textContent).toContain('21 groups · Page 1')
  await act(async () => {
    button(container, 'Next group page').click()
    await pause()
  })
  expect(container.textContent).toContain('21 groups · Page 2')
  await act(async () => {
    button(container, ' exact ').click()
  })
  expect(
    container.querySelector<HTMLInputElement>('#native-user-group')?.value
  ).toBe(' exact ')
  await act(async () => {
    button(container, 'Apply filters').click()
    await pause()
  })
  expect(seen.at(-1)?.params.group).toBe(' exact ')
  expect(
    seen
      .filter((value) => value.url?.endsWith('/groups'))
      .map((value) => value.params.page)
  ).toEqual([1, 2])
  expect(seen.every((value) => value.method === 'get')).toBe(true)
})

test('malformed successful data never renders a partial or legacy-shaped row', async () => {
  login()
  api.defaults.adapter = async (config) => ({
    config,
    data:
      config.url === '/api/status'
        ? nativeStatusFixture
        : {
            ...nativeListFixture(),
            capabilities: {
              ...nativeListFixture().capabilities,
              implemented_write_actions: ['normal_ban'],
            },
          },
    headers: {},
    status: 200,
    statusText: 'OK',
  })
  const container = await mount()
  expect(container.textContent).toContain('incompatible user data')
  expect(container.textContent).not.toContain(
    'synthetic-native@example.invalid'
  )
  expect(container.querySelector('[data-native-user-id]')).toBeNull()
})

test('saved supported page sizes are represented by a matching select option and exact request', async () => {
  login()
  api.defaults.adapter = async (config) => ({
    config,
    data:
      config.url === '/api/status'
        ? nativeStatusFixture
        : { ...nativeListFixture(), page_size: 37 },
    headers: {},
    status: 200,
    statusText: 'OK',
  })
  const container = await mount('?pageSize=37')
  const select = container.querySelector<HTMLSelectElement>(
    '#native-user-page-size'
  )
  expect(select?.value).toBe('37')
  expect(
    Array.from(select?.options ?? []).some((option) => option.value === '37')
  ).toBe(true)
  expect(container.querySelector('[data-native-user-id]')).not.toBeNull()
})

test('a denied group read clears all private projections and requires explicit retry before any new read', async () => {
  login()
  const seen: string[] = []
  api.defaults.adapter = async (config) => {
    seen.push(config.url ?? '')
    if (config.url?.endsWith('/groups')) {
      throw Object.assign(new Error('synthetic local denial'), {
        response: { status: 401 },
      })
    }
    return {
      config,
      data:
        config.url === '/api/status'
          ? nativeStatusFixture
          : nativeListFixture(),
      headers: {},
      status: 200,
      statusText: 'OK',
    }
  }
  const container = await mount()
  expect(container.textContent).toContain('synthetic-native@example.invalid')
  await act(async () => {
    button(container, 'Browse group directory').click()
    await pause()
  })
  expect(container.textContent).toContain(
    'Administrator access or multi-factor verification is required.'
  )
  expect(container.textContent).not.toContain(
    'synthetic-native@example.invalid'
  )
  expect(container.querySelector('[data-native-user-id]')).toBeNull()
  const deniedCalls = [...seen]
  await act(async () => {
    await pause()
  })
  expect(seen).toEqual(deniedCalls)
  await act(async () => {
    button(container, 'Try again').click()
    await pause()
  })
  await act(async () => {
    await pause()
  })
  expect(seen.slice(deniedCalls.length)).toEqual([
    '/api/status',
    '/core/v1/admin/users',
  ])
  expect(container.textContent).toContain('synthetic-native@example.invalid')
  expect(container.textContent).not.toContain('Group directory is unavailable.')
})

test('a denied list replacement unmounts groups and aborts an outstanding group page response', async () => {
  login()
  let userReads = 0
  let release: (() => void) | undefined
  let pendingSignal: AbortSignal | undefined
  api.defaults.adapter = async (config) => {
    if (config.url === '/api/status') {
      return {
        config,
        data: nativeStatusFixture,
        headers: {},
        status: 200,
        statusText: 'OK',
      }
    }
    if (config.url?.endsWith('/groups')) {
      if (config.params.page === 2) {
        pendingSignal = config.signal as AbortSignal
        await new Promise<void>((resolve) => {
          release = resolve
        })
      }
      return {
        config,
        data: {
          version: 1,
          items: ['synthetic-private-group'],
          total: 21,
          page: config.params.page,
          page_size: 20,
          source: 'enabled_explicit_user_routing_grants',
        },
        headers: {},
        status: 200,
        statusText: 'OK',
      }
    }
    if (++userReads > 1) {
      throw Object.assign(new Error('synthetic local denial'), {
        response: { status: 403 },
      })
    }
    return {
      config,
      data: nativeListFixture(),
      headers: {},
      status: 200,
      statusText: 'OK',
    }
  }
  const container = await mount()
  await act(async () => {
    button(container, 'Browse group directory').click()
    await pause()
  })
  expect(container.textContent).toContain('synthetic-private-group')
  await act(async () => {
    button(container, 'Next group page').click()
    await pause()
  })
  expect(pendingSignal?.aborted).toBe(false)
  await act(async () => {
    button(container, 'Apply filters').click()
    await pause()
  })
  expect(container.textContent).toContain(
    'Administrator access or multi-factor verification is required.'
  )
  expect(container.textContent).not.toContain('synthetic-private-group')
  expect(container.textContent).not.toContain(
    'synthetic-native@example.invalid'
  )
  expect(pendingSignal?.aborted).toBe(true)
  await act(async () => {
    release?.()
    await pause()
  })
  expect(container.textContent).not.toContain('synthetic-private-group')
  expect(container.querySelector('[data-native-user-id]')).toBeNull()
  expect(userReads).toBe(2)
})
