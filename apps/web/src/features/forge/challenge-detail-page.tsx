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
  ArrowLeft01Icon,
  ArrowRight01Icon,
  CheckmarkCircle02Icon,
  GitPullRequestIcon,
  LinkSquare02Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { type ReactNode, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Spinner } from '@/components/ui/spinner'
import { getChallengeAcceptanceState } from '@/features/open-source-bounties/acceptance'
import {
  acceptBounty,
  getBountyDetail,
} from '@/features/open-source-bounties/api'
import { useStatus } from '@/hooks/use-status'
import { toIntlLocale } from '@/i18n/languages'
import { getBackendCapabilities } from '@/lib/backend-capabilities'
import { formatQuota } from '@/lib/format'
import { useAuthStore } from '@/stores/auth-store'

import { ForgePublicShell } from './forge-public-shell'

type ChallengeDetailPageProps = {
  challengeId: number
}

function repositoryName(url: string): string {
  try {
    const parsed = new URL(url)
    return (
      parsed.pathname.replace(/^\//, '').replace(/\.git$/, '') || parsed.host
    )
  } catch {
    return url
  }
}

export function ChallengeDetailPage(props: ChallengeDetailPageProps) {
  const { i18n, t } = useTranslation()
  const queryClient = useQueryClient()
  const user = useAuthStore((state) => state.auth.user)
  const { status, capabilitiesReady } = useStatus()
  const canReadPublicBounties =
    getBackendCapabilities(status).bounty_public_read
  const [acceptOpen, setAcceptOpen] = useState(false)
  const [githubHandle, setGithubHandle] = useState('')
  const queryKey = ['open-source-bounties', 'detail', props.challengeId]
  const query = useQuery({
    queryKey,
    queryFn: () => getBountyDetail(props.challengeId),
    enabled:
      props.challengeId > 0 &&
      (Boolean(user) || (capabilitiesReady && canReadPublicBounties)),
  })
  const mutation = useMutation({
    mutationFn: () => acceptBounty(props.challengeId, githubHandle.trim()),
    onSuccess: async () => {
      setAcceptOpen(false)
      await Promise.all([
        queryClient.invalidateQueries({ queryKey }),
        queryClient.invalidateQueries({ queryKey: ['forge-challenges'] }),
      ])
      toast.success(t('Challenge accepted.'))
    },
    onError: (error) =>
      toast.error(
        error instanceof Error
          ? error.message
          : t('Unable to accept this challenge.')
      ),
  })

  if (query.isLoading) {
    return (
      <ForgePublicShell>
        <main className='mx-auto flex max-w-6xl flex-col gap-5 px-5 pt-32 pb-24 md:px-10'>
          <Skeleton className='h-12 w-2/3' />
          <Skeleton className='h-48 w-full' />
        </main>
      </ForgePublicShell>
    )
  }

  const detail = query.data
  if (!detail) {
    return (
      <ForgePublicShell>
        <main className='mx-auto max-w-4xl px-5 pt-32 pb-24 md:px-10'>
          <h1 className='mb-4 font-serif text-4xl'>
            {t('Challenge not found')}
          </h1>
          <Button variant='outline' render={<Link to='/challenges' />}>
            <HugeiconsIcon
              icon={ArrowLeft01Icon}
              data-icon='inline-start'
              strokeWidth={2}
              aria-hidden='true'
            />
            {t('Browse challenges')}
          </Button>
        </main>
      </ForgePublicShell>
    )
  }

  const project = detail.project
  const acceptanceState = getChallengeAcceptanceState(project.viewer_challenge)
  const canAccept =
    project.status === 'published' &&
    (acceptanceState === 'available' || acceptanceState === 'retryable')
  const developerAccessGranted = user?.developer_access_granted === true
  const isRetry = acceptanceState === 'retryable'
  const dateFormatter = new Intl.DateTimeFormat(toIntlLocale(i18n.language), {
    dateStyle: 'medium',
    timeStyle: 'short',
  })
  let acceptanceAction: ReactNode
  if (acceptanceState === 'active' || acceptanceState === 'completed') {
    let statusLabel = 'Accepted'
    if (project.viewer_challenge?.status === 'submitted') {
      statusLabel = 'Submitted'
    } else if (project.viewer_challenge?.status === 'approved') {
      statusLabel = 'Approved'
    }
    acceptanceAction = (
      <div className='border-foreground flex items-center gap-2 border px-4 py-3 text-sm font-semibold'>
        <HugeiconsIcon
          icon={CheckmarkCircle02Icon}
          className='size-4'
          strokeWidth={2}
          aria-hidden='true'
        />
        {t(statusLabel)}
      </div>
    )
  } else if (!canAccept) {
    acceptanceAction = (
      <div className='border-foreground border px-4 py-3 text-sm font-semibold'>
        {t(project.status === 'paused' ? 'Paused' : project.status)}
      </div>
    )
  } else if (user && !developerAccessGranted) {
    acceptanceAction = (
      <div className='border-foreground grid gap-3 border px-4 py-3'>
        <p className='text-sm font-semibold'>
          {t(
            'L0 accounts can browse challenges and ask the AI assistant to request L1 access.'
          )}
        </p>
        <Button
          variant='outline'
          size='sm'
          className='w-full'
          render={<Link to='/getting-started' />}
        >
          {t('Start with AI assistant')}
          <HugeiconsIcon
            icon={ArrowRight01Icon}
            data-icon='inline-end'
            strokeWidth={2}
            aria-hidden='true'
          />
        </Button>
      </div>
    )
  } else if (user) {
    acceptanceAction = (
      <Button
        className='bg-primary text-primary-foreground hover:bg-primary/85 w-full rounded-sm'
        onClick={() => setAcceptOpen(true)}
      >
        {t(isRetry ? 'Retry challenge' : 'Accept challenge')}
      </Button>
    )
  } else {
    acceptanceAction = (
      <Button
        className='bg-primary text-primary-foreground hover:bg-primary/85 w-full rounded-sm'
        render={
          <Link
            to='/sign-in'
            search={{ redirect: `/challenges/${project.id}` }}
          />
        }
      >
        {t('Sign in to accept')}
      </Button>
    )
  }

  return (
    <ForgePublicShell>
      <main className='bg-background text-foreground pt-16'>
        <section className='border-foreground text-foreground bg-accent border-y'>
          <div className='mx-auto grid max-w-7xl gap-10 px-5 py-14 md:grid-cols-[minmax(0,1fr)_300px] md:px-10 md:py-20'>
            <div>
              <Link
                to='/challenges'
                className='border-foreground mb-8 inline-flex items-center gap-2 border-b pb-1 text-xs font-bold uppercase'
              >
                <HugeiconsIcon
                  icon={ArrowLeft01Icon}
                  className='size-4'
                  strokeWidth={2}
                  aria-hidden='true'
                />
                {t('All challenges')}
              </Link>
              <p className='mb-4 text-sm font-semibold'>
                {repositoryName(project.repository_url)}
              </p>
              <h1 className='mb-6 max-w-4xl font-serif text-4xl leading-tight font-normal md:text-6xl'>
                {project.title}
              </h1>
              <p className='max-w-3xl text-base leading-7 md:text-lg'>
                {project.description}
              </p>
            </div>
            <aside className='border-foreground border-t-2 pt-5 md:border-t-0 md:border-l-2 md:pt-0 md:pl-7'>
              <p className='text-xs font-bold uppercase'>{t('Reward')}</p>
              <p className='my-3 font-serif text-4xl tabular-nums'>
                {formatQuota(project.net_reward_quota || project.reward_quota)}
              </p>
              <p className='mb-7 text-sm'>{t('per approved delivery')}</p>
              {acceptanceAction}
            </aside>
          </div>
        </section>

        <section className='mx-auto grid max-w-7xl gap-14 px-5 py-16 md:grid-cols-[minmax(0,1fr)_360px] md:px-10 md:py-24'>
          <div>
            <h2 className='mb-7 font-serif text-3xl font-normal'>
              {t('Acceptance rules')}
            </h2>
            <div className='border-foreground border-t-2 py-6 text-sm leading-7 whitespace-pre-wrap'>
              {project.rules}
            </div>
            <a
              href={project.repository_url}
              target='_blank'
              rel='noreferrer'
              className='border-foreground inline-flex items-center gap-2 border-b pb-1 text-sm font-semibold'
            >
              {t('Open repository')}
              <HugeiconsIcon
                icon={LinkSquare02Icon}
                className='size-4'
                strokeWidth={2}
                aria-hidden='true'
              />
            </a>
          </div>
          <div>
            <h2 className='mb-7 font-serif text-3xl font-normal'>
              {t('Delivery evidence')}
            </h2>
            <div className='border-foreground border-t-2'>
              {detail.challenges.length === 0 && (
                <p className='py-6 text-sm'>{t('No delivery evidence yet.')}</p>
              )}
              {detail.challenges.map((challenge) => (
                <article
                  key={challenge.id}
                  className='border-foreground/25 border-b py-5'
                >
                  <div className='mb-3 flex items-center justify-between gap-4'>
                    <span className='text-sm font-semibold'>
                      @{challenge.github_handle}
                    </span>
                    <span className='text-xs font-bold uppercase'>
                      {t(challenge.status)}
                    </span>
                  </div>
                  <div className='flex flex-wrap gap-4 text-sm'>
                    {challenge.issue_url && (
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
                    )}
                    {challenge.pull_request_url && (
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
                    )}
                  </div>
                </article>
              ))}
            </div>
          </div>
        </section>

        <section className='border-foreground text-foreground bg-secondary border-t'>
          <div className='mx-auto max-w-7xl px-5 py-16 md:px-10 md:py-20'>
            <h2 className='mb-7 font-serif text-3xl font-normal'>
              {t('Settlement ledger')}
            </h2>
            <div className='border-foreground border-t-2'>
              {detail.ledger.length === 0 && (
                <p className='py-6 text-sm'>{t('No settlement events yet.')}</p>
              )}
              {detail.ledger.map((entry) => (
                <div
                  key={entry.id}
                  className='border-foreground/30 grid gap-2 border-b py-5 sm:grid-cols-[150px_minmax(0,1fr)_140px] sm:items-center'
                >
                  <span className='text-xs font-bold uppercase'>
                    {entry.kind.replaceAll('_', ' ')}
                  </span>
                  <span className='text-sm'>
                    {entry.note || t('Recorded event')}
                  </span>
                  <span className='text-xs tabular-nums sm:text-right'>
                    {entry.quota ? formatQuota(entry.quota) : ''}
                    {entry.created_at > 0 && (
                      <span className='block opacity-70'>
                        {dateFormatter.format(
                          new Date(entry.created_at * 1000)
                        )}
                      </span>
                    )}
                  </span>
                </div>
              ))}
            </div>
          </div>
        </section>
      </main>

      <Dialog
        open={acceptOpen}
        onOpenChange={setAcceptOpen}
        title={t(isRetry ? 'Retry challenge' : 'Accept challenge')}
        description={t('Your GitHub handle will be attached to this delivery.')}
        footer={
          <Button
            disabled={!githubHandle.trim() || mutation.isPending}
            onClick={() => mutation.mutate()}
          >
            {mutation.isPending && <Spinner data-icon='inline-start' />}
            {mutation.isPending ? t('Accepting...') : t('Confirm acceptance')}
          </Button>
        }
      >
        <div className='flex flex-col gap-3'>
          <Label htmlFor='github-handle'>{t('GitHub handle')}</Label>
          <Input
            id='github-handle'
            value={githubHandle}
            onChange={(event) => setGithubHandle(event.target.value)}
            placeholder='octocat'
            autoComplete='off'
          />
        </div>
      </Dialog>
    </ForgePublicShell>
  )
}
