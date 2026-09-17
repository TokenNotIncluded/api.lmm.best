/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { inspectReferralEvidence } from './referral-evidence.ts'
import { createReferralSubmission } from './referral-submission.ts'

for (const character of ['a', '证', 'é', '🙂', '𠀀']) {
  test(`evidence counts Unicode characters: ${character}`, () => {
    for (const count of [1, 250, 334, 999, 1000, 1001]) {
      const value = character.repeat(count)
      assert.deepEqual(inspectReferralEvidence(value), {
        value,
        length: count,
        valid: count <= 1000,
      })
    }
  })
}

test('normalization preserves evidence and counts combining code points', () => {
  const padded = ' \u0085\u00a0证据\u3000 '
  assert.equal(inspectReferralEvidence(padded).value, '证据')
  assert.equal(
    inspectReferralEvidence('\ufeffevidence').value,
    '\ufeffevidence'
  )
  assert.equal(inspectReferralEvidence('e\u0301'.repeat(500)).valid, true)
  assert.equal(inspectReferralEvidence('e\u0301'.repeat(501)).valid, false)
  assert.equal(
    inspectReferralEvidence('  已核实\n保留原始记录  ').value,
    '已核实\n保留原始记录'
  )
})

test('empty, NUL and lone surrogate evidence is rejected before submission', () => {
  for (const value of [
    '',
    ' \t\r\n',
    '\u0085\u00a0\u3000',
    'before\0after',
    '\ud800',
    '\udfff',
  ]) {
    assert.equal(inspectReferralEvidence(value).valid, false)
  }
})

test('invalid input cannot reserve a request; retries retain exact text', () => {
  const submission = createReferralSubmission<{
    request_id: string
    evidence: string
  }>()
  let identities = 0
  const submit = (value: string) => {
    const input = inspectReferralEvidence(value)
    if (!input.valid) return undefined
    return submission(() => ({
      request_id: `test-request-${++identities}`,
      evidence: input.value,
    }))
  }
  assert.equal(submit('证'.repeat(1001)), undefined)
  assert.equal(identities, 0)
  const accepted = submit('  ' + '证'.repeat(1000) + '  ')
  assert.equal(accepted?.evidence, '证'.repeat(1000))
  assert.equal(identities, 1)
  // A failed response must not cause the next click to send a new operation.
  assert.equal(submit('another attempt'), accepted)
  assert.equal(identities, 1)
  assert.equal(Object.isFrozen(accepted), true)
})
