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
import {
  ArrowDown01Icon,
  Logout01Icon,
  SmartPhone01Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import { Label } from '@/components/ui/label'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { clearAuthenticatedClientState } from '@/lib/api'
import type { LoginSession } from '@/stores/auth-store'

import {
  getLoginSessions,
  revokeLoginSession,
  revokeOtherLoginSessions,
  updateLoginSessionSettings,
} from '../api'
import { LoginSessionDialogs } from './login-session-dialogs'
import { LoginSessionItem } from './login-session-item'

const sessionQueryKey = ['profile', 'login-sessions'] as const

export function LoginSessionsCard() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [sessionsOpen, setSessionsOpen] = useState(false)
  const [revokeTarget, setRevokeTarget] = useState<LoginSession | null>(null)
  const [confirmOthers, setConfirmOthers] = useState(false)

  const sessionsQuery = useQuery({
    queryKey: sessionQueryKey,
    queryFn: async () => {
      const response = await getLoginSessions()
      if (!response.success) {
        throw new Error(response.message || t('Failed to load login sessions'))
      }
      return {
        sessions: response.data ?? [],
        sessionAutoLogout: response.session_auto_logout ?? true,
      }
    },
  })

  const revokeMutation = useMutation({
    mutationFn: async (sid: string) => {
      const response = await revokeLoginSession(sid)
      if (!response.success) {
        throw new Error(response.message || t('Failed to sign out session'))
      }
      return sid
    },
    onSuccess: async (sid) => {
      const revokedCurrent = sessionsQuery.data?.sessions.some(
        (session) => session.sid === sid && session.current
      )
      setRevokeTarget(null)
      if (revokedCurrent) {
        clearAuthenticatedClientState(queryClient)
        void navigate({ to: '/sign-in', replace: true })
        return
      }
      toast.success(t('Session signed out'))
      await queryClient.invalidateQueries({ queryKey: sessionQueryKey })
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const revokeOthersMutation = useMutation({
    mutationFn: async () => {
      const response = await revokeOtherLoginSessions()
      if (!response.success) {
        throw new Error(
          response.message || t('Failed to sign out other sessions')
        )
      }
    },
    onSuccess: async () => {
      setConfirmOthers(false)
      toast.success(t('Other sessions signed out'))
      await queryClient.invalidateQueries({ queryKey: sessionQueryKey })
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const settingsMutation = useMutation({
    mutationFn: async (enabled: boolean) => {
      const response = await updateLoginSessionSettings(enabled)
      if (!response.success) {
        throw new Error(
          response.message || t('Failed to update login session settings')
        )
      }
    },
    onSuccess: async () => {
      toast.success(t('Login session settings updated'))
      await queryClient.invalidateQueries({ queryKey: sessionQueryKey })
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const sessions = sessionsQuery.data?.sessions ?? []
  const hasOtherSessions = sessions.some((session) => !session.current)
  let sessionsContent: ReactNode
  if (sessionsQuery.isLoading) {
    sessionsContent = (
      <div className='flex flex-col gap-3'>
        <Skeleton className='h-20 w-full' />
        <Skeleton className='h-20 w-full' />
      </div>
    )
  } else if (sessionsQuery.isError) {
    sessionsContent = (
      <Empty>
        <EmptyHeader>
          <EmptyMedia variant='icon'>
            <HugeiconsIcon icon={SmartPhone01Icon} strokeWidth={2} />
          </EmptyMedia>
          <EmptyTitle>{t('Unable to load login sessions')}</EmptyTitle>
          <EmptyDescription>
            {t('Refresh the list and try again.')}
          </EmptyDescription>
        </EmptyHeader>
        <Button
          type='button'
          variant='outline'
          onClick={() => sessionsQuery.refetch()}
        >
          {t('Retry')}
        </Button>
      </Empty>
    )
  } else if (sessions.length === 0) {
    sessionsContent = (
      <Empty>
        <EmptyHeader>
          <EmptyMedia variant='icon'>
            <HugeiconsIcon icon={SmartPhone01Icon} strokeWidth={2} />
          </EmptyMedia>
          <EmptyTitle>{t('No active login sessions')}</EmptyTitle>
        </EmptyHeader>
      </Empty>
    )
  } else {
    sessionsContent = (
      <div className='flex flex-col'>
        {sessions.map((session, index) => (
          <div key={session.sid}>
            {index > 0 && <Separator />}
            <LoginSessionItem session={session} onRevoke={setRevokeTarget} />
          </div>
        ))}
      </div>
    )
  }

  return (
    <>
      <Collapsible open={sessionsOpen} onOpenChange={setSessionsOpen}>
        <Card data-card-hover='false'>
          <CardHeader>
            <CardTitle>{t('Login sessions')}</CardTitle>
            <CardDescription>
              {t('Review and sign out devices currently using your account.')}
            </CardDescription>
            <CardAction className='flex items-center gap-2'>
              <Button
                type='button'
                variant='outline'
                size='sm'
                disabled={!hasOtherSessions || revokeOthersMutation.isPending}
                onClick={() => setConfirmOthers(true)}
              >
                <HugeiconsIcon
                  icon={Logout01Icon}
                  data-icon='inline-start'
                  strokeWidth={2}
                />
                {t('Sign out other sessions')}
              </Button>
              <CollapsibleTrigger
                render={
                  <Button
                    type='button'
                    variant='outline'
                    size='icon-sm'
                    aria-label={t(sessionsOpen ? 'Collapse' : 'Expand')}
                  />
                }
              >
                <HugeiconsIcon
                  icon={ArrowDown01Icon}
                  strokeWidth={2}
                  aria-hidden='true'
                  className={`transition-transform duration-200 motion-reduce:transition-none ${sessionsOpen ? 'rotate-180' : ''}`}
                />
              </CollapsibleTrigger>
            </CardAction>
          </CardHeader>
          <CardContent>
            <div className='flex items-start justify-between gap-4'>
              <div className='space-y-1'>
                <Label htmlFor='session-auto-logout'>
                  {t('Automatically sign out sessions after one week')}
                </Label>
                <p
                  id='session-auto-logout-description'
                  className='text-muted-foreground text-sm'
                >
                  {t(
                    'Enabled by default. Sessions are signed out one week after login, even if they are still active, including this device.'
                  )}
                </p>
              </div>
              <Switch
                id='session-auto-logout'
                aria-describedby='session-auto-logout-description'
                checked={sessionsQuery.data?.sessionAutoLogout ?? true}
                disabled={
                  !sessionsQuery.isSuccess ||
                  sessionsQuery.isFetching ||
                  settingsMutation.isPending
                }
                onCheckedChange={(enabled) => settingsMutation.mutate(enabled)}
              />
            </div>
          </CardContent>
          <CollapsibleContent>
            <CardContent>{sessionsContent}</CardContent>
          </CollapsibleContent>
        </Card>
      </Collapsible>

      <LoginSessionDialogs
        revokeTarget={revokeTarget}
        confirmOthers={confirmOthers}
        revoking={revokeMutation.isPending}
        revokingOthers={revokeOthersMutation.isPending}
        onRevokeTargetChange={setRevokeTarget}
        onConfirmOthersChange={setConfirmOthers}
        onRevoke={() => {
          if (revokeTarget) revokeMutation.mutate(revokeTarget.sid)
        }}
        onRevokeOthers={() => revokeOthersMutation.mutate()}
      />
    </>
  )
}
