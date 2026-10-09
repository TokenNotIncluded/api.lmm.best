/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { parseAssistantWorkspaceAction } from './assistant-workspace-contract'

const base = {
  type: 'workspace_action',
  requires_confirmation: true,
  confirmation_token: 'test-only-opaque-confirmation-token',
}
test('workspace preview requires explicit confirmation and a valid exact action', () => {
  const invitation = {
    ...base,
    tool: 'send_invitation',
    preview: { email: 'test@example.com' },
  }
  assert.ok(parseAssistantWorkspaceAction(invitation))
  assert.equal(
    parseAssistantWorkspaceAction({
      ...invitation,
      requires_confirmation: false,
    }),
    undefined
  )
  assert.equal(
    parseAssistantWorkspaceAction({ ...invitation, confirmation_token: '' }),
    undefined
  )
  assert.equal(
    parseAssistantWorkspaceAction({
      ...invitation,
      preview: { email: 'not-an-email' },
    }),
    undefined
  )
  assert.equal(
    parseAssistantWorkspaceAction({
      ...base,
      tool: 'arbitrary_command',
      preview: {},
    }),
    undefined
  )
  const greeting = {
    ...base,
    tool: 'set_overview_greeting',
    preview: { language: 'zh', template: 'HI,$name,现在是$time', revision: 0 },
  }
  assert.ok(parseAssistantWorkspaceAction(greeting))
  assert.equal(
    parseAssistantWorkspaceAction({
      ...greeting,
      preview: { ...greeting.preview, language: 'unknown' },
    }),
    undefined
  )
})
