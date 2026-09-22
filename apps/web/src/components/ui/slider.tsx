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
// Luma visual recipes adapted from shadcn/ui (MIT); see LUMA-LICENSE.txt.
import { Slider as SliderPrimitive } from '@base-ui/react/slider'

import { cn } from '@/lib/utils'

function Slider({
  className,
  defaultValue,
  value,
  min = 0,
  max = 100,
  ...props
}: SliderPrimitive.Root.Props) {
  const _values = Array.isArray(value)
    ? value
    : Array.isArray(defaultValue)
      ? defaultValue
      : [min, max]

  return (
    <SliderPrimitive.Root
      className={cn(
        'data-horizontal:w-full data-vertical:h-full data-vertical:min-h-40',
        className
      )}
      data-slot='slider'
      defaultValue={defaultValue}
      value={value}
      min={min}
      max={max}
      thumbAlignment='edge'
      {...props}
    >
      <SliderPrimitive.Control className='relative flex w-full touch-none items-center select-none data-disabled:opacity-50 data-vertical:h-full data-vertical:min-h-40 data-vertical:w-auto data-vertical:flex-col'>
        <SliderPrimitive.Track
          data-slot='slider-track'
          className={
            'bg-input/90 relative grow overflow-hidden rounded-full select-none data-horizontal:h-2 data-horizontal:w-full data-vertical:h-full data-vertical:w-2'
          }
        >
          <SliderPrimitive.Indicator
            data-slot='slider-range'
            className={
              'bg-primary select-none data-horizontal:h-full data-vertical:w-full'
            }
          />
        </SliderPrimitive.Track>
        {Array.from({ length: _values.length }, (_, index) => (
          <SliderPrimitive.Thumb
            data-slot='slider-thumb'
            key={index}
            className={
              'border-ring ring-foreground/10 bg-background hover:ring-ring/30 focus-visible:ring-ring/30 relative block h-4 w-6 shrink-0 rounded-full border shadow-md ring-1 transition-[color,box-shadow,background-color] select-none not-dark:bg-clip-padding after:absolute after:-inset-2 hover:ring-4 focus-visible:ring-4 focus-visible:outline-hidden active:ring-3 disabled:pointer-events-none disabled:opacity-50 data-vertical:h-6 data-vertical:w-4 motion-reduce:transition-none'
            }
          />
        ))}
      </SliderPrimitive.Control>
    </SliderPrimitive.Root>
  )
}

export { Slider }
