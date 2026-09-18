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
import type { PlanRecord } from '@/features/subscriptions/types'

export type AssistantPlanComparison = {
  record: PlanRecord
  includedCreditUSD: number | null
  monthlyCreditUSD: number | null
  monthlyCostAmount: number | null
  oneTimeOnly: boolean
  coverageRatio: number | null
  recommended: boolean
}

export type AssistantTopupOffer = {
  amount: number
  multiplier: number
  savingsPercent: number
}

function normalizedCredit(totalAmount: number, quotaPerUnit: number) {
  if (totalAmount <= 0) return null
  return totalAmount / quotaPerUnit
}

const MONTH_SECONDS = 30 * 24 * 60 * 60

function planDurationSeconds(plan: PlanRecord['plan']): number {
  const value = Number(plan.duration_value || 0)
  if (plan.duration_unit === 'year') return value * 365 * 24 * 60 * 60
  if (plan.duration_unit === 'month') return value * MONTH_SECONDS
  if (plan.duration_unit === 'day') return value * 24 * 60 * 60
  if (plan.duration_unit === 'hour') return value * 60 * 60
  return Number(plan.custom_seconds || 0)
}

// How many times per month the plan itself must be repurchased to sustain
// access, based on its own duration (independent of any internal quota reset
// cadence). This is the only extrapolation that may also be applied to price:
// a quota reset within a single purchase (e.g. daily refill on a 1-month
// plan) never implies repurchasing, but a plan whose duration is shorter
// than a month does.
function monthlyRepeatFactor(plan: PlanRecord['plan']): number | null {
  const durationSeconds = planDurationSeconds(plan)
  return durationSeconds > 0 ? MONTH_SECONDS / durationSeconds : null
}

function monthlyPlanCredit(
  record: PlanRecord,
  includedCreditUSD: number | null
): number | null {
  if (includedCreditUSD === null) return null
  const plan = record.plan
  if (plan.quota_reset_period === 'daily') return includedCreditUSD * 30
  if (plan.quota_reset_period === 'weekly') {
    return includedCreditUSD * (30 / 7)
  }
  if (plan.quota_reset_period === 'monthly') return includedCreditUSD
  if (plan.quota_reset_period === 'custom') {
    const resetSeconds = Number(plan.quota_reset_custom_seconds || 0)
    return resetSeconds > 0
      ? includedCreditUSD * (MONTH_SECONDS / resetSeconds)
      : includedCreditUSD
  }

  const factor = monthlyRepeatFactor(plan)
  return factor !== null ? includedCreditUSD * factor : includedCreditUSD
}

// Monthly-equivalent price: what it would actually cost, per month, to
// sustain access by repurchasing this plan at its own duration cadence.
// Must use the same repeat factor as the duration-based fallback above so a
// plan's displayed price and displayed capacity are never extrapolated by
// different multipliers.
function monthlyPlanCost(plan: PlanRecord['plan']): number | null {
  const price = Number(plan.price_amount || 0)
  if (price <= 0) return 0
  const factor = monthlyRepeatFactor(plan)
  return factor !== null ? price * factor : null
}

export function compareAssistantPlans(
  plans: PlanRecord[],
  expectedCreditUSD: number,
  quotaPerUnit: number
): AssistantPlanComparison[] {
  if (!Number.isFinite(quotaPerUnit) || quotaPerUnit <= 0) return []

  const expected =
    Number.isFinite(expectedCreditUSD) && expectedCreditUSD > 0
      ? expectedCreditUSD
      : 0
  const candidates = plans
    .filter((record) => record.plan?.enabled !== false)
    .map((record) => {
      const plan = record.plan
      const includedCreditUSD = normalizedCredit(
        Number(plan.total_amount || 0),
        quotaPerUnit
      )
      const monthlyCreditUSD = monthlyPlanCredit(record, includedCreditUSD)
      const monthlyCostAmount = monthlyPlanCost(plan)
      // A plan capped at one purchase per user (e.g. a new-user trial pack)
      // can never be repurchased to sustain a monthly-equivalent rate, so it
      // must never be treated as a recurring alternative to plans priced and
      // sized for ongoing monthly use.
      const oneTimeOnly = Number(plan.max_purchase_per_user || 0) === 1
      return {
        record,
        includedCreditUSD,
        monthlyCreditUSD,
        monthlyCostAmount,
        oneTimeOnly,
        coverageRatio:
          expected > 0 && monthlyCreditUSD !== null
            ? monthlyCreditUSD / expected
            : null,
      }
    })

  const repeatable = candidates.filter((item) => !item.oneTimeOnly)

  const covering = repeatable
    .filter(
      (item) =>
        item.monthlyCreditUSD === null || item.monthlyCreditUSD >= expected
    )
    .sort((left, right) => {
      if (left.monthlyCreditUSD === null) return 1
      if (right.monthlyCreditUSD === null) return -1
      // Prefer the cheapest true monthly cost among plans that cover the
      // estimate; only fall back to capacity when cost is a tie or unknown.
      if (left.monthlyCostAmount !== null && right.monthlyCostAmount !== null) {
        const costDiff = left.monthlyCostAmount - right.monthlyCostAmount
        if (costDiff !== 0) return costDiff
      }
      return left.monthlyCreditUSD - right.monthlyCreditUSD
    })
  const recommended =
    covering[0] ??
    repeatable
      .filter((item) => item.monthlyCreditUSD !== null)
      .sort(
        (left, right) =>
          (right.monthlyCreditUSD ?? 0) - (left.monthlyCreditUSD ?? 0)
      )[0] ??
    repeatable[0] ??
    candidates[0]

  return candidates
    .map((item) => ({
      ...item,
      recommended: item.record.plan.id === recommended?.record.plan.id,
    }))
    .sort((left, right) => {
      if (left.recommended) return -1
      if (right.recommended) return 1
      if (left.monthlyCreditUSD === null) return 1
      if (right.monthlyCreditUSD === null) return -1
      return left.monthlyCreditUSD - right.monthlyCreditUSD
    })
}

export function getAssistantTopupOffers(
  discounts: unknown
): AssistantTopupOffer[] {
  if (!discounts || typeof discounts !== 'object' || Array.isArray(discounts)) {
    return []
  }

  return Object.entries(discounts)
    .map(([amount, multiplier]) => ({
      amount: Number(amount),
      multiplier: Number(multiplier),
    }))
    .filter(
      (offer) =>
        Number.isFinite(offer.amount) &&
        offer.amount > 0 &&
        Number.isFinite(offer.multiplier) &&
        offer.multiplier > 0 &&
        offer.multiplier < 1
    )
    .map((offer) => ({
      ...offer,
      savingsPercent: (1 - offer.multiplier) * 100,
    }))
    .sort(
      (left, right) =>
        right.savingsPercent - left.savingsPercent || left.amount - right.amount
    )
}
