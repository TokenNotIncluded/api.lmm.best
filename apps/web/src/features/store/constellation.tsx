/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

import { STORE_CONSTELLATION_COPY as copy } from './constellation-copy'

const stars = [
  [8, 65],
  [23, 25],
  [40, 40],
  [55, 18],
  [69, 51],
  [87, 28],
  [81, 82],
] as const

/** A local, optional toy. No timers, network, storage or automatic motion. */
export function StoreConstellation() {
  const { t } = useTranslation()
  const [lit, setLit] = useState<ReadonlySet<number>>(() => new Set())
  const [visible, setVisible] = useState(true)
  if (!visible) {
    return (
      <Button variant='ghost' size='sm' onClick={() => setVisible(true)}>
        {t(copy.show)}
      </Button>
    )
  }
  return (
    <div className='mx-auto w-full max-w-xs space-y-2 text-center'>
      <p className='text-muted-foreground text-xs'>{t(copy.title)}</p>
      <div
        className='relative mx-auto h-40 w-full'
        role='group'
        aria-label={t(copy.title)}
      >
        <svg
          viewBox='0 0 100 100'
          preserveAspectRatio='none'
          className='pointer-events-none absolute inset-0 size-full'
          aria-hidden='true'
        >
          {stars.slice(1).map(([x, y], index) => (
            <line
              key={index}
              x1={stars[index][0]}
              y1={stars[index][1]}
              x2={x}
              y2={y}
              className={cn(
                'stroke-border transition-colors duration-200 motion-reduce:transition-none',
                lit.has(index) && lit.has(index + 1) && 'stroke-primary/60'
              )}
              strokeWidth='0.5'
            />
          ))}
        </svg>
        {stars.map(([x, y], index) => (
          <button
            key={index}
            type='button'
            aria-label={t(copy.star, { number: index + 1 })}
            aria-pressed={lit.has(index)}
            className='focus-visible:outline-ring absolute flex size-11 -translate-x-1/2 -translate-y-1/2 items-center justify-center rounded-md outline-offset-2 focus-visible:outline-2'
            style={{ left: `${x}%`, top: `${y}%` }}
            onClick={() => setLit((previous) => new Set(previous).add(index))}
          >
            <svg
              viewBox='0 0 16 16'
              className={cn(
                'text-muted-foreground/50 size-5 transition-colors duration-200 motion-reduce:transition-none',
                lit.has(index) && 'text-primary'
              )}
              aria-hidden='true'
            >
              <path fill='currentColor' d='M6 1h4v5h5v4h-5v5H6v-5H1V6h5z' />
            </svg>
          </button>
        ))}
      </div>
      <p className='text-muted-foreground min-h-5 text-xs' role='status'>
        {lit.size === stars.length
          ? t(copy.complete)
          : lit.size > 0
            ? t(copy.progress, { count: lit.size, total: stars.length })
            : t(copy.hint)}
      </p>
      <div className='flex justify-center gap-2'>
        <Button
          variant='ghost'
          size='sm'
          disabled={!lit.size}
          onClick={() => setLit(new Set())}
        >
          {t('Reset')}
        </Button>
        <Button variant='ghost' size='sm' onClick={() => setVisible(false)}>
          {t('Close')}
        </Button>
      </div>
    </div>
  )
}
