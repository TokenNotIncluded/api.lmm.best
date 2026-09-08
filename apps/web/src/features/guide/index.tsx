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
  ArrowRight,
  Check,
  ChevronDown,
  Copy,
  MessageCircle,
  ShieldCheck,
  Terminal,
} from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { PublicLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { getAssistantAvailableModels } from '@/features/assistant/api'
import {
  requestAssistantOpen,
  type AssistantPresetId,
} from '@/features/assistant/assistant-events'
import { AssistantSetupTool } from '@/features/assistant/assistant-setup-tool'
import { useStatus } from '@/hooks/use-status'
import {
  getOnboardingState,
  isConsoleActivated,
} from '@/lib/console-activation'
import { useAuthStore } from '@/stores/auth-store'

const COPY = {
  zh: {
    eyebrow: '新手指南',
    title: '第一次使用，从这里开始。',
    intro:
      '选好设备和软件，跟着步骤完成连接。下载安装可以先看；需要账号帮助时，让 AI 助手陪你继续。',
    start: '选择我的客户端',
    assistant: '让 AI 一步步带我配置',
    manualIntro:
      '选好设备和软件，跟着步骤完成连接。下载安装可以先看，账号和访问权限问题也可以联系支持。',
    contactSupport: '联系支持',
    requestManualAccess: '联系支持申请访问',
    supportTitle: '需要人工帮助？',
    supportBody:
      '访问申请或配置遇到问题时，可通过下方邮箱联系支持。说明账号、设备与具体问题，记得隐藏密钥。',
    supportTicket: '提交支持工单',
    manualTroubleBody:
      '保留错误码、客户端名称和请求 ID。点开相应问题查看处理方法；仍未解决时，可以联系支持。',
    guestAssistant: '登录，让 AI 带我配置',
    stages: [
      '选择软件',
      '下载安装',
      '申请访问并创建密钥',
      '填写配置',
      '开始第一次对话',
    ],
    setupTitle: '从你正在使用的设备开始',
    setupBody:
      '选择下面的设备和软件，查看官方安装入口与配置步骤。已有软件的用户可以直接查看连接配置。',
    accountTitle: '准备好你的账号',
    accountBody:
      '访问权限通过后，就可以创建专用密钥，把 LMM 连接到自己的软件。',
    accountSteps: ['获得 API 访问权限', '创建一把 API 密钥', '完成第一次请求'],
    complete: '已完成',
    pending: '待完成',
    signIn: '登录并继续配置',
    requestAccess: '让助手帮我申请访问',
    createKey: '创建密钥与快捷导入',
    requestAccessQuestion:
      '我正在阅读新手指南，想使用 LMM API。请说明申请访问需要哪些信息，并带我完成申请。',
    createKeyQuestion:
      '我已阅读新手指南。请帮我创建一把适合首次使用的 API 密钥，说明权限、额度与分组的选择，然后带我将密钥导入我使用的客户端。',
    setupQuestion:
      '我第一次使用 LMM。请先问我使用的设备、软件和用途，再带我从官方下载、安装、申请访问、创建密钥、配置地址与模型，到发出第一条消息。每一步请说明点击哪里以及成功的标志。',
    securityTitle: '密钥只填进你信任的客户端',
    securityBody:
      '聊天、截图和问题反馈中请遮住 API Key。若已泄露，到密钥页面撤销并重新创建；排查问题时提供请求 ID 即可。',
    modelsError:
      '暂时无法加载可用模型。你可以继续查看安装步骤，配置前请重试获取准确的模型 ID。',
    retry: '重新加载模型',
    firstTitle: '发一条消息，确认连接成功',
    firstBody:
      '保存配置后，新建对话，选择当前账号可用的模型，发送下面这句话。收到回复后，可到用量记录确认请求与消耗。',
    firstMessage: '你好，请用一句话介绍你能帮助我做什么。',
    usage: '查看用量记录',
    modelList: '查看模型与价格',
    troubleTitle: '卡在某一步？先看这里。',
    troubleBody:
      '保留错误码、客户端名称和请求 ID。点开相应问题，或把具体情况交给助手继续排查。',
    troubles: [
      {
        code: '401',
        title: '密钥验证失败',
        body: '重新复制 API Key，检查前后空格，确认密钥未过期或被撤销，并填入客户端的 API Key 字段。仍失败时，提供错误信息和请求 ID，隐藏密钥。',
        question:
          '我在配置客户端时遇到 HTTP 401。请先问我设备、客户端、错误信息及请求 ID（不要索取 API Key），再逐步排查密钥填写、有效期与权限。',
      },
      {
        code: '404',
        title: '接口地址或模型有误',
        body: '按上方所选客户端的说明核对地址，避免重复拼接 /v1。模型名称须与账号的实时可用模型 ID 完全一致，再发起一次请求。',
        question:
          '我在配置客户端时遇到 HTTP 404。请先确认客户端名称、Base URL、模型 ID 和错误信息，帮我检查路径拼接与模型是否可用。不要索取密钥。',
      },
      {
        code: '429',
        title: '请求受限，稍后再试',
        body: '先暂停连续重试，查看错误详情。如果提示限流，等待后降低并发；如果提示额度不足，检查密钥限额、余额和套餐。错误详情能帮助区分原因。',
        question:
          '我在首次使用时遇到 HTTP 429。请根据完整错误信息判断是请求频率、配额还是上游限流，并给出具体处理步骤。请提醒我隐藏密钥后再分享错误信息。',
      },
    ],
    askAboutError: '让 AI 帮我排查',
    guestError: '登录后让 AI 帮我排查',
    developerTitle: '开发者：用 curl 测试连接',
    developerBody:
      '在本地 Bash 或兼容终端执行。替换示例密钥与模型 ID；示例请求会按所选模型计费。',
    copy: '复制',
    copied: '已复制',
    copyFailed: '复制失败，请选中内容手动复制。',
  },
  en: {
    eyebrow: 'Getting started',
    title: 'Your first connection starts here.',
    intro:
      'Choose your device and app, then follow the steps to connect. Browse installation instructions now, and continue with the AI assistant whenever you need help.',
    start: 'Choose my client',
    assistant: 'Guide me with AI',
    manualIntro:
      'Choose your device and app, then follow the setup steps. Browse installation instructions now, and contact support for help with your account or access.',
    contactSupport: 'Contact support',
    requestManualAccess: 'Contact support for access',
    supportTitle: 'Need help from a person?',
    supportBody:
      'For access requests or setup issues, contact the support email below. Include your account, device and issue, with your API key hidden.',
    supportTicket: 'Open a support ticket',
    manualTroubleBody:
      'Keep the error code, app name and request ID handy. Open the matching issue below for steps, or contact support if you are still stuck.',
    guestAssistant: 'Sign in for AI guidance',
    stages: [
      'Choose an app',
      'Download and install',
      'Request access and create a key',
      'Configure your client',
      'Send your first message',
    ],
    setupTitle: 'Start with the device you use',
    setupBody:
      'Choose your device and app below for official downloads and setup instructions. Already installed? Continue to the connection settings.',
    accountTitle: 'Prepare your account',
    accountBody:
      'Once API access is approved, create a dedicated key to connect LMM to your app.',
    accountSteps: [
      'Get API access',
      'Create an API key',
      'Complete your first request',
    ],
    complete: 'Complete',
    pending: 'Pending',
    signIn: 'Sign in to continue setup',
    requestAccess: 'Ask the assistant for access',
    createKey: 'Create a key and import',
    requestAccessQuestion:
      'I am reading the getting-started guide and want to use the LMM API. Explain what information is needed for access and guide me through the application.',
    createKeyQuestion:
      'I have read the getting-started guide. Help me create an API key for my first connection, explain permissions, limits and routing groups, then guide me through importing it into my chosen client.',
    setupQuestion:
      'I am new to LMM. First ask which device and app I use and what I want to do, then guide me through the official download, installation, access request, key creation, base URL and model configuration, and my first message. Explain where to click and how to verify each step.',
    securityTitle: 'Enter keys only in trusted clients',
    securityBody:
      'Hide API keys in chats, screenshots and support requests. Revoke and replace a leaked key from API Keys. Share the request ID when troubleshooting.',
    modelsError:
      'Available models could not be loaded. You can keep reading installation steps; retry before configuring an exact model ID.',
    retry: 'Reload models',
    firstTitle: 'Send a message to check your connection',
    firstBody:
      'Save your settings, start a new conversation, select an available model for your account, and send the message below. After receiving a reply, check Usage Logs for the request and its cost.',
    firstMessage: 'Hello! Tell me in one sentence what you can help me with.',
    usage: 'Open usage logs',
    modelList: 'Explore models and pricing',
    troubleTitle: 'Stuck on a step? Start here.',
    troubleBody:
      'Keep the error code, app name and request ID handy. Open the matching issue below, or continue troubleshooting with the assistant.',
    troubles: [
      {
        code: '401',
        title: 'API key authentication failed',
        body: 'Copy your API key again, remove surrounding spaces, and check it has not expired or been revoked. Enter it in the app’s API Key field. If it still fails, share the error and request ID with the key hidden.',
        question:
          'I received HTTP 401 while setting up my client. Ask for my device, client, error message and request ID without asking for my API key, then help me check key entry, expiration and permissions step by step.',
      },
      {
        code: '404',
        title: 'Check the endpoint and model',
        body: 'Follow the address instructions for the selected client and check that /v1 is not duplicated. Use an exact model ID from your account’s available model list, then retry once.',
        question:
          'I received HTTP 404 while setting up my client. Ask for the client, Base URL, model ID and error message, then help me check endpoint paths and model availability. Do not ask for my API key.',
      },
      {
        code: '429',
        title: 'A request limit was reached',
        body: 'Pause repeated retries and read the error details. For rate limits, wait and reduce concurrency. For insufficient quota, check key limits, balance and your plan. The error details help distinguish the cause.',
        question:
          'I received HTTP 429 on my first connection. Use the error details to identify rate limits, quota or upstream throttling and give specific next steps. Remind me to hide the key before sharing the error.',
      },
    ],
    askAboutError: 'Troubleshoot with AI',
    guestError: 'Sign in to troubleshoot with AI',
    developerTitle: 'For developers: test with curl',
    developerBody:
      'Run in a local Bash-compatible terminal. Replace the example key and model ID; the request is billed at the selected model’s rate.',
    copy: 'Copy',
    copied: 'Copied',
    copyFailed: 'Copy failed. Select the content and copy it manually.',
  },
}

function GuideCode({
  label,
  value,
  copy,
}: {
  label: string
  value: string
  copy: (typeof COPY)['en']
}) {
  const [status, setStatus] = useState<'idle' | 'copied' | 'error'>('idle')
  const copyValue = async () => {
    try {
      await navigator.clipboard.writeText(value)
      setStatus('copied')
    } catch {
      setStatus('error')
    }
  }

  return (
    <div className='bg-muted/35 overflow-hidden rounded-xl border'>
      <div className='border-border/70 flex items-center justify-between gap-3 border-b px-4 py-2 text-xs'>
        <span className='text-muted-foreground'>{label}</span>
        <Button
          type='button'
          variant='ghost'
          className='min-h-10'
          onClick={() => void copyValue()}
        >
          {status === 'copied' ? (
            <Check aria-hidden='true' />
          ) : (
            <Copy aria-hidden='true' />
          )}
          {status === 'copied' ? copy.copied : copy.copy}
        </Button>
      </div>
      <pre
        className='overflow-x-auto p-4 text-sm leading-7'
        tabIndex={0}
        aria-label={label}
      >
        <code>{value}</code>
      </pre>
      <p className='sr-only' role='status'>
        {status === 'copied' ? copy.copied : ''}
      </p>
      {status === 'error' ? (
        <p className='text-destructive px-4 pb-4 text-sm' role='alert'>
          {copy.copyFailed}
        </p>
      ) : null}
    </div>
  )
}

export function Guide() {
  const { i18n } = useTranslation()
  const navigate = useNavigate()
  const { status, capabilitiesReady } = useStatus()
  const assistantAvailable =
    capabilitiesReady && status?.assistant?.enabled !== false
  const user = useAuthStore((state) => state.auth.user)
  const copy = i18n.language.toLowerCase().startsWith('zh') ? COPY.zh : COPY.en
  const developerAccessGranted = isConsoleActivated(user)
  const onboarding = getOnboardingState(user)
  const completedSteps = [
    onboarding.activationComplete,
    onboarding.credentialComplete,
    onboarding.firstRequestComplete,
  ]
  const rootUrl =
    typeof window === 'undefined'
      ? 'https://api.lmm.best'
      : window.location.origin
  const modelsQuery = useQuery({
    queryKey: ['guide-available-models', user?.id],
    queryFn: getAssistantAvailableModels,
    enabled: developerAccessGranted,
    staleTime: 60_000,
    retry: false,
  })
  const contactSupport = () => {
    if (developerAccessGranted) {
      void navigate({ to: '/support' })
      return
    }
    const support = document.getElementById('guide-support')
    support?.scrollIntoView({ block: 'center' })
    support?.focus({ preventScroll: true })
  }
  const askAssistant = (
    message: string,
    preset: AssistantPresetId = 'client-setup'
  ) => {
    if (!assistantAvailable) {
      contactSupport()
      return
    }
    requestAssistantOpen(preset, message)
    if (user) {
      void navigate({ to: '/getting-started' })
    } else {
      void navigate({
        to: '/sign-in',
        search: { redirect: '/getting-started' },
      })
    }
  }
  const createKey = () => {
    if (developerAccessGranted && !assistantAvailable) {
      void navigate({ to: '/keys' })
      return
    }
    askAssistant(copy.createKeyQuestion, 'api-key')
  }
  const requestAccess = () => {
    if (!assistantAvailable && !user) {
      void navigate({ to: '/sign-in', search: { redirect: '/guide' } })
      return
    }
    askAssistant(copy.requestAccessQuestion, 'onboarding')
  }
  const example = String.raw`export LMM_API_KEY='paste-your-key-here'
curl ${rootUrl}/v1/chat/completions \
  -H "Authorization: Bearer $LMM_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"your-model-id","messages":[{"role":"user","content":"Hello"}]}'`

  return (
    <PublicLayout showMainContainer={false}>
      <main className='dark:bg-background bg-[#faf9f6]'>
        <div className='mx-auto max-w-6xl px-5 pt-16 pb-20 sm:px-8 sm:pt-24 lg:px-10 lg:pb-28'>
          <header className='max-w-3xl'>
            <p className='text-muted-foreground text-xs font-semibold tracking-[0.18em]'>
              {copy.eyebrow}
            </p>
            <h1 className='mt-5 text-4xl leading-[1.2] font-semibold tracking-tight text-balance sm:text-5xl lg:text-6xl'>
              {copy.title}
            </h1>
            <p className='text-muted-foreground mt-6 max-w-2xl text-base leading-8'>
              {assistantAvailable ? copy.intro : copy.manualIntro}
            </p>
            <div className='mt-8 flex flex-col gap-3 sm:flex-row sm:flex-wrap'>
              <Button
                className='min-h-12 px-5'
                render={<a href='#client-setup' />}
              >
                {copy.start}
                <ArrowRight aria-hidden='true' />
              </Button>
              <Button
                variant='outline'
                className='min-h-12 px-5 whitespace-normal'
                onClick={
                  assistantAvailable
                    ? () => askAssistant(copy.setupQuestion)
                    : contactSupport
                }
              >
                <MessageCircle aria-hidden='true' />
                {assistantAvailable
                  ? user
                    ? copy.assistant
                    : copy.guestAssistant
                  : copy.contactSupport}
              </Button>
            </div>
          </header>

          <ol className='border-border/70 mt-12 grid gap-x-6 gap-y-5 border-y py-7 sm:grid-cols-3 lg:mt-16 lg:grid-cols-5'>
            {copy.stages.map((stage, index) => (
              <li
                key={stage}
                className='flex items-baseline gap-3 text-sm leading-6'
              >
                <span
                  className='text-muted-foreground font-mono text-xs'
                  aria-hidden='true'
                >
                  {String(index + 1).padStart(2, '0')}
                </span>
                <span className='font-medium'>{stage}</span>
              </li>
            ))}
          </ol>

          <div className='mt-12 grid items-start gap-10 lg:mt-16 lg:grid-cols-[minmax(0,1fr)_17rem] lg:gap-12'>
            <div className='min-w-0 space-y-12'>
              <section
                id='client-setup'
                className='scroll-mt-24'
                aria-labelledby='client-setup-title'
              >
                <h2
                  id='client-setup-title'
                  className='text-2xl font-semibold tracking-tight'
                >
                  {copy.setupTitle}
                </h2>
                <p className='text-muted-foreground mt-3 text-sm leading-7'>
                  {copy.setupBody}
                </p>
                <div className='mt-6'>
                  <AssistantSetupTool
                    publicGuide
                    rootUrl={rootUrl}
                    openAIBaseUrl={`${rootUrl}/v1`}
                    availableModels={
                      developerAccessGranted ? (modelsQuery.data ?? []) : []
                    }
                    modelsLoading={modelsQuery.isLoading}
                    developerAccessGranted={developerAccessGranted}
                    onCreateKey={createKey}
                    onRequestAccess={requestAccess}
                    onAskQuestion={
                      assistantAvailable ? askAssistant : undefined
                    }
                  />
                </div>
                {developerAccessGranted && modelsQuery.isError ? (
                  <div className='mt-4 rounded-xl border p-4' role='alert'>
                    <p className='text-muted-foreground text-sm leading-6'>
                      {copy.modelsError}
                    </p>
                    <Button
                      variant='outline'
                      className='mt-3 min-h-11'
                      disabled={modelsQuery.isFetching}
                      onClick={() => void modelsQuery.refetch()}
                    >
                      {copy.retry}
                    </Button>
                  </div>
                ) : null}
              </section>

              <section aria-labelledby='first-message-title'>
                <h2
                  id='first-message-title'
                  className='text-2xl font-semibold tracking-tight'
                >
                  {copy.firstTitle}
                </h2>
                <p className='text-muted-foreground mt-3 text-sm leading-7'>
                  {copy.firstBody}
                </p>
                <blockquote className='bg-background mt-5 rounded-xl border px-5 py-5 text-sm leading-7'>
                  {copy.firstMessage}
                </blockquote>
                <div className='mt-4 flex flex-wrap gap-x-5 gap-y-3'>
                  {developerAccessGranted ? (
                    <Link
                      to='/usage-logs'
                      className='text-sm font-medium underline underline-offset-4'
                    >
                      {copy.usage}
                    </Link>
                  ) : null}
                  <Link
                    to='/pricing'
                    className='text-sm font-medium underline underline-offset-4'
                  >
                    {copy.modelList}
                  </Link>
                </div>
              </section>

              <section aria-labelledby='troubleshooting-title'>
                <h2
                  id='troubleshooting-title'
                  className='text-2xl font-semibold tracking-tight'
                >
                  {copy.troubleTitle}
                </h2>
                <p className='text-muted-foreground mt-3 text-sm leading-7'>
                  {assistantAvailable
                    ? copy.troubleBody
                    : copy.manualTroubleBody}
                </p>
                <div className='mt-5 space-y-3'>
                  {copy.troubles.map((trouble) => (
                    <details
                      key={trouble.code}
                      className='group bg-background rounded-xl border'
                    >
                      <summary className='focus-visible:outline-ring flex min-h-16 cursor-pointer list-none items-center gap-3 rounded-xl px-4 py-4 focus-visible:outline-2 focus-visible:outline-offset-2 [&::-webkit-details-marker]:hidden'>
                        <span className='text-muted-foreground shrink-0 font-mono text-xs'>
                          {trouble.code}
                        </span>
                        <span className='min-w-0 flex-1 text-sm leading-6 font-medium'>
                          {trouble.title}
                        </span>
                        <ChevronDown
                          className='text-muted-foreground size-4 shrink-0 transition-transform group-open:rotate-180'
                          aria-hidden='true'
                        />
                      </summary>
                      <div className='px-4 pb-5 sm:px-5'>
                        <p className='text-muted-foreground text-sm leading-7'>
                          {trouble.body}
                        </p>
                        <Button
                          variant='outline'
                          className='mt-4 min-h-11 max-w-full whitespace-normal'
                          onClick={
                            assistantAvailable
                              ? () => askAssistant(trouble.question)
                              : contactSupport
                          }
                        >
                          <MessageCircle aria-hidden='true' />
                          {assistantAvailable
                            ? user
                              ? copy.askAboutError
                              : copy.guestError
                            : copy.contactSupport}
                        </Button>
                      </div>
                    </details>
                  ))}
                </div>
              </section>

              <details className='group border-t pt-5'>
                <summary className='focus-visible:outline-ring flex min-h-12 cursor-pointer list-none items-center gap-3 rounded-lg focus-visible:outline-2 focus-visible:outline-offset-2 [&::-webkit-details-marker]:hidden'>
                  <Terminal
                    className='text-muted-foreground size-4 shrink-0'
                    aria-hidden='true'
                  />
                  <span className='min-w-0 flex-1 text-sm font-medium'>
                    {copy.developerTitle}
                  </span>
                  <ChevronDown
                    className='text-muted-foreground size-4 shrink-0 transition-transform group-open:rotate-180'
                    aria-hidden='true'
                  />
                </summary>
                <p className='text-muted-foreground mt-3 text-sm leading-7'>
                  {copy.developerBody}
                </p>
                <div className='mt-4'>
                  <GuideCode
                    label='Bash · OpenAI-compatible API'
                    value={example}
                    copy={copy}
                  />
                </div>
              </details>
            </div>

            <aside className='space-y-7 lg:sticky lg:top-24'>
              <section className='bg-background rounded-2xl border p-6'>
                <h2 className='text-base font-semibold'>{copy.accountTitle}</h2>
                <p className='text-muted-foreground mt-3 text-sm leading-7'>
                  {copy.accountBody}
                </p>
                <ol className='mt-5 space-y-4'>
                  {copy.accountSteps.map((step, index) => (
                    <li
                      key={step}
                      className='flex items-start gap-2.5 text-sm leading-6'
                    >
                      <span
                        className='bg-muted mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full text-xs'
                        aria-label={
                          completedSteps[index] ? copy.complete : copy.pending
                        }
                      >
                        {completedSteps[index] ? (
                          <Check className='size-3.5' aria-hidden='true' />
                        ) : (
                          index + 1
                        )}
                      </span>
                      <span>{step}</span>
                    </li>
                  ))}
                </ol>
                {user ? (
                  <Button
                    className='mt-6 min-h-11 w-full whitespace-normal'
                    onClick={developerAccessGranted ? createKey : requestAccess}
                  >
                    {developerAccessGranted
                      ? copy.createKey
                      : assistantAvailable
                        ? copy.requestAccess
                        : copy.requestManualAccess}
                  </Button>
                ) : (
                  <Button
                    className='mt-6 min-h-11 w-full whitespace-normal'
                    render={
                      <Link to='/sign-in' search={{ redirect: '/guide' }} />
                    }
                  >
                    {copy.signIn}
                  </Button>
                )}
              </section>
              <section
                id='guide-support'
                tabIndex={-1}
                className='focus-visible:outline-ring scroll-mt-24 rounded-xl border p-5 focus-visible:outline-2 focus-visible:outline-offset-2'
              >
                <h2 className='text-sm font-semibold'>{copy.supportTitle}</h2>
                <p className='text-muted-foreground mt-2 text-sm leading-7'>
                  {copy.supportBody}
                </p>
                <a
                  href='mailto:support@lmm.best'
                  className='mt-3 inline-flex min-h-11 items-center text-sm font-medium underline underline-offset-4'
                >
                  support@lmm.best
                </a>
                {developerAccessGranted ? (
                  <Link
                    to='/support'
                    className='inline-flex min-h-11 items-center text-sm font-medium underline underline-offset-4'
                  >
                    {copy.supportTicket}
                  </Link>
                ) : null}
              </section>
              <section className='px-1 sm:px-2'>
                <ShieldCheck
                  className='text-muted-foreground size-5'
                  aria-hidden='true'
                />
                <h2 className='mt-3 text-sm font-semibold'>
                  {copy.securityTitle}
                </h2>
                <p className='text-muted-foreground mt-2 text-sm leading-7'>
                  {copy.securityBody}
                </p>
              </section>
            </aside>
          </div>
        </div>
      </main>
    </PublicLayout>
  )
}
