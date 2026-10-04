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

import { createInstance } from 'i18next'

import { ENDPOINT_TEMPLATES } from '@/features/models/constants'
import {
  ENDPOINT_TYPES,
  getEndpointTypeLabel,
  getEndpointTypeLabels,
} from '@/features/pricing/constants'
import { filterByEndpointType } from '@/features/pricing/lib/filters'
import type { PricingModel } from '@/features/pricing/types'

import {
  CHANNEL_TYPE_NEW_API,
  CHANNEL_TYPE_OPENAI,
  CHANNEL_TYPE_OPENHUMAN,
  CHANNEL_TYPE_TYPESAFE,
  CHANNEL_TYPE_OPTIONS,
  MODEL_FETCHABLE_TYPES,
  TYPESAFE_MODELS,
  isOpenAIChannelType,
  supportsResponsesWebSocket,
} from '../../constants'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  channelFormSchema,
  transformFormDataToCreatePayload,
} from '../channel-form'
import {
  getChannelTestEndpointOptions,
  getDefaultChannelTestEndpoint,
  supportsChannelStreamTest,
} from '../channel-test-config'
import { getChannelTypeConfig } from '../channel-type-config'

describe('native TypeSafe judgment channels', () => {
  test('keeps TypeSafe separate from OpenHuman and OpenAI transports', () => {
    assert.notEqual(CHANNEL_TYPE_TYPESAFE, CHANNEL_TYPE_OPENHUMAN)
    assert.equal(
      CHANNEL_TYPE_OPTIONS.find(
        (option) => option.value === CHANNEL_TYPE_TYPESAFE
      )?.label,
      'TypeSafe (Jev)'
    )
    assert.equal(isOpenAIChannelType(CHANNEL_TYPE_TYPESAFE), false)
    assert.equal(supportsResponsesWebSocket(CHANNEL_TYPE_TYPESAFE), false)
    assert.equal(MODEL_FETCHABLE_TYPES.has(CHANNEL_TYPE_TYPESAFE), false)

    const config = getChannelTypeConfig(CHANNEL_TYPE_TYPESAFE)
    assert.equal(config.defaultBaseUrl, 'https://api.typesafe.ai')
    assert.deepEqual(config.supportedModels, TYPESAFE_MODELS)
  })

  test('saves a native channel while discarding stale OpenAI settings', () => {
    const form = channelFormSchema.parse({
      ...CHANNEL_FORM_DEFAULT_VALUES,
      name: 'TypeSafe',
      type: CHANNEL_TYPE_TYPESAFE,
      key: 'typesafe-test-key',
      models: TYPESAFE_MODELS.join(','),
      responses_websocket_enabled: true,
      allow_service_tier: true,
      disable_store: true,
      settings: JSON.stringify({
        allow_service_tier: true,
        disable_store: true,
      }),
    })
    const channel = transformFormDataToCreatePayload(form).channel
    assert.equal(channel.base_url, null)
    assert.equal(channel.type, CHANNEL_TYPE_TYPESAFE)
    assert.equal(
      JSON.parse(channel.setting || '{}').responses_websocket_enabled,
      undefined
    )
    assert.equal(
      JSON.parse(channel.settings || '{}').allow_service_tier,
      undefined
    )
    assert.equal(JSON.parse(channel.settings || '{}').disable_store, undefined)
  })

  test('uses synchronous judgment testing for native channels and relay chains', () => {
    assert.equal(
      getDefaultChannelTestEndpoint(CHANNEL_TYPE_TYPESAFE),
      'systemone'
    )
    assert.deepEqual(
      getChannelTestEndpointOptions(CHANNEL_TYPE_TYPESAFE).map(
        (option) => option.value
      ),
      ['auto', 'systemone']
    )
    for (const endpoint of ['auto', 'systemone']) {
      assert.equal(
        supportsChannelStreamTest(CHANNEL_TYPE_TYPESAFE, endpoint),
        false
      )
    }
    assert.ok(
      getChannelTestEndpointOptions(CHANNEL_TYPE_NEW_API).some(
        (option) => option.value === 'systemone'
      )
    )
    assert.equal(
      supportsChannelStreamTest(CHANNEL_TYPE_NEW_API, 'systemone'),
      false
    )
    assert.equal(
      supportsChannelStreamTest(CHANNEL_TYPE_NEW_API, 'openai'),
      true
    )
  })

  test('offers synchronous moderation tests for OpenAI relays without exposing them to TypeSafe', () => {
    assert.equal(getDefaultChannelTestEndpoint(CHANNEL_TYPE_OPENAI), 'auto')
    assert.equal(
      getChannelTestEndpointOptions(CHANNEL_TYPE_OPENAI).find(
        (option) => option.value === 'moderation'
      )?.label,
      'Moderation (/v1/moderations)'
    )
    assert.ok(
      getChannelTestEndpointOptions(CHANNEL_TYPE_NEW_API).some(
        (option) => option.value === 'moderation'
      )
    )
    for (const type of [
      CHANNEL_TYPE_OPENAI,
      CHANNEL_TYPE_NEW_API,
      CHANNEL_TYPE_TYPESAFE,
    ]) {
      assert.equal(supportsChannelStreamTest(type, 'moderation'), false)
    }
    assert.equal(supportsChannelStreamTest(CHANNEL_TYPE_OPENAI, 'openai'), true)
    assert.equal(
      getChannelTestEndpointOptions(CHANNEL_TYPE_TYPESAFE).some(
        (option) => option.value === 'moderation'
      ),
      false
    )
  })

  test('keeps moderation catalog labels, filters and endpoint templates separate from judgment', async () => {
    const i18n = createInstance()
    await i18n.init({
      lng: 'en',
      resources: {
        en: {
          translation: {
            Moderation: 'Content safety',
            'TypeSafe (Jev)': 'TypeSafe judgment',
          },
        },
      },
    })
    const t = i18n.getFixedT('en')
    assert.equal(ENDPOINT_TYPES.MODERATION, 'moderation')
    assert.equal(getEndpointTypeLabels(t).moderation, 'Content safety')
    assert.equal(getEndpointTypeLabel('moderation', t), 'Content safety')
    assert.equal(getEndpointTypeLabel('systemone', t), 'TypeSafe judgment')
    assert.equal(getEndpointTypeLabel('openai', t), 'openai')
    assert.deepEqual(ENDPOINT_TEMPLATES.moderation, {
      path: '/v1/moderations',
      method: 'POST',
    })
    assert.deepEqual(ENDPOINT_TEMPLATES.systemone, {
      path: '/typesafe/v1/systemone',
      method: 'POST',
    })
    const model: PricingModel = {
      id: 1,
      model_name: 'safety-alias',
      quota_type: 0,
      model_ratio: 1,
      completion_ratio: 0,
      enable_groups: ['default'],
      supported_endpoint_types: ['moderation'],
    }
    const models = [
      model,
      {
        ...model,
        id: 2,
        model_name: 'judge-alias',
        supported_endpoint_types: ['systemone'],
      },
      {
        ...model,
        id: 3,
        model_name: 'chat-alias',
        supported_endpoint_types: ['openai'],
      },
    ]
    assert.deepEqual(
      filterByEndpointType(models, ENDPOINT_TYPES.MODERATION).map(
        (item) => item.model_name
      ),
      ['safety-alias']
    )
  })
})
