/*
Copyright (C) 2026 LIghtJUNction
*/
export const SMS_MINIMUM_BALANCE_USD = 10
export const SMS_MINIMUM_BALANCE_CODE = 'TEMPORARY_SMS_MINIMUM_BALANCE'

export function getSmsPurchaseBalance(quota: unknown, quotaPerUnit: number) {
  // Compare wallet quota with quota per USD, never a displayed fiat amount.
  if (
    typeof quota !== 'number' ||
    !Number.isFinite(quota) ||
    !Number.isFinite(quotaPerUnit) ||
    quotaPerUnit <= 0
  ) {
    return { status: 'unknown', balanceUSD: undefined } as const
  }
  return {
    status:
      quota >= SMS_MINIMUM_BALANCE_USD * quotaPerUnit
        ? 'allowed'
        : 'below-minimum',
    balanceUSD: quota / quotaPerUnit,
  } as const
}

export function isSmsMinimumBalanceError(error: unknown): boolean {
  if (typeof error !== 'object' || error === null) return false
  // Both the SMS business envelope and an HTTP 402 Axios error carry a code.
  if ('code' in error && error.code === SMS_MINIMUM_BALANCE_CODE) return true
  if (!('response' in error)) return false
  const response = error.response
  if (
    typeof response !== 'object' ||
    response === null ||
    !('data' in response)
  ) {
    return false
  }
  const data = response.data
  return (
    typeof data === 'object' &&
    data !== null &&
    'code' in data &&
    data.code === SMS_MINIMUM_BALANCE_CODE
  )
}
