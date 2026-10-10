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
const GITHUB_NAME_PATTERN = /^[A-Za-z0-9](?:[A-Za-z0-9_.-]{0,98}[A-Za-z0-9])?$/

export type BountyDraftValidationInput = {
  kind?: 'general' | 'open_source'
  publisherType?: 'individual' | 'company'
  deadlineAt?: string
  repositoryUrl: string
  title: string
  description: string
  rules: string
  rewardAmount: number | string
  rewardSlots: number | string
}

export type BountyDraftErrors = Partial<
  Record<keyof BountyDraftValidationInput, string>
>

export type BountyCharge = {
  gross: number
  netReward: number
  escrow: number
  platformFee: number
  feeRatePercent: number
  total: number
}

export type BountySubmissionLinks = {
  deliveryUrl?: string
  submissionNote?: string
  issueUrl: string
  pullRequestUrl: string
}

export function parseBountyNumericInput(value: number | string): number {
  if (typeof value === 'string' && value.trim() === '') return Number.NaN
  return Number(value)
}

export function calculateBountyCharge(
  rewardQuota: number,
  rewardSlots: number,
  feeRateBasisPoints: number
): BountyCharge {
  const reward = Number.isFinite(rewardQuota) ? Math.max(0, rewardQuota) : 0
  const slots = Number.isFinite(rewardSlots) ? Math.max(0, rewardSlots) : 0
  const feePerSlot = Math.ceil((reward * feeRateBasisPoints) / 10_000)
  const netReward = Math.max(0, reward - feePerSlot)
  const gross = reward * slots
  const platformFee = feePerSlot * slots
  return {
    gross,
    netReward,
    escrow: netReward * slots,
    platformFee,
    feeRatePercent: feeRateBasisPoints / 100,
    total: gross,
  }
}

function isGithubRepositoryUrl(rawUrl: string): boolean {
  try {
    const url = new URL(rawUrl.trim())
    const pathParts = url.pathname.replaceAll(/^\/+|\/+$/g, '').split('/')
    if (
      url.protocol !== 'https:' ||
      url.hostname.toLowerCase() !== 'github.com' ||
      url.username !== '' || url.password !== '' || url.port !== '' ||
      /[\\\x00-\x1f\x7f]/.test(rawUrl) ||
      pathParts.length !== 2
    ) {
      return false
    }

    const owner = pathParts[0]
    const repository = pathParts[1].replace(/\.git$/, '')
    return (
      GITHUB_NAME_PATTERN.test(owner) && GITHUB_NAME_PATTERN.test(repository)
    )
  } catch {
    return false
  }
}

export function validateBountyDraft(
  draft: BountyDraftValidationInput,
  options: { rawCredits?: boolean; contentOnly?: boolean } = {}
): BountyDraftErrors {
  const errors: BountyDraftErrors = {}
  const rewardAmount = parseBountyNumericInput(draft.rewardAmount)
  const rewardSlots = parseBountyNumericInput(draft.rewardSlots)
  const titleLength = Array.from(draft.title.trim()).length
  const descriptionLength = Array.from(draft.description.trim()).length
  const rulesLength = Array.from(draft.rules.trim()).length

  if (draft.kind !== 'general' && !isGithubRepositoryUrl(draft.repositoryUrl)) {
    errors.repositoryUrl =
      'Enter a GitHub repository URL in the format https://github.com/owner/repository.'
  }
  if (draft.kind === 'general' && draft.repositoryUrl.trim()) {
    errors.repositoryUrl = 'Complete every bounty field with valid values.'
  }
  if (draft.kind !== undefined && !['general', 'open_source'].includes(draft.kind)) {
    errors.kind = 'Complete every bounty field with valid values.'
  }
  if (draft.publisherType !== undefined && !['individual', 'company'].includes(draft.publisherType)) {
    errors.publisherType = 'Complete every bounty field with valid values.'
  }
  if (!options.contentOnly && !isBountyRecruitmentOpen({ deadline_at: parseBountyDeadline(draft.deadlineAt ?? '') })) {
    errors.deadlineAt = 'Expired time cannot be earlier than current time'
  }
  if (titleLength < 4 || titleLength > 120) {
    errors.title = 'Bounty title must contain 4 to 120 characters.'
  }
  if (descriptionLength < 20 || descriptionLength > 2000) {
    errors.description =
      draft.kind === 'general' ? 'Complete every bounty field with valid values.' : 'Project and defect scope must contain 20 to 2000 characters.'
  }
  if (rulesLength < 20 || rulesLength > 5000) {
    errors.rules =
      'Acceptance and verification rules must contain 20 to 5000 characters.'
  }
  if (
    !Number.isFinite(rewardAmount) ||
    rewardAmount <= 0 ||
    (options.rawCredits &&
      (!Number.isSafeInteger(rewardAmount) ||
        !Number.isSafeInteger(rewardAmount * rewardSlots)))
  ) {
    errors.rewardAmount = draft.kind === 'general' ? 'Invalid amount' : 'Reward per fix must be greater than zero.'
  }
  if (!Number.isInteger(rewardSlots) || rewardSlots < 1 || rewardSlots > 100) {
    errors.rewardSlots =
      'Reward slots must be a whole number between 1 and 100.'
  }

  return errors
}

export function parseBountyDeadline(value: string): number {
  if (!value.trim()) return 0
  // datetime-local is deliberately interpreted in the user's local time zone.
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(value)) return Number.NaN
  const timestamp = new Date(value).getTime()
  if (!Number.isFinite(timestamp)) return Number.NaN
  // Reject normalized impossible dates and daylight-saving gaps.
  const seconds = Math.floor(timestamp / 1000)
  return formatBountyDeadline(seconds) === value ? seconds : Number.NaN
}

export function formatBountyDeadline(seconds: number): string {
  if (!seconds) return ''
  const date = new Date(seconds * 1000)
  if (!Number.isFinite(date.getTime())) return ''
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${String(date.getFullYear()).padStart(4, '0')}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

export function isBountyRecruitmentOpen(
  project: { deadline_at?: number },
  now = Math.floor(Date.now() / 1000)
): boolean {
  const deadline = project.deadline_at ?? 0
  return deadline === 0 || (Number.isSafeInteger(deadline) && deadline > now && deadline <= 253402300799)
}

export function isBountyDeliveryUrl(raw: string): boolean {
  const value = raw.trim()
  if (!value || /[\\\x00-\x1f\x7f]/.test(value)) return false
  try {
    const url = new URL(value)
    return /^https:\/\//.test(value) && url.protocol === 'https:' &&
      Boolean(url.hostname) && !url.username && !url.password &&
      new TextEncoder().encode(value).length <= 2048 &&
      new TextEncoder().encode(url.href).length <= 2048
  } catch {
    return false
  }
}

export function validateBountySubmissionLinks(
  submission: BountySubmissionLinks,
  options: { kind?: 'general' | 'open_source' } = {}
): string | undefined {
  const noteLength = Array.from((submission.submissionNote ?? '').trim()).length
  const delivery = submission.deliveryUrl?.trim() ?? ''
  if (delivery && !isBountyDeliveryUrl(delivery)) return 'Must be a valid URL'
  if (noteLength > 2000) return 'Complete every bounty field with valid values.'
  if (options.kind === 'general') {
    if (submission.issueUrl.trim() || submission.pullRequestUrl.trim()) {
      return 'Complete every bounty field with valid values.'
    }
    if (!delivery && noteLength < 20) return 'Complete every bounty field with valid values.'
    return undefined
  }
  if (!submission.issueUrl.trim() && !submission.pullRequestUrl.trim()) {
    return 'Provide at least one GitHub Issue or pull request URL.'
  }
  return undefined
}
