/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { Window } from 'happy-dom'
import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import type { TrustLevelInfo, TrustLevelTier } from '@/stores/auth-store'

import type { UserWalletData } from '../types'
import { getTrustLevelProgress } from './trust-level-display'
import { TrustLevelPanel } from './trust-level-panel'

const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const tiers: TrustLevelTier[] = [0, 1, 2, 3, 4].map((level) => ({
  level,
  min_paid_amount: level,
  min_paid_credits: String(level * 500000),
  discount_percent: level,
}))
function info(level: number): TrustLevelInfo {
  return {
    level,
    automatic_level: level >= 5 ? 0 : level,
    override_level: null,
    paid_amount: 0,
    paid_credits: '0',
    discount_ratio: 1,
    discount_percent: 0,
    inactivity_decay_steps: 0,
    decay_period_days: 90,
    overridden: false,
  }
}
const walletFixture: UserWalletData = {
  id: 1,
  username: 'trust-display-fixture',
  quota: 1000000,
  used_quota: 50000,
  request_count: 42,
  aff_quota: 0,
  aff_history_quota: 0,
  aff_count: 0,
  group: 'default',
}
function render(level: number, role?: number) {
  const user: UserWalletData = {
    ...walletFixture,
    role,
    trust_level_info: info(level),
    trust_level_tiers: [
      ...tiers,
      {
        level: 5,
        min_paid_amount: 999,
        min_paid_credits: '999',
        discount_percent: 0,
      },
    ],
    trust_level_role_tiers: [
      {
        level: 5,
        role: 10,
        role_only: true,
        discount_ratio: 0.9,
        discount_percent: 10,
        benefits: ['administrator_access'],
      },
      {
        level: 6,
        role: 100,
        role_only: true,
        discount_ratio: 0.85,
        discount_percent: 15,
        benefits: ['superadministrator_access'],
      },
    ],
  }
  return renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <TrustLevelPanel user={user} />
    </I18nextProvider>
  )
}

test('L5 and L6 render role access without a recharge progress bar or eligible-balance claim', () => {
  for (const level of [5, 6]) {
    const markup = render(level)
    assert.match(markup, new RegExp(`L${level}`))
    assert.match(markup, /role-level-explanation/)
    assert.match(markup, /Assigned by account role/)
    assert.match(markup, /15%/)
    assert.match(markup, /Super administrator access/)
    assert.doesNotMatch(
      markup,
      /recharge-level-progress|role="progressbar"|Cumulative eligible recharge|Eligible credited balance|100%/
    )
    const document = new Window().document
    document.body.innerHTML = markup
    assert.doesNotMatch(document.body.textContent, /999/)
  }
  assert.match(render(0, 100), /L6/)
})

test('the automatic ladder uses exact cumulative credits before legacy dollar projections', () => {
  const state = {
    ...info(1),
    paid_amount: 9999,
    paid_credits: '750000',
    next_level: 2,
  }
  assert.equal(getTrustLevelProgress(state, tiers).progress, 50)
  const bigTiers = [
    { ...tiers[1], min_paid_credits: '9007199254740992' },
    { ...tiers[2], min_paid_credits: '9007199254741092' },
  ]
  assert.equal(
    getTrustLevelProgress(
      { ...state, paid_credits: '9007199254741042' },
      bigTiers
    ).progress,
    50
  )
  assert.equal(
    getTrustLevelProgress({ ...state, paid_credits: 'not-an-amount' }, tiers)
      .progress,
    null
  )
  assert.equal(
    getTrustLevelProgress({ ...state, next_level: 5 }, tiers).nextLevel,
    null
  )
  assert.equal(
    getTrustLevelProgress({ ...state, level_source: 'role' }, tiers).progress,
    null
  )
  assert.equal(
    getTrustLevelProgress(
      { ...state, paid_credit_projection_available: false },
      tiers
    ).progress,
    null
  )
  assert.equal(
    getTrustLevelProgress(
      {
        ...state,
        paid_credits: undefined,
        paid_credit_projection_available: false,
      },
      tiers
    ).progress,
    null
  )
})

test('the highest automatic level has no fabricated next-step percentage', () => {
  const markup = render(4)
  assert.match(markup, /Highest automatic level reached/)
  assert.doesNotMatch(markup, /recharge-level-progress|role="progressbar"|100%/)
})

test('unavailable canonical recharge history never becomes a zero balance or legacy progress', () => {
  const user: UserWalletData = {
    ...walletFixture,
    trust_level_info: {
      ...info(1),
      paid_credits: null,
      paid_amount: 9999,
      paid_credit_projection_available: false,
      next_level: 2,
      amount_to_next_level: 0,
    },
    trust_level_tiers: tiers,
  }
  const markup = renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <TrustLevelPanel user={user} />
    </I18nextProvider>
  )
  assert.match(markup, /Cumulative eligible recharge.*Unavailable/)
  assert.doesNotMatch(
    markup,
    /recharge-level-progress|role="progressbar"|100%|needed for L2/
  )
  assert.equal(
    getTrustLevelProgress(user.trust_level_info, tiers).automaticLevel,
    null
  )
})

test('configured fractional usage discounts remain visible for automatic and role levels', () => {
  const user: UserWalletData = {
    ...walletFixture,
    trust_level_info: { ...info(2), discount_percent: 12.5 },
    trust_level_tiers: tiers.map((tier) => ({
      ...tier,
      discount_percent: 12.5,
    })),
    trust_level_role_tiers: [
      {
        level: 5,
        role: 10,
        role_only: true,
        discount_ratio: 0.9275,
        discount_percent: 7.25,
        benefits: [],
      },
    ],
  }
  const markup = renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <TrustLevelPanel user={user} />
    </I18nextProvider>
  )
  assert.match(markup, /12\.5%/)
  assert.match(markup, /7\.25%/)
  assert.doesNotMatch(markup, /13%/)
})
