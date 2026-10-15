/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// @ts-expect-error Bun test mocking is available only to the test runtime.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { isRedirect } from '@tanstack/react-router'
import { Window } from 'happy-dom'

const dom = new Window({ url: 'https://console.example.test/profile' })
for (const key of [
  'window',
  'document',
  'navigator',
  'localStorage',
  'HTMLElement',
  'Node',
  'Element',
  'Event',
  'MutationObserver',
  'getComputedStyle',
  'requestAnimationFrame',
  'cancelAnimationFrame',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.assign(globalThis, { __LMM_PERSONA_DEBUG__: false })
window.localStorage.setItem('setup_status_checked', 'true')
const { setDevelopmentAuthRefreshAdapter, clearAuthentication } =
  await import('@/lib/auth-session')
const { useAuthStore } = await import('@/stores/auth-store')
const { Route: rootRoute } = await import('./__root')
const { Route: protectedRoute } = await import('./_authenticated/route')

afterEach(() => clearAuthentication(false, 'idle'))
after(() => {
  mock.restore()
  dom.close()
})

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => {
    resolve = done
  })
  return { promise, resolve }
}
const bundle = {
  access_token: 'synthetic-bootstrap',
  token_type: 'Bearer',
  access_expires_at: Math.floor(Date.now() / 1000) + 600,
  user: { id: 42, username: 'synthetic', role: 1 },
  session: {
    sid: 'synthetic-family',
    current: true,
    login_method: 'password',
    ip: '',
    user_agent: '',
    created_at: 1,
    last_active_at: 1,
    expires_at: 2_000_000_000,
  },
}
function runRoot(href: string, controller = new AbortController()) {
  return Promise.resolve(
    rootRoute.options.beforeLoad?.({
      location: { href, pathname: href },
      abortController: controller,
    } as never)
  )
}

test('actual protected root waits beyond its former 2500ms cutoff and the guard opens after one refresh success', async () => {
  useAuthStore.getState().auth.reset('idle')
  const entered = deferred(),
    release = deferred()
  let requests = 0,
    completed = false
  setDevelopmentAuthRefreshAdapter(async (config) => {
    requests++
    entered.resolve()
    await release.promise
    return {
      config,
      data: { success: true, data: bundle },
      headers: {},
      status: 200,
      statusText: 'fixture',
    }
  })
  const navigation = runRoot('/profile').then(() => {
    completed = true
  })
  await entered.promise
  try {
    await new Promise((resolve) => setTimeout(resolve, 2700))
    assert.equal(
      completed,
      false,
      'root must still own the pending authentication task'
    )
    assert.equal(useAuthStore.getState().auth.bootstrapState, 'checking')
    release.resolve()
    await navigation
    assert.equal(requests, 1)
    await protectedRoute.options.beforeLoad?.({
      location: { href: '/profile', pathname: '/profile' },
    } as never)
  } finally {
    release.resolve()
    await navigation
  }
})

test('actual public root remains nonblocking while its shared background check runs', async () => {
  const entered = deferred(),
    release = deferred()
  let requests = 0
  setDevelopmentAuthRefreshAdapter(async (config) => {
    requests++
    entered.resolve()
    await release.promise
    return {
      config,
      data: { success: true, data: bundle },
      headers: {},
      status: 200,
      statusText: 'fixture',
    }
  })
  await runRoot('/sign-in')
  await entered.promise
  assert.equal(useAuthStore.getState().auth.bootstrapState, 'checking')
  release.resolve()
  const { refreshAuthentication } = await import('@/lib/auth-session')
  await refreshAuthentication()
  assert.equal(requests, 1)
})

test('actual protected root retains anonymous redirect after a terminal unauthorized refresh', async () => {
  setDevelopmentAuthRefreshAdapter(async (config) => ({
    config,
    data: { success: false, code: 'AUTH_UNAUTHORIZED' },
    headers: {},
    status: 401,
    statusText: 'fixture',
  }))
  await runRoot('/profile')
  let thrown: unknown
  try {
    await protectedRoute.options.beforeLoad?.({
      location: { href: '/profile', pathname: '/profile' },
    } as never)
  } catch (error) {
    thrown = error
  }
  assert.ok(isRedirect(thrown))
  assert.equal(thrown.options.to, '/sign-in')
})
