/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
import { requestAssistantOpen } from '@/features/assistant/assistant-events'
import { safeLogDiagnostic } from '@/features/usage-logs/lib/recovery'
import { useAuthStore } from '@/stores/auth-store'

import { latestRequestStatus, loadLatestRequest } from './latest-request'

export function LatestRequestCard() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const session = useAuthStore((state) => state.auth.session?.sid)
  const query = useQuery({
    queryKey: ['latest-user-request', user?.id, session],
    queryFn: loadLatestRequest,
    enabled: user?.developer_access_granted === true,
    retry: false,
    staleTime: 30_000,
    refetchInterval: 60_000,
  })
  if (user?.developer_access_granted !== true) return null
  const log = query.data
  const status = log ? latestRequestStatus(log) : null
  return (
    <section
      className='space-y-3 rounded-md border p-4'
      aria-label={t('Latest API request')}
    >
      <h2 className='text-base font-semibold'>{t('Latest API request')}</h2>
      {query.isError ? (
        <div className='space-y-2'>
          <p>{t('Unable to load the latest request.')}</p>
          <Button variant='outline' onClick={() => void query.refetch()}>
            {t('Retry')}
          </Button>
        </div>
      ) : query.isPending ? (
        <p>{t('Loading')}</p>
      ) : !log ? (
        <p className='text-muted-foreground text-sm'>
          {t(
            'Your first API request will appear here. No request record is available yet.'
          )}
        </p>
      ) : (
        <>
          <dl className='grid grid-cols-2 gap-3 text-sm'>
            <div>
              <dt className='text-muted-foreground'>{t('Model')}</dt>
              <dd className='break-all'>{log.model_name || t('Unknown')}</dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>{t('Status')}</dt>
              <dd>
                {status === 'failed'
                  ? t('Failed')
                  : status === 'successful'
                    ? t('Successful API response observed')
                    : t(
                        'Usage recorded; check details for the request outcome.'
                      )}
              </dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>{t('API Key')}</dt>
              <dd className='break-words'>
                {log.token_name || t('Not recorded')}
              </dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>{t('Time')}</dt>
              <dd>{new Date(log.created_at * 1000).toLocaleString()}</dd>
            </div>
          </dl>
          {log.request_id && (
            <div className='bg-muted flex items-center justify-between gap-2 rounded-md p-2 text-xs'>
              <span className='break-all'>Request ID: {log.request_id}</span>
              <CopyButton value={log.request_id} className='size-11 shrink-0' />
            </div>
          )}
          <div className='flex flex-wrap gap-2'>
            <Button
              variant='outline'
              render={
                <a
                  href={`/usage-logs/common${log.request_id ? `?request_id=${encodeURIComponent(log.request_id)}` : ''}`}
                />
              }
            >
              {t('View details')}
            </Button>
            {status === 'failed' && (
              <Button
                onClick={() =>
                  requestAssistantOpen(
                    'usage',
                    `${t('Help me diagnose this API request.')}\n${safeLogDiagnostic(JSON.stringify({ request_id: log.request_id, model: log.model_name }))}`
                  )
                }
              >
                {t('Ask AI assistant')}
              </Button>
            )}
          </div>
        </>
      )}
    </section>
  )
}
