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
import { describe, test } from 'node:test'

import { CHANNEL_TYPE_OPENAI, CHANNEL_TYPE_TYPESAFE } from '../../constants'
import { getModelsMissingFromUpstream } from '../upstream-model-list'

describe('upstream model listing differences', () => {
  test('keeps documented TypeSafe versions when the real listing contains only aliases', () => {
    const fetchedModels = ['jev-latest', 'jev-preview']
    const selectedModels = [
      'jev-1.13.0',
      'jev-latest',
      'jev-preview',
      'jev-99.99.99',
    ]
    assert.deepEqual(
      getModelsMissingFromUpstream({
        channelType: CHANNEL_TYPE_TYPESAFE,
        selectedModels,
        fetchedModels,
      }),
      ['jev-99.99.99']
    )
    assert.deepEqual(fetchedModels, ['jev-latest', 'jev-preview'])
    assert.deepEqual(selectedModels, [
      'jev-1.13.0',
      'jev-latest',
      'jev-preview',
      'jev-99.99.99',
    ])
  })

  test('does not exempt TypeSafe aliases missing from the upstream listing', () => {
    assert.deepEqual(
      getModelsMissingFromUpstream({
        channelType: CHANNEL_TYPE_TYPESAFE,
        selectedModels: ['jev-1.13.0', 'jev-latest', 'jev-preview'],
        fetchedModels: ['jev-latest'],
      }),
      ['jev-preview']
    )
  })

  test('keeps ordinary channel removal detection and mapping aliases intact', () => {
    for (const channelType of [CHANNEL_TYPE_OPENAI, undefined]) {
      assert.deepEqual(
        getModelsMissingFromUpstream({
          channelType,
          selectedModels: [
            ' jev-1.13.0 ',
            'jev-1.13.0',
            'jev-latest',
            'judge-alias',
            ' ',
          ],
          fetchedModels: [' jev-latest '],
          redirectSourceModels: [' judge-alias '],
        }),
        ['jev-1.13.0']
      )
    }
  })
})
