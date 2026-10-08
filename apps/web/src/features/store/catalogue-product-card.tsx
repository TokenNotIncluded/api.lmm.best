/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { ArrowRight01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

import { StoreCatalogueTags } from './catalogue-tags'
import type { StoreCatalogueProduct } from './catalogue-types'
import { StoreCollectionActions } from './collection-actions'
import { StoreMerchantIdentity } from './merchant-identity'
import { StoreProductCardMedia } from './product-card-media'
import { StoreBadges } from './shared'
import {
  useStoreProductImpression,
  type StoreTrafficPage,
} from './traffic-page'
import { StoreProductPrice } from './variant-summary'

export function StoreCatalogueProductCard({
  product,
  featured,
  list,
  trafficPage,
}: {
  product: StoreCatalogueProduct
  featured: boolean
  list: boolean
  trafficPage?: StoreTrafficPage
}) {
  const { t } = useTranslation()
  const impressionRef = useStoreProductImpression(trafficPage, product)
  const href = `/store/products/${encodeURIComponent(product.id)}`
  const sold =
    typeof product.net_paid_quantity === 'number' &&
    Number.isSafeInteger(product.net_paid_quantity) &&
    product.net_paid_quantity >= 0
      ? t('Sold: {{count}}', { count: product.net_paid_quantity })
      : undefined

  return (
    <article
      ref={impressionRef}
      data-store-product-id={product.id}
      data-store-card-priority={featured ? 'lead' : 'standard'}
      className={cn(
        'min-w-0',
        list
          ? 'grid gap-6 py-6 sm:grid-cols-[minmax(0,1fr)_14rem]'
          : 'col-span-12 flex flex-col gap-5 py-5 sm:col-span-6',
        !list && (featured ? 'lg:col-span-4' : 'lg:col-span-3')
      )}
    >
      <div className={cn('min-w-0', list && 'sm:flex sm:items-start sm:gap-6')}>
        <StoreProductCardMedia
          images={product.image_urls || []}
          title={product.title}
          list={list}
          featured={!list && featured}
          href={href}
          className={cn('mb-5', list && 'sm:mb-0 sm:shrink-0')}
        />
        <div className='flex min-w-0 flex-1 flex-col gap-3'>
          <StoreBadges product={product} />
          <h2
            className={cn(
              'text-lg leading-7 font-semibold tracking-tight break-words',
              featured && !list && 'sm:text-xl'
            )}
          >
            <a
              href={href}
              className='focus-visible:outline-ring rounded-sm underline-offset-4 hover:underline focus-visible:outline-2'
            >
              {product.title}
            </a>
          </h2>
          {product.description?.trim() && (
            <p className='text-muted-foreground line-clamp-2 text-sm leading-6 break-words'>
              {product.description}
            </p>
          )}
        </div>
      </div>
      <div className={cn('flex min-w-0 flex-col gap-5', !list && 'mt-auto')}>
        <div className='flex flex-wrap items-center justify-between gap-3'>
          <strong className='min-w-0 text-lg font-semibold break-words'>
            <StoreProductPrice product={product} />
          </strong>
          <Button variant='ghost' className='h-11 max-w-full' render={<a href={href} />}>
            {t('View details')}
            <HugeiconsIcon icon={ArrowRight01Icon} data-icon='inline-end' className='size-4' />
          </Button>
        </div>
        {sold && <p className='text-muted-foreground text-xs'>{sold}</p>}
        <StoreMerchantIdentity seller={product.seller} sellerId={product.seller_id} />
        <details className='group min-w-0'>
          <summary className='text-muted-foreground focus-visible:outline-ring w-fit cursor-pointer rounded-sm py-2 text-sm underline-offset-4 hover:underline focus-visible:outline-2'>
            {t('More options')}
          </summary>
          <div className='space-y-4 pt-3'>
            <StoreCatalogueTags product={product} />
            <StoreCollectionActions product={product} />
          </div>
        </details>
      </div>
    </article>
  )
}
