/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  MODERATION_CATEGORIES,
  parseModerationGroupPolicies,
} from './moderation-config'

test('preserves explicit disabled, warning and strict group policies without inventing defaults', () => {
  const policies = parseModerationGroupPolicies(
    JSON.stringify({
      default: { mode: 'off', category_fines_usd: {} },
      premium: { mode: 'tolerant', category_fines_usd: {} },
      restricted: {
        mode: 'strict',
        category_fines_usd: { 'sexual/minors': 0.015, harassment: 0 },
      },
    })
  )
  assert.ok(policies)
  assert.equal(policies.default.mode, 'off')
  assert.equal(policies.premium.mode, 'tolerant')
  assert.equal(policies.restricted.category_fines_usd['sexual/minors'], 0.015)
  assert.equal(policies.missing, undefined)
  assert.deepEqual(Object.keys(parseModerationGroupPolicies('{}') ?? {}), [])
})

test('rejects malformed policies, wildcard scope and unsupported category amounts', () => {
  for (const source of [
    '',
    '[]',
    'null',
    JSON.stringify({ '*': { mode: 'strict', category_fines_usd: {} } }),
    JSON.stringify({
      ' default': { mode: 'tolerant', category_fines_usd: {} },
    }),
    JSON.stringify({ default: { mode: 'block', category_fines_usd: {} } }),
    JSON.stringify({
      default: { mode: 'strict', category_fines_usd: { unknown: 1 } },
    }),
    JSON.stringify({
      default: { mode: 'strict', category_fines_usd: { hate: -1 } },
    }),
    JSON.stringify({
      default: { mode: 'strict', category_fines_usd: { hate: 1000.000001 } },
    }),
    JSON.stringify({
      default: { mode: 'strict', category_fines_usd: { hate: 0.0000001 } },
    }),
    JSON.stringify({
      default: { mode: 'strict', category_fines_usd: { hate: '0.1' } },
    }),
  ]) {
    assert.equal(parseModerationGroupPolicies(source), null, source)
  }
  assert.equal(MODERATION_CATEGORIES.length, 13)
})
