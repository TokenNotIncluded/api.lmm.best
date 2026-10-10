/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Switch } from '@/components/ui/switch'

import { STORE_ACCESS_COPY as copy } from './access-copy'
import type { StoreAccessSettings, StoreVisibility } from './access-types'

export function StoreProductAccessSettings({
  value,
  disabled = false,
  onChange,
}: {
  value: StoreAccessSettings
  disabled?: boolean
  onChange: (value: StoreAccessSettings) => void
}) {
  const { t } = useTranslation()
  const [confirmGuest, setConfirmGuest] = useState(false)
  const requiresAccount = value.visibility !== 'public'
  function setLogin(required: boolean) {
    if (!required && value.pickup_login_required) {
      setConfirmGuest(true)
      return
    }
    onChange({ ...value, purchase_login_required: required })
  }
  return (
    <FieldSet className='rounded-lg border p-4'>
      <FieldLegend variant='label'>{t(copy.title)}</FieldLegend>
      <FieldGroup className='gap-4'>
        <Field data-disabled={disabled}>
          <FieldLabel id='store-visibility-label'>
            {t(copy.visibility)}
          </FieldLabel>
          <RadioGroup
            aria-labelledby='store-visibility-label'
            value={value.visibility}
            disabled={disabled}
            onValueChange={(visibility) => {
              if (
                ['public', 'registered', 'private'].includes(String(visibility))
              ) {
                onChange({
                  ...value,
                  visibility: visibility as StoreVisibility,
                  ...(visibility !== 'public'
                    ? { purchase_login_required: true }
                    : {}),
                })
              }
            }}
          >
            {(['public', 'registered', 'private'] as const).map(
              (visibility) => (
                <Field key={visibility} orientation='horizontal'>
                  <RadioGroupItem
                    id={`store-visibility-${visibility}`}
                    value={visibility}
                  />
                  <FieldContent>
                    <FieldLabel htmlFor={`store-visibility-${visibility}`}>
                      {t(copy[visibility])}
                    </FieldLabel>
                    <FieldDescription className='text-xs'>
                      {t(copy[`${visibility}Help`])}
                    </FieldDescription>
                  </FieldContent>
                </Field>
              )
            )}
          </RadioGroup>
        </Field>
        <Field
          orientation='horizontal'
          data-disabled={disabled || requiresAccount}
        >
          <FieldContent>
            <FieldLabel htmlFor='store-purchase-login'>
              {t(copy.login)}
            </FieldLabel>
            <FieldDescription className='text-xs'>
              {t(
                requiresAccount
                  ? copy[`${value.visibility}Help`]
                  : copy.loginHelp
              )}
            </FieldDescription>
          </FieldContent>
          <Switch
            id='store-purchase-login'
            checked={requiresAccount || value.purchase_login_required}
            disabled={disabled || requiresAccount}
            onCheckedChange={setLogin}
          />
        </Field>
      </FieldGroup>
      <AlertDialog open={confirmGuest} onOpenChange={setConfirmGuest}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(copy.conflictTitle)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(copy.conflictHelp)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={disabled}>
              {t('Cancel')}
            </AlertDialogCancel>
            <AlertDialogAction
              disabled={disabled}
              onClick={() => {
                onChange({
                  ...value,
                  purchase_login_required: false,
                  pickup_login_required: false,
                })
                setConfirmGuest(false)
              }}
            >
              {t(copy.conflictConfirm)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </FieldSet>
  )
}
