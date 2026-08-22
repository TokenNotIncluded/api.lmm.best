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
import { zodResolver } from '@hookform/resolvers/zod'
import {
  Alert02Icon,
  CheckmarkCircle02Icon,
  InformationCircleIcon,
  Key01Icon,
  Loading03Icon,
  Plug01Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { PasswordInput } from '@/components/password-input'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
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
import { Switch } from '@/components/ui/switch'
import { formatNumber } from '@/lib/format'

import {
  SettingsForm,
  SettingsFormGrid,
  SettingsFormGridItem,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import {
  SettingsPageActionsPortal,
  SettingsPageFormActions,
  SettingsPageTitleStatusPortal,
} from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import {
  clearHeroSmsApiKey,
  getHeroSmsPreviewQuota,
  getHeroSmsSettings,
  parseHeroSmsSettingsError,
  testHeroSmsConnection,
  toHeroSmsSettingsFormValues,
  updateHeroSmsSettings,
} from './hero-sms-api'

const heroSmsSettingsSchema = z.object({
  enabled: z.boolean(),
  apiKey: z.string(),
  priceMultiplier: z.number().min(0.01).max(10000),
})

type HeroSmsSettingsFormValues = z.infer<typeof heroSmsSettingsSchema>

export function HeroSmsSettingsSection() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [clearDialogOpen, setClearDialogOpen] = useState(false)
  const [testState, setTestState] = useState<{
    loading: boolean
    ok: boolean | null
    error: string | null
  }>({ loading: false, ok: null, error: null })

  const settingsQuery = useQuery({
    queryKey: ['hero-sms-settings'],
    queryFn: getHeroSmsSettings,
    placeholderData: (previousData) => previousData,
  })

  const updateMutation = useMutation({
    mutationFn: updateHeroSmsSettings,
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['hero-sms-settings'] })
      toast.success(t('HeroSMS settings saved'))
    },
  })

  const clearMutation = useMutation({
    mutationFn: clearHeroSmsApiKey,
    onSuccess: async () => {
      setClearDialogOpen(false)
      await queryClient.invalidateQueries({ queryKey: ['hero-sms-settings'] })
      toast.success(t('HeroSMS API key cleared'))
    },
  })

  const formDefaults = useMemo(
    () =>
      settingsQuery.data
        ? toHeroSmsSettingsFormValues(settingsQuery.data)
        : undefined,
    [settingsQuery.data]
  )

  const form = useForm<HeroSmsSettingsFormValues>({
    resolver: zodResolver(heroSmsSettingsSchema),
    defaultValues: {
      enabled: false,
      apiKey: '',
      priceMultiplier: 10,
    },
  })

  useResetForm(form, formDefaults)

  const priceMultiplier = form.watch('priceMultiplier')

  const configured = settingsQuery.data?.api_key_configured ?? false
  const enabled = form.watch('enabled')

  const handleSubmit = async (values: HeroSmsSettingsFormValues) => {
    await updateMutation.mutateAsync(values)
  }

  const handleTestConnection = async () => {
    setTestState({ loading: true, ok: null, error: null })
    try {
      await testHeroSmsConnection()
      setTestState({ loading: false, ok: true, error: null })
      toast.success(t('HeroSMS connection test passed'))
    } catch (error) {
      const parsed = parseHeroSmsSettingsError(error)
      setTestState({ loading: false, ok: false, error: parsed.message })
      toast.error(parsed.message || t('Connection failed'))
    }
  }

  const resetToServerState = () => {
    if (formDefaults) form.reset(formDefaults)
    setTestState({ loading: false, ok: null, error: null })
  }

  if (settingsQuery.isLoading && !settingsQuery.data) {
    return <LoadingState message={t('Loading HeroSMS settings...')} />
  }

  if (settingsQuery.isError && !settingsQuery.data) {
    return (
      <ErrorState
        title={t('Unable to load HeroSMS settings')}
        description={t('Retry to fetch the latest provider configuration before editing this section.')}
        onRetry={() => void settingsQuery.refetch()}
      />
    )
  }

  return (
    <SettingsSection title={t('HeroSMS Email')}>
      <SettingsPageTitleStatusPortal>
        <div className='flex flex-wrap items-center gap-2'>
          <Badge variant={configured ? 'default' : 'outline'}>
            <HugeiconsIcon
              icon={configured ? CheckmarkCircle02Icon : InformationCircleIcon}
              data-icon='inline-start'
              strokeWidth={2}
            />
            <span>{configured ? t('Configured') : t('Not configured')}</span>
          </Badge>
          <Badge variant={enabled ? 'secondary' : 'outline'}>
            <span>{enabled ? t('Enabled') : t('Disabled')}</span>
          </Badge>
        </div>
      </SettingsPageTitleStatusPortal>

      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(handleSubmit)}>
          <SettingsPageActionsPortal>
            <Button
              type='button'
              size='sm'
              variant='outline'
              onClick={() => void handleTestConnection()}
              disabled={testState.loading || settingsQuery.isLoading}
            >
              <HugeiconsIcon
                icon={testState.loading ? Loading03Icon : Plug01Icon}
                data-icon='inline-start'
                className={testState.loading ? 'animate-spin' : undefined}
                strokeWidth={2}
              />
              <span>{t('Test connection')}</span>
            </Button>
            <Button
              type='button'
              size='sm'
              variant='destructive'
              onClick={() => setClearDialogOpen(true)}
              disabled={!configured || clearMutation.isPending}
            >
              <HugeiconsIcon icon={Key01Icon} data-icon='inline-start' strokeWidth={2} />
              <span>{t('Clear saved key')}</span>
            </Button>
          </SettingsPageActionsPortal>

          <SettingsPageFormActions
            onSave={form.handleSubmit(handleSubmit)}
            onReset={resetToServerState}
            isSaving={updateMutation.isPending}
            isSaveDisabled={!form.formState.isDirty}
            isResetDisabled={!form.formState.isDirty}
          />

          {settingsQuery.isError && settingsQuery.data ? (
            <Alert variant='destructive'>
              <HugeiconsIcon icon={Alert02Icon} strokeWidth={2} aria-hidden='true' />
              <AlertTitle>{t('Using last loaded HeroSMS settings')}</AlertTitle>
              <AlertDescription>
                {t('Saving is still available, but refresh again if you suspect the server state changed elsewhere.')}
              </AlertDescription>
            </Alert>
          ) : null}

          {!enabled ? (
            <Alert>
              <HugeiconsIcon icon={InformationCircleIcon} strokeWidth={2} aria-hidden='true' />
              <AlertTitle>{t('HeroSMS purchasing is disabled')}</AlertTitle>
              <AlertDescription>
                {t('Turn this on only after the API key, multiplier, and test connection all succeed.')}
              </AlertDescription>
            </Alert>
          ) : null}

          {testState.ok ? (
            <Alert>
              <HugeiconsIcon icon={CheckmarkCircle02Icon} strokeWidth={2} aria-hidden='true' />
              <AlertTitle>{t('Connection test succeeded')}</AlertTitle>
              <AlertDescription>
                {t('The server can reach HeroSMS with the currently saved configuration.')}
              </AlertDescription>
            </Alert>
          ) : null}

          {testState.error ? (
            <Alert variant='destructive'>
              <HugeiconsIcon icon={Alert02Icon} strokeWidth={2} aria-hidden='true' />
              <AlertTitle>{t('Connection test failed')}</AlertTitle>
              <AlertDescription>{testState.error}</AlertDescription>
            </Alert>
          ) : null}

          <FormField
            control={form.control}
            name='enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enable HeroSMS email activations')}</FormLabel>
                  <FormDescription>
                    {t('Allow authenticated users to purchase HeroSMS temporary email activations from the console.')}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch checked={field.value} onCheckedChange={field.onChange} />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <SettingsFormGrid>
            <SettingsFormGridItem>
              <FormField
                control={form.control}
                name='apiKey'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Replacement API key')}</FormLabel>
                    <FormControl>
                      <PasswordInput
                        {...field}
                        autoComplete='new-password'
                        placeholder={t('Leave blank to keep the current saved key')}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('For security, the browser never reads back the saved secret. Enter a new key only when rotating it.')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </SettingsFormGridItem>

            <SettingsFormGridItem>
              <FormField
                control={form.control}
                name='priceMultiplier'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Price multiplier')}</FormLabel>
                    <FormControl>
                      <Input
                        value={String(field.value ?? '')}
                        onChange={(event) => {
                          const nextValue = Number(event.target.value)
                          field.onChange(Number.isFinite(nextValue) ? nextValue : 0)
                        }}
                        type='number'
                        min={0.01}
                        step='0.01'
                        inputMode='decimal'
                      />
                    </FormControl>
                    <FormDescription>
                      {t('$1 preview: {{quota}} quota', {
                        quota: formatNumber(getHeroSmsPreviewQuota(priceMultiplier || 10)),
                      })}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </SettingsFormGridItem>

            <SettingsFormGridItem>
              <FormItem>
                <FormLabel>{t('Currency')}</FormLabel>
                <FormControl>
                  <Input value='USD' readOnly aria-readonly='true' />
                </FormControl>
                <FormDescription>{t('Fixed provider settlement currency')}</FormDescription>
              </FormItem>
            </SettingsFormGridItem>

            <SettingsFormGridItem>
              <FormItem>
                <FormLabel>{t('Currency code')}</FormLabel>
                <FormControl>
                  <Input value='840' readOnly aria-readonly='true' />
                </FormControl>
                <FormDescription>{t('ISO numeric currency code')}</FormDescription>
              </FormItem>
            </SettingsFormGridItem>
          </SettingsFormGrid>
        </SettingsForm>
      </Form>

      <AlertDialog open={clearDialogOpen} onOpenChange={setClearDialogOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Clear saved HeroSMS API key')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t('This permanently removes the server-side secret. Purchasing and connection tests will fail until a new key is saved.')}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('Cancel')}</AlertDialogCancel>
            <AlertDialogAction
              onClick={(event) => {
                event.preventDefault()
                void clearMutation.mutateAsync()
              }}
              disabled={clearMutation.isPending}
            >
              {clearMutation.isPending ? t('Clearing...') : t('Clear key')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </SettingsSection>
  )
}
