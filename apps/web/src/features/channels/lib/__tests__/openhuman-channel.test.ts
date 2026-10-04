/*
Copyright (C) 2025 QuantumNous

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
import { describe, test } from 'node:test'

import {
  CHANNEL_TYPE_OPENAI,
  CHANNEL_TYPE_OPENHUMAN,
  CHANNEL_TYPE_TYPESAFE,
  CHANNEL_TYPE_OPTIONS,
  MODEL_FETCHABLE_TYPES,
  getDefaultResponsesWebSocketEnabled,
  isOpenAIChannelType,
  supportsResponsesWebSocket,
} from '../../constants'
import { channelSchema } from '../../types'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  channelFormSchema,
  transformChannelToFormDefaults,
  transformFormDataToCreatePayload,
} from '../channel-form'
import { CHANNEL_TYPE_CONFIGS, getDefaultBaseUrl } from '../channel-type-config'
import { getChannelTypeIcon, getChannelTypeLabel } from '../channel-utils'

describe('retired OpenHuman channel type', () => {
  test('reserves its historical ID without offering transport capabilities', () => {
    assert.equal(CHANNEL_TYPE_OPENHUMAN, 61)
    assert.equal(CHANNEL_TYPE_TYPESAFE, 62)
    assert.equal(
      getChannelTypeLabel(CHANNEL_TYPE_OPENHUMAN),
      'OpenHuman (removed)'
    )
    assert.equal(getChannelTypeIcon(CHANNEL_TYPE_OPENHUMAN), 'Unknown')
    assert.equal(isOpenAIChannelType(CHANNEL_TYPE_OPENHUMAN), false)
    assert.equal(MODEL_FETCHABLE_TYPES.has(CHANNEL_TYPE_OPENHUMAN), false)
    assert.equal(supportsResponsesWebSocket(CHANNEL_TYPE_OPENHUMAN), false)
    assert.equal(
      getDefaultResponsesWebSocketEnabled(CHANNEL_TYPE_OPENHUMAN),
      false
    )
    assert.equal(
      CHANNEL_TYPE_OPTIONS.some(
        (option) => option.value === CHANNEL_TYPE_OPENHUMAN
      ),
      false
    )
    assert.equal(CHANNEL_TYPE_CONFIGS[CHANNEL_TYPE_OPENHUMAN], undefined)
    assert.equal(getDefaultBaseUrl(CHANNEL_TYPE_OPENHUMAN), '')
  })

  test('rejects a retired form while keeping OpenAI-only options working', () => {
    const sharedForm = {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      name: 'Fixture provider',
      group: ['default'],
      key: 'sk-fixture-key',
      models: 'gpt-4o',
      openai_organization: 'org-fixture',
      force_format: true,
      disable_store: true,
      allow_safety_identifier: true,
    }
    assert.equal(
      channelFormSchema.safeParse({ ...sharedForm, type: 61 }).success,
      false
    )
    const openAI = channelFormSchema.parse({
      ...sharedForm,
      type: CHANNEL_TYPE_OPENAI,
    })
    const payload = transformFormDataToCreatePayload(openAI).channel
    assert.equal(payload.openai_organization, 'org-fixture')
    assert.equal(JSON.parse(payload.settings || '{}').disable_store, true)
    assert.equal(
      JSON.parse(payload.settings || '{}').allow_safety_identifier,
      true
    )
  })

  test('reads a historical record without converting its type or group', () => {
    const historical = channelSchema.parse({
      id: 42,
      type: CHANNEL_TYPE_OPENHUMAN,
      name: 'Historical provider',
      status: 2,
      key: '',
      models: 'gpt-4o',
      group: 'human',
      created_time: 0,
      test_time: 0,
      response_time: 0,
      balance_updated_time: 0,
      other: '',
      remark: '',
      channel_info: {},
    })
    const defaults = transformChannelToFormDefaults(historical)
    assert.equal(defaults.type, 61)
    assert.deepEqual(defaults.group, ['human'])
  })
})
