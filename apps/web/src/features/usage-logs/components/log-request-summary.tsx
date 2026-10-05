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
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { requestAssistantOpen } from '@/features/assistant/assistant-events'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'
import { formatTokens } from '@/lib/format'

import type { UsageLog } from '../data/schema'
import { logRecovery, safeLogDiagnostic } from '../lib/recovery'
import type { LogOtherData } from '../types'

export function LogRequestSummary({
  log,
  other,
  onClose,
}: {
  log: UsageLog
  other: LogOtherData | null
  onClose: () => void
}) {
  const { t } = useTranslation()
  const { formatQuota } = useWalletCurrency()
  const formatLogQuota = (raw: number) =>
    formatQuota(raw, { digitsLarge: 4, digitsSmall: 8, abbreviate: false })
  const { copyToClipboard } = useCopyToClipboard()
  if (![2, 5, 6].includes(log.type)) return null
  const cacheReadDetails = [
    { label: t('Text cache read tokens'), tokens: other?.cache_text_tokens },
    { label: t('Image cache read tokens'), tokens: other?.cache_image_tokens },
    { label: t('Audio cache read tokens'), tokens: other?.cache_audio_tokens },
  ]
  const formatCacheReadDetail = (tokens: number | null | undefined) =>
    other?.cache_read_details_status === 'reported' &&
    typeof tokens === 'number' &&
    Number.isSafeInteger(tokens) &&
    tokens >= 0
      ? tokens === 0
        ? '0'
        : formatTokens(tokens)
      : t('Not recorded')
  const audioSeconds = other?.audio_seconds
  const audioUsageLabel =
    other?.audio_usage_status === 'reported'
      ? t('Reported')
      : other?.audio_usage_status === 'estimated'
        ? t('Estimated')
        : null
  const hasAudioDuration =
    audioUsageLabel != null &&
    typeof audioSeconds === 'number' &&
    Number.isFinite(audioSeconds) &&
    audioSeconds >= 0
  const failed =
    log.type === 5 ||
    other?.stream_status?.status === 'error' ||
    Boolean(other?.stream_status?.end_error)
  const recovery = logRecovery(other)
  const diagnostic = safeLogDiagnostic(
    JSON.stringify(
      {
        request_id: log.request_id,
        model: log.model_name,
        status_code: other?.status_code,
        error_code: other?.error_code,
        error: failed ? safeLogDiagnostic(log.content) : undefined,
        stream_error: other?.stream_status?.end_error
          ? safeLogDiagnostic(other.stream_status.end_error)
          : undefined,
      },
      null,
      2
    )
  )
  const openAssistant = (target: typeof recovery.target) => {
    onClose()
    requestAssistantOpen(
      target,
      `${t('Help me diagnose this API request.')}\n${diagnostic}`
    )
  }
  return (
    <section className='border-border space-y-3 border-b pb-4'>
      <h3 className='font-semibold'>{t('Request and billing summary')}</h3>
      <dl className='grid grid-cols-2 gap-3 text-sm'>
        <div>
          <dt className='text-muted-foreground'>{t('Input Tokens')}</dt>
          <dd>
            {log.type === 5 && log.prompt_tokens === 0
              ? t('Not recorded')
              : formatTokens(log.prompt_tokens)}
          </dd>
        </div>
        <div>
          <dt className='text-muted-foreground'>{t('Output Tokens')}</dt>
          <dd>
            {log.type === 5 && log.completion_tokens === 0
              ? t('Not recorded')
              : formatTokens(log.completion_tokens)}
          </dd>
        </div>
        <div>
          <dt className='text-muted-foreground'>{t('Cache read tokens')}</dt>
          <dd>
            {other?.cache_tokens == null
              ? t('Not recorded')
              : formatTokens(other.cache_tokens)}
          </dd>
        </div>
        {other?.cache_read_details_status != null &&
          cacheReadDetails.map(({ label, tokens }) => (
            <div key={label}>
              <dt className='text-muted-foreground'>{label}</dt>
              <dd className='tabular-nums'>{formatCacheReadDetail(tokens)}</dd>
            </div>
          ))}
        <div>
          <dt className='text-muted-foreground'>{t('Cache write tokens')}</dt>
          <dd>
            {other?.cache_creation_tokens == null
              ? t('Not recorded')
              : formatTokens(other.cache_creation_tokens)}
          </dd>
        </div>
        {(other?.audio_usage_status != null || audioSeconds != null) && (
          <div>
            <dt className='text-muted-foreground'>
              {t('Audio duration (seconds)')}
              {audioUsageLabel && (
                <span className='text-xs'> ({audioUsageLabel})</span>
              )}
            </dt>
            <dd className='tabular-nums'>
              {hasAudioDuration ? audioSeconds : t('Not recorded')}
            </dd>
          </div>
        )}
        <div>
          <dt className='text-muted-foreground'>
            {t(
              log.type === 6
                ? 'Refund recorded in this entry'
                : other?.billing_source === 'subscription'
                  ? 'Plan quota used in this entry'
                  : 'Charge recorded in this entry'
            )}
          </dt>
          <dd>
            {formatLogQuota(
              other?.billing_source === 'subscription'
                ? (other.subscription_consumed ?? log.quota)
                : log.quota
            )}
          </dd>
        </div>
      </dl>
      <p className='text-muted-foreground text-xs'>
        {t(
          'This entry does not reconcile other charges or refunds for the same request. Final net charge is not available here.'
        )}
      </p>
      {failed && (
        <>
          <p className='text-sm'>{t(recovery.message)}</p>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Only diagnostic metadata is copied; raw error bodies are omitted.'
            )}
          </p>
          <div className='flex flex-wrap gap-2'>
            <Button
              type='button'
              variant='outline'
              onClick={() => openAssistant(recovery.target)}
            >
              {t(recovery.action)}
            </Button>
            <Button
              type='button'
              variant='outline'
              onClick={() => void copyToClipboard(diagnostic)}
            >
              {t('Copy safe error details')}
            </Button>
            <Button
              type='button'
              variant='outline'
              onClick={() => openAssistant('usage')}
            >
              {t('Ask AI assistant')}
            </Button>
          </div>
        </>
      )}
      {log.request_id && (
        <Button
          type='button'
          variant='outline'
          onClick={() => void copyToClipboard(log.request_id)}
        >
          {t('Copy Request ID')}
        </Button>
      )}
    </section>
  )
}
