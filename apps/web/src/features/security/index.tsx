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
*/
import { useQuery } from '@tanstack/react-query'
import {
  Activity,
  AlertTriangle,
  BookOpenCheck,
  CircleDollarSign,
  ExternalLink,
  Layers,
  ListChecks,
  ShieldCheck,
} from 'lucide-react'
import { lazy, Suspense } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { ForgePublicShell } from '@/features/forge/forge-public-shell'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatTimestampToDate } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  SECURITY_POLICY_ENDPOINT,
  SECURITY_STATS_ENDPOINT,
  getSecurityPolicy,
  getSecurityStats,
} from './api'
import type {
  SecurityPolicy,
  SecurityRiskCategory,
  SecurityRuleSummary,
  SecurityStats,
  SecurityStatsBucket,
  SecurityViolationFeeRule,
} from './types'

const SecurityAuditPanel = lazy(() =>
  import('@/features/system-settings/security/security-audit').then(
    ({ SecurityAuditPanel: panel }) => ({ default: panel })
  )
)

const DETECTION_PRINCIPLES = [
  {
    icon: Activity,
    title: 'Detection and enforcement',
    description:
      'The platform may combine rule matching, request context, and service signals to identify risk.',
  },
  {
    icon: ShieldCheck,
    title: 'Block before upstream',
    description:
      'Requests can be stopped before an upstream model call when a rule is triggered.',
  },
  {
    icon: BookOpenCheck,
    title: 'Review and appeal',
    description:
      'If you believe a request was blocked incorrectly, contact support with the request ID. Do not include secrets in a support ticket.',
  },
]

type Translate = ReturnType<typeof useTranslation>['t']

function formatCount(value: number, language: string): string {
  return formatNumber(value, toIntlLocale(language))
}

function formatFeeAmount(
  fee: SecurityViolationFeeRule,
  t: Translate,
  language: string
): string {
  if (!Number.isFinite(fee.amount_usd)) return t('Not published')
  return `$${formatNumber(fee.amount_usd, toIntlLocale(language))}`
}

function displayValue(value: string | undefined, t: Translate): string {
  return value?.trim() || t('Not published')
}

function UnavailableState({
  title,
  description,
  icon: Icon = AlertTriangle,
}: {
  title: string
  description: string
  icon?: typeof AlertTriangle
}) {
  return (
    <div className='border-border/70 bg-muted/20 rounded-xl border border-dashed p-6'>
      <div className='flex items-start gap-3'>
        <Icon className='text-muted-foreground mt-0.5 size-5 shrink-0' />
        <div className='min-w-0 space-y-1 break-words'>
          <p className='text-sm font-medium'>{title}</p>
          <p className='text-muted-foreground text-sm leading-6'>
            {description}
          </p>
        </div>
      </div>
    </div>
  )
}

function StatsPanel({
  stats,
  isLoading,
}: {
  stats?: SecurityStats
  isLoading: boolean
}) {
  const { t, i18n } = useTranslation()

  if (isLoading) {
    return (
      <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-5'>
        {Array.from({ length: 5 }, (_, index) => (
          <Card key={index} size='sm'>
            <CardHeader>
              <Skeleton className='h-4 w-28' />
            </CardHeader>
            <CardContent>
              <Skeleton className='h-8 w-20' />
            </CardContent>
          </Card>
        ))}
      </div>
    )
  }

  if (!stats) {
    return (
      <UnavailableState
        title={t('No live risk metrics are available yet.')}
        description={t(
          'The security statistics endpoint returned no data. No numbers are fabricated in this view.'
        )}
      />
    )
  }

  const cards = [
    { label: 'Total matches', value: stats.total_matches },
    { label: 'Blocked matches', value: stats.blocked_matches },
    { label: 'Audited matches', value: stats.audited_matches },
    { label: 'Affected requests', value: stats.affected_requests },
    { label: 'Affected users', value: stats.affected_users },
  ]
  const categories = stats.by_category ?? []
  const start = formatTimestampToDate(stats.start_timestamp)
  const end = formatTimestampToDate(stats.end_timestamp)

  return (
    <div className='space-y-5'>
      <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-5'>
        {cards.map((card) => (
          <Card key={card.label} size='sm'>
            <CardHeader>
              <CardDescription>{t(card.label)}</CardDescription>
            </CardHeader>
            <CardContent>
              <p className='text-3xl font-semibold tracking-tight tabular-nums'>
                {formatCount(card.value, i18n.language)}
              </p>
            </CardContent>
          </Card>
        ))}
      </div>

      <Card size='sm'>
        <CardHeader>
          <div className='flex items-start gap-3'>
            <ListChecks className='text-muted-foreground mt-0.5 size-5 shrink-0' />
            <div>
              <CardTitle>{t('Matches by risk category')}</CardTitle>
              <CardDescription className='mt-1'>
                {start} — {end}
              </CardDescription>
            </div>
          </div>
        </CardHeader>
        <CardContent>
          {categories.length === 0 ? (
            <UnavailableState
              title={t('No category statistics are available yet.')}
              description={t(
                'The server returned the aggregate totals but no category breakdown.'
              )}
            />
          ) : (
            <div className='space-y-3'>
              {categories.map((bucket) => (
                <StatsBucketRow
                  key={bucket.key}
                  bucket={bucket}
                  language={i18n.language}
                />
              ))}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

function StatsBucketRow({
  bucket,
  language,
}: {
  bucket: SecurityStatsBucket
  language: string
}) {
  return (
    <div className='flex items-center justify-between gap-4 border-b pb-3 last:border-b-0 last:pb-0'>
      <span className='min-w-0 text-sm [overflow-wrap:anywhere]'>
        {bucket.key}
      </span>
      <span className='shrink-0 text-sm font-medium tabular-nums'>
        {formatCount(bucket.count, language)}
      </span>
    </div>
  )
}

function PolicyMetadata({ policy }: { policy: SecurityPolicy }) {
  const { t } = useTranslation()

  return (
    <Card size='sm'>
      <CardHeader>
        <div className='flex items-start gap-3'>
          <ShieldCheck className='text-muted-foreground mt-0.5 size-5 shrink-0' />
          <div>
            <CardTitle>{t('Policy metadata')}</CardTitle>
            <CardDescription className='mt-1'>
              {displayValue(policy.alignment, t)}
            </CardDescription>
          </div>
        </div>
      </CardHeader>
      <CardContent>
        <dl className='grid gap-4 text-sm sm:grid-cols-2'>
          <div>
            <dt className='text-muted-foreground'>{t('Policy version')}</dt>
            <dd className='mt-1 font-mono [overflow-wrap:anywhere]'>
              {displayValue(policy.policy_version, t)}
            </dd>
          </div>
          <div>
            <dt className='text-muted-foreground'>{t('Effective date')}</dt>
            <dd className='mt-1'>
              {displayValue(policy.reference_effective_date, t)}
            </dd>
          </div>
          <div>
            <dt className='text-muted-foreground'>{t('Advanced Security')}</dt>
            <dd className='mt-1'>
              {policy.enforcement.enabled ? t('Enabled') : t('Disabled')}
            </dd>
          </div>
          <div>
            <dt className='text-muted-foreground'>
              {t('Inspect prompts before upstream')}
            </dt>
            <dd className='mt-1'>
              {policy.enforcement.on_prompt ? t('Enabled') : t('Disabled')}
            </dd>
          </div>
          <div className='sm:col-span-2'>
            <dt className='text-muted-foreground'>{t('Response action')}</dt>
            <dd className='mt-1'>
              {policy.enforcement.action === 'block'
                ? t('Block matching requests')
                : t('Audit matches without blocking')}
            </dd>
          </div>
          <div className='sm:col-span-2'>
            <dt className='text-muted-foreground'>{t('Reference')}</dt>
            <dd className='mt-1'>
              {policy.reference_url?.trim() ? (
                <a
                  className='focus-visible:ring-ring inline-flex max-w-full items-start gap-1 [overflow-wrap:anywhere] underline underline-offset-4 hover:no-underline focus-visible:ring-2 focus-visible:outline-none'
                  href={policy.reference_url}
                  target='_blank'
                  rel='noopener noreferrer'
                >
                  {policy.reference_url}
                  <ExternalLink
                    className='mt-1 size-3.5 shrink-0'
                    aria-hidden='true'
                  />
                </a>
              ) : (
                t('Not published')
              )}
            </dd>
          </div>
        </dl>
      </CardContent>
    </Card>
  )
}

function ProtectedGroupsSummary({ policy }: { policy: SecurityPolicy }) {
  const { t } = useTranslation()
  const groups = [
    ...new Set(
      (policy.protected_groups ?? [])
        .map((group) => group.trim())
        .filter(Boolean)
    ),
  ].sort((left, right) => left.localeCompare(right))

  return (
    <div className='border-border/70 space-y-3 rounded-xl border p-5'>
      <div className='flex items-start gap-3'>
        <ShieldCheck className='text-muted-foreground mt-0.5 size-5 shrink-0' />
        <div>
          <h3 className='font-medium'>{t('Protected groups')}</h3>
          <p className='text-muted-foreground mt-1 text-sm leading-6'>
            {t(
              'Only explicitly listed groups are covered by advanced security rules; rules do not apply globally.'
            )}
          </p>
        </div>
      </div>
      {groups.length > 0 ? (
        <div className='flex flex-wrap gap-2'>
          {groups.map((group) => (
            <Badge
              key={group}
              variant='outline'
              className='max-w-full font-mono text-xs break-all whitespace-normal'
            >
              {group}
            </Badge>
          ))}
        </div>
      ) : (
        <p className='text-muted-foreground text-sm'>
          {t('No protected groups are published yet.')}
        </p>
      )}
    </div>
  )
}

function RiskCategories({
  categories,
}: {
  categories: SecurityRiskCategory[]
}) {
  const { t } = useTranslation()

  if (categories.length === 0) {
    return (
      <UnavailableState
        title={t('No public risk categories are available yet.')}
        description={t(
          'The policy endpoint returned no risk categories. No category list is fabricated in this view.'
        )}
        icon={Layers}
      />
    )
  }

  return (
    <div className='grid gap-3 sm:grid-cols-2'>
      {categories.map((category) => (
        <Card key={category.id} size='sm'>
          <CardHeader>
            <div className='flex flex-wrap items-start justify-between gap-3'>
              <div className='flex min-w-0 flex-1 items-start gap-3'>
                <Layers className='text-muted-foreground mt-0.5 size-5 shrink-0' />
                <div className='min-w-0 [overflow-wrap:anywhere]'>
                  <CardTitle>{category.name}</CardTitle>
                  <CardDescription className='mt-1 font-mono text-xs'>
                    {category.id}
                  </CardDescription>
                </div>
              </div>
              <Badge variant='outline'>
                {displayValue(category.severity, t)}
              </Badge>
            </div>
          </CardHeader>
          <CardContent className='space-y-3'>
            <p className='text-muted-foreground leading-6'>
              {displayValue(category.description, t)}
            </p>
            <div className='text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs'>
              <span>
                {t('Layer')}: {displayValue(category.layer, t)}
              </span>
              <span>
                {t('Source')}: {displayValue(category.source, t)}
              </span>
            </div>
          </CardContent>
        </Card>
      ))}
    </div>
  )
}

function RuleSummaries({ rules }: { rules: SecurityRuleSummary[] }) {
  const { t } = useTranslation()

  if (rules.length === 0) {
    return (
      <UnavailableState
        title={t('No public rule summaries are available yet.')}
        description={t(
          'The policy endpoint returned no enabled rule summaries. No rules are fabricated in this view.'
        )}
        icon={ListChecks}
      />
    )
  }

  return (
    <div className='rounded-xl border'>
      <table
        role='table'
        className='block w-full text-left text-sm md:table md:table-fixed'
      >
        <thead className='bg-muted/40 text-muted-foreground border-b text-xs'>
          <tr role='row' className='sr-only md:not-sr-only md:table-row'>
            <th scope='col' className='px-4 py-3 font-medium md:w-1/4'>
              {t('Rule')}
            </th>
            <th scope='col' className='px-4 py-3 font-medium md:w-1/5'>
              {t('Category')}
            </th>
            <th scope='col' className='px-4 py-3 font-medium md:w-[15%]'>
              {t('Severity')}
            </th>
            <th scope='col' className='px-4 py-3 font-medium'>
              {t('Description')}
            </th>
          </tr>
        </thead>
        <tbody className='divide-border/70 block divide-y md:table-row-group'>
          {rules.map((rule) => (
            <tr
              key={rule.id}
              role='row'
              className='hover:bg-muted/20 grid grid-cols-2 gap-x-4 px-4 py-4 transition-colors md:table-row md:p-0'
            >
              <td
                role='cell'
                className='col-span-2 min-w-0 pb-3 align-top [overflow-wrap:anywhere] md:px-4 md:py-4'
              >
                <div className='font-medium'>{rule.name}</div>
                <div className='text-muted-foreground mt-1 font-mono text-xs'>
                  {rule.id} · {displayValue(rule.version, t)}
                </div>
              </td>
              <td
                role='cell'
                className='text-muted-foreground min-w-0 pb-3 align-top [overflow-wrap:anywhere] md:px-4 md:py-4'
              >
                <span
                  aria-hidden='true'
                  className='mb-1 block text-xs md:hidden'
                >
                  {t('Category')}
                </span>
                {displayValue(rule.category, t)}
              </td>
              <td
                role='cell'
                className='min-w-0 pb-3 align-top md:px-4 md:py-4'
              >
                <span
                  aria-hidden='true'
                  className='text-muted-foreground mb-1 block text-xs md:hidden'
                >
                  {t('Severity')}
                </span>
                <Badge variant='outline'>
                  {displayValue(rule.severity, t)}
                </Badge>
              </td>
              <td
                role='cell'
                className='text-muted-foreground col-span-2 min-w-0 align-top leading-6 [overflow-wrap:anywhere] md:px-4 md:py-4'
              >
                {displayValue(rule.description, t)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function ViolationFees({ fees }: { fees: SecurityViolationFeeRule[] }) {
  const { t, i18n } = useTranslation()

  if (fees.length === 0) {
    return (
      <UnavailableState
        title={t('No public violation fee rules are published yet.')}
        description={t(
          'The policy endpoint returned no public fee rules. No amount is shown until the server publishes one.'
        )}
        icon={CircleDollarSign}
      />
    )
  }

  return (
    <div className='grid gap-3 lg:grid-cols-2'>
      {fees.map((fee) => (
        <Card key={fee.code} size='sm'>
          <CardHeader>
            <div className='flex items-start justify-between gap-3'>
              <div>
                <CardTitle>{fee.provider}</CardTitle>
                <CardDescription className='mt-1 font-mono text-xs'>
                  {fee.code}
                </CardDescription>
              </div>
              <Badge variant={fee.enabled ? 'default' : 'outline'}>
                {fee.enabled ? t('Enabled') : t('Disabled')}
              </Badge>
            </div>
          </CardHeader>
          <CardContent className='space-y-4'>
            <dl className='grid gap-3 text-sm sm:grid-cols-2'>
              <div>
                <dt className='text-muted-foreground'>{t('Amount (USD)')}</dt>
                <dd className='mt-1 font-mono font-medium tabular-nums'>
                  {formatFeeAmount(fee, t, i18n.language)}
                </dd>
              </div>
              <div>
                <dt className='text-muted-foreground'>{t('Charge unit')}</dt>
                <dd className='mt-1'>{displayValue(fee.charge_unit, t)}</dd>
              </div>
              <div>
                <dt className='text-muted-foreground'>{t('Retryable')}</dt>
                <dd className='mt-1'>
                  {fee.retryable ? t('Retryable') : t('Not retryable')}
                </dd>
              </div>
              <div>
                <dt className='text-muted-foreground'>
                  {t('Local guardrail fee')}
                </dt>
                <dd className='mt-1'>
                  {fee.local_guardrail_fee ? t('Yes') : t('No')}
                </dd>
              </div>
            </dl>
            <div className='space-y-2 text-sm leading-6'>
              <p>
                <span className='font-medium'>{t('Trigger')}: </span>
                <span className='text-muted-foreground'>
                  {displayValue(fee.trigger, t)}
                </span>
              </p>
              <p className='text-muted-foreground'>
                {displayValue(fee.description, t)}
              </p>
              <p className='text-muted-foreground border-t pt-3 text-xs'>
                {displayValue(fee.charging_notes, t)}
              </p>
            </div>
          </CardContent>
        </Card>
      ))}
    </div>
  )
}

function PolicyPanel({
  policy,
  isLoading,
}: {
  policy?: SecurityPolicy
  isLoading: boolean
}) {
  const { t } = useTranslation()

  if (isLoading) {
    return (
      <div className='grid gap-3 sm:grid-cols-2'>
        <Card size='sm'>
          <CardHeader>
            <Skeleton className='h-5 w-36' />
            <Skeleton className='h-4 w-full' />
          </CardHeader>
          <CardContent>
            <Skeleton className='h-4 w-3/4' />
          </CardContent>
        </Card>
        <Card size='sm'>
          <CardHeader>
            <Skeleton className='h-5 w-32' />
            <Skeleton className='h-4 w-full' />
          </CardHeader>
          <CardContent>
            <Skeleton className='h-4 w-2/3' />
          </CardContent>
        </Card>
      </div>
    )
  }

  if (!policy) {
    return (
      <>
        <UnavailableState
          title={t('The public security policy is not available yet.')}
          description={t(
            'The policy endpoint returned no policy data. No categories, rules, or fee amounts are fabricated in this view.'
          )}
          icon={AlertTriangle}
        />
        <p className='text-muted-foreground text-xs'>
          <span className='font-medium'>{t('Policy endpoint')}</span>{' '}
          <code className='font-mono'>{SECURITY_POLICY_ENDPOINT}</code>
        </p>
      </>
    )
  }

  return (
    <div className='space-y-8'>
      <PolicyMetadata policy={policy} />
      <ProtectedGroupsSummary policy={policy} />

      <section
        aria-labelledby='security-categories-title'
        className='space-y-4'
      >
        <h3
          id='security-categories-title'
          className='scroll-mt-24 font-serif text-xl font-normal tracking-tight sm:text-2xl'
        >
          {t('Risk categories')}
        </h3>
        <RiskCategories categories={policy.risk_categories ?? []} />
      </section>

      <section aria-labelledby='security-rules-title' className='space-y-4'>
        <h3
          id='security-rules-title'
          className='scroll-mt-24 font-serif text-xl font-normal tracking-tight sm:text-2xl'
        >
          {t('Configured rule summaries')}
        </h3>
        <RuleSummaries rules={policy.rules ?? []} />
      </section>

      <section aria-labelledby='security-charges-title' className='space-y-4'>
        <div>
          <h3
            id='security-charges-title'
            className='scroll-mt-24 font-serif text-xl font-normal tracking-tight sm:text-2xl'
          >
            {t('Violation charges')}
          </h3>
          <p className='text-muted-foreground mt-2 text-sm leading-6'>
            {t('Only fees published by the live server policy are shown.')}
          </p>
        </div>
        <ViolationFees fees={policy.violation_fees ?? []} />
      </section>
    </div>
  )
}

export function SecurityContent() {
  const { t } = useTranslation()
  const isAdministrator = useAuthStore(
    (state) => (state.auth.user?.role ?? ROLE.GUEST) >= ROLE.ADMIN
  )
  const policyQuery = useQuery({
    queryKey: ['security-policy'],
    queryFn: getSecurityPolicy,
    retry: false,
    refetchOnWindowFocus: false,
    staleTime: 60_000,
  })
  const statsQuery = useQuery({
    queryKey: ['security-stats'],
    queryFn: getSecurityStats,
    retry: false,
    refetchOnWindowFocus: false,
    staleTime: 60_000,
  })
  const policy = policyQuery.data?.success ? policyQuery.data.data : undefined
  const stats = statsQuery.data?.success ? statsQuery.data.data : undefined

  return (
    <main className='mx-auto max-w-7xl px-5 pt-12 pb-20 sm:pt-16 md:px-10'>
      <div className='space-y-10 sm:space-y-12'>
        <header className='border-foreground/20 space-y-6 border-b pb-6 sm:pb-8'>
          <div className='max-w-4xl space-y-4'>
            <h1 className='font-serif text-4xl leading-[1.1] font-normal tracking-tight text-balance sm:text-5xl md:text-6xl'>
              {t('Security overview')}
            </h1>
            <p className='text-muted-foreground max-w-3xl text-base leading-7 md:text-lg'>
              {t(
                'How requests are screened, and what happens when risk is found.'
              )}
            </p>
          </div>
          <nav
            aria-label={t('Security overview')}
            className='flex flex-wrap gap-x-6 gap-y-1 text-sm'
          >
            {[
              ['security-metrics-title', 'Risk detection overview'],
              ['security-policy-title', 'Safety policy'],
              ['security-enforcement-title', 'Detection and response'],
            ].map(([id, label]) => (
              <a
                key={id}
                href={`#${id}`}
                className='text-muted-foreground hover:text-foreground focus-visible:ring-ring inline-flex min-h-11 items-center underline underline-offset-4 focus-visible:ring-2 focus-visible:outline-none'
              >
                {t(label)}
              </a>
            ))}
          </nav>
        </header>

        <Alert className='border-foreground/20 bg-foreground/[0.04]'>
          <ShieldCheck />
          <AlertTitle>{t('Public safety summary')}</AlertTitle>
          <AlertDescription>
            {t('Live metrics and charge schedules come from the server.')}
          </AlertDescription>
        </Alert>

        {isAdministrator ? (
          <Suspense
            fallback={
              <section
                className='border-border/70 border-t pt-6'
                aria-labelledby='security-audit-loading-title'
              >
                <h2
                  id='security-audit-loading-title'
                  className='scroll-mt-24 font-serif text-2xl font-normal tracking-tight sm:text-3xl'
                >
                  {t('Security audit details')}
                </h2>
                <p className='text-muted-foreground mt-2 text-sm'>
                  {t('Loading...')}
                </p>
              </section>
            }
          >
            <SecurityAuditPanel />
          </Suspense>
        ) : null}

        <p className='text-muted-foreground max-w-3xl text-xs leading-5'>
          {t(
            "This is a public summary of this site's configured safety rules. It is not official Anthropic policy, authorization, endorsement, or legal advice."
          )}
        </p>

        <section aria-labelledby='security-metrics-title' className='space-y-5'>
          <div>
            <h2
              id='security-metrics-title'
              className='scroll-mt-24 font-serif text-2xl font-normal tracking-tight sm:text-3xl'
            >
              {t('Risk detection overview')}
            </h2>
            <p className='text-muted-foreground mt-2 max-w-2xl text-sm leading-6'>
              {t('Totals appear when the public statistics endpoint responds.')}
            </p>
          </div>
          <StatsPanel stats={stats} isLoading={statsQuery.isLoading} />
          {!stats && !statsQuery.isLoading && (
            <p className='text-muted-foreground text-xs'>
              <span className='font-medium'>{t('Stats endpoint')}</span>{' '}
              <code className='font-mono'>{SECURITY_STATS_ENDPOINT}</code>
            </p>
          )}
        </section>

        <section aria-labelledby='security-policy-title' className='space-y-5'>
          <div>
            <h2
              id='security-policy-title'
              className='scroll-mt-24 font-serif text-2xl font-normal tracking-tight sm:text-3xl'
            >
              {t('Safety policy')}
            </h2>
            <p className='text-muted-foreground mt-2 max-w-3xl text-sm leading-6'>
              {t('Read from the public server policy, which takes precedence.')}
            </p>
          </div>

          <PolicyPanel policy={policy} isLoading={policyQuery.isLoading} />
        </section>

        <section
          aria-labelledby='security-enforcement-title'
          className='space-y-5'
        >
          <h2
            id='security-enforcement-title'
            className='scroll-mt-24 font-serif text-2xl font-normal tracking-tight sm:text-3xl'
          >
            {t('Detection and response')}
          </h2>
          <div className='grid gap-3 md:grid-cols-3'>
            {DETECTION_PRINCIPLES.map((principle) => {
              const Icon = principle.icon
              return (
                <Card key={principle.title} size='sm'>
                  <CardHeader>
                    <Icon className='text-muted-foreground mb-2 size-5' />
                    <CardTitle>{t(principle.title)}</CardTitle>
                  </CardHeader>
                  <CardContent className='text-muted-foreground leading-6'>
                    {t(principle.description)}
                  </CardContent>
                </Card>
              )
            })}
          </div>
        </section>

        {stats && (
          <p className='text-muted-foreground text-xs'>
            {t('Statistics window:')}{' '}
            {formatTimestampToDate(stats.start_timestamp)} —{' '}
            {formatTimestampToDate(stats.end_timestamp)}
          </p>
        )}
      </div>
    </main>
  )
}

export function Security() {
  return (
    <ForgePublicShell>
      <SecurityContent />
    </ForgePublicShell>
  )
}
