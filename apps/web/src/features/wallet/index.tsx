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
import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { useAuthUserRefresh } from '@/features/onboarding'
import { useStatus } from '@/hooks/use-status'
import { isConsoleActivated } from '@/lib/console-activation'
import { isLocalPreview } from '@/lib/local-preview'
import {
  getDefaultWaffoPancakeCheckoutRegion,
  getWaffoPancakeCheckoutLanguage,
  type WaffoPancakeCheckoutRegion,
} from '@/lib/waffo-pancake-checkout'
import { useAuthStore } from '@/stores/auth-store'

import { isApiSuccess, validateDiscountCode } from './api'
import { AffiliateRewardsCard } from './components/affiliate-rewards-card'
import { BillingHistoryDialog } from './components/dialogs/billing-history-dialog'
import { CreemConfirmDialog } from './components/dialogs/creem-confirm-dialog'
import { PaymentConfirmDialog } from './components/dialogs/payment-confirm-dialog'
import { TransferDialog } from './components/dialogs/transfer-dialog'
import { RechargeFormCard } from './components/recharge-form-card'
import { SubscriptionPlansCard } from './components/subscription-plans-card'
import { TrustLevelPanel } from './components/trust-level-panel'
import { WalletStatsCard } from './components/wallet-stats-card'
import { DEFAULT_DISCOUNT_RATE, PAYMENT_TYPES } from './constants'
import {
  useTopupInfo,
  usePayment,
  isPositivePaymentAmount,
  useAffiliate,
  useRedemption,
  useCreemPayment,
  useWaffoPayment,
  useWaffoPancakePayment,
} from './hooks'
import { useCheckoutScope } from './hooks/use-checkout-scope'
import {
  getTopupAvailability,
  getMinTopupAmount,
  isPaymentMethodCurrencySupported,
  dispatchSelectedPayment,
} from './lib'
import { discountAfterAmountChange } from './lib/discount-state'
import {
  expectedSettlement,
  isSettlementQuoteChanged,
} from './lib/settlement-quote'
import type {
  UserWalletData,
  PaymentMethod,
  PresetAmount,
  CreemProduct,
  WaffoPayMethod,
} from './types'

interface WalletProps {
  initialShowHistory?: boolean
}

type DiscountValidationContext = {
  amount: number
  paymentType: string
  revision: number
}

type PaymentFeedback = {
  tone: 'default' | 'success' | 'destructive'
  message: string
}

const PAYMENT_REFRESH_INTERVAL_MS = 3_000
const PAYMENT_REFRESH_DEADLINE_MS = 2 * 60 * 1_000

export function Wallet(props: WalletProps) {
  const { key } = useCheckoutScope()
  return <WalletCheckout key={key} {...props} />
}

function WalletCheckout(props: WalletProps) {
  const { t, i18n } = useTranslation()
  const { isCurrent } = useCheckoutScope()
  const authUser = useAuthStore((state) => state.auth.user)
  const { refreshUser } = useAuthUserRefresh()
  const user = authUser as UserWalletData | null
  const userLoading = authUser === null
  const localPreview = isLocalPreview()
  const developerAccessGranted = !localPreview && isConsoleActivated(authUser)
  const [enteredTopupAmount, setTopupAmount] = useState<number | null>(null)
  const [selectedPreset, setSelectedPreset] = useState<number | null>(null)
  const [selectedPaymentMethod, setSelectedPaymentMethod] =
    useState<PaymentMethod>()
  const [selectedWaffoMethodIndex, setSelectedWaffoMethodIndex] = useState<
    number | null
  >(null)
  const [paymentLoading, setPaymentLoading] = useState<string | null>(null)
  const [confirmDialogOpen, setConfirmDialogOpen] = useState(false)
  const [transferDialogOpen, setTransferDialogOpen] = useState(false)
  const [billingDialogOverride, setBillingDialogOpen] = useState<
    boolean | null
  >(null)
  const billingDialogOpen =
    billingDialogOverride ??
    (props.initialShowHistory === true && developerAccessGranted)
  const [redemptionCode, setRedemptionCode] = useState('')
  const [initialDiscountCode] = useState(() =>
    typeof window === 'undefined'
      ? ''
      : (new URLSearchParams(window.location.search)
          .get('discount_code')
          ?.trim() ?? '')
  )
  const [discountCode, setDiscountCode] = useState(initialDiscountCode)
  const [discountCodeFromUrl, setDiscountCodeFromUrl] =
    useState(initialDiscountCode)
  const [appliedDiscountCode, setAppliedDiscountCode] = useState('')
  const [discountPercent, setDiscountPercent] = useState<number | null>(null)
  const [discountApplying, setDiscountApplying] = useState(false)
  const [creemDialogOpen, setCreemDialogOpen] = useState(false)
  const [selectedCreemProduct, setSelectedCreemProduct] =
    useState<CreemProduct | null>(null)
  const [showSubscriptionPanel, setShowSubscriptionPanel] = useState(true)
  const [waffoPancakeCheckoutRegionOverride, setWaffoPancakeCheckoutRegion] =
    useState<WaffoPancakeCheckoutRegion | null>(null)
  const [pendingCheckoutDeadline, setPendingCheckoutDeadline] = useState<
    number | null
  >(null)
  const [paymentFeedback, setPaymentFeedback] =
    useState<PaymentFeedback | null>(null)
  const paymentInputRevisionRef = useRef(0)
  const confirmedQuoteRevisionRef = useRef<number | null>(null)
  const discountUrlValidationRef = useRef<
    (DiscountValidationContext & { code: string }) | null
  >(null)
  const { status } = useStatus()
  const {
    topupInfo,
    presetAmounts,
    loading: topupLoading,
    error: topupError,
    refetch: refetchTopupInfo,
  } = useTopupInfo()
  const topupAmount = enteredTopupAmount ?? getMinTopupAmount(topupInfo)
  const topupAvailability = useMemo(
    () => getTopupAvailability(topupInfo),
    [topupInfo]
  )
  const {
    amount: paymentAmount,
    calculating,
    processing,
    calculatePaymentAmount,
    processPayment,
    settlementQuote,
    invalidateQuote,
  } = usePayment()
  const resetPendingPayment = useCallback(() => {
    confirmedQuoteRevisionRef.current = null
    invalidateQuote()
    setConfirmDialogOpen(false)
    setPaymentLoading(null)
    setDiscountApplying(false)
    return ++paymentInputRevisionRef.current
  }, [invalidateQuote])
  const {
    affiliateLink,
    loading: affiliateLoading,
    transferQuota,
    transferring,
  } = useAffiliate({ enabled: developerAccessGranted })
  const { redeeming, redeemCode } = useRedemption()
  const { processing: creemProcessing, processCreemPayment } = useCreemPayment()
  const { processing: waffoProcessing, processWaffoPayment } = useWaffoPayment()
  const { processing: pancakeProcessing, processWaffoPancakePayment } =
    useWaffoPancakePayment()
  const interfaceLanguage = i18n.resolvedLanguage || i18n.language
  const waffoPancakeCheckoutRegion =
    waffoPancakeCheckoutRegionOverride ??
    getDefaultWaffoPancakeCheckoutRegion(interfaceLanguage)
  const waffoPancakeCheckoutLanguage =
    getWaffoPancakeCheckoutLanguage(interfaceLanguage)

  const handleWaffoPancakeCheckoutRegionChange = useCallback(
    (region: WaffoPancakeCheckoutRegion) => {
      setWaffoPancakeCheckoutRegion(region)
    },
    []
  )

  const refreshWalletUser = useCallback(async () => {
    await refreshUser()
  }, [refreshUser])

  const refreshAfterPaymentLaunch = useCallback(async () => {
    const refreshedUser = await refreshUser()
    if (
      isCurrent() &&
      !developerAccessGranted &&
      !isConsoleActivated(refreshedUser)
    ) {
      setPendingCheckoutDeadline(Date.now() + PAYMENT_REFRESH_DEADLINE_MS)
    }
  }, [developerAccessGranted, refreshUser, isCurrent])

  useEffect(() => {
    if (pendingCheckoutDeadline === null) return
    if (developerAccessGranted) return

    let cancelled = false
    let timeoutId: number | undefined

    const scheduleNextPoll = () => {
      timeoutId = window.setTimeout(
        () => void poll(),
        PAYMENT_REFRESH_INTERVAL_MS
      )
    }
    const poll = async () => {
      if (cancelled) return
      if (Date.now() >= pendingCheckoutDeadline) {
        setPendingCheckoutDeadline(null)
        return
      }
      if (document.visibilityState !== 'visible') {
        scheduleNextPoll()
        return
      }

      const refreshedUser = await refreshUser()
      if (cancelled) return
      if (isConsoleActivated(refreshedUser)) {
        setPendingCheckoutDeadline(null)
        return
      }
      scheduleNextPoll()
    }

    scheduleNextPoll()

    return () => {
      cancelled = true
      if (timeoutId !== undefined) window.clearTimeout(timeoutId)
    }
  }, [developerAccessGranted, pendingCheckoutDeadline, refreshUser])

  useEffect(() => {
    if (props.initialShowHistory) {
      window.history.replaceState({}, '', window.location.pathname)
    }
  }, [developerAccessGranted, props.initialShowHistory])

  // Initialize topup amount when topup info is loaded
  const topupAmountInitializedRef = useRef(false)
  useEffect(() => {
    if (topupInfo && !topupAmountInitializedRef.current) {
      const defaultPaymentType = topupAvailability.defaultQuotedType
      if (!defaultPaymentType) return

      topupAmountInitializedRef.current = true
      const minTopup = getMinTopupAmount(topupInfo)
      // Calculate initial payment amount with default payment type
      calculatePaymentAmount(minTopup, defaultPaymentType, appliedDiscountCode)
    }
  }, [
    topupInfo,
    topupAvailability,
    calculatePaymentAmount,
    appliedDiscountCode,
  ])

  // Get current payment type (selected or default)
  const getCurrentPaymentType = useCallback(() => {
    return selectedPaymentMethod?.type || topupAvailability.defaultQuotedType
  }, [selectedPaymentMethod, topupAvailability])

  const applyDiscountCode = useCallback(
    async (
      rawCode: string,
      fromUrl = false,
      context?: DiscountValidationContext
    ): Promise<number> => {
      const { amount, paymentType, revision } = context ?? {
        amount: topupAmount,
        paymentType: getCurrentPaymentType(),
        revision: resetPendingPayment(),
      }
      const code = rawCode.trim()
      setAppliedDiscountCode('')
      setDiscountPercent(null)
      if (!code || !paymentType) return 0

      // Record the full request before changing state so the URL effect cannot
      // duplicate a validation already started by a payment or amount change.
      if (fromUrl) {
        discountUrlValidationRef.current = {
          code,
          amount,
          paymentType,
          revision,
        }
      }
      setDiscountApplying(true)
      let applied = false
      try {
        const result = await validateDiscountCode({
          code,
          amount,
          payment_method: paymentType,
        })
        if (!isCurrent() || revision !== paymentInputRevisionRef.current) {
          return 0
        }
        if (!isApiSuccess(result) || !result.data) {
          toast.error(result.message || t('Discount code is invalid'))
          void calculatePaymentAmount(amount, paymentType)
          return 0
        }

        const calculatedAmount = await calculatePaymentAmount(
          amount,
          paymentType,
          result.data.code
        )
        if (!isCurrent() || revision !== paymentInputRevisionRef.current) {
          return 0
        }
        if (!isPositivePaymentAmount(calculatedAmount)) {
          toast.error(t('Payment request failed'))
          return 0
        }

        setAppliedDiscountCode(result.data.code)
        setDiscountCode(result.data.code)
        if (fromUrl) {
          discountUrlValidationRef.current = {
            code: result.data.code,
            amount,
            paymentType,
            revision,
          }
          setDiscountCodeFromUrl(result.data.code)
        }
        setDiscountPercent(result.data.discount_percent)
        applied = true
        toast.success(
          t('Discount applied: {{percent}}% off', {
            percent: result.data.discount_percent,
          })
        )
        return calculatedAmount
      } catch {
        if (!isCurrent() || revision !== paymentInputRevisionRef.current) {
          return 0
        }
        toast.error(t('Discount code is invalid'))
        void calculatePaymentAmount(amount, paymentType)
        return 0
      } finally {
        if (isCurrent() && revision === paymentInputRevisionRef.current) {
          setDiscountApplying(false)
          // A rejected link or failed quote must leave a manual recovery path.
          if (fromUrl && !applied) setDiscountCodeFromUrl('')
        }
      }
    },
    [
      calculatePaymentAmount,
      getCurrentPaymentType,
      isCurrent,
      resetPendingPayment,
      t,
      topupAmount,
    ]
  )

  useEffect(() => {
    if (!discountCodeFromUrl || !topupInfo) return
    if (topupAmount < getMinTopupAmount(topupInfo)) return
    const paymentType = getCurrentPaymentType()
    if (!paymentType) return
    const previous = discountUrlValidationRef.current
    if (
      previous?.amount === topupAmount &&
      previous.paymentType === paymentType &&
      previous.code === discountCodeFromUrl &&
      previous.revision === paymentInputRevisionRef.current
    ) {
      return
    }

    void applyDiscountCode(discountCodeFromUrl, true)
  }, [
    applyDiscountCode,
    discountCodeFromUrl,
    getCurrentPaymentType,
    topupAmount,
    topupInfo,
  ])

  const updateTopupAmount = (amount: number, preset: number | null) => {
    const revision = resetPendingPayment()
    const nextDiscount = discountAfterAmountChange(
      { code: appliedDiscountCode, percent: discountPercent },
      topupAmount,
      amount
    )
    setTopupAmount(amount)
    setSelectedPreset(preset)
    if (nextDiscount.code !== appliedDiscountCode) {
      setAppliedDiscountCode(nextDiscount.code)
      setDiscountPercent(nextDiscount.percent)
    }
    const paymentType = getCurrentPaymentType()
    if (!paymentType) return
    if (discountCodeFromUrl && amount >= getMinTopupAmount(topupInfo)) {
      void applyDiscountCode(discountCodeFromUrl, true, {
        amount,
        paymentType,
        revision,
      })
    } else {
      void calculatePaymentAmount(amount, paymentType, nextDiscount.code)
    }
  }

  const handleSelectPreset = (preset: PresetAmount) => {
    updateTopupAmount(preset.value, preset.value)
  }

  const handleTopupAmountChange = (amount: number) => {
    updateTopupAmount(amount, null)
  }

  const calculateCheckoutAmount = (paymentType: string, revision: number) => {
    const code =
      discountCodeFromUrl ||
      appliedDiscountCode ||
      (discountApplying ? discountCode.trim() : '')
    return code
      ? applyDiscountCode(code, Boolean(discountCodeFromUrl), {
          amount: topupAmount,
          paymentType,
          revision,
        })
      : calculatePaymentAmount(topupAmount, paymentType)
  }

  // Handle payment method selection
  const handlePaymentMethodSelect = async (method: PaymentMethod) => {
    if (!isPaymentMethodCurrencySupported(method.type)) {
      toast.error(
        t(
          'Waffo Pancake currently supports USD only. Please set this gateway currency to USD.'
        )
      )
      return
    }

    const revision = resetPendingPayment()
    setSelectedPaymentMethod(method)
    setSelectedWaffoMethodIndex(null)
    setPaymentLoading(method.type)

    try {
      // Validate minimum topup
      const minTopup = getMinTopupAmount(topupInfo)
      if (topupAmount < minTopup) {
        return
      }

      // Calculate payment amount and show confirmation dialog
      const calculatedAmount = await calculateCheckoutAmount(
        method.type,
        revision
      )
      if (!isCurrent() || revision !== paymentInputRevisionRef.current) return
      if (!isPositivePaymentAmount(calculatedAmount)) {
        setSelectedPaymentMethod(undefined)
        toast.error(t('Payment request failed'))
        return
      }
      confirmedQuoteRevisionRef.current = revision
      setConfirmDialogOpen(true)
    } finally {
      if (revision === paymentInputRevisionRef.current) {
        setPaymentLoading(null)
      }
    }
  }

  // Handle payment confirmation
  const handlePaymentConfirm = async () => {
    if (localPreview) {
      toast.info(
        t(
          'Local preview only: no payment is started and no balance is changed.'
        )
      )
      return
    }

    if (
      !isCurrent() ||
      !selectedPaymentMethod ||
      processing ||
      waffoProcessing ||
      pancakeProcessing ||
      (selectedPaymentMethod.type === PAYMENT_TYPES.WAFFO_PANCAKE &&
        !settlementQuote) ||
      discountApplying ||
      calculating ||
      !isPositivePaymentAmount(paymentAmount) ||
      confirmedQuoteRevisionRef.current !== paymentInputRevisionRef.current
    ) {
      return
    }

    setPaymentFeedback({ tone: 'default', message: t('Submitting...') })

    if (!isPaymentMethodCurrencySupported(selectedPaymentMethod.type)) {
      setConfirmDialogOpen(false)
      setPaymentFeedback({
        tone: 'destructive',
        message: t('Payment request failed'),
      })
      toast.error(
        t(
          'Waffo Pancake currently supports USD only. Please set this gateway currency to USD.'
        )
      )
      return
    }

    const revision = paymentInputRevisionRef.current
    try {
      const success = await dispatchSelectedPayment(
        selectedPaymentMethod,
        topupAmount,
        selectedWaffoMethodIndex,
        {
          regular: processPayment,
          waffo: processWaffoPayment,
          waffoPancake: processWaffoPancakePayment,
        },
        {
          checkout_region: waffoPancakeCheckoutRegion,
          checkout_language: waffoPancakeCheckoutLanguage,
          ...(settlementQuote ? expectedSettlement(settlementQuote) : {}),
        },
        appliedDiscountCode
      )
      if (!isCurrent() || revision !== paymentInputRevisionRef.current) return

      if (success) {
        setPaymentFeedback({
          tone: 'success',
          message: t('Payment page opened'),
        })
        setConfirmDialogOpen(false)
        await refreshAfterPaymentLaunch()
      } else {
        setPaymentFeedback({
          tone: 'destructive',
          message: t('Payment request failed'),
        })
      }
    } catch (error) {
      if (!isCurrent() || revision !== paymentInputRevisionRef.current) return
      if (isSettlementQuoteChanged(error)) {
        resetPendingPayment()
        setPaymentFeedback({
          tone: 'destructive',
          message: t(
            'The payment quote changed. Review the updated amount and confirm again.'
          ),
        })
        // Refresh only. A new method click and confirmation must start checkout.
        void calculatePaymentAmount(
          topupAmount,
          selectedPaymentMethod.type,
          appliedDiscountCode
        )
      } else {
        setPaymentFeedback({
          tone: 'destructive',
          message: t('Payment request failed'),
        })
      }
    }
  }

  // Handle redemption
  const handleRedeem = async () => {
    if (localPreview) {
      toast.info(
        t(
          'Local preview only: no payment is started and no balance is changed.'
        )
      )
      return
    }

    if (!redemptionCode) return

    const success = await redeemCode(redemptionCode)
    if (success) {
      setRedemptionCode('')
      await refreshWalletUser()
    }
  }

  const handleApplyDiscount = () => {
    void applyDiscountCode(discountCode)
  }

  // Handle transfer
  const handleTransfer = async (amount: number) => {
    if (localPreview) {
      toast.info(
        t(
          'Local preview only: no payment is started and no balance is changed.'
        )
      )
      return false
    }

    const success = await transferQuota(amount)
    if (success) {
      await refreshWalletUser()
    }
    return success
  }

  // Handle Creem product selection
  const handleCreemProductSelect = (product: CreemProduct) => {
    setSelectedCreemProduct(product)
    setCreemDialogOpen(true)
  }

  // Handle Creem payment confirmation
  const handleCreemConfirm = async () => {
    if (localPreview) {
      toast.info(
        t(
          'Local preview only: no payment is started and no balance is changed.'
        )
      )
      return
    }

    if (!selectedCreemProduct) return

    setPaymentFeedback({ tone: 'default', message: t('Submitting...') })

    const success = await processCreemPayment(selectedCreemProduct.productId)
    if (success) {
      setPaymentFeedback({
        tone: 'success',
        message: t('Payment page opened'),
      })
      setCreemDialogOpen(false)
      setSelectedCreemProduct(null)
      await refreshAfterPaymentLaunch()
    } else {
      setPaymentFeedback({
        tone: 'destructive',
        message: t('Payment request failed'),
      })
    }
  }

  const handleWaffoMethodSelect = async (
    method: WaffoPayMethod,
    index: number
  ) => {
    const revision = resetPendingPayment()
    const loadingKey = `waffo-${index}`
    setSelectedPaymentMethod({
      name: method.name,
      type: PAYMENT_TYPES.WAFFO,
      icon: method.icon,
      settlement_unit: topupInfo?.waffo_currency || 'USD',
      unit_price: topupInfo?.waffo_unit_price,
    })
    setSelectedWaffoMethodIndex(index)
    setPaymentLoading(loadingKey)

    try {
      const calculatedAmount = await calculateCheckoutAmount(
        PAYMENT_TYPES.WAFFO,
        revision
      )
      if (!isCurrent() || revision !== paymentInputRevisionRef.current) return
      if (!isPositivePaymentAmount(calculatedAmount)) {
        setSelectedPaymentMethod(undefined)
        setSelectedWaffoMethodIndex(null)
        toast.error(t('Payment request failed'))
        return
      }
      confirmedQuoteRevisionRef.current = revision
      setConfirmDialogOpen(true)
    } finally {
      if (revision === paymentInputRevisionRef.current) {
        setPaymentLoading(null)
      }
    }
  }

  // Get discount rate for current topup amount
  const getDiscountRate = useCallback(() => {
    return topupInfo?.discount?.[topupAmount] || DEFAULT_DISCOUNT_RATE
  }, [topupInfo, topupAmount])

  const handleSubscriptionAvailabilityChange = useCallback(
    (available: boolean) => {
      setShowSubscriptionPanel(available)
    },
    []
  )

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Wallet')}</SectionPageLayout.Title>
        <SectionPageLayout.Content>
          <div className='wallet-editorial mx-auto flex w-full max-w-7xl flex-col gap-4 sm:gap-5'>
            {paymentFeedback ? (
              <Alert
                variant={
                  paymentFeedback.tone === 'destructive'
                    ? 'destructive'
                    : 'default'
                }
                role={
                  paymentFeedback.tone === 'destructive' ? 'alert' : 'status'
                }
              >
                <AlertDescription>{paymentFeedback.message}</AlertDescription>
              </Alert>
            ) : null}
            {developerAccessGranted ? (
              <>
                <WalletStatsCard user={user} loading={userLoading} />
                <TrustLevelPanel user={user} loading={userLoading} />
              </>
            ) : null}

            <div
              className={
                developerAccessGranted && showSubscriptionPanel
                  ? 'grid gap-4 xl:grid-cols-[minmax(0,1.05fr)_minmax(360px,0.95fr)] xl:items-start'
                  : 'grid gap-4'
              }
            >
              <div id='wallet-add-funds' className='scroll-mt-4'>
                <RechargeFormCard
                  topupInfo={topupInfo}
                  topupAvailability={topupAvailability}
                  presetAmounts={presetAmounts}
                  selectedPreset={selectedPreset}
                  onSelectPreset={handleSelectPreset}
                  topupAmount={topupAmount}
                  onTopupAmountChange={handleTopupAmountChange}
                  paymentAmount={paymentAmount}
                  settlementQuote={settlementQuote}
                  selectedPaymentMethod={selectedPaymentMethod}
                  calculating={calculating}
                  onPaymentMethodSelect={handlePaymentMethodSelect}
                  paymentLoading={paymentLoading}
                  redemptionCode={redemptionCode}
                  onRedemptionCodeChange={setRedemptionCode}
                  onRedeem={handleRedeem}
                  redeeming={redeeming}
                  discountCode={discountCode}
                  discountCodeFromUrl={Boolean(discountCodeFromUrl)}
                  onDiscountCodeChange={(value) => {
                    if (discountCodeFromUrl) return
                    resetPendingPayment()
                    setDiscountCode(value)
                    if (
                      appliedDiscountCode &&
                      value.trim() !== appliedDiscountCode
                    ) {
                      setAppliedDiscountCode('')
                      setDiscountPercent(null)
                      const paymentType = getCurrentPaymentType()
                      if (paymentType) {
                        void calculatePaymentAmount(topupAmount, paymentType)
                      }
                    }
                  }}
                  onApplyDiscount={handleApplyDiscount}
                  discountApplying={discountApplying}
                  discountPercent={discountPercent}
                  topupLink={topupInfo?.topup_link}
                  loading={topupLoading}
                  error={topupError}
                  onRetry={refetchTopupInfo}
                  priceRatio={(status?.price as number) || 1}
                  onOpenBilling={() => setBillingDialogOpen(true)}
                  onCreemProductSelect={handleCreemProductSelect}
                  onWaffoMethodSelect={handleWaffoMethodSelect}
                  waffoPancakeCheckoutRegion={waffoPancakeCheckoutRegion}
                  onWaffoPancakeCheckoutRegionChange={
                    handleWaffoPancakeCheckoutRegionChange
                  }
                  neutralMode={!developerAccessGranted}
                />
              </div>

              {developerAccessGranted ? (
                <SubscriptionPlansCard
                  topupInfo={topupInfo}
                  onAvailabilityChange={handleSubscriptionAvailabilityChange}
                  userId={user?.id}
                  userQuota={user?.quota}
                  onPurchaseSuccess={refreshWalletUser}
                />
              ) : null}
            </div>

            {developerAccessGranted ? (
              <div id='referral-program' className='scroll-mt-4'>
                <AffiliateRewardsCard
                  user={user}
                  affiliateLink={affiliateLink}
                  onTransfer={() => setTransferDialogOpen(true)}
                  complianceConfirmed={
                    topupInfo?.payment_compliance_confirmed !== false
                  }
                  loading={affiliateLoading}
                />
              </div>
            ) : null}
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <PaymentConfirmDialog
        open={confirmDialogOpen}
        onOpenChange={setConfirmDialogOpen}
        onConfirm={handlePaymentConfirm}
        topupAmount={topupAmount}
        paymentAmount={paymentAmount}
        settlementQuote={settlementQuote}
        paymentMethod={selectedPaymentMethod}
        calculating={calculating || discountApplying}
        processing={processing || waffoProcessing || pancakeProcessing}
        discountRate={getDiscountRate()}
        discountCode={appliedDiscountCode}
        discountPercent={discountPercent}
        neutralMode={!developerAccessGranted}
      />

      {developerAccessGranted ? (
        <TransferDialog
          open={transferDialogOpen}
          onOpenChange={setTransferDialogOpen}
          onConfirm={handleTransfer}
          availableQuota={user?.aff_quota ?? 0}
          transferring={transferring}
        />
      ) : null}

      {developerAccessGranted ? (
        <BillingHistoryDialog
          open={billingDialogOpen}
          onOpenChange={setBillingDialogOpen}
        />
      ) : null}

      <CreemConfirmDialog
        open={creemDialogOpen}
        onOpenChange={setCreemDialogOpen}
        onConfirm={handleCreemConfirm}
        product={selectedCreemProduct}
        processing={creemProcessing}
        neutralMode={!developerAccessGranted}
      />
    </>
  )
}
