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
import type { RatioType } from '../types'
import {
  MODELS_DEV_PRESET_ID,
  MODELS_DEV_PRESET_NAME,
  OFFICIAL_CHANNEL_ID,
  OFFICIAL_CHANNEL_NAME,
  RATIO_TYPE_OPTIONS,
} from './constants'
import type { ModelPricingConfig } from './model-pricing-api'
import { ratioToUsdPerMillion } from './model-pricing-units'
import { formatPricingNumber } from './pricing-format'

export type RatioDifferenceEntry = {
  current: number | string | null
  upstreams: Record<string, number | string | 'same'>
  confidence: Record<string, boolean>
}

export type ModelRow = {
  key: string
  model: string
  ratioTypes: Partial<Record<RatioType, RatioDifferenceEntry>>
  billingConflict: boolean
}

export type ResolutionsMap = Record<string, Record<string, number | string>>

export type ResolutionSelection = {
  model: string
  ratioType: RatioType
  value: number | string
  sourceName: string
}

export type ResolvedResolutionSelection = ResolutionSelection & {
  ratioType: RatioType
}

export type ResolutionRemoval = {
  model: string
  ratioType: RatioType
}

export type ResolutionRemovalPlan = Map<string, Set<RatioType>>

export const RATIO_SYNC_FIELDS: RatioType[] = [
  'model_ratio',
  'completion_ratio',
  'cache_ratio',
  'create_cache_ratio',
  'image_ratio',
  'audio_ratio',
  'audio_completion_ratio',
]

export const SYNC_FIELD_ORDER: RatioType[] = [
  ...RATIO_SYNC_FIELDS,
  'model_price',
  'billing_mode',
  'billing_expr',
]

export const NUMERIC_SYNC_FIELDS = new Set<string>([
  ...RATIO_SYNC_FIELDS,
  'model_price',
])

// Fetch normalizes every source to the captured local pricing schema. Only the
// calibrated token ratio needs conversion for its USD-per-million label.
function formatSyncPricingNumber(value: number) {
  const formatted = formatPricingNumber(value)
  return Number.isFinite(value) && value !== 0 && Number(formatted) === 0
    ? value.toString()
    : formatted
}

export function getSyncFieldDisplayValue(
  field: string,
  value: number | string,
  config?: ModelPricingConfig
): string {
  if (value === 'same') return String(value)
  if (field === 'model_price') {
    return `USD ${formatSyncPricingNumber(Number(value))}`
  }
  if (field === 'model_ratio' && config) {
    return `USD ${formatSyncPricingNumber(ratioToUsdPerMillion(Number(value), config.credits_per_usd))} /1M`
  }
  return String(value)
}

const SYNC_OPTION_KEYS: Record<RatioType, keyof ModelPricingConfig['values']> =
  {
    model_ratio: 'ModelRatio',
    completion_ratio: 'CompletionRatio',
    cache_ratio: 'CacheRatio',
    create_cache_ratio: 'CreateCacheRatio',
    image_ratio: 'ImageRatio',
    audio_ratio: 'AudioRatio',
    audio_completion_ratio: 'AudioCompletionRatio',
    model_price: 'ModelPrice',
    billing_mode: 'billing_setting.billing_mode',
    billing_expr: 'billing_setting.billing_expr',
  }

export function parseSyncPricingMaps(config: ModelPricingConfig) {
  return Object.fromEntries(
    Object.entries(SYNC_OPTION_KEYS).map(([field, key]) => {
      const parsed: unknown = JSON.parse(config.values[key])
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
        throw new Error('Invalid pricing snapshot')
      }
      return [field, { ...(parsed as Record<string, number | string>) }]
    })
  ) as Record<RatioType, Record<string, number | string>>
}

export function getLocalSyncBillingCategory(
  maps: ReturnType<typeof parseSyncPricingMaps>,
  model: string
): 'price' | 'ratio' | 'tiered' | null {
  if (maps.billing_mode[model] === 'tiered_expr') return 'tiered'
  if (maps.model_price[model] !== undefined) return 'price'
  if (RATIO_SYNC_FIELDS.some((field) => maps[field][model] !== undefined)) {
    return 'ratio'
  }
  return null
}

// Keep the captured CAS baseline intact. Send only maps changed by selections;
// never resend unrelated locks, tool prices or canonical USD maps as legacy data.
export function buildUpstreamPricingUpdates(
  config: ModelPricingConfig,
  resolutions: ResolutionsMap
): Record<string, string> {
  const original = parseSyncPricingMaps(config)
  const maps = parseSyncPricingMaps(config)
  for (const [model, selection] of Object.entries(resolutions)) {
    if (!model.trim() || Object.keys(selection).length === 0) {
      throw new Error('Invalid upstream price selection')
    }
    for (const [field, value] of Object.entries(selection)) {
      if (!Object.hasOwn(SYNC_OPTION_KEYS, field)) {
        throw new Error('Invalid upstream price selection')
      }
      if (NUMERIC_SYNC_FIELDS.has(field)) {
        if (
          (typeof value === 'string' && !value.trim()) ||
          !Number.isFinite(Number(value)) ||
          Number(value) < 0
        ) {
          throw new Error('Invalid upstream price selection')
        }
      } else if (
        typeof value !== 'string' ||
        !value.trim() ||
        value === 'same'
      ) {
        throw new Error('Invalid upstream price selection')
      }
    }
    const hasExpression = selection.billing_expr !== undefined
    const hasPrice = selection.model_price !== undefined
    const hasRatio = RATIO_SYNC_FIELDS.some(
      (field) => selection[field] !== undefined
    )
    const mode = selection.billing_mode
    const existingBase = original.model_ratio[model]
    const hasUsableExistingBase =
      typeof existingBase === 'number' &&
      Number.isFinite(existingBase) &&
      existingBase >= 0
    if (
      (mode !== undefined && mode !== 'ratio' && mode !== 'tiered_expr') ||
      (hasPrice && hasRatio) ||
      (hasExpression && (hasPrice || hasRatio || mode === 'ratio')) ||
      (mode === 'tiered_expr' && !hasExpression) ||
      (!hasExpression && !hasPrice && !hasRatio) ||
      (hasRatio &&
        selection.model_ratio === undefined &&
        (getLocalSyncBillingCategory(original, model) !== 'ratio' ||
          !hasUsableExistingBase))
    ) {
      throw new Error('Select a complete upstream billing configuration')
    }
    if (hasExpression) {
      maps.billing_mode[model] = 'tiered_expr'
      maps.billing_expr[model] = String(selection.billing_expr)
    } else {
      maps.billing_mode[model] = 'ratio'
      delete maps.billing_expr[model]
      if (hasPrice) {
        for (const field of RATIO_SYNC_FIELDS) delete maps[field][model]
      } else {
        delete maps.model_price[model]
      }
      for (const [field, value] of Object.entries(selection)) {
        if (NUMERIC_SYNC_FIELDS.has(field)) {
          maps[field as RatioType][model] = Number(value)
        }
      }
    }
  }
  return Object.fromEntries(
    SYNC_FIELD_ORDER.filter(
      (field) => JSON.stringify(maps[field]) !== JSON.stringify(original[field])
    ).map((field) => [
      SYNC_OPTION_KEYS[field],
      JSON.stringify(maps[field], null, 2),
    ])
  )
}

export function getSyncFieldLabel(
  ratioType: string,
  t: (key: string) => string
): string {
  const opt = RATIO_TYPE_OPTIONS.find((o) => o.value === ratioType)
  if (opt) return t(opt.label)
  return ratioType
}

export function getOrderedRatioTypes(
  ratioTypes: Partial<Record<RatioType, RatioDifferenceEntry>>,
  filter?: string
): RatioType[] {
  const keys = Object.keys(ratioTypes) as RatioType[]
  const ordered = [
    ...SYNC_FIELD_ORDER.filter((f) => keys.includes(f)),
    ...keys.filter((f) => !SYNC_FIELD_ORDER.includes(f)),
  ]
  if (!filter || filter === '__all__') return ordered
  return ordered.filter((f) => f === filter)
}

export function getPreferredSyncField(
  ratioTypes: Partial<Record<RatioType, RatioDifferenceEntry>>,
  ratioType: RatioType,
  sourceName: string
): RatioType {
  const exprValue = ratioTypes.billing_expr?.upstreams?.[sourceName]
  if (
    ratioTypes.billing_mode?.upstreams?.[sourceName] !== 'ratio' &&
    ratioType !== 'billing_expr' &&
    exprValue !== null &&
    exprValue !== undefined &&
    exprValue !== 'same'
  ) {
    return 'billing_expr'
  }
  return ratioType
}

export function getVisibleRatioTypesForSource(
  ratioTypes: Partial<Record<RatioType, RatioDifferenceEntry>>,
  sourceName: string,
  filter?: string
): RatioType[] {
  return getOrderedRatioTypes(ratioTypes, filter).filter(
    (ratioType) =>
      getPreferredSyncField(ratioTypes, ratioType, sourceName) === ratioType
  )
}

export function getAlignedRatioTypes(
  ratioTypes: Partial<Record<RatioType, RatioDifferenceEntry>>,
  sourceNames: string[],
  filter?: string
): RatioType[] {
  const ordered = getOrderedRatioTypes(ratioTypes, filter)
  if (sourceNames.length === 0) return ordered

  const visible = new Set<RatioType>()
  sourceNames.forEach((sourceName) => {
    getVisibleRatioTypesForSource(ratioTypes, sourceName, filter).forEach(
      (ratioType) => visible.add(ratioType)
    )
  })

  return ordered.filter((ratioType) => visible.has(ratioType))
}

export function getBillingCategory(
  ratioType: string
): 'price' | 'ratio' | 'tiered' {
  if (ratioType === 'model_price') return 'price'
  if (ratioType === 'billing_mode' || ratioType === 'billing_expr') {
    return 'tiered'
  }
  return 'ratio'
}

export function isSelectableUpstreamValue(
  value: number | string | 'same' | null | undefined
): boolean {
  return value !== null && value !== undefined && value !== 'same'
}

export function isBulkSelectableUpstreamField(
  fields: Partial<Record<RatioType, RatioDifferenceEntry>>,
  ratioType: RatioType,
  sourceName: string
) {
  // A partially trusted model must not be imported by a column-wide action.
  return (
    !Object.values(fields).some(
      (field) => field?.confidence?.[sourceName] === false
    ) && isSelectableUpstreamValue(fields[ratioType]?.upstreams?.[sourceName])
  )
}

export function getUpstreamDisplayName(sourceName: string): string {
  const synthesizedPresets = [
    { name: OFFICIAL_CHANNEL_NAME, id: OFFICIAL_CHANNEL_ID, label: 'basellm' },
    {
      name: MODELS_DEV_PRESET_NAME,
      id: MODELS_DEV_PRESET_ID,
      label: 'models.dev',
    },
  ]

  for (const preset of synthesizedPresets) {
    if (
      sourceName === preset.name ||
      sourceName === `${preset.name}(${preset.id})`
    ) {
      return preset.label
    }
  }

  return sourceName
}

export function isSelectedResolutionValue(
  resolutions: ResolutionsMap,
  model: string,
  ratioType: RatioType,
  upstreamValue: number | string | 'same' | null | undefined
): boolean {
  if (!isSelectableUpstreamValue(upstreamValue)) return false

  const selectedValue = resolutions[model]?.[ratioType]
  if (selectedValue === undefined) return false

  if (NUMERIC_SYNC_FIELDS.has(ratioType)) {
    const selectedNumber = Number(selectedValue)
    const upstreamNumber = Number(upstreamValue)
    return (
      Number.isFinite(selectedNumber) &&
      Number.isFinite(upstreamNumber) &&
      selectedNumber === upstreamNumber
    )
  }

  return selectedValue === upstreamValue
}

export function deleteResolutionField(
  resolutions: ResolutionsMap,
  model: string,
  ratioType: RatioType
): ResolutionsMap {
  return applyResolutionRemovals(resolutions, [{ model, ratioType }])
}

function getDraftModelResolution(
  drafts: Map<string, Record<string, number | string>>,
  resolutions: ResolutionsMap,
  model: string
): Record<string, number | string> {
  const existingDraft = drafts.get(model)
  if (existingDraft) return existingDraft

  const draft = resolutions[model] ? { ...resolutions[model] } : {}
  drafts.set(model, draft)
  return draft
}

function applyResolutionSelectionToDraft(
  drafts: Map<string, Record<string, number | string>>,
  resolutions: ResolutionsMap,
  differences: Record<string, Partial<Record<RatioType, RatioDifferenceEntry>>>,
  selection: ResolutionSelection
) {
  const modelDiffs = differences[selection.model]
  const preferredType = getPreferredSyncField(
    modelDiffs || {},
    selection.ratioType,
    selection.sourceName
  )
  const preferredValue =
    preferredType === selection.ratioType
      ? selection.value
      : (modelDiffs?.[preferredType]?.upstreams?.[selection.sourceName] ??
        selection.value)

  const finalType = preferredType
  const finalValue = preferredValue as number | string
  const category = getBillingCategory(finalType)
  const newModelRes = getDraftModelResolution(
    drafts,
    resolutions,
    selection.model
  )

  Object.keys(newModelRes).forEach((rt) => {
    if (finalType === 'billing_mode' && finalValue === 'ratio') {
      if (rt === 'billing_expr') delete newModelRes[rt]
    } else if (getBillingCategory(rt) !== category) {
      delete newModelRes[rt]
    }
  })

  newModelRes[finalType] = finalValue

  if (category === 'tiered' && finalValue !== 'ratio' && modelDiffs) {
    const modeVal = modelDiffs.billing_mode?.upstreams?.[selection.sourceName]
    const exprVal = modelDiffs.billing_expr?.upstreams?.[selection.sourceName]
    if (modeVal !== undefined && modeVal !== null && modeVal !== 'same') {
      newModelRes['billing_mode'] = modeVal
    } else if (finalType === 'billing_expr') {
      newModelRes['billing_mode'] = 'tiered_expr'
    }
    if (exprVal !== undefined && exprVal !== null && exprVal !== 'same') {
      newModelRes['billing_expr'] = exprVal
    }
  }
}

export function resolveResolutionSelection(
  differences: Record<string, Partial<Record<RatioType, RatioDifferenceEntry>>>,
  selection: ResolutionSelection
): ResolvedResolutionSelection {
  const modelDiffs = differences[selection.model]
  const preferredType = getPreferredSyncField(
    modelDiffs || {},
    selection.ratioType,
    selection.sourceName
  )
  const preferredValue =
    preferredType === selection.ratioType
      ? selection.value
      : (modelDiffs?.[preferredType]?.upstreams?.[selection.sourceName] ??
        selection.value)

  return {
    ...selection,
    ratioType: preferredType,
    value: preferredValue as number | string,
  }
}

export function getEffectiveResolutionSelections(
  differences: Record<string, Partial<Record<RatioType, RatioDifferenceEntry>>>,
  selections: ResolutionSelection[]
): ResolvedResolutionSelection[] {
  const effectiveByKey = new Map<string, ResolvedResolutionSelection>()

  selections.forEach((selection) => {
    const resolved = resolveResolutionSelection(differences, selection)
    const category = getBillingCategory(resolved.ratioType)

    if (resolved.ratioType !== 'billing_mode' || resolved.value !== 'ratio') {
      for (const [key, existing] of effectiveByKey) {
        if (
          existing.model === resolved.model &&
          getBillingCategory(existing.ratioType) !== category
        ) {
          effectiveByKey.delete(key)
        }
      }
    }

    effectiveByKey.set(`${resolved.model}\u0000${resolved.ratioType}`, resolved)
  })

  return [...effectiveByKey.values()]
}

export function applyResolutionSelections(
  resolutions: ResolutionsMap,
  differences: Record<string, Partial<Record<RatioType, RatioDifferenceEntry>>>,
  selections: ResolutionSelection[]
): ResolutionsMap {
  if (selections.length === 0) return resolutions

  const next = { ...resolutions }
  const drafts = new Map<string, Record<string, number | string>>()

  selections.forEach((selection) => {
    applyResolutionSelectionToDraft(drafts, resolutions, differences, selection)
  })

  drafts.forEach((draft, model) => {
    if (Object.keys(draft).length === 0) {
      delete next[model]
    } else {
      next[model] = draft
    }
  })

  return next
}

export function applyResolutionSelection(
  resolutions: ResolutionsMap,
  differences: Record<string, Partial<Record<RatioType, RatioDifferenceEntry>>>,
  selection: ResolutionSelection
): ResolutionsMap {
  return applyResolutionSelections(resolutions, differences, [selection])
}

export function applyResolutionRemovals(
  resolutions: ResolutionsMap,
  removals: ResolutionRemoval[]
): ResolutionsMap {
  if (removals.length === 0) return resolutions

  const plan: ResolutionRemovalPlan = new Map()
  removals.forEach((removal) => {
    const ratioTypes = plan.get(removal.model)
    if (ratioTypes) {
      ratioTypes.add(removal.ratioType)
    } else {
      plan.set(removal.model, new Set([removal.ratioType]))
    }
  })

  return applyResolutionRemovalPlan(resolutions, plan)
}

export function applyResolutionRemovalPlan(
  resolutions: ResolutionsMap,
  plan: ResolutionRemovalPlan
): ResolutionsMap {
  if (plan.size === 0) return resolutions

  const next = { ...resolutions }

  plan.forEach((ratioTypes, model) => {
    const current = resolutions[model]
    if (!current) return

    const draft = { ...current }
    ratioTypes.forEach((ratioType) => {
      delete draft[ratioType]
      if (ratioType === 'billing_expr') delete draft['billing_mode']
      if (ratioType === 'billing_mode') delete draft['billing_expr']
    })
    if (Object.keys(draft).length === 0) {
      delete next[model]
    } else {
      next[model] = draft
    }
  })

  return next
}
