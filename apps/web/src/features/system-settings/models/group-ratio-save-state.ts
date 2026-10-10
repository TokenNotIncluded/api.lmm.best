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
/*
Copyright (C) 2026 LIghtJUNction
*/
import type { GroupRatioOptionValues } from './group-ratio-option-values'
import { formatJsonForTextarea, normalizeJsonString } from './utils'

const jsonFields = [
  'GroupRatio',
  'TopupGroupRatio',
  'UserUsableGroups',
  'GroupGroupRatio',
  'AutoGroups',
  'GroupSpecialUsableGroup',
  'GroupWarnings',
] as const

function mapJsonFields(
  values: GroupRatioOptionValues,
  map: (value: string) => string
): GroupRatioOptionValues {
  const result = { ...values }
  for (const key of jsonFields) result[key] = map(values[key])
  return result
}

export function normalizeGroupRatioValues(values: GroupRatioOptionValues) {
  return mapJsonFields(values, normalizeJsonString)
}

export function formatGroupRatioValues(values: GroupRatioOptionValues) {
  return mapJsonFields(values, formatJsonForTextarea)
}

// Only replace fields the administrator has not edited since this baseline.
// For a save response, the baseline is the submitted snapshot, not the older
// server snapshot: edits made while the request was pending must remain dirty.
export function mergeGroupRatioDraft(
  draft: GroupRatioOptionValues,
  baseline: GroupRatioOptionValues,
  incoming: GroupRatioOptionValues
): GroupRatioOptionValues {
  const normalizedDraft = normalizeGroupRatioValues(draft)
  const normalizedBaseline = normalizeGroupRatioValues(baseline)
  const result = formatGroupRatioValues(incoming)
  const keys = Object.keys(result) as Array<keyof GroupRatioOptionValues>
  for (const key of keys) {
    if (normalizedDraft[key] !== normalizedBaseline[key]) {
      Object.assign(result, { [key]: draft[key] })
    }
  }
  return result
}
