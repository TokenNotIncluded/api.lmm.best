/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { getL0AccessCopy } from './l0-access-copy'
import {
  getL0PaidAccess,
  watchL0Access,
  type L0AccessAccount,
  type L0AccessCheckState,
} from './l0-paid-access'

const account = (threshold = 1, paid = 0): L0AccessAccount => ({
  id: 7,
  developer_access_granted: false,
  onboarding: {
    paid_activation_enabled: true,
    paid_activation_min_amount: threshold,
  },
  trust_level_info: { paid_amount: paid },
})
const delay = (ms = 15) => new Promise((resolve) => setTimeout(resolve, ms))
const page = () => Object.assign(new EventTarget(), { hidden: false })

test('a zero threshold offers any successful top-up, not a free upgrade', () => {
  assert.equal(getL0PaidAccess(account(0)).mode, 'topup')
  assert.equal(getL0PaidAccess(account(0, 0.01)).mode, 'sync')
})

test('amounts use credited USD micros and the remaining configured threshold', () => {
  assert.equal(getL0PaidAccess(account(1, 0.6)).remaining, 0.4)
  assert.equal(getL0PaidAccess(account(0.3, 0.1 + 0.2)).mode, 'sync')
  assert.equal(getL0PaidAccess(account(0.000001, 0)).remaining, 0.000001)
})

test('missing and invalid policy data never advertise paid activation', () => {
  assert.equal(getL0PaidAccess(null).mode, 'unknown')
  assert.equal(getL0PaidAccess({ id: 7 }).mode, 'unknown')
  for (const threshold of [NaN, Infinity, -1, Number.MAX_VALUE]) {
    assert.equal(getL0PaidAccess(account(threshold)).mode, 'unknown')
  }
  for (const paid of [NaN, Infinity, -1]) {
    assert.equal(getL0PaidAccess(account(1, paid)).mode, 'unknown')
  }
  const user = account()
  user.onboarding!.details_available = false
  assert.equal(getL0PaidAccess(user).mode, 'unknown')
})

test('disabled paid activation and explicit restrictions remain server controlled', () => {
  const user = account()
  user.onboarding!.paid_activation_enabled = false
  assert.equal(getL0PaidAccess(user).mode, 'review')
  user.onboarding!.paid_activation_enabled = true
  user.trust_level_info!.overridden = true
  assert.equal(getL0PaidAccess(user).mode, 'review')
})

test('meeting the credit threshold does not invent developer permission', () => {
  const user = account(1, 50)
  assert.equal(getL0PaidAccess(user).mode, 'sync')
  assert.equal(user.developer_access_granted, false)
  user.developer_access_granted = true
  assert.equal(getL0PaidAccess(user).mode, 'active')
})

test('a confirmed paid flag asks for synchronization, never another top-up', () => {
  const user = account(1)
  user.onboarding!.paid_activation_complete = true
  assert.equal(getL0PaidAccess(user).mode, 'sync')
})

test('Chinese, traditional Chinese and fallback copy remain explicit about eligibility', () => {
  for (const language of [
    'zhCN',
    'zh-CN',
    'zhTW',
    'zh-TW',
    'en',
    'fr',
    'ru',
    'ja',
    'vi',
  ]) {
    const copy = getL0AccessCopy(language)
    assert.match(copy.eligibility, /LinuxDO Credit/)
    assert.ok(copy.topup.includes('L1'))
    assert.ok(Object.values(copy).every((value) => value.length > 0))
  }
  assert.match(getL0AccessCopy('zh-CN').topup, /充值/)
  assert.match(getL0AccessCopy('zh-TW').topup, /儲值/)
})

test('checking stops after the server grants access without a review-status dependency', async () => {
  const reports: L0AccessCheckState[] = []
  let calls = 0
  const stop = watchL0Access({
    userId: 7,
    target: page(),
    interval: 5,
    read: async () => ({ ...account(), developer_access_granted: ++calls > 1 }),
    report: (state) => reports.push(state),
  })
  await delay(30)
  assert.equal(calls, 2)
  assert.ok(reports.includes('waiting'))
  stop()
})

test('no overlapping requests and no late callbacks after unmount', async () => {
  const target = page()
  let resolve!: (value: L0AccessAccount) => void
  let calls = 0
  const reports: L0AccessCheckState[] = []
  const stop = watchL0Access({
    userId: 7,
    target,
    interval: 5,
    read: () => {
      calls++
      return new Promise((done) => {
        resolve = done
      })
    },
    report: (state) => reports.push(state),
  })
  target.dispatchEvent(new Event('visibilitychange'))
  target.dispatchEvent(new Event('visibilitychange'))
  assert.equal(calls, 1)
  stop()
  resolve(account())
  await delay()
  assert.deepEqual(reports, ['checking'])
  assert.equal(calls, 1)
})

test('hidden tabs do not poll; visibility resumes checks and cleanup removes the listener', async () => {
  const target = page()
  target.hidden = true
  let calls = 0
  const stop = watchL0Access({
    userId: 7,
    target,
    read: async () => {
      calls++
      return account()
    },
    report: () => {},
  })
  await delay()
  assert.equal(calls, 0)
  target.hidden = false
  target.dispatchEvent(new Event('visibilitychange'))
  await delay()
  assert.equal(calls, 1)
  stop()
  target.dispatchEvent(new Event('visibilitychange'))
  await delay()
  assert.equal(calls, 1)
})

test('failure is visible and checking has a deadline rather than polling forever', async () => {
  const reports: L0AccessCheckState[] = []
  const stop = watchL0Access({
    userId: 7,
    target: page(),
    interval: 5,
    duration: 20,
    read: async () => {
      throw new Error('offline')
    },
    report: (state) => reports.push(state),
  })
  await delay(60)
  assert.ok(reports.includes('error'))
  assert.equal(reports.at(-1), 'timeout')
  const length = reports.length
  await delay()
  assert.equal(reports.length, length)
  stop()
})

test('a different account response stops the old account watcher', async () => {
  let calls = 0
  const reports: L0AccessCheckState[] = []
  const stop = watchL0Access({
    userId: 7,
    target: page(),
    interval: 5,
    read: async () => {
      calls++
      return { ...account(), id: 8 }
    },
    report: (state) => reports.push(state),
  })
  await delay(20)
  assert.equal(calls, 1)
  assert.deepEqual(reports, ['checking'])
  stop()
})
