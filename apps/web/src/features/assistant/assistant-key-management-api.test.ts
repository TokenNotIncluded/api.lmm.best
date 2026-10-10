/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'

import { AxiosError, AxiosHeaders } from 'axios'

import { api } from '@/lib/api'

import {
  AssistantRequestError,
  confirmAssistantKeyAction,
  type AssistantKeyManagementAction,
} from './api'
import {
  keyActionReceipt,
  preparedKeyAction,
} from './assistant-key-management-test-fixtures'

const originalPost = api.post
afterEach(() => {
  api.post = originalPost
})

test('confirmation posts only the opaque token and normalized authentication code', async () => {
  const action = preparedKeyAction('disable', true)
  const calls: Array<{ url: string; data: unknown }> = []
  api.post = (async (url: string, data: unknown) => {
    calls.push({ url, data })
    return { data: { success: true, data: keyActionReceipt(action) } }
  }) as typeof api.post
  assert.deepEqual(
    await confirmAssistantKeyAction(action, '  ABCD-EFGH  '),
    keyActionReceipt(action)
  )
  assert.deepEqual(calls, [
    {
      url: '/api/assistant/tools/key-action',
      data: {
        confirmation_token: action.confirmation_token,
        two_factor_code: 'ABCD-EFGH',
      },
    },
  ])
})

test('malformed preparation cannot authorize a POST', async () => {
  let posts = 0
  api.post = (async () => {
    posts += 1
    throw new Error('must not post')
  }) as typeof api.post
  const tampered = {
    ...preparedKeyAction(),
    api_key: 'sk-private',
  } as AssistantKeyManagementAction
  await assert.rejects(
    () => confirmAssistantKeyAction(tampered),
    AssistantRequestError
  )
  assert.equal(posts, 0)
})

test('wrong target or secret-bearing result fails without exposing raw payload data', async () => {
  const action = preparedKeyAction()
  for (const data of [
    { ...keyActionReceipt(action), id: 8 },
    { ...keyActionReceipt(action), api_key: 'sk-private' },
    { ...keyActionReceipt(action), action: 'disable', status: 2 },
  ]) {
    api.post = (async () => ({
      data: { success: true, data },
    })) as typeof api.post
    await assert.rejects(
      () => confirmAssistantKeyAction(action),
      (error: unknown) =>
        error instanceof AssistantRequestError &&
        !error.message.includes('sk-private')
    )
  }
  api.post = (async () => ({
    data: {
      success: false,
      message: 'sk-private',
      code: 'ASSISTANT_KEY_CONFIRMATION_INVALID',
    },
  })) as typeof api.post
  await assert.rejects(
    () => confirmAssistantKeyAction(action),
    (error: unknown) =>
      error instanceof AssistantRequestError &&
      error.code === 'ASSISTANT_KEY_CONFIRMATION_INVALID' &&
      !error.message.includes('sk-private')
  )
})

test('preserves authoritative HTTP error codes without displaying raw error text', async () => {
  api.post = (async () => {
    const error = new AxiosError('sk-private')
    error.response = {
      data: {
        success: false,
        message: 'sk-private',
        code: 'ASSISTANT_TWO_FACTOR_INVALID',
      },
      status: 422,
      statusText: 'Unprocessable Entity',
      headers: {},
      config: { headers: new AxiosHeaders() },
    }
    throw error
  }) as typeof api.post
  await assert.rejects(
    () => confirmAssistantKeyAction(preparedKeyAction()),
    (error: unknown) =>
      error instanceof AssistantRequestError &&
      error.code === 'ASSISTANT_TWO_FACTOR_INVALID' &&
      !error.message.includes('sk-private')
  )
})

test('a matching receipt cannot turn a malformed success envelope into confirmation', async () => {
  const action = preparedKeyAction()
  for (const data of [
    null,
    'success',
    { success: 'false', data: keyActionReceipt(action) },
    { success: 1, data: keyActionReceipt(action) },
    { success: {}, data: keyActionReceipt(action) },
  ]) {
    api.post = (async () => ({ data })) as typeof api.post
    await assert.rejects(
      () => confirmAssistantKeyAction(action),
      AssistantRequestError
    )
  }
})
