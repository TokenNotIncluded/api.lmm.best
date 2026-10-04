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
          amount !== Math.round(amount * 1000000) / 1000000
        ) {
          return null
        }
      }
      result[group] = {
        mode: policy.mode as ModerationMode,
        category_fines_usd: fines,
      }
    }
    return result
  } catch {
    return null
  }
}
