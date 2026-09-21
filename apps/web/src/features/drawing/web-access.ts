/*
Copyright (C) 2026 LIghtJUNction
*/
import type { DrawingWebAccess } from '../assistant/api'

export const WEB_DRAWING_MINIMUM_USD = 10

export function resolveDrawingWebAccess(
  access: DrawingWebAccess | undefined,
  quota: number | undefined,
  quotaPerUSD: number
): DrawingWebAccess {
  if (access) {
    const balance =
      typeof access.balance_usd === 'number' &&
      Number.isFinite(access.balance_usd)
        ? access.balance_usd
        : null
    return {
      minimum_balance_usd: WEB_DRAWING_MINIMUM_USD,
      balance_usd: balance,
      allowed:
        access.allowed === true &&
        balance !== null &&
        balance >= WEB_DRAWING_MINIMUM_USD,
    }
  }
  // Wallet quota can inform the balance display during rollout, but cannot
  // authorize generation without the server's drawing-specific access check.
  const balance =
    typeof quota === 'number' &&
    Number.isFinite(quota) &&
    Number.isFinite(quotaPerUSD) &&
    quotaPerUSD > 0
      ? quota / quotaPerUSD
      : null
  return {
    minimum_balance_usd: WEB_DRAWING_MINIMUM_USD,
    balance_usd: balance,
    allowed: false,
  }
}

export function getDrawingWebDenial(
  error: unknown
): DrawingWebAccess | undefined {
  const payload = (
    error as {
      response?: {
        data?: {
          error?: { code?: string }
          drawing_web_access?: DrawingWebAccess
        }
      }
    }
  )?.response?.data
  if (payload?.error?.code !== 'WEB_DRAWING_MINIMUM_BALANCE') return undefined
  return {
    ...resolveDrawingWebAccess(payload.drawing_web_access, undefined, 0),
    allowed: false,
  }
}
