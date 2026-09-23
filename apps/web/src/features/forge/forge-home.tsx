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
import { useQuery } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { ArrowRight } from 'lucide-react'
import { type FormEvent, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from '@/components/ui/input-group'
import { getAssistantPreConversationPresets } from '@/features/assistant/api'
import { requestAssistantSend } from '@/features/assistant/assistant-events'
import { redactAssistantMessageForRequest } from '@/features/assistant/assistant-message-safety'
import {
  ASSISTANT_PROMPT_PRESET_COPY_VERSION,
  localizeAssistantPreConversationPresets,
} from '@/features/assistant/assistant-prompt-presets'
import { getAssistantPromptValidation } from '@/features/assistant/assistant-prompt-validation'
import { codeForTab, type CodeTab } from '@/features/home/home-code-examples'
import { CodePreview } from '@/features/home/home-code-preview'
import { HomeLanding } from '@/features/home/home-landing'
import { mountHomeMotion } from '@/features/home/home-motion'
import type { ConnectionMethod } from '@/features/onboarding/next-step'
import { useAccountNextStep } from '@/features/onboarding/use-account-next-step'
import { PublicScriptsPanel } from '@/features/scripts/scripts-panel'
import { useStatus } from '@/hooks/use-status'
import { useTopNavLinks } from '@/hooks/use-top-nav-links'
import { isConsoleActivated } from '@/lib/console-activation'
import { useAuthStore } from '@/stores/auth-store'

import { ForgePublicShell } from './forge-public-shell'
import { PurchaseJourney } from './purchase-journey'
import { useTypewriterPlaceholder } from './use-typewriter-placeholder'

import './forge-home.css'

const HOME_SETUP_PROMPTS = [
  {
    label: 'Help me choose an app',
    prompt:
      'I am new here. Help me choose an AI app. Ask about my device and what I want to do, then give me its official download link and installation steps.',
  },
  {
    label: 'Connect my API key',
    prompt:
      'Help me connect an AI app to LMM step by step. Ask which app and device I use, explain the API address and model settings, and show me where to safely import my API key. Do not ask me to paste my key into chat.',
  },
  {
    label: 'Fix a connection issue',
    prompt:
      'My AI app cannot connect. Ask which app I use and what error I see, then walk me through one check at a time. Remind me to hide API keys and personal details in screenshots.',
  },
] as const
const PI_INSTALL_COMMAND =
  'npm exec --yes --package=@tokennotincluded/pi-lmm-provider@0.1.0-alpha.2 -- lmm-pi-provider npm:@tokennotincluded/pi-lmm-provider@0.1.0-alpha.2'

function useCopyFeedback() {
  const [copied, setCopied] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const mounted = useRef(false)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
      clearTimeout(timer.current)
    }
  }, [])
  const copy = async (value: string) => {
    clearTimeout(timer.current)
    try {
      await navigator.clipboard.writeText(value)
      if (!mounted.current) return
      setCopied(true)
      timer.current = setTimeout(() => setCopied(false), 1400)
    } catch {
      if (mounted.current) setCopied(false)
    }
  }
  return { copied, copy }
}

export function ForgeHome() {
  const { t, i18n } = useTranslation()
  const navigate = useNavigate()
  const [connectionMethod, setConnectionMethod] =
    useState<ConnectionMethod>('oauth')
  const { nextStep } = useAccountNextStep(connectionMethod)
  const user = useAuthStore((state) => state.auth.user)
  const { status } = useStatus()
  const securityLink = useTopNavLinks().find(
    (link) => link.href === '/security'
  )
  const rootRef = useRef<HTMLElement>(null)
  const [message, setMessage] = useState('')
  const [messageFocused, setMessageFocused] = useState(false)
  const [codeTab, setCodeTab] = useState<CodeTab>('Chat')
  const [activeExplore, setActiveExplore] = useState('market')
  const piCopy = useCopyFeedback()
  const codeCopy = useCopyFeedback()
  const assistantEnabled = status?.assistant?.enabled !== false
  const messageInvalid = getAssistantPromptValidation(message).invalid
  const presetLanguage = i18n.resolvedLanguage || i18n.language || 'en'
  const preConversationPresetsQuery = useQuery({
    queryKey: [
      'assistant-pre-conversation-presets',
      presetLanguage,
      ASSISTANT_PROMPT_PRESET_COPY_VERSION,
    ],
    queryFn: () => getAssistantPreConversationPresets(presetLanguage),
    placeholderData: (previous) => previous,
    enabled: assistantEnabled,
    staleTime: 5 * 60_000,
    retry: false,
  })
  const animatedPlaceholder = useTypewriterPlaceholder(
    localizeAssistantPreConversationPresets(
      preConversationPresetsQuery.data?.presets,
      t
    ).map((preset) => preset.prompt),
    assistantEnabled && message.length === 0 && !messageFocused
  )
  useEffect(() => {
    if (rootRef.current) return mountHomeMotion(rootRef.current)
  }, [presetLanguage, connectionMethod])

  const startAssistant = (prompt: string) => {
    const safeMessage = redactAssistantMessageForRequest(prompt).content.trim()
    if (
      !safeMessage ||
      getAssistantPromptValidation(prompt).invalid ||
      !assistantEnabled
    ) {
      return
    }
    if (!user) {
      requestAssistantSend(undefined, safeMessage)
      void navigate({ to: '/sign-in', search: { redirect: '/dashboard' } })
      return
    }
    const activated = isConsoleActivated(user)
    requestAssistantSend(activated ? 'service' : 'onboarding', safeMessage)
    void navigate({ to: activated ? '/dashboard' : '/getting-started' })
  }
  const submitMessage = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    startAssistant(message)
  }

  return (
    <ForgePublicShell>
      <HomeLanding
        rootRef={rootRef}
        connectionMethod={connectionMethod}
        onConnectionMethodChange={setConnectionMethod}
        t={t}
        primaryAction={
          <Button
            size='lg'
            render={
              <Link
                to={nextStep.to}
                search={
                  nextStep.to === '/sign-in'
                    ? { redirect: '/getting-started' }
                    : undefined
                }
              />
            }
          >
            {t(nextStep.label)}
            <ArrowRight data-icon='inline-end' />
          </Button>
        }
        pricingAction={
          <Link to='/pricing' className='lmm-text-link'>
            {isConsoleActivated(user)
              ? t('View model pricing')
              : t('Pricing and access')}
            <ArrowRight aria-hidden='true' />
          </Link>
        }
        code={
          <CodePreview
            t={t}
            tab={codeTab}
            copied={codeCopy.copied}
            onTabChange={setCodeTab}
            onCopy={() => void codeCopy.copy(codeForTab(codeTab))}
          />
        }
        assistant={
          <form onSubmit={submitMessage}>
            <label
              className='forge-home-assistant-label'
              htmlFor='forge-home-message'
            >
              {t('Describe what you need...')}
            </label>
            <InputGroup className='forge-home-input'>
              <InputGroupInput
                id='forge-home-message'
                value={message}
                onChange={(event) => setMessage(event.target.value)}
                onFocus={() => setMessageFocused(true)}
                onBlur={() => setMessageFocused(false)}
                className='min-w-0'
                placeholder={
                  animatedPlaceholder || t('Describe what you need...')
                }
                maxLength={4000}
              />
              <InputGroupAddon align='inline-end'>
                <InputGroupButton
                  type='submit'
                  variant='default'
                  size='sm'
                  className='h-11 rounded-full px-4'
                  aria-label={t('Ask AI assistant')}
                  disabled={
                    !message.trim() || messageInvalid || !assistantEnabled
                  }
                >
                  <ArrowRight
                    className='forge-home-submit-icon size-4'
                    aria-hidden='true'
                  />
                </InputGroupButton>
              </InputGroupAddon>
            </InputGroup>
            {assistantEnabled && (
              <div className='forge-home-assistant-prompts'>
                {HOME_SETUP_PROMPTS.map((preset) => (
                  <button
                    key={preset.label}
                    type='button'
                    onClick={() => startAssistant(t(preset.prompt))}
                  >
                    {t(preset.label)}
                    <ArrowRight className='size-3.5' aria-hidden='true' />
                  </button>
                ))}
              </div>
            )}
          </form>
        }
        pi={
          <>
            <p className='lmm-pi-description'>
              {t(
                'Install the LMM Pi plugin, sign in with OAuth, and choose a model in Pi. Access uses your account and normal model pricing.'
              )}
            </p>
            <div className='lmm-pi-command'>
              <code>{PI_INSTALL_COMMAND}</code>
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={() => void piCopy.copy(PI_INSTALL_COMMAND)}
                aria-label={t('Copy Pi install command')}
              >
                <span aria-live='polite'>
                  {piCopy.copied ? t('Copied') : t('Copy')}
                </span>
              </Button>
            </div>
            <a href='/guide#client-setup' className='lmm-text-link'>
              {t('Read the Pi OAuth setup steps')}
              <ArrowRight aria-hidden='true' />
            </a>
            <p className='lmm-webmcp-note'>
              <strong>{t('WebMCP tools for compatible browsers')}</strong>{' '}
              {t(
                'Browser agents can read site information, model prices, and account status or open pages; the normal UI remains available when WebMCP is unsupported.'
              )}
            </p>
          </>
        }
        explore={
          <div className='lmm-explore-console'>
            <nav className='lmm-destinations' aria-label={t('Explore LMM')}>
              {[
                [
                  'market',
                  '/tool-market',
                  'Tool market',
                  'Browse, publish, authorize and run tools.',
                ],
                [
                  'pricing',
                  '/pricing',
                  'Models and pricing',
                  'Compare model capabilities and account pricing.',
                ],
                [
                  'challenges',
                  '/challenges',
                  'Open-source bounties',
                  'Find focused work with funded reward slots.',
                ],
                [
                  'scripts',
                  '/scripts',
                  'Public scripts',
                  'Use the reviewed installation and setup scripts.',
                ],
                ...(securityLink
                  ? [
                      [
                        'security',
                        securityLink.requiresAuth ? '/sign-in' : '/security',
                        'Security',
                        'Review account and platform security controls.',
                      ],
                    ]
                  : []),
              ].map(([id, href, label]) => (
                <a
                  key={id}
                  href={
                    id === 'security' && securityLink?.requiresAuth
                      ? '/sign-in?redirect=%2Fsecurity'
                      : href
                  }
                  aria-current={activeExplore === id ? 'page' : undefined}
                  onMouseEnter={() => setActiveExplore(id)}
                  onFocus={() => setActiveExplore(id)}
                  onClick={() => setActiveExplore(id)}
                >
                  <span>
                    <small>{t('Explore')}</small>
                    <strong>{t(label)}</strong>
                  </span>
                  <ArrowRight aria-hidden='true' />
                </a>
              ))}
            </nav>
            <div className='lmm-explore-preview' aria-live='polite'>
              {(() => {
                const descriptions: Record<string, string> = {
                  market: 'Browse, publish, authorize and run tools.',
                  pricing: 'Compare model capabilities and account pricing.',
                  challenges: 'Find focused work with funded reward slots.',
                  scripts: 'Use the reviewed installation and setup scripts.',
                  security: 'Review account and platform security controls.',
                }
                const labels: Record<string, string> = {
                  market: 'Tool market',
                  pricing: 'Models and pricing',
                  challenges: 'Open-source bounties',
                  scripts: 'Public scripts',
                  security: 'Security',
                }
                return (
                  <>
                    <span className='lmm-explore-preview-index'>
                      0
                      {Math.max(
                        1,
                        [
                          'market',
                          'pricing',
                          'challenges',
                          'scripts',
                          'security',
                        ].indexOf(activeExplore) + 1
                      )}
                    </span>
                    <strong>{t(labels[activeExplore] ?? labels.market)}</strong>
                    <p>
                      {t(descriptions[activeExplore] ?? descriptions.market)}
                    </p>
                  </>
                )
              })()}
            </div>
          </div>
        }
        scripts={<PublicScriptsPanel />}
        purchase={<PurchaseJourney />}
      />
    </ForgePublicShell>
  )
}
