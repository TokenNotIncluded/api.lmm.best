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

test('tool policy refresh merges independent local tool and remote group edits', () => {
  const initialPolicy = '{"version":1,"groups":{},"tools":{}}'
  const savedPolicy = '{"version":1,"groups":{"drawing":false},"tools":{}}'
  const localPolicy = '{"version":1,"groups":{},"tools":{"search_web":false}}'
  const before = values({ AssistantToolPolicy: initialPolicy })
  const incoming = values({ AssistantToolPolicy: savedPolicy })

  assert.equal(
    rebaseAssistantDraft(before, before, incoming).AssistantToolPolicy,
    savedPolicy
  )
  assert.deepEqual(
    JSON.parse(
      rebaseAssistantDraft(
        before,
        values({ AssistantToolPolicy: localPolicy }),
        incoming
      ).AssistantToolPolicy
    ),
    {
      version: 1,
      groups: { drawing: false },
      tools: { search_web: false },
    }
  )
  assert.equal(before.AssistantToolPolicy, initialPolicy)
})

test('an explicit default-enabled choice does not override a concurrent remote disable', () => {
  const before = values({
    AssistantToolPolicy: '{"version":1,"groups":{},"tools":{}}',
  })
  const draft = values({
    AssistantToolPolicy:
      '{"version":1,"groups":{},"tools":{"search_web":true}}',
  })
  const incoming = values({
    AssistantToolPolicy:
      '{"version":1,"groups":{"drawing":false},"tools":{"search_web":false}}',
  })

  assert.deepEqual(
    JSON.parse(
      rebaseAssistantDraft(before, draft, incoming).AssistantToolPolicy
    ),
    JSON.parse(incoming.AssistantToolPolicy)
  )
})

test('an intentional tool re-enable survives an unrelated remote group disable', () => {
  const before = values({
    AssistantToolPolicy:
      '{"version":1,"groups":{},"tools":{"search_web":false}}',
  })
  const draft = values({
    AssistantToolPolicy:
      '{"version":1,"groups":{},"tools":{"search_web":true}}',
  })
  const incoming = values({
    AssistantToolPolicy:
      '{"version":1,"groups":{"drawing":false},"tools":{"search_web":false}}',
  })

  assert.deepEqual(
    JSON.parse(
      rebaseAssistantDraft(before, draft, incoming).AssistantToolPolicy
    ),
    {
      version: 1,
      groups: { drawing: false },
      tools: { search_web: true },
    }
  )
})

test('legacy empty policies merge independent leaves without losing a remote disable', () => {
  const before = values({ AssistantToolPolicy: '' })
  const draft = values({
    AssistantToolPolicy: '{"version":1,"tools":{"search_web":false}}',
  })
  const incoming = values({
    AssistantToolPolicy: '{"version":1,"groups":{"drawing":false}}',
  })

  assert.deepEqual(
    JSON.parse(
      rebaseAssistantDraft(before, draft, incoming).AssistantToolPolicy
    ),
    {
      version: 1,
      groups: { drawing: false },
      tools: { search_web: false },
    }
  )
})

test('tool policy acknowledgement keeps a newer edit made while the previous policy was saving', () => {
  const sent = values({
    AssistantToolPolicy: '{"version":1,"groups":{"account":false},"tools":{}}',
    AssistantModel: 'old model',
  })
  const pending = {
    ...sent,
    AssistantToolPolicy:
      '{"version":1,"groups":{"account":false},"tools":{"web_search":false}}',
  }
  const confirmed = { ...sent, AssistantModel: 'server model' }

  assert.deepEqual(rebaseAssistantDraft(sent, pending, confirmed), {
    ...confirmed,
    AssistantToolPolicy: pending.AssistantToolPolicy,
  })
})

test('save acknowledgement retains a newer local tool edit alongside a remote group change', () => {
  const sent = values({
    AssistantToolPolicy:
      '{"version":1,"groups":{},"tools":{"search_web":false}}',
  })
  const pending = values({
    AssistantToolPolicy:
      '{"version":1,"groups":{},"tools":{"search_web":false,"request_create_key":false}}',
  })
  const confirmed = values({
    AssistantToolPolicy:
      '{"version":1,"groups":{"drawing":false},"tools":{"search_web":false}}',
  })

  assert.deepEqual(
    JSON.parse(
      rebaseAssistantDraft(sent, pending, confirmed).AssistantToolPolicy
    ),
    {
      version: 1,
      groups: { drawing: false },
      tools: { search_web: false, request_create_key: false },
    }
  )
})

test('invalid fields reveal the group that owns the input', () => {
  for (const [field, group] of [
    ['AssistantModel', 'model'],
    ['AssistantModerationEnabled', 'moderation'],
    ['AssistantModerationGroup', 'moderation'],
    ['AssistantModerationModel', 'moderation'],
    ['AssistantTemperature', 'model'],
    ['AssistantPreConversationPresets', 'conversation'],
    ['AssistantPersona', 'conversation'],
    ['AssistantSearchURL', 'tools'],
    ['AssistantToolPolicy', 'tools'],
    ['AssistantSkillFiles', 'tools'],
    ['AssistantMaxSteps', 'runtime'],
    ['AssistantCacheTTLMinutes', 'runtime'],
    ['AssistantRegistrationDailySuspendCap', 'review'],
    ['AssistantActiveRetentionDays', 'retention'],
    ['AssistantSecurityRetentionDays', 'retention'],
  ]) {
    assert.equal(getAssistantSettingsGroup(field), group)
  }
})
