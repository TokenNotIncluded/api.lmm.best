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
export const PROFILE_ACTIVITY_WEEKS = 53
export const PROFILE_ACTIVITY_DAYS = PROFILE_ACTIVITY_WEEKS * 7
export const PROFILE_USAGE_WINDOW_SECONDS = 28 * 24 * 60 * 60

export type ProfileActivityView = 'daily' | 'weekly' | 'cumulative'

export interface ProfileUsageRow {
  created_at: number
  count?: number
  token_used?: number
}

export interface ProfileUsageQueryRange {
  start_timestamp: number
  end_timestamp: number
}

export interface ProfileActivityRange extends ProfileUsageQueryRange {
  startDate: Date
  endDate: Date
}

export interface ProfileDailyUsage {
  date: Date
  dateKey: string
  tokens: number
  requests: number
}

export interface ProfileUsageSummary {
  totalTokens: number
  peakDailyTokens: number
  activeDays: number
  currentStreak: number
  currentStreakCapped: boolean
  longestStreak: number
}

export interface ProfileActivityCell extends ProfileDailyUsage {
  displayTokens: number
  displayRequests: number
  level: number
  periodStart: Date
  periodEnd: Date
}

function startOfLocalDay(date: Date): Date {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate())
}

function addLocalDays(date: Date, amount: number): Date {
  const next = new Date(date)
  next.setDate(next.getDate() + amount)
  return next
}

function nonNegativeNumber(value: number | null | undefined): number {
  const number = Number(value)
  return Number.isFinite(number) ? Math.max(0, number) : 0
}

export function formatProfileDateKey(date: Date): string {
  const year = String(date.getFullYear())
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

export function getProfileActivityRange(
  now = new Date()
): ProfileActivityRange {
  const endDate = startOfLocalDay(now)
  const startDate = addLocalDays(endDate, -(PROFILE_ACTIVITY_DAYS - 1))

  return {
    startDate,
    endDate,
    start_timestamp: Math.floor(startDate.getTime() / 1000),
    end_timestamp: Math.floor(now.getTime() / 1000),
  }
}

export function buildProfileUsageQueryRanges(
  startTimestamp: number,
  endTimestamp: number,
  maxWindowSeconds = PROFILE_USAGE_WINDOW_SECONDS
): ProfileUsageQueryRange[] {
  if (
    !Number.isFinite(startTimestamp) ||
    !Number.isFinite(endTimestamp) ||
    !Number.isFinite(maxWindowSeconds) ||
    startTimestamp > endTimestamp ||
    maxWindowSeconds < 1
  ) {
    return []
  }

  const ranges: ProfileUsageQueryRange[] = []
  let cursor = Math.floor(startTimestamp)
  const finalTimestamp = Math.floor(endTimestamp)
  const windowSeconds = Math.floor(maxWindowSeconds)

  while (cursor <= finalTimestamp) {
    const rangeEnd = Math.min(finalTimestamp, cursor + windowSeconds)
    ranges.push({ start_timestamp: cursor, end_timestamp: rangeEnd })
    cursor = rangeEnd + 1
  }

  return ranges
}

export function buildProfileDailyUsage(
  rows: ProfileUsageRow[],
  range: Pick<ProfileActivityRange, 'startDate' | 'endDate'>
): ProfileDailyUsage[] {
  const byDate = new Map<string, ProfileDailyUsage>()

  for (
    let date = startOfLocalDay(range.startDate);
    date <= range.endDate;
    date = addLocalDays(date, 1)
  ) {
    const dateKey = formatProfileDateKey(date)
    byDate.set(dateKey, {
      date: new Date(date),
      dateKey,
      tokens: 0,
      requests: 0,
    })
  }

  for (const row of rows) {
    const timestamp = Number(row.created_at)
    if (!Number.isFinite(timestamp)) continue

    const date = new Date(timestamp * 1000)
    const day = byDate.get(formatProfileDateKey(date))
    if (!day) continue

    day.tokens += nonNegativeNumber(row.token_used)
    day.requests += nonNegativeNumber(row.count)
  }

  return [...byDate.values()]
}

export function buildProfileUsageSummary(
  days: ProfileDailyUsage[]
): ProfileUsageSummary {
  let totalTokens = 0
  let peakDailyTokens = 0
  let activeDays = 0
  let longestStreak = 0
  let runningStreak = 0

  for (const day of days) {
    totalTokens += day.tokens
    peakDailyTokens = Math.max(peakDailyTokens, day.tokens)

    if (day.tokens > 0) {
      activeDays += 1
      runningStreak += 1
      longestStreak = Math.max(longestStreak, runningStreak)
    } else {
      runningStreak = 0
    }
  }

  let cursor = days.length - 1
  if (cursor >= 0 && days[cursor].tokens <= 0) cursor -= 1

  let currentStreak = 0
  while (cursor >= 0 && days[cursor].tokens > 0) {
    currentStreak += 1
    cursor -= 1
  }

  return {
    totalTokens,
    peakDailyTokens,
    activeDays,
    currentStreak,
    currentStreakCapped: currentStreak > 0 && cursor < 0,
    longestStreak,
  }
}

export function getProfileActivityLevel(value: number, max: number): number {
  if (value <= 0 || max <= 0) return 0
  return Math.min(5, Math.max(1, Math.ceil((value / max) * 5)))
}

export function buildProfileActivityCells(
  days: ProfileDailyUsage[],
  view: ProfileActivityView
): ProfileActivityCell[] {
  if (view === 'weekly') {
    const weekTotals = Array.from(
      { length: PROFILE_ACTIVITY_WEEKS },
      (_, i) => {
        const week = days.slice(i * 7, i * 7 + 7)
        return {
          tokens: week.reduce((sum, day) => sum + day.tokens, 0),
          requests: week.reduce((sum, day) => sum + day.requests, 0),
        }
      }
    )
    const maxWeek = Math.max(0, ...weekTotals.map((week) => week.tokens))

    return days.map((day, index) => {
      const weekIndex = Math.floor(index / 7)
      const week = weekTotals[weekIndex] ?? { tokens: 0, requests: 0 }
      const filledRows =
        week.tokens > 0 && maxWeek > 0
          ? Math.max(1, Math.round((week.tokens / maxWeek) * 7))
          : 0
      const rowIndex = index % 7
      const periodStart = days[weekIndex * 7]?.date ?? day.date
      const periodEnd = days[weekIndex * 7 + 6]?.date ?? day.date

      return {
        ...day,
        displayTokens: week.tokens,
        displayRequests: week.requests,
        level: rowIndex >= 7 - filledRows ? 5 : 0,
        periodStart,
        periodEnd,
      }
    })
  }

  if (view === 'cumulative') {
    const totalTokens = days.reduce((sum, day) => sum + day.tokens, 0)
    let runningTokens = 0
    let runningRequests = 0

    return days.map((day) => {
      runningTokens += day.tokens
      runningRequests += day.requests
      return {
        ...day,
        displayTokens: runningTokens,
        displayRequests: runningRequests,
        level: getProfileActivityLevel(runningTokens, totalTokens),
        periodStart: day.date,
        periodEnd: day.date,
      }
    })
  }

  const maxDay = Math.max(0, ...days.map((day) => day.tokens))
  return days.map((day) => ({
    ...day,
    displayTokens: day.tokens,
    displayRequests: day.requests,
    level: getProfileActivityLevel(day.tokens, maxDay),
    periodStart: day.date,
    periodEnd: day.date,
  }))
}
