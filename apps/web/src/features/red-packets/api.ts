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
/*
Copyright (C) 2026 LIghtJUNction
*/
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
