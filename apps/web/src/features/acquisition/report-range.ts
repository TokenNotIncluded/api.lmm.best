/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
export type AcquisitionRange = { from: number; to: number }
const DAY = 86400
export function presetAcquisitionRange(
  days: 0 | 7 | 30,
  now: number
): AcquisitionRange {
  return {
    from: days === 0 ? Math.floor(now / DAY) * DAY : now - days * DAY,
    to: now,
  }
}
export function customAcquisitionRange(
  start: string,
  end: string,
  now: number
): AcquisitionRange | null {
  const parse = (date: string) => {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) return Number.NaN
    const value = Date.parse(`${date}T00:00:00Z`)
    return Number.isFinite(value) &&
      new Date(value).toISOString().slice(0, 10) === date
      ? value / 1000
      : Number.NaN
  }
  const from = parse(start),
    endDay = parse(end)
  const to = Math.min(endDay + DAY, now)
  if (
    ![from, to, endDay].every(Number.isFinite) ||
    from <= 0 ||
    endDay < from ||
    from >= now ||
    endDay > now ||
    to <= from ||
    to - from > 366 * DAY
  )
    {return null}
  return { from, to }
}
