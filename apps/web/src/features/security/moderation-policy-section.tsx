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
import { ShieldCheck } from 'lucide-react'
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
import { useWalletCurrency } from '@/hooks/use-wallet-currency'

import type {
  SecurityModerationGroupPolicy,
  SecurityModerationPolicy,
} from './types'

const MODE_LABELS = {
  off: 'Off',
  tolerant: 'Tolerant mode',
  strict: 'Strict mode',
} as const

function GroupPolicy({
  group,
  policy,
}: {
  group: string
  policy: SecurityModerationGroupPolicy
}) {
  const { t } = useTranslation()
  const { formatUSD } = useWalletCurrency()
  const fines = Object.entries(policy.category_fines_usd ?? {})
    .filter(([, amount]) => Number.isFinite(amount) && amount >= 0)
    .sort(([left], [right]) => left.localeCompare(right))

  return (
    <Card size='sm'>
      <CardHeader>
        <div className='flex flex-wrap items-start justify-between gap-3'>
          <CardTitle className='min-w-0 font-mono [overflow-wrap:anywhere]'>
            {group}
          </CardTitle>
          <Badge variant={policy.mode === 'strict' ? 'warning' : 'outline'}>
            {t(MODE_LABELS[policy.mode])}
          </Badge>
        </div>
        <CardDescription>
          {policy.mode === 'off'
            ? t('Moderation is disabled for this group.')
            : policy.mode === 'tolerant'
              ? t(
                  'Flagged user input produces a site notification without a wallet deduction.'
                )
              : t(
                  'Flagged user input may incur the configured category fee from the user wallet.'
                )}
        </CardDescription>
      </CardHeader>
      {policy.mode === 'strict' ? (
        <CardContent>
          {fines.length > 0 ? (
            <dl className='space-y-3'>
              {fines.map(([category, amount]) => (
                <div
                  key={category}
                  className='flex items-start justify-between gap-4 text-sm'
                >
                  <dt className='min-w-0 font-mono [overflow-wrap:anywhere]'>
                    {category}
                  </dt>
                  <dd className='shrink-0 tabular-nums'>{formatUSD(amount)}</dd>
                </div>
              ))}
            </dl>
          ) : (
            <p className='text-muted-foreground text-sm'>
              {t('No category fees are configured; the review fee is zero.')}
            </p>
          )}
        </CardContent>
      ) : null}
    </Card>
  )
}

export function ModerationPolicySection({
  policy,
  isLoading,
}: {
  policy?: SecurityModerationPolicy
  isLoading: boolean
}) {
  const { t } = useTranslation()
  const groups = Object.entries(policy?.group_policies ?? {}).sort(
    ([left], [right]) => left.localeCompare(right)
  )

  return (
    <section aria-labelledby='security-moderation-title' className='space-y-5'>
      <div>
        <div className='flex flex-wrap items-center gap-3'>
          <h2
            id='security-moderation-title'
            className='scroll-mt-24 font-serif text-2xl font-normal tracking-tight sm:text-3xl'
          >
            {t('OpenAI Moderation')}
          </h2>
          <Badge variant='outline'>{t('Asynchronous review')}</Badge>
        </div>
        <p className='text-muted-foreground mt-2 max-w-3xl text-sm leading-6'>
          {t(
            'Safety reviews use OpenAI official Moderation models in the background. Requests do not wait for the review verdict.'
          )}
        </p>
      </div>

      {isLoading ? (
        <Skeleton className='h-24 w-full' />
      ) : policy ? (
        <Card size='sm'>
          <CardHeader>
            <CardTitle>{t('Current Moderation settings')}</CardTitle>
            <CardDescription>
              {t(
                'Features and groups are disabled by default. Administrators choose the review mode for each group.'
              )}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <dl className='grid gap-4 text-sm sm:grid-cols-2'>
              <div>
                <dt className='text-muted-foreground'>
                  {t('Security audit Moderation')}
                </dt>
                <dd className='mt-1 font-medium'>
                  {policy.enabled ? t('Enabled') : t('Disabled')}
                </dd>
              </div>
              <div>
                <dt className='text-muted-foreground'>
                  {t('AI assistant Moderation')}
                </dt>
                <dd className='mt-1 font-medium'>
                  {policy.assistant_enabled ? t('Enabled') : t('Disabled')}
                </dd>
              </div>
            </dl>
          </CardContent>
        </Card>
      ) : (
        <Alert>
          <ShieldCheck />
          <AlertTitle>
            {t('Moderation settings are not published yet.')}
          </AlertTitle>
          <AlertDescription>
            {t(
              'The server has not returned Moderation configuration. Active groups and fees cannot be confirmed.'
            )}
          </AlertDescription>
        </Alert>
      )}

      <div className='text-muted-foreground max-w-4xl space-y-3 text-sm leading-6'>
        <p>
          {t(
            'When enabled, reviewable text is sent to OpenAI for safety classification.'
          )}
        </p>
        <p>
          {t(
            'Tolerant mode sends a warning through site notifications. Strict mode can also deduct a category fee from the user wallet.'
          )}
        </p>
        <p>
          {t(
            'A strict review charges the largest configured fee among matched categories once. Unset category fees are $0, and reviews never overdraw the wallet.'
          )}
        </p>
        <p>
          {t(
            'Deductions round down to the wallet’s smallest supported amount. Smaller fees only trigger a warning.'
          )}
        </p>
        <p>
          {t(
            'Reviewed violations in user input contribute to the account risk score. Model output violations do not charge the user or increase their risk score.'
          )}
        </p>
        <p>
          {t(
            'These reviews currently support text only. Notifications stay inside the site.'
          )}
        </p>
      </div>

      {policy ? (
        <div className='space-y-3'>
          <h3 className='font-medium'>{t('Moderation group policies')}</h3>
          <p className='text-muted-foreground text-sm leading-6'>
            {t(
              'The following are configured group modes. A disabled feature does not review requests, even when a group mode is configured.'
            )}
          </p>
          {groups.length > 0 ? (
            <div className='grid gap-3 md:grid-cols-2'>
              {groups.map(([group, groupPolicy]) => (
                <GroupPolicy key={group} group={group} policy={groupPolicy} />
              ))}
            </div>
          ) : (
            <p className='text-muted-foreground text-sm'>
              {t(
                'No Moderation groups are configured. Unlisted groups remain disabled.'
              )}
            </p>
          )}
        </div>
      ) : null}

      <a
        href='/legal/safety-review.html'
        className='text-muted-foreground hover:text-foreground focus-visible:ring-ring inline-flex min-h-11 items-center text-sm underline underline-offset-4 focus-visible:ring-2 focus-visible:outline-none'
      >
        {t('Read the safety review notice')}
      </a>
    </section>
  )
}
