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
  ArrowRight01Icon,
  CircleDollarSignIcon,
  PackageSearchIcon,
  ReloadIcon,
  SparklesIcon,
  Tag01Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import type { TFunction } from 'i18next'
import { type ReactNode, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  Alert,
  AlertAction,
  AlertDescription,
  AlertTitle,
} from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { formatDuration, formatResetPeriod } from '@/features/subscriptions/lib'
import { formatCreditBalance as formatCreditBalanceBase } from '@/features/wallet/lib/format'
import { toIntlLocale } from '@/i18n/languages'
import { formatFiatCurrencyAmount, getCurrencyDisplay } from '@/lib/currency'

import { getAssistantPlanOffers } from './api'
import {
  compareAssistantPlans,
  getAssistantTopupOffers,
} from './plan-recommender'

function formatPlanPrice(amount: number, currency: string, locale?: string) {
  return formatFiatCurrencyAmount(amount, currency || 'USD', {
    locale,
    abbreviate: false,
    digitsLarge: 2,
    digitsSmall: 2,
  })
}

function getRecommendationMessage(
  t: TFunction,
  monthlyCreditUSD: number | null,
  expectedCreditUSD: number,
  amount: string
) {
  if (monthlyCreditUSD === null) {
    return t(
      'Recommended because unlimited capacity covers your {{amount}} monthly estimate.',
      { amount }
    )
  }
  if (monthlyCreditUSD >= expectedCreditUSD) {
    return t(
      'Recommended as the smallest available capacity that covers your {{amount}} monthly estimate.',
      { amount }
    )
  }
  return t(
    'No plan fully covers your {{amount}} monthly estimate; this option has the highest available capacity.',
    { amount }
  )
}

export function AssistantPlanTool(props: {
  developerAccessGranted: boolean
  onRequestAccess: () => void
}) {
  const { t, i18n } = useTranslation()
  const formatCreditBalance = (amount: number) =>
    formatCreditBalanceBase(amount, t('Platform'))
  const [expectedCredit, setExpectedCredit] = useState('20')
  const [topupCredit, setTopupCredit] = useState('100')
  const offersQuery = useQuery({
    queryKey: ['assistant-plan-offers'],
    queryFn: getAssistantPlanOffers,
    staleTime: 5 * 60 * 1000,
    retry: false,
  })
  const expected = Number(expectedCredit)
  const normalizedExpected =
    Number.isFinite(expected) && expected > 0 ? expected : 0
  const quotaPerUnit = getCurrencyDisplay().config.quotaPerUnit
  const comparisons = useMemo(
    () =>
      compareAssistantPlans(
        offersQuery.data?.plans ?? [],
        expected,
        quotaPerUnit
      ),
    [expected, offersQuery.data?.plans, quotaPerUnit]
  )
  const offers = useMemo(
    () => getAssistantTopupOffers(offersQuery.data?.topup_discounts),
    [offersQuery.data?.topup_discounts]
  )
  const topupAmount = Number(topupCredit)
  const normalizedTopupAmount =
    Number.isFinite(topupAmount) && topupAmount > 0 ? topupAmount : 0
  const exactTopupOffer = offers.find(
    (offer) => offer.amount === normalizedTopupAmount
  )
  const recommendedTopupOffer = exactTopupOffer ?? offers[0]
  const readOnly = offersQuery.data?.read_only === true
  const checkoutAvailable = offersQuery.data?.checkout_available === true

  let planContent: ReactNode = (
    <div className='grid gap-2'>
      {comparisons.slice(0, 3).map((comparison) => {
        const plan = comparison.record.plan
        return (
          <div
            key={plan.id}
            className={
              comparison.recommended
                ? 'border-primary/70 bg-primary/5 grid gap-2 rounded-lg border p-3'
                : 'grid gap-2 rounded-lg border p-3'
            }
          >
            <div className='flex items-start justify-between gap-3'>
              <div className='min-w-0'>
                <p className='truncate text-sm font-medium'>{plan.title}</p>
                <p className='text-muted-foreground text-xs'>
                  {formatDuration(plan, t)} · {formatResetPeriod(plan, t)}
                </p>
              </div>
              {comparison.recommended ? (
                <Badge className='shrink-0' variant='secondary'>
                  <HugeiconsIcon
                    icon={SparklesIcon}
                    strokeWidth={2}
                    aria-hidden='true'
                  />
                  {t('Closest fit')}
                </Badge>
              ) : null}
            </div>
            <div className='flex flex-wrap items-center justify-between gap-2 text-xs'>
              <span className='text-muted-foreground'>
                {t('Estimated monthly capacity')}:{' '}
                <strong className='text-foreground'>
                  {comparison.monthlyCreditUSD === null
                    ? t('Unlimited')
                    : formatCreditBalance(comparison.monthlyCreditUSD)}
                </strong>
              </span>
              <strong>
                {formatPlanPrice(
                  Number(plan.price_amount || 0),
                  plan.currency,
                  toIntlLocale(i18n.language)
                )}
              </strong>
            </div>
            {comparison.recommended ? (
              <p className='text-muted-foreground text-xs leading-5'>
                {getRecommendationMessage(
                  t,
                  comparison.monthlyCreditUSD,
                  normalizedExpected,
                  formatCreditBalance(normalizedExpected)
                )}
              </p>
            ) : null}
          </div>
        )
      })}
    </div>
  )
  if (offersQuery.isLoading) {
    planContent = (
      <div className='grid gap-2' aria-label={t('Loading...')}>
        <Skeleton className='h-24 w-full' />
        <Skeleton className='h-20 w-full' />
      </div>
    )
  } else if (offersQuery.isError) {
    planContent = (
      <Alert variant='destructive'>
        <HugeiconsIcon icon={Alert02Icon} strokeWidth={2} aria-hidden='true' />
        <AlertTitle>{t('Unable to load live subscription plans')}</AlertTitle>
        <AlertDescription>
          {t(
            'Plan recommendations are unavailable until current quotas and prices can be loaded.'
          )}
        </AlertDescription>
        <AlertAction>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => void offersQuery.refetch()}
          >
            <HugeiconsIcon
              icon={ReloadIcon}
              strokeWidth={2}
              data-icon='inline-start'
              aria-hidden='true'
            />
            {t('Retry')}
          </Button>
        </AlertAction>
      </Alert>
    )
  } else if (comparisons.length === 0) {
    planContent = (
      <Empty className='min-h-36 border'>
        <EmptyHeader>
          <EmptyMedia variant='icon'>
            <HugeiconsIcon
              icon={PackageSearchIcon}
              strokeWidth={2}
              aria-hidden='true'
            />
          </EmptyMedia>
          <EmptyTitle>
            {t('No subscription plans are currently available.')}
          </EmptyTitle>
          <EmptyDescription>
            {t('You can still add wallet funds and use pay-as-you-go billing.')}
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  let topupContent: ReactNode
  if (offersQuery.isLoading) {
    topupContent = (
      <Skeleton className='h-14 w-full' aria-label={t('Loading...')} />
    )
  } else if (offersQuery.isError) {
    topupContent = (
      <Alert>
        <HugeiconsIcon icon={Alert02Icon} strokeWidth={2} aria-hidden='true' />
        <AlertTitle>{t('Unable to load current top-up discounts')}</AlertTitle>
        <AlertDescription>
          {t(
            'Plan recommendations remain available, but discount details may be outdated.'
          )}
        </AlertDescription>
      </Alert>
    )
  } else if (offers.length > 0) {
    const discountPercent = recommendedTopupOffer
      ? new Intl.NumberFormat(toIntlLocale(i18n.language), {
          maximumFractionDigits: 1,
        }).format(recommendedTopupOffer.savingsPercent)
      : ''
    topupContent = (
      <div className='grid gap-3'>
        <div className='flex flex-wrap gap-2'>
          {offers.slice(0, 3).map((offer) => (
            <Badge key={offer.amount} variant='outline'>
              {formatCreditBalance(offer.amount)} ·{' '}
              {t('save {{percent}}%', {
                percent: new Intl.NumberFormat(toIntlLocale(i18n.language), {
                  maximumFractionDigits: 1,
                }).format(offer.savingsPercent),
              })}
            </Badge>
          ))}
        </div>
        <div className='grid gap-1.5'>
          <Label htmlFor='assistant-topup-credit'>
            {t('Platform credit to compare')}
          </Label>

          <Input
            id='assistant-topup-credit'
            type='number'
            inputMode='decimal'
            min={0}
            step={5}
            value={topupCredit}
            onChange={(event) => setTopupCredit(event.target.value)}
          />
        </div>
        {recommendedTopupOffer ? (
          <div className='grid gap-3 rounded-lg border p-3'>
            <div className='flex flex-wrap items-center gap-2'>
              <Badge variant='secondary'>
                {exactTopupOffer ? t('Configured offer') : t('Suggested offer')}
              </Badge>
              {!exactTopupOffer && normalizedTopupAmount > 0 ? (
                <span className='text-muted-foreground text-xs leading-5'>
                  {t(
                    'No exact discount matches {{amount}}. Showing the best current configured offer instead.',
                    { amount: formatCreditBalance(normalizedTopupAmount) }
                  )}
                </span>
              ) : null}
            </div>
            <dl className='grid grid-cols-2 gap-x-4 gap-y-2 text-xs'>
              <dt className='text-muted-foreground'>
                {t('Credited platform balance')}
              </dt>
              <dd className='text-right font-medium'>
                {formatCreditBalance(recommendedTopupOffer.amount)}
              </dd>
              <dt className='text-muted-foreground'>
                {t('Configured discount')}
              </dt>
              <dd className='text-right font-medium'>{discountPercent}%</dd>
              <dt className='text-muted-foreground'>
                {t('Estimated discounted base amount')}
              </dt>
              <dd className='text-right font-medium'>
                {formatCreditBalance(
                  recommendedTopupOffer.amount *
                    recommendedTopupOffer.multiplier
                )}
              </dd>
              <dt className='text-muted-foreground'>
                {t('Estimated savings')}
              </dt>
              <dd className='text-right font-medium'>
                {formatCreditBalance(
                  recommendedTopupOffer.amount *
                    (1 - recommendedTopupOffer.multiplier)
                )}
              </dd>
            </dl>
          </div>
        ) : null}
        {readOnly ? (
          <p className='text-muted-foreground text-xs leading-5'>
            {t(
              'This read-only estimate applies the configured amount discount only. It does not start a payment; currency, payment method, and group multipliers are shown only after L1 approval.'
            )}
          </p>
        ) : null}
      </div>
    )
  } else {
    topupContent = (
      <p className='text-muted-foreground text-xs'>
        {t('No top-up discount is currently available.')}
      </p>
    )
  }

  let checkoutContent: ReactNode
  if (checkoutAvailable) {
    checkoutContent = (
      <Button
        variant='outline'
        className='w-full justify-center sm:w-auto'
        render={<Link to='/wallet' />}
      >
        {t('Review plans and exact checkout prices')}
        <HugeiconsIcon
          icon={ArrowRight01Icon}
          strokeWidth={2}
          data-icon='inline-end'
          aria-hidden='true'
        />
      </Button>
    )
  } else if (readOnly) {
    checkoutContent = (
      <Alert>
        <HugeiconsIcon icon={Alert02Icon} strokeWidth={2} aria-hidden='true' />
        <AlertTitle>{t('Read-only plan advice')}</AlertTitle>
        <AlertDescription>
          {props.developerAccessGranted
            ? t('Payment is unavailable for this account.')
            : t(
                'You can compare live plans and discounts now. Checkout and payment remain locked until automatic review approves L1 or human fallback completes.'
              )}
        </AlertDescription>
        {!props.developerAccessGranted ? (
          <AlertAction>
            <Button
              type='button'
              variant='outline'
              onClick={props.onRequestAccess}
            >
              {t('Unlock L1 access')}
              <HugeiconsIcon
                icon={ArrowRight01Icon}
                strokeWidth={2}
                data-icon='inline-end'
                aria-hidden='true'
              />
            </Button>
          </AlertAction>
        ) : null}
      </Alert>
    )
  } else {
    checkoutContent = (
      <Alert>
        <HugeiconsIcon icon={Alert02Icon} strokeWidth={2} aria-hidden='true' />
        <AlertTitle>{t('Payment unavailable')}</AlertTitle>
        <AlertDescription>
          {t('Payment is unavailable for this account.')}
        </AlertDescription>
      </Alert>
    )
  }

  return (
    <Card size='sm'>
      <CardHeader>
        <CardTitle className='flex items-center gap-2'>
          <HugeiconsIcon
            icon={CircleDollarSignIcon}
            className='size-4'
            strokeWidth={2}
            aria-hidden='true'
          />
          {t('Live plan and discount advisor')}
          {readOnly ? <Badge variant='outline'>{t('Read-only')}</Badge> : null}
        </CardTitle>
        <CardDescription>
          {t(
            'Enter the API credit you expect to use each month. Recommendations use current plan quotas, not marketing labels.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='grid gap-4'>
        <div className='grid gap-1.5'>
          <Label htmlFor='assistant-expected-credit'>
            {t('Expected monthly platform credit')}
          </Label>
          <Input
            id='assistant-expected-credit'
            type='number'
            inputMode='decimal'
            min={0}
            step={5}
            value={expectedCredit}
            onChange={(event) => setExpectedCredit(event.target.value)}
          />
        </div>

        {planContent}

        <Separator />
        <section className='grid gap-2' aria-labelledby='assistant-topup-title'>
          <div
            id='assistant-topup-title'
            className='flex items-center gap-2 text-sm font-medium'
          >
            <HugeiconsIcon
              icon={Tag01Icon}
              className='size-4'
              strokeWidth={2}
              aria-hidden='true'
            />
            {t('Best current top-up discounts')}
          </div>
          {topupContent}
        </section>

        <p className='text-muted-foreground text-xs leading-5'>
          {t(
            'Plan fit compares included credit only. Reset rules, model access, payment method, and group multipliers may change the final value; checkout remains authoritative.'
          )}
        </p>
        {checkoutContent}
      </CardContent>
    </Card>
  )
}
