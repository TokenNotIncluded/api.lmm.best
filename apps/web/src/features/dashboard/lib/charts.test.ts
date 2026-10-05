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
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { describe, test } from 'node:test'

import {
  formatFiatCurrencyAmount,
  formatAmountInCurrency,
  formatQuotaInCurrency,
  quotaToDisplayAmount,
} from '@/lib/currency'
import { DEFAULT_CURRENCY_CONFIG } from '@/stores/system-config-store'

import type { QuotaDataItem } from '../types'
import {
  DASHBOARD_CHART_DARK_PALETTE,
  DASHBOARD_CHART_LIGHT_PALETTE,
  getDashboardChartColors,
  processChartData,
  processUserChartData,
  type ChartCurrencyFormatter,
} from './charts'

function channelLuminance(hex: string): number {
  const channels = hex
    .slice(1)
    .match(/../g)
    ?.map((channel) => Number.parseInt(channel, 16) / 255)

  assert.ok(channels?.length === 3, `expected a six-digit color: ${hex}`)

  const linear = channels.map((channel) =>
    channel <= 0.03928
      ? channel / 12.92
      : Math.pow((channel + 0.055) / 1.055, 2.4)
  )
  return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2]
}

function contrastRatio(first: string, second: string): number {
  const firstLuminance = channelLuminance(first)
  const secondLuminance = channelLuminance(second)
  const brighter = Math.max(firstLuminance, secondLuminance)
  const darker = Math.min(firstLuminance, secondLuminance)
  return (brighter + 0.05) / (darker + 0.05)
}

function colorDistance(first: string, second: string): number {
  const firstChannels = first
    .slice(1)
    .match(/../g)
    ?.map((channel) => Number.parseInt(channel, 16))
  const secondChannels = second
    .slice(1)
    .match(/../g)
    ?.map((channel) => Number.parseInt(channel, 16))

  assert.ok(firstChannels?.length === 3)
  assert.ok(secondChannels?.length === 3)
  return Math.hypot(
    firstChannels[0] - secondChannels[0],
    firstChannels[1] - secondChannels[1],
    firstChannels[2] - secondChannels[2]
  )
}

function assertStablePalette(
  palette: readonly string[],
  background: string,
  name: string
) {
  assert.equal(palette.length, 24, `${name} palette size changed`)
  assert.equal(
    new Set(palette).size,
    palette.length,
    `${name} palette contains duplicate colors`
  )
  assert.ok(
    palette.every((color) => /^#[\da-f]{6}$/i.test(color)),
    `${name} palette contains a non-hex color`
  )
  assert.ok(
    Math.min(...palette.map((color) => contrastRatio(color, background))) >= 3,
    `${name} palette has a color too close to its chart surface`
  )
  assert.ok(
    Math.min(
      ...palette
        .slice(1)
        .map((color, index) => colorDistance(color, palette[index] ?? ''))
    ) >= 50,
    `${name} palette has adjacent colors that are too similar`
  )
}

describe('dashboard chart palette', () => {
  test('keeps light and dark palettes unique and separated from chart surfaces', () => {
    assertStablePalette(DASHBOARD_CHART_LIGHT_PALETTE, '#f0eee6', 'light')
    assertStablePalette(DASHBOARD_CHART_DARK_PALETTE, '#24231f', 'dark')
  })

  test('keeps the complete fallback palette available for the largest model domain', () => {
    const colors = getDashboardChartColors(24)

    assert.deepEqual(colors, [...DASHBOARD_CHART_DARK_PALETTE])
    assert.equal(new Set(colors).size, 24)
    assert.deepEqual(getDashboardChartColors(28).slice(0, 24), [
      ...DASHBOARD_CHART_DARK_PALETTE,
    ])
  })

  test('does not reuse a color for model series plus Other', () => {
    const data: QuotaDataItem[] = Array.from({ length: 20 }, (_, index) => ({
      created_at: 1_720_000_000 + index * 3_600,
      model_name: `model-${index}`,
      quota: 100_000 + index,
      count: index + 1,
    }))
    const chartData = processChartData(data, 'hour')
    const color = chartData.spec_line.color as {
      domain: string[]
      range: string[]
    }

    assert.equal(color.domain.length, 21)
    assert.equal(color.range.length, 21)
    assert.equal(new Set(color.range).size, 21)
    assert.equal(chartData.spec_line.legends.visible, true)
    assert.equal(chartData.spec_line.bar.state.hover.lineWidth, 1.5)
    assert.equal(chartData.spec_area.area.style.fillOpacity, 0.14)
  })

  test('keeps dashboard series tied to the active theme tokens', () => {
    const stylesheet = readFileSync(
      new URL('../dashboard-editorial.css', import.meta.url),
      'utf8'
    )
    const series = [
      ...stylesheet.matchAll(/--forge-model-\d+:\s*([^;]+);/g),
    ].map((match) => match[1]?.trim())

    assert.equal(series.length, 24)
    assert.ok(
      series.every(
        (color) => color?.startsWith('var(') || color?.startsWith('color-mix(')
      )
    )
    assert.deepEqual(series.slice(0, 5), [
      'var(--chart-1)',
      'var(--chart-2)',
      'var(--chart-3)',
      'var(--chart-4)',
      'var(--chart-5)',
    ])
    assert.doesNotMatch(stylesheet, /--forge-model-\d+:\s*#[\da-f]{6}/i)
  })
})

describe('dashboard chart time buckets', () => {
  test('preserves sparse real buckets without inventing replacement periods', () => {
    const rows: QuotaDataItem[] = [
      {
        created_at: new Date('2026-09-15T12:00:00Z').getTime() / 1000,
        model_name: 'model-b',
        quota: 28_625_679,
        count: 3,
      },
      {
        created_at: new Date('2026-09-14T12:00:00Z').getTime() / 1000,
        model_name: 'model-a',
        quota: 11_671_440,
        count: 2,
      },
    ]

    const result = processChartData(rows, 'week', undefined, undefined, {
      formatQuota: (quota) => String(quota),
      quotaToAmount: (quota) => quota,
      formatAmount: (amount) => String(amount),
    })
    const values = result.spec_line.data[0].values as Array<{
      Time: string
      rawQuota: number
    }>

    assert.equal(new Set(values.map((row) => row.Time)).size, 2)
    assert.equal(
      values.reduce((sum, row) => sum + Number(row.rawQuota), 0),
      40_297_119
    )
    assert.equal(result.totalCountDisplay, '5')
  })

  test('shows a point when area and call-trend charts have only one bucket', () => {
    const result = processChartData(
      [
        {
          created_at: new Date('2026-09-15T12:00:00Z').getTime() / 1000,
          model_name: 'model-a',
          quota: 500_000,
          count: 2,
        },
      ],
      'day'
    )

    assert.equal(result.spec_area.point.visible, true)
    assert.equal(result.spec_model_line.point.visible, true)
  })
})

describe('dashboard chart monetary units', () => {
  const config = {
    ...DEFAULT_CURRENCY_CONFIG,
    currencyUnit: 'credit' as const,
    creditsPerUsd: 500_000,
    creditsPerUsdExact: '500000',
    cnyPerUsd: 7,
    cnyPerUsdExact: '7',
  }
  const currencyFormatter = (
    currency: 'USD' | 'CNY' | 'CREDIT'
  ): ChartCurrencyFormatter => ({
    formatQuota: (quota, options) =>
      formatQuotaInCurrency(
        quota,
        currency,
        { ...options, locale: 'en' },
        config
      ),
    quotaToAmount: (quota) => quotaToDisplayAmount(quota, currency, config),
    formatAmount: (amount, options) =>
      currency === 'CREDIT'
        ? formatAmountInCurrency(amount, 'CREDIT', { ...options, locale: 'en' })
        : formatFiatCurrencyAmount(amount, currency, {
            ...options,
            locale: 'en',
          }),
  })
  const rows = [
    {
      created_at: 1_720_000_000,
      model_name: 'model-a',
      username: 'alice',
      quota: 3_500_000,
      count: 1,
    },
    {
      created_at: 1_720_000_000,
      model_name: 'model-b',
      username: 'bob',
      quota: 1,
      count: 1,
    },
  ]

  test('model and user charts share true USD, CNY and exact Credit values', () => {
    for (const unit of ['USD', 'CNY', 'CREDIT'] as const) {
      const currency = currencyFormatter(unit)
      const model = processChartData(
        rows,
        'day',
        undefined,
        undefined,
        currency
      )
      const user = processUserChartData(rows, 'day', undefined, 10, currency)
      const values = model.spec_line.data[0].values
      const expectedLarge = unit === 'USD' ? 7 : unit === 'CNY' ? 49 : 3_500_000
      const expectedSmall =
        unit === 'USD' ? 1 / 500_000 : unit === 'CNY' ? 7 / 500_000 : 1
      assert.equal(values[0].Usage, expectedLarge)
      assert.equal(values[1].Usage, expectedSmall)
      assert.equal(
        model.spec_line.axes[1].label.formatMethod(expectedSmall),
        currency.formatQuota(1, {
          digitsLarge: 4,
          digitsSmall: 8,
          abbreviate: false,
        })
      )
      assert.equal(
        user.spec_user_rank.label.formatMethod(expectedSmall),
        currency.formatQuota(1, {
          digitsLarge: 2,
          digitsSmall: 8,
          abbreviate: false,
        })
      )
      assert.ok(
        values[1].Usage > 0,
        'one Credit must survive chart aggregation'
      )
      assert.equal(model.spec_area.data[0].values[1].Usage, expectedSmall)
      assert.equal(user.spec_user_rank.data[0].values[1].Usage, expectedSmall)
      assert.equal(user.spec_user_trend.data[0].values[1].Usage, expectedSmall)
      assert.equal(
        model.spec_line.axes[1].label.formatMethod(expectedLarge),
        currency.formatQuota(3_500_000, {
          digitsLarge: 4,
          digitsSmall: 8,
          abbreviate: false,
        })
      )
      assert.equal(
        user.spec_user_trend.axes[1].label.formatMethod(expectedLarge),
        currency.formatQuota(3_500_000, {
          digitsLarge: 2,
          digitsSmall: 8,
          abbreviate: false,
        })
      )
      const tooltip = model.spec_line.tooltip.dimension.updateContent(
        values.map((row: { Model: string; rawQuota: number }) => ({
          key: row.Model,
          value: row.rawQuota,
          datum: row,
        }))
      )
      assert.equal(
        tooltip[0].value,
        currency.formatQuota(3_500_001, {
          digitsLarge: 4,
          digitsSmall: 8,
          abbreviate: false,
        })
      )
    }
  })

  test('collapses excess models from raw Credits before converting the Other total', () => {
    const data = Array.from({ length: 17 }, (_, index) => ({
      created_at: 1_720_000_000,
      model_name: `model-${index}`,
      quota: index < 15 ? 10 : 1,
      count: 1,
    }))
    const result = processChartData(
      data,
      'day',
      undefined,
      undefined,
      currencyFormatter('USD')
    )
    const other = result.spec_area.data[0].values.find(
      (row: { Model: string }) => row.Model === 'Other'
    )
    assert.equal(other.rawQuota, 2)
    assert.equal(other.Usage, 2 / 500_000)
    assert.equal(result.totalQuotaDisplay, '0.000304 USD')
  })

  test('unknown denomination suppresses monetary series instead of plotting NaN', () => {
    const currency = {
      ...currencyFormatter('USD'),
      quotaToAmount: () => Number.NaN,
    }
    const result = processChartData(rows, 'day', undefined, undefined, currency)
    assert.equal(result.spec_line.data[0].values.length, 0)
    assert.equal(result.spec_area.data[0].values.length, 0)
    assert.equal(result.totalCountDisplay, '2')
  })
})
