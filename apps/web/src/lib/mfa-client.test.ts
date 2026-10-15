/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'

import {
  AxiosError,
  isCancel,
  type AxiosAdapter,
  type AxiosResponse,
} from 'axios'

import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

import {
  api,
  setup2FA,
  enable2FA,
  disable2FA,
  regenerate2FABackupCodes,
  get2FAStatus,
} from './api'
import {
  applyAuthBundle,
  clearAuthentication,
  setDevelopmentAuthRefreshAdapter,
  createRefreshRunner,
  isAuthBundle,
} from './auth-session'

const originalAdapter = api.defaults.adapter
function bundle(token = 'initial', sid = 'mfa-session', id = 42): AuthBundle {
  const expires = Math.floor(Date.now() / 1000) + 600
  return {
    access_token: token,
    token_type: 'Bearer',
    access_expires_at: expires,
    user: { id, username: 'synthetic-mfa', role: 1 },
    session: {
      sid,
      current: true,
      login_method: 'password',
      ip: '127.0.0.1',
      user_agent: 'fixture',
      created_at: 1,
      last_active_at: 1,
      expires_at: expires + 600,
    },
  }
}
function response(
  config: Parameters<AxiosAdapter>[0],
  status: number,
  data: unknown
): AxiosResponse {
  return { config, data, status, statusText: 'fixture', headers: {} }
}
afterEach(() => {
  api.defaults.adapter = originalAdapter
  setDevelopmentAuthRefreshAdapter(originalAdapter as AxiosAdapter)
  clearAuthentication(false, 'idle')
})

test('a same-SID newer rotation survives a late retried read 401', async () => {
  applyAuthBundle(bundle(), false)
  let release!: () => void, entered!: () => void
  const wait = new Promise<void>((resolve) => {
    release = resolve
  })
  const retryStarted = new Promise<void>((resolve) => {
    entered = resolve
  })
  let requests = 0
  setDevelopmentAuthRefreshAdapter(async (config) =>
    response(config, 200, { success: true, data: bundle('first-refresh') })
  )
  api.defaults.adapter = async (config) => {
    requests++
    if (requests === 2) {
      entered()
      await wait
    }
    throw new AxiosError(
      'old credential',
      'ERR_BAD_REQUEST',
      config,
      undefined,
      response(config, 401, {})
    )
  }
  const rejected = assert.rejects(
    api.get('/api/user/self', { disableDuplicate: true }),
    (error) => isCancel(error)
  )
  await retryStarted
  applyAuthBundle(bundle('newer-mfa-rotation'), false)
  release()
  await rejected
  assert.equal(requests, 2)
  assert.equal(useAuthStore.getState().auth.accessToken, 'newer-mfa-rotation')
})

test('an old same-SID successful mutation cannot overwrite a newer rotation', async () => {
  applyAuthBundle(bundle(), false)
  let release!: () => void, entered!: () => void
  const wait = new Promise<void>((resolve) => {
    release = resolve
  })
  const started = new Promise<void>((resolve) => {
    entered = resolve
  })
  api.defaults.adapter = async (config) => {
    entered()
    await wait
    return response(config, 200, { success: true, data: bundle('old-success') })
  }
  const rejected = assert.rejects(enable2FA('123456'), (error) =>
    isCancel(error)
  )
  await started
  applyAuthBundle(bundle('newer-mfa-rotation'), false)
  release()
  await rejected
  assert.equal(useAuthStore.getState().auth.accessToken, 'newer-mfa-rotation')
})

test('a cookie family mismatch is never retried without its expected SID', async () => {
  let expectedSID: string | undefined = 'mfa-session'
  const requested: Array<string | undefined> = [],
    accepted: AuthBundle[] = []
  const result = await createRefreshRunner({
    request: async (sid) => {
      requested.push(sid)
      return requested.length === 1
        ? { status: 409, data: { code: 'AUTH_SESSION_MISMATCH' } }
        : {
            status: 200,
            data: {
              success: true,
              data: bundle('wrong-account', 'wrong-family', 99),
            },
          }
    },
    getExpectedSID: () => expectedSID,
    parseBundle: (value) => (isAuthBundle(value) ? value : null),
    acceptBundle: (value) => {
      accepted.push(value)
    },
    clear: () => {
      expectedSID = undefined
    },
    markTransient() {},
    wait: async () => {},
  })()
  assert.equal(result.kind, 'out_of_sync')
  assert.deepEqual(requested, ['mfa-session'])
  assert.deepEqual(accepted, [])
})

test('a successful refresh response cannot substitute another account family', async () => {
  const accepted: AuthBundle[] = []
  let cleared = 0
  const result = await createRefreshRunner({
    request: async () => ({
      status: 200,
      data: {
        success: true,
        data: bundle('wrong-account', 'wrong-family', 99),
      },
    }),
    getExpectedSID: () => 'mfa-session',
    parseBundle: (value) => (isAuthBundle(value) ? value : null),
    acceptBundle: (value) => {
      accepted.push(value)
    },
    clear: () => {
      cleared++
    },
    markTransient() {},
    wait: async () => {},
  })()
  assert.equal(result.kind, 'out_of_sync')
  assert.deepEqual(accepted, [])
  assert.equal(cleared, 1)
})
test('one wrong management code dispatches once, preserves credentials and never refreshes', async () => {
  applyAuthBundle(bundle(), false)
  let calls = 0,
    refreshes = 0
  setDevelopmentAuthRefreshAdapter(async () => {
    refreshes++
    throw new Error('unexpected refresh')
  })
  api.defaults.adapter = async (config) => {
    calls++
    throw new AxiosError(
      'bad code',
      'ERR_BAD_REQUEST',
      config,
      undefined,
      response(config, 400, { success: false, code: 'MFA_CODE_INVALID' })
    )
  }
  for (const submit of [enable2FA, disable2FA, regenerate2FABackupCodes]) {
    await assert.rejects(submit('000000'))
  }
  assert.equal(calls, 3)
  assert.equal(refreshes, 0)
  assert.equal(useAuthStore.getState().auth.accessToken, 'initial')
})
test('setup may cross a legitimate same-SID refresh and enable consumes the new rotation', async () => {
  applyAuthBundle(bundle(), false)
  const headers: string[] = []
  setDevelopmentAuthRefreshAdapter(async (config) =>
    response(config, 200, { success: true, data: bundle('refreshed') })
  )
  api.defaults.adapter = async (config) => {
    headers.push(String(config.headers.Authorization))
    return response(config, 200, {
      success: true,
      data: config.url?.endsWith('/setup')
        ? {
            secret: 'synthetic',
            qr_code_data: 'otpauth://fixture',
            backup_codes: ['AAAA-BBBB'],
          }
        : config.url?.endsWith('/enable')
          ? bundle('enabled-rotation')
          : { enabled: true, locked: false, backup_codes_remaining: 4 },
    })
  }
  await setup2FA()
  const expired = bundle()
  expired.access_expires_at = 1
  applyAuthBundle(expired, false)
  await enable2FA('123456')
  assert.equal(useAuthStore.getState().auth.accessToken, 'enabled-rotation')
  assert.equal(useAuthStore.getState().auth.session?.sid, 'mfa-session')
  await get2FAStatus()
  assert.deepEqual(headers, [
    'Bearer initial',
    'Bearer refreshed',
    'Bearer enabled-rotation',
  ])
})
test('invalid rotation cannot be returned as a successful management mutation', async () => {
  applyAuthBundle(bundle(), false)
  api.defaults.adapter = async (config) =>
    response(config, 200, {
      success: true,
      data: { backup_codes: ['AAAA-BBBB'] },
    })
  await assert.rejects(
    regenerate2FABackupCodes('123456'),
    /Invalid authentication rotation/
  )
  assert.equal(useAuthStore.getState().auth.user, null)
})
test('old success and old 401 cannot clear or replay operations into a new session', async () => {
  for (const status of [200, 401]) {
    applyAuthBundle(bundle(), false)
    let release!: () => void,
      entered!: () => void,
      refreshes = 0,
      calls = 0
    const wait = new Promise<void>((resolve) => {
        release = resolve
      }),
      started = new Promise<void>((resolve) => {
        entered = resolve
      })
    setDevelopmentAuthRefreshAdapter(async () => {
      refreshes++
      throw new Error('unexpected refresh')
    })
    api.defaults.adapter = async (config) => {
      calls++
      entered()
      await wait
      const result = response(config, status, {
        success: true,
        data: bundle('old-rotation'),
      })
      if (status === 401) {
        throw new AxiosError(
          'expired old bearer',
          'ERR_BAD_REQUEST',
          config,
          undefined,
          result
        )
      }
      return result
    }
    const rejected = assert.rejects(enable2FA('123456'), (error) =>
      isCancel(error)
    )
    await started
    applyAuthBundle(bundle('account-b', 'session-b', 99), false)
    release()
    await rejected
    assert.equal(calls, 1)
    assert.equal(refreshes, 0)
    assert.equal(useAuthStore.getState().auth.accessToken, 'account-b')
  }
})
