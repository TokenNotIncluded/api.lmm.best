/*
Copyright (C) 2026 LIghtJUNction
*/
import type { AssistantKeyManagementAction } from './api'

export function preparedKeyAction(
  action: 'delete' | 'disable' = 'delete',
  twoFactorRequired = false
): AssistantKeyManagementAction {
  return {
    type: 'api_key_action',
    action,
    confirmation_token: 'opaque-key-action-token',
    requires_confirmation: true,
    expires_in_seconds: 600,
    token: {
      id: 7,
      name: 'Production SDK',
      status: 1,
      group: 'default',
      created_time: 1_780_000_000,
      accessed_time: 1_780_000_100,
      expired_time: -1,
    },
    two_factor_required: twoFactorRequired,
    ui_path: '/keys',
  }
}

export function keyActionReceipt(action = preparedKeyAction()) {
  return {
    id: action.token.id,
    name: action.token.name,
    group: action.token.group,
    action: action.action,
    ...(action.action === 'disable' ? { status: 2 as const } : {}),
  }
}
