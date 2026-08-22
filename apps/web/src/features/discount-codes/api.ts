/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/

import { api } from '@/lib/api'

import type {
  DiscountCode,
  DiscountCodeBatchInput,
  DiscountCodeInput,
  DiscountCodePage,
  DiscountCodeResponse,
} from './types.js'

export async function listDiscountCodes(params: {
  page: number
  pageSize: number
  keyword?: string
  status?: string
}): Promise<DiscountCodeResponse<DiscountCodePage>> {
  const query = new URLSearchParams({
    p: String(params.page),
    page_size: String(params.pageSize),
  })
  if (params.keyword?.trim()) query.set('keyword', params.keyword.trim())
  if (params.status) query.set('status', params.status)
  const path =
    params.keyword?.trim() || params.status
      ? '/api/discount-code/search'
      : '/api/discount-code/'
  const response = await api.get(`${path}?${query.toString()}`)
  return response.data
}

export async function createDiscountCode(
  input: DiscountCodeInput
): Promise<DiscountCodeResponse<DiscountCode>> {
  const response = await api.post('/api/discount-code/', input)
  return response.data
}

export async function createDiscountCodes(
  input: DiscountCodeBatchInput
): Promise<DiscountCodeResponse<DiscountCode[]>> {
  const response = await api.post('/api/discount-code/batch', input)
  return response.data
}

export async function updateDiscountCode(
  input: DiscountCodeInput & { id: number }
): Promise<DiscountCodeResponse<DiscountCode>> {
  const response = await api.put('/api/discount-code/', input)
  return response.data
}

export async function updateDiscountCodeStatus(
  id: number,
  status: number
): Promise<DiscountCodeResponse<DiscountCode>> {
  const response = await api.put('/api/discount-code/?status_only=true', {
    id,
    status,
  })
  return response.data
}

export async function deleteDiscountCode(
  id: number
): Promise<DiscountCodeResponse<null>> {
  const response = await api.delete(`/api/discount-code/${id}`)
  return response.data
}

export async function deleteExhaustedDiscountCodes(): Promise<
  DiscountCodeResponse<{ count: number }>
> {
  const response = await api.delete('/api/discount-code/exhausted')
  return response.data
}
