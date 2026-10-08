/*
Copyright (C) 2026 LIghtJUNction
*/
import { api } from '@/lib/api'

export interface ServiceTierPolicy {
  enabled: boolean
  fast_markup: number
  ultrafast_markup: number
  fast_groups: string[]
  ultrafast_groups: string[]
}
export interface ServiceTierState {
  policy: ServiceTierPolicy
  catalog: {
    source: string
    fetched_at: string
    sha256: string
    models: Record<string, unknown> | null
  }
  fresh: boolean
  max_age_hours: number
  groups: Record<string, number>
}
type Result = { success: boolean; message?: string; data: ServiceTierState }
const endpoint = '/api/ratio_sync/service_tiers'
function unwrap(result: Result) {
  if (!result.success || !result.data)
    throw new Error(result.message || 'Service-tier pricing is unavailable')
  return result.data
}
export async function getServiceTierPricing(): Promise<ServiceTierState> {
  return unwrap((await api.get<Result>(endpoint)).data)
}
export async function saveServiceTierPricing(policy: ServiceTierPolicy) {
  return unwrap((await api.put<Result>(endpoint, policy)).data)
}
export async function syncServiceTierPricing() {
  return unwrap((await api.post<Result>(`${endpoint}/sync`)).data)
}
