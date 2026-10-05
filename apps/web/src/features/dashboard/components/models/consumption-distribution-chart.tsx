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
import { VChart } from '@visactor/react-vchart'
import { WalletCards } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { IconBadge } from '@/components/ui/icon-badge'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useThemeCustomization } from '@/context/theme-customization-provider'
import { useTheme } from '@/context/theme-provider'
import {
  CONSUMPTION_DISTRIBUTION_CHART_OPTIONS,
  DEFAULT_TIME_GRANULARITY,
} from '@/features/dashboard/constants'
import { processChartData } from '@/features/dashboard/lib'
import type {
  ConsumptionDistributionChartType,
  QuotaDataItem,
} from '@/features/dashboard/types'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'
import {
  formatFiatCurrencyAmount,
  getCurrencyFormattingLocale,
} from '@/lib/currency'
import { useThemeRadiusPx } from '@/lib/theme-radius'
import type { TimeGranularity } from '@/lib/time'
import { VCHART_OPTION } from '@/lib/vchart'

let themeManagerPromise: Promise<
  (typeof import('@visactor/vchart'))['ThemeManager']
> | null = null

interface ConsumptionDistributionChartProps {
  data: QuotaDataItem[]
  loading?: boolean
  timeGranularity?: TimeGranularity
  defaultChartType?: ConsumptionDistributionChartType
}

export function ConsumptionDistributionChart(
  props: ConsumptionDistributionChartProps
) {
  const { t, i18n } = useTranslation()
  const { currency: unit, formatQuota, quotaToAmount } = useWalletCurrency()
  const locale = getCurrencyFormattingLocale(
    i18n.resolvedLanguage || i18n.language
  )
  const currency = useMemo(
    () => ({
      formatQuota,
      quotaToAmount,
      formatAmount: (
        amount: number,
        options?: import('@/lib/currency').CurrencyFormatOptions
      ) =>
        unit === 'CREDIT'
          ? formatQuota(Math.round(amount), options)
          : formatFiatCurrencyAmount(amount, unit, { ...options, locale }),
    }),
    [formatQuota, quotaToAmount, unit, locale]
  )
  const { resolvedTheme } = useTheme()
  const { customization } = useThemeCustomization()
  const chartRadius = useThemeRadiusPx(
    '--radius-md',
    `${customization.preset}:${customization.radius}`
  )
  const [chartType, setChartType] = useState<ConsumptionDistributionChartType>(
    props.defaultChartType ?? 'bar'
  )
  const [themeReady, setThemeReady] = useState(false)
  const themeManagerRef = useRef<
    (typeof import('@visactor/vchart'))['ThemeManager'] | null
  >(null)
  const timeGranularity = props.timeGranularity ?? DEFAULT_TIME_GRANULARITY

  useEffect(() => {
    if (props.defaultChartType) setChartType(props.defaultChartType)
  }, [props.defaultChartType])

  useEffect(() => {
    const updateTheme = async () => {
      setThemeReady(false)

      if (!themeManagerPromise) {
        themeManagerPromise = import('@visactor/vchart').then(
          (m) => m.ThemeManager
        )
      }

      const ThemeManager = await themeManagerPromise
      themeManagerRef.current = ThemeManager
      ThemeManager.setCurrentTheme(resolvedTheme === 'dark' ? 'dark' : 'light')
      setThemeReady(true)
    }

    updateTheme()
  }, [resolvedTheme])

  const chartData = useMemo(
    () =>
      processChartData(
        props.loading ? [] : props.data,
        timeGranularity,
        t,
        chartRadius,
        currency
      ),
    [props.data, props.loading, timeGranularity, t, chartRadius, currency]
  )
  const spec = chartType === 'bar' ? chartData.spec_line : chartData.spec_area
  const specType = typeof spec?.type === 'string' ? spec.type : chartType
  const chartKey = [
    chartType,
    specType,
    props.loading ? 'loading' : 'ready',
    props.data.length,
    resolvedTheme,
    unit,
    locale,
    quotaToAmount(1),
    customization.preset,
  ].join('-')

  return (
    <Card className='gap-0 py-0'>
      <CardHeader className='flex w-full flex-col gap-3 border-b px-4 py-3 sm:px-5 lg:flex-row lg:items-center lg:justify-between'>
        <div className='flex items-center gap-2'>
          <IconBadge tone='success' size='sm'>
            <WalletCards />
          </IconBadge>
          <div className='text-sm font-semibold'>{t('Quota Distribution')}</div>
          <span className='text-muted-foreground text-xs'>
            {t('Total:')} {chartData.totalQuotaDisplay}
          </span>
        </div>

        <Tabs
          value={chartType}
          onValueChange={(value) =>
            setChartType(value as ConsumptionDistributionChartType)
          }
        >
          <TabsList className='h-8 w-full sm:w-auto'>
            {CONSUMPTION_DISTRIBUTION_CHART_OPTIONS.map((item) => {
              return (
                <TabsTrigger key={item.value} value={item.value}>
                  {t(item.labelKey)}
                </TabsTrigger>
              )
            })}
          </TabsList>
        </Tabs>
      </CardHeader>

      <CardContent className='h-[300px] p-2 sm:h-96 sm:p-3'>
        {themeReady && spec && (
          <VChart
            key={chartKey}
            spec={{
              ...spec,
              theme: resolvedTheme === 'dark' ? 'dark' : 'light',
              background: 'transparent',
            }}
            option={VCHART_OPTION}
          />
        )}
      </CardContent>
    </Card>
  )
}
