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
