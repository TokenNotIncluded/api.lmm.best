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
import { readFileSync } from 'node:fs'
import { describe, test } from 'node:test'

import { api } from '@/lib/api'

import {
  applyUpstreamOverwrite,
  previewUpstreamDiff,
  syncUpstream,
} from './api'

const wizardSource = readFileSync(
  new URL('./components/dialogs/sync-wizard-dialog.tsx', import.meta.url),
  'utf8'
)

describe('upstream model sync', () => {
  test('sends locale and overwrite data without an ignored source option', async () => {
    const originalGet = api.get
    const originalPost = api.post
    const requests: Array<{ method: string; url: string; body?: unknown }> = []

    api.get = (async (url: string) => {
      requests.push({ method: 'GET', url })
      return { data: { success: true, data: { conflicts: [] } } }
    }) as typeof api.get
    api.post = (async (url: string, body?: unknown) => {
      requests.push({ method: 'POST', url, body })
      return { data: { success: true, data: {} } }
    }) as typeof api.post

    try {
      await previewUpstreamDiff({ locale: 'en' })
      await syncUpstream({ locale: 'ja' })
      await applyUpstreamOverwrite({
        locale: 'zh',
        overwrite: [{ model_name: 'gpt-test', fields: ['ratio'] }],
      })
    } finally {
      api.get = originalGet
      api.post = originalPost
    }

    assert.deepEqual(requests, [
      {
        method: 'GET',
        url: '/api/models/sync_upstream/preview?locale=en',
      },
      {
        method: 'POST',
        url: '/api/models/sync_upstream',
        body: { locale: 'ja' },
      },
      {
        method: 'POST',
        url: '/api/models/sync_upstream',
        body: {
          locale: 'zh',
          overwrite: [{ model_name: 'gpt-test', fields: ['ratio'] }],
        },
      },
    ])
  })

  test('keeps upstream conflict handling without offering configuration-file sync', () => {
    assert.equal(wizardSource.includes('Configuration File'), false)
    assert.equal(wizardSource.includes('getSyncSourceOptions'), false)
    assert.equal(wizardSource.includes('source='), false)
    assert.equal(wizardSource.includes('previewUpstreamDiff({ locale })'), true)
    assert.equal(wizardSource.includes('syncUpstream({ locale })'), true)
    assert.equal(wizardSource.includes("setOpen('upstream-conflict')"), true)
    assert.equal(wizardSource.includes('setUpstreamConflicts(conflicts)'), true)
  })
})
