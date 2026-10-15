/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  configSchema,
  hashHoneypotConfig,
  DEFAULT_LIMITS,
  isNumericLoopbackURL,
  policiesSchema,
  previewMatchesRequest,
  previewRequestSchema,
  type HoneypotPreview,
  type HoneypotPreviewRequest,
} from './contract'

const config = () => ({
  version: 1 as const,
  policies: [
    {
      id: 'fixed',
      name: 'Fixed',
      mode: 'fixed_text' as const,
      public_models: ['public-model'],
      fixed_text: 'Example response',
      clients: {},
      limits: { ...DEFAULT_LIMITS },
    },
  ],
})
test('only numeric loopback HTTP endpoints are editable', () => {
  for (const url of [
    'http://127.0.0.1:5000/v1',
    'http://127.1.2.3/api',
    'http://[::1]:6000/v1',
  ]) {
    assert.equal(isNumericLoopbackURL(url), true, url)
  }
  for (const url of [
    'https://127.0.0.1/v1',
    'http://localhost/v1',
    'http://127.1/v1',
    'http://127.0.0.999/v1',
    'http://127.0.0.1:99999',
    'http://127.0.0.1/v1?token=x',
    'http://127.0.0.1/v1#x',
    'http://x@127.0.0.1/v1',
    'http://127.0.0.1\\@example.test',
    'http://10.0.0.1/v1',
    'http://[::ffff:127.0.0.1]/v1',
    'http://127.0.0.1/\n',
  ]) {
    assert.equal(isNumericLoopbackURL(url), false, url)
  }
})
test('policy quotas, UTF-8 budgets and exact shapes are enforced', () => {
  assert.equal(configSchema.safeParse(config()).success, true)
  const variants = [
    { limits: { ...DEFAULT_LIMITS, requests_per_minute_per_user: 101 } },
    { limits: { ...DEFAULT_LIMITS, max_concurrent_per_user: 9 } },
    { limits: { ...DEFAULT_LIMITS, max_output_bytes: 1 } },
    { limits: { ...DEFAULT_LIMITS, total_timeout_seconds: 241 } },
    { fixed_text: '中'.repeat(6000) },
    { public_models: [] },
    { public_models: ['same', 'same'] },
    { public_models: ['bad\nmodel'] },
    { public_models: ['中'.repeat(100)] },
    { password: 'forbidden' },
  ]
  for (const patch of variants) {
    const value = config()
    Object.assign(value.policies[0], patch)
    assert.equal(
      configSchema.safeParse(value).success,
      false,
      JSON.stringify(patch)
    )
  }
  const duplicated = config()
  duplicated.policies.push(duplicated.policies[0])
  assert.equal(configSchema.safeParse(duplicated).success, false)
})
test('two client protocols can convert to four advertised upstream protocols, with aliases only', () => {
  const target = {
    base_url: 'http://127.0.0.1:8080/v1',
    upstream_protocol: 'anthropic_messages',
    model: 'upstream-model',
    credential_reference: 'loaded-alias',
  }
  const custom = {
    version: 1,
    policies: [
      {
        id: 'custom',
        name: 'Custom',
        mode: 'custom_upstream',
        public_models: ['public-model'],
        clients: {
          openai_chat: target,
          openai_responses: { ...target, upstream_protocol: 'gemini' },
        },
        limits: DEFAULT_LIMITS,
      },
    ],
  }
  assert.equal(configSchema.safeParse(custom).success, true)
  assert.equal(
    configSchema.safeParse({
      ...custom,
      policies: [
        {
          ...custom.policies[0],
          clients: {
            ...custom.policies[0].clients,
            anthropic_messages: target,
          },
        },
      ],
    }).success,
    false
  )
  assert.equal(
    configSchema.safeParse({
      ...custom,
      policies: [
        {
          ...custom.policies[0],
          clients: {
            ...custom.policies[0].clients,
            openai_chat: { ...target, api_key: 'private' },
          },
        },
      ],
    }).success,
    false
  )
})
test('legacy wrappers and unsupported capabilities fail closed', () => {
  const snapshot = {
    version: 1,
    revision: 0,
    config: config(),
    capabilities: {
      client_protocols: ['openai_chat', 'openai_responses'],
      upstream_protocols: [
        'openai_chat',
        'openai_responses',
        'anthropic_messages',
        'gemini',
      ],
      modes: ['fixed_text', 'custom_upstream'],
      numeric_loopback_only: true,
      credential_references: [],
    },
  }
  assert.equal(policiesSchema.safeParse(snapshot).success, true)
  assert.equal(
    policiesSchema.safeParse({ success: true, data: snapshot }).success,
    false
  )
  assert.equal(
    policiesSchema.safeParse({
      ...snapshot,
      capabilities: { ...snapshot.capabilities, numeric_loopback_only: false },
    }).success,
    false
  )
  assert.equal(
    policiesSchema.safeParse({
      ...snapshot,
      capabilities: {
        ...snapshot.capabilities,
        client_protocols: ['anthropic_messages'],
      },
    }).success,
    false
  )
})
test('preview must bind the exact operation, action, revisions, IDs and post-state', () => {
  const request: HoneypotPreviewRequest = {
    operation_id: 'op-preview-1',
    reason: 'Reviewed synthetic incident',
    action: 'mark',
    policy_id: 'fixed',
    expected_policy_revision: 4,
    targets: [{ user_id: '7', expected_revision: 0 }],
  }
  const preview: HoneypotPreview = {
    version: 2,
    preview_id: 'preview-1',
    preview_hash: 'a'.repeat(64),
    expires_at: 2000000000,
    operation_id: request.operation_id,
    action: 'mark',
    policy_id: 'fixed',
    policy_revision: 4,
    targets: [
      {
        user_id: '7',
        username: 'example',
        active: true,
        expected_revision: 0,
        before: { marked: false, policy_id: null },
        after: { marked: true, policy_id: 'fixed', active: true },
      },
    ],
  }
  assert.equal(previewMatchesRequest(preview, request), true)
  for (const patch of [
    { operation_id: 'other-op' },
    { policy_revision: 5 },
    { targets: [] },
    { targets: [...preview.targets, ...preview.targets] },
    { targets: [{ ...preview.targets[0], user_id: '8' }] },
    { targets: [{ ...preview.targets[0], expected_revision: 1 }] },
    {
      targets: [
        {
          ...preview.targets[0],
          after: { ...preview.targets[0].after, active: false },
        },
      ],
    },
  ]) {
    assert.equal(
      previewMatchesRequest({ ...preview, ...patch }, request),
      false
    )
  }
  assert.equal(
    previewRequestSchema.safeParse({
      ...request,
      targets: [...request.targets, ...request.targets],
    }).success,
    false
  )
  assert.equal(
    previewRequestSchema.safeParse({ ...request, action: 'unmark' }).success,
    false
  )
})

test('canonical UTF-8 config hash matches the frozen cross-language contract vector', async () => {
  const config = configSchema.parse(
    JSON.parse(
      '{"version":1,"policies":[{"id":"unicode-policy","name":"中文 é","mode":"fixed_text","fixed_text":"固定文本 é","public_models":["模型一","model-2"],"clients":{},"limits":{"requests_per_minute_per_user":1,"requests_per_minute_global":2,"max_concurrent_per_user":1,"max_concurrent_global":2,"max_input_bytes":128,"max_output_bytes":128,"max_output_tokens":16,"output_bytes_per_second":0,"total_timeout_seconds":10}}]}'
    )
  )
  assert.equal(
    await hashHoneypotConfig(config),
    '28830aa7a6a096a0d62028706ec2c0d6bbec9b4c537440f350fc3419da5024ce'
  )
  assert.equal(
    await hashHoneypotConfig({ policies: config.policies, version: 1 }),
    '28830aa7a6a096a0d62028706ec2c0d6bbec9b4c537440f350fc3419da5024ce'
  )
})
