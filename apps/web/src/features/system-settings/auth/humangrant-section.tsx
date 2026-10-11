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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'

const endpoint = '/api/user/auth/humangrant/settings'
export function HumanGrantSection() {
  const { t } = useTranslation()
  const [enabled, setEnabled] = useState<boolean | null>(null)
  const [csrf, setCSRF] = useState('')
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState('')
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    const controller = new AbortController()
    setEnabled(null); setCSRF(''); setMessage('')
    fetch(endpoint, { credentials: 'same-origin', cache: 'no-store', signal: controller.signal })
      .then(async response => { if (!response.ok) throw new Error('load'); return response.json() })
      .then(data => {
        if (typeof data.enabled !== 'boolean' || typeof data.csrf !== 'string') throw new Error('load')
        if (!controller.signal.aborted) { setEnabled(data.enabled); setCSRF(data.csrf) }
      })
      .catch(() => { if (!controller.signal.aborted) setMessage('HumanGrant settings could not be loaded. Sign in again or retry.') })
    return () => controller.abort()
  }, [revision])
  async function save() {
    if (enabled === null || !csrf) return
    setBusy(true); setMessage('')
    try {
      // Do not use the dashboard API interceptor. Only the HttpOnly human login
      // cookie and the page-bound CSRF proof are accepted by this endpoint.
      const response = await fetch(endpoint, { method: 'PUT', credentials: 'same-origin', cache: 'no-store',
        headers: { 'Content-Type': 'application/json', 'X-HG-CSRF': csrf }, body: JSON.stringify({ enabled }) })
      if (!response.ok) throw new Error('save')
      setMessage('HumanGrant settings saved.')
    } catch { setMessage('HumanGrant settings could not be saved. Reload the settings and retry.') }
    finally { setBusy(false) }
  }
  return (
    <section className='flex flex-col gap-5'>
      <h2 className='text-lg font-medium'>HumanGrant</h2>
      <p className='text-muted-foreground'>{t('HumanGrant lets an AI act with approved, limited access. It does not share your login session.')}</p>
      <label className='flex items-center gap-3'>
        <input type='checkbox' className='size-5' checked={enabled === true} disabled={busy || enabled === null}
          onChange={event => setEnabled(event.currentTarget.checked)} />
        {t('Enable HumanGrant')}
      </label>
      <p className='text-muted-foreground text-sm'>{t('Enabled by default. Turning it off stops new requests and invalidates existing AI grants. Turning it on does not restore old grants.')}</p>
      <div className='flex flex-wrap gap-3'>
        <Button type='button' onClick={save} disabled={busy || enabled === null}>{t('Save')}</Button>
        <Button type='button' variant='outline' disabled={busy} onClick={() => setRevision(value => value + 1)}>{t('Reload HumanGrant settings')}</Button>
      </div>
      <p role='status' aria-live='polite'>{message ? t(message) : null}</p>
      <a className='underline' href='/human/grants'>{t('Manage my AI grants')}</a>
    </section>
  )
}
