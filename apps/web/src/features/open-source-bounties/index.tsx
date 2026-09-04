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
  Award01Icon,
  Bug01Icon,
  CancelCircleIcon,
  CheckmarkCircle02Icon,
  Copy01Icon,
  CustomerSupportIcon,
  Delete02Icon,
  ExternalLinkIcon,
  FileEditIcon,
  GiftIcon,
  GithubIcon,
  Loading03Icon,
  Megaphone01Icon,
  MoneyLockIcon,
  PauseIcon,
  PlayIcon,
  PlusSignIcon,
  SourceCodeIcon,
  Upload01Icon,
  UserAdd01Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Heart } from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Main } from '@/components/layout'
import {
  CardStaggerContainer,
  CardStaggerItem,
} from '@/components/page-transition'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Separator } from '@/components/ui/separator'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { TitledCard } from '@/components/ui/titled-card'
import { useStatus } from '@/hooks/use-status'
import { getSelf } from '@/lib/api'
import { getBackendCapabilities } from '@/lib/backend-capabilities'
import { copyToClipboard } from '@/lib/copy-to-clipboard'
import {
  formatQuota,
  parseQuotaFromDollars,
  quotaUnitsToDollars,
} from '@/lib/format'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import { getChallengeAcceptanceState } from './acceptance'
import {
  acceptBounty,
  archiveBounty,
  cancelChallenge,
  closeBounty,
  createBounty,
  deleteBounty,
  getBountyConfig,
  getBountyDetail,
  getMcpTokenStatus,
  getPendingBountyReviewCount,
  listAcceptedBounties,
  listAdminBountyDisputes,
  listBounties,
  listMyBountyDisputes,
  listOwnedBounties,
  pauseBounty,
  publishBounty,
  rateBountyOwner,
  resolveBountyDispute,
  revokeMcpToken,
  resumeBounty,
  reviewChallenge,
  rotateMcpToken,
  submitChallenge,
  tipChallenge,
  unarchiveBounty,
  updateBounty,
  withdrawChallenge,
} from './api'
import {
  getBountyLifecycleSummary,
  type BountyLifecycleSummary,
} from './lifecycle'
import {
  selectBountyNotificationChallenge,
  type BountyNotificationDetailTarget,
} from './notification-target'
import { PendingReviewSuperscript } from './pending-review-superscript'
import {
  getBountyDisputeEvidenceComparison,
  type BountyChallenge,
  type BountyDispute,
  type BountyDisputeEvidenceField,
  type BountyDraftInput,
  type BountyProject,
  type BountyProjectDetail,
} from './types'
import {
  type BountyCharge,
  type BountyDraftErrors,
  calculateBountyCharge,
  parseBountyNumericInput,
  validateBountyDraft,
  validateBountySubmissionLinks,
} from './validation'

const BOUNTY_QUERY_KEYS = [
  ['open-source-bounties'],
  ['open-source-bounties', 'mine'],
  ['open-source-bounties', 'accepted'],
  ['open-source-bounties', 'disputes'],
] as const

const BOUNTY_VIEW_TAB_CLASS =
  'h-auto min-h-11 w-full flex-none px-2 py-2 leading-tight whitespace-normal lg:min-h-7 lg:w-auto lg:whitespace-nowrap'

const STATUS_KEYS = {
  draft: 'Draft',
  published: 'Published',
  paused: 'Paused',
  completed: 'Completed',
  closed: 'Closed',
  accepted: 'Accepted',
  submitted: 'Submitted',
  approved: 'Approved',
  rejected: 'Rejected',
  withdrawn: 'Withdrawn',
  cancelled: 'Cancelled by publisher',
} as const

const CLOSE_BLOCKER_ERROR_CODES = new Set([
  'OPEN_SOURCE_BOUNTY_ACTIVE_CHALLENGES',
  'OPEN_SOURCE_BOUNTY_APPEAL_WINDOW',
  'OPEN_SOURCE_BOUNTY_OPEN_DISPUTES',
])

const ERROR_KEYS: Record<string, string> = {
  OPEN_SOURCE_BOUNTY_INVALID_REPOSITORY:
    'Enter a GitHub repository URL in the format https://github.com/owner/repository.',
  OPEN_SOURCE_BOUNTY_INVALID_TITLE:
    'Bounty title must contain 4 to 120 characters.',
  OPEN_SOURCE_BOUNTY_INVALID_DESCRIPTION:
    'Project and defect scope must contain 20 to 2000 characters.',
  OPEN_SOURCE_BOUNTY_INVALID_RULES:
    'Acceptance and verification rules must contain 20 to 5000 characters.',
  OPEN_SOURCE_BOUNTY_INVALID_QUOTA: 'Reward per fix must be greater than zero.',
  OPEN_SOURCE_BOUNTY_INVALID_FEE:
    'The platform fee leaves no contributor reward.',
  OPEN_SOURCE_BOUNTY_FEE_RECIPIENT_NOT_FOUND:
    'An enabled super administrator is required to receive the platform fee.',
  OPEN_SOURCE_BOUNTY_INVALID_SLOTS:
    'Reward slots must be a whole number between 1 and 100.',
  OPEN_SOURCE_BOUNTY_INSUFFICIENT_BALANCE:
    'Your balance is not enough to publish this bounty.',
  OPEN_SOURCE_BOUNTY_ACTIVE_CHALLENGES:
    'Cancel unsubmitted challenges or review submitted work before closing this bounty.',
  OPEN_SOURCE_BOUNTY_APPEAL_WINDOW:
    'Rejected challenges can still be appealed. Wait until the seven-day appeal window ends unless a dispute is opened.',
  OPEN_SOURCE_BOUNTY_OPEN_DISPUTES:
    'Resolve all open disputes before closing this bounty or refunding escrow.',
  OPEN_SOURCE_BOUNTY_FULL: 'All reward slots are currently occupied.',
  OPEN_SOURCE_BOUNTY_ALREADY_ACCEPTED:
    'You have already accepted this challenge.',
  OPEN_SOURCE_BOUNTY_RETRY_PENDING:
    "Wait until the rejected attempt's dispute is resolved or its seven-day appeal window ends.",
  OPEN_SOURCE_BOUNTY_EVIDENCE_REPOSITORY_MISMATCH:
    'Every submitted Issue or pull request must belong to the bounty repository.',
  OPEN_SOURCE_BOUNTY_EVIDENCE_REQUIRED:
    'Provide at least one GitHub Issue or pull request URL.',
  OPEN_SOURCE_BOUNTY_INVALID_EVIDENCE:
    'Enter a GitHub Issue URL ending in /issues/number or a pull request URL ending in /pull/number.',
  OPEN_SOURCE_BOUNTY_DUPLICATE_PULL_REQUEST:
    'This pull request has already been submitted.',
  OPEN_SOURCE_BOUNTY_ARCHIVE_UNAVAILABLE:
    'Only completed or closed projects can be archived.',
}

type DraftForm = {
  repositoryUrl: string
  title: string
  description: string
  rules: string
  rewardAmount: string
  rewardSlots: string
}

const EMPTY_DRAFT: DraftForm = {
  repositoryUrl: '',
  title: '',
  description: '',
  rules: '',
  rewardAmount: '',
  rewardSlots: '1',
}

function projectToDraft(project: BountyProject): DraftForm {
  return {
    repositoryUrl: project.repository_url,
    title: project.title,
    description: project.description,
    rules: project.rules,
    rewardAmount: String(quotaUnitsToDollars(project.reward_quota)),
    rewardSlots: String(project.reward_slots),
  }
}

function statusLabel(t: (key: string) => string, status: string) {
  return t(STATUS_KEYS[status as keyof typeof STATUS_KEYS] ?? status)
}

function useBountyLifecycle(project: BountyProject, hasOpenDispute = false) {
  const deadline = project.appeal_window_ends_at ?? 0
  const [nowSeconds, setNowSeconds] = useState(() =>
    Math.floor(Date.now() / 1000)
  )

  useEffect(() => {
    const delay = deadline * 1000 - Date.now()
    if (delay <= 0) return
    const timeout = window.setTimeout(
      () => setNowSeconds(Math.floor(Date.now() / 1000)),
      delay + 50
    )
    return () => window.clearTimeout(timeout)
  }, [deadline])

  return useMemo(
    () => getBountyLifecycleSummary(project, hasOpenDispute, nowSeconds),
    [hasOpenDispute, nowSeconds, project]
  )
}

function availableSlots(
  project: BountyProject,
  lifecycle?: BountyLifecycleSummary
) {
  const expiredAppealCount = lifecycle
    ? Math.max(
        0,
        (project.appealable_challenge_count ?? 0) - lifecycle.appealableCount
      )
    : 0
  return Math.max(
    0,
    project.reward_slots -
      Math.max(0, project.active_challenge_count - expiredAppealCount) -
      project.approved_challenge_count
  )
}

function disputeTicketSearch(challenge: BountyChallenge) {
  return {
    category: 'bounty_dispute',
    referenceId: String(challenge.id),
  } as const
}

export function OpenSourceBounties({
  detailTarget,
  onDetailTargetConsumed,
}: {
  detailTarget?: BountyNotificationDetailTarget
  onDetailTargetConsumed?: () => void
} = {}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const user = useAuthStore((state) => state.auth.user)
  const setUser = useAuthStore((state) => state.auth.setUser)
  const { status } = useStatus()
  const canCancelChallenge =
    getBackendCapabilities(status).bounty_challenge_cancel
  const [pending, setPending] = useState('')
  const [draftOpen, setDraftOpen] = useState(false)
  const [editingProject, setEditingProject] = useState<BountyProject | null>(
    null
  )
  const [draft, setDraft] = useState<DraftForm>(EMPTY_DRAFT)
  const [draftValidationAttempted, setDraftValidationAttempted] =
    useState(false)
  const [acceptProject, setAcceptProject] = useState<BountyProject | null>(null)
  const [githubHandle, setGithubHandle] = useState('')
  const [submitTarget, setSubmitTarget] = useState<{
    projectId: number
    challenge: BountyChallenge
  } | null>(null)
  const [submission, setSubmission] = useState({
    issueUrl: '',
    pullRequestUrl: '',
    submissionNote: '',
  })
  const [detail, setDetail] = useState<BountyProjectDetail | null>(null)
  const [detailOpen, setDetailOpen] = useState(false)
  const [focusedChallengeId, setFocusedChallengeId] = useState<number>()
  const [reviewTarget, setReviewTarget] = useState<{
    challenge: BountyChallenge
    action: 'approve' | 'reject'
  } | null>(null)
  const [reviewNote, setReviewNote] = useState('')
  const [reviewRatingScore, setReviewRatingScore] = useState(5)
  const [reviewRatingComment, setReviewRatingComment] = useState('')
  const [tipTarget, setTipTarget] = useState<BountyChallenge | null>(null)
  const [tipAmount, setTipAmount] = useState(0)
  const [tipNote, setTipNote] = useState('')
  const [ratingTarget, setRatingTarget] = useState<BountyChallenge | null>(null)
  const [ownerRatingScore, setOwnerRatingScore] = useState(5)
  const [ownerRatingComment, setOwnerRatingComment] = useState('')
  const [showArchivedOwned, setShowArchivedOwned] = useState(false)

  const bountyQuery = useQuery({
    queryKey: BOUNTY_QUERY_KEYS[0],
    queryFn: listBounties,
  })
  const ownedQuery = useQuery({
    queryKey: [...BOUNTY_QUERY_KEYS[1], 'active'],
    queryFn: () => listOwnedBounties(false),
  })
  const pendingReviewCountQuery = useQuery({
    queryKey: [...BOUNTY_QUERY_KEYS[1], 'pending-review-count'],
    queryFn: getPendingBountyReviewCount,
    retry: false,
  })
  const archivedOwnedQuery = useQuery({
    queryKey: [...BOUNTY_QUERY_KEYS[1], 'archived'],
    queryFn: () => listOwnedBounties(true),
  })
  const acceptedQuery = useQuery({
    queryKey: BOUNTY_QUERY_KEYS[2],
    queryFn: listAcceptedBounties,
  })
  const bountyRankByProjectId = useMemo(() => {
    const items = bountyQuery.data?.items ?? []
    const page = bountyQuery.data?.page ?? 1
    const pageSize = bountyQuery.data?.page_size ?? 50
    const firstRank = (page - 1) * pageSize + 1

    return new Map(
      items.map((project, index) => [project.id, firstRank + index] as const)
    )
  }, [bountyQuery.data])
  const disputesQuery = useQuery({
    queryKey: BOUNTY_QUERY_KEYS[3],
    queryFn: listMyBountyDisputes,
  })
  const isAdmin = (user?.role ?? 0) >= 10
  const isSuperAdmin = (user?.role ?? 0) >= 100
  const adminDisputesQuery = useQuery({
    queryKey: ['open-source-bounties', 'disputes', 'admin'],
    queryFn: listAdminBountyDisputes,
    enabled: isAdmin,
  })
  const configQuery = useQuery({
    queryKey: ['open-source-bounties', 'config'],
    queryFn: getBountyConfig,
  })

  const draftCharge = useMemo(() => {
    const reward = parseQuotaFromDollars(
      parseBountyNumericInput(draft.rewardAmount)
    )
    const feeRateBps = configQuery.data?.rate_basis_points ?? 0
    return calculateBountyCharge(
      reward,
      parseBountyNumericInput(draft.rewardSlots),
      feeRateBps
    )
  }, [
    configQuery.data?.rate_basis_points,
    draft.rewardAmount,
    draft.rewardSlots,
  ])
  const draftErrors = draftValidationAttempted ? validateBountyDraft(draft) : {}

  const refresh = async (balanceChanged = false) => {
    await Promise.all(
      BOUNTY_QUERY_KEYS.map((queryKey) =>
        queryClient.invalidateQueries({ queryKey })
      )
    )
    if (balanceChanged) {
      const response = await getSelf()
      if (response.success && response.data) setUser(response.data)
    }
  }

  const errorMessage = useCallback(
    (error: unknown) => {
      const code = (error as Error & { code?: string })?.code
      return t(
        (code && ERROR_KEYS[code]) || 'Unable to complete the bounty action.'
      )
    },
    [t]
  )

  const runAction = async (
    key: string,
    action: () => Promise<unknown>,
    successMessage: string,
    balanceChanged = false
  ) => {
    setPending(key)
    try {
      await action()
      await refresh(balanceChanged)
      toast.success(t(successMessage))
      return true
    } catch (error) {
      toast.error(errorMessage(error))
      const code = (error as Error & { code?: string })?.code
      if (code && CLOSE_BLOCKER_ERROR_CODES.has(code)) {
        try {
          await refresh()
        } catch {
          // Keep the original action error visible; the next manual refresh can
          // still recover the lifecycle summary.
        }
      }
      return false
    } finally {
      setPending('')
    }
  }

  const openCreateDialog = () => {
    setEditingProject(null)
    setDraft(EMPTY_DRAFT)
    setDraftValidationAttempted(false)
    setDraftOpen(true)
  }

  const openEditDialog = (project: BountyProject) => {
    setEditingProject(project)
    setDraft(projectToDraft(project))
    setDraftValidationAttempted(false)
    setDraftOpen(true)
  }

  const saveDraft = async () => {
    const validationErrors = validateBountyDraft(draft)
    setDraftValidationAttempted(true)
    const firstValidationError = Object.values(validationErrors)[0]
    if (firstValidationError) {
      toast.error(t(firstValidationError))
      return
    }
    const input: BountyDraftInput = {
      repository_url: draft.repositoryUrl.trim(),
      title: draft.title.trim(),
      description: draft.description.trim(),
      rules: draft.rules.trim(),
      reward_quota: parseQuotaFromDollars(
        parseBountyNumericInput(draft.rewardAmount)
      ),
      reward_slots: parseBountyNumericInput(draft.rewardSlots),
    }
    const success = await runAction(
      'save-draft',
      () =>
        editingProject
          ? updateBounty(editingProject.id, input)
          : createBounty(input),
      editingProject ? 'Bounty draft updated.' : 'Bounty draft created.'
    )
    if (success) setDraftOpen(false)
  }

  const openProjectDetail = async (projectId: number) => {
    setPending(`detail-${projectId}`)
    try {
      setDetail(await getBountyDetail(projectId))
      setDetailOpen(true)
    } catch (error) {
      toast.error(errorMessage(error))
    } finally {
      setPending('')
    }
  }

  useEffect(() => {
    if (!detailTarget) return
    let cancelled = false
    // eslint-disable-next-line react-hooks/set-state-in-effect -- an external notification target starts this async load.
    setPending(`detail-${detailTarget.projectId}`)
    void getBountyDetail(detailTarget.projectId)
      .then((nextDetail) => {
        if (cancelled) return
        const challenge = selectBountyNotificationChallenge(
          nextDetail,
          detailTarget
        )
        if (!challenge) {
          onDetailTargetConsumed?.()
          return
        }
        setDetail(nextDetail)
        setFocusedChallengeId(challenge.id)
        setDetailOpen(true)
      })
      .catch((error) => {
        if (!cancelled) {
          toast.error(errorMessage(error))
          onDetailTargetConsumed?.()
        }
      })
      .finally(() => {
        if (!cancelled) setPending('')
      })
    return () => {
      cancelled = true
    }
  }, [detailTarget, errorMessage, onDetailTargetConsumed])

  const handleCancelChallenge = async (challenge: BountyChallenge) => {
    if (!canCancelChallenge) return
    if (
      !window.confirm(
        t(
          'Cancel the unsubmitted challenge from @{{username}}? This releases its reward slot and cannot be undone.',
          { username: challenge.github_handle }
        )
      )
    ) {
      return
    }
    const success = await runAction(
      `cancel-${challenge.id}`,
      () => cancelChallenge(challenge.id),
      'Challenge cancelled and reward slot released.'
    )
    if (success) {
      setDetail(await getBountyDetail(challenge.project_id))
    }
  }

  const confirmPublication = (project: BountyProject) => {
    const charge = calculateBountyCharge(
      project.reward_quota,
      project.reward_slots,
      configQuery.data?.rate_basis_points ?? 0
    )
    if (
      !window.confirm(
        t(
          'Publish now? Your balance will be debited {{gross}} gross. The public {{rate}}% platform fee of {{fee}} is credited to the super administrator and helps fund AI customer-service token costs, leaving {{netReward}} per approved fix and {{escrow}} total escrow.',
          {
            gross: formatQuota(charge.gross),
            rate: charge.feeRatePercent,
            fee: formatQuota(charge.platformFee),
            netReward: formatQuota(charge.netReward),
            escrow: formatQuota(charge.escrow),
          }
        )
      )
    ) {
      return
    }
    void runAction(
      `publish-${project.id}`,
      () => publishBounty(project.id),
      'Bounty published and reward pool funded.',
      true
    )
  }

  const handleAccept = async () => {
    if (!acceptProject || githubHandle.trim().length < 1) return
    const success = await runAction(
      `accept-${acceptProject.id}`,
      () => acceptBounty(acceptProject.id, githubHandle.trim()),
      'Challenge accepted.'
    )
    if (success) {
      setAcceptProject(null)
      setGithubHandle('')
    }
  }

  const handleSubmit = async () => {
    if (!submitTarget) return
    const submissionLinkError = validateBountySubmissionLinks(submission)
    if (submissionLinkError) {
      toast.error(t(submissionLinkError))
      return
    }
    const success = await runAction(
      `submit-${submitTarget.challenge.id}`,
      () =>
        submitChallenge(submitTarget.projectId, {
          issue_url: submission.issueUrl.trim(),
          pull_request_url: submission.pullRequestUrl.trim(),
          submission_note: submission.submissionNote.trim(),
        }),
      'Bounty work submitted for review.'
    )
    if (success) {
      setSubmitTarget(null)
      setSubmission({
        issueUrl: '',
        pullRequestUrl: '',
        submissionNote: '',
      })
    }
  }

  const handleReview = async () => {
    if (!reviewTarget) return
    if (
      reviewRatingScore < 1 ||
      reviewRatingScore > 5 ||
      reviewRatingComment.trim().length < 2
    ) {
      toast.error(t('A 1–5 score and public evaluation are required.'))
      return
    }
    const { challenge, action } = reviewTarget
    const success = await runAction(
      `${action}-${challenge.id}`,
      () =>
        reviewChallenge(challenge.id, action, {
          review_note: reviewNote.trim(),
          rating_score: reviewRatingScore,
          rating_comment: reviewRatingComment.trim(),
        }),
      action === 'approve'
        ? 'Submission approved and reward transferred.'
        : 'Submission rejected.',
      action === 'approve'
    )
    if (success) {
      setReviewTarget(null)
      setReviewNote('')
      setReviewRatingScore(5)
      setReviewRatingComment('')
      setDetail(await getBountyDetail(challenge.project_id))
    }
  }

  const handleTip = async () => {
    if (!tipTarget) return
    const quota = parseQuotaFromDollars(tipAmount)
    if (quota <= 0) {
      toast.error(t('Enter a positive tip amount.'))
      return
    }
    const idempotencyKey = crypto.randomUUID()
    const success = await runAction(
      `tip-${tipTarget.id}`,
      () =>
        tipChallenge(
          tipTarget.id,
          { quota, note: tipNote.trim() },
          idempotencyKey
        ),
      'Tip sent to the contributor.',
      true
    )
    if (success) {
      setTipTarget(null)
      setTipAmount(0)
      setTipNote('')
      setDetail(await getBountyDetail(tipTarget.project_id))
    }
  }

  const handleRateOwner = async () => {
    if (!ratingTarget) return
    if (
      ownerRatingScore < 1 ||
      ownerRatingScore > 5 ||
      ownerRatingComment.trim().length < 2
    ) {
      toast.error(t('A 1–5 score and public evaluation are required.'))
      return
    }
    const success = await runAction(
      `rate-owner-${ratingTarget.id}`,
      () =>
        rateBountyOwner(ratingTarget.id, {
          score: ownerRatingScore,
          comment: ownerRatingComment.trim(),
        }),
      'Publisher rating submitted.'
    )
    if (success) {
      setRatingTarget(null)
      setOwnerRatingScore(5)
      setOwnerRatingComment('')
    }
  }

  const openSubmitDialog = (projectId: number, challenge: BountyChallenge) => {
    setSubmitTarget({ projectId, challenge })
    setSubmission({
      issueUrl: challenge.issue_url || '',
      pullRequestUrl: challenge.pull_request_url || '',
      submissionNote: challenge.submission_note || '',
    })
  }

  let bountyBoardContent: React.ReactNode
  if (bountyQuery.isLoading) {
    bountyBoardContent = <LoadingState label={t('Loading bounties...')} />
  } else if ((bountyQuery.data?.items.length ?? 0) === 0) {
    bountyBoardContent = (
      <Empty className='min-h-72 border'>
        <EmptyHeader>
          <EmptyMedia variant='icon'>
            <HugeiconsIcon icon={Megaphone01Icon} strokeWidth={2} />
          </EmptyMedia>
          <EmptyTitle>{t('No public bounty projects yet')}</EmptyTitle>
          <EmptyDescription>
            {t(
              'The board starts empty. Publish the first project by spending your own balance and funding its reward pool.'
            )}
          </EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <Button onClick={openCreateDialog}>{t('Create bounty')}</Button>
        </EmptyContent>
      </Empty>
    )
  } else {
    bountyBoardContent = (
      <div className='grid gap-4 lg:grid-cols-2'>
        {bountyQuery.data?.items.map((project, index) => (
          <BountyCard
            key={project.id}
            project={project}
            rank={
              ((bountyQuery.data?.page ?? 1) - 1) *
                (bountyQuery.data?.page_size ?? 50) +
              index +
              1
            }
            viewerUserId={user?.id ?? 0}
            pending={pending}
            onAccept={() => setAcceptProject(project)}
            onSubmit={(challenge) => openSubmitDialog(project.id, challenge)}
          />
        ))}
      </div>
    )
  }

  return (
    <Main>
      <div className='min-h-0 flex-1 [scrollbar-gutter:stable] overflow-x-hidden overflow-y-auto px-3 py-3 sm:px-4 sm:py-6'>
        <CardStaggerContainer className='mx-auto flex w-full max-w-7xl flex-col gap-4 [overflow-wrap:anywhere] sm:gap-6'>
          <CardStaggerItem>
            <div className='flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between'>
              <div className='flex items-start gap-3 sm:gap-4'>
                <div className='bg-primary/10 text-primary flex size-10 shrink-0 items-center justify-center rounded-none sm:size-12'>
                  <HugeiconsIcon
                    icon={Award01Icon}
                    strokeWidth={1.8}
                    className='size-5 sm:size-6'
                  />
                </div>
                <div className='min-w-0'>
                  <h1 className='text-xl font-bold tracking-tight sm:text-2xl'>
                    {t('Open-source bounties')}
                  </h1>
                  <p className='text-muted-foreground mt-1 max-w-3xl text-sm leading-relaxed'>
                    {t(
                      'Publish real bug-fix challenges, accept work, verify the fix, and transfer rewards from escrow.'
                    )}
                  </p>
                </div>
              </div>
              <Button onClick={openCreateDialog}>
                <HugeiconsIcon
                  icon={PlusSignIcon}
                  strokeWidth={2}
                  data-icon='inline-start'
                />
                {t('Create bounty')}
              </Button>
            </div>
          </CardStaggerItem>

          <CardStaggerItem>
            <Alert>
              <HugeiconsIcon icon={MoneyLockIcon} strokeWidth={2} />
              <AlertTitle>
                {t('Every publisher pays from their own balance')}
              </AlertTitle>
              <AlertDescription>
                <div className='flex flex-col gap-3'>
                  <p>
                    {t(
                      'Publishing deducts the gross listing total from your balance. The public administrator-configured platform fee is credited to the super administrator account and helps fund AI customer-service token costs; the remainder becomes contributor escrow. Publishers and contributors settle directly; administrators intervene only in disputes.'
                    )}
                  </p>
                  <div className='flex flex-wrap items-center gap-2'>
                    <Badge variant='secondary'>
                      {t('Public platform fee: {{rate}}%', {
                        rate: (
                          (configQuery.data?.rate_basis_points ?? 0) / 100
                        ).toFixed(2),
                      })}
                    </Badge>
                    {isSuperAdmin ? (
                      <Button
                        variant='outline'
                        size='sm'
                        render={<a href='/system-settings/billing/quota' />}
                      >
                        <HugeiconsIcon
                          icon={FileEditIcon}
                          strokeWidth={2}
                          data-icon='inline-start'
                        />
                        {t('Fee settings')}
                      </Button>
                    ) : null}
                  </div>
                </div>
              </AlertDescription>
            </Alert>
          </CardStaggerItem>

          <CardStaggerItem>
            <Tabs defaultValue='browse' className='min-w-0'>
              <TabsList
                aria-label={t('Open-source bounties')}
                className='grid w-full grid-cols-2 gap-1 p-1 group-data-horizontal/tabs:!h-auto sm:grid-cols-3 lg:flex lg:w-fit lg:max-w-full lg:flex-wrap lg:justify-start'
              >
                <TabsTrigger value='browse' className={BOUNTY_VIEW_TAB_CLASS}>
                  {t('Bounty board')}
                </TabsTrigger>
                <TabsTrigger value='owned' className={BOUNTY_VIEW_TAB_CLASS}>
                  {t('My bounty projects')}
                  <PendingReviewSuperscript
                    count={pendingReviewCountQuery.data ?? 0}
                    label={t('Pending review')}
                  />
                </TabsTrigger>
                <TabsTrigger value='accepted' className={BOUNTY_VIEW_TAB_CLASS}>
                  {t('My challenges')}
                </TabsTrigger>
                <TabsTrigger value='disputes' className={BOUNTY_VIEW_TAB_CLASS}>
                  {t('My disputes')}
                </TabsTrigger>
                {isAdmin ? (
                  <TabsTrigger
                    value='admin-disputes'
                    className={BOUNTY_VIEW_TAB_CLASS}
                  >
                    {t('Dispute cases')}
                  </TabsTrigger>
                ) : null}
                <TabsTrigger value='mcp' className={BOUNTY_VIEW_TAB_CLASS}>
                  {t('MCP automation')}
                </TabsTrigger>
                <TabsTrigger value='rules' className={BOUNTY_VIEW_TAB_CLASS}>
                  {t('Rules')}
                </TabsTrigger>
              </TabsList>

              <TabsContent value='browse' className='mt-3 sm:mt-4'>
                {bountyBoardContent}
              </TabsContent>

              <TabsContent value='owned' className='mt-3 sm:mt-4'>
                <div className='mb-4 flex flex-wrap items-center gap-2'>
                  <Button
                    variant={showArchivedOwned ? 'outline' : 'secondary'}
                    size='sm'
                    onClick={() => setShowArchivedOwned(false)}
                  >
                    {t('Active projects')}
                  </Button>
                  <Button
                    variant={showArchivedOwned ? 'secondary' : 'outline'}
                    size='sm'
                    onClick={() => setShowArchivedOwned(true)}
                  >
                    {t('Archived projects')} (
                    {archivedOwnedQuery.data?.length ?? 0})
                  </Button>
                  <span className='text-muted-foreground text-sm'>
                    {showArchivedOwned
                      ? t(
                          'Archived projects are hidden from your active list and can be restored.'
                        )
                      : t(
                          'Completed and closed projects stay here until you archive them.'
                        )}
                  </span>
                </div>
                {((showArchivedOwned
                  ? archivedOwnedQuery.data?.length
                  : ownedQuery.data?.length) ?? 0) === 0 ? (
                  <Empty className='min-h-72 border'>
                    <EmptyHeader>
                      <EmptyMedia variant='icon'>
                        <HugeiconsIcon icon={SourceCodeIcon} strokeWidth={2} />
                      </EmptyMedia>
                      <EmptyTitle>
                        {showArchivedOwned
                          ? t('You have no archived bounty projects')
                          : t('You have no active bounty projects')}
                      </EmptyTitle>
                      <EmptyDescription>
                        {t(
                          showArchivedOwned
                            ? 'Archived projects will appear here.'
                            : 'Create a draft, fund it from your balance, then publish it to the board.'
                        )}
                      </EmptyDescription>
                    </EmptyHeader>
                    <EmptyContent>
                      {!showArchivedOwned && (
                        <Button onClick={openCreateDialog}>
                          {t('Create bounty')}
                        </Button>
                      )}
                    </EmptyContent>
                  </Empty>
                ) : (
                  <div className='grid gap-4'>
                    {(showArchivedOwned
                      ? archivedOwnedQuery.data
                      : ownedQuery.data
                    )?.map((project) => (
                      <OwnerProjectCard
                        key={project.id}
                        project={project}
                        pending={pending}
                        hasOpenDispute={(disputesQuery.data ?? []).some(
                          (dispute) =>
                            dispute.project_id === project.id &&
                            dispute.status === 'open'
                        )}
                        onEdit={() => openEditDialog(project)}
                        onReview={() => openProjectDetail(project.id)}
                        onPublish={() => confirmPublication(project)}
                        onPause={() =>
                          runAction(
                            `pause-${project.id}`,
                            () => pauseBounty(project.id),
                            'Bounty paused.'
                          )
                        }
                        onResume={() =>
                          runAction(
                            `resume-${project.id}`,
                            () => resumeBounty(project.id),
                            'Bounty resumed.'
                          )
                        }
                        onClose={() =>
                          runAction(
                            `close-${project.id}`,
                            () => closeBounty(project.id),
                            'Bounty closed and unused escrow refunded.',
                            true
                          )
                        }
                        onArchive={() =>
                          runAction(
                            `archive-${project.id}`,
                            () => archiveBounty(project.id),
                            'Bounty archived.'
                          )
                        }
                        onUnarchive={() =>
                          runAction(
                            `unarchive-${project.id}`,
                            () => unarchiveBounty(project.id),
                            'Bounty restored.'
                          )
                        }
                        onDelete={() =>
                          runAction(
                            `delete-${project.id}`,
                            () => deleteBounty(project.id),
                            'Bounty draft deleted.'
                          )
                        }
                      />
                    ))}
                  </div>
                )}
              </TabsContent>

              <TabsContent value='accepted' className='mt-3 sm:mt-4'>
                {(acceptedQuery.data?.length ?? 0) === 0 ? (
                  <Empty className='min-h-72 border'>
                    <EmptyHeader>
                      <EmptyMedia variant='icon'>
                        <HugeiconsIcon icon={Bug01Icon} strokeWidth={2} />
                      </EmptyMedia>
                      <EmptyTitle>
                        {t('You have not accepted a challenge')}
                      </EmptyTitle>
                      <EmptyDescription>
                        {t(
                          'Accept an available bounty, fix a real defect, and submit the matching Issue or pull request.'
                        )}
                      </EmptyDescription>
                    </EmptyHeader>
                  </Empty>
                ) : (
                  <div className='grid gap-4 lg:grid-cols-2'>
                    {acceptedQuery.data?.map((challenge) => (
                      <ChallengeCard
                        key={challenge.id}
                        challenge={challenge}
                        rank={bountyRankByProjectId.get(challenge.project_id)}
                        pending={pending}
                        onSubmit={() =>
                          openSubmitDialog(challenge.project_id, challenge)
                        }
                        onWithdraw={() =>
                          runAction(
                            `withdraw-${challenge.id}`,
                            () => withdrawChallenge(challenge.id),
                            'Challenge withdrawn.'
                          )
                        }
                        onRateOwner={() => {
                          setRatingTarget(challenge)
                          setOwnerRatingScore(5)
                          setOwnerRatingComment('')
                        }}
                      />
                    ))}
                  </div>
                )}
              </TabsContent>

              <TabsContent value='disputes' className='mt-3 sm:mt-4'>
                <DisputesPanel
                  items={disputesQuery.data ?? []}
                  loading={disputesQuery.isLoading}
                />
              </TabsContent>

              {isAdmin ? (
                <TabsContent value='admin-disputes' className='mt-3 sm:mt-4'>
                  <DisputesPanel
                    items={adminDisputesQuery.data ?? []}
                    loading={adminDisputesQuery.isLoading}
                    admin
                  />
                </TabsContent>
              ) : null}

              <TabsContent value='mcp' className='mt-3 sm:mt-4'>
                <McpSettingsPanel />
              </TabsContent>

              <TabsContent value='rules' className='mt-3 sm:mt-4'>
                <RulesPanel />
              </TabsContent>
            </Tabs>
          </CardStaggerItem>
        </CardStaggerContainer>
      </div>

      <DraftDialog
        open={draftOpen}
        onOpenChange={setDraftOpen}
        editing={Boolean(editingProject)}
        draft={draft}
        setDraft={setDraft}
        errors={draftErrors}
        charge={draftCharge}
        availableQuota={user?.quota ?? 0}
        pending={pending === 'save-draft'}
        onSave={saveDraft}
      />

      <Dialog
        open={Boolean(acceptProject)}
        onOpenChange={(open) => !open && setAcceptProject(null)}
        title={t('Accept challenge')}
        description={t(
          'Reserve one reward slot and identify your GitHub account.'
        )}
        contentClassName='sm:max-w-md'
        footer={
          <>
            <Button variant='outline' onClick={() => setAcceptProject(null)}>
              {t('Cancel')}
            </Button>
            <Button
              onClick={handleAccept}
              disabled={!githubHandle.trim() || pending.startsWith('accept-')}
            >
              {t('Accept challenge')}
            </Button>
          </>
        }
      >
        <div className='flex flex-col gap-2 py-2'>
          <Label htmlFor='bounty-github-handle'>{t('GitHub handle')}</Label>
          <Input
            id='bounty-github-handle'
            value={githubHandle}
            onChange={(event) => setGithubHandle(event.target.value)}
            placeholder='@username'
          />
        </div>
      </Dialog>

      <SubmissionDialog
        target={submitTarget}
        onOpenChange={(open) => !open && setSubmitTarget(null)}
        submission={submission}
        setSubmission={setSubmission}
        pending={pending.startsWith('submit-')}
        onSubmit={handleSubmit}
      />

      <ProjectReviewDialog
        open={detailOpen}
        onOpenChange={(open) => {
          setDetailOpen(open)
          if (!open && focusedChallengeId) {
            setFocusedChallengeId(undefined)
            onDetailTargetConsumed?.()
          }
        }}
        detail={detail}
        focusedChallengeId={focusedChallengeId}
        pending={pending}
        onReview={(challenge, action) => {
          setReviewTarget({ challenge, action })
          setReviewNote('')
          setReviewRatingScore(5)
          setReviewRatingComment('')
        }}
        onTip={(challenge) => {
          setTipTarget(challenge)
          setTipAmount(0)
          setTipNote('')
        }}
        onCancel={handleCancelChallenge}
      />

      <Dialog
        open={Boolean(reviewTarget)}
        onOpenChange={(open) => !open && setReviewTarget(null)}
        title={
          reviewTarget?.action === 'approve'
            ? t('Approve and transfer reward')
            : t('Reject submission')
        }
        description={
          reviewTarget?.action === 'approve'
            ? t(
                'Approval transfers the locked reward directly to the contributor balance and cannot be repeated.'
              )
            : t('Rejection releases the reward slot for another contributor.')
        }
        contentClassName='sm:max-w-lg'
        footer={
          <>
            <Button variant='outline' onClick={() => setReviewTarget(null)}>
              {t('Cancel')}
            </Button>
            <Button
              variant={
                reviewTarget?.action === 'reject' ? 'destructive' : 'default'
              }
              onClick={handleReview}
              disabled={
                pending.startsWith('approve-') ||
                pending.startsWith('reject-') ||
                reviewRatingScore < 1 ||
                reviewRatingScore > 5 ||
                reviewRatingComment.trim().length < 2
              }
            >
              {reviewTarget?.action === 'approve'
                ? t('Approve and pay')
                : t('Reject')}
            </Button>
          </>
        }
      >
        <div className='flex flex-col gap-4 py-2'>
          <Field
            label={t('Contributor score (1–5)')}
            htmlFor='bounty-review-score'
          >
            <Input
              id='bounty-review-score'
              type='number'
              min={1}
              max={5}
              step={1}
              value={reviewRatingScore}
              onChange={(event) =>
                setReviewRatingScore(Number(event.target.value))
              }
            />
          </Field>
          <Field
            label={t('Public contributor evaluation')}
            htmlFor='bounty-review-rating-comment'
          >
            <Textarea
              id='bounty-review-rating-comment'
              rows={4}
              value={reviewRatingComment}
              onChange={(event) => setReviewRatingComment(event.target.value)}
            />
          </Field>
          <Field
            label={t('Review note (optional)')}
            htmlFor='bounty-review-note'
          >
            <Textarea
              id='bounty-review-note'
              rows={4}
              value={reviewNote}
              onChange={(event) => setReviewNote(event.target.value)}
            />
          </Field>
        </div>
      </Dialog>

      <Dialog
        open={Boolean(tipTarget)}
        onOpenChange={(open) => !open && setTipTarget(null)}
        title={t('Tip the contributor')}
        description={t(
          'Tips are immediate, non-refundable transfers from your own balance. They do not reduce escrow or replace the formal reward.'
        )}
        contentClassName='sm:max-w-lg'
        footer={
          <>
            <Button variant='outline' onClick={() => setTipTarget(null)}>
              {t('Cancel')}
            </Button>
            <Button
              onClick={handleTip}
              disabled={tipAmount <= 0 || pending.startsWith('tip-')}
            >
              <HugeiconsIcon
                icon={GiftIcon}
                strokeWidth={2}
                data-icon='inline-start'
              />
              {t('Send tip')}
            </Button>
          </>
        }
      >
        <div className='flex flex-col gap-4 py-2'>
          <Field label={t('Tip amount')} htmlFor='bounty-tip-amount'>
            <Input
              id='bounty-tip-amount'
              type='number'
              min={0}
              value={tipAmount}
              onChange={(event) => setTipAmount(Number(event.target.value))}
            />
          </Field>
          <Field label={t('Tip note (optional)')} htmlFor='bounty-tip-note'>
            <Textarea
              id='bounty-tip-note'
              rows={4}
              value={tipNote}
              onChange={(event) => setTipNote(event.target.value)}
            />
          </Field>
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
            <Button variant='outline' onClick={() => setRatingTarget(null)}>
              {t('Cancel')}
            </Button>
            <Button
              onClick={handleRateOwner}
              disabled={
                ownerRatingScore < 1 ||
                ownerRatingScore > 5 ||
                ownerRatingComment.trim().length < 2 ||
                pending.startsWith('rate-owner-')
              }
            >
              {t('Submit rating')}
            </Button>
          </>
        }
      >
        <div className='flex flex-col gap-4 py-2'>
          <Field
            label={t('Publisher score (1–5)')}
            htmlFor='bounty-owner-score'
          >
            <Input
              id='bounty-owner-score'
              type='number'
              min={1}
              max={5}
              step={1}
              value={ownerRatingScore}
              onChange={(event) =>
                setOwnerRatingScore(Number(event.target.value))
              }
            />
          </Field>
          <Field
            label={t('Public publisher evaluation')}
            htmlFor='bounty-owner-rating-comment'
          >
            <Textarea
              id='bounty-owner-rating-comment'
              rows={4}
              value={ownerRatingComment}
              onChange={(event) => setOwnerRatingComment(event.target.value)}
            />
          </Field>
        </div>
      </Dialog>
    </Main>
  )
}

function LoadingState({ label }: { label: string }) {
  return (
    <div className='text-muted-foreground flex min-h-64 items-center justify-center gap-2 text-sm'>
      <HugeiconsIcon
        icon={Loading03Icon}
        strokeWidth={2}
        className='size-5 animate-spin'
      />
      {label}
    </div>
  )
}

export function BountyCard({
  project,
  rank,
  viewerUserId,
  pending,
  onAccept,
  onSubmit,
}: {
  project: BountyProject
  rank: number
  viewerUserId: number
  pending: string
  onAccept: () => void
  onSubmit: (challenge: BountyChallenge) => void
}) {
  const { t } = useTranslation()
  const challenge = project.viewer_challenge
  const lifecycle = useBountyLifecycle(project)
  const acceptanceState = getChallengeAcceptanceState(challenge)
  const slots = availableSlots(project, lifecycle)
  let viewerAction: React.ReactNode
  if (project.owner_user_id === viewerUserId) {
    viewerAction = <Badge variant='secondary'>{t('Managed by you')}</Badge>
  } else if (challenge?.status === 'accepted') {
    viewerAction = (
      <Button onClick={() => onSubmit(challenge)}>
        <HugeiconsIcon
          icon={Upload01Icon}
          strokeWidth={2}
          data-icon='inline-start'
        />
        {t('Submit work')}
      </Button>
    )
  } else if (acceptanceState === 'active' || acceptanceState === 'completed') {
    viewerAction = (
      <Badge variant='outline'>
        {statusLabel(t, challenge?.status ?? 'accepted')}
      </Badge>
    )
  } else {
    viewerAction = (
      <Button
        onClick={onAccept}
        disabled={
          project.status !== 'published' || slots === 0 || pending !== ''
        }
      >
        <HugeiconsIcon
          icon={UserAdd01Icon}
          strokeWidth={2}
          data-icon='inline-start'
        />
        {t(
          acceptanceState === 'retryable'
            ? 'Retry challenge'
            : 'Accept challenge'
        )}
      </Button>
    )
  }
  return (
    <TitledCard
      title={project.title}
      description={project.owner_username}
      titleClassName='[overflow-wrap:anywhere]'
      descriptionClassName='[overflow-wrap:anywhere]'
      icon={<HugeiconsIcon icon={Bug01Icon} strokeWidth={1.8} />}
      iconTone='primary'
      action={
        <BountyStatusBar project={project} lifecycle={lifecycle} rank={rank} />
      }
      disableHoverEffect
      contentClassName='flex h-full flex-col gap-4'
    >
      <p className='text-muted-foreground text-sm leading-relaxed [overflow-wrap:anywhere] whitespace-pre-wrap'>
        {project.description}
      </p>
      <div className='grid grid-cols-2 gap-2 sm:grid-cols-5'>
        <Metric
          label={t('Reward per fix')}
          value={formatQuota(project.reward_quota)}
        />
        <Metric
          label={t('Locked reward')}
          value={formatQuota(project.net_reward_quota || project.reward_quota)}
        />
        <Metric
          label={t('Available slots')}
          value={`${slots}/${project.reward_slots}`}
        />
        <Metric
          label={t('Publisher reputation')}
          value={
            project.owner_rating_count > 0
              ? `${project.owner_rating_average.toFixed(1)}/5 · ${project.owner_rating_count}`
              : t('No ratings yet')
          }
        />
        <Metric
          label={t('Thanks received')}
          value={
            <span className='inline-flex items-center gap-1.5'>
              <Heart className='console-status-danger-text size-4 fill-current' />
              {project.owner_thank_heart_count ?? 0}
            </span>
          }
        />
      </div>
      <div className='mt-auto flex flex-wrap gap-2'>
        <Button
          variant='outline'
          render={
            <a href={project.repository_url} target='_blank' rel='noreferrer' />
          }
        >
          <HugeiconsIcon
            icon={GithubIcon}
            strokeWidth={2}
            data-icon='inline-start'
          />
          {t('Repository')}
          <HugeiconsIcon
            icon={ExternalLinkIcon}
            strokeWidth={2}
            data-icon='inline-end'
          />
        </Button>
        {viewerAction}
      </div>
    </TitledCard>
  )
}

function BountyRankBadge({ rank }: { rank: number }) {
  let className = 'border-border bg-background text-muted-foreground'
  if (rank === 1) {
    className = 'console-rank-gold'
  } else if (rank === 2) {
    className = 'console-rank-silver'
  } else if (rank === 3) {
    className = 'console-rank-bronze'
  }

  return (
    <Badge
      variant='outline'
      className={`h-7 min-w-10 px-2 font-mono text-xs font-semibold tabular-nums ${className}`}
    >
      #{rank}
    </Badge>
  )
}

function BountyStatusBar({
  project,
  lifecycle,
  rank,
}: {
  project: BountyProject
  lifecycle: BountyLifecycleSummary
  rank?: number
}) {
  const { t } = useTranslation()
  const items = [
    {
      key: 'participants',
      label: 'Participants',
      count: lifecycle.participantCount,
      className: 'console-status-info-badge',
      always: true,
    },
    {
      key: 'accepted',
      label: 'In progress',
      count: lifecycle.acceptedCount,
    },
    {
      key: 'submitted',
      label: 'Awaiting review',
      count: lifecycle.submittedCount,
      className: 'console-status-warning-badge',
    },
    {
      key: 'approved',
      label: 'Approved',
      count: lifecycle.approvedCount,
      className: 'console-status-success-badge',
    },
    {
      key: 'rejected',
      label: 'Rejected',
      count: lifecycle.rejectedCount,
      className: 'console-status-danger-badge',
    },
    {
      key: 'withdrawn',
      label: 'Withdrawn',
      count: lifecycle.withdrawnCount,
    },
    {
      key: 'cancelled',
      label: 'Cancelled',
      count: lifecycle.cancelledCount,
    },
    {
      key: 'appealable',
      label: 'In appeal window',
      count: lifecycle.appealableCount,
      className: 'console-status-warning-badge',
    },
    {
      key: 'disputes',
      label: 'Open disputes',
      count: lifecycle.openDisputeCount,
      className: 'console-status-danger-badge',
    },
  ]

  return (
    <div
      data-bounty-status-bar
      aria-label={t('Bounty status summary')}
      className='flex max-w-full flex-wrap items-center gap-1.5 sm:max-w-xl sm:justify-end'
    >
      <Badge variant='outline'>{statusLabel(t, project.status)}</Badge>
      {items
        .filter((item) => item.always || item.count > 0)
        .map((item) => (
          <Badge
            key={item.key}
            variant='outline'
            className={cn(
              'gap-1.5 whitespace-nowrap tabular-nums',
              item.className
            )}
            aria-label={`${t(item.label)}: ${item.count}`}
          >
            <span className='font-mono font-semibold'>{item.count}</span>
            <span>{t(item.label)}</span>
          </Badge>
        ))}
      {rank === undefined ? null : <BountyRankBadge rank={rank} />}
    </div>
  )
}

export function OwnerProjectCard(props: {
  project: BountyProject
  pending: string
  hasOpenDispute: boolean
  onEdit: () => void
  onReview: () => void
  onPublish: () => void
  onPause: () => void
  onResume: () => void
  onClose: () => void
  onArchive: () => void
  onUnarchive: () => void
  onDelete: () => void
}) {
  const { t } = useTranslation()
  const { project } = props
  const lifecycle = useBountyLifecycle(project, props.hasOpenDispute)
  const busy = props.pending !== ''
  const closeBlockerId = `bounty-close-blockers-${project.id}`
  const appealDeadline = lifecycle.appealWindowEndsAt
    ? new Date(lifecycle.appealWindowEndsAt * 1000).toLocaleString()
    : ''
  return (
    <TitledCard
      title={project.title}
      description={project.repository_url}
      titleClassName='[overflow-wrap:anywhere]'
      descriptionClassName='break-all'
      icon={<HugeiconsIcon icon={SourceCodeIcon} strokeWidth={1.8} />}
      iconTone='info'
      disableHoverEffect
      action={<BountyStatusBar project={project} lifecycle={lifecycle} />}
    >
      <div className='flex flex-col gap-4'>
        <div className='grid gap-2 sm:grid-cols-5'>
          <Metric
            label={t('Reward per fix')}
            value={formatQuota(project.reward_quota)}
          />
          <Metric
            label={t('Locked reward')}
            value={
              project.status === 'draft'
                ? t('Calculated at publish')
                : formatQuota(project.net_reward_quota || project.reward_quota)
            }
          />
          <Metric
            label={t('Escrow remaining')}
            value={formatQuota(project.escrow_quota)}
          />
          <Metric
            label={t('Platform task fee')}
            value={
              project.status === 'draft'
                ? t('Calculated at publish')
                : `${formatQuota(project.platform_fee_quota)} (${(
                    project.platform_fee_rate_bps / 100
                  ).toFixed(2)}%)`
            }
          />
          <Metric
            label={t('Challenges')}
            value={`${project.active_challenge_count} / ${project.approved_challenge_count}`}
          />
        </div>
        {lifecycle.closeBlocked ? (
          <Alert id={closeBlockerId}>
            <HugeiconsIcon icon={MoneyLockIcon} strokeWidth={2} />
            <AlertTitle>{t('Why closing is unavailable')}</AlertTitle>
            <AlertDescription>
              <div className='space-y-2'>
                <p>
                  {t(
                    'This bounty cannot be closed yet. Resolve the blockers below:'
                  )}
                </p>
                <ul className='list-disc space-y-1 pl-5'>
                  {lifecycle.acceptedCount > 0 ||
                  lifecycle.submittedCount > 0 ? (
                    <li>
                      {t(
                        'In progress: {{accepted}} · Awaiting review: {{submitted}}',
                        {
                          accepted: lifecycle.acceptedCount,
                          submitted: lifecycle.submittedCount,
                        }
                      )}
                    </li>
                  ) : null}
                  {lifecycle.appealableCount > 0 ? (
                    <li>
                      {t(
                        'Challenges still in the appeal window: {{count}}. Latest deadline: {{date}}.',
                        {
                          count: lifecycle.appealableCount,
                          date: appealDeadline,
                        }
                      )}
                    </li>
                  ) : null}
                  {lifecycle.openDisputeCount > 0 ? (
                    <li>
                      {t(
                        'Open disputes: {{count}}. A third-party administrator must resolve them before escrow can be refunded.',
                        { count: lifecycle.openDisputeCount }
                      )}
                    </li>
                  ) : null}
                  {lifecycle.hasUnknownActiveBlocker ? (
                    <li>
                      {t(
                        'Cancel unsubmitted challenges or review submitted work before closing this bounty.'
                      )}
                    </li>
                  ) : null}
                </ul>
              </div>
            </AlertDescription>
          </Alert>
        ) : null}
        <div className='flex flex-wrap gap-2'>
          {project.status === 'draft' && (
            <>
              <Button variant='outline' onClick={props.onEdit} disabled={busy}>
                <HugeiconsIcon
                  icon={FileEditIcon}
                  strokeWidth={2}
                  data-icon='inline-start'
                />
                {t('Edit')}
              </Button>
              <Button onClick={props.onPublish} disabled={busy}>
                <HugeiconsIcon
                  icon={PlayIcon}
                  strokeWidth={2}
                  data-icon='inline-start'
                />
                {t('Publish and fund')}
              </Button>
              <Button
                variant='destructive'
                onClick={props.onDelete}
                disabled={busy}
              >
                <HugeiconsIcon
                  icon={Delete02Icon}
                  strokeWidth={2}
                  data-icon='inline-start'
                />
                {t('Delete')}
              </Button>
            </>
          )}
          {(project.status === 'published' || project.status === 'paused') && (
            <>
              <Button
                variant='outline'
                onClick={props.onReview}
                disabled={busy}
              >
                <HugeiconsIcon
                  icon={UserAdd01Icon}
                  strokeWidth={2}
                  data-icon='inline-start'
                />
                {t('Challenges')}
              </Button>
              {project.status === 'published' ? (
                <Button
                  variant='outline'
                  onClick={props.onPause}
                  disabled={busy}
                >
                  <HugeiconsIcon
                    icon={PauseIcon}
                    strokeWidth={2}
                    data-icon='inline-start'
                  />
                  {t('Pause')}
                </Button>
              ) : (
                <Button
                  variant='outline'
                  onClick={props.onResume}
                  disabled={busy}
                >
                  <HugeiconsIcon
                    icon={PlayIcon}
                    strokeWidth={2}
                    data-icon='inline-start'
                  />
                  {t('Resume')}
                </Button>
              )}
              <Button
                variant='destructive'
                onClick={props.onClose}
                disabled={busy || lifecycle.closeBlocked}
                aria-describedby={
                  lifecycle.closeBlocked ? closeBlockerId : undefined
                }
              >
                <HugeiconsIcon
                  icon={CancelCircleIcon}
                  strokeWidth={2}
                  data-icon='inline-start'
                />
                {t('Close and refund escrow')}
              </Button>
            </>
          )}
          {(project.status === 'completed' || project.status === 'closed') && (
            <>
              <Button
                variant='outline'
                onClick={props.onReview}
                disabled={busy}
              >
                {t('View lifecycle')}
              </Button>
              {project.archived_at > 0 ? (
                <Button
                  variant='outline'
                  onClick={props.onUnarchive}
                  disabled={busy}
                >
                  {t('Restore from archive')}
                </Button>
              ) : (
                <Button
                  variant='outline'
                  onClick={props.onArchive}
                  disabled={busy}
                >
                  {t('Archive project')}
                </Button>
              )}
            </>
          )}
        </div>
      </div>
    </TitledCard>
  )
}

function ChallengeCard({
  challenge,
  rank,
  pending,
  onSubmit,
  onWithdraw,
  onRateOwner,
}: {
  challenge: BountyChallenge
  rank?: number
  pending: string
  onSubmit: () => void
  onWithdraw: () => void
  onRateOwner: () => void
}) {
  const { t } = useTranslation()
  const actionable = challenge.status === 'accepted'
  const withdrawable =
    challenge.status === 'accepted' || challenge.status === 'submitted'
  return (
    <TitledCard
      title={challenge.project_title || t('Bounty challenge')}
      description={`${challenge.owner_username ?? ''} · ${statusLabel(t, challenge.status)}`}
      titleClassName='[overflow-wrap:anywhere]'
      descriptionClassName='[overflow-wrap:anywhere]'
      icon={<HugeiconsIcon icon={Bug01Icon} strokeWidth={1.8} />}
      iconTone='neutral'
      action={rank != null ? <BountyRankBadge rank={rank} /> : undefined}
      disableHoverEffect
    >
      <div className='flex flex-col gap-4'>
        <div className='grid grid-cols-2 gap-2'>
          <Metric
            label={t('Locked reward')}
            value={formatQuota(challenge.reward_quota)}
          />
          <Metric
            label={t('Tips received')}
            value={formatQuota(challenge.tip_quota)}
          />
        </div>
        <RatingView
          title={t('Verifier rating of your work')}
          score={challenge.owner_rating_score}
          comment={challenge.owner_rating_comment}
          average={challenge.participant_rating_average}
          count={challenge.participant_rating_count}
        />
        <RatingView
          title={t('Your rating of the publisher')}
          score={challenge.contributor_rating_score}
          comment={challenge.contributor_rating_comment}
          average={challenge.owner_rating_average}
          count={challenge.owner_rating_count}
        />
        {challenge.review_note && (
          <p className='text-muted-foreground text-sm'>
            {challenge.review_note}
          </p>
        )}
        {challenge.dispute ? (
          <DisputeSummary dispute={challenge.dispute} />
        ) : null}
        <div className='flex flex-wrap gap-2'>
          {challenge.repository_url && (
            <Button
              variant='outline'
              render={
                <a
                  href={challenge.repository_url}
                  target='_blank'
                  rel='noreferrer'
                />
              }
            >
              {t('Repository')}
              <HugeiconsIcon
                icon={ExternalLinkIcon}
                strokeWidth={2}
                data-icon='inline-end'
              />
            </Button>
          )}
          {actionable && (
            <Button
              onClick={onSubmit}
              disabled={challenge.dispute?.status === 'open'}
            >
              {t('Submit work')}
            </Button>
          )}
          {withdrawable && (
            <Button
              variant='outline'
              onClick={onWithdraw}
              disabled={pending !== '' || challenge.dispute?.status === 'open'}
            >
              {t('Withdraw')}
            </Button>
          )}
          {(challenge.status === 'approved' ||
            challenge.status === 'rejected') &&
            challenge.contributor_rating_score === 0 && (
              <Button
                variant='outline'
                onClick={onRateOwner}
                disabled={pending !== ''}
              >
                {t('Rate publisher')}
              </Button>
            )}
          {challenge.status !== 'withdrawn' &&
          challenge.status !== 'cancelled' &&
          !challenge.dispute ? (
            <Button
              variant='outline'
              render={
                <Link to='/support' search={disputeTicketSearch(challenge)} />
              }
            >
              <HugeiconsIcon
                icon={CustomerSupportIcon}
                strokeWidth={2}
                data-icon='inline-start'
              />
              {t('Submit dispute ticket')}
            </Button>
          ) : null}
        </div>
      </div>
    </TitledCard>
  )
}

function Metric({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className='bg-muted/50 min-w-0 rounded-lg border p-3'>
      <p className='text-muted-foreground text-xs [overflow-wrap:anywhere]'>
        {label}
      </p>
      <p className='mt-1 text-sm font-semibold [overflow-wrap:anywhere]'>
        {value}
      </p>
    </div>
  )
}

function RatingView({
  title,
  score,
  comment,
  average,
  count,
}: {
  title: string
  score: number
  comment: string
  average?: number
  count?: number
}) {
  const { t } = useTranslation()
  return (
    <div className='bg-muted/30 flex flex-col gap-2 rounded-lg border p-3'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <p className='text-sm font-medium'>{title}</p>
        <Badge variant='outline'>
          {score > 0 ? `${score}/5` : t('Not rated')}
        </Badge>
      </div>
      {comment ? (
        <p className='text-muted-foreground text-sm whitespace-pre-wrap'>
          {comment}
        </p>
      ) : null}
      {(count ?? 0) > 0 ? (
        <p className='text-muted-foreground text-xs'>
          {t('Historical average: {{average}}/5 from {{count}} ratings', {
            average: (average ?? 0).toFixed(1),
            count,
          })}
        </p>
      ) : null}
    </div>
  )
}

function DraftDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  editing: boolean
  draft: DraftForm
  setDraft: (draft: DraftForm) => void
  errors: BountyDraftErrors
  charge: BountyCharge
  availableQuota: number
  pending: boolean
  onSave: () => void
}) {
  const { t } = useTranslation()
  const errorFor = (field: keyof DraftForm) => {
    const error = props.errors[field]
    return error ? t(error) : undefined
  }
  const update = <K extends keyof DraftForm>(key: K, value: DraftForm[K]) =>
    props.setDraft({ ...props.draft, [key]: value })
  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={props.editing ? t('Edit bounty draft') : t('Create bounty')}
      description={t(
        'Drafts are free. Your balance is charged only when you publish.'
      )}
      contentClassName='sm:max-w-2xl'
      contentHeight='min(70vh, 760px)'
      bodyClassName='flex flex-col gap-4'
      footer={
        <>
          <Button
            variant='outline'
            onClick={() => props.onOpenChange(false)}
            disabled={props.pending}
          >
            {t('Cancel')}
          </Button>
          <Button onClick={props.onSave} disabled={props.pending}>
            {props.editing ? t('Save changes') : t('Save draft')}
          </Button>
        </>
      }
    >
      <Field
        label={t('GitHub repository URL')}
        htmlFor='bounty-repository'
        error={errorFor('repositoryUrl')}
      >
        <Input
          id='bounty-repository'
          value={props.draft.repositoryUrl}
          onChange={(e) => update('repositoryUrl', e.target.value)}
          placeholder='https://github.com/owner/repository'
          aria-invalid={Boolean(props.errors.repositoryUrl)}
          aria-describedby={
            props.errors.repositoryUrl ? 'bounty-repository-error' : undefined
          }
        />
      </Field>
      <Field
        label={t('Bounty title')}
        htmlFor='bounty-title'
        error={errorFor('title')}
      >
        <Input
          id='bounty-title'
          value={props.draft.title}
          minLength={4}
          maxLength={120}
          onChange={(e) => update('title', e.target.value)}
          aria-invalid={Boolean(props.errors.title)}
          aria-describedby={
            props.errors.title ? 'bounty-title-error' : undefined
          }
        />
      </Field>
      <Field
        label={t('Project and defect scope')}
        htmlFor='bounty-description'
        error={errorFor('description')}
      >
        <Textarea
          id='bounty-description'
          rows={4}
          value={props.draft.description}
          minLength={20}
          maxLength={2000}
          onChange={(e) => update('description', e.target.value)}
          aria-invalid={Boolean(props.errors.description)}
          aria-describedby={
            props.errors.description ? 'bounty-description-error' : undefined
          }
        />
      </Field>
      <Field
        label={t('Acceptance and verification rules')}
        htmlFor='bounty-rules'
        error={errorFor('rules')}
      >
        <Textarea
          id='bounty-rules'
          rows={7}
          value={props.draft.rules}
          minLength={20}
          maxLength={5000}
          onChange={(e) => update('rules', e.target.value)}
          aria-invalid={Boolean(props.errors.rules)}
          aria-describedby={
            props.errors.rules ? 'bounty-rules-error' : undefined
          }
          placeholder={t(
            'Describe eligible defects, required tests, review criteria, and exclusions.'
          )}
        />
      </Field>
      <div className='grid gap-4 sm:grid-cols-2'>
        <Field
          label={t('Reward per fix')}
          htmlFor='bounty-reward'
          error={errorFor('rewardAmount')}
        >
          <Input
            id='bounty-reward'
            type='number'
            min={0}
            step='any'
            value={props.draft.rewardAmount}
            onChange={(e) => update('rewardAmount', e.target.value)}
            aria-invalid={Boolean(props.errors.rewardAmount)}
            aria-describedby={
              props.errors.rewardAmount ? 'bounty-reward-error' : undefined
            }
          />
        </Field>
        <Field
          label={t('Reward slots')}
          htmlFor='bounty-slots'
          error={errorFor('rewardSlots')}
        >
          <Input
            id='bounty-slots'
            type='number'
            min={1}
            max={100}
            step={1}
            value={props.draft.rewardSlots}
            onChange={(e) => update('rewardSlots', e.target.value)}
            aria-invalid={Boolean(props.errors.rewardSlots)}
            aria-describedby={
              props.errors.rewardSlots ? 'bounty-slots-error' : undefined
            }
          />
        </Field>
      </div>
      <Alert>
        <HugeiconsIcon icon={MoneyLockIcon} strokeWidth={2} />
        <AlertTitle>{t('Publish charge')}</AlertTitle>
        <AlertDescription>
          {t(
            'Publish charge: {{total}} gross listing total. Of that amount, {{fee}} public platform fee ({{rate}}%) is credited to the super administrator and helps fund AI customer-service token costs, leaving {{netReward}} per approved fix and {{escrow}} total escrow. Current balance: {{balance}}.',
            {
              escrow: formatQuota(props.charge.escrow),
              fee: formatQuota(props.charge.platformFee),
              rate: props.charge.feeRatePercent.toFixed(2),
              netReward: formatQuota(props.charge.netReward),
              total: formatQuota(props.charge.total),
              balance: formatQuota(props.availableQuota),
            }
          )}
        </AlertDescription>
      </Alert>
    </Dialog>
  )
}

function Field({
  label,
  htmlFor,
  error,
  children,
}: {
  label: string
  htmlFor: string
  error?: string
  children: React.ReactNode
}) {
  return (
    <div
      role='group'
      data-invalid={Boolean(error)}
      className='data-[invalid=true]:text-destructive flex flex-col gap-2'
    >
      <Label htmlFor={htmlFor}>{label}</Label>
      {children}
      {error ? (
        <p
          id={`${htmlFor}-error`}
          role='alert'
          className='text-destructive text-sm'
        >
          {error}
        </p>
      ) : null}
    </div>
  )
}

function SubmissionDialog(props: {
  target: { projectId: number; challenge: BountyChallenge } | null
  onOpenChange: (open: boolean) => void
  submission: {
    issueUrl: string
    pullRequestUrl: string
    submissionNote: string
  }
  setSubmission: (value: {
    issueUrl: string
    pullRequestUrl: string
    submissionNote: string
  }) => void
  pending: boolean
  onSubmit: () => void
}) {
  const { t } = useTranslation()
  const update = (key: keyof typeof props.submission, value: string) =>
    props.setSubmission({ ...props.submission, [key]: value })
  const submissionLinkError = validateBountySubmissionLinks(props.submission)
  return (
    <Dialog
      open={Boolean(props.target)}
      onOpenChange={props.onOpenChange}
      title={t('Submit bounty work')}
      description={t(
        'Provide a GitHub Issue URL, pull request URL, or both. The bounty publisher will review the completed work directly.'
      )}
      contentClassName='sm:max-w-2xl'
      footer={
        <>
          <Button
            variant='outline'
            onClick={() => props.onOpenChange(false)}
            disabled={props.pending}
          >
            {t('Cancel')}
          </Button>
          <Button
            onClick={props.onSubmit}
            disabled={props.pending || Boolean(submissionLinkError)}
          >
            {t('Submit for review')}
          </Button>
        </>
      }
    >
      <div className='flex flex-col gap-4 py-2'>
        <Field label={t('GitHub Issue URL')} htmlFor='bounty-issue-url'>
          <Input
            id='bounty-issue-url'
            value={props.submission.issueUrl}
            onChange={(e) => update('issueUrl', e.target.value)}
            aria-describedby='bounty-completion-links-help'
          />
        </Field>
        <Field label={t('GitHub pull request URL')} htmlFor='bounty-pr-url'>
          <Input
            id='bounty-pr-url'
            value={props.submission.pullRequestUrl}
            onChange={(e) => update('pullRequestUrl', e.target.value)}
            aria-describedby='bounty-completion-links-help'
          />
        </Field>
        <p
          id='bounty-completion-links-help'
          className='text-muted-foreground text-sm'
        >
          {t('Provide at least one GitHub Issue or pull request URL.')}
        </p>
        <Field
          label={t('Completion note (optional)')}
          htmlFor='bounty-submission-note'
        >
          <Textarea
            id='bounty-submission-note'
            rows={4}
            value={props.submission.submissionNote}
            onChange={(e) => update('submissionNote', e.target.value)}
          />
        </Field>
      </div>
    </Dialog>
  )
}

export function ProjectReviewDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  detail: BountyProjectDetail | null
  focusedChallengeId?: number
  pending: string
  onReview: (challenge: BountyChallenge, action: 'approve' | 'reject') => void
  onTip: (challenge: BountyChallenge) => void
  onCancel: (challenge: BountyChallenge) => void
}) {
  const { t } = useTranslation()
  const focusedChallengeRef = useRef<HTMLDivElement | null>(null)
  useEffect(() => {
    if (!props.open || !props.focusedChallengeId) return
    const frame = requestAnimationFrame(() => {
      focusedChallengeRef.current?.scrollIntoView({ block: 'nearest' })
      focusedChallengeRef.current?.focus({ preventScroll: true })
    })
    return () => cancelAnimationFrame(frame)
  }, [props.detail, props.focusedChallengeId, props.open])
  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={props.detail?.project.title ?? t('Bounty lifecycle')}
      description={t(
        'Review participants, evidence, balance transfers, and escrow history.'
      )}
      contentClassName='sm:max-w-3xl'
      contentHeight='min(72vh, 820px)'
    >
      <div className='flex flex-col gap-4'>
        {(props.detail?.challenges.length ?? 0) === 0 ? (
          <Empty className='min-h-48 border'>
            <EmptyHeader>
              <EmptyTitle>{t('No challenge activity yet')}</EmptyTitle>
              <EmptyDescription>
                {t('Accepted challenges and submissions will appear here.')}
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          props.detail?.challenges.map((challenge) => (
            <div
              key={challenge.id}
              ref={
                challenge.id === props.focusedChallengeId
                  ? focusedChallengeRef
                  : undefined
              }
              id={`bounty-challenge-${challenge.id}`}
              tabIndex={
                challenge.id === props.focusedChallengeId ? -1 : undefined
              }
              aria-current={
                challenge.id === props.focusedChallengeId ? 'true' : undefined
              }
              className={cn(
                'flex flex-col gap-3 rounded-none border p-4',
                challenge.id === props.focusedChallengeId &&
                  'border-primary ring-primary/20 ring-2'
              )}
            >
              <div className='flex flex-wrap items-start justify-between gap-2'>
                <div>
                  <p className='font-medium'>
                    {t('Username')}: {challenge.participant_username}
                  </p>
                  <p className='text-muted-foreground text-xs'>
                    {t('User ID')}: {challenge.participant_user_id}
                  </p>
                </div>
                <Badge variant='outline'>
                  {statusLabel(t, challenge.status)}
                </Badge>
              </div>
              <div className='flex flex-wrap gap-2'>
                <Button
                  variant='outline'
                  render={
                    <a
                      href={`https://github.com/${challenge.github_handle}`}
                      target='_blank'
                      rel='noreferrer'
                    />
                  }
                >
                  <HugeiconsIcon
                    icon={GithubIcon}
                    strokeWidth={2}
                    data-icon='inline-start'
                  />
                  @{challenge.github_handle}
                  <HugeiconsIcon
                    icon={ExternalLinkIcon}
                    strokeWidth={2}
                    data-icon='inline-end'
                  />
                </Button>
              </div>
              <div className='grid gap-2 sm:grid-cols-2'>
                <Metric
                  label={t('Contributor reputation')}
                  value={
                    (challenge.participant_rating_count ?? 0) > 0
                      ? `${(challenge.participant_rating_average ?? 0).toFixed(1)}/5 · ${challenge.participant_rating_count}`
                      : t('No ratings yet')
                  }
                />
                <Metric
                  label={t('Tips sent')}
                  value={formatQuota(challenge.tip_quota)}
                />
              </div>
              {(challenge.issue_url || challenge.pull_request_url) && (
                <div className='flex flex-wrap gap-2'>
                  {challenge.issue_url && (
                    <Button
                      variant='outline'
                      render={
                        <a
                          href={challenge.issue_url}
                          target='_blank'
                          rel='noreferrer'
                        />
                      }
                    >
                      {t('Issue')}
                      <HugeiconsIcon
                        icon={ExternalLinkIcon}
                        strokeWidth={2}
                        data-icon='inline-end'
                      />
                    </Button>
                  )}
                  {challenge.pull_request_url && (
                    <Button
                      variant='outline'
                      render={
                        <a
                          href={challenge.pull_request_url}
                          target='_blank'
                          rel='noreferrer'
                        />
                      }
                    >
                      {t('Pull request')}
                      <HugeiconsIcon
                        icon={ExternalLinkIcon}
                        strokeWidth={2}
                        data-icon='inline-end'
                      />
                    </Button>
                  )}
                </div>
              )}
              {challenge.review_note && (
                <p className='text-muted-foreground text-sm'>
                  {challenge.review_note}
                </p>
              )}
              {challenge.dispute ? (
                <DisputeSummary dispute={challenge.dispute} />
              ) : null}
              <RatingView
                title={t('Your rating of the contributor')}
                score={challenge.owner_rating_score}
                comment={challenge.owner_rating_comment}
                average={challenge.participant_rating_average}
                count={challenge.participant_rating_count}
              />
              <RatingView
                title={t('Contributor rating of you')}
                score={challenge.contributor_rating_score}
                comment={challenge.contributor_rating_comment}
                average={challenge.owner_rating_average}
                count={challenge.owner_rating_count}
              />
              {challenge.status !== 'withdrawn' &&
                challenge.status !== 'cancelled' && (
                  <div className='flex flex-wrap gap-2'>
                    <Button
                      variant='outline'
                      onClick={() => props.onTip(challenge)}
                      disabled={props.pending !== ''}
                    >
                      <HugeiconsIcon
                        icon={GiftIcon}
                        strokeWidth={2}
                        data-icon='inline-start'
                      />
                      {t('Send tip')}
                    </Button>
                    {!challenge.dispute ? (
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
                          strokeWidth={2}
                          data-icon='inline-start'
                        />
                        {t('Submit dispute ticket')}
                      </Button>
                    ) : null}
                  </div>
                )}
              {challenge.status === 'submitted' && (
                <div className='flex flex-wrap gap-2'>
                  <Button
                    onClick={() => props.onReview(challenge, 'approve')}
                    disabled={
                      props.pending !== '' ||
                      challenge.dispute?.status === 'open'
                    }
                  >
                    <HugeiconsIcon
                      icon={CheckmarkCircle02Icon}
                      strokeWidth={2}
                      data-icon='inline-start'
                    />
                    {t('Approve and pay')} {formatQuota(challenge.reward_quota)}
                  </Button>
                  <Button
                    variant='destructive'
                    onClick={() => props.onReview(challenge, 'reject')}
                    disabled={
                      props.pending !== '' ||
                      challenge.dispute?.status === 'open'
                    }
                  >
                    {t('Reject')}
                  </Button>
                </div>
              )}
              <ChallengeCancelAction
                challenge={challenge}
                pending={props.pending}
                onCancel={props.onCancel}
              />
            </div>
          ))
        )}
        {(props.detail?.ledger.length ?? 0) > 0 && (
          <>
            <Separator />
            <div className='flex flex-col gap-2'>
              <h3 className='font-semibold'>{t('Balance ledger')}</h3>
              {props.detail?.ledger.map((entry) => (
                <div
                  key={entry.id}
                  className='rounded-lg border px-3 py-2 text-sm'
                >
                  <div className='flex items-center justify-between gap-4'>
                    <span>{t(entry.kind)}</span>
                    <span className='font-medium'>
                      {formatQuota(entry.quota)}
                    </span>
                  </div>
                  {entry.note ? (
                    <p className='text-muted-foreground mt-1 text-xs'>
                      {entry.note}
                    </p>
                  ) : null}
                </div>
              ))}
            </div>
          </>
        )}
      </div>
    </Dialog>
  )
}

export function ChallengeCancelAction(props: {
  challenge: BountyChallenge
  pending: string
  onCancel: (challenge: BountyChallenge) => void
}) {
  const { t } = useTranslation()
  const { status } = useStatus()
  const canCancelChallenge =
    getBackendCapabilities(status).bounty_challenge_cancel

  if (!canCancelChallenge || props.challenge.status !== 'accepted') return null

  return (
    <Button
      variant='destructive'
      onClick={() => props.onCancel(props.challenge)}
      disabled={
        props.pending !== '' || props.challenge.dispute?.status === 'open'
      }
    >
      <HugeiconsIcon
        icon={CancelCircleIcon}
        strokeWidth={2}
        data-icon='inline-start'
      />
      {t('Cancel challenge')}
    </Button>
  )
}

const DISPUTE_REASON_KEYS = {
  merged_but_unpaid: 'Fix merged but bounty unpaid',
  requirements_met_but_rejected: 'Requirements met but submission rejected',
  misleading_requirements: 'Misleading or changed requirements',
  abusive_conduct: 'Abusive conduct',
  other: 'Other bounty dispute',
} as const

const DISPUTE_STATUS_KEYS = {
  open: 'Awaiting third-party review',
  resolved_paid: 'Resolved with escrow payment',
  resolved_denied: 'Claim denied',
} as const

function DisputeSummary({ dispute }: { dispute: BountyDispute }) {
  const { t } = useTranslation()
  return (
    <Alert>
      <HugeiconsIcon icon={CustomerSupportIcon} strokeWidth={2} />
      <AlertTitle>{t(DISPUTE_STATUS_KEYS[dispute.status])}</AlertTitle>
      <AlertDescription className='space-y-2'>
        <p>{t(DISPUTE_REASON_KEYS[dispute.reason])}</p>
        <p className='whitespace-pre-wrap'>{dispute.statement}</p>
        {dispute.status === 'open' ? (
          <p className='font-medium'>
            {t(
              'The affected escrow and reward slot remain frozen until third-party review is complete.'
            )}
          </p>
        ) : null}
        {dispute.resolution ? (
          <p>
            <span className='font-medium'>
              {t('Administrator resolution')}:
            </span>{' '}
            {dispute.resolution}
          </p>
        ) : null}
        {dispute.resolved_at > 0 ? (
          <p className='text-xs'>
            {t('Resolved at')}:{' '}
            {new Date(dispute.resolved_at * 1000).toLocaleString()}
          </p>
        ) : null}
      </AlertDescription>
    </Alert>
  )
}

function DisputeEvidenceValue({
  label,
  value,
  current = false,
}: {
  label: string
  value: React.ReactNode
  current?: boolean
}) {
  const { t } = useTranslation()
  return (
    <div className='bg-muted/30 rounded-lg border p-3'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <p className='text-xs font-medium'>{label}</p>
        {current ? <Badge variant='outline'>{t('Current')}</Badge> : null}
      </div>
      <div className='text-muted-foreground mt-1 text-sm whitespace-pre-wrap'>
        {value}
      </div>
    </div>
  )
}

function DisputeEvidenceChange({
  label,
  original,
  current,
}: {
  label: string
  original: React.ReactNode
  current: React.ReactNode
}) {
  const { t } = useTranslation()
  return (
    <div className='rounded-lg border p-3'>
      <p className='text-sm font-medium'>{label}</p>
      <div className='mt-2 grid gap-2 sm:grid-cols-2'>
        <DisputeEvidenceValue label={t('Original value')} value={original} />
        <DisputeEvidenceValue label={t('Current Value')} value={current} />
      </div>
    </div>
  )
}

function DisputeEvidence({ dispute }: { dispute: BountyDispute }) {
  const { t } = useTranslation()
  const comparison = getBountyDisputeEvidenceComparison(dispute)
  const changed = new Set<BountyDisputeEvidenceField>(comparison.changedFields)
  const text = (value: string) => value || t('None')
  const rating = (score: number, comment: string) =>
    score > 0 ? `${score}/5${comment ? `\n${comment}` : ''}` : t('None')

  return (
    <div className='flex flex-col gap-3'>
      <div className='rounded-lg border p-3'>
        <p className='text-sm font-semibold'>{t('Original value')}</p>
        <div className='mt-3 grid gap-3 sm:grid-cols-2 lg:grid-cols-3'>
          <DisputeEvidenceValue
            label={t('Tip')}
            value={formatQuota(dispute.tip_quota_snapshot)}
            current={!changed.has('tipQuota')}
          />
          <DisputeEvidenceValue
            label={dispute.owner_username}
            value={rating(
              dispute.owner_rating_score_snapshot,
              dispute.owner_rating_comment_snapshot
            )}
            current={!changed.has('ownerRating')}
          />
          <DisputeEvidenceValue
            label={dispute.participant_username}
            value={rating(
              dispute.contributor_rating_score_snapshot,
              dispute.contributor_rating_comment_snapshot
            )}
            current={!changed.has('contributorRating')}
          />
          <DisputeEvidenceValue
            label={t('Rules')}
            value={text(dispute.project_rules_snapshot)}
            current={!changed.has('projectRules')}
          />
          <DisputeEvidenceValue
            label={t('Completion note (optional)')}
            value={text(dispute.submission_note_snapshot)}
            current={!changed.has('submissionNote')}
          />
        </div>
      </div>

      {comparison.showCurrentValues ? (
        <Alert>
          <AlertTitle>{t('Changed Fields')}</AlertTitle>
          <AlertDescription className='mt-3 grid gap-3'>
            {changed.has('projectTitle') ? (
              <DisputeEvidenceChange
                label={t('Project')}
                original={text(dispute.project_title_snapshot)}
                current={text(dispute.project_title)}
              />
            ) : null}
            {changed.has('repositoryUrl') ? (
              <DisputeEvidenceChange
                label={t('Repository')}
                original={text(dispute.repository_url_snapshot)}
                current={text(dispute.repository_url)}
              />
            ) : null}
            {changed.has('projectRules') ? (
              <DisputeEvidenceChange
                label={t('Rules')}
                original={text(dispute.project_rules_snapshot)}
                current={text(dispute.project_rules)}
              />
            ) : null}
            {changed.has('projectEscrowQuota') ? (
              <DisputeEvidenceChange
                label={t('Escrow remaining')}
                original={formatQuota(dispute.project_escrow_quota_snapshot)}
                current={formatQuota(dispute.current_project_escrow_quota)}
              />
            ) : null}
            {changed.has('challengeStatus') ? (
              <DisputeEvidenceChange
                label={t('Status')}
                original={statusLabel(t, dispute.challenge_status_snapshot)}
                current={statusLabel(t, dispute.challenge_status)}
              />
            ) : null}
            {changed.has('issueUrl') ? (
              <DisputeEvidenceChange
                label={t('Issue')}
                original={text(dispute.issue_url_snapshot)}
                current={text(dispute.issue_url)}
              />
            ) : null}
            {changed.has('pullRequestUrl') ? (
              <DisputeEvidenceChange
                label={t('Pull request')}
                original={text(dispute.pull_request_url_snapshot)}
                current={text(dispute.pull_request_url)}
              />
            ) : null}
            {changed.has('submissionNote') ? (
              <DisputeEvidenceChange
                label={t('Completion note (optional)')}
                original={text(dispute.submission_note_snapshot)}
                current={text(dispute.submission_note)}
              />
            ) : null}
            {changed.has('reviewNote') ? (
              <DisputeEvidenceChange
                label={t('Review note (optional)')}
                original={text(dispute.review_note_snapshot)}
                current={text(dispute.review_note)}
              />
            ) : null}
            {changed.has('rewardQuota') ? (
              <DisputeEvidenceChange
                label={t('Locked reward')}
                original={formatQuota(dispute.reward_quota_snapshot)}
                current={formatQuota(dispute.reward_quota)}
              />
            ) : null}
            {changed.has('tipQuota') ? (
              <DisputeEvidenceChange
                label={t('Tip')}
                original={formatQuota(dispute.tip_quota_snapshot)}
                current={formatQuota(dispute.tip_quota)}
              />
            ) : null}
            {changed.has('ownerRating') ? (
              <DisputeEvidenceChange
                label={dispute.owner_username}
                original={rating(
                  dispute.owner_rating_score_snapshot,
                  dispute.owner_rating_comment_snapshot
                )}
                current={rating(
                  dispute.owner_rating_score,
                  dispute.owner_rating_comment
                )}
              />
            ) : null}
            {changed.has('contributorRating') ? (
              <DisputeEvidenceChange
                label={dispute.participant_username}
                original={rating(
                  dispute.contributor_rating_score_snapshot,
                  dispute.contributor_rating_comment_snapshot
                )}
                current={rating(
                  dispute.contributor_rating_score,
                  dispute.contributor_rating_comment
                )}
              />
            ) : null}
          </AlertDescription>
        </Alert>
      ) : null}
    </div>
  )
}

function DisputesPanel({
  items,
  loading,
  admin = false,
}: {
  items: BountyDispute[]
  loading: boolean
  admin?: boolean
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [target, setTarget] = useState<{
    dispute: BountyDispute
    action: 'pay' | 'deny'
  } | null>(null)
  const [resolution, setResolution] = useState('')
  const [pending, setPending] = useState(false)

  const resolve = async () => {
    if (!target || resolution.trim().length < 10) {
      toast.error(
        t('Enter an administrator resolution of at least 10 characters.')
      )
      return
    }
    const confirmation =
      target.action === 'pay'
        ? t(
            'Pay {{amount}} from escrow to {{contributor}} and approve this challenge? This cannot be repeated.',
            {
              amount: formatQuota(target.dispute.reward_quota),
              contributor: target.dispute.participant_username,
            }
          )
        : t(
            'Deny this dispute claim? The administrator resolution will be visible to both parties.'
          )
    if (!window.confirm(confirmation)) return

    setPending(true)
    try {
      await resolveBountyDispute(target.dispute.id, {
        action: target.action,
        resolution: resolution.trim(),
      })
      await Promise.all([
        queryClient.invalidateQueries({
          queryKey: ['open-source-bounties', 'disputes'],
        }),
        ...BOUNTY_QUERY_KEYS.slice(0, 3).map((queryKey) =>
          queryClient.invalidateQueries({ queryKey })
        ),
      ])
      toast.success(
        target.action === 'pay'
          ? t('Dispute resolved and escrow reward transferred.')
          : t('Dispute claim denied.')
      )
      setTarget(null)
      setResolution('')
    } catch {
      toast.error(t('Unable to resolve the bounty dispute.'))
    } finally {
      setPending(false)
    }
  }

  if (loading) return <LoadingState label={t('Loading disputes...')} />
  if (items.length === 0) {
    return (
      <Empty className='min-h-64 border'>
        <EmptyHeader>
          <EmptyMedia variant='icon'>
            <HugeiconsIcon icon={CustomerSupportIcon} strokeWidth={2} />
          </EmptyMedia>
          <EmptyTitle>
            {admin ? t('No dispute cases') : t('You have no bounty disputes')}
          </EmptyTitle>
          <EmptyDescription>
            {admin
              ? t(
                  'Open bounty disputes requiring third-party review will appear here.'
                )
              : t(
                  'If a bounty payment or acceptance decision is disputed, submit a ticket from the challenge card.'
                )}
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  return (
    <>
      <div className='grid gap-4'>
        {items.map((dispute) => (
          <TitledCard
            key={dispute.id}
            title={dispute.project_title}
            description={`${dispute.opened_by_username} → ${dispute.against_username}`}
            icon={
              <HugeiconsIcon icon={CustomerSupportIcon} strokeWidth={1.8} />
            }
            iconTone={dispute.status === 'open' ? 'info' : 'neutral'}
            disableHoverEffect
            action={
              <Badge variant='outline'>
                {t(DISPUTE_STATUS_KEYS[dispute.status])}
              </Badge>
            }
          >
            <div className='flex flex-col gap-4'>
              <div className='grid gap-2 sm:grid-cols-3'>
                <Metric
                  label={t('Escrow reward')}
                  value={formatQuota(dispute.reward_quota)}
                />
                {!admin ? (
                  <Metric
                    label={t('Tips already paid')}
                    value={formatQuota(dispute.tip_quota)}
                  />
                ) : null}
                <Metric
                  label={t('Challenge status')}
                  value={statusLabel(t, dispute.challenge_status)}
                />
              </div>
              <div>
                <p className='text-sm font-medium'>
                  {t(DISPUTE_REASON_KEYS[dispute.reason])}
                </p>
                <p className='text-muted-foreground mt-1 text-sm whitespace-pre-wrap'>
                  {dispute.statement}
                </p>
              </div>
              {dispute.status === 'open' ? (
                <Alert>
                  <HugeiconsIcon icon={MoneyLockIcon} strokeWidth={2} />
                  <AlertTitle>
                    {t('Funds and reward slots are frozen')}
                  </AlertTitle>
                  <AlertDescription>
                    {t(
                      'The affected escrow and reward slot remain frozen until third-party review is complete.'
                    )}
                  </AlertDescription>
                </Alert>
              ) : null}
              {admin ? (
                <DisputeEvidence dispute={dispute} />
              ) : (
                <div className='grid gap-3 lg:grid-cols-2'>
                  <div className='bg-muted/40 rounded-lg border p-3'>
                    <p className='text-xs font-medium'>
                      {t('Published acceptance rules')}
                    </p>
                    <p className='text-muted-foreground mt-1 text-sm whitespace-pre-wrap'>
                      {dispute.project_rules_snapshot}
                    </p>
                  </div>
                  <div className='bg-muted/40 rounded-lg border p-3'>
                    <p className='text-xs font-medium'>
                      {t('Contributor completion note')}
                    </p>
                    <p className='text-muted-foreground mt-1 text-sm whitespace-pre-wrap'>
                      {dispute.submission_note_snapshot ||
                        t('No note provided.')}
                    </p>
                  </div>
                </div>
              )}
              <div className='flex flex-wrap gap-2'>
                <Button
                  variant='outline'
                  render={
                    <a
                      href={dispute.repository_url}
                      target='_blank'
                      rel='noreferrer'
                    />
                  }
                >
                  {t('Repository')}
                  <HugeiconsIcon
                    icon={ExternalLinkIcon}
                    strokeWidth={2}
                    data-icon='inline-end'
                  />
                </Button>
                {dispute.issue_url ? (
                  <Button
                    variant='outline'
                    render={
                      <a
                        href={dispute.issue_url}
                        target='_blank'
                        rel='noreferrer'
                      />
                    }
                  >
                    {t('Issue')}
                    <HugeiconsIcon
                      icon={ExternalLinkIcon}
                      strokeWidth={2}
                      data-icon='inline-end'
                    />
                  </Button>
                ) : null}
                {dispute.pull_request_url ? (
                  <Button
                    variant='outline'
                    render={
                      <a
                        href={dispute.pull_request_url}
                        target='_blank'
                        rel='noreferrer'
                      />
                    }
                  >
                    {t('Pull request')}
                    <HugeiconsIcon
                      icon={ExternalLinkIcon}
                      strokeWidth={2}
                      data-icon='inline-end'
                    />
                  </Button>
                ) : null}
              </div>
              {dispute.review_note ? (
                <div className='bg-muted/40 rounded-lg border p-3'>
                  <p className='text-xs font-medium'>
                    {t('Publisher review note')}
                  </p>
                  <p className='text-muted-foreground mt-1 text-sm whitespace-pre-wrap'>
                    {dispute.review_note}
                  </p>
                </div>
              ) : null}
              {!admin ? (
                <div className='grid gap-3 lg:grid-cols-2'>
                  <RatingView
                    title={t('Publisher rating of contributor')}
                    score={dispute.owner_rating_score}
                    comment={dispute.owner_rating_comment}
                  />
                  <RatingView
                    title={t('Contributor rating of publisher')}
                    score={dispute.contributor_rating_score}
                    comment={dispute.contributor_rating_comment}
                  />
                </div>
              ) : null}
              {dispute.resolution ? (
                <Alert>
                  <AlertTitle>{t('Administrator resolution')}</AlertTitle>
                  <AlertDescription className='whitespace-pre-wrap'>
                    <p>{dispute.resolution}</p>
                    {dispute.resolved_at > 0 ? (
                      <p className='mt-2 text-xs'>
                        {t('Resolved at')}:{' '}
                        {new Date(dispute.resolved_at * 1000).toLocaleString()}
                      </p>
                    ) : null}
                  </AlertDescription>
                </Alert>
              ) : null}
              {admin && dispute.status === 'open' ? (
                <div className='flex flex-wrap gap-2'>
                  <Button
                    onClick={() => {
                      setTarget({ dispute, action: 'pay' })
                      setResolution('')
                    }}
                  >
                    <HugeiconsIcon
                      icon={CheckmarkCircle02Icon}
                      strokeWidth={2}
                      data-icon='inline-start'
                    />
                    {t('Pay from escrow')}
                  </Button>
                  <Button
                    variant='destructive'
                    onClick={() => {
                      setTarget({ dispute, action: 'deny' })
                      setResolution('')
                    }}
                  >
                    {t('Deny claim')}
                  </Button>
                </div>
              ) : null}
            </div>
          </TitledCard>
        ))}
      </div>
      <Dialog
        open={Boolean(target)}
        onOpenChange={(open) => !open && setTarget(null)}
        title={
          target?.action === 'pay'
            ? t('Resolve dispute and pay from escrow')
            : t('Deny dispute claim')
        }
        description={t(
          'Record a neutral third-party conclusion. It will be visible to the publisher and contributor.'
        )}
        contentClassName='sm:max-w-lg'
        footer={
          <>
            <Button variant='outline' onClick={() => setTarget(null)}>
              {t('Cancel')}
            </Button>
            <Button
              variant={target?.action === 'deny' ? 'destructive' : 'default'}
              onClick={resolve}
              disabled={pending || resolution.trim().length < 10}
            >
              {target?.action === 'pay'
                ? t('Confirm escrow payment')
                : t('Confirm denial')}
            </Button>
          </>
        }
      >
        <Field
          label={t('Administrator resolution')}
          htmlFor='bounty-dispute-resolution'
        >
          <Textarea
            id='bounty-dispute-resolution'
            rows={6}
            value={resolution}
            onChange={(event) => setResolution(event.target.value)}
            placeholder={t(
              'Explain the evidence reviewed and the reason for this decision.'
            )}
          />
        </Field>
      </Dialog>
    </>
  )
}

function McpSettingsPanel() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [revealedToken, setRevealedToken] = useState('')
  const [pending, setPending] = useState(false)
  const connectionQuery = useQuery({
    queryKey: ['open-source-bounties', 'mcp-token'],
    queryFn: getMcpTokenStatus,
  })
  const endpoint =
    typeof window === 'undefined'
      ? '/mcp'
      : `${window.location.origin}${connectionQuery.data?.endpoint ?? '/mcp'}`
  const protocolVersion = connectionQuery.data?.protocol_version ?? '2026-07-28'
  const prompt = useMemo(
    () => `Connect to the api.lmm.best Open-source bounties MCP server.

Endpoint: ${endpoint}
Protocol: MCP ${protocolVersion}, stateless Streamable HTTP
Authorization: Bearer ${revealedToken || '<YOUR_PERSONAL_MCP_TOKEN>'}

Use the open_source_bounty_operator prompt and the open_source_bounties.* tools to manage my bounties end to end. Treat every bounty as a peer-to-peer transaction between its publisher and contributor; an administrator intervenes only when either party opens a dispute. Never fabricate defects, Issues, pull requests, tests, review results, dispute evidence, tips, or ratings. Read current state before changing anything. Publishing debits the gross listed price from my balance, credits the public platform fee to the enabled super administrator account, and locks the remaining net contributor rewards in escrow. If I am that super administrator, report both the gross debit and fee credit and the resulting net balance decrease. Daily check-in rewards are credited to the same balance and can fund listings. The public board ranks listings by gross price per fix from highest to lowest. When any tool returns input_required for publishing, approval/payment, rejection, closing/refunding, tipping, rating, dispute opening/resolution, draft deletion, or withdrawal, show me the exact action, recipient, public score, gross price, net reward, fee, evidence, and balance impact, then continue only after I explicitly confirm. Tips are non-refundable and separate from escrow. A contributor may submit a matching GitHub Issue URL, pull request URL, or both, plus an optional completion note. The bounty publisher reviews the completed work directly. Reviewers must record a truthful 1-5 contributor score and public evaluation; contributors may rate the publisher/verifier after review, and both sides can see mutual ratings and historical averages. If the parties disagree, open a dispute with the real challenge ID and evidence. Treat an open dispute as frozen until a third-party administrator records a conclusion and, when justified, transfers the locked reward from escrow.`,
    [endpoint, protocolVersion, revealedToken]
  )

  const rotate = async () => {
    if (
      connectionQuery.data?.status.configured &&
      !window.confirm(
        t(
          'Rotate the personal MCP token? The previous token will stop working immediately.'
        )
      )
    ) {
      return
    }
    setPending(true)
    try {
      const connection = await rotateMcpToken()
      setRevealedToken(connection.token)
      await queryClient.invalidateQueries({
        queryKey: ['open-source-bounties', 'mcp-token'],
      })
      toast.success(
        t('Personal MCP token generated. Copy it now; it is shown only once.')
      )
    } catch {
      toast.error(t('Unable to update the personal MCP token.'))
    } finally {
      setPending(false)
    }
  }

  const revoke = async () => {
    if (
      !window.confirm(
        t(
          'Revoke the personal MCP token? Connected AI clients will lose access immediately.'
        )
      )
    ) {
      return
    }
    setPending(true)
    try {
      await revokeMcpToken()
      setRevealedToken('')
      await queryClient.invalidateQueries({
        queryKey: ['open-source-bounties', 'mcp-token'],
      })
      toast.success(t('Personal MCP token revoked.'))
    } catch {
      toast.error(t('Unable to revoke the personal MCP token.'))
    } finally {
      setPending(false)
    }
  }

  const copyPrompt = async () => {
    const copied = await copyToClipboard(prompt)
    if (copied) {
      toast.success(t('AI prompt copied.'))
    } else {
      toast.error(
        t(
          'Copy failed. The complete prompt remains visible for manual copying.'
        )
      )
    }
  }

  return (
    <TitledCard
      title={t('Open-source bounty MCP')}
      description={t(
        'Use one personal token to let an AI publish, accept, verify, dispute, tip, rate, and settle bounties through /mcp.'
      )}
      icon={<HugeiconsIcon icon={SourceCodeIcon} strokeWidth={1.8} />}
      iconTone='info'
      disableHoverEffect
      action={<Badge variant='outline'>MCP {protocolVersion}</Badge>}
    >
      <div className='flex flex-col gap-4'>
        <div className='grid gap-4 sm:grid-cols-2'>
          <Field label={t('MCP endpoint')} htmlFor='bounty-mcp-endpoint'>
            <Input id='bounty-mcp-endpoint' value={endpoint} readOnly />
          </Field>
          <Field label={t('Token status')} htmlFor='bounty-mcp-token-status'>
            <Input
              id='bounty-mcp-token-status'
              value={
                connectionQuery.data?.status.configured
                  ? connectionQuery.data.status.token_hint
                  : t('Not configured')
              }
              readOnly
            />
          </Field>
        </div>
        {revealedToken ? (
          <Alert>
            <AlertTitle>{t('Copy this token now')}</AlertTitle>
            <AlertDescription>
              {t(
                'For security, the plaintext token will not be shown again after you leave this page.'
              )}
            </AlertDescription>
            <Textarea
              className='mt-3 font-mono text-xs'
              value={revealedToken}
              readOnly
              rows={3}
            />
          </Alert>
        ) : null}
        <div className='flex flex-wrap gap-2'>
          <Button onClick={rotate} disabled={pending}>
            {connectionQuery.data?.status.configured
              ? t('Rotate token')
              : t('Generate token')}
          </Button>
          {connectionQuery.data?.status.configured ? (
            <Button variant='destructive' onClick={revoke} disabled={pending}>
              {t('Revoke token')}
            </Button>
          ) : null}
          <Button variant='outline' onClick={copyPrompt} disabled={pending}>
            <HugeiconsIcon
              icon={Copy01Icon}
              strokeWidth={2}
              data-icon='inline-start'
            />
            {t('Copy AI prompt')}
          </Button>
        </div>
        <Field label={t('AI prompt')} htmlFor='bounty-mcp-prompt'>
          <Textarea
            id='bounty-mcp-prompt'
            value={prompt}
            readOnly
            rows={14}
            className='font-mono text-xs'
          />
        </Field>
      </div>
    </TitledCard>
  )
}

function RulesPanel() {
  const { t } = useTranslation()
  const steps = [
    [
      '1',
      'Find and document a real bug',
      'Open a valid Issue with the affected project, reproducible steps, expected behavior, actual behavior, and impact.',
    ],
    [
      '2',
      'Submit a focused fix',
      'Open a pull request that links the Issue and includes appropriate verification or tests.',
    ],
    [
      '3',
      'Settle directly with the publisher',
      'Submit the Issue or pull request in Open-source bounties. The publisher verifies the work, rates the contributor, and releases the escrowed reward directly.',
    ],
    [
      '4',
      'Escalate only disputed trades',
      'If either party disputes rejection or payment, open a platform ticket. A third-party administrator may review the preserved evidence and arbitrate the escrow.',
    ],
  ] as const
  return (
    <div className='grid gap-4 lg:grid-cols-[minmax(0,1fr)_340px]'>
      <TitledCard
        title={t('Real bug-fix contribution rewards')}
        description={t(
          'A separate incentive for genuine defects in public projects. It is not part of the Challenge II recovery process.'
        )}
        icon={<HugeiconsIcon icon={Bug01Icon} strokeWidth={1.8} />}
        iconTone='primary'
        disableHoverEffect
      >
        <div className='grid gap-3 sm:grid-cols-2'>
          {steps.map(([number, title, description]) => (
            <div key={number} className='flex gap-3 rounded-none border p-4'>
              <Badge variant='secondary'>{number}</Badge>
              <div>
                <h3 className='font-semibold'>{t(title)}</h3>
                <p className='text-muted-foreground mt-1 text-sm leading-relaxed'>
                  {t(description)}
                </p>
              </div>
            </div>
          ))}
        </div>
      </TitledCard>
      <TitledCard
        title={t('Quality requirements')}
        description={t('Only genuine, reviewable engineering work qualifies.')}
        icon={<HugeiconsIcon icon={Award01Icon} strokeWidth={1.8} />}
        iconTone='neutral'
        disableHoverEffect
      >
        <div className='flex flex-col gap-4'>
          <p className='text-muted-foreground text-sm leading-relaxed'>
            {t(
              'Low-quality reports, fabricated bugs, duplicate Issues, unrelated pull requests, mechanical spam, and changes made only to obtain a reward do not qualify.'
            )}
          </p>
          <Separator />
          <p className='text-sm font-medium'>
            {t(
              'Approved submissions transfer the locked reward directly to the contributor balance for use with supported models.'
            )}
          </p>
        </div>
      </TitledCard>
    </div>
  )
}
