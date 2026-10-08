/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useTranslation } from 'react-i18next'

import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'

import { normalizeStoreImageSource, STORE_SVG_MAX_BYTES } from './product-media'

export function StoreProductMediaEditor({
  logo,
  header,
  additional,
  svgSupported,
  onLogoChange,
  onHeaderChange,
  onAdditionalChange,
}: {
  logo: string
  header: string
  additional: string
  svgSupported: boolean
  onLogoChange: (value: string) => void
  onHeaderChange: (value: string) => void
  onAdditionalChange: (value: string) => void
}) {
  const { t } = useTranslation()
  return (
    <details className='min-w-0 sm:col-span-2'>
      <summary className='text-muted-foreground focus-visible:outline-ring w-fit cursor-pointer rounded-sm py-2 text-sm focus-visible:outline-2'>
        {t('Product images')} · {t('Optional')}
      </summary>
      <div className='space-y-5 pt-4'>
        <div className='grid gap-5 sm:grid-cols-2'>
          {[
            {
              id: 'store-logo-image',
              label: t('Product logo image'),
              value: logo,
              change: onLogoChange,
            },
            {
              id: 'store-header-image',
              label: t('Product header image'),
              value: header,
              change: onHeaderChange,
            },
          ].map((field) => {
            const preview = normalizeStoreImageSource(field.value)
            return (
              <div key={field.id} className='space-y-2'>
                <Label htmlFor={field.id}>{field.label}</Label>
                <Textarea
                  id={field.id}
                  rows={3}
                  maxLength={svgSupported ? STORE_SVG_MAX_BYTES : 4096}
                  value={field.value}
                  onChange={(event) => field.change(event.target.value)}
                  placeholder={
                    svgSupported ? t('HTTPS image URL or SVG text') : 'https://'
                  }
                />
                {preview && (
                  <img
                    key={preview}
                    src={preview}
                    alt={t('Preview')}
                    className='max-h-48 max-w-full rounded-lg object-contain'
                    referrerPolicy='no-referrer'
                    onError={(event) => {
                      event.currentTarget.hidden = true
                    }}
                  />
                )}
              </div>
            )
          })}
        </div>
        <div className='space-y-2'>
          <Label htmlFor='store-images'>{t('Additional image URLs')}</Label>
          <Textarea
            id='store-images'
            rows={2}
            maxLength={32 * Math.ceil(STORE_SVG_MAX_BYTES / 3) * 4}
            value={additional}
            onChange={(event) => onAdditionalChange(event.target.value)}
            placeholder={t('One URL per line')}
          />
        </div>
      </div>
    </details>
  )
}
