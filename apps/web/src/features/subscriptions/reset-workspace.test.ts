/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { describe, test } from 'node:test'

const source = readFileSync(
  new URL('./reset-workspace.tsx', import.meta.url),
  'utf8'
)
const recordSource = readFileSync(
  new URL('./components/subscription-records.tsx', import.meta.url),
  'utf8'
)
const typeSource = readFileSync(new URL('./types.ts', import.meta.url), 'utf8')
const routeSource = readFileSync(
  new URL(
    '../../routes/_authenticated/subscriptions/reset.tsx',
    import.meta.url
  ),
  'utf8'
)
const voucherSource = readFileSync(
  new URL(
    '../wallet/components/subscription-reset-vouchers.tsx',
    import.meta.url
  ),
  'utf8'
)

describe('subscription reset workspace safety contract', () => {
  test('guards the dedicated route with the root role', () => {
    assert.match(routeSource, /auth\.user\.role < ROLE\.SUPER_ADMIN/)
    assert.match(routeSource, /throw redirect\({[\s\S]{0,100}to: '\/403'/)
  })

  test('rejects late preview responses and clears prior approval before refresh', () => {
    assert.match(source, /const requestId = \+\+previewRequestId\.current/)
    assert.match(source, /requestId !== previewRequestId\.current/)
    assert.match(
      source,
      /setPreviewing\(true\)[\s\S]{0,180}setPreview\(null\)[\s\S]{0,80}setOperationId\(''\)/
    )
  })

  test('keeps stale target rows and bulk selection non-interactive', () => {
    assert.match(
      source,
      /const filtersSettled =[\s\S]{0,160}!eligibleQuery\.isFetching/
    )
    assert.match(
      source,
      /disabled={[\s\S]{0,100}!filtersSettled[\s\S]{0,100}allMatching/
    )
    assert.match(source, /const canPreview =[\s\S]{0,80}filtersSettled/)
  })

  test('applies explicit user filters to eligibility and frozen previews', () => {
    assert.match(source, /const userFilter = useMemo\(/)
    assert.match(source, /userIds: userFilter\.ids/)
    assert.match(source, /user_ids: userFilter\.ids\.length/)
    assert.match(source, /enabled: !userFilter\.invalid/)
    assert.match(source, /aria-invalid={userFilter\.invalid}/)
  })

  test('discloses banked resets in selection and preview', () => {
    assert.match(source, /item\.banked_voucher_count/)
    assert.match(source, /target\.banked_voucher_count/)
    assert.match(typeSource, /banked_voucher_count: number/)
  })

  test('locally disables a redeemed voucher before background refresh completes', () => {
    assert.match(voucherSource, /redeemedVoucherIds\.has\(voucher\.id\)/)
    assert.match(voucherSource, /next\.add\(selected\.id\)/)
    assert.match(voucherSource, /voucher\.expired === true/)
    assert.match(voucherSource, /void vouchersQuery\.refetch\(\)/)
  })

  test('renders the backend subscription id field', () => {
    assert.match(
      typeSource,
      /interface AdminSubscriptionRecord {[\s\S]*\n\s+id: number/
    )
    assert.match(recordSource, /key={record\.id}/)
    assert.doesNotMatch(recordSource, /record\.subscription_id/)
  })
})
