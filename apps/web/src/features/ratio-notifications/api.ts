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
import axios from 'axios'
/*
Copyright (C) 2026 LIghtJUNction
*/
import type { TFunction } from 'i18next'

import { api } from '@/lib/api'

export type RatioEvent = {
  event_id: string
  effective_at: number
  changes: Array<{
    option: string
    model?: string
    group?: string
    user_group?: string
    old: unknown
    new: unknown
  }>
}

export async function listRatioNotifications(before = '') {
  const response = await api
    .get<{ success: boolean; data: RatioEvent[]; next?: string }>(
      '/api/ratio-notifications',
      {
        params: before ? { before } : undefined,
        skipBusinessError: true,
        skipErrorHandler: true,
      }
    )
    .catch((error: unknown) => {
      if (
        axios.isAxiosError(error) &&
        [403, 404].includes(error.response?.status ?? 0)
      ) {
        return null
      }
      throw error
    })
  if (!response) return { events: [], next: undefined }
  if (!response.data.success || !Array.isArray(response.data.data)) {
    throw new Error('Ratio notification feed unavailable')
  }
  return { events: response.data.data, next: response.data.next || undefined }
}

export function ratioAnnouncement(
  event: RatioEvent,
  userId: number,
  t: TFunction
) {
  return {
    id: `ratio:${userId}:${event.event_id}`,
    type: 'info',
    plainText: true,
    publishDate: new Date(event.effective_at * 1000).toISOString(),
    content: `${t('Rate changes')}\n${event.changes
      .map((change) => {
        const target = [
          change.option,
          change.model,
          change.group,
          change.user_group,
        ]
          .filter(Boolean)
          .join(' · ')
        const value = (input: unknown) =>
          input === null ? t('Default') : JSON.stringify(input)
        return `${target}\n${t('Old value')}: ${value(change.old)} → ${t('New value')}: ${value(change.new)}`
      })
      .join('\n')}`,
    extra: t('Effective at {{time}}', {
      time: new Date(event.effective_at * 1000).toISOString(),
    }),
  }
}
