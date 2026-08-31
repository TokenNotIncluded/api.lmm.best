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

import {
  deletePlan,
  executeSubscriptionReset,
  getAdminPlans,
  getAdminSubscriptionRecords,
  getSubscriptionResetEligible,
  getSubscriptionResetVouchers,
  previewSubscriptionReset,
  redeemSubscriptionResetVoucher,
  restorePlan,
} from './api.js'
import type {
  SubscriptionResetExecuteRequest,
  SubscriptionResetPreviewRequest,
} from './types.js'

const originalDelete = api.delete
const originalGet = api.get
const originalPost = api.post

afterEach(() => {
  api.delete = originalDelete
  api.get = originalGet
  api.post = originalPost
})

describe('subscription administration API', () => {
  test('requests archived plans and preserves archive/delete response options', async () => {
    const getCalls: unknown[][] = []
    api.get = (async (...args: unknown[]) => {
      getCalls.push(args)
      return { data: { success: true, data: [] } }
    }) as typeof api.get

    await getAdminPlans(true)
    assert.deepEqual(getCalls, [
      ['/api/subscription/admin/plans', { params: { include_archived: '1' } }],
    ])

    let deleteCall: unknown[] = []
    api.delete = (async (...args: unknown[]) => {
      deleteCall = args
      return { data: { success: true, data: { action: 'archived' } } }
    }) as typeof api.delete
    const deleted = await deletePlan(42)
    assert.deepEqual(deleteCall, [
      '/api/subscription/admin/plans/42',
      { skipBusinessError: true, skipErrorHandler: true },
    ])
    assert.equal(deleted.data?.action, 'archived')

    let restorePath = ''
    api.post = (async (url: string) => {
      restorePath = url
      return { data: { success: true } }
    }) as typeof api.post
    await restorePlan(42)
    assert.equal(restorePath, '/api/subscription/admin/plans/42/restore')
  })

  test('serializes record and reset-target filters without losing cancellation', async () => {
    const calls: unknown[][] = []
    api.get = (async (...args: unknown[]) => {
      calls.push(args)
      return { data: { success: true, data: { items: [], total: 0 } } }
    }) as typeof api.get
    const controller = new AbortController()

    await getAdminSubscriptionRecords(
      {
        page: 3,
        pageSize: 20,
        query: 'alice',
        planId: 7,
        status: 'active',
      },
      controller.signal
    )
    await getSubscriptionResetEligible(
      {
        page: 2,
        pageSize: 50,
        query: 'pro',
        planIds: [7, 9],
        userIds: [11, 13],
      },
      controller.signal
    )

    assert.deepEqual(calls, [
      [
        '/api/subscription/admin/records',
        {
          params: {
            page: 3,
            page_size: 20,
            query: 'alice',
            plan_id: 7,
            status: 'active',
          },
          signal: controller.signal,
        },
      ],
      [
        '/api/subscription/root/reset-targets',
        {
          params: {
            page: 2,
            page_size: 50,
            query: 'pro',
            plan_ids: '7,9',
            user_ids: '11,13',
          },
          signal: controller.signal,
        },
      ],
    ])
  })

  test('uses preview-bound reset and user voucher contracts', async () => {
    const posts: unknown[][] = []
    api.post = (async (...args: unknown[]) => {
      posts.push(args)
      return { data: { success: true, data: {} } }
    }) as typeof api.post
    api.get = (async (url: string) => ({
      data: { success: true, data: url.includes('vouchers') ? [] : {} },
    })) as typeof api.get

    const preview: SubscriptionResetPreviewRequest = {
      mode: 'soft',
      all_matching: false,
      targets: [{ user_id: 11, plan_id: 7 }],
      filter: { query: 'alice', plan_ids: [7] },
    }
    const execute: SubscriptionResetExecuteRequest = {
      preview_token: 'preview-token',
      operation_id: 'operation-id',
    }

    await previewSubscriptionReset(preview)
    await executeSubscriptionReset(execute)
    await getSubscriptionResetVouchers()
    await redeemSubscriptionResetVoucher(99)

    assert.deepEqual(posts, [
      ['/api/subscription/root/reset/preview', preview],
      ['/api/subscription/root/reset', execute],
      ['/api/subscription/self/reset-vouchers/99/redeem'],
    ])
  })
})
