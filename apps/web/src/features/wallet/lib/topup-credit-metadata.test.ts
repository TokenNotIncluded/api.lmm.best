/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  creditProjection,
  hasCompleteCreditGrant,
  hasCompletePublicCreditCatalog,
} from './topup-credit-metadata'

const units = {
  credit_unit_schema_version: 2,
  quota_unit: 'LEDGER_QUOTA',
  legacy_credit_unit: 'LEDGER_QUOTA',
  public_credit_unit: 'CREDIT',
  ledger_quota_per_usd: 500000,
  ledger_quota_per_usd_exact: '500000',
  public_credits_per_usd: 500000,
  public_credits_per_usd_exact: '500000',
}
const catalog = {
  ...units,
  credit_metadata_available: true,
  credit_metadata_version: 1,
  public_credit_metadata_version: 2,
  public_credit_amount_unit: 'CREDIT',
  credit_amount_options: [10, 20],
  ledger_quota_amount_options: [10, 20],
  public_credit_amount_options: ['10', '20'],
  credit_discount: { '10': 0.9 },
  ledger_quota_discount: { '10': 0.9 },
  public_credit_discount: { '10': 0.9 },
  credit_min_topup: 1,
  ledger_quota_min_topup: 1,
  public_credit_min_topup: '1',
  stripe_credit_min_topup: 10,
  stripe_ledger_quota_min_topup: 10,
  stripe_public_credit_min_topup: '10',
  waffo_credit_min_topup: 0,
  waffo_ledger_quota_min_topup: 0,
  waffo_public_credit_min_topup: '0',
  pancake_credit_min_topup: 0,
  pancake_ledger_quota_min_topup: 0,
  pancake_public_credit_min_topup: '0',
  stripe_credit_max_topup: 100,
  stripe_ledger_quota_max_topup: 100,
  stripe_public_credit_max_topup: '100',
  waffo_credit_max_topup: null,
  waffo_ledger_quota_max_topup: null,
  waffo_public_credit_max_topup: null,
  pancake_credit_max_topup: null,
  pancake_ledger_quota_max_topup: null,
  pancake_public_credit_max_topup: null,
  pay_methods: [
    {
      name: 'Card',
      type: 'card',
      credit_amount_unit: 'LEDGER_QUOTA',
      min_topup_credit: '1',
      min_topup_ledger_quota: '1',
      min_topup_public_credit: '1',
    },
  ],
}

test('captures fixed units, preserves raw points, and rejects nonidentity projections', () => {
  const project = creditProjection(units)
  assert.ok(project)
  assert.equal(project(Number.MAX_SAFE_INTEGER), '9007199254740991')
  const repeating = creditProjection({
    ...units,
    ledger_quota_per_usd: 3,
    ledger_quota_per_usd_exact: '3',
  })
  assert.equal(repeating, null)
  assert.equal(hasCompletePublicCreditCatalog(catalog), true)
})

test('rejects old, partial, unknown and mixed-basis metadata instead of using legacy aliases', () => {
  for (const key of [
    'credit_metadata_available',
    'credit_unit_schema_version',
    'quota_unit',
    'legacy_credit_unit',
    'public_credit_unit',
    'ledger_quota_per_usd',
    'ledger_quota_per_usd_exact',
    'public_credits_per_usd',
    'public_credits_per_usd_exact',
    'credit_metadata_version',
    'public_credit_metadata_version',
    'public_credit_amount_unit',
    'ledger_quota_amount_options',
    'public_credit_amount_options',
    'ledger_quota_discount',
    'public_credit_discount',
    'stripe_ledger_quota_max_topup',
    'stripe_public_credit_max_topup',
    'waffo_ledger_quota_max_topup',
    'pancake_public_credit_max_topup',
  ]) {
    const incomplete: Record<string, unknown> = { ...catalog }
    delete incomplete[key]
    assert.equal(hasCompletePublicCreditCatalog(incomplete), false, key)
  }
  for (const override of [
    { credit_unit_schema_version: 3 },
    { public_credit_metadata_version: 1 },
    { quota_unit: 'CREDIT' },
    { legacy_credit_unit: 'CREDIT' },
    { public_credits_per_usd: 100000, public_credits_per_usd_exact: '100000' },
    { public_credits_per_usd_exact: '02' },
    { ledger_quota_per_usd_exact: '6' },
    { credit_amount_options: [10, 21] },
    { public_credit_amount_options: ['8', '4'] },
    { credit_discount: { '10': 0.8 } },
    { public_credit_discount: { '4.00001': 0.9 } },
    { ledger_quota_amount_options: [10, Number.MAX_SAFE_INTEGER + 1] },
    { stripe_public_credit_max_topup: null },
  ]) {
    assert.equal(
      hasCompletePublicCreditCatalog({ ...catalog, ...override }),
      false
    )
  }
})

test('method raw limits and public projections must be paired; unlimited caps need no guessed value', () => {
  for (const override of [
    { min_topup_public_credit: undefined },
    { min_topup_ledger_quota: '0.4' },
    { credit_amount_unit: 'CREDIT' },
    { max_topup_public_credit: '40' },
    { max_topup_ledger_quota: '100', max_topup_credit: '100' },
  ]) {
    assert.equal(
      hasCompletePublicCreditCatalog({
        ...catalog,
        pay_methods: [{ ...catalog.pay_methods[0], ...override }],
      }),
      false
    )
  }
  assert.equal(
    hasCompletePublicCreditCatalog({
      ...catalog,
      pay_methods: JSON.stringify(catalog.pay_methods),
    }),
    true
  )
})

test('successful grant metadata identifies the immutable quota and captured public basis', () => {
  const grant = {
    ...units,
    public_credit_metadata_version: 2,
    credited_quota: 10,
    credit_amount: 10,
    credit_amount_unit: 'LEDGER_QUOTA',
    public_credit_amount_unit: 'CREDIT',
    public_credit_amount: '10',
  }
  assert.equal(hasCompleteCreditGrant(grant, 10), true)
  assert.equal(hasCompleteCreditGrant(grant, 4), false)
  assert.equal(hasCompleteCreditGrant(grant, undefined, 10), true)
  assert.equal(hasCompleteCreditGrant(grant, undefined, 3), false)
  const fractionalLedger = {
    ...grant,
    ledger_quota_per_usd: 2.5,
    ledger_quota_per_usd_exact: '2.5',
    credited_quota: 3,
    credit_amount: 3,
    public_credit_amount: '2.4',
  }
  assert.equal(hasCompleteCreditGrant(fractionalLedger, undefined, 3), false)
  assert.equal(
    hasCompleteCreditGrant(
      {
        ...fractionalLedger,
        credited_quota: 4,
        credit_amount: 4,
        public_credit_amount: '3.2',
      },
      undefined,
      3
    ),
    false
  )
  for (const override of [
    { public_credit_amount: '4' },
    { credit_amount: 4 },
    { public_credit_metadata_version: undefined },
    { public_credit_amount_unit: 'LEDGER_QUOTA' },
  ]) {
    assert.equal(hasCompleteCreditGrant({ ...grant, ...override }, 10), false)
  }
})
