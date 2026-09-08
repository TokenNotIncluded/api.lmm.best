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
export type AssistantDesktopPlatform = 'windows' | 'macos' | 'linux'
export type AssistantSetupPlatform =
  | AssistantDesktopPlatform
  | 'android'
  | 'ios'

export type CCSwitchInstallGuide = {
  artifact: string
  command: string | null
}

export function isMobileSetupPlatform(
  platform: AssistantSetupPlatform
): platform is 'android' | 'ios' {
  return platform === 'android' || platform === 'ios'
}

export function detectAssistantSetupPlatform(
  platformHint?: string,
  userAgent?: string,
  maxTouchPoints = 0
): AssistantSetupPlatform {
  const platform = platformHint?.trim().toLowerCase() ?? ''
  const agent = userAgent?.toLowerCase() ?? ''
  // Android reports Linux; iPads in desktop mode report macOS.
  if (/android/.test(`${platform} ${agent}`)) return 'android'
  if (/iphone|ipad|ipod|ios/.test(`${platform} ${agent}`)) return 'ios'
  if (/mac/.test(`${platform} ${agent}`) && maxTouchPoints > 1) return 'ios'
  if (platform.includes('win')) return 'windows'
  if (platform.includes('mac')) return 'macos'
  if (platform.includes('linux')) return 'linux'
  if (/windows|win32|win64/.test(agent)) return 'windows'
  if (/macintosh|mac os x/.test(agent)) return 'macos'
  if (/linux|x11/.test(agent)) return 'linux'
  return 'windows'
}

function quotePOSIX(value: string): string {
  return `'${value.replaceAll("'", `'"'"'`)}'`
}

function quotePowerShell(value: string): string {
  return `'${value.replaceAll("'", "''")}'`
}

export function getClaudeInstallCommand(
  platform: AssistantDesktopPlatform
): string {
  if (platform === 'windows') return 'winget install Anthropic.ClaudeCode'
  if (platform === 'macos') return 'brew install --cask claude-code'
  return 'curl -fsSL https://claude.ai/install.sh | bash'
}

export function getClaudeSessionCommand(
  platform: AssistantDesktopPlatform,
  rootUrl: string,
  model: string
): string {
  const normalizedRoot = rootUrl.replace(/\/+$/, '')
  const normalizedModel = model.trim() || '<MODEL_ID>'
  if (platform === 'windows') {
    return [
      `$env:ANTHROPIC_BASE_URL=${quotePowerShell(normalizedRoot)}`,
      `$env:ANTHROPIC_AUTH_TOKEN='<YOUR_API_KEY>'`,
      `$env:ANTHROPIC_MODEL=${quotePowerShell(normalizedModel)}`,
      'claude',
    ].join('\n')
  }

  return [
    `export ANTHROPIC_BASE_URL=${quotePOSIX(normalizedRoot)}`,
    `export ANTHROPIC_AUTH_TOKEN='<YOUR_API_KEY>'`,
    `export ANTHROPIC_MODEL=${quotePOSIX(normalizedModel)}`,
    'claude',
  ].join('\n')
}

export function getCCSwitchInstallGuide(
  platform: AssistantDesktopPlatform
): CCSwitchInstallGuide {
  if (platform === 'windows') {
    return {
      artifact: 'CC-Switch-v{version}-Windows.msi',
      command: null,
    }
  }
  if (platform === 'macos') {
    return {
      artifact: 'CC-Switch-v{version}-macOS.dmg',
      command: 'brew install --cask cc-switch',
    }
  }
  return {
    artifact: 'CC-Switch-v{version}-Linux-{architecture}.AppImage',
    command: [
      '# Arch Linux',
      'paru -S cc-switch-bin',
      '',
      '# Debian / Ubuntu (after downloading the .deb)',
      'sudo apt install ./CC-Switch-v*-Linux-*.deb',
      '',
      '# Universal AppImage (after downloading it)',
      'chmod +x CC-Switch-v*-Linux-*.AppImage',
      './CC-Switch-v*-Linux-*.AppImage',
    ].join('\n'),
  }
}

export function getCCSwitchClaudeProviderJSON(
  rootUrl: string,
  model: string
): string {
  return JSON.stringify(
    {
      env: {
        ANTHROPIC_AUTH_TOKEN: '<YOUR_API_KEY>',
        ANTHROPIC_BASE_URL: rootUrl.replace(/\/+$/, ''),
        ANTHROPIC_MODEL: model.trim() || '<MODEL_ID>',
      },
    },
    null,
    2
  )
}

export function getOpenAICompatibleClientJSON(
  baseUrl: string,
  model: string
): string {
  return JSON.stringify(
    {
      base_url: baseUrl.replace(/\/+$/, ''),
      model: model.trim() || '<MODEL_ID>',
      api_key: '<YOUR_API_KEY>',
    },
    null,
    2
  )
}

export function getCodexInstallCommand(
  platform: AssistantDesktopPlatform
): string {
  if (platform === 'windows') return 'npm install -g @openai/codex'
  return 'curl -fsSL https://chatgpt.com/codex/install.sh | sh'
}

export function getCodexAPIKeyCommand(
  platform: AssistantDesktopPlatform
): string {
  if (platform === 'windows') return "$env:LMM_API_KEY='<YOUR_API_KEY>'"
  return "export LMM_API_KEY='<YOUR_API_KEY>'"
}

export function getCodexConfigPath(platform: AssistantDesktopPlatform): string {
  if (platform === 'windows') return '%USERPROFILE%\\.codex\\config.toml'
  return '~/.codex/config.toml'
}

export function getCodexConfig(baseUrl: string, model: string): string {
  const normalizedBaseUrl = baseUrl.replace(/\/+$/, '')
  const normalizedModel = model.trim() || '<MODEL_ID>'
  return [
    `model = ${JSON.stringify(normalizedModel)}`,
    'model_provider = "lmm"',
    '',
    '[model_providers.lmm]',
    'name = "LMM"',
    `base_url = ${JSON.stringify(normalizedBaseUrl)}`,
    'env_key = "LMM_API_KEY"',
    'wire_api = "responses"',
  ].join('\n')
}
