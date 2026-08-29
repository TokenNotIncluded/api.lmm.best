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

const apiSource = readFileSync(
  new URL('./model-details-api.tsx', import.meta.url),
  'utf8'
)
const statsSource = readFileSync(
  new URL('../lib/mock-stats.ts', import.meta.url),
  'utf8'
)

describe('public model API details', () => {
  test('does not present inferred parameters or seeded limits as authoritative', () => {
    for (const misleadingSymbol of [
      'SupportedParametersSection',
      'RateLimitsSection',
      'buildSupportedParameters',
      'buildRateLimits',
      'formatRateLimit',
    ]) {
      assert.equal(apiSource.includes(misleadingSymbol), false)
      assert.equal(statsSource.includes(misleadingSymbol), false)
    }
    assert.equal(apiSource.includes("t('Supported parameters')"), false)
    assert.equal(apiSource.includes("t('Rate limits')"), false)
  })

  test('keeps real endpoint-map-driven API examples and authentication help', () => {
    for (const sampleBuilder of [
      'buildChatSample',
      'buildAnthropicSample',
      'buildGeminiSample',
      'buildEmbeddingSample',
      'buildImageSample',
    ]) {
      assert.equal(apiSource.includes(`function ${sampleBuilder}`), true)
    }
    assert.equal(apiSource.includes('props.endpointMap[type]'), true)
    assert.equal(apiSource.includes('replaceModelInPath'), true)
    assert.equal(apiSource.includes('<CodeSamplesSection'), true)
    assert.equal(apiSource.includes('<AuthSection />'), true)
  })
})
