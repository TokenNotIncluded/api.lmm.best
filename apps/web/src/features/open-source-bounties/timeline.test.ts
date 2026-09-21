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
/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { bountyAvailableSlots, bountyTimeline } from './timeline'
import type { BountyChallenge } from './types'

test('keeps rejection before an overturned dispute and only marks actual payout', () => {
  const challenge = {
    accepted_at: 100,
    submitted_at: 200,
    rejected_at: 300,
    reviewed_at: 500,
    status: 'approved',
    paid_at: 500,
    dispute: { created_at: 400, resolved_at: 500, status: 'resolved_paid' },
  } as BountyChallenge
  assert.deepEqual(
    bountyTimeline(challenge).map((event) => [event.key, event.time]),
    [
      ['Accepted', 100],
      ['Submitted', 200],
      ['Rejected', 300],
      ['Dispute opened', 400],
      ['Resolved and paid', 500],
      ['Reward credited to API balance', 500],
    ]
  )
  assert.equal(
    bountyTimeline({ ...challenge, paid_at: 0 }).some(
      (event) => event.key === 'Reward credited to API balance'
    ),
    false
  )
})

test('does not invent development or withdrawal dates', () => {
  const events = bountyTimeline({
    accepted_at: 100,
    submitted_at: 0,
    reviewed_at: 0,
    paid_at: 0,
    status: 'withdrawn',
  } as BountyChallenge)
  assert.deepEqual(events, [{ key: 'Accepted', time: 100, evidence: false }])
})

test('reserved and paid slots are unavailable, including appeal reservations', () => {
  assert.equal(
    bountyAvailableSlots({
      reward_slots: 5,
      active_challenge_count: 3,
      approved_challenge_count: 1,
    }),
    1
  )
  assert.equal(
    bountyAvailableSlots({
      reward_slots: 1,
      active_challenge_count: 3,
      approved_challenge_count: 1,
    }),
    0
  )
})
