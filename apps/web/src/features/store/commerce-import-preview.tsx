/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Separator } from '@/components/ui/separator'

import type { StoreVisibility } from './access-types'
import { STORE_COMMERCE_IMPORT_COPY as copy } from './commerce-import-copy'
import type {
  CommerceImportConnection,
  CommerceImportDraft,
  CommerceImportListing,
  CommerceImportMapping,
  CommerceImportRestock,
} from './commerce-import-types'
import {
  commerceImportCount,
  commerceImportPriceQuota,
  commerceImportText,
  commerceImportUnsupportedFields,
  type CommerceImportPriceUnit,
} from './commerce-import-utils'

export function StoreCommerceImportPreview({
  listing,
  mapping,
  connection,
  busy,
  uncertain,
  onSave,
  onRestock,
}: {
  listing: CommerceImportListing
  mapping?: CommerceImportMapping
  connection: CommerceImportConnection
  busy: boolean
  uncertain: boolean
  onSave: (body: CommerceImportDraft) => Promise<void>
  onRestock: (body: CommerceImportRestock) => Promise<void>
}) {
  const { t } = useTranslation()
  const id = useId()
  const text = commerceImportText
  const [unit, setUnit] = useState<CommerceImportPriceUnit>('USD')
  const [prices, setPrices] = useState<Record<string, string>>({})
  const [enabled, setEnabled] = useState<Record<string, boolean>>({})
  const [visibility, setVisibility] = useState<StoreVisibility | ''>('')
  const [confirmed, setConfirmed] = useState(false)
  const unsupported = commerceImportUnsupportedFields(listing)
  const variants = listing.variants.map((variant) => ({
    external_id: variant.id,
    price_quota: commerceImportPriceQuota(prices[variant.id] ?? '', unit),
    enabled: variant.enabled && (enabled[variant.id] ?? variant.enabled),
  }))
  const validPrices =
    variants.length > 0 &&
    variants.every((variant) => variant.price_quota !== undefined)
  const canSave = validPrices && visibility !== '' && confirmed && !busy
  const changed = mapping && mapping.revision !== listing.revision
  return (
    <section className='space-y-5' aria-label={t(copy.preview)}>
      <div className='space-y-1'>
        <h3 className='text-lg font-semibold break-words'>
          {text(listing.product.name)}
        </h3>
        <p className='text-muted-foreground text-sm'>{t(copy.productsHelp)}</p>
        {mapping && (
          <p className='text-sm'>
            {t(copy.linked, { id: mapping.local_product_id })}
          </p>
        )}
      </div>
      {changed && (
        <Alert>
          <AlertDescription>{t(copy.changed)}</AlertDescription>
        </Alert>
      )}
      <dl className='grid gap-x-5 gap-y-2 text-sm sm:grid-cols-[auto_minmax(0,1fr)]'>
        <dt className='text-muted-foreground'>{t(copy.name)}</dt>
        <dd className='break-words'>{text(listing.product.name)}</dd>
        <dt className='text-muted-foreground'>{t(copy.descriptionField)}</dt>
        <dd className='max-h-48 overflow-y-auto break-words whitespace-pre-wrap'>
          {text(listing.product.description)}
        </dd>
        {typeof listing.product.support_email === 'string' &&
          listing.product.support_email && (
            <>
              <dt className='text-muted-foreground'>{t('Seller contact')}</dt>
              <dd className='break-words'>{listing.product.support_email}</dd>
            </>
          )}
        <dt className='text-muted-foreground'>{t(copy.externalProduct)}</dt>
        <dd className='font-mono break-all'>{listing.id}</dd>
        <dt className='text-muted-foreground'>{t(copy.redemption)}</dt>
        <dd className='break-all'>{listing.redemption_url}</dd>
        <dt className='text-muted-foreground'>{t(copy.revision)}</dt>
        <dd className='font-mono text-xs break-all'>{listing.revision}</dd>
      </dl>
      <Alert>
        <AlertTitle>{t(copy.mappingTitle)}</AlertTitle>
        <AlertDescription>
          <p>{t(copy.mappingHelp)}</p>
          {unsupported.length ? (
            <ul className='list-inside list-disc text-xs break-all'>
              {unsupported.map((field) => (
                <li key={field}>{field}</li>
              ))}
            </ul>
          ) : (
            <p>{t(copy.noUnsupported)}</p>
          )}
        </AlertDescription>
      </Alert>
      {listing.product.mode === 'stock' && (
        <Alert>
          <AlertDescription>{t(copy.stockHelp)}</AlertDescription>
        </Alert>
      )}
      <form
        onSubmit={(event) => {
          event.preventDefault()
          if (!canSave || !visibility) return
          const draftVariants = variants.filter(
            (variant): variant is typeof variant & { price_quota: number } =>
              variant.price_quota !== undefined
          )
          if (draftVariants.length !== listing.variants.length) return
          void onSave({
            product_id: listing.id,
            revision: listing.revision,
            visibility,
            variants: draftVariants,
            confirmed: true,
          })
        }}
      >
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor={`${id}-unit`}>{t(copy.priceUnit)}</FieldLabel>
            <NativeSelect
              id={`${id}-unit`}
              value={unit}
              disabled={busy}
              onChange={(event) => {
                setUnit(event.target.value as CommerceImportPriceUnit)
                setPrices({})
                setConfirmed(false)
              }}
            >
              <NativeSelectOption value='USD'>{t(copy.usd)}</NativeSelectOption>
              <NativeSelectOption value='QUOTA'>
                {t(copy.integerQuota)}
              </NativeSelectOption>
            </NativeSelect>
            <FieldDescription>{t(copy.priceHelp)}</FieldDescription>
          </Field>
          <FieldSet>
            <FieldLegend>{t(copy.variantName)}</FieldLegend>
            <FieldDescription>{t(copy.referenceHelp)}</FieldDescription>
            <FieldGroup className='gap-4'>
              {listing.variants.map((variant, index) => {
                const input = prices[variant.id] ?? ''
                const invalid =
                  input !== '' && variants[index].price_quota === undefined
                return (
                  <FieldSet
                    key={variant.id}
                    className='gap-3 rounded-lg border p-4'
                  >
                    <FieldLegend variant='label'>
                      {text(variant.name)}
                    </FieldLegend>
                    <p className='text-muted-foreground text-xs break-all'>
                      {t(copy.externalVariant)}: {variant.id}
                    </p>
                    {variant.description && (
                      <p className='text-sm break-words whitespace-pre-wrap'>
                        {text(variant.description)}
                      </p>
                    )}
                    <p className='text-sm'>
                      {t(copy.reference)}:{' '}
                      {typeof variant.price === 'string' && variant.price !== ''
                        ? `${variant.price} ${variant.currency || ''}`
                        : t(copy.unknownPrice)}
                    </p>
                    <Field data-invalid={invalid} data-disabled={busy}>
                      <FieldLabel htmlFor={`${id}-price-${index}`}>
                        {t(copy.price)} ·{' '}
                        {unit === 'USD' ? t(copy.usd) : t(copy.integerQuota)}
                      </FieldLabel>
                      <Input
                        id={`${id}-price-${index}`}
                        inputMode={unit === 'USD' ? 'decimal' : 'numeric'}
                        maxLength={64}
                        value={input}
                        disabled={busy}
                        aria-invalid={invalid}
                        onChange={(event) => {
                          setPrices((value) => ({
                            ...value,
                            [variant.id]: event.target.value,
                          }))
                          setConfirmed(false)
                        }}
                      />
                      {invalid && (
                        <FieldDescription>
                          {t(copy.priceError)}
                        </FieldDescription>
                      )}
                    </Field>
                    <Field
                      orientation='horizontal'
                      data-disabled={busy || !variant.enabled}
                    >
                      <Checkbox
                        id={`${id}-enabled-${index}`}
                        checked={variants[index].enabled}
                        disabled={busy || !variant.enabled}
                        onCheckedChange={(value) => {
                          setEnabled((values) => ({
                            ...values,
                            [variant.id]: value,
                          }))
                          setConfirmed(false)
                        }}
                      />
                      <FieldLabel htmlFor={`${id}-enabled-${index}`}>
                        {t(copy.localEnabled)}
                      </FieldLabel>
                      {!variant.enabled && (
                        <Badge variant='secondary'>
                          {t(copy.externalDisabled)}
                        </Badge>
                      )}
                    </Field>
                  </FieldSet>
                )
              })}
            </FieldGroup>
          </FieldSet>
          <Field>
            <FieldLabel htmlFor={`${id}-visibility`}>
              {t(copy.visibility)}
            </FieldLabel>
            <NativeSelect
              id={`${id}-visibility`}
              value={visibility}
              disabled={busy}
              onChange={(event) => {
                setVisibility(event.target.value as StoreVisibility)
                setConfirmed(false)
              }}
            >
              <NativeSelectOption value=''>
                {t(copy.chooseVisibility)}
              </NativeSelectOption>
              <NativeSelectOption value='public'>
                {t(copy.publicVisibility)}
              </NativeSelectOption>
              <NativeSelectOption value='registered'>
                {t(copy.registeredVisibility)}
              </NativeSelectOption>
              <NativeSelectOption value='private'>
                {t(copy.privateVisibility)}
              </NativeSelectOption>
            </NativeSelect>
            <FieldDescription>{t(copy.visibilityHelp)}</FieldDescription>
          </Field>
          <Field orientation='horizontal' data-disabled={busy}>
            <Checkbox
              id={`${id}-confirm`}
              checked={confirmed}
              disabled={busy}
              onCheckedChange={setConfirmed}
            />
            <FieldLabel htmlFor={`${id}-confirm`}>{t(copy.confirm)}</FieldLabel>
          </Field>
          <Button type='submit' disabled={!canSave}>
            {t(copy.saveDraft)}
          </Button>
        </FieldGroup>
      </form>
      {mapping && (
        <>
          <Separator />
          <StoreCommerceImportRestockForm
            listing={listing}
            mapping={mapping}
            connection={connection}
            busy={busy}
            uncertain={uncertain}
            onRestock={onRestock}
          />
        </>
      )}
    </section>
  )
}

function StoreCommerceImportRestockForm({
  listing,
  mapping,
  connection,
  busy,
  uncertain,
  onRestock,
}: {
  listing: CommerceImportListing
  mapping: CommerceImportMapping
  connection: CommerceImportConnection
  busy: boolean
  uncertain: boolean
  onRestock: (body: CommerceImportRestock) => Promise<void>
}) {
  const { t } = useTranslation()
  const id = useId()
  const [variantId, setVariantId] = useState('')
  const [input, setInput] = useState('')
  const [label, setLabel] = useState('')
  const [confirmed, setConfirmed] = useState(false)
  const maximum = Math.min(100, connection.maximum_cards_per_request)
  const count = commerceImportCount(input, maximum)
  const variants = listing.variants.filter((variant) =>
    mapping.variants.some(
      (link) => link.external_id === variant.id && link.local_variant_id
    )
  )
  const selected = variants.find((variant) => variant.id === variantId)
  const authorized =
    connection.status === 'active' &&
    connection.scope.split(/\s+/).includes('cards.issue')
  const allowed =
    authorized &&
    listing.product.mode !== 'stock' &&
    selected?.enabled === true &&
    !busy &&
    !uncertain
  const labelValid =
    label.length <= 100 &&
    Array.from(label).every((character) => {
      const code = character.charCodeAt(0)
      return code > 31 && code !== 127
    })
  const canSubmit = allowed && count !== undefined && labelValid && confirmed
  return (
    <form
      className='space-y-4'
      aria-label={t(copy.restock)}
      onSubmit={(event) => {
        event.preventDefault()
        if (!canSubmit || count === undefined) return
        setConfirmed(false)
        void onRestock({
          product_id: listing.id,
          variant_id: variantId,
          count,
          expected_revision: listing.revision,
          label,
        })
      }}
    >
      <h3 className='font-semibold'>{t(copy.restock)}</h3>
      <p className='text-muted-foreground text-sm'>{t(copy.restockHelp)}</p>
      <FieldGroup>
        <Field
          data-disabled={
            !authorized || busy || uncertain || listing.product.mode === 'stock'
          }
        >
          <FieldLabel htmlFor={`${id}-variant`}>
            {t(copy.restockVariant)}
          </FieldLabel>
          <NativeSelect
            id={`${id}-variant`}
            value={variantId}
            disabled={
              !authorized ||
              busy ||
              uncertain ||
              listing.product.mode === 'stock'
            }
            onChange={(event) => {
              setVariantId(event.target.value)
              setConfirmed(false)
            }}
          >
            <NativeSelectOption value=''>
              {t(copy.chooseVariant)}
            </NativeSelectOption>
            {variants.map((variant) => (
              <NativeSelectOption
                key={variant.id}
                value={variant.id}
                disabled={!variant.enabled}
              >
                {commerceImportText(variant.name)} · {variant.id}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </Field>
        <Field
          data-invalid={input !== '' && count === undefined}
          data-disabled={!allowed}
        >
          <FieldLabel htmlFor={`${id}-count`}>{t(copy.count)}</FieldLabel>
          <Input
            id={`${id}-count`}
            inputMode='numeric'
            maxLength={4}
            value={input}
            disabled={!allowed}
            aria-invalid={input !== '' && count === undefined}
            onChange={(event) => {
              setInput(event.target.value)
              setConfirmed(false)
            }}
          />
          <FieldDescription>{t(copy.countHelp, { maximum })}</FieldDescription>
        </Field>
        <Field data-disabled={!allowed}>
          <FieldLabel htmlFor={`${id}-label`}>{t(copy.label)}</FieldLabel>
          <Input
            id={`${id}-label`}
            maxLength={100}
            value={label}
            disabled={!allowed}
            aria-invalid={!labelValid}
            onChange={(event) => {
              setLabel(event.target.value)
              setConfirmed(false)
            }}
          />
        </Field>
        <Field orientation='horizontal' data-disabled={!allowed}>
          <Checkbox
            id={`${id}-confirm`}
            checked={confirmed}
            disabled={!allowed}
            onCheckedChange={setConfirmed}
          />
          <FieldLabel htmlFor={`${id}-confirm`}>
            {t(copy.restockConfirm)}
          </FieldLabel>
        </Field>
        <Button type='submit' disabled={!canSubmit}>
          {t(copy.generate)}
        </Button>
      </FieldGroup>
    </form>
  )
}
