/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useTranslation } from 'react-i18next'

import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from '@/components/ui/field'
import { Textarea } from '@/components/ui/textarea'

import { STORE_FIXED_CONTENT_COPY as copy } from './fixed-content-copy'
import { StoreError } from './shared'
import type { useStoreFixedContentDraft } from './use-store-fixed-content'

export function StoreFixedContentEditor({
  id,
  draft,
  disabled = false,
}: {
  id: string
  draft: ReturnType<typeof useStoreFixedContentDraft>
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const invalid = !draft.loading && !draft.error && !draft.valid
  return (
    <div className='space-y-3'>
      <StoreError error={draft.error} retry={draft.retry} />
      <Field data-disabled={disabled || draft.loading} data-invalid={invalid}>
        <FieldLabel htmlFor={id}>{t(copy.content)}</FieldLabel>
        <Textarea
          id={id}
          rows={8}
          value={draft.content}
          required
          disabled={disabled || draft.loading}
          aria-invalid={invalid}
          onChange={(event) => draft.setContent(event.target.value)}
        />
        <FieldDescription>{t(copy.privateHelp)}</FieldDescription>
        {draft.loading && (
          <FieldDescription>{t('Loading...')}</FieldDescription>
        )}
        {invalid && <FieldError>{t(copy.invalid)}</FieldError>}
      </Field>
    </div>
  )
}
