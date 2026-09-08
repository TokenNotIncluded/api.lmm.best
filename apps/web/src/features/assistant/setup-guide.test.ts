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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

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
} from './setup-guide'

describe('assistant setup guide', () => {
  test('detects desktop, Android and iOS platforms including desktop-mode iPads', () => {
    assert.equal(detectAssistantSetupPlatform('Windows', ''), 'windows')
    assert.equal(detectAssistantSetupPlatform('macOS', ''), 'macos')
    assert.equal(detectAssistantSetupPlatform('Linux', ''), 'linux')
    assert.equal(
      detectAssistantSetupPlatform(
        '',
        'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)'
      ),
      'macos'
    )
    assert.equal(
      detectAssistantSetupPlatform('', 'Mozilla/5.0 (X11; Linux x86_64)'),
      'linux'
    )
    assert.equal(
      detectAssistantSetupPlatform('', 'Mozilla/5.0 (Linux; Android 15)'),
      'android'
    )
    assert.equal(
      detectAssistantSetupPlatform('Linux', 'Mozilla/5.0 (Linux; Android 15)'),
      'android'
    )
    assert.equal(detectAssistantSetupPlatform('iOS', ''), 'ios')
    assert.equal(
      detectAssistantSetupPlatform(
        '',
        'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0)'
      ),
      'ios'
    )
    assert.equal(
      detectAssistantSetupPlatform(
        'MacIntel',
        'Mozilla/5.0 (Macintosh; Intel Mac OS X)',
        5
      ),
      'ios'
    )
    assert.equal(
      detectAssistantSetupPlatform(
        'MacIntel',
        'Mozilla/5.0 (Macintosh; Intel Mac OS X)',
        0
      ),
      'macos'
    )
    assert.equal(detectAssistantSetupPlatform('', ''), 'windows')
  })

  test('uses current official install commands for each platform', () => {
    assert.equal(
      getClaudeInstallCommand('windows'),
      'winget install Anthropic.ClaudeCode'
    )
    assert.equal(
      getClaudeInstallCommand('macos'),
      'brew install --cask claude-code'
    )
    assert.equal(
      getClaudeInstallCommand('linux'),
      'curl -fsSL https://claude.ai/install.sh | bash'
    )
  })

  test('builds PowerShell and POSIX session-only gateway configuration', () => {
    assert.equal(
      getClaudeSessionCommand(
        'windows',
        'https://api.lmm.best/',
        'deepseek-v4-flash'
      ),
      [
        "$env:ANTHROPIC_BASE_URL='https://api.lmm.best'",
        "$env:ANTHROPIC_AUTH_TOKEN='<YOUR_API_KEY>'",
        "$env:ANTHROPIC_MODEL='deepseek-v4-flash'",
        'claude',
      ].join('\n')
    )
    assert.equal(
      getClaudeSessionCommand('linux', 'https://api.lmm.best', "model'o"),
      [
        "export ANTHROPIC_BASE_URL='https://api.lmm.best'",
        "export ANTHROPIC_AUTH_TOKEN='<YOUR_API_KEY>'",
        "export ANTHROPIC_MODEL='model'\"'\"'o'",
        'claude',
      ].join('\n')
    )
  })

  test('uses official CC Switch packages for each platform', () => {
    assert.deepEqual(getCCSwitchInstallGuide('windows'), {
      artifact: 'CC-Switch-v{version}-Windows.msi',
      command: null,
    })
    assert.deepEqual(getCCSwitchInstallGuide('macos'), {
      artifact: 'CC-Switch-v{version}-macOS.dmg',
      command: 'brew install --cask cc-switch',
    })
    const linux = getCCSwitchInstallGuide('linux')
    assert.match(linux.command ?? '', /paru -S cc-switch-bin/)
    assert.match(linux.command ?? '', /sudo apt install/)
    assert.match(linux.command ?? '', /AppImage/)
  })

  test('builds a copyable CC Switch Claude provider configuration', () => {
    assert.equal(
      getCCSwitchClaudeProviderJSON(
        'https://api.lmm.best/',
        'deepseek-v4-flash'
      ),
      JSON.stringify(
        {
          env: {
            ANTHROPIC_AUTH_TOKEN: '<YOUR_API_KEY>',
            ANTHROPIC_BASE_URL: 'https://api.lmm.best',
            ANTHROPIC_MODEL: 'deepseek-v4-flash',
          },
        },
        null,
        2
      )
    )
  })

  test('builds a copyable OpenAI-compatible client configuration', () => {
    assert.equal(
      getOpenAICompatibleClientJSON(
        'https://api.lmm.best/v1/',
        'deepseek-v4-flash'
      ),
      JSON.stringify(
        {
          base_url: 'https://api.lmm.best/v1',
          model: 'deepseek-v4-flash',
          api_key: '<YOUR_API_KEY>',
        },
        null,
        2
      )
    )
  })

  test('builds current Codex install and Responses provider configuration', () => {
    assert.equal(
      getCodexInstallCommand('linux'),
      'curl -fsSL https://chatgpt.com/codex/install.sh | sh'
    )
    assert.equal(
      getCodexInstallCommand('windows'),
      'npm install -g @openai/codex'
    )
    assert.equal(
      getCodexAPIKeyCommand('windows'),
      "$env:LMM_API_KEY='<YOUR_API_KEY>'"
    )
    assert.equal(
      getCodexConfigPath('windows'),
      '%USERPROFILE%\\.codex\\config.toml'
    )
    assert.equal(
      getCodexConfig('https://api.lmm.best/v1/', 'gpt-5.6-codex'),
      [
        'model = "gpt-5.6-codex"',
        'model_provider = "lmm"',
        '',
        '[model_providers.lmm]',
        'name = "LMM"',
        'base_url = "https://api.lmm.best/v1"',
        'env_key = "LMM_API_KEY"',
        'wire_api = "responses"',
      ].join('\n')
    )
  })
})
