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
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { sourceEntry } from './source-entry'

const CONSENT_KEY = 'lmm:source-consent:v2'

function preference() {
  try {
    return localStorage.getItem(CONSENT_KEY) ?? 'ask'
  } catch {
    return 'no'
  }
}
export function SourceConsent() {
  const { t } = useTranslation()
  const userID = useAuthStore((state) => state.auth.user?.id)
  const [choice, setChoice] = useState(preference)
  const [entry] = useState(() =>
    sourceEntry(window.location.href, document.referrer)
  )
  const [expanded, setExpanded] = useState(false)
  const [saving, setSaving] = useState(false)
  const [failed, setFailed] = useState<'grant' | 'withdraw' | null>(null)
  const pending = useRef(new Set<Promise<unknown>>())
  const trackingBlocked = navigator.doNotTrack === '1'
  const withdraw = useCallback(async () => {
    await Promise.allSettled(pending.current)
    const response = await api.delete('/api/acquisition/consent', {
      skipErrorHandler: true,
      skipBusinessError: true,
    })
    if (response.data?.success === false) {
      throw new Error('Privacy withdrawal failed')
    }
  }, [])
  useEffect(() => {
    if (
      choice !== 'yes' ||
      !entry ||
      trackingBlocked ||
      __LMM_PERSONA_DEBUG__
    ) {
      return
    }
    const work = api
      .post('/api/acquisition/visit', entry, {
        skipAuthRefresh: true,
        skipErrorHandler: true,
        skipBusinessError: true,
      })
      .catch(() => undefined)
    pending.current.add(work)
    void work.finally(() => pending.current.delete(work))
  }, [choice, entry, userID, trackingBlocked])
  useEffect(() => {
    if (!trackingBlocked || choice !== 'yes') return
    void withdraw()
      .then(() => {
        setChoice('no')
        try {
          localStorage.setItem(CONSENT_KEY, 'no')
        } catch {
          /* Optional storage. */
        }
      })
      .catch(() => {
        setExpanded(true)
        setFailed('withdraw')
      })
  }, [trackingBlocked, choice, withdraw, userID])
  const choose = async (value: 'yes' | 'no') => {
    setFailed(null)
    setSaving(true)
    setExpanded(true)
    if (value === 'no') {
      setChoice('no')
      try {
        localStorage.setItem(CONSENT_KEY, 'no')
      } catch {
        /* Optional storage. */
      }
    }
    try {
      if (value === 'yes') {
        if (userID) {
          const response = await api.post(
            '/api/acquisition/consent',
            {},
            { skipBusinessError: true, skipErrorHandler: true }
          )
          if (!response.data.success) throw new Error('Consent was not saved')
        }
        setChoice('yes')
        try {
          localStorage.setItem(CONSENT_KEY, 'yes')
        } catch {
          /* Optional storage. */
        }
      } else {
        await withdraw()
      }
      setExpanded(false)
    } catch {
      setFailed(value === 'yes' ? 'grant' : 'withdraw')
    } finally {
      setSaving(false)
    }
  }
  if ((!entry || choice !== 'ask' || trackingBlocked) && !expanded) {
    return (
      <button
        type='button'
        className='text-muted-foreground mx-4 my-2 text-xs underline'
        onClick={() => setExpanded(true)}
      >
        {t('Source privacy')}
      </button>
    )
  }
  return (
    <section
      className='bg-background relative z-50 border-t p-4 text-sm'
      aria-label={t('Source privacy')}
    >
      <p className='max-w-3xl'>
        {t(
          'Allow analysis of sources, registrations, successful API use and payments? Entry records are kept for 90 days; account attribution and daily activity for 365 days. No API keys, messages, IP addresses or device fingerprints. Optional; access is unchanged.'
        )}
      </p>
      {failed && (
        <p className='mt-2' role='alert'>
          {t(
            failed === 'withdraw'
              ? 'Unable to save privacy settings. Please retry to stop server-side analytics.'
              : 'Failed to update setting'
          )}
        </p>
      )}
      <div className='mt-3 flex flex-wrap gap-2'>
        <Button
          size='sm'
          disabled={saving || trackingBlocked}
          onClick={() => void choose('yes')}
        >
          {t('Allow source analytics')}
        </Button>
        <Button
          size='sm'
          variant='outline'
          disabled={saving}
          onClick={() => void choose('no')}
        >
          {t(saving ? 'Saving' : 'Do not collect')}
        </Button>
      </div>
    </section>
  )
}
