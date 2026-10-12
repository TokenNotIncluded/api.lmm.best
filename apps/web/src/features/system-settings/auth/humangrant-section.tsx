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
import {
  readHumanGrantSettings,
  writeHumanGrantSettings,
  type HumanGrantSettings,
} from '@/features/auth/lib/humangrant'

export function HumanGrantSection() {
  const { t } = useTranslation()
  const [settings, setSettings] = useState<HumanGrantSettings | null>(null)
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState('')
  const request = useRef<AbortController | null>(null)
  // Ref guards against a second submission before React renders disabled.
  const saving = useRef(false)

  const reload = useCallback(async () => {
    if (saving.current) return
    request.current?.abort()
    const active = new AbortController()
    request.current = active
    const timer = setTimeout(() => active.abort(), 10_000)
    setBusy(true)
    setSettings(null)
    setMessage('')
    try {
      const current = await readHumanGrantSettings(active.signal)
      active.signal.throwIfAborted()
      if (request.current === active && !active.signal.aborted) {
        setSettings(current)
      }
    } catch {
      if (request.current === active) {
        setMessage(
          'HumanGrant settings could not be loaded. Sign in again or retry.'
        )
      }
    } finally {
      clearTimeout(timer)
      if (request.current === active) {
        request.current = null
        setBusy(false)
      }
    }
  }, [])

  useEffect(() => {
    void reload()
    return () => {
      request.current?.abort()
      request.current = null
    }
  }, [reload])

  async function save() {
    if (saving.current || busy || settings === null) return
    saving.current = true
    request.current?.abort()
    const active = new AbortController()
    request.current = active
    const timer = setTimeout(() => active.abort(), 10_000)
    setBusy(true)
    setMessage('')
    try {
      const current = await writeHumanGrantSettings(settings, active.signal)
      active.signal.throwIfAborted()
      if (request.current === active && !active.signal.aborted) {
        setSettings(current)
        setMessage('HumanGrant settings saved.')
      }
    } catch {
      if (request.current === active) {
        // UNKNOWN writes require reconciliation; never retry without a new proof.
        setSettings(null)
        setMessage(
          'HumanGrant settings could not be saved. Reload the settings and retry.'
        )
      }
    } finally {
      clearTimeout(timer)
      saving.current = false
      if (request.current === active) {
        request.current = null
        setBusy(false)
      }
    }
  }

  return (
    <section className='flex flex-col gap-5' aria-busy={busy}>
      <h2 className='text-lg font-medium'>HumanGrant</h2>
      <p className='text-muted-foreground'>
        {t(
          'HumanGrant lets an AI act with approved, limited access. It does not share your login session.'
        )}
      </p>
      <label className='flex items-center gap-3'>
        <input
          type='checkbox'
          className='size-5'
          checked={settings?.enabled === true}
          disabled={busy || settings === null}
          onChange={(event) => {
            const enabled = event.currentTarget.checked
            setSettings((current) =>
              current === null ? null : { ...current, enabled }
            )
            setMessage('')
          }}
        />
        {t('Enable HumanGrant')}
      </label>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Enabled by default. Turning it off stops new requests and invalidates existing AI grants. Turning it on does not restore old grants.'
        )}
      </p>
      <div className='flex flex-wrap gap-3'>
        <Button type='button' onClick={save} disabled={busy || settings === null}>
          {t('Save')}
        </Button>
        <Button
          type='button'
          variant='outline'
          disabled={busy}
          onClick={() => {
            void reload()
          }}
        >
          {t('Reload HumanGrant settings')}
        </Button>
      </div>
      <p role='status' aria-live='polite'>
        {message ? t(message) : null}
      </p>
      <a className='underline' href='/human/grants'>
        {t('Manage my AI grants')}
      </a>
    </section>
  )
}
