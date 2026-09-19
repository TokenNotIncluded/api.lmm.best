/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'

import type { AcquisitionRange } from './report-range'

type Visitors = {
  available_from: number
  coverage_complete: boolean
  observed_visitors: number
  channels: { source: string; visitors: number }[]
}
export function AcquisitionVisitorPanel({ from, to }: AcquisitionRange) {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: ['acquisition-visitors', from, to],
    retry: false,
    queryFn: async () => {
      const response = await api.get(
        `/api/admin/acquisition/visitors?from=${from}&to=${to}`
      )
      if (!response.data.success) throw new Error('Visitor data unavailable')
      return response.data.data as Visitors
    },
  })
  return (
    <section className='space-y-3 border-t pt-6'>
      <h2 className='text-lg font-semibold'>
        {t('Identifiable browser visitors')}
      </h2>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Counts cover consenting browser identifiers, not people. One browser can appear in several channels, so channel counts must not be added together.'
        )}
      </p>
      {query.isError ? (
        <Button onClick={() => void query.refetch()}>
          {t('Reload report')}
        </Button>
      ) : query.isPending ? (
        <p>{t('Loading')}</p>
      ) : (
        <>
          {!query.data.coverage_complete && (
            <p role='status'>
              {t(
                'Visitor records do not cover this entire period. Counts below include only retained observations.'
              )}
            </p>
          )}
          <p>
            {t('Observed browser identifiers')}: {query.data.observed_visitors}
          </p>
          <p className='text-muted-foreground text-xs'>
            {t('Records available from')}:{' '}
            {new Date(query.data.available_from * 1000).toISOString()}
          </p>
          <ul className='divide-y'>
            {query.data.channels.map((row) => (
              <li
                key={row.source}
                className='flex justify-between gap-3 py-2 text-sm'
              >
                <span>
                  {row.source === 'unknown'
                    ? t('Direct / unknown source')
                    : row.source}
                </span>
                <span>{row.visitors}</span>
              </li>
            ))}
          </ul>
          {query.data.channels.length === 0 && (
            <p>
              {t(
                'No visitor observations in the retained part of this period.'
              )}
            </p>
          )}
        </>
      )}
    </section>
  )
}
