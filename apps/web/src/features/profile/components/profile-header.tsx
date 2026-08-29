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
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import { UserAvatar } from '@/components/user-avatar'
import { formatCompactNumber, formatNumber } from '@/lib/format'
import { getRoleLabel } from '@/lib/roles'
import { cn } from '@/lib/utils'

import { useProfileActivity } from '../hooks/use-profile-activity'
import { getDisplayName } from '../lib'
import {
  buildProfileDailyUsage,
  buildProfileUsageSummary,
  type ProfileDailyUsage,
} from '../lib/activity'
import type { UserProfile } from '../types'
import { ProfileActivity } from './profile-activity'

interface ProfileHeaderProps {
  profile: UserProfile | null
  loading: boolean
}

interface ProfileStat {
  label: string
  value: string
  loading?: boolean
}

function ProfileStatRail({ stats }: { stats: ProfileStat[] }) {
  return (
    <dl className='border-border/60 bg-card/25 grid grid-cols-2 overflow-hidden rounded-3xl border md:grid-cols-5'>
      {stats.map((stat, index) => (
        <div
          key={stat.label}
          className={cn(
            'border-border/60 flex min-h-24 flex-col items-center justify-center gap-1 px-3 py-4 text-center',
            index % 2 === 1 && 'border-l',
            index >= 2 && 'border-t',
            index === stats.length - 1 && 'col-span-2',
            index > 0 && 'md:border-l',
            'md:col-span-1 md:border-t-0'
          )}
        >
          <dd className='text-foreground text-xl font-medium tabular-nums sm:text-2xl'>
            {stat.loading ? (
              <Skeleton className='h-7 w-20 rounded-md' />
            ) : (
              stat.value
            )}
          </dd>
          <dt className='text-muted-foreground text-xs leading-5 sm:text-sm'>
            {stat.label}
          </dt>
        </div>
      ))}
    </dl>
  )
}

function ProfileOverviewSkeleton({ days }: { days: ProfileDailyUsage[] }) {
  const { t } = useTranslation()
  const stats = [
    'Tokens in the past year',
    'Peak daily tokens',
    'API Requests',
    'Current streak',
    'Longest streak this year',
  ].map((label) => ({ label: t(label), value: '', loading: true }))

  return (
    <section
      data-testid='profile-overview-skeleton'
      className='px-1 py-8 sm:px-6 sm:py-12 lg:px-10 lg:py-14'
    >
      <div className='flex flex-col items-center text-center'>
        <Skeleton className='size-24 rounded-full sm:size-32' />
        <Skeleton className='mt-7 h-10 w-52 rounded-lg sm:w-72' />
        <Skeleton className='mt-4 h-6 w-40 rounded-md' />
      </div>
      <div className='mt-10 sm:mt-12'>
        <ProfileStatRail stats={stats} />
      </div>
      <div className='mt-12 sm:mt-16'>
        <ProfileActivity
          days={days}
          activeDays={0}
          loading
          error={null}
          onRetry={() => undefined}
        />
      </div>
    </section>
  )
}

export function ProfileHeader({ profile, loading }: ProfileHeaderProps) {
  const { t, i18n } = useTranslation()
  const activityQuery = useProfileActivity(
    profile?.created_time,
    Boolean(profile) && !loading
  )
  const days = useMemo(
    () => buildProfileDailyUsage(activityQuery.data ?? [], activityQuery.range),
    [activityQuery.data, activityQuery.range]
  )
  const summary = useMemo(() => buildProfileUsageSummary(days), [days])

  if (loading) return <ProfileOverviewSkeleton days={days} />
  if (!profile) return null

  const locale = i18n.resolvedLanguage || i18n.language
  const displayName = getDisplayName(profile)
  const roleLabel = getRoleLabel(profile.role)
  const activityLoading = activityQuery.isPending && !activityQuery.data
  const activityError = activityQuery.data ? null : activityQuery.error
  const streakSuffix = summary.currentStreakCapped ? '+' : ''
  const stats: ProfileStat[] = [
    {
      label: t('Tokens in the past year'),
      value: formatCompactNumber(summary.totalTokens, locale),
      loading: activityLoading,
    },
    {
      label: t('Peak daily tokens'),
      value: formatCompactNumber(summary.peakDailyTokens, locale),
      loading: activityLoading,
    },
    {
      label: t('API Requests'),
      value: formatCompactNumber(profile.request_count, locale),
    },
    {
      label: t('Current streak'),
      value: `${formatNumber(summary.currentStreak, locale)}${streakSuffix} ${t('days')}`,
      loading: activityLoading,
    },
    {
      label: t('Longest streak this year'),
      value: `${formatNumber(summary.longestStreak, locale)} ${t('days')}`,
      loading: activityLoading,
    },
  ]

  if (activityError) {
    for (const index of [0, 1, 3, 4]) stats[index].value = '—'
  }

  return (
    <section
      data-testid='profile-overview'
      className='px-1 py-8 sm:px-6 sm:py-12 lg:px-10 lg:py-14'
      aria-labelledby='profile-display-name'
    >
      <div className='flex flex-col items-center text-center'>
        <UserAvatar
          name={profile.username || displayName}
          email={profile.email}
          alt={displayName}
          className='ring-border/70 size-24 rounded-full ring-1 sm:size-32'
          fallbackClassName='text-3xl sm:text-4xl'
          gravatarSize={256}
        />
        <h1
          id='profile-display-name'
          className='text-foreground mt-7 !font-sans text-3xl font-medium tracking-tight sm:text-4xl'
        >
          {displayName}
        </h1>
        <div className='text-muted-foreground mt-3 flex flex-wrap items-center justify-center gap-2 text-base sm:text-lg'>
          <span>@{profile.username}</span>
          <span aria-hidden='true'>·</span>
          <Badge
            variant='outline'
            className='text-muted-foreground h-7 px-2.5 text-sm font-normal'
          >
            {roleLabel}
          </Badge>
        </div>
      </div>

      <div className='mt-10 sm:mt-12'>
        <ProfileStatRail stats={stats} />
      </div>

      <div className='mt-12 sm:mt-16'>
        <ProfileActivity
          days={days}
          activeDays={summary.activeDays}
          loading={activityLoading}
          error={activityError}
          onRetry={() => void activityQuery.refetch()}
        />
      </div>
    </section>
  )
}
