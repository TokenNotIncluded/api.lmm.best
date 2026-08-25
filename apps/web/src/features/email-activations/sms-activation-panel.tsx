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
import { isAxiosError } from 'axios'
import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'

import {
  createHeroSmsIdempotencyKey,
  formatHeroSmsUSD,
  parseHeroSmsError,
} from './api.js'
import { usePageVisibility } from './hooks.js'
import {
  cancelHeroSmsSmsOrder,
  createHeroSmsSmsOrder,
  getHeroSmsSmsOffer,
  listCurrentHeroSmsSmsOrders,
  listHeroSmsSmsCountries,
  listHeroSmsSmsOrders,
  listHeroSmsSmsServices,
  refreshHeroSmsSmsOrder,
  type HeroSmsSmsCountry,
  type HeroSmsSmsOffer,
  type HeroSmsSmsOrder,
  type HeroSmsSmsService,
} from './sms-api.js'
import {
  SmsActiveOrdersCard,
  SmsOrderHistoryCard,
} from './sms-order-sections.js'
import { SmsPurchaseCard } from './sms-purchase-card.js'
import {
  purchaseHeroSmsBatch,
  type HeroSmsBatchPurchaseResult,
} from './sms-purchase.js'
import {
  clampHeroSmsQuantity,
  getHeroSmsCountryName,
  hasHeroSmsFavorite,
  HERO_SMS_MAX_FAVORITES,
  HERO_SMS_MAX_QUANTITY,
  isActiveHeroSmsSmsOrder,
  loadHeroSmsFavorites,
  toggleHeroSmsFavorite,
  type HeroSmsFavoritePair,
} from './sms-selection.js'

const smsKeys = {
  countries: (service = 'all') =>
    ['hero-sms', 'sms', 'countries', service] as const,
  services: ['hero-sms', 'sms', 'services'] as const,
  offer: (country: string, service: string, operator: string) =>
    ['hero-sms', 'sms', 'offer', country, service, operator] as const,
  current: ['hero-sms', 'sms', 'current'] as const,
  currentList: ['hero-sms', 'sms', 'current-list'] as const,
  history: ['hero-sms', 'sms', 'history'] as const,
}

type Translate = ReturnType<typeof useTranslation>['t']

interface SmsPurchaseMutationOptions {
  offer?: HeroSmsSmsOffer
  quantity: number
  country: string
  service: string
  operator: string
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
  if (result.failure.code === 'PRICE_CHANGED') {
    return t('The price changed before item {{item}}. Review the new quote.', {
      item: result.failure.item,
    })
  }
  if (result.failure.code === 'OUT_OF_STOCK') {
    return t('Inventory ran out before item {{item}}.', {
      item: result.failure.item,
    })
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
  return useMutation({
    mutationFn: () => {
      if (!options.offer) throw new Error('HeroSMS request failed')
      return purchaseHeroSmsBatch({
        initialOffer: options.offer,
        quantity: options.quantity,
        idempotencyKey: createHeroSmsIdempotencyKey(),
        getFreshOffer: () =>
          getHeroSmsSmsOffer({
            country: Number(options.country),
            service: options.service,
            operator: options.operator.trim() || undefined,
          }),
        createOrder: createHeroSmsSmsOrder,
        isAmbiguousNetworkError: (error) =>
          isAxiosError(error) && !error.response,
        onProgress: (completed, total) =>
          options.setBatchProgress({ completed, total }),
      })
    },
    onMutate: () => {
      options.setBatchResult(null)
      options.setBatchProgress({ completed: 0, total: options.quantity })
    },
    onSuccess: async (result) => {
      options.setConfirmOpen(false)
      options.setBatchResult(result)
      showBatchResult(result, options.t)
      options.setBatchProgress(null)
      await options.invalidate()
      await options.refetchOffer()
    },
    onError: (error) => {
      options.setBatchProgress(null)
      toast.error(options.t(parseHeroSmsError(error).message))
    },
  })
}

function useSmsPurchaseReconciliation({
  result,
  setResult,
  invalidate,
  refetchOffer,
  t,
}: {
  result: HeroSmsBatchPurchaseResult | null
  setResult: (result: HeroSmsBatchPurchaseResult | null) => void
  invalidate: () => Promise<void>
  refetchOffer: () => Promise<unknown>
  t: Translate
}) {
  const [pending, setPending] = useState(false)
  const run = async () => {
    const failure = result?.failure
    if (!failure?.ambiguous || !failure.offerId || !failure.idempotencyKey) {
      return
    }
    setPending(true)
    try {
      await createHeroSmsSmsOrder(failure.offerId, failure.idempotencyKey)
      toast.success(t('Purchase result reconciled'))
      setResult(null)
      await invalidate()
      await refetchOffer()
    } catch (error) {
      const parsed = parseHeroSmsError(error)
      const stillAmbiguous =
        (isAxiosError(error) && !error.response) ||
        parsed.code === 'UPSTREAM_BUSY' ||
        (parsed.status !== undefined && parsed.status >= 500)
      if (stillAmbiguous) {
        toast.error(
          t(
            'The last purchase result is uncertain. Resolve it before buying again.'
          )
        )
      } else {
        toast.error(t(parsed.message))
        setResult(null)
        await invalidate()
        await refetchOffer()
      }
    } finally {
      setPending(false)
    }
  }
  return { pending, run }
}

function useSmsCatalogQueries(
  country: string,
  service: string,
  operator: string,
  pageVisible: boolean
) {
  const allCountries = useQuery({
    queryKey: smsKeys.countries(),
    queryFn: () => listHeroSmsSmsCountries(),
    staleTime: 5 * 60 * 1000,
  })
  const countries = useQuery({
    queryKey: smsKeys.countries(service),
    queryFn: () => listHeroSmsSmsCountries(service),
    enabled: service !== '',
    staleTime: 5 * 60 * 1000,
  })
  const services = useQuery({
    queryKey: smsKeys.services,
    queryFn: listHeroSmsSmsServices,
    staleTime: 5 * 60 * 1000,
  })
  const offer = useQuery({
    queryKey: smsKeys.offer(country, service, operator),
    queryFn: () =>
      getHeroSmsSmsOffer({
        country: Number(country),
        service,
        operator: operator.trim() || undefined,
      }),
    enabled: country !== '' && service !== '',
    retry: false,
  })
  const current = useQuery({
    queryKey: smsKeys.currentList,
    queryFn: listCurrentHeroSmsSmsOrders,
    refetchInterval: (query) => {
      if (!query.state.data?.some(isActiveHeroSmsSmsOrder)) return false
      return pageVisible ? 10_000 : 60_000
    },
  })
  const history = useQuery({
    queryKey: smsKeys.history,
    queryFn: () => listHeroSmsSmsOrders(1, 50),
  })
  return { allCountries, countries, services, offer, current, history }
}

function useSmsSelectionState() {
  const [country, setCountry] = useState('')
  const [service, setService] = useState('')
  const [operator, setOperator] = useState('')
  const [quantity, setQuantity] = useState(1)
  const [favorites, setFavorites] = useState<HeroSmsFavoritePair[]>(() =>
    loadHeroSmsFavorites()
  )
  const [batchResult, setBatchResult] =
    useState<HeroSmsBatchPurchaseResult | null>(null)

  const resetSelectionTail = () => {
    setOperator('')
    setQuantity(1)
    setBatchResult(null)
  }
  const selectService = (value: string) => {
    setService(value)
    setCountry('')
    resetSelectionTail()
  }
  const selectCountry = (value: string) => {
    setCountry(value)
    resetSelectionTail()
  }
  const selectFavorite = (favorite: HeroSmsFavoritePair) => {
    setService(favorite.serviceCode)
    setCountry(String(favorite.countryId))
    resetSelectionTail()
  }
  return {
    country,
    setCountry,
    service,
    setService,
    operator,
    setOperator,
    quantity,
    setQuantity,
    favorites,
    setFavorites,
    batchResult,
    setBatchResult,
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

function useSmsHistoryOrders(items: HeroSmsSmsOrder[] | undefined) {
  return useMemo(
    () => (items ?? []).filter((order) => !isActiveHeroSmsSmsOrder(order)),
    [items]
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
  const toggle = () => {
    if (!service || !country) return
    const update = toggleHeroSmsFavorite(favorites, {
      serviceCode: service.code,
      countryId: country.id,
    })
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
  return { selected, toggle }
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
  const language = resolveSmsLanguage(i18n.resolvedLanguage, i18n.language)
  const pageVisible = usePageVisibility()
  const {
    country,
    service,
    operator,
    setOperator,
    quantity,
    setQuantity,
    favorites,
    setFavorites,
    batchResult,
    setBatchResult,
    selectService,
    selectCountry,
    selectFavorite,
  } = useSmsSelectionState()
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [batchProgress, setBatchProgress] = useState<{
    completed: number
    total: number
  } | null>(null)
  const queries = useSmsCatalogQueries(country, service, operator, pageVisible)

  const { serviceMap, countryMap } = useSmsCatalogMaps(
    queries.services.data,
    queries.allCountries.data
  )
  const selectedService = serviceMap.get(service)
  const selectedCountry = countryMap.get(Number(country))
  const currentOrders = queries.current.data ?? []
  const historyOrders = useSmsHistoryOrders(queries.history.data?.items)

  const effectiveQuantity = resolveSmsQuantity(quantity, queries.offer.data)
  const invalidate = useCallback(async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: smsKeys.current }),
      queryClient.invalidateQueries({ queryKey: smsKeys.currentList }),
      queryClient.invalidateQueries({ queryKey: smsKeys.history }),
      queryClient.invalidateQueries({ queryKey: ['user'] }),
    ])
  }, [queryClient])
  const purchaseMutation = useSmsPurchaseMutation({
    offer: queries.offer.data,
    quantity: effectiveQuantity,
    country,
    service,
    operator,
    t,
    invalidate,
    refetchOffer: queries.offer.refetch,
    setConfirmOpen,
    setBatchProgress,
    setBatchResult,
  })
  const refreshMutation = useMutation({
    mutationFn: (orderId: string) => refreshHeroSmsSmsOrder(orderId),
    onSuccess: invalidate,
    onError: (error) => toast.error(t(parseHeroSmsError(error).message)),
  })
  const cancelMutation = useMutation({
    mutationFn: (orderId: string) => cancelHeroSmsSmsOrder(orderId),
    onSuccess: async () => {
      toast.success(t('Phone activation cancelled and refunded'))
      await invalidate()
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
  const reconciliation = useSmsPurchaseReconciliation({
    result: batchResult,
    setResult: setBatchResult,
    invalidate,
    refetchOffer: queries.offer.refetch,
    t,
  })

  const view = createSmsPanelView({
    effectiveQuantity,
    offer: queries.offer.data,
    purchasePending: purchaseMutation.isPending,
    batchResult,
    selectedCountry,
    country,
    language,
    historyError: queries.history.error,
    t,
  })

  return (
    <div className='space-y-6'>
      <div className='grid gap-4 xl:grid-cols-[minmax(0,430px)_minmax(0,1fr)]'>
        <SmsPurchaseCard
          language={language}
          services={queries.services.data ?? []}
          countries={queries.countries.data ?? []}
          favoriteCountries={queries.allCountries.data ?? []}
          servicesState={{
            isPending: queries.services.isPending,
            isError: queries.services.isError,
            onRetry: () => void queries.services.refetch(),
          }}
          countriesState={{
            isPending: queries.countries.isPending,
            isError: queries.countries.isError,
            onRetry: () => void queries.countries.refetch(),
          }}
          favorites={favorites}
          service={service}
          country={country}
          operator={operator}
          quantity={view.effectiveQuantity}
          selectedService={selectedService}
          selectedCountry={selectedCountry}
          selectedIsFavorite={favoriteController.selected}
          offer={queries.offer.data}
          offerIsFetching={queries.offer.isFetching}
          offerIsError={queries.offer.isError}
          offerError={queries.offer.error}
          batchProgress={batchProgress}
          batchResult={batchResult}
          batchFeedback={view.batchFeedback}
          canPurchase={view.canPurchase}
          reconciliationPending={
            reconciliation.pending || queries.current.isFetching
          }
          onServiceChange={selectService}
          onCountryChange={selectCountry}
          onOperatorChange={setOperator}
          onQuantityChange={setQuantity}
          onSelectFavorite={selectFavorite}
          onToggleFavorite={favoriteController.toggle}
          onRefreshOffer={() => void queries.offer.refetch()}
          onReconcile={() => void reconciliation.run()}
          onPurchase={() => setConfirmOpen(true)}
        />
        <SmsActiveOrdersCard
          orders={currentOrders}
          countries={countryMap}
          services={serviceMap}
          language={language}
          isPending={queries.current.isPending}
          isError={queries.current.isError}
          errorTitle={t('Unable to load current phone activation')}
          errorDescription={t(parseHeroSmsError(queries.current.error).message)}
          onRetry={() => void queries.current.refetch()}
          refresh={{
            pendingOrderId: refreshMutation.isPending
              ? refreshMutation.variables
              : undefined,
            onOrder: (orderId) => refreshMutation.mutate(orderId),
          }}
          cancel={{
            pendingOrderId: cancelMutation.isPending
              ? cancelMutation.variables
              : undefined,
            onOrder: (orderId) => cancelMutation.mutate(orderId),
          }}
        />
      </div>
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
      />
      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t('Confirm phone activation purchase')}
        desc={t(
          'Quantity: {{quantity}} · Service: {{service}} · Country: {{country}} · Total: {{price}} of platform balance.',
          {
            quantity: view.effectiveQuantity,
            service: selectedService?.name ?? service,
            country: view.selectedCountryName,
            price: formatHeroSmsUSD(view.totalPrice),
          }
        )}
        confirmText={t('Confirm purchase')}
        handleConfirm={() => purchaseMutation.mutate()}
        isLoading={purchaseMutation.isPending}
      />
    </div>
  )
}
