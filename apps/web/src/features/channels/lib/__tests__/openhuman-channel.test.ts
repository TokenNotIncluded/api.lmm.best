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
  CHANNEL_TYPE_OPTIONS,
  MODEL_FETCHABLE_TYPES,
  isOpenAIChannelType,
} from '../../constants'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  transformFormDataToCreatePayload,
} from '../channel-form'
import { CHANNEL_TYPE_CONFIGS } from '../channel-type-config'
import { getChannelTypeIcon, getChannelTypeLabel } from '../channel-utils'

describe('OpenHuman channel type', () => {
  test('is registered as an OpenAI-equivalent channel with a distinct name', () => {
    assert.equal(CHANNEL_TYPE_OPENHUMAN, 61)
    assert.equal(getChannelTypeLabel(CHANNEL_TYPE_OPENHUMAN), 'OpenHuman')
    assert.equal(getChannelTypeIcon(CHANNEL_TYPE_OPENHUMAN), 'OpenAI')
    assert.equal(isOpenAIChannelType(CHANNEL_TYPE_OPENHUMAN), true)
    assert.equal(MODEL_FETCHABLE_TYPES.has(CHANNEL_TYPE_OPENHUMAN), true)
    assert.equal(
      CHANNEL_TYPE_OPTIONS.some(
        (option) =>
          option.value === CHANNEL_TYPE_OPENHUMAN &&
          option.label === 'OpenHuman'
      ),
      true
    )

    const {
      id: _openAIId,
      name: _openAIName,
      ...openAIConfig
    } = CHANNEL_TYPE_CONFIGS[CHANNEL_TYPE_OPENAI]
    const {
      id: _openHumanId,
      name: _openHumanName,
      ...openHumanConfig
    } = CHANNEL_TYPE_CONFIGS[CHANNEL_TYPE_OPENHUMAN]
    assert.deepEqual(openHumanConfig, openAIConfig)
  })

  test('serializes OpenAI-only settings without changing their content', () => {
    const sharedForm = {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      name: 'OpenHuman',
      base_url: 'https://api.openai.com',
      key: 'sk-openhuman-example-key',
      openai_organization: 'org-openhuman',
      models: 'gpt-4,gpt-4o',
      force_format: true,
      allow_service_tier: true,
      disable_store: true,
      allow_safety_identifier: true,
      allow_include_obfuscation: true,
      allow_inference_geo: true,
    }

    const openAI = transformFormDataToCreatePayload({
      ...sharedForm,
      type: CHANNEL_TYPE_OPENAI,
    }).channel
    const openHuman = transformFormDataToCreatePayload({
      ...sharedForm,
      type: CHANNEL_TYPE_OPENHUMAN,
    }).channel

    assert.equal(openHuman.openai_organization, 'org-openhuman')
    assert.equal(openHuman.setting, openAI.setting)
    assert.equal(openHuman.settings, openAI.settings)
  })
})
