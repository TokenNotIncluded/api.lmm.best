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

import { buildSystemoneSample } from './systemone-samples'

const baseUrl = 'https://gateway.example'
const apiKey = 'test-jev-key'
const answer = { account_support: { type: 'noul', noul: 0.99 } }
const context = {
  baseUrl,
  apiKeyEnv: 'NEW_API_KEY',
  modelName: 'jev-latest',
  endpointPath: '/typesafe/v1/systemone',
}

function assertNativePayload(value: unknown) {
  assert.deepEqual(value, {
    model: 'jev-latest',
    state: 'I forgot my password and need to reset it.',
    questions: {
      account_support: {
        type: 'noul',
        instructions: 'Does this request concern account support?',
      },
    },
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
    return io.BytesIO(b'{"answers":{"account_support":{"type":"noul","noul":0.99}}}')

urllib.request.urlopen = urlopen
output = io.StringIO()
with contextlib.redirect_stdout(output):
    exec(sys.argv[1])
print(json.dumps({"request": captured, "output": output.getvalue()}))
`

describe('native TypeSafe code samples', () => {
  for (const endpointPath of ['/typesafe/v1/systemone', '/v1/systemone']) {
    const ctx = { ...context, endpointPath }
    const url = `${baseUrl}${endpointPath}`

    test(`cURL sends native JSON to ${endpointPath}`, () => {
      const script = buildSystemoneSample('curl', ctx)
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
      assertNativePayload(JSON.parse(args[args.indexOf('-d') + 1]))
    })

    test(`Python sends native JSON to ${endpointPath} and reads answers`, () => {
      const result = spawnSync(
        'python3',
        ['-c', pythonCapture, buildSystemoneSample('python', ctx)],
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
      assertNativePayload(captured.request.payload)
      assert.equal(
        captured.output.trim(),
        "{'account_support': {'type': 'noul', 'noul': 0.99}}"
      )
    })

    for (const lang of ['typescript', 'javascript'] as const) {
      test(`${lang} sends native JSON to ${endpointPath} and reads answers`, async () => {
        let requestCount = 0
        let printed: unknown
        const script = buildSystemoneSample(lang, ctx)
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
            assertNativePayload(JSON.parse(String(init.body)))
            return { json: async () => ({ answers: answer }) }
          },
          { env: { NEW_API_KEY: apiKey } },
          { log: (value: unknown) => (printed = value) }
        )
        assert.equal(requestCount, 1)
        assert.deepEqual(printed, answer)
      })
    }
  }
})
