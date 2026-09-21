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

import type { AxiosAdapter, AxiosResponse } from 'axios'
import { Window } from 'happy-dom'
import type { Root } from 'react-dom/client'

import type { AuthBundle } from '@/stores/auth-store'

const domWindow = new Window({ url: 'https://console.example.test/channels' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Node',
  'Element',
  'Event',
  'MutationObserver',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const i18next = (await import('i18next')).default
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { useSecureVerification } = await import('./use-secure-verification')

await i18next.init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        'Request failed': 'Request failed',
        'Session expired!': 'Session expired!',
      },
    },
  },
})

const originalAPIAdapter = api.defaults.adapter
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

type HookValue = ReturnType<typeof useSecureVerification>
let currentHook: HookValue | null = null

function response(
  config: Parameters<AxiosAdapter>[0],
  data: unknown
): AxiosResponse {
  return {
    config,
    data,
    headers: {},
    status: 200,
    statusText: 'OK',
  }
}

function authBundle(): AuthBundle {
  const expiresAt = Math.floor(Date.now() / 1000) + 600
  return {
    access_token: 'secure-verification-test-token',
    token_type: 'Bearer',
    access_expires_at: expiresAt,
    user: {
      id: 42,
      username: 'secure-verification-test',
      role: 1,
    },
    session: {
      sid: 'secure-verification-test-session',
      current: true,
      login_method: 'password',
      ip: '127.0.0.1',
      user_agent: 'test',
      created_at: 1,
      last_active_at: 1,
      expires_at: expiresAt + 600,
    },
  }
}

function Harness(props: { onError?: (error: unknown) => void }) {
  currentHook = useSecureVerification({ onError: props.onError })
  return null
}

function requireHook(): HookValue {
  if (!currentHook) throw new Error('secure verification hook is not mounted')
  return currentHook
}

async function mountHarness(
  onError?: (error: unknown) => void
): Promise<{ root: Root; container: HTMLDivElement }> {
  const container = document.createElement('div')
  const root = createRoot(container)
  await act(async () => {
    root.render(<Harness onError={onError} />)
  })
  return { root, container }
}

afterEach(() => {
  api.defaults.adapter = originalAPIAdapter
  useAuthStore.getState().auth.reset('idle')
  currentHook = null
})

after(() => domWindow.close())

describe('useSecureVerification', () => {
  test('does not probe verification methods merely because a page mounted', async () => {
    useAuthStore.getState().auth.setBundle(authBundle())
    let requests = 0
    api.defaults.adapter = async (config) => {
      requests += 1
      return response(config, { success: true, data: {} })
    }
    const { root } = await mountHarness()

    try {
      await act(async () => undefined)
      assert.equal(requests, 0)
    } finally {
      await act(async () => root.unmount())
    }
  })

  test('probes methods only when verification is explicitly started', async () => {
    useAuthStore.getState().auth.setBundle(authBundle())
    const requestedPaths: string[] = []
    api.defaults.adapter = async (config) => {
      requestedPaths.push(String(config.url))
      if (config.url === '/api/user/self') {
        return response(config, {
          success: true,
          data: { email: 'owner@example.com' },
        })
      }
      return response(config, {
        success: true,
        data: { enabled: false },
      })
    }
    const { root } = await mountHarness()

    try {
      let started = false
      await act(async () => {
        started = await requireHook().startVerification(async () => undefined, {
          scope: 'channel.key.read',
        })
      })

      assert.equal(started, true)
      assert.deepEqual(requestedPaths.sort(), [
        '/api/user/2fa/status',
        '/api/user/passkey',
        '/api/user/self',
      ])
      assert.equal(currentHook?.methods.availability, 'complete')
      assert.equal(currentHook?.methods.hasEmail, true)
    } finally {
      await act(async () => root.unmount())
    }
  })

  test('reuses a caller-provided method snapshot without probing again', async () => {
    useAuthStore.getState().auth.setBundle(authBundle())
    let requests = 0
    api.defaults.adapter = async (config) => {
      requests += 1
      return response(config, { success: true, data: {} })
    }
    const { root } = await mountHarness()

    try {
      let started = false
      await act(async () => {
        started = await requireHook().startVerification(async () => undefined, {
          scope: 'channel.key.read',
          verificationMethods: {
            hasEmail: true,
            has2FA: false,
            hasPasskey: false,
            passkeySupported: false,
            availability: 'complete',
          },
        })
      })

      assert.equal(started, true)
      assert.equal(requests, 0)
      assert.equal(currentHook?.methods.hasEmail, true)
    } finally {
      await act(async () => root.unmount())
    }
  })

  test('does not probe methods for a completed anonymous session', async () => {
    useAuthStore.getState().auth.reset('complete')
    let requests = 0
    let reportedError: unknown
    api.defaults.adapter = async (config) => {
      requests += 1
      return response(config, { success: true, data: {} })
    }
    const { root } = await mountHarness((error) => {
      reportedError = error
    })

    try {
      let started = true
      await act(async () => {
        started = await requireHook().startVerification(async () => undefined, {
          scope: 'channel.key.read',
        })
      })

      assert.equal(started, false)
      assert.equal(requests, 0)
      assert.match(String(reportedError), /Session expired/)
    } finally {
      await act(async () => root.unmount())
    }
  })
})
