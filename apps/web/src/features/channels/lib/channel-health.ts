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
import { CHANNEL_STATUS, RESPONSE_TIME_THRESHOLDS } from '../constants'
import type { Channel } from '../types'
import { isTagAggregateRow } from './channel-utils'

/** Channel status counts plus timing/balance roll-ups for a page of rows. */
export type ChannelHealthSummary = {
  /** Rows counted (tag aggregate rows are excluded). */
  sampled: number
  enabled: number
  manualDisabled: number
  autoDisabled: number
  unknown: number
  /** Enabled channels that were never response-tested. */
  neverTested: number
  /** Tested channels slower than the "poor" threshold. */
  slowOverThreshold: number
  /** Channels sitting at or below a zero balance. */
  zeroBalance: number
  /** Mean response time across tested channels, or null when none were tested. */
  averageResponseTimeMs: number | null
  /** 0–100 share of enabled channels, or null when nothing was sampled. */
  healthPercent: number | null
}

const EMPTY_SUMMARY: ChannelHealthSummary = {
  sampled: 0,
  enabled: 0,
  manualDisabled: 0,
  autoDisabled: 0,
  unknown: 0,
  neverTested: 0,
  slowOverThreshold: 0,
  zeroBalance: 0,
  averageResponseTimeMs: null,
  healthPercent: null,
}

/**
 * Roll up a page of channels into the numbers the health strip shows.
 *
 * Tag aggregate rows are synthetic (one row standing in for many children)
 * so counting them would double-count their children; they only appear when
 * tag mode is on, and the strip is hidden in that mode.
 */
export function summarizeChannelHealth(
  channels: readonly Channel[]
): ChannelHealthSummary {
  let sampled = 0
  let enabled = 0
  let manualDisabled = 0
  let autoDisabled = 0
  let unknown = 0
  let neverTested = 0
  let slowOverThreshold = 0
  let zeroBalance = 0
  let testedCount = 0
  let responseTimeTotal = 0

  for (const channel of channels) {
    if (isTagAggregateRow(channel)) continue
    sampled += 1

    switch (channel.status) {
      case CHANNEL_STATUS.ENABLED:
        enabled += 1
        break
      case CHANNEL_STATUS.MANUAL_DISABLED:
        manualDisabled += 1
        break
      case CHANNEL_STATUS.AUTO_DISABLED:
        autoDisabled += 1
        break
      default:
        unknown += 1
        break
    }

    const responseTime = channel.response_time ?? 0
    if (responseTime > 0) {
      testedCount += 1
      responseTimeTotal += responseTime
      if (responseTime > RESPONSE_TIME_THRESHOLDS.POOR) {
        slowOverThreshold += 1
      }
    } else if (channel.status === CHANNEL_STATUS.ENABLED) {
      neverTested += 1
    }

    if (typeof channel.balance === 'number' && channel.balance <= 0) {
      zeroBalance += 1
    }
  }

  if (sampled === 0) {
    return EMPTY_SUMMARY
  }

  return {
    sampled,
    enabled,
    manualDisabled,
    autoDisabled,
    unknown,
    neverTested,
    slowOverThreshold,
    zeroBalance,
    averageResponseTimeMs:
      testedCount > 0 ? Math.round(responseTimeTotal / testedCount) : null,
    healthPercent: Math.round((enabled / sampled) * 100),
  }
}
