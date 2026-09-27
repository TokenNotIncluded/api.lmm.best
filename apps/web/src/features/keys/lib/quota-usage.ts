/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import type { ApiKey } from '../types'

/** Remaining-quota share at or below which the bar turns destructive. */
export const QUOTA_DANGER_PERCENT = 10
/** Remaining-quota share at or below which the bar turns warning. */
export const QUOTA_WARNING_PERCENT = 30

export type QuotaUsage = {
  used: number
  remaining: number
  /** Lifetime allowance, i.e. used + remaining. */
  total: number
  /** Share of the allowance still available, 0-100. */
  remainingPercent: number
  /** Share of the allowance already spent, 0-100. */
  usedPercent: number
}

/**
 * Derive a key's quota breakdown. Unlimited keys have no meaningful
 * percentage, so callers should branch on `unlimited_quota` before using this.
 */
export function getQuotaUsage(
  apiKey: Pick<ApiKey, 'used_quota' | 'remain_quota'>
): QuotaUsage {
  const used = Number.isFinite(apiKey.used_quota) ? apiKey.used_quota : 0
  const remaining = Number.isFinite(apiKey.remain_quota)
    ? apiKey.remain_quota
    : 0
  const total = used + remaining
  const remainingPercent = total > 0 ? (remaining / total) * 100 : 0
  return {
    used,
    remaining,
    total,
    remainingPercent,
    usedPercent: total > 0 ? (used / total) * 100 : 0,
  }
}

/**
 * Semantic progress-bar class for a remaining-quota percentage. Single source
 * of truth so the table column and the mobile usage bar cannot disagree.
 */
export function getQuotaProgressColor(remainingPercent: number): string {
  if (remainingPercent <= QUOTA_DANGER_PERCENT) {
    return 'console-status-progress-danger'
  }
  if (remainingPercent <= QUOTA_WARNING_PERCENT) {
    return 'console-status-progress-warning'
  }
  return 'console-status-progress-success'
}

/**
 * How urgent a key's remaining allowance is, as a coarse band. Used for the
 * status text/icon pair (never colour alone) on compact layouts.
 */
export function getQuotaBand(
  remainingPercent: number
): 'healthy' | 'low' | 'critical' {
  if (remainingPercent <= QUOTA_DANGER_PERCENT) return 'critical'
  if (remainingPercent <= QUOTA_WARNING_PERCENT) return 'low'
  return 'healthy'
}
