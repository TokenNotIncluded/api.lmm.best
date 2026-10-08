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
  ConfirmPaymentComplianceResponse,
  FetchUpstreamRatiosRequest,
  LogCleanupTask,
  SystemOptionsResponse,
  SystemTaskListResponse,
  SystemTaskResponse,
  UpdateOptionRequest,
  UpdateOptionResponse,
  UsdExchangeRateResponse,
  UpstreamChannelsResponse,
  UpstreamRatiosResponse,
} from './types'

type SettingsRequestOptions = { silent?: boolean }

function settingsRequestConfig(options?: SettingsRequestOptions) {
  return options?.silent
    ? { skipBusinessError: true, skipErrorHandler: true }
    : undefined
}

export function getSystemOptions(
  options: SettingsRequestOptions
): Promise<SystemOptionsResponse>
export function getSystemOptions(): Promise<SystemOptionsResponse>
export async function getSystemOptions(
  options?: SettingsRequestOptions
): Promise<SystemOptionsResponse> {
  const res = await api.get<SystemOptionsResponse>(
    '/api/option/',
    settingsRequestConfig(options)
  )
  if (res.data.success !== true || !Array.isArray(res.data.data)) {
    throw Object.assign(
      new Error(res.data.message || 'Failed to load settings'),
      {
        response: { data: res.data },
      }
    )
  }
  return res.data
}

export async function getSystemGroups() {
  const res = await api.get<{
    success: boolean
    message?: string
    data: string[]
  }>('/api/group/')
  return res.data
}

export async function updateSystemOption(
  request: UpdateOptionRequest,
  options?: SettingsRequestOptions
) {
  const res = await api.put<UpdateOptionResponse>(
    '/api/option/',
    request,
    settingsRequestConfig(options)
  )
  return res.data
}

export async function getUsdExchangeRate(currency: string) {
  const code = currency.trim().toUpperCase()
  if (!/^[A-Z]{3}$/.test(code)) {
    throw new Error('Currency must be a three-letter ISO 4217 code')
  }

  const res = await api.get<UsdExchangeRateResponse>(
    `/api/option/exchange-rate?currency=${encodeURIComponent(code)}`,
    { skipErrorHandler: true }
  )
  return res.data
}

export async function validateSystemOptions(values: Record<string, string>) {
  const res = await api.post<UpdateOptionResponse>('/api/option/validate', {
    values,
  })
  return res.data
}

export async function updateSystemOptions(
  values: Record<string, string>,
  options?: SettingsRequestOptions
) {
  const res = await api.post<UpdateOptionResponse>(
    '/api/option/bulk',
    {
      values,
    },
    settingsRequestConfig(options)
  )
  return res.data
}

export async function confirmPaymentCompliance() {
  const res = await api.post<ConfirmPaymentComplianceResponse>(
    '/api/option/payment_compliance',
    { confirmed: true }
  )
  return res.data
}

export async function startLogCleanupTask(targetTimestamp: number) {
  const res = await api.post<SystemTaskResponse<LogCleanupTask>>(
    '/api/system-task/log-cleanup',
    null,
    {
      params: { target_timestamp: targetTimestamp },
    }
  )
  return res.data
}

export async function getCurrentLogCleanupTask() {
  const res = await api.get<SystemTaskResponse<LogCleanupTask | null>>(
    '/api/system-task/current',
    {
      params: { type: 'log_cleanup' },
    }
  )
  return res.data
}

export async function getSystemTask<TTask = LogCleanupTask>(taskId: string) {
  const res = await api.get<SystemTaskResponse<TTask>>(
    `/api/system-task/${taskId}`
  )
  return res.data
}

export async function listSystemTasks(limit = 20) {
  const res = await api.get<SystemTaskListResponse>('/api/system-task/list', {
    params: { limit },
  })
  return res.data
}

export async function resetModelRatios() {
  const res = await api.post<UpdateOptionResponse>(
    '/api/option/rest_model_ratio'
  )
  return res.data
}

export async function getUpstreamChannels() {
  const res = await api.get<UpstreamChannelsResponse>(
    '/api/ratio_sync/channels'
  )
  return res.data
}

export async function fetchUpstreamRatios(
  request: FetchUpstreamRatiosRequest,
  options: { silent?: boolean } = {}
) {
  const res = await api.post<UpstreamRatiosResponse>(
    '/api/ratio_sync/fetch',
    request,
    options.silent
      ? { skipBusinessError: true, skipErrorHandler: true }
      : undefined
  )
  return res.data
}
