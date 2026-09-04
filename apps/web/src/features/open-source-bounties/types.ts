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
export type BountyProjectStatus =
  | 'draft'
  | 'published'
  | 'paused'
  | 'completed'
  | 'closed'

export type BountyChallengeStatus =
  | 'accepted'
  | 'submitted'
  | 'approved'
  | 'rejected'
  | 'withdrawn'
  | 'cancelled'

export type BountyDisputeReason =
  | 'merged_but_unpaid'
  | 'requirements_met_but_rejected'
  | 'misleading_requirements'
  | 'abusive_conduct'
  | 'other'

export type BountyDisputeStatus = 'open' | 'resolved_paid' | 'resolved_denied'

export interface BountyDispute {
  id: number
  challenge_id: number
  project_id: number
  opened_by_user_id: number
  against_user_id: number
  reason: BountyDisputeReason
  statement: string
  project_title_snapshot: string
  repository_url_snapshot: string
  project_rules_snapshot: string
  project_escrow_quota_snapshot: number
  challenge_status_snapshot: BountyChallengeStatus
  issue_url_snapshot: string
  pull_request_url_snapshot: string
  submission_note_snapshot: string
  review_note_snapshot: string
  reward_quota_snapshot: number
  tip_quota_snapshot: number
  owner_rating_score_snapshot: number
  owner_rating_comment_snapshot: string
  contributor_rating_score_snapshot: number
  contributor_rating_comment_snapshot: string
  status: BountyDisputeStatus
  resolution: string
  resolved_by_user_id: number
  created_at: number
  updated_at: number
  resolved_at: number
  project_title: string
  repository_url: string
  project_rules: string
  current_project_escrow_quota: number
  challenge_status: BountyChallengeStatus
  issue_url: string
  pull_request_url: string
  submission_note: string
  review_note: string
  reward_quota: number
  tip_quota: number
  owner_rating_score: number
  owner_rating_comment: string
  contributor_rating_score: number
  contributor_rating_comment: string
  owner_username: string
  participant_username: string
  opened_by_username: string
  against_username: string
  live_evidence_changed: boolean
}

export type BountyDisputeEvidenceField =
  | 'projectTitle'
  | 'repositoryUrl'
  | 'projectRules'
  | 'projectEscrowQuota'
  | 'challengeStatus'
  | 'issueUrl'
  | 'pullRequestUrl'
  | 'submissionNote'
  | 'reviewNote'
  | 'rewardQuota'
  | 'tipQuota'
  | 'ownerRating'
  | 'contributorRating'

export function getBountyDisputeEvidenceComparison(dispute: BountyDispute): {
  changedFields: BountyDisputeEvidenceField[]
  showCurrentValues: boolean
} {
  const changes: Array<[BountyDisputeEvidenceField, boolean]> = [
    ['projectTitle', dispute.project_title_snapshot !== dispute.project_title],
    [
      'repositoryUrl',
      dispute.repository_url_snapshot !== dispute.repository_url,
    ],
    ['projectRules', dispute.project_rules_snapshot !== dispute.project_rules],
    [
      'projectEscrowQuota',
      dispute.project_escrow_quota_snapshot !==
        dispute.current_project_escrow_quota,
    ],
    [
      'challengeStatus',
      dispute.challenge_status_snapshot !== dispute.challenge_status,
    ],
    ['issueUrl', dispute.issue_url_snapshot !== dispute.issue_url],
    [
      'pullRequestUrl',
      dispute.pull_request_url_snapshot !== dispute.pull_request_url,
    ],
    [
      'submissionNote',
      dispute.submission_note_snapshot !== dispute.submission_note,
    ],
    ['reviewNote', dispute.review_note_snapshot !== dispute.review_note],
    ['rewardQuota', dispute.reward_quota_snapshot !== dispute.reward_quota],
    ['tipQuota', dispute.tip_quota_snapshot !== dispute.tip_quota],
    [
      'ownerRating',
      dispute.owner_rating_score_snapshot !== dispute.owner_rating_score ||
        dispute.owner_rating_comment_snapshot !== dispute.owner_rating_comment,
    ],
    [
      'contributorRating',
      dispute.contributor_rating_score_snapshot !==
        dispute.contributor_rating_score ||
        dispute.contributor_rating_comment_snapshot !==
          dispute.contributor_rating_comment,
    ],
  ]

  const changedFields = changes
    .filter(([, changed]) => changed)
    .map(([field]) => field)

  return {
    changedFields,
    showCurrentValues:
      dispute.live_evidence_changed && changedFields.length > 0,
  }
}

export interface BountyChallenge {
  id: number
  project_id: number
  participant_user_id: number
  participant_username?: string
  github_handle: string
  status: BountyChallengeStatus
  issue_url: string
  pull_request_url: string
  submission_note: string
  review_note: string
  reward_quota: number
  tip_quota: number
  owner_rating_score: number
  owner_rating_comment: string
  owner_rated_at: number
  contributor_rating_score: number
  contributor_rating_comment: string
  contributor_rated_at: number
  participant_rating_average?: number
  participant_rating_count?: number
  owner_rating_average?: number
  owner_rating_count?: number
  accepted_at: number
  submitted_at: number
  reviewed_at: number
  paid_at: number
  project_title?: string
  repository_url?: string
  owner_username?: string
  dispute?: BountyDispute
}

export interface BountyProject {
  id: number
  owner_user_id: number
  owner_username: string
  repository_url: string
  title: string
  description: string
  rules: string
  reward_quota: number
  net_reward_quota: number
  reward_slots: number
  escrow_quota: number
  platform_fee_rate_bps: number
  platform_fee_quota: number
  status: BountyProjectStatus
  created_at: number
  updated_at: number
  published_at: number
  closed_at: number
  archived_at: number
  participant_count?: number
  active_challenge_count: number
  accepted_challenge_count?: number
  submitted_challenge_count?: number
  approved_challenge_count: number
  rejected_challenge_count?: number
  withdrawn_challenge_count?: number
  cancelled_challenge_count?: number
  appealable_challenge_count?: number
  appeal_window_ends_at?: number
  open_dispute_count?: number
  owner_rating_average: number
  owner_rating_count: number
  owner_thank_heart_count: number
  viewer_challenge?: BountyChallenge
}

export interface BountyTipNotification {
  id: number
  project_id: number
  challenge_id: number
  sender_user_id: number
  sender_username: string
  project_title: string
  quota: number
  note: string
  recipient_read_at: number
  thanked_at: number
  created_at: number
}

export type BountyNotificationKind =
  | 'tip_transfer'
  | 'reward_transfer'
  | 'dispute_reward_transfer'

export interface BountyNotification extends BountyTipNotification {
  kind: BountyNotificationKind
}

export interface BountyDraftInput {
  repository_url: string
  title: string
  description: string
  rules: string
  reward_quota: number
  reward_slots: number
}

export interface BountyProjectDetail {
  project: BountyProject
  challenges: BountyChallenge[]
  ledger: Array<{
    id: number
    project_id: number
    challenge_id: number
    user_id: number
    counterparty_user_id: number
    kind: string
    quota: number
    note: string
    created_at: number
  }>
}

export interface BountyFeeConfig {
  rate_percent: number
  rate_basis_points: number
}

export interface BountyMcpTokenStatus {
  configured: boolean
  token_hint: string
  created_at: number
  last_used_at: number
}

export interface BountyMcpConnection {
  status: BountyMcpTokenStatus
  endpoint: string
  protocol_version: string
  token?: string
}
