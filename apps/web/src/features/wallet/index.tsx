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
import {
  getTopupAvailability,
  getMinTopupAmount,
  isPaymentMethodCurrencySupported,
  dispatchSelectedPayment,
} from './lib'
import { discountAfterAmountChange } from './lib/discount-state'
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

type PaymentFeedback = {
  tone: 'default' | 'success' | 'destructive'
  message: string
}

const PAYMENT_REFRESH_INTERVAL_MS = 3_000
const PAYMENT_REFRESH_DEADLINE_MS = 2 * 60 * 1_000

export function Wallet(props: WalletProps) {
  const { t, i18n } = useTranslation()
  const authUser = useAuthStore((state) => state.auth.user)
  const { refreshUser } = useAuthUserRefresh()
  const user = authUser as UserWalletData | null
  const userLoading = authUser === null
  const localPreview = isLocalPreview()
  const developerAccessGranted = !localPreview && isConsoleActivated(authUser)
  const [topupAmount, setTopupAmount] = useState(0)
  const [selectedPreset, setSelectedPreset] = useState<number | null>(null)
  const [selectedPaymentMethod, setSelectedPaymentMethod] =
    useState<PaymentMethod>()
  const [selectedWaffoMethodIndex, setSelectedWaffoMethodIndex] = useState<
    number | null
  >(null)
  const [paymentLoading, setPaymentLoading] = useState<string | null>(null)
  const [confirmDialogOpen, setConfirmDialogOpen] = useState(false)
  const [transferDialogOpen, setTransferDialogOpen] = useState(false)
  const [billingDialogOpen, setBillingDialogOpen] = useState(false)
  const [redemptionCode, setRedemptionCode] = useState('')
  const [discountCode, setDiscountCode] = useState('')
  const [discountCodeFromUrl, setDiscountCodeFromUrl] = useState('')
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

  const { status } = useStatus()
  const {
    topupInfo,
    presetAmounts,
    loading: topupLoading,
    error: topupError,
    refetch: refetchTopupInfo,
  } = useTopupInfo()
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
  } = usePayment()
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
    if (!developerAccessGranted && !isConsoleActivated(refreshedUser)) {
      setPendingCheckoutDeadline(Date.now() + PAYMENT_REFRESH_DEADLINE_MS)
    }
  }, [developerAccessGranted, refreshUser])

  useEffect(() => {
    if (pendingCheckoutDeadline === null) return
    if (developerAccessGranted) {
      setPendingCheckoutDeadline(null)
      return
    }

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
      if (developerAccessGranted) setBillingDialogOpen(true)
      window.history.replaceState({}, '', window.location.pathname)
    }
  }, [developerAccessGranted, props.initialShowHistory])

  // Read the checkout link once. The actual validation waits for the quoted
  // amount and payment method so a shared link never applies a stale quote.
  useEffect(() => {
    if (typeof window === 'undefined') return
    const code = new URLSearchParams(window.location.search)
      .get('discount_code')
      ?.trim()
    if (!code) return
    setDiscountCodeFromUrl(code)
    setDiscountCode(code)
  }, [])

  // Initialize topup amount when topup info is loaded
  const topupAmountInitializedRef = useRef(false)
  useEffect(() => {
    if (topupInfo && !topupAmountInitializedRef.current) {
      const defaultPaymentType = topupAvailability.defaultQuotedType
      if (!defaultPaymentType) return

      topupAmountInitializedRef.current = true
      const minTopup = getMinTopupAmount(topupInfo)
      setTopupAmount(minTopup)

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
    async (rawCode: string, fromUrl = false) => {
      const code = rawCode.trim()
      if (!code) {
        setAppliedDiscountCode('')
        setDiscountPercent(null)
        return
      }

      setDiscountApplying(true)
      try {
        const result = await validateDiscountCode({
          code,
          amount: topupAmount,
          payment_method: getCurrentPaymentType() || undefined,
        })
        if (!isApiSuccess(result) || !result.data) {
          toast.error(result.message || t('Discount code is invalid'))
          setAppliedDiscountCode('')
          setDiscountPercent(null)
          return
        }

        setAppliedDiscountCode(result.data.code)
        setDiscountCode(result.data.code)
        if (fromUrl) setDiscountCodeFromUrl(result.data.code)
        setDiscountPercent(result.data.discount_percent)
        const paymentType = getCurrentPaymentType()
        if (paymentType) {
          await calculatePaymentAmount(
            topupAmount,
            paymentType,
            result.data.code
          )
        }
        toast.success(
          t('Discount applied: {{percent}}% off', {
            percent: result.data.discount_percent,
          })
        )
      } catch {
        toast.error(t('Discount code is invalid'))
        setAppliedDiscountCode('')
        setDiscountPercent(null)
      } finally {
        setDiscountApplying(false)
      }
    },
    [calculatePaymentAmount, getCurrentPaymentType, t, topupAmount]
  )

  const discountUrlValidatedAmountRef = useRef<number | null>(null)
  useEffect(() => {
    if (!discountCodeFromUrl || !topupInfo) return
    if (topupAmount < getMinTopupAmount(topupInfo)) return
    if (!getCurrentPaymentType()) return
    if (discountUrlValidatedAmountRef.current === topupAmount) return

    discountUrlValidatedAmountRef.current = topupAmount
    void applyDiscountCode(discountCodeFromUrl, true)
  }, [
    applyDiscountCode,
    discountCodeFromUrl,
    getCurrentPaymentType,
    topupAmount,
    topupInfo,
  ])

  // Handle preset selection
  const handleSelectPreset = (preset: PresetAmount) => {
    const nextDiscount = discountAfterAmountChange(
      { code: appliedDiscountCode, percent: discountPercent },
      topupAmount,
      preset.value
    )
    setTopupAmount(preset.value)
    setSelectedPreset(preset.value)
    if (nextDiscount.code !== appliedDiscountCode) {
      setAppliedDiscountCode(nextDiscount.code)
      setDiscountPercent(nextDiscount.percent)
    }
    const paymentType = getCurrentPaymentType()
    if (paymentType) {
      calculatePaymentAmount(preset.value, paymentType, nextDiscount.code)
    }
  }

  // Handle topup amount change
  const handleTopupAmountChange = (amount: number) => {
    const nextDiscount = discountAfterAmountChange(
      { code: appliedDiscountCode, percent: discountPercent },
      topupAmount,
      amount
    )
    setTopupAmount(amount)
    setSelectedPreset(null)
    if (nextDiscount.code !== appliedDiscountCode) {
      setAppliedDiscountCode(nextDiscount.code)
      setDiscountPercent(nextDiscount.percent)
    }
    const paymentType = getCurrentPaymentType()
    if (paymentType) {
      calculatePaymentAmount(amount, paymentType, nextDiscount.code)
    }
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
      const calculatedAmount = await calculatePaymentAmount(
        topupAmount,
        method.type,
        appliedDiscountCode
      )
      if (!isPositivePaymentAmount(calculatedAmount)) {
        setSelectedPaymentMethod(undefined)
        toast.error(t('Payment request failed'))
        return
      }
      setConfirmDialogOpen(true)
    } finally {
      setPaymentLoading(null)
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

    if (!selectedPaymentMethod) return

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
      },
      appliedDiscountCode
    )

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
      const calculatedAmount = await calculatePaymentAmount(
        topupAmount,
        PAYMENT_TYPES.WAFFO,
        appliedDiscountCode
      )
      if (!isPositivePaymentAmount(calculatedAmount)) {
        setSelectedPaymentMethod(undefined)
        setSelectedWaffoMethodIndex(null)
        toast.error(t('Payment request failed'))
        return
      }
      setConfirmDialogOpen(true)
    } finally {
      setPaymentLoading(null)
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
                    setDiscountCode(value)
                    if (
                      appliedDiscountCode &&
                      value.trim() !== appliedDiscountCode
                    ) {
                      setAppliedDiscountCode('')
                      setDiscountPercent(null)
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
              <AffiliateRewardsCard
                user={user}
                affiliateLink={affiliateLink}
                onTransfer={() => setTransferDialogOpen(true)}
                complianceConfirmed={
                  topupInfo?.payment_compliance_confirmed !== false
                }
                loading={affiliateLoading}
              />
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
        paymentMethod={selectedPaymentMethod}
        calculating={calculating}
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
