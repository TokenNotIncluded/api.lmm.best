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
  'HTMLButtonElement',
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
const originalAdapter = api.defaults.adapter
const previousAuth = useAuthStore.getState().auth
const cleanups: (() => void)[] = []
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const available = {
  success: true,
  data: {
    admin_users_protocol: 'core.admin-users.v1',
    capabilities: { admin_users_read: true },
  },
}

function login(role = 100, sid = 'synthetic-entry-session') {
  useAuthStore.getState().auth.setBundle({
    access_token: 'synthetic-entry-credential',
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

async function mount() {
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
              <div data-private-workspace>
                synthetic_{scope.role}@example.invalid
                <button type='button' onClick={recheck}>
                  Recheck capabilities
                </button>
              </div>
            )}
          </NativeAdminUsersEntry>
        </I18nextProvider>
      </QueryClientProvider>
    )
    await new Promise((resolve) => setTimeout(resolve, 20))
  })
  return container
}

afterEach(async () => {
  await act(async () => {
    for (const cleanup of cleanups.splice(0)) cleanup()
  })
  api.defaults.adapter = originalAdapter
  useAuthStore.setState({ auth: previousAuth })
})

test('ordinary users and missing sessions cannot mount or fetch admin data', async () => {
  let calls = 0
  api.defaults.adapter = async () => {
    calls++
    throw new Error('network forbidden')
  }
  login(1)
  const container = await mount()
  expect(container.textContent).toContain(
    'A current administrator session is required.'
  )
  expect(container.querySelector('[data-private-workspace]')).toBeNull()
  expect(calls).toBe(0)
  await act(async () => {
    login(100, '')
  })
  expect(container.querySelector('[data-private-workspace]')).toBeNull()
  expect(calls).toBe(0)
})

test('a disabled capability never mounts a workspace or tries a legacy endpoint', async () => {
  login()
  const seen: string[] = []
  api.defaults.adapter = async (config) => {
    seen.push(config.url ?? '')
    return {
      config,
      data: {
        ...available,
        data: { ...available.data, capabilities: { admin_users_read: false } },
      },
      headers: {},
      status: 200,
      statusText: 'OK',
    }
  }
  const container = await mount()
  expect(container.textContent).toContain(
    'User management is unavailable on this server.'
  )
  expect(container.querySelector('[data-private-workspace]')).toBeNull()
  expect(seen).toEqual(['/api/status'])
})

test('failed fresh probing removes previously displayed private results', async () => {
  login()
  const seen: string[] = []
  api.defaults.adapter = async (config) => {
    seen.push(config.url ?? '')
    if (seen.length > 1) {
      throw Object.assign(new Error('synthetic private upstream diagnostic'), {
        response: { status: 503 },
      })
    }
    return {
      config,
      data: available,
      headers: {},
      status: 200,
      statusText: 'OK',
    }
  }
  const container = await mount()
  expect(container.textContent).toContain('synthetic_100@example.invalid')
  await act(async () => {
    container
      .querySelector<HTMLButtonElement>('[data-private-workspace] button')
      ?.click()
    await new Promise((resolve) => setTimeout(resolve, 20))
  })
  expect(container.textContent).not.toContain('synthetic_100@example.invalid')
  expect(container.textContent).toContain(
    'User management is unavailable on this server.'
  )
  expect(container.textContent).not.toContain('private upstream diagnostic')
  expect(seen).toEqual(['/api/status', '/api/status'])
})

test('same-SID demotion immediately unmounts private results without further reads', async () => {
  login()
  const seen: string[] = []
  api.defaults.adapter = async (config) => {
    seen.push(config.url ?? '')
    return {
      config,
      data: available,
      headers: {},
      status: 200,
      statusText: 'OK',
    }
  }
  const container = await mount()
  expect(container.querySelector('[data-private-workspace]')).not.toBeNull()
  await act(async () => {
    login(1)
  })
  expect(container.querySelector('[data-private-workspace]')).toBeNull()
  expect(container.textContent).not.toContain('synthetic_100@example.invalid')
  expect(seen).toEqual(['/api/status'])
})

test('a new SID ignores a late successful capability response from the old SID', async () => {
  login()
  let release: (() => void) | undefined
  const seen: string[] = []
  api.defaults.adapter = async (config) => {
    seen.push(config.url ?? '')
    const sequence = seen.length
    if (sequence === 1) {
      await new Promise<void>((resolve) => {
        release = resolve
      })
    }
    return {
      config,
      data: sequence === 1 ? available : { success: false },
      headers: {},
      status: 200,
      statusText: 'OK',
    }
  }
  const container = await mount()
  await act(async () => {
    login(100, 'synthetic-new-session')
    await new Promise((resolve) => setTimeout(resolve, 20))
    release?.()
    await new Promise((resolve) => setTimeout(resolve, 20))
  })
  expect(container.querySelector('[data-private-workspace]')).toBeNull()
  expect(container.textContent).toContain(
    'User management is unavailable on this server.'
  )
  expect(seen).toEqual(['/api/status', '/api/status'])
})

test('an explicit retry uses a fresh successful status before showing results', async () => {
  login(10)
  let calls = 0
  api.defaults.adapter = async (config) => ({
    config,
    data: ++calls === 1 ? { success: false } : available,
    headers: {},
    status: 200,
    statusText: 'OK',
  })
  const container = await mount()
  expect(container.querySelector('[data-private-workspace]')).toBeNull()
  await act(async () => {
    Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find((button) => button.textContent === 'Try again')
      ?.click()
    await new Promise((resolve) => setTimeout(resolve, 20))
  })
  expect(container.textContent).toContain('synthetic_10@example.invalid')
  expect(calls).toBe(2)
})
