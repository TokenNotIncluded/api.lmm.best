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
import { Copy, ExternalLink, Gift, Trash2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

import { canDeleteRedPacket, redPacketStatus } from './status'
import type { RedPacket } from './types'

export function RedPacketCard({
  packet,
  onDelete,
}: {
  packet: RedPacket
  onDelete: () => void
}) {
  const { t } = useTranslation()
  const [now, setNow] = useState(() => Math.floor(Date.now() / 1000))
  useEffect(() => {
    const refresh = () => setNow(Math.floor(Date.now() / 1000))
    refresh()
    const nextBoundary = [packet.start_at, packet.end_at]
      .filter((at) => at * 1000 > Date.now())
      .sort((a, b) => a - b)[0]
    const timer = nextBoundary
      ? window.setTimeout(
          refresh,
          Math.min(2_147_483_647, Math.max(1, nextBoundary * 1000 - Date.now()))
        )
      : null
    window.addEventListener('focus', refresh)
    return () => {
      if (timer !== null) window.clearTimeout(timer)
      window.removeEventListener('focus', refresh)
    }
  }, [packet.start_at, packet.end_at, now])
  const status = redPacketStatus(packet, now)
  const statusLabel = {
    Live: t('Live'),
    Paused: t('Paused'),
    Ended: t('Ended'),
    Exhausted: t('Fully claimed'),
    Scheduled: t('Scheduled'),
  }[status]
  const removable = canDeleteRedPacket(packet, now)
  const shareUrl = `${window.location.origin}/red-packet/${packet.slug}`
  const claimed =
    packet.total_items > 0
      ? Math.max(
          0,
          Math.min(
            100,
            Math.round((1 - packet.remaining_items / packet.total_items) * 100)
          )
        )
      : 0

  return (
    <article
      className='bg-card group hover:border-primary/40 min-w-0 overflow-hidden rounded-xl border transition-colors'
      data-packet-id={packet.id}
    >
      <div className='relative'>
        {packet.cover_image ? (
          <img
            src={packet.cover_image}
            alt=''
            className='aspect-[3/1] w-full object-cover'
          />
        ) : (
          <div className='from-primary/15 to-muted flex aspect-[3/1] items-center justify-center bg-gradient-to-br'>
            <Gift className='text-muted-foreground size-8' />
          </div>
        )}
        <span
          className={`bg-background/90 absolute end-2 top-2 rounded-full px-2 py-0.5 text-[10px] font-medium ${status === 'Live' ? 'text-primary' : 'text-muted-foreground'}`}
        >
          {statusLabel}
        </span>
      </div>
      <div className='space-y-3 p-4'>
        <div className='flex items-start justify-between gap-3'>
          <div className='min-w-0'>
            <h2 className='font-medium break-words'>{packet.title}</h2>
            <div className='text-muted-foreground mt-1 text-xs'>
              {packet.remaining_items}/{packet.total_items} {t('remaining')} ·{' '}
              {packet.claim_count} {t('claims')}
            </div>
          </div>
          {removable && (
            <Button
              type='button'
              size='sm'
              variant='ghost'
              className='text-muted-foreground hover:text-destructive shrink-0'
              aria-label={t('Delete red packet')}
              title={t('Delete red packet')}
              onClick={onDelete}
            >
              <Trash2 className='size-4' />
              {t('Delete')}
            </Button>
          )}
        </div>
        <div
          className='bg-muted h-1.5 overflow-hidden rounded-full'
          role='progressbar'
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={claimed}
          aria-label={t('{{percent}}% claimed', { percent: claimed })}
        >
          <span
            className='bg-primary block h-full rounded-full transition-[width] motion-reduce:transition-none'
            style={{ width: `${claimed}%` }}
          />
        </div>
        <div className='flex min-w-0 gap-2'>
          <Input
            value={shareUrl}
            readOnly
            className='h-8 min-w-0 text-xs'
            aria-label={t('Share link')}
          />
          <Button
            type='button'
            size='sm'
            variant='outline'
            aria-label={t('Copy share link')}
            title={t('Copy share link')}
            onClick={async () => {
              try {
                await navigator.clipboard.writeText(shareUrl)
                toast.success(t('Copied to clipboard'))
              } catch {
                toast.error(t('Failed to copy'))
              }
            }}
          >
            <Copy className='size-4' />
          </Button>
          <Button
            size='sm'
            variant='outline'
            aria-label={t('Open claim page')}
            title={t('Open claim page')}
            render={
              <a
                href={`/red-packet/${packet.slug}`}
                target='_blank'
                rel='noopener noreferrer'
              />
            }
          >
            <ExternalLink className='size-4' />
          </Button>
        </div>
      </div>
    </article>
  )
}
