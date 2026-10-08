/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { storePurchaseLimit } from './purchase-limits'

test('seller limits accept arbitrary safe whole quantities or explicitly unlimited', () => {
  assert.equal(storePurchaseLimit(''), null)
  assert.equal(storePurchaseLimit('  '), null)
  for (const value of ['1', '7', '234', '9007199254740991']) {
    assert.equal(storePurchaseLimit(value), Number(value))
  }
  for (const value of [
    '0',
    '-1',
    '1.5',
    '1e3',
    '1.0',
    '01',
    '9007199254740992',
  ]) {
    assert.equal(storePurchaseLimit(value), undefined)
  }
})
