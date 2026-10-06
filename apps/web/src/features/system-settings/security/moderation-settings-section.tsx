/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'

import { getSystemGroups } from '../api'
import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOptions } from '../hooks/use-update-option'
import { ModerationGroupPolicyEditor } from './moderation-group-policy-editor'
import { ModerationRouteFields } from './moderation-route-fields'
import {
  moderationSettingsSchema,
  type ModerationSettingsFormValues,
} from './moderation-settings-schema'
import { SecurityAuditPanel } from './security-audit'

function rebaseModerationDraft(
  previous: ModerationSettingsFormValues,
  draft: ModerationSettingsFormValues,
  incoming: ModerationSettingsFormValues
): ModerationSettingsFormValues {
  return Object.fromEntries(
    Object.entries(incoming).map(([key, value]) => [
      key,
      draft[key as keyof ModerationSettingsFormValues] !==
      previous[key as keyof ModerationSettingsFormValues]
        ? draft[key as keyof ModerationSettingsFormValues]
        : value,
    ])
  ) as ModerationSettingsFormValues
}

export function ModerationSettingsSection({
  defaultValues,
}: {
  defaultValues: ModerationSettingsFormValues
}) {
  const { t } = useTranslation()
  const updateOptions = useUpdateOptions()
  const baseline = useRef(defaultValues)
  const [pricesValid, setPricesValid] = useState(true)
  const form = useForm<ModerationSettingsFormValues>({
    resolver: zodResolver(moderationSettingsSchema),
    defaultValues,
  })

  useEffect(() => {
    const draft = rebaseModerationDraft(
      baseline.current,
      form.getValues(),
      defaultValues
    )
    baseline.current = defaultValues
    form.reset(defaultValues)
    form.reset(draft, { keepDefaultValues: true })
  }, [defaultValues, form])
  const [enabled, group, model] = useWatch({
    control: form.control,
    name: ['ModerationEnabled', 'ModerationGroup', 'ModerationModel'],
  })
  const groupsQuery = useQuery({
    queryKey: ['groups'],
    queryFn: getSystemGroups,
    staleTime: 60_000,
  })
  const groups = groupsQuery.data?.data ?? []
  const onSubmit = async (values: ModerationSettingsFormValues) => {
    if (!pricesValid) return
    const updates = Object.fromEntries(
      Object.entries(values)
        .filter(
          ([key, value]) =>
            value !==
            baseline.current[key as keyof ModerationSettingsFormValues]
        )
        .map(([key, value]) => [key, String(value)])
    )
    if (!Object.keys(updates).length) return
    try {
      const before = baseline.current
      const response = await updateOptions.mutateAsync(updates)
      if (response.success) {
        const saved = baseline.current !== before ? baseline.current : values
        const draft = rebaseModerationDraft(values, form.getValues(), saved)
        baseline.current = saved
        form.reset(saved)
        form.reset(draft, { keepDefaultValues: true })
      }
    } catch {
      // The mutation hook reports errors. Keep the editable draft intact.
    }
  }
  return (
    <SettingsSection title={t('Content safety review')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            onReset={() => form.reset(baseline.current)}
            isSaveDisabled={!form.formState.isDirty || !pricesValid}
            isResetDisabled={!form.formState.isDirty}
            isSaving={updateOptions.isPending}
            saveLabel='Save moderation settings'
          />
          <p className='text-muted-foreground text-sm lg:col-span-2'>
            {t(
              'OpenAI Moderation reviews text in the background. Requests and assistant replies never wait for review. Notifications stay on this site; review failures do not trigger penalties.'
            )}
          </p>
          <FormField
            control={form.control}
            name='ModerationEnabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enable API moderation')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Queue API text for safety review. Disabled by default; assistant review is configured separately.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
                <FormMessage />
              </SettingsSwitchItem>
            )}
          />
          <FormField
            control={form.control}
            name='ModerationPolicyScope'
            render={({ field }) => (
              <FormItem className='lg:col-span-2'>
                <FormLabel>{t('API review policy scope')}</FormLabel>
                <Select
                  value={field.value}
                  onValueChange={field.onChange}
                  disabled={updateOptions.isPending}
                >
                  <FormControl>
                    <SelectTrigger>
                      <SelectValue>
                        {t(
                          field.value === 'request_group'
                            ? 'Request group'
                            : 'Account group'
                        )}
                      </SelectValue>
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      <SelectItem value='account_group'>
                        {t('Account group')}
                      </SelectItem>
                      <SelectItem value='request_group'>
                        {t('Request group')}
                      </SelectItem>
                    </SelectGroup>
                  </SelectContent>
                </Select>
                <FormDescription>
                  {t(
                    'Account group uses the user’s account group. Request group uses the trusted group selected for the API request; a missing request group stays off. Changing scope cancels queued API reviews. Assistant reviews always use account groups with the same policy map.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='ModerationSafetyIdentifierEnabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>
                    {t('Enable private upstream safety identifiers')}
                  </FormLabel>
                  <FormDescription>
                    {t(
                      'Send a stable private user identifier with supported official OpenAI requests when review is enabled for the selected group policy. Raw user IDs and email addresses are not sent. Disabled by default.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                    disabled={updateOptions.isPending}
                  />
                </FormControl>
                <FormMessage />
              </SettingsSwitchItem>
            )}
          />
          <ModerationRouteFields
            group={group}
            model={model}
            groups={groups}
            idPrefix='api-moderation'
            disabled={!enabled || updateOptions.isPending}
            onGroupChange={(value) =>
              form.setValue('ModerationGroup', value, {
                shouldDirty: true,
                shouldValidate: true,
              })
            }
            onModelChange={(value) =>
              form.setValue(
                'ModerationModel',
                value as ModerationSettingsFormValues['ModerationModel'],
                { shouldDirty: true, shouldValidate: true }
              )
            }
          />
          <FormField
            control={form.control}
            name='ModerationGroupPolicies'
            render={({ field }) => (
              <FormItem className='lg:col-span-2'>
                <FormLabel>{t('Group review policies')}</FormLabel>
                <ModerationGroupPolicyEditor
                  value={field.value}
                  onValidityChange={setPricesValid}
                  groups={groups}
                  onChange={field.onChange}
                  disabled={updateOptions.isPending}
                />
                <FormMessage>
                  {form.formState.errors.ModerationGroupPolicies?.message
                    ? t(
                        'Use explicit groups, valid modes, and category fees from $0 to $1000 with up to six decimal places.'
                      )
                    : null}
                </FormMessage>
              </FormItem>
            )}
          />
          <a
            href='/legal/safety-review.html'
            target='_blank'
            rel='noopener noreferrer'
            className='text-primary text-sm underline underline-offset-4'
          >
            {t('Read the public safety review notice')}
          </a>
          <div className='flex justify-end gap-2 lg:col-span-2'>
            <Button
              type='button'
              variant='outline'
              disabled={!form.formState.isDirty || updateOptions.isPending}
              onClick={() => form.reset(baseline.current)}
            >
              {t('Reset')}
            </Button>
            <Button
              type='submit'
              disabled={!form.formState.isDirty || updateOptions.isPending}
            >
              {t(
                updateOptions.isPending
                  ? 'Saving...'
                  : 'Save moderation settings'
              )}
            </Button>
          </div>
        </SettingsForm>
      </Form>
      <SecurityAuditPanel />
    </SettingsSection>
  )
}
