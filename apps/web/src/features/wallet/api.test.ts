/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import {
  requestPayment,
  requestStripePayment,
  requestCreemPayment,
  requestWaffoPayment,
  requestWaffoPancakePayment,
  getAllBillingHistory,
  getUserBillingHistory,
  sendAffiliateInvitation,
} from './api'
import { prepareTopup, readPendingTopups } from './lib/topup-cloud-storage'

const dom = new Window({ url: 'https://example.test/wallet' })
Object.defineProperty(globalThis, 'window', { configurable: true, value: dom })
after(() => dom.close())

const originalGet = api.get
const originalPost = api.post

afterEach(() => {
  api.get = originalGet
  api.post = originalPost
  dom.localStorage.clear()
  useAuthStore.getState().auth.reset()
})

test('billing history APIs send the global sort contract to user and admin routes', async () => {
  const capturedUrls: string[] = []
  api.get = (async (url: string) => {
    capturedUrls.push(url)
    return { data: { success: true, data: { items: [], total: 0 } } }
  }) as typeof api.get

  await getUserBillingHistory(2, 25, 'order 42', 'money', 'asc')
  await getAllBillingHistory(3, 50, '', 'payment_method', 'desc')

  assert.equal(
    capturedUrls[0],
    '/api/user/topup/self?p=2&page_size=25&sort_by=money&sort_order=asc&keyword=order+42'
  )
  assert.equal(
    capturedUrls[1],
    '/api/user/topup?p=3&page_size=50&sort_by=payment_method&sort_order=desc'
  )
})

test('sendAffiliateInvitation posts only the recipient to the SMTP-backed route', async () => {
  let capturedUrl = ''
  let capturedBody: unknown
  let capturedConfig: Record<string, unknown> | undefined

  api.post = (async (
    url: string,
    body: unknown,
    config?: Record<string, unknown>
  ) => {
    capturedUrl = url
    capturedBody = body
    capturedConfig = config
    return { data: { success: true, message: 'sent' } }
  }) as typeof api.post

  const response = await sendAffiliateInvitation({
    email: 'friend@example.com',
  })

  assert.deepEqual(response, { success: true, message: 'sent' })
  assert.equal(capturedUrl, '/api/user/aff/invite')
  assert.deepEqual(capturedBody, { email: 'friend@example.com' })
  assert.equal(capturedConfig?.skipBusinessError, true)
})

test('all five gateways bind the server order before returning to redirect hooks', async () => {
  useAuthStore.getState().auth.setUser({ id: 7, username: 'test', role: 1 })
  const calls = [
    () => requestPayment({ amount: 10, payment_method: 'alipay' }),
    () => requestStripePayment({ amount: 10, payment_method: 'stripe' }),
    () =>
      requestCreemPayment({
        product_id: 'test-product',
        payment_method: 'creem',
      }),
    () => requestWaffoPayment({ amount: 10 }),
    () => requestWaffoPancakePayment({ amount: 10 }),
  ]
  for (const [index, invoke] of calls.entries()) {
    const intent = prepareTopup(7, 0, 10)
    const order = `gateway-order-${index}`
    api.post = (async () => ({
      data: { success: true, data: { trade_no: order } },
    })) as typeof api.post
    await invoke()
    assert.equal(
      readPendingTopups(7).find((x) => x.attemptId === intent.attemptId)
        ?.tradeNo,
      order
    )
  }
})

test('a late gateway response cannot bind a receipt after account switch', async () => {
  useAuthStore.getState().auth.setUser({ id: 7, username: 'first', role: 1 })
  prepareTopup(7, 0, 10)
  let resolve!: (response: {
    data: { success: boolean; trade_no: string }
  }) => void
  const delayed = new Promise<{ data: { success: boolean; trade_no: string } }>(
    (accept) => {
      resolve = accept
    }
  )
  api.post = (() => delayed) as typeof api.post
  const response = requestPayment({ amount: 10, payment_method: 'alipay' })
  useAuthStore.getState().auth.setUser({ id: 8, username: 'second', role: 1 })
  resolve({ data: { success: true, trade_no: 'first-users-order' } })
  await response
  assert.equal(readPendingTopups(7)[0]?.tradeNo, undefined)
  assert.deepEqual(readPendingTopups(8), [])
})
