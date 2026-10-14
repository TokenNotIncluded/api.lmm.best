/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import axios, { type AxiosAdapter } from 'axios'

import { login } from '@/features/auth/api'
import { api } from '@/lib/api'
import {
  applyAuthBundle,
  applyPending2FAFlow,
  bootstrapAuthentication,
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
let redirects: string[] = []
const location = {
  replace: (value: string) => redirects.push(value),
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
  assert.equal(storage.size, 0)
})
after(() => {
  for (const [key, value] of descriptors) {
    if (value) Object.defineProperty(globalThis, key, value)
    else Reflect.deleteProperty(globalThis, key)
  }
})

test('a queued explicit password login must not silently disappear when the earlier background bootstrap commits', async () => {
  const entered = deferred(),
    release = deferred()
  let posts = 0
  setDevelopmentAuthRefreshAdapter(async (config) => {
    entered.resolve()
    await release.promise
    return response(config, 200, {
      success: true,
      data: bundle('background-A'),
    })
  })
  api.defaults.adapter = async (config) => {
    posts++
    return response(config, 200, {
      success: true,
      data: bundle('explicit-B', 'family-B', 43),
    })
  }
  const background = bootstrapAuthentication()
  await entered.promise
  const explicit = login({ username: 'explicit-B', password: 'synthetic' })
  const observed = explicit.then(
    (value) => ({ value }),
    (error) => ({ error })
  )
  release.resolve()
  await background
  const result = await observed

  assert.ok(
    !('error' in result),
    'the explicit queued action should run after passive bootstrap'
  )
  assert.equal(posts, 1)
  assert.equal(useAuthStore.getState().auth.user?.id, 43)
})

async function queuedBackground(status = 200, signal?: AbortSignal) {
  const entered = deferred(),
    release = deferred()
  let posts = 0
  setDevelopmentAuthRefreshAdapter(async (config) => {
    entered.resolve()
    await release.promise
    return response(
      config,
      status,
      status === 200
        ? { success: true, data: bundle('background-A') }
        : { success: false }
    )
  })
  api.defaults.adapter = async (config) => {
    posts++
    return response(config, 200, {
      success: true,
      data: bundle('explicit-B', 'family-B', 43),
    })
  }
  const background = bootstrapAuthentication()
  await entered.promise
  const explicit = login(
    { username: 'explicit-B', password: 'synthetic' },
    signal
  )
  const observed = explicit.then(
    (value) => ({ value }),
    (error) => ({ error })
  )
  return { background, release, observed, posts: () => posts }
}

test('a transient passive refresh does not supersede a queued login', async () => {
  const scene = await queuedBackground(503)
  scene.release.resolve()
  await scene.background
  const result = await scene.observed
  assert.ok(!('error' in result))
  assert.equal(scene.posts(), 1)
  assert.equal(useAuthStore.getState().auth.user?.id, 43)
})

test('a passive unauthorized clear remains a superseding identity change', async () => {
  const scene = await queuedBackground(401)
  scene.release.resolve()
  await scene.background
  const result = await scene.observed
  assert.ok('error' in result && axios.isCancel(result.error))
  assert.equal(scene.posts(), 0)
  assert.equal(useAuthStore.getState().auth.user, null)
})

test('a profile write before the passive commit still supersedes the old login', async () => {
  const scene = await queuedBackground()
  useAuthStore
    .getState()
    .auth.setUser({ id: 44, username: 'profile-write', role: 1 })
  scene.release.resolve()
  await scene.background
  const result = await scene.observed
  assert.ok('error' in result && axios.isCancel(result.error))
  assert.equal(scene.posts(), 0)
})

test('a synchronous newer bundle is never attributed to the passive refresh', async () => {
  const scene = await queuedBackground()
  let unsubscribe = () => {}
  unsubscribe = useAuthStore.subscribe((state) => {
    if (state.auth.accessToken === 'background-A') {
      unsubscribe()
      applyAuthBundle(bundle('newer-C', 'family-C', 44), false)
    }
  })
  try {
    scene.release.resolve()
    await scene.background
    const result = await scene.observed
    assert.ok('error' in result && axios.isCancel(result.error))
    assert.equal(scene.posts(), 0)
    assert.equal(useAuthStore.getState().auth.user?.id, 44)
  } finally {
    unsubscribe()
  }
})

test('a newer factor challenge with unchanged account epoch supersedes the queued login', async () => {
  const scene = await queuedBackground()
  const expires = Math.floor(Date.now() / 1000) + 120
  let unsubscribe = () => {}
  unsubscribe = useAuthStore.subscribe((state) => {
    if (state.auth.accessToken === 'background-A') {
      unsubscribe()
      applyPending2FAFlow('newer-factor-flow', expires)
    }
  })
  try {
    scene.release.resolve()
    await scene.background
    const result = await scene.observed
    assert.ok('error' in result && axios.isCancel(result.error))
    assert.equal(scene.posts(), 0)
    assert.equal(
      useAuthStore.getState().auth.pending2FAFlowToken,
      'newer-factor-flow'
    )
    assert.equal(useAuthStore.getState().auth.pending2FAFlowExpiresAt, expires)
  } finally {
    unsubscribe()
  }
})

test('a synchronous clear is never attributed to the successful refresh', async () => {
  const scene = await queuedBackground()
  let unsubscribe = () => {}
  unsubscribe = useAuthStore.subscribe((state) => {
    if (state.auth.accessToken === 'background-A') {
      unsubscribe()
      clearAuthentication(false)
    }
  })
  try {
    scene.release.resolve()
    await scene.background
    const result = await scene.observed
    assert.ok('error' in result && axios.isCancel(result.error))
    assert.equal(scene.posts(), 0)
    assert.equal(useAuthStore.getState().auth.user, null)
  } finally {
    unsubscribe()
  }
})

test('cancelling queued login never dispatches or cancels the earlier refresh', async () => {
  const controller = new AbortController()
  const scene = await queuedBackground(200, controller.signal)
  controller.abort()
  scene.release.resolve()
  await scene.background
  const result = await scene.observed
  assert.ok('error' in result)
  assert.equal(scene.posts(), 0)
  assert.equal(useAuthStore.getState().auth.accessToken, 'background-A')
})

test('a login cancelled after dispatch still commits its known Cookie response', async () => {
  const entered = deferred(),
    release = deferred()
  const controller = new AbortController()
  api.defaults.adapter = async (config) => {
    entered.resolve()
    await release.promise
    return response(config, 200, {
      success: true,
      data: bundle('active-B', 'family-B', 43),
    })
  }
  const explicit = login(
    { username: 'explicit-B', password: 'synthetic' },
    controller.signal
  )
  const observed = explicit.then(
    (value) => ({ value }),
    (error) => ({ error })
  )
  await entered.promise
  controller.abort()
  release.resolve()
  const result = await observed
  assert.ok('error' in result && axios.isCancel(result.error))
  assert.equal(useAuthStore.getState().auth.accessToken, 'active-B')
})

test('an explicit writer between the accepted refresh and queued login supersedes it', async () => {
  const entered = deferred(),
    release = deferred()
  let posts = 0
  setDevelopmentAuthRefreshAdapter(async (config) => {
    entered.resolve()
    await release.promise
    return response(config, 200, {
      success: true,
      data: bundle('background-A'),
    })
  })
  api.defaults.adapter = async (config) => {
    posts++
    return response(config, 200, {
      success: true,
      data: bundle('must-not-commit', 'family-B', 43),
    })
  }
  const background = bootstrapAuthentication()
  await entered.promise
  const competing = locks.request('new-api:auth-refresh', {}, async () => {
    applyAuthBundle(bundle('newer-C', 'family-C', 44), false)
  })
  const result = login({ username: 'old-B', password: 'synthetic' }).then(
    (value) => ({ value }),
    (error) => ({ error })
  )
  release.resolve()
  await background
  await competing
  const observed = await result
  assert.ok('error' in observed && axios.isCancel(observed.error))
  assert.equal(posts, 0)
  assert.equal(useAuthStore.getState().auth.user?.id, 44)
})

test('a factor flow created after the accepted refresh invalidates the captured receipt', async () => {
  const entered = deferred(),
    release = deferred()
  let posts = 0
  const expires = Math.floor(Date.now() / 1000) + 120
  setDevelopmentAuthRefreshAdapter(async (config) => {
    entered.resolve()
    await release.promise
    return response(config, 200, {
      success: true,
      data: bundle('background-A'),
    })
  })
  api.defaults.adapter = async (config) => {
    posts++
    return response(config, 200, {
      success: true,
      data: bundle('must-not-commit', 'family-B', 43),
    })
  }
  const background = bootstrapAuthentication()
  await entered.promise
  const competing = locks.request('new-api:auth-refresh', {}, async () => {
    applyPending2FAFlow('newest-flow', expires)
  })
  const result = login({ username: 'old-B', password: 'synthetic' }).then(
    (value) => ({ value }),
    (error) => ({ error })
  )
  release.resolve()
  await background
  await competing
  const observed = await result
  assert.ok('error' in observed && axios.isCancel(observed.error))
  assert.equal(posts, 0)
  assert.equal(useAuthStore.getState().auth.pending2FAFlowToken, 'newest-flow')
  assert.equal(useAuthStore.getState().auth.pending2FAFlowExpiresAt, expires)
})
