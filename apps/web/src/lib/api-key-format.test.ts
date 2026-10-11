/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { formatApiKey, NATIVE_CREDENTIAL_PATTERN } from './api-key-format'

const native = `lmmk_${'a1'.repeat(32)}`

test('issued native and prefixed keys stay byte-identical, while bare legacy secrets gain sk-', () => {
  for (const [key, expected] of [
    [native, native],
    ['sk-legacy-secret', 'sk-legacy-secret'],
    ['legacy-secret', 'sk-legacy-secret'],
  ]) {
    assert.equal(formatApiKey(key), expected)
    assert.equal(formatApiKey(formatApiKey(key)), expected)
  }
})

test('native credential matching requires an entire Core-format value', () => {
  for (const prefix of ['lmmk_', 'lmms_', 'lmmi_']) {
    const secret = `${prefix}${'a1'.repeat(32)}`
    assert.deepEqual(`(${secret})`.match(NATIVE_CREDENTIAL_PATTERN), [secret])
    for (const ordinary of [
      `${prefix}guide`,
      `${secret.slice(0, -1)}`,
      `${secret}a`,
      `project_${secret}`,
      `${secret}-guide`,
      `${prefix}${'z'.repeat(64)}`,
    ]) {
      assert.equal(ordinary.match(NATIVE_CREDENTIAL_PATTERN), null)
    }
  }
})
