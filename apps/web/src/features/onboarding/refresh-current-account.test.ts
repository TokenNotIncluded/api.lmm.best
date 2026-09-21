/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'

import { api } from '@/lib/api'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'

import { refreshCurrentAccount } from './use-auth-user-refresh'

const originalGet = api.get
const previousAuth = useAuthStore.getState().auth
const user = { id: 41, username: 'test', quota: 10 } as AuthUser

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
