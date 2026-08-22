/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { describe, test } from 'node:test'

const source = readFileSync(new URL('./index.tsx', import.meta.url), 'utf8')

describe('chat management responsive history', () => {
  test('shows one pane at a time on narrow screens and provides a back affordance', () => {
    assert.match(source, /selected && 'hidden lg:block'/)
    assert.match(source, /!selected && 'hidden lg:block'/)
    assert.match(source, /data-testid='chat-management-back'/)
  })

  test('uses the server-bounded history page instead of requesting 100 rows', () => {
    assert.doesNotMatch(source, /limit=\{100\}/)
    assert.match(source, /<AssistantHistory\s+active\s+presentation='rows'/)
  })
})

describe('chat management page conventions', () => {
  test('renders inside the standard section page shell', () => {
    assert.match(source, /SectionPageLayout fixedContent/)
    assert.match(
      source,
      /SectionPageLayout\.Title>\s*\{t\('Conversation records'\)\}/
    )
  })

  test('keeps the list and transcript panes independently scrollable', () => {
    assert.match(
      source,
      /section[\s\S]*?min-h-0 min-w-0 overflow-y-auto lg:pr-6/
    )
    assert.match(
      source,
      /aside[\s\S]*?min-h-0 min-w-0 overflow-y-auto[\s\S]*?lg:pl-6/
    )
  })

  test('uses the shared empty state for the unselected transcript pane', () => {
    assert.match(source, /EmptyState/)
    assert.match(source, /title=\{t\('Open a conversation'\)\}/)
  })
})
