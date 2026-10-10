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

import i18n from '@/i18n/config'
import {
  applyAuthBundle,
  bindAuthCache,
  clearAuthentication,
  refreshAuthentication,
  setDevelopmentAuthRefreshAdapter,
} from '@/lib/auth-session'
import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

import { api } from './http-client'
import { createQueryRetry } from './query-retry'
import { isRateLimitedError } from './request-rate-limit'

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
  clearAuthentication(false, 'idle')
})

describe('canonical browser credit-unit acknowledgement', () => {
  test('declares the fixed unit on dispatched requests without changing raw payloads', async () => {
    const now = Math.floor(Date.now() / 1000)
    useAuthStore.getState().auth.setBundle(bundle('unit-test-token', now + 600))
    const requests: Array<{
      method: string | undefined
      unit: unknown
      data: unknown
      authorization: unknown
    }> = []
    api.defaults.adapter = async (config) => {
      requests.push({
        method: config.method,
        unit: config.headers.get('X-LMM-Credit-Unit'),
        data: config.data,
        authorization: config.headers.Authorization,
      })
      return response(config, 200, { success: true })
    }

    await api.get('/api/status')
    for (const method of ['post', 'put', 'patch', 'delete'] as const) {
      await api.request({
        method,
        url: '/api/wallet-transfer',
        data: { quota: 100000, request_key: 'raw-unit-test' },
        headers: {
          'X-LMM-Credit-Unit': method === 'delete' ? false : '100000',
        },
      })
    }

    assert.deepEqual(
      requests.map((request) => request.method),
      ['get', 'post', 'put', 'patch', 'delete']
    )
    for (const request of requests) {
      assert.equal(request.unit, '500000')
      assert.equal(request.authorization, 'Bearer unit-test-token')
    }
    assert.equal(requests[0].data, undefined)
    for (const request of requests.slice(1)) {
      assert.deepEqual(JSON.parse(String(request.data)), {
        quota: 100000,
        request_key: 'raw-unit-test',
      })
    }
  })

  test('preserves the unit acknowledgement through the existing same-session 401 retry', async () => {
    const now = Math.floor(Date.now() / 1000)
    useAuthStore.getState().auth.setBundle(bundle('original-token', now + 600))
    setDevelopmentAuthRefreshAdapter(async (config) =>
      response(config, 200, {
        success: true,
        data: bundle('rotated-token', now + 600),
      })
    )
    const units: unknown[] = []
    api.defaults.adapter = async (config) => {
      units.push(config.headers.get('X-LMM-Credit-Unit'))
      if (units.length === 1) {
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
    await api.post('/api/wallet-transfer', { quota: 100000 })
    assert.deepEqual(units, ['500000', '500000'])
    assert.equal(useAuthStore.getState().auth.accessToken, 'rotated-token')
  })

  test('a refresh-required 409 stays rejected without replaying or clearing authentication', async () => {
    const now = Math.floor(Date.now() / 1000)
    useAuthStore.getState().auth.setBundle(bundle('current-token', now + 600))
    let refreshCalls = 0
    setDevelopmentAuthRefreshAdapter(async () => {
      refreshCalls += 1
      throw new Error('unit mismatch must not refresh authentication')
    })
    let writes = 0
    const message = '点数单位已更新，请刷新页面后重试。'
    api.defaults.adapter = async (config) => {
      writes += 1
      throw new AxiosError(
        message,
        'ERR_BAD_REQUEST',
        config,
        undefined,
        response(config, 409, {
          success: false,
          code: 'CREDIT_UNIT_REFRESH_REQUIRED',
          message,
        })
      )
    }
    const originalToast = toast.error
    const messages: unknown[] = []
    toast.error = ((value: unknown) => {
      messages.push(value)
      return 'unit-refresh-test'
    }) as typeof toast.error
    try {
      await assert.rejects(
        api.post('/api/wallet-transfer', { quota: 100000 }),
        (error: unknown) => {
          assert.ok(error instanceof AxiosError)
          assert.equal(error.response?.status, 409)
          assert.equal(error.response.data.code, 'CREDIT_UNIT_REFRESH_REQUIRED')
          return true
        }
      )
      assert.equal(writes, 1)
      assert.equal(refreshCalls, 0)
      assert.equal(useAuthStore.getState().auth.user?.id, 42)
      assert.equal(useAuthStore.getState().auth.accessToken, 'current-token')
      assert.deepEqual(messages, [message])
    } finally {
      toast.error = originalToast
    }
  })
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

  test('concurrent outage reads use one notification identity and never retry a save', async () => {
    const originalToast = toast.error
    const notifications: Array<{ message: unknown; id?: unknown }> = []
    const requests: string[] = []
    toast.error = ((message: unknown, options?: { id?: unknown }) => {
      notifications.push({ message, id: options?.id })
      return 'outage-test'
    }) as typeof toast.error
    api.defaults.adapter = async (config) => {
      requests.push(`${config.method}:${config.url}`)
      const rejected = response(config, 503, {
        error: {
          code: 'service_temporarily_unavailable',
          message: 'private diagnostic',
        },
      })
      throw new AxiosError(
        'Request failed with status code 503',
        'ERR_BAD_RESPONSE',
        config,
        undefined,
        rejected
      )
    }
    try {
      const results = await Promise.allSettled([
        api.get('/api/outage-settings-test'),
        api.get('/api/outage-models-test'),
        api.post('/api/outage-save-test', { value: 'changed' }),
      ])
      assert.deepEqual(
        results.map((result) => result.status),
        ['rejected', 'rejected', 'rejected']
      )
      assert.equal(requests.length, 3)
      assert.equal(
        requests.filter((request) => request.startsWith('post:')).length,
        1
      )
      assert.deepEqual(
        notifications.map(({ id }) => id),
        Array(3).fill('service-temporarily-unavailable')
      )
      for (const { message } of notifications) {
        assert.equal(typeof message, 'string')
        assert.equal(
          message,
          i18n.t(
            'The service is temporarily unavailable. Please try again later.'
          )
        )
      }
      notifications.length = 0
      await assert.rejects(
        api.get('/api/outage-silent-test', { skipErrorHandler: true })
      )
      assert.equal(notifications.length, 0)
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

describe('refresh rate-limit backpressure', () => {
  test('multiple protected query waves share cooldown without dispatching expired tokens', async () => {
    const originalNow = Date.now
    let clock = originalNow()
    Date.now = () => clock
    const queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: createQueryRetry(true), retryDelay: 0 },
      },
    })
    try {
      applyAuthBundle(
        bundle('expired-token', Math.floor(clock / 1000) - 1),
        false
      )
      let refreshCalls = 0
      let protectedCalls = 0
      let queryCalls = 0
      setDevelopmentAuthRefreshAdapter(async (config) => {
        refreshCalls++
        const limitedResponse = response(config, 429, {})
        limitedResponse.headers = { 'Retry-After': '120' }
        throw new AxiosError(
          'Too many requests',
          'ERR_BAD_REQUEST',
          config,
          undefined,
          limitedResponse
        )
      })
      api.defaults.adapter = async (config) => {
        protectedCalls++
        return response(config, 200, {})
      }
      const wave = async (label: string) => {
        const results = await Promise.allSettled(
          [1, 2, 3].map((id) =>
            queryClient.fetchQuery({
              queryKey: [label, id],
              queryFn: () => {
                queryCalls++
                return api.get(`/api/user/test-${label}-${id}`, {
                  skipErrorHandler: true,
                })
              },
            })
          )
        )
        for (const result of results) {
          assert.equal(result.status, 'rejected')
          if (result.status === 'rejected') {
            assert.equal(isRateLimitedError(result.reason), true)
          }
        }
      }
      await wave('first')
      await wave('second')
      assert.equal(refreshCalls, 1)
      assert.equal(queryCalls, 6)
      assert.equal(protectedCalls, 0)
      assert.equal(useAuthStore.getState().auth.session?.sid, 'refresh-session')
      assert.equal(useAuthStore.getState().auth.user?.id, 42)
      clock += 120_001
      await wave('after-cooldown')
      assert.equal(refreshCalls, 2)
      const changed = bundle('new-expired-token', Math.floor(clock / 1000) - 1)
      changed.session.sid = 'different-session'
      applyAuthBundle(changed, false)
      await wave('new-session')
      assert.equal(refreshCalls, 3)
      assert.equal(protectedCalls, 0)
    } finally {
      Date.now = originalNow
      queryClient.clear()
    }
  })
})

test('refresh 429 without Retry-After waits 30 seconds without clearing identity', async () => {
  const originalNow = Date.now
  let clock = originalNow()
  Date.now = () => clock
  try {
    applyAuthBundle(
      bundle('expired-token', Math.floor(clock / 1000) - 1),
      false
    )
    let calls = 0
    setDevelopmentAuthRefreshAdapter(async (config) => {
      calls++
      throw new AxiosError(
        'Too many requests',
        'ERR_BAD_REQUEST',
        config,
        undefined,
        response(config, 429, {})
      )
    })
    assert.equal((await refreshAuthentication()).kind, 'transient_error')
    clock += 29_999
    assert.equal((await refreshAuthentication()).kind, 'transient_error')
    assert.equal(calls, 1)
    assert.equal(useAuthStore.getState().auth.user?.id, 42)
    clock += 2
    await refreshAuthentication()
    assert.equal(calls, 2)
  } finally {
    Date.now = originalNow
  }
})
