/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import type { QuotaDataItem } from '../types'

export type ModelUsageMetric = 'count' | 'tokens' | 'quota'
export type ModelUsageSort = {
  metric: ModelUsageMetric
  direction: 'ascending' | 'descending'
}

export interface ModelUsageRow {
  model: string
  count: number
  tokens: number
  quota: number
  quota_raw: string | null
  share: number | null
}

const finite = (value: number | undefined) =>
  typeof value === 'number' && Number.isFinite(value) ? value : 0

/** Keep model identity intact; translate an empty name only when displaying it. */
export function summarizeModelUsage(data: readonly QuotaDataItem[]) {
  const models = new Map<string, ModelUsageRow>()
  let totalQuota = 0n
  let completeRawQuota = true
  for (const item of data) {
    const model = item.model_name ?? ''
    let row = models.get(model)
    if (!row) {
      row = {
        model,
        count: 0,
        tokens: 0,
        quota: 0,
        quota_raw: '0',
        share: null,
      }
      models.set(model, row)
    }
    row.count += finite(item.count)
    row.tokens += finite(item.token_used)
    row.quota += finite(item.quota)
    const raw =
      item.quota_money_basis !== 'invalid_stored_quota' &&
      typeof item.quota === 'number' &&
      Number.isSafeInteger(item.quota)
        ? BigInt(item.quota)
        : null
    row.quota_raw =
      row.quota_raw !== null && raw !== null
        ? (BigInt(row.quota_raw) + raw).toString()
        : null
    if (raw === null) {
      completeRawQuota = false
    } else {
      totalQuota += raw
    }
  }
  return Array.from(models.values(), (row) => ({
    ...row,
    // Unknown input makes the entire period's denominator unknown. A known
    // row must not acquire 100% by silently treating another row as zero.
    share:
      completeRawQuota && totalQuota > 0n && row.quota_raw !== null
        ? Number(BigInt(row.quota_raw)) / Number(totalQuota)
        : null,
  }))
}

export function selectModelUsage(
  rows: readonly ModelUsageRow[],
  query: string,
  sort: ModelUsageSort,
  unknownLabel: string
) {
  const needle = query.trim().toLocaleLowerCase()
  const direction = sort.direction === 'ascending' ? 1 : -1
  return rows
    .filter((row) =>
      (row.model || unknownLabel).toLocaleLowerCase().includes(needle)
    )
    .sort((a, b) => {
      const difference = (a[sort.metric] - b[sort.metric]) * direction
      // Stable tie order regardless of API row order, without mutating input.
      return difference || (a.model < b.model ? -1 : a.model > b.model ? 1 : 0)
    })
}

function csvCell(value: string | number | bigint) {
  let text = String(value)
  // Quoting alone does not stop a spreadsheet from executing a model name.
  if (typeof value === 'string' && /^[\s\uFEFF]*[=+@-]/u.test(text)) {
    text = `'${text}`
  }
  return `"${text.replaceAll('"', '""')}"`
}

export function modelUsageCsv(
  rows: readonly ModelUsageRow[],
  headers: readonly string[],
  unknownLabel: string
) {
  const records: (string | number | bigint)[][] = [
    [...headers],
    ...rows.map((row) => [
      row.model || unknownLabel,
      row.count,
      row.tokens,
      row.quota_raw === null ? '' : BigInt(row.quota_raw),
      // Hour buckets do not capture a historical money basis. Aggregating raw
      // quota cannot establish one, even if today's wallet FX is available.
      'raw_quota',
      'false',
      row.quota_raw === null
        ? 'raw_quota_not_exact'
        : 'bucket_without_credit_basis',
      row.share === null ? '' : row.share * 100,
    ]),
  ]
  // BOM preserves non-ASCII model names in common spreadsheet applications.
  return `\uFEFF${records.map((record) => record.map(csvCell).join(',')).join('\r\n')}\r\n`
}
