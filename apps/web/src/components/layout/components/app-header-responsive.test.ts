/*
Copyright (C) 2026 LIghtJUNction

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
*/
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { describe, test } from 'node:test'

const appHeaderSource = readFileSync(
  new URL('./app-header.tsx', import.meta.url),
  'utf8'
)
const topNavSource = readFileSync(
  new URL('./top-nav.tsx', import.meta.url),
  'utf8'
)
const headerSource = readFileSync(
  new URL('./header.tsx', import.meta.url),
  'utf8'
)
const languageSwitcherSource = readFileSync(
  new URL('../../language-switcher.tsx', import.meta.url),
  'utf8'
)

describe('authenticated header responsive navigation', () => {
  test('keeps the dynamic navigation available below the desktop breakpoint', () => {
    assert.match(
      appHeaderSource,
      /hidden lg:block[\s\S]*lg:hidden[\s\S]*<TopNav links=\{links\}/
    )
  })

  test('labels the compact navigation tray for touch and assistive users', () => {
    assert.match(topNavSource, /aria-label=\{t\('More'\)\}/)
    assert.match(topNavSource, /title=\{t\('More'\)\}/)
  })

  test('keeps the mobile navigation and language controls reachable', () => {
    assert.match(headerSource, /className='size-11 sm:size-8'/)
    assert.match(topNavSource, /className='size-11 lg:size-8'/)
    assert.match(languageSwitcherSource, /className='h-11 w-11[\s\S]*sm:h-8/)
    assert.match(languageSwitcherSource, /className='min-h-11 sm:min-h-8'/)
  })
})
