/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import { StoreAmount, StoreError } from './shared'
import type { useStoreProductPromotion } from './use-store-product-promotion'

export function StoreProductPromotion({
  promotion,
  onRemove,
}: {
  promotion: ReturnType<typeof useStoreProductPromotion>
  onRemove: () => void
}) {
  const { t } = useTranslation()
  if (!promotion.supplied) return null
  return (
    <section
      aria-label={t('Promotion code')}
      className='space-y-3 rounded-md border p-3 text-sm'
    >
      <div className='flex min-w-0 items-center justify-between gap-2'>
        <span className='min-w-0 font-mono break-all'>
          {promotion.supplied}
        </span>
        <Button type='button' size='sm' variant='ghost' onClick={onRemove}>
          {t('Remove promotion')}
        </Button>
      </div>
      {promotion.pending && <p role='status'>{t('Checking promotion...')}</p>}
      <StoreError error={promotion.error} retry={promotion.retry} />
      {promotion.quote && (
        <>
          <p role='status' className='font-medium'>
            {t('Promotion code applied')} · {promotion.quote.discount_bps / 100}
            %
          </p>
          <dl className='space-y-1 tabular-nums'>
            <div className='flex flex-wrap justify-between gap-2'>
              <dt>{t('Original price')}</dt>
              <dd>
                <StoreAmount quota={promotion.quote.original_price_quota} />
              </dd>
            </div>
            <div className='flex flex-wrap justify-between gap-2'>
              <dt>{t('Discount')}</dt>
              <dd>
                −<StoreAmount quota={promotion.quote.discount_quota} />
              </dd>
            </div>
            <div className='flex flex-wrap justify-between gap-2 font-semibold'>
              <dt>{t('Final price')}</dt>
              <dd>
                {promotion.quote.free ? (
                  t('Free claim')
                ) : (
                  <StoreAmount quota={promotion.quote.price_quota} />
                )}
              </dd>
            </div>
          </dl>
        </>
      )}
    </section>
  )
}
