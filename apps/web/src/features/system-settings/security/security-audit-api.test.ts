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
/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { afterEach, describe, test } from 'node:test'

import type { AxiosAdapter, AxiosResponse } from 'axios'

import { api } from '@/lib/api'

import {
  getModerationModels,
  listModerationReviews,
  listModerationAppeals,
} from './security-audit-api'

const originalAdapter = api.defaults.adapter

function response(
  config: Parameters<AxiosAdapter>[0],
  data: unknown
): AxiosResponse {
  return {
    config,
    data,
    headers: {},
    status: 200,
    statusText: 'OK',
  }
}

afterEach(() => {
  api.defaults.adapter = originalAdapter
})

test('loads the bounded appeal records with GET without pretending to request all-time totals', async () => {
  let captured: Parameters<AxiosAdapter>[0] | undefined
  api.defaults.adapter = async (config) => {
    captured = config
    return response(config, {
      success: true,
      data: [{ id: 1, record_id: 2, status: 'pending' }],
    })
  }
  const result = await listModerationAppeals()
  assert.equal(captured?.method, 'get')
  assert.equal(captured?.url, '/api/security/admin/violation-fee-appeals')
  assert.equal(captured?.params, undefined)
  assert.equal(result.data?.[0].record_id, 2)
})

describe('Moderation metadata API', () => {
  test('keeps target user group filters separate from model routing group', async () => {
    const captured: Parameters<AxiosAdapter>[0][] = []
    api.defaults.adapter = async (config) => {
      captured.push(config)
      return response(config, {
        success: true,
        data: config.url?.endsWith('/models')
          ? { group: 'review-route', models: ['omni-moderation-latest'] }
          : { rows: [], total: 0, page: 2, page_size: 20 },
      })
    }
    assert.deepEqual(await getModerationModels('review-route'), [
      'omni-moderation-latest',
    ])
    const reviews = await listModerationReviews({
      page: 2,
      page_size: 20,
      group: 'target-users',
      status: 'completed',
      source: 'assistant_input',
    })
    assert.deepEqual(reviews.data?.rows, [])
    assert.deepEqual(captured[0].params, { group: 'review-route' })
    assert.deepEqual(captured[1].params, {
      p: 2,
      page_size: 20,
      group: 'target-users',
      status: 'completed',
      source: 'assistant_input',
    })
    assert.equal(captured[1].url, '/api/security/admin/moderation-reviews')
  })
})
