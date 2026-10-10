/*
Copyright (C) 2026 LIghtJUNction
*/
export const SMS_MINIMUM_BALANCE_LEGACY_UNITS = 10
export const SMS_MINIMUM_BALANCE_CODE = 'TEMPORARY_SMS_MINIMUM_BALANCE'

export function getSmsPurchaseBalance(quota: unknown, quotaPerUnit: number) {
  // Preserve the established raw purchase boundary; display its denomination separately.
  if (
    typeof quota !== 'number' ||
    !Number.isFinite(quota) ||
    !Number.isFinite(quotaPerUnit) ||
    quotaPerUnit <= 0
  ) {
    return {
      status: 'unknown',
      balanceQuota: undefined,
    } as const
  }
  return {
    status:
      quota >= SMS_MINIMUM_BALANCE_LEGACY_UNITS * quotaPerUnit
        ? 'allowed'
        : 'below-minimum',
    balanceQuota: quota,
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
