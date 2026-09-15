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
import { runInNewContext } from 'node:vm'

import { buildCCSwitchProviderURL } from './cc-switch-deep-link'

describe('CC Switch deep links', () => {
  test('imports an account-scoped balance script without embedding credentials', () => {
    const url = new URL(
      buildCCSwitchProviderURL({
        app: 'codex',
        name: 'Fixture',
        apiKey: 'sk-fixture',
        endpoint: 'https://example.com/v1',
        accountBalanceURL: 'https://example.com/v1/balance',
      })
    )
    assert.equal(url.searchParams.get('usageEnabled'), 'true')
    assert.equal(url.searchParams.get('usageAutoInterval'), '5')
    assert.equal(
      url.searchParams.get('usageBaseUrl'),
      'https://example.com/v1/balance'
    )
    const encodedScript = url.searchParams.get('usageScript')
    assert.ok(encodedScript)
    const script = atob(encodedScript)
    assert.ok(!script.includes('sk-fixture'))
    const query = runInNewContext(script)
    assert.equal(query.request.url, '{{baseUrl}}')
    for (const remaining of [23.61, 0, -1]) {
      const result = query.extractor({
        valid: true,
        scope: 'account',
        currency: 'USD',
        remaining,
      })
      assert.equal(result.isValid, true)
      assert.equal(result.remaining, remaining)
      assert.equal(result.unit, 'USD')
    }
    for (const response of [
      { valid: true, scope: 'token', currency: 'USD', remaining: 100 },
      { valid: true, scope: 'account', currency: 'USD', remaining: null },
      { valid: true, scope: 'account', currency: 'USD', remaining: Infinity },
      { valid: false, error: 'account_balance_access_required' },
    ]) {
      assert.equal(query.extractor(response).isValid, false)
      assert.equal(query.extractor(response).remaining, undefined)
    }
  })
  test('builds a URL-encoded Claude provider import link', () => {
    assert.equal(
      buildCCSwitchProviderURL({
        app: 'claude',
        name: 'LMM Claude',
        endpoint: 'https://api.lmm.best',
        apiKey: 'sk-secret',
        models: { model: 'deepseek-v4-flash', haikuModel: '' },
        homepage: 'https://api.lmm.best',
        enabled: true,
      }),
      'ccswitch://v1/import?resource=provider&app=claude&name=LMM+Claude&endpoint=https%3A%2F%2Fapi.lmm.best&apiKey=sk-secret&model=deepseek-v4-flash&homepage=https%3A%2F%2Fapi.lmm.best&enabled=true'
    )
  })
})
