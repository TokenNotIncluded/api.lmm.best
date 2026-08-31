/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { api } from '@/lib/api'

import type {
  ApiResponse,
  PlanRecord,
  PlanPayload,
  SubscriptionPlan,
  UserSubscriptionRecord,
  CreateUserSubscriptionRequest,
  SubscriptionPlanRemovalResult,
  AdminSubscriptionRecordPage,
  AdminSubscriptionResetEligiblePage,
  SubscriptionResetPreviewRequest,
  SubscriptionResetPreviewResult,
  SubscriptionResetExecuteRequest,
  SubscriptionResetBatchResult,
  SubscriptionResetVoucher,
  SubscriptionResetResult,
  SubscriptionPayResponse,
  SubscriptionPayRequest,
  WaffoPancakeSubscriptionPayRequest,
  WaffoPancakeProductType,
  SelfSubscriptionData,
} from './types'

// ============================================================================
// Admin Plan Management
// ============================================================================

export async function getAdminPlans(
  includeArchived = false
): Promise<ApiResponse<PlanRecord[]>> {
  const res = await api.get('/api/subscription/admin/plans', {
    params: includeArchived ? { include_archived: '1' } : undefined,
  })
  return res.data
}

export async function createPlan(
  data: PlanPayload
): Promise<ApiResponse<PlanRecord>> {
  const res = await api.post('/api/subscription/admin/plans', data)
  return res.data
}

export async function updatePlan(
  id: number,
  data: PlanPayload
): Promise<ApiResponse<PlanRecord>> {
  const res = await api.put(`/api/subscription/admin/plans/${id}`, data)
  return res.data
}

export async function deletePlan(
  id: number
): Promise<ApiResponse<SubscriptionPlanRemovalResult>> {
  const res = await api.delete(`/api/subscription/admin/plans/${id}`, {
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  return res.data
}

export async function restorePlan(
  id: number
): Promise<ApiResponse<SubscriptionPlan>> {
  const res = await api.post(`/api/subscription/admin/plans/${id}/restore`)
  return res.data
}

export async function patchPlanStatus(
  id: number,
  enabled: boolean
): Promise<ApiResponse> {
  const res = await api.patch(`/api/subscription/admin/plans/${id}`, {
    enabled,
  })
  return res.data
}

// ============================================================================
// Admin User Subscription Management
// ============================================================================

export async function getUserSubscriptions(
  userId: number
): Promise<ApiResponse<UserSubscriptionRecord[]>> {
  const res = await api.get(
    `/api/subscription/admin/users/${userId}/subscriptions`
  )
  return res.data
}

export async function createUserSubscription(
  userId: number,
  data: CreateUserSubscriptionRequest
): Promise<ApiResponse<{ message?: string }>> {
  const res = await api.post(
    `/api/subscription/admin/users/${userId}/subscriptions`,
    data
  )
  return res.data
}

export async function invalidateUserSubscription(
  subId: number
): Promise<ApiResponse<{ message?: string }>> {
  const res = await api.post(
    `/api/subscription/admin/user_subscriptions/${subId}/invalidate`
  )
  return res.data
}

export async function deleteUserSubscription(
  subId: number
): Promise<ApiResponse> {
  const res = await api.delete(
    `/api/subscription/admin/user_subscriptions/${subId}`
  )
  return res.data
}

export async function getAdminSubscriptionRecords(
  params: {
    page: number
    pageSize: number
    query?: string
    planId?: number
    status?: string
  },
  signal?: AbortSignal
): Promise<ApiResponse<AdminSubscriptionRecordPage>> {
  const res = await api.get('/api/subscription/admin/records', {
    params: {
      page: params.page,
      page_size: params.pageSize,
      query: params.query || undefined,
      plan_id: params.planId || undefined,
      status: params.status || 'all',
    },
    signal,
  })
  return res.data
}

export async function getSubscriptionResetEligible(
  params: {
    page: number
    pageSize: number
    query?: string
    planIds?: number[]
    userIds?: number[]
  },
  signal?: AbortSignal
): Promise<ApiResponse<AdminSubscriptionResetEligiblePage>> {
  const res = await api.get('/api/subscription/root/reset-targets', {
    params: {
      page: params.page,
      page_size: params.pageSize,
      query: params.query || undefined,
      plan_ids: params.planIds?.join(',') || undefined,
      user_ids: params.userIds?.join(',') || undefined,
    },
    signal,
  })
  return res.data
}

export async function previewSubscriptionReset(
  data: SubscriptionResetPreviewRequest
): Promise<ApiResponse<SubscriptionResetPreviewResult>> {
  const res = await api.post('/api/subscription/root/reset/preview', data)
  return res.data
}

export async function executeSubscriptionReset(
  data: SubscriptionResetExecuteRequest
): Promise<ApiResponse<SubscriptionResetBatchResult>> {
  const res = await api.post('/api/subscription/root/reset', data)
  return res.data
}

// ============================================================================
// User-facing Subscription Payment
// ============================================================================

export async function paySubscriptionStripe(
  data: SubscriptionPayRequest
): Promise<SubscriptionPayResponse> {
  const res = await api.post('/api/subscription/stripe/pay', data)
  return res.data
}

export async function paySubscriptionCreem(
  data: SubscriptionPayRequest
): Promise<SubscriptionPayResponse> {
  const res = await api.post('/api/subscription/creem/pay', data)
  return res.data
}

export async function paySubscriptionWaffoPancake(
  data: WaffoPancakeSubscriptionPayRequest
): Promise<SubscriptionPayResponse> {
  const res = await api.post('/api/subscription/waffo-pancake/pay', data)
  return res.data
}

export async function paySubscriptionBalance(
  data: SubscriptionPayRequest
): Promise<SubscriptionPayResponse> {
  const res = await api.post('/api/subscription/balance/pay', data)
  return res.data
}

// Mints the selected Pancake plan product. amount and currency are the plan's
// real ISO-fiat list price; the server converts it to Pancake USD.
export async function createWaffoPancakePlanProduct(data: {
  name: string
  amount: string
  currency: string
  duration_unit: string
  duration_value: number
  product_type: WaffoPancakeProductType
}): Promise<
  ApiResponse<{
    product_id: string
    product_name: string
    store_id: string
    settlement_currency: 'USD'
    settlement_amount: string
    product_type: WaffoPancakeProductType
  }>
> {
  const res = await api.post(
    '/api/option/waffo-pancake/subscription-product',
    data
  )
  return res.data
}

// Returns both one-time and recurring products in the saved Pancake store.
export async function listWaffoPancakePlanProductOptions(): Promise<
  ApiResponse<{
    store_id: string
    products: {
      id: string
      name: string
      status: string
      billingPeriod?: string
      product_type?: WaffoPancakeProductType
    }[]
  }>
> {
  const res = await api.get(
    '/api/option/waffo-pancake/subscription-product-options'
  )
  return res.data
}

export async function paySubscriptionEpay(
  data: SubscriptionPayRequest & { payment_method: string }
): Promise<SubscriptionPayResponse & { url?: string }> {
  const res = await api.post('/api/subscription/epay/pay', data)
  return {
    ...res.data,
    // SAFETY: the legacy EPay interceptor can expose `url` on the response
    // wrapper even though Axios' static type only models the `data` payload.
    url: res.data.url || (res as unknown as { url?: string }).url,
  }
}

// ============================================================================
// User Self Subscriptions
// ============================================================================

export async function getSelfSubscriptions(): Promise<
  ApiResponse<UserSubscriptionRecord[]>
> {
  const res = await api.get('/api/subscription/self')
  return res.data
}

export async function getSelfSubscriptionFull(): Promise<
  ApiResponse<SelfSubscriptionData>
> {
  const res = await api.get('/api/subscription/self')
  return res.data
}

export async function getSubscriptionResetVouchers(): Promise<
  ApiResponse<SubscriptionResetVoucher[]>
> {
  const res = await api.get('/api/subscription/self/reset-vouchers')
  return res.data
}

export async function redeemSubscriptionResetVoucher(
  voucherId: number
): Promise<ApiResponse<SubscriptionResetResult>> {
  const res = await api.post(
    `/api/subscription/self/reset-vouchers/${voucherId}/redeem`
  )
  return res.data
}

export async function getPublicPlans(): Promise<ApiResponse<PlanRecord[]>> {
  const res = await api.get('/api/subscription/plans')
  return res.data
}

export async function updateBillingPreference(
  preference: string
): Promise<ApiResponse<{ billing_preference?: string }>> {
  const res = await api.put('/api/subscription/self/preference', {
    billing_preference: preference,
  })
  return res.data
}

export async function getGroups(): Promise<ApiResponse<string[]>> {
  const res = await api.get('/api/group')
  return res.data
}
