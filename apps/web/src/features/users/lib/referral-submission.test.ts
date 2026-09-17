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

import { createReferralSubmission } from './referral-submission'

type Payload = {
  request_id: string
  id: number
  evidence: string
  penalize_inviter: boolean
}
const payload = (request_id: string): Payload => ({
  request_id,
  id: 7,
  evidence: 'reviewed evidence',
  penalize_inviter: false,
})

test('network and application failures both retain the exact request', () => {
  const submission = createReferralSubmission<Payload>()
  const first = submission(() => payload('same-request'))
  for (const failure of ['network', 'application']) {
    try {
      if (failure === 'network') {
        throw new Error('connection lost after commit')
      }
      const response = { success: false, message: 'cache publication failed' }
      if (!response.success) throw new Error(response.message)
    } catch {
      const retry = submission(() => payload('must-not-be-created'))
      assert.equal(retry, first)
      assert.equal(retry.request_id, 'same-request')
    }
  }
})

test('later form edits and external mutations cannot change a retry', () => {
  const submission = createReferralSubmission<Payload>()
  const source = payload('stable')
  const first = submission(() => source)
  source.evidence = 'changed form'
  source.penalize_inviter = true
  assert.equal(first.evidence, 'reviewed evidence')
  assert.equal(first.penalize_inviter, false)
  assert.equal(Object.isFrozen(first), true)
  const retry = submission(() => source)
  assert.equal(retry, first)
})

test('repeated submissions allocate the request ID only once', () => {
  let allocations = 0
  const submission = createReferralSubmission<Payload>()
  for (let index = 0; index < 10; index++) {
    const request = submission(() => payload(String(++allocations)))
    assert.equal(request.request_id, '1')
  }
  assert.equal(allocations, 1)
})

test('creation failure does not retain an invalid request', () => {
  const submission = createReferralSubmission<Payload>()
  const blank = () => payload('  ')
  assert.throws(() => submission(blank))
  const unavailable = (): Payload => {
    throw new Error('UUID unavailable')
  }
  assert.throws(() => submission(unavailable))
  const valid = submission(() => payload('valid'))
  assert.equal(valid.request_id, 'valid')
})

test('separate dialogs do not share retained evidence', () => {
  const first = createReferralSubmission<Payload>()
  const second = createReferralSubmission<Payload>()
  const one = first(() => payload('one'))
  const two = second(() => payload('two'))
  assert.equal(one.request_id, 'one')
  assert.equal(two.request_id, 'two')
})
