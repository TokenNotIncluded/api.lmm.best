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
import type { BountyChallenge } from './types'

export function bountyTimeline(challenge: BountyChallenge) {
  const events: Array<{ key: string; time: number; evidence?: boolean }> = []
  const add = (key: string, time: number | undefined, evidence = false) => {
    if (time && time > 0) events.push({ key, time, evidence })
  }
  add('Accepted', challenge.accepted_at)
  add('Submitted', challenge.submitted_at, true)
  // A dispute can overturn a rejection; preserve the original rejection event.
  add(
    'Rejected',
    challenge.rejected_at ||
      (challenge.status === 'rejected' ? challenge.reviewed_at : 0)
  )
  if (challenge.status === 'approved' && !challenge.dispute) {
    add('Approved', challenge.reviewed_at)
  }
  add('Dispute opened', challenge.dispute?.created_at, true)
  if (challenge.dispute?.status !== 'open') {
    add(
      challenge.dispute?.status === 'resolved_paid'
        ? 'Resolved and paid'
        : 'Resolved and denied',
      challenge.dispute?.resolved_at
    )
  }
  add('Reward credited to API balance', challenge.paid_at)
  return events.sort((a, b) => a.time - b.time)
}

export function bountyAvailableSlots(project: {
  reward_slots: number
  active_challenge_count: number
  approved_challenge_count: number
}) {
  return Math.max(
    0,
    project.reward_slots -
      project.active_challenge_count -
      project.approved_challenge_count
  )
}
