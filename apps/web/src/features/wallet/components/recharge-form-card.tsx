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
  ExternalLinkIcon,
  GiftIcon,
  Invoice01Icon,
  Loading03Icon,
  Tick02Icon,
  WalletCardsIcon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { Minus, Plus } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { IconBadge } from '@/components/ui/icon-badge'
import { Input } from '@/components/ui/input'
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
} from '@/components/ui/input-group'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { TitledCard } from '@/components/ui/titled-card'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { WaitCompanion } from '@/components/wait-companion'
import {
  formatFiatCurrencyAmount,
  getCurrencyFormattingLocale,
} from '@/lib/currency'
import { cn } from '@/lib/utils'
import {
  getDefaultWaffoPancakeCheckoutRegion,
  type WaffoPancakeCheckoutRegion,
} from '@/lib/waffo-pancake-checkout'

import { PAYMENT_TYPES } from '../constants'
import { usePaymentCurrency } from '../hooks/use-payment-currency'
import {
  getPaymentIcon,
  getPaymentMaxTopupQuota,
  getPaymentMinTopupQuota,
  getDedicatedPaymentLimits,
  getPaymentTopupRatio,
  getDefaultPaymentType,
  getTopupAvailability,
  getMinTopupAmount,
  formatPaymentAmount,
  formatPaymentSettlementRate,
  formatSettlementAmount,
  getPaymentSettlementUnit,
  isFiatPaymentCurrency,
  isWaffoPancakeCurrencySupported,
  isWaffoPancakePayment,
  isPositivePaymentAmount,
  isSafeHttpCheckoutUrl,
} from '../lib'
import type { TopupAvailability } from '../lib/payment'
import { formatFiatAmountInput } from '../lib/payment-amount-input'
import {
  currentPaymentDiscount,
  formatDiscountPercent,
  type PaymentDiscount,
} from '../lib/payment-discount'
import {
  formatSettlementQuote,
  parseSettlementQuote,
  type SettlementQuote,
} from '../lib/settlement-quote'
import type {
  PaymentMethod,
  PresetAmount,
  TopupInfo,
  CreemProduct,
  WaffoPayMethod,
} from '../types'
import { CreemProductsSection } from './creem-products-section'
import { PlatformCreditHelp } from './platform-credit-help'

interface RechargeFormCardProps {
  topupInfo: TopupInfo | null
  topupAvailability?: TopupAvailability
  presetAmounts: PresetAmount[]
  selectedPreset: number | null
  onSelectPreset: (preset: PresetAmount) => void
  /** Selected raw integer Credit amount, independent of display currency. */
  topupAmount: number
  onTopupAmountChange: (
    amount: number,
    options?: { deferQuote?: boolean }
  ) => void
  paymentCurrency?: string
  paymentAmount: number
  settlementQuote?: SettlementQuote | null
  paymentDiscount?: PaymentDiscount | null
  selectedPaymentMethod?: PaymentMethod
  calculating: boolean
  quoteError?: string | null
  onRetryQuote?: () => void
  onPaymentMethodSelect: (method: PaymentMethod) => void
  paymentLoading: string | null
  redemptionCode: string
  onRedemptionCodeChange: (code: string) => void
  onRedeem: () => void
  redeeming: boolean
  discountCode?: string
  discountApplied?: boolean
  appliedDiscountCode?: string
  discountCodeFromUrl?: boolean
  onDiscountCodeChange?: (code: string) => void
  onApplyDiscount?: () => void
  discountApplying?: boolean
  discountPercent?: number | null
  topupLink?: string
  loading?: boolean
  error?: Error | null
  onRetry?: () => void | Promise<void>
  priceRatio?: number
  onOpenBilling?: () => void
  onCreemProductSelect?: (product: CreemProduct) => void
  onWaffoMethodSelect?: (method: WaffoPayMethod, index: number) => void
  waffoPancakeCheckoutRegion?: WaffoPancakeCheckoutRegion
  onWaffoPancakeCheckoutRegionChange?: (
    region: WaffoPancakeCheckoutRegion
  ) => void
  neutralMode?: boolean
  onProceedToPayment?: () => void
  onRemoveDiscount?: () => void
}

export function RechargeFormCard({
  topupInfo,
  topupAvailability: providedTopupAvailability,
  presetAmounts,
  selectedPreset,
  onSelectPreset,
  topupAmount,
  onTopupAmountChange,
  paymentAmount: legacyPaymentAmount,
  paymentCurrency,
  settlementQuote,
  paymentDiscount,
  selectedPaymentMethod,
  calculating,
  quoteError,
  onRetryQuote,
  onPaymentMethodSelect,
  paymentLoading,
  redemptionCode,
  onRedemptionCodeChange,
  onRedeem,
  redeeming,
  discountCode = '',
  discountApplied = false,
  appliedDiscountCode = '',
  discountCodeFromUrl = false,
  onDiscountCodeChange,
  onApplyDiscount,
  discountApplying = false,
  discountPercent,
  topupLink,
  loading,
  error,
  onRetry,
  onOpenBilling,
  onCreemProductSelect,
  onWaffoMethodSelect,
  waffoPancakeCheckoutRegion,
  onWaffoPancakeCheckoutRegionChange,
  neutralMode = false,
  onProceedToPayment,
  onRemoveDiscount,
}: RechargeFormCardProps) {
  const { t, i18n } = useTranslation()
  const currency = usePaymentCurrency()
  const formatCreditQuota = currency.formatQuota
  const currencyKey = JSON.stringify([currency.currency, currency.config])
  const displayAmount = useCallback(
    (amount: number) => currency.quotaToInput(amount),
    [currency]
  )
  const [amountEditing, setAmountEditing] = useState(false)
  const [amountInput, setAmountInput] = useState(() => ({
    sourceAmount: topupAmount,
    currencyKey,
    value: displayAmount(topupAmount),
  }))
  const localAmount =
    amountInput.sourceAmount === topupAmount &&
    amountInput.currencyKey === currencyKey
      ? amountInput.value
      : displayAmount(topupAmount)
  const [localWaffoPancakeRegionOverride, setLocalWaffoPancakeRegion] =
    useState<WaffoPancakeCheckoutRegion | null>(null)
  const holdRef = useRef<{
    delta: number
    startedAt: number
    timeoutId: number | null
  } | null>(null)

  const handleAmountChange = useCallback(
    (value: string) => {
      setAmountEditing(true)
      const quota = currency.amountToQuota(value)
      const rawQuota = Number.isSafeInteger(quota) && quota >= 0 ? quota : 0
      setAmountInput({ sourceAmount: rawQuota, currencyKey, value })
      onTopupAmountChange(rawQuota)
    },
    [currency, currencyKey, onTopupAmountChange]
  )

  const handlePresetSelect = (preset: PresetAmount) => {
    setAmountEditing(false)
    setAmountInput({
      sourceAmount: preset.value,
      currencyKey,
      value: displayAmount(preset.value),
    })
    onSelectPreset(preset)
  }

  const topupAvailability =
    providedTopupAvailability ?? getTopupAvailability(topupInfo)
  const {
    standardMethods,
    waffoMethods,
    creemProducts,
    defaultQuotedType,
    hasPaymentMethod: hasAnyTopup,
  } = topupAvailability
  const hasConfigurableTopup = defaultQuotedType !== null
  const hasStandardPaymentMethods = standardMethods.length > 0
  const hasWaffoPaymentMethods = waffoMethods.length > 0
  const configuredMinimum = getMinTopupAmount(topupInfo)
  const redemptionEnabled = topupInfo?.enable_redemption !== false
  const waffoLimits = getDedicatedPaymentLimits(topupInfo, PAYMENT_TYPES.WAFFO)
  const effectivePaymentMethod =
    selectedPaymentMethod ??
    standardMethods.find(
      (method) => method.type === getDefaultPaymentType(topupInfo)
    ) ??
    standardMethods[0] ??
    (waffoMethods.length > 0
      ? {
          name: waffoMethods[0].name,
          type: PAYMENT_TYPES.WAFFO,
          min_topup_credit: waffoLimits?.minimum,
          max_topup_credit: waffoLimits?.maximum ?? undefined,
          icon: waffoMethods[0].icon,
          settlement_unit: topupInfo?.waffo_currency || 'USD',
          unit_price: topupInfo?.waffo_unit_price,
        }
      : undefined)
  const minTopup = effectivePaymentMethod
    ? Math.max(1, getPaymentMinTopupQuota(effectivePaymentMethod))
    : configuredMinimum
  const maxTopup = getPaymentMaxTopupQuota(effectivePaymentMethod)
  const changeAmountBy = useCallback(
    (delta: number) => {
      const deltaQuota = currency.amountToQuota(String(delta))
      if (
        !Number.isSafeInteger(deltaQuota) ||
        !Number.isSafeInteger(topupAmount) ||
        !Number.isSafeInteger(minTopup)
      ) {
        return
      }
      const lower = BigInt(minTopup)
      const upper = BigInt(maxTopup ?? Number.MAX_SAFE_INTEGER)
      if (lower > upper) return
      const candidate = BigInt(topupAmount) + BigInt(deltaQuota)
      const next = Number(
        candidate < lower ? lower : candidate > upper ? upper : candidate
      )
      if (next !== topupAmount) {
        const parsedValue = next
        onTopupAmountChange(parsedValue, { deferQuote: true })
        setAmountInput({
          sourceAmount: parsedValue,
          currencyKey,
          value: displayAmount(next),
        })
      }
    },
    [
      minTopup,
      maxTopup,
      currency,
      currencyKey,
      displayAmount,
      onTopupAmountChange,
      topupAmount,
    ]
  )
  const stopAmountHold = useCallback(() => {
    const hold = holdRef.current
    if (!hold) return
    if (hold.timeoutId !== null) window.clearTimeout(hold.timeoutId)
    holdRef.current = null
  }, [])
  const startAmountHold = useCallback(
    (delta: number, event: React.PointerEvent<HTMLButtonElement>) => {
      if (event.pointerType === 'mouse' && event.button !== 0) return
      event.currentTarget.setPointerCapture?.(event.pointerId)
      stopAmountHold()
      changeAmountBy(delta)
      const hold = {
        delta,
        startedAt: Date.now(),
        timeoutId: null as number | null,
      }
      const tick = () => {
        if (holdRef.current !== hold) return
        changeAmountBy(delta)
        const elapsed = Date.now() - hold.startedAt
        hold.timeoutId = window.setTimeout(
          tick,
          Math.max(45, 180 - Math.floor(elapsed / 1000) * 30)
        )
      }
      hold.timeoutId = window.setTimeout(tick, 450)
      holdRef.current = hold
    },
    [changeAmountBy, stopAmountHold]
  )
  useEffect(() => stopAmountHold, [stopAmountHold])
  useEffect(() => {
    window.addEventListener('blur', stopAmountHold)
    return () => window.removeEventListener('blur', stopAmountHold)
  }, [stopAmountHold])
  const usesSettlementQuote = isWaffoPancakePayment(
    effectivePaymentMethod?.type ?? ''
  )
  const quote = parseSettlementQuote(settlementQuote)
  const settlementUnit = usesSettlementQuote
    ? null
    : getPaymentSettlementUnit(effectivePaymentMethod, true)
  const actualPaymentCurrency = usesSettlementQuote
    ? quote?.currency
    : (paymentCurrency ?? settlementUnit?.label ?? 'USD')
  const fiatPayment = isFiatPaymentCurrency(actualPaymentCurrency)
  const paymentAmount = usesSettlementQuote
    ? quote
      ? Number(quote.amount)
      : 0
    : legacyPaymentAmount
  const hasCurrentPaymentAmount =
    fiatPayment && !calculating && isPositivePaymentAmount(paymentAmount)
  const effectivePaymentAmount =
    usesSettlementQuote && quote ? Number(quote.amount) : paymentAmount
  const discount = currentPaymentDiscount(
    paymentDiscount,
    effectivePaymentAmount,
    actualPaymentCurrency,
    calculating || discountApplying
  )
  const couponDiscount =
    discountApplied === true &&
    appliedDiscountCode.trim() !== '' &&
    appliedDiscountCode.trim() === discountCode.trim()
      ? discount
      : null
  const selectedPaymentMethodName =
    neutralMode || !effectivePaymentMethod?.name
      ? t('Payment Method')
      : effectivePaymentMethod.name
  const shouldShowSettlementRule = (paymentMethod: PaymentMethod) =>
    !isWaffoPancakePayment(paymentMethod.type) &&
    isFiatPaymentCurrency(getPaymentSettlementUnit(paymentMethod, true)?.label)
  const getSettlementRule = (paymentMethod: PaymentMethod) =>
    formatPaymentSettlementRate(
      paymentMethod,
      currency.label,
      true,
      currency.formatLegacyAmount
    )
  const formatSelectedPaymentAmount = (amount: number) =>
    !fiatPayment
      ? t('Payment unavailable')
      : usesSettlementQuote
        ? quote
          ? amount === Number(quote.amount)
            ? formatSettlementQuote(quote)
            : formatPaymentAmount(amount, quote.currency)
          : t('Payment unavailable')
        : paymentCurrency
          ? formatPaymentAmount(amount, paymentCurrency)
          : settlementUnit
            ? formatSettlementAmount(amount, settlementUnit.label)
            : formatPaymentAmount(amount, 'USD')
  const isUpdatingQuote = calculating || discountApplying
  const paymentAmountLabel = isUpdatingQuote
    ? discountApplying
      ? t('Validating discount...')
      : t('Calculating...')
    : hasCurrentPaymentAmount
      ? formatSelectedPaymentAmount(paymentAmount)
      : t('Payment unavailable')
  const pancakeCurrencySupported = isWaffoPancakeCurrencySupported()
  const interfaceLanguage = i18n.resolvedLanguage || i18n.language
  const paymentFormattingLocale = getCurrencyFormattingLocale(interfaceLanguage)
  const displayedPresetAmounts = presetAmounts.filter(
    (preset, index, all) =>
      all.findIndex((candidate) => candidate.value === preset.value) === index
  )
  const effectiveWaffoPancakeCheckoutRegion =
    waffoPancakeCheckoutRegion ??
    localWaffoPancakeRegionOverride ??
    getDefaultWaffoPancakeCheckoutRegion(interfaceLanguage)
  const hasConfiguredPancakeMethod = (topupInfo?.pay_methods ?? []).some(
    (method) => isWaffoPancakePayment(method.type)
  )
  const canChooseWaffoPancakeRegion =
    topupInfo?.enable_waffo_pancake_topup === true &&
    hasConfiguredPancakeMethod &&
    pancakeCurrencySupported &&
    !!onWaffoPancakeCheckoutRegionChange

  const handleWaffoPancakeCheckoutRegionChange = (value: string | null) => {
    if (value !== 'china' && value !== 'global') return
    setLocalWaffoPancakeRegion(value)
    onWaffoPancakeCheckoutRegionChange?.(value)
  }
  const activeSelectedPreset =
    selectedPreset !== null && selectedPreset === topupAmount
      ? selectedPreset
      : null
  const selectedPresetDetails =
    activeSelectedPreset === null
      ? null
      : (presetAmounts.find((item) => item.value === activeSelectedPreset) ??
        null)
  const selectedPresetQuoteBreakdown = discount
    ? {
        originalPrice: discount.original,
        savedAmount: discount.savings,
        hasDiscount: true,
      }
    : null

  if (loading) {
    return (
      <Card data-card-hover='false' className='gap-0 overflow-hidden py-0'>
        <CardHeader className='border-b p-3 !pb-3 sm:p-5 sm:!pb-5'>
          <Skeleton className='h-6 w-32' />
          <Skeleton className='mt-2 h-4 w-48' />
        </CardHeader>
        <CardContent className='space-y-4 p-3 sm:space-y-6 sm:p-5'>
          <div className='space-y-4 sm:space-y-6'>
            {/* ASCII Banner Skeleton */}
            <div className='bg-primary/5 rounded-none border p-4 text-center font-mono'>
              <Skeleton className='mx-auto h-16 w-3/4' />
            </div>
            {/* Preset Amounts Skeleton */}
            <div className='space-y-3'>
              <Skeleton className='h-3 w-16' />
              <div className='grid grid-cols-2 gap-2 sm:grid-cols-3'>
                {Array.from({ length: 8 }, (_, index) => `preset-${index}`).map(
                  (key) => (
                    <Skeleton key={key} className='h-14 rounded-lg' />
                  )
                )}
              </div>
            </div>

            {/* Custom Amount Input Skeleton */}
            <div className='space-y-3'>
              <Skeleton className='h-3 w-28' />
              <Skeleton className='h-[42px] w-full' />
            </div>

            {/* Payment Methods Skeleton */}
            <div className='space-y-3'>
              <Skeleton className='h-3 w-32' />
              <div className='flex flex-wrap gap-3'>
                {['primary', 'secondary', 'tertiary'].map((key) => (
                  <Skeleton key={key} className='h-10 w-24 rounded-lg' />
                ))}
              </div>
            </div>
          </div>

          {/* Redemption Code Section Skeleton */}
          {!neutralMode ? (
            <div className='space-y-3 border-t pt-8'>
              <Skeleton className='h-3 w-24' />
              <div className='flex gap-2'>
                <Skeleton className='h-10 flex-1' />
                <Skeleton className='h-10 w-20' />
              </div>
            </div>
          ) : null}
        </CardContent>
      </Card>
    )
  }

  if (error) {
    return (
      <TitledCard
        title={t('Add Funds')}
        description={t('Choose an amount and payment method')}
        icon={<HugeiconsIcon icon={WalletCardsIcon} strokeWidth={2} />}
        iconTone='success'
        disableHoverEffect
      >
        <Alert>
          <AlertDescription>
            {t(
              'Payment availability could not be verified. Contact support before attempting to add funds.'
            )}
          </AlertDescription>
          {onRetry ? (
            <Button
              type='button'
              variant='outline'
              size='sm'
              className='mt-3'
              onClick={() => void onRetry()}
              disabled={loading}
            >
              {t('Retry')}
            </Button>
          ) : null}
        </Alert>
      </TitledCard>
    )
  }

  return (
    <TitledCard
      title={t('Add Funds')}
      description={t('Choose an amount and payment method')}
      icon={<HugeiconsIcon icon={WalletCardsIcon} strokeWidth={2} />}
      iconTone='success'
      disableHoverEffect
      action={
        onOpenBilling && !neutralMode ? (
          <Button
            variant='outline'
            size='sm'
            onClick={onOpenBilling}
            className='w-full gap-2 sm:w-auto'
          >
            <HugeiconsIcon icon={Invoice01Icon} data-icon='inline-start' />
            {t('Order History')}
          </Button>
        ) : null
      }
      appearance='outlined'
      className='bg-card'
      headerClassName='bg-muted/20'
      contentClassName='space-y-5 sm:space-y-6'
    >
      {/* Online Topup Section */}
      {hasAnyTopup ? (
        <div className='space-y-4 sm:space-y-6'>
          {hasConfigurableTopup && (
            <div className='grid gap-7 lg:grid-cols-[minmax(0,1.18fr)_minmax(0,1fr)] lg:gap-0'>
              <div className='min-w-0 space-y-6 lg:pr-7'>
                {displayedPresetAmounts.length > 0 && (
                  <FieldGroup>
                    <Field>
                      <div className='flex items-center gap-1'>
                        <FieldLabel>{t('Credited balance')}</FieldLabel>
                        <PlatformCreditHelp />
                      </div>
                      <div className='grid grid-cols-2 gap-2 sm:grid-cols-3'>
                        {displayedPresetAmounts.map((preset) => {
                          const credits = formatCreditQuota(preset.value)
                          const isSelected =
                            activeSelectedPreset === preset.value
                          const selectedQuote =
                            isSelected &&
                            hasCurrentPaymentAmount &&
                            !isUpdatingQuote
                          const showSelectedPayment =
                            selectedQuote &&
                            (Boolean(discount) ||
                              actualPaymentCurrency !== currency.currency ||
                              formatFiatCurrencyAmount(
                                paymentAmount,
                                actualPaymentCurrency,
                                { locale: paymentFormattingLocale }
                              ) !== credits)
                          const presetDiscountPercent =
                            typeof preset.discount === 'number' &&
                            Number.isFinite(preset.discount) &&
                            preset.discount > 0 &&
                            preset.discount < 1
                              ? (1 - preset.discount) * 100
                              : null
                          const visibleDiscountPercent =
                            selectedQuote && discount
                              ? discount.percent
                              : presetDiscountPercent
                          const discountLabel =
                            visibleDiscountPercent !== null
                              ? t('{{percent}}% off', {
                                  percent: formatDiscountPercent(
                                    visibleDiscountPercent
                                  ),
                                })
                              : null
                          return (
                            <Button
                              key={preset.value}
                              variant='outline'
                              className={cn(
                                'relative isolate flex h-auto min-h-14 min-w-0 flex-col items-start justify-center gap-1 overflow-hidden rounded-lg px-3 py-2.5 text-left whitespace-normal transition-colors',
                                isSelected
                                  ? 'border-primary bg-primary/5 text-foreground hover:bg-primary/10 dark:border-primary dark:bg-primary/5 dark:hover:bg-primary/10'
                                  : 'border-border/70 bg-background hover:border-primary/40 hover:bg-muted/40 dark:hover:bg-muted/30'
                              )}
                              onClick={() => handlePresetSelect(preset)}
                              aria-pressed={isSelected}
                              aria-label={[
                                selectedQuote
                                  ? t(
                                      'Preset amount: {{credit}}. Actual payment: {{payment}}.',
                                      {
                                        credit: credits,
                                        payment:
                                          formatSelectedPaymentAmount(
                                            paymentAmount
                                          ),
                                      }
                                    )
                                  : t(
                                      'Preset amount: {{credit}}. Select to get the current payment quote.',
                                      { credit: credits }
                                    ),
                                discountLabel,
                              ]
                                .filter(Boolean)
                                .join(' · ')}
                            >
                              <div className='pointer-events-none relative z-10 flex w-full min-w-0 flex-col items-start gap-1'>
                                <div className='flex w-full min-w-0 items-center gap-2'>
                                  <span
                                    data-slot='wallet-credit-value'
                                    className='min-w-0 flex-1 text-sm leading-5 font-semibold wrap-anywhere tabular-nums'
                                  >
                                    {credits}
                                  </span>
                                  {isSelected && (
                                    <span className='bg-primary text-primary-foreground flex size-4 shrink-0 items-center justify-center rounded-full'>
                                      <HugeiconsIcon
                                        icon={Tick02Icon}
                                        className='size-3'
                                        strokeWidth={2.5}
                                        aria-hidden='true'
                                      />
                                    </span>
                                  )}
                                </div>
                                {visibleDiscountPercent !== null && (
                                  <Badge
                                    variant='outline'
                                    className='border-success/25 bg-success/10 dark:text-success h-auto max-w-full justify-start px-1.5 py-0.5 text-left leading-4 whitespace-normal text-[color-mix(in_oklch,var(--success),var(--foreground)_25%)]'
                                  >
                                    {discountLabel}
                                  </Badge>
                                )}
                                {showSelectedPayment && (
                                  <div className='w-full min-w-0 text-xs leading-4 wrap-anywhere tabular-nums'>
                                    <span
                                      className={cn(
                                        discount
                                          ? 'font-medium text-[color-mix(in_oklch,var(--success),var(--foreground)_25%)] dark:text-success'
                                          : 'text-muted-foreground'
                                      )}
                                    >
                                      {t('Pay {{amount}}', {
                                        amount:
                                          formatSelectedPaymentAmount(
                                            paymentAmount
                                          ),
                                      })}
                                    </span>
                                  </div>
                                )}
                              </div>
                            </Button>
                          )
                        })}
                      </div>
                      {!neutralMode && selectedPresetDetails ? (
                        <div className='space-y-1.5 border-t pt-3 text-xs leading-5'>
                          <>
                            <p className='text-muted-foreground'>
                              {t(
                                'Selected method: {{method}} · Amount due: {{amount}} (actual payment)',
                                {
                                  method: selectedPaymentMethodName,
                                  amount: paymentAmountLabel,
                                }
                              )}
                            </p>
                            {selectedPresetQuoteBreakdown?.hasDiscount && (
                              <p className='dark:text-success text-[color-mix(in_oklch,var(--success),var(--foreground)_25%)]'>
                                {t('Discount applied {{amount}}', {
                                  amount: formatSelectedPaymentAmount(
                                    selectedPresetQuoteBreakdown.savedAmount
                                  ),
                                })}
                              </p>
                            )}
                          </>
                        </div>
                      ) : null}
                    </Field>
                  </FieldGroup>
                )}

                <FieldGroup>
                  <Field>
                    <div className='flex min-w-0 items-center justify-between gap-2'>
                      <FieldLabel htmlFor='topup-amount'>
                        {t('Top-up amount')} ({currency.label})
                      </FieldLabel>
                      <Select
                        items={[
                          { value: 'CNY', label: 'CNY' },
                          { value: 'USD', label: 'USD' },
                        ]}
                        value={currency.currency}
                        onValueChange={(value) => {
                          if (value === 'CNY' || value === 'USD') {
                            currency.setPreference(value)
                          }
                        }}
                      >
                        <SelectTrigger
                          size='sm'
                          aria-label={t('Recharge display currency')}
                          className='min-w-22'
                        >
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent alignItemWithTrigger={false}>
                          <SelectGroup>
                            <SelectItem value='CNY'>CNY</SelectItem>
                            <SelectItem value='USD'>USD</SelectItem>
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                    </div>
                    <FieldDescription id='topup-amount-description'>
                      {neutralMode
                        ? t('Payment adds credit to this account.')
                        : t(
                            'Destination: current signed-in account · API usage balance'
                          )}
                    </FieldDescription>
                    <div className='space-y-2'>
                      <div className='flex min-w-0 items-center gap-2'>
                        <InputGroup className='h-11 min-w-0 flex-1'>
                          <InputGroupInput
                            id='topup-amount'
                            type='text'
                            inputMode='decimal'
                            value={
                              amountEditing
                                ? localAmount
                                : formatFiatAmountInput(
                                    localAmount,
                                    i18n.language
                                  )
                            }
                            onFocus={() => setAmountEditing(true)}
                            onBlur={() => setAmountEditing(false)}
                            onChange={(e) => handleAmountChange(e.target.value)}
                            min={displayAmount(minTopup) || undefined}
                            disabled={displayAmount(minTopup) === ''}
                            placeholder={t('Minimum {{amount}}', {
                              amount: formatCreditQuota(minTopup),
                            })}
                            aria-describedby='topup-amount-description'
                            aria-label={`${t('Top-up amount')} (${currency.label})`}
                            className='text-base sm:text-lg'
                          />
                          <InputGroupAddon align='inline-end'>
                            <span>{currency.label}</span>
                            <PlatformCreditHelp />
                          </InputGroupAddon>
                        </InputGroup>
                        <div className='flex shrink-0 gap-1'>
                          <Button
                            type='button'
                            variant='outline'
                            size='icon'
                            className='size-11 touch-manipulation'
                            aria-label={t('Increase amount')}
                            disabled={
                              maxTopup !== null && topupAmount >= maxTopup
                            }
                            onPointerDown={(event) => startAmountHold(1, event)}
                            onPointerUp={stopAmountHold}
                            onPointerCancel={stopAmountHold}
                            onLostPointerCapture={stopAmountHold}
                            onKeyDown={(event) => {
                              if (event.key === 'Enter' || event.key === ' ') {
                                event.preventDefault()
                                changeAmountBy(1)
                              }
                            }}
                          >
                            <Plus className='size-4' aria-hidden='true' />
                          </Button>
                          <Button
                            type='button'
                            variant='outline'
                            size='icon'
                            className='size-11 touch-manipulation'
                            aria-label={t('Decrease amount')}
                            disabled={topupAmount <= minTopup}
                            onPointerDown={(event) =>
                              startAmountHold(-1, event)
                            }
                            onPointerUp={stopAmountHold}
                            onPointerCancel={stopAmountHold}
                            onLostPointerCapture={stopAmountHold}
                            onKeyDown={(event) => {
                              if (event.key === 'Enter' || event.key === ' ') {
                                event.preventDefault()
                                changeAmountBy(-1)
                              }
                            }}
                          >
                            <Minus className='size-4' aria-hidden='true' />
                          </Button>
                        </div>
                      </div>
                      <div className='text-muted-foreground flex min-w-0 items-start gap-2 text-xs leading-5'>
                        <div className='flex min-w-0 flex-col gap-1 py-1'>
                          <span>
                            {t(
                              'Selected method: {{method}} · Amount due: {{amount}} (actual payment)',
                              {
                                method: selectedPaymentMethodName,
                                amount: paymentAmountLabel,
                              }
                            )}
                          </span>
                        </div>
                      </div>
                    </div>
                  </Field>
                </FieldGroup>
              </div>
              <div className='min-w-0 space-y-6 lg:border-l lg:pl-7'>
                {hasConfigurableTopup ? (
                  <FieldGroup>
                    <Field>
                      <div className='flex items-center justify-between gap-2'>
                        <div className='flex items-center gap-2'>
                          <IconBadge tone='success' size='xs'>
                            <HugeiconsIcon icon={GiftIcon} strokeWidth={2} />
                          </IconBadge>
                          <Label
                            htmlFor='discount-code'
                            className='text-foreground text-sm font-semibold'
                          >
                            {discountCodeFromUrl
                              ? t('Discount code from URL')
                              : t('Discount code')}
                          </Label>
                        </div>
                        {couponDiscount && (
                          <Badge
                            variant='outline'
                            className='border-success/25 bg-success/10 dark:text-success text-xs font-medium text-[color-mix(in_oklch,var(--success),var(--foreground)_25%)]'
                          >
                            {t('Discount applied: {{percent}}% off', {
                              percent: formatDiscountPercent(
                                couponDiscount.percent
                              ),
                            })}
                          </Badge>
                        )}
                      </div>
                      <div className='grid min-w-0 grid-cols-1 gap-2 sm:grid-cols-[minmax(0,1fr)_auto]'>
                        <Input
                          id='discount-code'
                          value={discountCode}
                          onChange={(e) =>
                            onDiscountCodeChange?.(e.target.value)
                          }
                          placeholder={t('Enter your discount code')}
                          readOnly={discountCodeFromUrl}
                          aria-readonly={discountCodeFromUrl}
                          className={cn(
                            'h-11 min-w-0 text-base uppercase sm:h-9 sm:text-sm',
                            discountCodeFromUrl && 'bg-muted font-mono'
                          )}
                          autoComplete='off'
                          maxLength={64}
                        />
                        <div className='flex flex-wrap items-center justify-end gap-1.5'>
                          {onRemoveDiscount &&
                            (discountPercent !== null ||
                              (!discountCodeFromUrl &&
                                discountCode.trim())) && (
                              <Button
                                type='button'
                                onClick={onRemoveDiscount}
                                disabled={discountApplying}
                                variant='ghost'
                                className='text-muted-foreground hover:text-foreground min-h-11 px-2.5 sm:min-h-9'
                              >
                                {t('Remove')}
                              </Button>
                            )}
                          <Button
                            onClick={onApplyDiscount}
                            disabled={
                              discountCodeFromUrl ||
                              discountApplying ||
                              !discountCode.trim()
                            }
                            variant='outline'
                            className='min-h-11 px-4 sm:min-h-9'
                          >
                            {discountApplying && (
                              <HugeiconsIcon
                                icon={Loading03Icon}
                                className='animate-spin'
                                data-icon='inline-start'
                              />
                            )}
                            {discountCodeFromUrl && discountPercent !== null
                              ? t('Applied')
                              : t('Apply')}
                          </Button>
                        </div>
                      </div>
                      {discountCodeFromUrl ? (
                        <p className='text-muted-foreground text-xs'>
                          {t(
                            'This code came from the checkout link and cannot be edited.'
                          )}
                        </p>
                      ) : null}
                      {couponDiscount ? (
                        <div className='dark:text-success flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-[color-mix(in_oklch,var(--success),var(--foreground)_25%)]'>
                          <span>
                            {t('Discount applied: {{percent}}% off', {
                              percent: formatDiscountPercent(
                                couponDiscount.percent
                              ),
                            })}
                          </span>
                          <span className='font-medium'>
                            {t('You save')}:{' '}
                            {formatSelectedPaymentAmount(
                              couponDiscount.savings
                            )}
                          </span>
                        </div>
                      ) : (
                        <p className='text-muted-foreground text-xs'>
                          {t(
                            'A valid discount code is applied at checkout and cannot be combined with another code.'
                          )}
                        </p>
                      )}
                    </Field>
                  </FieldGroup>
                ) : null}

                <FieldGroup>
                  <Field>
                    <Label className='text-foreground text-sm font-semibold'>
                      {t('Payment Method')}
                    </Label>
                    {hasStandardPaymentMethods ? (
                      <div className='grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-1 xl:grid-cols-2'>
                        {standardMethods.map((method, index) => {
                          const minTopup = Math.max(
                            1,
                            getPaymentMinTopupQuota(method)
                          )
                          const maxTopup = getPaymentMaxTopupQuota(method)
                          const belowMinimum = minTopup > topupAmount
                          const aboveMaximum =
                            maxTopup !== null && topupAmount > maxTopup
                          const fiatMethod = isFiatPaymentCurrency(
                            getPaymentSettlementUnit(method, true)?.label ??
                              'USD'
                          )
                          const disabled =
                            !fiatMethod || belowMinimum || aboveMaximum
                          let disabledReason: string | undefined
                          let disabledLabel: string | undefined
                          if (!fiatMethod) {
                            disabledReason = t('Payment unavailable')
                            disabledLabel = disabledReason
                          } else if (belowMinimum) {
                            disabledReason = t(
                              'Minimum topup amount: {{amount}}',
                              {
                                amount: formatCreditQuota(minTopup),
                              }
                            )
                            disabledLabel = `${t('Minimum:')} ${formatCreditQuota(minTopup)}`
                          } else if (aboveMaximum) {
                            disabledReason = t(
                              'Maximum credited balance per payment: {{amount}}',
                              {
                                amount: formatCreditQuota(maxTopup),
                              }
                            )
                            disabledLabel = t('Maximum: {{amount}}', {
                              amount: formatCreditQuota(maxTopup),
                            })
                          }
                          const settlementRule = shouldShowSettlementRule(
                            method
                          )
                            ? getSettlementRule(method)
                            : null
                          const methodTopupRatio = getPaymentTopupRatio(method)
                          const paymentMethodLabel = neutralMode
                            ? t('Payment option {{number}}', {
                                number: index + 1,
                              })
                            : method.name
                          const isSelected =
                            effectivePaymentMethod?.type === method.type

                          const button = (
                            <Button
                              key={method.type}
                              variant='outline'
                              onClick={() => onPaymentMethodSelect(method)}
                              disabled={disabled || !!paymentLoading}
                              title={disabledReason}
                              aria-label={
                                disabledReason
                                  ? `${paymentMethodLabel}. ${disabledReason}`
                                  : paymentMethodLabel
                              }
                              className={cn(
                                'min-h-14 min-w-0 justify-start gap-2 rounded-lg px-3 py-2 text-left transition-colors',
                                isSelected &&
                                  'border-primary bg-primary/10 ring-1 ring-primary/30'
                              )}
                            >
                              {paymentLoading === method.type ? (
                                <HugeiconsIcon
                                  icon={Loading03Icon}
                                  className='animate-spin'
                                  data-icon='inline-start'
                                />
                              ) : (
                                getPaymentIcon(
                                  method.type,
                                  'h-4 w-4',
                                  method.icon,
                                  paymentMethodLabel,
                                  method.color
                                )
                              )}
                              <span className='flex min-w-0 flex-col items-start gap-0.5'>
                                <span className='max-w-full truncate'>
                                  {paymentMethodLabel}
                                </span>
                                {disabledLabel && (
                                  <span className='text-muted-foreground max-w-full truncate text-[11px] leading-4 font-normal'>
                                    {disabledLabel}
                                  </span>
                                )}
                                {settlementRule && (
                                  <span className='text-muted-foreground max-w-full truncate text-[11px] leading-4 font-normal'>
                                    {settlementRule}
                                  </span>
                                )}
                                {method.description && (
                                  <span className='text-muted-foreground line-clamp-2 max-w-full text-[11px] leading-4 font-normal'>
                                    {method.description}
                                  </span>
                                )}
                                {!neutralMode && methodTopupRatio !== 1 && (
                                  <span className='text-muted-foreground max-w-full truncate text-[11px] leading-4 font-normal'>
                                    {t('Channel multiplier ×{{ratio}}', {
                                      ratio: method.topup_ratio,
                                    })}
                                  </span>
                                )}
                              </span>
                            </Button>
                          )

                          return disabled ? (
                            <TooltipProvider key={method.type}>
                              <Tooltip>
                                <TooltipTrigger render={button} />
                                <TooltipContent>
                                  {disabledReason}
                                </TooltipContent>
                              </Tooltip>
                            </TooltipProvider>
                          ) : (
                            button
                          )
                        })}
                      </div>
                    ) : null}
                    {!hasStandardPaymentMethods && !hasWaffoPaymentMethods && (
                      <Alert>
                        <AlertDescription>
                          {t(
                            'No payment methods available. Please contact administrator.'
                          )}
                        </AlertDescription>
                      </Alert>
                    )}
                    {canChooseWaffoPancakeRegion && (
                      <div className='mt-4 min-w-0 space-y-1.5 border-t pt-4'>
                        <Label
                          htmlFor='waffo-pancake-checkout-region'
                          className='text-foreground text-sm font-semibold'
                        >
                          {t('Waffo Pancake checkout region')}
                        </Label>
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
                          value={effectiveWaffoPancakeCheckoutRegion}
                          onValueChange={handleWaffoPancakeCheckoutRegionChange}
                        >
                          <SelectTrigger
                            id='waffo-pancake-checkout-region'
                            className='w-full min-w-0'
                          >
                            <SelectValue>
                              {effectiveWaffoPancakeCheckoutRegion === 'china'
                                ? t('China')
                                : t('Global')}
                            </SelectValue>
                          </SelectTrigger>
                          <SelectContent alignItemWithTrigger={false}>
                            <SelectGroup>
                              <SelectItem value='china'>
                                {t('China')}
                              </SelectItem>
                              <SelectItem value='global'>
                                {t('Global')}
                              </SelectItem>
                            </SelectGroup>
                          </SelectContent>
                        </Select>
                      </div>
                    )}
                    {topupInfo?.enable_waffo_pancake_topup &&
                      hasConfiguredPancakeMethod &&
                      !pancakeCurrencySupported && (
                        <Alert>
                          <AlertDescription>
                            {t(
                              'Waffo Pancake currently supports USD only. Please set this gateway currency to USD.'
                            )}
                          </AlertDescription>
                        </Alert>
                      )}
                  </Field>
                </FieldGroup>

                {hasWaffoPaymentMethods && onWaffoMethodSelect && (
                  <div className='space-y-2.5 sm:space-y-3'>
                    <Label className='text-foreground text-sm font-semibold'>
                      {neutralMode ? t('Payment Method') : t('Waffo Payment')}
                    </Label>
                    <div className='grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-1 xl:grid-cols-2'>
                      {waffoMethods.map((method, index) => {
                        const loadingKey = `waffo-${index}`
                        const methodKey = `${method.payMethodType ?? 'unknown'}-${method.payMethodName ?? method.name}`
                        const waffoMin =
                          waffoLimits?.minimum ?? Number.POSITIVE_INFINITY
                        const waffoMax = waffoLimits?.maximum ?? null
                        const belowMin = waffoMin > topupAmount
                        const aboveMax =
                          waffoMax !== null && topupAmount > waffoMax
                        const disabledReason = belowMin
                          ? t('Minimum topup amount: {{amount}}', {
                              amount: formatCreditQuota(waffoMin),
                            })
                          : aboveMax && waffoMax !== null
                            ? t(
                                'Maximum credited balance per payment: {{amount}}',
                                { amount: formatCreditQuota(waffoMax) }
                              )
                            : undefined
                        const disabledLabel = belowMin
                          ? `${t('Minimum:')} ${formatCreditQuota(waffoMin)}`
                          : aboveMax && waffoMax !== null
                            ? t(
                                'Maximum credited balance per payment: {{amount}}',
                                { amount: formatCreditQuota(waffoMax) }
                              )
                            : undefined
                        const paymentMethodLabel = neutralMode
                          ? t('Payment option {{number}}', {
                              number: index + 1,
                            })
                          : method.name

                        let methodIcon = getPaymentIcon('waffo')
                        if (paymentLoading === loadingKey) {
                          methodIcon = (
                            <HugeiconsIcon
                              icon={Loading03Icon}
                              className='animate-spin'
                              data-icon='inline-start'
                            />
                          )
                        } else if (method.icon) {
                          methodIcon = (
                            <img
                              src={method.icon}
                              alt={paymentMethodLabel}
                              className='h-4 w-4 object-contain'
                            />
                          )
                        }

                        const button = (
                          <Button
                            key={methodKey}
                            variant='outline'
                            onClick={() => onWaffoMethodSelect(method, index)}
                            disabled={belowMin || aboveMax || !!paymentLoading}
                            title={disabledReason}
                            aria-label={
                              disabledReason
                                ? `${paymentMethodLabel}. ${disabledReason}`
                                : paymentMethodLabel
                            }
                            className='min-h-14 min-w-0 justify-start gap-2 rounded-lg px-3 py-2 text-left'
                          >
                            {methodIcon}
                            <span className='flex min-w-0 flex-col items-start gap-0.5'>
                              <span className='max-w-full truncate'>
                                {paymentMethodLabel}
                              </span>
                              {disabledLabel && (
                                <span className='text-muted-foreground max-w-full truncate text-[11px] leading-4 font-normal'>
                                  {disabledLabel}
                                </span>
                              )}
                            </span>
                          </Button>
                        )

                        return belowMin || aboveMax ? (
                          <TooltipProvider key={methodKey}>
                            <Tooltip>
                              <TooltipTrigger render={button} />
                              <TooltipContent>{disabledReason}</TooltipContent>
                            </Tooltip>
                          </TooltipProvider>
                        ) : (
                          button
                        )
                      })}
                    </div>
                  </div>
                )}
                <WaitCompanion pending={calculating} />
                {quoteError && !calculating && (
                  <Alert variant='destructive'>
                    <AlertDescription>
                      <p>{quoteError}</p>
                      {onRetryQuote && (
                        <Button
                          type='button'
                          variant='outline'
                          onClick={onRetryQuote}
                          disabled={!!paymentLoading}
                        >
                          {t('Retry')}
                        </Button>
                      )}
                    </AlertDescription>
                  </Alert>
                )}
                {(hasStandardPaymentMethods || hasWaffoPaymentMethods) &&
                  effectivePaymentMethod && (
                    <div className='bg-primary/5 border-primary/20 mt-2 rounded-xl border p-4'>
                      <div className='flex flex-col gap-4'>
                        <div className='flex min-w-0 flex-col'>
                          <span className='text-muted-foreground text-xs font-medium'>
                            {t('Total')}
                          </span>
                          <div className='flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1'>
                            <span className='text-primary text-2xl font-semibold tracking-tight break-words tabular-nums'>
                              {paymentAmountLabel}
                            </span>
                            {discount &&
                              hasCurrentPaymentAmount &&
                              !isUpdatingQuote && (
                                <span className='text-muted-foreground text-xs line-through'>
                                  {formatSelectedPaymentAmount(
                                    discount.original
                                  )}
                                </span>
                              )}
                          </div>
                        </div>
                        {discount && (
                          <div className='flex flex-wrap items-center gap-2 text-sm'>
                            <Badge
                              variant='outline'
                              className='border-success/25 bg-success/10 dark:text-success h-auto max-w-full py-1 leading-4 whitespace-normal text-[color-mix(in_oklch,var(--success),var(--foreground)_25%)]'
                            >
                              {t('Discount applied: {{percent}}% off', {
                                percent: formatDiscountPercent(
                                  discount.percent
                                ),
                              })}
                            </Badge>
                            <span className='dark:text-success font-medium text-[color-mix(in_oklch,var(--success),var(--foreground)_25%)]'>
                              {t('You save')}:{' '}
                              {formatSelectedPaymentAmount(discount.savings)}
                            </span>
                          </div>
                        )}
                        <Button
                          type='button'
                          size='default'
                          className='h-11 w-full font-semibold'
                          disabled={
                            isUpdatingQuote ||
                            !hasCurrentPaymentAmount ||
                            Boolean(paymentLoading)
                          }
                          onClick={() =>
                            onProceedToPayment
                              ? onProceedToPayment()
                              : onPaymentMethodSelect(effectivePaymentMethod)
                          }
                        >
                          {isUpdatingQuote ? (
                            <>
                              <HugeiconsIcon
                                icon={Loading03Icon}
                                className='animate-spin'
                                data-icon='inline-start'
                              />
                              {discountApplying
                                ? t('Validating discount...')
                                : t('Calculating...')}
                            </>
                          ) : (
                            t('Pay {{amount}}', { amount: paymentAmountLabel })
                          )}
                        </Button>
                      </div>
                    </div>
                  )}
              </div>
            </div>
          )}
        </div>
      ) : (
        <Alert>
          <AlertDescription>
            {neutralMode
              ? t('No payment methods available. Please contact administrator.')
              : t(
                  'Online topup is not enabled. Please use redemption code or contact administrator.'
                )}
          </AlertDescription>
        </Alert>
      )}

      {/* Creem Products Section */}
      {creemProducts.length > 0 && onCreemProductSelect && (
        <div className='flex flex-col gap-3 pt-4 sm:pt-6'>
          <Separator />
          <Label className='text-muted-foreground text-xs font-medium tracking-wider uppercase'>
            {neutralMode ? t('Payment Method') : t('Creem Payment')}
          </Label>
          <CreemProductsSection
            products={creemProducts}
            onProductSelect={onCreemProductSelect}
            neutralMode={neutralMode}
          />
        </div>
      )}

      {/* Redemption Code Section */}
      {!neutralMode && redemptionEnabled ? (
        <div className='flex flex-col gap-3 pt-4 sm:pt-6'>
          <Separator />
          <div className='flex items-center gap-2'>
            <IconBadge tone='warning' size='xs'>
              <HugeiconsIcon icon={GiftIcon} strokeWidth={2} />
            </IconBadge>
            <Label
              htmlFor='redemption-code'
              className='text-muted-foreground text-xs font-medium tracking-wider uppercase'
            >
              {t('Have a Code?')}
            </Label>
          </div>
          <div className='grid grid-cols-[minmax(0,1fr)_auto] gap-2'>
            <Input
              id='redemption-code'
              value={redemptionCode}
              onChange={(e) => onRedemptionCodeChange(e.target.value)}
              placeholder={t('Enter your redemption code')}
              className='h-9 min-w-0'
            />
            <Button
              onClick={onRedeem}
              disabled={redeeming}
              variant='outline'
              className='h-9 px-4'
            >
              {redeeming && (
                <HugeiconsIcon
                  icon={Loading03Icon}
                  className='animate-spin'
                  data-icon='inline-start'
                />
              )}
              {t('Redeem')}
            </Button>
          </div>
          {isSafeHttpCheckoutUrl(topupLink) && (
            <p className='text-muted-foreground text-xs'>
              {t('Need a redemption code?')}{' '}
              <a
                href={topupLink}
                target='_blank'
                rel='noopener noreferrer'
                className='inline-flex items-center gap-1 underline-offset-4 hover:underline'
              >
                {t('Get one here')}
                <HugeiconsIcon icon={ExternalLinkIcon} data-icon='inline-end' />
              </a>
            </p>
          )}
        </div>
      ) : !neutralMode ? (
        <Alert className='border-t'>
          <AlertDescription>
            {t(
              'Redemption codes are disabled until the administrator confirms compliance terms.'
            )}
          </AlertDescription>
        </Alert>
      ) : null}
    </TitledCard>
  )
}
