/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { ArrowLeft01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { MessagesSquare } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

import type { AssistantConversationHistoryItem } from '../assistant/api'
import {
  AssistantHistory,
  AssistantHistoryConversation,
} from '../assistant/assistant-history'

/**
 * A calm, full-page home for assistant conversations. The launcher remains
 * useful for a quick exchange; this view is for users who have accumulated a
 * real history and need to browse it without a cramped side rail.
 *
 * Follows the standard section page shell (`SectionPageLayout`) used by the
 * other console pages: fixed header, and two independently scrolling panes —
 * the conversation list on the left, the selected transcript on the right.
 * Narrow screens keep the single-pane master/detail swap with a back
 * affordance.
 */
export function ChatManagement() {
  const { t } = useTranslation()
  const [selected, setSelected] =
    useState<AssistantConversationHistoryItem | null>(null)

  return (
    <SectionPageLayout fixedContent>
      <SectionPageLayout.Title>
        {t('Conversation records')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='grid h-full min-h-0 gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(20rem,26rem)] lg:gap-0'>
          <section
            aria-label={t('Conversation history')}
            className={cn(
              'min-h-0 min-w-0 overflow-y-auto lg:pr-6',
              selected && 'hidden lg:block'
            )}
          >
            <AssistantHistory
              active
              presentation='rows'
              onOpenConversation={setSelected}
            />
          </section>

          <aside
            className={cn(
              'min-h-0 min-w-0 overflow-y-auto border-t pt-6 lg:border-t-0 lg:border-l lg:pt-0 lg:pl-6',
              !selected && 'hidden lg:block'
            )}
          >
            {selected ? (
              <div className='grid gap-4'>
                <Button
                  type='button'
                  variant='ghost'
                  size='sm'
                  className='w-fit px-0 lg:hidden'
                  data-testid='chat-management-back'
                  onClick={() => setSelected(null)}
                >
                  <HugeiconsIcon
                    icon={ArrowLeft01Icon}
                    className='size-4'
                    strokeWidth={2}
                    aria-hidden='true'
                  />
                  {t('Back to list')}
                </Button>
                <AssistantHistoryConversation conversation={selected} />
              </div>
            ) : (
              <EmptyState
                icon={MessagesSquare}
                title={t('Open a conversation')}
                description={t(
                  'Select a conversation to read the full transcript. Private credentials remain protected.'
                )}
              />
            )}
          </aside>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
