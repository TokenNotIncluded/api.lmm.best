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
import { AlertTriangle, KeyRound, Loader2, ShieldAlert } from 'lucide-react'
import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StatusBadge } from '@/components/status-badge'
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
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { IconBadge } from '@/components/ui/icon-badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import {
  usePasskeyManagement,
  type PasskeyCredentialSummary,
} from '@/features/auth/passkey'
import {
  SecureVerificationDialog,
  useSecureVerification,
  type VerificationMethod,
  type VerificationMethods,
} from '@/features/auth/secure-verification'
import dayjs from '@/lib/dayjs'

interface PasskeyCardProps {
  loading: boolean
}

export function PasskeyCard({ loading: pageLoading }: PasskeyCardProps) {
  const { t } = useTranslation()
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [passkeyName, setPasskeyName] = useState('')
  const [removeTarget, setRemoveTarget] =
    useState<PasskeyCredentialSummary | null>(null)
  const [restrictedMethod, setRestrictedMethod] =
    useState<VerificationMethod | null>(null)

  const {
    status,
    loading,
    registering,
    removing,
    supported,
    enabled,
    lastUsed,
    register,
    remove,
  } = usePasskeyManagement()
  const credentials = status?.credentials ?? []

  const {
    open: verificationOpen,
    setOpen: setVerificationOpen,
    methods: verificationMethods,
    state: verificationState,
    startVerification,
    executeVerification,
    cancel: cancelVerification,
    setCode,
    switchMethod,
    fetchVerificationMethods,
    sendEmailCode,
    emailCodeSending,
    emailCodeSent,
  } = useSecureVerification({
    onSuccess: () => {
      setRestrictedMethod(null)
    },
  })

  const dialogMethods = useMemo<VerificationMethods>(() => {
    if (!restrictedMethod) return verificationMethods
    return {
      ...verificationMethods,
      hasEmail: restrictedMethod === 'email' && verificationMethods.hasEmail,
      has2FA: restrictedMethod === '2fa' && verificationMethods.has2FA,
      hasPasskey:
        restrictedMethod === 'passkey' && verificationMethods.hasPasskey,
    }
  }, [restrictedMethod, verificationMethods])

  const handleRegister = useCallback(async () => {
    if (!supported) {
      toast.info(t('This device does not support Passkey'))
      return
    }

    const name = passkeyName.trim()
    const completeRegistration = async (proofToken?: string) => {
      const registered = await register(name, proofToken)
      if (registered) setPasskeyName('')
      return registered
    }
    const methods = await fetchVerificationMethods()
    if (!methods.hasEmail && !methods.has2FA && !methods.hasPasskey) {
      if (methods.availability !== 'complete') {
        toast.error(t('Request failed'))
        return
      }
      // The first Passkey is the fallback credential itself, so there is no
      // existing Passkey available for a step-up proof yet.
      await completeRegistration()
      return
    }

    let requiredMethod: VerificationMethod = 'passkey'
    if (methods.hasEmail) requiredMethod = 'email'
    else if (methods.has2FA) requiredMethod = '2fa'
    setRestrictedMethod(requiredMethod)
    await startVerification(completeRegistration, {
      scope: 'passkey.register',
      preferredMethod: requiredMethod,
      title: t('Security verification'),
      description: t('Confirm your identity before registering a Passkey.'),
      verificationMethods: methods,
    })
  }, [
    fetchVerificationMethods,
    passkeyName,
    register,
    startVerification,
    supported,
    t,
  ])

  const handleRemove = useCallback(async () => {
    if (!removeTarget) return
    const credentialID = removeTarget.id
    const methods = await fetchVerificationMethods()
    let required: VerificationMethod | null = null
    if (methods.hasEmail) {
      required = 'email'
    } else if (methods.has2FA) {
      required = '2fa'
    } else if (methods.hasPasskey) {
      required = 'passkey'
    }

    if (!required) {
      toast.error(
        methods.availability === 'complete'
          ? t('Please bind an email or set up a Passkey before proceeding')
          : t('Request failed')
      )
      return
    }

    if (required === 'passkey' && !methods.passkeySupported) {
      toast.info(t('This device does not support Passkey'))
      return
    }

    setConfirmOpen(false)
    setRestrictedMethod(required)
    await startVerification((proofToken) => remove(credentialID, proofToken), {
      scope: 'passkey.delete',
      preferredMethod: required,
      title: t('Security verification'),
      description: t(
        'Confirm your identity before removing this Passkey from your account.'
      ),
      verificationMethods: methods,
    })
  }, [fetchVerificationMethods, remove, removeTarget, startVerification, t])

  const handleVerificationCancel = useCallback(() => {
    setRestrictedMethod(null)
    cancelVerification()
  }, [cancelVerification])

  const handleVerificationOpenChange = useCallback(
    (next: boolean) => {
      if (!next) {
        setRestrictedMethod(null)
      }
      setVerificationOpen(next)
    },
    [setVerificationOpen]
  )

  // Adapt the hook's `Promise<unknown>` return into the dialog's
  // `void | Promise<void>` signature without losing error propagation
  // semantics (errors are surfaced via toast inside the hook).
  const handleDialogVerify = useCallback(
    async (method: VerificationMethod, code?: string) => {
      try {
        await executeVerification(method, code)
      } catch {
        // Errors are already surfaced by useSecureVerification via toast.
      }
    },
    [executeVerification]
  )

  if (pageLoading || loading) {
    return (
      <Card data-card-hover='false' className='gap-0 overflow-hidden py-0'>
        <CardHeader className='p-3 sm:p-5'>
          <Skeleton className='h-6 w-48' />
          <Skeleton className='mt-2 h-4 w-64' />
        </CardHeader>
        <CardContent className='p-3 sm:p-5'>
          <Skeleton className='h-20 w-full' />
        </CardContent>
      </Card>
    )
  }

  const formattedLastUsed =
    lastUsed && !Number.isNaN(Date.parse(lastUsed))
      ? dayjs(lastUsed).fromNow()
      : t('Not used yet')

  const showUnsupportedNotice = !supported
  const passkeyLabel = (credential: PasskeyCredentialSummary) =>
    credential.name || `${t('Passkey')} #${credential.id}`

  return (
    <>
      <Card data-card-hover='false' className='gap-0 overflow-hidden py-0'>
        <CardHeader className='p-3 sm:p-5'>
          <CardTitle className='text-lg tracking-tight sm:text-xl'>
            {t('Passkey Login')}
          </CardTitle>
          <CardDescription className='text-xs sm:text-sm'>
            {t('Use Passkey to sign in without entering your password.')}
          </CardDescription>
        </CardHeader>

        <CardContent className='p-3 sm:p-5'>
          <div className='space-y-6'>
            <div className='flex items-start gap-4'>
              <IconBadge tone='info' size='sm'>
                <KeyRound />
              </IconBadge>
              <div className='space-y-1'>
                <div className='flex flex-wrap items-center gap-2'>
                  <p className='font-medium'>{t('Passkey Authentication')}</p>
                  <StatusBadge
                    label={enabled ? t('Enabled') : t('Disabled')}
                    variant={enabled ? 'success' : 'neutral'}
                    showDot
                    copyable={false}
                  />
                </div>
                <p className='text-muted-foreground text-sm'>
                  {t('Last used:')} {formattedLastUsed}
                </p>
              </div>
            </div>

            <div className='space-y-2'>
              <Label htmlFor='passkey-name'>
                {t('Passkey name (optional)')}
              </Label>
              <div className='flex flex-col gap-2 sm:flex-row'>
                <Input
                  id='passkey-name'
                  value={passkeyName}
                  onChange={(event) => setPasskeyName(event.target.value)}
                  placeholder={t('e.g. Laptop or security key')}
                  maxLength={64}
                  disabled={registering || removing}
                />
                <Button
                  className='w-full shrink-0 sm:w-auto'
                  onClick={handleRegister}
                  disabled={!supported || registering || removing}
                >
                  {registering && (
                    <Loader2 className='mr-2 h-4 w-4 animate-spin' />
                  )}
                  {enabled ? t('Add Passkey') : t('Enable Passkey')}
                </Button>
              </div>
            </div>

            {credentials.length > 0 && (
              <div className='space-y-3 border-t pt-5'>
                {credentials.map((credential) => (
                  <div
                    key={credential.id}
                    className='flex flex-col gap-3 rounded-lg border p-3 sm:flex-row sm:items-center sm:justify-between'
                  >
                    <div className='min-w-0 space-y-1'>
                      <div className='flex flex-wrap items-center gap-2'>
                        <p className='truncate font-medium'>
                          {passkeyLabel(credential)}
                        </p>
                        <StatusBadge
                          label={
                            credential.backup_eligible
                              ? credential.backup_state
                                ? t('Backed up')
                                : t('Not backed up')
                              : t('No backup')
                          }
                          variant={
                            credential.backup_eligible
                              ? credential.backup_state
                                ? 'success'
                                : 'warning'
                              : 'neutral'
                          }
                          showDot
                          copyable={false}
                        />
                      </div>
                      <p className='text-muted-foreground text-xs'>
                        {t('Added on {{date}}', {
                          date: dayjs(credential.created_at).format(
                            'YYYY-MM-DD HH:mm'
                          ),
                        })}
                        {' · '}
                        {t('Last used:')}{' '}
                        {credential.last_used_at
                          ? dayjs(credential.last_used_at).fromNow()
                          : t('Not used yet')}
                      </p>
                    </div>
                    <Button
                      variant='outline'
                      size='sm'
                      className='text-destructive shrink-0'
                      disabled={removing || registering}
                      aria-label={t('Remove {{name}}', {
                        name: passkeyLabel(credential),
                      })}
                      onClick={() => {
                        setRemoveTarget(credential)
                        setConfirmOpen(true)
                      }}
                    >
                      <AlertTriangle className='mr-2 h-4 w-4' />
                      {t('Remove')}
                    </Button>
                  </div>
                ))}
              </div>
            )}

            <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
              <AlertDialogContent>
                <AlertDialogHeader>
                  <AlertDialogTitle>{t('Remove Passkey?')}</AlertDialogTitle>
                  <AlertDialogDescription>
                    {removeTarget && (
                      <>
                        {passkeyLabel(removeTarget)}:{' '}
                        {credentials.length === 1
                          ? t(
                              'Removing Passkey will require you to sign in with your password next time. You can re-register anytime.'
                            )
                          : t(
                              'This Passkey will stop working for sign-in. Your other Passkeys will remain available.'
                            )}
                      </>
                    )}
                  </AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                  <AlertDialogCancel disabled={removing}>
                    {t('Cancel')}
                  </AlertDialogCancel>
                  <AlertDialogAction
                    variant='destructive'
                    disabled={removing || !removeTarget}
                    onClick={(event) => {
                      event.preventDefault()
                      handleRemove()
                    }}
                  >
                    {t('Remove')}
                  </AlertDialogAction>
                </AlertDialogFooter>
              </AlertDialogContent>
            </AlertDialog>

            {showUnsupportedNotice && (
              <div className='bg-muted/60 text-muted-foreground flex items-start gap-3 rounded-md p-4 text-sm'>
                <ShieldAlert className='console-status-warning-icon mt-0.5 h-4 w-4 flex-shrink-0' />
                <div>
                  <p className='text-foreground font-medium'>
                    {t('Passkey not supported on this device')}
                  </p>
                  <p>
                    {t(
                      'Use a compatible browser or device with biometric authentication or a security key to register a Passkey.'
                    )}
                  </p>
                </div>
              </div>
            )}
          </div>
        </CardContent>
      </Card>

      <SecureVerificationDialog
        open={verificationOpen}
        onOpenChange={handleVerificationOpenChange}
        methods={dialogMethods}
        state={verificationState}
        onVerify={handleDialogVerify}
        onCancel={handleVerificationCancel}
        onCodeChange={setCode}
        onMethodChange={switchMethod}
        onSendEmailCode={sendEmailCode}
        emailCodeSending={emailCodeSending}
        emailCodeSent={emailCodeSent}
      />
    </>
  )
}
