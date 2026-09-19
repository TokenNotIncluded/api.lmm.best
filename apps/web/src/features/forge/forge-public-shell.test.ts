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

const source = readFileSync(
  new URL('./forge-public-shell.tsx', import.meta.url),
  'utf8'
)

describe('Forge public navigation', () => {
  test('keeps the header focused on core discovery routes', () => {
    assert.match(source, /title: 'Home'/)
    assert.match(source, /title: 'Models and pricing'/)
    assert.match(source, /title: 'Guide'/)
    assert.match(source, /href: '\/guide'/)
    assert.match(source, /title: 'Challenges'/)
    assert.match(source, /securityLink/)
  })
})
