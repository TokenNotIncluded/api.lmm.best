/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Field, FieldDescription, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { useAuthStore } from '@/stores/auth-store'

import {
  metaDelegationAPI,
  metaDelegationQuota,
  type MetaDelegationInput,
  type MetaDelegationTarget,
} from './meta-delegation-api'
import { metaDelegationCopy } from './meta-delegation-copy'
import type { MetaDelegationSetup } from './meta-delegation-setup'

// Render inside the connection-creation form. Enabling this requires the
// explicit existing invoke/manage checkboxes; it never changes those silently.
export function MetaDelegationSetupFields({
  value,
  onChange,
  permitted,
  disabled = false,
}: {
  value: MetaDelegationSetup
  onChange: (value: MetaDelegationSetup) => void
  permitted: boolean
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const quotaID = useId()
  const quotaHelpID = `${quotaID}-help`
  const quota = metaDelegationQuota(value.quota)
  return (
    <div className='space-y-3'>
      <label className='focus-within:ring-ring flex min-h-11 cursor-pointer items-center gap-3 rounded-md px-2 text-sm focus-within:ring-2'>
        <input
          type='checkbox'
          className='accent-foreground size-4'
          checked={value.enabled && permitted}
          disabled={disabled || !permitted}
          onChange={(event) =>
            onChange({ ...value, enabled: event.target.checked })
          }
        />
        {t(metaDelegationCopy.title)}
      </label>
      {!permitted ? (
        <p className='text-muted-foreground text-sm'>
          {t(metaDelegationCopy.permissions)}
        </p>
      ) : (
        <>
          <p className='text-muted-foreground text-sm'>
            {t(metaDelegationCopy.help)}
          </p>
          {value.enabled && (
            <Field>
              <FieldLabel htmlFor={quotaID}>
                {t(metaDelegationCopy.budget)}
              </FieldLabel>
              <Input
                id={quotaID}
                value={value.quota}
                inputMode='numeric'
                maxLength={16}
                disabled={disabled}
                aria-invalid={quota === undefined}
                aria-describedby={quotaHelpID}
                className='min-h-11 text-base sm:text-sm'
                onChange={(event) =>
                  onChange({ ...value, quota: event.target.value })
                }
              />
              <FieldDescription id={quotaHelpID}>
                {quota === undefined
                  ? t(metaDelegationCopy.invalid, {
                      max: Number.MAX_SAFE_INTEGER,
                    })
                  : t(metaDelegationCopy.free)}
              </FieldDescription>
            </Field>
          )}
        </>
      )}
    </div>
  )
}

// Remount private drafts/query identity on an account or connection change.
export function MetaDelegationSettings({
  target,
  permitted,
}: {
  target: MetaDelegationTarget
  permitted: boolean
}) {
  const userID = useAuthStore((state) => state.auth.user?.id)
  if (!userID) return null
  return (
    <MetaDelegationSettingsInner
      key={`${userID}:${target.kind}:${target.id}`}
      userID={userID}
      target={target}
      permitted={permitted}
    />
  )
}

function MetaDelegationSettingsInner({
  userID,
  target,
  permitted,
}: {
  userID: number
  target: MetaDelegationTarget
  permitted: boolean
}) {
  const { t } = useTranslation()
  const cache = useQueryClient()
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState<MetaDelegationSetup>()
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState<string>()
  const queryKey = ['market-meta-delegation', userID, target.kind, target.id]
  const query = useQuery({
    queryKey,
    queryFn: () => metaDelegationAPI.get(target),
    enabled: open && permitted,
    retry: false,
  })
  const value = draft ?? {
    enabled: query.data?.enabled ?? false,
    quota: String(query.data?.max_total_quota ?? 0),
  }
  const quota = metaDelegationQuota(value.quota)
  return (
    <>
      <Button
        type='button'
        variant='outline'
        size='sm'
        className='min-h-11'
        onClick={() => {
          setDraft(undefined)
          setMessage(undefined)
          setOpen(true)
        }}
      >
        {t(metaDelegationCopy.title)}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(metaDelegationCopy.title)}</DialogTitle>
            <DialogDescription>{t(metaDelegationCopy.help)}</DialogDescription>
          </DialogHeader>
          <MetaDelegationSetupFields
            value={value}
            onChange={(next) => {
              setDraft(next)
              setMessage(undefined)
            }}
            permitted={permitted}
            disabled={saving || query.isPending || query.isError}
          />
          <p className='text-muted-foreground text-sm'>
            {t(metaDelegationCopy.budgets)}
          </p>
          {(query.isError || message) && (
            <p role='status' className='text-sm'>
              {message ?? t(metaDelegationCopy.failed)}
            </p>
          )}
          {query.isError && (
            <Button
              type='button'
              variant='outline'
              onClick={() => query.refetch()}
            >
              {t('Retry')}
            </Button>
          )}
          <Button
            type='button'
            className='min-h-11'
            disabled={
              !permitted ||
              saving ||
              query.isPending ||
              query.isError ||
              (value.enabled && quota === undefined)
            }
            onClick={async () => {
              if (
                useAuthStore.getState().auth.user?.id !== userID ||
                saving ||
                (value.enabled && quota === undefined)
              ) {
                return
              }
              const input: MetaDelegationInput = value.enabled
                ? { enabled: true, max_total_quota: quota ?? 0, expires_at: 0 }
                : { enabled: false, max_total_quota: 0, expires_at: 0 }
              setSaving(true)
              setMessage(undefined)
              try {
                const saved = await metaDelegationAPI.set(target, input)
                if (useAuthStore.getState().auth.user?.id !== userID) return
                cache.setQueryData(queryKey, saved)
                await cache.invalidateQueries({
                  queryKey: ['tool-market', userID, 'budgets'],
                })
                if (useAuthStore.getState().auth.user?.id !== userID) return
                setDraft({
                  enabled: saved.enabled,
                  quota: String(saved.max_total_quota),
                })
                setMessage(
                  saved.enabled && saved.max_total_quota !== quota
                    ? t(metaDelegationCopy.tighter, {
                        limit: saved.max_total_quota,
                      })
                    : t(metaDelegationCopy.saved)
                )
              } catch {
                if (useAuthStore.getState().auth.user?.id === userID) {
                  setMessage(t(metaDelegationCopy.failed))
                }
              } finally {
                setSaving(false)
              }
            }}
          >
            {t('Save')}
          </Button>
        </DialogContent>
      </Dialog>
    </>
  )
}
