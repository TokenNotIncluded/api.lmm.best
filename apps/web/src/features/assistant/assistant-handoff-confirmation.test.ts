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
import { test } from 'node:test'

import {
  getAssistantHandoffConfirmationToken,
  minAssistantHandoffCharacters,
} from './assistant-handoff-confirmation'

for (const [name, action] of [
  ['absent action', undefined],
  ['null action', null],
  ['short Chinese message', { confirmation_token: 'bound', message: '放行IP' }],
  ['empty message', { confirmation_token: 'bound', message: '' }],
  ['whitespace message', { confirmation_token: 'bound', message: '     ' }],
  ['empty token', { confirmation_token: '', message: 'Please contact support.' }],
  ['whitespace token', { confirmation_token: '  ', message: 'Please contact support.' }],
  ['four Unicode code points', { confirmation_token: 'bound', message: '\u{1F600}'.repeat(4) }],
] as const) {
  test(`does not bind an unusable prepared action: ${name}`, () => {
    assert.equal(getAssistantHandoffConfirmationToken(action), undefined)
  })
}

test('accepts exactly five Unicode code points and preserves the bound token', () => {
  const action = {
    confirmation_token: 'bound-token',
    message: `  ${'\u{1F600}'.repeat(minAssistantHandoffCharacters)}  `,
  }
  assert.equal(getAssistantHandoffConfirmationToken(action), 'bound-token')
})

test('does not mutate the action or attach its token after editing a short message', () => {
  const action = Object.freeze({
    confirmation_token: 'short-message-token',
    message: '放行IP',
  })
  const token = getAssistantHandoffConfirmationToken(action)
  const editedMessage = 'Please ask an administrator to review my access problem.'
  assert.ok(editedMessage.length >= minAssistantHandoffCharacters)
  assert.equal(token, undefined)
  assert.equal(action.message, '放行IP')
  assert.equal(action.confirmation_token, 'short-message-token')
})
