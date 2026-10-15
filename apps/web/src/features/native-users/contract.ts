/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { z } from 'zod'

import { NativeUsersFailure } from './access'

// Exact literal from the frozen Core CONTRACT-v2, never a client permission grant.
export const NATIVE_USERS_CAPABILITIES = {
  filters: ['keyword', 'group', 'role', 'status', 'service_level'],
  sorts: [
    'id',
    'username',
    'display_name',
    'core_created_at',
    'registered_at',
    'groups',
    'service_level',
    'role',
    'status',
  ],
  implemented_write_actions: [],
  unavailable_projections: [
    'legacy_group',
    'legacy_trust',
    'used_quota',
    'normalized_used_quota',
    'request_count',
    'wallet_risk',
    'transfer_history',
    'checkin_history',
    'topup_summary',
    'referral_rewards',
    'referral_counts',
    'referral_history',
    'deleted_at',
    'last_login_at',
    'oauth_bindings',
    'assistant_profile',
    'assistant_conversations',
    'assistant_violations',
    'permission_matrix',
    'user_detail',
  ],
  unavailable_write_actions: [
    'create',
    'update',
    'delete',
    'enable',
    'promote',
    'demote',
    'adjust_money',
    'set_trust_level',
    'reset_onboarding',
    'normal_ban',
    'ban_abuse',
    'restore_referral',
    'inviter_penalty',
    'revoke_rewards',
    'unbind',
    'reset_passkey',
    'reset_2fa',
    'assistant_policy',
    'reset_assistant_violations',
    'subscription_mutations',
    'account_action_requests',
  ],
} as const

const encoder = new TextEncoder()
// Unicode mode treats valid surrogate pairs as one astral code point; only lone surrogates match.
const validUnicode = (value: string) => !/[\ud800-\udfff]/u.test(value)
const text = z.string().max(65536).refine(validUnicode)
const id = z
  .string()
  .regex(/^[1-9][0-9]*$/)
  .max(64)
const safeInteger = z.number().int().safe()
const total = safeInteger.nonnegative()
const hasControls = (value: string) =>
  [...value].some((character) => {
    const code = character.charCodeAt(0)
    return code <= 31 || (code >= 127 && code <= 159)
  })
const group = z
  .string()
  .refine(
    (value) =>
      validUnicode(value) &&
      [...value].length >= 1 &&
      [...value].length <= 64 &&
      encoder.encode(value).length <= 256 &&
      !hasControls(value)
  )
const units = z
  .string()
  .refine(
    (value) =>
      value.length <= 17 &&
      /^(?:0|[1-9][0-9]*|-[1-9][0-9]*)$/.test(value) &&
      BigInt(value) >= -9007199254740991n &&
      BigInt(value) <= 9007199254740991n
  )
const magnitude = units.refine((value) => !value.startsWith('-'))

function utf8Compare(left: string, right: string): number {
  const a = encoder.encode(left)
  const b = encoder.encode(right)
  for (let index = 0; index < Math.min(a.length, b.length); index++) {
    if (a[index] !== b[index]) return a[index] - b[index]
  }
  return a.length - b.length
}
function uniqueCOrder(values: string[]): boolean {
  return values.every(
    (value, index) => index === 0 || utf8Compare(values[index - 1], value) < 0
  )
}
const groups = z.array(group).refine(uniqueCOrder)
const exactStrings = (expected: readonly string[]) =>
  z
    .array(z.string())
    .refine(
      (values) =>
        values.length === expected.length &&
        values.every((value, index) => value === expected[index])
    )

export const nativeUsersCapabilitiesSchema = z
  .object({
    filters: exactStrings(NATIVE_USERS_CAPABILITIES.filters),
    sorts: exactStrings(NATIVE_USERS_CAPABILITIES.sorts),
    implemented_write_actions: z.array(z.never()).length(0),
    unavailable_projections: exactStrings(
      NATIVE_USERS_CAPABILITIES.unavailable_projections
    ),
    unavailable_write_actions: exactStrings(
      NATIVE_USERS_CAPABILITIES.unavailable_write_actions
    ),
  })
  .strict()

export const nativeWalletSchema = z.discriminatedUnion('status', [
  z
    .object({
      status: z.literal('available'),
      unit: z.literal('core_credit'),
      available_units: units,
      wallet_units: magnitude,
      reserved_units: magnitude,
      debt_units: magnitude,
      money_basis: z.literal('unknown'),
      money_available: z.literal(false),
      currency: z.null(),
      provenance: z
        .object({
          reader: z.literal('financial_profile.read_wallet.v1'),
          user_id: id,
          personal_account_id: id,
        })
        .strict(),
    })
    .strict(),
  z
    .object({
      status: z.literal('unavailable'),
      code: z.enum([
        'WALLET_SUBJECT_UNAVAILABLE',
        'WALLET_PROJECTION_UNAVAILABLE',
      ]),
    })
    .strict(),
])

export const nativeUserSchema = z
  .object({
    id,
    personal_account_id: id,
    auth_version: id,
    profile_available: z.boolean(),
    username: text.nullable(),
    display_name: text.nullable(),
    email: text.nullable(),
    platform_role: z.enum(['user', 'admin', 'superadmin']),
    role: z.union([z.literal(1), z.literal(10), z.literal(100)]),
    active: z.boolean(),
    status: z.union([z.literal(1), z.literal(2)]),
    account_active: z.boolean(),
    service_level: z.union([
      z.literal(0),
      z.literal(1),
      z.literal(2),
      z.literal(3),
      z.literal(4),
    ]),
    developer_access_granted: z.boolean(),
    groups,
    core_account_created_at_unix_ms: safeInteger,
    registered_at_unix_ms: safeInteger.nullable(),
    wallet: nativeWalletSchema,
  })
  .strict()
  .superRefine((row, context) => {
    const role = { user: 1, admin: 10, superadmin: 100 }[row.platform_role]
    if (row.role !== role || row.status !== (row.active ? 1 : 2)) {
      context.addIssue({
        code: 'custom',
        message: 'Inconsistent identity projection',
      })
    }
    if (
      !row.profile_available &&
      [row.username, row.display_name, row.email].some(
        (value) => value !== null
      )
    ) {
      context.addIssue({
        code: 'custom',
        message: 'Unavailable profile has fields',
      })
    }
    if (
      row.wallet.status === 'available' &&
      (!row.active ||
        !row.account_active ||
        row.wallet.provenance.user_id !== row.id ||
        row.wallet.provenance.personal_account_id !== row.personal_account_id)
    ) {
      context.addIssue({ code: 'custom', message: 'Wallet subject mismatch' })
    }
  })

export const nativeUsersResponseSchema = z
  .object({
    version: z.literal(1),
    items: z.array(nativeUserSchema).max(100),
    total,
    page: safeInteger.min(1).max(100000),
    page_size: safeInteger.min(1).max(100),
    capabilities: nativeUsersCapabilitiesSchema,
  })
  .strict()
  .refine(
    (value) =>
      value.items.length <= value.page_size &&
      value.items.length <=
        Math.max(0, value.total - (value.page - 1) * value.page_size) &&
      new Set(value.items.map((row) => row.id)).size === value.items.length
  )

export const nativeGroupsResponseSchema = z
  .object({
    version: z.literal(1),
    items: groups,
    total,
    page: safeInteger.min(1).max(100000),
    page_size: safeInteger.min(1).max(100),
    source: z.literal('enabled_explicit_user_routing_grants'),
  })
  .strict()
  .refine(
    (value) =>
      value.items.length <= value.page_size &&
      value.items.length <=
        Math.max(0, value.total - (value.page - 1) * value.page_size)
  )

export type NativeUser = z.infer<typeof nativeUserSchema>
export type NativeWallet = z.infer<typeof nativeWalletSchema>
export type NativeUsersResponse = z.infer<typeof nativeUsersResponseSchema>
export type NativeGroupsResponse = z.infer<typeof nativeGroupsResponseSchema>
export type NativeUsersSort = (typeof NATIVE_USERS_CAPABILITIES.sorts)[number]
export type NativeUsersQuery = {
  keyword: string
  group: string
  role: '' | 'user' | 'admin' | 'superadmin'
  status: '' | 'active' | 'disabled'
  service_level: '' | '0' | '1' | '2' | '3' | '4'
  page: number
  page_size: number
  sort_by: NativeUsersSort
  sort_order: 'asc' | 'desc'
}
export const DEFAULT_NATIVE_USERS_QUERY: NativeUsersQuery = {
  keyword: '',
  group: '',
  role: '',
  status: '',
  service_level: '',
  page: 1,
  page_size: 20,
  sort_by: 'id',
  sort_order: 'desc',
}

export function nativeUsersQuery(value: NativeUsersQuery): NativeUsersQuery {
  const expected = Object.keys(DEFAULT_NATIVE_USERS_QUERY)
  if (
    typeof value !== 'object' ||
    value === null ||
    Array.isArray(value) ||
    Object.keys(value).length !== expected.length ||
    expected.some((key) => !Object.hasOwn(value, key)) ||
    typeof value.keyword !== 'string' ||
    typeof value.group !== 'string' ||
    !validUnicode(value.keyword) ||
    encoder.encode(value.keyword.trim()).length > 256 ||
    hasControls(value.keyword) ||
    (value.group !== '' && !group.safeParse(value.group).success) ||
    !['', 'user', 'admin', 'superadmin'].includes(value.role) ||
    !['', 'active', 'disabled'].includes(value.status) ||
    !['', '0', '1', '2', '3', '4'].includes(value.service_level) ||
    !Number.isSafeInteger(value.page) ||
    value.page < 1 ||
    value.page > 100000 ||
    !Number.isSafeInteger(value.page_size) ||
    value.page_size < 1 ||
    value.page_size > 100 ||
    !(NATIVE_USERS_CAPABILITIES.sorts as readonly string[]).includes(
      value.sort_by
    ) ||
    !['asc', 'desc'].includes(value.sort_order)
  ) {
    throw new NativeUsersFailure('invalid_query')
  }
  return { ...value, keyword: value.keyword.trim() }
}
