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
/** Ledger balances are integer Credits. USD values use a fixed backend denomination, never a live recharge ratio. */
import i18n from '@/i18n/config'
import { normalizeInterfaceLanguage } from '@/i18n/languages'
import { useAuthStore } from '@/stores/auth-store'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
  type CurrencyConfig,
  type CurrencyDisplayType,
} from '@/stores/system-config-store'
import {
  useWalletCurrencyPreferenceStore,
  type WalletDisplayCurrency,
  type WalletDisplayCurrencyPreference,
} from '@/stores/wallet-currency-preference-store'

export interface CurrencyFormatOptions {
  digitsLarge?: number
  digitsSmall?: number
  abbreviate?: boolean
  minimumNonZero?: number
  compact?: boolean
  showSymbol?: boolean
  locale?: Intl.LocalesArgument
  /** Captured translation supplied by reactive consumers. */
  creditLabel?: string
}

type Rational = { numerator: bigint; denominator: bigint }
type DisplayMeta =
  | {
      kind: 'currency'
      symbol: string
      currencyCode: string
      exchangeRate: number
    }
  | { kind: 'tokens'; quotaPerUnit: number }
const MAX_SAFE = BigInt(Number.MAX_SAFE_INTEGER)

export function normalizeWalletDisplayCurrencyPreference(
  value: unknown
): WalletDisplayCurrencyPreference {
  return value === 'CREDIT' || value === 'CNY' || value === 'USD' ? value : ''
}

export function resolveWalletDisplayCurrency(
  preference: unknown,
  language?: string
): WalletDisplayCurrency {
  const explicit = normalizeWalletDisplayCurrencyPreference(preference)
  if (explicit) return explicit
  const locale = normalizeInterfaceLanguage(language || '')
  return locale === 'zhCN' || locale === 'zhTW' ? 'CNY' : 'USD'
}

export function parseWalletCurrencySettings(
  setting: unknown
): Record<string, unknown> {
  try {
    const parsed: unknown =
      typeof setting === 'string' ? JSON.parse(setting) : setting
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed)
      ? (parsed as Record<string, unknown>)
      : {}
  } catch {
    return {}
  }
}

export function getWalletDisplayCurrencyPreference(): WalletDisplayCurrencyPreference {
  const user = useAuthStore.getState().auth.user
  return user
    ? normalizeWalletDisplayCurrencyPreference(
        parseWalletCurrencySettings(user.setting).wallet_display_currency
      )
    : normalizeWalletDisplayCurrencyPreference(
        useWalletCurrencyPreferenceStore.getState().preference
      )
}

export function getWalletDisplayCurrency(): WalletDisplayCurrency {
  return resolveWalletDisplayCurrency(
    getWalletDisplayCurrencyPreference(),
    i18n.resolvedLanguage || i18n.language
  )
}

export function isCurrencyDisplayType(
  value: unknown
): value is CurrencyDisplayType {
  return (
    value === 'USD' ||
    value === 'CNY' ||
    value === 'TOKENS' ||
    value === 'CUSTOM'
  )
}

export function parseCurrencyDisplayType(
  value: unknown,
  fallback: CurrencyDisplayType = 'USD'
): CurrencyDisplayType {
  return isCurrencyDisplayType(value) ? value : fallback
}

function positive(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0
    ? value
    : Number.NaN
}

function getConfig(
  input = useSystemConfigStore.getState().config.currency
): CurrencyConfig {
  const config = { ...DEFAULT_CURRENCY_CONFIG, ...input }
  return {
    ...config,
    quotaPerUnit: positive(config.quotaPerUnit),
    creditsPerUsd:
      config.currencyUnit === 'credit'
        ? positive(config.creditsPerUsd)
        : Number.NaN,
    cnyPerUsd: positive(config.cnyPerUsd),
    legacyPricingUnitsPerUsd: positive(config.legacyPricingUnitsPerUsd),
  }
}

export function getCurrencyDisplay(input?: CurrencyConfig) {
  const config = getConfig(input)
  const currency = getWalletDisplayCurrency()
  const meta: DisplayMeta =
    currency === 'CREDIT'
      ? { kind: 'tokens', quotaPerUnit: Number(config.creditsPerUsd) }
      : {
          kind: 'currency',
          symbol: currency === 'CNY' ? '¥' : '$',
          currencyCode: currency,
          exchangeRate: currency === 'CNY' ? Number(config.cnyPerUsd) : 1,
        }
  return { config, meta, currency }
}

/** Parse decimal text exactly; reject non-finite values and oversized input. */
function decimal(value: number | string): Rational | null {
  if (typeof value === 'number' && !Number.isFinite(value)) return null
  const source = String(value).trim()
  if (!source || source.length > 512) return null
  const match = source.match(/^([+-]?)(\d*)(?:\.(\d*))?(?:e([+-]?\d+))?$/i)
  if (!match || !(match[2] || match[3])) return null
  const exponent = Number(match[4] || 0) - (match[3]?.length || 0)
  if (!Number.isInteger(exponent) || Math.abs(exponent) > 400) return null
  let numerator = BigInt((match[2] || '0') + (match[3] || ''))
  if (match[1] === '-') numerator = -numerator
  return exponent >= 0
    ? { numerator: numerator * 10n ** BigInt(exponent), denominator: 1n }
    : { numerator, denominator: 10n ** BigInt(-exponent) }
}

function rate(value: number | undefined, exact?: string): Rational | null {
  if (!Number.isFinite(value) || !value || value <= 0) return null
  const parsed = decimal(exact && Number(exact) === value ? exact : value)
  return parsed && parsed.numerator > 0n ? parsed : null
}

function multiply(left: Rational, right: Rational): Rational {
  return {
    numerator: left.numerator * right.numerator,
    denominator: left.denominator * right.denominator,
  }
}

function divide(left: Rational, right: Rational): Rational {
  return {
    numerator: left.numerator * right.denominator,
    denominator: left.denominator * right.numerator,
  }
}

function floor(rational: Rational): bigint {
  const quotient = rational.numerator / rational.denominator
  return rational.numerator < 0n &&
    rational.numerator % rational.denominator !== 0n
    ? quotient - 1n
    : quotient
}

function safeInteger(rational: Rational | null): number {
  if (!rational) return Number.NaN
  const integer = floor(rational)
  return integer >= -MAX_SAFE && integer <= MAX_SAFE
    ? Number(integer)
    : Number.NaN
}

function displayRational(
  quota: number,
  currency: WalletDisplayCurrency,
  config = getConfig()
): Rational | null {
  if (!Number.isSafeInteger(quota)) return null
  const raw = { numerator: BigInt(quota), denominator: 1n }
  if (currency === 'CREDIT') return raw
  const denomination = rate(config.creditsPerUsd, config.creditsPerUsdExact)
  if (!denomination) return null
  const usd = divide(raw, denomination)
  if (currency === 'USD') return usd
  const fx = rate(config.cnyPerUsd, config.cnyPerUsdExact)
  return fx ? multiply(usd, fx) : null
}

export function quotaToDisplayAmount(
  quota: number,
  currency = getWalletDisplayCurrency(),
  config = getConfig()
): number {
  const rational = displayRational(quota, currency, config)
  if (!rational) return Number.NaN
  return Number(rational.numerator) / Number(rational.denominator)
}

export function displayAmountToQuota(
  amount: number | string,
  currency = getWalletDisplayCurrency(),
  config = getConfig()
): number {
  let rational = decimal(amount)
  if (!rational) return Number.NaN
  if (currency === 'CREDIT') return safeInteger(rational)
  const denomination = rate(config.creditsPerUsd, config.creditsPerUsdExact)
  if (!denomination) return Number.NaN
  if (currency === 'CNY') {
    const fx = rate(config.cnyPerUsd, config.cnyPerUsdExact)
    if (!fx) return Number.NaN
    rational = divide(rational, fx)
  }
  return safeInteger(multiply(rational, denomination))
}

/** Editable decimal text rounds upward at 30 decimal places, so parsing cannot erase the last Credit. */
export function quotaToDisplayInput(
  quota: number,
  currency = getWalletDisplayCurrency(),
  config = getConfig()
): string {
  const rational = displayRational(quota, currency, config)
  if (!rational) return ''
  const scale = 10n ** 30n
  const scaled = rational.numerator * scale
  let value = scaled / rational.denominator
  if (scaled > 0n && scaled % rational.denominator !== 0n) value += 1n
  const sign = value < 0n ? '-' : ''
  const digits = (value < 0n ? -value : value).toString().padStart(31, '0')
  const text = `${sign}${digits.slice(0, -30)}.${digits.slice(-30)}`
  const result = text.replace(/0+$/, '').replace(/\.$/, '')
  return displayAmountToQuota(result, currency, config) === quota ? result : ''
}

/** Compatibility bridge for historical batch-price values. This never changes an order body. */
export function legacyPlatformAmountToQuota(
  amount: number | string,
  config = getConfig()
): number {
  const input = decimal(amount)
  const units = rate(config.quotaPerUnit)
  return input && units ? safeInteger(multiply(input, units)) : Number.NaN
}

export function quotaToLegacyPlatformAmount(
  quota: number,
  config = getConfig()
): number {
  const units = config.quotaPerUnit
  return Number.isSafeInteger(quota) && Number.isFinite(units)
    ? quota / units
    : Number.NaN
}

export function getCurrencyFormattingLocale(
  activeLanguage = i18n.resolvedLanguage || i18n.language
): Intl.LocalesArgument {
  const language = normalizeInterfaceLanguage(activeLanguage)
  return (
    {
      zhCN: 'zh-CN',
      zhTW: 'zh-TW',
      en: 'en',
      fr: 'fr',
      ja: 'ja',
      ru: 'ru',
      vi: 'vi',
    } as const
  )[language]
}

export function getCurrencyFractionDigits(
  value: number,
  options?: CurrencyFormatOptions
): number {
  return Math.abs(value) >= 1
    ? (options?.digitsLarge ?? 2)
    : (options?.digitsSmall ?? 6)
}

function numberText(
  value: number,
  options?: CurrencyFormatOptions,
  integer = false
): string {
  const requested = getCurrencyFractionDigits(value, options)
  const digits = integer
    ? 0
    : value !== 0 && Math.abs(value) < 10 ** -requested
      ? Math.min(
          20,
          Math.max(requested, Math.ceil(-Math.log10(Math.abs(value))) + 2)
        )
      : requested
  return new Intl.NumberFormat(
    options?.locale ?? getCurrencyFormattingLocale(),
    {
      notation:
        !integer && (options?.compact || options?.abbreviate)
          ? 'compact'
          : 'standard',
      minimumFractionDigits: 0,
      maximumFractionDigits:
        !integer && (options?.compact || options?.abbreviate)
          ? 6
          : Math.min(20, digits),
    }
  ).format(value)
}

/** Raw, integral smallest ledger units; never labelled tokens or dollars. */
export function formatCreditAmount(
  quota: number | null | undefined,
  options?: CurrencyFormatOptions
): string {
  if (quota == null || !Number.isSafeInteger(quota)) return '-'
  const number = numberText(quota, options, true)
  return options?.showSymbol === false
    ? number
    : `${number} ${options?.creditLabel ?? i18n.t('Credits')}`
}

/** Amount is already in this fiat currency. Formatting never converts it. */
export function formatFiatCurrencyAmount(
  amount: number | null | undefined,
  currencyCode = 'USD',
  options?: CurrencyFormatOptions
): string {
  if (amount == null || !Number.isFinite(amount)) return '-'
  const code = currencyCode.trim().toUpperCase() || 'USD'
  const number = numberText(amount, options)
  return options?.showSymbol === false ? number : `${number} ${code}`
}

/** The input is canonical real USD. CREDIT rates retain fractional units with K, CNY uses fiat FX only. */
export function formatUSDInCurrency(
  amountUSD: number | null | undefined,
  currency: WalletDisplayCurrency,
  options?: CurrencyFormatOptions,
  config = getConfig()
): string {
  if (amountUSD == null || !Number.isFinite(amountUSD)) return '-'
  if (currency === 'USD') {
    return formatFiatCurrencyAmount(amountUSD, 'USD', options)
  }
  if (currency === 'CREDIT') {
    const value = amountUSD * positive(config.creditsPerUsd)
    if (
      !Number.isFinite(positive(config.creditsPerUsd)) ||
      !Number.isFinite(value)
    ) {
      return '-'
    }
    const text = numberText(value, options)
    return options?.showSymbol === false
      ? text
      : `${text} ${options?.creditLabel ?? i18n.t('Credits')}`
  }
  const fx = positive(config.cnyPerUsd)
  return Number.isFinite(fx)
    ? formatFiatCurrencyAmount(amountUSD * Number(fx), 'CNY', options)
    : '-'
}

export function formatQuotaWithCurrency(
  quota: number | null | undefined,
  options?: CurrencyFormatOptions
): string {
  if (quota == null || !Number.isSafeInteger(quota)) return '-'
  return formatQuotaInCurrency(quota, getWalletDisplayCurrency(), options)
}

export function formatQuotaInCurrency(
  quota: number,
  currency: WalletDisplayCurrency,
  options?: CurrencyFormatOptions,
  config = getConfig()
): string {
  return currency === 'CREDIT'
    ? formatCreditAmount(quota, options)
    : formatFiatCurrencyAmount(
        quotaToDisplayAmount(quota, currency, config),
        currency,
        options
      )
}

/** Legacy batch values are explicitly bridged through raw Credits. */
export function formatPlatformAmount(
  amount: number | null | undefined,
  options?: CurrencyFormatOptions,
  _legacyPlatformLabel?: string
): string {
  return amount == null
    ? '-'
    : formatQuotaWithCurrency(legacyPlatformAmountToQuota(amount), options)
}

export function formatCurrencyFromUSD(
  amountUSD: number | null | undefined,
  options?: CurrencyFormatOptions
): string {
  return formatUSDInCurrency(amountUSD, getWalletDisplayCurrency(), options)
}

/** Billing values are actual USD; a raw balance unit preference does not relabel fiat invoices. */
export function formatBillingCurrencyFromUSD(
  amountUSD: number | null | undefined,
  options?: CurrencyFormatOptions
): string {
  const preferred = getWalletDisplayCurrency()
  return formatUSDInCurrency(
    amountUSD,
    preferred === 'CREDIT' ? 'USD' : preferred,
    options
  )
}

export function formatLocalCurrencyAmount(
  amount: number | null | undefined,
  options?: CurrencyFormatOptions
): string {
  const preferred = getWalletDisplayCurrency()
  return formatFiatCurrencyAmount(
    amount,
    preferred === 'CREDIT' ? 'USD' : preferred,
    options
  )
}

export function getCurrencyLabel(): string {
  const currency = getWalletDisplayCurrency()
  return currency === 'CREDIT' ? i18n.t('Credits') : currency
}

export function getPlatformCurrencyLabel(
  _legacyPlatformLabel?: string
): string {
  return getCurrencyLabel()
}

export function isCurrencyDisplayEnabled(): boolean {
  return getWalletDisplayCurrency() !== 'CREDIT'
}

export type {
  WalletDisplayCurrency,
  WalletDisplayCurrencyPreference,
} from '@/stores/wallet-currency-preference-store'
