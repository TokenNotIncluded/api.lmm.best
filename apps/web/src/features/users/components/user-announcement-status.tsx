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
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import type { MandatoryAnnouncement } from '@/features/onboarding/mandatory-announcements'
import { api } from '@/lib/api'
import { formatDateTimeObject } from '@/lib/time'

export function UserAnnouncementStatus({ userID }: { userID: number }) {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: ['user-announcements', userID],
    queryFn: async () => {
      const response = await api.get(`/api/user/${userID}/announcements`, {
        skipBusinessError: true,
        skipErrorHandler: true,
      })
      if (!response.data.success || !Array.isArray(response.data.data))
        {throw new Error('Unable to load announcements')}
      return response.data.data as MandatoryAnnouncement[]
    },
    retry: false,
  })
  return (
    <section className='space-y-3'>
      <h3 className='font-medium'>{t('Announcement reading status')}</h3>
      {query.isPending ? (
        <p>{t('Loading')}</p>
      ) : query.isError ? (
        <Button
          type='button'
          variant='outline'
          onClick={() => void query.refetch()}
        >
          {t('Retry')}
        </Button>
      ) : query.data.length === 0 ? (
        <p>{t('No required announcements')}</p>
      ) : (
        <ul className='space-y-3'>
          {query.data.map((item) => (
            <li key={item.revision} className='space-y-1 border-b pb-3'>
              <p className='line-clamp-2 break-words'>{item.content}</p>
              <p className='text-muted-foreground text-sm'>
                {item.read_at
                  ? formatDateTimeObject(new Date(item.read_at * 1000))
                  : t('Not read')}
              </p>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
