/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'

import {
  DELIVERY_FIELDS,
  DELIVERY_FIELD_BYTE_LIMITS,
  validDeliveryUrl,
  validateComposedItems,
  encodeDeliveryItem,
  structuredTemplate,
  type StoreDeliveryTemplate,
} from './delivery-template'
import { STORE_DELIVERY_TEMPLATE_COPY as copy } from './delivery-template-copy'
import { StoreError } from './shared'

export function StoreInventoryComposer({
  template,
  items,
  onChange,
  disabled,
}: {
  template: StoreDeliveryTemplate
  items: string[]
  onChange: (items: string[]) => void
  disabled: boolean
}) {
  const { t } = useTranslation()
  const [text, setText] = useState('')
  const [fields, setFields] = useState<Record<string, string>>({})
  const [error, setError] = useState<unknown>(null)
  function add() {
    setError(null)
    try {
      const definitions = structuredTemplate(template)
        ? DELIVERY_FIELDS[template]
        : []
      if (
        definitions.some(
          (field) =>
            new TextEncoder().encode(fields[field.name] ?? '').length >
            DELIVERY_FIELD_BYTE_LIMITS[field.name]
        )
      ) {
        throw new Error('Inventory import is too large')
      }
      if (
        definitions.some(
          (field) =>
            field.url &&
            fields[field.name] &&
            !validDeliveryUrl(fields[field.name])
        )
      ) {
        throw new Error('Use valid HTTP or HTTPS links')
      }
      const item = structuredTemplate(template)
        ? encodeDeliveryItem(template, fields)
        : text.trim()
          ? text
          : undefined
      if (item === undefined) throw new Error(t(copy.missing))
      const next = [...items, item]
      validateComposedItems(next)
      onChange(next)
      setText('')
      setFields({})
    } catch (issue) {
      setError(issue)
    }
  }
  return (
    <div className='space-y-3'>
      <StoreError error={error} />
      {structuredTemplate(template) ? (
        DELIVERY_FIELDS[template].map((field) => {
          const id = `store-delivery-${field.name}`
          return (
            <div key={field.name} className='space-y-2'>
              <Label htmlFor={id}>
                {t(field.label)}
                {field.required ? ' *' : ''}
              </Label>
              {field.name === 'instructions' ? (
                <Textarea
                  id={id}
                  rows={3}
                  value={fields[field.name] ?? ''}
                  disabled={disabled}
                  onChange={(event) =>
                    setFields((current) => ({
                      ...current,
                      [field.name]: event.target.value,
                    }))
                  }
                />
              ) : (
                <Input
                  id={id}
                  type={field.password ? 'password' : 'text'}
                  autoComplete='off'
                  spellCheck={false}
                  value={fields[field.name] ?? ''}
                  disabled={disabled}
                  onChange={(event) =>
                    setFields((current) => ({
                      ...current,
                      [field.name]: event.target.value,
                    }))
                  }
                />
              )}
            </div>
          )
        })
      ) : (
        <div className='space-y-2'>
          <Label htmlFor='store-complete-item'>{t(copy.contents)}</Label>
          <Textarea
            id='store-complete-item'
            rows={6}
            value={text}
            disabled={disabled}
            onChange={(event) => setText(event.target.value)}
          />
        </div>
      )}
      <Button
        type='button'
        size='sm'
        variant='outline'
        disabled={disabled}
        onClick={add}
      >
        {t(copy.add)}
      </Button>
      {items.length > 0 && (
        <ol className='max-h-40 divide-y overflow-y-auto rounded-md border'>
          {items.map((_, index) => (
            <li
              key={index}
              className='flex items-center justify-between gap-2 p-2 text-xs'
            >
              <span>{t('Item {{number}}', { number: index + 1 })}</span>
              <Button
                type='button'
                size='sm'
                variant='ghost'
                disabled={disabled}
                aria-label={t(copy.remove)}
                onClick={() =>
                  onChange(items.filter((_, position) => position !== index))
                }
              >
                {t('Remove')}
              </Button>
            </li>
          ))}
        </ol>
      )}
    </div>
  )
}
