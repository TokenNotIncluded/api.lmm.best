/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { todosSearchSchema } from './index'

test('discards a retired developer-access navigation target', () => {
  assert.deepEqual(
    todosSearchSchema.parse({ todo: 'developer_access', request: 42 }),
    { todo: undefined, request: 42 }
  )
})
