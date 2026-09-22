/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const source = readFileSync(
  new URL('../src/components/ui/sheet.tsx', import.meta.url),
  'utf8'
)

test('Sheet sizing defaults must not outrank consumer width overrides', () => {
  const content = source.slice(
    source.indexOf('function SheetContent('),
    source.indexOf('function SheetHeader(')
  )
  assert.ok(content.length > 0, 'SheetContent must be present')
  // Inspect class strings, not comments. Checking only for the ordinary
  // sm:max-w-sm conditional missed the duplicate data-side sizing regression.
  const literals = [...content.matchAll(/'([^'\n]*)'/g)].map(
    (match) => match[1]
  )
  const conflicting = literals
    .flatMap((literal) => literal.split(/\s+/))
    .filter((token) => /(?:^|:)data-\[side=[^\]]+\]:/.test(token))
  assert.deepEqual(
    conflicting,
    [],
    'Use plain side conditionals: data-side variants survive tailwind-merge and override w-full / sm:max-w-5xl'
  )
  assert.match(content, /data-side=\{side\}/)
  for (const side of ['left', 'right', 'top', 'bottom']) {
    assert.ok(content.includes(`side === '${side}'`))
  }
})
