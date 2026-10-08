/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { Image01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'

import { cn } from '@/lib/utils'

import { safeStoreMediaUrl } from './product-media'
import { storeProductHeaderImage } from './product-media-fields'

export function StoreProductCardMedia({
  images,
  title,
  list,
  featured = false,
}: {
  images: readonly string[]
  title: string
  list: boolean
  featured?: boolean
}) {
  const header = storeProductHeaderImage(images)
  const logo = safeStoreMediaUrl(images[0])
  const hasHeader = safeStoreMediaUrl(images[1]) !== undefined
  return (
    <div
      className={cn(
        'bg-muted relative flex aspect-[16/9] w-full items-center justify-center overflow-hidden',
        featured && 'aspect-[16/10]',
        list && 'sm:w-44 sm:shrink-0 sm:self-start'
      )}
    >
      {header ? (
        <img
          src={header}
          alt={title}
          className={cn(
            'size-full object-cover',
            !hasHeader && 'object-contain p-6'
          )}
          loading='lazy'
          referrerPolicy='no-referrer'
        />
      ) : (
        <HugeiconsIcon
          icon={Image01Icon}
          className='text-muted-foreground size-8'
          aria-hidden='true'
        />
      )}
      {logo && logo !== header && (
        <img
          src={logo}
          alt=''
          className='bg-card absolute bottom-3 left-3 size-12 rounded-lg border object-contain p-1 shadow-sm'
          loading='lazy'
          referrerPolicy='no-referrer'
        />
      )}
    </div>
  )
}
