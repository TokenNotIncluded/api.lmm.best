/*
Copyright (C) 2026 LIghtJUNction
*/
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { useStatus } from '@/hooks/use-status'
import { copyToClipboard } from '@/lib/copy-to-clipboard'

import { resolveApiBaseUrl } from '../lib/api-base-url'

export function ApiBaseUrl() {
  const { t } = useTranslation()
  const { status, loading } = useStatus()
  const baseUrl = status
    ? resolveApiBaseUrl(
        status.server_address ?? status.data?.server_address,
        window.location.origin
      )
    : null
  const usageCommand = baseUrl
    ? `curl '${baseUrl.replaceAll("'", "'\\''")}/usage' \\\n  -H 'Authorization: Bearer API_KEY'`
    : ''
  return (
    <section
      className='mb-4 min-w-0 space-y-2 rounded-lg border p-3'
      aria-label={t('Base URL')}
    >
      <p className='text-sm font-medium'>{t('OpenAI-compatible Base URL')}</p>
      <div className='flex min-w-0 flex-wrap items-center gap-2'>
        <code className='min-w-0 flex-1 text-sm break-all'>
          {baseUrl ?? t(loading ? 'Loading...' : 'API address unavailable')}
        </code>
        <Button
          type='button'
          variant='outline'
          className='min-h-11 sm:min-h-8'
          disabled={!baseUrl}
          onClick={async () => {
            if (!baseUrl) return
            if (await copyToClipboard(baseUrl)) {
              toast.success(t('Base URL copied'))
            } else toast.error(t('Failed to copy Base URL'))
          }}
        >
          {t('Copy Base URL')}
        </Button>
      </div>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Includes /v1. Do not append /v1 again in your client. Copy the API key separately.'
        )}
      </p>
      <details className='text-sm'>
        <summary className='min-h-11 cursor-pointer py-2'>
          {t('Query API key quota (read-only)')}
        </summary>
        <div className='space-y-2'>
          <p>
            {t(
              'GET /v1/usage reports this API key only (scope=token, currency=USD), not your account balance or subscription.'
            )}
          </p>
          <pre className='bg-muted overflow-x-auto rounded p-2 text-xs break-all whitespace-pre-wrap'>
            {usageCommand}
          </pre>
          <Button
            type='button'
            variant='outline'
            disabled={!baseUrl}
            onClick={async () => {
              if (await copyToClipboard(usageCommand)) {
                toast.success(t('Copied to clipboard'))
              } else toast.error(t('Failed to copy'))
            }}
          >
            {t('Copy quota query')}
          </Button>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Replace API_KEY locally with your key. This example never includes your real key.'
            )}
          </p>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Response fields: valid, currency, remaining, used_today, used_total, total_quota, unlimited, updated_at. With unlimited=true, remaining and total_quota are null. used_today uses retained UTC logs; it is null when logging is disabled and may be incomplete after log deletion or interruptions.'
            )}
          </p>
          <p className='text-muted-foreground text-xs'>
            {t('Model prices remain available at GET /v1/pricing.')}
          </p>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Limit: 30 queries per minute per account. Poll once per minute; on HTTP 429, respect Retry-After. Quota queries require a valid key but no developer level; pricing uses normal relay authentication.'
            )}
          </p>
        </div>
      </details>
    </section>
  )
}
