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
import { readFile } from 'node:fs/promises'
import { describe, test } from 'node:test'

import { createInstance } from 'i18next'

import { api } from '@/lib/api'

import { getAssistantPreConversationPresets } from './api'
import {
  ASSISTANT_PROMPT_PRESET_COPY_VERSION,
  localizeAssistantPreConversationPresets,
} from './assistant-prompt-presets'

const starterKeys = {
  ai_recommendation: 'Help me write an L1 recommendation.',
  getting_started: 'Where should I start?',
  new_user_gift: 'How do I get the new-user gift?',
  weekly_discount: 'Any top-up discounts this week?',
}

const legacyPresets = Object.keys(starterKeys).map((id) => ({
  id,
  label: '旧标签',
  prompt: '请围绕旧模板说明权限边界。',
}))

describe('localized assistant conversation starters', () => {
  for (const locale of ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi']) {
    test(`${locale}: translates cached copy and fallback without changing IDs or ordering`, async () => {
      const resource = JSON.parse(
        await readFile(
          new URL(`../../i18n/locales/${locale}.json`, import.meta.url),
          'utf8'
        )
      ) as { translation: Record<string, string> }
      const i18n = createInstance()
      await i18n.init({
        lng: locale,
        fallbackLng: 'en',
        resources: { [locale]: resource },
      })
      const t = (key: string) => i18n.t(key)
      const localized = localizeAssistantPreConversationPresets(
        legacyPresets,
        t
      )
      const fallback = localizeAssistantPreConversationPresets(undefined, t)
      assert.deepEqual(localized, fallback)
      assert.equal(localized.length, 4)
      for (const [index, preset] of localized.entries()) {
        assert.equal(preset.id, legacyPresets[index].id)
        const key = starterKeys[preset.id as keyof typeof starterKeys]
        assert.ok(resource.translation[key])
        assert.equal(preset.prompt, resource.translation[key])
        assert.equal(preset.label, preset.prompt)
        if (locale !== 'en') assert.notEqual(preset.prompt, key)
      }
      assert.equal(
        legacyPresets[0].label,
        '旧标签',
        'cached data must remain immutable'
      )
      assert.deepEqual(
        localizeAssistantPreConversationPresets(
          [...legacyPresets].reverse(),
          t
        ),
        [...localized].reverse()
      )
    })
  }

  test('keeps unknown server copy verbatim and does not translate arbitrary IDs as keys', () => {
    const unknown = {
      id: '__proto__',
      label: 'Custom starter',
      prompt: 'Server-reviewed copy',
    }
    const presets = localizeAssistantPreConversationPresets([unknown], () => {
      throw new Error('unknown IDs must not be translated')
    })
    assert.deepEqual(presets, [unknown])
    assert.deepEqual(
      localizeAssistantPreConversationPresets([], (key) => key),
      []
    )
  })

  test('unknown locales use the English fallback rather than old Chinese copy', async () => {
    const i18n = createInstance()
    await i18n.init({
      lng: 'unknown',
      fallbackLng: 'en',
      resources: { en: { translation: {} } },
    })
    const presets = localizeAssistantPreConversationPresets(
      legacyPresets,
      (key) => i18n.t(key)
    )
    assert.deepEqual(
      presets.map((preset) => preset.prompt),
      Object.values(starterKeys)
    )
  })

  test('separates public HTTP cache entries by language and copy version', async () => {
    const originalGet = api.get
    const data = {
      generation: 0,
      version: 'backend-seed-v2',
      presets: legacyPresets,
    }
    const params: unknown[] = []
    api.get = (async (url: string, options: { params: unknown }) => {
      assert.equal(url, '/api/assistant/pre-conversation-presets')
      params.push(options.params)
      return { data: { success: true, data } }
    }) as typeof api.get
    try {
      assert.deepEqual(await getAssistantPreConversationPresets('zhTW'), data)
      assert.deepEqual(await getAssistantPreConversationPresets('fr'), data)
      assert.deepEqual(params, [
        {
          language: 'zhTW',
          copy_version: ASSISTANT_PROMPT_PRESET_COPY_VERSION,
        },
        { language: 'fr', copy_version: ASSISTANT_PROMPT_PRESET_COPY_VERSION },
      ])
    } finally {
      api.get = originalGet
    }
  })
})
