/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { Ellipsis } from 'lucide-react'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

export function StoreNavigation({
  path,
  canReview,
}: {
  path: string
  canReview: boolean
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const panelId = useId()
  return (
    <nav aria-label={t('Store navigation')} className='flex flex-col gap-2'>
      <div className='flex items-center gap-1 sm:gap-2'>
        {[
          ['/store', 'Shop'],
          ['/store/cart', 'Shopping cart'],
          ['/store/favorites', 'Favorites'],
          ['/store/orders', 'My orders'],
        ].map(([href, label]) => (
          <a
            key={href}
            href={href}
            aria-current={path === href ? 'page' : undefined}
            className='text-muted-foreground hover:text-foreground focus-visible:outline-ring aria-[current=page]:bg-foreground aria-[current=page]:text-background flex min-h-11 min-w-0 flex-1 items-center justify-center rounded-full px-2 py-2 text-center text-xs leading-4 transition-colors focus-visible:outline-2 motion-reduce:transition-none sm:flex-none sm:px-5 sm:text-sm'
          >
            {t(label)}
          </a>
        ))}
        <Button
          type='button'
          variant='ghost'
          className='size-11 shrink-0 rounded-full sm:ms-auto'
          aria-label={t('More')}
          aria-expanded={open}
          aria-controls={panelId}
          onClick={() => setOpen((value) => !value)}
        >
          <Ellipsis className='size-5' aria-hidden='true' />
        </Button>
      </div>
      <div id={panelId} hidden={!open}>
        <div className='bg-muted/40 grid gap-1 rounded-2xl p-2 sm:grid-cols-3'>
          {[
            ['/store/manage', 'Seller center'],
            ['/store/settings', 'Settings'],
            ...(canReview ? [['/store/review', 'Review products']] : []),
          ].map(([href, label]) => (
            <a
              key={href}
              href={href}
              aria-current={path === href ? 'page' : undefined}
              className='hover:bg-muted focus-visible:outline-ring aria-[current=page]:bg-muted flex min-h-11 items-center rounded-xl px-4 py-2 text-sm focus-visible:outline-2'
            >
              {t(label)}
            </a>
          ))}
        </div>
      </div>
    </nav>
  )
}
