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

import type { QuotaDataItem } from '../types'
import {
  DASHBOARD_CHART_DARK_PALETTE,
  DASHBOARD_CHART_LIGHT_PALETTE,
  getDashboardChartColors,
  processChartData,
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
