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
import { useNavigate } from '@tanstack/react-router'
import { Loader2, Pencil, ShieldAlert } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { PasswordInput } from '@/components/password-input'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardFooter,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { logout } from '@/features/auth/api'
import { clearAuthentication } from '@/lib/api'

import {
  executeAssistantUserAction,
  getAssistantDisplayNameValidationError,
  type AssistantUserAction,
} from './api'

export function AssistantUserActionTool(props: {
  action: AssistantUserAction
  onCompleted: () => void
  onUpdated?: () => void | Promise<void>
}) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [loading, setLoading] = useState(false)
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [usernameConfirmation, setUsernameConfirmation] = useState('')
  const [displayName, setDisplayName] = useState(() =>
    props.action.type === 'user_display_name_change'
      ? props.action.proposed_display_name
      : ''
  )
  const [error, setError] = useState<string | null>(null)
  const isDisplayNameChange = props.action.type === 'user_display_name_change'

  const targetLabel = props.action.target_display_name
    ? `${props.action.target_display_name} (${props.action.target_username})`
    : props.action.target_username

  const submit = async () => {
    if (loading) return
    setError(null)
    if (isDisplayNameChange) {
      const validationError =
        getAssistantDisplayNameValidationError(displayName)
      if (validationError) {
        setError(t(validationError))
        return
      }
    }
    if (props.action.type === 'user_password_change') {
      if (props.action.target_is_self && !currentPassword) {
        toast.error(t('Please enter your current password'))
        return
      }
      if (!newPassword) {
        toast.error(t('Please enter a new password'))
        return
      }
      if (newPassword.length < 8) {
        toast.error(t('Password must be at least 8 characters'))
        return
      }
      if (newPassword !== confirmPassword) {
        toast.error(t('Passwords do not match'))
        return
      }
    }
    if (
      props.action.type === 'user_account_action' &&
      props.action.action === 'delete' &&
      usernameConfirmation !== props.action.target_username
    ) {
      toast.error(t('Username confirmation does not match'))
      return
    }

    try {
      setLoading(true)
      const result = await executeAssistantUserAction(props.action, {
        currentPassword,
        newPassword,
        displayName,
      })
      if (result.selfDeleted) {
        toast.success(t('Account deleted successfully'))
        try {
          await logout()
        } catch {
          // The account has already been deleted; local auth is still cleared.
        }
        clearAuthentication()
        navigate({ to: '/sign-in' })
        return
      }
      toast.success(
        props.action.type === 'user_password_change'
          ? t('Password changed successfully')
          : isDisplayNameChange
            ? t('Display name changed successfully')
            : t('User action completed')
      )
      try {
        await props.onUpdated?.()
      } catch {
        // The mutation succeeded. A later account refresh can retry without
        // reporting a failed change or retaining an already consumed token.
      }
      props.onCompleted()
    } catch (error) {
      const message = isDisplayNameChange
        ? error instanceof Error
          ? t(error.message)
          : t('Unable to change display name')
        : t('User action failed')
      if (isDisplayNameChange) setError(message)
      toast.error(message)
    } finally {
      setLoading(false)
    }
  }

  const title =
    props.action.type === 'user_display_name_change'
      ? t('Change display name')
      : props.action.type === 'user_password_change'
        ? t('Change password')
        : props.action.type === 'user_oauth_unbind'
          ? t('Unbind OAuth login')
          : props.action.action === 'delete'
            ? t('Delete user')
            : t('Disable user')

  return (
    <Card
      size='sm'
      className={isDisplayNameChange ? 'w-full' : 'border-warning/50 w-full'}
    >
      <CardHeader>
        <CardTitle className='flex items-center gap-2 text-sm'>
          {isDisplayNameChange ? (
            <Pencil className='size-4' aria-hidden='true' />
          ) : (
            <ShieldAlert className='size-4' aria-hidden='true' />
          )}
          {title}
        </CardTitle>
        <p className='text-muted-foreground text-sm'>
          {t('Target')}: <strong>{targetLabel}</strong>
        </p>
      </CardHeader>
      <CardContent className='space-y-4'>
        {!isDisplayNameChange ? (
          <Alert>
            <ShieldAlert className='size-4' aria-hidden='true' />
            <AlertTitle>{t('Confirmation required')}</AlertTitle>
            <AlertDescription>
              {t(
                'Review this account action carefully. It is sent through the normal authenticated API only after you confirm.'
              )}
            </AlertDescription>
          </Alert>
        ) : null}

        {isDisplayNameChange ? (
          <div className='space-y-2'>
            <Label htmlFor='assistant-display-name'>{t('Display Name')}</Label>
            <Input
              id='assistant-display-name'
              value={displayName}
              onChange={(event) => {
                setDisplayName(event.target.value)
                setError(null)
              }}
              placeholder={t('Enter display name')}
              autoComplete='nickname'
              aria-invalid={Boolean(error)}
              aria-describedby={
                error ? 'assistant-display-name-error' : undefined
              }
              disabled={loading}
            />
            {error ? (
              <p
                id='assistant-display-name-error'
                role='alert'
                className='text-destructive text-sm'
              >
                {error}
              </p>
            ) : null}
          </div>
        ) : null}

        {props.action.type === 'user_password_change' ? (
          <div className='space-y-3'>
            {props.action.target_is_self ? (
              <div className='space-y-2'>
                <Label htmlFor='assistant-current-password'>
                  {t('Current Password')}
                </Label>
                <PasswordInput
                  id='assistant-current-password'
                  value={currentPassword}
                  onChange={(event) => setCurrentPassword(event.target.value)}
                  autoComplete='current-password'
                  disabled={loading}
                />
              </div>
            ) : null}
            <div className='space-y-2'>
              <Label htmlFor='assistant-new-password'>
                {t('New Password')}
              </Label>
              <PasswordInput
                id='assistant-new-password'
                value={newPassword}
                onChange={(event) => setNewPassword(event.target.value)}
                autoComplete='new-password'
                minLength={8}
                disabled={loading}
              />
            </div>
            <div className='space-y-2'>
              <Label htmlFor='assistant-confirm-password'>
                {t('Confirm New Password')}
              </Label>
              <PasswordInput
                id='assistant-confirm-password'
                value={confirmPassword}
                onChange={(event) => setConfirmPassword(event.target.value)}
                autoComplete='new-password'
                minLength={8}
                disabled={loading}
              />
            </div>
            <p className='text-muted-foreground text-xs'>
              {t(
                'The password is never sent to the AI assistant or stored in chat.'
              )}
            </p>
          </div>
        ) : null}

        {props.action.type === 'user_oauth_unbind' ? (
          <p className='text-sm'>
            {t('OAuth provider')}:{' '}
            <strong>{props.action.provider_label}</strong>
          </p>
        ) : null}

        {props.action.type === 'user_account_action' &&
        props.action.action === 'delete' ? (
          <div className='space-y-2'>
            <Label htmlFor='assistant-delete-confirmation'>
              {t('Type')} <strong>{props.action.target_username}</strong>{' '}
              {t('to confirm')}
            </Label>
            <Input
              id='assistant-delete-confirmation'
              value={usernameConfirmation}
              onChange={(event) => setUsernameConfirmation(event.target.value)}
              placeholder={props.action.target_username}
              autoComplete='off'
              disabled={loading}
            />
          </div>
        ) : null}
      </CardContent>
      <CardFooter className='justify-end gap-2'>
        <Button
          type='button'
          variant='outline'
          onClick={props.onCompleted}
          disabled={loading}
        >
          {t('Cancel')}
        </Button>
        <Button
          type='button'
          variant={
            props.action.type === 'user_account_action'
              ? 'destructive'
              : 'default'
          }
          onClick={() => void submit()}
          disabled={loading}
        >
          {loading ? <Loader2 className='mr-2 size-4 animate-spin' /> : null}
          {t('Confirm')}
        </Button>
      </CardFooter>
    </Card>
  )
}
