/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'

import type { InternalAxiosRequestConfig } from 'axios'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { HoneypotFailure, saveReviewedHoneypotPolicies } from './api'
import {
  capabilitiesSchema,
  configSchema,
  DEFAULT_LIMITS,
  hashHoneypotConfig,
  isUpstreamAllowedByCapabilities,
  policiesSchema,
  upstreamOrigin,
} from './contract'

const legacy = {
  client_protocols: ['openai_chat', 'openai_responses'],
  upstream_protocols: [
    'openai_chat',
    'openai_responses',
    'anthropic_messages',
    'gemini',
  ],
  modes: ['fixed_text', 'custom_upstream'],
  numeric_loopback_only: true,
  credential_references: ['sink'],
}
const caps = {
  ...legacy,
  numeric_loopback_only: false,
  external_https: true,
  local_http: false,
  allowed_origins: { sink: ['https://example.test'] },
}
const target = {
  base_url: 'https://example.test/v1',
  upstream_protocol: 'openai_chat' as const,
  model: 'public',
  credential_reference: 'sink',
}
const custom = (url = target.base_url, alias = 'sink') => ({
  version: 1 as const,
  policies: [
    {
      id: 'https',
      name: 'HTTPS',
      mode: 'custom_upstream' as const,
      public_models: ['public'],
      clients: {
        openai_chat: { ...target, base_url: url, credential_reference: alias },
        openai_responses: {
          ...target,
          base_url: url,
          credential_reference: alias,
          upstream_protocol: 'gemini' as const,
        },
      },
      limits: DEFAULT_LIMITS,
    },
  ],
})
const snapshot = (
  capabilities: unknown = caps,
  config: unknown = custom(),
  revision = 4
) => policiesSchema.parse({ version: 1, revision, config, capabilities })
const fixed = {
  version: 1 as const,
  policies: [
    {
      id: 'fixed',
      name: 'Fixed',
      mode: 'fixed_text' as const,
      public_models: ['public'],
      fixed_text: 'Reviewed',
      clients: {},
      limits: DEFAULT_LIMITS,
    },
  ],
}

test('exact five-key legacy and complete eight-key Core capabilities both parse', () => {
  snapshot(legacy, fixed)
  snapshot(
    {
      ...legacy,
      external_https: false,
      local_http: true,
      allowed_origins: null,
    },
    fixed
  )
  snapshot()
})
test('partial new capabilities never fall back to legacy, including fixed policies', () => {
  for (const key of Object.keys(caps)) {
    const partial: Record<string, unknown> = { ...caps }
    delete partial[key]
    assert.equal(
      policiesSchema.safeParse({
        version: 1,
        revision: 4,
        config: fixed,
        capabilities: partial,
      }).success,
      false,
      key
    )
  }
  for (const extra of [{ unexpected: true }, { password: 'forbidden' }]) {
    assert.equal(
      capabilitiesSchema.safeParse({ ...caps, ...extra }).success,
      false
    )
  }
})
test('the full host alias registry, protocol sets and canonical origins are checked', () => {
  for (const patch of [
    { credential_references: ['sink', 'sink'] },
    { credential_references: ['other'] },
    { upstream_protocols: ['openai_chat'] },
    { modes: ['fixed_text', 'fixed_text'] },
    { allowed_origins: { sink: ['https://EXAMPLE.test:443/'] } },
    { allowed_origins: { sink: ['https://example.test/path'] } },
    { allowed_origins: { sink: ['https://example.test?'] } },
    {
      allowed_origins: {
        sink: ['https://example.test', 'https://example.test'],
      },
    },
    { numeric_loopback_only: true },
    { external_https: false },
    { allowed_origins: { sink: ['http://127.0.0.1:8080'] } },
  ]) {
    assert.equal(
      capabilitiesSchema.safeParse({ ...caps, ...patch }).success,
      false,
      JSON.stringify(patch)
    )
  }
})
test('HTTPS matches only the same loaded alias canonical origin, without DNS from the browser', () => {
  const parsed = snapshot().capabilities
  assert.equal(
    isUpstreamAllowedByCapabilities(
      { ...target, base_url: 'https://EXAMPLE.TEST:443/custom/v1' },
      parsed
    ),
    true
  )
  for (const url of [
    'https://example.test:8443/v1',
    'https://example.test.attacker.test/v1',
    'http://example.test/v1',
    'https://other.test/v1',
  ]) {
    assert.equal(
      isUpstreamAllowedByCapabilities({ ...target, base_url: url }, parsed),
      false,
      url
    )
  }
  assert.equal(
    isUpstreamAllowedByCapabilities(
      { ...target, credential_reference: 'other' },
      parsed
    ),
    false
  )
})
test('numeric local HTTP and HTTPS each need the local flag and that exact alias origin', () => {
  const local = snapshot(
    {
      ...caps,
      local_http: true,
      allowed_origins: { sink: ['http://127.0.0.1:8080', 'https://[::1]'] },
    },
    fixed
  ).capabilities
  assert.equal(
    isUpstreamAllowedByCapabilities(
      { ...target, base_url: 'http://127.0.0.1:8080/v1' },
      local
    ),
    true
  )
  assert.equal(
    isUpstreamAllowedByCapabilities(
      { ...target, base_url: 'https://[0:0:0:0:0:0:0:1]:443/v1' },
      local
    ),
    true
  )
  for (const url of [
    'http://localhost/v1',
    'http://10.0.0.1/v1',
    'http://[::1]/v1',
    'https://127.0.0.1/v1',
  ]) {
    assert.equal(
      isUpstreamAllowedByCapabilities({ ...target, base_url: url }, local),
      false,
      url
    )
  }
})
test('ambiguous URLs and controls cannot be new grants, while stored Unicode routes remain repairable', () => {
  for (const url of [
    'https://127.1/v1',
    'https://example.test:0/v1',
    'https://example.test/v1?',
    'https://example.test/v1#',
    'https://user@example.test/v1',
    'https://example.test/\\path',
    'https://example.test/中文',
    'https://example.test/\u0085',
    'https://example.test/v1@part',
  ]) {
    assert.equal(upstreamOrigin(url, true), null, url)
  }
  const before = custom('https://旧.example.test/中文@path', 'removed-alias')
  assert.deepEqual(snapshot(caps, before).config, before)
  assert.equal(configSchema.safeParse(before).success, false)
  assert.equal(configSchema.safeParse(fixed).success, true)
})

const adapter = api.defaults.adapter,
  previous = useAuthStore.getState().auth
const scope = { userId: 901, sessionId: 'https-unit-session' }
function login() {
  useAuthStore.getState().auth.setBundle({
    access_token: 'synthetic-unit-only',
    token_type: 'Bearer',
    access_expires_at: 2000000000,
    user: { id: 901, username: 'synthetic-root', role: 100 },
    session: {
      sid: scope.sessionId,
      current: true,
      login_method: 'password',
      ip: '127.0.0.1',
      user_agent: 'synthetic-unit',
      created_at: 1,
      last_active_at: 1,
      expires_at: 2000000000,
    },
  })
}
afterEach(() => {
  api.defaults.adapter = adapter
  useAuthStore.setState({ auth: previous })
})
const request = {
  operation_id: 'https-unit-operation',
  reason: 'Reviewed',
  expected_revision: 4,
  config: custom(),
}
function reply(config: InternalAxiosRequestConfig, data: unknown) {
  return { config, data, status: 200, statusText: 'OK', headers: {} }
}
test('a reviewed save rechecks current capabilities and revision, then sends one hash-verified PUT', async () => {
  login()
  const reviewed = snapshot(),
    calls: string[] = []
  api.defaults.adapter = async (config) => {
    calls.push(config.method ?? '')
    assert.deepEqual(config.authScope, scope)
    return reply(
      config,
      config.method === 'get'
        ? reviewed
        : {
            version: 1,
            status: 'applied',
            operation_id: request.operation_id,
            kind: 'policies',
            policy_revision: 5,
            users: [],
            applied_config_hash: await hashHoneypotConfig(request.config),
          }
    )
  }
  assert.equal(
    (await saveReviewedHoneypotPolicies(request, scope, reviewed))
      .policy_revision,
    5
  )
  assert.deepEqual(calls, ['get', 'put'])
})
test('changed capabilities or revision prevent PUT and require a new review', async () => {
  login()
  const reviewed = snapshot()
  for (const current of [
    snapshot(caps, custom(), 5),
    snapshot(
      { ...caps, allowed_origins: { sink: ['https://other.test'] } },
      custom()
    ),
  ]) {
    const calls: string[] = []
    api.defaults.adapter = async (config) => {
      calls.push(config.method ?? '')
      return reply(config, current)
    }
    await assert.rejects(
      saveReviewedHoneypotPolicies(request, scope, reviewed),
      (error) => error instanceof HoneypotFailure && error.kind === 'conflict'
    )
    assert.deepEqual(calls, ['get'])
  }
})
test('invalid after metadata is never dispatched, and incomplete eight-key reads are unavailable', async () => {
  login()
  let calls = 0
  api.defaults.adapter = async (config) => {
    calls++
    return reply(config, {
      ...snapshot(),
      capabilities: { ...caps, allowed_origins: undefined },
    })
  }
  await assert.rejects(
    saveReviewedHoneypotPolicies(
      { ...request, config: custom('https://other.test/v1') },
      scope,
      snapshot()
    ),
    (error) => error instanceof HoneypotFailure && error.kind === 'invalid'
  )
  assert.equal(calls, 0)
  await assert.rejects(
    saveReviewedHoneypotPolicies(request, scope, snapshot()),
    (error) => error instanceof HoneypotFailure && error.kind === 'unavailable'
  )
  assert.equal(calls, 1)
})
