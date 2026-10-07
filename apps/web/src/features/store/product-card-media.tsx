/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { cn } from '@/lib/utils'

import { safeStoreMediaUrl } from './product-media'
import { storeProductHeaderImage } from './product-media-fields'

export function StoreProductCardMedia({
  images,
  title,
  list,
}: {
  images: readonly string[]
  title: string
  list: boolean
}) {
  const header = storeProductHeaderImage(images)
  const logo = safeStoreMediaUrl(images[0])
  if (!header) return null
  return (
    <div
      className={cn(
        'bg-muted relative aspect-[16/9] w-full overflow-hidden',
        list && 'sm:w-44 sm:shrink-0 sm:self-start'
      )}
    >
      <img
        src={header}
        alt={title}
        className='size-full object-cover'
        loading='lazy'
        referrerPolicy='no-referrer'
      />
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
