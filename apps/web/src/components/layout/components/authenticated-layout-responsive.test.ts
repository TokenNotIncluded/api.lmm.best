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
  new URL('./authenticated-layout.tsx', import.meta.url),
  'utf8'
)
const documentSource = readFileSync(
  new URL('../../../../index.html', import.meta.url),
  'utf8'
)

describe('authenticated layout responsive contract', () => {
  test('keeps the narrow viewport content row and inset stretchable', () => {
    assert.match(
      source,
      /console-editorial h-dvh min-h-0 flex-col overflow-hidden/
    )
    assert.match(
      source,
      /flex min-h-0 w-full min-w-0 flex-1 basis-0 flex-col flex-nowrap md:flex-row/
    )
    assert.match(source, /'min-h-0 min-w-0 flex-1 basis-0 overflow-hidden'/)
    assert.match(source, /'pb-\[env\(safe-area-inset-bottom\)\] xl:pb-0'/)
    assert.match(source, /showMobileAssistant\s/)
    assert.doesNotMatch(source, /assistantPage/)
    assert.match(source, /<AssistantLauncher hideMobileLauncher \/>/)
    assert.match(documentSource, /interactive-widget=resizes-content/)
  })
})
