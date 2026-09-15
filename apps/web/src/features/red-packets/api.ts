import { api } from '@/lib/api'

import type {
  ApiResponse,
  RedPacket,
  RedPacketMutation,
  RedPacketPublic,
  RedPacketReward,
} from './types'

export async function listRedPackets(): Promise<ApiResponse<RedPacket[]>> {
  const response = await api.get('/api/red-packet/admin')
  return response.data
}

export async function createRedPacket(
  input: RedPacketMutation
): Promise<ApiResponse<RedPacket>> {
  const response = await api.post('/api/red-packet/admin', input)
  return response.data
}

export async function updateRedPacket(
  id: number,
  input: RedPacketMutation
): Promise<ApiResponse<null>> {
  const response = await api.put(`/api/red-packet/admin/${id}`, input)
  return response.data
}

export async function deleteRedPacket(id: number): Promise<ApiResponse<null>> {
  const response = await api.delete(`/api/red-packet/admin/${id}`)
  return response.data
}

export async function getRedPacket(
  slug: string
): Promise<ApiResponse<RedPacketPublic>> {
  const response = await api.get(
    `/api/red-packet/public/${encodeURIComponent(slug)}`,
    { skipBusinessError: true } as Record<string, unknown>
  )
  return response.data
}

export async function claimRedPacket(
  slug: string
): Promise<ApiResponse<RedPacketReward>> {
  const response = await api.post(
    `/api/red-packet/public/${encodeURIComponent(slug)}/claim`,
    {}
  )
  return response.data
}

export async function getMyRedPacketClaims(
  slug: string
): Promise<ApiResponse<RedPacketReward[]>> {
  const response = await api.get(
    `/api/red-packet/public/${encodeURIComponent(slug)}/claims/me`,
    { skipBusinessError: true, skipErrorHandler: true }
  )
  return response.data
}
