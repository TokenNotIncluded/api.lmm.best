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
import { test } from 'node:test'

import { AxiosHeaders, type InternalAxiosRequestConfig } from 'axios'
import { Window } from 'happy-dom'

const domWindow = new Window({ url: 'http://127.0.0.1:4174/' })
Object.defineProperty(globalThis, 'window', {
  configurable: true,
  value: domWindow,
})
const { consolePageFixture, withConsolePageFixtures } =
  await import('./console-page-fixtures')
const config = (url: string, method = 'get') =>
  ({ url, method, headers: new AxiosHeaders() }) as InternalAxiosRequestConfig

test('review fixtures never handle writes, unknown paths, or remote requests', async () => {
  assert.equal(consolePageFixture(config('/api/token/', 'post')), undefined)
  assert.equal(
    consolePageFixture(config('https://example.invalid/api/token/')),
    undefined
  )
  assert.equal(
    consolePageFixture(config('/api/not-in-the-explicit-catalog')),
    undefined
  )
  let passed = 0
  const wrapped = withConsolePageFixtures(async (request) => {
    passed += 1
    throw new Error(`blocked:${request.url}`)
  })
  await assert.rejects(
    wrapped(config('/api/hero-sms/sms/orders', 'post')),
    /blocked/
  )
  assert.equal(passed, 1)
  const response = await wrapped(config('/api/token/'))
  assert.equal(response.status, 200)
  assert.equal(passed, 1)
})

test('empty read responses are cloned so one page cannot mutate another fixture', () => {
  const first = consolePageFixture(config('/api/user/oauth/bindings')) as {
    data: unknown[]
  }
  first.data.push('local mutation')
  const second = consolePageFixture(config('/api/user/oauth/bindings')) as {
    data: unknown[]
  }
  assert.deepEqual(second.data, [])
})

test('admin list fixtures keep the API array contract rather than a paginated envelope', () => {
  for (const url of [
    '/api/red-packet/admin',
    '/api/security/admin/review-runs',
    '/api/assistant/admin/registration-events',
  ]) {
    const response = consolePageFixture(config(url)) as { data: unknown }
    assert.ok(Array.isArray(response.data), url)
  }
})

test('assistant model reads are explicit, cloned fixtures and never authorize writes', () => {
  const first = consolePageFixture(
    config('/api/assistant/models?group=default')
  ) as { data: string[] }
  assert.ok(Array.isArray(first.data))
  assert.ok(first.data.length > 0)
  assert.ok(first.data.every((model) => typeof model === 'string'))
  first.data.push('mutated-preview')
  const second = consolePageFixture(
    config('/api/assistant/models?group=default')
  ) as { data: string[] }
  assert.ok(!second.data.includes('mutated-preview'))
  assert.equal(
    consolePageFixture(config('/api/assistant/models', 'post')),
    undefined
  )
})
