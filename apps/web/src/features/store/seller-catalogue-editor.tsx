/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

import { catalogueApi } from './catalogue-api'
import { useStoreCatalogueSupport } from './catalogue-support'
import type { StoreCatalogueProduct } from './catalogue-types'
import { StoreError } from './shared'
import { currentStoreViewer, useStoreViewer } from './store-viewer'

export function SellerCatalogueEditor({
  product,
  onSaved,
}: {
  product: StoreCatalogueProduct
  onSaved?: () => void
}) {
  const viewer = useStoreViewer()
  return (
    <CatalogueEditor
      key={`${viewer}-${product.id}`}
      product={product}
      onSaved={onSaved}
    />
  )
}

function CatalogueEditor({
  product,
  onSaved,
}: {
  product: StoreCatalogueProduct
  onSaved?: () => void
}) {
  const { t } = useTranslation()
  const id = useId()
  const client = useQueryClient()
  const viewer = useStoreViewer()
  const support = useStoreCatalogueSupport()
  const [tags, setTags] = useState(
    (product.catalogue?.custom_tags ?? []).join('\n')
  )
  const [auto, setAuto] = useState(product.catalogue?.auto_delivery ?? false)
  const [ai, setAi] = useState(product.catalogue?.ai_processing ?? false)
  const [saved, setSaved] = useState(false)
  const save = useMutation({
    mutationFn: () => {
      if (currentStoreViewer() !== viewer) {
        throw new Error(
          'Your account changed. Refresh this page before continuing.'
        )
      }
      return catalogueApi.saveCatalogue(product.id, {
        custom_tags: [
          ...new Set(
            tags
              .split(/\r?\n/)
              .map((tag) => tag.trim())
              .filter(Boolean)
          ),
        ],
        auto_delivery: auto,
        ai_processing: ai,
      })
    },
    onSuccess: () => {
      if (currentStoreViewer() !== viewer) return
      setSaved(true)
      void client.invalidateQueries({ queryKey: ['store'] })
      onSaved?.()
    },
  })
  if (!support.catalogueSupported) return null
  return (
    <form
      className='border-t pt-4'
      onSubmit={(event) => {
        event.preventDefault()
        if (!save.isPending) {
          setSaved(false)
          save.mutate()
        }
      }}
    >
      <FieldSet>
        <FieldLegend>{t('Catalogue tags')}</FieldLegend>
        <FieldDescription>
          {t(
            'Tags describe this product. Delivery and access rules are enforced separately.'
          )}
        </FieldDescription>
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor={`${id}-tags`}>
              {t('Custom product tags')}
            </FieldLabel>
            <Textarea
              id={`${id}-tags`}
              value={tags}
              onChange={(event) => {
                setTags(event.target.value)
                setSaved(false)
              }}
              rows={3}
              disabled={save.isPending}
            />
            <FieldDescription>{t('Enter one tag per line.')}</FieldDescription>
          </Field>
          <Field orientation='horizontal'>
            <FieldLabel htmlFor={`${id}-auto`}>
              {t('Automatic delivery')}
            </FieldLabel>
            <Switch
              id={`${id}-auto`}
              checked={auto}
              onCheckedChange={(value) => {
                setAuto(value)
                setSaved(false)
              }}
              disabled={save.isPending}
            />
          </Field>
          <Field orientation='horizontal'>
            <FieldLabel htmlFor={`${id}-ai`}>{t('AI processing')}</FieldLabel>
            <Switch
              id={`${id}-ai`}
              checked={ai}
              onCheckedChange={(value) => {
                setAi(value)
                setSaved(false)
              }}
              disabled={save.isPending}
            />
          </Field>
        </FieldGroup>
        <StoreError error={save.error} />
        {saved && (
          <p role='status' className='text-muted-foreground text-sm'>
            {t('Catalogue tags saved')}
          </p>
        )}
        <Button type='submit' variant='outline' disabled={save.isPending}>
          {t(save.isPending ? 'Saving...' : 'Save catalogue tags')}
        </Button>
      </FieldSet>
    </form>
  )
}
