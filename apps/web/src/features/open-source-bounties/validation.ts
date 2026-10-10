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
  options: { rawCredits?: boolean } = {}
): BountyDraftErrors {
  const errors: BountyDraftErrors = {}
  const rewardAmount = parseBountyNumericInput(draft.rewardAmount)
  const rewardSlots = parseBountyNumericInput(draft.rewardSlots)
  const titleLength = draft.title.trim().length
  const descriptionLength = draft.description.trim().length
  const rulesLength = draft.rules.trim().length

  if (!isGithubRepositoryUrl(draft.repositoryUrl)) {
    errors.repositoryUrl =
      'Enter a GitHub repository URL in the format https://github.com/owner/repository.'
  }
  if (titleLength < 4 || titleLength > 120) {
    errors.title = 'Bounty title must contain 4 to 120 characters.'
  }
  if (descriptionLength < 20 || descriptionLength > 2000) {
    errors.description =
      'Project and defect scope must contain 20 to 2000 characters.'
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
    errors.rewardAmount = 'Reward per fix must be greater than zero.'
  }
  if (!Number.isInteger(rewardSlots) || rewardSlots < 1 || rewardSlots > 100) {
    errors.rewardSlots =
      'Reward slots must be a whole number between 1 and 100.'
  }

  return errors
}

export function validateBountySubmissionLinks(
  submission: BountySubmissionLinks
): string | undefined {
  if (
    submission.issueUrl.trim().length === 0 &&
    submission.pullRequestUrl.trim().length === 0
  ) {
    return 'Provide at least one GitHub Issue or pull request URL.'
  }
  return undefined
}
