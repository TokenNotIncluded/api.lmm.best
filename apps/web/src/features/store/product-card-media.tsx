/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useState } from 'react'

import { cn } from '@/lib/utils'

import { safeStoreMediaUrl } from './product-media'
import { storeProductHeaderImage } from './product-media-fields'

export function StoreProductCardMedia({
  images,
  title,
  list,
  featured = false,
  href,
}: {
  images: readonly string[]
  title: string
  list: boolean
  featured?: boolean
  href?: string
}) {
  const header = storeProductHeaderImage(images)
  const logo = safeStoreMediaUrl(images[0])
  const hasHeader = safeStoreMediaUrl(images[1]) !== undefined
  const [failedHeader, setFailedHeader] = useState<string>()
  const [failedLogo, setFailedLogo] = useState<string>()
  // An absent or failed image must not leave an empty frame or focusable link.
  if (!header || header === failedHeader) return null
  const media = (
    <div
      className={cn(
        'bg-muted/40 relative flex aspect-[16/9] w-full items-center justify-center overflow-hidden rounded-xl',
        featured && 'aspect-[16/10]',
        list && 'sm:w-44 sm:shrink-0 sm:self-start'
      )}
    >
      <img
        src={header}
        alt={title}
        className={cn(
          'size-full object-cover',
          !hasHeader && 'object-contain p-6'
        )}
        loading='lazy'
        referrerPolicy='no-referrer'
        onError={() => setFailedHeader(header)}
      />
      {logo && logo !== header && logo !== failedLogo && (
        <img
          src={logo}
          alt=''
          className='bg-card absolute bottom-3 left-3 size-12 rounded-lg object-contain p-1'
          loading='lazy'
          referrerPolicy='no-referrer'
          onError={() => setFailedLogo(logo)}
        />
      )}
    </div>
  )
  return href ? (
    <a
      href={href}
      aria-label={title}
      className={cn(
        'focus-visible:outline-ring block rounded-xl focus-visible:outline-2',
        list && 'sm:w-44 sm:shrink-0 sm:self-start'
      )}
    >
      {media}
    </a>
  ) : (
    media
  )
}
