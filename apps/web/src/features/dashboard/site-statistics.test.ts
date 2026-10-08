/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { formatSiteCredits, formatSitePaymentMicros } from './site-statistics'

test('site credit totals remain exact above the browser and SQL integer ranges', () => {
  assert.equal(
    formatSiteCredits('9277415232383220730', 'en'),
    '9,277,415,232,383,220,730'
  )
  assert.equal(
    formatSiteCredits('-9007199254740993', 'en'),
    '-9,007,199,254,740,993'
  )
})

test('native payment micros preserve six decimal places without floating rounding', () => {
  assert.equal(formatSitePaymentMicros('749999', 'en'), '0.749999')
  assert.equal(formatSitePaymentMicros('1000000', 'en'), '1.00')
  assert.equal(formatSitePaymentMicros('1000001', 'de'), '1,000001')
  assert.equal(
    formatSitePaymentMicros('9277415232383220730', 'en'),
    '9,277,415,232,383.22073'
  )
})
