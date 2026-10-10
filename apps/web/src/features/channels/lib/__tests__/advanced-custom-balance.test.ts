/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import type {
  AdvancedCustomBalanceConfig,
  AdvancedCustomConfig,
  AdvancedCustomRoute,
} from '../../types'
import {
  ADVANCED_CUSTOM_BALANCE_PATH,
  ADVANCED_CUSTOM_TEMPLATE_OPTIONS,
  getAdvancedCustomTemplateConfig,
  normalizeAdvancedCustomConfig,
  parseAdvancedCustomConfig,
  stringifyAdvancedCustomConfig,
  validateAdvancedCustomConfig,
} from '../advanced-custom'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  channelFormSchema,
  transformFormDataToCreatePayload,
  transformFormDataToUpdatePayload,
} from '../channel-form'

function route(balance?: AdvancedCustomBalanceConfig): AdvancedCustomRoute {
  return {
    incoming_path: ADVANCED_CUSTOM_BALANCE_PATH,
    upstream_path: 'https://account.example.test/balance',
    converter: 'none',
    ...(balance !== undefined ? { balance } : {}),
  }
}
function config(balance?: AdvancedCustomBalanceConfig): AdvancedCustomConfig {
  return { advanced_routes: [route(balance)] }
}

test('every existing Advanced Custom template keeps its inference routes and round-trips without balance settings', () => {
  for (const option of ADVANCED_CUSTOM_TEMPLATE_OPTIONS) {
    const template = getAdvancedCustomTemplateConfig(option.value)
    assert.equal(validateAdvancedCustomConfig(template), null, option.value)
    assert.deepEqual(
      parseAdvancedCustomConfig(stringifyAdvancedCustomConfig(template)),
      template
    )
    assert.ok(
      template.advanced_routes?.every((item) => item.balance === undefined),
      option.value
    )
  }
})

test('legacy balance routes stay GET by omission and keep their saved JSON contract', () => {
  const legacy = config()
  assert.equal(validateAdvancedCustomConfig(legacy), null)
  assert.deepEqual(
    parseAdvancedCustomConfig(stringifyAdvancedCustomConfig(legacy)),
    legacy
  )
  assert.equal(
    Object.hasOwn(
      normalizeAdvancedCustomConfig(legacy).advanced_routes?.[0] ?? {},
      'balance'
    ),
    false
  )
  assert.equal(
    validateAdvancedCustomConfig(config({ method: 'POST' })),
    null,
    'POST without a body is valid'
  )
})

test('POST body, JSON pointer and scale survive create and update payloads without changing type', () => {
  const balance: AdvancedCustomBalanceConfig = {
    method: 'POST',
    body_template: '{"api_key":"{api_key}","currency":"USD"}',
    json_pointer: '/a~1b/m~0n/0',
    scale: 0.01,
  }
  const desired = config(balance)
  const serialized = stringifyAdvancedCustomConfig(desired)
  assert.deepEqual(parseAdvancedCustomConfig(serialized), desired)
  const form = channelFormSchema.parse({
    ...CHANNEL_FORM_DEFAULT_VALUES,
    name: 'Custom balance',
    type: 58,
    key: 'test-key',
    models: 'test-model',
    advanced_custom: serialized,
  })
  for (const payload of [
    transformFormDataToCreatePayload(form).channel,
    transformFormDataToUpdatePayload(form, 7),
  ]) {
    assert.equal(payload.type, 58)
    assert.deepEqual(
      JSON.parse(payload.settings || '{}').advanced_custom,
      desired
    )
  }
})

test('invalid balance settings are rejected and never erased by normalization', () => {
  const invalid: unknown[] = [
    true,
    false,
    'POST',
    [],
    { method: 123 },
    { method: 'PUT' },
    { body_template: '{}' },
    { method: 'POST', body_template: '' },
    { method: 'POST', body_template: '{secret' },
    { method: 'POST', body_template: JSON.stringify('界'.repeat(6000)) },
    { json_pointer: '$.balance' },
    { json_pointer: '/bad~2escape' },
    { json_pointer: '/trailing~' },
    { json_pointer: `/${'x'.repeat(1024)}` },
    { json_pointer: '/x'.repeat(33) },
    { scale: 1 },
    { json_pointer: '/balance', scale: -1 },
    { json_pointer: '/balance', scale: 0 },
    { json_pointer: '/balance', scale: Infinity },
    { json_pointer: '/balance', scale: Number.NaN },
  ]
  for (const balance of invalid) {
    const desired = config(balance as AdvancedCustomBalanceConfig)
    const error = validateAdvancedCustomConfig(desired)
    assert.ok(error, JSON.stringify(balance))
    assert.equal(error.routeIndex, 0)
    assert.deepEqual(
      normalizeAdvancedCustomConfig(desired).advanced_routes?.[0]?.balance,
      balance
    )
  }
  assert.equal(
    validateAdvancedCustomConfig(
      config({ json_pointer: '/~01/0', scale: 0.01 })
    ),
    null
  )
})

test('balance fields cannot become inference configuration, duplicate balance routes or credential-bearing endpoints', () => {
  for (const changed of [
    { ...route({ method: 'POST' }), incoming_path: '/v1/chat/completions' },
    { ...route(), models: ['gpt-4o'] },
    { ...route(), converter: 'openai-to-gemini' },
    { ...route(), upstream_path: '/balance/{model}' },
    { ...route(), upstream_path: 'https://key:secret@example.test/balance' },
    {
      ...route(),
      upstream_path: 'https://account.example.test/balance#fragment',
    },
  ]) {
    assert.ok(
      validateAdvancedCustomConfig({
        advanced_routes: [changed as AdvancedCustomRoute],
      })
    )
  }
  assert.match(
    validateAdvancedCustomConfig({ advanced_routes: [route(), route()] })
      ?.message || '',
    /Only one balance route/
  )
})
