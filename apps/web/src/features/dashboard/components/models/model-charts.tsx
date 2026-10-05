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
import { PieChart as PieChartIcon } from 'lucide-react'
import { useEffect, useMemo, useState, useRef } from 'react'
import { useTranslation } from 'react-i18next'

import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { IconBadge } from '@/components/ui/icon-badge'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useThemeCustomization } from '@/context/theme-customization-provider'
import { useTheme } from '@/context/theme-provider'
import {
  DEFAULT_TIME_GRANULARITY,
  MODEL_ANALYTICS_CHART_OPTIONS,
} from '@/features/dashboard/constants'
import { processChartData } from '@/features/dashboard/lib'
import type {
  ModelAnalyticsChartTab,
  QuotaDataItem,
} from '@/features/dashboard/types'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'
import { getCurrencyFormattingLocale } from '@/lib/currency'
import { useThemeRadiusPx } from '@/lib/theme-radius'
import type { TimeGranularity } from '@/lib/time'
import { VCHART_OPTION } from '@/lib/vchart'

let themeManagerPromise: Promise<
  (typeof import('@visactor/vchart'))['ThemeManager']
> | null = null

type ChartSpecKey = 'spec_model_line' | 'spec_pie' | 'spec_rank_bar'

const CHART_SPEC_KEYS: Record<ModelAnalyticsChartTab, ChartSpecKey> = {
  trend: 'spec_model_line',
  proportion: 'spec_pie',
  top: 'spec_rank_bar',
}

interface ModelChartsProps {
  data: QuotaDataItem[]
  loading?: boolean
  timeGranularity?: TimeGranularity
  defaultChartTab?: ModelAnalyticsChartTab
}

export function ModelCharts(props: ModelChartsProps) {
  const { t, i18n } = useTranslation()
  const {
    currency: unit,
    formatQuota,
    quotaToAmount,
    formatAmount,
  } = useWalletCurrency()
  const locale = getCurrencyFormattingLocale(
    i18n.resolvedLanguage || i18n.language
  )
  const currency = useMemo(
    () => ({
      formatQuota,
      quotaToAmount,
      formatAmount,
    }),
    [formatQuota, quotaToAmount, formatAmount]
  )
  const { resolvedTheme } = useTheme()
  const { customization } = useThemeCustomization()
  const chartRadius = useThemeRadiusPx(
    '--radius-md',
    `${customization.preset}:${customization.radius}`
  )
  const [activeTab, setActiveTab] = useState<ModelAnalyticsChartTab>(
    props.defaultChartTab ?? 'trend'
  )
  const [themeReady, setThemeReady] = useState(false)
  const themeManagerRef = useRef<
    (typeof import('@visactor/vchart'))['ThemeManager'] | null
  >(null)
  const timeGranularity = props.timeGranularity ?? DEFAULT_TIME_GRANULARITY

  useEffect(() => {
    if (props.defaultChartTab) setActiveTab(props.defaultChartTab)
  }, [props.defaultChartTab])

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

  const spec = chartData[CHART_SPEC_KEYS[activeTab]]
  const specType = typeof spec?.type === 'string' ? spec.type : activeTab
  const chartKey = [
    activeTab,
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
          <IconBadge tone='chart-4' size='sm'>
            <PieChartIcon />
          </IconBadge>
          <div className='text-sm font-semibold'>
            {t('Model Call Analytics')}
          </div>
          <span className='text-muted-foreground text-xs'>
            {t('Total:')} {chartData.totalCountDisplay}
          </span>
        </div>

        <Tabs
          value={activeTab}
          onValueChange={(value) =>
            setActiveTab(value as ModelAnalyticsChartTab)
          }
        >
          <TabsList className='h-auto min-h-8 w-full max-w-full flex-wrap justify-start sm:h-8 sm:w-auto sm:max-w-none sm:flex-nowrap'>
            {MODEL_ANALYTICS_CHART_OPTIONS.map((tab) => (
              <TabsTrigger
                key={tab.value}
                value={tab.value}
                className='shrink-0'
              >
                {t(tab.labelKey)}
              </TabsTrigger>
            ))}
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
