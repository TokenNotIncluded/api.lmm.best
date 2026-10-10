/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
/**
 * Per-model usage stats for the profile share report.
 *
 * The backend groups `/api/data/self` by `model_name` and hour, so a single
 * bounded window carries everything we need — but one request may never span
 * more than a month (`controller/usedata.go` rejects a wider range), so the
 * caller fans out with `buildProfileUsageQueryRanges` and hands every window
 * here. `mergeModelUsageRows` then folds them into one row per model.
 */
import {
  buildProfileUsageQueryRanges,
  type ProfileUsageQueryRange,
  type ProfileUsageRow,
} from './activity'

/** Mirrors the server-side window cap in `controller/usedata.go`. */
export const MODEL_USAGE_WINDOW_SECONDS = 28 * 24 * 60 * 60

/** Bounded history for the explicitly labelled one-year report. */
export const MODEL_USAGE_YEAR_DAYS = 365

export type ModelUsageRangeKey = '7d' | '30d' | '365d'

export interface ModelUsageRow extends ProfileUsageRow {
  model_name?: string
  quota?: number
}

export interface ModelUsageStat {
  modelName: string
  requests: number
  tokens: number
  quota: number
  /** Share of the largest model's quota, 0–1. Drives bar lengths. */
  intensity: number
  /** Share of total quota, 0–1. */
  share: number
}

export interface ModelUsageTotals {
  requests: number
  tokens: number
  quota: number
  modelCount: number
  /** Most-used model by quota, or null when nothing was spent. */
  topModel: ModelUsageStat | null
}

export interface ModelUsageReport {
  models: ModelUsageStat[]
  totals: ModelUsageTotals
  shareMetric: 'quota' | 'tokens' | 'requests'
}

function nonNegativeNumber(value: number | null | undefined): number {
  const number = Number(value)
  return Number.isFinite(number) ? Math.max(0, number) : 0
}

/**
 * Users often hit the same upstream model through aliases, and the API may
 * return an empty name for legacy rows — surface those honestly rather than
 * dropping their spend on the floor.
 */
export function normalizeModelName(modelName?: string | null): string {
  const trimmed = (modelName ?? '').trim()
  return trimmed.length > 0 ? trimmed : 'unknown'
}

/**
 * Fold bounded windows into one row per model. Rows whose `model_name` is
 * missing still count, aggregated under `unknown`.
 */
export function mergeModelUsageRows(
  rows: ModelUsageRow[]
): Map<string, ModelUsageStat> {
  const byModel = new Map<string, ModelUsageStat>()

  for (const row of rows) {
    const modelName = normalizeModelName(row.model_name)
    const existing = byModel.get(modelName) ?? {
      modelName,
      requests: 0,
      tokens: 0,
      quota: 0,
      intensity: 0,
      share: 0,
    }

    existing.requests += nonNegativeNumber(row.count)
    existing.tokens += nonNegativeNumber(row.token_used)
    existing.quota += nonNegativeNumber(row.quota)

    byModel.set(modelName, existing)
  }

  return byModel
}

/**
 * Rank models by spend, then compute bar intensities so the chart and the
 * leaderboard agree on scale. Ties fall back to tokens so that a model with
 * real traffic never hides below an idle one.
 */
export function buildModelUsageReport(rows: ModelUsageRow[]): ModelUsageReport {
  const byModel = mergeModelUsageRows(rows)
  const models = [...byModel.values()].sort(
    (a, b) =>
      b.quota - a.quota ||
      b.tokens - a.tokens ||
      b.requests - a.requests ||
      a.modelName.localeCompare(b.modelName)
  )

  let totalQuota = 0
  let totalTokens = 0
  let totalRequests = 0
  for (const model of models) {
    totalQuota += model.quota
    totalTokens += model.tokens
    totalRequests += model.requests
  }

  // When quota is absent (older gateways do not report it) fall back to tokens
  // so the visualization still communicates relative weight instead of
  // collapsing into a single flat bar.
  const weightMetric =
    totalQuota > 0 ? 'quota' : totalTokens > 0 ? 'tokens' : 'requests'
  const maxWeight = Math.max(0, ...models.map((model) => model[weightMetric]))

  const totalWeight = models.reduce(
    (sum, model) => sum + model[weightMetric],
    0
  )

  const ranked = models.map((model) => {
    const weight = model[weightMetric]
    return {
      ...model,
      // `intensity` scales against the biggest model (bar lengths);
      // `share` is the slice of the whole (percentages and the ring).
      intensity: maxWeight > 0 ? weight / maxWeight : 0,
      share: totalWeight > 0 ? weight / totalWeight : 0,
    }
  })

  return {
    models: ranked,
    shareMetric: weightMetric,
    totals: {
      requests: totalRequests,
      tokens: totalTokens,
      quota: totalQuota,
      modelCount: ranked.length,
      topModel: ranked[0] ?? null,
    },
  }
}

/** Resolve the timestamps for a report range, clamped to account age. */
export function getModelUsageRange(
  rangeKey: ModelUsageRangeKey,
  now = new Date(),
  accountCreatedTime?: number
): ProfileUsageQueryRange {
  const endTimestamp = Math.floor(now.getTime() / 1000)
  const dayCount =
    rangeKey === '7d' ? 7 : rangeKey === '30d' ? 30 : MODEL_USAGE_YEAR_DAYS
  const startTimestamp = endTimestamp - dayCount * 24 * 60 * 60
  const created = Number(accountCreatedTime)

  return {
    start_timestamp:
      Number.isFinite(created) && created > 0 && created > startTimestamp
        ? Math.min(endTimestamp, Math.floor(created))
        : startTimestamp,
    end_timestamp: endTimestamp,
  }
}

export function buildModelUsageQueryRanges(
  range: ProfileUsageQueryRange
): ProfileUsageQueryRange[] {
  return buildProfileUsageQueryRanges(
    range.start_timestamp,
    range.end_timestamp,
    MODEL_USAGE_WINDOW_SECONDS
  )
}
