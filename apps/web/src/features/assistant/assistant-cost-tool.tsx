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
  Calculator01Icon,
  ReloadIcon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useQuery } from '@tanstack/react-query'
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
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { formatPlatformAmount } from '@/lib/currency'

import { getAssistantPricing } from './api'
import { calculateAssistantTextCost } from './cost-calculator'

function parseTokenCount(value: string): number {
  const parsed = Number(value)
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : Number.NaN
}

export function AssistantCostTool(props: { developerAccessGranted: boolean }) {
  const { t } = useTranslation()
  const [modelName, setModelName] = useState('')
  const [group, setGroup] = useState('')
  const [inputTokens, setInputTokens] = useState('100000')
  const [outputTokens, setOutputTokens] = useState('10000')
  const pricingQuery = useQuery({
    queryKey: ['assistant-pricing'],
    queryFn: getAssistantPricing,
    enabled: props.developerAccessGranted,
    staleTime: 5 * 60 * 1000,
    retry: false,
  })

  const models = useMemo(
    () =>
      (pricingQuery.data?.data ?? [])
        .filter(
          (model) =>
            model.quota_type === 0 && model.billing_mode !== 'tiered_expr'
        )
        .sort((left, right) => left.model_name.localeCompare(right.model_name)),
    [pricingQuery.data?.data]
  )
  const selectedModel =
    models.find((model) => model.model_name === modelName) ?? models[0]
  const groups = useMemo(() => {
    if (!selectedModel || !pricingQuery.data) return []
    const usableGroups = pricingQuery.data.usable_group
    const enabledGroups = selectedModel.enable_groups.includes('all')
      ? Object.keys(usableGroups)
      : selectedModel.enable_groups
    return enabledGroups
      .filter((name) => usableGroups[name])
      .sort((left, right) => left.localeCompare(right))
  }, [pricingQuery.data, selectedModel])
  const selectedGroup = groups.includes(group) ? group : (groups[0] ?? '')
  const groupRatio = selectedGroup
    ? (pricingQuery.data?.group_ratio[selectedGroup] ?? 1)
    : 1
  const estimate = selectedModel
    ? calculateAssistantTextCost(
        selectedModel,
        groupRatio,
        parseTokenCount(inputTokens),
        parseTokenCount(outputTokens)
      )
    : null
  const formatCost = (amount: number) =>
    formatPlatformAmount(
      amount,
      {
        abbreviate: false,
        digitsLarge: 4,
        digitsSmall: 6,
      },
      t('Platform')
    )

  let calculatorContent: ReactNode
  if (!props.developerAccessGranted) {
    calculatorContent = (
      <Alert>
        <HugeiconsIcon icon={Alert02Icon} strokeWidth={2} aria-hidden='true' />
        <AlertTitle>{t('Read-only')}</AlertTitle>
        <AlertDescription>
          {t(
            'This live estimate is read-only while your L1 request is under review.'
          )}
        </AlertDescription>
      </Alert>
    )
  } else if (pricingQuery.isLoading) {
    calculatorContent = (
      <div className='grid gap-3' aria-label={t('Loading...')}>
        <Skeleton className='h-9 w-full' />
        <Skeleton className='h-9 w-full' />
        <div className='grid grid-cols-2 gap-3'>
          <Skeleton className='h-9 w-full' />
          <Skeleton className='h-9 w-full' />
        </div>
        <Skeleton className='h-20 w-full' />
      </div>
    )
  } else if (pricingQuery.isError) {
    calculatorContent = (
      <Alert variant='destructive'>
        <HugeiconsIcon icon={Alert02Icon} strokeWidth={2} aria-hidden='true' />
        <AlertTitle>{t('Unable to load live pricing')}</AlertTitle>
        <AlertDescription>
          {t('Live prices are unavailable, so no estimate is shown.')}
        </AlertDescription>
        <AlertAction>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => void pricingQuery.refetch()}
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
  } else if (!selectedModel) {
    calculatorContent = (
      <Empty className='min-h-36 border'>
        <EmptyHeader>
          <EmptyMedia variant='icon'>
            <HugeiconsIcon
              icon={Calculator01Icon}
              strokeWidth={2}
              aria-hidden='true'
            />
          </EmptyMedia>
          <EmptyTitle>{t('No text-token pricing is available')}</EmptyTitle>
          <EmptyDescription>
            {t(
              'This calculator supports models with fixed input and output token rates.'
            )}
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  } else {
    calculatorContent = (
      <>
        <div className='grid gap-1.5'>
          <Label htmlFor='assistant-cost-model'>{t('Model')}</Label>
          <NativeSelect
            className='w-full'
            id='assistant-cost-model'
            value={selectedModel.model_name}
            onChange={(event) => {
              setModelName(event.target.value)
              setGroup('')
            }}
          >
            {models.map((model) => (
              <NativeSelectOption
                key={model.model_name}
                value={model.model_name}
              >
                {model.model_name}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
        <div className='grid gap-1.5'>
          <Label htmlFor='assistant-cost-group'>{t('Group')}</Label>
          <NativeSelect
            className='w-full'
            id='assistant-cost-group'
            value={selectedGroup}
            disabled={groups.length === 0}
            onChange={(event) => setGroup(event.target.value)}
          >
            {groups.map((name) => (
              <NativeSelectOption key={name} value={name}>
                {pricingQuery.data?.usable_group[name]?.desc || name}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
        <div className='grid grid-cols-2 gap-3'>
          <div className='grid gap-1.5'>
            <Label htmlFor='assistant-input-tokens'>{t('Input tokens')}</Label>
            <Input
              id='assistant-input-tokens'
              type='number'
              min={0}
              step={1000}
              inputMode='numeric'
              value={inputTokens}
              onChange={(event) => setInputTokens(event.target.value)}
            />
          </div>
          <div className='grid gap-1.5'>
            <Label htmlFor='assistant-output-tokens'>
              {t('Output tokens')}
            </Label>
            <Input
              id='assistant-output-tokens'
              type='number'
              min={0}
              step={1000}
              inputMode='numeric'
              value={outputTokens}
              onChange={(event) => setOutputTokens(event.target.value)}
            />
          </div>
        </div>
        {estimate ? (
          <div className='bg-muted/50 grid gap-2 rounded-lg border p-3'>
            <div className='flex items-center justify-between gap-3'>
              <span className='text-muted-foreground text-xs'>
                {t('Estimated text cost')}
              </span>
              <strong className='text-base'>
                {formatCost(estimate.totalUSD)}
              </strong>
            </div>
            <div className='flex flex-wrap gap-2'>
              <Badge variant='outline'>
                {t('Input {{amount}} / 1M', {
                  amount: formatCost(estimate.inputRatePerMillionUSD),
                })}
              </Badge>
              <Badge variant='outline'>
                {t('Output {{amount}} / 1M', {
                  amount: formatCost(estimate.outputRatePerMillionUSD),
                })}
              </Badge>
            </div>
          </div>
        ) : (
          <p className='text-destructive text-sm'>
            {t('Enter valid token counts to calculate the estimate.')}
          </p>
        )}
      </>
    )
  }

  return (
    <Card size='sm'>
      <CardHeader>
        <CardTitle className='flex items-center gap-2'>
          <HugeiconsIcon
            icon={Calculator01Icon}
            className='size-4'
            strokeWidth={2}
            aria-hidden='true'
          />
          {t('Live token cost calculator')}
        </CardTitle>
        <CardDescription>
          {t(
            'Uses current server pricing. Images, audio, tools, and cache may add separate charges.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='grid gap-3'>
        {!props.developerAccessGranted ? (
          <Alert>
            <HugeiconsIcon
              icon={Alert02Icon}
              strokeWidth={2}
              aria-hidden='true'
            />
            <AlertTitle>{t('Read-only')}</AlertTitle>
            <AlertDescription>
              {t(
                'This live estimate is read-only while your L1 request is under review.'
              )}
            </AlertDescription>
          </Alert>
        ) : null}
        {calculatorContent}
      </CardContent>
    </Card>
  )
}
