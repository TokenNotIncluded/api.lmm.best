/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'

import type { StoreCatalogueProduct } from './catalogue-types'

const STORE_CATALOGUE_TAG_LABELS = {
  in_stock: 'In stock',
  out_of_stock: 'Out of stock',
  auto_delivery: 'Automatic delivery',
  ai_processing: 'AI processing',
  guest_purchase: 'Guest purchase available',
} as const

export function StoreCatalogueTags({
  product,
}: {
  product: Pick<StoreCatalogueProduct, 'display_tags' | 'catalogue'>
}) {
  const { t } = useTranslation()
  // Tags are supplied by the visibility-aware server projection. Inventory
  // totals and collection credentials do not establish purchase availability.
  const tags = [...new Set(product.display_tags ?? [])]
  const customTags = [...new Set(product.catalogue?.custom_tags ?? [])]
  if (!tags.length && !customTags.length) return null
  return (
    <div className='flex flex-wrap gap-1' aria-label={t('Product tags')}>
      {tags.map((tag) => (
        <Badge
          key={tag}
          variant='secondary'
          className='max-w-full break-words whitespace-normal'
        >
          {tag in STORE_CATALOGUE_TAG_LABELS
            ? t(
                STORE_CATALOGUE_TAG_LABELS[
                  tag as keyof typeof STORE_CATALOGUE_TAG_LABELS
                ]
              )
            : tag}
        </Badge>
      ))}
      {customTags.map((tag) => (
        <Badge
          key={`custom-${tag}`}
          variant='outline'
          className='max-w-full break-words whitespace-normal'
        >
          {tag}
        </Badge>
      ))}
    </div>
  )
}
