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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { BrandLogo } from '@/components/brand-logo'
import { ErrorState } from '@/components/error-state'
import { LanguageSwitcher } from '@/components/language-switcher'
import { LoadingState } from '@/components/loading-state'
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Form } from '@/components/ui/form'
import { Skeleton } from '@/components/ui/skeleton'
import { useSystemConfig } from '@/hooks/use-system-config'
import { cn } from '@/lib/utils'

import { buildSetupPayload, getSetupStatus, submitSetup } from './api'
import { AdminStep } from './components/admin-step'
import { CompleteStep } from './components/complete-step'
import { DatabaseStep } from './components/database-step'
import { StepNavigation } from './components/step-navigation'
import { UsageModeStep } from './components/usage-mode-step'
import type { SetupFormValues, SetupStatus } from './types'

const STEPS = [
  {
    titleKey: 'Database check',
    descriptionKey: 'Verify your database connection',
  },
  {
    titleKey: 'Administrator account',
    descriptionKey: 'Create credentials for the root user',
  },
  {
    titleKey: 'Usage mode',
    descriptionKey: 'Choose how the platform will operate',
  },
  {
    titleKey: 'Review & initialize',
    descriptionKey: 'Confirm settings and finish setup',
  },
]

const DEFAULT_FORM_VALUES: SetupFormValues = {
  username: '',
  password: '',
  confirmPassword: '',
  usageMode: 'external',
}

export function SetupWizard() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { systemName, logo, loading: systemConfigLoading } = useSystemConfig()

  const [currentStep, setCurrentStep] = useState(0)
  const [setupStatus, setSetupStatus] = useState<SetupStatus | undefined>()

  const form = useForm<SetupFormValues>({
    defaultValues: DEFAULT_FORM_VALUES,
    mode: 'onBlur',
  })

  const watchedValues = form.watch()

  const {
    data: statusResponse,
    isLoading,
    isError,
    refetch,
  } = useQuery({
    queryKey: ['setup-status'],
    queryFn: getSetupStatus,
    retry: false,
  })

  const mutation = useMutation({
    mutationKey: ['setup-submit'],
    mutationFn: submitSetup,
    onSuccess: async (response) => {
      if (response.success) {
        toast.success(t('System initialized successfully! Redirecting…'))
        await queryClient.invalidateQueries({ queryKey: ['setup-status'] })
        setTimeout(() => {
          navigate({ to: '/' })
        }, 1200)
      } else {
        toast.error(
          response.message || t('Initialization failed, please try again.')
        )
      }
    },
    onError: () => {
      toast.error(t('Failed to initialize system'))
    },
  })

  useEffect(() => {
    if (!statusResponse) return

    if (!statusResponse.success) {
      toast.error(statusResponse.message || t('Failed to load setup status'))
      return
    }

    const status = statusResponse.data
    if (!status) return

    if (status.status) {
      navigate({ to: '/' })
      return
    }

    setSetupStatus(status)
    setCurrentStep(0)

    // Pre-fill usage mode if backend echoes it
    if (status.SelfUseModeEnabled) {
      form.setValue('usageMode', 'self', {
        shouldDirty: false,
        shouldTouch: false,
        shouldValidate: false,
      })
    } else if (status.DemoSiteEnabled) {
      form.setValue('usageMode', 'demo', {
        shouldDirty: false,
        shouldTouch: false,
        shouldValidate: false,
      })
    } else {
      form.setValue('usageMode', 'external', {
        shouldDirty: false,
        shouldTouch: false,
        shouldValidate: false,
      })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [statusResponse, navigate, form])

  useEffect(() => {
    if (!setupStatus?.root_init) return

    form.setValue('confirmPassword', '', {
      shouldDirty: false,
      shouldTouch: false,
      shouldValidate: false,
    })
  }, [setupStatus, form])

  const currentStepComponent = useMemo(() => {
    if (currentStep === 0) {
      return <DatabaseStep status={setupStatus} />
    }
    if (currentStep === 1) {
      return (
        <AdminStep
          form={form}
          rootInitialized={Boolean(setupStatus?.root_init)}
        />
      )
    }
    if (currentStep === 2) {
      return <UsageModeStep form={form} />
    }
    return <CompleteStep status={setupStatus} values={watchedValues} />
  }, [currentStep, setupStatus, form, watchedValues])

  const validateAdminStep = () => {
    const username = form.getValues('username')?.trim()
    const password = form.getValues('password')?.trim()
    const confirmPassword = form.getValues('confirmPassword')?.trim()
    const rootInitialized = Boolean(setupStatus?.root_init)

    if (!username) {
      form.setError('username', {
        type: 'manual',
        message: t('Please enter an administrator username'),
      })
      toast.error(t('Please enter an administrator username'))
      return false
    }

    if (rootInitialized) {
      if (!password) {
        form.setError('password', {
          type: 'manual',
          message: t('Please enter the existing administrator password'),
        })
        toast.error(t('Please enter the existing administrator password'))
        return false
      }
      return true
    }

    if (!password || password.length < 8) {
      form.setError('password', {
        type: 'manual',
        message: t('Password must be at least 8 characters'),
      })
      toast.error(t('Password must be at least 8 characters'))
      return false
    }

    if (password !== confirmPassword) {
      form.setError('confirmPassword', {
        type: 'manual',
        message: t('Passwords do not match'),
      })
      toast.error(t('Passwords do not match'))
      return false
    }

    return true
  }

  const validateUsageModeStep = () => {
    const usageMode = form.getValues('usageMode')
    if (!usageMode) {
      form.setError('usageMode', {
        type: 'manual',
        message: t('Select a usage mode to continue'),
      })
      toast.error(t('Select a usage mode to continue'))
      return false
    }
    return true
  }

  const handleNextStep = () => {
    if (currentStep === 1 && !validateAdminStep()) return
    if (currentStep === 2 && !validateUsageModeStep()) return

    setCurrentStep((step) => Math.min(step + 1, STEPS.length - 1))
  }

  const handlePreviousStep = () => {
    setCurrentStep((step) => Math.max(step - 1, 0))
  }

  const handleSubmit = async () => {
    const adminValid = validateAdminStep()
    const usageValid = validateUsageModeStep()
    if (!adminValid || !usageValid) return

    const payload = buildSetupPayload(
      form.getValues(),
      Boolean(setupStatus?.root_init)
    )

    mutation.mutate(payload)
  }

  let setupBody: ReactNode
  if (isLoading) {
    setupBody = <LoadingState message={t('Loading setup status…')} />
  } else if (isError) {
    setupBody = (
      <ErrorState
        title={t('We could not load the setup status.')}
        onRetry={() => refetch()}
      />
    )
  } else {
    setupBody = (
      <Form {...form}>
        <form
          className='space-y-6'
          onSubmit={(event) => event.preventDefault()}
        >
          {currentStepComponent}
        </form>
      </Form>
    )
  }

  return (
    <div className='setup-editorial relative min-h-svh px-4 py-6 sm:px-8 sm:py-10'>
      <div className='absolute top-4 right-4 sm:top-8 sm:right-8'>
        <LanguageSwitcher />
      </div>
      <div className='mx-auto flex max-w-6xl flex-col gap-8 sm:gap-10'>
        <header className='setup-editorial-masthead flex flex-col gap-6 pr-12 sm:flex-row sm:items-end sm:justify-between sm:gap-10 sm:pr-0'>
          <div className='flex items-center gap-3 sm:gap-4'>
            <div className='relative h-10 w-10 shrink-0 sm:h-12 sm:w-12'>
              {systemConfigLoading ? (
                <Skeleton className='absolute inset-0 rounded-full' />
              ) : (
                <BrandLogo
                  src={logo}
                  className='h-10 w-10 object-contain sm:h-12 sm:w-12'
                />
              )}
            </div>
            <div className='space-y-1'>
              <p className='setup-editorial-kicker'>LMM API / INITIALIZATION</p>
              {systemConfigLoading ? (
                <Skeleton className='h-8 w-48' />
              ) : (
                <h1 className='font-serif text-2xl leading-none tracking-tight sm:text-3xl'>
                  {t('Initialize')} {systemName}
                </h1>
              )}
            </div>
          </div>
          <p className='setup-editorial-lede max-w-md text-sm sm:text-right sm:text-base'>
            {t(
              'Follow the guided steps to prepare your workspace before the first login.'
            )}
          </p>
        </header>

        <div className='setup-editorial-index flex items-center justify-between border-y py-2 text-xs'>
          <span>SETUP / {String(currentStep + 1).padStart(2, '0')}</span>
          <span>{String(STEPS.length).padStart(2, '0')} STEPS</span>
        </div>

        <Card className='setup-editorial-frame'>
          <CardHeader className='setup-editorial-frame-header space-y-2'>
            <p className='setup-editorial-kicker'>SYSTEM / SETUP</p>
            <CardTitle className='font-serif text-2xl font-normal tracking-tight sm:text-3xl'>
              {t('System setup wizard')}
            </CardTitle>
            <CardDescription className='max-w-xl'>
              {t('Complete these steps to finish the initial installation.')}
            </CardDescription>
          </CardHeader>

          <CardContent className='setup-editorial-frame-content space-y-8'>
            <ol className='setup-editorial-step-list grid sm:grid-cols-4'>
              {STEPS.map((step, index) => {
                const isActive = currentStep === index
                const isCompleted = currentStep > index
                let stepState: 'active' | 'complete' | 'idle' = 'idle'
                if (isActive) {
                  stepState = 'active'
                } else if (isCompleted) {
                  stepState = 'complete'
                }
                return (
                  <li
                    key={step.titleKey}
                    className={cn(
                      'setup-editorial-step border-b p-4 sm:border-r sm:border-b-0',
                      index === STEPS.length - 1 && 'sm:border-r-0',
                      isActive && 'is-active',
                      isCompleted && 'is-complete'
                    )}
                    data-state={stepState}
                  >
                    <div className='flex items-start gap-3'>
                      <span
                        className={cn(
                          'setup-editorial-step-number flex size-7 shrink-0 items-center justify-center rounded-full border text-xs font-semibold',
                          isActive && 'is-active',
                          isCompleted && 'is-complete'
                        )}
                      >
                        {index + 1}
                      </span>
                      <div className='space-y-1'>
                        <p className='text-sm font-semibold'>
                          {t(step.titleKey)}
                        </p>
                        <p className='text-muted-foreground text-xs'>
                          {t(step.descriptionKey)}
                        </p>
                      </div>
                    </div>
                  </li>
                )
              })}
            </ol>

            {setupBody}
          </CardContent>

          {!isLoading && !isError && (
            <CardFooter className='setup-editorial-frame-footer w-full justify-end border-t'>
              <StepNavigation
                currentStep={currentStep}
                totalSteps={STEPS.length}
                onBack={handlePreviousStep}
                onNext={handleNextStep}
                onSubmit={handleSubmit}
                isSubmitting={mutation.isPending}
              />
            </CardFooter>
          )}
        </Card>
      </div>
    </div>
  )
}
