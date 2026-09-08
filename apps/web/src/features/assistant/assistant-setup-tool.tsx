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
import {
  ArrowRight01Icon,
  ExternalLinkIcon,
  Key01Icon,
  LaptopIcon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import { ClientKeyImport } from './client-key-import'
import {
  detectAssistantSetupPlatform,
  getCCSwitchClaudeProviderJSON,
  getCCSwitchInstallGuide,
  getClaudeInstallCommand,
  getClaudeSessionCommand,
  getCodexAPIKeyCommand,
  getCodexConfig,
  getCodexConfigPath,
  getCodexInstallCommand,
  getOpenAICompatibleClientJSON,
  isMobileSetupPlatform,
  type AssistantSetupPlatform,
} from './setup-guide'

const CHERRY_DOWNLOAD = 'https://www.cherry-ai.com/download'
const CHERRY_DOCS = 'https://docs.cherry-ai.com/pre-basic/providers/newapi'
const CHATBOX_DOWNLOAD =
  'https://chatboxai.app/en/guide/getting-started/download'
const CHATBOX_DOCS = 'https://docs.chatboxai.app/guides/providers'
const CLAUDE_INSTALL_DOCS = 'https://code.claude.com/docs/en/installation'
const CLAUDE_DESKTOP_DOCS = 'https://code.claude.com/docs/en/desktop-quickstart'
const CC_SWITCH_RELEASES = 'https://github.com/farion1231/cc-switch/releases'
const CC_SWITCH_INSTALL_DOCS =
  'https://github.com/farion1231/cc-switch/blob/main/docs/user-manual/en/1-getting-started/1.2-installation.md'
const CC_SWITCH_PROVIDER_DOCS =
  'https://github.com/farion1231/cc-switch/blob/main/docs/user-manual/en/2-providers/2.1-add.md'
const CC_SWITCH_DESKTOP_DOCS =
  'https://github.com/farion1231/cc-switch/blob/main/docs/user-manual/en/2-providers/2.6-claude-desktop.md'
const CHATGPT_DOWNLOAD = 'https://chatgpt.com/download/'
const CHATGPT_WEB = 'https://chatgpt.com/'
const CODEX_DOCS = 'https://developers.openai.com/codex/'
const CODEX_CONFIG_DOCS = 'https://developers.openai.com/codex/config-reference'
const CURSOR_DOWNLOADS = 'https://www.cursor.com/en/downloads'
const OPEN_WEBUI_GUIDE =
  'https://docs.openwebui.com/getting-started/quick-start/connect-a-provider/starting-with-openai-compatible/'
const PLATFORM_LABELS: Record<AssistantSetupPlatform, string> = {
  windows: 'Windows',
  macos: 'macOS',
  linux: 'Linux',
  android: 'Android',
  ios: 'iOS / iPadOS',
}
type ClientTab =
  | 'cherry-studio'
  | 'chatbox'
  | 'claude-code'
  | 'cc-switch'
  | 'claude-desktop'
  | 'chatgpt'
  | 'codex'
  | 'openai-compatible'

type NavigatorWithUserAgentData = Navigator & {
  userAgentData?: { platform?: string }
}

function detectBrowserSetupPlatform(): AssistantSetupPlatform {
  if (typeof navigator === 'undefined') return 'windows'
  const browserNavigator = navigator as NavigatorWithUserAgentData
  return detectAssistantSetupPlatform(
    browserNavigator.userAgentData?.platform,
    browserNavigator.userAgent,
    browserNavigator.maxTouchPoints
  )
}

function CodeSnippet(props: { label: string; value: string }) {
  return (
    <div className='grid gap-2'>
      <span className='text-muted-foreground text-xs'>{props.label}</span>
      <div className='bg-background flex items-start gap-2 rounded-lg border p-3'>
        <pre className='min-w-0 flex-1 overflow-x-auto font-mono text-xs leading-5 break-all whitespace-pre-wrap'>
          {props.value}
        </pre>
        <CopyButton value={props.value} size='sm' />
      </div>
    </div>
  )
}

function ConnectionValue(props: { label: string; value: string }) {
  return (
    <div className='flex flex-wrap items-center justify-between gap-2 border-b py-3 last:border-b-0'>
      <span className='text-muted-foreground text-xs'>{props.label}</span>
      <div className='flex min-w-0 items-center gap-1.5'>
        <code className='text-xs break-all'>{props.value}</code>
        <CopyButton value={props.value} size='sm' />
      </div>
    </div>
  )
}

function OfficialLink(props: { href: string; label: string }) {
  return (
    <Button
      variant='outline'
      size='sm'
      className='h-auto min-h-10 max-w-full text-left whitespace-normal'
      render={<a href={props.href} target='_blank' rel='noopener noreferrer' />}
    >
      {props.label}
      <HugeiconsIcon
        icon={ExternalLinkIcon}
        strokeWidth={2}
        data-icon='inline-end'
        aria-hidden='true'
      />
    </Button>
  )
}

function SetupStep(props: {
  number: number
  title: string
  description: string
}) {
  return (
    <li className='flex items-start gap-3'>
      <Badge
        variant='secondary'
        className='mt-0.5 size-6 shrink-0 justify-center rounded-full p-0'
      >
        {props.number}
      </Badge>
      <div className='min-w-0'>
        <p className='text-sm font-medium'>{props.title}</p>
        <p className='text-muted-foreground mt-1 text-sm leading-6'>
          {props.description}
        </p>
      </div>
    </li>
  )
}

export function AssistantSetupTool(props: {
  rootUrl: string
  openAIBaseUrl: string
  availableModels: string[]
  modelsLoading?: boolean
  developerAccessGranted: boolean
  onCreateKey: () => void
  onRequestAccess: () => void
  onAskQuestion?: (question: string) => void
  publicGuide?: boolean
}) {
  const { t } = useTranslation()
  const [platform, setPlatform] = useState<AssistantSetupPlatform>(
    detectBrowserSetupPlatform
  )
  const [clientTab, setClientTab] = useState<ClientTab>(() =>
    isMobileSetupPlatform(detectBrowserSetupPlatform())
      ? 'chatbox'
      : 'cherry-studio'
  )
  const [selectedModel, setSelectedModel] = useState('')
  const mobile = isMobileSetupPlatform(platform)
  const desktopPlatform = mobile ? 'windows' : platform
  const canConnect = props.developerAccessGranted
  const model =
    canConnect && props.availableModels.includes(selectedModel)
      ? selectedModel
      : canConnect
        ? (props.availableModels[0] ?? '<MODEL_ID>')
        : '<MODEL_ID>'
  const clientNames: Record<ClientTab, string> = {
    'cherry-studio': 'Cherry Studio',
    chatbox: 'Chatbox',
    'claude-code': 'Claude Code',
    'cc-switch': 'CC Switch',
    'claude-desktop': 'Claude Desktop',
    chatgpt: 'ChatGPT',
    codex: 'Codex',
    'openai-compatible': t('OpenAI-compatible clients'),
  }
  const clients: ClientTab[] = mobile
    ? ['chatbox', 'chatgpt']
    : [
        'cherry-studio',
        'chatbox',
        'cc-switch',
        'claude-code',
        'codex',
        'claude-desktop',
        'chatgpt',
        'openai-compatible',
      ]
  const ccSwitchInstall = getCCSwitchInstallGuide(desktopPlatform)

  const connectionValues = (root = false) =>
    canConnect ? (
      <div className='rounded-xl border px-4'>
        <ConnectionValue
          label={t(root ? 'API endpoint root' : 'OpenAI-compatible Base URL')}
          value={root ? props.rootUrl : props.openAIBaseUrl}
        />
        <ConnectionValue label={t('Model ID')} value={model} />
        <ConnectionValue label={t('API key')} value='<YOUR_API_KEY>' />
      </div>
    ) : null
  const createKey = canConnect ? (
    <Button
      type='button'
      variant='outline'
      className='min-h-10'
      onClick={props.onCreateKey}
    >
      <HugeiconsIcon
        icon={Key01Icon}
        strokeWidth={2}
        data-icon='inline-start'
        aria-hidden='true'
      />
      {t('Create API key')}
    </Button>
  ) : null

  const installChatDescription =
    clientTab === 'cherry-studio'
      ? t(
          'Open the official download page, choose {{platform}}, install Cherry Studio, and open it.',
          { platform: PLATFORM_LABELS[platform] }
        )
      : platform === 'ios'
        ? t(
            'Open the official download guide, follow its App Store link, and install Chatbox AI on your iPhone or iPad.'
          )
        : platform === 'android'
          ? t(
              'Open the official download guide and use its Google Play or official APK link. Install Chatbox AI, then open it.'
            )
          : t(
              'Open the official download guide, choose {{platform}}, install Chatbox, and open it.',
              { platform: PLATFORM_LABELS[platform] }
            )

  return (
    <Card className='min-w-0 overflow-hidden rounded-2xl'>
      <CardHeader className='gap-3 p-5 sm:p-6'>
        <CardTitle className='flex items-center gap-2'>
          <HugeiconsIcon
            icon={LaptopIcon}
            className='size-5'
            strokeWidth={2}
            aria-hidden='true'
          />
          {t('Client setup guide')}
        </CardTitle>
        <CardDescription className='text-sm leading-6'>
          {t(
            'Choose your device and app. Follow the steps from download to your first reply, with help whenever you need it.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='grid gap-6 px-5 pb-5 sm:px-6 sm:pb-6'>
        <div className='grid gap-2.5'>
          <span className='text-sm font-medium'>{t('Platform')}</span>
          <div className='flex flex-wrap gap-2' aria-label={t('Platform')}>
            {(Object.keys(PLATFORM_LABELS) as AssistantSetupPlatform[]).map(
              (item) => (
                <Button
                  key={item}
                  type='button'
                  size='sm'
                  className='min-h-10 px-3'
                  variant={platform === item ? 'default' : 'outline'}
                  aria-pressed={platform === item}
                  onClick={() => {
                    setPlatform(item)
                    if (isMobileSetupPlatform(item)) setClientTab('chatbox')
                    else if (isMobileSetupPlatform(platform)) {
                      setClientTab('cherry-studio')
                    }
                  }}
                >
                  {PLATFORM_LABELS[item]}
                </Button>
              )
            )}
          </div>
          {mobile ? (
            <p className='text-muted-foreground text-sm leading-6'>
              {t(
                'Chatbox works on phones and tablets. Desktop coding tools are available when you choose a desktop platform.'
              )}
            </p>
          ) : null}
        </div>

        {!canConnect ? (
          <Alert>
            <AlertTitle>
              {t(
                props.publicGuide
                  ? 'Start with the installation'
                  : 'Ask for L1 access'
              )}
            </AlertTitle>
            <AlertDescription>
              {t(
                props.publicGuide
                  ? 'Browse the download and setup steps freely. Sign in and complete access setup to see your connection values and create an API key.'
                  : 'You can install clients while L0 access is under review. API requests become available after L1 approval.'
              )}
            </AlertDescription>
          </Alert>
        ) : (
          <label className='grid gap-2 text-sm font-medium'>
            {t('Model ID')}
            <NativeSelect
              value={model}
              disabled={
                props.modelsLoading || props.availableModels.length === 0
              }
              onChange={(event) => setSelectedModel(event.target.value)}
              aria-label={t('Model ID')}
            >
              {props.modelsLoading || props.availableModels.length === 0 ? (
                <NativeSelectOption value='<MODEL_ID>'>
                  {t(
                    props.modelsLoading
                      ? 'Loading current models...'
                      : 'No available models'
                  )}
                </NativeSelectOption>
              ) : (
                props.availableModels.map((item) => (
                  <NativeSelectOption key={item} value={item}>
                    {item}
                  </NativeSelectOption>
                ))
              )}
            </NativeSelect>
          </label>
        )}

        <Tabs
          value={clientTab}
          onValueChange={(value) => setClientTab(value as ClientTab)}
          className='min-w-0'
        >
          <TabsList className='flex h-auto w-full flex-wrap justify-start gap-1.5 p-1.5 group-data-horizontal/tabs:h-auto'>
            {clients.map((client) => (
              <TabsTrigger
                key={client}
                value={client}
                className='h-auto min-h-11 max-w-full flex-none px-3 py-2 text-sm whitespace-normal'
              >
                {clientNames[client]}
              </TabsTrigger>
            ))}
          </TabsList>

          {(['cherry-studio', 'chatbox'] as const)
            .filter((client) => clients.includes(client))
            .map((client) => (
              <TabsContent
                key={client}
                value={client}
                className='mt-5 grid gap-5'
              >
                <div className='flex flex-wrap items-center gap-2'>
                  <Badge variant='secondary'>
                    {t(
                      mobile ? 'For phones and tablets' : 'No terminal needed'
                    )}
                  </Badge>
                  <p className='text-muted-foreground text-sm'>
                    {t('Download → connect → first reply')}
                  </p>
                </div>
                <ol
                  className='grid gap-5'
                  aria-label={t('{{client}} setup steps', {
                    client: clientNames[client],
                  })}
                >
                  <SetupStep
                    number={1}
                    title={t('Download and install {{client}}', {
                      client: clientNames[client],
                    })}
                    description={installChatDescription}
                  />
                  <SetupStep
                    number={2}
                    title={t('Choose a model provider')}
                    description={
                      client === 'cherry-studio'
                        ? t(
                            'Open Settings → Model Service → New API. If your version has no New API preset, add an OpenAI-compatible provider named LMM.'
                          )
                        : t(
                            'Open Settings → Model Provider → Add. Name it LMM and select OpenAI API Compatible. Use your own API key connection.'
                          )
                    }
                  />
                  <SetupStep
                    number={3}
                    title={t('Enter the connection values')}
                    description={
                      client === 'cherry-studio'
                        ? t(
                            'Paste the API endpoint root into API Address and your private key into API Key. The app adds the version path automatically; do not append /v1 or /chat/completions.'
                          )
                        : t(
                            'Paste the API endpoint root into API Host and your private key into API Key. Keep API Path as /v1/chat/completions; do not add /v1 to API Host.'
                          )
                    }
                  />
                  <SetupStep
                    number={4}
                    title={t('Add a model and check the connection')}
                    description={
                      client === 'cherry-studio'
                        ? t(
                            'Fetch models or add an exact Model ID from this guide. Run Check, then turn on the provider switch in the top right.'
                          )
                        : t(
                            'Add at least one exact Model ID from this guide. Save the provider, then run Check. Start with a text model.'
                          )
                    }
                  />
                  <SetupStep
                    number={5}
                    title={t('Send your first message')}
                    description={t(
                      'Create a new chat, choose the LMM provider and your model, and send a short greeting. A normal reply confirms the setup. Test requests use your API balance.'
                    )}
                  />
                </ol>
                {connectionValues(true)}
                {canConnect && client === 'chatbox' ? (
                  <ConnectionValue
                    label={t('API path')}
                    value='/v1/chat/completions'
                  />
                ) : null}
                <div className='flex flex-wrap gap-2'>
                  <OfficialLink
                    href={
                      client === 'cherry-studio'
                        ? CHERRY_DOWNLOAD
                        : CHATBOX_DOWNLOAD
                    }
                    label={t('Download {{client}}', {
                      client: clientNames[client],
                    })}
                  />
                  <OfficialLink
                    href={
                      client === 'cherry-studio' ? CHERRY_DOCS : CHATBOX_DOCS
                    }
                    label={t('Official setup guide')}
                  />
                  {createKey}
                </div>
              </TabsContent>
            ))}

          {!mobile ? (
            <>
              <TabsContent value='cc-switch' className='mt-5 grid gap-5'>
                <p className='text-muted-foreground text-sm leading-6'>
                  {t(
                    'Use CC Switch to manage Claude Code and Codex providers. Install the coding tool you want to use as well.'
                  )}
                </p>
                <ol
                  className='grid gap-5'
                  aria-label={t('CC Switch setup steps')}
                >
                  <SetupStep
                    number={1}
                    title={t('Open the official CC Switch app')}
                    description={t(
                      'Install and launch CC Switch from the official release or package.'
                    )}
                  />
                  <SetupStep
                    number={2}
                    title={t('Create or select an API key')}
                    description={t(
                      'Create a key here or copy an existing key from API Keys. Keep it out of assistant messages.'
                    )}
                  />
                  <SetupStep
                    number={3}
                    title={t('Import and review the provider')}
                    description={t(
                      'Use the import panel below, choose Claude Code or Codex, and confirm inside CC Switch. Enable the imported provider after reviewing the endpoint and model.'
                    )}
                  />
                  <SetupStep
                    number={4}
                    title={t('Run a small test request')}
                    description={t(
                      'Open the coding tool in a new terminal and send a short test. Confirm the active provider and model before starting a larger task.'
                    )}
                  />
                </ol>
                {ccSwitchInstall.command ? (
                  <CodeSnippet
                    label={t('Install command')}
                    value={ccSwitchInstall.command}
                  />
                ) : (
                  <p className='bg-muted/30 rounded-xl border p-4 text-sm leading-6'>
                    {t(
                      'Download {{artifact}} from GitHub Releases, open it, and finish the Windows installer.',
                      { artifact: ccSwitchInstall.artifact }
                    )}
                  </p>
                )}
                <div className='flex flex-wrap gap-2'>
                  <OfficialLink
                    href={CC_SWITCH_RELEASES}
                    label={t('Open official releases')}
                  />
                  <OfficialLink
                    href={CC_SWITCH_INSTALL_DOCS}
                    label={t('Installation manual')}
                  />
                  <OfficialLink
                    href={CC_SWITCH_PROVIDER_DOCS}
                    label={t('Provider manual')}
                  />
                  {createKey}
                </div>
                {canConnect ? (
                  <>
                    <ClientKeyImport
                      key={platform}
                      rootUrl={props.rootUrl}
                      openAIBaseUrl={props.openAIBaseUrl}
                      model={model}
                      availableModels={props.availableModels}
                      modelsLoading={props.modelsLoading}
                    />
                    <details className='rounded-xl border p-4'>
                      <summary className='cursor-pointer text-sm font-medium'>
                        {t('Manual setup fallback')}
                      </summary>
                      <div className='mt-4 grid gap-4'>
                        <p className='text-muted-foreground text-sm leading-6'>
                          {t(
                            'In CC Switch, add a custom provider. Claude Code uses the API endpoint root; Codex uses the Base URL ending in /v1. Paste your key directly into the app, then save and enable the provider.'
                          )}
                        </p>
                        {connectionValues(true)}
                        <ConnectionValue
                          label={t('Codex Base URL')}
                          value={props.openAIBaseUrl}
                        />
                        <CodeSnippet
                          label={t('Claude Code provider JSON')}
                          value={getCCSwitchClaudeProviderJSON(
                            props.rootUrl,
                            model
                          )}
                        />
                      </div>
                    </details>
                  </>
                ) : (
                  <p className='text-muted-foreground text-sm'>
                    {t('CC Switch one-click import')}:{' '}
                    {t('Available after access setup and API key creation.')}
                  </p>
                )}
              </TabsContent>

              <TabsContent value='claude-code' className='mt-5 grid gap-5'>
                <ol
                  className='grid gap-5'
                  aria-label={t('Claude Code setup steps')}
                >
                  <SetupStep
                    number={1}
                    title={t('Install Claude Code')}
                    description={t(
                      'Open PowerShell on Windows or Terminal on macOS and Linux. Run the install command, then check claude --version.'
                    )}
                  />
                  <SetupStep
                    number={2}
                    title={t('Configure the current terminal')}
                    description={t(
                      'Use the session configuration below. Replace only the API key placeholder with your private key in your terminal.'
                    )}
                  />
                  <SetupStep
                    number={3}
                    title={t('Run a small test request')}
                    description={t(
                      'Open your project folder, run claude, and send a short test message. Keep the configured terminal open.'
                    )}
                  />
                </ol>
                <CodeSnippet
                  label={t('Install command')}
                  value={getClaudeInstallCommand(desktopPlatform)}
                />
                {canConnect ? (
                  <CodeSnippet
                    label={t(
                      platform === 'windows'
                        ? 'PowerShell session configuration'
                        : 'Shell session configuration'
                    )}
                    value={getClaudeSessionCommand(
                      desktopPlatform,
                      props.rootUrl,
                      model
                    )}
                  />
                ) : null}
                <div className='flex flex-wrap gap-2'>
                  <OfficialLink
                    href={CLAUDE_INSTALL_DOCS}
                    label={t('Official installation guide')}
                  />
                  {createKey}
                </div>
              </TabsContent>

              <TabsContent value='codex' className='mt-5 grid gap-5'>
                <ol className='grid gap-5' aria-label={t('Codex setup steps')}>
                  <SetupStep
                    number={1}
                    title={t('Install Codex')}
                    description={t(
                      'Install the official CLI, then verify it with codex --version.'
                    )}
                  />
                  <SetupStep
                    number={2}
                    title={t('Add the LMM provider')}
                    description={t(
                      'Save the provider block in the user-level config file. Codex uses the Responses API, so keep the /v1 Base URL.'
                    )}
                  />
                  <SetupStep
                    number={3}
                    title={t('Set the API key in your shell')}
                    description={t(
                      'Run the API-key command in the current terminal. The secret is not stored in config.toml.'
                    )}
                  />
                  <SetupStep
                    number={4}
                    title={t('Verify the active provider')}
                    description={t(
                      'Run codex in a project and use /status to verify the LMM provider and selected model.'
                    )}
                  />
                </ol>
                <CodeSnippet
                  label={t('Install command')}
                  value={getCodexInstallCommand(desktopPlatform)}
                />
                {canConnect ? (
                  <>
                    <CodeSnippet
                      label={t('API key')}
                      value={getCodexAPIKeyCommand(desktopPlatform)}
                    />
                    <CodeSnippet
                      label={t('Codex configuration: {{path}}', {
                        path: getCodexConfigPath(desktopPlatform),
                      })}
                      value={getCodexConfig(props.openAIBaseUrl, model)}
                    />
                  </>
                ) : null}
                <div className='flex flex-wrap gap-2'>
                  <OfficialLink
                    href={CODEX_DOCS}
                    label={t('Official Codex guide')}
                  />
                  <OfficialLink
                    href={CODEX_CONFIG_DOCS}
                    label={t('Configuration reference')}
                  />
                  {createKey}
                </div>
              </TabsContent>

              <TabsContent value='claude-desktop' className='mt-5 grid gap-5'>
                {platform === 'linux' ? (
                  <Alert>
                    <AlertTitle>
                      {t(
                        'CC Switch Desktop provider setup is not available on Linux'
                      )}
                    </AlertTitle>
                    <AlertDescription>
                      {t(
                        'Choose Cherry Studio for desktop chat or Claude Code for terminal use on Linux.'
                      )}
                    </AlertDescription>
                  </Alert>
                ) : (
                  <>
                    <ol
                      className='grid gap-5'
                      aria-label={t('Claude Desktop setup steps')}
                    >
                      <SetupStep
                        number={1}
                        title={t('Install Claude Desktop')}
                        description={t(
                          'Download the official app for {{platform}}, install it, and launch it once.',
                          { platform: PLATFORM_LABELS[platform] }
                        )}
                      />
                      <SetupStep
                        number={2}
                        title={t('Open Claude Desktop in CC Switch')}
                        description={t(
                          'In the CC Switch app switcher, select Claude Desktop. If it is hidden, enable it under Settings, General, Homepage Display.'
                        )}
                      />
                      <SetupStep
                        number={3}
                        title={t('Import the Claude Code provider')}
                        description={t(
                          'Choose Import existing providers from Claude Code, or add a custom provider with the endpoint and API key below.'
                        )}
                      />
                      <SetupStep
                        number={4}
                        title={t('Enable model mapping')}
                        description={t(
                          'Map a supported model to the Sonnet role, enable local routing, and keep CC Switch running. Enable the provider, fully quit Claude Desktop, then reopen it.'
                        )}
                      />
                    </ol>
                    {connectionValues(true)}
                    {canConnect ? (
                      <ConnectionValue
                        label={t('API format')}
                        value='Anthropic Messages'
                      />
                    ) : null}
                  </>
                )}
                <div className='flex flex-wrap gap-2'>
                  <OfficialLink
                    href={CLAUDE_DESKTOP_DOCS}
                    label={t('Official Desktop guide')}
                  />
                  <OfficialLink
                    href={CC_SWITCH_DESKTOP_DOCS}
                    label={t('CC Switch Desktop manual')}
                  />
                  {createKey}
                </div>
              </TabsContent>

              <TabsContent
                value='openai-compatible'
                className='mt-5 grid gap-5'
              >
                <Alert>
                  <AlertTitle>
                    {t('Codex, Cursor, Open WebUI, and more')}
                  </AlertTitle>
                  <AlertDescription>
                    {t(
                      'Choose the custom OpenAI-compatible provider in your client. Check its official guide for supported endpoints and model features.'
                    )}
                  </AlertDescription>
                </Alert>
                <ol
                  className='grid gap-5'
                  aria-label={t('OpenAI-compatible client setup steps')}
                >
                  <SetupStep
                    number={1}
                    title={t('Install the official client')}
                    description={t(
                      'Use the official download or installation guide for Codex, Cursor, Open WebUI, or another compatible client.'
                    )}
                  />
                  <SetupStep
                    number={2}
                    title={t('Choose a custom provider')}
                    description={t(
                      'Open the client provider, model, or API settings and select its custom OpenAI-compatible option.'
                    )}
                  />
                  <SetupStep
                    number={3}
                    title={t('Paste the three connection values')}
                    description={t(
                      'Paste the Base URL, exact Model ID, and API key shown below. Keep the API key private.'
                    )}
                  />
                  <SetupStep
                    number={4}
                    title={t('Run a small test request')}
                    description={t(
                      'Verify the connection with a short test before enabling automation, agents, or long-running tasks.'
                    )}
                  />
                </ol>
                {connectionValues()}
                {canConnect ? (
                  <CodeSnippet
                    label={t('OpenAI-compatible connection JSON')}
                    value={getOpenAICompatibleClientJSON(
                      props.openAIBaseUrl,
                      model
                    )}
                  />
                ) : null}
                <div className='flex flex-wrap gap-2'>
                  <OfficialLink href={CODEX_DOCS} label='Codex' />
                  <OfficialLink href={CURSOR_DOWNLOADS} label='Cursor' />
                  <OfficialLink href={OPEN_WEBUI_GUIDE} label='Open WebUI' />
                  {createKey}
                </div>
              </TabsContent>
            </>
          ) : null}

          <TabsContent value='chatgpt' className='mt-5 grid gap-5'>
            <Alert>
              <AlertTitle>
                {t('Official ChatGPT desktop uses OpenAI sign-in')}
              </AlertTitle>
              <AlertDescription>
                {t(
                  'The official ChatGPT app does not accept this service Base URL and API key as a custom provider. Choose Chatbox or Cherry Studio to use your API balance.'
                )}
              </AlertDescription>
            </Alert>
            {platform === 'linux' ? (
              <Alert>
                <AlertTitle>
                  {t(
                    'The official ChatGPT desktop app is not available for Linux'
                  )}
                </AlertTitle>
                <AlertDescription>
                  {t('Use ChatGPT in your browser')}
                </AlertDescription>
              </Alert>
            ) : null}
            {connectionValues()}
            <div className='flex flex-wrap gap-2'>
              <OfficialLink
                href={platform === 'linux' ? CHATGPT_WEB : CHATGPT_DOWNLOAD}
                label={t(
                  platform === 'linux'
                    ? 'Open ChatGPT in browser'
                    : 'Download official ChatGPT'
                )}
              />
              <Button
                type='button'
                variant='outline'
                className='min-h-10'
                onClick={() => setClientTab('chatbox')}
              >
                {t('Set up Chatbox')}
              </Button>
            </div>
          </TabsContent>
        </Tabs>

        {!canConnect ? (
          <Button
            type='button'
            className='min-h-11'
            onClick={props.onRequestAccess}
          >
            {t(
              props.publicGuide
                ? 'Continue to account setup'
                : 'Unlock L1 access'
            )}
            <HugeiconsIcon
              icon={ArrowRight01Icon}
              strokeWidth={2}
              data-icon='inline-end'
              aria-hidden='true'
            />
          </Button>
        ) : null}

        {props.onAskQuestion ? (
          <div className='border-t pt-5'>
            <Button
              type='button'
              variant='outline'
              className='h-auto min-h-11 w-full whitespace-normal'
              onClick={() =>
                props.onAskQuestion?.(
                  t(
                    'I am setting up {{client}} on {{platform}}. Please walk me through downloading it, entering the connection settings, and sending a first test. Ask which step I am on, and never ask for my API key.',
                    {
                      client: clientNames[clientTab],
                      platform: PLATFORM_LABELS[platform],
                      interpolation: { escapeValue: false },
                    }
                  )
                )
              }
            >
              {t('Walk me through this with the AI assistant')}
            </Button>
          </div>
        ) : null}
      </CardContent>
    </Card>
  )
}
