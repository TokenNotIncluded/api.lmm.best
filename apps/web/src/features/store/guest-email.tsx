/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'

import { STORE_ACCESS_COPY as copy } from './access-copy'
import { StoreError } from './shared'
import type { useStoreGuestEmailVerification } from './use-store-guest-email'
import { isStoreEmail } from './utils'

export function StoreGuestEmailVerification({
  state,
  email,
}: {
  state: ReturnType<typeof useStoreGuestEmailVerification>
  email: string
}) {
  const { t } = useTranslation()
  if (!isStoreEmail(email)) return null
  return (
    <section className='space-y-3 rounded-lg border p-3'>
      <h3 className='text-sm font-semibold'>{t(copy.emailVerify)}</h3>
      <StoreError error={state.error} />
      {state.verified ? (
        <p role='status' className='text-sm'>
          {t(copy.emailVerified)}
        </p>
      ) : (
        <>
          <Button
            type='button'
            variant='outline'
            size='sm'
            disabled={state.busy || state.checking || state.seconds > 0}
            onClick={() => void state.send()}
          >
            {t(state.seconds > 0 ? copy.emailResend : copy.emailSend, {
              seconds: state.seconds,
            })}
          </Button>
          {state.challenge && (
            <FieldGroup className='gap-3'>
              <p role='status' className='text-muted-foreground text-xs'>
                {t(copy.emailSent, { email })}
              </p>
              <Field data-disabled={state.busy}>
                <FieldLabel htmlFor='store-guest-email-code'>
                  {t(copy.emailCode)}
                </FieldLabel>
                <Input
                  id='store-guest-email-code'
                  inputMode='numeric'
                  autoComplete='one-time-code'
                  pattern='[0-9]{6}'
                  maxLength={6}
                  value={state.code}
                  disabled={state.busy}
                  onChange={(event) => state.setCode(event.target.value)}
                />
              </Field>
              <Button
                type='button'
                className='self-start'
                disabled={state.busy || !/^[0-9]{6}$/.test(state.code)}
                onClick={() => void state.confirm()}
              >
                {t(copy.emailConfirm)}
              </Button>
            </FieldGroup>
          )}
        </>
      )}
    </section>
  )
}
