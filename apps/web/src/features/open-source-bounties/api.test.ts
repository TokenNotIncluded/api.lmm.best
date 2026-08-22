/*
Copyright (C) 2023-2026 QuantumNous

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

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { afterEach, describe, test } from 'node:test'

import { api } from '@/lib/api'

import {
  archiveBounty,
  cancelChallenge,
  listOwnedBounties,
  listCompatibleBountyNotifications,
  listBountyNotifications,
  listBounties,
  listReceivedBountyTips,
  markBountyNotificationsRead,
  markCompatibleBountyNotificationsRead,
  markReceivedBountyTipsRead,
  openBountyDispute,
  thankBountyTip,
  tipChallenge,
  unarchiveBounty,
} from './api'

const originalGet = api.get
const originalPost = api.post

afterEach(() => {
  api.get = originalGet
  api.post = originalPost
})

describe('open-source bounty lists', () => {
  test('normalizes a null project list from an empty database to an array', async () => {
    api.get = (async () => ({
      data: {
        success: true,
        data: { items: null, total: 0 },
      },
    })) as typeof api.get

    const result = await listBounties()

    assert.deepEqual(result, { items: [], total: 0 })
  })

  test('keeps active and archived owner lists separate', async () => {
    const gets: string[] = []
    api.get = (async (url) => {
      gets.push(url)
      return { data: { success: true, data: [] } }
    }) as typeof api.get

    await listOwnedBounties()
    await listOwnedBounties(true)

    assert.deepEqual(gets, [
      '/api/open-source-bounties/mine?archived=false',
      '/api/open-source-bounties/mine?archived=true',
    ])
  })
})

describe('open-source bounty archive actions', () => {
  test('posts archive and restore actions to the selected project', async () => {
    const posts: string[] = []
    api.post = (async (url) => {
      posts.push(url)
      return { data: { success: true, data: { id: 42, archived_at: 1 } } }
    }) as typeof api.post

    await archiveBounty(42)
    await unarchiveBounty(42)

    assert.deepEqual(posts, [
      '/api/open-source-bounties/projects/42/archive',
      '/api/open-source-bounties/projects/42/unarchive',
    ])
  })
})

describe('open-source bounty tips', () => {
  test('sends the supplied idempotency key unchanged across a retry', async () => {
    const requests: Array<{
      url: string
      data: unknown
      idempotencyKey: string | undefined
    }> = []

    api.post = (async (url, data, config) => {
      const headers = config?.headers as Record<string, string> | undefined
      requests.push({
        url,
        data,
        idempotencyKey: headers?.['Idempotency-Key'],
      })
      return {
        data: {
          success: true,
          data: {
            challenge: {},
            transferred_quota: 250_000,
            remaining_quota: 750_000,
          },
        },
      }
    }) as typeof api.post

    const idempotencyKey = '56c69ad7-64c3-4c66-' + '91ed-044837157f5f'
    const input = { quota: 250_000, note: 'partial progress' }

    await tipChallenge(42, input, idempotencyKey)
    await tipChallenge(42, input, idempotencyKey)

    assert.deepEqual(requests, [
      {
        url: '/api/open-source-bounties/challenges/42/tip',
        data: input,
        idempotencyKey,
      },
      {
        url: '/api/open-source-bounties/challenges/42/tip',
        data: input,
        idempotencyKey,
      },
    ])
  })

  test('uses recipient-scoped notification and thank endpoints', async () => {
    const gets: string[] = []
    const posts: string[] = []
    api.get = (async (url) => {
      gets.push(url)
      return { data: { success: true, data: [] } }
    }) as typeof api.get
    api.post = (async (url) => {
      posts.push(url)
      return { data: { success: true, data: null } }
    }) as typeof api.post

    await listReceivedBountyTips()
    await markReceivedBountyTipsRead()
    await thankBountyTip(17)

    assert.deepEqual(gets, ['/api/open-source-bounties/tips/received'])
    assert.deepEqual(posts, [
      '/api/open-source-bounties/tips/received/read',
      '/api/open-source-bounties/tips/17/thank',
    ])
  })
})

describe('open-source bounty notifications', () => {
  test('uses neutral recipient-scoped list and read endpoints', async () => {
    const gets: string[] = []
    const posts: string[] = []
    api.get = (async (url) => {
      gets.push(url)
      return { data: { success: true, data: [] } }
    }) as typeof api.get
    api.post = (async (url) => {
      posts.push(url)
      return { data: { success: true, data: null } }
    }) as typeof api.post

    await listBountyNotifications()
    await markBountyNotificationsRead()

    assert.deepEqual(gets, ['/api/open-source-bounties/notifications'])
    assert.deepEqual(posts, ['/api/open-source-bounties/notifications/read'])
  })

  test('uses legacy tip endpoints when unified notifications are not advertised', async () => {
    const gets: string[] = []
    const posts: string[] = []
    api.get = (async (url) => {
      gets.push(url)
      return {
        data: {
          success: true,
          data: [
            {
              id: 7,
              project_id: 2,
              challenge_id: 3,
              sender_user_id: 4,
              sender_username: 'legacy-sender',
              project_title: 'Legacy project',
              quota: 100,
              note: '',
              recipient_read_at: 0,
              thanked_at: 0,
              created_at: 1,
            },
          ],
        },
      }
    }) as typeof api.get
    api.post = (async (url) => {
      posts.push(url)
      return { data: { success: true, data: null } }
    }) as typeof api.post

    const notifications = await listCompatibleBountyNotifications(false)
    await markCompatibleBountyNotificationsRead(false)

    assert.deepEqual(gets, ['/api/open-source-bounties/tips/received'])
    assert.deepEqual(posts, ['/api/open-source-bounties/tips/received/read'])
    assert.equal(notifications[0]?.kind, 'tip_transfer')
  })

  test('uses unified endpoints only when the backend advertises them', async () => {
    const gets: string[] = []
    const posts: string[] = []
    api.get = (async (url) => {
      gets.push(url)
      return { data: { success: true, data: [] } }
    }) as typeof api.get
    api.post = (async (url) => {
      posts.push(url)
      return { data: { success: true, data: null } }
    }) as typeof api.post

    await listCompatibleBountyNotifications(true)
    await markCompatibleBountyNotificationsRead(true)

    assert.deepEqual(gets, ['/api/open-source-bounties/notifications'])
    assert.deepEqual(posts, ['/api/open-source-bounties/notifications/read'])
  })
})

describe('open-source bounty challenge cancellation', () => {
  test('posts the selected challenge to the publisher cancellation endpoint', async () => {
    let requestUrl = ''
    api.post = (async (url) => {
      requestUrl = url
      return {
        data: {
          success: true,
          data: { id: 42, status: 'cancelled' },
        },
      }
    }) as typeof api.post

    const challenge = await cancelChallenge(42)

    assert.equal(requestUrl, '/api/open-source-bounties/challenges/42/cancel')
    assert.equal(challenge.status, 'cancelled')
  })
})

describe('open-source bounty disputes', () => {
  test('posts the selected challenge, reason, and statement to the dispute endpoint', async () => {
    const requests: Array<{ url: string; data: unknown }> = []
    api.post = (async (url, data) => {
      requests.push({ url, data })
      return {
        data: {
          success: true,
          data: { id: 9, challenge_id: 42 },
        },
      }
    }) as typeof api.post

    const input = {
      reason: 'requirements_met_but_rejected' as const,
      statement:
        'The submitted evidence satisfies every published requirement.',
    }
    await openBountyDispute(42, input)

    assert.deepEqual(requests, [
      {
        url: '/api/open-source-bounties/challenges/42/disputes',
        data: input,
      },
    ])
  })
})
