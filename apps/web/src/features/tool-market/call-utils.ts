/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import { MarketAPIError, type CallResponse } from './api'
import { schemaObject } from './schema-form-utils'

export function callCanBeEdited(error: unknown) {
  return (
    error instanceof MarketAPIError &&
    [
      'TOOL_MARKET_ARGUMENTS',
      'TOOL_MARKET_INVALID_INPUT',
      'TOOL_MARKET_BUDGET',
      'TOOL_MARKET_BALANCE',
      'TOOL_MARKET_BUSY',
      'TOOL_MARKET_DENIED',
      'TOOL_MARKET_NOT_FOUND',
      'TOOL_MARKET_REMOTE_CONNECTION',
      'TOOL_MARKET_REMOTE_CHANGED',
      'TOOL_MARKET_REMOTE_AUTH',
    ].includes(error.code)
  )
}

export function callConfirmation(response: CallResponse | null) {
  if (
    !response ||
    response.call.execution_status !== 'awaiting_confirmation' ||
    !schemaObject(response.result) ||
    typeof response.result.requestState !== 'string' ||
    !response.result.requestState ||
    !schemaObject(response.result.inputRequests) ||
    !schemaObject(response.result.inputRequests.confirmation)
  ) {
    return null
  }
  const request = response.result.inputRequests.confirmation
  if (request.mode !== 'form' || typeof request.message !== 'string') {
    return null
  }
  return {
    requestState: response.result.requestState,
    message: request.message,
  }
}

export function canStartAnotherCall(response: CallResponse | null) {
  return (
    !!response &&
    ['succeeded', 'failed', 'cancelled'].includes(
      response.call.execution_status
    ) &&
    ['settled', 'released'].includes(response.call.settlement_status)
  )
}

export function marketErrorKey(error: unknown) {
  const code = error instanceof MarketAPIError ? error.code : ''
  switch (code) {
    case 'TOOL_MARKET_ARGUMENTS':
    case 'TOOL_MARKET_INVALID_INPUT':
      return 'Check the tool parameters and try again.'
    case 'TOOL_MARKET_BUDGET':
      return 'This authorization or budget has no remaining allowance.'
    case 'TOOL_MARKET_BALANCE':
      return 'Your available balance is too low for this call.'
    case 'TOOL_MARKET_REMOTE_CHANGED':
      return 'The provider changed this tool. Refresh its definitions before calling.'
    case 'TOOL_MARKET_REMOTE_CONNECTION':
      return 'The provider could not be connected. Check its endpoint and credentials.'
    case 'TOOL_MARKET_REMOTE_AUTH':
      return 'The provider rejected its credentials. Update the service authentication.'
    case 'TOOL_MARKET_CREDENTIALS_UNAVAILABLE':
      return 'Service credential storage is unavailable. Contact an administrator.'
    case 'TOOL_MARKET_BUSY':
      return 'The tool is busy. Wait briefly and retry.'
    case 'TOOL_MARKET_DENIED':
    case 'TOOL_MARKET_NOT_FOUND':
      return 'This tool or authorization is no longer available. Refresh access.'
    case 'TOOL_MARKET_CONFLICT':
      return 'The request or version changed. Refresh the original call before trying again.'
    default:
      return 'The call could not be confirmed. Retry the same request to check its status.'
  }
}
