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
import { ShieldCheck, KeyRound, Loader2, Mail, Smartphone } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import {
  getPreferredVerificationMethods,
  type SecureVerificationState,
  type VerificationMethod,
  type VerificationMethods,
} from '../types'

interface SecureVerificationDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  methods: VerificationMethods
  state: SecureVerificationState
  onVerify: (method: VerificationMethod, code?: string) => void | Promise<void>
  onCancel: () => void
  onCodeChange: (code: string) => void
  onMethodChange: (method: VerificationMethod) => void
  onSendEmailCode?: () => void | Promise<unknown>
  emailCodeSending?: boolean
  emailCodeSent?: boolean
}

export function SecureVerificationDialog({
  open,
  onOpenChange,
  methods,
  state,
  onVerify,
  onCancel,
  onCodeChange,
  onMethodChange,
  onSendEmailCode,
  emailCodeSending = false,
  emailCodeSent = false,
}: SecureVerificationDialogProps) {
  const { t } = useTranslation()
  const preferredMethods = useMemo(
    () => getPreferredVerificationMethods(methods, state.scope),
    [methods, state.scope]
  )
  const availableTabs: VerificationMethod[] = useMemo(() => {
    const tabs: VerificationMethod[] = []
    if (preferredMethods.hasEmail) tabs.push('email')
    if (preferredMethods.has2FA) tabs.push('2fa')
    if (preferredMethods.hasPasskey) tabs.push('passkey')
    return tabs
  }, [preferredMethods])

  const activeMethod =
    state.method && availableTabs.includes(state.method)
      ? state.method
      : availableTabs.length > 0
        ? availableTabs[0]
        : null

  const title =
    state.title ??
    (availableTabs.length
      ? 'Additional verification required'
      : 'Verification unavailable')

  const description =
    state.description ??
    (availableTabs.length
      ? 'Confirm your identity before accessing this sensitive action.'
      : 'Bind an email, enable 2FA, or set up a Passkey before continuing.')

  const handleVerify = () => {
    if (!activeMethod) return
    const payload = activeMethod === '2fa' ? state.code : undefined
    onVerify(activeMethod, payload)
  }

  const verifyDisabled =
    state.loading ||
    (activeMethod === 'email' &&
      (!emailCodeSent || !state.code.trim() || state.code.length < 6)) ||
    (activeMethod === '2fa' && (!state.code.trim() || state.code.length < 6))

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={
        <>
          <ShieldCheck className='text-primary h-5 w-5' />
          {title}
        </>
      }
      description={description}
      contentClassName='top-[8vh] max-w-[calc(100%-1.5rem)] translate-y-0 overflow-hidden border border-border shadow-none sm:top-1/2 sm:max-w-md sm:translate-y-[-50%] sm:rounded-none'
      headerClassName='border-b pb-4 text-left'
      titleClassName='flex items-center gap-2 text-lg font-semibold'
      descriptionClassName='text-left'
      contentHeight='auto'
      bodyClassName='px-1 py-1'
      showCloseButton={!state.loading}
      footerClassName='bg-muted/30 border-t px-6 py-4 sm:flex-row sm:justify-end'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            disabled={state.loading}
            onClick={onCancel}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='button'
            onClick={handleVerify}
            disabled={availableTabs.length === 0 || verifyDisabled}
          >
            {state.loading && <Loader2 className='h-4 w-4 animate-spin' />}
            {t('Verify')}
          </Button>
        </>
      }
    >
      {availableTabs.length === 0 ? (
        <div className='grid place-items-center gap-4 text-center'>
          <div className='bg-muted flex h-16 w-16 items-center justify-center rounded-2xl'>
            <ShieldCheck className='text-muted-foreground h-8 w-8' />
          </div>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Bind an email, enable 2FA, or set up a Passkey in your profile to unlock sensitive operations.'
            )}
          </p>
        </div>
      ) : (
        <Tabs
          value={activeMethod ?? availableTabs[0]}
          onValueChange={(value) => onMethodChange(value as VerificationMethod)}
          className='gap-4'
        >
          <TabsList>
            {preferredMethods.hasEmail && (
              <TabsTrigger value='email'>{t('Email verification')}</TabsTrigger>
            )}
            {preferredMethods.has2FA && (
              <TabsTrigger value='2fa'>
                {t('Two-factor authentication')}
              </TabsTrigger>
            )}
            {preferredMethods.hasPasskey && (
              <TabsTrigger value='passkey'>{t('Passkey')}</TabsTrigger>
            )}
          </TabsList>

          <TabsContent value='email' className='space-y-3'>
            <p className='text-muted-foreground text-sm'>
              {methods.emailHint
                ? t('Send a one-time code to {{email}}.', {
                    email: methods.emailHint,
                  })
                : t('Send a one-time code to your bound email address.')}
            </p>
            <div className='flex gap-2'>
              <Input
                inputMode='numeric'
                maxLength={6}
                value={state.code}
                onChange={(event) => onCodeChange(event.target.value)}
                placeholder={t('Enter verification code')}
                disabled={state.loading}
                autoFocus={activeMethod === 'email'}
                onKeyDown={(event) => {
                  if (event.key === 'Enter' && !verifyDisabled) {
                    event.preventDefault()
                    handleVerify()
                  }
                }}
              />
              <Button
                type='button'
                variant='outline'
                onClick={onSendEmailCode}
                disabled={state.loading || emailCodeSending}
              >
                {emailCodeSending ? (
                  <Loader2 className='h-4 w-4 animate-spin' />
                ) : (
                  <Mail className='h-4 w-4' />
                )}
                {emailCodeSent ? t('Resend code') : t('Send code')}
              </Button>
            </div>
          </TabsContent>

          <TabsContent value='2fa' className='space-y-3'>
            <div className='bg-muted/50 flex items-center gap-3 rounded-lg p-4'>
              <Smartphone className='text-primary h-6 w-6' />
              <p className='text-muted-foreground text-sm'>
                {t(
                  'Enter the code from your authenticator app or a backup code.'
                )}
              </p>
            </div>
            <Input
              inputMode='numeric'
              autoComplete='one-time-code'
              value={state.code}
              onChange={(event) => onCodeChange(event.target.value)}
              placeholder={t('Enter verification code or backup code')}
              disabled={state.loading}
              autoFocus={activeMethod === '2fa'}
              onKeyDown={(event) => {
                if (event.key === 'Enter' && !verifyDisabled) {
                  event.preventDefault()
                  handleVerify()
                }
              }}
            />
          </TabsContent>

          <TabsContent value='passkey' className='space-y-4'>
            <div className='bg-muted/50 flex items-center justify-center rounded-lg p-4'>
              <div className='text-muted-foreground flex items-center gap-3'>
                <KeyRound className='text-primary h-6 w-6' />
                <div className='text-left text-sm'>
                  <p className='text-foreground font-medium'>
                    {t('Use your Passkey')}
                  </p>
                  <p>
                    {t(
                      'We will prompt your device to confirm using biometrics or your hardware key.'
                    )}
                  </p>
                </div>
              </div>
            </div>
          </TabsContent>
        </Tabs>
      )}
    </Dialog>
  )
}
