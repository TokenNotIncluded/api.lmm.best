import { z } from 'zod'

const securityRuleSchema = z
  .object({
    enabled: z.boolean().optional(),
    groups: z.array(z.string()).optional(),
  })
  .catchall(z.unknown())
const ruleArraySchema = z.array(securityRuleSchema)
const ruleEnvelopeSchema = z
  .object({ rules: ruleArraySchema })
  .catchall(z.unknown())

type SecurityRuleRecord = z.infer<typeof securityRuleSchema>
type RuleEnvelope = z.infer<typeof ruleEnvelopeSchema>

type ParsedRuleDocument =
  | {
      document: SecurityRuleRecord[]
      rules: SecurityRuleRecord[]
      format: 'array'
    }
  | { document: RuleEnvelope; rules: SecurityRuleRecord[]; format: 'object' }

export interface ProtectedGroupRuleState {
  enabledRuleCount: number
  groups: string[]
  valid: boolean
}

function parseRuleDocument(value: string): ParsedRuleDocument | null {
  let rawDocument: unknown
  try {
    rawDocument = JSON.parse(value)
  } catch {
    return null
  }

  const arrayResult = ruleArraySchema.safeParse(rawDocument)
  if (arrayResult.success) {
    return {
      document: arrayResult.data,
      rules: arrayResult.data,
      format: 'array',
    }
  }
  const envelopeResult = ruleEnvelopeSchema.safeParse(rawDocument)
  if (!envelopeResult.success) return null
  return {
    document: envelopeResult.data,
    rules: envelopeResult.data.rules,
    format: 'object',
  }
}

function normalizeGroups(groups: string[]): string[] | null {
  const normalized = [
    ...new Set(groups.map((group) => group.trim()).filter(Boolean)),
  ]
  if (
    normalized.length === 0 ||
    normalized.some((group) => group === '*' || group.length > 64)
  ) {
    return null
  }
  return normalized.sort((left, right) => left.localeCompare(right))
}

export function inspectProtectedGroupRules(
  value: string
): ProtectedGroupRuleState {
  const parsed = parseRuleDocument(value)
  if (!parsed) return { enabledRuleCount: 0, groups: [], valid: false }

  const enabledRules = parsed.rules.filter((rule) => rule.enabled === true)
  const groups = [
    ...new Set(
      enabledRules.flatMap((rule) =>
        (rule.groups ?? [])
          .map((group) => group.trim())
          .filter((group) => group.length > 0)
      )
    ),
  ].sort((left, right) => left.localeCompare(right))

  return {
    enabledRuleCount: enabledRules.length,
    groups,
    valid: true,
  }
}

export function replaceEnabledRuleGroups(
  value: string,
  groups: string[]
): string | null {
  const parsed = parseRuleDocument(value)
  const normalizedGroups = normalizeGroups(groups)
  if (!parsed || !normalizedGroups) return null

  let enabledRuleCount = 0
  const rules = parsed.rules.map((rule) => {
    if (rule.enabled !== true) return rule
    enabledRuleCount += 1
    return { ...rule, groups: normalizedGroups }
  })
  if (enabledRuleCount === 0) return null

  const updated =
    parsed.format === 'array' ? rules : { ...parsed.document, rules }
  return JSON.stringify(updated, null, 2)
}
