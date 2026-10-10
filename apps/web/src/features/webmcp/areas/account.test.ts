/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import type { WebMcpRouter } from '../tool-kit'
import { accountTools } from './account'

const originalGet = api.get
const originalAuth = useAuthStore.getState().auth
const calls: Array<{ to: string }> = []
const router: WebMcpRouter = {
  navigate: async (options) => {
    calls.push(options)
  },
  subscribe: () => () => undefined,
}
const tools = accountTools({ router })
const options = () => ({ signal: new AbortController().signal })
const find = (name: string) => {
  const tool = tools.find((entry) => entry.name === name)
  assert.ok(tool)
  return tool
}
function signIn(role: number = ROLE.USER) {
  useAuthStore.setState({
    auth: { ...originalAuth, user: { id: 1, role } } as never,
  })
}
afterEach(() => {
  api.get = originalGet
  useAuthStore.setState({ auth: originalAuth })
  calls.length = 0
})

test('all account tools require sign-in and expose no mutations', async () => {
  useAuthStore.setState({ auth: { ...originalAuth, user: null } })
  assert.equal(new Set(tools.map((tool) => tool.name)).size, tools.length)
  for (const tool of tools) {
    assert.match(tool.name, /^lmm_account_[a-z0-9_]+$/)
    assert.equal(tool.annotations?.readOnlyHint, true)
    await assert.rejects(tool.execute({}, options()), /Sign in first/)
  }
  assert.equal(calls.length, 0)
})

test('subscription administration and root reset navigation enforce roles', async () => {
  signIn()
  for (const name of [
    'lmm_account_open_subscriptions',
    'lmm_account_open_subscription_reset',
    'lmm_account_list_subscription_plans',
  ]) {
    await assert.rejects(
      find(name).execute({}, options()),
      /administrator account/
    )
  }
  signIn(ROLE.ADMIN)
  await assert.rejects(
    find('lmm_account_open_subscription_reset').execute({}, options()),
    /root administrator/
  )
  signIn(ROLE.SUPER_ADMIN)
  await find('lmm_account_open_subscription_reset').execute({}, options())
  assert.deepEqual(calls, [{ to: '/subscriptions/reset' }])
})

test('profile and share responses never expose credentials or share tokens', async () => {
  signIn()
  api.get = (async () => ({
    data: {
      success: true,
      data: {
        id: 1,
        username: 'Alice',
        enabled: true,
        quota: 200,
        access_token: 'secret-access',
        password: 'secret-password',
        token: 'secret-share',
        url: 'https://example.test/secret-share.svg',
        setting: '{"webhook_secret":"secret-webhook"}',
      },
    },
  })) as typeof api.get
  const profile = await find('lmm_account_get_profile').execute({}, options())
  const share = await find('lmm_account_get_share_status').execute(
    {},
    options()
  )
  assert.equal((profile as { quota: number }).quota, 200)
  assert.deepEqual(share, { enabled: true, path: '/profile/share' })
  assert.doesNotMatch(
    JSON.stringify([profile, share]),
    /secret-|password|access_token|webhook/
  )
})

test('log tools only request self endpoints and strip raw content', async () => {
  signIn()
  const paths: string[] = []
  api.get = (async (path: string) => {
    paths.push(path)
    return {
      data: {
        success: true,
        data: {
          total: 1,
          items: [
            {
              id: 4,
              model_name: 'test-model',
              prompt_tokens: 12,
              completion_tokens: 4,
              quota: 3,
              other: 'secret-detail',
              content: 'secret-prompt',
              key: 'secret-key',
              token_name: 'secret-token',
            },
          ],
        },
      },
    }
  }) as typeof api.get
  for (const category of ['common', 'drawing', 'task']) {
    const data = await find('lmm_account_list_usage_logs').execute(
      { category },
      options()
    )
    assert.doesNotMatch(JSON.stringify(data), /secret-/)
  }
  assert.deepEqual(paths, ['/api/log/self', '/api/mj/self', '/api/task/self'])
})

test('per-model usage includes every bounded window without overlap', async () => {
  signIn()
  const ranges: Array<{ start_timestamp: number; end_timestamp: number }> = []
  api.get = (async (
    _path: string,
    config: { params: { start_timestamp: number; end_timestamp: number } }
  ) => {
    ranges.push(config.params)
    return {
      data: {
        success: true,
        data: [
          {
            model_name: 'free',
            created_at: config.params.start_timestamp,
            token_used: 100,
            count: 2,
            quota: 0,
          },
          {
            model_name: 'paid',
            created_at: config.params.start_timestamp,
            token_used: 10,
            count: 1,
            quota: 5,
          },
        ],
      },
    }
  }) as typeof api.get
  const result = (await find('lmm_account_get_model_usage').execute(
    { range: '365d' },
    options()
  )) as {
    totals: { tokens: number; requests: number }
    models: Array<{ modelName: string; share: number }>
  }
  assert.ok(ranges.length > 1)
  for (let i = 0; i < ranges.length; i += 1) {
    assert.ok(ranges[i].end_timestamp - ranges[i].start_timestamp <= 28 * 86400)
    if (i > 0) {
      assert.equal(ranges[i].start_timestamp, ranges[i - 1].end_timestamp + 1)
    }
  }
  assert.equal(result.totals.tokens, ranges.length * 110)
  assert.equal(result.totals.requests, ranges.length * 3)
  assert.equal(result.models.find((row) => row.modelName === 'free')?.share, 0)
  assert.equal(result.models.find((row) => row.modelName === 'paid')?.share, 1)
})

test('invalid input and aborted requests never access the API', async () => {
  signIn()
  let requested = false
  api.get = (async () => {
    requested = true
    throw new Error('unexpected')
  }) as typeof api.get
  await assert.rejects(
    find('lmm_account_list_usage_logs').execute(
      { page_size: 10000 },
      options()
    ),
    /page_size/
  )
  await assert.rejects(
    find('lmm_account_get_model_usage').execute({ range: 'all' }, options()),
    /range/
  )
  const controller = new AbortController()
  controller.abort()
  await assert.rejects(
    find('lmm_account_get_profile').execute({}, { signal: controller.signal })
  )
  assert.equal(requested, false)
})
