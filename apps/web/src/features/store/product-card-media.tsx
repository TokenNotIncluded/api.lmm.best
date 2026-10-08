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
  className,
}: {
  images: readonly string[]
  title: string
  list: boolean
  featured?: boolean
  href?: string
  className?: string
}) {
  const header = storeProductHeaderImage(images)
  const logo = safeStoreMediaUrl(images[0])
  if (!header) return null
  return (
    <ProductMedia
      key={`${header}\n${logo ?? ''}`}
      header={header}
      logo={logo}
      title={title}
      list={list}
      featured={featured}
      hasHeader={safeStoreMediaUrl(images[1]) !== undefined}
      href={href}
      className={className}
    />
  )
}

function ProductMedia({
  header,
  logo,
  title,
  list,
  featured,
  hasHeader,
  href,
  className,
}: {
  header: string
  logo?: string
  title: string
  list: boolean
  featured: boolean
  hasHeader: boolean
  href?: string
  className?: string
}) {
  const [failed, setFailed] = useState<readonly string[]>([])
  const source = [header, logo].find(
    (value): value is string => !!value && !failed.includes(value)
  )
  if (!source) return null
  const media = (
    <div
      className={cn(
        'relative flex aspect-[16/9] w-full items-center justify-center overflow-hidden rounded-xl',
        featured && 'aspect-[16/10]',
        list && 'sm:w-44 sm:shrink-0 sm:self-start',
        !href && className
      )}
    >
      <img
        src={source}
        alt={title}
        className={cn(
          'size-full object-cover',
          (!hasHeader || source !== header) && 'object-contain p-6'
        )}
        loading='lazy'
        decoding='async'
        referrerPolicy='no-referrer'
        onError={() => setFailed((previous) => [...previous, source])}
      />
      {logo && logo !== source && !failed.includes(logo) && (
        <img
          src={logo}
          alt=''
          className='bg-background absolute bottom-3 left-3 size-12 rounded-lg object-contain p-1'
          loading='lazy'
          referrerPolicy='no-referrer'
          onError={() => setFailed((previous) => [...previous, logo])}
        />
      )}
    </div>
  )
  return href ? (
    <a
      href={href}
      aria-label={title}
      className={cn(
        'focus-visible:outline-ring block overflow-hidden rounded-xl focus-visible:outline-2',
        className
      )}
    >
      {media}
    </a>
  ) : media
}
