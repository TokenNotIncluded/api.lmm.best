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
import { z } from 'zod'

import type { getUserGroups } from '@/lib/api'

import type { AssistantCreateKeyAction } from './api'

export type AssistantGroupWarning = {
  enabled: boolean
  message: string
  mode: 'modal' | 'banner' | 'inline'
  confirmations: number
}

export type AssistantSelectableGroup = {
  id: string
  warning?: AssistantGroupWarning
}

type UserGroupsPayload = Awaited<ReturnType<typeof getUserGroups>>

const groupWarningSchema = z.strictObject({
  enabled: z.boolean(),
  message: z.string().trim().min(1),
  mode: z.enum(['modal', 'banner', 'inline']),
  confirmations: z.number().int().min(1).max(3),
})

const groupCatalogueSchema = z.record(
  z.string(),
  z.strictObject({
    desc: z.string().trim().min(1),
    // Zod number schemas reject NaN and infinities before range checks.
    ratio: z.number().nonnegative(),
    warning: groupWarningSchema.optional(),
  })
)

const exactTrimmedString = (maxLength: number) =>
  z
    .string()
    .min(1)
    .max(maxLength)
    .refine((value) => value === value.trim())

const preparedActionSchema = z.strictObject({
  type: z.literal('create_key'),
  confirmation_token: exactTrimmedString(512),
  requires_confirmation: z.literal(true),
  expires_in_seconds: z.number().positive(),
  name: exactTrimmedString(50),
  group: exactTrimmedString(128).refine(
    (group) => group.toLowerCase() !== 'auto'
  ),
  conversation_id: z.number().int().nonnegative().optional(),
  ui_path: z.literal('/keys').optional(),
})

export function selectableAssistantKeyGroups(
  payload: UserGroupsPayload | undefined
): AssistantSelectableGroup[] {
  if (!payload?.success) return []
  const catalogue = groupCatalogueSchema.safeParse(payload.data)
  if (!catalogue.success) return []
  const groups = new Map<string, AssistantSelectableGroup>()
  for (const [rawId, details] of Object.entries(catalogue.data)) {
    const id = rawId.trim()
    if (!id || id !== rawId || id.toLowerCase() === 'auto') continue
    groups.set(
      id,
      details.warning?.enabled ? { id, warning: details.warning } : { id }
    )
  }
  return [...groups.values()].sort((left, right) =>
    left.id.localeCompare(right.id)
  )
}

export function isAuthoritativePreparedKeyAction(
  value: AssistantCreateKeyAction | null | undefined,
  liveGroups: readonly AssistantSelectableGroup[],
  requested?: { name: string; group: string }
): value is AssistantCreateKeyAction {
  const parsed = preparedActionSchema.safeParse(value)
  if (!parsed.success) return false
  const action = parsed.data
  if (!liveGroups.some((option) => option.id === action.group)) return false
  return (
    !requested ||
    (requested.name === action.name && requested.group === action.group)
  )
}
