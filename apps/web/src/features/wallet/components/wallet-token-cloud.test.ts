/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'

import {
  createWalletTokenLayout,
  walletCloudExtent,
  walletCloudParticleCount,
} from '../lib/wallet-token-cloud'
import { WalletTokenCloud } from './wallet-token-cloud'

test('larger balances and purchase amounts make strictly denser clouds', () => {
  for (const variant of ['balance', 'preset'] as const) {
    const amounts = [0, 10, 20, 35, 50, 100, 200, 500, 1000]
    const counts = amounts.map((amount) =>
      walletCloudParticleCount(amount, variant)
    )
    assert.equal(counts[0], 0)
    for (let index = 1; index < counts.length; index += 1) {
      assert.ok(counts[index] > counts[index - 1])
    }
    assert.ok(walletCloudExtent(1000, variant) > walletCloudExtent(10, variant))
  }
})

test('invalid balances cannot produce particles', () => {
  assert.equal(walletCloudParticleCount(Number.NaN, 'balance'), 0)
  assert.equal(walletCloudParticleCount(-1, 'preset'), 0)
})

test('token placement is varied but stable across renders', () => {
  const first = createWalletTokenLayout(80)
  assert.deepEqual(first, createWalletTokenLayout(80))
  assert.ok(new Set(first.map((token) => `${token.x},${token.y}`)).size > 75)
  assert.ok(new Set(first.map((token) => token.glyph)).size >= 6)
})

test('the cloud is made of readable token glyphs rather than points', () => {
  const markup = renderToStaticMarkup(
    createElement(WalletTokenCloud, { amount: 100 })
  )
  assert.match(markup, /<text[^>]*>token<\/text>/)
  assert.match(markup, /<text[^>]*>\{\}<\/text>/)
  assert.doesNotMatch(markup, /<circle/)
})
