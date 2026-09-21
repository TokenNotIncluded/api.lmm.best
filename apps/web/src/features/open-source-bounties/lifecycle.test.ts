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

import { getBountyLifecycleSummary } from './lifecycle'
import type { BountyProject } from './types'

function project(overrides: Partial<BountyProject> = {}): BountyProject {
  return {
    id: 1,
    owner_user_id: 2,
    owner_username: 'owner',
    repository_url: 'https://github.com/example/repository',
    title: 'Bounty',
    description: 'Description',
    rules: 'Rules',
    reward_quota: 1_000,
    net_reward_quota: 900,
    reward_slots: 10,
    escrow_quota: 9_000,
    platform_fee_rate_bps: 1_000,
    platform_fee_quota: 1_000,
    status: 'paused',
    created_at: 1,
    updated_at: 1,
    published_at: 1,
    closed_at: 0,
    archived_at: 0,
    active_challenge_count: 4,
    approved_challenge_count: 1,
    owner_rating_average: 0,
    owner_rating_count: 0,
    owner_thank_heart_count: 0,
    ...overrides,
  }
}

test('reports detailed lifecycle counts and close blockers', () => {
  const summary = getBountyLifecycleSummary(
    project({
      participant_count: 8,
      accepted_challenge_count: 1,
      submitted_challenge_count: 1,
      rejected_challenge_count: 3,
      withdrawn_challenge_count: 1,
      cancelled_challenge_count: 1,
      appealable_challenge_count: 2,
      appeal_window_ends_at: 2_000,
      open_dispute_count: 1,
    }),
    false,
    1_000
  )

  assert.equal(summary.participantCount, 8)
  assert.equal(summary.acceptedCount, 1)
  assert.equal(summary.submittedCount, 1)
  assert.equal(summary.appealableCount, 2)
  assert.equal(summary.openDisputeCount, 1)
  assert.equal(summary.closeBlocked, true)
  assert.equal(summary.hasUnknownActiveBlocker, false)
})

test('expires an appeal blocker at the backend-provided deadline', () => {
  const summary = getBountyLifecycleSummary(
    project({
      active_challenge_count: 0,
      accepted_challenge_count: 0,
      submitted_challenge_count: 0,
      appealable_challenge_count: 2,
      appeal_window_ends_at: 2_000,
      open_dispute_count: 0,
    }),
    false,
    2_000
  )

  assert.equal(summary.appealableCount, 0)
  assert.equal(summary.appealWindowEndsAt, 0)
  assert.equal(summary.closeBlocked, false)
})

test('keeps older API responses fail-closed when only active count exists', () => {
  const summary = getBountyLifecycleSummary(project())

  assert.equal(summary.participantCount, 5)
  assert.equal(summary.hasUnknownActiveBlocker, true)
  assert.equal(summary.closeBlocked, true)
})
