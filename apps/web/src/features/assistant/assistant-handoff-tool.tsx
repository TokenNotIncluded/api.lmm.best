/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import {
  Alert02Icon,
  CheckmarkCircle02Icon,
  MailSend01Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  Alert,
  AlertAction,
  AlertDescription,
  AlertTitle,
} from '@/components/ui/alert'
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
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Spinner } from '@/components/ui/spinner'
import { Textarea } from '@/components/ui/textarea'
import { useAuthStore } from '@/stores/auth-store'

import {
  getAssistantHandoff,
  submitAssistantHandoff,
  type AssistantHumanSupportAction,
} from './api'
import {
  createAssistantHandoffConfirmation,
  getAssistantHandoffConfirmationToken,
  isSameAssistantHandoffConfirmation,
  maxAssistantHandoffCharacters,
  minAssistantHandoffCharacters,
  type AssistantHandoffConfirmation,
} from './assistant-handoff-confirmation'

export function AssistantHandoffTool(props: {
  confirmationAction?: AssistantHumanSupportAction | null
  messageInputId?: string
  onTransfer?: () => Promise<boolean>
  transferring?: boolean
}) {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id ?? null)
  const sessionId = useAuthStore((state) => state.auth.session?.sid ?? null)
  if (props.onTransfer) {
    return (
      <Card size='sm'>
        <CardHeader>
          <CardTitle>{t('Human technical support')}</CardTitle>
          <CardDescription>
            {t(
              'An administrator can join this conversation after you request a transfer.'
            )}
          </CardDescription>
        </CardHeader>
        <CardContent className='grid gap-3'>
          {props.confirmationAction?.message ? (
            <p className='text-sm whitespace-pre-wrap'>
              {props.confirmationAction.message}
            </p>
          ) : null}
          <Button
            type='button'
            disabled={props.transferring}
            onClick={() => void props.onTransfer?.()}
          >
            {t('Transfer to human')}
          </Button>
        </CardContent>
      </Card>
    )
  }
  return (
    <AssistantHandoffToolContent
      key={JSON.stringify([userId, sessionId])}
      {...props}
      userId={userId}
      sessionId={sessionId}
    />
  )
}

function AssistantHandoffToolContent(props: {
  confirmationAction?: AssistantHumanSupportAction | null
  messageInputId?: string
  userId: number | null
  sessionId: string | null
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const messageInputId = props.messageInputId ?? 'assistant-handoff-message'
  const mountedRef = useRef(false)
  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])
  const [message, setMessage] = useState(
    props.confirmationAction?.message ?? ''
  )
  const [confirmation, setConfirmation] =
    useState<AssistantHandoffConfirmation | null>(null)
  const [submissionError, setSubmissionError] = useState<string | null>(null)
  const submissionRef = useRef(false)
  const [submitting, setSubmitting] = useState(false)
  const [inactiveConfirmationToken, setInactiveConfirmationToken] = useState<
    string | null
  >(null)
  const queryKey = ['assistant-handoff', props.userId, props.sessionId] as const
  const handoffQuery = useQuery({
    queryKey,
    queryFn: getAssistantHandoff,
    staleTime: 30_000,
    retry: false,
  })
  const current = handoffQuery.data
  const confirmationAction =
    props.confirmationAction?.confirmation_token === inactiveConfirmationToken
      ? undefined
      : props.confirmationAction
  const confirmationToken =
    getAssistantHandoffConfirmationToken(confirmationAction)
  const isPreparedAction = Boolean(confirmationToken)
  const preparedMessage = confirmationAction?.message
  const preparedToken = confirmationAction?.confirmation_token
  const latestPreparedTokenRef = useRef(preparedToken)
  useEffect(() => {
    latestPreparedTokenRef.current = preparedToken
    if (preparedMessage !== undefined) {
      setMessage(preparedMessage)
    }
    setConfirmation(null)
    setSubmissionError(null)
  }, [preparedMessage, preparedToken])
  const trimmedMessage = message.trim()
  const messageLength = [...trimmedMessage].length
  const messageTooShort =
    trimmedMessage.length > 0 && messageLength < minAssistantHandoffCharacters
  const messageTooLong = messageLength > maxAssistantHandoffCharacters
  const currentConfirmation = createAssistantHandoffConfirmation(
    message,
    confirmationAction
  )
  const reviewIsCurrent = isSameAssistantHandoffConfirmation(
    confirmation,
    currentConfirmation
  )

  const submit = async () => {
    // State alone cannot prevent two clicks before React commits a render.
    if (submissionRef.current || !confirmation || !reviewIsCurrent) return
    const submitted = confirmation
    const isCurrentSubmission = () => {
      const auth = useAuthStore.getState().auth
      return (
        mountedRef.current &&
        (auth.user?.id ?? null) === props.userId &&
        (auth.session?.sid ?? null) === props.sessionId
      )
    }
    if (!isCurrentSubmission()) return
    submissionRef.current = true
    setSubmitting(true)
    setSubmissionError(null)
    try {
      const result = await submitAssistantHandoff(
        submitted.message,
        submitted.confirmationToken
      )
      if (!isCurrentSubmission()) return
      // A status lookup started before submission must not overwrite the
      // newly created request. Keep one cache that later refreshes can update.
      await queryClient.cancelQueries({ queryKey, exact: true })
      if (!isCurrentSubmission()) return
      queryClient.setQueryData(queryKey, result)
      if (submitted.confirmationToken) {
        setInactiveConfirmationToken(submitted.confirmationToken)
      }
      // A newer prepared action may arrive while the request is in flight.
      if (latestPreparedTokenRef.current === preparedToken) {
        setMessage((current) =>
          current.trim() === submitted.message ? '' : current
        )
      }
      setConfirmation(null)
      toast.success(t('Your message was sent to an administrator'))
    } catch (error) {
      if (!isCurrentSubmission()) return
      const errorMessage =
        error instanceof Error ? error.message : t('Unable to contact support')
      setSubmissionError(errorMessage)
      toast.error(errorMessage)
    } finally {
      submissionRef.current = false
      if (isCurrentSubmission()) setSubmitting(false)
    }
  }

  if (current?.status === 'pending') {
    return (
      <Card size='sm'>
        <CardHeader>
          <CardTitle className='flex items-center gap-2'>
            <HugeiconsIcon
              icon={CheckmarkCircle02Icon}
              strokeWidth={2}
              aria-hidden='true'
            />
            {t('Administrator follow-up requested')}
          </CardTitle>
          <CardDescription>
            {t(
              'Your request is waiting in the administrator queue. You do not need to send it again.'
            )}
          </CardDescription>
        </CardHeader>
        <CardContent className='grid gap-3'>
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <Badge variant='outline'>{t('Pending')}</Badge>
            <Button
              type='button'
              variant='outline'
              size='sm'
              className='min-h-11 sm:min-h-8'
              onClick={() => void handoffQuery.refetch()}
              disabled={handoffQuery.isFetching}
            >
              {handoffQuery.isFetching ? (
                <Spinner data-icon='inline-start' />
              ) : null}
              {t('Refresh')}
            </Button>
          </div>
          {handoffQuery.isError ? (
            <p className='text-destructive text-sm' role='alert'>
              {t('Unable to check support request status')}
            </p>
          ) : null}
        </CardContent>
      </Card>
    )
  }

  return (
    <>
      <Card size='sm'>
        <CardHeader>
          <CardTitle>{t('Send a message to an administrator')}</CardTitle>
          <CardDescription>
            {t(
              'Describe the page, issue, and approximate time. Obvious secrets are removed before storage.'
            )}
          </CardDescription>
        </CardHeader>
        <CardContent className='grid gap-3'>
          {handoffQuery.isLoading ? (
            <div className='grid gap-2' aria-label={t('Loading...')}>
              <Skeleton className='h-4 w-40' />
              <Skeleton className='h-12 w-full' />
            </div>
          ) : null}
          {handoffQuery.isError ? (
            <Alert variant='destructive'>
              <HugeiconsIcon
                icon={Alert02Icon}
                strokeWidth={2}
                aria-hidden='true'
              />
              <AlertTitle>
                {t('Unable to check support request status')}
              </AlertTitle>
              <AlertDescription>
                {t(
                  'You can still review and send your message; the server prevents duplicate pending requests.'
                )}
              </AlertDescription>
              <AlertAction>
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  onClick={() => void handoffQuery.refetch()}
                  disabled={handoffQuery.isFetching}
                >
                  {handoffQuery.isFetching ? (
                    <Spinner data-icon='inline-start' />
                  ) : null}
                  {t('Retry')}
                </Button>
              </AlertAction>
            </Alert>
          ) : null}
          {current?.status === 'resolved' ? (
            <Alert>
              <HugeiconsIcon
                icon={CheckmarkCircle02Icon}
                strokeWidth={2}
                aria-hidden='true'
              />
              <AlertTitle>{t('Previous request resolved')}</AlertTitle>
              {current.admin_note ? (
                <AlertDescription className='whitespace-pre-wrap'>
                  {current.admin_note}
                </AlertDescription>
              ) : null}
            </Alert>
          ) : null}
          <div className='grid gap-1.5'>
            <Label htmlFor={isPreparedAction ? undefined : messageInputId}>
              {t('Issue description')}
            </Label>
            {isPreparedAction ? (
              <div
                className='bg-muted/40 rounded-lg border px-3 py-2.5 text-sm leading-6 whitespace-pre-wrap'
                aria-label={t('Issue description')}
              >
                {message}
              </div>
            ) : (
              <Textarea
                id={messageInputId}
                rows={4}
                maxLength={maxAssistantHandoffCharacters * 2}
                minLength={minAssistantHandoffCharacters}
                required
                aria-required='true'
                disabled={submitting}
                aria-invalid={messageTooShort || messageTooLong}
                aria-describedby={
                  messageTooShort
                    ? `${messageInputId}-hint`
                    : `${messageInputId}-length`
                }
                value={message}
                onChange={(event) => {
                  setMessage(event.target.value)
                  setConfirmation(null)
                  setSubmissionError(null)
                }}
                placeholder={t('What happened, where, and when?')}
              />
            )}
            {isPreparedAction ? (
              <Button
                type='button'
                size='sm'
                variant='outline'
                disabled={submitting}
                onClick={() => {
                  if (submissionRef.current || !confirmationToken) return
                  setInactiveConfirmationToken(confirmationToken)
                  setConfirmation(null)
                  setSubmissionError(null)
                }}
              >
                {t('Edit')}
              </Button>
            ) : (
              <p
                id={`${messageInputId}-length`}
                className={
                  messageTooLong
                    ? 'text-destructive text-sm'
                    : 'text-muted-foreground text-sm'
                }
                role={messageTooLong ? 'alert' : undefined}
              >
                {messageLength} / {maxAssistantHandoffCharacters}
              </p>
            )}
            {!isPreparedAction && messageTooShort ? (
              <p
                id={`${messageInputId}-hint`}
                className='text-destructive text-sm'
                role='alert'
              >
                {t('Support message must contain at least 5 characters.')}
              </p>
            ) : null}
          </div>
          <Button
            type='button'
            onClick={() => {
              if (submissionRef.current || !currentConfirmation) return
              setSubmissionError(null)
              setConfirmation(currentConfirmation)
            }}
            disabled={submitting || !currentConfirmation}
          >
            <HugeiconsIcon
              icon={MailSend01Icon}
              strokeWidth={2}
              data-icon='inline-start'
              aria-hidden='true'
            />
            {t('Review message')}
          </Button>
        </CardContent>
      </Card>

      <AlertDialog
        open={confirmation !== null}
        onOpenChange={(open) => {
          if (!open && !submissionRef.current) setConfirmation(null)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Send this message?')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                'The message will be stored for administrators. Do not include passwords, API keys, or session cookies.'
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <div
            className='bg-muted/40 max-h-60 overflow-y-auto rounded-lg border p-3 text-sm whitespace-pre-wrap'
            aria-label={t('Issue description')}
            data-testid='assistant-handoff-review-message'
          >
            {confirmation?.message}
          </div>
          {submissionError ? (
            <p className='text-destructive text-sm' role='alert'>
              {submissionError}
            </p>
          ) : null}
          <AlertDialogFooter>
            <AlertDialogCancel disabled={submitting}>
              {t('Cancel')}
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={(event) => {
                event.preventDefault()
                void submit()
              }}
              disabled={submitting || !reviewIsCurrent}
            >
              {submitting ? <Spinner data-icon='inline-start' /> : null}
              {t('Confirm and send')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
