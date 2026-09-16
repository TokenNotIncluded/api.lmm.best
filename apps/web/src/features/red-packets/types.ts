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
export type RedPacketDrawMode = 'random' | 'weighted' | 'sequence'
export type RedPacketItemType = 'redemption' | 'discount'

export interface RedPacketItemInput {
  item_type: RedPacketItemType
  source_id: number
  weight: number
}

export interface RedPacket {
  id: number
  slug: string
  title: string
  description: string
  cover_image: string
  cover_prompt: string
  draw_mode: RedPacketDrawMode
  per_user_limit: number
  start_at: number
  end_at: number
  enabled: boolean
  created_by: number
  created_at: number
  updated_at: number
  total_items: number
  remaining_items: number
  claim_count: number
}

export interface RedPacketPublic {
  slug: string
  title: string
  description: string
  cover_image: string
  draw_mode: RedPacketDrawMode
  per_user_limit: number
  start_at: number
  end_at: number
  enabled: boolean
  total_items: number
  remaining_items: number
  claim_count: number
}

export interface RedPacketReward {
  claim_id: number
  item_type: RedPacketItemType
  code: string
  name: string
  claimed_at: number
  reward_type?: 'quota' | 'reset_voucher'
  quota?: number
  reset_plan_id?: number
  reset_voucher_expires_at?: number
  discount_percent?: number
  min_amount?: number
  expires_at?: number
}

export interface RedPacketMutation {
  title: string
  description: string
  cover_image: string
  cover_prompt: string
  draw_mode: RedPacketDrawMode
  per_user_limit: number
  start_at: number
  end_at: number
  enabled: boolean
  items: RedPacketItemInput[]
}

export interface ApiResponse<T> {
  success: boolean
  data?: T
  message?: string
}
