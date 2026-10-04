/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useFormContext, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import {
  FormControl,
  FormDescription,
  FormField,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Switch } from '@/components/ui/switch'

import {
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { ModerationRouteFields } from '../security/moderation-route-fields'
import type { AssistantSettingsFormValues } from './assistant-settings-schema'

export function AssistantModerationSettings({ groups }: { groups: string[] }) {
  const { t } = useTranslation()
  const form = useFormContext<AssistantSettingsFormValues>()
  const [enabled, group, model] = useWatch({
    control: form.control,
    name: [
      'AssistantModerationEnabled',
      'AssistantModerationGroup',
      'AssistantModerationModel',
    ],
  })
  return (
    <div className='space-y-6' data-testid='assistant-moderation-settings'>
      <FormField
        control={form.control}
        name='AssistantModerationEnabled'
        render={({ field }) => (
          <SettingsSwitchItem>
            <SettingsSwitchContent>
              <FormLabel>{t('Enable assistant moderation')}</FormLabel>
              <FormDescription>
                {t(
                  'Review assistant conversations with OpenAI Moderation in the background. Disabled by default.'
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
      <ModerationRouteFields
        group={group}
        model={model}
        groups={groups}
        idPrefix='assistant-moderation'
        disabled={!enabled}
        onGroupChange={(value) =>
          form.setValue('AssistantModerationGroup', value, {
            shouldDirty: true,
            shouldValidate: true,
          })
        }
        onModelChange={(value) =>
          form.setValue(
            'AssistantModerationModel',
            value as AssistantSettingsFormValues['AssistantModerationModel'],
            { shouldDirty: true, shouldValidate: true }
          )
        }
      />
      <p className='text-muted-foreground text-sm'>
        {t(
          'User group policies determine whether review is off, warning-only, or strict. Groups without a policy stay off.'
        )}
      </p>
      <a
        href='/system-settings/security/moderation'
        className='text-primary text-sm underline underline-offset-4'
      >
        {t('Configure group modes and category fines')}
      </a>
    </div>
  )
}
