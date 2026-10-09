/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/

export type PublicRelay = {
  id: number
  contributor_email: string
  name: string
  base_url: string
  group: string
  models: string
  description: string
  status: 'pending' | 'approved' | 'rejected'
  created_at: number
  updated_at: number
  review_note?: string
  used_quota?: number
  tip_quota?: number
  tip_count?: number
  withdrawn_quota?: number
  available_tip_quota?: number
  used_quota_usd?: number
  tip_quota_usd?: number
  withdrawn_quota_usd?: number
  channel_id?: number
  rating_average?: number
  rating_count?: number
}

export type PublicRelayReport = {
  id: number
  contribution_id: number
  reporter_user_id: number
  reason: string
  status: 'open' | 'closed'
  created_at: number
}

export type PublicRelayConfig = {
  group: string
  minimum_withdrawal_usd: number
  /** The server's immutable Credit policy; absent metadata disables withdrawal. */
  minimum_withdrawal_quota?: number
  maximum_tip_quota?: number
  maximum_tip_usd?: number
}

export type PublicRelayReview = {
  id: number
  contribution_id: number
  rating: number
  comment: string
  created_at: number
  updated_at: number
}

export type PublicRelayRoutingItem = PublicRelay & {
  channel_id: number
  disabled: boolean
  position: number
}
