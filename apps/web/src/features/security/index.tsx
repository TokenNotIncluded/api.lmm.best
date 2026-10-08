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
import { AlertTriangle, ExternalLink, ShieldCheck } from 'lucide-react'
import { lazy, Suspense } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
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
import { formatQuotaWithCurrency } from '@/lib/currency'
import { formatNumber } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  SECURITY_STATS_ENDPOINT,
  getSecurityPolicy,
  getSecurityStats,
} from './api'
import { ModerationPolicySection } from './moderation-policy-section'
import type {
  SecurityPolicy,
  SecurityModerationStats,
  SecurityStats,
} from './types'

const ModerationAuditPanel = lazy(() =>
  import('@/features/system-settings/security/moderation-audit-panel').then(
    ({ ModerationAuditPanel: panel }) => ({ default: panel })
  )
)

type Translate = ReturnType<typeof useTranslation>['t']

function formatCount(value: number, language: string): string {
  return formatNumber(value, toIntlLocale(language))
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
  const { t } = useTranslation()

  if (isLoading) {
    return (
      <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-4'>
        {Array.from({ length: 4 }, (_, index) => (
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

  return <ModerationStatsPanel stats={stats.moderation} />
}

function ModerationStatsPanel({ stats }: { stats?: SecurityModerationStats }) {
  const { t, i18n } = useTranslation()
  if (!stats) {
    return (
      <UnavailableState
        title={t('Moderation statistics are not available yet.')}
        description={t(
          'The server has not published Moderation counts. No totals are estimated.'
        )}
      />
    )
  }
  const cards = [
    { label: 'Completed Moderation reviews', value: stats.completed },
    { label: 'Flagged Moderation reviews', value: stats.flagged },
    { label: 'Reviews with wallet deductions', value: stats.fined },
    { label: 'Failed Moderation reviews', value: stats.failed },
  ]
  return (
    <div className='space-y-3'>
      <h3 className='font-medium'>{t('All-time Moderation statistics')}</h3>
      <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-4'>
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
      <dl className='text-muted-foreground flex flex-wrap gap-x-6 gap-y-2 text-sm'>
        {[
          ['Pending reviews', stats.pending],
          ['Running reviews', stats.running],
          ['Cancelled reviews', stats.cancelled],
        ].map(([label, value]) => (
          <div key={label} className='flex gap-2'>
            <dt>{t(String(label))}</dt>
            <dd className='tabular-nums'>
              {formatCount(Number(value), i18n.language)}
            </dd>
          </div>
        ))}
        <div className='flex gap-2'>
          <dt>{t('Total Moderation wallet deductions')}</dt>
          <dd className='tabular-nums'>
            {formatQuotaWithCurrency(stats.charged_quota, {
              digitsLarge: 6,
              digitsSmall: 6,
              abbreviate: false,
              locale: toIntlLocale(i18n.language),
            })}
          </dd>
        </div>
      </dl>
      <p className='text-muted-foreground text-xs leading-5'>
        {t(
          'Flagged reviews include user input and assistant output. Only user input contributes to account risk.'
        )}
      </p>
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
              ['security-moderation-title', 'OpenAI Moderation'],
              ['security-metrics-title', 'Risk detection overview'],
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

        <ModerationPolicySection
          policy={policy?.moderation}
          isLoading={policyQuery.isLoading}
        />
        {policy ? <PolicyMetadata policy={policy} /> : null}

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
            <ModerationAuditPanel />
          </Suspense>
        ) : null}

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
