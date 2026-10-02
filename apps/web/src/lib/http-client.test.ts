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

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { afterEach, describe, test } from 'node:test'

import { QueryClient } from '@tanstack/react-query'
import {
  AxiosError,
  CanceledError,
  type AxiosAdapter,
  type AxiosResponse,
  isCancel,
} from 'axios'
import { toast } from 'sonner'

import {
  applyAuthBundle,
  bindAuthCache,
  setDevelopmentAuthRefreshAdapter,
} from '@/lib/auth-session'
import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

import { api } from './http-client'

const originalAPIAdapter = api.defaults.adapter

function response(
  config: Parameters<AxiosAdapter>[0],
  status: number,
  data: unknown
): AxiosResponse {
  return {
    config,
    data,
    headers: {},
    status,
    statusText: status === 200 ? 'OK' : 'Unauthorized',
  }
}

function bundle(token: string, expiresAt: number): AuthBundle {
  return {
    access_token: token,
    token_type: 'Bearer',
    access_expires_at: expiresAt,
    user: {
      id: 42,
      username: 'refresh-test',
      role: 1,
      developer_access_granted: true,
    },
    session: {
      sid: 'refresh-session',
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

afterEach(() => {
  api.defaults.adapter = originalAPIAdapter
  useAuthStore.getState().auth.reset('idle')
})

describe('authenticated HTTP requests', () => {
  test('refreshes an expiring token before protected requests fan out', async () => {
    const now = Math.floor(Date.now() / 1000)
    const refreshed = bundle('fresh-token', now + 600)
    useAuthStore.getState().auth.setBundle(bundle('expiring-token', now + 10))
    let refreshCalls = 0
    let refreshTimeout = 0
    setDevelopmentAuthRefreshAdapter(async (config) => {
      refreshCalls += 1
      refreshTimeout = Number(config.timeout)
      return response(config, 200, { success: true, data: refreshed })
    })
    const authorizations: string[] = []
    api.defaults.adapter = async (config) => {
      authorizations.push(String(config.headers.Authorization ?? ''))
      return response(config, 200, { success: true, data: [] })
    }

    await Promise.all([
      api.get('/api/user/models'),
      api.get('/api/user/groups'),
      api.get('/api/user/2fa/status'),
    ])

    assert.equal(refreshCalls, 1)
    assert.equal(refreshTimeout, 10_000)
    assert.deepEqual(authorizations, [
      'Bearer fresh-token',
      'Bearer fresh-token',
      'Bearer fresh-token',
    ])
  })

  test('does not send a newly expired token after a transient refresh failure', async () => {
    const originalDateNow = Date.now
    let nowMs = 1_000_000
    Date.now = () => nowMs
    useAuthStore
      .getState()
      .auth.setBundle(bundle('nearly-expired-token', nowMs / 1000 + 1))
    setDevelopmentAuthRefreshAdapter(async (config) => {
      nowMs += 2_000
      return response(config, 503, { success: false })
    })
    let protectedCalls = 0
    api.defaults.adapter = async (config) => {
      protectedCalls += 1
      return response(config, 200, { success: true, data: [] })
    }

    try {
      await assert.rejects(
        api.get('/api/user/2fa/status', { skipErrorHandler: true }),
        (error: unknown) => {
          const requestError = error as {
            config?: { skipErrorHandler?: boolean }
          }
          assert.equal(requestError.config?.skipErrorHandler, true)
          return true
        }
      )
      assert.equal(protectedCalls, 0)
    } finally {
      Date.now = originalDateNow
    }
  })

  test('does not send protected requests after refresh rejects the session', async () => {
    const now = Math.floor(Date.now() / 1000)
    useAuthStore.getState().auth.setBundle(bundle('expired-token', now - 1))
    const queryClient = new QueryClient()
    queryClient.setQueryData(['assistant-status'], {
      developer_access_granted: true,
    })
    const unbind = bindAuthCache(queryClient)
    setDevelopmentAuthRefreshAdapter(async (config) =>
      response(config, 401, { success: false })
    )
    let protectedCalls = 0
    api.defaults.adapter = async (config) => {
      protectedCalls += 1
      return response(config, 200, { success: true, data: [] })
    }

    try {
      await assert.rejects(api.get('/api/user/models'), Error)
      assert.equal(protectedCalls, 0)
      assert.equal(queryClient.getQueryCache().getAll().length, 0)
      assert.equal(useAuthStore.getState().auth.user, null)
    } finally {
      unbind()
    }
  })
})

describe('route navigation request cancellation', () => {
  test('independent abort signals cannot share a cancelled request', async () => {
    const first = new AbortController()
    const second = new AbortController()
    const releases: Array<() => void> = []
    api.defaults.adapter = (config) =>
      new Promise((resolve) => {
        releases.push(() =>
          resolve(response(config, 200, { success: true, data: [] }))
        )
      })
    const results = Promise.allSettled([
      api.get('/api/navigation-cancellation-test', {
        signal: first.signal,
        skipErrorHandler: true,
      }),
      api.get('/api/navigation-cancellation-test', {
        signal: second.signal,
        skipErrorHandler: true,
      }),
    ])
    await new Promise((resolve) => setTimeout(resolve, 0))
    first.abort()
    releases.forEach((release) => release())
    const settled = await results
    assert.equal(settled[0].status, 'rejected')
    assert.equal(settled[1].status, 'fulfilled')
    assert.equal(releases.length, 2)
  })

  test('signal-free concurrent reads still share a single request', async () => {
    let calls = 0
    api.defaults.adapter = async (config) => {
      calls += 1
      return response(config, 200, { success: true, data: [] })
    }
    await Promise.all([
      api.get('/api/navigation-deduplication-test'),
      api.get('/api/navigation-deduplication-test'),
    ])
    assert.equal(calls, 1)
  })

  test('cancellation stays rejected without a failure toast; network failures remain visible', async () => {
    const originalToast = toast.error
    const messages: unknown[] = []
    toast.error = ((message: unknown) => {
      messages.push(message)
      return 'toast-test'
    }) as typeof toast.error
    try {
      api.defaults.adapter = async (config) => {
        throw new CanceledError('canceled', config)
      }
      await assert.rejects(
        api.get('/api/navigation-cancel-toast-test'),
        /canceled/
      )
      assert.equal(messages.length, 0)
      api.defaults.adapter = async (config) => {
        throw new AxiosError('Network failure', 'ERR_NETWORK', config)
      }
      await assert.rejects(
        api.get('/api/navigation-real-failure-test'),
        /Network failure/
      )
      assert.equal(messages.length, 1)
    } finally {
      toast.error = originalToast
    }
  })
})

function deferred() {
  let release!: () => void
  const promise = new Promise<void>((resolve) => {
    release = resolve
  })
  return { promise, release }
}

function switchedBundle(token: string, userId: number, sessionId: string) {
  const auth = bundle(token, Math.floor(Date.now() / 1000) + 600)
  auth.user.id = userId
  auth.session.sid = sessionId
  return auth
}

function rejectsChangedScope(request: Promise<unknown>) {
  return assert.rejects(request, (error: unknown) => {
    assert.ok(isCancel(error), 'scope changes must cancel without auth retry')
    assert.equal(error.code, 'ERR_CANCELED')
    return true
  })
}

describe('requests bound to the initiating authentication', () => {
  const authScope = { userId: 42, sessionId: 'refresh-session' }
  const purchaseURL = '/api/hero-sms/sms/orders'

  test('blocks each identity component before dispatch, including skipped refresh', async () => {
    let calls = 0
    api.defaults.adapter = async (config) => {
      calls += 1
      return response(config, 200, { success: true })
    }
    for (const [userId, sessionId] of [
      [43, 'refresh-session'],
      [42, 'other-session'],
    ] as const) {
      applyAuthBundle(switchedBundle('other-token', userId, sessionId), false)
      await rejectsChangedScope(
        api.post(
          purchaseURL,
          { request_key: 'old-purchase' },
          { authScope, skipAuthRefresh: true }
        )
      )
      assert.equal(calls, 0)
    }
  })

  test('blocks a purchase if login changes while its initial refresh is pending', async () => {
    const now = Math.floor(Date.now() / 1000)
    applyAuthBundle(bundle('expiring-token', now + 10), false)
    const refreshEntered = deferred(),
      releaseRefresh = deferred()
    let refreshCalls = 0
    setDevelopmentAuthRefreshAdapter(async (config) => {
      refreshCalls += 1
      refreshEntered.release()
      await releaseRefresh.promise
      return response(config, 200, {
        success: true,
        data: bundle('old-refreshed-token', now + 600),
      })
    })
    const authorizations: string[] = []
    api.defaults.adapter = async (config) => {
      authorizations.push(String(config.headers.Authorization ?? ''))
      return response(config, 200, { success: true })
    }
    const rejected = rejectsChangedScope(
      api.post(purchaseURL, { request_key: 'old-purchase' }, { authScope })
    )
    try {
      await refreshEntered.promise
      applyAuthBundle(
        switchedBundle('new-account-token', 43, 'new-session'),
        false
      )
    } finally {
      releaseRefresh.release()
    }
    await rejected
    assert.equal(refreshCalls, 1)
    assert.deepEqual(
      authorizations,
      [],
      'no purchase may use the new account token'
    )
    assert.equal(useAuthStore.getState().auth.accessToken, 'new-account-token')
  })

  test('blocks a 401 retry when refresh authenticates another session', async () => {
    applyAuthBundle(
      bundle('original-token', Math.floor(Date.now() / 1000) + 600),
      false
    )
    const refreshEntered = deferred(),
      releaseRefresh = deferred()
    setDevelopmentAuthRefreshAdapter(async (config) => {
      refreshEntered.release()
      await releaseRefresh.promise
      return response(config, 200, {
        success: true,
        data: switchedBundle('new-account-token', 43, 'new-session'),
      })
    })
    const authorizations: string[] = []
    api.defaults.adapter = async (config) => {
      authorizations.push(String(config.headers.Authorization ?? ''))
      if (authorizations.length === 1) {
        throw new AxiosError(
          'Unauthorized',
          'ERR_BAD_REQUEST',
          config,
          undefined,
          response(config, 401, {})
        )
      }
      return response(config, 200, { success: true })
    }
    const rejected = rejectsChangedScope(
      api.post(purchaseURL, { request_key: 'old-purchase' }, { authScope })
    )
    try {
      await refreshEntered.promise
    } finally {
      releaseRefresh.release()
    }
    await rejected
    assert.deepEqual(
      authorizations,
      ['Bearer original-token'],
      'the original POST must not be replayed with a new token'
    )
    assert.equal(useAuthStore.getState().auth.accessToken, 'new-account-token')
  })

  test('late 401 responses cannot refresh or clear a newer login', async () => {
    for (const authRetry of [false, true]) {
      applyAuthBundle(
        bundle('original-token', Math.floor(Date.now() / 1000) + 600),
        false
      )
      const requestEntered = deferred(),
        releaseRequest = deferred()
      let refreshCalls = 0
      setDevelopmentAuthRefreshAdapter(async (config) => {
        refreshCalls += 1
        return response(config, 200, {
          success: true,
          data: switchedBundle('new-account-token', 43, 'new-session'),
        })
      })
      let calls = 0
      api.defaults.adapter = async (config) => {
        calls += 1
        requestEntered.release()
        await releaseRequest.promise
        throw new AxiosError(
          'Unauthorized',
          'ERR_BAD_REQUEST',
          config,
          undefined,
          response(config, 401, {})
        )
      }
      const rejected = rejectsChangedScope(
        api.post(
          purchaseURL,
          { request_key: 'old-purchase' },
          { authScope, authRetry }
        )
      )
      try {
        await requestEntered.promise
        applyAuthBundle(
          switchedBundle('new-account-token', 43, 'new-session'),
          false
        )
      } finally {
        releaseRequest.release()
      }
      await rejected
      assert.equal(calls, 1)
      assert.equal(refreshCalls, 0)
      assert.equal(
        useAuthStore.getState().auth.accessToken,
        'new-account-token'
      )
    }
  })

  test('scoped GETs cannot borrow an unscoped in-flight request', async () => {
    applyAuthBundle(
      switchedBundle('new-account-token', 43, 'new-session'),
      false
    )
    const requestEntered = deferred(),
      releaseRequest = deferred()
    let calls = 0
    api.defaults.adapter = async (config) => {
      calls += 1
      requestEntered.release()
      await releaseRequest.promise
      return response(config, 200, { success: true })
    }
    const ordinary = api.get('/api/auth-scope-deduplication-test')
    await requestEntered.promise
    const rejected = rejectsChangedScope(
      api.get('/api/auth-scope-deduplication-test', { authScope })
    )
    releaseRequest.release()
    await Promise.all([ordinary, rejected])
    assert.equal(calls, 1)
  })

  test('same-session rotation and ordinary unscoped 401 retry still succeed', async () => {
    for (const scoped of [false, true]) {
      applyAuthBundle(
        bundle('original-token', Math.floor(Date.now() / 1000) + 600),
        false
      )
      setDevelopmentAuthRefreshAdapter(async (config) =>
        response(config, 200, {
          success: true,
          data: bundle('rotated-token', Math.floor(Date.now() / 1000) + 600),
        })
      )
      const authorizations: string[] = []
      api.defaults.adapter = async (config) => {
        authorizations.push(String(config.headers.Authorization ?? ''))
        if (authorizations.length === 1) {
          throw new AxiosError(
            'Unauthorized',
            'ERR_BAD_REQUEST',
            config,
            undefined,
            response(config, 401, {})
          )
        }
        return response(config, 200, { success: true })
      }
      await api.post(
        purchaseURL,
        { request_key: 'same-session-purchase' },
        scoped ? { authScope } : {}
      )
      assert.deepEqual(authorizations, [
        'Bearer original-token',
        'Bearer rotated-token',
      ])
    }
  })
})
