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
import {
  BILLING_CACHE_VAR_MAP,
  BILLING_VARS,
  evaluateBillingExpression,
  parseTiersFromExpr,
} from './billing-expr'

export const CACHE_MODE_TIMED = 'timed'
export const CACHE_MODE_GENERIC = 'generic'
export type CacheMode = typeof CACHE_MODE_TIMED | typeof CACHE_MODE_GENERIC

export type TierConditionInput = {
  var: 'p' | 'c' | 'len'
  op: '<' | '<=' | '>' | '>='
  value: number | string
}

export type VisualTier = {
  label: string
  conditions: TierConditionInput[]
  input_unit_cost: number
  output_unit_cost: number
  cache_mode: CacheMode
  cache_read_unit_cost?: number
  cache_create_unit_cost?: number
  cache_create_1h_unit_cost?: number
  image_unit_cost?: number
  image_output_unit_cost?: number
  audio_input_unit_cost?: number
  audio_output_unit_cost?: number
  [field: string]: unknown
}

export type VisualConfig = {
  tiers: VisualTier[]
}

export function getTierCacheMode(
  tier: Partial<VisualTier> | null | undefined
): CacheMode {
  if (tier?.cache_mode === CACHE_MODE_TIMED) return CACHE_MODE_TIMED
  if (tier?.cache_mode === CACHE_MODE_GENERIC) return CACHE_MODE_GENERIC
  return Number(tier?.cache_create_1h_unit_cost) > 0
    ? CACHE_MODE_TIMED
    : CACHE_MODE_GENERIC
}

export function normalizeVisualTier(
  tier: Partial<VisualTier> = {}
): VisualTier {
  return {
    label: tier.label ?? '',
    input_unit_cost: Number(tier.input_unit_cost) || 0,
    output_unit_cost: Number(tier.output_unit_cost) || 0,
    cache_mode: getTierCacheMode(tier),
    conditions: Array.isArray(tier.conditions) ? tier.conditions : [],
    ...tier,
    cache_read_unit_cost: Number(tier.cache_read_unit_cost) || 0,
    cache_create_unit_cost: Number(tier.cache_create_unit_cost) || 0,
    cache_create_1h_unit_cost: Number(tier.cache_create_1h_unit_cost) || 0,
    image_unit_cost: Number(tier.image_unit_cost) || 0,
    image_output_unit_cost: Number(tier.image_output_unit_cost) || 0,
    audio_input_unit_cost: Number(tier.audio_input_unit_cost) || 0,
    audio_output_unit_cost: Number(tier.audio_output_unit_cost) || 0,
  }
}

export function createDefaultVisualConfig(): VisualConfig {
  return {
    tiers: [
      normalizeVisualTier({
        conditions: [],
        input_unit_cost: 0,
        output_unit_cost: 0,
        label: 'base',
        cache_mode: CACHE_MODE_GENERIC,
      }),
    ],
  }
}

export function normalizeVisualConfig(
  config: VisualConfig | null | undefined
): VisualConfig {
  if (!config || !Array.isArray(config.tiers) || config.tiers.length === 0) {
    return createDefaultVisualConfig()
  }
  return {
    ...config,
    tiers: config.tiers.map((tier) => normalizeVisualTier(tier)),
  }
}

function buildConditionStr(conditions: TierConditionInput[]): string {
  if (!conditions || conditions.length === 0) return ''
  const allowedVars = new Set(['p', 'c', 'len'])
  const allowedOps = new Set(['<', '<=', '>', '>='])
  const parts: string[] = []
  for (const condition of conditions) {
    const value = Number(condition.value)
    if (
      !allowedVars.has(condition.var) ||
      !allowedOps.has(condition.op) ||
      !Number.isFinite(value)
    ) {
      continue
    }
    parts.push(`${condition.var} ${condition.op} ${value}`)
  }
  return parts.join(' && ')
}

function buildTierBodyExpr(tier: VisualTier): string {
  const parts: string[] = []
  const ic = Number(tier.input_unit_cost) || 0
  const oc = Number(tier.output_unit_cost) || 0
  parts.push(`p * ${ic}`)
  parts.push(`c * ${oc}`)
  for (const cv of BILLING_CACHE_VAR_MAP) {
    const v = Number((tier as Record<string, unknown>)[cv.field]) || 0
    if (v !== 0) parts.push(`${cv.exprVar} * ${v}`)
  }
  return parts.join(' + ')
}

export function generateExprFromVisualConfig(
  config: VisualConfig | null | undefined
): string {
  if (!config || !config.tiers || config.tiers.length === 0) {
    return 'p * 0 + c * 0'
  }
  const tiers = config.tiers

  if (tiers.length === 1) {
    const tier = tiers[0]
    const label = tier.label || 'default'
    const body = `tier(${JSON.stringify(label)}, ${buildTierBodyExpr(tier)})`
    const cond = buildConditionStr(tier.conditions)
    if (cond) {
      return `${cond} ? ${body} : p * 0 + c * 0`
    }
    return body
  }

  const parts: string[] = []
  for (let i = 0; i < tiers.length; i++) {
    const tier = tiers[i]
    const label = tier.label || `tier_${i + 1}`
    const body = `tier(${JSON.stringify(label)}, ${buildTierBodyExpr(tier)})`
    const cond = buildConditionStr(tier.conditions)

    if (i < tiers.length - 1 && cond) {
      parts.push(`${cond} ? ${body}`)
    } else {
      parts.push(body)
    }
  }
  return parts.join(' : ')
}

export function tryParseVisualConfig(
  exprStr: string | null | undefined
): VisualConfig | null {
  if (!exprStr) return null
  try {
    let body = exprStr.trim()
    const versionMatch = /^v\d+:([\s\S]*)$/.exec(body)
    if (versionMatch) body = versionMatch[1]

    const parsedTiers = parseTiersFromExpr(body)
    if (parsedTiers.length === 0) return null
    const tiers = parsedTiers.map((parsed) => {
      const tier: Partial<VisualTier> = {
        label: parsed.label,
        conditions: parsed.conditions,
        input_unit_cost: Number(parsed.inputPrice) || 0,
        output_unit_cost: Number(parsed.outputPrice) || 0,
      }
      for (const cacheVar of BILLING_CACHE_VAR_MAP) {
        const billingVar = BILLING_VARS.find(
          (candidate) => candidate.key === cacheVar.exprVar
        )
        if (billingVar?.field) {
          tier[cacheVar.field] = Number(parsed[billingVar.field]) || 0
        }
      }
      return normalizeVisualTier(tier)
    })

    const config = normalizeVisualConfig({ tiers })
    const regenerated = generateExprFromVisualConfig(config)
    if (regenerated.replaceAll(/\s+/g, '') !== body.replaceAll(/\s+/g, '')) {
      return null
    }
    return config
  } catch {
    return null
  }
}

// ---------------------------------------------------------------------------
// Local cost evaluator (for the estimator preview)
// ---------------------------------------------------------------------------

const ESTIMATOR_VARS = [
  { var: 'cr', stateKey: 'cacheReadTokens' },
  { var: 'cc', stateKey: 'cacheCreateTokens' },
  { var: 'cc1h', stateKey: 'cacheCreate1hTokens' },
  { var: 'img', stateKey: 'imageTokens' },
  { var: 'img_o', stateKey: 'imageOutputTokens' },
  { var: 'ai', stateKey: 'audioInputTokens' },
  { var: 'ao', stateKey: 'audioOutputTokens' },
] as const

export type ExtraTokenValues = Record<
  (typeof ESTIMATOR_VARS)[number]['stateKey'],
  number
>

export type EvalResult = {
  cost: number
  matchedTier: string
  error: string | null
}

export function evalExprLocally(
  exprStr: string,
  promptTokens: number,
  completionTokens: number,
  extraTokenValues: ExtraTokenValues
): EvalResult {
  try {
    if (!exprStr || !exprStr.trim()) {
      return { cost: 0, matchedTier: '', error: null }
    }
    const cacheReadTokens = extraTokenValues.cacheReadTokens || 0
    const cacheCreateTokens = extraTokenValues.cacheCreateTokens || 0
    const cacheCreate1hTokens = extraTokenValues.cacheCreate1hTokens || 0
    const len =
      promptTokens + cacheReadTokens + cacheCreateTokens + cacheCreate1hTokens
    const variables: Record<string, number> = {
      p: promptTokens,
      c: completionTokens,
      len,
    }
    for (const field of ESTIMATOR_VARS) {
      variables[field.var] = extraTokenValues[field.stateKey] || 0
    }
    const evaluated = evaluateBillingExpression(exprStr, variables)
    return {
      cost: evaluated.value || 0,
      matchedTier: evaluated.matchedTier,
      error: null,
    }
  } catch (e) {
    const message = e instanceof Error ? e.message : String(e)
    return { cost: 0, matchedTier: '', error: message }
  }
}

export function exprUsesExtraVars(exprStr: string): boolean {
  if (!exprStr) return false
  const names = new Set<string>(ESTIMATOR_VARS.map((field) => field.var))
  let index = 0
  while (index < exprStr.length) {
    const char = exprStr[index]
    const startsIdentifier =
      char === '_' ||
      (char >= 'a' && char <= 'z') ||
      (char >= 'A' && char <= 'Z')
    if (!startsIdentifier) {
      index += 1
      continue
    }
    const start = index
    index += 1
    while (index < exprStr.length) {
      const next = exprStr[index]
      const continuesIdentifier =
        next === '_' ||
        (next >= 'a' && next <= 'z') ||
        (next >= 'A' && next <= 'Z') ||
        (next >= '0' && next <= '9')
      if (!continuesIdentifier) break
      index += 1
    }
    if (names.has(exprStr.slice(start, index))) return true
  }
  return false
}

export const ESTIMATOR_EXTRA_FIELDS = ESTIMATOR_VARS
