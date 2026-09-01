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
import { afterEach, test } from 'node:test'

import { api } from '@/lib/api'

import {
  getAllBillingHistory,
  getUserBillingHistory,
  sendAffiliateInvitation,
} from './api'

const originalGet = api.get
const originalPost = api.post

afterEach(() => {
  api.get = originalGet
  api.post = originalPost
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
