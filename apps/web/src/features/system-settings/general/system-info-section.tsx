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
import type { Resolver } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import * as z from 'zod'

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
import { Textarea } from '@/components/ui/textarea'

import { normalizeServerAddress } from '../auth/oauth-callback-url'
import { FormDirtyIndicator } from '../components/form-dirty-indicator'
import { FormNavigationGuard } from '../components/form-navigation-guard'
import { SettingsDisclosure } from '../components/settings-disclosure'
import {
  SettingsForm,
  SettingsFormGrid,
  SettingsFormGridItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useSettingsForm } from '../hooks/use-settings-form'
import { useUpdateOption } from '../hooks/use-update-option'

const _systemInfoSchema = z.object({
  SystemName: z.string().min(1),
  ServerAddress: z.string().optional(),
  Logo: z.string().url().optional().or(z.literal('')),
  Footer: z.string().optional(),
  About: z.string().optional(),
  HomePageContent: z.string().optional(),
  legal: z.object({
    user_agreement: z.string().optional(),
    privacy_policy: z.string().optional(),
    user_agreement_en: z.string().optional(),
    privacy_policy_en: z.string().optional(),
  }),
})

type SystemInfoFormValues = z.infer<typeof _systemInfoSchema>

type SystemInfoSectionProps = {
  defaultValues: SystemInfoFormValues
}

function normalizeValue(value: unknown): string {
  if (value === undefined || value === null) return ''
  return typeof value === 'string' ? value : String(value)
}

export function SystemInfoSection({ defaultValues }: SystemInfoSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const normalizedDefaults: SystemInfoFormValues = {
    SystemName: normalizeValue(defaultValues.SystemName),
    ServerAddress: normalizeServerAddress(
      normalizeValue(defaultValues.ServerAddress)
    ),
    Logo: normalizeValue(defaultValues.Logo),
    Footer: normalizeValue(defaultValues.Footer),
    About: normalizeValue(defaultValues.About),
    HomePageContent: normalizeValue(defaultValues.HomePageContent),
    legal: {
      user_agreement: normalizeValue(defaultValues.legal?.user_agreement),
      privacy_policy: normalizeValue(defaultValues.legal?.privacy_policy),
      user_agreement_en: normalizeValue(defaultValues.legal?.user_agreement_en),
      privacy_policy_en: normalizeValue(defaultValues.legal?.privacy_policy_en),
    },
  }

  const systemInfoSchemaWithI18n = z.object({
    SystemName: z.string().min(1, {
      error: () => t('System name is required'),
    }),
    ServerAddress: z.string().optional(),
    Logo: z.string().url().optional().or(z.literal('')),
    Footer: z.string().optional(),
    About: z.string().optional(),
    HomePageContent: z.string().optional(),
    legal: z.object({
      user_agreement: z.string().optional(),
      privacy_policy: z.string().optional(),
      user_agreement_en: z.string().optional(),
      privacy_policy_en: z.string().optional(),
    }),
  })

  const { form, handleSubmit, handleReset, isDirty, isSubmitting } =
    useSettingsForm<SystemInfoFormValues>({
      resolver: zodResolver(systemInfoSchemaWithI18n) as Resolver<
        SystemInfoFormValues,
        unknown,
        SystemInfoFormValues
      >,
      defaultValues: normalizedDefaults,
      onSubmit: async (_data, changedFields) => {
        for (const [key, value] of Object.entries(changedFields)) {
          let v = normalizeValue(value)
          if (key === 'ServerAddress') {
            v = normalizeServerAddress(v)
          }
          await updateOption.mutateAsync({
            key,
            value: v,
          })
        }
      },
    })

  return (
    <>
      <FormNavigationGuard when={isDirty} />

      <SettingsSection title={t('System Information')}>
        <Form {...form}>
          <SettingsForm className='settings-stack' onSubmit={handleSubmit}>
            <SettingsPageFormActions
              onSave={handleSubmit}
              onReset={handleReset}
              isSaving={isSubmitting || updateOption.isPending}
              isResetDisabled={!isDirty}
              isSaveDisabled={!isDirty}
            />
            <FormDirtyIndicator isDirty={isDirty} />
            <SettingsDisclosure
              title={t('Brand & address')}
              description={t('The essentials visitors see')}
              defaultOpen
            >
              <div
                className='settings-brand-preview'
                aria-label={t('Brand preview')}
              >
                <span className='settings-brand-monogram' aria-hidden='true'>
                  {(form.watch('SystemName') || 'L')
                    .trim()
                    .slice(0, 1)
                    .toUpperCase()}
                </span>
                <div className='min-w-0'>
                  <p className='truncate text-base font-semibold'>
                    {form.watch('SystemName') || t('System Name')}
                  </p>
                  <p className='text-muted-foreground mt-1 truncate font-mono text-xs'>
                    {form.watch('ServerAddress') || t('Server Address')}
                  </p>
                </div>
                <span className='text-muted-foreground ml-auto hidden text-xs sm:block'>
                  {t('Brand preview')}
                </span>
              </div>
              <SettingsFormGrid>
                <FormField
                  control={form.control}
                  name='SystemName'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('System Name')}</FormLabel>
                      <FormControl>
                        <Input placeholder='LMM API' {...field} />
                      </FormControl>
                      <FormDescription>
                        {t('The name displayed across the application')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name='ServerAddress'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Server Address')}</FormLabel>
                      <FormControl>
                        <Input
                          placeholder='https://yourdomain.com'
                          {...field}
                          onBlur={(event) => {
                            const normalized = normalizeServerAddress(
                              event.currentTarget.value
                            )
                            if (normalized !== event.currentTarget.value) {
                              field.onChange(normalized)
                            }
                            field.onBlur()
                          }}
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'The public URL of your server, used for OAuth callbacks, webhooks, and other external integrations'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name='Logo'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Logo URL')}</FormLabel>
                      <FormControl>
                        <Input
                          placeholder={t('https://example.com/logo.png')}
                          {...field}
                        />
                      </FormControl>
                      <FormDescription>
                        {t('URL to your logo image (optional)')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </SettingsFormGrid>
            </SettingsDisclosure>
            <SettingsDisclosure
              title={t('Page content')}
              description={t('Home page, about and footer')}
            >
              <SettingsFormGrid>
                <FormField
                  control={form.control}
                  name='Footer'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Footer')}</FormLabel>
                      <FormControl>
                        <Textarea
                          placeholder={t(
                            '© 2025 Your Company. All rights reserved.'
                          )}
                          rows={4}
                          {...field}
                        />
                      </FormControl>
                      <FormDescription>
                        {t('Footer text displayed at the bottom of pages')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name='About'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('About')}</FormLabel>
                      <FormControl>
                        <Textarea
                          placeholder={t(
                            'Enter HTML code (e.g., <p>About us...</p>) or a URL (e.g., https://example.com) to embed as iframe'
                          )}
                          rows={4}
                          {...field}
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'Supports HTML markup or iframe embedding. Enter HTML code directly, or provide a complete URL to automatically embed it as an iframe.'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <SettingsFormGridItem span='full'>
                  <FormField
                    control={form.control}
                    name='HomePageContent'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Home Page Content')}</FormLabel>
                        <FormControl>
                          <Textarea
                            placeholder={t('Welcome to our LMM API...')}
                            rows={6}
                            {...field}
                          />
                        </FormControl>
                        <FormDescription>
                          {t(
                            'Content displayed on the home page (supports Markdown)'
                          )}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </SettingsFormGridItem>
              </SettingsFormGrid>
            </SettingsDisclosure>
            <SettingsDisclosure
              title={t('Legal documents')}
              description={t('Agreements and privacy policy')}
            >
              <SettingsFormGrid>
                <FormField
                  control={form.control}
                  name='legal.user_agreement'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('User Agreement')}</FormLabel>
                      <FormControl>
                        <Textarea
                          placeholder={t(
                            'Provide Markdown, HTML, or an external URL for the user agreement'
                          )}
                          rows={6}
                          {...field}
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'Leave empty to disable the agreement requirement. Supports Markdown, HTML, or a full URL to redirect users.'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name='legal.privacy_policy'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Privacy Policy')}</FormLabel>
                      <FormControl>
                        <Textarea
                          placeholder={t(
                            'Provide Markdown, HTML, or an external URL for the privacy policy'
                          )}
                          rows={6}
                          {...field}
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'Leave empty to disable the privacy policy requirement. Supports Markdown, HTML, or a full URL to redirect users.'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </SettingsFormGrid>
              <SettingsDisclosure title={t('English versions')}>
                <SettingsFormGrid>
                  <FormField
                    control={form.control}
                    name='legal.user_agreement_en'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('User Agreement')} (English)</FormLabel>
                        <FormControl>
                          <Textarea
                            placeholder={t(
                              'Provide Markdown, HTML, or an external URL for the user agreement'
                            )}
                            rows={6}
                            {...field}
                          />
                        </FormControl>
                        <FormDescription>
                          {t(
                            'Shown to English-language visitors. Leave empty to fall back to the primary-language version above.'
                          )}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  <FormField
                    control={form.control}
                    name='legal.privacy_policy_en'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Privacy Policy')} (English)</FormLabel>
                        <FormControl>
                          <Textarea
                            placeholder={t(
                              'Provide Markdown, HTML, or an external URL for the privacy policy'
                            )}
                            rows={6}
                            {...field}
                          />
                        </FormControl>
                        <FormDescription>
                          {t(
                            'Shown to English-language visitors. Leave empty to fall back to the primary-language version above.'
                          )}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </SettingsFormGrid>
              </SettingsDisclosure>
            </SettingsDisclosure>
          </SettingsForm>
        </Form>
      </SettingsSection>
    </>
  )
}
