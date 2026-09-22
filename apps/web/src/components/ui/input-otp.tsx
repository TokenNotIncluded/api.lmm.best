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
import { OTPField } from '@base-ui/react/otp-field'
import { Minus } from 'lucide-react'
import * as React from 'react'
import { useTranslation } from 'react-i18next'

import { cn } from '@/lib/utils'

const OTPPresentation = React.createContext<{
  length: number
  invalid?: React.AriaAttributes['aria-invalid']
  describedBy?: string
}>({ length: 6 })

function InputOTP({
  className,
  containerClassName,
  length,
  'aria-invalid': invalid,
  'aria-describedby': describedBy,
  ...props
}: OTPField.Root.Props & { containerClassName?: string }) {
  const presentation = React.useMemo(
    () => ({ length, invalid, describedBy }),
    [length, invalid, describedBy]
  )
  return (
    <OTPPresentation.Provider value={presentation}>
      <OTPField.Root
        data-slot='input-otp'
        length={length}
        aria-invalid={invalid}
        aria-describedby={describedBy}
        className={cn('flex min-w-0 items-center gap-2 has-disabled:opacity-50', containerClassName, className)}
        {...props}
      />
    </OTPPresentation.Provider>
  )
}

function InputOTPGroup({ className, ...props }: React.ComponentProps<'div'>) {
  return <div data-slot='input-otp-group'
    className={cn('flex min-w-0 items-center gap-1', className)} {...props} />
}

function InputOTPSlot({ index, className, ...props }: OTPField.Input.Props & { index: number }) {
  const { t } = useTranslation()
  const { length, invalid, describedBy } = React.useContext(OTPPresentation)
  return (
    <OTPField.Input
      data-slot='input-otp-slot'
      aria-invalid={invalid}
      aria-describedby={describedBy}
      aria-label={index === 0 ? undefined : `${t('Verification Code')} ${index + 1} / ${length}`}
      className={cn(
        'bg-input/50 border-transparent text-foreground size-10 min-w-0 rounded-xl border text-center text-base tabular-nums outline-none transition-[border-color,box-shadow,background-color] focus-visible:border-ring focus-visible:ring-ring/30 focus-visible:ring-3 aria-invalid:border-destructive aria-invalid:ring-destructive/20 disabled:cursor-not-allowed motion-reduce:transition-none sm:size-12',
        className
      )}
      {...props}
    />
  )
}

function InputOTPSeparator({ className, ...props }: OTPField.Separator.Props) {
  return <OTPField.Separator data-slot='input-otp-separator' aria-hidden='true'
    className={cn('text-muted-foreground flex shrink-0 items-center', className)} {...props}>
    <Minus className='size-3' />
  </OTPField.Separator>
}

export { InputOTP, InputOTPGroup, InputOTPSlot, InputOTPSeparator }
