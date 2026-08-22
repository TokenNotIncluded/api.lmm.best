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

import {
  ASSISTANT_RAIL_MIN_WIDTH,
  isAssistantRailViewport,
} from './assistant-responsive'

const launcherSource = readFileSync(
  new URL('./assistant-launcher.tsx', import.meta.url),
  'utf8'
)
const panelSource = readFileSync(
  new URL('./assistant-panel.tsx', import.meta.url),
  'utf8'
)
const consoleEditorialStyles = readFileSync(
  new URL('../../styles/console-editorial.css', import.meta.url),
  'utf8'
)

describe('assistant responsive presentation', () => {
  test('keeps the assistant in an overlay below the xl rail breakpoint', () => {
    assert.equal(ASSISTANT_RAIL_MIN_WIDTH, 1280)
    for (const width of [767, 768, 1039, 1279]) {
      assert.equal(
        isAssistantRailViewport(width),
        false,
        `${width}px should use the assistant overlay`
      )
    }
  })

  test('enables the assistant rail at the xl breakpoint', () => {
    assert.equal(isAssistantRailViewport(1280), true)
  })

  test('keeps the launcher pill below xl and the overlay dialog everywhere', () => {
    // The floating launcher pill is a touch/small-screen affordance; desktop
    // opens the same overlay dialog from the header chat button instead of a
    // persistent side rail.
    assert.match(launcherSource, /xl:hidden/)
    assert.doesNotMatch(panelSource, /mode==='rail'/)
    assert.match(panelSource, /sm:rounded-xl/)
  })

  test('moves the assistant textarea focus outline to its rounded shell', () => {
    assert.match(panelSource, /assistant-prompt-input/)
    assert.match(
      consoleEditorialStyles,
      /\.assistant-prompt-input:has\(\[data-slot='input-group-control'\]:focus-visible\)/
    )
    assert.ok(
      consoleEditorialStyles.includes(
        'outline: 2px solid var(--console-clay, var(--forge-clay-light));'
      )
    )
    assert.ok(
      consoleEditorialStyles.includes(
        'outline-color: var(--console-clay, var(--forge-clay-dark));'
      )
    )
    assert.ok(
      consoleEditorialStyles.includes(
        ".assistant-prompt-input [data-slot='input-group-control']:focus-visible {\n  outline: none;"
      )
    )
  })
})
