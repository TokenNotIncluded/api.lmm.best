/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { assistantRunFailureDetails } from './api'
import {
  AssistantStreamError,
  assistantTimeoutError,
} from './assistant-ai-stream'

test('reads structural run details from a streamed timeout event', () => {
  const error = new AssistantStreamError(
    408,
    {
      code: 'ASSISTANT_REQUEST_TIMEOUT',
      status: 408,
      steps: 4,
      max_steps: 12,
      work_started: true,
      elapsed_ms: 61_000,
      timeout_ms: 60_000,
    },
    'assistant request stopped'
  )
  assert.deepEqual(assistantRunFailureDetails(error), {
    steps: 4,
    max_steps: 12,
    work_started: true,
    elapsed_ms: 61_000,
    timeout_ms: 60_000,
  })
})

test('reads no details from a bare timeout and keeps retryable false', () => {
  const error = assistantTimeoutError()
  assert.equal(assistantRunFailureDetails(error), undefined)
  assert.equal(error.retryable, false)
})

test('ignores non-numeric or missing diagnostic fields', () => {
  const error = new AssistantStreamError(
    408,
    { code: 'ASSISTANT_REQUEST_TIMEOUT', steps: 'many', work_started: 'yes' },
    'stopped'
  )
  assert.equal(assistantRunFailureDetails(error), undefined)
})
