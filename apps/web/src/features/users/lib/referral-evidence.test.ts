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
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

import { validateReferralEvidence } from './referral-evidence'
import { createReferralSubmission } from './referral-submission'

type Fixture = {
  name: string
  text: string
  repeat: number
  bytes: number
  valid: boolean
}
const fixtures: Fixture[] = JSON.parse(
  readFileSync(
    new URL(
      '../../../../../../contracts/referral-evidence.json',
      import.meta.url
    ),
    'utf8'
  )
)

for (const fixture of fixtures) {
  test(`shared Go evidence contract: ${fixture.name}`, () => {
    const input = fixture.text.repeat(fixture.repeat)
    const result = validateReferralEvidence(input)
    assert.equal(result.bytes, fixture.bytes)
    assert.equal(result.valid, fixture.valid)
  })
}

test('Chinese and emoji overflow cannot become a retained financial request', () => {
  const submit = createReferralSubmission<{
    request_id: string
    evidence: string
  }>()
  for (const evidence of ['证'.repeat(334), '😀'.repeat(251), 'bad\0text']) {
    assert.throws(() => submit(() => ({ request_id: 'invalid', evidence })))
  }
  const valid = submit(() => ({ request_id: 'corrected', evidence: '审核完成' }))
  assert.equal(valid.request_id, 'corrected')
  assert.equal(valid.evidence, '审核完成')
})

test('normalization happens before retention and exact retries stay immutable', () => {
  const submit = createReferralSubmission<{
    request_id: string
    evidence: string
  }>()
  const first = submit(() => ({
    request_id: 'stable',
    evidence: '\u0085  审核完成 \u3000',
  }))
  assert.equal(first.evidence, '审核完成')
  assert.equal(Object.isFrozen(first), true)
  const retry = submit(() => ({ request_id: 'new', evidence: 'different' }))
  assert.equal(retry, first)
})

test('isolated UTF-16 surrogates are not silently replaced in audit evidence', () => {
  for (const value of ['\ud800', '\udfff', 'test\ud800end']) {
    assert.equal(validateReferralEvidence(value).valid, false)
  }
  assert.equal(validateReferralEvidence('😀').valid, true)
})

test('exact mixed-byte boundary is accepted without truncating the explanation', () => {
  const exact = '证'.repeat(333) + 'a'
  const result = validateReferralEvidence(` \t${exact}\n`)
  assert.equal(result.valid, true)
  assert.equal(result.bytes, 1000)
  assert.equal(result.value, exact)
  assert.equal(validateReferralEvidence(exact + 'a').valid, false)
})
