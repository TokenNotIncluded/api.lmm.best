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
import { describe, test } from 'node:test'

import { channelSchema, type Channel } from '../../types'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  channelFormSchema,
  transformChannelToFormDefaults,
  transformFormDataToCreatePayload,
  transformFormDataToUpdatePayload,
} from '../channel-form'

function ollamaChannel(overrides: Partial<Channel> = {}): Channel {
  return channelSchema.parse({
    id: 19,
    type: 4,
    name: 'Local Ollama',
    key: 'test-key',
    status: 1,
    base_url: 'http://localhost:11434',
    models: 'llama3.2',
    created_time: 0,
    test_time: 0,
    response_time: 0,
    balance_updated_time: 0,
    ...overrides,
    remark: overrides.remark ?? '',
    other: overrides.other ?? '',
  })
}

function ollamaForm() {
  return {
    ...CHANNEL_FORM_DEFAULT_VALUES,
    name: 'Local Ollama',
    type: 4,
    key: 'test-key',
    base_url: 'http://localhost:11434',
    models: 'llama3.2',
  }
}

function parseSettings(
  value: string | null | undefined
): Record<string, unknown> {
  assert.ok(typeof value === 'string')
  return JSON.parse(value) as Record<string, unknown>
}

describe('Ollama chat transport setting', () => {
  test('keeps native chat as the default for new and legacy channels', () => {
    assert.equal(CHANNEL_FORM_DEFAULT_VALUES.ollama_openai_chat, false)
    assert.equal(
      transformChannelToFormDefaults(ollamaChannel()).ollama_openai_chat,
      false
    )

    const payload = transformFormDataToCreatePayload(ollamaForm()).channel
    assert.equal(parseSettings(payload.settings).ollama_openai_chat, false)
    assert.equal(
      Object.hasOwn(parseSettings(payload.setting), 'ollama_openai_chat'),
      false
    )
  })

  test('accepts a boolean opt-in and saves it in other settings', () => {
    const form = channelFormSchema.parse({
      ...ollamaForm(),
      ollama_openai_chat: true,
      settings: JSON.stringify({ vendor_options: { keep_alive: '5m' } }),
      other: 'provider-metadata',
    })
    const payload = transformFormDataToCreatePayload(form).channel
    const settings = parseSettings(payload.settings)

    assert.equal(settings.ollama_openai_chat, true)
    assert.deepEqual(settings.vendor_options, { keep_alive: '5m' })
    assert.equal(payload.other, 'provider-metadata')
    assert.equal(payload.type, 4)
    assert.equal(payload.base_url, 'http://localhost:11434')
    assert.equal(
      transformChannelToFormDefaults(ollamaChannel(payload)).ollama_openai_chat,
      true
    )
    assert.equal(
      channelFormSchema.safeParse({
        ...ollamaForm(),
        ollama_openai_chat: 'true',
      }).success,
      false
    )
  })

  test('round-trips enabling, editing, disabling, and reloading a channel', () => {
    const stored = ollamaChannel({
      settings: JSON.stringify({
        ollama_openai_chat: true,
        vendor_options: { keep_alive: '5m' },
        upstream_model_update_check_enabled: true,
        upstream_model_update_last_check_time: 123,
        upstream_model_update_last_detected_models: ['llama3.2'],
      }),
    })
    const loaded = transformChannelToFormDefaults(stored)
    assert.equal(loaded.ollama_openai_chat, true)

    const saved = transformFormDataToUpdatePayload(
      { ...loaded, name: 'Renamed Ollama' },
      stored.id
    )
    const savedSettings = parseSettings(saved.settings)
    assert.equal(savedSettings.ollama_openai_chat, true)
    assert.deepEqual(savedSettings.vendor_options, { keep_alive: '5m' })
    assert.equal(savedSettings.upstream_model_update_last_check_time, 123)
    assert.deepEqual(savedSettings.upstream_model_update_last_detected_models, [
      'llama3.2',
    ])

    const reloaded = transformChannelToFormDefaults(
      ollamaChannel({ ...stored, ...saved })
    )
    assert.equal(reloaded.name, 'Renamed Ollama')
    assert.equal(reloaded.ollama_openai_chat, true)

    const disabled = transformFormDataToUpdatePayload(
      { ...reloaded, ollama_openai_chat: false },
      stored.id
    )
    assert.equal(parseSettings(disabled.settings).ollama_openai_chat, false)
    assert.deepEqual(parseSettings(disabled.settings).vendor_options, {
      keep_alive: '5m',
    })
    assert.equal(
      transformChannelToFormDefaults(ollamaChannel({ ...stored, ...disabled }))
        .ollama_openai_chat,
      false
    )
  })

  test('does not treat non-boolean stored values as an opt-in', () => {
    for (const value of [false, 'true', 1, null]) {
      const defaults = transformChannelToFormDefaults(
        ollamaChannel({
          settings: JSON.stringify({ ollama_openai_chat: value }),
        })
      )
      assert.equal(defaults.ollama_openai_chat, false)
    }
  })

  test('removes the Ollama-only setting when switching channel types', () => {
    for (const type of [1, 14, 8, 19, 58, 60]) {
      const payload = transformFormDataToCreatePayload({
        ...ollamaForm(),
        type,
        ollama_openai_chat: true,
        settings: JSON.stringify({
          ollama_openai_chat: true,
          vendor_options: { keep_alive: '5m' },
        }),
      }).channel
      const settings = parseSettings(payload.settings)

      assert.equal(Object.hasOwn(settings, 'ollama_openai_chat'), false)
      assert.deepEqual(settings.vendor_options, { keep_alive: '5m' })
    }
  })
})
