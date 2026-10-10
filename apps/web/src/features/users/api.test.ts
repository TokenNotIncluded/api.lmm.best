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
import { describe, test } from 'node:test'

import { api } from '@/lib/api'

import {
  getAssistantUserProfile,
  getUsers,
  listAssistantRequestReviews,
  searchUsers,
  updateAssistantUserProfile,
} from './api'

describe('user management API filters', () => {
  test('sends activity filters and risk sorting to both list endpoints including a zero lower bound', async () => {
    const originalGet = api.get
    const calls: {
      url: string
      config?: { params?: Record<string, unknown> }
    }[] = []
    api.get = (async (
      url: string,
      config?: { params?: Record<string, unknown> }
    ) => {
      calls.push({ url, config })
      return { data: { success: true, data: { items: [], total: 0 } } }
    }) as typeof api.get
    const filters = {
      risk_min: 0,
      risk_max: 0.799,
      transfers: 'sent',
      usage: 'zero',
      funding: 'unpaid',
      checkin: 'yes',
      sort_by: 'risk_score',
      sort_order: 'desc',
    } as const
    try {
      await getUsers(filters)
      await searchUsers({ ...filters, keyword: 'alice' })
      for (const [key, value] of Object.entries(filters)) {
        assert.equal(calls[0].config?.params?.[key], value)
        assert.equal(
          new URLSearchParams(calls[1].url.split('?')[1]).get(key),
          String(value)
        )
      }
    } finally {
      api.get = originalGet
    }
  })

  test('passes the L0 filter to the paginated user endpoint', async () => {
    const originalGet = api.get
    let requestConfig: unknown
    api.get = (async (url: string, config?: unknown) => {
      assert.equal(url, '/api/user/')
      requestConfig = config
      return { data: { success: true, data: { items: [], total: 0 } } }
    }) as typeof api.get

    try {
      await getUsers({ p: 2, page_size: 20, trust_level: 0 })
      assert.deepEqual(requestConfig, {
        params: {
          p: 2,
          page_size: 20,
          trust_level: 0,
        },
      })
    } finally {
      api.get = originalGet
    }
  })

  test('passes the L0 filter to user search', async () => {
    const originalGet = api.get
    let requestUrl = ''
    api.get = (async (url: string) => {
      requestUrl = url
      return { data: { success: true, data: { items: [], total: 0 } } }
    }) as typeof api.get

    try {
      await searchUsers({ keyword: 'alice', trust_level: 0 })
      const [path, query = ''] = requestUrl.split('?')
      assert.equal(path, '/api/user/search')
      const params = new URLSearchParams(query)
      assert.equal(params.get('keyword'), 'alice')
      assert.equal(params.get('trust_level'), '0')
    } finally {
      api.get = originalGet
    }
  })
})

describe('administrator assistant profile API', () => {
  test('uses the admin-only per-user profile endpoints', async () => {
    const originalGet = api.get
    const originalPut = api.put
    const requests: Array<{ method: string; url: string; data?: unknown }> = []
    api.get = (async (url: string) => {
      requests.push({ method: 'GET', url })
      return {
        data: {
          success: true,
          data: {
            profile_key: 'guided_buyer',
            tags: ['new-user'],
            strategy: 'Ask one question at a time.',
            enabled: true,
            updated_at: 1,
          },
        },
      }
    }) as typeof api.get
    api.put = (async (url: string, data: unknown) => {
      requests.push({ method: 'PUT', url, data })
      return {
        data: {
          success: true,
          data: {
            profile_key: 'guided_buyer',
            tags: ['new-user'],
            strategy: 'Ask one question at a time.',
            enabled: true,
            updated_at: 2,
          },
        },
      }
    }) as typeof api.put

    try {
      await getAssistantUserProfile(41)
      await updateAssistantUserProfile(41, {
        profile_key: 'guided_buyer',
        tags: ['new-user'],
        strategy: 'Ask one question at a time.',
        enabled: true,
      })
      assert.deepEqual(requests, [
        { method: 'GET', url: '/api/user/41/assistant-profile' },
        {
          method: 'PUT',
          url: '/api/user/41/assistant-profile',
          data: {
            profile_key: 'guided_buyer',
            tags: ['new-user'],
            strategy: 'Ask one question at a time.',
            enabled: true,
          },
        },
      ])
    } finally {
      api.get = originalGet
      api.put = originalPut
    }
  })
})

describe('administrator assistant review API', () => {
  test('passes the requested review page and uses a 20-item default page', async () => {
    const originalGet = api.get
    let requestConfig: unknown
    api.get = (async (url: string, config?: unknown) => {
      assert.equal(url, '/api/assistant/admin/request-reviews')
      requestConfig = config
      return {
        data: {
          success: true,
          data: {
            items: [],
            total: 0,
            page: 2,
            page_size: 20,
            violation_count: 0,
            reset_at: 0,
          },
        },
      }
    }) as typeof api.get

    try {
      await listAssistantRequestReviews(41, 2)
      assert.deepEqual(requestConfig, {
        params: { user_id: 41, page: 2, page_size: 20 },
      })
    } finally {
      api.get = originalGet
    }
  })
})
