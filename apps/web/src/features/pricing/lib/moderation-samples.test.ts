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
import { spawnSync } from 'node:child_process'
import { describe, test } from 'node:test'

import { buildModerationSample } from './moderation-samples'

const apiKey = 'test-moderation-key'
const results = [
  {
    flagged: false,
    categories: { harassment: false },
    category_scores: { harassment: 0.001 },
  },
]
const context = {
  baseUrl: 'https://gateway.example',
  apiKeyEnv: 'NEW_API_KEY',
  modelName: 'omni-moderation-latest',
  endpointPath: '/v1/moderations',
}

function assertNativePayload(value: unknown, modelName: string) {
  assert.deepEqual(value, {
    model: modelName,
    input: 'I forgot my password and need to reset it.',
  })
}

const pythonCapture = `
import contextlib
import io
import json
import sys
import urllib.request

captured = {}
def urlopen(request):
    captured.update({
        "url": request.full_url,
        "method": request.get_method(),
        "authorization": request.get_header("Authorization"),
        "content_type": request.get_header("Content-type"),
        "payload": json.loads(request.data),
    })
    return io.BytesIO(b'{"results":[{"flagged":false,"categories":{"harassment":false},"category_scores":{"harassment":0.001}}]}')

urllib.request.urlopen = urlopen
output = io.StringIO()
with contextlib.redirect_stdout(output):
    exec(sys.argv[1])
print(json.dumps({"request": captured, "output": output.getvalue()}))
`

describe('native moderation code samples', () => {
  for (const ctx of [
    context,
    {
      ...context,
      baseUrl: 'https://tenant.example/relay',
      endpointPath: '/custom/v1/moderations',
      modelName: 'account-moderator',
    },
  ]) {
    const url = `${ctx.baseUrl}${ctx.endpointPath}`
    const label = `${ctx.endpointPath} with ${ctx.modelName}`

    test(`cURL sends native JSON to ${label}`, () => {
      const script = buildModerationSample('curl', ctx)
      const result = spawnSync(
        'bash',
        ['-c', `curl() { printf "%s\\0" "$@"; }\n${script}`],
        {
          env: { ...process.env, NEW_API_KEY: apiKey },
          encoding: 'utf8',
        }
      )
      assert.equal(result.status, 0, result.stderr)
      const args = result.stdout.split('\0').slice(0, -1)
      assert.equal(args[0], url)
      assert.ok(args.includes(`Authorization: Bearer ${apiKey}`))
      assert.ok(args.includes('Content-Type: application/json'))
      // cURL's data option makes this a POST request.
      assert.ok(args.includes('-d'))
      assertNativePayload(
        JSON.parse(args[args.indexOf('-d') + 1]),
        ctx.modelName
      )
    })

    test(`Python sends native JSON to ${label} and reads results`, () => {
      const result = spawnSync(
        'python3',
        ['-c', pythonCapture, buildModerationSample('python', ctx)],
        {
          env: { ...process.env, NEW_API_KEY: apiKey },
          encoding: 'utf8',
        }
      )
      assert.equal(result.status, 0, result.stderr)
      const captured = JSON.parse(result.stdout)
      assert.equal(captured.request.url, url)
      assert.equal(captured.request.method, 'POST')
      assert.equal(captured.request.authorization, `Bearer ${apiKey}`)
      assert.equal(captured.request.content_type, 'application/json')
      assertNativePayload(captured.request.payload, ctx.modelName)
      assert.equal(
        captured.output.trim(),
        "[{'flagged': False, 'categories': {'harassment': False}, 'category_scores': {'harassment': 0.001}}]"
      )
    })

    for (const lang of ['typescript', 'javascript'] as const) {
      test(`${lang} sends native JSON to ${label} and reads results`, async () => {
        let requestCount = 0
        let printed: unknown
        const script = buildModerationSample(lang, ctx)
        const run = new Function(
          'fetch',
          'process',
          'console',
          `return (async () => { ${script} })()`
        )
        await run(
          async (requestUrl: string, init: RequestInit) => {
            requestCount++
            assert.equal(requestUrl, url)
            assert.equal(init.method, 'POST')
            const headers = new Headers(init.headers)
            assert.equal(headers.get('Authorization'), `Bearer ${apiKey}`)
            assert.equal(headers.get('Content-Type'), 'application/json')
            assertNativePayload(JSON.parse(String(init.body)), ctx.modelName)
            return { json: async () => ({ results }) }
          },
          { env: { NEW_API_KEY: apiKey } },
          { log: (value: unknown) => (printed = value) }
        )
        assert.equal(requestCount, 1)
        assert.deepEqual(printed, results)
      })
    }
  }
})
