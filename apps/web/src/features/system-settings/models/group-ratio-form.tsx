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
import { Link } from '@tanstack/react-router'
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
import { Code2, Eye, HelpCircle } from 'lucide-react'
import { memo, useCallback, useMemo, useState, type ReactNode } from 'react'
import type { UseFormReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import {
  sideDrawerContentClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
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
import { Input } from '@/components/ui/input'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'
import { SystemJsonCodeEditor } from '@/features/system-settings/components/system-json-code-editor'

import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageActionsPortal } from '../components/settings-page-context'
import { safeJsonParse } from '../utils/json-parser'
import { safeNumberFieldProps } from '../utils/numeric-field'
import { GroupRatioVisualEditor } from './group-ratio-visual-editor'
import { GroupSpecialUsableRulesEditor } from './group-special-usable-editor'

type GroupFormValues = {
  GroupRatio: string
  TopupGroupRatio: string
  UserUsableGroups: string
  GroupGroupRatio: string
  AutoGroups: string
  MaxTokenAutoGroups: number
  DefaultUseAutoGroup: boolean
  GroupSpecialUsableGroup: string
  GroupWarnings: string
}

type GroupRatioFormProps = {
  form: UseFormReturn<GroupFormValues>
  onSave: (values: GroupFormValues) => Promise<void>
  isSaving: boolean
}

const GROUP_WARNINGS_EXAMPLE =
  '{\n  "free": {\n    "enabled": true,\n    "message": "This group is community-operated. Do not send secrets.",\n    "mode": "modal",\n    "confirmations": 3\n  }\n}'

export const GroupRatioForm = memo(function GroupRatioForm({
  form,
  onSave,
  isSaving,
}: GroupRatioFormProps) {
  const { t } = useTranslation()
  const [editMode, setEditMode] = useState<'visual' | 'json'>('visual')
  const [guideOpen, setGuideOpen] = useState(false)

  const handleFieldChange = useCallback(
    (field: keyof GroupFormValues, value: string) => {
      form.setValue(field, value, {
        shouldValidate: true,
        shouldDirty: true,
      })
    },
    [form]
  )

  const toggleEditMode = useCallback(() => {
    setEditMode((prev) => (prev === 'visual' ? 'json' : 'visual'))
  }, [])

  const watchedGroupRatio = form.watch('GroupRatio')
  const watchedUserUsableGroups = form.watch('UserUsableGroups')
  const watchedTopupGroupRatio = form.watch('TopupGroupRatio')
  const groupNames = useMemo(() => {
    const ratioMap = safeJsonParse<Record<string, number>>(watchedGroupRatio, {
      fallback: {},
      silent: true,
    })
    const usableMap = safeJsonParse<Record<string, string>>(
      watchedUserUsableGroups,
      { fallback: {}, silent: true }
    )
    const topupMap = safeJsonParse<Record<string, number>>(
      watchedTopupGroupRatio,
      { fallback: {}, silent: true }
    )
    return [
      ...new Set([
        ...Object.keys(ratioMap),
        ...Object.keys(usableMap),
        ...Object.keys(topupMap),
      ]),
    ]
  }, [watchedGroupRatio, watchedUserUsableGroups, watchedTopupGroupRatio])

  return (
    <div className='space-y-6'>
      <div className='flex flex-wrap justify-end gap-2'>
        <Button
          variant='outline'
          size='sm'
          render={
            <Link
              to='/system-settings/models/$section'
              params={{ section: 'dynamic-group-multiplier' }}
            />
          }
        >
          {t('Dynamic Profit Pricing')}
        </Button>
        <Button variant='outline' size='sm' onClick={() => setGuideOpen(true)}>
          <HelpCircle className='mr-2 h-4 w-4' />
          {t('Usage guide')}
        </Button>
        <Button variant='outline' size='sm' onClick={toggleEditMode}>
          {editMode === 'visual' ? (
            <>
              <Code2 className='mr-2 h-4 w-4' />
              {t('Switch to JSON')}
            </>
          ) : (
            <>
              <Eye className='mr-2 h-4 w-4' />
              {t('Switch to Visual')}
            </>
          )}
        </Button>
      </div>

      <GroupPricingGuide open={guideOpen} onOpenChange={setGuideOpen} />

      <Form {...form}>
        <SettingsPageActionsPortal>
          <Button
            type='button'
            size='sm'
            onClick={form.handleSubmit(onSave)}
            disabled={isSaving}
          >
            {isSaving ? t('Saving...') : t('Save group pricing')}
          </Button>
        </SettingsPageActionsPortal>
        {editMode === 'visual' ? (
          <div className='space-y-6'>
            <GroupRatioVisualEditor
              groupRatio={form.watch('GroupRatio')}
              topupGroupRatio={form.watch('TopupGroupRatio')}
              userUsableGroups={form.watch('UserUsableGroups')}
              groupGroupRatio={form.watch('GroupGroupRatio')}
              autoGroups={form.watch('AutoGroups')}
              maxTokenAutoGroupsField={
                <FormField
                  control={form.control}
                  name='MaxTokenAutoGroups'
                  render={({ field, fieldState }) => (
                    <FormItem data-invalid={fieldState.invalid}>
                      <FormLabel>
                        {t('Maximum custom groups per token')}
                      </FormLabel>
                      <FormControl>
                        <Input
                          {...safeNumberFieldProps(field)}
                          type='number'
                          min={1}
                          step={1}
                          aria-invalid={fieldState.invalid}
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'Limits only token-specific Auto snapshots. Global Auto inheritance remains unlimited.'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              }
              groupSpecialUsableGroup={form.watch('GroupSpecialUsableGroup')}
              onChange={(field, value) =>
                handleFieldChange(field as keyof GroupFormValues, value)
              }
            />

            <GroupSpecialUsableRulesEditor
              value={form.watch('GroupSpecialUsableGroup')}
              groupOptions={groupNames}
              onChange={(value) =>
                handleFieldChange('GroupSpecialUsableGroup', value)
              }
            />

            <FormField
              control={form.control}
              name='GroupWarnings'
              render={({ field }) => (
                <FormItem>
                  <div className='flex flex-wrap items-center justify-between gap-2'>
                    <FormLabel>{t('Group warnings')}</FormLabel>
                    <Button
                      type='button'
                      variant='ghost'
                      size='sm'
                      onClick={() => field.onChange(GROUP_WARNINGS_EXAMPLE)}
                    >
                      {t('Use recommended preset')}
                    </Button>
                  </div>
                  <FormControl>
                    <SystemJsonCodeEditor
                      configurationKey='group_ratio_setting.group_warnings'
                      value={field.value}
                      onChange={field.onChange}
                      name={field.name}
                      onBlur={field.onBlur}
                      textareaRef={field.ref}
                      heightClassName='h-40 min-h-40 max-h-40'
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Configure per-group warnings as JSON. Use the recommended preset as a safe starting point, then edit each group. Each entry may set enabled, message, mode (modal, banner, or inline), and confirmations from 1 to 3. Modal is the default.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='DefaultUseAutoGroup'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>{t('Default to auto groups')}</FormLabel>
                    <FormDescription>
                      {t(
                        'When enabled, newly created tokens start in the first auto group.'
                      )}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </SettingsSwitchItem>
              )}
            />
          </div>
        ) : (
          <SettingsForm onSubmit={form.handleSubmit(onSave)}>
            <FormField
              control={form.control}
              name='GroupRatio'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Group cost multipliers')}</FormLabel>
                  <FormControl>
                    <SystemJsonCodeEditor
                      configurationKey='GroupRatio'
                      value={field.value}
                      onChange={field.onChange}
                      name={field.name}
                      onBlur={field.onBlur}
                      textareaRef={field.ref}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'JSON map of group → cost multiplier used as the base for that billing group. Dynamic pricing adds the profit multiplier.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='TopupGroupRatio'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Top-up group ratios')}</FormLabel>
                  <FormControl>
                    <SystemJsonCodeEditor
                      configurationKey='TopupGroupRatio'
                      value={field.value}
                      onChange={field.onChange}
                      name={field.name}
                      onBlur={field.onBlur}
                      textareaRef={field.ref}
                      heightClassName='h-40 min-h-40 max-h-40'
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Optional multiplier per user group used when calculating recharge pricing. Provide a JSON object such as'
                    )}
                    {` { "default": 1, "vip": 1.2 }`}.
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='UserUsableGroups'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Selectable groups')}</FormLabel>
                  <FormControl>
                    <SystemJsonCodeEditor
                      configurationKey='UserUsableGroups'
                      value={field.value}
                      onChange={field.onChange}
                      name={field.name}
                      onBlur={field.onBlur}
                      textareaRef={field.ref}
                      heightClassName='h-40 min-h-40 max-h-40'
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'JSON map of group → description exposed when users create API keys.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='GroupGroupRatio'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Inter-group overrides')}</FormLabel>
                  <FormControl>
                    <SystemJsonCodeEditor
                      configurationKey='GroupGroupRatio'
                      value={field.value}
                      onChange={field.onChange}
                      name={field.name}
                      onBlur={field.onBlur}
                      textareaRef={field.ref}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Nested JSON: source group →')}{' '}
                    {`{ targetGroup: costMultiplier }`}{' '}
                    {t(
                      'to override billing when a user in one group uses a token of another group.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='AutoGroups'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Auto assignment order')}</FormLabel>
                  <FormControl>
                    <SystemJsonCodeEditor
                      configurationKey='AutoGroups'
                      value={field.value}
                      onChange={field.onChange}
                      name={field.name}
                      onBlur={field.onBlur}
                      textareaRef={field.ref}
                      heightClassName='h-40 min-h-40 max-h-40'
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'JSON array of group identifiers. When enabled below, new tokens rotate through this list.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='MaxTokenAutoGroups'
              render={({ field, fieldState }) => (
                <FormItem data-invalid={fieldState.invalid}>
                  <FormLabel>{t('Maximum custom groups per token')}</FormLabel>
                  <FormControl>
                    <Input
                      {...safeNumberFieldProps(field)}
                      type='number'
                      min={1}
                      step={1}
                      aria-invalid={fieldState.invalid}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Limits only token-specific Auto snapshots. Global Auto inheritance remains unlimited.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='GroupSpecialUsableGroup'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Special usable group rules')}</FormLabel>
                  <FormControl>
                    <SystemJsonCodeEditor
                      configurationKey='group_ratio_setting.group_special_usable_group'
                      value={field.value}
                      onChange={field.onChange}
                      name={field.name}
                      onBlur={field.onBlur}
                      textareaRef={field.ref}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Nested JSON defining per-group rules for adding (+:), removing (-:), or appending usable groups.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='GroupWarnings'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Group warnings')}</FormLabel>
                  <FormControl>
                    <SystemJsonCodeEditor
                      configurationKey='group_ratio_setting.group_warnings'
                      value={field.value}
                      onChange={field.onChange}
                      name={field.name}
                      onBlur={field.onBlur}
                      textareaRef={field.ref}
                      heightClassName='h-40 min-h-40 max-h-40'
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Configure per-group warnings and acknowledgement count (1–3). Use modal for the most prominent warning.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='DefaultUseAutoGroup'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>{t('Default to auto groups')}</FormLabel>
                    <FormDescription>
                      {t(
                        'When enabled, newly created tokens start in the first auto group.'
                      )}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </SettingsSwitchItem>
              )}
            />
          </SettingsForm>
        )}
      </Form>
    </div>
  )
})

type GroupPricingGuideProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

function GuideCodeBlock({ children }: { children: string }) {
  return (
    <pre className='bg-muted/60 overflow-x-auto rounded-none border px-3 py-2 text-xs leading-6 whitespace-pre-wrap'>
      {children}
    </pre>
  )
}

function GuideStepRow({
  chip,
  children,
}: {
  chip: string
  children: ReactNode
}) {
  return (
    <div className='flex items-start gap-2.5 text-sm leading-6'>
      <span className='bg-muted text-muted-foreground mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full text-xs font-medium'>
        {chip}
      </span>
      <span className='text-muted-foreground min-w-0'>{children}</span>
    </div>
  )
}

function GroupPricingGuide({ open, onOpenChange }: GroupPricingGuideProps) {
  const { t } = useTranslation()

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side='right'
        className={sideDrawerContentClassName('sm:max-w-2xl')}
      >
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>{t('Group pricing usage guide')}</SheetTitle>
          <SheetDescription>
            {t(
              'Understand how user groups, cost multipliers, profit pricing, and special rules work together.'
            )}
          </SheetDescription>
        </SheetHeader>

        <div className={sideDrawerFormClassName('gap-5')}>
          <section className='space-y-2'>
            <h3 className='text-sm font-semibold'>
              {t('The two roles of a group')}
            </h3>
            <div className='text-muted-foreground space-y-2 text-sm leading-6'>
              <p>
                {t(
                  'Every group name in the pricing table can be used in two places: on a user (the user group, assigned by admins) and on a token (the token group, chosen when creating the token). Same name pool, two different jobs.'
                )}
              </p>
              <p>
                <span className='text-foreground font-medium'>
                  {t('Token group')}
                </span>
                {': '}
                {t(
                  'decides which channels are used and which base cost multiplier applies.'
                )}
              </p>
              <p>
                <span className='text-foreground font-medium'>
                  {t('User group')}
                </span>
                {': '}
                {t(
                  'decides the top-up ratio, which groups the user can pick for tokens, and whether a cost override applies.'
                )}
              </p>
            </div>
          </section>

          <section className='space-y-2'>
            <h3 className='text-sm font-semibold'>
              {t('How a call is priced')}
            </h3>
            <ol className='text-muted-foreground list-decimal space-y-2 pl-5 text-sm leading-6'>
              <li>
                <span className='text-foreground font-medium'>
                  {t('Find the billing group.')}
                </span>{' '}
                {t(
                  'Use the group set on the token. If the token has no group, use the user group. The auto group tries the auto assignment order from top to bottom.'
                )}
              </li>
              <li>
                <span className='text-foreground font-medium'>
                  {t('Find the cost multiplier.')}
                </span>{' '}
                {t(
                  'Look for a special cost rule matching this user group and this billing group. If one exists, use its cost multiplier. Otherwise use the billing group base cost from the pricing table.'
                )}
              </li>
              <li>
                <span className='text-foreground font-medium'>
                  {t('Charge.')}
                </span>{' '}
                {t(
                  'Final charge = model base cost × group cost multiplier × dynamic profit multiplier.'
                )}
              </li>
            </ol>
            <p className='text-muted-foreground text-sm leading-6'>
              {t(
                'The group value is a cost basis, not a personal discount. Dynamic pricing supplies the profit multiplier separately.'
              )}
            </p>
          </section>

          <section className='space-y-3'>
            <h3 className='text-sm font-semibold'>{t('Worked example')}</h3>
            <p className='text-muted-foreground text-sm leading-6'>
              {t(
                'The admin configured three groups and one special cost rule:'
              )}
            </p>

            <div className='overflow-hidden rounded-none border'>
              <div className='bg-muted/40 border-b px-3 py-1.5 text-xs font-medium'>
                {t('Pricing groups')}
              </div>
              <table className='w-full text-sm'>
                <thead>
                  <tr className='text-muted-foreground border-b text-xs'>
                    <th className='px-3 py-1.5 text-left font-medium'>
                      {t('Group name')}
                    </th>
                    <th className='px-3 py-1.5 text-right font-medium'>
                      {t('Cost multiplier')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  <tr className='border-b'>
                    <td className='px-3 py-1.5'>default</td>
                    <td className='px-3 py-1.5 text-right'>1.0</td>
                  </tr>
                  <tr className='border-b'>
                    <td className='px-3 py-1.5'>premium</td>
                    <td className='px-3 py-1.5 text-right'>0.5</td>
                  </tr>
                  <tr>
                    <td className='px-3 py-1.5'>vip</td>
                    <td className='px-3 py-1.5 text-right'>0.8</td>
                  </tr>
                </tbody>
              </table>
            </div>

            <div className='overflow-hidden rounded-none border'>
              <div className='bg-muted/40 border-b px-3 py-1.5 text-xs font-medium'>
                {t('Special cost rules')}
              </div>
              <div className='p-3 text-sm leading-6'>
                {t('Users of vip, when billed as premium, use cost multiplier')}{' '}
                <span className='bg-primary/10 ring-primary/40 rounded px-1.5 py-0.5 font-semibold ring-1'>
                  0.3
                </span>{' '}
                <span className='text-muted-foreground text-xs'>
                  {t('(instead of {{ratio}})', { ratio: 0.5 })}
                </span>
              </div>
            </div>

            <p className='text-muted-foreground text-sm leading-6'>
              {t(
                'Three calls made by the same vip user. Assume the base price of one call is 10.'
              )}
            </p>

            <div className='space-y-3'>
              <div className='overflow-hidden rounded-none border'>
                <div className='bg-muted/40 border-b px-3 py-2 text-sm font-medium'>
                  {t('Call 1: the token group is premium')}
                </div>
                <div className='space-y-2 p-3'>
                  <GuideStepRow chip='1'>
                    {t(
                      'Billing group = premium (the token has a group, so use it)'
                    )}
                  </GuideStepRow>
                  <GuideStepRow chip='2'>
                    {t(
                      'There is a rule for vip billed as premium → use its cost multiplier 0.3'
                    )}
                  </GuideStepRow>
                  <GuideStepRow chip='='>
                    <span className='text-foreground font-medium'>
                      {t('Cost basis = 10 × 0.3 = 3')}
                    </span>
                  </GuideStepRow>
                </div>
              </div>

              <div className='overflow-hidden rounded-none border'>
                <div className='bg-muted/40 border-b px-3 py-2 text-sm font-medium'>
                  {t('Call 2: the token group is default')}
                </div>
                <div className='space-y-2 p-3'>
                  <GuideStepRow chip='1'>
                    {t(
                      'Billing group = default (the token has a group, so use it)'
                    )}
                  </GuideStepRow>
                  <GuideStepRow chip='2'>
                    {t(
                      'No rule for vip billed as default → use the base cost of default, 1.0 (the 0.8 of vip is not used)'
                    )}
                  </GuideStepRow>
                  <GuideStepRow chip='='>
                    <span className='text-foreground font-medium'>
                      {t('Cost basis = 10 × 1.0 = 10')}
                    </span>
                  </GuideStepRow>
                </div>
              </div>

              <div className='overflow-hidden rounded-none border'>
                <div className='bg-muted/40 border-b px-3 py-2 text-sm font-medium'>
                  {t('Call 3: the token has no group')}
                </div>
                <div className='space-y-2 p-3'>
                  <GuideStepRow chip='1'>
                    {t(
                      'Billing group = vip (the token has no group, so use the user group)'
                    )}
                  </GuideStepRow>
                  <GuideStepRow chip='2'>
                    {t(
                      'No rule for vip billed as vip → use the base cost of vip, 0.8'
                    )}
                  </GuideStepRow>
                  <GuideStepRow chip='='>
                    <span className='text-foreground font-medium'>
                      {t('Cost basis = 10 × 0.8 = 8')}
                    </span>
                  </GuideStepRow>
                </div>
              </div>
            </div>
          </section>

          <Accordion className='rounded-none border px-3'>
            <AccordionItem value='groups'>
              <AccordionTrigger>{t('Pricing group example')}</AccordionTrigger>
              <AccordionContent className='space-y-3'>
                <p className='text-muted-foreground text-sm leading-6'>
                  {t(
                    'Use the pricing group table to manage the cost multiplier and whether the group appears in the token creation dropdown.'
                  )}
                </p>
                <GuideCodeBlock>
                  {`${t('Group name')}   ${t('Cost multiplier')}   ${t('User selectable')}   ${t('Description')}
standard     1.0     ${t('Yes')}               ${t('Standard price')}
premium      0.5     ${t('Yes')}               ${t('Premium plan, half price')}
vip          0.5     ${t('No')}                ${t('Assigned by administrator only')}`}
                </GuideCodeBlock>
                <p className='text-muted-foreground text-sm leading-6'>
                  {t(
                    'Users only see groups marked as user selectable. Non-selectable groups can still be assigned by administrators.'
                  )}
                </p>
              </AccordionContent>
            </AccordionItem>

            <AccordionItem value='auto'>
              <AccordionTrigger>{t('Auto group behavior')}</AccordionTrigger>
              <AccordionContent className='space-y-3'>
                <p className='text-muted-foreground text-sm leading-6'>
                  {t(
                    'When a token uses the auto group, the system tries groups from top to bottom until it finds an available group.'
                  )}
                </p>
                <GuideCodeBlock>{`["default", "vip"]`}</GuideCodeBlock>
                <p className='text-muted-foreground text-sm leading-6'>
                  {t(
                    'If default auto group is enabled, newly created tokens start with auto instead of an empty group.'
                  )}
                </p>
              </AccordionContent>
            </AccordionItem>

            <AccordionItem value='special-ratio'>
              <AccordionTrigger>{t('Special cost rules')}</AccordionTrigger>
              <AccordionContent className='space-y-3'>
                <p className='text-muted-foreground text-sm leading-6'>
                  {t(
                    'In JSON, the user group is the outer key and the billing group is the inner key. The example below means: vip users use cost multiplier 0.8 when billed as standard, and 0.3 when billed as premium.'
                  )}
                </p>
                <GuideCodeBlock>{`{
  "vip": {
    "standard": 0.8,
    "premium": 0.3
  }
}`}</GuideCodeBlock>
                <p className='text-muted-foreground text-sm leading-6'>
                  {t(
                    'Only configured combinations are overridden. All other calls keep the billing group base cost multiplier.'
                  )}
                </p>
              </AccordionContent>
            </AccordionItem>

            <AccordionItem value='usable'>
              <AccordionTrigger>
                {t('Special usable group rules')}
              </AccordionTrigger>
              <AccordionContent className='space-y-3'>
                <p className='text-muted-foreground text-sm leading-6'>
                  {t(
                    'Special usable group rules make extra token groups visible to, or hide default ones from, users of a specific user group.'
                  )}
                </p>
                <GuideCodeBlock>{`{
  "vip": {
    "+:premium": "${t('Premium plan, half price')}",
    "-:default": "remove",
    "special": "${t('Special group')}"
  }
}`}</GuideCodeBlock>
                <p className='text-muted-foreground text-sm leading-6'>
                  {t(
                    'In the visual editor these appear as Extra visible and Hidden. In JSON, +: (or no prefix) adds a group and -: removes one.'
                  )}
                </p>
              </AccordionContent>
            </AccordionItem>
          </Accordion>
        </div>
      </SheetContent>
    </Sheet>
  )
}
