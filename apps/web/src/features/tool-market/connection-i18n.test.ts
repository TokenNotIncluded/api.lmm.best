/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  marketConnectionTranslations,
  registerMarketConnectionTranslations,
} from './connection-i18n'

const placeholders = (value: string) =>
  [...value.matchAll(/{{\s*([^},\s]+)[^}]*}}/g)].map((match) => match[1]).sort()

test('connection copy is translated with matching placeholders in every locale', () => {
  const keys = Object.keys(marketConnectionTranslations.en).sort()
  assert.ok(keys.length > 0)
  for (const [language, copy] of Object.entries(marketConnectionTranslations)) {
    assert.deepEqual(Object.keys(copy).sort(), keys)
    for (const key of keys) {
      const id = key as keyof typeof copy
      assert.ok(copy[id].trim(), `${language}:${id}`)
      assert.deepEqual(
        placeholders(copy[id]),
        placeholders(marketConnectionTranslations.en[id])
      )
      if (language !== 'en')
        assert.notEqual(copy[id], marketConnectionTranslations.en[id])
    }
  }
})

test('registration is instance-scoped and idempotent without overwriting translations', () => {
  const calls: unknown[][] = []
  const instance = {
    addResourceBundle: (...args: unknown[]) => {
      calls.push(args)
    },
  } as unknown as Parameters<typeof registerMarketConnectionTranslations>[0]
  registerMarketConnectionTranslations(instance)
  registerMarketConnectionTranslations(instance)
  assert.equal(calls.length, 7)
  for (const args of calls) {
    assert.equal(args[1], 'tool-market-connections')
    assert.equal(args[3], true)
    assert.equal(args[4], false)
  }
})
