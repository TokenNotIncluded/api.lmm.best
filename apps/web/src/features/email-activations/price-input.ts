/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import type { CurrencyConfig } from '@/stores/system-config-store'

type Ratio = { n: bigint; d: bigint }
type Currency = 'CREDIT' | 'CNY' | 'USD'

function decimal(value: string | number | undefined): Ratio | undefined {
  const text = String(value ?? '').trim()
  if (text.length > 64 || !/^\d+(?:\.\d{1,30})?$/.test(text)) return undefined
  const [whole, fraction = ''] = text.split('.')
  return { n: BigInt(whole + fraction), d: 10n ** BigInt(fraction.length) }
}

function priceFactor(
  currency: Currency,
  config: CurrencyConfig
): Ratio | undefined {
  const units = decimal(config.quotaPerUnit)
  if (!units?.n) return undefined
  if (currency === 'CREDIT') return units
  if (
    config.currencyUnit !== 'credit' ||
    !Number.isFinite(config.creditsPerUsd) ||
    Number(config.creditsPerUsd) <= 0
  ) {
    return undefined
  }
  const k = decimal(config.creditsPerUsdExact ?? config.creditsPerUsd)
  if (!k?.n) return undefined
  const usd = { n: units.n * k.d, d: units.d * k.n }
  if (currency === 'USD') return usd
  if (!Number.isFinite(config.cnyPerUsd) || Number(config.cnyPerUsd) <= 0) {
    return undefined
  }
  const fx = decimal(config.cnyPerUsdExact ?? config.cnyPerUsd)
  return fx?.n ? { n: usd.n * fx.n, d: usd.d * fx.d } : undefined
}

function text(ratio: Ratio, digits: number, ceil: boolean): string {
  const numerator = ratio.n * 10n ** BigInt(digits)
  const value = numerator / ratio.d + (ceil && numerator % ratio.d ? 1n : 0n)
  const all = value.toString().padStart(digits + 1, '0')
  return `${all.slice(0, -digits)}.${all.slice(-digits)}`
    .replace(/0+$/, '')
    .replace(/\.$/, '')
}

/** A canonical legacy bid survives denomination changes without being reinterpreted. */
export function heroSmsPriceToInput(
  value: string,
  currency: Currency,
  config: CurrencyConfig
): string {
  const price = decimal(value)
  const factor = priceFactor(currency, config)
  return price && factor
    ? text({ n: price.n * factor.n, d: price.d * factor.d }, 30, true)
    : ''
}

/** The legacy API accepts six decimals. Round down so a maximum bid never increases. */
export function heroSmsInputToPrice(
  value: string,
  currency: Currency,
  config: CurrencyConfig
): string {
  const price = decimal(value)
  const factor = priceFactor(currency, config)
  return price && factor
    ? text({ n: price.n * factor.d, d: price.d * factor.n }, 6, false)
    : ''
}
