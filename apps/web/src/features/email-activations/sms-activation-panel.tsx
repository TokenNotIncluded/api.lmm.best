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
/*
Copyright (C) 2026 LIghtJUNction
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useDebounce } from '@/hooks/use-debounce'
import { useAuthStore } from '@/stores/auth-store'

import {
  createHeroSmsIdempotencyKey,
  formatHeroSmsPlatformAmount,
  parseHeroSmsError,
} from './api.js'
import { usePageVisibility } from './hooks.js'
import {
  cancelHeroSmsSmsOrder,
  clearHeroSmsSmsOrderHistory,
  createHeroSmsSmsOrder,
  getHeroSmsSmsOffer,
  hideHeroSmsSmsOrderFromHistory,
  listCurrentHeroSmsSmsOrders,
  listHeroSmsSmsCountries,
  listHeroSmsSmsOperators,
  listHeroSmsSmsOrders,
  listHeroSmsSmsServices,
  refreshHeroSmsSmsOrder,
  submitHeroSmsSmsComplaint,
  type HeroSmsSmsComplaintReason,
  type HeroSmsSmsCountry,
  type HeroSmsSmsOffer,
  type HeroSmsSmsOrder,
  type HeroSmsSmsService,
} from './sms-api.js'
import { SmsBalanceNotice } from './sms-balance-notice.js'
import { isSmsMinimumBalanceError } from './sms-balance.js'
import { describeSmsAccessError } from './sms-error.js'
import {
  SmsActiveOrdersCard,
  SmsOrderDetailDialog,
  SmsOrderHistoryCard,
} from './sms-order-sections.js'
import { SmsPurchaseCard } from './sms-purchase-card.js'
import {
  checkSmsPurchaseRecovery,
  clearSmsPurchaseRecovery,
  readSmsPurchaseRecovery,
  recoveryBatchResult,
  saveSmsPurchaseRecovery,
  useSmsPurchaseRecovery,
} from './sms-purchase-recovery.js'
import {
  purchaseHeroSmsBatch,
  selectHeroSmsPriceTier,
  type HeroSmsBatchPurchaseResult,
} from './sms-purchase.js'
import {
  clampHeroSmsQuantity,
  getHeroSmsCountryName,
  getHeroSmsCurrentOrderPollingInterval,
  hasHeroSmsFavorite,
  HERO_SMS_MAX_FAVORITES,
  HERO_SMS_MAX_QUANTITY,
  isHeroSmsWhatsAppService,
  loadHeroSmsFavorites,
  resolveHeroSmsReceivingChannel,
  selectHeroSmsHistoryOrders,
  toggleHeroSmsFavorite,
  type HeroSmsFavoritePair,
} from './sms-selection.js'
import { useSmsPurchaseBalance } from './sms-use-purchase-balance.js'

const smsKeys = {
  countries: (service = 'all') =>
    ['hero-sms', 'sms', 'countries', service] as const,
  services: ['hero-sms', 'sms', 'services'] as const,
  operators: (country: string) =>
    ['hero-sms', 'sms', 'operators', country] as const,
  offer: (country: string, service: string, operator: string) =>
    ['hero-sms', 'sms', 'offer', country, service, operator] as const,
  bidOffer: (
    country: string,
    service: string,
    operator: string,
    maxPriceUSD: string
  ) =>
    [
      'hero-sms',
      'sms',
      'bid-offer',
      country,
      service,
      operator,
      maxPriceUSD,
    ] as const,
  current: ['hero-sms', 'sms', 'current'] as const,
  currentList: ['hero-sms', 'sms', 'current-list'] as const,
  history: ['hero-sms', 'sms', 'history'] as const,
  order: (orderId: string) => ['hero-sms', 'sms', 'order', orderId] as const,
}

type Translate = ReturnType<typeof useTranslation>['t']

interface SmsPurchaseMutationOptions {
  userId: number | undefined
  isMounted: () => boolean
  balance: ReturnType<typeof useSmsPurchaseBalance>
  offer?: HeroSmsSmsOffer
  quantity: number
  getFreshOffer: () => Promise<HeroSmsSmsOffer>
  t: Translate
  invalidate: () => Promise<void>
  refetchOffer: () => Promise<unknown>
  setConfirmOpen: (open: boolean) => void
  setBatchProgress: (
    progress: { completed: number; total: number } | null
  ) => void
  setBatchResult: (result: HeroSmsBatchPurchaseResult | null) => void
}

function batchFailureMessage(result: HeroSmsBatchPurchaseResult, t: Translate) {
  if (!result.failure) return ''
  if (result.failure.ambiguous) {
    return t(
      'The last purchase result is uncertain. Resolve it before buying again.'
    )
  }
  if (
    result.failure.code === 'PRICE_CHANGED' ||
    isSmsPurchaseNotCreated(result.failure.error)
  ) {
    return t('The price changed before item {{item}}. Review the new quote.', {
      item: result.failure.item,
    })
  }
  if (result.failure.code === 'OUT_OF_STOCK') {
    return t('Inventory ran out before item {{item}}.', {
      item: result.failure.item,
    })
  }
  if (isSmsMinimumBalanceError(result.failure.error)) {
    return t('Temporary SMS purchases require a balance of at least USD 10')
  }
  return t(parseHeroSmsError(result.failure.error).message)
}

function showBatchResult(result: HeroSmsBatchPurchaseResult, t: Translate) {
  const failureMessage = batchFailureMessage(result, t)
  if (!result.failure) {
    toast.success(
      t('{{count}} phone activations purchased', {
        count: result.orders.length,
      })
    )
    return
  }
  if (result.orders.length > 0) {
    toast.warning(
      t(
        'Purchased {{succeeded}} of {{requested}} phone activations. {{reason}}',
        {
          succeeded: result.orders.length,
          requested: result.requested,
          reason: failureMessage,
        }
      )
    )
    return
  }
  toast.error(failureMessage)
}

function useSmsPurchaseMutation(options: SmsPurchaseMutationOptions) {
  const mutation = useMutation({
    mutationFn: (attempt: SmsPurchaseMutationOptions) => {
      if (!attempt.offer || attempt.userId === undefined) {
        throw new Error('HeroSMS request failed')
      }
      const userId = attempt.userId
      const assertActive = () => {
        if (!attempt.isMounted() || !attempt.balance.isCurrentSession()) {
          throw new Error('HeroSMS request failed')
        }
      }
      assertActive()
      if (readSmsPurchaseRecovery(userId)) {
        throw new Error('HeroSMS request failed')
      }
      return purchaseHeroSmsBatch({
        initialOffer: attempt.offer,
        quantity: attempt.quantity,
        idempotencyKey: createHeroSmsIdempotencyKey(),
        getFreshOffer: async () => {
          assertActive()
          const offer = await attempt.getFreshOffer()
          assertActive()
          return offer
        },
        createOrder: async (offerId, idempotencyKey, item, requested) => {
          assertActive()
          const record = { userId, offerId, idempotencyKey, item, requested }
          try {
            await saveSmsPurchaseRecovery(
              record,
              () => attempt.isMounted() && attempt.balance.isCurrentSession()
            )
          } catch {
            throw new SmsPurchaseNotStartedError('HeroSMS request failed')
          }
          if (!attempt.isMounted() || !attempt.balance.isCurrentSession()) {
            // Once published, another tab can already be replaying this pair.
            // Keep it recoverable even if this panel never sends its own POST.
            throw new Error('HeroSMS request failed')
          }
          const result = await createHeroSmsSmsOrder(offerId, idempotencyKey)
          if (attempt.balance.isCurrentSession()) {
            const settled = await clearSmsPurchaseRecovery(
              record,
              attempt.balance.isCurrentSession,
              () => {
                if (attempt.isMounted() && attempt.balance.isCurrentSession()) {
                  attempt.balance.recordQuota(result.quota)
                }
              }
            )
            if (!attempt.balance.isCurrentSession()) return result
            if (!settled || !attempt.isMounted()) {
              await attempt.balance.invalidateQuota()
            }
          }
          return result
        },
        isAmbiguousNetworkError: isUncertainSmsPurchaseError,
        onProgress: (completed, total) => {
          if (attempt.isMounted() && attempt.balance.isCurrentSession()) {
            attempt.setBatchProgress({ completed, total })
          }
        },
      })
    },
    onMutate: (attempt) => {
      attempt.setBatchResult(null)
      attempt.setBatchProgress({ completed: 0, total: attempt.quantity })
    },
    onSuccess: async (result, attempt) => {
      let failure = result.failure
      if (failure && isSmsPurchaseNotCreated(failure.error)) {
        // The safe retry conservatively wraps its errors as unknown. This
        // server proof settles the original pair as well as the retry.
        failure = { ...failure, ambiguous: false }
        result = { ...result, failure }
      }
      if (
        isSmsPurchaseNotCreated(failure?.error) &&
        failure?.offerId &&
        failure.idempotencyKey &&
        attempt.userId !== undefined &&
        attempt.balance.isCurrentSession()
      ) {
        await clearSmsPurchaseRecovery(
          {
            userId: attempt.userId,
            offerId: failure.offerId,
            idempotencyKey: failure.idempotencyKey,
            item: failure.item,
            requested: result.requested,
          },
          attempt.balance.isCurrentSession
        )
      }
      if (!attempt.isMounted() || !attempt.balance.isCurrentSession()) return
      attempt.setConfirmOpen(false)
      attempt.setBatchResult(result)
      if (isSmsMinimumBalanceError(failure?.error)) attempt.balance.markDenied()
      showBatchResult(result, attempt.t)
      attempt.setBatchProgress(null)
      await attempt.invalidate()
      if (attempt.isMounted() && attempt.balance.isCurrentSession()) {
        await attempt.refetchOffer()
      }
    },
    onError: (error, attempt) => {
      if (!attempt.isMounted() || !attempt.balance.isCurrentSession()) return
      attempt.setBatchProgress(null)
      if (isSmsMinimumBalanceError(error)) {
        attempt.balance.markDenied()
        toast.error(
          attempt.t(
            'Temporary SMS purchases require a balance of at least USD 10'
          )
        )
      } else {
        toast.error(attempt.t(parseHeroSmsError(error).message))
      }
    },
  })
  return { ...mutation, purchase: () => mutation.mutate(options) }
}

class SmsPurchaseNotStartedError extends Error {}

function isSmsPurchaseNotCreated(error: unknown) {
  const parsed = parseHeroSmsError(error)
  return parsed.status === 409 && parsed.code === 'PURCHASE_NOT_CREATED'
}

function isUncertainSmsPurchaseError(error: unknown) {
  if (error instanceof SmsPurchaseNotStartedError) return false
  // Another context may still have this exact request in flight. Only the
  // backend's serialized no-purchase proof can rule out a future settlement.
  return !isSmsPurchaseNotCreated(error)
}

function useSmsPurchaseReconciliation({
  recovery,
  balance,
  isMounted,
  purchasePending,
  setResult,
  invalidate,
  refetchOffer,
  t,
}: {
  recovery: ReturnType<typeof useSmsPurchaseRecovery>
  balance: ReturnType<typeof useSmsPurchaseBalance>
  isMounted: () => boolean
  purchasePending: boolean
  setResult: (result: HeroSmsBatchPurchaseResult | null) => void
  invalidate: () => Promise<void>
  refetchOffer: () => Promise<unknown>
  t: Translate
}) {
  const sessionId = useAuthStore((state) => state.auth.session?.sid)
  const [pendingOwner, setPendingOwner] = useState<{
    userId: number | undefined
    sessionId: string | undefined
  } | null>(null)
  const pending =
    pendingOwner !== null &&
    pendingOwner.userId === recovery.userId &&
    pendingOwner.sessionId === sessionId
  const run = async () => {
    const record = recovery.pending
    if (
      !record ||
      pending ||
      purchasePending ||
      !isMounted() ||
      !balance.isCurrentSession()
    ) {
      return
    }
    const owner = { userId: recovery.userId, sessionId }
    setPendingOwner(owner)
    try {
      const current = await checkSmsPurchaseRecovery(
        record,
        () => isMounted() && balance.isCurrentSession()
      )
      if (!isMounted() || !balance.isCurrentSession()) return
      if (!current) {
        toast.error(t('HeroSMS request failed'))
        return
      }
      const result = await createHeroSmsSmsOrder(
        record.offerId,
        record.idempotencyKey
      )
      if (!balance.isCurrentSession()) return
      const settled = await clearSmsPurchaseRecovery(
        record,
        balance.isCurrentSession,
        () => {
          if (!isMounted() || !balance.isCurrentSession()) return
          balance.recordQuota(result.quota)
          toast.success(t('Purchase result reconciled'))
          setResult(null)
        }
      )
      if (!balance.isCurrentSession()) return
      if (!settled || !isMounted()) {
        await balance.invalidateQuota()
        return
      }
      await invalidate()
      if (isMounted() && balance.isCurrentSession()) await refetchOffer()
    } catch (error) {
      if (!isMounted() || !balance.isCurrentSession()) return
      const parsed = parseHeroSmsError(error)
      if (isSmsPurchaseNotCreated(error)) {
        const settled = await clearSmsPurchaseRecovery(
          record,
          balance.isCurrentSession,
          () => {
            if (!isMounted() || !balance.isCurrentSession()) return
            const result: HeroSmsBatchPurchaseResult = {
              requested: record.requested,
              orders: [],
              completedCount: record.item - 1,
              failure: { code: 'PRICE_CHANGED', item: record.item },
            }
            setResult(result)
            toast.error(batchFailureMessage(result, t))
          }
        )
        if (!isMounted() || !balance.isCurrentSession()) return
        if (!settled) {
          await balance.invalidateQuota()
          return
        }
        await invalidate()
        if (isMounted() && balance.isCurrentSession()) await refetchOffer()
      } else if (isUncertainSmsPurchaseError(error)) {
        toast.error(
          t(
            'The last purchase result is uncertain. Resolve it before buying again.'
          )
        )
      } else {
        toast.error(t(parsed.message))
      }
    } finally {
      if (isMounted()) {
        setPendingOwner((current) => (current === owner ? null : current))
      }
    }
  }
  return { pending, run }
}

interface SmsCatalogQueryOptions {
  country: string
  service: string
  operator: string
  bidMaxPriceUSD: string
  pageVisible: boolean
  catalogEnabled: boolean
}

function useSmsCatalogQueries({
  country,
  service,
  operator,
  bidMaxPriceUSD,
  pageVisible,
  catalogEnabled,
}: SmsCatalogQueryOptions) {
  const allCountries = useQuery({
    queryKey: smsKeys.countries(),
    queryFn: () => listHeroSmsSmsCountries(),
    enabled: catalogEnabled,
    retry: false,
    staleTime: 5 * 60 * 1000,
  })
  const countries = useQuery({
    queryKey: smsKeys.countries(service),
    queryFn: () => listHeroSmsSmsCountries(service),
    enabled: catalogEnabled && service !== '',
    staleTime: 5 * 60 * 1000,
  })
  const services = useQuery({
    queryKey: smsKeys.services,
    queryFn: listHeroSmsSmsServices,
    enabled: catalogEnabled,
    retry: false,
    staleTime: 5 * 60 * 1000,
  })
  const operators = useQuery({
    queryKey: smsKeys.operators(country),
    queryFn: () => listHeroSmsSmsOperators(Number(country)),
    enabled: catalogEnabled && country !== '',
    staleTime: 5 * 60 * 1000,
    retry: false,
  })
  const offer = useQuery({
    queryKey: smsKeys.offer(country, service, operator),
    queryFn: () =>
      getHeroSmsSmsOffer({
        country: Number(country),
        service,
        operator: operator.trim() || undefined,
      }),
    enabled: catalogEnabled && country !== '' && service !== '',
    retry: false,
  })
  const bidOffer = useQuery({
    queryKey: smsKeys.bidOffer(country, service, operator, bidMaxPriceUSD),
    queryFn: () =>
      getHeroSmsSmsOffer({
        country: Number(country),
        service,
        operator: operator.trim() || undefined,
        maxPriceUSD: bidMaxPriceUSD,
      }),
    enabled:
      catalogEnabled &&
      country !== '' &&
      service !== '' &&
      Number.isFinite(Number(bidMaxPriceUSD)) &&
      Number(bidMaxPriceUSD) > 0,
    retry: false,
  })
  const current = useQuery({
    queryKey: smsKeys.currentList,
    queryFn: listCurrentHeroSmsSmsOrders,
    refetchInterval: (query) =>
      getHeroSmsCurrentOrderPollingInterval(query.state.data, pageVisible),
  })
  const history = useQuery({
    queryKey: smsKeys.history,
    queryFn: () => listHeroSmsSmsOrders(1, 50),
  })
  return {
    allCountries,
    countries,
    services,
    operators,
    offer,
    bidOffer,
    current,
    history,
  }
}

interface SmsMarketplaceQueryOptions {
  country: string
  service: string
  operator: string
  selectedTierPrice: string
  bidEnabled: boolean
  bidPrice: string
  pageVisible: boolean
  catalogEnabled: boolean
}

function useSmsMarketplaceQueries({
  country,
  service,
  operator,
  selectedTierPrice,
  bidEnabled,
  bidPrice,
  pageVisible,
  catalogEnabled,
}: SmsMarketplaceQueryOptions) {
  const normalizedBidPrice = bidEnabled ? bidPrice.trim() : ''
  const debouncedBidPrice = useDebounce(normalizedBidPrice, 400)
  const queries = useSmsCatalogQueries({
    country,
    service,
    operator,
    bidMaxPriceUSD: debouncedBidPrice,
    pageVisible,
    catalogEnabled,
  })
  const tierOffer = queries.offer.data
    ? selectHeroSmsPriceTier(queries.offer.data, selectedTierPrice)
    : undefined
  const bidInputReady =
    normalizedBidPrice !== '' &&
    normalizedBidPrice === debouncedBidPrice &&
    Number.isFinite(Number(normalizedBidPrice)) &&
    Number(normalizedBidPrice) > 0
  let effectiveOffer = tierOffer
  if (bidEnabled) {
    const bidOffer = queries.bidOffer.data
    effectiveOffer =
      bidInputReady && bidOffer?.bid === true ? bidOffer : undefined
  }
  const effectiveOfferQuery = bidEnabled ? queries.bidOffer : queries.offer
  const getFreshOffer = useCallback(async () => {
    const fresh = await getHeroSmsSmsOffer({
      country: Number(country),
      service,
      operator: operator.trim() || undefined,
      maxPriceUSD: bidEnabled ? normalizedBidPrice : undefined,
    })
    if (bidEnabled) {
      if (fresh.bid !== true) throw new Error('HeroSMS request failed')
      return fresh
    }
    const selected = selectHeroSmsPriceTier(fresh, selectedTierPrice)
    if (!selected) throw new Error('HeroSMS request failed')
    return selected
  }, [
    bidEnabled,
    country,
    normalizedBidPrice,
    operator,
    selectedTierPrice,
    service,
  ])
  const refetchBaseOffer = queries.offer.refetch
  const refetchBidOffer = queries.bidOffer.refetch
  const refetchEffectiveOffer = useCallback(
    () => (bidEnabled ? refetchBidOffer() : refetchBaseOffer()),
    [bidEnabled, refetchBaseOffer, refetchBidOffer]
  )
  return {
    queries,
    effectiveOffer,
    effectiveOfferQuery,
    getFreshOffer,
    refetchEffectiveOffer,
  }
}

function useSmsSelectionState() {
  const userId = useAuthStore((state) => state.auth.user?.id)
  const sessionId = useAuthStore((state) => state.auth.session?.sid)
  const [country, setCountry] = useState('')
  const [service, setService] = useState('')
  const [operator, setOperator] = useState('')
  const [selectedTierPrice, setSelectedTierPrice] = useState('')
  const [bidEnabled, setBidEnabled] = useState(false)
  const [bidPrice, setBidPrice] = useState('')
  const [quantity, setQuantity] = useState(1)
  const [favorites, setFavorites] = useState<HeroSmsFavoritePair[]>(() =>
    loadHeroSmsFavorites()
  )
  const [batchState, setBatchState] = useState<{
    userId: number | undefined
    sessionId: string | undefined
    result: HeroSmsBatchPurchaseResult | null
  } | null>(null)
  const batchResult =
    batchState &&
    batchState.userId === userId &&
    batchState.sessionId === sessionId
      ? batchState.result
      : null
  const setBatchResult = (result: HeroSmsBatchPurchaseResult | null) => {
    setBatchState({ userId, sessionId, result })
  }
  const [lastSmsService, setLastSmsService] = useState('')

  const resetSelectionTail = () => {
    setOperator('')
    setSelectedTierPrice('')
    setBidEnabled(false)
    setBidPrice('')
    setQuantity(1)
    setBatchResult(null)
  }
  const selectService = (value: string) => {
    if (value && !isHeroSmsWhatsAppService(value)) {
      setLastSmsService(value)
    }
    setService(value)
    setCountry('')
    resetSelectionTail()
  }
  const selectCountry = (value: string) => {
    setCountry(value)
    resetSelectionTail()
  }
  const selectFavorite = (favorite: HeroSmsFavoritePair) => {
    if (!isHeroSmsWhatsAppService(favorite.serviceCode)) {
      setLastSmsService(favorite.serviceCode)
    }
    setService(favorite.serviceCode)
    setCountry(String(favorite.countryId))
    resetSelectionTail()
  }
  const selectOperator = (value: string) => {
    setOperator(value)
    setSelectedTierPrice('')
    setBidEnabled(false)
    setBidPrice('')
    setQuantity(1)
    setBatchResult(null)
  }
  return {
    country,
    setCountry,
    service,
    setService,
    operator,
    setOperator,
    selectedTierPrice,
    setSelectedTierPrice,
    bidEnabled,
    setBidEnabled,
    bidPrice,
    setBidPrice,
    selectOperator,
    quantity,
    setQuantity,
    favorites,
    setFavorites,
    batchResult,
    setBatchResult,
    lastSmsService,
    selectService,
    selectCountry,
    selectFavorite,
  }
}

function useSmsCatalogMaps(
  services: HeroSmsSmsService[] | undefined,
  countries: HeroSmsSmsCountry[] | undefined
) {
  const serviceMap = useMemo(
    () => new Map((services ?? []).map((item) => [item.code, item] as const)),
    [services]
  )
  const countryMap = useMemo(
    () => new Map((countries ?? []).map((item) => [item.id, item] as const)),
    [countries]
  )
  return { serviceMap, countryMap }
}

function useSmsHistoryOrders(
  items: HeroSmsSmsOrder[] | undefined,
  current: HeroSmsSmsOrder[]
) {
  return useMemo(
    () => selectHeroSmsHistoryOrders(items, current),
    [current, items]
  )
}

function createFavoriteController({
  favorites,
  setFavorites,
  service,
  country,
  t,
}: {
  favorites: HeroSmsFavoritePair[]
  setFavorites: (favorites: HeroSmsFavoritePair[]) => void
  service?: { code: string }
  country?: { id: number }
  t: Translate
}) {
  const selected = Boolean(
    service &&
    country &&
    hasHeroSmsFavorite(favorites, service.code, country.id)
  )
  const updateFavorite = (favorite: HeroSmsFavoritePair) => {
    const update = toggleHeroSmsFavorite(favorites, favorite)
    if (update.limitReached) {
      toast.error(
        t('You can save up to {{count}} favorite combinations', {
          count: HERO_SMS_MAX_FAVORITES,
        })
      )
      return
    }
    setFavorites(update.items)
    if (!update.persisted) {
      toast.warning(
        t(
          'Favorite changed for this session, but browser storage is unavailable'
        )
      )
    }
  }
  const toggle = () => {
    if (!service || !country) return
    updateFavorite({
      serviceCode: service.code,
      countryId: country.id,
    })
  }
  const remove = (favorite: HeroSmsFavoritePair) => {
    if (
      hasHeroSmsFavorite(favorites, favorite.serviceCode, favorite.countryId)
    ) {
      updateFavorite(favorite)
    }
  }
  return { selected, toggle, remove }
}

function resolveSmsLanguage(resolved?: string, configured?: string) {
  return resolved || configured || 'en'
}

function resolveSmsQuantity(quantity: number, offer?: HeroSmsSmsOffer) {
  return clampHeroSmsQuantity(
    quantity,
    offer?.inventory ?? HERO_SMS_MAX_QUANTITY
  )
}

function createSmsPanelView({
  effectiveQuantity,
  offer,
  purchasePending,
  batchResult,
  selectedCountry,
  country,
  language,
  historyError,
  t,
}: {
  effectiveQuantity: number
  offer?: HeroSmsSmsOffer
  purchasePending: boolean
  batchResult: HeroSmsBatchPurchaseResult | null
  selectedCountry?: HeroSmsSmsCountry
  country: string
  language: string
  historyError: unknown
  t: Translate
}) {
  return {
    effectiveQuantity,
    totalPrice: Number(offer?.customer_price_usd ?? 0) * effectiveQuantity,
    canPurchase: Boolean(
      offer &&
      offer.inventory >= effectiveQuantity &&
      !purchasePending &&
      !batchResult?.failure?.ambiguous
    ),
    batchFeedback: batchResult ? batchFailureMessage(batchResult, t) : '',
    selectedCountryName: selectedCountry
      ? getHeroSmsCountryName(selectedCountry, language)
      : country,
    historyError: t(parseHeroSmsError(historyError).message),
  }
}

// pi-lens-ignore: high-fan-out -- composition root delegates domain and rendering responsibilities.
export function HeroSmsSmsActivationPanel() {
  const { t, i18n } = useTranslation()
  const queryClient = useQueryClient()
  const purchaseBalance = useSmsPurchaseBalance()
  const recovery = useSmsPurchaseRecovery()
  const sessionId = useAuthStore((state) => state.auth.session?.sid)
  const mounted = useRef(false)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])
  const isMounted = useCallback(() => mounted.current, [])
  const language = resolveSmsLanguage(i18n.resolvedLanguage, i18n.language)
  const pageVisible = usePageVisibility()
  const {
    country,
    service,
    operator,
    selectedTierPrice,
    setSelectedTierPrice,
    bidEnabled,
    setBidEnabled,
    bidPrice,
    setBidPrice,
    selectOperator,
    quantity,
    setQuantity,
    favorites,
    setFavorites,
    batchResult: selectionBatchResult,
    setBatchResult,
    lastSmsService,
    selectService,
    selectCountry,
    selectFavorite,
  } = useSmsSelectionState()
  const batchResult = recovery.pending
    ? recoveryBatchResult(recovery.pending)
    : selectionBatchResult?.failure?.ambiguous
      ? null
      : selectionBatchResult
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [historyDetailOrderId, setHistoryDetailOrderId] = useState<
    string | null
  >(null)
  const [historyCleanupTarget, setHistoryCleanupTarget] = useState<
    { kind: 'one'; orderId: string } | { kind: 'all' } | null
  >(null)
  const [cancelConfirmOrderId, setCancelConfirmOrderId] = useState<
    string | null
  >(null)
  const [progressState, setProgressState] = useState<{
    userId: number | undefined
    sessionId: string | undefined
    progress: { completed: number; total: number } | null
  } | null>(null)
  const batchProgress =
    progressState &&
    progressState.userId === recovery.userId &&
    progressState.sessionId === sessionId
      ? progressState.progress
      : null
  const setBatchProgress = (
    progress: { completed: number; total: number } | null
  ) => {
    setProgressState({ userId: recovery.userId, sessionId, progress })
  }
  useEffect(() => setConfirmOpen(false), [recovery.userId, sessionId])
  const {
    queries,
    effectiveOffer,
    effectiveOfferQuery,
    getFreshOffer,
    refetchEffectiveOffer,
  } = useSmsMarketplaceQueries({
    country,
    service,
    operator,
    selectedTierPrice,
    bidEnabled,
    bidPrice,
    pageVisible,
    catalogEnabled: purchaseBalance.canPurchase,
  })

  const historyDetailQuery = useQuery({
    queryKey: smsKeys.order(historyDetailOrderId ?? 'none'),
    queryFn: () => refreshHeroSmsSmsOrder(historyDetailOrderId || ''),
    enabled: historyDetailOrderId !== null,
    staleTime: 30_000,
  })

  const { serviceMap, countryMap } = useSmsCatalogMaps(
    queries.services.data,
    queries.allCountries.data
  )
  const selectedService = serviceMap.get(service)
  const selectedCountry = countryMap.get(Number(country))
  const receivingChannel = resolveHeroSmsReceivingChannel(
    selectedService ?? service
  )
  const whatsappService = (queries.services.data ?? []).find(
    isHeroSmsWhatsAppService
  )
  const firstSmsService = (queries.services.data ?? []).find(
    (item) => !isHeroSmsWhatsAppService(item)
  )
  const selectReceivingChannel = (channel: 'sms' | 'whatsapp') => {
    selectService(
      channel === 'whatsapp'
        ? whatsappService?.code || 'wa'
        : lastSmsService || firstSmsService?.code || ''
    )
  }
  const currentOrders = useMemo(
    () => queries.current.data ?? [],
    [queries.current.data]
  )
  const observedCodes = useRef<Map<string, string> | null>(null)
  useEffect(() => {
    const nextCodes = new Map(
      currentOrders.map((order) => [order.id, order.code || ''])
    )
    if (observedCodes.current) {
      for (const order of currentOrders) {
        const previousCode = observedCodes.current.get(order.id)
        if (previousCode !== undefined && !previousCode && order.code) {
          toast.success(t('Verification code received'))
        }
      }
    }
    observedCodes.current = nextCodes
  }, [currentOrders, t])
  const historyOrders = useSmsHistoryOrders(
    queries.history.data?.items,
    currentOrders
  )
  const effectiveQuantity = resolveSmsQuantity(quantity, effectiveOffer)
  const invalidate = useCallback(async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: smsKeys.current }),
      queryClient.invalidateQueries({ queryKey: smsKeys.currentList }),
      queryClient.invalidateQueries({ queryKey: smsKeys.history }),
      queryClient.invalidateQueries({ queryKey: ['user'] }),
    ])
  }, [queryClient])
  const purchaseMutation = useSmsPurchaseMutation({
    userId: recovery.userId,
    isMounted,
    balance: purchaseBalance,
    offer: effectiveOffer,
    quantity: effectiveQuantity,
    getFreshOffer,
    t,
    invalidate,
    refetchOffer: refetchEffectiveOffer,
    setConfirmOpen,
    setBatchProgress,
    setBatchResult,
  })
  const refreshMutation = useMutation({
    mutationFn: (orderId: string) => refreshHeroSmsSmsOrder(orderId),
    onSuccess: invalidate,
    onError: (error) => toast.error(t(parseHeroSmsError(error).message)),
  })
  const complaintMutation = useMutation({
    mutationFn: (input: {
      orderId: string
      reason: HeroSmsSmsComplaintReason
    }) => submitHeroSmsSmsComplaint(input.orderId, input.reason),
    onSuccess: async () => {
      toast.success(t('Complaint submitted to HeroSMS'))
      await invalidate()
    },
    onError: (error) => toast.error(t(parseHeroSmsError(error).message)),
  })
  const cancelMutation = useMutation({
    mutationFn: async (orderId: string) => {
      const result = await cancelHeroSmsSmsOrder(orderId)
      purchaseBalance.recordQuota(result.quota)
      return result
    },
    onSuccess: async (result) => {
      if (
        result.order.status === 'cancelled' &&
        result.order.refunded_quota > 0
      ) {
        toast.success(t('Upstream cancellation confirmed and balance refunded'))
      } else {
        toast.info(
          t(
            'Cancellation submitted. Your balance is refunded only after HeroSMS confirms the upstream cancellation.'
          )
        )
      }
      setCancelConfirmOrderId(null)
      await invalidate()
    },
    onError: (error) => toast.error(t(parseHeroSmsError(error).message)),
  })
  const historyCleanupMutation = useMutation({
    mutationFn: async (
      target: { kind: 'one'; orderId: string } | { kind: 'all' }
    ) => {
      if (target.kind === 'one') {
        await hideHeroSmsSmsOrderFromHistory(target.orderId)
        return target
      }
      await clearHeroSmsSmsOrderHistory()
      return target
    },
    onSuccess: async (target) => {
      toast.success(
        t(
          target.kind === 'one'
            ? 'Phone activation record removed'
            : 'Phone activation history cleared'
        )
      )
      if (target.kind === 'all') {
        queryClient.removeQueries({ queryKey: ['hero-sms', 'sms', 'order'] })
        setHistoryDetailOrderId(null)
      } else {
        queryClient.removeQueries({ queryKey: smsKeys.order(target.orderId) })
        if (target.orderId === historyDetailOrderId) {
          setHistoryDetailOrderId(null)
        }
      }
      setHistoryCleanupTarget(null)
      await queryClient.invalidateQueries({ queryKey: smsKeys.history })
    },
    onError: (error) => toast.error(t(parseHeroSmsError(error).message)),
  })

  const favoriteController = createFavoriteController({
    favorites,
    setFavorites,
    service: selectedService,
    country: selectedCountry,
    t,
  })
  const ownPurchasePending =
    purchaseMutation.isPending &&
    Boolean(purchaseMutation.variables?.balance.isCurrentSession())
  const reconciliation = useSmsPurchaseReconciliation({
    recovery,
    balance: purchaseBalance,
    isMounted,
    purchasePending: ownPurchasePending,
    setResult: setBatchResult,
    invalidate,
    refetchOffer: refetchEffectiveOffer,
    t,
  })

  const view = createSmsPanelView({
    effectiveQuantity,
    offer: effectiveOffer,
    purchasePending: ownPurchasePending,
    batchResult,
    selectedCountry,
    country,
    language,
    historyError: queries.history.error,
    t,
  })
  const catalogError = queries.services.error ?? queries.allCountries.error
  const catalogFeedback =
    purchaseBalance.canPurchase && catalogError
      ? describeSmsAccessError(catalogError, t)
      : null

  return (
    <div className='space-y-6'>
      {catalogFeedback ? (
        <Alert variant='destructive' role='alert'>
          <AlertTitle>{catalogFeedback.title}</AlertTitle>
          <AlertDescription>{catalogFeedback.description}</AlertDescription>
        </Alert>
      ) : null}
      <SmsBalanceNotice
        {...purchaseBalance}
        onRefresh={() => void purchaseBalance.refresh()}
      />
      <div className='console-sms-workspace grid gap-5 xl:grid-cols-[minmax(0,430px)_minmax(0,1fr)]'>
        <SmsPurchaseCard
          language={language}
          services={queries.services.data ?? []}
          countries={queries.countries.data ?? []}
          favoriteCountries={queries.allCountries.data ?? []}
          servicesState={{
            isPending:
              purchaseBalance.canPurchase && queries.services.isPending,
            isError: queries.services.isError,
            onRetry: () => void queries.services.refetch(),
          }}
          countriesState={{
            isPending:
              purchaseBalance.canPurchase && queries.countries.isPending,
            isError: queries.countries.isError,
            onRetry: () => void queries.countries.refetch(),
          }}
          favorites={favorites}
          channel={receivingChannel}
          service={service}
          country={country}
          operator={operator}
          operators={queries.operators.data ?? []}
          operatorsState={{
            isPending:
              purchaseBalance.canPurchase && queries.operators.isPending,
            isError: queries.operators.isError,
            onRetry: () => void queries.operators.refetch(),
          }}
          selectedTierPrice={selectedTierPrice}
          bidEnabled={bidEnabled}
          bidPrice={bidPrice}
          quantity={view.effectiveQuantity}
          selectedService={selectedService}
          selectedCountry={selectedCountry}
          selectedIsFavorite={favoriteController.selected}
          offer={effectiveOffer}
          catalogOffer={queries.offer.data}
          offerIsFetching={effectiveOfferQuery.isFetching}
          offerIsError={effectiveOfferQuery.isError}
          offerError={effectiveOfferQuery.error}
          batchProgress={batchProgress}
          batchResult={batchResult}
          batchFeedback={view.batchFeedback}
          canPurchase={view.canPurchase && purchaseBalance.canPurchase}
          reconciliationPending={
            ownPurchasePending ||
            reconciliation.pending ||
            queries.current.isFetching
          }
          onChannelChange={selectReceivingChannel}
          onServiceChange={selectService}
          onCountryChange={selectCountry}
          onOperatorChange={selectOperator}
          onTierChange={(price) => {
            setSelectedTierPrice(price)
            setBidEnabled(false)
          }}
          onBidEnabledChange={setBidEnabled}
          onBidPriceChange={(value) => {
            setBidPrice(value)
            setBidEnabled(true)
          }}
          onQuantityChange={setQuantity}
          onSelectFavorite={selectFavorite}
          onRemoveFavorite={favoriteController.remove}
          onToggleFavorite={favoriteController.toggle}
          onRefreshOffer={() => void refetchEffectiveOffer()}
          onReconcile={() => void reconciliation.run()}
          onPurchase={() => {
            if (view.canPurchase && purchaseBalance.canPurchase) {
              setConfirmOpen(true)
            }
          }}
        />
        <Tabs defaultValue='current' className='console-sms-orders min-w-0'>
          <TabsList
            aria-label={t('Phone number')}
            className='grid w-full grid-cols-2 group-data-horizontal/tabs:h-auto'
          >
            <TabsTrigger
              value='current'
              className='h-auto min-w-0 px-2 whitespace-normal'
            >
              {t('Active phone activations')} ({currentOrders.length})
            </TabsTrigger>
            <TabsTrigger
              value='history'
              className='h-auto min-w-0 px-2 whitespace-normal'
            >
              {t('Phone activation history')}
            </TabsTrigger>
          </TabsList>
          <TabsContent value='current' keepMounted>
            <SmsActiveOrdersCard
              orders={currentOrders}
              countries={countryMap}
              services={serviceMap}
              language={language}
              isPending={queries.current.isPending}
              isError={queries.current.isError}
              errorTitle={t('Unable to load current phone activation')}
              errorDescription={t(
                parseHeroSmsError(queries.current.error).message
              )}
              onRetry={() => void queries.current.refetch()}
              refresh={{
                pendingOrderId: refreshMutation.isPending
                  ? refreshMutation.variables
                  : undefined,
                onOrder: (orderId) => refreshMutation.mutate(orderId),
              }}
              complaint={{
                pendingOrderId: complaintMutation.isPending
                  ? complaintMutation.variables?.orderId
                  : undefined,
                onOrder: (orderId, reason) =>
                  complaintMutation.mutate({ orderId, reason }),
              }}
              cancel={{
                pendingOrderId: cancelMutation.isPending
                  ? cancelMutation.variables
                  : undefined,
                onOrder: setCancelConfirmOrderId,
              }}
            />
          </TabsContent>
          <TabsContent value='history' keepMounted>
            <SmsOrderHistoryCard
              orders={historyOrders}
              countries={countryMap}
              services={serviceMap}
              language={language}
              isPending={queries.history.isPending}
              isError={queries.history.isError}
              errorTitle={t('Unable to load phone activation history')}
              errorDescription={view.historyError}
              onRetry={() => void queries.history.refetch()}
              onOpenOrder={setHistoryDetailOrderId}
              onRemoveOrder={(orderId) =>
                setHistoryCleanupTarget({ kind: 'one', orderId })
              }
              onClearHistory={() => setHistoryCleanupTarget({ kind: 'all' })}
              cleanupPending={historyCleanupMutation.isPending}
            />
          </TabsContent>
        </Tabs>
      </div>
      <SmsOrderDetailDialog
        open={historyDetailOrderId !== null}
        onOpenChange={(open) => {
          if (!open) setHistoryDetailOrderId(null)
        }}
        order={historyDetailQuery.data?.order}
        countries={countryMap}
        services={serviceMap}
        language={language}
        isPending={historyDetailQuery.isPending}
        isError={historyDetailQuery.isError}
        errorDescription={t(
          parseHeroSmsError(historyDetailQuery.error).message
        )}
        onRetry={() => void historyDetailQuery.refetch()}
      />
      <ConfirmDialog
        open={cancelConfirmOrderId !== null}
        onOpenChange={(open) => {
          if (!open && !cancelMutation.isPending) {
            setCancelConfirmOrderId(null)
          }
        }}
        title={t('Cancel this phone activation?')}
        desc={t(
          'HeroSMS will be asked to cancel this activation. Your balance is refunded only after HeroSMS confirms cancellation; a verification code received first will complete the order instead.'
        )}
        confirmText={t('Cancel and request refund')}
        destructive
        handleConfirm={() => {
          if (cancelConfirmOrderId) {
            cancelMutation.mutate(cancelConfirmOrderId)
          }
        }}
        isLoading={cancelMutation.isPending}
      />
      <ConfirmDialog
        open={historyCleanupTarget !== null}
        onOpenChange={(open) => {
          if (!open && !historyCleanupMutation.isPending) {
            setHistoryCleanupTarget(null)
          }
        }}
        title={t(
          historyCleanupTarget?.kind === 'all'
            ? 'Clear phone activation history?'
            : 'Remove this phone activation record?'
        )}
        desc={t(
          historyCleanupTarget?.kind === 'all'
            ? 'All completed, cancelled, and failed records disappear from your history view. Active orders and billing audit data are retained.'
            : 'The record disappears from your history view. Billing and refund audit data are retained.'
        )}
        confirmText={t(
          historyCleanupTarget?.kind === 'all' ? 'Clear' : 'Remove'
        )}
        destructive
        handleConfirm={() => {
          if (historyCleanupTarget) {
            historyCleanupMutation.mutate(historyCleanupTarget)
          }
        }}
        isLoading={historyCleanupMutation.isPending}
      />
      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t('Confirm phone activation purchase')}
        desc={t(
          'Quantity: {{quantity}} · Service: {{service}} · Country: {{country}} · Maximum reserved total: {{price}}. Any lower settlement is refunded.',
          {
            quantity: view.effectiveQuantity,
            service: selectedService?.name ?? service,
            country: view.selectedCountryName,
            price: formatHeroSmsPlatformAmount(view.totalPrice),
          }
        )}
        confirmText={t('Confirm purchase')}
        disabled={
          !view.canPurchase ||
          !purchaseBalance.canPurchase ||
          purchaseBalance.isRefreshing
        }
        handleConfirm={() => {
          void purchaseBalance.refresh().then((allowed) => {
            if (allowed && isMounted() && purchaseBalance.isCurrentSession()) {
              purchaseMutation.purchase()
            }
          })
        }}
        isLoading={purchaseMutation.isPending}
      >
        <SmsBalanceNotice
          {...purchaseBalance}
          id='sms-confirm-balance-notice'
          onRefresh={() => void purchaseBalance.refresh()}
        />
      </ConfirmDialog>
    </div>
  )
}
