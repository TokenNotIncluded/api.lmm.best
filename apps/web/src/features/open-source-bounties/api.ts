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
import { api } from '@/lib/api'

import type {
  BountyNotification,
  BountyChallenge,
  BountyDraftInput,
  BountyDispute,
  BountyDisputeReason,
  BountyFeeConfig,
  BountyMcpConnection,
  BountyProject,
  BountyProjectDetail,
  BountyTipNotification,
} from './types'

interface ApiEnvelope<T> {
  success: boolean
  code?: string
  message?: string
  data: T
}

async function unwrap<T>(request: Promise<{ data: ApiEnvelope<T> }>) {
  const response = await request
  if (!response.data.success) {
    const error = new Error(
      response.data.message || 'Open-source bounty request failed'
    )
    Object.assign(error, { code: response.data.code })
    throw error
  }
  return response.data.data
}

export async function listBounties() {
  const result = await unwrap<{
    items: BountyProject[] | null
    total: number
    page: number
    page_size: number
  }>(
    api.get('/api/open-source-bounties?page=1&page_size=50', {
      // Public challenges are optional on deployments that have not mounted
      // the bounty candidate yet.  ChallengeList owns the inline fallback;
      // never surface a probe 401/404 as a global toast.
      skipBusinessError: true,
      skipErrorHandler: true,
    })
  )
  return { ...result, items: result.items ?? [] }
}

export function getBountyConfig() {
  return unwrap<BountyFeeConfig>(api.get('/api/open-source-bounties/config'))
}

export function getMcpTokenStatus() {
  return unwrap<BountyMcpConnection>(
    api.get('/api/open-source-bounties/mcp-token')
  )
}

export function rotateMcpToken() {
  return unwrap<BountyMcpConnection & { token: string }>(
    api.post('/api/open-source-bounties/mcp-token')
  )
}

export function revokeMcpToken() {
  return unwrap<null>(api.delete('/api/open-source-bounties/mcp-token'))
}

export function listOwnedBounties(archived = false) {
  return unwrap<BountyProject[]>(
    api.get(
      `/api/open-source-bounties/mine?archived=${archived ? 'true' : 'false'}`
    )
  )
}

export async function getPendingBountyReviewCount() {
  const result = await unwrap<{ total: number }>(
    api.get('/api/todos?category=open_source_bounty_review&p=1&page_size=1', {
      skipBusinessError: true,
      skipErrorHandler: true,
    })
  )
  return Number.isSafeInteger(result.total) && result.total > 0
    ? result.total
    : 0
}

export function listAcceptedBounties() {
  return unwrap<BountyChallenge[]>(
    api.get('/api/open-source-bounties/accepted')
  )
}

export function getBountyDetail(projectId: number) {
  return unwrap<BountyProjectDetail>(
    api.get(`/api/open-source-bounties/projects/${projectId}`)
  )
}

export function createBounty(input: BountyDraftInput) {
  return unwrap<BountyProject>(api.post('/api/open-source-bounties', input))
}

export function updateBounty(projectId: number, input: BountyDraftInput) {
  return unwrap<BountyProject>(
    api.put(`/api/open-source-bounties/projects/${projectId}`, input)
  )
}

export function deleteBounty(projectId: number) {
  return unwrap<null>(
    api.delete(`/api/open-source-bounties/projects/${projectId}`)
  )
}

export function publishBounty(projectId: number) {
  return unwrap<{ project: BountyProject; charged_quota: number }>(
    api.post(`/api/open-source-bounties/projects/${projectId}/publish`)
  )
}

export function pauseBounty(projectId: number) {
  return unwrap<BountyProject>(
    api.post(`/api/open-source-bounties/projects/${projectId}/pause`)
  )
}

export function resumeBounty(projectId: number) {
  return unwrap<BountyProject>(
    api.post(`/api/open-source-bounties/projects/${projectId}/resume`)
  )
}

export function closeBounty(projectId: number) {
  return unwrap<{ project: BountyProject; refunded_quota: number }>(
    api.post(`/api/open-source-bounties/projects/${projectId}/close`)
  )
}

export function archiveBounty(projectId: number) {
  return unwrap<BountyProject>(
    api.post(`/api/open-source-bounties/projects/${projectId}/archive`)
  )
}

export function unarchiveBounty(projectId: number) {
  return unwrap<BountyProject>(
    api.post(`/api/open-source-bounties/projects/${projectId}/unarchive`)
  )
}

export function acceptBounty(projectId: number, githubHandle: string) {
  return unwrap<BountyChallenge>(
    api.post(`/api/open-source-bounties/projects/${projectId}/accept`, {
      github_handle: githubHandle,
    })
  )
}

export function submitChallenge(
  projectId: number,
  input: {
    issue_url: string
    pull_request_url: string
    submission_note: string
  }
) {
  return unwrap<BountyChallenge>(
    api.post(`/api/open-source-bounties/projects/${projectId}/submit`, input)
  )
}

export function withdrawChallenge(challengeId: number) {
  return unwrap<BountyChallenge>(
    api.post(`/api/open-source-bounties/challenges/${challengeId}/withdraw`)
  )
}

export function cancelChallenge(challengeId: number) {
  return unwrap<BountyChallenge>(
    api.post(`/api/open-source-bounties/challenges/${challengeId}/cancel`)
  )
}

export function reviewChallenge(
  challengeId: number,
  action: 'approve' | 'reject',
  input: {
    review_note: string
    rating_score: number
    rating_comment: string
  }
) {
  return unwrap<{ challenge: BountyChallenge; transferred_quota: number }>(
    api.post(
      `/api/open-source-bounties/challenges/${challengeId}/${action}`,
      input
    )
  )
}

export function tipChallenge(
  challengeId: number,
  input: { quota: number; note: string },
  idempotencyKey: string
) {
  return unwrap<{
    challenge: BountyChallenge
    transferred_quota: number
    remaining_quota: number
  }>(
    api.post(`/api/open-source-bounties/challenges/${challengeId}/tip`, input, {
      headers: { 'Idempotency-Key': idempotencyKey },
    })
  )
}

export function listBountyNotifications() {
  return unwrap<BountyNotification[]>(
    api.get('/api/open-source-bounties/notifications', {
      // Older production candidates may advertise the capability before the
      // unified route is mounted. A missing optional notification feed must
      // not become a global "Not Found" toast or block the bounty page.
      skipBusinessError: true,
      skipErrorHandler: true,
    })
  )
}

export function markBountyNotificationsRead() {
  return unwrap<null>(
    api.post('/api/open-source-bounties/notifications/read', undefined, {
      skipBusinessError: true,
      skipErrorHandler: true,
    })
  )
}

export function listReceivedBountyTips() {
  return unwrap<BountyTipNotification[]>(
    api.get('/api/open-source-bounties/tips/received')
  )
}

export function markReceivedBountyTipsRead() {
  return unwrap<null>(api.post('/api/open-source-bounties/tips/received/read'))
}

export async function listCompatibleBountyNotifications(
  supportsUnifiedNotifications: boolean
): Promise<BountyNotification[]> {
  if (supportsUnifiedNotifications) return listBountyNotifications()

  const tips = await listReceivedBountyTips()
  return tips.map((tip) => ({ ...tip, kind: 'tip_transfer' }))
}

export function markCompatibleBountyNotificationsRead(
  supportsUnifiedNotifications: boolean
) {
  return supportsUnifiedNotifications
    ? markBountyNotificationsRead()
    : markReceivedBountyTipsRead()
}

export function thankBountyTip(tipId: number) {
  return unwrap<BountyTipNotification>(
    api.post(`/api/open-source-bounties/tips/${tipId}/thank`)
  )
}

export function rateBountyOwner(
  challengeId: number,
  input: { score: number; comment: string }
) {
  return unwrap<BountyChallenge>(
    api.post(
      `/api/open-source-bounties/challenges/${challengeId}/rate-owner`,
      input
    )
  )
}

export function openBountyDispute(
  challengeId: number,
  input: { reason: BountyDisputeReason; statement: string }
) {
  return unwrap<BountyDispute>(
    api.post(
      `/api/open-source-bounties/challenges/${challengeId}/disputes`,
      input
    )
  )
}

export function listMyBountyDisputes() {
  return unwrap<BountyDispute[]>(
    api.get('/api/open-source-bounties/disputes/mine')
  )
}

export function listAdminBountyDisputes() {
  return unwrap<BountyDispute[]>(
    api.get('/api/open-source-bounties/disputes/admin')
  )
}

export function resolveBountyDispute(
  disputeId: number,
  input: { action: 'pay' | 'deny'; resolution: string }
) {
  return unwrap<{ dispute: BountyDispute; transferred_quota: number }>(
    api.post(`/api/open-source-bounties/disputes/${disputeId}/resolve`, input)
  )
}
