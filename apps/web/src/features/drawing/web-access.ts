/*
Copyright (C) 2026 LIghtJUNction
*/
import type { DrawingWebAccess } from '../assistant/api'

const isQuota = (value: unknown): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value)

export function resolveDrawingWebAccess(
  access: DrawingWebAccess | undefined,
  quota: number | undefined,
  creditsPerUSD: number
): DrawingWebAccess {
  if (access) {
    const minimumCredit =
      isQuota(access.minimum_balance_credit) &&
      access.minimum_balance_credit >= 0
        ? access.minimum_balance_credit
        : null
    const hasCreditGate = minimumCredit !== null
    const balanceCredit = isQuota(access.balance_credit)
      ? access.balance_credit
      : null
    const minimumUSD =
      hasCreditGate &&
      typeof access.minimum_balance_usd === 'number' &&
      Number.isFinite(access.minimum_balance_usd) &&
      access.minimum_balance_usd >= 0
        ? access.minimum_balance_usd
        : null
    const balanceUSD =
      hasCreditGate &&
      typeof access.balance_usd === 'number' &&
      Number.isFinite(access.balance_usd)
        ? access.balance_usd
        : null
    return {
      minimum_balance_usd: minimumUSD,
      minimum_balance_credit: minimumCredit,
      balance_usd: balanceUSD,
      balance_credit: balanceCredit,
      allowed:
        access.allowed === true &&
        minimumUSD !== null &&
        balanceUSD !== null &&
        balanceCredit !== null &&
        minimumCredit !== null &&
        balanceCredit >= minimumCredit,
    }
  }
  // A calibrated wallet balance can inform display, but only the server's
  // drawing-specific raw Credit gate can authorize browser generation.
  const balance =
    typeof quota === 'number' &&
    Number.isSafeInteger(quota) &&
    Number.isFinite(creditsPerUSD) &&
    creditsPerUSD > 0
      ? quota / creditsPerUSD
      : null
  return {
    minimum_balance_usd: null,
    minimum_balance_credit: null,
    balance_usd: balance,
    balance_credit: isQuota(quota) ? quota : null,
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
