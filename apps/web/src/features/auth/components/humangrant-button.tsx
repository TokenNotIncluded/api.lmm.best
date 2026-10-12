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

import { Button } from '@/components/ui/button'

import { watchHumanGrantAvailability } from '../lib/humangrant'

export function HumanGrantButton({ disabled = false }: { disabled?: boolean }) {
  const [available, setAvailable] = useState(false)

  useEffect(() => {
    const watcher = watchHumanGrantAvailability(setAvailable)
    const refresh = () => {
      void watcher.refresh()
    }
    refresh()
    window.addEventListener('focus', refresh)
    return () => {
      watcher.dispose()
      window.removeEventListener('focus', refresh)
    }
  }, [])

  if (!available) return null
  return (
    <Button
      type='button'
      variant='outline'
      disabled={disabled}
      className='h-auto min-h-11 w-full justify-center rounded-xl px-3 py-2 whitespace-normal'
      onClick={() => window.location.assign('/humangrant')}
    >
      Continue with human beings
    </Button>
  )
}
