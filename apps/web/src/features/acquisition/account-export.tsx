import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { hasPermission } from '@/lib/admin-permissions'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

export function SourceAccountExport({
  source,
  from,
  to,
}: {
  source: string
  from: number
  to: number
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(false)
  if (
    !hasPermission(user, 'acquisition', 'details') ||
    !hasPermission(user, 'acquisition', 'export')
  ) {
    return null
  }
  const download = async () => {
    setBusy(true)
    setError(false)
    try {
      const params = new URLSearchParams({
        source,
        from: String(from),
        to: String(to),
      })
      const response = await api.get(
        `/api/admin/acquisition/users/export?${params}`,
        { responseType: 'blob', skipBusinessError: true }
      )
      const blob = response.data as unknown as Blob
      if (!(blob instanceof Blob) || !blob.type.startsWith('text/csv')) {
        throw new Error()
      }
      const url = URL.createObjectURL(blob)
      const anchor = document.createElement('a')
      anchor.href = url
      anchor.download = 'acquisition-accounts.csv'
      document.body.append(anchor)
      anchor.click()
      anchor.remove()
      setTimeout(() => URL.revokeObjectURL(url), 1000)
    } catch {
      setError(true)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className='space-y-2'>
      <Button
        type='button'
        variant='outline'
        disabled={busy}
        onClick={() => void download()}
      >
        {t('Export filtered account details')}
      </Button>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Exports account IDs and conversion status for the current source and registration period, without email addresses. Maximum 10,000 accounts.'
        )}
      </p>
      {error && (
        <p role='alert' className='text-destructive text-sm'>
          {t(
            'Account export failed. Check export permission or narrow the registration period.'
          )}
        </p>
      )}
    </div>
  )
}
