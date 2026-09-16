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
  createAssistantHandoffConfirmation,
  getAssistantHandoffConfirmationToken,
  isSameAssistantHandoffConfirmation,
  isValidAssistantHandoffMessage,
  maxAssistantHandoffCharacters,
  minAssistantHandoffCharacters,
} from './assistant-handoff-confirmation'

for (const [name, action] of [
  ['absent action', undefined],
  ['null action', null],
  ['short Chinese message', { confirmation_token: 'bound', message: '放行IP' }],
  ['empty message', { confirmation_token: 'bound', message: '' }],
  ['whitespace message', { confirmation_token: 'bound', message: '     ' }],
  [
    'empty token',
    { confirmation_token: '', message: 'Please contact support.' },
  ],
  [
    'whitespace token',
    { confirmation_token: '  ', message: 'Please contact support.' },
  ],
  [
    'four Unicode code points',
    { confirmation_token: 'bound', message: '\u{1F600}'.repeat(4) },
  ],
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
  const editedMessage =
    'Please ask an administrator to review my access problem.'
  assert.ok(editedMessage.length >= minAssistantHandoffCharacters)
  assert.equal(token, undefined)
  assert.equal(action.message, '放行IP')
  assert.equal(action.confirmation_token, 'short-message-token')
})

for (const character of ['a', '中', '\u{1F600}']) {
  for (const length of [4, 5, 1999, 2000, 2001]) {
    test(`message bounds count Unicode code points: ${character} x ${length}`, () => {
      const message = `  ${character.repeat(length)}  `
      const valid = length >= 5 && length <= 2000
      assert.equal(isValidAssistantHandoffMessage(message), valid)
      assert.equal(Boolean(createAssistantHandoffConfirmation(message)), valid)
      const action = { message, confirmation_token: 'signed' }
      assert.equal(
        getAssistantHandoffConfirmationToken(action),
        valid ? 'signed' : undefined
      )
    })
  }
}

test('an oversized prepared action becomes editable instead of keeping its token', () => {
  const action = {
    confirmation_token: 'oversized',
    message: '中'.repeat(maxAssistantHandoffCharacters + 1),
  }
  assert.equal(getAssistantHandoffConfirmationToken(action), undefined)
  assert.equal(createAssistantHandoffConfirmation(action.message, action), null)
})

test('review is an immutable copy, not a live reference to the AI action', () => {
  const action = {
    message: 'Original support request',
    confirmation_token: 'v1',
  }
  const confirmation = createAssistantHandoffConfirmation(
    action.message,
    action
  )
  assert.ok(confirmation)
  assert.equal(Object.isFrozen(confirmation), true)
  action.message = 'An entirely different request'
  action.confirmation_token = 'v2'
  assert.deepEqual(confirmation, {
    message: 'Original support request',
    confirmationToken: 'v1',
  })
  assert.equal(
    isSameAssistantHandoffConfirmation(
      confirmation,
      createAssistantHandoffConfirmation(action.message, action)
    ),
    false
  )
})

test('a changed token requires a fresh review even when the message is unchanged', () => {
  const message = 'Please investigate the login problem.'
  const before = createAssistantHandoffConfirmation(message, {
    message,
    confirmation_token: 'before',
  })
  const after = createAssistantHandoffConfirmation(message, {
    message,
    confirmation_token: 'after',
  })
  assert.equal(isSameAssistantHandoffConfirmation(before, after), false)
})

test('refuses a visible message that disagrees with the server-bound action', () => {
  assert.equal(
    createAssistantHandoffConfirmation('User edited this message', {
      message: 'The original signed message',
      confirmation_token: 'original-token',
    }),
    null
  )
})

test('manual recovery is reviewed without replaying a signed action', () => {
  const confirmation = createAssistantHandoffConfirmation(
    '  Edited support request  ',
    null
  )
  assert.deepEqual(confirmation, {
    message: 'Edited support request',
    confirmationToken: undefined,
  })
})

test('blank and absent reviews are never considered confirmation', () => {
  assert.equal(createAssistantHandoffConfirmation('   '), null)
  assert.equal(isSameAssistantHandoffConfirmation(null, null), false)
  assert.equal(
    isSameAssistantHandoffConfirmation(
      createAssistantHandoffConfirmation('Valid message'),
      null
    ),
    false
  )
})

test('equivalent reviewed values remain current without depending on object identity', () => {
  const message = 'A request that is still unchanged.'
  assert.equal(
    isSameAssistantHandoffConfirmation(
      createAssistantHandoffConfirmation(message),
      createAssistantHandoffConfirmation(`  ${message}  `)
    ),
    true
  )
})
