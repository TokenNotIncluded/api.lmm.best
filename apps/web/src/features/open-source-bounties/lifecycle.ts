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
import type { BountyProject } from './types'

export interface BountyLifecycleSummary {
  participantCount: number
  acceptedCount: number
  submittedCount: number
  approvedCount: number
  rejectedCount: number
  withdrawnCount: number
  cancelledCount: number
  appealableCount: number
  appealWindowEndsAt: number
  openDisputeCount: number
  hasUnknownActiveBlocker: boolean
  closeBlocked: boolean
}

function nonNegative(value: number | undefined) {
  return Math.max(0, value ?? 0)
}

export function getBountyLifecycleSummary(
  project: BountyProject,
  hasOpenDisputeFallback = false,
  nowSeconds = Math.floor(Date.now() / 1000)
): BountyLifecycleSummary {
  const acceptedCount = nonNegative(project.accepted_challenge_count)
  const submittedCount = nonNegative(project.submitted_challenge_count)
  const approvedCount = nonNegative(project.approved_challenge_count)
  const rejectedCount = nonNegative(project.rejected_challenge_count)
  const withdrawnCount = nonNegative(project.withdrawn_challenge_count)
  const cancelledCount = nonNegative(project.cancelled_challenge_count)
  const appealWindowEndsAt = nonNegative(project.appeal_window_ends_at)
  const reportedAppealableCount = nonNegative(
    project.appealable_challenge_count
  )
  const appealableCount =
    appealWindowEndsAt > 0 && appealWindowEndsAt <= nowSeconds
      ? 0
      : reportedAppealableCount
  const openDisputeCount = Math.max(
    nonNegative(project.open_dispute_count),
    hasOpenDisputeFallback ? 1 : 0
  )
  const hasDetailedBreakdown = [
    project.accepted_challenge_count,
    project.submitted_challenge_count,
    project.appealable_challenge_count,
    project.open_dispute_count,
  ].some((value) => value !== undefined)
  const hasUnknownActiveBlocker =
    !hasDetailedBreakdown && nonNegative(project.active_challenge_count) > 0
  const knownParticipants =
    acceptedCount +
    submittedCount +
    approvedCount +
    rejectedCount +
    withdrawnCount +
    cancelledCount
  const participantCount = Math.max(
    nonNegative(project.participant_count),
    knownParticipants,
    nonNegative(project.active_challenge_count) + approvedCount
  )

  return {
    participantCount,
    acceptedCount,
    submittedCount,
    approvedCount,
    rejectedCount,
    withdrawnCount,
    cancelledCount,
    appealableCount,
    appealWindowEndsAt: appealableCount > 0 ? appealWindowEndsAt : 0,
    openDisputeCount,
    hasUnknownActiveBlocker,
    closeBlocked:
      acceptedCount > 0 ||
      submittedCount > 0 ||
      appealableCount > 0 ||
      openDisputeCount > 0 ||
      hasUnknownActiveBlocker,
  }
}
