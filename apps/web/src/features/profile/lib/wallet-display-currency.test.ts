/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  sameWalletDisplayOwner,
  walletDisplayCurrencyPreference,
  walletDisplayOwnerKey,
} from './wallet-display-currency'

describe('wallet display preferences', () => {
  test('reads only the independent wallet preference from JSON or settings records', () => {
    assert.equal(
      walletDisplayCurrencyPreference({
        wallet_display_currency: 'CREDIT',
        settlement_currency: 'CNY',
      }),
      'CREDIT'
    )
    assert.equal(
      walletDisplayCurrencyPreference(
        '{"wallet_display_currency":"USD","settlement_currency":"CNY"}'
      ),
      'USD'
    )
    assert.equal(
      walletDisplayCurrencyPreference({ settlement_currency: 'CNY' }),
      ''
    )
  })

  test('unsupported or malformed preferences follow language', () => {
    for (const value of [
      null,
      'broken-json',
      [],
      4,
      { wallet_display_currency: 'EUR' },
      { wallet_display_currency: null },
    ]) {
      assert.equal(walletDisplayCurrencyPreference(value), '')
    }
  })

  test('keeps user, session and access-token changes in separate owners', () => {
    const owner = {
      user: { id: 1 },
      session: { sid: 'test-session' },
      accessToken: 'test-token',
    }
    assert.equal(
      sameWalletDisplayOwner(owner, { ...owner, user: { id: 1 } }),
      true
    )
    for (const other of [
      { ...owner, user: { id: 2 } },
      { ...owner, session: { sid: 'other-session' } },
      { ...owner, accessToken: 'rotated-test-token' },
    ]) {
      assert.equal(sameWalletDisplayOwner(owner, other), false)
      assert.notEqual(
        walletDisplayOwnerKey(owner),
        walletDisplayOwnerKey(other)
      )
    }
  })
})
