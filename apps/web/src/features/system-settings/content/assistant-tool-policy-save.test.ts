/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'

import { isCancel, type AxiosRequestConfig, type AxiosResponse } from 'axios'

import { api } from '@/lib/api'
import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

import { updateAssistantSystemOptions } from '../api'
import { DEFAULT_ASSISTANT_TOOL_POLICY } from './assistant-tool-policy'
import {
  captureAssistantSettingsAuthScope,
  getLatestAssistantToolPolicy,
} from './assistant-tool-policy-save'

const originalAdapter = api.defaults.adapter
const previousAuth = useAuthStore.getState().auth
const remotePolicy = '{"version":1,"groups":{"drawing":false},"tools":{}}'

function rootBundle(id: number, sid: string): AuthBundle {
  return {
    access_token: 'local-assistant-policy-test-token',
    token_type: 'Bearer',
    access_expires_at: 2000000000,
    user: { id, username: `local-root-${id}`, role: 100 },
    session: {
      sid,
      current: true,
      login_method: 'password',
      ip: '127.0.0.1',
      user_agent: 'local-assistant-policy-test',
      created_at: 1,
      last_active_at: 1,
      expires_at: 2000000000,
    },
  }
}

function deferredOptionReads() {
  const reads: Array<{
    config: AxiosRequestConfig
    finish: (policy: string) => void
  }> = []
  // Keep the real duplicate-request and identity interceptors. Only the
  // transport adapter is replaced, so these tests never send a network request.
  api.defaults.adapter = (config) =>
    new Promise<AxiosResponse>((resolve) => {
      reads.push({
        config,
        finish: (policy) =>
          resolve({
            status: 200,
            statusText: 'OK',
            headers: {},
            config,
            data: {
              success: true,
              message: '',
              data: [{ key: 'AssistantToolPolicy', value: policy }],
            },
          }),
      })
    })
  return reads
}

async function waitForReads(reads: unknown[], count: number) {
  for (let tick = 0; tick < 50 && reads.length < count; tick++) {
    await Promise.resolve()
  }
  assert.equal(
    reads.length,
    count,
    'expected physical transport reads must dispatch'
  )
}

afterEach(() => {
  api.defaults.adapter = originalAdapter
  useAuthStore.setState({ auth: previousAuth })
})

test('a policy preflight dispatches a fresh scoped read instead of joining an older deduplicated option read', async () => {
  useAuthStore
    .getState()
    .auth.setBundle(rootBundle(901, 'local-root-a-session'))
  const scope = captureAssistantSettingsAuthScope()
  const reads = deferredOptionReads()
  const readConfig = { skipBusinessError: true, skipErrorHandler: true }
  const oldRead = api.get('/api/option/', readConfig)
  const duplicateRead = api.get('/api/option/', readConfig)
  await waitForReads(reads, 1)

  const preflight = getLatestAssistantToolPolicy(scope)
  await waitForReads(reads, 2)
  assert.equal(reads[1].config.url, '/api/option/')
  assert.equal(reads[1].config.disableDuplicate, true)
  assert.deepEqual(reads[1].config.authScope, {
    userId: 901,
    sessionId: 'local-root-a-session',
  })
  reads[1].finish(remotePolicy)
  assert.equal(await preflight, remotePolicy)
  reads[0].finish(DEFAULT_ASSISTANT_TOOL_POLICY)
  await Promise.all([oldRead, duplicateRead])
  assert.equal(reads.length, 2)
})

test('an old administrator policy response is rejected after switching accounts and cannot start a write', async () => {
  useAuthStore
    .getState()
    .auth.setBundle(rootBundle(901, 'local-root-a-session'))
  const scope = captureAssistantSettingsAuthScope()
  const reads = deferredOptionReads()
  const preflight = getLatestAssistantToolPolicy(scope)
  const rejection = assert.rejects(preflight, (error) => isCancel(error))
  await waitForReads(reads, 1)

  useAuthStore
    .getState()
    .auth.setBundle(rootBundle(902, 'local-root-b-session'))
  reads[0].finish(remotePolicy)
  await rejection
  assert.equal(useAuthStore.getState().auth.user?.id, 902)
  assert.equal(
    useAuthStore.getState().auth.session?.sid,
    'local-root-b-session'
  )
  assert.equal(reads.filter((read) => read.config.method === 'post').length, 0)
})

test('switching administrator after a fresh read cancels the old scoped policy write before transport', async () => {
  useAuthStore
    .getState()
    .auth.setBundle(rootBundle(901, 'local-root-a-session'))
  const scope = captureAssistantSettingsAuthScope()
  const reads = deferredOptionReads()
  const preflight = getLatestAssistantToolPolicy(scope)
  await waitForReads(reads, 1)
  reads[0].finish(DEFAULT_ASSISTANT_TOOL_POLICY)
  const baseline = await preflight

  useAuthStore
    .getState()
    .auth.setBundle(rootBundle(902, 'local-root-b-session'))
  await assert.rejects(
    updateAssistantSystemOptions(
      {
        values: {
          AssistantToolPolicy:
            '{"version":1,"groups":{},"tools":{"search_web":false}}',
        },
        expectedValues: { AssistantToolPolicy: baseline },
        authScope: scope,
      },
      { silent: true }
    ),
    (error) => isCancel(error)
  )
  assert.equal(
    reads.length,
    1,
    'a stale scoped POST must never reach the adapter'
  )
  assert.equal(reads.filter((read) => read.config.method === 'post').length, 0)
  assert.equal(useAuthStore.getState().auth.user?.id, 902)
})
