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
  Award01Icon,
  CancelCircleIcon,
  CustomerSupportIcon,
  GitPullRequestIcon,
  LinkSquare02Icon,
  Upload01Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Spinner } from '@/components/ui/spinner'
import { Textarea } from '@/components/ui/textarea'
import {
  listAcceptedBounties,
  rateBountyOwner,
  submitChallenge,
  withdrawChallenge,
} from '@/features/open-source-bounties/api'
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
import { BountyProgress } from '@/features/open-source-bounties/bounty-progress'
import type { BountyChallenge } from '@/features/open-source-bounties/types'
import { validateBountySubmissionLinks } from '@/features/open-source-bounties/validation'
import { formatQuota } from '@/lib/format'

const STATUS_KEYS: Record<string, string> = {
  accepted: 'Accepted',
  submitted: 'Submitted',
  approved: 'Approved',
  rejected: 'Rejected',
  withdrawn: 'Withdrawn',
  cancelled: 'Cancelled by publisher',
}

const DISPUTE_STATUS_KEYS: Record<string, string> = {
  open: 'Open',
  resolved_paid: 'Resolved and paid',
  resolved_denied: 'Resolved and denied',
}

function disputeTicketSearch(challenge: BountyChallenge) {
  return {
    category: 'bounty_dispute',
    referenceId: String(challenge.id),
  } as const
}

export function AcceptedChallengeList() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [submitTarget, setSubmitTarget] = useState<BountyChallenge | null>(null)
  const [submission, setSubmission] = useState({
    issueUrl: '',
    pullRequestUrl: '',
    submissionNote: '',
  })
  const [ratingTarget, setRatingTarget] = useState<BountyChallenge | null>(null)
  const [ratingScore, setRatingScore] = useState(5)
  const [ratingComment, setRatingComment] = useState('')

  const query = useQuery({
    queryKey: ['open-source-bounties', 'accepted'],
    queryFn: listAcceptedBounties,
  })
  const submitMutation = useMutation({
    mutationFn: async () => {
      if (!submitTarget) throw new Error('missing submission target')
      const error = validateBountySubmissionLinks({
        issueUrl: submission.issueUrl,
        pullRequestUrl: submission.pullRequestUrl,
      })
      if (error) throw new Error(t(error))
      return submitChallenge(submitTarget.project_id, {
        issue_url: submission.issueUrl.trim(),
        pull_request_url: submission.pullRequestUrl.trim(),
        submission_note: submission.submissionNote.trim(),
      })
    },
    onSuccess: async () => {
      setSubmitTarget(null)
      await queryClient.invalidateQueries({
        queryKey: ['open-source-bounties', 'accepted'],
      })
      toast.success(t('Bounty work submitted for review.'))
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Action failed'))
    },
  })
  const withdrawMutation = useMutation({
    mutationFn: withdrawChallenge,
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: ['open-source-bounties', 'accepted'],
      })
      toast.success(t('Challenge withdrawn.'))
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Action failed'))
    },
  })
  const ratingMutation = useMutation({
    mutationFn: async () => {
      if (!ratingTarget) throw new Error('missing rating target')
      if (
        ratingScore < 1 ||
        ratingScore > 5 ||
        ratingComment.trim().length < 2
      ) {
        throw new Error(t('A 1–5 score and public evaluation are required.'))
      }
      return rateBountyOwner(ratingTarget.id, {
        score: ratingScore,
        comment: ratingComment.trim(),
      })
    },
    onSuccess: async () => {
      setRatingTarget(null)
      setRatingScore(5)
      setRatingComment('')
      await queryClient.invalidateQueries({
        queryKey: ['open-source-bounties', 'accepted'],
      })
      toast.success(t('Publisher rating submitted.'))
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Action failed'))
    },
  })

  const openSubmit = (challenge: BountyChallenge) => {
    setSubmitTarget(challenge)
    setSubmission({
      issueUrl: challenge.issue_url || '',
      pullRequestUrl: challenge.pull_request_url || '',
      submissionNote: challenge.submission_note || '',
    })
  }

  const challenges = query.data ?? []

  return (
    <section className='border-foreground/30 mt-10 border-t pt-8'>
      <div className='mb-5 flex flex-wrap items-end justify-between gap-3'>
        <div>
          <p className='mb-2 text-xs font-bold uppercase'>
            {t('My challenges')}
          </p>
          <h2 className='font-serif text-3xl font-normal'>
            {t('Keep your delivery trail current.')}
          </h2>
        </div>
        <Link
          to='/challenges'
          className='border-foreground border-b pb-1 text-sm font-semibold'
        >
          {t('Browse challenges')}
        </Link>
      </div>

      {query.isLoading ? (
        <div className='grid gap-3 md:grid-cols-2'>
          <Skeleton className='bg-foreground/10 h-48 w-full' />
          <Skeleton className='bg-foreground/10 h-48 w-full' />
        </div>
      ) : query.isError ? (
        <div className='border-foreground/30 border px-5 py-6 text-sm'>
          <p className='mb-3'>{t('Unable to load challenges.')}</p>
          <Button variant='outline' onClick={() => void query.refetch()}>
            {t('Retry')}
          </Button>
        </div>
      ) : challenges.length === 0 ? (
        <div className='border-foreground/30 border px-5 py-8 text-sm'>
          {t('You have not accepted a challenge')}
        </div>
      ) : (
        <div className='grid gap-3 md:grid-cols-2'>
          {challenges.map((challenge) => {
            const actionable = challenge.status === 'accepted'
            const withdrawable =
              challenge.status === 'accepted' ||
              challenge.status === 'submitted'
            const hasDispute = challenge.dispute?.status === 'open'
            const canRate =
              (challenge.status === 'approved' ||
                challenge.status === 'rejected') &&
              challenge.contributor_rating_score === 0
            return (
              <article
                key={challenge.id}
                className='border-foreground/30 bg-background border p-5'
              >
                <div className='mb-4 flex items-start justify-between gap-3'>
                  <div>
                    <h3 className='font-serif text-2xl font-normal'>
                      {challenge.project_title || t('Bounty challenge')}
                    </h3>
                    <p className='mt-1 text-xs font-semibold uppercase'>
                      @{challenge.github_handle} ·{' '}
                      {t(STATUS_KEYS[challenge.status] ?? challenge.status)}
                    </p>
                  </div>
                  <span className='font-serif text-xl tabular-nums'>
                    {formatQuota(challenge.reward_quota)}
                    <span className='block text-xs font-normal'>
                      {t('API account balance')}
                    </span>
                  </span>
                </div>
                <BountyProgress challenge={challenge} />
                {challenge.repository_url ? (
                  <a
                    href={challenge.repository_url}
                    target='_blank'
                    rel='noreferrer'
                    className='mb-4 inline-flex items-center gap-1.5 text-sm underline'
                  >
                    {t('Repository')}
                    <HugeiconsIcon
                      icon={LinkSquare02Icon}
                      className='size-3'
                      strokeWidth={2}
                      aria-hidden='true'
                    />
                  </a>
                ) : null}
                {challenge.issue_url || challenge.pull_request_url ? (
                  <div className='mb-4 flex flex-wrap gap-3 text-sm'>
                    {challenge.issue_url ? (
                      <a
                        href={challenge.issue_url}
                        target='_blank'
                        rel='noreferrer'
                        className='inline-flex items-center gap-1.5 underline'
                      >
                        {t('Issue evidence')}
                        <HugeiconsIcon
                          icon={LinkSquare02Icon}
                          className='size-3'
                          strokeWidth={2}
                          aria-hidden='true'
                        />
                      </a>
                    ) : null}
                    {challenge.pull_request_url ? (
                      <a
                        href={challenge.pull_request_url}
                        target='_blank'
                        rel='noreferrer'
                        className='inline-flex items-center gap-1.5 underline'
                      >
                        <HugeiconsIcon
                          icon={GitPullRequestIcon}
                          className='size-3'
                          strokeWidth={2}
                          aria-hidden='true'
                        />
                        {t('Pull request')}
                      </a>
                    ) : null}
                  </div>
                ) : null}
                {challenge.dispute ? (
                  <div className='border-foreground/20 mb-4 border-y py-3 text-sm'>
                    <div className='flex flex-wrap items-center justify-between gap-2'>
                      <span className='font-semibold'>
                        {t('Dispute status')}
                      </span>
                      <Badge variant='outline'>
                        {t(
                          DISPUTE_STATUS_KEYS[challenge.dispute.status] ??
                            challenge.dispute.status
                        )}
                      </Badge>
                    </div>
                    {challenge.dispute.resolution ? (
                      <p className='mt-2 leading-6'>
                        {challenge.dispute.resolution}
                      </p>
                    ) : null}
                  </div>
                ) : null}
                <div className='flex flex-wrap gap-2'>
                  {actionable ? (
                    <Button
                      onClick={() => openSubmit(challenge)}
                      disabled={hasDispute}
                    >
                      <HugeiconsIcon
                        icon={Upload01Icon}
                        data-icon='inline-start'
                        strokeWidth={2}
                        aria-hidden='true'
                      />
                      {t('Submit work')}
                    </Button>
                  ) : null}
                  {canRate ? (
                    <Button
                      variant='outline'
                      onClick={() => setRatingTarget(challenge)}
                      disabled={ratingMutation.isPending}
                    >
                      <HugeiconsIcon
                        icon={Award01Icon}
                        data-icon='inline-start'
                        strokeWidth={2}
                        aria-hidden='true'
                      />
                      {t('Rate publisher')}
                    </Button>
                  ) : null}
                  {withdrawable ? (
                    <Button
                      variant='outline'
                      onClick={() => {
                        if (
                          window.confirm(
                            t('Withdraw this challenge and release its slot?')
                          )
                        ) {
                          withdrawMutation.mutate(challenge.id)
                        }
                      }}
                      disabled={hasDispute || withdrawMutation.isPending}
                    >
                      <HugeiconsIcon
                        icon={CancelCircleIcon}
                        data-icon='inline-start'
                        strokeWidth={2}
                        aria-hidden='true'
                      />
                      {t('Withdraw')}
                    </Button>
                  ) : null}
                  {challenge.status !== 'withdrawn' &&
                  challenge.status !== 'cancelled' &&
                  !challenge.dispute ? (
                    <Button
                      variant='outline'
                      render={
                        <Link
                          to='/support'
                          search={disputeTicketSearch(challenge)}
                        />
                      }
                    >
                      <HugeiconsIcon
                        icon={CustomerSupportIcon}
                        data-icon='inline-start'
                        strokeWidth={2}
                        aria-hidden='true'
                      />
                      {t('Submit dispute ticket')}
                    </Button>
                  ) : null}
                </div>
              </article>
            )
          })}
        </div>
      )}

      <Dialog
        open={Boolean(submitTarget)}
        onOpenChange={(open) => !open && setSubmitTarget(null)}
        title={t('Submit work')}
        description={t(
          'Provide a GitHub Issue URL, pull request URL, or both. The bounty publisher will review the completed work directly.'
        )}
        contentClassName='sm:max-w-lg'
        footer={
          <>
            <Button variant='outline' onClick={() => setSubmitTarget(null)}>
              {t('Cancel')}
            </Button>
            <Button
              onClick={() => submitMutation.mutate()}
              disabled={submitMutation.isPending}
            >
              {submitMutation.isPending ? t('Submitting...') : t('Submit work')}
            </Button>
          </>
        }
      >
        <div className='flex flex-col gap-4 py-2'>
          <div className='flex flex-col gap-2'>
            <Label htmlFor='contributor-issue-url'>
              {t('GitHub Issue URL')}
            </Label>
            <Input
              id='contributor-issue-url'
              value={submission.issueUrl}
              onChange={(event) =>
                setSubmission((current) => ({
                  ...current,
                  issueUrl: event.target.value,
                }))
              }
              placeholder='https://github.com/owner/repository/issues/123'
            />
          </div>
          <div className='flex flex-col gap-2'>
            <Label htmlFor='contributor-pr-url'>
              {t('GitHub pull request URL')}
            </Label>
            <Input
              id='contributor-pr-url'
              value={submission.pullRequestUrl}
              onChange={(event) =>
                setSubmission((current) => ({
                  ...current,
                  pullRequestUrl: event.target.value,
                }))
              }
              placeholder='https://github.com/owner/repository/pull/123'
            />
          </div>
          <div className='flex flex-col gap-2'>
            <Label htmlFor='contributor-completion-note'>
              {t('Completion note (optional)')}
            </Label>
            <Textarea
              id='contributor-completion-note'
              value={submission.submissionNote}
              onChange={(event) =>
                setSubmission((current) => ({
                  ...current,
                  submissionNote: event.target.value,
                }))
              }
              rows={5}
            />
          </div>
        </div>
      </Dialog>

      <Dialog
        open={Boolean(ratingTarget)}
        onOpenChange={(open) => !open && setRatingTarget(null)}
        title={t('Rate the publisher and verifier')}
        description={t(
          'Your score and public evaluation are visible to both sides and contribute to the publisher’s history.'
        )}
        contentClassName='sm:max-w-lg'
        footer={
          <>
            <Button
              variant='outline'
              onClick={() => setRatingTarget(null)}
              disabled={ratingMutation.isPending}
            >
              {t('Cancel')}
            </Button>
            <Button
              onClick={() => ratingMutation.mutate()}
              disabled={
                ratingMutation.isPending ||
                ratingScore < 1 ||
                ratingScore > 5 ||
                ratingComment.trim().length < 2
              }
            >
              {ratingMutation.isPending ? (
                <Spinner data-icon='inline-start' />
              ) : null}
              {t('Submit rating')}
            </Button>
          </>
        }
      >
        <div className='flex flex-col gap-4 py-2'>
          <div className='flex flex-col gap-2'>
            <Label htmlFor='contributor-publisher-score'>
              {t('Publisher score (1–5)')}
            </Label>
            <Input
              id='contributor-publisher-score'
              type='number'
              min={1}
              max={5}
              step={1}
              value={ratingScore}
              onChange={(event) => setRatingScore(Number(event.target.value))}
            />
          </div>
          <div className='flex flex-col gap-2'>
            <Label htmlFor='contributor-publisher-evaluation'>
              {t('Public publisher evaluation')}
            </Label>
            <Textarea
              id='contributor-publisher-evaluation'
              rows={4}
              value={ratingComment}
              onChange={(event) => setRatingComment(event.target.value)}
            />
          </div>
        </div>
      </Dialog>
    </section>
  )
}
