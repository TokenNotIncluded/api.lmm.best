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

import { sendAffiliateInvitation } from './api'

const originalPost = api.post

afterEach(() => {
  api.post = originalPost
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
