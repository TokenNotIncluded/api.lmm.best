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
import {
  type LucideIcon,
  ArrowRight,
  BadgePercent,
  BookOpen,
  Braces,
  Check,
  ChevronRight,
  CircleHelp,
  Code2,
  Copy,
  Gauge,
  Globe2,
  Image,
  MessageCircle,
  ShieldCheck,
  Sparkles,
  Workflow,
} from 'lucide-react'
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
import { getAssistantPromptValidation } from '@/features/assistant/assistant-prompt-validation'
import { useStatus } from '@/hooks/use-status'
import { isConsoleActivated } from '@/lib/console-activation'
import { useAuthStore } from '@/stores/auth-store'

import { ForgePublicShell } from './forge-public-shell'
import { useTypewriterPlaceholder } from './use-typewriter-placeholder'

import './forge-home.css'

const FEATURE_CARDS: Array<{
  icon: LucideIcon
  title: string
  description: string
  tone: string
}> = [
  {
    icon: BookOpen,
    title: 'Setup guide',
    description: 'The built-in assistant configures keys, models, and budgets.',
    tone: 'text-primary',
  },
  {
    icon: BadgePercent,
    title: 'Clear pricing',
    description: 'Per-token billing with visible rates before you commit.',
    tone: 'text-chart-2',
  },
  {
    icon: Sparkles,
    title: 'Model Square',
    description:
      'Discover curated AI models, compare pricing and capabilities, and choose the right model for every scenario.',
    tone: 'text-chart-3',
  },
  {
    icon: Gauge,
    title: 'Uptime',
    description: 'Health-checked upstreams with latency you can inspect.',
    tone: 'text-success',
  },
  {
    icon: Globe2,
    title: 'One endpoint',
    description:
      'Chat, reasoning, vision, and audio models behind one endpoint.',
    tone: 'text-info',
  },
  {
    icon: ShieldCheck,
    title: 'Support',
    description: 'Support and access requests stay auditable and fair.',
    tone: 'text-chart-4',
  },
]

const USE_CASES: Array<{
  icon: LucideIcon
  title: string
  description: string
}> = [
  {
    icon: MessageCircle,
    title: 'Chat',
    description:
      'A guided assistant for setup, account, billing, and model questions.',
  },
  {
    icon: Code2,
    title: 'Client setup guide',
    description:
      'Use one Base URL, model ID, and API key across your compatible tools.',
  },
  {
    icon: Braces,
    title: 'API Endpoints',
    description:
      'OpenAI-compatible access for applications, scripts, and agents.',
  },
  {
    icon: Image,
    title: 'Model Square',
    description:
      'Compare providers, capabilities, and transparent token pricing before you choose.',
  },
  {
    icon: Workflow,
    title: 'Open-source challenges',
    description:
      'Connect public work, review evidence, and a practical AI gateway in one place.',
  },
]

const CODE_TABS = ['Chat', 'API', 'Claude', 'Gemini'] as const
type CodeTab = (typeof CODE_TABS)[number]

const HOME_MODEL_NAMES = [
  'deepseek-v4-pro-0813',
  'gemini-3.7-flash',
  'gemini-3.7-flash-search',
  'grok-4.6',
] as const
const HOME_MODEL_ROTATION_MS = 3500

function codeForTab(tab: CodeTab) {
  if (tab === 'Claude') {
    return `curl https://api.lmm.best/v1/messages \\
  -H "x-api-key: sk-••••" \\
  -H "anthropic-version: 2023-06-01" \\
  -d '{"model":"model-name","max_tokens":256}'`
  }
  if (tab === 'Gemini') {
    return `curl https://api.lmm.best/v1beta/models \\
  -H "Authorization: Bearer sk-••••" \\
  -d '{"model":"model-name","contents":[]}'`
  }
  if (tab === 'API') {
    return `const client = new OpenAI({
  baseURL: "https://api.lmm.best/v1",
  apiKey: process.env.LMM_API_KEY,
})

const response = await client.chat.completions.create({
  model: "model-name",
  messages: [{ role: "user", content: "your prompt" }],
})`
  }
  return `curl -X POST "https://api.lmm.best/v1/chat/completions" \\
  -H "Authorization: Bearer sk-••••" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "model-name",
    "messages": [{ "role": "user", "content": "your prompt" }]
  }'`
}

function HomeSectionHeading(props: {
  eyebrow: string
  title: string
  description: string
}) {
  return (
    <div className='forge-home-section-heading'>
      <span className='forge-home-pill'>
        {props.eyebrow}
        <span className='bg-primary size-1.5 rounded-full' aria-hidden='true' />
      </span>
      <h2>{props.title}</h2>
      <p>{props.description}</p>
    </div>
  )
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
      <div className='forge-home-window-bar'>
        <span />
        <span />
        <span />
      </div>
      <div className='forge-home-code-tabs' role='tablist'>
        {CODE_TABS.map((tab) => (
          <button
            key={tab}
            type='button'
            role='tab'
            aria-selected={props.tab === tab}
            className={props.tab === tab ? 'is-active' : undefined}
            onClick={() => props.onTabChange(tab)}
          >
            {t(tab)}
          </button>
        ))}
      </div>
      <div className='forge-home-code-label'>
        <span>{t('Request')}</span>
        <Button
          variant='ghost'
          size='icon-sm'
          className='text-muted-foreground hover:text-foreground size-7'
          onClick={() => void copyCode()}
          aria-label={t('Copy')}
        >
          {copied ? <Check /> : <Copy />}
        </Button>
      </div>
      <pre className='forge-home-code-block'>
        <code>{code}</code>
      </pre>
      <div className='forge-home-code-label forge-home-code-response'>
        <span>{t('Response')}</span>
        <span className='forge-home-code-status'>200 OK</span>
      </div>
      <pre className='forge-home-code-block forge-home-response-block'>
        <code>{`{
  "choices": [{
    "message": { "role": "assistant", "content": "completion text..." }
  }],
  "usage": { "total_tokens": 15 }
}`}</code>
      </pre>
    </div>
  )
}

export function ForgeHome() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const user = useAuthStore((state) => state.auth.user)
  const { status } = useStatus()
  const [message, setMessage] = useState('')
  const [messageFocused, setMessageFocused] = useState(false)
  const [codeTab, setCodeTab] = useState<CodeTab>('Chat')
  const [modelIndex, setModelIndex] = useState(0)
  const modelMeasureRef = useRef<HTMLSpanElement>(null)
  const [modelWidth, setModelWidth] = useState<number>()
  const assistantEnabled = status?.assistant?.enabled !== false
  const activeModelName = HOME_MODEL_NAMES[modelIndex]

  useEffect(() => {
    const measureModel = () => {
      const width = modelMeasureRef.current?.getBoundingClientRect().width
      if (width && Number.isFinite(width)) {
        setModelWidth(Math.ceil(width))
      }
    }

    measureModel()
    if (typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(measureModel)
    if (modelMeasureRef.current) observer.observe(modelMeasureRef.current)
    return () => observer.disconnect()
  }, [activeModelName])

  useEffect(() => {
    const intervalId = window.setInterval(() => {
      setModelIndex((current) => (current + 1) % HOME_MODEL_NAMES.length)
    }, HOME_MODEL_ROTATION_MS)
    return () => window.clearInterval(intervalId)
  }, [])
  const messageInvalid = getAssistantPromptValidation(message).invalid
  const preConversationPresetsQuery = useQuery({
    queryKey: ['assistant-pre-conversation-presets'],
    queryFn: getAssistantPreConversationPresets,
    enabled: assistantEnabled,
    staleTime: 5 * 60_000,
    retry: false,
  })
  const animatedPlaceholder = useTypewriterPlaceholder(
    preConversationPresetsQuery.data?.presets.map((preset) => preset.prompt) ??
      [],
    message.length === 0 && !messageFocused
  )

  const submitMessage = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const safeMessage = redactAssistantMessageForRequest(message).content.trim()
    if (!safeMessage || messageInvalid || !assistantEnabled) return

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

  return (
    <ForgePublicShell>
      <main className='forge-home-page'>
        <div className='forge-home-aurora' aria-hidden='true'>
          <div className='forge-home-aurora-layer' />
        </div>

        <section className='forge-home-hero' aria-labelledby='forge-home-title'>
          <div className='forge-home-grid' aria-hidden='true' />
          <div
            className='forge-home-orb forge-home-orb-left'
            aria-hidden='true'
          />
          <div
            className='forge-home-orb forge-home-orb-right'
            aria-hidden='true'
          />
          <div className='forge-home-hero-content'>
            <div className='forge-home-model-badge'>
              <span className='forge-home-badge-label'>
                <Sparkles className='size-3' />
                {t('New')}
              </span>
              <span
                className='forge-home-model-viewport'
                aria-live='polite'
                style={modelWidth ? { width: `${modelWidth}px` } : undefined}
              >
                <span
                  ref={modelMeasureRef}
                  aria-hidden='true'
                  className='invisible absolute whitespace-nowrap'
                >
                  {activeModelName}
                </span>
                <span
                  key={activeModelName}
                  className='forge-home-model-current'
                >
                  <Link to='/pricing'>{activeModelName}</Link>
                </span>
              </span>
              <ChevronRight className='text-muted-foreground size-4' />
            </div>
            <h1 id='forge-home-title'>
              <span>{t('Just one endpoint')}</span>
              <span>{t('Connect the world’s most popular models')}</span>
            </h1>
            <p className='forge-home-hero-description'>
              {t(
                'A semi-public-interest AI gateway for high-quality, transparent access.'
              )}
            </p>
            <p className='forge-home-hero-summary'>
              {t(
                'Pay as you go, no time limits, fast chat, transparent details, no hidden fees, and online recharge for access to every model.'
              )}
            </p>
            <div className='forge-home-hero-actions'>
              <Button
                size='lg'
                className='group h-14 rounded-full px-8 text-base'
                render={
                  <Link
                    to={user ? '/dashboard' : '/sign-in'}
                    search={user ? undefined : { redirect: '/dashboard' }}
                  />
                }
              >
                {t('Get started')}
                <ArrowRight className='ml-2 size-4 transition-transform group-hover:translate-x-1' />
              </Button>
              <Button
                variant='outline'
                size='lg'
                className='border-border/80 bg-card/50 h-14 rounded-full px-8 text-base'
                render={<Link to='/guide' />}
              >
                {t('Read the guide')}
              </Button>
            </div>
            <form
              className='forge-home-hero-assistant'
              onSubmit={submitMessage}
            >
              <label className='sr-only' htmlFor='forge-home-message'>
                {t('Tell us what you want to do')}
              </label>
              <InputGroup className='border-border/60 bg-card/60 h-12 rounded-full px-1 backdrop-blur-xl'>
                <InputGroupInput
                  id='forge-home-message'
                  value={message}
                  onChange={(event) => setMessage(event.target.value)}
                  onFocus={() => setMessageFocused(true)}
                  onBlur={() => setMessageFocused(false)}
                  className='focus-visible:!outline-none'
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
                    className='h-10 rounded-full px-4'
                    disabled={
                      !message.trim() || messageInvalid || !assistantEnabled
                    }
                  >
                    {t('Ask AI assistant')}
                  </InputGroupButton>
                </InputGroupAddon>
              </InputGroup>
            </form>
          </div>
        </section>

        <section
          className='forge-home-section'
          aria-labelledby='forge-home-features-title'
        >
          <HomeSectionHeading
            eyebrow={t('Usage at a glance')}
            title={t('A gateway that stays out of your way')}
            description={t(
              'Use one clear API for your work, connect a client, or explore public open-source challenges.'
            )}
          />
          <div
            id='forge-home-features-title'
            className='forge-home-feature-grid'
          >
            {FEATURE_CARDS.map((feature) => {
              const Icon = feature.icon
              return (
                <article
                  key={feature.title}
                  className='forge-home-feature-card'
                >
                  <div className={`forge-home-feature-icon ${feature.tone}`}>
                    <Icon />
                  </div>
                  <div>
                    <h3>{t(feature.title)}</h3>
                    <p>{t(feature.description)}</p>
                  </div>
                </article>
              )
            })}
          </div>
        </section>

        <section
          className='forge-home-section forge-home-quickstart'
          aria-labelledby='forge-home-quickstart-title'
        >
          <HomeSectionHeading
            eyebrow={t('Get started')}
            title={t('A guide that ships answers')}
            description={t(
              'Use our unified OpenAI-compatible endpoint in your applications'
            )}
          />
          <div className='forge-home-quickstart-grid'>
            <div className='forge-home-steps'>
              {[
                [
                  '1',
                  'Create an API key',
                  'Generate and manage your API access token',
                ],
                [
                  '2',
                  'API token management',
                  'Set API key access restrictions',
                ],
                ['3', 'Connect your client', 'Client setup guide'],
              ].map(([number, title, description]) => (
                <div key={number} className='forge-home-step'>
                  <span className='forge-home-step-number'>{number}</span>
                  <div>
                    <h3>{t(title)}</h3>
                    <p>{t(description)}</p>
                  </div>
                  <ChevronRight className='forge-home-step-arrow' />
                </div>
              ))}
              <div className='forge-home-quick-links'>
                <Link to='/guide' className='forge-home-quick-link'>
                  <Code2 />
                  <span>{t('Read setup guide')}</span>
                  <ArrowRight />
                </Link>
                <Link to='/pricing' className='forge-home-quick-link'>
                  <CircleHelp />
                  <span>{t('View model pricing')}</span>
                  <ArrowRight />
                </Link>
              </div>
            </div>
            <CodePreview tab={codeTab} onTabChange={setCodeTab} />
          </div>
        </section>

        <section
          className='forge-home-section'
          aria-labelledby='forge-home-use-cases-title'
        >
          <HomeSectionHeading
            eyebrow={t('Model Square')}
            title={t('One platform, many uses')}
            description={t(
              'Discover curated AI models, compare pricing and capabilities, and choose the right model for every scenario.'
            )}
          />
          <div
            id='forge-home-use-cases-title'
            className='forge-home-use-case-grid'
          >
            {USE_CASES.map((item) => {
              const Icon = item.icon
              return (
                <article key={item.title} className='forge-home-use-case'>
                  <Icon className='text-primary size-5' />
                  <h3>{t(item.title)}</h3>
                  <p>{t(item.description)}</p>
                </article>
              )
            })}
          </div>
        </section>
      </main>
    </ForgePublicShell>
  )
}
