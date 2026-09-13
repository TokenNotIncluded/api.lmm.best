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
import { ArrowRight, Check, ChevronRight, Copy } from 'lucide-react'
import { type FormEvent, useState } from 'react'
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
import { PublicScriptsPanel } from '@/features/scripts/scripts-panel'
import { useStatus } from '@/hooks/use-status'
import { useTopNavLinks } from '@/hooks/use-top-nav-links'
import { isConsoleActivated } from '@/lib/console-activation'
import { useAuthStore } from '@/stores/auth-store'

import { ForgePublicShell } from './forge-public-shell'
import { PurchaseJourney } from './purchase-journey'
import { usePurchaseEntry } from './use-purchase-entry'
import { useTypewriterPlaceholder } from './use-typewriter-placeholder'

import './forge-home.css'

const CODE_TABS = ['Chat', 'API', 'Claude', 'Gemini'] as const
type CodeTab = (typeof CODE_TABS)[number]

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
  'pi install git:github.com/TokenNotIncluded/pi-lmm-provider'

function codeForTab(tab: CodeTab) {
  if (tab === 'Claude') {
    return `curl https://api.lmm.best/v1/messages \\
  -H "x-api-key: $LMM_API_KEY" \\
  -H "anthropic-version: 2023-06-01" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "model-name",
    "max_tokens": 256,
    "messages": [{ "role": "user", "content": "Hello" }]
  }'`
  }
  if (tab === 'Gemini') {
    return `curl "https://api.lmm.best/v1beta/models/model-name:generateContent" \\
  -H "x-goog-api-key: $LMM_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "contents": [{
      "role": "user",
      "parts": [{ "text": "Hello" }]
    }]
  }'`
  }
  if (tab === 'API') {
    return `import OpenAI from "openai"

const client = new OpenAI({
  baseURL: "https://api.lmm.best/v1",
  apiKey: process.env.LMM_API_KEY,
})

const response = await client.chat.completions.create({
  model: "model-name",
  messages: [{ role: "user", content: "your prompt" }],
})`
  }
  return `curl -X POST "https://api.lmm.best/v1/chat/completions" \\
  -H "Authorization: Bearer $LMM_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "model-name",
    "messages": [{ "role": "user", "content": "your prompt" }]
  }'`
}

function CodePreview(props: {
  tab: CodeTab
  onTabChange: (tab: CodeTab) => void
}) {
  const { t } = useTranslation()
  const code = codeForTab(props.tab)
  const [copied, setCopied] = useState(false)

  const copyCode = async () => {
    try {
      await navigator.clipboard.writeText(code)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1400)
    } catch {
      setCopied(false)
    }
  }

  return (
    <div className='forge-home-code-card'>
      <div
        className='forge-home-code-tabs'
        role='tablist'
        aria-label={t('API Endpoints')}
      >
        {CODE_TABS.map((tab, index) => (
          <button
            key={tab}
            type='button'
            role='tab'
            id={`home-code-tab-${tab}`}
            aria-controls='home-code-panel'
            tabIndex={props.tab === tab ? 0 : -1}
            aria-selected={props.tab === tab}
            className={props.tab === tab ? 'is-active' : undefined}
            onClick={() => props.onTabChange(tab)}
            onKeyDown={(event) => {
              const next =
                event.key === 'ArrowRight'
                  ? (index + 1) % CODE_TABS.length
                  : event.key === 'ArrowLeft'
                    ? (index + CODE_TABS.length - 1) % CODE_TABS.length
                    : event.key === 'Home'
                      ? 0
                      : event.key === 'End'
                        ? CODE_TABS.length - 1
                        : null
              if (next === null) return
              event.preventDefault()
              props.onTabChange(CODE_TABS[next])
              event.currentTarget.parentElement
                ?.querySelectorAll<HTMLButtonElement>('[role="tab"]')
                [next]?.focus()
            }}
          >
            {t(tab)}
          </button>
        ))}
      </div>
      <div
        id='home-code-panel'
        role='tabpanel'
        aria-labelledby={`home-code-tab-${props.tab}`}
      >
        <div className='forge-home-code-label'>
          <span>{t('API Requests')}</span>
          <Button
            variant='ghost'
            size='icon'
            className='text-muted-foreground hover:text-foreground size-11'
            onClick={() => void copyCode()}
            aria-label={t('Copy')}
          >
            {copied ? <Check /> : <Copy />}
          </Button>
        </div>
        <pre
          className='forge-home-code-block'
          tabIndex={0}
          aria-label={t('API Requests')}
        >
          <code>{code}</code>
        </pre>
        <p className='forge-home-code-help'>
          {t(
            'Replace model-name with an available model ID for the selected API. Set LMM_API_KEY locally; never put your key in browser code.'
          )}
        </p>
        {props.tab === 'API' && (
          <p className='forge-home-code-help'>
            {t('For server-side JavaScript, install the SDK first:')}{' '}
            <code>npm install openai</code>
          </p>
        )}
      </div>
    </div>
  )
}

export function ForgeHome() {
  const { t, i18n } = useTranslation()
  const navigate = useNavigate()
  const purchaseEntry = usePurchaseEntry()
  const user = useAuthStore((state) => state.auth.user)
  const { status } = useStatus()
  const securityLink = useTopNavLinks().find(
    (link) => link.href === '/security'
  )
  const [message, setMessage] = useState('')
  const [messageFocused, setMessageFocused] = useState(false)
  const [codeTab, setCodeTab] = useState<CodeTab>('Chat')
  const [piCommandCopied, setPiCommandCopied] = useState(false)
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
      void navigate({
        to: '/sign-in',
        search: { redirect: '/dashboard' },
      })
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

  const copyPiCommand = async () => {
    try {
      await navigator.clipboard.writeText(PI_INSTALL_COMMAND)
      setPiCommandCopied(true)
      window.setTimeout(() => setPiCommandCopied(false), 1400)
    } catch {
      setPiCommandCopied(false)
    }
  }

  return (
    <ForgePublicShell>
      <main className='forge-home-page'>
        <section className='forge-home-hero' aria-labelledby='forge-home-title'>
          <div className='forge-home-hero-content'>
            <div className='forge-home-intro'>
              <h1 id='forge-home-title'>
                {t('Use Pi without manually creating an API key')}
              </h1>
              <p className='forge-home-hero-description'>
                {t(
                  'One address for compatible apps. Compare rates before you start.'
                )}
              </p>
              <div className='forge-home-hero-actions'>
                <Button
                  size='lg'
                  className='group h-12 rounded-full px-7'
                  render={
                    <Link
                      to={purchaseEntry.to}
                      search={
                        purchaseEntry.to === '/sign-in'
                          ? { redirect: '/wallet' }
                          : undefined
                      }
                    />
                  }
                >
                  {t(purchaseEntry.label)}
                  <ArrowRight data-icon='inline-end' />
                </Button>
                <Link to='/pricing' className='forge-home-text-link'>
                  {isConsoleActivated(user)
                    ? t('View model pricing')
                    : t('Pricing and access')}
                  <ArrowRight aria-hidden='true' />
                </Link>
              </div>
              <p className='forge-home-access-note'>
                {t(
                  'Developer access requires approval. Payment does not unlock access.'
                )}
              </p>
              <div className='mt-6 max-w-xl p-0'>
                <h2 className='text-sm font-semibold'>
                  {t('Use Pi without manually creating an API key')}
                </h2>
                <p className='text-muted-foreground mt-1 text-sm leading-6'>
                  {t(
                    'Install the LMM Pi plugin, sign in with OAuth, and choose a model in Pi. Access uses your account and normal model pricing.'
                  )}
                </p>
                <div className='mt-3 flex max-w-full items-center gap-2'>
                  <code className='bg-muted min-w-0 flex-1 overflow-x-auto px-2 py-1 text-xs'>
                    {PI_INSTALL_COMMAND}
                  </code>
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    className='shrink-0'
                    onClick={() => void copyPiCommand()}
                    aria-label={t('Copy Pi install command')}
                  >
                    {piCommandCopied ? t('Copied') : t('Copy')}
                  </Button>
                </div>
                <a href='/guide#pi-oauth' className='forge-home-text-link mt-3'>
                  {t('Read the Pi OAuth setup steps')}
                  <ArrowRight aria-hidden='true' />
                </a>
                <p className='text-muted-foreground mt-4 border-t border-current/10 pt-3 text-xs leading-5'>
                  <strong className='text-foreground'>
                    {t('WebMCP tools for compatible browsers')}
                  </strong>{' '}
                  {t(
                    'Browser agents can read site information, model prices, and account status or open pages; the normal UI remains available when WebMCP is unsupported.'
                  )}
                </p>
              </div>
            </div>
            <form
              className='forge-home-hero-assistant'
              onSubmit={submitMessage}
            >
              <label
                className='forge-home-assistant-label'
                htmlFor='forge-home-message'
              >
                {t('Tell us what you want to do')}
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
                    <span className='forge-home-submit-label'>
                      {t('Ask AI assistant')}
                    </span>
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
          </div>
          <div className='forge-home-connection'>
            <span>{t('One endpoint')}</span>
            <code>api.lmm.best</code>
            <Link to='/guide' className='forge-home-text-link'>
              {t('New here? Start with the setup guide')}
              <ArrowRight aria-hidden='true' />
            </Link>
          </div>
        </section>

        <section
          className='forge-home-section forge-home-quickstart'
          aria-labelledby='forge-home-quickstart-title'
        >
          <div className='forge-home-section-heading'>
            <h2 id='forge-home-quickstart-title'>
              {t('From download to your first conversation')}
            </h2>
            <Link to='/guide' className='forge-home-text-link'>
              {t('Read the guide')}
              <ArrowRight aria-hidden='true' />
            </Link>
          </div>
          <div className='forge-home-quickstart-grid'>
            <div className='forge-home-steps'>
              {[
                [
                  '1',
                  'Download an AI app',
                  'Find official downloads and instructions for your device.',
                ],
                [
                  '2',
                  'Connect your API key',
                  'After access approval, create a key and configure your app.',
                ],
                [
                  '3',
                  'Send your first message',
                  'Choose a model, test the connection, and check your usage.',
                ],
              ].map(([number, title, description]) => (
                <Link to='/guide' key={number} className='forge-home-step'>
                  <span className='forge-home-step-number' aria-hidden='true'>
                    {number}
                  </span>
                  <div>
                    <h3>{t(title)}</h3>
                    <p>{t(description)}</p>
                  </div>
                  <ChevronRight
                    className='forge-home-step-arrow'
                    aria-hidden='true'
                  />
                </Link>
              ))}
            </div>
            <CodePreview tab={codeTab} onTabChange={setCodeTab} />
          </div>
        </section>

        <section
          className='forge-home-section forge-home-explore'
          aria-labelledby='forge-home-explore-title'
        >
          <h2 id='forge-home-explore-title'>
            {t('Make room for your next idea.')}
          </h2>
          <div className='forge-home-destinations'>
            <Link to='/pricing'>
              <span>{t('Model Square')}</span>
              <ArrowRight aria-hidden='true' />
            </Link>
            <Link to='/challenges'>
              <span>{t('Open-source challenges')}</span>
              <ArrowRight aria-hidden='true' />
            </Link>
            {securityLink && (
              <Link
                to={securityLink.requiresAuth ? '/sign-in' : '/security'}
                search={
                  securityLink.requiresAuth
                    ? { redirect: '/security' }
                    : undefined
                }
              >
                <span>{t('Security')}</span>
                <ArrowRight aria-hidden='true' />
              </Link>
            )}
          </div>
        </section>

        <section
          className='forge-home-section'
          aria-labelledby='forge-home-scripts-title'
        >
          <div className='forge-home-section-heading'>
            <h2 id='forge-home-scripts-title'>{t('Scripts')}</h2>
          </div>
          <PublicScriptsPanel />
        </section>

        <section
          className='forge-home-section forge-home-purchase-path'
          aria-labelledby='forge-home-purchase-title'
        >
          <details>
            <summary id='forge-home-purchase-title'>
              {t('Account and access')}
              <ChevronRight aria-hidden='true' />
            </summary>
            <PurchaseJourney />
          </details>
        </section>
      </main>
    </ForgePublicShell>
  )
}
