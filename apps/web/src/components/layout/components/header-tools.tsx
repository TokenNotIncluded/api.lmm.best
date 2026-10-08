/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { Ellipsis } from 'lucide-react'
import {
  Fragment,
  useEffect,
  useEffectEvent,
  useState,
  type ReactNode,
} from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Popover,
  PopoverContent,
  PopoverTitle,
  PopoverTrigger,
} from '@/components/ui/popover'
import { useIsMobile } from '@/hooks/use-mobile'

export type HeaderTool = {
  id: string
  label: string
  content: ReactNode
}

type HeaderToolsProps = {
  items: HeaderTool[]
  unreadCount?: number
  onDismiss?: () => void
}

function MobileHeaderTools({
  items,
  unreadCount = 0,
  onDismiss,
}: HeaderToolsProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)

  const dismissOnUnmount = useEffectEvent(() => onDismiss?.())
  // A new callback from the parent is not an unmount. Keep open panels intact.
  useEffect(() => () => dismissOnUnmount(), [])

  const close = () => {
    setOpen(false)
    onDismiss?.()
  }

  return (
    <Popover
      open={open}
      onOpenChange={(nextOpen) => {
        setOpen(nextOpen)
        if (!nextOpen) onDismiss?.()
      }}
    >
      <PopoverTrigger
        render={
          <Button
            type='button'
            variant='ghost'
            size='icon'
            className='relative size-11 shrink-0 rounded-lg'
            aria-label={
              unreadCount > 0
                ? `${t('More')} · ${t('Notifications')}: ${unreadCount}`
                : t('More')
            }
            title={t('More')}
            data-testid='header-more-actions'
          />
        }
      >
        <Ellipsis aria-hidden='true' />
        {unreadCount > 0 && (
          <span
            aria-hidden='true'
            className='bg-primary text-primary-foreground absolute -top-0.5 -right-0.5 flex min-w-4 items-center justify-center rounded-full px-1 text-[10px] leading-4 font-semibold tabular-nums'
          >
            {unreadCount > 99 ? '99+' : unreadCount}
          </span>
        )}
      </PopoverTrigger>
      <PopoverContent
        align='end'
        sideOffset={8}
        collisionPadding={12}
        className='max-h-[min(24rem,var(--available-height,calc(100dvh-5rem)))] w-64 max-w-[calc(100vw-1.5rem)] gap-3 overflow-y-auto overscroll-contain rounded-2xl p-4'
        data-testid='header-tools-menu'
        onClick={(event) => {
          // Keep nested notification/settings controls open; close only for links.
          if (
            event.target instanceof Element &&
            event.target.closest('a[href]')
          ) {
            close()
          }
        }}
      >
        <PopoverTitle className='text-sm'>{t('More')}</PopoverTitle>
        <div className='grid grid-cols-2 gap-x-4 gap-y-3'>
          {items.map((item) => (
            <div
              key={item.id}
              data-testid={`header-tool-${item.id}`}
              className='flex min-w-0 flex-col items-center gap-1.5 [&_a]:min-h-11 [&_a]:min-w-11 [&_button]:min-h-11 [&_button]:min-w-11'
            >
              {item.content}
              <span className='text-muted-foreground text-center text-xs leading-snug font-medium break-words'>
                {item.label}
              </span>
            </div>
          ))}
        </div>
      </PopoverContent>
    </Popover>
  )
}

/** Keep one mounted copy of each control; phones use the sidebar breakpoint. */
export function HeaderTools(props: HeaderToolsProps) {
  const isMobile = useIsMobile()
  if (props.items.length === 0) return null
  if (isMobile && props.items.length > 1) {
    return <MobileHeaderTools {...props} />
  }
  return props.items.map((item) => (
    <Fragment key={item.id}>{item.content}</Fragment>
  ))
}
