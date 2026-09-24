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
  createWalletTokenSeed,
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

test('token placement is varied, seedable and stable within one cloud', () => {
  const first = createWalletTokenLayout(80, 0x12345678)
  const repeated = createWalletTokenLayout(80, 0x12345678)
  const different = createWalletTokenLayout(80, 0x87654321)

  assert.deepEqual(first, repeated)
  assert.notDeepEqual(first, different)
  assert.ok(new Set(first.map((token) => `${token.x},${token.y}`)).size > 75)
  assert.ok(new Set(first.map((token) => token.glyph)).size >= 20)
})

test('token glyphs are procedurally generated instead of a fixed vocabulary', () => {
  const tokens = createWalletTokenLayout(240, 0x51a7c10d)
  const glyphs = tokens.map((token) => token.glyph)
  const uniqueGlyphs = new Set(glyphs)

  assert.ok(uniqueGlyphs.size >= 60)
  assert.ok(glyphs.some((glyph) => /[a-z]/.test(glyph)))
  assert.ok(glyphs.some((glyph) => /\d/.test(glyph)))
  assert.ok(glyphs.some((glyph) => /[^a-z\d]/.test(glyph)))
})

test('wallet token glyphs never look like a dollar-denominated credit label', () => {
  for (let seed = 1; seed <= 64; seed += 1) {
    const glyphs = createWalletTokenLayout(240, seed).map((token) => token.glyph)
    assert.equal(glyphs.some((glyph) => glyph.includes('

test('runtime seeds are valid unsigned integers', () => {
  const seed = createWalletTokenSeed()
  assert.ok(Number.isInteger(seed))
  assert.ok(seed > 0)
  assert.ok(seed <= 0xffffffff)
})

test('the cloud is made of readable token glyphs rather than points', () => {
  const markup = renderToStaticMarkup(
    createElement(WalletTokenCloud, { amount: 100 })
  )
  const glyphs = Array.from(
    markup.matchAll(/<text[^>]*>([^<]+)<\/text>/g),
    (match) => match[1]
  )

  assert.ok(glyphs.length > 20)
  assert.ok(new Set(glyphs).size >= 8)
  assert.doesNotMatch(markup, /<circle/)
})
)), false)
  }
})

test('runtime seeds are valid unsigned integers', () => {
  const seed = createWalletTokenSeed()
  assert.ok(Number.isInteger(seed))
  assert.ok(seed > 0)
  assert.ok(seed <= 0xffffffff)
})

test('the cloud is made of readable token glyphs rather than points', () => {
  const markup = renderToStaticMarkup(
    createElement(WalletTokenCloud, { amount: 100 })
  )
  const glyphs = Array.from(
    markup.matchAll(/<text[^>]*>([^<]+)<\/text>/g),
    (match) => match[1]
  )

  assert.ok(glyphs.length > 20)
  assert.ok(new Set(glyphs).size >= 8)
  assert.doesNotMatch(markup, /<circle/)
})
