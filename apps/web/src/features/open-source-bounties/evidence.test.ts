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
import { describe, test } from 'node:test'

import { getBountyDisputeEvidenceComparison, type BountyDispute } from './types'

function disputeFixture(overrides: Partial<BountyDispute> = {}): BountyDispute {
  return {
    id: 1,
    challenge_id: 2,
    project_id: 3,
    opened_by_user_id: 4,
    against_user_id: 5,
    reason: 'merged_but_unpaid',
    statement: 'The merged fix remains unpaid.',
    project_title_snapshot: 'Parser fix',
    repository_url_snapshot: 'https://github.com/example/project',
    project_rules_snapshot: 'Fix the reproducible parser defect.',
    project_escrow_quota_snapshot: 1_000_000,
    challenge_status_snapshot: 'submitted',
    issue_url_snapshot: 'https://github.com/example/project/issues/1',
    pull_request_url_snapshot: 'https://github.com/example/project/pull/2',
    submission_note_snapshot: 'Verified locally.',
    review_note_snapshot: '',
    reward_quota_snapshot: 1_000_000,
    tip_quota_snapshot: 0,
    owner_rating_score_snapshot: 4,
    owner_rating_comment_snapshot: 'Good fix.',
    contributor_rating_score_snapshot: 5,
    contributor_rating_comment_snapshot: 'Clear requirements.',
    status: 'open',
    resolution: '',
    resolved_by_user_id: 0,
    created_at: 1,
    updated_at: 1,
    resolved_at: 0,
    project_title: 'Parser fix',
    repository_url: 'https://github.com/example/project',
    project_rules: 'Fix the reproducible parser defect.',
    current_project_escrow_quota: 1_000_000,
    challenge_status: 'submitted',
    issue_url: 'https://github.com/example/project/issues/1',
    pull_request_url: 'https://github.com/example/project/pull/2',
    submission_note: 'Verified locally.',
    review_note: '',
    reward_quota: 1_000_000,
    tip_quota: 0,
    owner_rating_score: 4,
    owner_rating_comment: 'Good fix.',
    contributor_rating_score: 5,
    contributor_rating_comment: 'Clear requirements.',
    owner_username: 'publisher',
    participant_username: 'contributor',
    opened_by_username: 'contributor',
    against_username: 'publisher',
    live_evidence_changed: false,
    ...overrides,
  }
}

describe('bounty dispute evidence comparison', () => {
  test('distinguishes post-filing tip and mutual rating changes', () => {
    const comparison = getBountyDisputeEvidenceComparison(
      disputeFixture({
        tip_quota: 250_000,
        owner_rating_score: 2,
        owner_rating_comment: 'Changed after filing.',
        contributor_rating_score: 1,
        contributor_rating_comment: 'Payment still missing.',
        live_evidence_changed: true,
      })
    )

    assert.equal(comparison.showCurrentValues, true)
    assert.deepEqual(comparison.changedFields, [
      'tipQuota',
      'ownerRating',
      'contributorRating',
    ])
  })

  test('does not request duplicate current evidence when values are unchanged', () => {
    const comparison = getBountyDisputeEvidenceComparison(
      disputeFixture({ live_evidence_changed: true })
    )

    assert.equal(comparison.showCurrentValues, false)
    assert.deepEqual(comparison.changedFields, [])
  })
})

test('general delivery snapshots remain distinct from changed live evidence', () => {
  const dispute = disputeFixture({
    delivery_url_snapshot: 'https://example.com/original',
    delivery_url: 'https://example.com/changed',
    live_evidence_changed: true,
  })
  const comparison = getBountyDisputeEvidenceComparison(dispute)
  assert.deepEqual(comparison.changedFields, ['deliveryUrl'])
  assert.equal(comparison.showCurrentValues, true)
})
