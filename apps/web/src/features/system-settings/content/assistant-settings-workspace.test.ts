/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/

import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { AssistantSettingsFormValues } from './assistant-settings-schema'
import {
  getAssistantSettingsGroup,
  rebaseAssistantDraft,
} from './assistant-settings-workspace'

const values = (entry: Record<string, unknown>) =>
  entry as AssistantSettingsFormValues

test('server refresh updates untouched fields but never replaces unsaved edits', () => {
  const before = values({
    AssistantModel: 'old',
    AssistantSystemPrompt: 'old prompt',
    AssistantEnabled: true,
  })
  const draft = { ...before, AssistantSystemPrompt: 'my draft' }
  const incoming = {
    ...before,
    AssistantModel: 'new model',
    AssistantSystemPrompt: 'server prompt',
  }
  assert.deepEqual(rebaseAssistantDraft(before, draft, incoming), {
    ...incoming,
    AssistantSystemPrompt: 'my draft',
  })
  assert.equal(before.AssistantModel, 'old')
  assert.equal(incoming.AssistantSystemPrompt, 'server prompt')
})

test('save acknowledgement retains edits made during saving and clears acknowledged secrets', () => {
  const sent = values({
    AssistantSystemPrompt: 'sent',
    AssistantSearchAPIKey: 'new secret',
  })
  const pending = { ...sent, AssistantSystemPrompt: 'typed during save' }
  const confirmed = { ...sent, AssistantSearchAPIKey: '' }
  assert.deepEqual(rebaseAssistantDraft(sent, pending, confirmed), {
    AssistantSystemPrompt: 'typed during save',
    AssistantSearchAPIKey: '',
  })
})

test('invalid fields reveal the group that owns the input', () => {
  for (const [field, group] of [
    ['AssistantModel', 'model'],
    ['AssistantTemperature', 'model'],
    ['AssistantPreConversationPresets', 'conversation'],
    ['AssistantPersona', 'conversation'],
    ['AssistantSearchURL', 'tools'],
    ['AssistantSkillFiles', 'tools'],
    ['AssistantMaxSteps', 'runtime'],
    ['AssistantCacheTTLMinutes', 'runtime'],
    ['AssistantL1AutoReviewModel', 'review'],
    ['AssistantRegistrationDailySuspendCap', 'review'],
    ['AssistantActiveRetentionDays', 'retention'],
    ['AssistantSecurityRetentionDays', 'retention'],
  ]) {
    assert.equal(getAssistantSettingsGroup(field), group)
  }
})
