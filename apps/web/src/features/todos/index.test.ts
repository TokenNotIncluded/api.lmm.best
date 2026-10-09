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

const todosSource = readFileSync(
  new URL('./index.tsx', import.meta.url),
  'utf8'
)
const usersSource = readFileSync(
  new URL('../users/index.tsx', import.meta.url),
  'utf8'
)

describe('admin to-do page layout', () => {
  test('owns the pending-work panels instead of the users page', () => {
    for (const panel of ['AssistantLeadsPanel', 'AccountActionRequestsPanel']) {
      assert.match(todosSource, new RegExp(`<${panel}[^>]*\\/>`))
      assert.doesNotMatch(usersSource, new RegExp(`<${panel} \\/>`))
    }
  })

  test('keeps secondary admin panels collapsed and lazy by default', () => {
    assert.match(
      todosSource,
      /<AdminTodoSection title=\{t\('Assistant support tasks'\)\}>/
    )
    assert.match(todosSource, /title=\{t\('Account safety review'\)\}/)
    assert.match(
      todosSource,
      /initiallyExpanded=\{focusAccountActionId !== undefined\}/
    )
    assert.match(
      todosSource,
      /const \[mounted, setMounted\] = useState\(props\.initiallyExpanded \?\? false\)/
    )
    assert.match(
      todosSource,
      /\{mounted \? <div className='pt-5'>\{props.children\}<\/div> : null\}/
    )
  })

  test('never mounts or imports the retired L1 application review', () => {
    for (const source of [todosSource, usersSource]) {
      assert.doesNotMatch(
        source,
        /DeveloperAccessRequestsPanel|L1 access requests|focusDeveloperAccessId/
      )
    }
  })

  test('uses the scrolling section layout', () => {
    assert.match(todosSource, /<SectionPageLayout>/)
    assert.doesNotMatch(todosSource, /<SectionPageLayout fixedContent>/)
  })
})
