/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'

import { api } from '@/lib/api'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'

import { refreshCurrentAccount } from './use-auth-user-refresh'

const originalGet = api.get
const previousAuth = useAuthStore.getState().auth
const user = { id: 41, username: 'test', quota: 10 } as AuthUser

const bundle = (sid: string, granted = false) => ({
  access_token: 'local-test-token',
  token_type: 'Bearer',
  access_expires_at: 1900000000,
  user: { ...user, developer_access_granted: granted },
  session: {
    sid,
    current: true,
    login_method: 'password',
    ip: '127.0.0.1',
    user_agent: 'local-test',
    created_at: 1,
    last_active_at: 1,
    expires_at: 1900000000,
  },
})

function deferredAccountRead() {
  let resolve: (value: unknown) => void = () => undefined
  let reject: (error: Error) => void = () => undefined
  const response = new Promise<unknown>((resolveResponse, rejectResponse) => {
    resolve = resolveResponse
    reject = rejectResponse
  })
  return {
    response,
    resolve: (granted: boolean) =>
      resolve({
        data: {
          success: true,
          data: { ...user, developer_access_granted: granted },
        },
      }),
    reject,
  }
}

afterEach(() => {
  api.get = originalGet
  useAuthStore.setState({ auth: previousAuth })
})

test('refreshes the current account after a key mutation', async () => {
  useAuthStore.getState().auth.setUser(user)
  api.get = (async () => ({
    data: { success: true, data: { ...user, quota: 20 } },
  })) as typeof api.get
  await refreshCurrentAccount()
  assert.equal(useAuthStore.getState().auth.user?.quota, 20)
})

test('does not restore the old account if it changes while refreshing', async () => {
  useAuthStore.getState().auth.setUser(user)
  let finish: (value: unknown) => void = () => undefined
  api.get = (() =>
    new Promise<unknown>((resolve) => {
      finish = resolve
    })) as typeof api.get
  const request = refreshCurrentAccount()
  useAuthStore.getState().auth.setUser({ ...user, id: 42 })
  finish({ data: { success: true, data: { ...user, quota: 20 } } })
  assert.equal(await request, null)
  assert.equal(useAuthStore.getState().auth.user?.id, 42)
})

test('a grant refresh cannot promote a newer session of the same account', async () => {
  useAuthStore.getState().auth.setBundle(bundle('old-session'))
  let finish: (value: unknown) => void = () => undefined
  api.get = (() =>
    new Promise<unknown>((resolve) => {
      finish = resolve
    })) as typeof api.get
  const request = refreshCurrentAccount()
  useAuthStore.getState().auth.setBundle(bundle('new-session'))
  finish({
    data: { success: true, data: { ...user, developer_access_granted: true } },
  })
  assert.equal(await request, null)
  assert.equal(useAuthStore.getState().auth.session?.sid, 'new-session')
  assert.equal(
    useAuthStore.getState().auth.user?.developer_access_granted,
    false
  )
})

for (const order of ['new-first', 'old-first']) {
  test(`same-account reads retain the new L1 result when responses finish ${order}`, async () => {
    useAuthStore.getState().auth.setBundle(bundle('same-session', false))
    const oldRead = deferredAccountRead()
    const newRead = deferredAccountRead()
    let calls = 0
    api.get = (() =>
      ++calls === 1 ? oldRead.response : newRead.response) as typeof api.get
    const oldRequest = refreshCurrentAccount()
    const newRequest = refreshCurrentAccount()
    if (order === 'new-first') {
      newRead.resolve(true)
      assert.equal((await newRequest)?.developer_access_granted, true)
      oldRead.resolve(false)
      assert.equal(await oldRequest, null)
    } else {
      oldRead.resolve(false)
      assert.equal(await oldRequest, null)
      newRead.resolve(true)
      assert.equal((await newRequest)?.developer_access_granted, true)
    }
    assert.equal(
      useAuthStore.getState().auth.user?.developer_access_granted,
      true
    )
  })
}

for (const order of ['failure-first', 'stale-first']) {
  test(`a failed newer refresh preserves confirmed L1 when responses finish ${order}`, async () => {
    useAuthStore.getState().auth.setBundle(bundle('same-session', true))
    const oldRead = deferredAccountRead()
    const newRead = deferredAccountRead()
    let calls = 0
    api.get = (() =>
      ++calls === 1 ? oldRead.response : newRead.response) as typeof api.get
    const oldRequest = refreshCurrentAccount()
    const newRequest = refreshCurrentAccount()
    if (order === 'failure-first') {
      newRead.reject(new Error('HTTP 503'))
      assert.equal(await newRequest, null)
      oldRead.resolve(false)
      assert.equal(await oldRequest, null)
    } else {
      oldRead.resolve(false)
      assert.equal(await oldRequest, null)
      newRead.reject(new Error('HTTP 503'))
      assert.equal(await newRequest, null)
    }
    assert.equal(
      useAuthStore.getState().auth.user?.developer_access_granted,
      true
    )
  })
}
