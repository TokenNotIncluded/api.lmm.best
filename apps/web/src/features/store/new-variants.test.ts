/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { storeNewVariantInput, storeNewVariantsError } from './new-variants'
import type { StoreVariantInput } from './types'

const variant = (name = 'Monthly'): StoreVariantInput => ({
  name,
  price_quota: 500000,
  template: 'card-key',
  enabled: true,
})
test('new specification validation preserves exact prices and detects ambiguous names', () => {
  assert.equal(
    storeNewVariantsError(
      [variant(), { ...variant('Annual'), price_quota: 6000000 }],
      0
    ),
    undefined
  )
  for (const variants of [
    [],
    [variant(), variant(' monthly ')],
    [{ ...variant(), price_quota: Number.NaN }],
    [{ ...variant(), name: '规'.repeat(67) }],
    [{ ...variant(), enabled: false }],
  ]) {
    assert.ok(storeNewVariantsError(variants, 0))
  }
  assert.ok(storeNewVariantsError([variant()], undefined))
  assert.ok(storeNewVariantsError([variant()], 500001))
  assert.ok(
    storeNewVariantsError(
      Array.from({ length: 201 }, (_, i) => variant(String(i))),
      0
    )
  )
})
test('new fixed specifications require private content and never leak it to stock templates', () => {
  assert.ok(
    storeNewVariantsError([{ ...variant(), template: 'fixed-content' }], 0)
  )
  assert.ok(
    storeNewVariantsError(
      [{ ...variant(), template: 'fixed-content', fixed_content: 'a\0b' }],
      0
    )
  )
  const fixed = {
    ...variant(),
    template: 'fixed-content' as const,
    fixed_content: 'private delivery',
  }
  assert.equal(storeNewVariantsError([fixed], 0), undefined)
  assert.equal(storeNewVariantInput(fixed).fixed_content, 'private delivery')
  assert.deepEqual(
    storeNewVariantInput({ ...fixed, template: 'card-key' }),
    variant()
  )
})
