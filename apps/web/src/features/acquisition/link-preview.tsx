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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'

type Preview = {
  link_id: string
  target: string
  source: string
  medium: string
  campaign: string
  content: string
  evidence: string
  referrer_host: string
  excluded: boolean
  archived: boolean
}

export function AcquisitionLinkPreview({ id }: { id: string }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const preview = useQuery({
    queryKey: ['acquisition-link-preview', id],
    enabled: open,
    staleTime: 0,
    queryFn: async () => {
      const response = await api.get(
        `/api/admin/acquisition/links/${encodeURIComponent(id)}/preview`
      )
      if (!response.data.success) throw new Error('Preview unavailable')
      return response.data.data as Preview
    },
  })
  const value = preview.data
  const testURL = value ? new URL(value.target, window.location.origin) : null
  if (testURL && value) {
    testURL.searchParams.set('lmm_source', value.link_id)
    testURL.searchParams.set('source_test', '1')
    for (const [key, entry] of Object.entries({
      utm_source: value.source,
      utm_medium: value.medium,
      utm_campaign: value.campaign,
      utm_content: value.content,
    })) {
      if (entry) testURL.searchParams.set(key, entry)
    }
  }
  return (
    <>
      <Button variant='outline' onClick={() => setOpen(true)}>
        {t('Test link')}
      </Button>
      <Dialog
        open={open}
        onOpenChange={setOpen}
        title={t('Promotion link preview')}
        description={t('Preview source recognition without recording a visit.')}
      >
        {preview.isPending ? (
          <p>{t('Loading')}</p>
        ) : preview.isError ? (
          <div className='space-y-3'>
            <p>{t('Unable to preview this promotion link.')}</p>
            <Button onClick={() => void preview.refetch()}>{t('Retry')}</Button>
          </div>
        ) : (
          value && (
            <div className='space-y-4'>
              <dl className='grid grid-cols-2 gap-3 text-sm'>
                {[
                  ['Target page', value.target],
                  ['Source platform', value.source],
                  ['Promotion method', value.medium],
                  ['Campaign', value.campaign],
                  ['Content', value.content],
                ].map(([label, text]) => (
                  <div key={label}>
                    <dt className='text-muted-foreground'>{t(label)}</dt>
                    <dd className='break-words'>{text || t('Not provided')}</dd>
                  </div>
                ))}
              </dl>
              <p className='text-sm'>
                {t('Recognition basis: saved promotion link identifier.')}
              </p>
              <p className='text-muted-foreground text-sm'>
                {t(
                  'No external referrer was observed in this preview. A tagged link does not prove where someone saw it.'
                )}
              </p>
              <p className='text-sm'>
                {t(
                  'Preview requests and test links are excluded from acquisition statistics.'
                )}
              </p>
              {value.archived && <p className='text-sm'>{t('Archived')}</p>}
              {testURL && value.excluded && (
                <Button
                  variant='outline'
                  render={
                    <a href={testURL.href} target='_blank' rel='noreferrer' />
                  }
                >
                  {t('Open target in test mode')}
                </Button>
              )}
            </div>
          )
        )}
      </Dialog>
    </>
  )
}
