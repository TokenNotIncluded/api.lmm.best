/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { TFunction } from 'i18next'

import {
  getApiKeyFormDefaultValues,
  getApiKeyFormSchema,
  transformFormDataToPayload as keyPayload,
} from '@/features/keys/lib/api-key-form'
import { validateBountyDraft } from '@/features/open-source-bounties/validation'
import {
  REDEMPTION_FORM_DEFAULT_VALUES,
  getRedemptionFormSchema,
  transformFormDataToPayload as redemptionPayload,
} from '@/features/redemption-codes/lib/redemption-form'
import {
  PLAN_FORM_DEFAULTS,
  getPlanFormSchema,
  formValuesToPlanPayload,
} from '@/features/subscriptions/lib/plan-form'

import { creditAmountSchema, signedCreditAmountSchema } from './quota-input'

const t = ((key: string) => key) as TFunction
test('money schemas and request mappings preserve literal raw credit and the exact safe bound', () => {
  for (const raw of [0, 1, 500001, Number.MAX_SAFE_INTEGER]) {
    const key = getApiKeyFormSchema(t).parse({
      ...getApiKeyFormDefaultValues(false),
      name: 'key',
      group: 'default',
      unlimited_quota: false,
      remain_quota_credits: raw,
    })
    assert.equal(keyPayload(key).remain_quota, raw)
    const redemption = getRedemptionFormSchema(t).parse({
      ...REDEMPTION_FORM_DEFAULT_VALUES,
      name: 'code',
      quota_credits: raw,
    })
    assert.equal(redemptionPayload(redemption).quota, raw)
    const plan = getPlanFormSchema(t).parse({
      ...PLAN_FORM_DEFAULTS,
      title: 'plan',
      total_amount: raw,
    })
    assert.equal(formValuesToPlanPayload(plan).plan.total_amount, raw)
  }
})
test('all quota payload boundaries reject invalid, negative, fractional and unsafe amounts', () => {
  for (const raw of [
    Number.NaN,
    Infinity,
    -1,
    0.1,
    Number.MAX_SAFE_INTEGER + 1,
  ]) {
    assert.equal(creditAmountSchema.safeParse(raw).success, false)
    assert.equal(
      getApiKeyFormSchema(t).safeParse({
        ...getApiKeyFormDefaultValues(false),
        name: 'key',
        group: 'default',
        unlimited_quota: false,
        remain_quota_credits: raw,
      }).success,
      false
    )
    assert.equal(
      getRedemptionFormSchema(t).safeParse({
        ...REDEMPTION_FORM_DEFAULT_VALUES,
        name: 'code',
        quota_credits: raw,
      }).success,
      false
    )
    assert.equal(
      getPlanFormSchema(t).safeParse({
        ...PLAN_FORM_DEFAULTS,
        title: 'plan',
        total_amount: raw,
      }).success,
      false
    )
    assert.throws(() =>
      keyPayload({
        ...getApiKeyFormDefaultValues(false),
        unlimited_quota: false,
        remain_quota_credits: raw,
      })
    )
    assert.throws(() =>
      redemptionPayload({
        ...REDEMPTION_FORM_DEFAULT_VALUES,
        quota_credits: raw,
      })
    )
    assert.throws(() =>
      formValuesToPlanPayload({ ...PLAN_FORM_DEFAULTS, total_amount: raw })
    )
  }
  assert.equal(signedCreditAmountSchema.safeParse(-1).success, true)
  assert.equal(
    signedCreditAmountSchema.safeParse(-Number.MAX_SAFE_INTEGER - 1).success,
    false
  )
})
test('creation defaults keep their previous raw budget and bounty escrow multiplication is bounded', () => {
  assert.equal(getApiKeyFormDefaultValues(false).remain_quota_credits, 5000000)
  assert.equal(REDEMPTION_FORM_DEFAULT_VALUES.quota_credits, 5000000)
  const draft = {
    repositoryUrl: 'https://github.com/owner/repo',
    title: 'Good title',
    description: 'Detailed scope with enough characters.',
    rules: 'Acceptance requirements with enough characters.',
    rewardAmount: 1,
    rewardSlots: 1,
  }
  assert.equal(
    validateBountyDraft(draft, { rawCredits: true }).rewardAmount,
    undefined
  )
  for (const raw of [0, -1, 0.1, Infinity, Number.MAX_SAFE_INTEGER + 1]) {
    assert.ok(
      validateBountyDraft({ ...draft, rewardAmount: raw }, { rawCredits: true })
        .rewardAmount
    )
  }
  assert.ok(
    validateBountyDraft(
      { ...draft, rewardAmount: Number.MAX_SAFE_INTEGER, rewardSlots: 2 },
      { rawCredits: true }
    ).rewardAmount
  )
})
