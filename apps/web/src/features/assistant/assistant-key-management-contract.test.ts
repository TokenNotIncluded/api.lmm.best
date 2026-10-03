/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { parseAssistantAction } from './api'
import {
  parseAssistantKeyManagementAction,
  parseAssistantKeyManagementReceipt,
} from './assistant-key-management-contract'
import {
  keyActionReceipt,
  preparedKeyAction,
} from './assistant-key-management-test-fixtures'

test('accepts exact server-owned key metadata including legacy empty names and groups', () => {
  for (const verb of ['delete', 'disable'] as const) {
    const action = preparedKeyAction(verb)
    assert.deepEqual(parseAssistantAction(action), action)
    const legacy = {
      ...action,
      token: { ...action.token, name: '', group: '' },
    }
    assert.deepEqual(parseAssistantAction(legacy), legacy)
  }
})

test('rejects malformed or tampered key actions before fields can be discarded', () => {
  const original = preparedKeyAction()
  const invalid: unknown[] = [
    { ...original, action: 'enable' },
    { ...original, requires_confirmation: false },
    { ...original, confirmation_token: '' },
    { ...original, confirmation_token: ' padded ' },
    { ...original, confirmation_token: 'x'.repeat(513) },
    { ...original, expires_in_seconds: 0 },
    { ...original, expires_in_seconds: Infinity },
    { ...original, expires_in_seconds: 0.5 },
    { ...original, ui_path: '/keys?secret=sk-private' },
    { ...original, two_factor_required: undefined },
    { ...original, api_key: 'sk-private' },
    { ...original, token: { ...original.token, key: 'sk-private' } },
    { ...original, token: { ...original.token, id: -7 } },
    {
      ...original,
      token: { ...original.token, id: Number.MAX_SAFE_INTEGER + 1 },
    },
    { ...original, token: { ...original.token, status: 99 } },
    { ...original, token: { ...original.token, accessed_time: Number.NaN } },
    { ...original, token: { ...original.token, expired_time: -2 } },
    { ...original, token: { ...original.token, name: 7 } },
  ]
  for (const payload of invalid) {
    assert.equal(parseAssistantKeyManagementAction(payload), undefined)
    assert.equal(parseAssistantAction(payload), undefined)
  }
})

test('receipts match the prepared target and never accept unexpected secret fields', () => {
  const action = preparedKeyAction()
  const receipt = keyActionReceipt(action)
  assert.deepEqual(parseAssistantKeyManagementReceipt(receipt, action), receipt)
  for (const payload of [
    { ...receipt, id: 8 },
    { ...receipt, action: 'disable' },
    { ...receipt, name: 'Another key' },
    { ...receipt, group: 'other' },
    { ...receipt, api_key: 'sk-private' },
    { ...receipt, token: { key: 'sk-private' } },
  ]) {
    assert.equal(parseAssistantKeyManagementReceipt(payload, action), undefined)
  }
})

test('disabling receipts must establish the disabled status', () => {
  const action = preparedKeyAction('disable')
  const receipt = keyActionReceipt(action)
  assert.deepEqual(parseAssistantKeyManagementReceipt(receipt, action), receipt)
  assert.equal(
    parseAssistantKeyManagementReceipt({ ...receipt, status: 1 }, action),
    undefined
  )
  assert.equal(
    parseAssistantKeyManagementReceipt(
      { ...receipt, status: undefined },
      action
    ),
    undefined
  )
})
