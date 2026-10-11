/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  PAGE_SIZE_OPTIONS,
  parsePageSize,
  resetInlinePaginationScroll,
} from './pagination-state.ts'

test('page size accepts only the existing choices, never a cleared selection', () => {
  for (const size of PAGE_SIZE_OPTIONS) {
    assert.equal(parsePageSize(String(size)), size)
  }
  for (const value of [
    null,
    '',
    '0',
    '-1',
    'NaN',
    '1e2',
    '20.5',
    '999',
    ' 20 ',
  ]) {
    assert.equal(parsePageSize(value), null)
  }
})

test('unchanged pagination leaves reading position untouched', () => {
  const control = {
    closest: () => assert.fail('A refresh must not reset scroll'),
  } as unknown as HTMLElement
  resetInlinePaginationScroll(
    control,
    { pageIndex: 2, pageSize: 20 },
    { pageIndex: 2, pageSize: 20 }
  )
})

test('page changes and page-size changes reset only the containing scroller', () => {
  const calls: ScrollToOptions[] = []
  const control = {
    closest: (selector: string) => {
      assert.equal(selector, '[data-mobile-scroll-root]')
      return { scrollTo: (options: ScrollToOptions) => calls.push(options) }
    },
  } as unknown as HTMLElement
  resetInlinePaginationScroll(
    control,
    { pageIndex: 0, pageSize: 20 },
    { pageIndex: 1, pageSize: 20 }
  )
  resetInlinePaginationScroll(
    control,
    { pageIndex: 0, pageSize: 20 },
    { pageIndex: 0, pageSize: 100 }
  )
  assert.deepEqual(calls, [
    { top: 0, behavior: 'instant' },
    { top: 0, behavior: 'instant' },
  ])
})

test('desktop footer and unmounted pagination do not move another scroller', () => {
  const control = { closest: () => null } as unknown as HTMLElement
  const before = { pageIndex: 0, pageSize: 20 }
  const after = { pageIndex: 1, pageSize: 20 }
  assert.doesNotThrow(() => resetInlinePaginationScroll(control, before, after))
  assert.doesNotThrow(() => resetInlinePaginationScroll(null, before, after))
})
