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
import { useEffect, useCallback } from 'react'

import { getStatus } from '@/lib/api'
import { DEFAULT_LOGO, isDefaultLogo, resolveSystemName } from '@/lib/constants'
import {
  useSystemConfigStore,
  type CurrencyConfig,
  type CurrencyDisplayType,
  type SystemConfig,
  DEFAULT_CURRENCY_CONFIG,
} from '@/stores/system-config-store'

interface UseSystemConfigOptions {
  /** Automatically fetch config from backend (use only in root component) */
  autoLoad?: boolean
}

interface StatusApiResponse {
  success: boolean
  data: {
    system_name?: string
    logo?: string
    footer_html?: string
    demo_site_enabled?: boolean
    display_token_stat_enabled?: boolean
    display_in_currency?: boolean
    quota_display_type?: CurrencyDisplayType
    currency_unit?: string
    credits_per_usd?: number | string
    credits_per_usd_exact?: string
    ledger_quota_per_usd?: number | string
    ledger_quota_per_usd_exact?: string
    public_credits_per_usd?: number | string
    public_credits_per_usd_exact?: string
    credit_unit_schema_version?: number
    quota_unit?: string
    public_credit_unit?: string
    legacy_credit_unit?: string
    cny_per_usd?: number | string
    legacy_pricing_units_per_usd?: number | string
    quota_per_unit?: number
    usd_exchange_rate?: number
    custom_currency_symbol?: string
    custom_currency_code?: string
    custom_currency_exchange_rate?: number
  }
}

function toNumber(value: unknown, fallback: number): number {
  if (typeof value === 'number' && !Number.isNaN(value)) return value
  if (typeof value === 'string') {
    const parsed = Number(value)
    if (!Number.isNaN(parsed)) return parsed
  }
  return fallback
}

function normalizeIsoCurrencyCode(value: unknown): string {
  if (typeof value !== 'string') return ''

  const code = value.trim().toUpperCase()
  return /^[A-Z]{3}$/.test(code) ? code : ''
}

/**
 * Map `/api/status` response data to our persisted system config structure
 */
export function mapStatusDataToConfig(
  data: StatusApiResponse['data'] | undefined
): Partial<SystemConfig> {
  if (!data) return {}

  const quotaDisplayType =
    (data.quota_display_type as CurrencyDisplayType | undefined) ??
    DEFAULT_CURRENCY_CONFIG.quotaDisplayType

  const positive = (value: unknown): number => {
    const number =
      typeof value === 'string' || typeof value === 'number'
        ? Number(value)
        : Number.NaN
    return Number.isFinite(number) && number > 0 ? number : 0
  }
  const currency: CurrencyConfig = {
    currencyUnit: data.currency_unit === 'credit' ? 'credit' : 'unknown',
    creditsPerUsd:
      data.currency_unit === 'credit' ? positive(data.credits_per_usd) : 0,
    // Explicit undefined clears any persisted v2 face value after a v1 server response.
    ledgerQuotaPerUsd:
      data.ledger_quota_per_usd === undefined
        ? undefined
        : positive(data.ledger_quota_per_usd),
    ledgerQuotaPerUsdExact: data.ledger_quota_per_usd_exact,
    publicCreditsPerUsd:
      data.public_credits_per_usd === undefined
        ? undefined
        : positive(data.public_credits_per_usd),
    publicCreditsPerUsdExact: data.public_credits_per_usd_exact,
    creditUnitSchemaVersion: data.credit_unit_schema_version,
    quotaUnit: data.quota_unit,
    publicCreditUnit: data.public_credit_unit,
    legacyCreditUnit: data.legacy_credit_unit,
    cnyPerUsd: positive(data.cny_per_usd),
    legacyPricingUnitsPerUsd: positive(data.legacy_pricing_units_per_usd),
    creditsPerUsdExact:
      data.currency_unit === 'credit' && positive(data.credits_per_usd)
        ? (data.credits_per_usd_exact ?? String(data.credits_per_usd))
        : '',
    cnyPerUsdExact: positive(data.cny_per_usd) ? String(data.cny_per_usd) : '',
    displayInCurrency:
      data.display_in_currency ?? DEFAULT_CURRENCY_CONFIG.displayInCurrency,
    quotaDisplayType,
    quotaPerUnit: toNumber(
      data.quota_per_unit,
      DEFAULT_CURRENCY_CONFIG.quotaPerUnit
    ),
    usdExchangeRate: toNumber(
      data.usd_exchange_rate,
      DEFAULT_CURRENCY_CONFIG.usdExchangeRate
    ),
    customCurrencySymbol:
      data.custom_currency_symbol?.trim() ||
      DEFAULT_CURRENCY_CONFIG.customCurrencySymbol,
    customCurrencyCode:
      normalizeIsoCurrencyCode(data.custom_currency_code) ||
      DEFAULT_CURRENCY_CONFIG.customCurrencyCode,
    customCurrencyExchangeRate: toNumber(
      data.custom_currency_exchange_rate,
      DEFAULT_CURRENCY_CONFIG.customCurrencyExchangeRate
    ),
  }

  return {
    systemName: resolveSystemName(data.system_name),
    logo: isDefaultLogo(data.logo) ? DEFAULT_LOGO : data.logo || DEFAULT_LOGO,
    footerHtml: data.footer_html,
    demoSiteEnabled: data.demo_site_enabled,
    displayTokenStatEnabled: data.display_token_stat_enabled,
    currency,
  }
}

// Fetch system config from API
async function fetchSystemConfig(): Promise<Partial<SystemConfig>> {
  // Share the session-scoped in-flight request and error policy with status
  // consumers; a raw fetch bypassed that deduplication during startup.
  const data = await getStatus()
  return mapStatusDataToConfig(data as StatusApiResponse['data'])
}

// Preload image and return cleanup function
function preloadImage(
  src: string,
  onLoad: () => void,
  onError: () => void
): () => void {
  const img = new Image()
  img.onload = onLoad
  img.onerror = onError
  img.src = src

  return () => {
    img.onload = null
    img.onerror = null
  }
}

/**
 * System configuration hook with auto-loading and logo preloading
 *
 * @example
 * // Root component - auto-load from backend
 * useSystemConfig({ autoLoad: true })
 *
 * @example
 * // Other components - use cached config
 * const { systemName, logo, loading } = useSystemConfig()
 */
export function useSystemConfig(options: UseSystemConfigOptions = {}) {
  const { autoLoad = false } = options
  const {
    config,
    loading,
    loadedLogoUrl,
    setConfig,
    setLoadedLogoUrl,
    setLoading,
  } = useSystemConfigStore()

  // Load config from backend
  const loadConfig = useCallback(async () => {
    try {
      setLoading(true)
      const newConfig = await fetchSystemConfig()
      setConfig(newConfig)
    } catch (error) {
      // eslint-disable-next-line no-console
      console.error('Failed to load system config:', error)
    } finally {
      setLoading(false)
    }
  }, [setConfig, setLoading])

  useEffect(() => {
    if (autoLoad) loadConfig()
  }, [autoLoad, loadConfig])

  // Preload logo image when URL changes
  useEffect(() => {
    const { logo } = config

    // The built-in brand is inline SVG. Never issue a request for the legacy
    // bitmap sentinel; only tenant-provided logos need asynchronous loading.
    if (isDefaultLogo(logo)) return

    // Skip if logo is already loaded
    if (!logo || logo === loadedLogoUrl) return

    // Preload new logo
    return preloadImage(
      logo,
      () => {
        setLoadedLogoUrl(logo)
      },
      () => {
        if (!isDefaultLogo(logo)) {
          // eslint-disable-next-line no-console
          console.error('Failed to load logo:', logo)
        }
        // Mark as loaded even on error to prevent infinite retry
        setLoadedLogoUrl(logo)
      }
    )
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [config.logo, loadedLogoUrl, setLoadedLogoUrl])

  return {
    ...config,
    systemName: resolveSystemName(config.systemName),
    logo: isDefaultLogo(config.logo) ? DEFAULT_LOGO : config.logo,
    loading,
    // DEFAULT_LOGO renders as LmmBrandMark rather than an image resource.
    logoLoaded:
      isDefaultLogo(config.logo) ||
      (config.logo === loadedLogoUrl && !!loadedLogoUrl),
  }
}
