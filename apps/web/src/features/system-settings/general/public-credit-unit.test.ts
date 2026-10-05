/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  parsePublicCreditOption,
  updatePublicCreditUnitOption,
} from './public-credit-unit'

test('only the fixed 500000 per USD credit basis is accepted', () => {
  assert.equal(parsePublicCreditOption('500000'), 500000)
  for (const value of [
    undefined,
    '',
    '100000',
    '3359744',
    '0',
    '-1',
    '500000.1',
    'Infinity',
  ]) {
    assert.equal(parsePublicCreditOption(value), undefined)
  }
})

test('a changed display denomination fails before any network write', async () => {
  await assert.rejects(
    updatePublicCreditUnitOption({
      key: 'PublicCreditsPerUSD',
      value: '100000',
      publicCreditUnitBaseline: {
        publicCreditsPerUsd: 500000,
        ledgerQuotaPerUsd: 500000,
      },
    }),
  )
})
