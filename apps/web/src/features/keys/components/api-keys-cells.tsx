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
import type { PopoverRootProps } from '@base-ui/react/popover'
import { Check, Copy, Loader2 } from 'lucide-react'
import { useState, useCallback, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { BadgeCell } from '@/components/data-table'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { copyToClipboard } from '@/lib/copy-to-clipboard'
import { formatQuota } from '@/lib/format'

import type { ApiKey } from '../types'
import { useApiKeys } from './api-keys-provider'

type PopoverOpenChangeDetails = Parameters<
  NonNullable<PopoverRootProps['onOpenChange']>
>[1]

export function ApiKeyCell({ apiKey }: { apiKey: ApiKey }) {
  const { t } = useTranslation()
  const {
    resolveRealKey,
    resolvedKeys,
    loadingKeys,
    revealOpenKeyId,
    setRevealOpenKeyId,
    copiedKeyId,
    markKeyCopied,
  } = useApiKeys()
  const popoverOpen = revealOpenKeyId === apiKey.id
  const [revealStatus, setRevealStatus] = useState<
    'idle' | 'pending' | 'failed'
  >('idle')
  const revealRequestedRef = useRef<number | null>(null)
  const triggerRef = useRef<HTMLButtonElement | null>(null)
  const revealedInputRef = useRef<HTMLInputElement>(null)

  const isLoading = !!loadingKeys[apiKey.id]
  const resolvedFullKey = resolvedKeys[apiKey.id]
  const isCopied = copiedKeyId === apiKey.id
  const maskedKey = `sk-${apiKey.key}`
  const showRevealLoading =
    popoverOpen && !resolvedFullKey && revealStatus === 'pending'
  const showRevealError =
    popoverOpen && !resolvedFullKey && revealStatus === 'failed'
  const revealStatusId = `api-key-reveal-status-${apiKey.id}`

  const handlePopoverOpen = useCallback(
    (open: boolean, eventDetails: PopoverOpenChangeDetails) => {
      if (
        !open &&
        eventDetails.reason === 'focus-out' &&
        revealRequestedRef.current === apiKey.id &&
        !resolvedFullKey &&
        revealStatus !== 'failed'
      ) {
        eventDetails.cancel()
        return
      }

      setRevealOpenKeyId(open ? apiKey.id : null)
      if (!open) {
        revealRequestedRef.current = null
        setRevealStatus('idle')
      }
    },
    [apiKey.id, resolvedFullKey, revealStatus, setRevealOpenKeyId]
  )

  const handleRevealTriggerClick = useCallback(() => {
    if (popoverOpen) {
      return
    }

    setRevealOpenKeyId(apiKey.id)

    if (resolvedFullKey || revealRequestedRef.current === apiKey.id) {
      return
    }

    revealRequestedRef.current = apiKey.id
    setRevealStatus('pending')
    void resolveRealKey(apiKey.id).then((realKey) => {
      if (revealRequestedRef.current !== apiKey.id || resolvedFullKey) return
      if (!realKey) {
        revealRequestedRef.current = null
        setRevealStatus('failed')
      }
    })
  }, [
    apiKey.id,
    popoverOpen,
    resolveRealKey,
    resolvedFullKey,
    setRevealOpenKeyId,
  ])

  useEffect(() => {
    setRevealStatus('idle')
    revealRequestedRef.current = null
  }, [apiKey.id])

  useEffect(() => {
    if (!popoverOpen || !resolvedFullKey) return
    setRevealStatus('idle')

    const activeElement = document.activeElement
    const input = revealedInputRef.current
    const trigger = triggerRef.current
    const content = input?.closest('[data-slot="popover-content"]')
    const focusWithinTrigger =
      activeElement instanceof Node &&
      trigger instanceof Node &&
      trigger.contains(activeElement)
    const focusWithinContent =
      activeElement instanceof Node &&
      content instanceof Node &&
      content.contains(activeElement)
    if (!input || (!focusWithinTrigger && !focusWithinContent)) return

    input.focus({ preventScroll: true })
    input.select()
  }, [apiKey.id, popoverOpen, resolvedFullKey])

  const handleCopy = useCallback(async () => {
    const realKey = resolvedFullKey || (await resolveRealKey(apiKey.id))
    if (!realKey) return

    const ok = await copyToClipboard(realKey)
    if (ok) markKeyCopied(apiKey.id)
    else toast.error(t('Failed to copy to clipboard'))
  }, [resolvedFullKey, resolveRealKey, apiKey.id, markKeyCopied, t])

  let copyIcon = <Copy className='size-3.5' />
  let copyTooltip = t('Copy API key')
  if (isLoading) {
    copyIcon = <Loader2 className='size-3.5 animate-spin' />
    copyTooltip = t('Loading...')
  } else if (isCopied) {
    copyIcon = <Check className='console-status-success-icon size-3.5' />
    copyTooltip = t('Copied!')
  }

  return (
    <div className='flex max-w-full min-w-0 items-center'>
      <Popover open={popoverOpen} onOpenChange={handlePopoverOpen}>
        <PopoverTrigger
          onClick={handleRevealTriggerClick}
          ref={triggerRef}
          render={
            <Button
              variant='ghost'
              size='sm'
              className='text-muted-foreground h-7 max-w-full min-w-0 justify-start truncate px-0 font-mono text-xs hover:bg-transparent aria-expanded:bg-transparent'
            />
          }
        >
          <span className='truncate'>{maskedKey}</span>
        </PopoverTrigger>
        <PopoverContent
          className='w-auto max-w-[min(90vw,28rem)]'
          align='start'
          initialFocus={false}
        >
          <div className='space-y-2'>
            <p className='text-muted-foreground text-xs'>{t('Full API Key')}</p>
            <input
              ref={revealedInputRef}
              readOnly
              aria-label={t('Full API Key')}
              aria-busy={showRevealLoading || undefined}
              aria-describedby={
                showRevealLoading || showRevealError
                  ? revealStatusId
                  : undefined
              }
              aria-invalid={showRevealError || undefined}
              value={resolvedFullKey ?? ''}
              onFocus={(e) => {
                if (resolvedFullKey) e.target.select()
              }}
              className='bg-muted/50 w-full min-w-[280px] rounded-md border px-3 py-2 font-mono text-xs outline-none'
            />
            {showRevealLoading ? (
              <div
                id={revealStatusId}
                role='status'
                className='text-muted-foreground flex items-center gap-2 text-xs'
              >
                <Loader2 className='size-3.5 animate-spin' />
                <span>{t('Loading...')}</span>
              </div>
            ) : null}
            {showRevealError ? (
              <div
                id={revealStatusId}
                role='status'
                className='text-muted-foreground text-xs'
              >
                {t('An unexpected error occurred')}
              </div>
            ) : null}
          </div>
        </PopoverContent>
      </Popover>
      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              variant='ghost'
              size='icon'
              className='size-7 shrink-0'
              onClick={handleCopy}
              disabled={isLoading}
            />
          }
        >
          {copyIcon}
        </TooltipTrigger>
        <TooltipContent>{copyTooltip}</TooltipContent>
      </Tooltip>
    </div>
  )
}

type UnlimitedQuotaBadgeProps = {
  used: number
}

export function UnlimitedQuotaBadge(props: UnlimitedQuotaBadgeProps) {
  const { t } = useTranslation()
  const formattedUsed = formatQuota(props.used)

  return (
    <Popover>
      <PopoverTrigger
        render={
          <button
            type='button'
            className='focus-visible:ring-ring/50 -ml-1.5 cursor-help rounded-4xl focus-visible:ring-[3px] focus-visible:outline-none'
            aria-label={`${t('Unlimited')}; ${t('Used:')} ${formattedUsed}`}
          />
        }
      >
        <StatusBadge
          label={t('Unlimited')}
          variant='neutral'
          copyable={false}
        />
      </PopoverTrigger>
      <PopoverContent className='w-auto p-2' side='top'>
        <span className='text-xs'>
          {t('Used:')} {formattedUsed}
        </span>
      </PopoverContent>
    </Popover>
  )
}

export function ModelLimitsCell({ apiKey }: { apiKey: ApiKey }) {
  const { t } = useTranslation()

  if (!apiKey.model_limits_enabled || !apiKey.model_limits) {
    return (
      <StatusBadge
        label={t('Unlimited')}
        variant='neutral'
        copyable={false}
        className='-ml-1.5'
      />
    )
  }

  const models = apiKey.model_limits.split(',').filter(Boolean)

  return (
    <Tooltip>
      <TooltipTrigger render={<BadgeCell />}>
        <StatusBadge
          label={t('{{count}} model(s)', { count: models.length })}
          variant='neutral'
          copyable={false}
        />
      </TooltipTrigger>
      <TooltipContent side='top' className='max-w-xs'>
        <div className='max-h-[200px] space-y-0.5 overflow-y-auto text-xs'>
          {models.map((m) => (
            <div key={m} className='font-mono'>
              {m}
            </div>
          ))}
        </div>
      </TooltipContent>
    </Tooltip>
  )
}

export function IpRestrictionsCell({ apiKey }: { apiKey: ApiKey }) {
  const { t } = useTranslation()
  const allowIps = apiKey.allow_ips?.trim()

  if (!allowIps) {
    return (
      <StatusBadge
        label={t('No restriction')}
        variant='neutral'
        copyable={false}
        className='-ml-1.5'
      />
    )
  }

  const ips = allowIps
    .split('\n')
    .map((ip) => ip.trim())
    .filter(Boolean)

  return (
    <Tooltip>
      <TooltipTrigger render={<BadgeCell />}>
        <StatusBadge
          label={t('{{count}} IP(s)', { count: ips.length })}
          variant='neutral'
          copyable={false}
        />
      </TooltipTrigger>
      <TooltipContent side='top' className='max-w-xs'>
        <div className='max-h-[200px] space-y-0.5 overflow-y-auto text-xs'>
          {ips.map((ip) => (
            <div key={ip} className='font-mono'>
              {ip}
            </div>
          ))}
        </div>
      </TooltipContent>
    </Tooltip>
  )
}
