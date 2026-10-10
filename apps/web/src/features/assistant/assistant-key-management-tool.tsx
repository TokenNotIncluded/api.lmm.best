/*
Copyright (C) 2026 LIghtJUNction
*/
import { useQueryClient } from '@tanstack/react-query'
import { Check, KeyRound } from 'lucide-react'
import { useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'

import {
  AssistantRequestError,
  confirmAssistantKeyAction,
  type AssistantKeyManagementAction,
  type AssistantKeyManagementReceipt,
} from './api'
import { parseAssistantKeyManagementAction } from './assistant-key-management-contract'

type KeyManagementProps = {
  action: AssistantKeyManagementAction
  onCancelled: () => void
}

export function AssistantKeyManagementTool(props: KeyManagementProps) {
  const action = parseAssistantKeyManagementAction(props.action)
  // No unvalidated metadata, token, or unexpected secret reaches the DOM.
  if (!action) return null
  return (
    <PreparedKeyManagementTool
      key={action.confirmation_token}
      {...props}
      action={action}
    />
  )
}

function PreparedKeyManagementTool(props: KeyManagementProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const codeId = useId()
  const requestPending = useRef(false)
  const [submitting, setSubmitting] = useState(false)
  const [cancelled, setCancelled] = useState(false)
  const [twoFactorRequired, setTwoFactorRequired] = useState(
    props.action.two_factor_required
  )
  const [twoFactorCode, setTwoFactorCode] = useState('')
  const [receipt, setReceipt] = useState<AssistantKeyManagementReceipt | null>(
    null
  )
  const [failure, setFailure] = useState<
    'retry' | 'expired' | 'two-factor' | null
  >(null)
  const deleting = props.action.action === 'delete'

  const cancel = () => {
    if (requestPending.current) return
    setTwoFactorCode('')
    setCancelled(true)
    props.onCancelled()
  }

  const confirm = async () => {
    if (
      requestPending.current ||
      cancelled ||
      receipt ||
      failure === 'expired' ||
      (twoFactorRequired && !twoFactorCode.trim())
    ) {
      return
    }
    requestPending.current = true
    setSubmitting(true)
    setFailure(null)
    try {
      const result = await confirmAssistantKeyAction(
        props.action,
        twoFactorCode
      )
      setReceipt(result)
      void queryClient.invalidateQueries({ queryKey: ['keys'] })
      void queryClient.invalidateQueries({ queryKey: ['api-key', result.id] })
    } catch (error) {
      // Error payloads are not trusted display text and may contain secrets.
      const expired =
        error instanceof AssistantRequestError &&
        (error.code === 'ASSISTANT_KEY_ACTION_CONFIRMATION_INVALID' ||
          error.code === 'ASSISTANT_KEY_CONFIRMATION_INVALID')
      const invalidTwoFactor =
        error instanceof AssistantRequestError &&
        error.code === 'ASSISTANT_TWO_FACTOR_INVALID'
      if (invalidTwoFactor) setTwoFactorRequired(true)
      setFailure(
        expired ? 'expired' : invalidTwoFactor ? 'two-factor' : 'retry'
      )
    } finally {
      setTwoFactorCode('')
      requestPending.current = false
      setSubmitting(false)
    }
  }

  if (cancelled) return null
  if (receipt) {
    return (
      <Card size='sm' className='border-primary/30 bg-primary/5'>
        <CardHeader>
          <CardTitle className='flex items-center gap-2 text-sm'>
            <Check className='size-4' aria-hidden='true' />
            {receipt.action === 'delete'
              ? t('API key deleted')
              : t('API key disabled')}
          </CardTitle>
          <CardDescription role='status'>
            {receipt.name || t('Unnamed API key')} · ID {receipt.id} ·{' '}
            {t('Group')}: {receipt.group || t('Default')}
          </CardDescription>
        </CardHeader>
        <CardFooter>
          <Button type='button' variant='outline' onClick={cancel}>
            {t('Close')}
          </Button>
        </CardFooter>
      </Card>
    )
  }

  return (
    <Card size='sm' className='border-destructive/30 w-full'>
      <CardHeader>
        <CardTitle className='flex items-center gap-2 text-sm'>
          <KeyRound className='size-4' aria-hidden='true' />
          {deleting ? t('Delete API key') : t('Disable API key')}
        </CardTitle>
        <CardDescription>
          {props.action.token.name || t('Unnamed API key')} · ID{' '}
          {props.action.token.id} · {t('Group')}:{' '}
          {props.action.token.group || t('Default')}
        </CardDescription>
      </CardHeader>
      <CardContent className='grid gap-4'>
        <Alert variant='destructive'>
          <AlertTitle>{t('Confirmation required')}</AlertTitle>
          <AlertDescription>
            {deleting
              ? t(
                  'Deleting this API key is irreversible. Applications using it will no longer be able to send requests.'
                )
              : t(
                  'Disabling this API key stops future requests from applications using it. You can enable it again on the API keys page.'
                )}
          </AlertDescription>
        </Alert>
        {twoFactorRequired ? (
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor={codeId}>
                {t('Two-factor authentication code')}
              </FieldLabel>
              <Input
                id={codeId}
                type='password'
                autoComplete='one-time-code'
                maxLength={128}
                value={twoFactorCode}
                onChange={(event) => setTwoFactorCode(event.target.value)}
                disabled={submitting || failure === 'expired'}
              />
              <FieldDescription>
                {t(
                  'Enter the current code or a recovery code here. It is sent only to the confirmation endpoint and is never added to chat.'
                )}
              </FieldDescription>
            </Field>
          </FieldGroup>
        ) : null}
        {failure ? (
          <p role='alert' className='text-destructive text-sm'>
            {failure === 'expired'
              ? t(
                  'This confirmation is no longer valid. Ask the assistant to prepare the key action again.'
                )
              : failure === 'two-factor'
                ? t(
                    'Check your current authentication code or recovery code and try again.'
                  )
                : t(
                    'Unable to confirm the key action. Try again or ask the assistant to prepare it again.'
                  )}
          </p>
        ) : null}
      </CardContent>
      <CardFooter className='justify-end gap-2'>
        <Button
          type='button'
          variant='outline'
          onClick={cancel}
          disabled={submitting}
        >
          {t('Cancel')}
        </Button>
        <Button
          type='button'
          variant='destructive'
          onClick={() => void confirm()}
          disabled={
            submitting ||
            failure === 'expired' ||
            (twoFactorRequired && !twoFactorCode.trim())
          }
        >
          {submitting ? <Spinner data-icon='inline-start' /> : null}
          {deleting ? t('Confirm deletion') : t('Confirm disabling')}
        </Button>
      </CardFooter>
    </Card>
  )
}
