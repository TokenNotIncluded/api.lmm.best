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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useLocation } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { ForgePublicShell } from '@/features/forge/forge-public-shell'
import { useAuthStore } from '@/stores/auth-store'

import { claimTransfer, inspectTransfer, formatTransferQuota } from './api'

const pendingTransferKey = 'wallet-transfer-login'
export function ClaimTransfer() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const location = useLocation()
  const hash = location.hash.replace(/^#/, '')
  const [savedToken] = useState(
    () => sessionStorage.getItem(pendingTransferKey) ?? ''
  )
  const token = /^[a-f0-9]{64}$/.test(hash) ? hash : savedToken
  const valid = /^[a-f0-9]{64}$/.test(token)
  const client = useQueryClient()
  // Keep the bearer secret out of the login redirect query (and server logs).
  useEffect(() => {
    if (valid && !user) sessionStorage.setItem(pendingTransferKey, token)
  }, [valid, user, token])
  const key = ['wallet-transfer-receipt', user?.id, token]
  const receipt = useQuery({
    queryKey: key,
    queryFn: () => inspectTransfer(token),
    enabled: !!user && valid,
    retry: false,
    refetchInterval: user && valid ? 10000 : false,
  })
  const claim = useMutation({
    mutationFn: () => claimTransfer(token),
    onSuccess: (data) => {
      client.setQueryData(key, data)
      sessionStorage.removeItem(pendingTransferKey)
      toast.success(t('Transfer claimed. Your wallet has been credited.'))
    },
    onError: (err: Error) => {
      toast.error(t(err.message))
      void receipt.refetch()
    },
  })
  const data = receipt.data
  return (
    <ForgePublicShell>
      <main className='mx-auto grid w-full max-w-lg gap-5 p-6'>
        <h1 className='text-2xl font-semibold'>{t('Wallet transfer')}</h1>
        <p className='text-destructive text-sm'>
          {t('Do not share this link publicly.')}
        </p>
        {!valid ? (
          <p role='alert'>{t('Invalid transfer link.')}</p>
        ) : !user ? (
          <>
            <p>{t('Sign in to claim this transfer.')}</p>
            <Button
              render={<Link to='/sign-in' search={{ redirect: '/transfer' }} />}
            >
              {t('Sign in')}
            </Button>
          </>
        ) : receipt.isPending ? (
          <p role='status'>{t('Loading...')}</p>
        ) : receipt.isError ? (
          <>
            <p role='alert'>{t('Unable to load this transfer.')}</p>
            <Button variant='outline' onClick={() => void receipt.refetch()}>
              {t('Retry')}
            </Button>
          </>
        ) : data ? (
          <>
            <p className='text-3xl font-semibold'>
              {formatTransferQuota(data.quota)}
            </p>
            <p>
              {t('Created at')}:{' '}
              {new Date(data.created_at * 1000).toLocaleString()}
            </p>
            {data.status === 'pending' ? (
              data.is_sender ? (
                <p>{t('You cannot claim your own transfer.')}</p>
              ) : (
                <>
                  <p>
                    {t(
                      'Claiming shares your user ID, name and email with the sender.'
                    )}
                  </p>
                  <Button
                    disabled={claim.isPending}
                    onClick={() => claim.mutate()}
                  >
                    {claim.isPending ? t('Claiming...') : t('Claim transfer')}
                  </Button>
                </>
              )
            ) : (
              <p role='status'>
                {data.status === 'cancelled'
                  ? t('This transfer was cancelled.')
                  : data.claimed_by_me
                    ? t('Transfer claimed. Your wallet has been credited.')
                    : t('This transfer has already been claimed.')}
              </p>
            )}
            {data.claimed_at > 0 && (
              <p>
                {t('Claimed at')}:{' '}
                {new Date(data.claimed_at * 1000).toLocaleString()}
              </p>
            )}
            <Button variant='outline' render={<Link to='/wallet' />}>
              {t('Wallet')}
            </Button>
          </>
        ) : null}
      </main>
    </ForgePublicShell>
  )
}
