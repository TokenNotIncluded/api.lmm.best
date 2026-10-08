/*
Copyright (C) 2026 LIghtJUNction
*/
import { zodResolver } from '@hookform/resolvers/zod'
import { useMemo, useState } from 'react'
import type { Resolver } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Field,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field'
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
import { Separator } from '@/components/ui/separator'
import { Switch } from '@/components/ui/switch'

import { getSystemOptions } from '../api'
import { FormDirtyIndicator } from '../components/form-dirty-indicator'
import { FormNavigationGuard } from '../components/form-navigation-guard'
import {
  SettingsForm,
  SettingsFormGrid,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useSettingsForm } from '../hooks/use-settings-form'
import { useSystemOptions } from '../hooks/use-system-options'
import { useUpdateOption } from '../hooks/use-update-option'
import { getSettingsErrorMessage } from '../utils/settings-error-message'
import {
  parseTrustLevelBenefits,
  serializeTrustLevelBenefits,
  TRUST_LEVEL_BENEFIT_CODES,
  TRUST_LEVEL_BENEFITS_OPTION,
  trustLevelBenefitsSchema,
  type TrustLevelBenefitsConfig,
} from './trust-level-benefits-schema'

const benefitLabels = {
  standard_access: 'Standard access',
  developer_access: 'Developer console access',
  usage_discount: 'Usage discount',
} as const
const roleBenefitLabels = {
  administrator_access: 'Administrator access',
  superadministrator_access: 'Super administrator access',
  usage_discount: 'Usage discount',
} as const

export function TrustLevelBenefitsSection({ value }: { value: string }) {
  const { t } = useTranslation()
  const config = useMemo(() => parseTrustLevelBenefits(value), [value])
  const { data } = useSystemOptions()
  const supported = data?.capabilities?.trust_level_benefits === true
  return (
    <SettingsSection title={t('Levels & Benefits')}>
      {config && supported ? (
        <TrustLevelBenefitsForm defaultValues={config} />
      ) : (
        <Alert variant='destructive'>
          <AlertTitle>{t('Level configuration unavailable')}</AlertTitle>
          <AlertDescription>
            {t(
              supported
                ? 'Load a valid level configuration from the server before editing. No recharge thresholds have been assumed.'
                : 'The server does not support configurable level benefits. Update the server before editing.'
            )}
          </AlertDescription>
        </Alert>
      )}
    </SettingsSection>
  )
}

function TrustLevelBenefitsForm({
  defaultValues,
}: {
  defaultValues: TrustLevelBenefitsConfig
}) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const [saveError, setSaveError] = useState<string | null>(null)
  const { form, handleSubmit, handleReset, isDirty, isSubmitting } =
    useSettingsForm<TrustLevelBenefitsConfig>({
      defaultValues,
      resolver: zodResolver(
        trustLevelBenefitsSchema
      ) as Resolver<TrustLevelBenefitsConfig>,
      onSubmit: async (data) => {
        setSaveError(null)
        try {
          const current = await getSystemOptions({ silent: true })
          if (
            !current.success ||
            current.capabilities?.trust_level_benefits !== true
          ) {
            throw new Error(
              current.message ||
                t(
                  'The server does not support configurable level benefits. Update the server before editing.'
                )
            )
          }
          await updateOption.mutateAsync({
            key: TRUST_LEVEL_BENEFITS_OPTION,
            value: serializeTrustLevelBenefits(data),
          })
        } catch (error) {
          setSaveError(
            getSettingsErrorMessage(error, t('Failed to update setting'))
          )
          throw error
        }
      },
    })
  const saving = updateOption.isPending || isSubmitting
  const submit = () => {
    void handleSubmit().catch(() => {
      /* The mutation reports the error and the draft stays dirty. */
    })
  }
  return (
    <>
      <FormNavigationGuard when={isDirty} />
      <Alert>
        <AlertTitle>{t('Automatic recharge levels (L0–L4)')}</AlertTitle>
        <AlertDescription>
          {t(
            'Set four cumulative recharge thresholds for L1–L4. These levels never grant administrator roles.'
          )}
        </AlertDescription>
      </Alert>
      <Form {...form}>
        <SettingsForm
          onSubmit={(event) => {
            event.preventDefault()
            submit()
          }}
        >
          <SettingsPageFormActions
            onSave={submit}
            onReset={handleReset}
            isSaving={saving}
            isSaveDisabled={!isDirty}
            isResetDisabled={!isDirty}
          />
          <FormDirtyIndicator isDirty={isDirty} />
          {saveError && (
            <Alert variant='destructive'>
              <AlertDescription>{saveError}</AlertDescription>
            </Alert>
          )}
          <SettingsFormGrid>
            <FormField
              control={form.control}
              name='paid_activation_enabled'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>
                      {t('Let a recharge unlock the console')}
                    </FormLabel>
                    <FormDescription>
                      {t(
                        'When enabled, reaching the configured L1 recharge threshold activates developer access. Invitation and manual review access remain separate.'
                      )}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                      disabled={saving}
                    />
                  </FormControl>
                </SettingsSwitchItem>
              )}
            />
            <FormField
              control={form.control}
              name='decay_period_days'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Inactivity review period (days)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={0}
                      max={3650}
                      step={1}
                      {...field}
                      disabled={saving}
                      onChange={(event) =>
                        field.onChange(
                          event.currentTarget.value === ''
                            ? ''
                            : event.currentTarget.valueAsNumber
                        )
                      }
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Set 0 to disable inactivity decay. Administrator roles do not decay.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </SettingsFormGrid>
          <FieldGroup className='gap-6'>
            {defaultValues.tiers.map((tier, index) => (
              <FieldSet key={tier.level} className='gap-3'>
                <FieldLegend>L{tier.level}</FieldLegend>
                <SettingsFormGrid>
                  {tier.level === 0 ? (
                    <p className='text-muted-foreground text-sm'>
                      {t('L0 has no recharge threshold.')}
                    </p>
                  ) : (
                    <FormField
                      control={form.control}
                      name={`tiers.${index}.min_paid_credits`}
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>
                            {t(
                              'Cumulative recharge required for L{{level}} (credits)',
                              { level: tier.level }
                            )}
                          </FormLabel>
                          <FormControl>
                            <Input
                              type='number'
                              min={1}
                              max={Number.MAX_SAFE_INTEGER}
                              step={1}
                              {...field}
                              disabled={saving}
                              onChange={(event) =>
                                field.onChange(
                                  event.currentTarget.value === ''
                                    ? ''
                                    : event.currentTarget.valueAsNumber
                                )
                              }
                            />
                          </FormControl>
                          <FormDescription>
                            {t(
                              'Cumulative successful eligible recharges, not the extra amount for this step. 500,000 credits equal 1 USD.'
                            )}
                          </FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  )}
                  <FormField
                    control={form.control}
                    name={`tiers.${index}.discount_ratio`}
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>
                          {t('L{{level}} usage discount (%)', {
                            level: tier.level,
                          })}
                        </FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min={0}
                            max={99.999999}
                            step='any'
                            name={field.name}
                            ref={field.ref}
                            onBlur={field.onBlur}
                            value={
                              typeof field.value === 'number' &&
                              Number.isFinite(field.value)
                                ? Number(((1 - field.value) * 100).toFixed(10))
                                : ''
                            }
                            disabled={saving}
                            onChange={(event) =>
                              field.onChange(
                                event.currentTarget.value === ''
                                  ? Number.NaN
                                  : 1 - event.currentTarget.valueAsNumber / 100
                              )
                            }
                          />
                        </FormControl>
                        <FormDescription>
                          {t(
                            '0% uses the standard usage price. This discount does not change recharge amounts.'
                          )}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  <FormField
                    control={form.control}
                    name={`tiers.${index}.benefits`}
                    render={({ field }) => (
                      <FormItem data-settings-form-span='full'>
                        <FormLabel>{t('Displayed benefits')}</FormLabel>
                        <FieldGroup className='gap-3 sm:flex-row sm:flex-wrap'>
                          {TRUST_LEVEL_BENEFIT_CODES.map((code) => {
                            const id = `level-${tier.level}-benefit-${code}`
                            return (
                              <Field
                                key={code}
                                orientation='horizontal'
                                className='w-auto'
                                data-disabled={saving}
                              >
                                <Checkbox
                                  id={id}
                                  checked={(field.value ?? []).includes(code)}
                                  disabled={saving}
                                  onCheckedChange={(checked) =>
                                    field.onChange(
                                      checked
                                        ? [...(field.value ?? []), code]
                                        : (field.value ?? []).filter(
                                            (benefit) => benefit !== code
                                          )
                                    )
                                  }
                                />
                                <FieldLabel htmlFor={id}>
                                  {t(benefitLabels[code])}
                                </FieldLabel>
                              </Field>
                            )
                          })}
                        </FieldGroup>
                        <FormDescription>
                          {t(
                            'Benefit labels describe this level. Developer access follows activation rules; administrator permissions follow account roles.'
                          )}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </SettingsFormGrid>
                {tier.level < 4 && <Separator />}
              </FieldSet>
            ))}
          </FieldGroup>
          <Alert>
            <AlertTitle>{t('Role levels (L5–L6)')}</AlertTitle>
            <AlertDescription>
              <p>{t('L5: Administrator. L6: Super administrator.')}</p>
              <p>
                {t(
                  'Assigned through account roles only. There are no recharge thresholds for L5 or L6.'
                )}
              </p>
            </AlertDescription>
          </Alert>
          {defaultValues.role_tiers ? (
            <FieldGroup className='gap-6'>
              {defaultValues.role_tiers.map((tier, index) => (
                <FieldSet key={tier.level} className='gap-3'>
                  <FieldLegend>
                    L{tier.level} ·{' '}
                    {t(
                      tier.level === 5 ? 'Administrator' : 'Super administrator'
                    )}
                  </FieldLegend>
                  <SettingsFormGrid>
                    <FormField
                      control={form.control}
                      name={`role_tiers.${index}.discount_ratio`}
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>
                            {t('L{{level}} usage discount (%)', {
                              level: tier.level,
                            })}
                          </FormLabel>
                          <FormControl>
                            <Input
                              type='number'
                              min={0}
                              max={99.999999}
                              step='any'
                              name={field.name}
                              ref={field.ref}
                              onBlur={field.onBlur}
                              value={
                                typeof field.value === 'number' &&
                                Number.isFinite(field.value)
                                  ? Number(
                                      ((1 - field.value) * 100).toFixed(10)
                                    )
                                  : ''
                              }
                              disabled={saving}
                              onChange={(event) =>
                                field.onChange(
                                  event.currentTarget.value === ''
                                    ? Number.NaN
                                    : 1 -
                                        event.currentTarget.valueAsNumber / 100
                                )
                              }
                            />
                          </FormControl>
                          <FormDescription>
                            {t(
                              'Role discounts are configured independently of recharge levels.'
                            )}
                          </FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                    <FormField
                      control={form.control}
                      name={`role_tiers.${index}.benefits`}
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('Displayed benefits')}</FormLabel>
                          <FieldGroup className='gap-3'>
                            {(
                              [
                                tier.level === 5
                                  ? 'administrator_access'
                                  : 'superadministrator_access',
                                'usage_discount',
                              ] as const
                            ).map((code) => {
                              const id = `role-level-${tier.level}-benefit-${code}`
                              return (
                                <Field
                                  key={code}
                                  orientation='horizontal'
                                  data-disabled={saving}
                                >
                                  <Checkbox
                                    id={id}
                                    checked={(field.value ?? []).includes(code)}
                                    disabled={saving}
                                    onCheckedChange={(checked) =>
                                      field.onChange(
                                        checked
                                          ? [...(field.value ?? []), code]
                                          : (field.value ?? []).filter(
                                              (benefit) => benefit !== code
                                            )
                                      )
                                    }
                                  />
                                  <FieldLabel htmlFor={id}>
                                    {t(roleBenefitLabels[code])}
                                  </FieldLabel>
                                </Field>
                              )
                            })}
                          </FieldGroup>
                          <FormDescription>
                            {t(
                              'Editing role benefits does not assign or remove account roles.'
                            )}
                          </FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  </SettingsFormGrid>
                </FieldSet>
              ))}
            </FieldGroup>
          ) : (
            <Alert>
              <AlertDescription>
                {t(
                  'Reload server settings to edit the independent L5 and L6 role benefits.'
                )}
              </AlertDescription>
            </Alert>
          )}
        </SettingsForm>
      </Form>
    </>
  )
}
