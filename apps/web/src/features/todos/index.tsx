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
import { useSearch } from '@tanstack/react-router'
import { ChevronRight } from 'lucide-react'
import {
  lazy,
  Suspense,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { ROLE } from '@/lib/roles'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import { UnifiedTodoList } from './unified-todo-list'

const AccountActionRequestsPanel = lazy(() =>
  import('@/features/users/components/account-action-requests-panel').then(
    (module) => ({ default: module.AccountActionRequestsPanel })
  )
)
const AssistantLeadsPanel = lazy(() =>
  import('@/features/users/components/assistant-leads-panel').then(
    (module) => ({ default: module.AssistantLeadsPanel })
  )
)
const DeveloperAccessRequestsPanel = lazy(() =>
  import('@/features/users/components/developer-access-requests-panel').then(
    (module) => ({ default: module.DeveloperAccessRequestsPanel })
  )
)

export function Todos() {
  const { t } = useTranslation()
  const search = useSearch({ from: '/_authenticated/todos/' })
  const user = useAuthStore((state) => state.auth.user)
  const sessionId = useAuthStore((state) => state.auth.session?.sid)
  const isAdmin = (user?.role ?? 0) >= ROLE.ADMIN
  const focusAccountActionId =
    search.todo === 'account_action' ? search.request : undefined
  const focusDeveloperAccessId =
    search.todo === 'developer_access' ? search.request : undefined

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('To-dos')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div
          key={`${user?.id ?? ''}:${sessionId ?? ''}:${user?.role ?? 0}`}
          className='mx-auto flex min-h-0 w-full max-w-6xl flex-col gap-8 pb-6'
        >
          <UnifiedTodoList />
          {isAdmin ? (
            <div className='space-y-3'>
              <AdminTodoSection title={t('Assistant support tasks')}>
                <AssistantLeadsPanel />
              </AdminTodoSection>
              <AdminTodoSection
                title={t('Account safety review')}
                initiallyExpanded={focusAccountActionId !== undefined}
                focusRequestId={focusAccountActionId}
              >
                <AccountActionRequestsPanel
                  focusRequestId={focusAccountActionId}
                />
              </AdminTodoSection>
              <AdminTodoSection
                title={t('L1 access requests')}
                initiallyExpanded={focusDeveloperAccessId !== undefined}
                focusRequestId={focusDeveloperAccessId}
              >
                <DeveloperAccessRequestsPanel
                  focusRequestId={focusDeveloperAccessId}
                />
              </AdminTodoSection>
            </div>
          ) : null}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

function AdminTodoSection(props: {
  title: string
  children: ReactNode
  initiallyExpanded?: boolean
  focusRequestId?: number
}) {
  const { t } = useTranslation()
  const [expanded, setExpanded] = useState(props.initiallyExpanded ?? false)
  const [mounted, setMounted] = useState(props.initiallyExpanded ?? false)
  const summaryRef = useRef<HTMLElement>(null)

  useEffect(() => {
    if (!props.initiallyExpanded) return
    setExpanded(true)
    setMounted(true)
    const frame = requestAnimationFrame(() => {
      summaryRef.current?.scrollIntoView({ block: 'start' })
      summaryRef.current?.focus({ preventScroll: true })
    })
    return () => cancelAnimationFrame(frame)
  }, [props.initiallyExpanded, props.focusRequestId])

  return (
    <details
      className='border-border rounded-xl border px-4 py-1 sm:px-5'
      open={expanded}
      onToggle={(event) => {
        const open = event.currentTarget.open
        setExpanded(open)
        if (open) setMounted(true)
      }}
    >
      <summary
        ref={summaryRef}
        className='focus-visible:ring-ring text-foreground flex min-h-11 cursor-pointer scroll-mt-4 list-none items-center justify-between gap-3 rounded-sm text-sm font-medium outline-none focus-visible:ring-2 [&::-webkit-details-marker]:hidden'
      >
        {props.title}
        <ChevronRight
          aria-hidden='true'
          className={cn(
            'text-muted-foreground size-4 shrink-0 transition-transform motion-reduce:transition-none',
            expanded && 'rotate-90'
          )}
        />
      </summary>
      <Suspense
        fallback={
          <p role='status' className='text-muted-foreground py-5 text-sm'>
            {t('Loading')}
          </p>
        }
      >
        {mounted ? <div className='pt-5'>{props.children}</div> : null}
      </Suspense>
    </details>
  )
}
