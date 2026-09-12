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
      )
        {return null}
      throw error
    })
  if (!response) return { events: [], next: undefined }
  if (!response.data.success || !Array.isArray(response.data.data))
    {throw new Error('Ratio notification feed unavailable')}
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
