/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import type { User } from '../types'

type WalletRisk = NonNullable<User['wallet_risk']>
type TopupMethod = NonNullable<User['topup_summary']>['methods'][number]

function object(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function nonnegativeInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
}

/** Invalid display data is unknown; it must never become a zero risk score. */
export function readableWalletRisk(value: unknown): value is WalletRisk {
  if (!object(value)) return false
  return (
    typeof value.score === 'number' &&
    Number.isFinite(value.score) &&
    value.score >= 0 &&
    value.score <= 1 &&
    typeof value.high_risk === 'boolean' &&
    nonnegativeInteger(value.version) &&
    Array.isArray(value.reasons) &&
    value.reasons.every((reason) => typeof reason === 'string') &&
    [
      'checkin_quota',
      'checkin_count',
      'transferred_quota',
      'pending_quota',
      'received_quota',
      'high_risk_senders',
    ].every((key) => nonnegativeInteger(value[key])) &&
    ['moderation_reviewed_count', 'moderation_flagged_count'].every(
      (key) => value[key] === undefined || nonnegativeInteger(value[key])
    )
  )
}

/** Null preserves the legacy empty list; malformed metadata stays unavailable. */
export function readableTopupMethods(value: unknown): TopupMethod[] | null {
  if (value === undefined || value === null) return []
  if (!Array.isArray(value)) return null
  if (
    !value.every(
      (method) =>
        object(method) &&
        typeof method.method === 'string' &&
        (method.provider === undefined ||
          typeof method.provider === 'string') &&
        (method.settlement_currency === undefined ||
          typeof method.settlement_currency === 'string') &&
        ['quota', 'money_micros', 'orders'].every((key) =>
          nonnegativeInteger(method[key])
        )
    )
  ) {
    return null
  }
  return value as TopupMethod[]
}

/** Currency-only callers do not require the rest of a method's amount DTO. */
export function readableTopupCurrencies(value: unknown): string[] | null {
  if (value === undefined || value === null) return []
  if (
    !Array.isArray(value) ||
    !value.every(
      (method) =>
        object(method) &&
        (method.settlement_currency === undefined ||
          typeof method.settlement_currency === 'string')
    )
  ) {
    return null
  }
  return value.map(
    (method) => method.settlement_currency?.trim().toUpperCase() || 'UNKNOWN'
  )
}
