/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  commitHoneypotUsers,
  getHoneypotOperation,
  getHoneypotPolicies,
  getHoneypotUser,
  HoneypotFailure,
  previewHoneypotUsers,
  userResultMatchesPreview,
  type HoneypotScope,
} from './api'
import type { HoneypotAction, HoneypotPreview, HoneypotUser } from './contract'
import { HoneypotFeedback } from './feedback'

const actionLabels: Record<HoneypotAction, string> = {
  mark: 'Mark for honeypot',
  unmark: 'Remove honeypot mark',
  convert_disabled: 'Convert disabled users to honeypot',
}
async function readSelectedUsers(
  ids: number[],
  scope: HoneypotScope
): Promise<HoneypotUser[]> {
  if (
    !ids.length ||
    ids.length > 200 ||
    new Set(ids).size !== ids.length ||
    ids.some((id) => !Number.isSafeInteger(id) || id <= 0)
  ) {
    throw new HoneypotFailure('invalid')
  }
  const users: HoneypotUser[] = []
  // Bound transport fan-out; a legacy table selection carries no native revisions.
  for (let i = 0; i < ids.length; i += 8) {
    users.push(
      ...(await Promise.all(
        ids.slice(i, i + 8).map((id) => getHoneypotUser(id, scope))
      ))
    )
  }
  if (users.some((user, index) => user.user_id !== ids[index])) {
    throw new HoneypotFailure('invalid')
  }
  return users
}

export function HoneypotBulkActionButton(props: {
  ids: number[]
  onApplied?: () => void
  className?: string
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const sessionID = useAuthStore((state) => state.auth.session?.sid)
  const [selected, setSelected] = useState<number[]>()
  if (user?.role !== ROLE.SUPER_ADMIN || !sessionID) return null
  return (
    <>
      <Button
        type='button'
        variant='outline'
        size='sm'
        className={props.className}
        disabled={!props.ids.length || props.ids.length > 200}
        onClick={() => setSelected([...props.ids])}
      >
        {t('Manage honeypot marks')}
      </Button>
      {selected && (
        <HoneypotBulkDialog
          key={`${user.id}:${sessionID}`}
          ids={selected}
          scope={{ userId: user.id, sessionId: sessionID }}
          onClose={() => setSelected(undefined)}
          onApplied={props.onApplied}
        />
      )}
    </>
  )
}

export function HoneypotBulkDialog(props: {
  ids: number[]
  scope: HoneypotScope
  onClose: () => void
  onApplied?: () => void
}) {
  const { t } = useTranslation()
  const [action, setAction] = useState<HoneypotAction>('mark')
  const [policyID, setPolicyID] = useState('')
  const [reason, setReason] = useState('')
  const [preview, setPreview] = useState<HoneypotPreview>()
  const [unknown, setUnknown] = useState(false)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<unknown>()
  const [applied, setApplied] = useState(false)
  const state = useQuery({
    queryKey: [
      'honeypot-selected',
      props.scope.userId,
      props.scope.sessionId,
      props.ids,
    ],
    queryFn: async () => ({
      policies: await getHoneypotPolicies(props.scope),
      users: await readSelectedUsers(props.ids, props.scope),
    }),
    retry: false,
    refetchOnWindowFocus: false,
    staleTime: 0,
  })
  const selectedPolicy = state.data?.policies.config.policies.find(
    (policy) => policy.id === policyID
  )
  const allowedTargets =
    state.data?.users.every(
      (user) =>
        user.platform_role === 'user' &&
        user.user_id !== props.scope.userId &&
        (action === 'convert_disabled'
          ? !user.active
          : action === 'mark'
            ? user.active
            : true)
    ) === true
  const valid =
    allowedTargets &&
    reason.trim().length > 0 &&
    (action === 'unmark' || selectedPolicy !== undefined)
  const blocked = pending || unknown

  async function review() {
    if (!state.data || !valid || blocked) return
    setPending(true)
    setError(undefined)
    try {
      setPreview(
        await previewHoneypotUsers(
          {
            operation_id: `web-users-${crypto.randomUUID()}`,
            reason: reason.trim(),
            action,
            ...(action === 'unmark' ? {} : { policy_id: policyID }),
            expected_policy_revision: state.data.policies.revision,
            targets: state.data.users.map((user) => ({
              user_id: user.user_id,
              expected_revision: user.revision,
            })),
          },
          props.scope
        )
      )
    } catch (caught) {
      setError(caught)
    } finally {
      setPending(false)
    }
  }
  function complete() {
    setUnknown(false)
    setApplied(true)
    setError(undefined)
    props.onApplied?.()
  }
  async function apply() {
    if (!preview || blocked) return
    if (Date.now() >= preview.expires_at * 1000) {
      setError(new HoneypotFailure('expired'))
      setPreview(undefined)
      return
    }
    setPending(true)
    setError(undefined)
    try {
      await commitHoneypotUsers(preview, props.scope)
      complete()
    } catch (caught) {
      setError(caught)
      if (caught instanceof HoneypotFailure && caught.kind === 'unknown') {
        setUnknown(true)
      } else setPreview(undefined)
    } finally {
      setPending(false)
    }
  }
  async function checkOriginal() {
    if (!preview || !unknown || pending) return
    setPending(true)
    try {
      const result = await getHoneypotOperation(
        preview.operation_id,
        props.scope
      )
      if (!userResultMatchesPreview(result, preview)) {
        throw new HoneypotFailure('unknown', preview.operation_id)
      }
      complete()
    } catch (caught) {
      setError(caught)
    } finally {
      setPending(false)
    }
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !blocked) props.onClose()
      }}
    >
      <DialogContent className='max-h-[90dvh] overflow-y-auto sm:max-w-2xl'>
        <DialogHeader>
          <DialogTitle>{t('Manage honeypot marks')}</DialogTitle>
          <DialogDescription>
            {t(
              'Only selected accounts use these policies. No users are selected automatically.'
            )}
          </DialogDescription>
        </DialogHeader>
        <p className='text-sm break-all'>
          {t('Selected user IDs')}: {props.ids.join(', ')}
        </p>
        <HoneypotFeedback
          error={error || state.error}
          operationID={unknown ? preview?.operation_id : undefined}
          check={unknown ? () => void checkOriginal() : undefined}
          checking={pending}
        />
        {state.isPending && <p role='status'>{t('Loading...')}</p>}
        {applied ? (
          <p role='status'>
            {t('Applied at revision {{revision}}', {
              revision: preview?.policy_revision,
            })}
          </p>
        ) : preview ? (
          <section className='space-y-3'>
            <h3 className='font-medium'>{t('Review selected users')}</h3>
            <p>
              {t(actionLabels[preview.action])} ·{' '}
              {selectedPolicy
                ? `${selectedPolicy.name} (${selectedPolicy.mode})`
                : '—'}
            </p>
            <p className='text-sm break-words'>{reason}</p>
            <p className='text-muted-foreground text-sm'>
              {t('Preview only; no account has changed.')}
            </p>
            {preview.action === 'convert_disabled' && (
              <p role='alert'>
                {t(
                  'This action reactivates disabled users with the selected honeypot policy.'
                )}
              </p>
            )}
            <ul className='max-h-64 space-y-2 overflow-y-auto text-sm'>
              {preview.targets.map((target) => (
                <li key={target.user_id} className='border-b pb-2 break-all'>
                  #{target.user_id} · {target.username} ·{' '}
                  {t(target.before.marked ? 'Marked' : 'Normal')} →{' '}
                  {t(target.after.marked ? 'Marked' : 'Normal')} ·{' '}
                  {target.after.policy_id ?? '—'} ·{' '}
                  {t(target.after.active ? 'Enabled' : 'Disabled')}
                </li>
              ))}
            </ul>
          </section>
        ) : (
          state.data && (
            <fieldset disabled={blocked} className='space-y-4'>
              <Label className='flex flex-col items-stretch gap-2'>
                {t('Action')}
                <NativeSelect
                  className='w-full'
                  value={action}
                  onChange={(event) => {
                    setAction(event.target.value as HoneypotAction)
                    setError(undefined)
                  }}
                >
                  {(Object.keys(actionLabels) as HoneypotAction[]).map(
                    (key) => (
                      <NativeSelectOption key={key} value={key}>
                        {t(actionLabels[key])}
                      </NativeSelectOption>
                    )
                  )}
                </NativeSelect>
              </Label>
              {action !== 'unmark' && (
                <Label className='flex flex-col items-stretch gap-2'>
                  {t('Policy')}
                  <NativeSelect
                    className='w-full'
                    value={policyID}
                    onChange={(event) => setPolicyID(event.target.value)}
                  >
                    <NativeSelectOption value=''>
                      {t('Select policy')}
                    </NativeSelectOption>
                    {state.data.policies.config.policies.map((policy) => (
                      <NativeSelectOption key={policy.id} value={policy.id}>
                        {policy.name} ({policy.mode})
                      </NativeSelectOption>
                    ))}
                  </NativeSelect>
                </Label>
              )}
              <Label className='flex flex-col items-stretch gap-2'>
                {t('Audit reason')}
                <Textarea
                  value={reason}
                  maxLength={1000}
                  onChange={(event) => setReason(event.target.value)}
                />
              </Label>
              {!allowedTargets && (
                <p role='alert' className='text-destructive text-sm'>
                  {t(
                    'Configuration or selection is invalid. Check the fields and limits.'
                  )}
                </p>
              )}
              {action === 'convert_disabled' && (
                <p role='alert' className='text-sm'>
                  {t(
                    'This action reactivates disabled users with the selected honeypot policy.'
                  )}
                </p>
              )}
            </fieldset>
          )
        )}
        <DialogFooter>
          {!applied && !preview && (
            <Button
              type='button'
              disabled={blocked || !valid || state.isFetching}
              onClick={() => void review()}
            >
              {t('Review selected users')}
            </Button>
          )}
          {!applied && preview && (
            <>
              <Button
                type='button'
                disabled={blocked}
                onClick={() => void apply()}
              >
                {t('Apply reviewed change')}
              </Button>
              <Button
                type='button'
                variant='outline'
                disabled={blocked}
                onClick={() => setPreview(undefined)}
              >
                {t('Back to editing')}
              </Button>
            </>
          )}
          {!applied && !preview && (
            <Button
              type='button'
              variant='outline'
              disabled={blocked || state.isFetching}
              onClick={() => {
                setError(undefined)
                void state.refetch()
              }}
            >
              {t('Reload')}
            </Button>
          )}
          <Button
            type='button'
            variant='outline'
            disabled={blocked}
            onClick={props.onClose}
          >
            {t('Close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
