/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { CHANNEL_TYPES, supportsResponsesWebSocket } from '../../constants'
import { channelSchema, type Channel } from '../../types'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  buildSettingJSON,
  channelFormSchema,
  transformChannelToFormDefaults,
  transformFormDataToCreatePayload,
  transformFormDataToUpdatePayload,
  type ChannelFormValues,
} from '../channel-form'

const supportedTypes = [1, 57, 58, 59, 60]

function channelForm(type: number): ChannelFormValues {
  return {
    ...CHANNEL_FORM_DEFAULT_VALUES,
    type,
    name: 'Responses upstream',
    base_url: 'https://relay.example',
    key:
      type === 57
        ? JSON.stringify({
            access_token: 'test-token',
            account_id: 'test-account',
          })
        : 'test-key',
    models: 'gpt-5',
    advanced_custom: JSON.stringify({
      advanced_routes: [
        {
          incoming_path: '/v1/responses',
          upstream_path: '/v1/responses',
          converter: 'none',
        },
      ],
    }),
  }
}

function savedChannel(payload: Partial<Channel>): Channel {
  return channelSchema.parse({
    id: 42,
    status: 1,
    created_time: 0,
    test_time: 0,
    response_time: 0,
    balance_updated_time: 0,
    ...payload,
    key: payload.key || '',
    other: payload.other || '',
    remark: payload.remark || '',
  })
}

function websocketEnabled(setting: string | null | undefined): unknown {
  return JSON.parse(setting || '{}').responses_websocket_enabled
}

describe('Responses WebSocket channel settings', () => {
  test('shares the eligibility matrix across all registered channel types', () => {
    for (const type of Object.keys(CHANNEL_TYPES).map(Number)) {
      assert.equal(
        supportsResponsesWebSocket(type),
        supportedTypes.includes(type),
        `type ${type}`
      )
    }
    assert.equal(supportsResponsesWebSocket(999), false)
  })

  for (const type of supportedTypes) {
    for (const enabled of [true, false]) {
      test(`type ${type} preserves explicit ${enabled} through schema, create, reload and update`, () => {
        const parsedForm = channelFormSchema.parse({
          ...channelForm(type),
          responses_websocket_enabled: enabled,
        })
        const created = transformFormDataToCreatePayload(parsedForm).channel
        assert.equal(websocketEnabled(created.setting), enabled)

        const loaded = transformChannelToFormDefaults(savedChannel(created))
        assert.equal(loaded.responses_websocket_enabled, enabled)
        const updated = transformFormDataToUpdatePayload(loaded, 42)
        assert.equal(websocketEnabled(updated.setting), enabled)
        assert.equal(
          transformChannelToFormDefaults(
            savedChannel({ ...created, ...updated })
          ).responses_websocket_enabled,
          enabled
        )
      })
    }

    test(`type ${type} keeps its unset compatibility default`, () => {
      const expected = [1, 57].includes(type)
      const form = channelFormSchema.parse(channelForm(type))
      assert.equal(websocketEnabled(buildSettingJSON(form)), expected)
      const created = transformFormDataToCreatePayload(form).channel
      assert.equal(websocketEnabled(created.setting), expected)

      for (const setting of [undefined, '{}']) {
        const loaded = transformChannelToFormDefaults(
          savedChannel({ ...created, setting })
        )
        assert.equal(loaded.responses_websocket_enabled, expected)
        assert.equal(
          websocketEnabled(
            transformFormDataToUpdatePayload(loaded, 42).setting
          ),
          expected
        )
      }
    })
  }

  test('omits the WebSocket setting for unsupported types even when stale data enables it', () => {
    for (const type of Object.keys(CHANNEL_TYPES)
      .map(Number)
      .filter((type) => !supportsResponsesWebSocket(type))) {
      const form = { ...channelForm(type), responses_websocket_enabled: true }
      const created = transformFormDataToCreatePayload(form).channel
      assert.equal(
        websocketEnabled(created.setting),
        undefined,
        `create type ${type}`
      )
      const loaded = transformChannelToFormDefaults(
        savedChannel({
          ...created,
          setting: JSON.stringify({ responses_websocket_enabled: true }),
        })
      )
      assert.equal(
        loaded.responses_websocket_enabled,
        false,
        `reload type ${type}`
      )
      assert.equal(
        websocketEnabled(transformFormDataToUpdatePayload(loaded, 42).setting),
        undefined,
        `update type ${type}`
      )
    }
  })

  test('requires a boolean and treats non-boolean stored values as unset', () => {
    assert.equal(
      channelFormSchema.safeParse({
        ...channelForm(1),
        responses_websocket_enabled: 'false',
      }).success,
      false
    )
    for (const type of [1, 58]) {
      const loaded = transformChannelToFormDefaults(
        savedChannel({
          ...transformFormDataToCreatePayload(channelForm(type)).channel,
          setting: JSON.stringify({ responses_websocket_enabled: 'false' }),
        })
      )
      assert.equal(loaded.responses_websocket_enabled, type === 1)
    }
  })
})
