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
import { toast } from 'sonner'

import { Switch } from '@/components/ui/switch'
import { api } from '@/lib/api'

import {
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'

/** HumanGrant is a separate Rust-owned setting, not an unguarded Go option. */
export function HumanGrantSetting() {
  const { t } = useTranslation()
  const [enabled, setEnabled] = useState(false)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    let active = true
    void (async () => {
      try {
        const { data } = await api.get<{ enabled: boolean }>(
          '/api/humangrant/settings',
          {
            skipErrorHandler: true,
            skipBusinessError: true,
          }
        )
        if (active) setEnabled(data.enabled === true)
      } catch {
        if (active) toast.error(t('Unable to load HumanGrant setting'))
      } finally {
        if (active) setLoading(false)
      }
    })()
    return () => {
      active = false
    }
  }, [t])

  const update = async (next: boolean) => {
    setSaving(true)
    try {
      const { data } = await api.put<{ enabled: boolean }>(
        '/api/humangrant/settings',
        { enabled: next },
        { skipErrorHandler: true }
      )
      setEnabled(data.enabled === true)
      toast.success(t('HumanGrant setting saved'))
    } catch {
      toast.error(t('Unable to save HumanGrant setting'))
    } finally {
      setSaving(false)
    }
  }

  return (
    <SettingsSwitchItem>
      <SettingsSwitchContent>
        <div className='text-sm font-medium'>
          {t('HumanGrant remote authorization')}
        </div>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Allow AI clients to request limited access with explicit human approval. Existing login methods stay available.'
          )}
        </p>
      </SettingsSwitchContent>
      <Switch
        aria-label={t('HumanGrant remote authorization')}
        checked={enabled}
        disabled={loading || saving}
        onCheckedChange={(value) => {
          void update(value)
        }}
      />
    </SettingsSwitchItem>
  )
}
