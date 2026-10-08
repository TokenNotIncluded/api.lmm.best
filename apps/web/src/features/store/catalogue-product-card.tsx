/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { ArrowUpRight, Ellipsis } from 'lucide-react'
import { useId, useState } from 'react'
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
  const [actionsOpen, setActionsOpen] = useState(false)
  const actionsId = useId()
  const href = `/store/products/${product.id}`
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
      data-store-card-priority={
        list ? undefined : featured ? 'lead' : 'standard'
      }
      className={cn(
        'group/product flex min-w-0 flex-col gap-2.5 py-5 sm:py-3',
        list
          ? 'sm:grid sm:grid-cols-[minmax(0,1fr)_16rem] sm:gap-x-8'
          : 'col-span-12 sm:col-span-6 sm:gap-4',
        !list && (featured ? 'lg:col-span-4' : 'lg:col-span-3')
      )}
    >
      <div
        className={cn(
          'flex min-w-0 flex-col gap-4',
          list && 'sm:row-span-2 sm:flex-row'
        )}
      >
        <StoreProductCardMedia
          images={product.image_urls || []}
          title={product.title}
          list={list}
          featured={featured && !list}
          href={href}
        />
        <div className='flex min-w-0 flex-1 flex-col gap-2.5'>
          <StoreBadges product={product} />
          <h2 className='line-clamp-2 text-xl leading-snug font-semibold tracking-tight break-words'>
            <a
              href={href}
              className='focus-visible:outline-ring rounded-sm underline-offset-4 hover:underline focus-visible:outline-2'
            >
              {product.title}
            </a>
          </h2>
          {product.description && (
            <p className='text-muted-foreground line-clamp-2 text-sm leading-6 break-words'>
              {product.description}
            </p>
          )}
          <StoreCatalogueTags product={product} />
        </div>
      </div>
      <div
        className={cn(
          'flex flex-wrap items-center justify-between gap-x-4 gap-y-2',
          !list && 'sm:mt-auto'
        )}
      >
        <div className='flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1'>
          <strong className='min-w-0 text-2xl leading-8 font-semibold tracking-tight break-words'>
            <StoreProductPrice product={product} />
          </strong>
          {sold && (
            <span className='text-muted-foreground text-xs'>{sold}</span>
          )}
        </div>
        <Button
          variant='secondary'
          className='h-11 max-w-full shrink-0 gap-1.5 rounded-full px-4'
          render={<a href={href} />}
        >
          {t('View details')}
          <ArrowUpRight className='size-4' aria-hidden='true' />
        </Button>
      </div>
      <div className='flex min-w-0 items-center justify-between gap-2'>
        <StoreMerchantIdentity
          seller={product.seller}
          sellerId={product.seller_id}
          compact
        />
        <Button
          type='button'
          variant='ghost'
          className='ms-auto size-11 shrink-0 rounded-full'
          aria-label={t('Cart and favorites')}
          aria-expanded={actionsOpen}
          aria-controls={actionsId}
          onClick={() => setActionsOpen((open) => !open)}
        >
          <Ellipsis className='size-5' aria-hidden='true' />
        </Button>
      </div>
      <div id={actionsId} hidden={!actionsOpen} className='col-span-full'>
        <div className='bg-muted/40 flex flex-col gap-4 rounded-2xl p-4'>
          <StoreMerchantIdentity
            seller={product.seller}
            sellerId={product.seller_id}
          />
          <StoreCollectionActions product={product} />
        </div>
      </div>
    </article>
  )
}
