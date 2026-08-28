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
  WalletCardsIcon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useState, useEffect } from 'react'
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
import { getPlatformCurrencyLabel } from '@/lib/currency'
import { cn } from '@/lib/utils'
import {
  getDefaultWaffoPancakeCheckoutRegion,
  type WaffoPancakeCheckoutRegion,
} from '@/lib/waffo-pancake-checkout'

import {
  getPaymentIcon,
  getPaymentMaxTopup,
  getPaymentTopupRatio,
  getDefaultPaymentType,
  getTopupAvailability,
  getMinTopupAmount,
  calculatePresetPricing,
  formatPlatformCreditBalance as formatPlatformCreditBalanceBase,
  formatPaymentAmount,
  formatPaymentSettlementRate,
  formatSettlementAmount,
  getPaymentSettlementUnit,
  isWaffoPancakeCurrencySupported,
  isWaffoPancakePayment,
  isSafeHttpCheckoutUrl,
} from '../lib'
import { discountCodeSavings } from '../lib/discount-state'
import type { TopupAvailability } from '../lib/payment'
import type {
  PaymentMethod,
  PresetAmount,
  TopupInfo,
  CreemProduct,
  WaffoPayMethod,
} from '../types'
import { CreemProductsSection } from './creem-products-section'

interface RechargeFormCardProps {
  topupInfo: TopupInfo | null
  topupAvailability?: TopupAvailability
  presetAmounts: PresetAmount[]
  selectedPreset: number | null
  onSelectPreset: (preset: PresetAmount) => void
  topupAmount: number
  onTopupAmountChange: (amount: number) => void
  paymentAmount: number
  selectedPaymentMethod?: PaymentMethod
  calculating: boolean
  onPaymentMethodSelect: (method: PaymentMethod) => void
  paymentLoading: string | null
  redemptionCode: string
  onRedemptionCodeChange: (code: string) => void
  onRedeem: () => void
  redeeming: boolean
  discountCode?: string
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
}

export function RechargeFormCard({
  topupInfo,
  topupAvailability: providedTopupAvailability,
  presetAmounts,
  selectedPreset,
  onSelectPreset,
  topupAmount,
  onTopupAmountChange,
  paymentAmount,
  selectedPaymentMethod,
  calculating,
  onPaymentMethodSelect,
  paymentLoading,
  redemptionCode,
  onRedemptionCodeChange,
  onRedeem,
  redeeming,
  discountCode = '',
  discountCodeFromUrl = false,
  onDiscountCodeChange,
  onApplyDiscount,
  discountApplying = false,
  discountPercent,
  topupLink,
  loading,
  error,
  onRetry,
  priceRatio = 1,
  onOpenBilling,
  onCreemProductSelect,
  onWaffoMethodSelect,
  waffoPancakeCheckoutRegion,
  onWaffoPancakeCheckoutRegionChange,
  neutralMode = false,
}: RechargeFormCardProps) {
  const { t, i18n } = useTranslation()
  const formatPlatformCreditBalance = (amount: number) =>
    formatPlatformCreditBalanceBase(amount, t('Platform'))
  const [localAmount, setLocalAmount] = useState(topupAmount.toString())
  const [localWaffoPancakeRegionOverride, setLocalWaffoPancakeRegion] =
    useState<WaffoPancakeCheckoutRegion | null>(null)

  useEffect(() => {
    // Empty string must survive, otherwise the field can never be cleared
    setLocalAmount((prev) =>
      prev === '' && topupAmount === 0 ? prev : topupAmount.toString()
    )
  }, [topupAmount])

  const handleAmountChange = (value: string) => {
    setLocalAmount(value)
    const parsedValue = Number.parseInt(value, 10)
    if (Number.isFinite(parsedValue) && parsedValue >= 0) {
      onTopupAmountChange(parsedValue)
    } else if (value === '') {
      onTopupAmountChange(0)
    }
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
  const minTopup = getMinTopupAmount(topupInfo)
  const topupGroupRatio = topupInfo?.topup_group_ratio ?? 1
  const redemptionEnabled = topupInfo?.enable_redemption !== false
  const customDiscount = topupInfo?.discount?.[topupAmount] || 1
  const customHasDiscount = customDiscount > 0 && customDiscount < 1
  const customOriginalPayment = customHasDiscount
    ? paymentAmount / customDiscount
    : paymentAmount
  const customDiscountAmount = customOriginalPayment - paymentAmount
  const discountCodeSavingAmount = discountCodeSavings(
    paymentAmount,
    discountPercent
  )
  const defaultPaymentType = getDefaultPaymentType(topupInfo)
  const effectivePaymentMethod =
    selectedPaymentMethod ??
    standardMethods.find((method) => method.type === defaultPaymentType)
  const settlementUnit = getPaymentSettlementUnit(effectivePaymentMethod, true)
  const paymentTopupRatio = getPaymentTopupRatio(effectivePaymentMethod)
  const selectedPaymentMethodName =
    neutralMode || !effectivePaymentMethod?.name
      ? t('Payment Method')
      : effectivePaymentMethod.name
  const shouldShowSettlementRule = (paymentMethod: PaymentMethod) =>
    getPaymentSettlementUnit(paymentMethod, true) !== null
  const getSettlementRule = (paymentMethod: PaymentMethod) =>
    formatPaymentSettlementRate(
      paymentMethod,
      getPlatformCurrencyLabel(t('Platform')),
      true
    )
  const formatSelectedPaymentAmount = (amount: number) =>
    settlementUnit
      ? formatSettlementAmount(amount, settlementUnit.label)
      : formatPaymentAmount(amount, 'USD')
  const formatPresetPaymentAmount = (amount: number) =>
    settlementUnit
      ? formatSettlementAmount(amount, settlementUnit.label)
      : formatPaymentAmount(amount, 'USD')
  const pancakeCurrencySupported = isWaffoPancakeCurrencySupported()
  const interfaceLanguage = i18n.resolvedLanguage || i18n.language
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
  const selectedPresetPricing = (() => {
    if (selectedPreset === null) return null
    const preset = presetAmounts.find((item) => item.value === selectedPreset)
    if (!preset) return null
    const discount =
      preset.discount || topupInfo?.discount?.[preset.value] || 1.0
    return calculatePresetPricing(
      preset.value,
      (settlementUnit?.unitPrice ?? priceRatio) *
        topupGroupRatio *
        paymentTopupRatio,
      discount
    )
  })()

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
              <div className='grid grid-cols-2 gap-3 sm:grid-cols-4'>
                {Array.from({ length: 8 }, (_, index) => `preset-${index}`).map(
                  (key) => (
                    <Skeleton key={key} className='h-[72px] rounded-lg' />
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
      contentClassName='space-y-4 sm:space-y-6'
    >
      {/* Online Topup Section */}
      {hasAnyTopup ? (
        <div className='space-y-4 sm:space-y-6'>
          {hasConfigurableTopup && (
            <>
              {presetAmounts.length > 0 && (
                <FieldGroup>
                  <Field>
                    <FieldLabel>
                      {neutralMode
                        ? t('Current account balance')
                        : t('Platform credit')}
                    </FieldLabel>
                    <FieldDescription>
                      {neutralMode
                        ? t(
                            'Funds are added to your current account after payment.'
                          )
                        : t(
                            'Credits are added to your current signed-in account for API usage.'
                          )}
                    </FieldDescription>
                    <div className='grid grid-cols-2 gap-2'>
                      {presetAmounts.map((preset) => {
                        const discount =
                          preset.discount ||
                          topupInfo?.discount?.[preset.value] ||
                          1.0
                        const defaultPricing = calculatePresetPricing(
                          preset.value,
                          priceRatio * topupGroupRatio * paymentTopupRatio,
                          discount
                        )
                        const configuredSettlementPrice = settlementUnit
                          ? calculatePresetPricing(
                              preset.value,
                              settlementUnit.unitPrice *
                                topupGroupRatio *
                                paymentTopupRatio,
                              discount
                            )
                          : null
                        const {
                          originalPrice,
                          actualPrice,
                          savedAmount,
                          hasDiscount,
                        } = configuredSettlementPrice
                          ? {
                              ...configuredSettlementPrice,
                              hasDiscount: discount < 1,
                            }
                          : defaultPricing
                        const credits = formatPlatformCreditBalance(
                          preset.value
                        )
                        const payment = formatPresetPaymentAmount(actualPrice)
                        const originalPayment =
                          formatPresetPaymentAmount(originalPrice)
                        const discountPercent = Math.round((1 - discount) * 100)
                        const discountSummary = hasDiscount
                          ? `${t('Platform discount {{percent}}%', {
                              percent: discountPercent,
                            })}. ${t('Discount applied {{amount}}', {
                              amount: formatPresetPaymentAmount(savedAmount),
                            })}`
                          : t('Platform discount {{percent}}%', { percent: 0 })
                        return (
                          <Button
                            key={preset.value}
                            variant='outline'
                            className={cn(
                              'flex min-h-32 min-w-0 flex-col items-start rounded-lg px-3 py-2.5 text-left whitespace-normal sm:min-h-24 sm:p-4',
                              selectedPreset === preset.value
                                ? 'border-primary bg-primary/5'
                                : 'border-muted'
                            )}
                            onClick={() => onSelectPreset(preset)}
                            aria-pressed={selectedPreset === preset.value}
                            aria-label={t(
                              'Preset amount: {{credit}}. Actual payment: {{payment}}. Original payment: {{original}}. {{discount}}',
                              {
                                credit: credits,
                                payment,
                                original: originalPayment,
                                discount: discountSummary,
                              }
                            )}
                          >
                            <div className='flex w-full min-w-0 flex-col items-start gap-1 sm:flex-row sm:items-center sm:justify-between'>
                              <div className='min-w-0 text-base font-semibold sm:text-lg'>
                                {credits}
                              </div>
                              {hasDiscount && (
                                <Badge variant='secondary'>
                                  {t('Platform discount {{percent}}%', {
                                    percent: discountPercent,
                                  })}
                                </Badge>
                              )}
                            </div>
                          </Button>
                        )
                      })}
                    </div>
                    {!neutralMode ? (
                      <Card className='bg-muted/30 border-dashed shadow-none'>
                        <CardContent className='space-y-1.5 p-3 text-xs sm:p-4'>
                          <div className='font-medium'>
                            {t('Payment notes')}
                          </div>
                          <p className='text-muted-foreground'>
                            {t(
                              'The amount shown on each card is the platform credit. The actual payment and any discount are calculated for the selected payment method.'
                            )}
                          </p>
                          {selectedPreset !== null && (
                            <>
                              <p className='text-muted-foreground'>
                                {t(
                                  'Selected method: {{method}} · Estimated payment: {{amount}} (original {{original}})',
                                  {
                                    method: selectedPaymentMethodName,
                                    amount: formatPresetPaymentAmount(
                                      selectedPresetPricing?.actualPrice ?? 0
                                    ),
                                    original: formatPresetPaymentAmount(
                                      selectedPresetPricing?.originalPrice ?? 0
                                    ),
                                  }
                                )}
                              </p>
                              {selectedPresetPricing?.hasDiscount && (
                                <p className='text-muted-foreground'>
                                  {t('Discount applied {{amount}}', {
                                    amount: formatPresetPaymentAmount(
                                      selectedPresetPricing.savedAmount
                                    ),
                                  })}
                                </p>
                              )}
                            </>
                          )}
                        </CardContent>
                      </Card>
                    ) : null}
                  </Field>
                </FieldGroup>
              )}

              <FieldGroup>
                <Field>
                  <FieldLabel htmlFor='topup-amount'>
                    {neutralMode
                      ? t('Current account balance')
                      : t('Custom platform credit')}
                  </FieldLabel>
                  <FieldDescription id='topup-amount-description'>
                    {neutralMode
                      ? t(
                          'Funds are added to your current account after payment.'
                        )
                      : t(
                          'Destination: current signed-in account · API usage balance'
                        )}
                  </FieldDescription>
                  <div className='grid min-w-0 gap-2 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-center'>
                    <InputGroup className='h-9 sm:h-10'>
                      <InputGroupAddon aria-hidden='true'>$</InputGroupAddon>
                      <InputGroupInput
                        id='topup-amount'
                        type='number'
                        step='1'
                        value={localAmount}
                        onChange={(e) => handleAmountChange(e.target.value)}
                        min={minTopup}
                        placeholder={t('Minimum {{amount}}', {
                          amount: formatPlatformCreditBalance(minTopup),
                        })}
                        aria-describedby='topup-amount-description'
                        aria-label={t('Custom platform credit')}
                        className='text-base sm:text-lg'
                      />
                      <InputGroupAddon align='inline-end' aria-hidden='true'>
                        ({t('Platform')})
                      </InputGroupAddon>
                    </InputGroup>
                    <div className='bg-muted flex min-h-9 min-w-0 items-center justify-between gap-2 rounded-md border px-3 lg:min-w-52'>
                      <div className='flex min-w-0 flex-col gap-1 py-1'>
                        <span className='text-muted-foreground text-xs'>
                          {t(
                            'Selected method: {{method}} · Amount due: {{amount}} (actual payment)',
                            {
                              method: selectedPaymentMethodName,
                              amount:
                                formatSelectedPaymentAmount(paymentAmount),
                            }
                          )}
                        </span>
                        <div className='flex flex-wrap gap-1'>
                          <Badge variant='secondary'>
                            {t('Platform discount {{percent}}%', {
                              percent: Math.round((1 - customDiscount) * 100),
                            })}
                          </Badge>
                          {customHasDiscount && customDiscountAmount > 0 && (
                            <Badge variant='outline'>
                              {t('Discount applied {{amount}}', {
                                amount:
                                  formatSelectedPaymentAmount(
                                    customDiscountAmount
                                  ),
                              })}
                            </Badge>
                          )}
                        </div>
                      </div>
                      {calculating ? <Skeleton className='h-5 w-16' /> : null}
                    </div>
                  </div>
                </Field>
              </FieldGroup>

              <FieldGroup>
                <Field>
                  <Label className='text-muted-foreground text-xs font-medium tracking-wider uppercase'>
                    {t('Payment Method')}
                  </Label>
                  {hasStandardPaymentMethods ? (
                    <div className='grid grid-cols-2 gap-1.5 sm:gap-3 lg:grid-cols-3'>
                      {standardMethods.map((method, index) => {
                        const minTopup = Math.max(
                          method.min_topup || 0,
                          getMinTopupAmount(topupInfo)
                        )
                        const maxTopup = getPaymentMaxTopup(method)
                        const belowMinimum = minTopup > topupAmount
                        const aboveMaximum =
                          maxTopup !== null && topupAmount > maxTopup
                        const disabled = belowMinimum || aboveMaximum
                        let disabledReason: string | undefined
                        let disabledLabel: string | undefined
                        if (belowMinimum) {
                          disabledReason = t(
                            'Minimum topup amount: {{amount}}',
                            {
                              amount: formatPlatformCreditBalance(minTopup),
                            }
                          )
                          disabledLabel = `${t('Minimum:')} ${formatPlatformCreditBalance(minTopup)}`
                        } else if (aboveMaximum) {
                          disabledReason = t(
                            'Maximum platform credit per payment: {{amount}}',
                            { amount: formatPlatformCreditBalance(maxTopup) }
                          )
                          disabledLabel = t('Maximum: {{amount}}', {
                            amount: formatPlatformCreditBalance(maxTopup),
                          })
                        }
                        const settlementRule = shouldShowSettlementRule(method)
                          ? getSettlementRule(method)
                          : null
                        const methodTopupRatio = getPaymentTopupRatio(method)
                        const paymentMethodLabel = neutralMode
                          ? t('Payment option {{number}}', {
                              number: index + 1,
                            })
                          : method.name

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
                            className='min-h-14 min-w-0 justify-start gap-2 rounded-lg px-3 py-2 text-left'
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
                              <TooltipContent>{disabledReason}</TooltipContent>
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
                    <div className='mt-3 max-w-[320px] min-w-0 space-y-1.5 sm:mt-4'>
                      <Label
                        htmlFor='waffo-pancake-checkout-region'
                        className='text-muted-foreground text-xs font-medium tracking-wider uppercase'
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
                          className='w-full max-w-[320px] min-w-0'
                        >
                          <SelectValue>
                            {effectiveWaffoPancakeCheckoutRegion === 'china'
                              ? t('China')
                              : t('Global')}
                          </SelectValue>
                        </SelectTrigger>
                        <SelectContent alignItemWithTrigger={false}>
                          <SelectGroup>
                            <SelectItem value='china'>{t('China')}</SelectItem>
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
                  <Label className='text-muted-foreground text-xs font-medium tracking-wider uppercase'>
                    {neutralMode ? t('Payment Method') : t('Waffo Payment')}
                  </Label>
                  <div className='grid grid-cols-2 gap-1.5 sm:gap-3 lg:grid-cols-3'>
                    {waffoMethods.map((method, index) => {
                      const loadingKey = `waffo-${index}`
                      const methodKey = `${method.payMethodType ?? 'unknown'}-${method.payMethodName ?? method.name}`
                      const waffoMin = topupInfo?.waffo_min_topup || 0
                      const belowMin = waffoMin > topupAmount
                      const disabledReason = belowMin
                        ? t('Minimum topup amount: {{amount}}', {
                            amount: formatPlatformCreditBalance(waffoMin),
                          })
                        : undefined
                      const disabledLabel = belowMin
                        ? `${t('Minimum:')} ${formatPlatformCreditBalance(waffoMin)}`
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
                          disabled={belowMin || !!paymentLoading}
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

                      return belowMin ? (
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
            </>
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

      {!neutralMode && hasConfigurableTopup ? (
        <div className='flex flex-col gap-3 pt-4 sm:pt-6'>
          <Separator />
          <div className='flex items-center gap-2'>
            <IconBadge tone='success' size='xs'>
              <HugeiconsIcon icon={GiftIcon} strokeWidth={2} />
            </IconBadge>
            <Label
              htmlFor='discount-code'
              className='text-muted-foreground text-xs font-medium tracking-wider uppercase'
            >
              {discountCodeFromUrl
                ? t('Discount code from URL')
                : t('Discount code')}
            </Label>
          </div>
          <div className='grid grid-cols-[minmax(0,1fr)_auto] gap-2'>
            <Input
              id='discount-code'
              value={discountCode}
              onChange={(e) => onDiscountCodeChange?.(e.target.value)}
              placeholder={t('Enter your discount code')}
              readOnly={discountCodeFromUrl}
              aria-readonly={discountCodeFromUrl}
              className={cn(
                'h-9 min-w-0 uppercase',
                discountCodeFromUrl && 'bg-muted font-mono'
              )}
              autoComplete='off'
              maxLength={64}
            />
            <Button
              onClick={onApplyDiscount}
              disabled={
                discountCodeFromUrl || discountApplying || !discountCode.trim()
              }
              variant='outline'
              className='h-9 px-4'
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
          {discountCodeFromUrl ? (
            <p className='text-muted-foreground text-xs'>
              {t('This code came from the checkout link and cannot be edited.')}
            </p>
          ) : null}
          {discountPercent !== null && discountPercent !== undefined ? (
            <div className='text-success flex flex-wrap items-center gap-x-3 gap-y-1 text-xs'>
              <span>
                {t('Discount applied: {{percent}}% off', {
                  percent: discountPercent,
                })}
              </span>
              {discountCodeSavingAmount > 0 ? (
                <span className='font-medium'>
                  {t('Discount code saves {{amount}}', {
                    amount: formatSelectedPaymentAmount(
                      discountCodeSavingAmount
                    ),
                  })}
                </span>
              ) : null}
            </div>
          ) : (
            <p className='text-muted-foreground text-xs'>
              {t(
                'A valid discount code is applied at checkout and cannot be combined with another code.'
              )}
            </p>
          )}
        </div>
      ) : null}
    </TitledCard>
  )
}
