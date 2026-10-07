/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  storeCheckoutCapacity,
  storeClampQuantity,
  storeQuantity,
} from './quantity'
import type { StorePaymentMethod } from './types'
import { storeTotal } from './utils'

const product = { price_quota: 500000, available_stock: 2000 }

test('unlimited fixed content keeps zero stock and honors payment, sales, order and buyer caps', () => {
  const fixed = {
    ...product,
    available_stock: 0,
    sale_available: 0,
    unlimited_supply: true,
    sale_limit: null,
  }
  assert.equal(storeCheckoutCapacity(fixed, 'balance'), 1000)
  assert.equal(storeCheckoutCapacity(fixed, 'external:epay'), 100)
  assert.equal(
    storeCheckoutCapacity({ ...fixed, max_quantity_per_order: 4 }, 'balance'),
    4
  )
  assert.equal(
    storeCheckoutCapacity(
      { ...fixed, max_quantity_per_buyer: 10, buyer_purchase_remaining: 2 },
      'balance'
    ),
    2
  )
  assert.equal(
    storeCheckoutCapacity({ ...fixed, max_quantity_per_buyer: 10 }, 'balance'),
    0
  )
  assert.equal(
    storeCheckoutCapacity(
      { ...fixed, sale_limit: 12, sale_available: 3 },
      'balance'
    ),
    3
  )
  assert.equal(
    storeCheckoutCapacity({ ...fixed, sale_limit: 12 }, 'balance'),
    0
  )
  assert.equal(
    storeCheckoutCapacity(
      { ...fixed, sale_limit: 12, sale_available: undefined },
      'balance'
    ),
    0
  )
})

test('quantity takes the minimum of stock, sales quota, order cap and buyer remainder', () => {
  const limited = {
    ...product,
    sale_available: 30,
    max_quantity_per_order: 7,
    max_quantity_per_buyer: 20,
    buyer_purchase_remaining: 5,
  }
  assert.equal(storeCheckoutCapacity(limited, 'balance'), 5)
  assert.equal(
    storeCheckoutCapacity({ ...limited, available_stock: 2 }, 'balance'),
    2
  )
  assert.equal(
    storeCheckoutCapacity({ ...limited, sale_available: 3 }, 'balance'),
    3
  )
  assert.equal(
    storeCheckoutCapacity(
      { ...limited, buyer_purchase_remaining: 12 },
      'balance'
    ),
    7
  )
  assert.equal(
    storeCheckoutCapacity(
      { ...limited, buyer_purchase_remaining: 0 },
      'balance'
    ),
    0
  )
  assert.equal(
    storeCheckoutCapacity(
      { ...limited, buyer_purchase_remaining: undefined },
      'balance'
    ),
    0
  )
  assert.equal(
    storeCheckoutCapacity(
      {
        ...product,
        max_quantity_per_order: null,
        max_quantity_per_buyer: null,
      },
      'balance'
    ),
    1000
  )
  assert.equal(
    storeCheckoutCapacity({ ...product, max_quantity_per_order: 0 }, 'balance'),
    0
  )
})

test('bounds updates retain valid counts and clamp stale or invalid counts to at least one', () => {
  assert.equal(storeClampQuantity('7', 3), '3')
  assert.equal(storeClampQuantity('3', 7), '3')
  assert.equal(storeClampQuantity('3', 0), '1')
  assert.equal(storeClampQuantity('', 7), '1')
})

test('only positive safe whole-number quantity input is accepted', () => {
  assert.equal(storeQuantity('1'), 1)
  assert.equal(storeQuantity('1000'), 1000)
  for (const invalid of [
    '',
    '0',
    '-1',
    '1.5',
    '1.0',
    '1e2',
    ' 2 ',
    '9007199254740992',
  ]) {
    assert.equal(storeQuantity(invalid), undefined, invalid)
  }
})

test('all gateway methods including platform payments are capped at 100 and balance at 1000', () => {
  for (const method of [
    'platform:waffo_pancake',
    'platform:linuxdo',
    'external:epay',
    'external:waffo_pancake',
  ] satisfies StorePaymentMethod[]) {
    assert.equal(storeCheckoutCapacity(product, method), 100, method)
  }
  assert.equal(storeCheckoutCapacity(product, 'balance'), 1000)
})

test('stock and safe integer total price bound quantity before ordering', () => {
  assert.equal(
    storeCheckoutCapacity({ ...product, available_stock: 2 }, 'balance'),
    2
  )
  assert.equal(
    storeCheckoutCapacity({ ...product, available_stock: 0 }, 'balance'),
    0
  )
  const expensive = { ...product, price_quota: Number.MAX_SAFE_INTEGER }
  assert.equal(storeCheckoutCapacity(expensive, 'balance'), 1)
  assert.equal(storeTotal(expensive.price_quota, 1), Number.MAX_SAFE_INTEGER)
  assert.throws(() => storeTotal(expensive.price_quota, 2), /Invalid amount/)
  assert.equal(storeTotal(product.price_quota, 3), 1500000)
})

test('invalid server stock or credit prices never enable checkout', () => {
  for (const stock of [
    -1,
    1.5,
    Number.NaN,
    Infinity,
    Number.MAX_SAFE_INTEGER + 1,
  ]) {
    assert.equal(
      storeCheckoutCapacity({ ...product, available_stock: stock }, 'balance'),
      0
    )
  }
  for (const price of [
    0,
    -1,
    0.5,
    Number.NaN,
    Infinity,
    Number.MAX_SAFE_INTEGER + 1,
  ]) {
    assert.equal(
      storeCheckoutCapacity({ ...product, price_quota: price }, 'balance'),
      0
    )
  }
})
