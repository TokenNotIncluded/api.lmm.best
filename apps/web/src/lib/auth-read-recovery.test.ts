/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import axios, { AxiosError, type AxiosAdapter } from 'axios'

import { api } from '@/lib/api'
import { getAuthCookieBlockedState } from '@/lib/auth-cookie'
import {
  applyAuthBundle,
  clearAuthentication,
  setDevelopmentAuthRefreshAdapter,
} from '@/lib/auth-session'
import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

const descriptors = new Map(
  ['window', 'navigator', 'isSecureContext'].map((key) => [
    key,
    Object.getOwnPropertyDescriptor(globalThis, key),
  ])
)
const storage = new Map<string, string>()
let tail: Promise<void> = Promise.resolve(),
  held = 0
const locks = {
  async request<T>(
    _name: string,
    options: { signal?: AbortSignal },
    callback: () => Promise<T>
  ) {
    const previous = tail
    let release!: () => void
    tail = new Promise<void>((done) => {
      release = done
    })
    await previous
    try {
      if (options.signal?.aborted) {
        throw new DOMException('Aborted', 'AbortError')
      }
      held++
      try {
        return await callback()
      } finally {
        held--
      }
    } finally {
      release()
    }
  },
}
let redirects: string[] = [],
  expectUncertain = false
const location = {
  replace: (value: string) => {
    assert.equal(
      held,
      0,
      'hard navigation must happen after the Cookie lock releases'
    )
    assert.equal(storage.get('new-api:auth-cookie:uncertain') ?? null, null)
    redirects.push(value)
  },
  origin: 'https://console.example.test',
  pathname: '/profile',
  search: '',
  hash: '',
  get href() {
    return 'https://console.example.test/profile'
  },
  set href(value: string) {
    redirects.push(value)
  },
}
Object.defineProperty(globalThis, 'isSecureContext', {
  configurable: true,
  value: true,
})
Object.defineProperty(globalThis, 'navigator', {
  configurable: true,
  value: { locks },
})
Object.defineProperty(globalThis, 'window', {
  configurable: true,
  value: {
    location,
    localStorage: {
      getItem: (key: string) => storage.get(key) ?? null,
      setItem: (key: string, value: string) => storage.set(key, value),
      removeItem: (key: string) => storage.delete(key),
    },
  },
})
const original = api.defaults.adapter
function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => {
    resolve = done
  })
  return { promise, resolve }
}
function bundle(token: string, sid = 'family-A', id = 42): AuthBundle {
  return {
    access_token: token,
    token_type: 'Bearer',
    access_expires_at: Math.floor(Date.now() / 1000) + 600,
    user: { id, username: 'synthetic', role: 1 },
    session: {
      sid,
      current: true,
      login_method: 'password',
      ip: '',
      user_agent: '',
      created_at: 1,
      last_active_at: 1,
      expires_at: 2_000_000_000,
    },
  }
}
function response(
  config: Parameters<AxiosAdapter>[0],
  status: number,
  data: unknown
) {
  return { config, status, statusText: 'fixture', headers: {}, data }
}
afterEach(async () => {
  await tail
  assert.equal(held, 0)
  api.defaults.adapter = original
  setDevelopmentAuthRefreshAdapter(original as AxiosAdapter)
  clearAuthentication(false, 'idle')
  redirects = []
  if (expectUncertain) {
    assert.match(
      storage.get('new-api:auth-cookie:uncertain') ?? '',
      /^uncertain:/
    )
  } else {
    assert.equal(storage.size, 0)
  }
})
after(() => {
  for (const [key, value] of descriptors) {
    if (value) Object.defineProperty(globalThis, key, value)
    else Reflect.deleteProperty(globalThis, key)
  }
})

test('refresh and protected retry GET must not lose its bearer to a foreign same-family rotation between them', async () => {
  applyAuthBundle(bundle('expired-server-access'), false)
  let serverAccess = 'server-newer-access',
    refreshes = 0,
    reads = 0
  let foreign: Promise<unknown> | undefined
  const trace: string[] = []
  setDevelopmentAuthRefreshAdapter(async (config) => {
    assert.equal(held, 1)
    refreshes++
    serverAccess = 'local-refresh-access'
    trace.push('A refresh200')
    const local = bundle(serverAccess)
    foreign = locks.request('new-api:auth-refresh', {}, async () => {
      serverAccess = 'foreign-refresh-access'
      trace.push('B sameSID refresh200')
    })
    return response(config, 200, { success: true, data: local })
  })
  api.defaults.adapter = async (config) => {
    reads++
    // No old bearer is ever accepted. The foreign rotation is an actual
    // competing request on the same lock fixture, enqueued by the refresh.

    const sent = config.headers.get('Authorization')
    if (sent !== `Bearer ${serverAccess}`) {
      trace.push('A GET401')
      throw new AxiosError(
        'old access rejected',
        'ERR_BAD_REQUEST',
        config,
        undefined,
        response(config, 401, { code: 'AUTH_UNAUTHORIZED' })
      )
    }
    trace.push('A GET200')
    return response(config, 200, { success: true, data: { accepted: true } })
  }
  const result = await api
    .get('/api/user/self/announcements', { disableDuplicate: true })
    .then(
      (value) => ({ value }),
      (error) => ({ error })
    )
  await foreign
  assert.deepEqual(trace, [
    'A GET401',
    'A refresh200',
    'A GET200',
    'B sameSID refresh200',
  ])
  assert.ok(
    !('error' in result),
    'a coordinated read retry must complete with the fresh same-family credential'
  )
  assert.equal(refreshes, 1)
  assert.equal(reads, 2)
  assert.equal(redirects.length, 0)
  assert.equal(useAuthStore.getState().auth.session?.sid, 'family-A')
})

test('a failed or cancelled retry GET after confirmed Cookie200 cannot create Cookie uncertainty', async () => {
  for (const cancelled of [false, true]) {
    applyAuthBundle(bundle('old-A'), false)
    let reads = 0,
      refreshes = 0
    setDevelopmentAuthRefreshAdapter(async (config) => {
      refreshes++
      return response(config, 200, { success: true, data: bundle('known-A') })
    })
    api.defaults.adapter = async (config) => {
      reads++
      if (!config.authRetry) {
        throw new AxiosError(
          'initial401',
          'ERR_BAD_REQUEST',
          config,
          undefined,
          response(config, 401, {})
        )
      }
      assert.equal(config.timeout, 300)
      if (cancelled) throw new axios.CanceledError('observer cancelled', config)
      throw new AxiosError('read network failed', 'ERR_NETWORK', config)
    }
    await assert.rejects(
      api.get('/api/user/self/announcements', {
        timeout: 300,
        disableDuplicate: true,
        skipErrorHandler: true,
      }),
      (error) =>
        cancelled
          ? axios.isCancel(error)
          : axios.isAxiosError(error) && error.code === 'ERR_NETWORK'
    )
    assert.equal(reads, 2)
    assert.equal(refreshes, 1)
    assert.equal(useAuthStore.getState().auth.accessToken, 'known-A')
    assert.equal(storage.size, 0)
    assert.equal(getAuthCookieBlockedState(), null)
    assert.equal(redirects.length, 0)
  }
})

test('a genuine retry401 still clears the rejected principal and goes to sign in', async () => {
  applyAuthBundle(bundle('old-A'), false)
  let reads = 0,
    refreshes = 0
  setDevelopmentAuthRefreshAdapter(async (config) => {
    refreshes++
    return response(config, 200, { success: true, data: bundle('known-A') })
  })
  api.defaults.adapter = async (config) => {
    reads++
    throw new AxiosError(
      'revoked by server',
      'ERR_BAD_REQUEST',
      config,
      undefined,
      response(config, 401, {})
    )
  }
  await assert.rejects(
    api.get('/api/user/self/announcements', {
      disableDuplicate: true,
      skipErrorHandler: true,
    }),
    (error) => axios.isAxiosError(error) && error.response?.status === 401
  )
  assert.equal(reads, 2)
  assert.equal(refreshes, 1)
  assert.equal(useAuthStore.getState().auth.user, null)
  assert.deepEqual(redirects, ['/sign-in?redirect=%2Fprofile'])
  assert.equal(storage.size, 0)
  assert.equal(getAuthCookieBlockedState(), null)
})

test('refresh429 keeps its cooldown across subsequent read401 attempts and never retries the GET', async () => {
  applyAuthBundle(bundle('old-A'), false)
  let reads = 0,
    refreshes = 0
  setDevelopmentAuthRefreshAdapter(async (config) => {
    refreshes++
    const res = {
      ...response(config, 429, { code: 'AUTH_RATE_LIMITED' }),
      headers: { 'retry-after': '30' },
    }
    throw new AxiosError(
      'rate limited',
      'ERR_BAD_REQUEST',
      config,
      undefined,
      res
    )
  })
  api.defaults.adapter = async (config) => {
    reads++
    throw new AxiosError(
      'old access',
      'ERR_BAD_REQUEST',
      config,
      undefined,
      response(config, 401, {})
    )
  }
  for (let n = 0; n < 2; n++) {
    await assert.rejects(
      api.get('/api/user/self/announcements', {
        disableDuplicate: true,
        skipErrorHandler: true,
      })
    )
  }
  assert.equal(reads, 2)
  assert.equal(refreshes, 1)
  assert.equal(useAuthStore.getState().auth.session?.sid, 'family-A')
  assert.equal(redirects.length, 0)
  assert.equal(storage.size, 0)
})

test('changing local account while read recovery queues cannot refresh or replay under the new family', async () => {
  applyAuthBundle(bundle('old-A'), false)
  const release = deferred(),
    entered = deferred()
  let reads = 0,
    refreshes = 0
  const blocker = locks.request(
    'new-api:auth-refresh',
    {},
    () => release.promise
  )
  setDevelopmentAuthRefreshAdapter(async (config) => {
    refreshes++
    return response(config, 200, {
      success: true,
      data: bundle('unexpected-A'),
    })
  })
  api.defaults.adapter = async (config) => {
    reads++
    entered.resolve()
    throw new AxiosError(
      'old access',
      'ERR_BAD_REQUEST',
      config,
      undefined,
      response(config, 401, {})
    )
  }
  const request = api
    .get('/api/user/self/announcements', {
      disableDuplicate: true,
      skipErrorHandler: true,
    })
    .then(
      () => ({ cancelled: false }),
      (error) => ({ cancelled: axios.isCancel(error) })
    )
  await entered.promise
  await new Promise((resolve) => setTimeout(resolve, 0))
  applyAuthBundle(bundle('new-B', 'family-B', 43), false)
  release.resolve()
  await blocker
  assert.deepEqual(await request, { cancelled: true })
  assert.equal(reads, 1)
  assert.equal(refreshes, 0)
  assert.equal(useAuthStore.getState().auth.user?.id, 43)
  assert.equal(redirects.length, 0)
  assert.equal(storage.size, 0)
})

test('this read coordination does not add a Cookie lock or extra retry to the existing POST response path', async () => {
  applyAuthBundle(bundle('old-A'), false)
  let writes = 0,
    refreshes = 0
  setDevelopmentAuthRefreshAdapter(async (config) => {
    refreshes++
    return response(config, 200, { success: true, data: bundle('known-A') })
  })
  api.defaults.adapter = async (config) => {
    writes++
    assert.equal(held, 0)
    if (!config.authRetry) {
      throw new AxiosError(
        'old access',
        'ERR_BAD_REQUEST',
        config,
        undefined,
        response(config, 401, {})
      )
    }
    return response(config, 200, { success: true })
  }
  await api.post(
    '/api/owned-fixture-save',
    { synthetic: true },
    { skipErrorHandler: true }
  )
  assert.equal(writes, 2)
  assert.equal(refreshes, 1)
  assert.equal(storage.size, 0)
})

test('a valid B factor flow prevents old A read recovery from refreshing away the ceremony', async () => {
  applyAuthBundle(bundle('old-A'), false)
  const expiresAt = Math.floor(Date.now() / 1000) + 300
  useAuthStore.getState().auth.setPending2FAFlowToken('pending-B', expiresAt)
  let reads = 0,
    refreshes = 0
  setDevelopmentAuthRefreshAdapter(async (config) => {
    refreshes++
    return response(config, 200, {
      success: true,
      data: bundle('unexpected-A'),
    })
  })
  api.defaults.adapter = async (config) => {
    reads++
    throw new AxiosError(
      'old access',
      'ERR_BAD_REQUEST',
      config,
      undefined,
      response(config, 401, {})
    )
  }
  await assert.rejects(
    api.get('/api/user/self/announcements', {
      disableDuplicate: true,
      skipErrorHandler: true,
    })
  )
  assert.equal(reads, 1)
  assert.equal(refreshes, 0)
  assert.equal(useAuthStore.getState().auth.pending2FAFlowToken, 'pending-B')
  assert.equal(useAuthStore.getState().auth.pending2FAFlowExpiresAt, expiresAt)
  assert.equal(redirects.length, 0)
  assert.equal(storage.size, 0)
})

test('provider paths, external origins, downloads and streams stay outside ordinary API-read coordination', async () => {
  for (const input of [
    { url: '/api/user/../oauth/google/callback' },
    {
      url: '/api/user/self/announcements',
      baseURL: 'https://outside.example.test/',
    },
    { url: '/api/user/self/announcements', responseType: 'blob' as const },
    {
      url: '/api/user/self/announcements',
      headers: { Accept: 'text/event-stream' },
    },
  ]) {
    applyAuthBundle(bundle('old-A'), false)
    let reads = 0,
      refreshes = 0
    setDevelopmentAuthRefreshAdapter(async (config) => {
      refreshes++
      return response(config, 200, { success: true, data: bundle('known-A') })
    })
    api.defaults.adapter = async (config) => {
      reads++
      if (!config.authRetry) {
        throw new AxiosError(
          'old access',
          'ERR_BAD_REQUEST',
          config,
          undefined,
          response(config, 401, {})
        )
      }
      assert.equal(held, 0)
      return response(config, 200, { success: true })
    }
    await api.request({ ...input, method: 'get', skipErrorHandler: true })
    assert.equal(reads, 2)
    assert.equal(refreshes, 1)
    assert.equal(storage.size, 0)
  }
})

test('unknown refresh transport keeps the persistent marker and blocks another read recovery before dispatch', async () => {
  expectUncertain = true
  applyAuthBundle(bundle('old-A'), false)
  let refreshes = 0,
    reads = 0
  setDevelopmentAuthRefreshAdapter(async (config) => {
    refreshes++
    throw new AxiosError('Cookie response unknown', 'ETIMEDOUT', config)
  })
  api.defaults.adapter = async (config) => {
    reads++
    throw new AxiosError(
      'old access',
      'ERR_BAD_REQUEST',
      config,
      undefined,
      response(config, 401, {})
    )
  }
  await assert.rejects(
    api.get('/api/user/self/announcements', {
      disableDuplicate: true,
      skipErrorHandler: true,
    })
  )
  const marker = storage.get('new-api:auth-cookie:uncertain')
  assert.match(marker ?? '', /^uncertain:/)
  assert.equal(getAuthCookieBlockedState(), 'uncertain')
  await assert.rejects(
    api.get('/api/user/self/announcements', {
      disableDuplicate: true,
      skipErrorHandler: true,
    })
  )
  assert.equal(refreshes, 1)
  assert.equal(reads, 2)
  assert.equal(storage.get('new-api:auth-cookie:uncertain'), marker)
  assert.equal(redirects.length, 0)
})
