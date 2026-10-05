/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { mapStatusDataToConfig } from '@/hooks/use-system-config'
import i18n from '@/i18n/config'
import { getStatus } from '@/lib/api'
import { getCurrencyDisplay } from '@/lib/currency'
import { api } from '@/lib/http-client'
import { useSystemConfigStore } from '@/stores/system-config-store'

import type { UpdateOptionRequest, UpdateOptionResponse } from '../types'

const endpoint = '/api/option/public-credit-unit'
const silent = { skipBusinessError: true, skipErrorHandler: true }

export function parsePublicCreditOption(value: unknown): number | undefined {
  if (typeof value !== 'string' || !/^\d+$/.test(value)) return undefined
  const number = Number(value)
  return number === 500000 ? number : undefined
}

function decodeCreditUnit(data: unknown) {
  const mapped = mapStatusDataToConfig(
    data as Parameters<typeof mapStatusDataToConfig>[0]
  ).currency
  const config = mapped && getCurrencyDisplay(mapped).config
  if (
    !config ||
    config.creditUnitSchemaVersion !== 2 ||
    !Number.isFinite(config.creditsPerUsd) ||
    Number(config.creditsPerUsd) <= 0 ||
    !Number.isSafeInteger(config.publicCreditsPerUsd) ||
    Number(config.publicCreditsPerUsd) <= 0 ||
    typeof config.ledgerQuotaPerUsdExact !== 'string' ||
    typeof config.publicCreditsPerUsdExact !== 'string'
  ) {
    throw new Error(
      i18n.t('Public credit settings are unavailable on this server.')
    )
  }
  return {
    ledgerQuotaPerUsd: Number(config.creditsPerUsd),
    publicCreditsPerUsd: Number(config.publicCreditsPerUsd),
    ledgerQuotaPerUsdExact: config.ledgerQuotaPerUsdExact,
    publicCreditsPerUsdExact: config.publicCreditsPerUsdExact,
  }
}

/** A dedicated v2 endpoint prevents an older node from acknowledging an unknown generic option. */
export async function updatePublicCreditUnitOption(
  request: UpdateOptionRequest
): Promise<UpdateOptionResponse> {
  const value = parsePublicCreditOption(String(request.value))
  const baseline = request.publicCreditUnitBaseline
  if (
    request.key !== 'PublicCreditsPerUSD' ||
    value === undefined ||
    !baseline ||
    !Number.isSafeInteger(baseline.publicCreditsPerUsd) ||
    baseline.publicCreditsPerUsd <= 0 ||
    !Number.isFinite(baseline.ledgerQuotaPerUsd) ||
    baseline.ledgerQuotaPerUsd <= 0
  ) {
    throw new Error(
      i18n.t('Used for stored balances and billing. This value is fixed.')
    )
  }
  const before = await api.get(endpoint, silent).catch(() => {
    throw new Error(
      i18n.t('Public credit settings are unavailable on this server.')
    )
  })
  if (before.data?.success !== true) {
    throw new Error(
      i18n.t('Public credit settings are unavailable on this server.')
    )
  }
  const current = decodeCreditUnit(before.data.data)
  if (
    current.publicCreditsPerUsd !== baseline.publicCreditsPerUsd ||
    current.ledgerQuotaPerUsd !== baseline.ledgerQuotaPerUsd
  ) {
    throw new Error(
      i18n.t('Public credit settings changed. Refresh before saving again.')
    )
  }
  const response = await api
    .put(
      endpoint,
      {
        credit_unit_schema_version: 2,
        public_credits_per_usd_exact: String(value),
        expected_public_credits_per_usd_exact: current.publicCreditsPerUsdExact,
        expected_ledger_quota_per_usd_exact: current.ledgerQuotaPerUsdExact,
      },
      silent
    )
    .catch((error: unknown) => {
      const status = (error as { response?: { status?: number } })?.response
        ?.status
      if (status === 409) {
        throw new Error(
          i18n.t('Public credit settings changed. Refresh before saving again.')
        )
      }
      if (status === 404) {
        throw new Error(
          i18n.t('Public credit settings are unavailable on this server.')
        )
      }
      throw error
    })
  if (response.data?.success !== true) {
    throw Object.assign(
      new Error(response.data?.message || i18n.t('Failed to update setting')),
      {
        response: { data: response.data },
      }
    )
  }
  const unverified = () =>
    new Error(
      i18n.t(
        'Credit display update could not be verified. Refresh before saving again.'
      )
    )
  let status: Awaited<ReturnType<typeof getStatus>>
  try {
    const saved = decodeCreditUnit(response.data.data)
    if (
      saved.publicCreditsPerUsd !== value ||
      saved.ledgerQuotaPerUsd !== current.ledgerQuotaPerUsd
    ) {
      throw unverified()
    }
    status = await getStatus()
    const refreshed = decodeCreditUnit(status)
    if (
      refreshed.publicCreditsPerUsd !== value ||
      refreshed.ledgerQuotaPerUsd !== current.ledgerQuotaPerUsd
    ) {
      throw unverified()
    }
  } catch {
    throw unverified()
  }
  // Only the verified live status can update display conversion; no optimistic face value.
  useSystemConfigStore
    .getState()
    .setConfig(
      mapStatusDataToConfig(
        status as Parameters<typeof mapStatusDataToConfig>[0]
      )
    )
  return {
    success: true,
    message: response.data.message || '',
    warnings: response.data.warnings,
  }
}
