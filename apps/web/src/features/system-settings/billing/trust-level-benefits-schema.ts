/*
Copyright (C) 2026 LIghtJUNction
*/
import * as z from 'zod'

export const TRUST_LEVEL_BENEFITS_OPTION = 'TrustLevelBenefits'
export const TRUST_LEVEL_BENEFIT_CODES = [
  'standard_access',
  'developer_access',
  'usage_discount',
] as const
export const TRUST_ROLE_BENEFIT_CODES = [
  'administrator_access',
  'superadministrator_access',
  'usage_discount',
] as const

const roleTierSchema = z
  .strictObject({
    level: z.union([z.literal(5), z.literal(6)]),
    role: z.union([z.literal(10), z.literal(100)]),
    discount_ratio: z.number().finite().gt(0).max(1),
    benefits: z.array(z.enum(TRUST_ROLE_BENEFIT_CODES)).max(2),
  })
  .superRefine((tier, context) => {
    const access =
      tier.level === 5 ? 'administrator_access' : 'superadministrator_access'
    if (
      tier.role !== (tier.level === 5 ? 10 : 100) ||
      new Set(tier.benefits).size !== tier.benefits.length ||
      tier.benefits.some((code) => code !== access && code !== 'usage_discount')
    ) {
      context.addIssue({
        code: 'custom',
        path: ['benefits'],
        message: 'Role benefits must match the existing account role',
      })
    }
  })

const tierSchema = z.strictObject({
  level: z.number().int().min(0).max(4),
  min_paid_credits: z.number().int().min(0).max(Number.MAX_SAFE_INTEGER),
  discount_ratio: z.number().finite().gt(0).max(1),
  benefits: z
    .array(z.enum(TRUST_LEVEL_BENEFIT_CODES))
    .max(3)
    .refine(
      (benefits) => new Set(benefits).size === benefits.length,
      'Benefits must not be repeated'
    ),
})

export const trustLevelBenefitsSchema = z
  .strictObject({
    version: z.literal(1),
    paid_activation_enabled: z.boolean(),
    decay_period_days: z.number().int().min(0).max(3650),
    tiers: z.array(tierSchema).length(5),
    role_tiers: z.array(roleTierSchema).length(2).optional(),
  })
  .superRefine((config, context) => {
    config.tiers.forEach((tier, index) => {
      if (
        tier.level !== index ||
        (index === 0 && tier.min_paid_credits !== 0)
      ) {
        context.addIssue({
          code: 'custom',
          path: ['tiers', index, 'min_paid_credits'],
          message:
            'Automatic levels must be L0–L4, with a zero threshold for L0',
        })
      }
      if (
        index > 0 &&
        tier.min_paid_credits <= config.tiers[index - 1].min_paid_credits
      ) {
        context.addIssue({
          code: 'custom',
          path: ['tiers', index, 'min_paid_credits'],
          message: 'Recharge thresholds must increase from L1 to L4',
        })
      }
    })
    config.role_tiers?.forEach((tier, index) => {
      if (tier.level !== index + 5) {
        context.addIssue({
          code: 'custom',
          path: ['role_tiers', index, 'benefits'],
          message: 'Role benefits must match the existing account role',
        })
      }
    })
  })

export type TrustLevelBenefitsConfig = z.infer<typeof trustLevelBenefitsSchema>

export function parseTrustLevelBenefits(
  raw: string
): TrustLevelBenefitsConfig | null {
  try {
    const parsed = trustLevelBenefitsSchema.safeParse(JSON.parse(raw))
    return parsed.success ? parsed.data : null
  } catch {
    return null
  }
}

export function serializeTrustLevelBenefits(
  config: TrustLevelBenefitsConfig
): string {
  return JSON.stringify(trustLevelBenefitsSchema.parse(config))
}
