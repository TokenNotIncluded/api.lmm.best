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
    const wrapper = source.match(
      /<SidebarProvider\b[^>]*\bclassName='([^']+)'/
    )
    assert.ok(wrapper, 'SidebarProvider must declare its layout classes')
    const classes = new Set(wrapper[1].split(/\s+/))
    for (const name of [
      'console-editorial',
      'workspace-ui',
      'h-dvh',
      'min-h-0',
      'flex-col',
      'overflow-hidden',
    ]) {
      assert.ok(classes.has(name), `Missing wrapper class: ${name}`)
    }
    assert.match(
      source,
      /flex min-h-0 w-full min-w-0 flex-1 basis-0 flex-col flex-nowrap md:flex-row/
    )
    assert.match(source, /'min-h-0 min-w-0 flex-1 basis-0 overflow-hidden'/)
    assert.match(
      source,
      /assistantPage[\s\S]*\? 'pb-0'[\s\S]*: 'pb-\[calc\(4\.5rem\+env\(safe-area-inset-bottom\)\)\] md:pb-16 xl:pb-0'/
    )
    assert.match(documentSource, /interactive-widget=resizes-content/)
  })
})
