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
/*
Copyright (C) 2026 LIghtJUNction
*/
import {
  ArrowRight01Icon,
  CheckmarkCircle02Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Textarea } from '@/components/ui/textarea'
import {
  developerAccessRequestQueryKey,
  getDeveloperAccessRequest,
  submitDeveloperAccessRequest,
  type DeveloperAccessRequest,
} from '@/features/onboarding/api'
import { getServerErrorMessageKey } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import type { AssistantL1RecommendationAction } from './api'

function AdministratorReply(props: { request: DeveloperAccessRequest }) {
  const { t } = useTranslation()
  if (!props.request.admin_note) return null
  return (
    <div className='border-border grid gap-1 border-l-2 py-1 pl-3'>
      <p className='text-xs font-medium'>{t('Administrator reply')}</p>
      <p className='text-muted-foreground text-xs leading-5 [overflow-wrap:anywhere] break-words whitespace-pre-wrap'>
        {props.request.admin_note}
      </p>
    </div>
  )
}

export function AssistantActivationTool(props: {
  recommendationDraft?: AssistantL1RecommendationAction | null
  onContinueSetup?: () => void
  onSubmitted?: (request: DeveloperAccessRequest) => void
  onDraftConsumed?: () => void
  onApproved?: () => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [loading, setLoading] = useState(false)
  const [manualReason, setManualReason] = useState('')
  const [manualRequestEditing, setManualRequestEditing] = useState(false)
  const [letter, setLetter] = useState<string | null>(null)
  const [draftEditSource, setDraftEditSource] = useState<'ai' | 'user'>('ai')
  const [pendingLetterEditing, setPendingLetterEditing] = useState(false)
  const [pendingLetterDraft, setPendingLetterDraft] = useState('')
  const [pendingEditSource, setPendingEditSource] = useState<'ai' | 'user'>(
    'user'
  )
  const initializedLetterKey = useRef('')
  const initializedRevisionKey = useRef('')

  const userId = useAuthStore((state) => state.auth.user?.id) ?? 0
  const requestQueryKey = developerAccessRequestQueryKey(userId)
  const requestQuery = useQuery({
    queryKey: requestQueryKey,
    queryFn: async () => {
      const request = await getDeveloperAccessRequest()
      return request ? { ...request } : request
    },
    initialData: null,
    staleTime: 0,
    retry: false,
    refetchInterval: (query) =>
      query.state.data?.status === 'pending' ? 15_000 : false,
  })
  const request = requestQuery.data ?? null

  useEffect(() => {
    if (request?.status === 'approved') {
      void queryClient.invalidateQueries({ queryKey: ['assistant-status'] })
      props.onApproved?.()
    }
  }, [props.onApproved, queryClient, request?.status])

  useEffect(() => {
    if (props.recommendationDraft) {
      const key = `draft:${props.recommendationDraft.confirmation_token}`
      if (initializedLetterKey.current !== key) {
        initializedLetterKey.current = key
        setLetter(props.recommendationDraft.recommendation)
        setDraftEditSource('ai')
      }
      if (
        request?.status === 'pending' &&
        initializedRevisionKey.current !== key
      ) {
        initializedRevisionKey.current = key
        setPendingLetterDraft(props.recommendationDraft.recommendation)
        setPendingEditSource('ai')
        setPendingLetterEditing(true)
      }
      return
    }
    if (request?.status === 'pending') {
      const key = `request:${request.id}`
      if (initializedLetterKey.current !== key) {
        initializedLetterKey.current = key
        setLetter(request.ai_recommendation)
      }
    }
  }, [props.recommendationDraft, request])

  const showSubmitError = (error: unknown, recoverManualEdit = false) => {
    const messageKey = getServerErrorMessageKey(error)
    if (messageKey && recoverManualEdit) {
      props.onDraftConsumed?.()
      setPendingEditSource('user')
    }
    toast.error(
      messageKey
        ? t(messageKey)
        : error instanceof Error
          ? error.message
          : t('Unable to submit unlock request')
    )
  }

  const submit = async () => {
    const draft = props.recommendationDraft
    if (loading || request?.status === 'pending' || !draft) return
    const recommendation = (letter ?? draft.recommendation).trim()
    if (recommendation.length < 20) return
    const confirmationToken =
      draftEditSource === 'ai' ? draft.confirmation_token : undefined
    setLoading(true)
    try {
      const submitted = await submitDeveloperAccessRequest({
        // Keep the user's concrete use case separate from the AI-written
        // letter. Administrators need the former as the durable request
        // rationale; editing the letter must never overwrite it.
        reason: draft.user_statement.trim(),
        ai_recommendation: recommendation,
        confirmation_token: confirmationToken,
        confirmed: true,
      })
      initializedRevisionKey.current = `draft:${draft.confirmation_token}`
      queryClient.setQueryData(requestQueryKey, submitted)
      props.onSubmitted?.(submitted)
      toast.success(t('Unlock request submitted'))
    } catch (error) {
      showSubmitError(error)
    } finally {
      setLoading(false)
    }
  }

  const savePendingLetter = async () => {
    if (!request || request.status !== 'pending' || loading) return
    const recommendation = pendingLetterDraft.trim()
    if (recommendation.length > 0 && recommendation.length < 20) return
    setLoading(true)
    try {
      const confirmationToken =
        pendingEditSource === 'ai'
          ? props.recommendationDraft?.confirmation_token
          : undefined
      const submitted = await submitDeveloperAccessRequest(
        recommendation
          ? {
              // Revisions change the shared letter only. Preserve the
              // original user rationale so an editor cannot accidentally
              // turn the queue's reason into recommendation prose.
              reason: request.reason,
              ai_recommendation: recommendation,
              confirmation_token: confirmationToken,
              confirmed: true,
            }
          : {
              reason: request.reason,
              confirmed: true,
            }
      )
      queryClient.setQueryData(requestQueryKey, submitted)
      setLetter(submitted.ai_recommendation)
      setPendingLetterEditing(false)
      if (confirmationToken) props.onDraftConsumed?.()
      setPendingEditSource('user')
      toast.success(t('Your changes were saved.'))
    } catch (error) {
      showSubmitError(error, true)
    } finally {
      setLoading(false)
    }
  }

  const submitWithoutAI = async () => {
    const reason = manualReason.trim()
    if (loading || props.recommendationDraft || reason.length < 5) return
    setLoading(true)
    try {
      const submitted = await submitDeveloperAccessRequest({
        reason,
        confirmed: true,
      })
      queryClient.setQueryData(requestQueryKey, submitted)
      props.onSubmitted?.(submitted)
      toast.success(t('Unlock request submitted'))
    } catch (error) {
      showSubmitError(error)
    } finally {
      setLoading(false)
    }
  }

  if (request?.status === 'approved') {
    return (
      <section className='grid gap-4 py-4'>
        <h3 className='flex items-center gap-2 text-sm font-medium'>
          <HugeiconsIcon
            icon={CheckmarkCircle02Icon}
            className='text-success size-4'
            strokeWidth={2}
            aria-hidden='true'
          />
          {t('L1 access approved')}
        </h3>
        <p className='text-muted-foreground text-sm leading-6'>
          {t(
            'Your developer access is active. Continue setup to create a key and connect your client.'
          )}
        </p>
        <AdministratorReply request={request} />
        {props.onContinueSetup ? (
          <Button type='button' onClick={props.onContinueSetup}>
            {t('Continue setup')}
            <HugeiconsIcon
              icon={ArrowRight01Icon}
              strokeWidth={2}
              data-icon='inline-end'
              aria-hidden='true'
            />
          </Button>
        ) : null}
      </section>
    )
  }

  if (request?.status === 'pending') {
    const recommendation = letter !== null ? letter : request.ai_recommendation
    const hasRecommendation = request.ai_recommendation.trim().length > 0
    if (!pendingLetterEditing) {
      return (
        <section
          className='flex min-w-0 items-center gap-2 py-2'
          data-testid='assistant-pending-recommendation'
        >
          <p className='text-muted-foreground min-w-0 flex-1 truncate text-xs'>
            {t(
              hasRecommendation
                ? 'AI recommendation submitted'
                : 'Access request submitted'
            )}
            <span aria-hidden='true'> · </span>
            {t('Pending review')}
          </p>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            className='shrink-0'
            aria-label={`${t('Edit')} ${t('Recommendation letter')}`}
            onClick={() => {
              setPendingLetterDraft(recommendation)
              setPendingEditSource('user')
              setPendingLetterEditing(true)
            }}
          >
            {t('Edit')}
          </Button>
        </section>
      )
    }

    return (
      <section
        className='grid min-w-0 gap-3 py-3'
        data-testid='assistant-pending-recommendation-editor'
      >
        <FieldGroup className='gap-3'>
          <Field>
            <FieldLabel
              htmlFor='assistant-pending-recommendation-letter'
              className='sr-only'
            >
              {t('Recommendation letter')}
            </FieldLabel>
            <Textarea
              id='assistant-pending-recommendation-letter'
              value={pendingLetterDraft}
              onChange={(event) => {
                setPendingLetterDraft(event.target.value)
                // An AI draft's one-time token authorizes confirming the exact
                // text prepared by the assistant. As soon as the user edits
                // that text, use the normal session-authenticated manual-edit
                // path instead of submitting a stale AI confirmation token.
                if (pendingEditSource === 'ai') setPendingEditSource('user')
              }}
              maxLength={2000}
              rows={4}
              className='focus-visible:border-foreground/30 min-h-28 w-full resize-y text-base focus-visible:ring-0 sm:text-sm'
              placeholder={t('Recommendation letter')}
            />
          </Field>
        </FieldGroup>
        <AdministratorReply request={request} />
        <div className='flex flex-wrap justify-end gap-2'>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            disabled={loading}
            onClick={() => setPendingLetterEditing(false)}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='button'
            size='sm'
            onClick={() => void savePendingLetter()}
            disabled={
              loading ||
              (pendingLetterDraft.trim().length > 0 &&
                pendingLetterDraft.trim().length < 20)
            }
          >
            {loading ? t('Submitting...') : t('Save changes')}
          </Button>
        </div>
      </section>
    )
  }

  const draft = props.recommendationDraft

  return (
    <section className='grid min-w-0 gap-3 py-3'>
      <h3 className='text-sm font-medium'>
        {draft ? t('Confirm AI recommendation') : t('Unlock L1 with AI')}
      </h3>
      <p className='text-muted-foreground text-xs leading-5'>
        {draft
          ? t(
              'Review what the AI will send. Nothing is submitted until you explicitly confirm.'
            )
          : t(
              'Continue chatting and explain your use case. When the AI has enough information, it will prepare a recommendation for your confirmation.'
            )}
      </p>
      {request?.status === 'rejected' ? (
        <div className='border-destructive/60 grid gap-2 border-l-2 py-1 pl-3'>
          <p className='text-sm font-medium'>
            {t('Previous request rejected')}
          </p>
          <AdministratorReply request={request} />
          {!draft ? (
            <p className='text-muted-foreground text-xs leading-5'>
              {t(
                'Continue the conversation and address the administrator feedback before asking the AI to prepare another recommendation.'
              )}
            </p>
          ) : null}
        </div>
      ) : null}
      {draft ? (
        <div className='grid min-w-0 gap-3'>
          <Textarea
            value={letter ?? draft.recommendation}
            onChange={(event) => {
              setLetter(event.target.value)
              if (draftEditSource === 'ai') setDraftEditSource('user')
            }}
            maxLength={2000}
            rows={5}
            className='focus-visible:border-foreground/30 min-h-36 w-full resize-y text-base focus-visible:ring-0 sm:text-sm'
            aria-label={t('Recommendation letter')}
          />
          <Button
            type='button'
            className='w-full sm:w-auto sm:justify-self-start'
            onClick={() => void submit()}
            disabled={
              loading || (letter ?? draft.recommendation).trim().length < 20
            }
          >
            {loading
              ? t('Submitting...')
              : t('Confirm and submit for review')}
          </Button>
        </div>
      ) : null}
      {!draft && manualRequestEditing ? (
        <div className='grid gap-3'>
          <Textarea
            value={manualReason}
            onChange={(event) => setManualReason(event.target.value)}
            placeholder={t(
              'Explain what you want to build and why you need L1 access.'
            )}
            maxLength={2000}
            rows={4}
            className='focus-visible:border-foreground/30 min-h-32 w-full resize-y text-base focus-visible:ring-0 sm:text-sm'
            aria-label={t('L1 access request explanation')}
            disabled={loading}
          />
          <p className='text-muted-foreground text-xs leading-5'>
            {t(
              'You can submit without an AI recommendation. The automatic review agent handles clear requests; uncertain cases remain available for human fallback.'
            )}
          </p>
          <Button
            type='button'
            variant='outline'
            className='w-full sm:w-auto sm:justify-self-start'
            onClick={() => void submitWithoutAI()}
            disabled={loading || manualReason.trim().length < 5}
          >
            {loading
              ? t('Submitting...')
              : t('Submit for review')}
          </Button>
        </div>
      ) : null}
      {!draft && !manualRequestEditing ? (
        <Button
          type='button'
          variant='link'
          size='sm'
          className='w-fit px-0'
          onClick={() => setManualRequestEditing(true)}
        >
          {t('Write request myself')}
        </Button>
      ) : null}
    </section>
  )
}
