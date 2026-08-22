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
import { Check, Copy } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { PublicLayout } from '@/components/layout'

type GuideCopy = {
  eyebrow: string
  title: string
  intro: string
  steps: Array<{ title: string; body: string }>
  keyTitle: string
  keyBody: string
  connectionTitle: string
  connectionBody: string
  securityTitle: string
  securityBody: string
  nextTitle: string
  nextBody: string
  codeLabel: string
  copyLabel: string
  copiedLabel: string
}

const COPY: Record<'zh' | 'en', GuideCopy> = {
  zh: {
    eyebrow: '接入指南',
    title: '从注册到第一次调用',
    intro:
      '把 LMM 接入你的客户端只需要几分钟。先创建密钥，再选择分组，最后用兼容接口发出第一条请求。',
    steps: [
      {
        title: '1. 创建 API Key',
        body: '打开控制台的 API 密钥页面，点击创建。密钥只会在创建后显示一次，请立即复制并保存在密码管理器中。',
      },
      {
        title: '2. 选择路由分组',
        body: '分组决定模型路由与计费。需要稳定入口时选择具体分组；希望自动故障切换时使用 auto（如果你的账号可见）。',
      },
      {
        title: '3. 填写客户端',
        body: 'OpenAI 兼容客户端使用 /v1，Claude 客户端使用根地址。模型 ID 必须从控制台的实时模型列表复制，不要凭记忆填写。',
      },
    ],
    keyTitle: '密钥创建后，你需要保存什么？',
    keyBody:
      '只保存 API Key。不要在聊天、工单、截图或 Git 仓库里发送它。Base URL 和模型 ID 不是秘密，可以安全地放进客户端配置。',
    connectionTitle: 'OpenAI 兼容示例',
    connectionBody:
      '把环境变量替换为你自己的密钥，然后在本地终端运行。示例不会产生额外的配置文件。',
    securityTitle: '安全边界',
    securityBody:
      '如果密钥泄露，立即到 API 密钥页面撤销并重新创建。遇到价格、余额或路由问题，提供请求 ID，不要提供密钥本身。',
    nextTitle: '接下来可以做什么？',
    nextBody:
      '查看实时模型价格、连接 Claude Code / Codex / Cursor，或让内置助手按你的客户端一步一步完成配置。',
    codeLabel: 'bash',
    copyLabel: '复制代码',
    copiedLabel: '已复制',
  },
  en: {
    eyebrow: 'Connection guide',
    title: 'From sign-up to your first request',
    intro:
      'Connect LMM to a client in a few minutes: create a key, choose a group, then send a request through a compatible API.',
    steps: [
      {
        title: '1. Create an API key',
        body: 'Open API Keys in the console and create one. The secret is shown once; copy it immediately into a password manager.',
      },
      {
        title: '2. Choose a routing group',
        body: 'The group controls routing and billing. Choose a named group for a stable route, or use auto when it is available to your account.',
      },
      {
        title: '3. Configure your client',
        body: 'OpenAI-compatible clients use /v1; Claude clients use the root URL. Copy an exact model ID from the live model list instead of guessing.',
      },
    ],
    keyTitle: 'What should you save?',
    keyBody:
      'Save only the API key. Never paste it into chat, tickets, screenshots, or a Git repository. The Base URL and model ID are not secrets.',
    connectionTitle: 'OpenAI-compatible example',
    connectionBody:
      'Replace the environment variable with your own key and run this in a local terminal. It does not create a configuration file.',
    securityTitle: 'Security boundary',
    securityBody:
      'If a key leaks, revoke it from API Keys and create a replacement. For billing or routing issues, share a request ID—not the key.',
    nextTitle: 'Where to go next',
    nextBody:
      'Check live model pricing, connect Claude Code / Codex / Cursor, or ask the built-in assistant to guide your exact client setup.',
    codeLabel: 'bash',
    copyLabel: 'Copy code',
    copiedLabel: 'Copied',
  },
}

const EXAMPLE = String.raw`export LMM_API_KEY='paste-your-key-here'
curl https://api.lmm.best/v1/chat/completions \
  -H "Authorization: Bearer $LMM_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"your-model-id","messages":[{"role":"user","content":"Hello"}]}'`

function GuideCode({
  label,
  copyLabel,
  copiedLabel,
  children,
}: {
  label: string
  copyLabel: string
  copiedLabel: string
  children: string
}) {
  const [copied, setCopied] = useState(false)

  const copy = async () => {
    if (!navigator.clipboard) return
    try {
      await navigator.clipboard.writeText(children)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1600)
    } catch {
      setCopied(false)
    }
  }

  return (
    <div className='bg-muted/40 overflow-hidden rounded-lg border'>
      <div className='border-border/70 flex items-center justify-between gap-3 border-b px-4 py-2 text-xs'>
        <span className='text-muted-foreground'>{label}</span>
        <button
          type='button'
          className='text-muted-foreground hover:text-foreground inline-flex items-center gap-1.5 font-medium transition-colors'
          onClick={() => void copy()}
          aria-label={copied ? copiedLabel : copyLabel}
        >
          {copied ? <Check className='size-4' /> : <Copy className='size-4' />}
          <span>{copied ? copiedLabel : copyLabel}</span>
        </button>
      </div>
      <pre className='overflow-x-auto p-4 text-sm leading-6'>
        <code>{children}</code>
      </pre>
    </div>
  )
}

/**
 * Integration guide, following the same public-page language as the
 * rankings page: standard shell, soft top glow, rounded bordered cards,
 * bold tracking-tight headings.
 */
export function Guide() {
  const { i18n } = useTranslation()
  const copy = i18n.language.toLowerCase().startsWith('zh') ? COPY.zh : COPY.en

  return (
    <PublicLayout showMainContainer={false}>
      <div className='relative'>
        <div
          aria-hidden
          className='pointer-events-none absolute inset-x-0 top-0 h-[600px] opacity-20 dark:opacity-[0.10]'
          style={{
            background: [
              'radial-gradient(ellipse 60% 50% at 20% 20%, oklch(0.72 0.18 250 / 80%) 0%, transparent 70%)',
              'radial-gradient(ellipse 50% 40% at 80% 15%, oklch(0.65 0.15 200 / 60%) 0%, transparent 70%)',
              'radial-gradient(ellipse 40% 35% at 50% 70%, oklch(0.70 0.12 280 / 40%) 0%, transparent 70%)',
            ].join(', '),
            maskImage:
              'linear-gradient(to bottom, black 40%, transparent 100%)',
            WebkitMaskImage:
              'linear-gradient(to bottom, black 40%, transparent 100%)',
          }}
        />
        <main className='relative mx-auto max-w-6xl px-4 pt-16 pb-16 sm:px-6 sm:pt-20 md:px-8 md:pb-24'>
          <header className='mx-auto max-w-3xl text-center'>
            <p className='text-muted-foreground text-xs font-medium tracking-[0.2em] uppercase sm:text-xs sm:tracking-[0.32em]'>
              {copy.eyebrow}
            </p>
            <h1 className='mt-4 text-3xl leading-[1.15] font-bold tracking-tight sm:text-4xl md:text-5xl'>
              {copy.title}
            </h1>
            <p className='text-muted-foreground mx-auto mt-4 max-w-2xl text-sm leading-7 sm:text-base'>
              {copy.intro}
            </p>
          </header>

          <div className='mt-12 grid gap-8 lg:grid-cols-[minmax(0,1fr)_minmax(18rem,22rem)] lg:gap-10'>
            <div className='min-w-0 space-y-4'>
              {copy.steps.map((step) => (
                <section
                  key={step.title}
                  className='bg-card/50 border-border/60 rounded-xl border p-5 backdrop-blur md:p-6'
                >
                  <h2 className='text-lg font-semibold tracking-tight'>
                    {step.title}
                  </h2>
                  <p className='text-muted-foreground mt-2 text-sm leading-6'>
                    {step.body}
                  </p>
                </section>
              ))}

              <section className='bg-card/50 border-border/60 rounded-xl border p-5 backdrop-blur md:p-6'>
                <h2 className='text-lg font-semibold tracking-tight'>
                  {copy.connectionTitle}
                </h2>
                <p className='text-muted-foreground mt-2 text-sm leading-6'>
                  {copy.connectionBody}
                </p>
                <div className='mt-4'>
                  <GuideCode
                    label={copy.codeLabel}
                    copyLabel={copy.copyLabel}
                    copiedLabel={copy.copiedLabel}
                  >
                    {EXAMPLE}
                  </GuideCode>
                </div>
              </section>
            </div>

            <aside className='min-w-0 space-y-4'>
              <section className='bg-card/50 border-border/60 rounded-xl border p-5 backdrop-blur'>
                <p className='text-foreground text-sm font-semibold'>
                  {copy.keyTitle}
                </p>
                <p className='text-muted-foreground mt-2 text-sm leading-6'>
                  {copy.keyBody}
                </p>
              </section>
              <section className='bg-card/50 border-border/60 rounded-xl border p-5 backdrop-blur'>
                <p className='text-foreground text-sm font-semibold'>
                  {copy.securityTitle}
                </p>
                <p className='text-muted-foreground mt-2 text-sm leading-6'>
                  {copy.securityBody}
                </p>
              </section>
              <section className='bg-card/50 border-border/60 rounded-xl border p-5 backdrop-blur'>
                <p className='text-foreground text-sm font-semibold'>
                  {copy.nextTitle}
                </p>
                <p className='text-muted-foreground mt-2 text-sm leading-6'>
                  {copy.nextBody}
                </p>
              </section>
            </aside>
          </div>
        </main>
      </div>
    </PublicLayout>
  )
}
