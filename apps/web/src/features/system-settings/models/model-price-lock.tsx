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
import { LockKeyhole, LockKeyholeOpen } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

export function ModelPriceLockButton({
  name,
  locked,
  pending,
  onToggle,
}: {
  name: string
  locked: boolean
  pending?: boolean
  onToggle: () => void
}) {
  const { t } = useTranslation()
  const label = t(
    locked ? 'Unlock {{name}} pricing' : 'Lock {{name}} pricing',
    { name }
  )
  const Icon = locked ? LockKeyhole : LockKeyholeOpen
  return (
    <Button
      type='button'
      variant='ghost'
      size='icon-sm'
      aria-label={label}
      title={label}
      aria-pressed={locked}
      disabled={pending}
      onClick={(event) => {
        event.stopPropagation()
        onToggle()
      }}
    >
      <Icon className='size-4' />
    </Button>
  )
}
