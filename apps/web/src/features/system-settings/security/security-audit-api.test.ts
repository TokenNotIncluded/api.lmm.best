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
  ADMIN_ASSISTANT_REVIEW_CLEANUP_PREVIEW_ENDPOINT,
  ADMIN_ASSISTANT_REVIEW_RUNS_ENDPOINT,
  deleteAssistantReviewRuns,
  previewAssistantReviewRunCleanup,
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

describe('assistant review cleanup API', () => {
  test('requests a cleanup preview without a security proof', async () => {
    let captured: Parameters<AxiosAdapter>[0] | undefined
    api.defaults.adapter = async (config) => {
      captured = config
      return response(config, {
        success: true,
        data: {
          task_type: 'assistant_review',
          keep: 30,
          eligible_count: 5,
          deleted_count: 0,
        },
      })
    }

    const result = await previewAssistantReviewRunCleanup(30)

    assert.equal(captured?.method, 'get')
    assert.equal(captured?.url, ADMIN_ASSISTANT_REVIEW_CLEANUP_PREVIEW_ENDPOINT)
    assert.deepEqual(captured?.params, { keep: 30 })
    assert.equal(result.data?.eligible_count, 5)
  })

  test('sends the scoped proof when deleting cleanup candidates', async () => {
    let captured: Parameters<AxiosAdapter>[0] | undefined
    api.defaults.adapter = async (config) => {
      captured = config
      return response(config, {
        success: true,
        data: {
          task_type: 'assistant_review',
          keep: 30,
          eligible_count: 5,
          deleted_count: 5,
        },
      })
    }

    const result = await deleteAssistantReviewRuns(30, 5, 'proof-token')

    assert.equal(captured?.method, 'delete')
    assert.equal(captured?.url, ADMIN_ASSISTANT_REVIEW_RUNS_ENDPOINT)
    assert.deepEqual(captured?.params, { keep: 30, expected_count: 5 })
    assert.equal(captured?.headers?.['X-Security-Proof'], 'proof-token')
    assert.equal(result.data?.deleted_count, 5)
  })
})
