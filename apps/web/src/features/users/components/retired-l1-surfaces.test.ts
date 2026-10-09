/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { existsSync, readFileSync, readdirSync } from 'node:fs'
import { test } from 'node:test'

const root = new URL('../../', import.meta.url)
const read = (path: string) => readFileSync(new URL(path, root), 'utf8')

test('user management has no L1 application or archive component or client API', () => {
  for (const name of [
    'developer-access-requests-panel',
    'user-recommendation-archive-dialog',
  ]) {
    assert.equal(
      existsSync(new URL(`users/components/${name}.tsx`, root)),
      false
    )
  }
  assert.doesNotMatch(
    read('users/components/data-table-row-actions.tsx'),
    /UserRecommendationArchive/
  )
  assert.doesNotMatch(
    read('users/api.ts'),
    /DeveloperAccessRequest|RecommendationArchive|developer-access/
  )
  assert.match(
    read('users/components/data-table-row-actions.tsx'),
    /ReferralModerationDialog/
  )
})

test('L0 and settings copy describe activation, not a letter or review queue', () => {
  for (const path of [
    'onboarding/l0-welcome.tsx',
    'assistant/assistant-registration-state.ts',
    'assistant/assistant-cost-tool.tsx',
    'assistant/assistant-plan-tool.tsx',
    'system-settings/content/assistant-registration-guard-settings.tsx',
  ]) {
    assert.doesNotMatch(
      read(path),
      /recommendation.?letter|application.?letter|L1 request.*review|automatic review approves L1/i,
      path
    )
  }
  for (const path of [
    'system-settings/content/assistant-settings-schema.ts',
    'system-settings/content/section-registry.tsx',
    'system-settings/content/index.tsx',
    'system-settings/types.ts',
  ]) {
    assert.doesNotMatch(read(path), /AssistantL1AutoReview/, path)
  }
})

test('all seven locales omit retired L1 labels but keep current activation and invitation copy', () => {
  const localeRoot = new URL('../../../i18n/locales/', import.meta.url)
  const names = readdirSync(localeRoot).filter((name) => name.endsWith('.json'))
  assert.equal(names.length, 7)
  for (const name of names) {
    const { translation } = JSON.parse(
      readFileSync(new URL(name, localeRoot), 'utf8')
    )
    for (const key of [
      'Recommendation letter',
      'L1 recommendation archive',
      'View L1 recommendation archive',
      'L1 application review',
    ]) {
      assert.equal(Object.hasOwn(translation, key), false, `${name}: ${key}`)
    }
    assert.ok(
      translation['The assistant can enable L1 during this conversation.'],
      name
    )
    assert.ok(translation['Invitation rewards'], name)
  }
})
