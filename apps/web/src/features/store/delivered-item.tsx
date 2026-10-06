/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'

import {
  DELIVERY_FIELDS,
  parseDeliveryItem,
  deliveryItemText,
} from './delivery-template'
import { STORE_DELIVERY_TEMPLATE_COPY as copy } from './delivery-template-copy'
import { CopyStoreValue } from './shared'
import { safeStoreUrl } from './utils'

export function StoreDeliveredItem({
  raw,
  template,
  index,
}: {
  raw: string
  template: string | undefined
  index: number
}) {
  const { t } = useTranslation()
  const [passwordShown, setPasswordShown] = useState(false)
  const parsed = parseDeliveryItem(raw, template)
  const itemId = `pickup-item-${index}`
  if (!parsed) {
    return (
      <div className='space-y-2 border-t pt-4'>
        <Label htmlFor={itemId}>
          {t('Item {{number}}', { number: index + 1 })}
        </Label>
        <Textarea
          id={itemId}
          value={raw}
          readOnly
          rows={Math.min(6, Math.max(2, raw.split('\n').length))}
          autoComplete='off'
          spellCheck={false}
        />
        <CopyStoreValue value={raw} />
      </div>
    )
  }
  return (
    <section className='space-y-3 border-t pt-4'>
      <h3 className='text-sm font-medium'>
        {t('Item {{number}}', { number: index + 1 })}
      </h3>
      {DELIVERY_FIELDS[parsed.template].map((field) => {
        const value = parsed.fields[field.name]
        if (!value) return null
        const id = `${itemId}-${field.name}`
        const url = field.url ? safeStoreUrl(value) : undefined
        return (
          <div key={field.name} className='space-y-2'>
            <Label htmlFor={id}>{t(field.label)}</Label>
            {field.password ? (
              <Input
                id={id}
                value={value}
                type={passwordShown ? 'text' : 'password'}
                readOnly
                autoComplete='off'
                spellCheck={false}
              />
            ) : (
              <Textarea
                id={id}
                value={value}
                readOnly
                rows={Math.min(6, Math.max(2, value.split('\n').length))}
                autoComplete='off'
                spellCheck={false}
              />
            )}
            <div className='flex flex-wrap gap-2'>
              <CopyStoreValue value={value} />
              {field.password && (
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  aria-pressed={passwordShown}
                  onClick={() => setPasswordShown((shown) => !shown)}
                >
                  {t(passwordShown ? copy.hidePassword : copy.showPassword)}
                </Button>
              )}
              {url && (
                <Button
                  size='sm'
                  variant='outline'
                  render={
                    <a href={url} target='_blank' rel='noopener noreferrer' />
                  }
                >
                  {t(
                    parsed.template === 'download-link'
                      ? copy.openDownload
                      : parsed.template === 'redemption-code'
                        ? copy.openRedemption
                        : copy.openLogin
                  )}
                </Button>
              )}
            </div>
          </div>
        )
      })}
      <CopyStoreValue value={deliveryItemText(raw, template, t)} />
    </section>
  )
}
