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
import { Crown, CalendarClock, Package } from 'lucide-react'
import { useState, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { GroupBadge } from '@/components/group-badge'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Separator } from '@/components/ui/separator'
import { useCheckoutScope } from '@/features/wallet/hooks/use-checkout-scope'
import {
  cancelPaymentCheckout,
  redirectCurrentWindowToPaymentCheckout,
  redirectToPaymentCheckout,
  reservePaymentCheckout,
  submitPaymentForm,
} from '@/features/wallet/lib'
import {
  expectedSettlement,
  formatSettlementQuote,
  getAvailableSettlementQuote,
  isSettlementQuoteChanged,
  SettlementQuoteChangedError,
} from '@/features/wallet/lib/settlement-quote'
import { formatQuota } from '@/lib/format'
import {
  getDefaultWaffoPancakeCheckoutRegion,
  getWaffoPancakeCheckoutLanguage,
  type WaffoPancakeCheckoutRegion,
} from '@/lib/waffo-pancake-checkout'

import {
  paySubscriptionStripe,
  paySubscriptionCreem,
  paySubscriptionEpay,
  paySubscriptionWaffoPancake,
  paySubscriptionBalance,
} from '../../api'
import { usePublicPlans } from '../../hooks/use-public-plans'
import { formatDuration, formatResetPeriod } from '../../lib'
import { formatPlanSourcePrice } from '../../lib/source-price'
import type { PlanRecord } from '../../types'

interface PaymentMethod {
  type: string
  name?: string
}

function getPlanEpayMethods(
  paymentMethods: string[] | undefined,
  epayMethods: PaymentMethod[] | undefined
): PaymentMethod[] {
  if (!Array.isArray(paymentMethods)) return epayMethods || []
  return (epayMethods || []).filter((method) =>
    paymentMethods.includes(method.type)
  )
}

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  plan: PlanRecord | null
  enableStripe?: boolean
  enableCreem?: boolean
  enableWaffoPancake?: boolean
  enableOnlineTopUp?: boolean
  epayMethods?: PaymentMethod[]
  /** Authoritative per-plan checkout catalog from /subscription/plans. */
  paymentMethods?: string[]
  purchaseLimit?: number
  purchaseCount?: number
  userQuota?: number
  onPurchaseSuccess?: () => void | Promise<void>
  onCheckoutStarted?: () => void
}

export function SubscriptionPurchaseDialog(props: Props) {
  const { key } = useCheckoutScope()
  const paymentInFlightRef = useRef(false)
  return (
    <ScopedSubscriptionPurchaseDialog
      key={`${key}:${props.plan?.plan.id}:${props.open}`}
      {...props}
      acquirePayment={() => {
        if (paymentInFlightRef.current) return false
        paymentInFlightRef.current = true
        return true
      }}
      releasePayment={() => {
        paymentInFlightRef.current = false
      }}
    />
  )
}

function ScopedSubscriptionPurchaseDialog(
  props: Props & { acquirePayment: () => boolean; releasePayment: () => void }
) {
  const { t, i18n } = useTranslation()
  const { isCurrent } = useCheckoutScope()
  const plansQuery = usePublicPlans(props.open && !!props.plan)
  const [paying, setPaying] = useState(false)
  const [quoteInvalidated, setQuoteInvalidated] = useState(false)
  const [quoteChanged, setQuoteChanged] = useState(false)
  const [selectedEpayMethodOverride, setSelectedEpayMethod] = useState('')
  const [waffoPancakeCheckoutRegionOverride, setWaffoPancakeCheckoutRegion] =
    useState<WaffoPancakeCheckoutRegion | null>(null)

  const planRecord = props.plan
  if (!planRecord) return null
  const plan = planRecord.plan

  const hasAuthoritativePaymentCatalog = Array.isArray(props.paymentMethods)
  const paymentMethods = hasAuthoritativePaymentCatalog
    ? props.paymentMethods || []
    : []
  const hasStripe = hasAuthoritativePaymentCatalog
    ? paymentMethods.includes('stripe')
    : props.enableStripe && !!plan.stripe_price_id
  const hasCreem = hasAuthoritativePaymentCatalog
    ? paymentMethods.includes('creem')
    : props.enableCreem && !!plan.creem_product_id
  const hasWaffoPancake = hasAuthoritativePaymentCatalog
    ? paymentMethods.includes('waffo_pancake')
    : props.enableWaffoPancake && !!plan.waffo_pancake_product_id
  const availableEpayMethods = getPlanEpayMethods(
    props.paymentMethods,
    props.epayMethods
  )
  const hasEpay = hasAuthoritativePaymentCatalog
    ? availableEpayMethods.length > 0
    : props.enableOnlineTopUp && availableEpayMethods.length > 0
  const quotedPlan =
    !plansQuery.isError && !plansQuery.isFetching
      ? plansQuery.data?.find((record) => record.plan.id === plan.id)
      : undefined
  const settlementQuote =
    !quoteInvalidated &&
    (!quotedPlan?.payment_methods ||
      quotedPlan.payment_methods.includes('waffo_pancake'))
      ? getAvailableSettlementQuote(quotedPlan?.waffo_pancake_settlement)
      : null
  const showWaffoPancake =
    hasWaffoPancake ||
    planRecord.waffo_pancake_settlement !== undefined ||
    quotedPlan?.waffo_pancake_settlement !== undefined
  const hasAnyPayment = hasStripe || hasCreem || showWaffoPancake || hasEpay
  const interfaceLanguage = i18n.resolvedLanguage || i18n.language
  const waffoPancakeCheckoutRegion =
    waffoPancakeCheckoutRegionOverride ??
    getDefaultWaffoPancakeCheckoutRegion(interfaceLanguage)
  const waffoPancakeCheckoutLanguage =
    getWaffoPancakeCheckoutLanguage(interfaceLanguage)
  const selectedEpayMethod =
    availableEpayMethods.find(
      (method) => method.type === selectedEpayMethodOverride
    )?.type ??
    availableEpayMethods[0]?.type ??
    ''
  const selectedEpayMethodLabel =
    availableEpayMethods.find((m) => m.type === selectedEpayMethod)?.name ||
    selectedEpayMethod ||
    t('Select payment method')
  const totalAmount = Number(plan.total_amount || 0)
  const price = formatPlanSourcePrice(plan)
  const balanceCost = Math.max(
    0,
    Math.ceil(Number(planRecord.balance_price_quota || 0))
  )
  const userQuota = Math.max(0, Number(props.userQuota || 0))
  const allowBalancePay = hasAuthoritativePaymentCatalog
    ? paymentMethods.includes('balance') && balanceCost > 0
    : plan.allow_balance_pay !== false && balanceCost > 0
  const insufficientBalance = userQuota < balanceCost
  const limitReached =
    (props.purchaseLimit || 0) > 0 &&
    (props.purchaseCount || 0) >= (props.purchaseLimit || 0)

  const beginPayment = () => {
    if (
      !props.open ||
      !isCurrent() ||
      limitReached ||
      !props.acquirePayment()
    ) {
      return false
    }
    setPaying(true)
    return true
  }
  const finishPayment = () => {
    props.releasePayment()
    if (isCurrent()) setPaying(false)
  }
  const refreshSettlementQuote = async () => {
    if (!isCurrent()) return
    setQuoteInvalidated(true)
    const result = await plansQuery.refetch()
    if (isCurrent() && result.isSuccess) setQuoteInvalidated(false)
  }

  const handlePayStripe = async () => {
    if (!beginPayment()) return
    let checkout: ReturnType<typeof reservePaymentCheckout> | null = null
    try {
      checkout = reservePaymentCheckout()
      const res = await paySubscriptionStripe({ plan_id: plan.id })
      if (!isCurrent()) {
        cancelPaymentCheckout(checkout)
        return
      }
      if (res.message === 'success' && res.data?.pay_link) {
        if (!redirectToPaymentCheckout(checkout, res.data.pay_link)) {
          cancelPaymentCheckout(checkout)
          toast.error(t('Invalid payment redirect URL'))
          return
        }
        props.onCheckoutStarted?.()
        toast.success(t('Payment page opened'))
        props.onOpenChange(false)
      } else {
        cancelPaymentCheckout(checkout)
        toast.error(
          res.message && res.message !== 'success'
            ? res.message
            : t('Payment request failed')
        )
      }
    } catch {
      if (checkout) cancelPaymentCheckout(checkout)
      if (isCurrent()) toast.error(t('Payment request failed'))
    } finally {
      finishPayment()
    }
  }

  const handlePayCreem = async () => {
    if (!beginPayment()) return
    let checkout: ReturnType<typeof reservePaymentCheckout> | null = null
    try {
      checkout = reservePaymentCheckout()
      const res = await paySubscriptionCreem({ plan_id: plan.id })
      if (!isCurrent()) {
        cancelPaymentCheckout(checkout)
        return
      }
      if (res.message === 'success' && res.data?.checkout_url) {
        if (!redirectToPaymentCheckout(checkout, res.data.checkout_url)) {
          cancelPaymentCheckout(checkout)
          toast.error(t('Invalid payment redirect URL'))
          return
        }
        props.onCheckoutStarted?.()
        toast.success(t('Payment page opened'))
        props.onOpenChange(false)
      } else {
        cancelPaymentCheckout(checkout)
        toast.error(
          res.message && res.message !== 'success'
            ? res.message
            : t('Payment request failed')
        )
      }
    } catch {
      if (checkout) cancelPaymentCheckout(checkout)
      if (isCurrent()) toast.error(t('Payment request failed'))
    } finally {
      finishPayment()
    }
  }

  // In-tab redirect (not window.open) — user-gesture context is lost
  // across the await, so a popup would be blocked. Same as the wallet hook.
  const handlePayWaffoPancake = async () => {
    if (!hasWaffoPancake || !settlementQuote || !beginPayment()) return
    try {
      const res = await paySubscriptionWaffoPancake({
        plan_id: plan.id,
        checkout_region: waffoPancakeCheckoutRegion,
        checkout_language: waffoPancakeCheckoutLanguage,
        ...expectedSettlement(settlementQuote),
      })
      if (!isCurrent()) return
      if (isSettlementQuoteChanged(res)) throw new SettlementQuoteChangedError()
      if (res.message === 'success' && res.data?.checkout_url) {
        if (!redirectCurrentWindowToPaymentCheckout(res.data.checkout_url)) {
          toast.error(t('Invalid payment redirect URL'))
          return
        }
        props.onCheckoutStarted?.()
        toast.success(t('Redirecting to payment page...'))
      } else {
        toast.error(
          res.message && res.message !== 'success'
            ? res.message
            : t('Payment request failed')
        )
      }
    } catch (error) {
      if (!isCurrent()) return
      if (isSettlementQuoteChanged(error)) {
        setQuoteChanged(true)
        toast.error(
          t(
            'Settlement quote changed. Review the refreshed amount and confirm again.'
          )
        )
        // Refresh only the preview. A new user click must confirm the new pair.
        await refreshSettlementQuote()
      } else {
        toast.error(t('Payment request failed'))
      }
    } finally {
      finishPayment()
    }
  }

  const handlePayEpay = async () => {
    if (!isCurrent()) return
    if (!selectedEpayMethod) {
      toast.error(t('Please select a payment method'))
      return
    }
    if (!beginPayment()) return
    let checkout: ReturnType<typeof reservePaymentCheckout> | null = null
    try {
      checkout = reservePaymentCheckout()
      const res = await paySubscriptionEpay({
        plan_id: plan.id,
        payment_method: selectedEpayMethod,
      })
      if (!isCurrent()) {
        cancelPaymentCheckout(checkout)
        return
      }
      if (res.message === 'success' && res.url) {
        if (!submitPaymentForm(res.url, res.data || {}, checkout.target)) {
          cancelPaymentCheckout(checkout)
          toast.error(t('Invalid payment redirect URL'))
          return
        }
        props.onCheckoutStarted?.()
        toast.success(t('Payment initiated'))
        props.onOpenChange(false)
      } else {
        cancelPaymentCheckout(checkout)
        toast.error(
          res.message && res.message !== 'success'
            ? res.message
            : t('Payment request failed')
        )
      }
    } catch {
      if (checkout) cancelPaymentCheckout(checkout)
      if (isCurrent()) toast.error(t('Payment request failed'))
    } finally {
      finishPayment()
    }
  }

  const handlePayBalance = async () => {
    if (!isCurrent()) return
    if (!allowBalancePay) {
      toast.error(t('This plan does not allow balance redemption'))
      return
    }
    if (insufficientBalance || !beginPayment()) return
    try {
      const res = await paySubscriptionBalance({ plan_id: plan.id })
      if (!isCurrent()) return
      if (res.success) {
        toast.success(t('Subscription purchased successfully'))
        void props.onPurchaseSuccess?.()
        props.onOpenChange(false)
      } else {
        toast.error(
          res.message && res.message !== 'success'
            ? res.message
            : t('Payment request failed')
        )
      }
    } catch {
      if (isCurrent()) toast.error(t('Payment request failed'))
    } finally {
      finishPayment()
    }
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={
        <>
          <Crown className='h-5 w-5' />
          {t('Purchase Subscription')}
        </>
      }
      contentClassName='max-sm:w-[calc(100vw-1.5rem)] sm:max-w-md'
      titleClassName='flex items-center gap-2'
      contentHeight='auto'
      bodyClassName='space-y-4'
    >
      <div className='space-y-3 sm:space-y-4'>
        <div className='bg-muted/50 space-y-2.5 rounded-lg border p-3 sm:space-y-3 sm:p-4'>
          <div className='flex justify-between'>
            <span className='text-muted-foreground text-sm'>
              {t('Plan Name')}
            </span>
            <span className='max-w-[200px] truncate text-sm font-medium'>
              {plan.title}
            </span>
          </div>
          <div className='flex items-center justify-between'>
            <span className='text-muted-foreground text-sm'>
              {t('Validity Period')}
            </span>
            <span className='flex items-center gap-1 text-sm'>
              <CalendarClock className='h-3.5 w-3.5' />
              {formatDuration(plan, t)}
            </span>
          </div>
          {formatResetPeriod(plan, t) !== t('No Reset') && (
            <div className='flex justify-between'>
              <span className='text-muted-foreground text-sm'>
                {t('Reset Period')}
              </span>
              <span className='text-sm'>{formatResetPeriod(plan, t)}</span>
            </div>
          )}
          <div className='flex items-center justify-between'>
            <span className='text-muted-foreground text-sm'>
              {t('Plan Quota')}
            </span>
            <span className='flex items-center gap-1 text-sm'>
              <Package className='h-3.5 w-3.5' />
              {totalAmount > 0 ? formatQuota(totalAmount) : t('Unlimited')}
            </span>
          </div>
          {plan.upgrade_group && (
            <div className='flex items-center justify-between'>
              <span className='text-muted-foreground text-sm'>
                {t('Upgrade Group')}
              </span>
              <GroupBadge group={plan.upgrade_group} />
            </div>
          )}
          <Separator />
          <div className='flex items-center justify-between'>
            <span className='text-sm font-medium'>{t('Source price')}</span>
            <span className='text-primary text-lg font-bold'>
              {price ?? t('Source price unavailable')}
            </span>
          </div>
        </div>

        {showWaffoPancake && (
          <div
            className='space-y-2'
            aria-live='polite'
            aria-busy={plansQuery.isFetching}
          >
            <div className='flex flex-wrap items-center justify-between gap-2 text-sm'>
              <span>{t('Waffo Pancake payable')}</span>
              <span className='font-medium'>
                {settlementQuote
                  ? formatSettlementQuote(settlementQuote)
                  : plansQuery.isFetching
                    ? t('Loading...')
                    : t('Settlement quote unavailable')}
              </span>
            </div>
            {quoteChanged && (
              <Alert>
                <AlertDescription>
                  {t(
                    'Settlement quote changed. Review the refreshed amount and confirm again.'
                  )}
                </AlertDescription>
              </Alert>
            )}
            {!settlementQuote && !plansQuery.isFetching && (
              <Alert>
                <AlertDescription>
                  {t(
                    quotedPlan?.waffo_pancake_settlement?.reason ||
                      'Settlement quote unavailable'
                  )}
                </AlertDescription>
              </Alert>
            )}
            <div className='flex flex-wrap items-center gap-3 text-xs'>
              <Button
                variant='outline'
                size='sm'
                disabled={paying || plansQuery.isFetching}
                onClick={() => void refreshSettlementQuote()}
              >
                {t('Refresh')}
              </Button>
              <a href='/profile' className='underline underline-offset-4'>
                {t('Change settlement currency')}
              </a>
            </div>
          </div>
        )}

        {limitReached && (
          <Alert variant='destructive'>
            <AlertDescription>
              {t('Purchase limit reached')} ({props.purchaseCount}/
              {props.purchaseLimit})
            </AlertDescription>
          </Alert>
        )}

        <div className='flex flex-col gap-2 rounded-md border p-3'>
          <div className='flex items-center justify-between gap-2 text-xs'>
            <span className='text-muted-foreground'>{t('Required')}</span>
            <span>{formatQuota(balanceCost)}</span>
          </div>
          <div className='flex items-center justify-between gap-2 text-xs'>
            <span className='text-muted-foreground'>{t('Available')}</span>
            <span>{formatQuota(userQuota)}</span>
          </div>
          {!allowBalancePay ? (
            <Alert variant='destructive'>
              <AlertDescription>
                {t('This plan does not allow balance redemption')}
              </AlertDescription>
            </Alert>
          ) : (
            insufficientBalance && (
              <Alert variant='destructive'>
                <AlertDescription>{t('Insufficient balance')}</AlertDescription>
              </Alert>
            )
          )}
          <Button
            variant='outline'
            onClick={handlePayBalance}
            disabled={
              paying || limitReached || !allowBalancePay || insufficientBalance
            }
          >
            {t('Pay with Balance')}
          </Button>
        </div>

        {hasAnyPayment && (
          <div className='space-y-3'>
            <p className='text-muted-foreground text-xs'>
              {t('Select payment method')}
            </p>
            {(hasStripe || hasCreem || showWaffoPancake) && (
              <div className='grid grid-cols-2 gap-2 sm:flex'>
                {hasStripe && (
                  <Button
                    variant='outline'
                    className='flex-1'
                    onClick={handlePayStripe}
                    disabled={paying || limitReached}
                  >
                    Stripe
                  </Button>
                )}
                {hasCreem && (
                  <Button
                    variant='outline'
                    className='flex-1'
                    onClick={handlePayCreem}
                    disabled={paying || limitReached}
                  >
                    Creem
                  </Button>
                )}
                {showWaffoPancake && (
                  <Button
                    variant='outline'
                    className='flex-1'
                    onClick={handlePayWaffoPancake}
                    disabled={
                      paying ||
                      limitReached ||
                      !hasWaffoPancake ||
                      !settlementQuote
                    }
                  >
                    Waffo Pancake
                  </Button>
                )}
              </div>
            )}
            {hasWaffoPancake && (
              <div className='max-w-[320px] min-w-0 space-y-1.5'>
                <label
                  htmlFor='subscription-waffo-pancake-checkout-region'
                  className='text-muted-foreground text-xs font-medium tracking-wider uppercase'
                >
                  {t('Waffo Pancake checkout region')}
                </label>
                <p className='text-muted-foreground text-xs leading-5'>
                  {t(
                    'China locks the checkout to the China billing market. Global lets you choose your billing region in checkout.'
                  )}
                </p>
                <Select
                  items={[
                    { value: 'china', label: t('China') },
                    { value: 'global', label: t('Global') },
                  ]}
                  value={waffoPancakeCheckoutRegion}
                  onValueChange={(value) => {
                    if (value === 'china' || value === 'global') {
                      setWaffoPancakeCheckoutRegion(value)
                    }
                  }}
                >
                  <SelectTrigger
                    id='subscription-waffo-pancake-checkout-region'
                    className='w-full max-w-[320px] min-w-0'
                  >
                    <SelectValue>
                      {waffoPancakeCheckoutRegion === 'china'
                        ? t('China')
                        : t('Global')}
                    </SelectValue>
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      <SelectItem value='china'>{t('China')}</SelectItem>
                      <SelectItem value='global'>{t('Global')}</SelectItem>
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </div>
            )}
            {hasEpay && (
              <div className='grid grid-cols-[minmax(0,1fr)_auto] gap-2'>
                <Select
                  items={availableEpayMethods.map((m) => ({
                    value: m.type,
                    label: m.name || m.type,
                  }))}
                  value={selectedEpayMethod}
                  onValueChange={(v) => v !== null && setSelectedEpayMethod(v)}
                  disabled={limitReached}
                >
                  <SelectTrigger className='flex-1'>
                    <SelectValue>{selectedEpayMethodLabel}</SelectValue>
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      {availableEpayMethods.map((m) => (
                        <SelectItem key={m.type} value={m.type}>
                          {m.name || m.type}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
                <Button
                  onClick={handlePayEpay}
                  disabled={paying || !selectedEpayMethod || limitReached}
                >
                  {t('Pay')}
                </Button>
              </div>
            )}
          </div>
        )}
        {!hasAnyPayment && (!allowBalancePay || insufficientBalance) && (
          <Alert variant='destructive'>
            <AlertDescription>
              {t('No payment methods available. Please contact administrator.')}
            </AlertDescription>
          </Alert>
        )}
      </div>
    </Dialog>
  )
}
