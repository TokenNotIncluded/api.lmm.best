/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Textarea } from '@/components/ui/textarea'

import { storeAccessApi, StoreAccessRequestError } from './access-api'
import { STORE_ACCESS_COPY as copy } from './access-copy'
import type { StoreSellerTerms } from './access-types'
import { StoreError, StoreLoading } from './shared'

export function StoreMerchantTermsEditor({ sellerId }: { sellerId: number }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const query = useQuery({
    queryKey: ['store', 'my-terms', sellerId],
    queryFn: storeAccessApi.myTerms,
    retry: false,
  })
  return (
    <section id='seller-terms' className='space-y-4 rounded-lg border p-4'>
      <h2 className='font-semibold'>{t(copy.termsTitle)}</h2>
      <p className='text-muted-foreground text-sm'>{t(copy.termsHelp)}</p>
      <StoreError error={query.error} retry={() => void query.refetch()} />
      {query.isPending && <StoreLoading />}
      {query.data && (
        <StoreMerchantTermsForm
          key={query.data.version}
          terms={query.data}
          onReload={async () => {
            await query.refetch()
          }}
          onSaved={async () => {
            await client.invalidateQueries({
              queryKey: ['store', 'my-terms', sellerId],
            })
          }}
        />
      )}
    </section>
  )
}
export function StoreMerchantTermsForm({
  terms,
  onSaved,
  onReload,
}: {
  terms: StoreSellerTerms
  onSaved: () => Promise<void>
  onReload?: () => Promise<void>
}) {
  const { t } = useTranslation()
  const [content, setContent] = useState(terms.content)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [saved, setSaved] = useState(false)
  const invalid = content.includes('\0')
    ? copy.termsInvalid
    : new TextEncoder().encode(content.trim()).length > 64 * 1024
      ? copy.termsTooLong
      : ''
  async function save(event: React.FormEvent) {
    event.preventDefault()
    if (busy || !content.trim() || invalid) return
    setBusy(true)
    setSaved(false)
    setError(null)
    try {
      await storeAccessApi.saveTerms(content.trim(), terms.version)
      await onSaved()
      setSaved(true)
    } catch (issue) {
      setError(issue)
    } finally {
      setBusy(false)
    }
  }
  return (
    <form onSubmit={(event) => void save(event)}>
      <FieldGroup className='gap-4'>
        <Field data-disabled={busy}>
          <FieldLabel htmlFor='store-seller-terms'>
            {t(copy.termsContent)}
          </FieldLabel>
          <Textarea
            aria-invalid={!!invalid}
            id='store-seller-terms'
            value={content}
            disabled={busy}
            rows={7}
            placeholder={t(copy.termsPlaceholder)}
            required
            onChange={(event) => {
              setContent(event.target.value)
              setSaved(false)
            }}
          />
          <FieldDescription className='text-xs'>
            {t(copy.termsRequired)}
          </FieldDescription>
        </Field>
        {terms.version && (
          <p className='text-muted-foreground text-xs'>
            {t(copy.termsVersion, { version: terms.version })}
          </p>
        )}
        {invalid && (
          <p role='alert' className='text-destructive text-sm'>
            {t(invalid)}
          </p>
        )}
        <StoreError error={error} />
        {onReload &&
          error instanceof StoreAccessRequestError &&
          error.code === 'STORE_CONFLICT' && (
            <Button
              type='button'
              variant='outline'
              className='self-start'
              disabled={busy}
              onClick={() => void onReload()}
            >
              {t(copy.termsReload)}
            </Button>
          )}
        {saved && (
          <p role='status' className='text-sm'>
            {t(copy.termsSaved)}
          </p>
        )}
        <Button
          type='submit'
          className='self-start'
          disabled={
            busy ||
            !!invalid ||
            !content.trim() ||
            content.trim() === terms.content.trim()
          }
        >
          {t(busy ? 'Saving...' : 'Save')}
        </Button>
      </FieldGroup>
    </form>
  )
}
export function StoreMerchantTermsAcceptance({
  terms,
  loading,
  error,
  accepted,
  setAccepted,
  refresh,
}: {
  terms: StoreSellerTerms | null
  loading: boolean
  error: unknown
  accepted: boolean
  setAccepted: (accepted: boolean) => void
  refresh: () => Promise<void>
}) {
  const { t } = useTranslation()
  return (
    <section className='space-y-3 rounded-lg border p-3'>
      <h3 className='text-sm font-semibold'>{t(copy.termsTitle)}</h3>
      {loading && <StoreLoading />}
      <StoreError error={error} retry={() => void refresh()} />
      {!loading && !error && !terms?.configured && (
        <p role='status' className='text-muted-foreground text-sm'>
          {t(copy.termsRequired)}
        </p>
      )}
      {terms?.configured && (
        <>
          <p className='max-h-48 overflow-y-auto text-sm leading-6 break-words whitespace-pre-wrap'>
            {terms.content}
          </p>
          <FieldGroup className='gap-3'>
            <Field orientation='horizontal' data-disabled={loading || !!error}>
              <Checkbox
                id='store-accept-seller-terms'
                checked={accepted}
                disabled={loading || !!error}
                onCheckedChange={(value) => setAccepted(value === true)}
              />
              <FieldLabel
                htmlFor='store-accept-seller-terms'
                className='text-xs font-normal'
              >
                {t(copy.termsAccept)}
              </FieldLabel>
            </Field>
          </FieldGroup>
        </>
      )}
    </section>
  )
}
