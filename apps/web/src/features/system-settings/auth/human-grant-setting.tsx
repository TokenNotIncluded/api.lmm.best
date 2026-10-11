import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Switch } from '@/components/ui/switch'
import { api } from '@/lib/api'

import { SettingsSwitchContent, SettingsSwitchItem } from '../components/settings-form-layout'

/** HumanGrant is a separate Rust-owned setting, not an unguarded Go option. */
export function HumanGrantSetting() {
  const { t } = useTranslation()
  const [enabled, setEnabled] = useState(false)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    let active = true
    api.get<{ enabled: boolean }>('/api/humangrant/settings', {
      skipErrorHandler: true,
      skipBusinessError: true,
    }).then(({ data }) => {
      if (active) setEnabled(data.enabled === true)
    }).catch(() => {
      if (active) toast.error(t('Unable to load HumanGrant setting'))
    }).finally(() => {
      if (active) setLoading(false)
    })
    return () => { active = false }
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
        <div className='text-sm font-medium'>{t('HumanGrant remote authorization')}</div>
        <p className='text-muted-foreground text-sm'>
          {t('Allow AI clients to request limited access with explicit human approval. Existing login methods stay available.')}
        </p>
      </SettingsSwitchContent>
      <Switch
        aria-label={t('HumanGrant remote authorization')}
        checked={enabled}
        disabled={loading || saving}
        onCheckedChange={(value) => { void update(value) }}
      />
    </SettingsSwitchItem>
  )
}
