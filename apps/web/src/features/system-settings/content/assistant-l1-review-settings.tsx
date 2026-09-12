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
import { useQuery } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { useFormContext, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

import {
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { safeNumberFieldProps } from '../utils/numeric-field'
import type { AssistantSettingsFormValues } from './assistant-settings-schema'

export function AssistantL1ReviewSettings({
  groups,
  groupsLoading,
  getModels,
}: {
  groups: string[]
  groupsLoading: boolean
  getModels: (group: string) => Promise<string[]>
}) {
  const { t } = useTranslation()
  const form = useFormContext<AssistantSettingsFormValues>()
  const group = useWatch({
    control: form.control,
    name: 'AssistantL1AutoReviewGroup',
  })
  const selectedModel = useWatch({
    control: form.control,
    name: 'AssistantL1AutoReviewModel',
  })
  const modelsQuery = useQuery({
    queryKey: ['assistant-routing-models', group],
    queryFn: () => getModels(group),
    enabled: false,
    retry: false,
    staleTime: 60_000,
  })
  const models = modelsQuery.data ?? []
  const modelOptions = [...new Set([...models, selectedModel].filter(Boolean))]
  const groupOptions = [...new Set([...groups, group].filter(Boolean))]

  return (
    <div
      className='grid gap-5 border-t pt-6'
      data-testid='assistant-l1-review-settings'
    >
      <div>
        <h3 className='text-sm font-medium'>{t('L1 application review')}</h3>
        <p className='text-muted-foreground mt-1 text-sm'>
          {t(
            'Review new L1 applications in the background. Failed, uncertain or invalid reviews stay in the manual queue.'
          )}
        </p>
      </div>
      <FormField
        control={form.control}
        name='AssistantL1AutoReviewEnabled'
        render={({ field }) => (
          <SettingsSwitchItem>
            <SettingsSwitchContent>
              <FormLabel>{t('Enable automatic L1 review')}</FormLabel>
              <FormDescription>
                {t(
                  'Uses its own model and instructions, independently of chat and scheduled reviews. Only L0 to L1 access can be approved.'
                )}
              </FormDescription>
            </SettingsSwitchContent>
            <FormControl>
              <Switch checked={field.value} onCheckedChange={field.onChange} />
            </FormControl>
            <FormMessage />
          </SettingsSwitchItem>
        )}
      />
      <div className='grid gap-5 sm:grid-cols-2'>
        <FormField
          control={form.control}
          name='AssistantL1AutoReviewGroup'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('L1 review routing group')}</FormLabel>
              <div className='flex flex-col gap-2 sm:flex-row sm:items-center'>
                <Select
                  value={field.value}
                  onValueChange={(value) => {
                    if (
                      typeof value !== 'string' ||
                      !value.trim() ||
                      value === field.value
                    ) {
                      return
                    }
                    field.onChange(value)
                    form.setValue('AssistantL1AutoReviewModel', '', {
                      shouldDirty: true,
                      shouldValidate: true,
                    })
                  }}
                >
                  <FormControl>
                    <SelectTrigger
                      className='w-full sm:flex-1'
                      disabled={groupsLoading}
                    >
                      <SelectValue placeholder={t('Select a group')} />
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      {groupOptions.map((value) => (
                        <SelectItem key={value} value={value}>
                          {value}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
                <Button
                  type='button'
                  variant='outline'
                  className='w-full sm:w-auto'
                  data-testid='assistant-l1-get-model-list'
                  disabled={!group || modelsQuery.isFetching}
                  onClick={() => {
                    void modelsQuery.refetch()
                  }}
                >
                  <RefreshCw
                    data-icon='inline-start'
                    className={
                      modelsQuery.isFetching ? 'animate-spin' : undefined
                    }
                  />
                  <span>{t('Get model list')}</span>
                </Button>
              </div>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name='AssistantL1AutoReviewModel'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('L1 review model')}</FormLabel>
              <Select
                value={field.value}
                onValueChange={(value) => {
                  if (typeof value === 'string' && value.trim()) {
                    field.onChange(value)
                  }
                }}
              >
                <FormControl>
                  <SelectTrigger
                    className='w-full'
                    disabled={
                      modelsQuery.data === undefined ||
                      modelsQuery.isFetching ||
                      modelsQuery.isError ||
                      models.length === 0
                    }
                  >
                    <SelectValue placeholder={t('Select a model ID')} />
                  </SelectTrigger>
                </FormControl>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    {modelOptions.map((value) => (
                      <SelectItem key={value} value={value}>
                        {value}
                        {modelsQuery.data !== undefined &&
                        !models.includes(value)
                          ? ` · ${t('not enabled')}`
                          : null}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
              <FormDescription>
                {modelsQuery.isError
                  ? t('Could not load review models. Try again.')
                  : modelsQuery.isFetching
                    ? t('Loading model list...')
                    : modelsQuery.data !== undefined && models.length === 0
                      ? t('This group has no enabled model IDs.')
                      : t(
                          'Choose a group, then click Get model list to load its enabled model IDs.'
                        )}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name='AssistantL1AutoReviewMinConfidence'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Minimum review confidence')}</FormLabel>
              <FormControl>
                <Input
                  type='number'
                  min={0}
                  max={1}
                  step={0.01}
                  {...safeNumberFieldProps(field)}
                />
              </FormControl>
              <FormDescription>
                {t(
                  'Use a value from 0 to 1. Results below this threshold require manual review.'
                )}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name='AssistantL1AutoApprovalUserIDs'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Trial user IDs (optional)')}</FormLabel>
              <FormControl>
                <Input {...field} maxLength={4000} />
              </FormControl>
              <FormDescription>
                {t(
                  'Leave blank to review all new applications when enabled. Enter comma-separated user IDs to limit the rollout.'
                )}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
      </div>
      <FormField
        control={form.control}
        name='AssistantL1AutoReviewPrompt'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('L1 review instructions')}</FormLabel>
            <FormControl>
              <Textarea {...field} rows={6} maxLength={8000} />
            </FormControl>
            <FormDescription>
              {t(
                'Describe the evidence needed for approval. The agent replies to the applicant in their language; safety rules and the required output format cannot be overridden.'
              )}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
    </div>
  )
}
