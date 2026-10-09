/* Copyright (C) 2026 LIghtJUNction; licensed under AGPL-3.0-or-later. */
import { describe, expect, it } from 'bun:test'
import { compareOpenUICells, isBoundedOpenUI, OPENUI_MAX_CHARS, OPENUI_MAX_LINES, resolveOpenUIPage } from './policy'
describe('OpenUI display boundary', () => {
  it('accepts a bounded root program', () => {
    expect(isBoundedOpenUI('root = Stack([])')).toBe(true)
  })
  it('rejects ordinary text, HTML and non-root programs', () => {
    for (const value of ['Hello', '<script>bad()</script>', 'other = Stack([])']) expect(isBoundedOpenUI(value)).toBe(false)
  })
  it('limits input before parsing', () => {
    expect(isBoundedOpenUI('root = Stack([])' + ' '.repeat(OPENUI_MAX_CHARS))).toBe(false)
    expect(isBoundedOpenUI('root = Stack([])' + '\n'.repeat(OPENUI_MAX_LINES))).toBe(false)
  })
  it('resolves only known existing console pages', () => {
    expect(resolveOpenUIPage('usage')).toBe('/usage-logs')
    expect(resolveOpenUIPage('keys')).toBe('/keys')
    expect(resolveOpenUIPage('tools')).toBe('/tool-market')
  })
  it('rejects external URLs, API paths and prototype names', () => {
    for (const value of ['javascript:alert(1)', 'https://bad.example', '//bad.example', '/api/user/delete', '__proto__', 'constructor', null, 1]) expect(resolveOpenUIPage(value)).toBeNull()
  })
  it('sorts numbers numerically without changing the input', () => {
    const values = [12, 2, 0]
    expect([...values].sort(compareOpenUICells)).toEqual([0, 2, 12])
    expect(values).toEqual([12, 2, 0])
    expect(compareOpenUICells(undefined, 1)).toBeGreaterThan(0)
  })
})
