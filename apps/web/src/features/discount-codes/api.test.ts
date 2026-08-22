/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { afterEach, describe, test } from 'node:test'

import { api } from '@/lib/api'

import { deleteExhaustedDiscountCodes } from './api.js'

const originalDelete = api.delete

afterEach(() => {
  api.delete = originalDelete
})

describe('discount code cleanup API', () => {
  test('deletes exhausted codes through the static route', async () => {
    api.delete = (async (url: string) => {
      assert.equal(url, '/api/discount-code/exhausted')
      return { data: { success: true, message: '', data: { count: 2 } } }
    }) as typeof api.delete

    const result = await deleteExhaustedDiscountCodes()
    assert.equal(result.success, true)
    assert.equal(result.data?.count, 2)
  })
})
