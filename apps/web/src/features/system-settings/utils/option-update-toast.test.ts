/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { afterEach, beforeEach, test } from 'node:test'

import { toast } from 'sonner'

import { showOptionUpdateToast } from './option-update-toast'

const originalWarning = toast.warning
const originalSuccess = toast.success
const originalError = toast.error
let warnings: unknown[] = []
let successes: unknown[] = []
let errors: unknown[] = []

beforeEach(() => {
  warnings = []
  successes = []
  errors = []
  toast.warning = ((message: unknown) => {
    warnings.push(message)
    return 'warning'
  }) as typeof toast.warning
  toast.success = ((message: unknown) => {
    successes.push(message)
    return 'success'
  }) as typeof toast.success
  toast.error = ((message: unknown) => {
    errors.push(message)
    return 'error'
  }) as typeof toast.error
})

afterEach(() => {
  toast.warning = originalWarning
  toast.success = originalSuccess
  toast.error = originalError
})

test('ignored locked prices produce warnings without success or error toasts', () => {
  showOptionUpdateToast(
    {
      success: true,
      message: '',
      warnings: ['alpha is locked', 'beta is locked'],
      locked_models: ['alpha', 'beta'],
    },
    'Prices saved'
  )

  assert.deepEqual(warnings, ['alpha is locked\nbeta is locked'])
  assert.deepEqual(successes, [])
  assert.deepEqual(errors, [])
})

test('accepted changes retain the normal success notification', () => {
  for (const response of [
    { success: true, message: '' },
    { success: true, message: '', warnings: [] },
  ]) {
    showOptionUpdateToast(response, 'Prices saved')
  }

  assert.deepEqual(successes, ['Prices saved', 'Prices saved'])
  assert.deepEqual(warnings, [])
  assert.deepEqual(errors, [])
})
