/* Copyright (C) 2026 LIghtJUNction; licensed under AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'

import {
  compareOpenUICells,
  isBoundedOpenUI,
  OPENUI_MAX_CHARS,
  OPENUI_MAX_LINES,
  resolveOpenUIPage,
} from './policy'

describe('OpenUI display boundary', () => {
  it('accepts a bounded root program', () => {
    assert.equal(isBoundedOpenUI('root = Stack([])'), true)
  })
  it('rejects ordinary text, HTML and non-root programs', () => {
    for (const value of [
      'Hello',
      '<script>bad()</script>',
      'other = Stack([])',
    ]) {
      assert.equal(isBoundedOpenUI(value), false)
    }
  })
  it('limits input before parsing', () => {
    assert.equal(
      isBoundedOpenUI(`root = Stack([])${' '.repeat(OPENUI_MAX_CHARS)}`),
      false
    )
    assert.equal(
      isBoundedOpenUI(`root = Stack([])${'\n'.repeat(OPENUI_MAX_LINES)}`),
      false
    )
  })
  it('resolves only known existing console pages', () => {
    assert.equal(resolveOpenUIPage('usage'), '/usage-logs')
    assert.equal(resolveOpenUIPage('keys'), '/keys')
    assert.equal(resolveOpenUIPage('tools'), '/tool-market')
  })
  it('rejects external URLs, API paths and prototype names', () => {
    for (const value of [
      'javascript:alert(1)',
      'https://bad.example',
      '//bad.example',
      '/api/user/delete',
      '__proto__',
      'constructor',
      null,
      1,
    ]) {
      assert.equal(resolveOpenUIPage(value), null)
    }
  })
  it('sorts numbers numerically without changing the input', () => {
    const values = [12, 2, 0]
    assert.deepEqual([...values].sort(compareOpenUICells), [0, 2, 12])
    assert.deepEqual(values, [12, 2, 0])
    assert.ok(compareOpenUICells(undefined, 1) > 0)
  })
})
