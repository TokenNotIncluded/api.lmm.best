/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
export const MODERATION_MAX_CATEGORY_FINE_USD = 1000

export const MODERATION_MODELS = [
  'omni-moderation-latest',
  'omni-moderation-2024-09-26',
] as const

export const MODERATION_CATEGORIES = [
  'harassment',
  'harassment/threatening',
  'hate',
  'hate/threatening',
  'illicit',
  'illicit/violent',
  'self-harm',
  'self-harm/intent',
  'self-harm/instructions',
  'sexual',
  'sexual/minors',
  'violence',
  'violence/graphic',
] as const

export const MODERATION_CATEGORY_LABELS: Record<
  (typeof MODERATION_CATEGORIES)[number],
  string
> = {
  harassment: 'Harassment',
  'harassment/threatening': 'Threatening harassment',
  hate: 'Hate',
  'hate/threatening': 'Threatening hate',
  illicit: 'Illicit activity',
  'illicit/violent': 'Violent illicit activity',
  'self-harm': 'Self-harm',
  'self-harm/intent': 'Self-harm intent',
  'self-harm/instructions': 'Self-harm instructions',
  sexual: 'Sexual content',
  'sexual/minors': 'Sexual content involving minors',
  violence: 'Violence',
  'violence/graphic': 'Graphic violence',
}

export type ModerationMode = 'off' | 'tolerant' | 'strict'
export type ModerationGroupPolicy = {
  mode: ModerationMode
  amount_currency?: 'USD' | 'legacy_pricing_unit'
  category_fines_usd: Partial<
    Record<(typeof MODERATION_CATEGORIES)[number], number>
  >
}
export type ModerationGroupPolicies = Record<string, ModerationGroupPolicy>

export function parseModerationGroupPolicies(
  value: string
): ModerationGroupPolicies | null {
  try {
    if (new TextEncoder().encode(value).length > 65536) return null
    const parsed: unknown = JSON.parse(value)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return null
    }
    if (Object.keys(parsed).length > 64) return null
    const result: ModerationGroupPolicies = Object.create(null)
    for (const [group, entry] of Object.entries(parsed)) {
      if (
        !group.trim() ||
        group !== group.trim() ||
        group.length > 64 ||
        group === '*'
      ) {
        return null
      }
      if (!entry || typeof entry !== 'object' || Array.isArray(entry)) {
        return null
      }
      const policy = entry as Record<string, unknown>
      if (!['off', 'tolerant', 'strict'].includes(String(policy.mode))) {
        return null
      }
      if (
        policy.amount_currency !== undefined &&
        !['USD', 'legacy_pricing_unit'].includes(String(policy.amount_currency))
      ) {
        return null
      }
      const fines = policy.category_fines_usd ?? {}
      if (!fines || typeof fines !== 'object' || Array.isArray(fines)) {
        return null
      }
      for (const [category, amount] of Object.entries(fines)) {
        if (
          !MODERATION_CATEGORIES.includes(
            category as (typeof MODERATION_CATEGORIES)[number]
          ) ||
          typeof amount !== 'number' ||
          !Number.isFinite(amount) ||
          amount < 0 ||
          amount > MODERATION_MAX_CATEGORY_FINE_USD ||
          !isModerationFineUsd(amount)
        ) {
          return null
        }
      }
      result[group] = {
        mode: policy.mode as ModerationMode,
        category_fines_usd: fines,
        ...(policy.amount_currency === undefined
          ? {}
          : {
              amount_currency: policy.amount_currency as
                | 'USD'
                | 'legacy_pricing_unit',
            }),
      }
    }
    return result
  } catch {
    return null
  }
}

// Decimal arithmetic prevents binary floating-point rounding from changing a fine.
function decimalRatio(value: number): [bigint, bigint] | null {
  if (!Number.isFinite(value)) return null
  const match = String(value).match(
    /^([+-]?)(\d+)(?:\.(\d+))?(?:e([+-]?\d+))?$/i
  )
  if (!match) return null
  const fraction = match[3] ?? ''
  const exponent = Number(match[4] ?? 0) - fraction.length
  let numerator = BigInt(match[2] + fraction) * (match[1] === '-' ? -1n : 1n)
  let denominator = 1n
  if (exponent >= 0) numerator *= 10n ** BigInt(exponent)
  else denominator = 10n ** BigInt(-exponent)
  return [numerator, denominator]
}

export function isModerationFineUsd(amount: number): boolean {
  const ratio = decimalRatio(amount)
  return Boolean(
    ratio &&
    amount >= 0 &&
    amount <= MODERATION_MAX_CATEGORY_FINE_USD &&
    (ratio[0] * 1_000_000n) % ratio[1] === 0n
  )
}

export function moderationFineDisplayUsd(
  amount: number,
  policy: ModerationGroupPolicy,
  legacyUnitsPerUsd: number
): number {
  return policy.amount_currency === 'USD' ? amount : amount / legacyUnitsPerUsd
}

/** Conversion is all-or-nothing. Never round existing category prices to fit USD. */
export function normalizeModerationPolicyUsd(
  policy: ModerationGroupPolicy,
  legacyUnitsPerUsd: number
): ModerationGroupPolicy | null {
  if (policy.amount_currency === 'USD') {
    return { ...policy, category_fines_usd: { ...policy.category_fines_usd } }
  }
  const scale = decimalRatio(legacyUnitsPerUsd)
  if (!scale || scale[0] <= 0n) return null
  const fines: ModerationGroupPolicy['category_fines_usd'] = {}
  for (const [category, amount] of Object.entries(policy.category_fines_usd)) {
    const ratio = decimalRatio(amount)
    if (!ratio) return null
    const numerator = ratio[0] * scale[1] * 1_000_000n
    const denominator = ratio[1] * scale[0]
    if (numerator % denominator !== 0n) return null
    const usd = Number(numerator / denominator) / 1_000_000
    if (!isModerationFineUsd(usd)) return null
    fines[category as keyof typeof fines] = usd
  }
  return { ...policy, amount_currency: 'USD', category_fines_usd: fines }
}
