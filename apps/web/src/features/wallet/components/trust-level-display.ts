/*
Copyright (C) 2026 LIghtJUNction
*/
import type { TrustLevelInfo, TrustLevelTier } from '@/stores/auth-store'

export function parseTrustCredits(
  value: string | null | undefined
): bigint | null {
  if (typeof value !== 'string' || !/^\d+$/.test(value)) return null
  return BigInt(value)
}

/** Role levels never participate in the recharge ladder, including older DTOs. */
export function getTrustLevelProgress(
  info: TrustLevelInfo | undefined,
  tiers: TrustLevelTier[],
  role?: number
) {
  const currentLevel =
    role && role >= 100 ? 6 : role && role >= 10 ? 5 : (info?.level ?? 0)
  const roleAssigned = info?.level_source === 'role' || currentLevel >= 5
  const automaticLevel =
    roleAssigned || info?.paid_credit_projection_available === false
      ? null
      : (info?.automatic_level ?? currentLevel)
  const nextLevel =
    !roleAssigned &&
    info?.next_level != null &&
    info.next_level >= 1 &&
    info.next_level <= 4
      ? info.next_level
      : null
  const automaticTiers = tiers.filter(
    (tier) => tier.level >= 0 && tier.level <= 4
  )
  const currentTier = automaticTiers.find(
    (tier) => tier.level === automaticLevel
  )
  const nextTier = automaticTiers.find((tier) => tier.level === nextLevel)
  let progress: number | null = null
  if (
    info?.paid_credit_projection_available !== false &&
    nextLevel != null &&
    nextTier
  ) {
    const paid = parseTrustCredits(info?.paid_credits)
    const previous = parseTrustCredits(currentTier?.min_paid_credits)
    const next = parseTrustCredits(nextTier.min_paid_credits)
    if (paid != null && previous != null && next != null && next > previous) {
      const units = ((paid - previous) * 10000n) / (next - previous)
      progress = Number(units < 0n ? 0n : units > 10000n ? 10000n : units) / 100
    }
  }
  return {
    currentLevel,
    roleAssigned,
    automaticLevel,
    automaticTiers,
    nextLevel,
    progress,
  }
}
